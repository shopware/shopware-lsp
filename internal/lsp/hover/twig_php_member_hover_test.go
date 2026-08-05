package hover

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigPHPMemberHoverShowsDeprecation(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Bar.php",
		[]byte(`<?php
namespace Foo;
class Bar {
    /**
     * Legacy accessor.
     *
     * @deprecated
     */
    public function getDeprecated(): static { return $this; }
}`),
	)))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })

	source := `{# @var bar \Foo\Bar #} {{ bar.deprecated }}`
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		source,
		1,
	)
	offset := strings.LastIndex(source, "deprecated") + 1
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Character = offset
	result, err := NewTwigHoverProvider(
		"/project",
		nil,
		phpIndex,
		twigIndex,
	).GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				uint32(offset),
			),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Foo\\Bar::getDeprecated")
	assert.Contains(t, result.Contents.Value, "Legacy accessor")
	assert.Contains(t, result.Contents.Value, "**Deprecated**")
	require.NotNil(t, result.Range)
	assert.Equal(t, "deprecated", hoverRangeText(document, *result.Range))
}

func TestTwigPHPClassConstantHover(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Bar.php",
		[]byte(`<?php
namespace Foo;
class Bar {
    /** The model kind. */
    public const KIND = 'bar';
}`),
	)))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })

	source := `{# @var bar \Foo\Bar #} {{ bar.KIND }}`
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		source,
		1,
	)
	offset := strings.LastIndex(source, "KIND") + 1
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Character = offset
	result, err := NewTwigHoverProvider(
		"/project",
		nil,
		phpIndex,
		twigIndex,
	).GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				uint32(offset),
			),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Foo\\Bar::KIND")
	assert.Contains(t, result.Contents.Value, "The model kind")
	require.NotNil(t, result.Range)
	assert.Equal(t, "KIND", hoverRangeText(document, *result.Range))
}

func hoverRangeText(
	document *lsp.TextDocument,
	rng protocol.Range,
) string {
	start := document.LineIndex.OffsetUTF16(
		uint32(rng.Start.Line),
		uint32(rng.Start.Character),
	)
	end := document.LineIndex.OffsetUTF16(
		uint32(rng.End.Line),
		uint32(rng.End.Character),
	)
	return string(document.Text[start:end])
}
