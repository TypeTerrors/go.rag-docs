package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const (
	// DefaultFile is the default filename written to the project root.
	DefaultFile = ".go-rag-pack.json"
)

// Config captures persisted user preferences across select/build runs.
type Config struct {
	IncludeProject  bool     `json:"includeProject"`
	IncludeStdlib   bool     `json:"includeStdlib"`
	SelectedModules []string `json:"selectedModules"`
	ManualModules   []string `json:"manualModules"`
	OutputPath      string   `json:"outputPath"`
	LastProjectRoot string   `json:"lastProjectRoot"`
}

// Load reads configuration from the provided path. If the file does not exist,
// an empty config and os.ErrNotExist are returned to allow callers to initialise defaults.
func Load(path string) (Config, error) {
	var cfg Config

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, err
		}
		return cfg, err
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// Save writes the configuration to disk, creating parent directories as needed.
func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}

// Default creates a new configuration with sensible defaults for a project rooted at root.
func Default(root string) Config {
	return Config{
		IncludeProject:  true,
		IncludeStdlib:   false,
		SelectedModules: nil,
		ManualModules:   nil,
		OutputPath:      filepath.Join("rag", "go_docs.jsonl"),
		LastProjectRoot: root,
	}
}
