package hover

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/stimulus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStimulusHoverDescribesController(t *testing.T) {
	index, err := stimulus.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/assets/controllers.json",
		[]byte(`{
  "controllers": {
    "@symfony/ux-chartjs": {"chart": {"enabled": true}}
  }
}`),
	)))
	source := `<div data-controller="symfony--ux-chartjs--chart"></div>`
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "ux-chartjs") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, hoverErr := NewStimulusHoverProvider(
		"/project",
		index,
	).GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	})
	require.NoError(t, hoverErr)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Stimulus controller")
	assert.Contains(t, result.Contents.Value, "controllers.json")
	assert.Contains(t, result.Contents.Value, "@symfony/ux-chartjs/chart")
}
