package completion

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

func TestStimulusCompletionUsesHTMLAndTwigControllerNames(t *testing.T) {
	index, err := stimulus.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range map[string]string{
		"/project/assets/controllers/hello_controller.js": `import { Controller } from '@hotwired/stimulus';
export default class extends Controller {}`,
		"/project/assets/controllers.json": `{
  "controllers": {
    "@symfony/ux-chartjs": {"chart": {"enabled": true}}
  }
}`,
	} {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	source := `<div data-controller=""></div>
{{ stimulus_controller('') }}`
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		source,
		1,
	)
	provider := NewStimulusCompletionProvider(index)
	for _, test := range []struct {
		needle string
		label  string
	}{
		{`""`, "symfony--ux-chartjs--chart"},
		{`''`, "@symfony/ux-chartjs/chart"},
	} {
		offset := uint32(strings.Index(source, test.needle) + 1)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		items := provider.GetCompletions(
			context.Background(),
			doctrineCompletionRequestAt(document, node, offset),
		)
		item := requireCompletion(t, items, test.label)
		assert.Equal(t, int(protocol.ModuleCompletion), item.Kind)
		edit, ok := item.TextEdit.(protocol.TextEdit)
		require.True(t, ok)
		assert.Equal(t, test.label, edit.NewText)
	}

	htmlDocument := lsp.NewTextDocument(
		"file:///project/public/page.html",
		`<div data-controller=""></div>`,
		1,
	)
	htmlOffset := uint32(strings.Index(htmlDocument.Source, `""`) + 1)
	htmlNode := htmlDocument.SyntaxTree.Root.NodeAtOffset(htmlOffset)
	htmlItems := provider.GetCompletions(
		context.Background(),
		doctrineCompletionRequestAt(
			htmlDocument,
			htmlNode,
			htmlOffset,
		),
	)
	requireCompletion(t, htmlItems, "hello")
}
