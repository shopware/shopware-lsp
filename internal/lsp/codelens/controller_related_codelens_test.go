package codelens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControllerRelatedCodeLensesLinkPHPAndTwigBothWays(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	classPath := filepath.Join(root, "src", "NavController.php")
	twigPath := filepath.Join(root, "templates", "navigation.html.twig")
	require.NoError(t, os.MkdirAll(filepath.Dir(classPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(twigPath), 0o755))
	classSource := `<?php
namespace App\Controller;
class NavController {
    public function menu(): void {}
}`
	twigSource := `{{ render(controller('App\\Controller\\NavController::menu')) }}
{{ controller('app.navigation:menu') }}`
	require.NoError(t, os.WriteFile(classPath, []byte(classSource), 0o644))
	require.NoError(t, os.WriteFile(twigPath, []byte(twigSource), 0o644))
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		[]byte(classSource),
	)))
	services, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, services.Close()) })
	require.NoError(t, services.Index(indexer.NewParsedFile(
		filepath.Join(root, "services.yaml"),
		[]byte(`services:
  app.navigation:
    class: App\Controller\NavController
`),
	)))
	usages, err := symfony.NewRouteUsageIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, usages.Close()) })
	require.NoError(t, usages.Index(indexer.NewParsedFile(
		twigPath,
		[]byte(twigSource),
	)))
	provider := NewControllerRelatedCodeLensProvider(
		usages,
		services,
		phpIndex,
	)
	phpLenses := relatedCodeLensesFor(
		t,
		provider,
		classPath,
		classSource,
	)
	require.Len(t, phpLenses, 1)
	assert.Equal(t, "Open 2 Twig controller usages", phpLenses[0].Command.Title)
	assert.Equal(t, []string{
		relatedTarget(twigPath, 1),
		relatedTarget(twigPath, 2),
	}, relatedLensTargets(t, phpLenses[0]))

	twigLenses := relatedCodeLensesFor(
		t,
		provider,
		twigPath,
		twigSource,
	)
	require.Len(t, twigLenses, 2)
	assert.Equal(t, []string{
		"Open controller method",
		"Open controller method",
	}, relatedLensTitles(twigLenses))
	for _, lens := range twigLenses {
		assert.Equal(t, []string{
			relatedTarget(classPath, 4),
		}, relatedLensTargets(t, lens))
	}
}
