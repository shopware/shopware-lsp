package codeaction

import (
	"context"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
)

const (
	invokableCommandClass = "Symfony\\Component\\Console\\Command\\" +
		"InvokableCommand"
	commandArgumentAttribute = "Symfony\\Component\\Console\\Attribute\\" +
		"Argument"
	commandOptionAttribute = "Symfony\\Component\\Console\\Attribute\\" +
		"Option"
	commandInputInterface = "Symfony\\Component\\Console\\Input\\" +
		"InputInterface"
	commandOutputInterface = "Symfony\\Component\\Console\\Output\\" +
		"OutputInterface"
)

// InvokableCommandMigrationCodeActionProvider ports the Symfony 7.3
// CommandToInvokableIntention as a conservative whole-document rewrite. It is
// offered only when configure() consists entirely of statically convertible
// addArgument()/addOption() calls.
type InvokableCommandMigrationCodeActionProvider struct {
	phpIndex *php.PHPIndex
}

func NewInvokableCommandMigrationCodeActionProvider(
	phpIndex *php.PHPIndex,
) *InvokableCommandMigrationCodeActionProvider {
	return &InvokableCommandMigrationCodeActionProvider{
		phpIndex: phpIndex,
	}
}

func (p *InvokableCommandMigrationCodeActionProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *InvokableCommandMigrationCodeActionProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || p == nil || p.phpIndex == nil ||
		request == nil || request.CodeActionParams == nil ||
		request.Document == nil || request.Root == nil ||
		request.Node == nil ||
		request.Document.SyntaxLanguage != language.PHP {
		return nil
	}
	if _, found := p.phpIndex.FindClass(invokableCommandClass); !found {
		return nil
	}
	class := phpquery.ClassAt(request.Node)
	if class == nil || class.Kind() != phpsyntax.PhpClassDeclaration ||
		phpClassIsReadonly(class) {
		return nil
	}
	resolver := php.NewNameResolver(request.Root)
	extends := phpquery.DirectChild(class, phpsyntax.PhpExtendsClause)
	extendsNames := phpquery.ClassExtends(class)
	if extends == nil || len(extendsNames) != 1 ||
		!strings.EqualFold(
			strings.Trim(
				resolver.Resolve(extendsNames[0]),
				"\\",
			),
			consoleCommandClass,
		) {
		return nil
	}
	execute := phpOwnMethod(class, "execute")
	if execute == nil || phpOwnMethod(class, "__invoke") != nil {
		return nil
	}
	configure := phpOwnMethod(class, "configure")
	if configure != nil && !safeInvokableCommandConfigure(configure) {
		return nil
	}

	path := propertyServiceDocumentPath(request)
	arguments, options := console.LegacyCommandInputs(class, path)
	expectedInputs := invokableConfigureInputCount(configure)
	if len(arguments)+len(options) != expectedInputs {
		return nil
	}
	if !safeInvokableCommandDefaults(arguments, options) {
		return nil
	}

	argumentQualifier := ""
	optionQualifier := ""
	var importReplacements []phpSourceReplacement
	if len(arguments) != 0 {
		var edit *protocol.TextEdit
		argumentQualifier, edit = phpClassQualifier(
			request,
			commandArgumentAttribute,
		)
		if argumentQualifier == "" {
			return nil
		}
		if edit != nil {
			replacement, ok := phpTextEditReplacement(request, *edit)
			if !ok {
				return nil
			}
			importReplacements = append(importReplacements, replacement)
		}
	}
	if len(options) != 0 {
		var edit *protocol.TextEdit
		optionQualifier, edit = phpClassQualifier(
			request,
			commandOptionAttribute,
		)
		if optionQualifier == "" {
			return nil
		}
		if edit != nil {
			replacement, ok := phpTextEditReplacement(request, *edit)
			if !ok {
				return nil
			}
			importReplacements = append(importReplacements, replacement)
		}
	}

	migration, ok := buildInvokableCommandMigration(
		request,
		class,
		execute,
		configure,
		extends,
		resolver,
		arguments,
		options,
		argumentQualifier,
		optionQualifier,
	)
	if !ok {
		return nil
	}
	migration = append(migration, importReplacements...)
	updated, ok := applyPHPSourceReplacements(
		request.Document.Source,
		migration,
	)
	if !ok {
		return nil
	}
	updated = cleanupInvokableCommandImports(
		request.Document.URI,
		updated,
	)
	parsed := lsp.NewTextDocument(
		request.Document.URI,
		updated,
		request.Document.Version+1,
	)
	if len(parsed.ParseErrors) != 0 {
		return nil
	}
	return []protocol.CodeAction{{
		Title: "Symfony: Migrate to invokable command",
		Kind:  protocol.CodeActionRefactorRewrite,
		Edit: &protocol.WorkspaceEdit{
			Changes: map[string][]protocol.TextEdit{
				request.TextDocument.URI: {
					{
						Range: offsetRange(
							request,
							0,
							uint32(len(request.Document.Source)),
						),
						NewText: updated,
					},
				},
			},
		},
	}}
}

