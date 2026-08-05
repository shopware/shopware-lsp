package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	missingFormTypeCode     lsp.DiagnosticID = "symfony.form.type.missing"
	missingFormOptionCode   lsp.DiagnosticID = "symfony.form.option.missing"
	missingFormFieldCode    lsp.DiagnosticID = "symfony.form.field.missing"
	missingFormViewVarCode  lsp.DiagnosticID = "symfony.form.view_var.missing"
	legacyFormTypeAliasCode lsp.DiagnosticID = "symfony.form.type.legacy_alias"
)

type FormAnalyzer struct {
	index    *form.Index
	phpIndex *php.PHPIndex
}

func NewFormAnalyzer(
	index *form.Index,
	phpIndex *php.PHPIndex,
) *FormAnalyzer {
	return &FormAnalyzer{index: index, phpIndex: phpIndex}
}

func (p *FormAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.index == nil || p.phpIndex == nil ||
		document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil {
		return nil, nil
	}
	switch strings.ToLower(filepath.Ext(document.URI)) {
	case ".twig":
		return p.twigFieldDiagnostics(ctx, document)
	case ".php":
	default:
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
	references := formDocumentReferences(
		validationContext,
		document.SyntaxTree.Root,
	)
	var assistantTypes []*phpsyntax.Node
	for _, literal := range phpquery.Nodes(
		document.SyntaxTree.Root,
		phpsyntax.PhpString,
	) {
		if _, tags := php.AssistantArgumentTags(
			validationContext,
			literal,
			"FormType",
		); len(tags) != 0 {
			assistantTypes = append(assistantTypes, literal)
		}
	}
	if len(references) == 0 && len(assistantTypes) == 0 {
		return nil, nil
	}
	types, err := p.index.GetTypes()
	if err != nil {
		return nil, err
	}
	typeNames := formTypeNames(types)
	localTypes := make(map[string]form.Type)
	for _, current := range form.TypesInDocument(
		path,
		document.SyntaxTree.Root,
	) {
		localTypes[strings.ToLower(current.Class)] = current
	}
	legacyAliasesDeprecated := p.legacyFormAliasesDeprecated()

	var result []lsp.Problem
	for _, literal := range assistantTypes {
		name := phpquery.StringValue(literal)
		if name == "" {
			continue
		}
		_, found, typeErr := p.index.GetType(name)
		if typeErr != nil {
			return nil, typeErr
		}
		if found || len(typeNames) == 0 {
			continue
		}
		result = append(result, formDiagnostic(
			document,
			form.Reference{
				Role: form.ReferenceType,
				Name: name,
				Node: literal,
			},
			missingFormTypeCode,
			fmt.Sprintf("Symfony form type '%s' not found", name),
			suggestion.Similar(name, typeNames),
		))
	}
	for _, reference := range references {
		if ctx.Err() != nil {
			return result, nil
		}
		if legacyAliasesDeprecated &&
			form.IsLegacyBuilderTypeAlias(validationContext, reference) {
			className := ""
			if reference.Name != "" {
				current, found, typeErr := p.index.GetType(reference.Name)
				if typeErr != nil {
					return nil, typeErr
				}
				if found {
					className = current.Class
				}
			}
			result = append(
				result,
				legacyFormTypeAliasDiagnostic(
					document,
					reference,
					className,
				),
			)
		}
		if reference.Name == "" {
			continue
		}
		switch reference.Role {
		case form.ReferenceType:
			_, found, typeErr := p.index.GetType(reference.Name)
			if typeErr != nil {
				return nil, typeErr
			}
			if found || len(typeNames) == 0 {
				continue
			}
			result = append(result, formDiagnostic(
				document,
				reference,
				missingFormTypeCode,
				fmt.Sprintf(
					"Symfony form type '%s' not found",
					reference.Name,
				),
				suggestion.Similar(reference.Name, typeNames),
			))
		case form.ReferenceOption:
			if reference.Origin == form.OriginDefinition {
				continue
			}
			var options []form.Option
			var optionErr error
			localKey := strings.ToLower(reference.Class)
			if current, exists := localTypes[localKey]; exists &&
				diagnosticFormTypeMatches(
					current,
					reference.FormType,
				) {
				options, optionErr = p.index.EffectiveOptionsFor(current)
			} else {
				options, optionErr = p.index.EffectiveOptions(
					reference.FormType,
				)
			}
			if optionErr != nil {
				return nil, optionErr
			}
			if len(options) == 0 ||
				hasFormOption(options, reference.Name) {
				continue
			}
			names := make([]string, 0, len(options))
			for _, option := range options {
				names = append(names, option.Name)
			}
			result = append(result, formDiagnostic(
				document,
				reference,
				missingFormOptionCode,
				fmt.Sprintf(
					"Option '%s' is not defined for form type '%s'",
					reference.Name,
					reference.FormType,
				),
				suggestion.Similar(reference.Name, names),
			))
		case form.ReferenceField:
			diagnostic, found, fieldErr := p.fieldDiagnostic(
				document,
				reference,
				localTypes[strings.ToLower(reference.Class)],
			)
			if fieldErr != nil {
				return nil, fieldErr
			}
			if found {
				result = append(result, diagnostic)
			}
		}
	}
	return result, nil
}

func (p *FormAnalyzer) twigFieldDiagnostics(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, err
	}
	variables, err := form.TwigFormVariables(p.phpIndex, path)
	if err != nil {
		return nil, err
	}
	references := form.TwigFieldReferences(
		document.SyntaxTree.Root,
		variables,
	)
	var result []lsp.Problem
	for _, reference := range form.TwigViewVarReferences(
		document.SyntaxTree.Root,
		variables,
	) {
		if ctx.Err() != nil {
			return result, nil
		}
		var viewVars []form.ViewVar
		for _, formType := range reference.FormTypes {
			current, viewErr := p.index.EffectiveViewVars(formType)
			if viewErr != nil {
				return nil, viewErr
			}
			viewVars = append(viewVars, current...)
		}
		if len(viewVars) == 0 || hasFormViewVar(viewVars, reference.Name) {
			continue
		}
		names := make([]string, 0, len(viewVars))
		seen := make(map[string]struct{}, len(viewVars))
		for _, viewVar := range viewVars {
			key := strings.ToLower(viewVar.Name)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			names = append(names, viewVar.Name)
		}
		result = append(result, formDiagnostic(
			document,
			form.Reference{
				Role:     form.ReferenceField,
				Origin:   form.OriginFieldAccess,
				Name:     reference.Name,
				Node:     reference.Node,
				FormType: strings.Join(reference.FormTypes, "|"),
			},
			missingFormViewVarCode,
			fmt.Sprintf(
				"FormView variable '%s' is not defined",
				reference.Name,
			),
			suggestion.Similar(reference.Name, names),
		))
	}
	for _, reference := range references {
		if ctx.Err() != nil {
			return result, nil
		}
		switch strings.ToLower(reference.Name) {
		case "vars", "parent", "children":
			continue
		}
		var fields []form.Field
		for _, formType := range reference.FormTypes {
			current, fieldErr := p.index.EffectiveFields(formType)
			if fieldErr != nil {
				return nil, fieldErr
			}
			fields = append(fields, current...)
		}
		if len(fields) == 0 || hasFormField(fields, reference.Name) {
			continue
		}
		names := make([]string, 0, len(fields))
		seen := make(map[string]struct{}, len(fields))
		for _, field := range fields {
			key := strings.ToLower(field.Name)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			names = append(names, field.Name)
		}
		formTypes := strings.Join(reference.FormTypes, "|")
		result = append(result, formDiagnostic(
			document,
			form.Reference{
				Role:     form.ReferenceField,
				Origin:   form.OriginFieldAccess,
				Name:     reference.Name,
				Node:     reference.Node,
				FormType: formTypes,
			},
			missingFormFieldCode,
			fmt.Sprintf(
				"Field '%s' is not defined by form type '%s'",
				reference.Name,
				formTypes,
			),
			suggestion.Similar(reference.Name, names),
		))
	}
	return result, nil
}

