package codelens

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPServiceCodeLensLinksAutowiredConstructorToPrototype(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpPath := filepath.Join(root, "src", "WiredService.php")
	servicePath := filepath.Join(root, "config", "services.yaml")
	phpSource := `<?php
namespace App;

final class WiredService
{
    public function __construct() {}
}
`
	serviceSource := `services:
  _defaults:
    autowire: true
  App\:
    resource: ../src/
`

	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		servicePath,
		[]byte(serviceSource),
	)))

	lenses := relatedCodeLensesFor(
		t,
		NewPHPCodeLensProvider(phpIndex, serviceIndex),
		phpPath,
		phpSource,
	)
	require.Len(t, lenses, 2)
	assert.Equal(t, []string{
		"Open Service Definition",
		"Open autowired service definition",
	}, relatedLensTitles(lenses))
	assert.Equal(t, 3, lenses[0].Range.Start.Line)
	assert.Equal(t, 5, lenses[1].Range.Start.Line)
	for _, lens := range lenses {
		assert.Equal(t, []string{
			relatedTarget(servicePath, 4),
		}, relatedLensTargets(t, lens))
	}
}

func TestPHPServiceCodeLensOmitsConstructorForDisabledAutowire(
	t *testing.T,
) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpPath := filepath.Join(root, "src", "ManualService.php")
	servicePath := filepath.Join(root, "config", "services.yaml")
	phpSource := `<?php
namespace App;
final class ManualService
{
    public function __construct() {}
}
`
	serviceSource := `services:
  App\ManualService:
    autowire: false
`

	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		servicePath,
		[]byte(serviceSource),
	)))

	lenses := relatedCodeLensesFor(
		t,
		NewPHPCodeLensProvider(phpIndex, serviceIndex),
		phpPath,
		phpSource,
	)
	require.Len(t, lenses, 1)
	assert.Equal(t, "Open Service Definition", lenses[0].Command.Title)
}
