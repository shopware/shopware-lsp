package hover

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestTwigConstantHoverShowsClassConstantMetadata(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "src", "CardSuite.php"),
		[]byte(`<?php
namespace App;
class CardSuite {
    /**
     * Card suit code.
     * @deprecated Use HEARTS.
     */
    public const CLUBS = 'clubs';
}`),
	)))
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	twigIndex.SetDependencies(phpIndex, nil)

	source := `{# @var suite \App\CardSuite #}
{{ constant('CLUBS', suite) }}`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root,
			"templates",
			"cards.html.twig",
		)),
		source,
		1,
	)
	offset := uint32(strings.LastIndex(source, "CLUBS") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewTwigConstantHoverProvider(
		phpIndex,
		twigIndex,
	).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result.Contents.Value, "PHP class constant")
	require.Contains(t, result.Contents.Value, "App\\CardSuite::CLUBS")
	require.Contains(t, result.Contents.Value, "Card suit code")
	require.Contains(t, result.Contents.Value, "Deprecated")
}