func phpOwnMethod(
	class *phpsyntax.Node,
	name string,
) *phpsyntax.Node {
	for _, method := range phpquery.Methods(class) {
		if strings.EqualFold(phpquery.MethodName(method), name) {
			return method
		}
	}
	return nil
}

func safeInvokableCommandConfigure(method *phpsyntax.Node) bool {
	block := phpquery.DirectChild(method, phpsyntax.PhpBlock)
	if block == nil {
		return false
	}
	for child := range block.ChildNodes() {
		if child.Kind() != phpsyntax.PhpExpressionStatement {
			return false
		}
		var statementCalls int
		for _, call := range phpquery.Nodes(
			child,
			phpsyntax.PhpMemberCall,
			phpsyntax.PhpScopedCall,
			phpsyntax.PhpFunctionCall,
		) {
			if phpquery.FunctionLikeAt(call) != method {
				continue
			}
			if call.Kind() != phpsyntax.PhpMemberCall ||
				!invokableCommandCallRootedAtThis(call) {
				return false
			}
			switch strings.ToLower(phpquery.CallMethodName(call)) {
			case "addargument", "addoption":
				statementCalls++
			default:
				return false
			}
		}
		if statementCalls == 0 {
			return false
		}
	}
	return true
}

func invokableCommandCallRootedAtThis(call *phpsyntax.Node) bool {
	receiver := phpquery.CallReceiver(call)
	for receiver != nil {
		switch receiver.Kind() {
		case phpsyntax.PhpVariable:
			return strings.EqualFold(
				phpquery.VariableName(receiver),
				"this",
			)
		case phpsyntax.PhpMemberCall, phpsyntax.PhpScopedCall:
			receiver = phpquery.CallReceiver(receiver)
		case phpsyntax.PhpMemberAccess, phpsyntax.PhpScopedAccess:
			var first *phpsyntax.Node
			for child := range receiver.ChildNodes() {
				first = child
				break
			}
			receiver = first
		default:
			return false
		}
	}
	return false
}

func invokableConfigureInputCount(
	configure *phpsyntax.Node,
) int {
	if configure == nil {
		return 0
	}
	count := 0
	for _, call := range phpquery.Nodes(
		configure,
		phpsyntax.PhpMemberCall,
	) {
		if phpquery.FunctionLikeAt(call) != configure {
			continue
		}
		switch strings.ToLower(phpquery.CallMethodName(call)) {
		case "addargument", "addoption":
			count++
		}
	}
	return count
}

func safeInvokableCommandDefaults(
	arguments,
	options []console.Input,
) bool {
	for _, input := range append(
		append([]console.Input(nil), arguments...),
		options...,
	) {
		value := strings.ToLower(strings.TrimSpace(input.Default))
		if value == "" {
			continue
		}
		if strings.Contains(value, "new ") ||
			strings.ContainsAny(value, "();") {
			return false
		}
	}
	return true
}

type invokableCommandInputCall struct {
	call         *phpsyntax.Node
	replacement  *phpsyntax.Node
	variableName string
}

type invokableCommandParameter struct {
	text     string
	optional bool
}

