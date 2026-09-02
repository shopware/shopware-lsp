package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/translation"
)

type SnippetAnalyzer struct {
	snippetIndex     *snippet.SnippetIndexer
	translationIndex *translation.Index
}

func NewSnippetAnalyzer(
	snippetIndexer *snippet.SnippetIndexer,
	translationIndexes ...*translation.Index,
) *SnippetAnalyzer {
	provider := &SnippetAnalyzer{snippetIndex: snippetIndexer}
	if len(translationIndexes) != 0 {
		provider.translationIndex = translationIndexes[0]
	}
	return provider
}

func (s *SnippetAnalyzer) Analyze(ctx context.Context, document *lsp.TextDocument) ([]lsp.Problem, error) {
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".twig":
		return s.twigDiagnostics(ctx, document)
	case ".js", ".ts":
		return s.jsDiagnostics(ctx, document)
	default:
		return []lsp.Problem{}, nil
	}
}

func (s *SnippetAnalyzer) twigDiagnostics(ctx context.Context, document *lsp.TextDocument) ([]lsp.Problem, error) {
	var diagnostics []lsp.Problem
	root := document.SyntaxTree.Root

	// Check if this is an admin file
	isAdminFile := strings.Contains(document.URI, "/Resources/app/administration/")

	if isAdminFile {
		for _, reference := range snippet.AdminTwigReferences(root) {
			if ctx.Err() != nil {
				return nil, nil
			}
			snippetText := reference.Key

			// Skip empty strings
			if snippetText == "" {
				continue
			}

			snippets, err := s.snippetIndex.GetAdminSnippet(snippetText)
			if err != nil {
				return nil, fmt.Errorf("query admin snippet %q: %w", snippetText, err)
			}

			if len(snippets) == 0 {
				diagnostics = append(diagnostics, lsp.Problem{
					Range:    reference.Range,
					Message:  fmt.Sprintf("Admin snippet '%s' not found", snippetText),
					Source:   "shopware",
					Severity: protocol.DiagnosticSeverityError,
					ID:       "admin.snippet.missing",
					Payload: map[string]any{
						"snippetText": snippetText,
					},
				})
			}
		}
	} else {
		var matches []*twigsyntax.Node
		// Check for frontend snippet pattern: {{ 'key'|trans }}
		for candidate := range twigquery.IterateNodes(root, twigsyntax.TwigLiteralString) {
			if ctx.Err() != nil {
				return nil, nil
			}
			if twigquery.StringInFilter(candidate, "trans") {
				matches = append(matches, candidate)
			}
		}

		for _, match := range matches {
			if ctx.Err() != nil {
				return nil, nil
			}
			snippetText := twigquery.StringValue(match)

			// Skip empty strings
			if snippetText == "" {
				continue
			}

			snippets, err := s.snippetIndex.GetFrontendSnippet(snippetText)
			if err != nil {
				return nil, fmt.Errorf("query frontend snippet %q: %w", snippetText, err)
			}
			if len(snippets) == 0 && s.translationIndex != nil {
				exists, translationErr := s.translationIndex.HasMessage(
					"messages",
					snippetText,
				)
				if translationErr != nil {
					return nil, fmt.Errorf(
						"query Symfony translation %q: %w",
						snippetText,
						translationErr,
					)
				}
				if exists {
					continue
				}
			}

			if len(snippets) == 0 {
				diagnostics = append(diagnostics, lsp.Problem{
					Range:    match.RangeTrimmedTrivia(),
					Message:  fmt.Sprintf("Snippet '%s' not found", snippetText),
					Source:   "shopware",
					Severity: protocol.DiagnosticSeverityError,
					ID:       "frontend.snippet.missing",
					Payload: map[string]any{
						"snippetText": snippetText,
					},
				})
			}
		}
	}

	return diagnostics, nil
}

func (s *SnippetAnalyzer) jsDiagnostics(ctx context.Context, document *lsp.TextDocument) ([]lsp.Problem, error) {
	var diagnostics []lsp.Problem
	root := document.SyntaxTree.Root

	for _, match := range snippet.AdminJavaScriptStringReferences(root) {
		if ctx.Err() != nil {
			return nil, nil
		}
		snippetText := jsquery.StringValue(match)

		// Skip empty strings
		if snippetText == "" {
			continue
		}

		snippets, err := s.snippetIndex.GetAdminSnippet(snippetText)
		if err != nil {
			return nil, fmt.Errorf("query admin snippet %q: %w", snippetText, err)
		}

		if len(snippets) == 0 {
			matchRange := match.RangeTrimmedTrivia()
			diagnostics = append(diagnostics, lsp.Problem{
				Range:    matchRange,
				Message:  fmt.Sprintf("Admin snippet '%s' not found", snippetText),
				Source:   "shopware",
				Severity: protocol.DiagnosticSeverityError,
				ID:       "admin.snippet.missing",
				Payload: map[string]any{
					"snippetText": snippetText,
				},
			})
		}
	}

	return diagnostics, nil
}
