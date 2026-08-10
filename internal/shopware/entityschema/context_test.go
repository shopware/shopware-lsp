package entityschema

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindPluginContextPrefersYAMLServiceConfiguration(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, "src", "Resources", "config")
	require.NoError(t, os.MkdirAll(configDirectory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{
  "name": "acme/example",
  "type": "shopware-platform-plugin",
  "autoload": {"psr-4": {"Acme\\Example\\": "src/"}},
  "extra": {"shopware-plugin-class": "Acme\\Example\\Example"}
}`), 0o644))
	for _, name := range []string{"services.xml", "services.php", "services.yaml"} {
		require.NoError(t, os.WriteFile(filepath.Join(configDirectory, name), []byte("services:\n"), 0o644))
	}

	context, err := FindPluginContext(root, filepath.Join(root, "src"))
	require.NoError(t, err)
	require.Len(t, context.ServiceURIs, 3)
	require.Equal(t, filepath.Join(configDirectory, "services.yaml"), context.ServiceURIs[0])
	require.Equal(t, filepath.Join(configDirectory, "services.xml"), context.ServiceURIs[2])
}
