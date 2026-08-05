package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigCustomTagDefinitionForOpeningAndClosingTag(t *testing.T) {
	root := t.TempDir()
	parserPath := filepath.Join(root, "src", "LegacyTokenParser.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(parserPath), 0o755))
	parserSource := []byte(`<?php
class LegacyTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'legacy'; }
}`)
	require.NoError(t, os.WriteFile(parserPath, parserSource, 0o644))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		parserPath,
		parserSource,
	)))
	provider := NewTwigDefinitionProvider(root, twigIndex, nil, nil)

	for _, name := range []string{"legacy", "endlegacy"} {
		source := "{% legacy %}\n{% endlegacy %}"
		offset := strings.Index(source, name) + 2
		document := lsp.NewTextDocument(
			uriutil.FileURI(filepath.Join(root, "templates", "page.html.twig")),
			source,
			1,
		)
		line, character := document.LineIndex.PositionUTF16(uint32(offset))
		params := &protocol.DefinitionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		locations := provider.GetDefinition(
			context.Background(),
			&lsp.DefinitionRequest{
				DefinitionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document:        document,
					DocumentContent: document.Text,
					DocumentTree:    document.SyntaxTree,
					LineIndex:       document.LineIndex,
					Root:            document.SyntaxTree.Root,
					Node: document.SyntaxTree.Root.NodeAtOffset(
						uint32(offset),
					),
				},
			},
		)
		require.Len(t, locations, 1, name)
		assert.Equal(t, uriutil.FileURI(parserPath), locations[0].URI)
		start := documentOffsetForPosition(
			parserSource,
			locations[0].Range.Start,
		)
		end := documentOffsetForPosition(
			parserSource,
			locations[0].Range.End,
		)
		assert.Equal(
			t,
			"legacy",
			string(parserSource[start:end]),
		)
	}
}

func documentOffsetForPosition(
	source []byte,
	position protocol.Position,
) int {
	document := lsp.NewTextDocument("file:///source.php", string(source), 1)
	return int(document.LineIndex.OffsetUTF16(
		uint32(position.Line),
		uint32(position.Character),
	))
}
