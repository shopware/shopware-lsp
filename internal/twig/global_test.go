package twig

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

func TestGlobalsInYAML(t *testing.T) {
	source := `twig:
  globals:
    clock: '@app.clock'
    site_name: 'Storefront'
    retries: 3
when@test:
  twig:
    globals:
      test_mode: true
`
	tree := yamlparser.Parse(source).Tree
	globals := GlobalsInYAML("/project/config/packages/twig.yaml", tree.Root)
	require.Len(t, globals, 4)
	byName := globalMap(globals)
	require.Equal(t, "app.clock", byName["clock"].ServiceID)
	require.Empty(t, byName["clock"].Type)
	require.Equal(t, "string", byName["site_name"].Type)
	require.Equal(t, "int", byName["retries"].Type)
	require.Equal(t, "bool", byName["test_mode"].Type)
	require.Equal(
		t,
		"clock",
		source[byName["clock"].Range.Start:byName["clock"].Range.End],
	)
}

func TestGlobalsInPHPExtension(t *testing.T) {
	source := `<?php
namespace App\Twig;
use App\Clock;
use Twig\Extension\AbstractExtension;
final class StorefrontExtension extends AbstractExtension {
    public function getGlobals(): array {
        return [
            'clock' => new Clock(),
            'answer' => 42,
        ];
    }
}`
	tree := phpparser.Parse(source).Tree
	globals := GlobalsInPHPExtension(
		"/project/src/Twig/StorefrontExtension.php",
		tree.Root,
		nil,
	)
	require.Len(t, globals, 2)
	byName := globalMap(globals)
	require.Equal(t, "App\\Clock", byName["clock"].Type)
	require.Equal(t, "int", byName["answer"].Type)
	require.Equal(
		t,
		"clock",
		source[byName["clock"].Range.Start:byName["clock"].Range.End],
	)
}

func TestTwigIndexerResolvesAndRestoresTypedGlobals(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "config/services.yaml"),
		[]byte(`services:
  app.clock:
    class: App\Clock
`),
	)))

	first, err := NewTwigIndexer(cache)
	require.NoError(t, err)
	first.SetDependencies(phpIndex, serviceIndex)
	yamlPath := filepath.Join(root, "config/packages/twig.yaml")
	require.NoError(t, first.Index(indexer.NewParsedFile(
		yamlPath,
		[]byte(`twig:
  globals:
    clock: '@app.clock'
    title: 'Store'
`),
	)))
	phpPath := filepath.Join(root, "src/Twig/StorefrontExtension.php")
	phpSource := []byte(`<?php
namespace App\Twig;
use App\Feature;
use Twig\Extension\AbstractExtension;
final class StorefrontExtension extends AbstractExtension {
    public function getGlobals(): array {
        return ['feature' => new Feature()];
    }
}`)
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		phpSource,
	)))
	require.NoError(t, first.Index(indexer.NewParsedFile(
		phpPath,
		phpSource,
	)))

	globals, err := first.GetAllGlobals()
	require.NoError(t, err)
	byName := globalMap(globals)
	require.Equal(t, "App\\Clock", byName["clock"].Type)
	require.Equal(t, "string", byName["title"].Type)
	require.Equal(t, "App\\Feature", byName["feature"].Type)
	require.NoError(t, first.Close())

	restored, err := NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restored.SetDependencies(phpIndex, serviceIndex)
	restoredGlobals, err := restored.GetAllGlobals()
	require.NoError(t, err)
	require.Equal(t, globals, restoredGlobals)

	require.NoError(t, restored.Index(indexer.NewParsedFile(
		yamlPath,
		[]byte("framework:\n  secret: value\n"),
	)))
	remaining, err := restored.GetAllGlobals()
	require.NoError(t, err)
	for _, global := range remaining {
		require.False(
			t,
			strings.EqualFold(global.Name, "clock") ||
				strings.EqualFold(global.Name, "title"),
		)
	}
}

func globalMap(globals []Global) map[string]Global {
	result := make(map[string]Global, len(globals))
	for _, global := range globals {
		result[global.Name] = global
	}
	return result
}
