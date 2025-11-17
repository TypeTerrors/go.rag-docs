package discover

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Module represents a Go module known to the project.
type Module struct {
	Path     string  `json:"Path"`
	Version  string  `json:"Version"`
	Dir      string  `json:"Dir"`
	GoMod    string  `json:"GoMod"`
	Main     bool    `json:"Main"`
	Replace  *Module `json:"Replace"`
	Indirect bool    `json:"Indirect"`
}

// Package describes a Go package, either in the project or a dependency.
type Package struct {
	ImportPath string `json:"ImportPath"`
	Dir        string `json:"Dir"`
	Name       string `json:"Name"`
	Module     *Module
	Standard   bool `json:"Standard"`
	DepOnly    bool `json:"DepOnly"`
}

// ModuleUsage ties a module to the packages the project imports from it.
type ModuleUsage struct {
	Module   Module
	Packages []Package
}

// Project summarises the Go project located at Root.
type Project struct {
	Root             string
	MainModule       Module
	InternalPackages []Package
	ThirdParty       []ModuleUsage
	StdlibPackages   []Package
	AllModules       []Module
}

// Discover inspects the repository rooted at root and gathers details about
// its modules, packages, and dependencies.
func Discover(root string) (Project, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Project{}, err
	}

	modules, err := goListModules(absRoot)
	if err != nil {
		return Project{}, err
	}
	if len(modules) == 0 {
		return Project{}, errors.New("no go modules found; ensure go.mod exists")
	}

	moduleByPath := map[string]Module{}
	var mainModule Module
	for _, m := range modules {
		moduleByPath[m.Path] = m
		if m.Main {
			mainModule = m
		}
	}
	if mainModule.Path == "" {
		return Project{}, errors.New("main module not identified in go list output")
	}

	internalPkgs, err := goListPackages(absRoot, "./...")
	if err != nil {
		return Project{}, err
	}
	internalPkgs = filterPackagesByModule(internalPkgs, mainModule.Path)

	depPkgs, err := goListDeps(absRoot)
	if err != nil {
		return Project{}, err
	}

	stdlib := collectStdlib(depPkgs)
	thirdParty := collectThirdParty(depPkgs, moduleByPath, mainModule.Path)

	return Project{
		Root:             absRoot,
		MainModule:       mainModule,
		InternalPackages: internalPkgs,
		ThirdParty:       thirdParty,
		StdlibPackages:   stdlib,
		AllModules:       modules,
	}, nil
}

func collectStdlib(pkgs []Package) []Package {
	seen := make(map[string]Package)
	for _, p := range pkgs {
		if !p.Standard {
			continue
		}
		if p.ImportPath == "" {
			continue
		}
		seen[p.ImportPath] = p
	}

	out := make([]Package, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ImportPath < out[j].ImportPath
	})
	return out
}

func collectThirdParty(depPkgs []Package, moduleByPath map[string]Module, mainPath string) []ModuleUsage {
	type entry struct {
		module   Module
		packages map[string]Package
	}

	third := make(map[string]*entry)
	for _, p := range depPkgs {
		if p.Standard {
			continue
		}
		if p.Module == nil {
			continue
		}
		if p.Module.Path == mainPath {
			continue
		}

		mod := *p.Module
		if mod.Dir == "" {
			if known, ok := moduleByPath[mod.Path]; ok {
				mod = known
			}
		}

		ent, ok := third[mod.Path]
		if !ok {
			ent = &entry{
				module:   mod,
				packages: make(map[string]Package),
			}
			third[mod.Path] = ent
		}
		ent.packages[p.ImportPath] = p
	}

	paths := make([]string, 0, len(third))
	for path := range third {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	result := make([]ModuleUsage, 0, len(paths))
	for _, path := range paths {
		ent := third[path]
		pkgs := make([]Package, 0, len(ent.packages))
		for _, pkg := range ent.packages {
			pkgs = append(pkgs, pkg)
		}
		sort.Slice(pkgs, func(i, j int) bool {
			return pkgs[i].ImportPath < pkgs[j].ImportPath
		})
		result = append(result, ModuleUsage{
			Module:   ent.module,
			Packages: pkgs,
		})
	}

	return result
}

func goListModules(dir string) ([]Module, error) {
	output, err := runGoCommand(dir, "list", "-m", "-json", "all")
	if err != nil {
		return nil, err
	}

	var modules []Module
	dec := json.NewDecoder(bytes.NewReader(output))
	for {
		var m Module
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if rep := m.Replace; rep != nil && rep.Dir != "" {
			// Use the replacement directory when available.
			m.Dir = rep.Dir
		}
		modules = append(modules, m)
	}
	return modules, nil
}

func goListPackages(dir string, pattern string) ([]Package, error) {
	output, err := runGoCommand(dir, "list", "-json", pattern)
	if err != nil {
		return nil, err
	}

	var pkgs []Package
	dec := json.NewDecoder(bytes.NewReader(output))
	for {
		var p Package
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

func goListDeps(dir string) ([]Package, error) {
	output, err := runGoCommand(dir, "list", "-deps", "-json", "./...")
	if err != nil {
		return nil, err
	}

	var pkgs []Package
	dec := json.NewDecoder(bytes.NewReader(output))
	for {
		var p Package
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

func runGoCommand(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func filterPackagesByModule(pkgs []Package, modulePath string) []Package {
	out := pkgs[:0]
	for _, p := range pkgs {
		if p.Module != nil && p.Module.Path == modulePath {
			out = append(out, p)
			continue
		}
		// go list ./... for local packages may not populate Module, so fall back on path heuristic.
		if p.Module == nil && strings.HasPrefix(p.ImportPath, modulePath) {
			out = append(out, p)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].ImportPath < out[j].ImportPath
	})
	return out
}