func buildInvokableCommandMigration(
	request *lsp.CodeActionRequest,
	class,
	execute,
	configure,
	extends *phpsyntax.Node,
	resolver *php.NameResolver,
	arguments,
	options []console.Input,
	argumentQualifier,
	optionQualifier string,
) ([]phpSourceReplacement, bool) {
	inputName, inputType := invokableCommandInterfaceParameter(
		execute,
		resolver,
		commandInputInterface,
	)
	outputName, outputType := invokableCommandInterfaceParameter(
		execute,
		resolver,
		commandOutputInterface,
	)
	inputVariables := make(map[string]string, len(arguments)+len(options))
	var required, optional []invokableCommandParameter
	usedNames := make(map[string]struct{})
	for _, input := range arguments {
		variable := invokableCommandVariableName(input.Name)
		if variable == "" {
			return nil, false
		}
		if _, exists := usedNames[strings.ToLower(variable)]; exists {
			return nil, false
		}
		usedNames[strings.ToLower(variable)] = struct{}{}
		inputVariables["argument:"+input.Name] = variable
		parameter := invokableArgumentParameter(
			input,
			variable,
			argumentQualifier,
		)
		if invokableArgumentOptional(input) {
			optional = append(optional, parameter)
		} else {
			required = append(required, parameter)
		}
	}
	for _, input := range options {
		variable := invokableCommandVariableName(input.Name)
		if variable == "" {
			return nil, false
		}
		if _, exists := usedNames[strings.ToLower(variable)]; exists {
			return nil, false
		}
		usedNames[strings.ToLower(variable)] = struct{}{}
		inputVariables["option:"+input.Name] = variable
		optional = append(optional, invokableOptionParameter(
			input,
			variable,
			optionQualifier,
		))
	}

	calls := invokableInputCalls(execute, inputName, inputVariables)
	var replacements []phpSourceReplacement
	replacedRanges := make([]phpsyntax.TextRange, 0, len(calls))
	for _, item := range calls {
		target := item.replacement
		if statement := invokableRedundantInputAssignment(
			item,
		); statement != nil {
			replacements = append(replacements, phpSourceReplacement{
				start: statement.Range().Start,
				end:   statement.Range().End,
			})
			replacedRanges = append(replacedRanges, statement.Range())
			continue
		}
		replacements = append(replacements, phpSourceReplacement{
			start: target.RangeTrimmedTrivia().Start,
			end:   target.RangeTrimmedTrivia().End,
			text:  "$" + item.variableName,
		})
		replacedRanges = append(replacedRanges, item.call.Range())
	}

	inputUsed := invokableVariableUsedOutsideRanges(
		execute,
		inputName,
		replacedRanges,
	)
	outputUsed := invokableVariableUsedOutsideRanges(
		execute,
		outputName,
		nil,
	)
	if inputUsed && inputName != "" {
		if _, exists := usedNames[strings.ToLower(inputName)]; exists {
			return nil, false
		}
		required = append([]invokableCommandParameter{{
			text: inputType + " $" + inputName,
		}}, required...)
		usedNames[strings.ToLower(inputName)] = struct{}{}
	}
	if outputUsed && outputName != "" {
		if _, exists := usedNames[strings.ToLower(outputName)]; exists {
			return nil, false
		}
		position := 0
		if inputUsed {
			position = 1
		}
		required = append(
			required,
			invokableCommandParameter{},
		)
		copy(required[position+1:], required[position:])
		required[position] = invokableCommandParameter{
			text: outputType + " $" + outputName,
		}
		usedNames[strings.ToLower(outputName)] = struct{}{}
	}
	for _, parameter := range phpquery.Parameters(execute) {
		name := strings.TrimPrefix(phpquery.ParameterName(parameter), "$")
		if name == "" || strings.EqualFold(name, inputName) ||
			strings.EqualFold(name, outputName) {
			continue
		}
		if _, exists := usedNames[strings.ToLower(name)]; exists {
			return nil, false
		}
		candidate := invokableCommandParameter{
			text: strings.TrimSpace(parameter.Text()),
			optional: phpquery.ParameterOptional(parameter) ||
				phpquery.ParameterVariadic(parameter),
		}
		if candidate.optional {
			optional = append(optional, candidate)
		} else {
			required = append(required, candidate)
		}
		usedNames[strings.ToLower(name)] = struct{}{}
	}

	signature, ok := invokableCommandSignatureReplacement(
		request.Document.Source,
		class,
		execute,
		append(required, optional...),
	)
	if !ok {
		return nil, false
	}
	replacements = append(replacements, signature)
	replacements = append(replacements, phpSourceReplacement{
		start: extends.Range().Start,
		end:   extends.Range().End,
	})
	if configure != nil {
		replacements = append(replacements, phpSourceReplacement{
			start: configure.Range().Start,
			end:   configure.Range().End,
		})
	}
	replacements = append(
		replacements,
		invokableParentConstructorReplacements(class)...,
	)
	commandQualifier, commandImportEdit := phpClassQualifier(
		request,
		consoleCommandClass,
	)
	exitConstantReplacements := invokableSelfExitConstantReplacements(
		execute,
		commandQualifier,
	)
	replacements = append(replacements, exitConstantReplacements...)
	if len(exitConstantReplacements) != 0 && commandImportEdit != nil {
		commandImport, importOK := phpTextEditReplacement(
			request,
			*commandImportEdit,
		)
		if !importOK {
			return nil, false
		}
		replacements = append(replacements, commandImport)
	}
	return replacements, true
}

