package linkedediting

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminLinkedEditingLinksTheExactNestedComponentTagPair(t *testing.T) {
	source := "😀 <sw-card>\n  <sw-button>Save</sw-button>\n</sw-card>"
	provider := NewAdminLinkedEditingProvider()

	card := adminLinkedEditingAt(t, provider, source, "sw-card", 2)
	require.NotNil(t, card)
	require.Len(t, card.Ranges, 2)
	assert.Equal(t, []string{"sw-card", "sw-card"}, []string{
		adminLinkedEditingRangeText(t, source, card.Ranges[0]),
		adminLinkedEditingRangeText(t, source, card.Ranges[1]),
	})
	// The leading astral rune occupies two UTF-16 units, not four UTF-8 bytes.
	assert.Equal(t, protocol.Position{Line: 0, Character: 4}, card.Ranges[0].Start)
	assert.Equal(t, adminComponentTagWordPattern, card.WordPattern)

	button := adminLinkedEditingAt(t, provider, source, "sw-button", 4)
	require.NotNil(t, button)
	require.Len(t, button.Ranges, 2)
	assert.Equal(t, []string{"sw-button", "sw-button"}, []string{
		adminLinkedEditingRangeText(t, source, button.Ranges[0]),
		adminLinkedEditingRangeText(t, source, button.Ranges[1]),
	})
}

func TestAdminLinkedEditingWorksFromClosingNameAndWordBoundary(t *testing.T) {
	source := `<sw-card>Content</sw-card>`
	provider := NewAdminLinkedEditingProvider()
	result := adminLinkedEditingAt(
		t, provider, source, "</sw-card", len("</sw-card"),
	)
	require.NotNil(t, result)
	require.Len(t, result.Ranges, 2)
	assert.Equal(t, []string{"sw-card", "sw-card"}, []string{
		adminLinkedEditingRangeText(t, source, result.Ranges[0]),
		adminLinkedEditingRangeText(t, source, result.Ranges[1]),
	})
}

func TestAdminLinkedEditingIsConservativeOutsidePairedVueTags(t *testing.T) {
	provider := NewAdminLinkedEditingProvider()
	for _, test := range []struct {
		name   string
		source string
		needle string
	}{
		{name: "native HTML", source: `<div></div>`, needle: "div"},
		{name: "self closing", source: `<sw-card />`, needle: "sw-card"},
		{name: "missing close", source: `<sw-card>`, needle: "sw-card"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Nil(t, adminLinkedEditingAt(
				t, provider, test.source, test.needle, 1,
			))
		})
	}

	dynamic := adminLinkedEditingAt(
		t, provider, `<component :is="name"></component>`, "component", 2,
	)
	require.NotNil(t, dynamic)
	require.Len(t, dynamic.Ranges, 2)
}

func adminLinkedEditingAt(
	t *testing.T,
	provider *AdminLinkedEditingProvider,
	source,
	needle string,
	within int,
) *protocol.LinkedEditingRanges {
	t.Helper()
	document := lsp.NewTextDocument(
		uriutil.FileURI("/project/component.html.twig"), source, 2,
	)
	offset := strings.Index(source, needle) + within
	require.GreaterOrEqual(t, offset, 0)
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.LinkedEditingRangeParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
		Position: protocol.Position{
			Line: int(line), Character: int(character),
		},
	}
	result, err := provider.GetLinkedEditingRanges(
		context.Background(),
		&lsp.LinkedEditingRangeRequest{
			LinkedEditingRangeParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, Language: document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            document.SyntaxTree.Root.NodeAtOffset(uint32(offset)),
			},
		},
	)
	require.NoError(t, err)
	return result
}

func adminLinkedEditingRangeText(
	t *testing.T,
	source string,
	rangeValue protocol.Range,
) string {
	t.Helper()
	document := lsp.NewTextDocument(
		uriutil.FileURI("/project/component.html.twig"), source, 1,
	)
	start := document.LineIndex.OffsetUTF16(
		uint32(rangeValue.Start.Line), uint32(rangeValue.Start.Character),
	)
	end := document.LineIndex.OffsetUTF16(
		uint32(rangeValue.End.Line), uint32(rangeValue.End.Character),
	)
	return source[start:end]
}
