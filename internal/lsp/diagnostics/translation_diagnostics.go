package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	missingTranslationKeyCode    lsp.DiagnosticID = "symfony.translation.key.missing"
	missingTranslationDomainCode lsp.DiagnosticID = "symfony.translation.domain.missing"
)

type TranslationAnalyzer struct {
	index        *translation.Index
	phpIndex     *php.PHPIndex
	snippetIndex *snippet.SnippetIndexer
}

func NewTranslationAnalyzer(
	index *translation.Index,
	phpIndex *php.PHPIndex,
	snippetIndexes ...*snippet.SnippetIndexer,
) *TranslationAnalyzer {
	provider := &TranslationAnalyzer{
		index:    index,
		phpIndex: phpIndex,
	}
	if len(snippetIndexes) != 0 {
		provider.snippetIndex = snippetIndexes[0]
	}
	return provider
}

func (p *TranslationAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	extension := strings.ToLower(filepath.Ext(document.URI))
	if extension != ".php" && extension != ".twig" {
		return nil, nil
	}
	domains, err := p.index.GetDomains()
	if err != nil {
		return nil, fmt.Errorf("query translation domains: %w", err)
	}
	if len(domains) == 0 {
		return nil, nil
	}
	domainSet := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		domainSet[strings.ToLower(domain)] = struct{}{}
	}

	validationContext := ctx
	if extension == ".php" && p.phpIndex != nil {
		path, _ := uriutil.Path(document.URI)
		validationContext = p.phpIndex.AddDocumentContext(
			ctx,
			path,
			document.Version,
			document.SyntaxTree.Root,
			document.SyntaxTree.Root,
		)
	}

	var result []lsp.Problem
	seen := make(map[string]struct{})
	for _, reference := range translation.References(
		document.URI,
		document.SyntaxTree.Root,
		document.Text,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		if extension == ".php" && !translation.ValidatePHPReference(
			validationContext,
			reference,
			p.phpIndex,
			document.Text,
		) {
			continue
		}
		if reference.Node == nil {
			continue
		}
		rng := reference.Node.RangeTrimmedTrivia()
		identity := fmt.Sprintf(
			"%d:%d:%d",
			reference.Role,
			rng.Start,
			rng.End,
		)
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}

		switch reference.Role {
		case translation.ReferenceDomain:
			if reference.Domain == "" {
				continue
			}
			if _, exists := domainSet[strings.ToLower(reference.Domain)]; exists {
				continue
			}
			result = append(result, lsp.Problem{
				Range: valueNodeTextRange(reference.Node, reference.Domain),
				Message: fmt.Sprintf(
					"Translation domain '%s' not found",
					reference.Domain,
				),
				Source:   "symfony",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       missingTranslationDomainCode,
				Payload: map[string]any{
					"domain":      reference.Domain,
					"suggestions": suggestion.Similar(reference.Domain, domains),
				},
			})
		case translation.ReferenceKey:
			if !staticTranslationKey(extension, reference) {
				continue
			}
			if _, exists := domainSet[strings.ToLower(reference.Domain)]; !exists {
				continue
			}
			found, lookupErr := p.translationExists(
				reference.Domain,
				reference.Key,
			)
			if lookupErr != nil {
				return nil, lookupErr
			}
			if found {
				continue
			}
			keys, keyErr := p.index.GetKeys(reference.Domain)
			if keyErr != nil {
				return nil, fmt.Errorf(
					"query translation keys for domain %q: %w",
					reference.Domain,
					keyErr,
				)
			}
			data := map[string]any{
				"domain":      reference.Domain,
				"key":         reference.Key,
				"suggestions": suggestion.Similar(reference.Key, keys),
			}
			result = append(result, lsp.Problem{
				Range: valueNodeTextRange(reference.Node, reference.Key),
				Message: fmt.Sprintf(
					"Translation key '%s' not found in domain '%s'",
					reference.Key,
					reference.Domain,
				),
				Source:   "symfony",
				Severity: protocol.DiagnosticSeverityWarning,
				ID:       missingTranslationKeyCode,
				Payload:  data,
			})
		}
	}
	if extension == ".php" && p.phpIndex != nil {
		for _, literal := range phpquery.Nodes(
			document.SyntaxTree.Root,
			phpsyntax.PhpString,
		) {
			if ctx.Err() != nil {
				return nil, nil
			}
			_, tags := php.AssistantArgumentTags(
				validationContext,
				literal,
				"TranslationDomain",
				"TranslationKey",
			)
			for _, tag := range tags {
				value := phpquery.StringValue(literal)
				if value == "" {
					continue
				}
				rng := literal.RangeTrimmedTrivia()
				identity := fmt.Sprintf(
					"assistant:%s:%d:%d",
					tag,
					rng.Start,
					rng.End,
				)
				if _, exists := seen[identity]; exists {
					continue
				}
				seen[identity] = struct{}{}
				switch tag {
				case "TranslationDomain":
					if _, exists := domainSet[strings.ToLower(value)]; exists {
						continue
					}
					result = append(result, lsp.Problem{
						Range: valueNodeTextRange(literal, value),
						Message: fmt.Sprintf(
							"Translation domain '%s' not found",
							value,
						),
						Source:   "symfony",
						Severity: protocol.DiagnosticSeverityWarning,
						ID:       missingTranslationDomainCode,
						Payload: map[string]any{
							"domain":      value,
							"suggestions": suggestion.Similar(value, domains),
						},
					})
				case "TranslationKey":
					domain := "messages"
					if sibling, found :=
						php.AssistantSiblingStringArgument(
							validationContext,
							literal,
							"TranslationDomain",
						); found {
						domain = sibling
					}
					if _, exists := domainSet[strings.ToLower(domain)]; !exists {
						continue
					}
					found, lookupErr := p.translationExists(domain, value)
					if lookupErr != nil {
						return nil, lookupErr
					}
					if found {
						continue
					}
					keys, keyErr := p.index.GetKeys(domain)
					if keyErr != nil {
						return nil, fmt.Errorf(
							"query translation keys for domain %q: %w",
							domain,
							keyErr,
						)
					}
					result = append(result, lsp.Problem{
						Range: valueNodeTextRange(literal, value),
						Message: fmt.Sprintf(
							"Translation key '%s' not found in domain '%s'",
							value,
							domain,
						),
						Source:   "symfony",
						Severity: protocol.DiagnosticSeverityWarning,
						ID:       missingTranslationKeyCode,
						Payload: map[string]any{
							"domain":      domain,
							"key":         value,
							"suggestions": suggestion.Similar(value, keys),
						},
					})
				}
			}
		}
	}
	return result, nil
}

func (p *TranslationAnalyzer) translationExists(
	domain,
	key string,
) (bool, error) {
	found, err := p.index.HasMessage(domain, key)
	if err != nil || found {
		return found, err
	}
	if p.snippetIndex == nil || !strings.EqualFold(domain, "messages") {
		return false, nil
	}
	snippets, err := p.snippetIndex.GetFrontendSnippet(key)
	return len(snippets) != 0, err
}

func staticTranslationKey(
	extension string,
	reference translation.Reference,
) bool {
	if reference.Key == "" || strings.Contains(reference.Key, "$") {
		return false
	}
	if extension == ".twig" {
		return twigquery.StringIsStatic(reference.Node)
	}
	return true
}