func (p *FormAnalyzer) legacyFormAliasesDeprecated() bool {
	if p == nil || p.phpIndex == nil {
		return false
	}
	version, found := p.phpIndex.Project().DependencyVersion(
		"symfony/http-kernel",
		"symfony/framework-bundle",
		"symfony/form",
	)
	return found && version.AtLeast(2, 8)
}

func legacyFormTypeAliasDiagnostic(
	_ *lsp.TextDocument,
	reference form.Reference,
	className string,
) lsp.Problem {
	return lsp.Problem{
		Range:    valueNodeTextRange(reference.Node, reference.Name),
		Message:  "Use fully-qualified class name (FQCN)",
		Severity: protocol.DiagnosticSeverityHint,
		Source:   "symfony",
		ID:       legacyFormTypeAliasCode,
		Payload: map[string]any{
			"className": className,
		},
	}
}

func (p *FormAnalyzer) fieldDiagnostic(
	document *lsp.TextDocument,
	reference form.Reference,
	local form.Type,
) (lsp.Problem, bool, error) {
	var fields []form.Field
	var err error
	if local.Class != "" &&
		diagnosticFormTypeMatches(local, reference.FormType) {
		fields, err = p.index.EffectiveFieldsFor(local)
	} else {
		fields, err = p.index.EffectiveFields(reference.FormType)
	}
	if err != nil {
		return lsp.Problem{}, false, err
	}
	if reference.Origin == form.OriginFieldAccess {
		if len(fields) == 0 || hasFormField(fields, reference.Name) {
			return lsp.Problem{}, false, nil
		}
		names := make([]string, 0, len(fields))
		for _, field := range fields {
			names = append(names, field.Name)
		}
		return formDiagnostic(
			document,
			reference,
			missingFormFieldCode,
			fmt.Sprintf(
				"Field '%s' is not defined by form type '%s'",
				reference.Name,
				reference.FormType,
			),
			suggestion.Similar(reference.Name, names),
		), true, nil
	}

	var declaration *form.Field
	for index := range fields {
		if formFieldMatch(fields[index].Name, reference.Name) {
			current := fields[index]
			declaration = &current
			break
		}
	}
	if declaration == nil || !declaration.Mapped {
		return lsp.Problem{}, false, nil
	}
	dataClass := local.DataClass
	if dataClass == "" {
		dataClass, err = p.index.DataClassFor(reference.FormType)
	}
	if err != nil || dataClass == "" {
		return lsp.Problem{}, false, err
	}
	var dataFields []form.DataField
	if local.DataClass != "" {
		dataFields = p.index.DataFieldsForClass(local.DataClass)
	} else {
		dataFields, err = p.index.DataFieldsFor(reference.FormType)
		if err != nil {
			return lsp.Problem{}, false, err
		}
	}
	if len(dataFields) == 0 {
		return lsp.Problem{}, false, nil
	}
	target := reference.Name
	if declaration.PropertyPath != "" {
		target = declaration.PropertyPath
	}
	names := make([]string, 0, len(dataFields))
	for _, field := range dataFields {
		names = append(names, field.Name)
		if formFieldMatch(field.Name, target) {
			return lsp.Problem{}, false, nil
		}
	}
	return formDiagnostic(
		document,
		reference,
		missingFormFieldCode,
		fmt.Sprintf(
			"Mapped form field '%s' has no writable property on '%s'",
			target,
			dataClass,
		),
		suggestion.Similar(target, names),
	), true, nil
}

