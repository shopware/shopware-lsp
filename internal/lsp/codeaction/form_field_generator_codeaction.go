package codeaction

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	generateFormFieldsAction   = "shopware.symfony.generateFormFields"
	formFieldCandidatesCommand = "shopware/symfony/form/fields/candidates"
	generateFormFieldsCommand  = "shopware/symfony/form/fields/generate"

	symfonyFormTypeInterface = "Symfony\\Component\\Form\\FormTypeInterface"
	symfonyAbstractFormType  = "Symfony\\Component\\Form\\AbstractType"
	formBuilderInterface     = "Symfony\\Component\\Form\\FormBuilderInterface"
)

const (
	checkboxType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"CheckboxType"
	choiceType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"ChoiceType"
	countryType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"CountryType"
	dateTimeType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"DateTimeType"
	emailType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"EmailType"
	enumType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"EnumType"
	integerType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"IntegerType"
	languageType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"LanguageType"
	moneyType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"MoneyType"
	numberType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"NumberType"
	passwordType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"PasswordType"
	telType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"TelType"
	textType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"TextType"
	textAreaType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"TextareaType"
	ulidType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"UlidType"
	urlType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"UrlType"
	uuidType = "Symfony\\Component\\Form\\Extension\\Core\\Type\\" +
		"UuidType"
	entityType = "Symfony\\Bridge\\Doctrine\\Form\\Type\\EntityType"
)

// FormFieldGeneratorProvider ports the reference plugin's interactive
// FormBuilder field generator. The editor owns field selection while the
// server owns semantic discovery, type guessing, imports, and source edits.
type FormFieldGeneratorProvider struct {
	forms    *form.Index
	phpIndex *php.PHPIndex
	doctrine *doctrine.Index
}

func NewFormFieldGeneratorProvider(
	forms *form.Index,
	phpIndex *php.PHPIndex,
	doctrineIndex *doctrine.Index,
) *FormFieldGeneratorProvider {
	return &FormFieldGeneratorProvider{
		forms:    forms,
		phpIndex: phpIndex,
		doctrine: doctrineIndex,
	}
}

func (p *FormFieldGeneratorProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *FormFieldGeneratorProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || p == nil || p.forms == nil ||
		p.phpIndex == nil || request == nil ||
		request.CodeActionParams == nil || request.Document == nil ||
		request.Root == nil || request.Node == nil ||
		request.Document.SyntaxLanguage != language.PHP {
		return nil
	}
	method := phpquery.MethodAt(request.Node)
	if method == nil ||
		!strings.EqualFold(phpquery.MethodName(method), "buildForm") {
		return nil
	}
	class := phpquery.ClassAt(method)
	if class == nil || class.Kind() != phpsyntax.PhpClassDeclaration {
		return nil
	}
	className := phpClassFullyQualifiedName(request.Root, class)
	if className == "" {
		return nil
	}
	path := formGeneratorPath(request.Document.URI)
	document := p.phpIndex.AnalyzeDocument(
		path,
		request.Document.Version,
		request.Root,
	)
	snapshot := p.phpIndex.SemanticSnapshot().WithDocument(document)
	if !p.isSymfonyFormClass(
		className,
		class,
		request.Root,
		snapshot,
	) ||
		formBuilderVariable(method, request.Root, snapshot) == "" {
		return nil
	}
	return []protocol.CodeAction{{
		Title: "Symfony: Generate form fields",
		Kind:  protocol.CodeActionRefactorRewrite,
		Command: &protocol.CommandAction{
			Title:     "Symfony: Generate form fields",
			Command:   generateFormFieldsAction,
			Arguments: []any{request.TextDocument.URI, className},
		},
	}}
}

func (p *FormFieldGeneratorProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		formFieldCandidatesCommand: p.formFieldCandidates,
		generateFormFieldsCommand:  p.generateFormFields,
	}
}

type formFieldGeneratorRequest struct {
	FileURI        string   `json:"fileUri"`
	ClassName      string   `json:"className"`
	Source         string   `json:"source"`
	Version        int      `json:"version"`
	SelectedFields []string `json:"selectedFields,omitempty"`
}

