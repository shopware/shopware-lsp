package diagnostics

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/shopware/shopware-lsp/internal/validation"
)

const missingConstraintOptionCode lsp.DiagnosticID = "symfony.validation.option.missing"

type ValidationAnalyzer struct {
	phpIndex *php.PHPIndex
}

func NewValidationAnalyzer(
	phpIndex *php.PHPIndex,
) *ValidationAnalyzer {
	return &ValidationAnalyzer{phpIndex: phpIndex}
}

func (p *ValidationAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.phpIndex == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		!strings.HasSuffix(strings.ToLower(document.URI), ".php") {
		return nil, nil
	}
	path, _ := uriutil.Path(document.URI)
	validationContext := p.phpIndex.AddDocumentContext(
		ctx,
		path,
		document.Version,
		document.SyntaxTree.Root,
		document.SyntaxTree.Root,
	)
	var result []lsp.Problem
	for _, literal := range phpquery.Nodes(
		document.SyntaxTree.Root,
		phpsyntax.PhpString,
	) {
		reference, found := validation.OptionReferenceAt(
			validationContext,
			document.SyntaxTree.Root,
			literal,
		)
		if !found || reference.Name == "" {
			continue
		}
		properties := validation.ConstraintPropertiesInContext(
			validationContext,
			reference.Constraint,
		)
		if _, exists := validation.FindConstraintProperty(
			properties,
			reference.Name,
		); exists {
			continue
		}
		names := make([]string, 0, len(properties))
		for _, property := range properties {
			names = append(names, property.Name)
		}
		result = append(result, lsp.Problem{
			Range: valueNodeTextRange(reference.Node, reference.Name),
			Message: fmt.Sprintf(
				"Option '%s' is not defined by constraint '%s'",
				reference.Name,
				reference.Constraint,
			),
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "symfony",
			ID:       missingConstraintOptionCode,
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
