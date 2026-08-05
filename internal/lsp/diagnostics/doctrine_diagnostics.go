package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	shopwaredal "github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/uriutil"
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

func (p *DoctrineAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || p.phpIndex == nil ||
		document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil {
		return nil, nil
	}
	path, _ := uriutil.Path(document.URI)
	result := p.typeRegistrationDiagnostics(document, path)
	mappingResult, err := p.mappingDiagnostics(document, path)
	if err != nil {
		return nil, err
	}
	result = append(result, mappingResult...)
	if strings.ToLower(filepath.Ext(path)) != ".php" {
		return result, nil
	}
	validationContext := p.phpIndex.AddDocumentContext(
		ctx,
		path,
		document.Version,
		document.SyntaxTree.Root,
		document.SyntaxTree.Root,
	)
	entityNames, err := p.index.EntityNames()
	if err != nil {
		return nil, err
	}
	models, err := p.index.Models()
	if err != nil {
		return nil, err
	}
	dbalSchema := newDBALSchemaCatalog(
		p.index,
		p.dalIndex,
		models,
	)
	seen := make(map[string]struct{})
	for _, literal := range phpquery.Nodes(
		document.SyntaxTree.Root,
		phpsyntax.PhpString,
	) {
		if _, tags := php.AssistantArgumentTags(
			validationContext,
			literal,
			"Entity",
		); len(tags) != 0 {
			name := phpquery.StringValue(literal)
			if name == "" {
				continue
			}
			if _, exists, modelErr := p.index.Model(name); modelErr != nil {
				return nil, modelErr
			} else if !exists {
				rng := phpquery.StringContentRange(literal)
				key := fmt.Sprintf(
					"assistant-entity:%d:%d",
					rng.Start,
					rng.End,
				)
				if _, duplicate := seen[key]; !duplicate {
					seen[key] = struct{}{}
					result = append(result, doctrineDiagnostic(
						document,
						rng,
						missingDoctrineEntityCode,
						fmt.Sprintf(
							"Doctrine model '%s' not found",
							name,
						),
						suggestion.Similar(name, entityNames),
					))
				}
			}
			continue
		}
		if reference, found := p.index.DBALReferenceAt(
			validationContext,
			document.SyntaxTree.Root,
			literal,
		); found {
			switch reference.Role {
			case doctrine.DBALTableReference:
				tableExists, tableErr := dbalSchema.HasTable(reference.Name)
				if tableErr != nil {
					return nil, tableErr
				}
				if !tableExists {
					tableNames, namesErr := dbalSchema.TableNames()
					if namesErr != nil {
						return nil, namesErr
					}
					suggestions := adminNearbySuggestions(
						reference.Name,
						tableNames,
					)
					if len(suggestions) == 0 {
						continue
					}
					result = append(result, doctrineDiagnostic(
						document,
						reference.Range,
						missingDoctrineTableCode,
						fmt.Sprintf(
							"Doctrine DBAL table '%s' not found",
							reference.Name,
						),
						suggestions,
					))
				}
			case doctrine.DBALColumnReference:
				columns, tableExists, columnErr := dbalSchema.Columns(
					reference.Table,
				)
				if columnErr != nil {
					return nil, columnErr
				}
				if tableExists && !hasDBALSchemaName(columns, reference.Name) {
					suggestions := adminNearbySuggestions(
						reference.Name,
						columns,
					)
					if len(suggestions) == 0 {
						continue
					}
					result = append(result, doctrineDiagnostic(
						document,
						reference.Range,
						missingDoctrineColumnCode,
						fmt.Sprintf(
							"Doctrine DBAL column '%s' not found on table '%s'",
							reference.Name,
							reference.Table,
						),
						suggestions,
					))
				}
			}
			continue
		}
		reference, found := p.index.ReferenceAt(
			validationContext,
			document.SyntaxTree.Root,
			literal,
		)
		if !found || reference.Name == "" {
			continue
		}
		rng := doctrine.ReferenceRange(reference)
		key := fmt.Sprintf(
			"%d:%d:%d",
			reference.Role,
			rng.Start,
			rng.End,
		)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		switch reference.Role {
		case doctrine.EntityReference:
			if _, exists, modelErr := p.index.Model(
				reference.Name,
			); modelErr != nil {
				return nil, modelErr
			} else if exists {
				continue
			}
			result = append(result, doctrineDiagnostic(
				document,
				rng,
				missingDoctrineEntityCode,
				fmt.Sprintf(
					"Doctrine model '%s' not found",
					reference.Name,
				),
				suggestion.Similar(reference.Name, entityNames),
			))
		case doctrine.FieldReference:
			fields, fieldErr := p.index.Fields(reference.Entity)
			if fieldErr != nil {
				return nil, fieldErr
			}
			if len(fields) == 0 || hasDoctrineField(fields, reference.Name) {
				continue
			}
			names := make([]string, 0, len(fields))
			for _, field := range fields {
				names = append(names, field.Name)
			}
			result = append(result, doctrineDiagnostic(
				document,
				rng,
				missingDoctrineFieldCode,
				fmt.Sprintf(
					"Doctrine field '%s' is not mapped on '%s'",
					reference.Name,
					reference.Entity,
				),
				suggestion.Similar(reference.Name, names),
			))
		}
	}
	for _, reference := range doctrine.ValidatedDQLReferencesInDocument(
		p.index,
		validationContext,
		document.SyntaxTree.Root,
		path,
	) {
		key := fmt.Sprintf(
			"dql:%d:%d:%d",
			reference.Role,
			reference.Range.Start,
			reference.Range.End,
		)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		switch reference.Role {
		case doctrine.DQLEntityReference:
			if _, exists, modelErr := p.index.Model(
				reference.Entity,
			); modelErr != nil {
				return nil, modelErr
			} else if exists {
				continue
			}
			candidates := suggestion.Similar(reference.Entity, entityNames)
			result = append(result, doctrineDiagnostic(
				document,
				reference.Range,
				missingDoctrineEntityCode,
				fmt.Sprintf(
					"Doctrine model '%s' not found in DQL",
					reference.Entity,
				),
				dqlEntitySuggestions(
					document.Source,
					reference.Range,
					candidates,
				),
			))
		case doctrine.DQLFieldReference:
			fields, fieldErr := p.index.Fields(reference.Entity)
			if fieldErr != nil {
				return nil, fieldErr
			}
			if len(fields) == 0 ||
				hasDoctrineField(fields, reference.Field) {
				continue
			}
			names := make([]string, 0, len(fields))
			for _, field := range fields {
				names = append(names, field.Name)
			}
			result = append(result, doctrineDiagnostic(
				document,
				reference.Range,
				missingDoctrineFieldCode,
				fmt.Sprintf(
					"Doctrine field '%s' is not mapped on '%s' in DQL",
					reference.Field,
					reference.Entity,
				),
				suggestion.Similar(reference.Field, names),
			))
		}
	}
	for _, call := range phpquery.Calls(document.SyntaxTree.Root) {
		magic, found := p.index.MagicMethodAt(
			validationContext,
			document.SyntaxTree.Root,
			call,
		)
		if !found || len(magic.Unknown) == 0 {
			continue
		}
		completions := p.index.MagicMethodCompletionsAt(
			validationContext,
			document.SyntaxTree.Root,
			call,
		)
		names := make([]string, 0, len(completions))
		for _, completion := range completions {
			names = append(names, completion.Name)
		}
		result = append(result, doctrineDiagnostic(
			document,
			magic.NameRange,
			missingDoctrineMagicFieldCode,
			fmt.Sprintf(
				"Doctrine magic method '%s' references unmapped field criteria: %s",
				magic.Name,
				strings.Join(magic.Unknown, ", "),
			),
			suggestion.Similar(magic.Name, names),
		))
	}
	return result, nil
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
	classNames := p.phpIndex.ClassNamesView()
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
	var result []lsp.Problem
	for _, reference := range references {
		if reference.Name == "" || reference.Range.Len() == 0 {
			continue
		}
		switch reference.Role {
		case doctrine.MappingDiscriminatorClass:
			candidates := doctrine.DiscriminatorClasses(
				p.phpIndex,
				reference.Owner,
			)
			if doctrine.IsDiscriminatorClass(
				p.phpIndex,
				reference.Owner,
				reference.Name,
			) {
				continue
			}
			if _, found := p.phpIndex.FindClass(reference.Name); !found {
				result = append(result, doctrineDiagnostic(
					document,
					reference.Range,
					missingDoctrineMappingClass,
					fmt.Sprintf(
						"Doctrine discriminator class '%s' not found",
						reference.Name,
					),
					suggestion.Similar(reference.Name, candidates),
				))
				continue
			}
			result = append(result, doctrineDiagnostic(
				document,
				reference.Range,
				invalidDoctrineDiscriminatorClassCode,
				fmt.Sprintf(
					"Doctrine discriminator class '%s' must extend '%s'",
					reference.Name,
					reference.Owner,
				),
				candidates,
			))
		case doctrine.MappingModelClass,
			doctrine.MappingRepositoryClass,
			doctrine.MappingTargetClass,
			doctrine.MappingEmbeddedClass,
			doctrine.MappingEnumClass:
			if _, found := p.phpIndex.FindClass(reference.Name); found {
				continue
			}
			result = append(result, doctrineDiagnostic(
				document,
				reference.Range,
				missingDoctrineMappingClass,
				fmt.Sprintf(
					"Doctrine mapping class '%s' not found",
					reference.Name,
				),
				suggestion.Similar(reference.Name, classNames),
			))
		case doctrine.MappingType:
			if doctrine.IsKnownType(reference.Name, customTypes) {
				continue
			}
			result = append(result, doctrineDiagnostic(
				document,
				reference.Range,
				unknownDoctrineTypeCode,
				fmt.Sprintf(
					"Doctrine mapping type '%s' is not registered",
					reference.Name,
				),
				suggestion.Similar(reference.Name, typeNames),
			))
		case doctrine.MappingProperty:
			if _, found := p.phpIndex.FindClass(reference.Owner); !found {
				continue
			}
			if len(p.phpIndex.FindProperties(
				reference.Owner,
				reference.Name,
			)) != 0 {
				continue
			}
			properties := p.phpIndex.Properties(reference.Owner)
			names := make([]string, 0, len(properties))
			for _, property := range properties {
				names = append(names, property.Name)
			}
			result = append(result, doctrineDiagnostic(
				document,
				reference.Range,
				missingDoctrinePropertyCode,
				fmt.Sprintf(
					"Doctrine field '%s' has no PHP property on '%s'",
					reference.Name,
					reference.Owner,
				),
				suggestion.Similar(reference.Name, names),
			))
		case doctrine.MappingConstraintField,
			doctrine.MappingConstraintColumn:
			key := strings.ToLower(reference.Owner)
			fields := constraintFields[key]
			if indexed, err := p.index.Fields(
				reference.Owner,
			); err == nil {
				fields = append(fields, indexed...)
			}
			var names []string
			found := false
			for _, field := range fields {
				name := field.Name
				if reference.Role ==
					doctrine.MappingConstraintColumn {
					name = field.Column
					if name == "" {
						name = field.Name
					}
				}
				if name == "" {
					continue
				}
				names = append(names, name)
				if strings.EqualFold(name, reference.Name) {
					found = true
				}
			}
			if found {
				continue
			}
			kind := "field"
			code := missingDoctrineConstraintFieldCode
			if reference.Role ==
				doctrine.MappingConstraintColumn {
				kind = "column"
				code = missingDoctrineConstraintColumnCode
			}
			result = append(result, doctrineDiagnostic(
				document,
				reference.Range,
				code,
				fmt.Sprintf(
					"Doctrine table constraint references unknown %s '%s' on '%s'",
					kind,
					reference.Name,
					reference.Owner,
				),
				suggestion.Similar(reference.Name, names),
			))
		case doctrine.MappingLifecycleMethod:
			if _, found := p.phpIndex.FindClass(reference.Owner); !found {
				continue
			}
			if len(p.phpIndex.FindMethods(
				reference.Owner,
				reference.Name,
			)) != 0 {
				continue
			}
			methods := p.phpIndex.Methods(reference.Owner)
			names := make([]string, 0, len(methods))
			for _, method := range methods {
				names = append(names, method.Name)
			}
			result = append(result, doctrineDiagnostic(
				document,
				reference.Range,
				missingDoctrineCallbackCode,
				fmt.Sprintf(
					"Doctrine lifecycle callback '%s::%s' not found",
					reference.Owner,
					reference.Name,
				),
				suggestion.Similar(reference.Name, names),
			))
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