type formFieldCandidate struct {
	Name          string `json:"name"`
	PHPType       string `json:"phpType"`
	SuggestedType string `json:"suggestedType"`
}

type formFieldCandidatesResponse struct {
	DataClass string               `json:"dataClass"`
	Fields    []formFieldCandidate `json:"fields"`
}

type formFieldGenerationResponse struct {
	Content string `json:"content"`
}

type formGeneratorContext struct {
	root            *phpsyntax.Node
	class           *phpsyntax.Node
	method          *phpsyntax.Node
	snapshot        *semantic.Snapshot
	current         form.Type
	dataClass       string
	builderVariable string
	dataFields      []form.DataField
	existingFields  map[string]struct{}
}

func (p *FormFieldGeneratorProvider) formFieldCandidates(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	params, generator, err := p.prepareFormGenerator(ctx, raw)
	if err != nil {
		return nil, err
	}
	_ = params
	fields, err := p.availableFormFields(generator)
	if err != nil {
		return nil, err
	}
	result := formFieldCandidatesResponse{DataClass: generator.dataClass}
	for _, field := range fields {
		guess := p.guessFormField(
			field.Name,
			dataFieldType(field),
			generator.snapshot,
		)
		result.Fields = append(result.Fields, formFieldCandidate{
			Name:          field.Name,
			PHPType:       displayPHPType(field.Type),
			SuggestedType: shortPHPClassName(guess.formType),
		})
	}
	return result, nil
}

func (p *FormFieldGeneratorProvider) generateFormFields(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	params, generator, err := p.prepareFormGenerator(ctx, raw)
	if err != nil {
		return nil, err
	}
	if len(params.SelectedFields) == 0 {
		return nil, fmt.Errorf("select at least one form field")
	}
	available, err := p.availableFormFields(generator)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]form.DataField, len(available))
	for _, field := range available {
		byName[strings.ToLower(field.Name)] = field
	}
	selected := make([]form.DataField, 0, len(params.SelectedFields))
	seen := make(map[string]struct{}, len(params.SelectedFields))
	for _, name := range params.SelectedFields {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		field, exists := byName[key]
		if !exists {
			return nil, fmt.Errorf("form field %q is no longer available", name)
		}
		seen[key] = struct{}{}
		selected = append(selected, field)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("select at least one form field")
	}
	sort.SliceStable(selected, func(left, right int) bool {
		return strings.ToLower(selected[left].Name) <
			strings.ToLower(selected[right].Name)
	})
	content, err := p.rewriteFormFields(params.Source, generator, selected)
	if err != nil {
		return nil, err
	}
	return formFieldGenerationResponse{Content: content}, nil
}

