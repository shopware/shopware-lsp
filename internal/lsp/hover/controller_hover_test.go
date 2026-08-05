package hover

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControllerHoverShowsResolvedTwigControllerSignature(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	classPath := filepath.Join(t.TempDir(), "NavController.php")
	source := `<?php
namespace App\Controller;
class NavController {
    /** Builds the navigation. */
    public function menu(string $section): array {}
}`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		[]byte(source),
	)))
	document := lsp.NewTextDocument(
		"file:///project/navigation.html.twig",
		`{{ controller('App\\Controller\\NavController::menu') }}`,
		1,
	)
	literals := twigquery.Nodes(
		document.SyntaxTree.Root,
		twigsyntax.TwigLiteralString,
	)
	require.Len(t, literals, 1)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	result, err := NewControllerHoverProvider(nil, phpIndex).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:  document,
				Root:      document.SyntaxTree.Root,
				Node:      literals[0],
				LineIndex: document.LineIndex,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(
		t,
		result.Contents.Value,
		"App\\Controller\\NavController::menu",
	)
	assert.Contains(t, result.Contents.Value, "$section")
	assert.Contains(t, result.Contents.Value, "Builds the navigation.")
}
