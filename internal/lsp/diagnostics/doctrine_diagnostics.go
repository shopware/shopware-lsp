package diagnostics

import (
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	shopwaredal "github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/suggestion"
)

const (
	missingDoctrineTableCode     lsp.DiagnosticID = "symfony.doctrine.table.missing"
	missingDoctrineColumnCode    lsp.DiagnosticID = "symfony.doctrine.column.missing"
	missingDoctrineTypeClassCode lsp.DiagnosticID = "symfony.doctrine.type_class.missing"
	invalidDoctrineTypeClassCode lsp.DiagnosticID = "symfony.doctrine.type_class.invalid"
)

type DoctrineAnalyzer struct {
	index    *doctrine.Index
	phpIndex *php.PHPIndex
	dalIndex *shopwaredal.Index
}

func NewDoctrineAnalyzer(
	index *doctrine.Index,
	phpIndex *php.PHPIndex,
	dalIndex *shopwaredal.Index,
) *DoctrineAnalyzer {
	return &DoctrineAnalyzer{
		index:    index,
		phpIndex: phpIndex,
		dalIndex: dalIndex,
	}
}

func (p *DoctrineAnalyzer) typeRegistrationDiagnostics(
	document *lsp.TextDocument,
	path string,
) []lsp.Problem {
	registrations := doctrine.TypeRegistrationsInDocument(
		path,
		document.SyntaxTree.Root,
	)
	if len(registrations) == 0 {
		return nil
	}
	snapshot := p.phpIndex.SemanticSnapshot()
	var typeClasses []string
	for _, symbol := range p.phpIndex.ClassSymbolsView() {
		if strings.EqualFold(
			symbol.FullyQualified,
			"Doctrine\\DBAL\\Types\\Type",
		) || !snapshot.IsSubtypeOf(
			symbol.FullyQualified,
			"Doctrine\\DBAL\\Types\\Type",
		) {
			continue
		}
		typeClasses = append(typeClasses, symbol.FullyQualified)
	}
	var result []lsp.Problem
	for _, registration := range registrations {
		symbol, found := p.phpIndex.FindClass(registration.Class)
		if !found {
			result = append(result, doctrineDiagnostic(
				document,
				registration.ClassRange,
				missingDoctrineTypeClassCode,
				fmt.Sprintf(
					"Doctrine DBAL type class '%s' not found",
					registration.Class,
				),
				suggestion.Similar(registration.Class, typeClasses),
			))
			continue
		}
		if strings.EqualFold(
			symbol.FullyQualified,
			"Doctrine\\DBAL\\Types\\Type",
		) || !snapshot.IsSubtypeOf(
			symbol.FullyQualified,
			"Doctrine\\DBAL\\Types\\Type",
		) {
			result = append(result, doctrineDiagnostic(
				document,
				registration.ClassRange,
				invalidDoctrineTypeClassCode,
				fmt.Sprintf(
					"Class '%s' is not a Doctrine DBAL type",
					registration.Class,
				),
				nil,
			))
		}
	}
	return result
}

func doctrineDiagnostic(
	_ *lsp.TextDocument,
	rng cst.TextRange,
	code lsp.DiagnosticID,
	message string,
	suggestions []string,
) lsp.Problem {
	return lsp.Problem{
		Range:    rng,
		Message:  message,
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "symfony",
		ID:       code,
		Payload: map[string]any{
			"suggestions": suggestions,
		},
	}
}
