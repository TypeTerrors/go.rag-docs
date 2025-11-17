package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/natedelduca/go-rag-pack/internal/chunk"
	"github.com/natedelduca/go-rag-pack/internal/config"
	"github.com/natedelduca/go-rag-pack/internal/discover"
	"github.com/natedelduca/go-rag-pack/internal/output"
	"github.com/natedelduca/go-rag-pack/internal/ui"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "init":
		err = runInit(args)
	case "select":
		err = runSelect(args)
	case "build":
		err = runBuild(args)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `go-rag-pack – generate RAG-friendly docs for Go projects

Usage:
  go-rag-pack init [--config path]
  go-rag-pack select [--config path]
  go-rag-pack build [--config path] [--output path] [--auto]
`)
}

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", config.DefaultFile, "config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg := config.Default(root)
	cfg.LastProjectRoot = root

	return config.Save(resolvePath(root, *configPath), cfg)
}

func runSelect(args []string) error {
	fs := flag.NewFlagSet("select", flag.ExitOnError)
	configPath := fs.String("config", config.DefaultFile, "config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := loadOrDefault(root, *configPath)
	if err != nil {
		return err
	}

	project, err := discover.Discover(root)
	if err != nil {
		return err
	}

	selection, err := ui.RunSelection(project, cfg)
	if err != nil {
		return err
	}

	cfg.IncludeProject = selection.IncludeProject
	cfg.IncludeStdlib = selection.IncludeStdlib
	if selection.IncludeModules {
		cfg.SelectedModules = selection.SelectedModules
		cfg.ManualModules = selection.ManualModules
	} else {
		cfg.SelectedModules = nil
		cfg.ManualModules = nil
	}
	cfg.LastProjectRoot = root

	return config.Save(resolvePath(root, *configPath), cfg)
}

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	configPath := fs.String("config", config.DefaultFile, "config file path")
	outputPath := fs.String("output", "", "output file path (overrides config)")
	auto := fs.Bool("auto", false, "select everything automatically")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := os.Getwd()
	if err != nil {
		return err
	}

	project, err := discover.Discover(root)
	if err != nil {
		return err
	}

	cfg, err := loadOrDefault(root, *configPath)
	if err != nil {
		return err
	}

	if *auto {
		cfg.IncludeProject = true
		cfg.IncludeStdlib = len(project.StdlibPackages) > 0
		cfg.SelectedModules = nil
		for _, mod := range project.ThirdParty {
			cfg.SelectedModules = append(cfg.SelectedModules, mod.Module.Path)
		}
		cfg.ManualModules = nil
	}

	selectedModules := make(map[string]struct{})
	for _, mod := range cfg.SelectedModules {
		selectedModules[mod] = struct{}{}
	}
	for _, mod := range cfg.ManualModules {
		selectedModules[mod] = struct{}{}
	}

	var sources []chunk.PackageSource
	if cfg.IncludeProject {
		for _, pkg := range project.InternalPackages {
			sources = append(sources, chunk.PackageSource{
				ModulePath:    project.MainModule.Path,
				ModuleVersion: project.MainModule.Version,
				ModuleDir:     project.Root,
				ImportPath:    pkg.ImportPath,
				Dir:           pkg.Dir,
				Kind:          chunk.SourceProject,
			})
		}
	}

	if cfg.IncludeStdlib {
		goRoot := runtime.GOROOT()
		stdRoot := filepath.Join(goRoot, "src")
		for _, pkg := range project.StdlibPackages {
			if pkg.Dir == "" {
				continue
			}
			sources = append(sources, chunk.PackageSource{
				ModulePath:    "std",
				ModuleVersion: "",
				ModuleDir:     stdRoot,
				ImportPath:    pkg.ImportPath,
				Dir:           pkg.Dir,
				Kind:          chunk.SourceStdlib,
			})
		}
	}

	if len(selectedModules) > 0 {
		modUsage := make(map[string]discover.ModuleUsage)
		for _, mu := range project.ThirdParty {
			modUsage[mu.Module.Path] = mu
		}
		allModules := make(map[string]discover.Module)
		for _, mod := range project.AllModules {
			allModules[mod.Path] = mod
		}

		for path := range selectedModules {
			if mu, ok := modUsage[path]; ok {
				for _, pkg := range mu.Packages {
					dir := pkg.Dir
					if dir == "" && pkg.Module != nil {
						dir = pkg.Module.Dir
					}
					if dir == "" {
						continue
					}
					moduleDir := mu.Module.Dir
					if moduleDir == "" && pkg.Module != nil {
						moduleDir = pkg.Module.Dir
					}
					if moduleDir == "" {
						moduleDir = dir
					}
					sources = append(sources, chunk.PackageSource{
						ModulePath:    mu.Module.Path,
						ModuleVersion: mu.Module.Version,
						ModuleDir:     moduleDir,
						ImportPath:    pkg.ImportPath,
						Dir:           dir,
						Kind:          chunk.SourceThirdParty,
					})
				}
				continue
			}

			// Manual module handling: discover packages by scanning the module directory.
			module, ok := allModules[path]
			if !ok {
				fmt.Fprintf(os.Stderr, "warning: module %s not found; skipping\n", path)
				continue
			}
			if module.Dir == "" {
				fmt.Fprintf(os.Stderr, "warning: module %s has no source directory; skipping\n", path)
				continue
			}
			pkgs, err := scanModulePackages(module)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: module %s: %v\n", path, err)
				continue
			}
			for _, pkg := range pkgs {
				sources = append(sources, chunk.PackageSource{
					ModulePath:    module.Path,
					ModuleVersion: module.Version,
					ModuleDir:     module.Dir,
					ImportPath:    pkg.ImportPath,
					Dir:           pkg.Dir,
					Kind:          chunk.SourceThirdParty,
				})
			}
		}
	}

	if len(sources) == 0 {
		return errors.New("no sources selected; run go-rag-pack select or use --auto")
	}

	chunks, err := chunk.Build(dedupeSources(sources))
	if err != nil {
		return err
	}

	outPath := cfg.OutputPath
	if *outputPath != "" {
		outPath = *outputPath
	}
	if outPath == "" {
		outPath = filepath.Join("rag", "go_docs.jsonl")
	}
	absOut := resolvePath(root, outPath)
	if err := output.WriteJSONL(absOut, chunks); err != nil {
		return err
	}

	fmt.Printf("wrote %d chunks to %s\n", len(chunks), absOut)
	return nil
}

