package codelens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/symfonyconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSymfonyConfigCodeLensesLinkRootsAndResources(t *testing.T) {
	root := t.TempDir()
	declarationPath := filepath.Join(
		root,
		"src",
		"DependencyInjection",
		"Configuration.php",
	)
	resourcePath := filepath.Join(
		root,
		"config",
		"packages",
		"legacy.yaml",
	)
	require.NoError(t, os.MkdirAll(
		filepath.Dir(declarationPath),
		0o755,
	))
	require.NoError(t, os.MkdirAll(filepath.Dir(resourcePath), 0o755))
	declarationSource := `<?php
namespace App\DependencyInjection;
use Symfony\Component\Config\Definition\Builder\TreeBuilder;
final class Configuration {
    public function getConfigTreeBuilder(): TreeBuilder {
        return new TreeBuilder('app_root');
    }
}
`
	require.NoError(t, os.WriteFile(
		declarationPath,
		[]byte(declarationSource),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		resourcePath,
		[]byte("framework: {}\n"),
		0o644,
	))

	index, err := symfonyconfig.NewIndex(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		declarationPath,
		[]byte(declarationSource),
	)))
	provider := NewSymfonyConfigCodeLensProvider(index)

	for _, fixture := range []struct {
		name         string
		source       string
		rootLine     int
		resourceLine int
	}{
		{
			name: "app.yaml",
			source: `when@prod:
  app_root: {}
  imports:
    - resource: legacy.yaml
`,
			rootLine:     1,
			resourceLine: 3,
		},
		{
			name: "app.php",
			source: `<?php
return [
    'app_root' => [],
    'imports' => [['resource' => 'legacy.yaml']],
];
`,
			rootLine:     2,
			resourceLine: 3,
		},
	} {
		path := filepath.Join(
			root,
			"config",
			"packages",
			fixture.name,
		)
		lenses := relatedCodeLensesFor(
			t,
			provider,
			path,
			fixture.source,
		)
		require.Len(t, lenses, 2, fixture.name)

		rootLens := relatedLensByTitle(
			t,
			lenses,
			"Open configuration declaration",
		)
		assert.Equal(t, fixture.rootLine, rootLens.Range.Start.Line)
		assert.Equal(t, []string{
			relatedTarget(declarationPath, 6),
		}, relatedLensTargets(t, rootLens))

		resourceLens := relatedLensByTitle(
			t,
			lenses,
			"Open configuration resource",
		)
		assert.Equal(
			t,
			fixture.resourceLine,
			resourceLens.Range.Start.Line,
		)
		assert.Equal(t, []string{
			relatedTarget(resourcePath, 1),
		}, relatedLensTargets(t, resourceLens))
	}
}