func invokableCommandInterfaceParameter(
	method *phpsyntax.Node,
	resolver *php.NameResolver,
	interfaceName string,
) (string, string) {
	for _, parameter := range phpquery.Parameters(method) {
		for _, candidate := range strings.FieldsFunc(
			phpquery.ParameterType(parameter),
			func(value rune) bool {
				return value == '?' || value == '|' || value == '&' ||
					value == '(' || value == ')'
			},
		) {
			if strings.EqualFold(
				strings.Trim(resolver.Resolve(candidate), "\\"),
				interfaceName,
			) {
				return strings.TrimPrefix(
						phpquery.ParameterName(parameter),
						"$",
					),
					phpquery.ParameterType(parameter)
			}
		}
	}
	return "", ""
}

func invokableInputCalls(
	execute *phpsyntax.Node,
	inputName string,
	variables map[string]string,
) []invokableCommandInputCall {
	if inputName == "" {
		return nil
	}
	var result []invokableCommandInputCall
	for _, call := range phpquery.Nodes(
		execute,
		phpsyntax.PhpMemberCall,
	) {
		if phpquery.FunctionLikeAt(call) != execute {
			continue
		}
		receiver := phpquery.CallReceiver(call)
		if receiver == nil || receiver.Kind() != phpsyntax.PhpVariable ||
			!strings.EqualFold(
				phpquery.VariableName(receiver),
				inputName,
			) {
			continue
		}
		kind := ""
		switch strings.ToLower(phpquery.CallMethodName(call)) {
		case "getargument":
			kind = "argument:"
		case "getoption":
			kind = "option:"
		default:
			continue
		}
		literal := phpquery.StringArgument(call, 0)
		if literal == nil {
			continue
		}
		variable := variables[kind+phpquery.StringValue(literal)]
		if variable == "" {
			continue
		}
		replacement := call
		if parent := call.Parent(); parent != nil &&
			parent.Kind() == phpsyntax.PhpCastExpression {
			replacement = parent
		}
		result = append(result, invokableCommandInputCall{
			call:         call,
			replacement:  replacement,
			variableName: variable,
		})
	}
	return result
}

func invokableRedundantInputAssignment(
	item invokableCommandInputCall,
) *phpsyntax.Node {
	statement := phpAncestorOfKind(
		item.replacement,
		phpsyntax.PhpExpressionStatement,
	)
	if statement == nil ||
		!strings.EqualFold(
			phpquery.AssignedVariable(statement),
			"$"+item.variableName,
		) {
		return nil
	}
	assignments := phpquery.Nodes(
		statement,
		phpsyntax.PhpAssignmentExpression,
	)
	if len(assignments) != 1 {
		return nil
	}
	var values []*phpsyntax.Node
	for child := range assignments[0].ChildNodes() {
		values = append(values, child)
	}
	if len(values) != 2 || values[1] != item.replacement {
		return nil
	}
	return statement
}

func invokableVariableUsedOutsideRanges(
	method *phpsyntax.Node,
	name string,
	excluded []phpsyntax.TextRange,
) bool {
	if name == "" {
		return false
	}
	block := phpquery.DirectChild(method, phpsyntax.PhpBlock)
	if block == nil {
		return false
	}
	for _, variable := range phpquery.Nodes(block, phpsyntax.PhpVariable) {
		if !strings.EqualFold(phpquery.VariableName(variable), name) {
			continue
		}
		skip := false
		for _, rng := range excluded {
			if variable.Range().Start >= rng.Start &&
				variable.Range().End <= rng.End {
				skip = true
				break
			}
		}
		if !skip {
			return true
		}
	}
	return false
}

func invokableArgumentParameter(
	input console.Input,
	variable,
	qualifier string,
) invokableCommandParameter {
	var fields []string
	if variable != input.Name {
		fields = append(fields, "name: '"+
			escapePHPSingleQuoted(input.Name)+"'")
	}
	if input.Description != "" {
		fields = append(fields, "description: '"+
			escapePHPSingleQuoted(input.Description)+"'")
	}
	attribute := "#[" + qualifier
	if len(fields) != 0 {
		attribute += "(" + strings.Join(fields, ", ") + ")"
	}
	attribute += "] "
	parameterType := "string"
	if strings.Contains(
		strings.ToUpper(input.Mode),
		"IS_ARRAY",
	) {
		parameterType = "array"
	}
	optional := invokableArgumentOptional(input)
	if optional {
		parameterType = "?" + parameterType
	}
	text := attribute + parameterType + " $" + variable
	if optional {
		value := strings.TrimSpace(input.Default)
		if value == "" {
			value = "null"
		}
		text += " = " + value
	}
	return invokableCommandParameter{
		text:     text,
		optional: optional,
	}
}

