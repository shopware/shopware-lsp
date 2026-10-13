package extension

import (
	"path/filepath"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
)

// ConfigLocations follows shopware-cli's first-existing-file precedence.
var ConfigLocations = []string{".config/shopware-extension.yml", ".shopware-extension.yml", ".shopware-extension.yaml"}

func DefaultConfig() Config {
	config := Config{CompatibilityDate: "2026-02-11"}
	config.Build.Zip.Assets.Enabled = true
	config.Build.Zip.Composer.Enabled = true
	return config
}

// ParseConfigTree extracts all CLI extension configuration from a shared CST.
// Decoding errors are retained as data so incomplete editor input can still
// publish a useful snapshot and does not fall back to a lower-precedence file.
func ParseConfigTree(path string, root *cst.Node) Config {
	config := DefaultConfig()
	config.StorageLocation = filepath.Clean(path)
	if err := yamlquery.Decode(root, &config); err != nil {
		config.DecodeError = err.Error()
	}
	if config.CompatibilityDate == "" {
		config.CompatibilityDate = DefaultConfig().CompatibilityDate
	}
	return config
}

// ConfigurationRoot identifies supported locations without walking the disk.
func ConfigurationRoot(path string) (string, bool) {
	path = filepath.Clean(path)
	switch filepath.Base(path) {
	case ".shopware-extension.yml", ".shopware-extension.yaml":
		return filepath.Dir(path), true
	case "shopware-extension.yml":
		dir := filepath.Dir(path)
		if filepath.Base(dir) == ".config" {
			return filepath.Dir(dir), true
		}
	}
	return "", false
}
