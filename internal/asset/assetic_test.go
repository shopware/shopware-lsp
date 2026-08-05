package asset

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAsseticCatalogLoadsAndReloadsCompiledNamedAssets(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(
		root,
		"app",
		"Resources",
		"bower",
		"jquery1.js",
	)
	second := filepath.Join(
		root,
		"app",
		"Resources",
		"bower",
		"jquery2.js",
	)
	for _, target := range []string{first, second} {
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
		require.NoError(t, os.WriteFile(target, []byte("jquery"), 0o644))
	}
	index, err := NewIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	names, err := index.AsseticNamedNames()
	require.NoError(t, err)
	assert.Empty(t, names)

	container := filepath.Join(
		root,
		"app",
		"cache",
		"dev",
		"appDevDebugProjectContainer.xml",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(container), 0o755))
	require.NoError(t, os.WriteFile(
		container,
		[]byte(asseticContainerFixture("jquery_js")),
		0o644,
	))
	require.NoError(t, index.ReloadAsseticCatalog())

	names, err = index.AsseticNamedNames()
	require.NoError(t, err)
	assert.Equal(t, []string{"jquery_js"}, names)
	resources, err := index.FindAsseticNamed("jquery_js")
	require.NoError(t, err)
	require.Len(t, resources, 2)
	assert.ElementsMatch(t, []string{first, second}, []string{
		resources[0].Target,
		resources[1].Target,
	})
	assert.Equal(t, container, resources[0].File)
	assert.NotZero(t, resources[0].Range.Len())

	require.NoError(t, os.WriteFile(
		container,
		[]byte(asseticContainerFixture("jquery_runtime")),
		0o644,
	))
	require.NoError(t, index.ReloadAsseticCatalog())
	names, err = index.AsseticNamedNames()
	require.NoError(t, err)
	assert.Equal(t, []string{"jquery_runtime"}, names)
}

func asseticContainerFixture(name string) string {
	return fmt.Sprintf(`<container>
  <services>
    <service id="assetic.asset_manager">
      <call method="addResource">
        <argument type="service">
          <service class="Symfony\Bundle\AsseticBundle\Factory\Resource\ConfigurationResource">
            <argument type="collection">
              <argument key=%q type="collection">
                <argument type="collection">
                  <argument>../app/Resources/bower/jquery1.js</argument>
                  <argument>../app/Resources/bower/jquery2.js</argument>
                </argument>
                <argument type="collection"/>
                <argument type="collection"/>
              </argument>
            </argument>
          </service>
        </argument>
      </call>
    </service>
  </services>
</container>`, name)
}