func (p *FormFieldGeneratorProvider) prepareFormGenerator(
	ctx context.Context,
	raw *json.RawMessage,
) (formFieldGeneratorRequest, *formGeneratorContext, error) {
	var params formFieldGeneratorRequest
	if err := decodeSymfonyGeneratorRequest(raw, &params); err != nil {
		return params, nil, err
	}
	if err := ctx.Err(); err != nil {
		return params, nil, err
	}
	if p == nil || p.forms == nil || p.phpIndex == nil {
		return params, nil, fmt.Errorf("symfony form field generator is unavailable")
	}
	className := strings.Trim(params.ClassName, "\\ ")
	if className == "" || params.Source == "" {
		return params, nil, fmt.Errorf("missing form class or source")
	}
	parsed := phpparser.Parse(params.Source)
	if parsed.Tree == nil || parsed.Tree.Root == nil {
		return params, nil, fmt.Errorf("parse form source")
	}
	root := parsed.Tree.Root
	var class *phpsyntax.Node
	for _, candidate := range phpquery.Classes(root) {
		if candidate.Kind() == phpsyntax.PhpClassDeclaration &&
			strings.EqualFold(
				phpClassFullyQualifiedName(root, candidate),
				className,
			) {
			class = candidate
			break
		}
	}
	if class == nil {
		return params, nil, fmt.Errorf(
			"form class %q was not found in the document",
			className,
		)
	}
	var method *phpsyntax.Node
	for _, candidate := range phpquery.Methods(class) {
		if strings.EqualFold(phpquery.MethodName(candidate), "buildForm") {
			method = candidate
			break
		}
	}
	if method == nil {
		return params, nil, fmt.Errorf(
			"form class %q has no buildForm method",
			className,
		)
	}
	path := formGeneratorPath(params.FileURI)
	document := p.phpIndex.AnalyzeDocument(
		path,
		params.Version,
		root,
	)
	snapshot := p.phpIndex.SemanticSnapshot().WithDocument(document)
	if !p.isSymfonyFormClass(className, class, root, snapshot) {
		return params, nil, fmt.Errorf(
			"PHP class %q is not a Symfony form type",
			className,
		)
	}
	builder := formBuilderVariable(method, root, snapshot)
	if builder == "" {
		return params, nil, fmt.Errorf(
			"buildForm has no FormBuilderInterface parameter",
		)
	}
	current, found := form.TypeInDocument(path, root, className)
	if !found {
		current = form.Type{Class: className, File: path}
	}
	dataClass, err := p.currentFormDataClass(
		current,
		class,
		root,
	)
	if err != nil {
		return params, nil, err
	}
	if dataClass == "" {
		return params, nil, fmt.Errorf(
			"no data_class option was found for %s",
			className,
		)
	}
	existing, err := p.currentFormFields(current, class, root)
	if err != nil {
		return params, nil, err
	}
	existingNames := make(map[string]struct{}, len(existing)*2)
	for _, field := range existing {
		if field.Name != "" {
			existingNames[strings.ToLower(field.Name)] = struct{}{}
		}
		if field.PropertyPath != "" {
			existingNames[strings.ToLower(field.PropertyPath)] = struct{}{}
		}
	}
	return params, &formGeneratorContext{
		root:            root,
		class:           class,
		method:          method,
		snapshot:        snapshot,
		current:         current,
		dataClass:       dataClass,
		builderVariable: builder,
		dataFields: form.DataFieldsForClassInSnapshot(
			snapshot,
			dataClass,
		),
		existingFields: existingNames,
	}, nil
}

