package app

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

func projectCacheFolder(projectRoot string) (string, error) {
	configDir, err := userCacheDir()
	if err != nil {
		return "", err
	}

	replacer := strings.NewReplacer("/", "_", ":", "_", "\\", "_")
	expectedDir := filepath.Join(configDir, "shopware-lsp", replacer.Replace(projectRoot))
	if err := os.MkdirAll(expectedDir, 0o755); err != nil {
		return "", fmt.Errorf("create project cache directory: %w", err)
	}
	return expectedDir, nil
}

// ProjectCacheFolder returns the persistent cache directory for a workspace.
// CLI diagnostics and statistics use the same location as the LSP runtime.
func ProjectCacheFolder(projectRoot string) (string, error) {
	return projectCacheFolder(projectRoot)
}

func userCacheDir() (string, error) {
	if override := os.Getenv("SHOPWARE_LSP_CACHE_DIR"); override != "" {
		return override, nil
	}
	configDir, err := os.UserCacheDir()
	if err == nil {
		return configDir, nil
	}
	current, userErr := user.Current()
	if userErr != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", errors.Join(err, userErr))
	}
	return filepath.Join(current.HomeDir, ".config"), nil
}