func diagnosticFormTypeMatches(current form.Type, name string) bool {
	if strings.EqualFold(current.Class, strings.TrimPrefix(name, `\`)) {
		return true
	}
	for _, alias := range current.Aliases {
		if strings.EqualFold(alias, name) {
			return true
		}
	}
	return false
}

func formDocumentReferences(
	ctx context.Context,
	root *phpsyntax.Node,
) []form.Reference {
	var result []form.Reference
	seen := make(map[string]struct{})
	for _, node := range phpquery.Nodes(root, phpsyntax.PhpString) {
		reference, ok := form.ReferenceAt(ctx, root, node)
		if !ok || reference.Node == nil {
			continue
		}
		rng := reference.Node.Range()
		key := fmt.Sprintf("%d:%d:%d", reference.Role, rng.Start, rng.End)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	return result
}

func formTypeNames(types []form.Type) []string {
	var result []string
	for _, current := range types {
		if len(current.Aliases) == 0 {
			result = append(result, current.Class)
			continue
		}
		result = append(result, current.Aliases...)
	}
	return result
}

func hasFormOption(options []form.Option, name string) bool {
	for _, option := range options {
		if strings.EqualFold(option.Name, name) {
			return true
		}
	}
	return false
}

func hasFormField(fields []form.Field, name string) bool {
	for _, field := range fields {
		if formFieldMatch(field.Name, name) {
			return true
		}
	}
	return false
}

func hasFormViewVar(viewVars []form.ViewVar, name string) bool {
	for _, viewVar := range viewVars {
		if strings.EqualFold(viewVar.Name, name) {
			return true
		}
	}
	return false
}

func formFieldMatch(left, right string) bool {
	normalize := func(value string) string {
		return strings.ToLower(strings.ReplaceAll(value, "_", ""))
	}
	return normalize(left) == normalize(right)
}

func formDiagnostic(
	_ *lsp.TextDocument,
	reference form.Reference,
	code lsp.DiagnosticID,
	message string,
	suggestions []string,
) lsp.Problem {
	return lsp.Problem{
		Range:    valueNodeTextRange(reference.Node, reference.Name),
		Message:  message,
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "symfony",
		ID:       code,
		Payload: map[string]any{
			"suggestions": suggestions,
		},
	}
}
