package symfonyconfig

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

func TestRootsInPHPCollectsModernAndLegacyTreeBuilders(t *testing.T) {
	source := `<?php
namespace App\DependencyInjection;

use Symfony\Component\Config\Definition\Builder\TreeBuilder;
use Symfony\Component\Config\Definition\ConfigurationInterface;

final class ModernConfiguration implements ConfigurationInterface
{
    public function getConfigTreeBuilder(): TreeBuilder
    {
        $treeBuilder = new TreeBuilder('app_modern');
        return $treeBuilder;
    }
}

final class LegacyConfiguration implements ConfigurationInterface
{
    public function getConfigTreeBuilder(): TreeBuilder
    {
        return (new TreeBuilder())->root('app_legacy');
    }

    private function helper(): TreeBuilder
    {
        return new TreeBuilder('not_a_root');
    }
}
`
	file := indexer.NewParsedFile(
		"/project/src/DependencyInjection/Configuration.php",
		[]byte(source),
	)
	roots := rootsInPHP(file.Path, file.SyntaxTree().Root)
	require.Len(t, roots, 2)
	require.Equal(t, "app_modern", roots[0].Name)
	require.Equal(
		t,
		"App\\DependencyInjection\\ModernConfiguration",
		roots[0].Class,
	)
	require.Equal(t, "app_legacy", roots[1].Name)
}

func TestIndexRestoresAndRemovesConfigurationRoots(t *testing.T) {
	cacheDir := t.TempDir()
	index, err := NewIndex(cacheDir)
	require.NoError(t, err)
	path := "/project/src/DependencyInjection/Configuration.php"
	source := `<?php
use Symfony\Component\Config\Definition\Builder\TreeBuilder;
final class Configuration {
    public function getConfigTreeBuilder(): TreeBuilder {
        return new TreeBuilder('app_root');
    }
}
`
	require.NoError(t, index.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	names, err := index.Names()
	require.NoError(t, err)
	require.Equal(t, []string{"app_root"}, names)
	require.NoError(t, index.Close())

	restored, err := NewIndex(cacheDir)
	require.NoError(t, err)
	roots, err := restored.Roots("APP_ROOT")
	require.NoError(t, err)
	require.Len(t, roots, 1)
	require.Equal(t, path, roots[0].File)
	require.NoError(t, restored.Index(indexer.NewParsedFile(
		path,
		[]byte("<?php final class Configuration {}"),
	)))
	roots, err = restored.Roots("app_root")
	require.NoError(t, err)
	require.Empty(t, roots)
	require.NoError(t, restored.Close())
}

func TestPHPConfigRootAndResourceReferences(t *testing.T) {
	source := `<?php
return [
    'framework' => [],
    'when@prod' => [
        'twig' => [],
        'imports' => [
            ['resource' => 'legacy.php'],
        ],
    ],
    'nested' => ['framework' => []],
];
`
	file := indexer.NewParsedFile(
		"/project/config/packages/app.php",
		[]byte(source),
	)
	tree := file.SyntaxTree()
	require.NotNil(t, tree)

	for _, name := range []string{"framework", "twig"} {
		offset := strings.Index(source, "'"+name+"'") + 2
		reference, found := RootReferenceAt(
			tree.Root.NodeAtOffset(uint32(offset)),
		)
		require.True(t, found, name)
		require.Equal(t, name, reference.Name)
	}
	for _, name := range []string{"when@prod", "imports"} {
		offset := strings.Index(source, "'"+name+"'") + 2
		_, found := RootReferenceAt(tree.Root.NodeAtOffset(uint32(offset)))
		require.False(t, found, name)
	}
	nestedOffset := strings.LastIndex(source, "'framework'") + 2
	_, found := RootReferenceAt(
		tree.Root.NodeAtOffset(uint32(nestedOffset)),
	)
	require.False(t, found)

	resourceOffset := strings.Index(source, "legacy.php") + 2
	resource, found := ResourceReferenceAt(
		tree.Root.NodeAtOffset(uint32(resourceOffset)),
	)
	require.True(t, found)
	require.Equal(t, "legacy.php", resource.Path)
}

func TestPHPConfigReferencesAcceptConfigFactoryAndRejectClassReturns(
	t *testing.T,
) {
	source := `<?php
App::config(['framework' => []]);
final class NotConfig {
    public function data(): array {
        return ['twig' => []];
    }
}
`
	file := indexer.NewParsedFile("/project/config/app.php", []byte(source))
	tree := file.SyntaxTree()
	offset := strings.Index(source, "framework") + 2
	_, found := RootReferenceAt(tree.Root.NodeAtOffset(uint32(offset)))
	require.True(t, found)
	offset = strings.Index(source, "twig") + 2
	_, found = RootReferenceAt(tree.Root.NodeAtOffset(uint32(offset)))
	require.False(t, found)
}

func TestYAMLConfigRootAndResourceReferences(t *testing.T) {
	source := `framework: {}
when@prod:
  twig: {}
  imports:
    - resource: 'legacy_*.yaml'
nested:
  framework: {}
services:
  App\Service\:
    resource: ../src/
`
	file := indexer.NewParsedFile(
		"/project/config/packages/app.yaml",
		[]byte(source),
	)
	tree := file.SyntaxTree()
	require.NotNil(t, tree)

	for _, name := range []string{"framework", "twig"} {
		offset := strings.Index(source, name) + 2
		reference, found := RootReferenceAt(
			tree.Root.NodeAtOffset(uint32(offset)),
		)
		require.True(t, found, name)
		assert.Equal(t, name, reference.Name)
	}
	for _, name := range []string{"when@prod", "imports"} {
		offset := strings.Index(source, name) + 2
		_, found := RootReferenceAt(
			tree.Root.NodeAtOffset(uint32(offset)),
		)
		assert.False(t, found, name)
	}
	nestedOffset := strings.LastIndex(source, "framework") + 2
	_, found := RootReferenceAt(
		tree.Root.NodeAtOffset(uint32(nestedOffset)),
	)
	assert.False(t, found)

	offset := strings.Index(source, "legacy_*.yaml") + 2
	reference, referenceFound := ResourceReferenceAt(
		tree.Root.NodeAtOffset(uint32(offset)),
	)
	require.True(t, referenceFound)
	assert.Equal(t, "legacy_*.yaml", reference.Path)

	for _, value := range []string{"../src/"} {
		offset = strings.Index(source, value) + 2
		reference, referenceFound := ResourceReferenceAt(
			tree.Root.NodeAtOffset(uint32(offset)),
		)
		assert.False(t, referenceFound, value)
		assert.Empty(t, reference)
	}
}
