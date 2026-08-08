//go:build integration

package symfony

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestServicesXMLConversionOnTrunk dry-runs every convention-loaded plugin
// services.xml in a real Shopware checkout. It never writes to the checkout.
func TestServicesXMLConversionOnTrunk(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Skipf("real-world Shopware checkout is unavailable: %s", root)
	}

	checked := 0
	generated := 0
	err := filepath.WalkDir(root, func(
		path string, entry fs.DirEntry, walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		normalized := strings.ToLower(filepath.ToSlash(path))
		if !strings.HasSuffix(normalized, "/resources/config/services.xml") {
			return nil
		}

		plan, planErr := PlanServicesXMLConversion(
			context.Background(), path,
			func(_ context.Context, path string) ([]byte, error) {
				return os.ReadFile(path)
			},
			func(path string) (bool, error) {
				_, statErr := os.Stat(path)
				switch {
				case statErr == nil:
					return true, nil
				case errors.Is(statErr, os.ErrNotExist):
					return false, nil
				default:
					return false, statErr
				}
			},
		)
		require.NoErrorf(t, planErr, "convert %s", path)
		require.NotEmpty(t, plan, path)
		for _, conversion := range plan {
			var parsed map[string]any
			require.NoErrorf(
				t, yaml.Unmarshal(conversion.Content, &parsed),
				"generated YAML for %s", conversion.SourcePath,
			)
		}
		checked++
		generated += len(plan)
		t.Logf("%s: generated=%d", path, len(plan))
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, checked, 0, "trunk should contain plugin services.xml fixtures")
	t.Logf("services.xml files=%d generated YAML files=%d", checked, generated)
}