func invokableArgumentOptional(input console.Input) bool {
	return input.Default != "" || strings.Contains(
		strings.ToUpper(input.Mode),
		"OPTIONAL",
	)
}

func invokableOptionParameter(
	input console.Input,
	variable,
	qualifier string,
) invokableCommandParameter {
	var fields []string
	if variable != input.Name {
		fields = append(fields, "name: '"+
			escapePHPSingleQuoted(input.Name)+"'")
	}
	if input.Shortcut != "" {
		fields = append(fields, "shortcut: '"+
			escapePHPSingleQuoted(input.Shortcut)+"'")
	}
	if input.Description != "" {
		fields = append(fields, "description: '"+
			escapePHPSingleQuoted(input.Description)+"'")
	}
	attribute := "#[" + qualifier
	if len(fields) != 0 {
		attribute += "(" + strings.Join(fields, ", ") + ")"
	}
	attribute += "] "
	mode := strings.ToUpper(input.Mode)
	parameterType := "?string"
	defaultValue := "null"
	switch {
	case strings.Contains(mode, "VALUE_NONE"):
		parameterType = "bool"
		defaultValue = "false"
	case strings.Contains(mode, "VALUE_IS_ARRAY") ||
		strings.Contains(mode, "IS_ARRAY"):
		parameterType = "?array"
	}
	if value := strings.TrimSpace(input.Default); value != "" {
		defaultValue = value
	}
	return invokableCommandParameter{
		text: attribute + parameterType + " $" + variable +
			" = " + defaultValue,
		optional: true,
	}
}

func invokableCommandVariableName(name string) string {
	if name == "" {
		return ""
	}
	valid := true
	for index, value := range name {
		if index == 0 {
			if value != '_' && !unicode.IsLetter(value) {
				valid = false
				break
			}
			continue
		}
		if value != '_' && !unicode.IsLetter(value) &&
			!unicode.IsDigit(value) {
			valid = false
			break
		}
	}
	if valid {
		return name
	}
	var builder strings.Builder
	upperNext := false
	for _, value := range name {
		if unicode.IsLetter(value) || unicode.IsDigit(value) ||
			value == '_' {
			if builder.Len() == 0 && unicode.IsDigit(value) {
				builder.WriteByte('_')
			}
			if upperNext && unicode.IsLetter(value) {
				value = unicode.ToUpper(value)
			}
			builder.WriteRune(value)
			upperNext = false
			continue
		}
		upperNext = builder.Len() != 0
	}
	if builder.Len() == 0 {
		return "arg"
	}
	return builder.String()
}

func invokableCommandSignatureReplacement(
	source string,
	class,
	method *phpsyntax.Node,
	parameters []invokableCommandParameter,
) (phpSourceReplacement, bool) {
	start := uint32(0)
	for token := range method.ChildTokens() {
		text := strings.ToLower(token.Text())
		if text == "public" || text == "protected" ||
			text == "private" || text == "function" {
			start = token.Range().Start
			break
		}
	}
	block := phpquery.DirectChild(method, phpsyntax.PhpBlock)
	open, _ := phpBlockDelimiters(block)
	if start == 0 || open == nil || start >= open.Range().Start {
		return phpSourceReplacement{}, false
	}
	methodIndent := phpLineIndentation(source, start)
	parameterIndent := methodIndent + phpMemberIndentUnit(
		source,
		class,
		methodIndent,
	)
	var text strings.Builder
	text.WriteString("public function __invoke(")
	if len(parameters) != 0 {
		text.WriteByte('\n')
		for index, parameter := range parameters {
			text.WriteString(parameterIndent)
			text.WriteString(parameter.text)
			if index < len(parameters)-1 {
				text.WriteByte(',')
			}
			text.WriteByte('\n')
		}
		text.WriteString(methodIndent)
	}
	text.WriteString("): int ")
	return phpSourceReplacement{
		start: start,
		end:   open.Range().Start,
		text:  text.String(),
	}, true
}

