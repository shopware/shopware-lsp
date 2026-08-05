package symfony

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveServiceClassName(t *testing.T) {
	serviceIndex, err := NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		"/project/services.yaml",
		[]byte(`services:
  app.alias:
    alias: app.concrete
  app.concrete:
    class: App\Concrete
  app.cycle_a:
    alias: app.cycle_b
  app.cycle_b:
    alias: app.cycle_a
`),
	)))

	className, found, err := serviceIndex.ResolveServiceClassName("app.alias")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, `App\Concrete`, className)

	_, found, err = serviceIndex.ResolveServiceClassName("app.cycle_a")
	require.NoError(t, err)
	assert.False(t, found)
}
