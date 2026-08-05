package symfony

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceIndexIncludesCompiledContainerParameters(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "var", "cache", "dev-test")
	require.NoError(t, os.MkdirAll(cache, 0o755))
	containerPath := filepath.Join(
		cache,
		"Shopware_Core_KernelDevDebugContainer.xml",
	)
	require.NoError(t, os.WriteFile(containerPath, []byte(`<container>
<parameters>
  <parameter key="kernel.project_dir">/project</parameter>
  <parameter key="App.MixedCase">value</parameter>
</parameters>
<services>
  <service id="twig">
    <call method="addGlobal">
      <argument>app</argument>
      <argument type="service" id="App\Twig\AppVariable"/>
    </call>
  </service>
</services>
</container>`), 0o644))

	idx, err := NewServiceIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	parameters, err := idx.GetAllParameters()
	require.NoError(t, err)
	var names []string
	for _, parameter := range parameters {
		names = append(names, parameter.Name)
	}
	assert.Contains(t, names, "kernel.project_dir")
	assert.Contains(t, names, "App.MixedCase")

	parameter, found, err := idx.GetParameterByName("App.MixedCase")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "value", parameter.Value)
	globals := idx.GetTwigGlobals()
	require.Len(t, globals, 1)
	assert.Equal(t, "app", globals[0].Name)
	assert.Equal(t, "App\\Twig\\AppVariable", globals[0].ServiceID)
	assert.Equal(t, containerPath, globals[0].Path)
}
