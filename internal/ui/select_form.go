package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/natedelduca/go-rag-pack/internal/config"
	"github.com/natedelduca/go-rag-pack/internal/discover"
)

// Selection captures the user's choices from the interactive form.
type Selection struct {
	IncludeProject  bool
	IncludeStdlib   bool
	IncludeModules  bool
	SelectedModules []string
	ManualModules   []string
}

// RunSelection displays the Charmbracelet/huh form and returns the user's selection.
func RunSelection(proj discover.Project, current config.Config) (Selection, error) {
	docKinds := make([]string, 0, 3)
	if current.IncludeProject {
		docKinds = append(docKinds, "project")
	}
	if current.IncludeStdlib {
		docKinds = append(docKinds, "stdlib")
	}
	if len(current.SelectedModules) > 0 || len(proj.ThirdParty) > 0 {
		docKinds = append(docKinds, "third-party")
	}

	docForm := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Which kinds of docs do you want?").
				Options(
					huh.NewOption("Project code", "project"),
					huh.NewOption("Stdlib docs for used packages", "stdlib"),
					huh.NewOption("Third-party modules detected", "third-party"),
				).
				Value(&docKinds),
		),
	)

	if err := docForm.Run(); err != nil {
		return Selection{}, err
	}

	selection := Selection{}
	for _, kind := range docKinds {
		switch kind {
		case "project":
			selection.IncludeProject = true
		case "stdlib":
			selection.IncludeStdlib = true
		case "third-party":
			selection.IncludeModules = true
		}
	}

	if selection.IncludeModules && len(proj.ThirdParty) > 0 {
		moduleOptions := make([]huh.Option[string], 0, len(proj.ThirdParty))
		moduleDefaults := make(map[string]struct{})
		for _, m := range current.SelectedModules {
			moduleDefaults[m] = struct{}{}
		}
		if len(moduleDefaults) == 0 {
			for _, mu := range proj.ThirdParty {
				moduleDefaults[mu.Module.Path] = struct{}{}
			}
		}

		value := make([]string, 0, len(moduleDefaults))

		for _, mu := range proj.ThirdParty {
			label := mu.Module.Path
			if mu.Module.Version != "" {
				label = fmt.Sprintf("%s@%s", mu.Module.Path, mu.Module.Version)
			}
			moduleOptions = append(moduleOptions, huh.NewOption(label, mu.Module.Path))
			if _, ok := moduleDefaults[mu.Module.Path]; ok {
				value = append(value, mu.Module.Path)
			}
		}

		modForm := huh.NewForm(
			huh.NewGroup(
				huh.NewMultiSelect[string]().
					Title("Select third-party modules").
					Options(moduleOptions...).
					Value(&value),
			),
		)
		if err := modForm.Run(); err != nil {
			return Selection{}, err
		}
		selection.SelectedModules = value

		var manual string
		if len(current.ManualModules) > 0 {
			manual = strings.Join(current.ManualModules, ", ")
		}
		manualForm := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Extra modules (comma separated, optional)").
					Value(&manual),
			),
		)
		if err := manualForm.Run(); err != nil {
			return Selection{}, err
		}
		if trimmed := strings.TrimSpace(manual); trimmed != "" {
			parts := strings.Split(trimmed, ",")
			for _, part := range parts {
				if name := strings.TrimSpace(part); name != "" {
					selection.ManualModules = append(selection.ManualModules, name)
				}
			}
		}
	}

	return selection, nil
}
