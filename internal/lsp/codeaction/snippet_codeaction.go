package codeaction

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/snippet"
)

// SnippetCodeActionProvider provides code actions for snippet diagnostics
type SnippetCodeActionProvider struct {
	snippetIndex *snippet.SnippetIndexer
}

// NewSnippetCodeActionProvider creates a new SnippetCodeActionProvider
func NewSnippetCodeActionProvider(snippetIndexer *snippet.SnippetIndexer) *SnippetCodeActionProvider {
	return &SnippetCodeActionProvider{snippetIndex: snippetIndexer}
}

// GetCodeActionKinds returns the kinds of code actions this provider can provide
func (s *SnippetCodeActionProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{
		protocol.CodeActionQuickFix,
	}
}

// GetCodeActions returns code actions for snippet diagnostics
func (s *SnippetCodeActionProvider) GetCodeActions(ctx context.Context, params *lsp.CodeActionRequest) []protocol.CodeAction {
	if !strings.HasSuffix(strings.ToLower(filepath.Ext(params.TextDocument.URI)), ".twig") {
		return []protocol.CodeAction{}
	}

	var codeActions []protocol.CodeAction

	// Check if this is an admin file
	isAdminFile := strings.Contains(params.TextDocument.URI, "/Resources/app/administration/")

	if params.Range.Start.Line == params.Range.End.Line && params.Range.Start.Character == params.Range.End.Character {
		// No selection, so we can't create a snippet from selection
		codeActions = append(codeActions, protocol.CodeAction{
			Title: "Insert Snippet",
			Kind:  protocol.CodeActionQuickFix,
			Command: &protocol.CommandAction{
				Title:   "Insert Snippet",
				Command: "shopware.insertSnippet",
			},
		})
	}

	if params.Range.Start.Line != params.Range.End.Line || params.Range.Start.Character != params.Range.End.Character {
		// There is a text selection
		selectedText := textForRange(params.DocumentContent, params.Range, params.LineIndex)
		if selectedText != "" {
			if isAdminFile {
				codeActions = append(codeActions, protocol.CodeAction{
					Title: "Create admin snippet from selection",
					Kind:  protocol.CodeActionQuickFix,
					Command: &protocol.CommandAction{
						Title:     "Create Admin Snippet from Selection",
						Command:   "shopware.createAdminSnippetFromSelection",
						Arguments: []any{params.TextDocument.URI, selectedText},
					},
				})
			} else {
				codeActions = append(codeActions, protocol.CodeAction{
					Title: "Create snippet from selection",
					Kind:  protocol.CodeActionQuickFix,
					Command: &protocol.CommandAction{
						Title:     "Create Snippet from Selection",
						Command:   "shopware.createSnippetFromSelection",
						Arguments: []any{params.TextDocument.URI, selectedText},
					},
				})
			}
		}
	}

	return codeActions
}

func textForRange(
	content []byte,
	selectedRange protocol.Range,
	lineIndex *cst.LineIndex,
) string {
	if lineIndex == nil {
		lineIndex = cst.NewLineIndex(string(content))
	}
	start := lineIndex.OffsetUTF16(
		uint32(selectedRange.Start.Line),
		uint32(selectedRange.Start.Character),
	)
	end := lineIndex.OffsetUTF16(
		uint32(selectedRange.End.Line),
		uint32(selectedRange.End.Character),
	)
	if start > end || int(end) > len(content) {
		return ""
	}
	return string(content[start:end])
}
