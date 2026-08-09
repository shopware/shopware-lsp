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
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type translationDiagnosticsRun struct {
	provider          *TranslationAnalyzer
	ctx               context.Context
	document          *lsp.TextDocument
	extension         string
	domains           []string
	domainSet         map[string]struct{}
	validationContext context.Context
	result            []lsp.Problem
	seen              map[string]struct{}
}

func newTranslationDiagnosticsRun(
	ctx context.Context,
	document *lsp.TextDocument,
	provider *TranslationAnalyzer,
) (*translationDiagnosticsRun, error) {
	if provider == nil || provider.index == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	extension := strings.ToLower(filepath.Ext(document.URI))
	if extension != ".php" && extension != ".twig" {
		return nil, nil
	}
	domains, err := provider.index.GetDomains()
	if err != nil {
		return nil, fmt.Errorf("query translation domains: %w", err)
	}
	if len(domains) == 0 {
		return nil, nil
	}
	run := &translationDiagnosticsRun{
		provider:          provider,
		ctx:               ctx,
		document:          document,
		extension:         extension,
		domains:           domains,
		domainSet:         make(map[string]struct{}, len(domains)),
		validationContext: ctx,
		seen:              make(map[string]struct{}),
	}
	for _, domain := range domains {
		run.domainSet[strings.ToLower(domain)] = struct{}{}
	}
	if extension == ".php" && provider.phpIndex != nil {
		path, _ := uriutil.Path(document.URI)
		run.validationContext = provider.phpIndex.AddDocumentContext(
			ctx,
			path,
			document.Version,
			document.SyntaxTree.Root,
			document.SyntaxTree.Root,
		)
	}
	return run, nil
}

func (r *translationDiagnosticsRun) analyze() ([]lsp.Problem, error) {
	if err := r.analyzeReferences(); err != nil {
		return nil, err
	}
	if r.ctx.Err() != nil {
		return nil, nil
	}
	if r.extension == ".php" && r.provider.phpIndex != nil {
		if err := r.analyzeAssistantArguments(); err != nil {
			return nil, err
		}
	}
	if r.ctx.Err() != nil {
		return nil, nil
	}
	return r.result, nil
}

func (r *translationDiagnosticsRun) analyzeReferences() error {
	for _, reference := range translation.References(
		r.document.URI,
		r.document.SyntaxTree.Root,
		r.document.Text,
	) {
		if r.ctx.Err() != nil {
			return nil
		}
		if !r.validReference(reference) || !r.markReferenceSeen(reference) {
			continue
		}
		switch reference.Role {
		case translation.ReferenceDomain:
			r.addMissingDomain(reference.Node, reference.Domain)
		case translation.ReferenceKey:
			if err := r.addMissingKey(reference.Node, reference.Domain, reference.Key); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *translationDiagnosticsRun) validReference(reference translation.Reference) bool {
	if reference.Node == nil {
		return false
	}
	return r.extension != ".php" || translation.ValidatePHPReference(
		r.validationContext,
		reference,
		r.provider.phpIndex,
		r.document.Text,
	)
}

func (r *translationDiagnosticsRun) markReferenceSeen(reference translation.Reference) bool {
	rng := reference.Node.RangeTrimmedTrivia()
	identity := fmt.Sprintf("%d:%d:%d", reference.Role, rng.Start, rng.End)
	if _, exists := r.seen[identity]; exists {
		return false
	}
	r.seen[identity] = struct{}{}
	return true
}

func (r *translationDiagnosticsRun) addMissingDomain(
	node *phpsyntax.Node,
	domain string,
) {
	if domain == "" || r.hasDomain(domain) {
		return
	}
	r.result = append(r.result, lsp.Problem{
		Range:    valueNodeTextRange(node, domain),
		Message:  fmt.Sprintf("Translation domain '%s' not found", domain),
		Source:   "symfony",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       missingTranslationDomainCode,
		Payload: map[string]any{
			"domain":      domain,
			"suggestions": suggestion.Similar(domain, r.domains),
		},
	})
}

func (r *translationDiagnosticsRun) addMissingKey(
	node *phpsyntax.Node,
	domain,
	key string,
) error {
	if !staticTranslationKey(r.extension, translation.Reference{Node: node, Key: key}) ||
		!r.hasDomain(domain) {
		return nil
	}
	found, err := r.provider.translationExists(domain, key)
	if err != nil || found {
		return err
	}
	keys, err := r.provider.index.GetKeys(domain)
	if err != nil {
		return fmt.Errorf("query translation keys for domain %q: %w", domain, err)
	}
	r.result = append(r.result, lsp.Problem{
		Range:    valueNodeTextRange(node, key),
		Message:  fmt.Sprintf("Translation key '%s' not found in domain '%s'", key, domain),
		Source:   "symfony",
		Severity: protocol.DiagnosticSeverityWarning,
		ID:       missingTranslationKeyCode,
		Payload: map[string]any{
			"domain":      domain,
			"key":         key,
			"suggestions": suggestion.Similar(key, keys),
		},
	})
	return nil
}

func (r *translationDiagnosticsRun) hasDomain(domain string) bool {
	_, exists := r.domainSet[strings.ToLower(domain)]
	return exists
}

func (r *translationDiagnosticsRun) analyzeAssistantArguments() error {
	for _, literal := range phpquery.Nodes(
		r.document.SyntaxTree.Root,
		phpsyntax.PhpString,
	) {
		if r.ctx.Err() != nil {
			return nil
		}
		if err := r.analyzeAssistantLiteral(literal); err != nil {
			return err
		}
	}
	return nil
}

func (r *translationDiagnosticsRun) analyzeAssistantLiteral(literal *phpsyntax.Node) error {
	_, tags := php.AssistantArgumentTags(
		r.validationContext,
		literal,
		"TranslationDomain",
		"TranslationKey",
	)
	value := phpquery.StringValue(literal)
	if value == "" {
		return nil
	}
	for _, tag := range tags {
		if !r.markAssistantSeen(literal, tag) {
			continue
		}
		switch tag {
		case "TranslationDomain":
			r.addMissingDomain(literal, value)
		case "TranslationKey":
			if err := r.addMissingKey(literal, r.assistantDomain(literal), value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *translationDiagnosticsRun) markAssistantSeen(
	literal *phpsyntax.Node,
	tag string,
) bool {
	rng := literal.RangeTrimmedTrivia()
	identity := fmt.Sprintf("assistant:%s:%d:%d", tag, rng.Start, rng.End)
	if _, exists := r.seen[identity]; exists {
		return false
	}
	r.seen[identity] = struct{}{}
	return true
}

func (r *translationDiagnosticsRun) assistantDomain(literal *phpsyntax.Node) string {
	if sibling, found := php.AssistantSiblingStringArgument(
		r.validationContext,
		literal,
		"TranslationDomain",
	); found {
		return sibling
	}
	return "messages"
}