func resolvePath(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

func loadOrDefault(root, configPath string) (config.Config, error) {
	path := resolvePath(root, configPath)
	cfg, err := config.Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg = config.Default(root)
			cfg.LastProjectRoot = root
			return cfg, nil
		}
		return config.Config{}, err
	}
	if cfg.OutputPath == "" {
		cfg.OutputPath = filepath.Join("rag", "go_docs.jsonl")
	}
	return cfg, nil
}

func dedupeSources(sources []chunk.PackageSource) []chunk.PackageSource {
	if len(sources) <= 1 {
		return sources
	}
	type key struct {
		importPath string
		dir        string
	}
	seen := make(map[key]chunk.PackageSource)
	for _, src := range sources {
		k := key{importPath: src.ImportPath, dir: src.Dir}
		// If duplicates exist, prefer project sources, then third-party, then stdlib.
		if existing, ok := seen[k]; ok {
			order := func(k chunk.SourceKind) int {
				switch k {
				case chunk.SourceProject:
					return 0
				case chunk.SourceThirdParty:
					return 1
				case chunk.SourceStdlib:
					return 2
				default:
					return 3
				}
			}
			if order(src.Kind) < order(existing.Kind) {
				seen[k] = src
			}
			continue
		}
		seen[k] = src
	}

	keys := make([]key, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b key) int {
		if cmp := strings.Compare(a.importPath, b.importPath); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.dir, b.dir)
	})

	deduped := make([]chunk.PackageSource, 0, len(keys))
	for _, k := range keys {
		deduped = append(deduped, seen[k])
	}
	return deduped
}

func scanModulePackages(module discover.Module) ([]discover.Package, error) {
	var packages []discover.Package
	err := filepath.WalkDir(module.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}

		name := d.Name()
		switch name {
		case "vendor", "testdata":
			return filepath.SkipDir
		}
		if strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}

		hasGo := false
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fileName := entry.Name()
			if !strings.HasSuffix(fileName, ".go") {
				continue
			}
			if shouldSkipManualFile(fileName) {
				continue
			}
			hasGo = true
			break
		}
		if !hasGo {
			return nil
		}

		rel, err := filepath.Rel(module.Dir, path)
		if err != nil {
			return err
		}
		importPath := module.Path
		if rel != "." {
			importPath = module.Path + "/" + filepath.ToSlash(rel)
		}
		packages = append(packages, discover.Package{
			ImportPath: importPath,
			Dir:        path,
			Name:       filepath.Base(path),
			Module: &discover.Module{
				Path:    module.Path,
				Version: module.Version,
				Dir:     module.Dir,
			},
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return packages, nil
}

func shouldSkipManualFile(name string) bool {
	switch {
	case strings.HasSuffix(name, "_test.go"),
		strings.HasSuffix(name, "_mock.go"),
		strings.HasSuffix(name, "_generated.go"),
		strings.Contains(name, ".pb.go"),
		strings.Contains(name, "_pb2.go"):
		return true
	default:
		return false
	}
}
