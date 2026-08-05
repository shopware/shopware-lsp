package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/security"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const missingSecurityAttributeCode lsp.DiagnosticID = "symfony.security.attribute.missing"
const missingSecurityProviderCode lsp.DiagnosticID = "symfony.security.provider.missing"

type SecurityAnalyzer struct {
	index    *security.Index
	phpIndex *php.PHPIndex
}

func NewSecurityAnalyzer(
	index *security.Index,
	phpIndex *php.PHPIndex,
) *SecurityAnalyzer {
	return &SecurityAnalyzer{
		index:    index,
		phpIndex: phpIndex,
	}
}

func (p *SecurityAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	extension := strings.ToLower(filepath.Ext(document.URI))
	if extension == ".xml" {
		return p.configDiagnostics(ctx, document)
	}
	if extension != ".php" && extension != ".twig" &&
		extension != ".yaml" && extension != ".yml" {
		return nil, nil
	}
	var configResult []lsp.Problem
	if extension == ".php" || extension == ".yaml" ||
		extension == ".yml" {
		var err error
		configResult, err = p.configDiagnostics(ctx, document)
		if err != nil {
			return nil, err
		}
		if extension == ".yaml" || extension == ".yml" {
			return configResult, nil
		}
	}
	path, _ := uriutil.Path(document.URI)
	validationContext := ctx
	if extension == ".php" && p.phpIndex != nil {
		validationContext = p.phpIndex.AddDocumentContext(
			ctx,
			path,
			document.Version,
			document.SyntaxTree.Root,
			document.SyntaxTree.Root,
		)
	}
	references := security.ReferencesInDocument(
		validationContext,
		path,
		document.SyntaxTree.Root,
		document.Source,
	)
	if len(references) == 0 {
		return configResult, nil
	}
	names, err := p.index.Names()
	if err != nil {
		return nil, err
	}
	projectNames, err := p.index.ProjectNames()
	if err != nil {
		return nil, err
	}
	hasProjectDeclarations := len(projectNames) != 0
	for _, occurrence := range security.OccurrencesInDocument(
		path,
		document.SyntaxTree.Root,
		document.Source,
	) {
		if occurrence.Role != security.DeclarationOccurrence {
			continue
		}
		hasProjectDeclarations = true
		if !containsSecurityName(names, occurrence.Name) {
			names = append(names, occurrence.Name)
		}
	}
	if !hasProjectDeclarations {
		return configResult, nil
	}

	result := configResult
	for _, reference := range references {
		if ctx.Err() != nil {
			return nil, nil
		}
		if reference.Name == "" ||
			containsSecurityName(names, reference.Name) {
			continue
		}
		result = append(result, lsp.Problem{
			Range: reference.Range,
			Message: fmt.Sprintf(
				"Symfony security attribute '%s' not found",
				reference.Name,
			),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "symfony",
			ID:       missingSecurityAttributeCode,
			Payload: map[string]any{
				"suggestions": suggestion.Similar(
					reference.Name,
					names,
				),
			},
		})
	}
	return result, nil
}

func (p *SecurityAnalyzer) configDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	path, _ := uriutil.Path(document.URI)
	occurrences := security.ConfigOccurrencesInDocument(
		path,
		document.SyntaxTree.Root,
	)
	if len(occurrences) == 0 {
		return nil, nil
	}
	symbols, err := p.index.ConfigSymbols()
	if err != nil {
		return nil, err
	}
	var providers []string
	for _, symbol := range symbols {
		if symbol.Kind != security.ConfigProvider {
			continue
		}
		for _, declaration := range symbol.Declarations() {
			if declaration.File != path {
				providers = appendSecurityName(providers, symbol.Name)
				break
			}
		}
	}
	for _, occurrence := range occurrences {
		if occurrence.Kind == security.ConfigProvider &&
			occurrence.Role == security.ConfigDeclaration {
			providers = appendSecurityName(providers, occurrence.Name)
		}
	}

	var result []lsp.Problem
	for _, occurrence := range occurrences {
		if ctx.Err() != nil {
			return nil, nil
		}
		if occurrence.Kind != security.ConfigProvider ||
			occurrence.Role != security.ConfigReference ||
			occurrence.Name == "" ||
			containsSecurityName(providers, occurrence.Name) {
			continue
		}
		result = append(result, lsp.Problem{
			Range: occurrence.Range,
			Message: fmt.Sprintf(
				"Symfony user provider '%s' not found",
				occurrence.Name,
			),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "symfony",
			ID:       missingSecurityProviderCode,
			Payload: map[string]any{
				"suggestions": suggestion.Similar(
					occurrence.Name,
					providers,
				),
			},
		})
	}
	return result, nil
}

func containsSecurityName(names []string, name string) bool {
	for _, candidate := range names {
		if strings.EqualFold(candidate, name) {
			return true
		}
	}
	return false
}

func appendSecurityName(names []string, name string) []string {
	if name == "" || containsSecurityName(names, name) {
		return names
	}
	return append(names, name)
}
