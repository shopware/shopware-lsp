package codeaction

import (
	"sort"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func applyCodeActionEdit(
	t *testing.T,
	source string,
	action protocol.CodeAction,
	uri string,
	document *lsp.TextDocument,
) string {
	t.Helper()
	require.NotNil(t, action.Edit)
	require.NotNil(t, document)
	edits := append([]protocol.TextEdit(nil), action.Edit.Changes[uri]...)
	require.NotEmpty(t, edits)
	type offsetEdit struct {
		start uint32
		end   uint32
		text  string
	}
	offsets := make([]offsetEdit, 0, len(edits))
	for _, edit := range edits {
		offsets = append(offsets, offsetEdit{
			start: document.LineIndex.OffsetUTF16(
				uint32(edit.Range.Start.Line),
				uint32(edit.Range.Start.Character),
			),
			end: document.LineIndex.OffsetUTF16(
				uint32(edit.Range.End.Line),
				uint32(edit.Range.End.Character),
			),
			text: edit.NewText,
		})
	}
	sort.SliceStable(offsets, func(left, right int) bool {
		if offsets[left].start == offsets[right].start {
			return offsets[left].end > offsets[right].end
		}
		return offsets[left].start > offsets[right].start
	})
	updated := source
	for _, edit := range offsets {
		require.LessOrEqual(t, edit.start, edit.end)
		require.LessOrEqual(t, int(edit.end), len(updated))
		updated = updated[:edit.start] + edit.text + updated[edit.end:]
	}
	return updated
}
