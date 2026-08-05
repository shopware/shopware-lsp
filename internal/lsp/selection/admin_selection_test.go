package selection

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

func TestAdminJavaScriptSelectionExpandsThroughSemanticSyntaxContainers(
	t *testing.T,
) {
	source := `// 😀
Component.register('sw-card', {
    methods: {
        save() {
            return this.value;
        },
    },
});`
	ranges := adminSelectionForNeedles(
		t, "/project/sw-card/index.ts", source, []string{"value"},
	)
	require.Len(t, ranges, 1)
	texts := adminSelectionTexts(t, source, ranges[0])
	require.NotEmpty(t, texts)
	assert.Equal(t, "value", texts[0])
	assert.Contains(t, texts, "this.value")
	assert.Contains(t, texts, "return this.value;")
	assert.Contains(t, texts, source)
	assertAdminSelectionStrictlyNested(t, ranges[0])
}

func TestAdminTwigSelectionSupportsMultiplePositionsAndUTF16(t *testing.T) {
	source := "😀 <sw-card :title=\"product.name\">\n" +
		"    <span>{{ product.name }}</span>\n" +
		"</sw-card>"
	ranges := adminSelectionForNeedles(
		t,
		"/project/sw-card/sw-card.html.twig",
		source,
		[]string{"sw-card", "product.name", "product.name"},
	)
	require.Len(t, ranges, 3)

	tagTexts := adminSelectionTexts(t, source, ranges[0])
	assert.Equal(t, "sw-card", tagTexts[0])
	assert.Contains(t, tagTexts, `<sw-card :title="product.name">`)
	assert.Contains(t, tagTexts, source)
	// The astral rune counts as two UTF-16 units before '<' and the tag name.
	assert.Equal(
		t,
		protocol.Position{Line: 0, Character: 4},
		ranges[0].Range.Start,
	)

	attributeTexts := adminSelectionTexts(t, source, ranges[1])
	assert.Equal(t, "name", attributeTexts[0])
	assert.Contains(t, attributeTexts, "product.name")
	assert.Contains(t, attributeTexts, `:title="product.name"`)

	interpolationTexts := adminSelectionTexts(t, source, ranges[2])
	assert.Equal(t, "name", interpolationTexts[0])
	assert.Contains(t, interpolationTexts, "product.name")
	assert.Contains(t, interpolationTexts, "{{ product.name }}")
	for _, rangeValue := range ranges {
		assertAdminSelectionStrictlyNested(t, rangeValue)
	}
}

func TestAdminSelectionReturnsTheEmptyDocumentRange(t *testing.T) {
	ranges := adminSelectionForNeedles(
		t, "/project/empty.ts", "", []string{""},
	)
	require.Len(t, ranges, 1)
	assert.Equal(t, protocol.Range{}, ranges[0].Range)
	assert.Nil(t, ranges[0].Parent)
}

func adminSelectionForNeedles(
	t *testing.T,
	path,
	source string,
	needles []string,
) []protocol.SelectionRange {
	t.Helper()
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 8)
	positions := make([]protocol.Position, 0, len(needles))
	searchFrom := 0
	for _, needle := range needles {
		relative := strings.Index(source[searchFrom:], needle)
		require.GreaterOrEqual(t, relative, 0)
		offset := searchFrom + relative
		if needle != "" {
			offset += max(len(needle)-1, 0)
		}
		line, character := document.LineIndex.PositionUTF16(uint32(offset))
		positions = append(positions, protocol.Position{
			Line: int(line), Character: int(character),
		})
		searchFrom = offset + 1
	}
	params := &protocol.SelectionRangeParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
		Positions:    positions,
	}
	ranges, err := NewAdminSelectionRangeProvider().GetSelectionRanges(
		context.Background(),
		&lsp.SelectionRangeRequest{
			SelectionRangeParams: params, Document: document,
		},
	)
	require.NoError(t, err)
	return ranges
}

func adminSelectionTexts(
	t *testing.T,
	source string,
	selection protocol.SelectionRange,
) []string {
	t.Helper()
	document := lsp.NewTextDocument(
		uriutil.FileURI("/project/selection.html.twig"), source, 1,
	)
	var result []string
	for current := &selection; current != nil; current = current.Parent {
		start := document.LineIndex.OffsetUTF16(
			uint32(current.Range.Start.Line),
			uint32(current.Range.Start.Character),
		)
		end := document.LineIndex.OffsetUTF16(
			uint32(current.Range.End.Line),
			uint32(current.Range.End.Character),
		)
		result = append(result, source[start:end])
	}
	return result
}

func assertAdminSelectionStrictlyNested(
	t *testing.T,
	selection protocol.SelectionRange,
) {
	t.Helper()
	for current := &selection; current.Parent != nil; current = current.Parent {
		parent := current.Parent.Range
		child := current.Range
		assert.True(t, adminSelectionPositionLessOrEqual(parent.Start, child.Start))
		assert.True(t, adminSelectionPositionLessOrEqual(child.End, parent.End))
		assert.NotEqual(t, child, parent)
	}
}

func adminSelectionPositionLessOrEqual(
	left,
	right protocol.Position,
) bool {
	return left.Line < right.Line ||
		left.Line == right.Line && left.Character <= right.Character
}
