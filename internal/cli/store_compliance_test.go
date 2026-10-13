package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckStoreComplianceOptIn(t *testing.T) {
	for _, test := range []struct {
		name, setting, configuration string
		want                         bool
	}{
		{"explicit true even for a local path package", "validation:\n  store_compliance: true\n", `{"version":1,"domains":{"php":false}}`, true},
		{"missing extension configuration", "", `{"version":1}`, false},
		{"explicit false", "validation:\n  store_compliance: false\n", `{"version":1}`, false},
		{"quoted true", "validation:\n  store_compliance: 'true'\n", `{"version":1}`, false},
		{"Store domain disabled", "validation:\n  store_compliance: true\n", `{"version":1,"domains":{"shopware.store":false}}`, false},
		{"individual Store rule disabled", "validation:\n  store_compliance: true\n", `{"version":1,"diagnostics":{"rules":{"shopware.store.require-core":"off"}}}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
			root := t.TempDir()
			dir := filepath.Join(root, "custom/plugins/Local")
			plugin := filepath.Join(dir, "composer.json")
			configuration := filepath.Join(root, ".config/shopware/lsp.yaml")
			require.NoError(t, os.MkdirAll(dir, 0755))
			require.NoError(t, os.MkdirAll(filepath.Dir(configuration), 0755))
			require.NoError(t, os.WriteFile(configuration, []byte(test.configuration), 0644))
			require.NoError(t, os.WriteFile(plugin, []byte(`{"name":"acme/local","type":"shopware-platform-plugin"}`), 0644))
			require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{"require":{"acme/local":"*"},"repositories":[{"type":"path","url":"custom/plugins/*"}]}`), 0644))
			if test.setting != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, ".shopware-extension.yml"), []byte(test.setting), 0644))
			}
			// Both cold indexing and cached restarts must find the hidden YAML setting.
			for range 2 {
				var output, errors bytes.Buffer
				require.NoError(t, New(Options{Version: "test"}).Run(context.Background(), []string{"-root", root, "-allow-unsupported-project", "-json", "check", "-fail-on", "off", plugin}, strings.NewReader(""), &output, &errors), errors.String())
				require.Equal(t, test.want, strings.Contains(output.String(), "shopware.store."), output.String())
				if test.name == "individual Store rule disabled" {
					require.NotContains(t, output.String(), "shopware.store.require-core")
				}
			}
		})
	}
}

func TestCheckExtensionConfigLocations(t *testing.T) {
	for _, location := range []string{".config/shopware-extension.yml", ".shopware-extension.yml", ".shopware-extension.yaml"} {
		t.Run(location, func(t *testing.T) {
			t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
			root := t.TempDir()
			config := filepath.Join(root, location)
			require.NoError(t, os.MkdirAll(filepath.Dir(config), 0755))
			require.NoError(t, os.WriteFile(config, []byte("validation: {store_compliance: true}"), 0644))
			composer := filepath.Join(root, "composer.json")
			require.NoError(t, os.WriteFile(composer, []byte(`{"type":"shopware-platform-plugin"}`), 0644))
			for range 2 {
				var output, errors bytes.Buffer
				require.NoError(t, New(Options{Version: "test"}).Run(context.Background(), []string{"-root", root, "-allow-unsupported-project", "-json", "check", "-fail-on", "off", composer}, strings.NewReader(""), &output, &errors), errors.String())
				require.Contains(t, output.String(), "shopware.store.")
			}
			if location == ".config/shopware-extension.yml" {
				require.NoError(t, os.WriteFile(config, []byte("validation: {store_compliance: false}"), 0644))
				require.NoError(t, os.WriteFile(filepath.Join(root, ".shopware-extension.yml"), []byte("validation: {store_compliance: true}"), 0644))
				var output, errors bytes.Buffer
				require.NoError(t, New(Options{Version: "test"}).Run(context.Background(), []string{"-root", root, "-allow-unsupported-project", "-json", "check", "-fail-on", "off", composer}, strings.NewReader(""), &output, &errors), errors.String())
				require.NotContains(t, output.String(), "shopware.store.")
			}
		})
	}
}