func (p *FormFieldGeneratorProvider) isSymfonyFormClass(
	className string,
	class,
	root *phpsyntax.Node,
	snapshot *semantic.Snapshot,
) bool {
	if snapshot != nil &&
		snapshot.IsSubtypeOf(className, symfonyFormTypeInterface) {
		return true
	}
	resolver := php.NewNameResolver(root)
	for _, implemented := range phpquery.ClassImplements(class) {
		if strings.EqualFold(
			strings.Trim(resolver.Resolve(implemented), `\`),
			symfonyFormTypeInterface,
		) {
			return true
		}
	}
	for _, parent := range phpquery.ClassExtends(class) {
		resolved := strings.Trim(resolver.Resolve(parent), `\`)
		if strings.EqualFold(resolved, symfonyAbstractFormType) {
			return true
		}
		if p != nil && p.forms != nil {
			if _, found, err := p.forms.GetType(resolved); err == nil && found {
				return true
			}
		}
	}
	return false
}

func (p *FormFieldGeneratorProvider) currentFormDataClass(
	current form.Type,
	class,
	root *phpsyntax.Node,
) (string, error) {
	if current.DataClass != "" {
		return strings.Trim(current.DataClass, "\\"), nil
	}
	resolver := php.NewNameResolver(root)
	for _, parent := range phpquery.ClassExtends(class) {
		dataClass, err := p.forms.DataClassFor(resolver.Resolve(parent))
		if err != nil {
			return "", err
		}
		if dataClass != "" {
			return strings.Trim(dataClass, "\\"), nil
		}
	}
	if current.Parent != "" {
		dataClass, err := p.forms.DataClassFor(current.Parent)
		if err != nil {
			return "", err
		}
		if dataClass != "" {
			return strings.Trim(dataClass, "\\"), nil
		}
	}
	return "", nil
}

func (p *FormFieldGeneratorProvider) currentFormFields(
	current form.Type,
	class,
	root *phpsyntax.Node,
) ([]form.Field, error) {
	result := append([]form.Field(nil), current.Fields...)
	resolver := php.NewNameResolver(root)
	parents := append([]string(nil), phpquery.ClassExtends(class)...)
	if current.Parent != "" {
		parents = append(parents, current.Parent)
	}
	for _, parent := range parents {
		resolved := parent
		if !strings.EqualFold(parent, current.Parent) {
			resolved = resolver.Resolve(parent)
		}
		fields, err := p.forms.EffectiveFields(resolved)
		if err != nil {
			return nil, err
		}
		result = append(result, fields...)
	}
	return result, nil
}

func (p *FormFieldGeneratorProvider) availableFormFields(
	generator *formGeneratorContext,
) ([]form.DataField, error) {
	if generator == nil {
		return nil, fmt.Errorf("missing form generator context")
	}
	var result []form.DataField
	for _, field := range generator.dataFields {
		key := strings.ToLower(field.Name)
		if _, exists := generator.existingFields[key]; exists {
			continue
		}
		result = append(result, field)
	}
	sort.SliceStable(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) <
			strings.ToLower(result[right].Name)
	})
	return result, nil
}

type guessedFormField struct {
	formType string
	options  []guessedFormOption
}

type guessedFormOption struct {
	name       string
	value      string
	classValue string
}

func (p *FormFieldGeneratorProvider) guessFormField(
	name string,
	fieldType types.Type,
	snapshot *semantic.Snapshot,
) guessedFormField {
	fieldType = formFieldCoreType(fieldType)
	if fieldType.IsUnknown() || fieldType.Kind() == types.MixedKind {
		return guessedFormField{formType: textType}
	}
	if fieldType.Kind() == types.ObjectKind {
		className := fieldType.Name()
		switch {
		case snapshot != nil && snapshot.IsSubtypeOf(
			className,
			"DateTimeImmutable",
		):
			return guessedFormField{
				formType: dateTimeType,
				options: []guessedFormOption{{
					name:  "input",
					value: "'datetime_immutable'",
				}},
			}
		case snapshot != nil && snapshot.IsSubtypeOf(
			className,
			"DateTimeInterface",
		):
			return guessedFormField{formType: dateTimeType}
		case isPHPEnum(snapshot, className):
			return guessedFormField{
				formType: enumType,
				options: []guessedFormOption{{
					name:       "class",
					classValue: className,
				}},
			}
		case p.isDoctrineModel(className):
			return guessedFormField{
				formType: entityType,
				options: []guessedFormOption{{
					name:       "class",
					classValue: className,
				}},
			}
		default:
			return guessedFormField{}
		}
	}
	switch fieldType.Kind() {
	case types.IntKind, types.LiteralIntKind:
		return guessedFormField{formType: integerType}
	case types.FloatKind, types.LiteralFloatKind:
		return guessedFormField{formType: numberType}
	case types.ArrayKind, types.ListKind, types.ArrayShapeKind:
		return guessedFormField{
			formType: choiceType,
			options: []guessedFormOption{{
				name:  "choices",
				value: "[]",
			}},
		}
	case types.BoolKind, types.TrueKind, types.FalseKind:
		return guessedFormField{formType: checkboxType}
	case types.StringKind, types.LiteralStringKind:
		return guessedFormField{
			formType: guessedStringFormType(name),
		}
	default:
		return guessedFormField{}
	}
}

func (p *FormFieldGeneratorProvider) isDoctrineModel(
	className string,
) bool {
	if p == nil || p.doctrine == nil || className == "" {
		return false
	}
	_, found, err := p.doctrine.Model(className)
	return err == nil && found
}

func (p *FormFieldGeneratorProvider) rewriteFormFields(
	source string,
	generator *formGeneratorContext,
	fields []form.DataField,
) (string, error) {
	block := phpquery.DirectChild(generator.method, phpsyntax.PhpBlock)
	_, close := phpBlockDelimiters(block)
	if block == nil || close == nil {
		return "", fmt.Errorf("buildForm has no writable body")
	}
	guesses := make([]guessedFormField, len(fields))
	var classes []string
	for index, field := range fields {
		guesses[index] = p.guessFormField(
			field.Name,
			dataFieldType(field),
			generator.snapshot,
		)
		if guesses[index].formType != "" {
			classes = append(classes, guesses[index].formType)
		}
		for _, option := range guesses[index].options {
			if option.classValue != "" {
				classes = append(classes, option.classValue)
			}
		}
	}
	planner := newPHPImportPlanner(generator.root)
	planner.plan(classes)
	methodIndent := phpLineIndentation(
		source,
		generator.method.RangeTrimmedTrivia().Start,
	)
	statementIndent := methodIndent + phpMemberIndentUnit(
		source,
		generator.class,
		methodIndent,
	)
	statements := make([]string, 0, len(fields))
	for index, field := range fields {
		guess := guesses[index]
		statement := statementIndent + generator.builderVariable +
			"->add('" + escapePHPSingleQuoted(field.Name) + "'"
		if guess.formType != "" {
			statement += ", " + planner.qualifier(guess.formType) + "::class"
		}
		if len(guess.options) != 0 {
			var options []string
			for _, option := range guess.options {
				value := option.value
				if option.classValue != "" {
					value = planner.qualifier(option.classValue) + "::class"
				}
				options = append(
					options,
					"'"+escapePHPSingleQuoted(option.name)+"' => "+value,
				)
			}
			statement += ", [" + strings.Join(options, ", ") + "]"
		}
		statement += ");"
		statements = append(statements, statement)
	}
	var replacements []phpSourceReplacement
	if imports := planner.imports(); len(imports) != 0 {
		replacements = append(replacements, phpSourceReplacement{
			start: phpImportInsertionOffset(generator.root),
			end:   phpImportInsertionOffset(generator.root),
			text:  phpImportBlock(generator.root, imports),
		})
	}
	replacements = append(replacements, replacementBeforePHPCloseBrace(
		source,
		close.Range().Start,
		strings.Join(statements, "\n"),
		methodIndent,
		false,
	))
	updated, ok := applyPHPSourceReplacements(source, replacements)
	if !ok {
		return "", fmt.Errorf("apply generated form field edits")
	}
	parsedBefore := phpparser.Parse(source)
	parsedAfter := phpparser.Parse(updated)
	if len(parsedBefore.Errors) == 0 && len(parsedAfter.Errors) != 0 {
		return "", fmt.Errorf("generated form source is not valid PHP")
	}
	return updated, nil
}

type phpImportPlanner struct {
	namespace  string
	qualifiers map[string]string
	aliases    map[string]string
	pending    map[string]struct{}
}

func newPHPImportPlanner(root *phpsyntax.Node) *phpImportPlanner {
	planner := &phpImportPlanner{
		namespace:  strings.Trim(phpquery.Namespace(root), "\\"),
		qualifiers: make(map[string]string),
		aliases:    make(map[string]string),
		pending:    make(map[string]struct{}),
	}
	for _, declaration := range phpquery.UseDeclarations(root) {
		for _, imported := range phpresolver.ParseUseDeclaration(
			declaration.Text(),
		) {
			if imported.Kind != phpresolver.ClassImport {
				continue
			}
			target := strings.Trim(imported.Target, "\\")
			planner.qualifiers[strings.ToLower(target)] = imported.Alias
			planner.aliases[strings.ToLower(imported.Alias)] = target
		}
	}
	for _, class := range phpquery.Classes(root) {
		name := phpquery.ClassName(class)
		if name != "" {
			planner.aliases[strings.ToLower(name)] =
				phpClassFullyQualifiedName(root, class)
		}
	}
	return planner
}

func (p *phpImportPlanner) plan(classes []string) {
	unique := make(map[string]string, len(classes))
	for _, className := range classes {
		className = strings.Trim(className, "\\ ")
		if className != "" {
			unique[strings.ToLower(className)] = className
		}
	}
	ordered := make([]string, 0, len(unique))
	for _, className := range unique {
		ordered = append(ordered, className)
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		return strings.ToLower(ordered[left]) <
			strings.ToLower(ordered[right])
	})
	for _, className := range ordered {
		key := strings.ToLower(className)
		if _, exists := p.qualifiers[key]; exists {
			continue
		}
		short := shortPHPClassName(className)
		namespace := namespaceOfPHPClass(className)
		aliasTarget, aliasUsed := p.aliases[strings.ToLower(short)]
		if strings.EqualFold(namespace, p.namespace) && !aliasUsed {
			p.qualifiers[key] = short
			p.aliases[strings.ToLower(short)] = className
			continue
		}
		if !aliasUsed || strings.EqualFold(aliasTarget, className) {
			p.qualifiers[key] = short
			p.aliases[strings.ToLower(short)] = className
			p.pending[className] = struct{}{}
			continue
		}
		p.qualifiers[key] = `\` + className
	}
}

func (p *phpImportPlanner) qualifier(className string) string {
	className = strings.Trim(className, "\\ ")
	if qualifier := p.qualifiers[strings.ToLower(className)]; qualifier != "" {
		return qualifier
	}
	return `\` + className
}

func (p *phpImportPlanner) imports() []string {
	result := make([]string, 0, len(p.pending))
	for className := range p.pending {
		result = append(result, className)
	}
	sort.SliceStable(result, func(left, right int) bool {
		return strings.ToLower(result[left]) <
			strings.ToLower(result[right])
	})
	return result
}

func formBuilderVariable(
	method,
	root *phpsyntax.Node,
	snapshot *semantic.Snapshot,
) string {
	resolver := php.NewNameResolver(root)
	for _, parameter := range phpquery.Parameters(method) {
		parameterType := strings.Trim(
			resolver.Resolve(phpquery.ParameterType(parameter)),
			"\\",
		)
		if parameterType == "" {
			continue
		}
		if strings.EqualFold(parameterType, formBuilderInterface) ||
			snapshot != nil && snapshot.IsSubtypeOf(
				parameterType,
				formBuilderInterface,
			) {
			return phpquery.ParameterName(parameter)
		}
	}
	return ""
}

func dataFieldType(field form.DataField) types.Type {
	if field.Symbol.Kind == semantic.MethodSymbol &&
		len(field.Symbol.Parameters) != 0 {
		return field.Symbol.Parameters[0].Type
	}
	if !field.Symbol.Type.IsUnknown() {
		return field.Symbol.Type
	}
	parsed, err := types.Parse(field.Type)
	if err != nil {
		return types.Unknown()
	}
	return parsed
}

func formFieldCoreType(value types.Type) types.Type {
	if value.Kind() != types.UnionKind {
		return value
	}
	var nonNull []types.Type
	for _, member := range value.Arguments() {
		if member.Kind() != types.NullKind {
			nonNull = append(nonNull, member)
		}
	}
	if len(nonNull) == 1 {
		return nonNull[0]
	}
	return value
}

func isPHPEnum(snapshot *semantic.Snapshot, className string) bool {
	if snapshot == nil || className == "" {
		return false
	}
	for _, class := range snapshot.Classes(className) {
		if class.Kind == semantic.EnumSymbol {
			return true
		}
	}
	return false
}

func guessedStringFormType(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "description"),
		strings.Contains(lower, "note"),
		strings.Contains(lower, "beschreibung"),
		strings.Contains(lower, "comment"):
		return textAreaType
	case strings.Contains(lower, "mail"):
		return emailType
	case strings.Contains(lower, "password"):
		return passwordType
	case strings.Contains(lower, "url"):
		return urlType
	case strings.Contains(lower, "language"):
		return languageType
	case lower == "uuid":
		return uuidType
	case lower == "ulid":
		return ulidType
	case strings.Contains(lower, "country"):
		return countryType
	case strings.Contains(lower, "currency"):
		return moneyType
	case strings.Contains(lower, "telephone"),
		strings.Contains(lower, "phone"),
		strings.Contains(lower, "mobile"):
		return telType
	default:
		return textType
	}
}

func shortPHPClassName(className string) string {
	className = strings.Trim(className, "\\ ")
	if index := strings.LastIndex(className, `\`); index >= 0 {
		return className[index+1:]
	}
	return className
}

func displayPHPType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "unknown" {
		return "mixed"
	}
	return value
}

func formGeneratorPath(uri string) string {
	if path, err := uriutil.Path(uri); err == nil {
		return path
	}
	return uri
}

var _ lsp.ActionProvider = (*FormFieldGeneratorProvider)(nil)
var _ lsp.CommandProvider = (*FormFieldGeneratorProvider)(nil)
