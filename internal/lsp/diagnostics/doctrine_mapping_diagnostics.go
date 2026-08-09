package diagnostics

import (
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/suggestion"
)

type doctrineMappingDiagnosticsRun struct {
	analyzer         *DoctrineAnalyzer
	document         *lsp.TextDocument
	classNames       []string
	customTypes      []doctrine.TypeDeclaration
	typeNames        []string
	constraintFields map[string][]doctrine.Field
}

func (r doctrineMappingDiagnosticsRun) problem(
	reference doctrine.MappingReference,
) *lsp.Problem {
	switch reference.Role {
	case doctrine.MappingDiscriminatorClass:
		return r.discriminatorProblem(reference)
	case doctrine.MappingModelClass,
		doctrine.MappingRepositoryClass,
		doctrine.MappingTargetClass,
		doctrine.MappingEmbeddedClass,
		doctrine.MappingEnumClass:
		return r.classProblem(reference)
	case doctrine.MappingType:
		return r.typeProblem(reference)
	case doctrine.MappingProperty:
		return r.propertyProblem(reference)
	case doctrine.MappingConstraintField, doctrine.MappingConstraintColumn:
		return r.constraintProblem(reference)
	case doctrine.MappingLifecycleMethod:
		return r.lifecycleProblem(reference)
	default:
		return nil
	}
}

func (r doctrineMappingDiagnosticsRun) discriminatorProblem(
	reference doctrine.MappingReference,
) *lsp.Problem {
	candidates := doctrine.DiscriminatorClasses(r.analyzer.phpIndex, reference.Owner)
	if doctrine.IsDiscriminatorClass(
		r.analyzer.phpIndex,
		reference.Owner,
		reference.Name,
	) {
		return nil
	}
	if _, found := r.analyzer.phpIndex.FindClass(reference.Name); !found {
		problem := doctrineDiagnostic(
			r.document,
			reference.Range,
			missingDoctrineMappingClass,
			fmt.Sprintf("Doctrine discriminator class '%s' not found", reference.Name),
			suggestion.Similar(reference.Name, candidates),
		)
		return &problem
	}
	problem := doctrineDiagnostic(
		r.document,
		reference.Range,
		invalidDoctrineDiscriminatorClassCode,
		fmt.Sprintf(
			"Doctrine discriminator class '%s' must extend '%s'",
			reference.Name,
			reference.Owner,
		),
		candidates,
	)
	return &problem
}

func (r doctrineMappingDiagnosticsRun) classProblem(
	reference doctrine.MappingReference,
) *lsp.Problem {
	if _, found := r.analyzer.phpIndex.FindClass(reference.Name); found {
		return nil
	}
	problem := doctrineDiagnostic(
		r.document,
		reference.Range,
		missingDoctrineMappingClass,
		fmt.Sprintf("Doctrine mapping class '%s' not found", reference.Name),
		suggestion.Similar(reference.Name, r.classNames),
	)
	return &problem
}

func (r doctrineMappingDiagnosticsRun) typeProblem(
	reference doctrine.MappingReference,
) *lsp.Problem {
	if doctrine.IsKnownType(reference.Name, r.customTypes) {
		return nil
	}
	problem := doctrineDiagnostic(
		r.document,
		reference.Range,
		unknownDoctrineTypeCode,
		fmt.Sprintf("Doctrine mapping type '%s' is not registered", reference.Name),
		suggestion.Similar(reference.Name, r.typeNames),
	)
	return &problem
}

func (r doctrineMappingDiagnosticsRun) propertyProblem(
	reference doctrine.MappingReference,
) *lsp.Problem {
	if _, found := r.analyzer.phpIndex.FindClass(reference.Owner); !found ||
		len(r.analyzer.phpIndex.FindProperties(reference.Owner, reference.Name)) != 0 {
		return nil
	}
	properties := r.analyzer.phpIndex.Properties(reference.Owner)
	names := make([]string, 0, len(properties))
	for _, property := range properties {
		names = append(names, property.Name)
	}
	problem := doctrineDiagnostic(
		r.document,
		reference.Range,
		missingDoctrinePropertyCode,
		fmt.Sprintf(
			"Doctrine field '%s' has no PHP property on '%s'",
			reference.Name,
			reference.Owner,
		),
		suggestion.Similar(reference.Name, names),
	)
	return &problem
}

func (r doctrineMappingDiagnosticsRun) constraintProblem(
	reference doctrine.MappingReference,
) *lsp.Problem {
	fields := r.constraintFields[strings.ToLower(reference.Owner)]
	if indexed, err := r.analyzer.index.Fields(reference.Owner); err == nil {
		fields = append(fields, indexed...)
	}
	names, found := doctrineConstraintNames(fields, reference)
	if found {
		return nil
	}
	kind := "field"
	code := missingDoctrineConstraintFieldCode
	if reference.Role == doctrine.MappingConstraintColumn {
		kind = "column"
		code = missingDoctrineConstraintColumnCode
	}
	problem := doctrineDiagnostic(
		r.document,
		reference.Range,
		code,
		fmt.Sprintf(
			"Doctrine table constraint references unknown %s '%s' on '%s'",
			kind,
			reference.Name,
			reference.Owner,
		),
		suggestion.Similar(reference.Name, names),
	)
	return &problem
}

func doctrineConstraintNames(
	fields []doctrine.Field,
	reference doctrine.MappingReference,
) ([]string, bool) {
	var names []string
	found := false
	for _, field := range fields {
		name := field.Name
		if reference.Role == doctrine.MappingConstraintColumn {
			name = field.Column
			if name == "" {
				name = field.Name
			}
		}
		if name == "" {
			continue
		}
		names = append(names, name)
		found = found || strings.EqualFold(name, reference.Name)
	}
	return names, found
}

func (r doctrineMappingDiagnosticsRun) lifecycleProblem(
	reference doctrine.MappingReference,
) *lsp.Problem {
	if _, found := r.analyzer.phpIndex.FindClass(reference.Owner); !found ||
		len(r.analyzer.phpIndex.FindMethods(reference.Owner, reference.Name)) != 0 {
		return nil
	}
	methods := r.analyzer.phpIndex.Methods(reference.Owner)
	names := make([]string, 0, len(methods))
	for _, method := range methods {
		names = append(names, method.Name)
	}
	problem := doctrineDiagnostic(
		r.document,
		reference.Range,
		missingDoctrineCallbackCode,
		fmt.Sprintf(
			"Doctrine lifecycle callback '%s::%s' not found",
			reference.Owner,
			reference.Name,
		),
		suggestion.Similar(reference.Name, names),
	)
	return &problem
}