func invokableParentConstructorReplacements(
	class *phpsyntax.Node,
) []phpSourceReplacement {
	constructor := phpClassConstructor(class)
	if constructor == nil {
		return nil
	}
	var result []phpSourceReplacement
	for _, call := range phpquery.Nodes(
		constructor,
		phpsyntax.PhpScopedCall,
	) {
		if phpquery.FunctionLikeAt(call) != constructor ||
			!strings.EqualFold(
				phpquery.CallMethodName(call),
				"__construct",
			) {
			continue
		}
		receiver := phpquery.CallReceiver(call)
		if receiver == nil ||
			!strings.EqualFold(
				phpquery.NameValue(receiver),
				"parent",
			) {
			continue
		}
		statement := phpAncestorOfKind(
			call,
			phpsyntax.PhpExpressionStatement,
		)
		if statement != nil {
			result = append(result, phpSourceReplacement{
				start: statement.Range().Start,
				end:   statement.Range().End,
			})
		}
	}
	return result
}

func invokableSelfExitConstantReplacements(
	execute *phpsyntax.Node,
	commandQualifier string,
) []phpSourceReplacement {
	if commandQualifier == "" {
		return nil
	}
	var result []phpSourceReplacement
	for _, access := range phpquery.Nodes(
		execute,
		phpsyntax.PhpMemberAccess,
		phpsyntax.PhpScopedAccess,
	) {
		var names []string
		for child := range access.ChildNodes() {
			if child.Kind() == phpsyntax.PhpName {
				names = append(names, phpquery.NameValue(child))
			}
		}
		if len(names) != 2 ||
			(!strings.EqualFold(names[0], "self") &&
				!strings.EqualFold(names[0], "static")) {
			continue
		}
		switch strings.ToUpper(names[1]) {
		case "SUCCESS", "FAILURE", "INVALID":
			result = append(result, phpSourceReplacement{
				start: access.RangeTrimmedTrivia().Start,
				end:   access.RangeTrimmedTrivia().End,
				text:  commandQualifier + "::" + names[1],
			})
		}
	}
	return result
}

func phpAncestorOfKind(
	node *phpsyntax.Node,
	kind phpsyntax.Kind,
) *phpsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}

func phpTextEditReplacement(
	request *lsp.CodeActionRequest,
	edit protocol.TextEdit,
) (phpSourceReplacement, bool) {
	if request == nil || request.Document == nil ||
		request.Document.LineIndex == nil {
		return phpSourceReplacement{}, false
	}
	start := request.Document.LineIndex.OffsetUTF16(
		uint32(edit.Range.Start.Line),
		uint32(edit.Range.Start.Character),
	)
	end := request.Document.LineIndex.OffsetUTF16(
		uint32(edit.Range.End.Line),
		uint32(edit.Range.End.Character),
	)
	if start > end || int(end) > len(request.Document.Source) {
		return phpSourceReplacement{}, false
	}
	return phpSourceReplacement{
		start: start,
		end:   end,
		text:  edit.NewText,
	}, true
}

func cleanupInvokableCommandImports(uri, source string) string {
	document := lsp.NewTextDocument(uri, source, 1)
	if document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return source
	}
	root := document.SyntaxTree.Root
	resolver := php.NewNameResolver(root)
	targets := map[string]struct{}{
		strings.ToLower(consoleCommandClass):                                 {},
		strings.ToLower(commandInputInterface):                               {},
		strings.ToLower(commandOutputInterface):                              {},
		strings.ToLower("Symfony\\Component\\Console\\Input\\InputArgument"): {},
		strings.ToLower("Symfony\\Component\\Console\\Input\\InputOption"):   {},
	}
	var replacements []phpSourceReplacement
	for _, declaration := range phpquery.UseDeclarations(root) {
		imports := phpresolver.ParseUseDeclaration(declaration.Text())
		if len(imports) != 1 ||
			imports[0].Kind != phpresolver.ClassImport {
			continue
		}
		target := strings.Trim(imports[0].Target, "\\")
		if _, removable := targets[strings.ToLower(target)]; !removable {
			continue
		}
		if phpClassNameUsedOutside(
			root,
			resolver,
			target,
			declaration,
		) {
			continue
		}
		replacements = append(replacements, phpSourceReplacement{
			start: declaration.Range().Start,
			end:   declaration.Range().End,
		})
	}
	if len(replacements) == 0 {
		return source
	}
	updated, ok := applyPHPSourceReplacements(source, replacements)
	if !ok {
		return source
	}
	return updated
}
