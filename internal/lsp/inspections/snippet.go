package inspections

import (
	"context"
	"fmt"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/translation"
)

const createSnippetFixID lsp.FixID = "create-snippet"

type createSnippetPayload struct {
	Key   string `json:"key"`
	Admin bool   `json:"admin,omitempty"`
}

func NewSnippet(
	snippetIndex *snippet.SnippetIndexer,
	translationIndex *translation.Index,
) lsp.Inspection {
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID: "shopware.snippet",
			Languages: []language.ID{
				language.JavaScript,
				language.Twig,
			},
			Problems: []lsp.ProblemDefinition{
				{ID: "admin.snippet.missing", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
				{ID: "frontend.snippet.missing", Source: "shopware-lsp", DefaultSeverity: protocol.DiagnosticSeverityWarning},
			},
		},
		analyzer: diagnostics.NewSnippetAnalyzer(snippetIndex, translationIndex),
		fixes:    []lsp.QuickFix{createSnippetFix{}},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			key := mapString(payload, "snippetText")
			if key == "" {
				return nil
			}
			return []lsp.BoundFix{lsp.BindFix(createSnippetFixID, createSnippetPayload{
				Key:   key,
				Admin: string(code) == "admin.snippet.missing",
			})}
		},
	}
}

type createSnippetFix struct{}

func (createSnippetFix) ID() lsp.FixID { return createSnippetFixID }

func (createSnippetFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[createSnippetPayload](fixContext)
	if err != nil || payload.Key == "" {
		return lsp.FixPresentation{}, false, err
	}
	title := fmt.Sprintf("Create snippet '%s'", payload.Key)
	if payload.Admin {
		title = fmt.Sprintf("Create admin snippet '%s'", payload.Key)
	}
	return lsp.FixPresentation{
		Title:      title,
		Kind:       protocol.CodeActionQuickFix,
		Resolution: lsp.FixEager,
	}, true, nil
}

func (createSnippetFix) BuildCommand(
	_ context.Context,
	fixContext lsp.FixContext,
) (*protocol.CommandAction, error) {
	payload, err := lsp.DecodeBoundFixPayload[createSnippetPayload](fixContext)
	if err != nil {
		return nil, err
	}
	command := "shopware.createSnippet"
	title := "Create Snippet"
	if payload.Admin {
		command = "shopware.createAdminSnippet"
		title = "Create Admin Snippet"
	}
	return &protocol.CommandAction{
		Title:     title,
		Command:   command,
		Arguments: []any{payload.Key, fixContext.Document.URI},
	}, nil
}
