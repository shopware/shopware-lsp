package reference

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControllerReferencesLinkPHPMethodAndEquivalentTwigUsages(t *testing.T) {
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
	twigSource := `{{ controller('App\\Controller\\NavController::menu') }}
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
	provider := NewControllerReferenceProvider(usages, services, phpIndex)

	twigDocument := lsp.NewTextDocument(
		uriutil.FileURI(twigPath),
		twigSource,
		1,
	)
	literals := twigquery.Nodes(
		twigDocument.SyntaxTree.Root,
		twigsyntax.TwigLiteralString,
	)
	require.Len(t, literals, 2)
	twigParams := &protocol.ReferenceParams{}
	twigParams.TextDocument.URI = twigDocument.URI
	twigParams.Context.IncludeDeclaration = true
	twigLocations, err := provider.GetReferences(
		context.Background(),
		&lsp.ReferenceRequest{
			ReferenceParams: twigParams,
			SyntaxContext: lsp.SyntaxContext{
				Document:  twigDocument,
				Root:      twigDocument.SyntaxTree.Root,
				Node:      literals[0],
				LineIndex: twigDocument.LineIndex,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, twigLocations, 3)
	assert.Equal(t, []string{
		uriutil.FileURI(classPath),
		uriutil.FileURI(twigPath),
		uriutil.FileURI(twigPath),
	}, []string{
		twigLocations[0].URI,
		twigLocations[1].URI,
		twigLocations[2].URI,
	})

	phpDocument := lsp.NewTextDocument(
		uriutil.FileURI(classPath),
		classSource,
		1,
	)
	var method semantic.Symbol
	for _, symbol := range phpIndex.SemanticSnapshot().SymbolsIn(classPath) {
		if symbol.Kind == semantic.MethodSymbol {
			method = symbol
			break
		}
	}
	require.NotEmpty(t, method.Name)
	phpParams := &protocol.ReferenceParams{}
	phpParams.TextDocument.URI = phpDocument.URI
	line, character := phpDocument.LineIndex.PositionUTF16(
		method.SelectionRange.Start,
	)
	phpParams.Position.Line = int(line)
	phpParams.Position.Character = int(character)
	phpLocations, err := provider.GetReferences(
		context.Background(),
		&lsp.ReferenceRequest{
			ReferenceParams: phpParams,
			SyntaxContext: lsp.SyntaxContext{
				Document:  phpDocument,
				Root:      phpDocument.SyntaxTree.Root,
				Node:      phpDocument.SyntaxTree.Root,
				LineIndex: phpDocument.LineIndex,
			},
		},
	)
	require.NoError(t, err)
	assert.Len(t, phpLocations, 2)
}
