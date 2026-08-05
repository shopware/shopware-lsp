package inspections

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

const suggestionFixID lsp.FixID = "replace-with-suggestion"

type suggestionPayload struct {
	Replacement string `json:"replacement"`
}

type suggestionFix struct{}

func (suggestionFix) ID() lsp.FixID { return suggestionFixID }

func (suggestionFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[suggestionPayload](fixContext)
	if err != nil || payload.Replacement == "" {
		return lsp.FixPresentation{}, false, err
	}
	return lsp.FixPresentation{
		Title:      fmt.Sprintf("Replace with '%s'", payload.Replacement),
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, true, nil
}

func (suggestionFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[suggestionPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if _, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	builder := rewrite.NewBuilder(fixContext.Document.Source)
	if err := builder.ReplaceRange(
		protocolTextRange(fixContext.Document.LineIndex, fixContext.Diagnostic.Range),
		payload.Replacement,
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	edits, err := builder.Finish()
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	version := fixContext.Document.Version
	return rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
		rewrite.NewDocumentPlan(
			fixContext.Document.URI,
			&version,
			fixContext.Document.Source,
			edits,
		),
	}}, nil
}

func suggestionBoundFixes(payload map[string]any) []lsp.BoundFix {
	var values []string
	switch suggestions := payload["suggestions"].(type) {
	case []string:
		values = suggestions
	case []any:
		values = make([]string, 0, len(suggestions))
		for _, suggestion := range suggestions {
			if value, ok := suggestion.(string); ok {
				values = append(values, value)
			}
		}
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]lsp.BoundFix, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, lsp.BindFix(
			suggestionFixID,
			suggestionPayload{Replacement: value},
		))
	}
	return result
}
