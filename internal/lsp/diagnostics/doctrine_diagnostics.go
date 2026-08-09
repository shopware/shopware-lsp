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
	missingDoctrineEntityCode             lsp.DiagnosticID = "symfony.doctrine.entity.missing"
	missingDoctrineFieldCode              lsp.DiagnosticID = "symfony.doctrine.field.missing"
	missingDoctrineTableCode              lsp.DiagnosticID = "symfony.doctrine.table.missing"
	missingDoctrineColumnCode             lsp.DiagnosticID = "symfony.doctrine.column.missing"
	missingDoctrineMagicFieldCode         lsp.DiagnosticID = "symfony.doctrine.magic_field.missing"
	missingDoctrineMappingClass           lsp.DiagnosticID = "symfony.doctrine.mapping_class.missing"
	missingDoctrinePropertyCode           lsp.DiagnosticID = "symfony.doctrine.mapping_property.missing"
	missingDoctrineConstraintFieldCode    lsp.DiagnosticID = "symfony.doctrine.constraint_field.missing"
	missingDoctrineConstraintColumnCode   lsp.DiagnosticID = "symfony.doctrine.constraint_column.missing"
	missingDoctrineCallbackCode           lsp.DiagnosticID = "symfony.doctrine.lifecycle_method.missing"
	unknownDoctrineTypeCode               lsp.DiagnosticID = "symfony.doctrine.type.unknown"
	missingDoctrineTypeClassCode          lsp.DiagnosticID = "symfony.doctrine.type_class.missing"
	invalidDoctrineTypeClassCode          lsp.DiagnosticID = "symfony.doctrine.type_class.invalid"
	invalidDoctrineDiscriminatorClassCode lsp.DiagnosticID = "symfony.doctrine.discriminator_class.invalid"
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

func (p *DoctrineAnalyzer) mappingDiagnostics(
	document *lsp.TextDocument,
	path string,
) ([]lsp.Problem, error) {
	references := doctrine.MappingReferencesInDocument(
		path,
		document.SyntaxTree.Root,
		document.Source,
	)
	if len(references) == 0 {
		return nil, nil
	}
	customTypes := doctrine.TypeDeclarationsForMapping(
		path,
		p.index.TypeDeclarations(p.phpIndex),
	)
	typeNames := doctrine.BuiltInTypes()
	for _, declaration := range customTypes {
		typeNames = append(typeNames, declaration.Name)
	}
	constraintFields := make(map[string][]doctrine.Field)
	for _, model := range doctrine.ModelsInDocument(
		path,
		document.SyntaxTree.Root,
		document.Source,
	) {
		key := strings.ToLower(model.Class)
		constraintFields[key] = append(
			constraintFields[key],
			model.Fields...,
		)
	}
	run := doctrineMappingDiagnosticsRun{
		analyzer:         p,
		document:         document,
		classNames:       p.phpIndex.ClassNamesView(),
		customTypes:      customTypes,
		typeNames:        typeNames,
		constraintFields: constraintFields,
	}
	var result []lsp.Problem
	for _, reference := range references {
		if reference.Name == "" || reference.Range.Len() == 0 {
			continue
		}
		problem := run.problem(reference)
		if problem != nil {
			result = append(result, *problem)
		}
	}
	return result, nil
}

func hasDoctrineField(fields []doctrine.Field, name string) bool {
	for _, field := range fields {
		if strings.EqualFold(field.Name, name) {
			return true
		}
	}
	return false
}

func dqlEntitySuggestions(
	source string,
	rng cst.TextRange,
	candidates []string,
) []string {
	if int(rng.Start) > len(source) {
		return candidates
	}
	doubleQuoted := false
	for position := int(rng.Start) - 1; position >= 0; position-- {
		switch source[position] {
		case '"':
			doubleQuoted = true
			position = -1
		case '\'', '\n', '\r':
			position = -1
		}
	}
	if !doubleQuoted {
		return candidates
	}
	result := make([]string, len(candidates))
	for position, candidate := range candidates {
		result[position] = strings.ReplaceAll(candidate, `\`, `\\`)
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
