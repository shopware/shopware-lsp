package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/symfonyconfig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestSymfonyConfigDefinitionNavigatesToTreeRoot(t *testing.T) {
	root := t.TempDir()
	declarationPath := filepath.Join(
		root,
		"src",
		"DependencyInjection",
		"Configuration.php",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(declarationPath), 0o755))
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
	index, err := symfonyconfig.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		declarationPath,
		[]byte(declarationSource),
	)))

	provider := NewSymfonyConfigDefinitionProvider(index)
	for _, fixture := range []struct {
		name   string
		source string
	}{
		{
			name:   "app.php",
			source: "<?php return ['when@prod' => ['app_root' => []]];",
		},
		{
			name: "app.yaml",
			source: `when@prod:
  app_root: {}
`,
		},
	} {
		configPath := filepath.Join(
			root,
			"config",
			"packages",
			fixture.name,
		)
		document := lsp.NewTextDocument(
			uriutil.FileURI(configPath),
			fixture.source,
			1,
		)
		offset := uint32(
			strings.Index(fixture.source, "app_root") + 2,
		)
		locations := provider.GetDefinition(
			context.Background(),
			securityDefinitionRequest(
				document,
				document.SyntaxTree.Root.NodeAtOffset(offset),
				offset,
			),
		)
		require.Len(t, locations, 1)
		assert.Equal(
			t,
			uriutil.FileURI(declarationPath),
			locations[0].URI,
		)
		assert.Equal(t, 5, locations[0].Range.Start.Line)
		assert.Equal(t, 32, locations[0].Range.Start.Character)
	}
}

func TestSymfonyConfigDefinitionNavigatesToImportedPHPResource(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config", "packages")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	targetPath := filepath.Join(configDir, "legacy.php")
	require.NoError(t, os.WriteFile(
		targetPath,
		[]byte("<?php return [];"),
		0o644,
	))
	index, err := symfonyconfig.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	provider := NewSymfonyConfigDefinitionProvider(index)
	for _, fixture := range []struct {
		name   string
		source string
	}{
		{
			name: "app.php",
			source: `<?php
return [
    'imports' => [
        ['resource' => 'legacy.php'],
    ],
];
`,
		},
		{
			name: "app.yaml",
			source: `imports:
  - resource: legacy.php
`,
		},
	} {
		configPath := filepath.Join(configDir, fixture.name)
		document := lsp.NewTextDocument(
			uriutil.FileURI(configPath),
			fixture.source,
			1,
		)
		offset := uint32(
			strings.Index(fixture.source, "legacy.php") + 2,
		)
		locations := provider.GetDefinition(
			context.Background(),
			securityDefinitionRequest(
				document,
				document.SyntaxTree.Root.NodeAtOffset(offset),
				offset,
			),
		)
		require.Len(t, locations, 1)
		assert.Equal(t, uriutil.FileURI(targetPath), locations[0].URI)
	}
}

func TestSymfonyConfigDefinitionNavigatesToImportedResourceGlob(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config", "packages")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	var expected []string
	for _, name := range []string{"legacy_a.php", "legacy_b.php"} {
		path := filepath.Join(configDir, name)
		require.NoError(t, os.WriteFile(
			path,
			[]byte("<?php return [];"),
			0o644,
		))
		expected = append(expected, uriutil.FileURI(path))
	}
	source := `<?php
return ['imports' => [['resource' => 'legacy_*.php']]];
`
	configPath := filepath.Join(configDir, "app.php")
	document := lsp.NewTextDocument(
		uriutil.FileURI(configPath),
		source,
		1,
	)
	offset := uint32(strings.Index(source, "legacy_") + 2)
	index, err := symfonyconfig.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	locations := NewSymfonyConfigDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(
			document,
			document.SyntaxTree.Root.NodeAtOffset(offset),
			offset,
		),
	)
	require.Len(t, locations, 2)
	assert.Equal(t, expected[0], locations[0].URI)
	assert.Equal(t, expected[1], locations[1].URI)
}
