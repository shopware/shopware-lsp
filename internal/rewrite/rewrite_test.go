package rewrite_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/stretchr/testify/require"
)

func TestBuilderCoalescesInsertionsInDeclarationOrder(t *testing.T) {
	builder := rewrite.NewBuilder("abc")
	require.NoError(t, builder.Insert(1, "first"))
	require.NoError(t, builder.Insert(1, "-second"))
	edits, err := builder.Finish()
	require.NoError(t, err)
	require.Len(t, edits, 1)
	updated, err := rewrite.Apply("abc", edits)
	require.NoError(t, err)
	require.Equal(t, "afirst-secondbc", updated)
}

func TestBuilderRejectsOverlappingStructuralEdits(t *testing.T) {
	builder := rewrite.NewBuilder("abcdef")
	require.NoError(t, builder.ReplaceRange(cst.TextRange{Start: 1, End: 4}, "x"))
	require.NoError(t, builder.ReplaceRange(cst.TextRange{Start: 3, End: 5}, "y"))
	_, err := builder.Finish()
	require.ErrorIs(t, err, rewrite.ErrOverlap)

	builder = rewrite.NewBuilder("abcdef")
	require.NoError(t, builder.ReplaceRange(cst.TextRange{Start: 1, End: 4}, "x"))
	require.NoError(t, builder.Insert(2, "y"))
	_, err = builder.Finish()
	require.ErrorIs(t, err, rewrite.ErrOverlap)
}

func TestWorkspacePlanEmitsVersionedUTF16DocumentEdit(t *testing.T) {
	source := "emoji: 😀\n"
	start := uint32(len("emoji: "))
	builder := rewrite.NewBuilder(source)
	require.NoError(t, builder.ReplaceRange(
		cst.TextRange{Start: start, End: start + uint32(len("😀"))},
		"value",
	))
	edits, err := builder.Finish()
	require.NoError(t, err)
	version := 7
	plan := rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
		rewrite.NewDocumentPlan("file:///project/test.yaml", &version, source, edits),
	}}
	wire, err := plan.WorkspaceEdit()
	require.NoError(t, err)
	require.Len(t, wire.DocumentChanges, 1)
	change := wire.DocumentChanges[0]
	require.Equal(t, &version, change.TextDocument.Version)
	require.Equal(t, 7, change.Edits[0].Range.Start.Character)
	require.Equal(t, 9, change.Edits[0].Range.End.Character)
}

func TestWorkspacePlanMarshalsCreatedFileEditWithNullVersion(t *testing.T) {
	plan := rewrite.WorkspacePlan{Creates: []rewrite.CreateFilePlan{{
		URI:     "file:///project/Generated.php",
		Content: "<?php\n",
	}}}
	wire, err := plan.WorkspaceEdit()
	require.NoError(t, err)

	encoded, err := json.Marshal(wire)
	require.NoError(t, err)

	var payload struct {
		DocumentChanges []json.RawMessage `json:"documentChanges"`
	}
	require.NoError(t, json.Unmarshal(encoded, &payload))
	require.Len(t, payload.DocumentChanges, 2)

	var textEdit struct {
		TextDocument map[string]any `json:"textDocument"`
	}
	require.NoError(t, json.Unmarshal(payload.DocumentChanges[1], &textEdit))
	version, exists := textEdit.TextDocument["version"]
	require.True(t, exists, "unversioned LSP document edits must include version")
	require.Nil(t, version)
}

func TestElementHandleRejectsChangedDocumentVersion(t *testing.T) {
	document := lsp.NewTextDocument("file:///project/test.yaml", "value: bad\n", 3)
	element := document.SyntaxTree.Root.DescendantForRange(cst.TextRange{Start: 7, End: 10})
	handle, err := rewrite.NewElementHandle(
		document.URI,
		document.Version,
		document.SyntaxLanguage,
		element,
	)
	require.NoError(t, err)
	_, err = handle.Resolve(
		document.URI,
		4,
		document.SyntaxLanguage,
		document.SyntaxTree,
	)
	require.True(t, errors.Is(err, rewrite.ErrStaleHandle))
}

func TestElementHandleSurvivesUntypedJSONRoundTrip(t *testing.T) {
	document := lsp.NewTextDocument("file:///project/test.yaml", "value: transport-hash\n", 3)
	element := document.SyntaxTree.Root.DescendantForRange(cst.TextRange{Start: 7, End: 21})
	handle, err := rewrite.NewElementHandle(
		document.URI,
		document.Version,
		document.SyntaxLanguage,
		element,
	)
	require.NoError(t, err)

	encoded, err := json.Marshal(handle)
	require.NoError(t, err)
	var untyped map[string]any
	require.NoError(t, json.Unmarshal(encoded, &untyped))
	require.IsType(t, "", untyped["textHash"])
	encoded, err = json.Marshal(untyped)
	require.NoError(t, err)
	var decoded rewrite.ElementHandle
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, handle, decoded)
	_, err = decoded.Resolve(
		document.URI,
		document.Version,
		document.SyntaxLanguage,
		document.SyntaxTree,
	)
	require.NoError(t, err)
}
