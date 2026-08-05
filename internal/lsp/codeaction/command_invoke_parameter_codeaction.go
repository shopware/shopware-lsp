package codeaction

import (
	"context"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	asCommandAttributeClass = "Symfony\\Component\\Console\\Attribute\\AsCommand"
	consoleCommandClass     = "Symfony\\Component\\Console\\Command\\Command"
)

type phpSuggestedParameter struct {
	className    string
	variableName string
}

var commandInvokeParameters = []phpSuggestedParameter{
	{
		className:    "Symfony\\Component\\Console\\Input\\InputInterface",
		variableName: "input",
	},
	{
		className:    "Symfony\\Component\\Console\\Output\\OutputInterface",
		variableName: "output",
	},
	{
		className:    "Symfony\\Component\\Console\\Cursor",
		variableName: "cursor",
	},
	{
		className:    "Symfony\\Component\\Console\\Style\\SymfonyStyle",
		variableName: "io",
	},
	{
		className:    "Symfony\\Component\\Console\\Application",
		variableName: "application",
	},
}

// CommandInvokeParameterCodeActionProvider ports the Symfony plugin's
// chooser-based CommandInvokeParameterIntention to one LSP refactor action per
// available parameter type.
type CommandInvokeParameterCodeActionProvider struct {
	phpIndex *php.PHPIndex
}

func NewCommandInvokeParameterCodeActionProvider(
	phpIndex *php.PHPIndex,
) *CommandInvokeParameterCodeActionProvider {
	return &CommandInvokeParameterCodeActionProvider{phpIndex: phpIndex}
}

func (p *CommandInvokeParameterCodeActionProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *CommandInvokeParameterCodeActionProvider) GetCodeActions(
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

	class := phpquery.ClassAt(request.Node)
	if class == nil {
		return nil
	}
	resolver := php.NewNameResolver(request.Root)
	if !hasResolvedPHPAttribute(class, resolver, asCommandAttributeClass) ||
		p.isConsoleCommandSubclass(request, class, resolver) {
		return nil
	}
	var invokeMethod *phpsyntax.Node
	for _, method := range phpquery.Methods(class) {
		if strings.EqualFold(phpquery.MethodName(method), "__invoke") {
			invokeMethod = method
			break
		}
	}
	if invokeMethod == nil {
		return nil
	}

	existingTypes := phpExistingParameterTypes(
		invokeMethod,
		resolver,
	)
	var result []protocol.CodeAction
	for _, parameter := range commandInvokeParameters {
		if _, found := existingTypes[strings.ToLower(parameter.className)]; found {
			continue
		}
		qualifier, importEdit := phpClassQualifier(
			request,
			parameter.className,
		)
		parameterEdit, found := phpRequiredParameterEdit(
			request,
			class,
			invokeMethod,
			qualifier,
			parameter.variableName,
		)
		if !found {
			continue
		}
		edits := []protocol.TextEdit{parameterEdit}
		if importEdit != nil {
			edits = append(edits, *importEdit)
		}
		result = append(result, protocol.CodeAction{
			Title: "Symfony: Add " + phpClassShortName(
				parameter.className,
			) + " parameter to __invoke",
			Kind: protocol.CodeActionRefactorRewrite,
			Edit: &protocol.WorkspaceEdit{
				Changes: map[string][]protocol.TextEdit{
					request.TextDocument.URI: edits,
				},
			},
		})
	}
	return result
}

func (p *CommandInvokeParameterCodeActionProvider) isConsoleCommandSubclass(
	request *lsp.CodeActionRequest,
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) bool {
	for _, parent := range phpquery.ClassExtends(class) {
		if strings.EqualFold(
			strings.Trim(resolver.Resolve(parent), `\`),
			consoleCommandClass,
		) {
			return true
		}
	}
	path, err := uriutil.Path(request.Document.URI)
	if err != nil {
		return false
	}
	document := p.phpIndex.AnalyzeDocument(
		path,
		request.Document.Version,
		request.Root,
	)
	snapshot := p.phpIndex.SemanticSnapshot().WithDocument(document)
	className := phpquery.ClassName(class)
	if namespace := phpquery.Namespace(request.Root); namespace != "" {
		className = namespace + `\` + className
	}
	return snapshot.IsSubtypeOf(className, consoleCommandClass)
}

func phpExistingParameterTypes(
	method *phpsyntax.Node,
	resolver *php.NameResolver,
) map[string]struct{} {
	result := map[string]struct{}{}
	for _, parameter := range phpquery.Parameters(method) {
		for _, typeName := range strings.FieldsFunc(
			phpquery.ParameterType(parameter),
			func(value rune) bool {
				return value == '?' || value == '|' || value == '&' ||
					value == '(' || value == ')'
			},
		) {
			typeName = strings.TrimSpace(typeName)
			if typeName == "" {
				continue
			}
			result[strings.ToLower(strings.Trim(
				resolver.Resolve(typeName),
				`\`,
			))] = struct{}{}
		}
	}
	return result
}

func phpRequiredParameterEdit(
	request *lsp.CodeActionRequest,
	class,
	method *phpsyntax.Node,
	qualifier,
	variableName string,
) (protocol.TextEdit, bool) {
	if qualifier == "" || variableName == "" {
		return protocol.TextEdit{}, false
	}
	list := phpquery.DirectChild(method, phpsyntax.PhpParameterList)
	if list == nil {
		return protocol.TextEdit{}, false
	}
	open, close := phpParameterListDelimiters(list)
	if open == nil || close == nil ||
		open.Range().End > close.Range().Start ||
		int(close.Range().Start) > len(request.Document.Source) {
		return protocol.TextEdit{}, false
	}
	newParameter := qualifier + " $" + variableName
	parameters := phpquery.Parameters(method)
	multiline := strings.ContainsAny(
		request.Document.Source[open.Range().End:close.Range().Start],
		"\r\n",
	)

	for _, parameter := range parameters {
		if !phpquery.ParameterOptional(parameter) &&
			!phpquery.ParameterVariadic(parameter) {
			continue
		}
		offset := parameter.RangeTrimmedTrivia().Start
		newText := newParameter + ", "
		if multiline {
			newText = newParameter + ",\n" +
				phpLineIndentation(request.Document.Source, offset)
		}
		return protocol.TextEdit{
			Range:   offsetRange(request, offset, offset),
			NewText: newText,
		}, true
	}

	if len(parameters) == 0 {
		offset := open.Range().End
		newText := newParameter
		if multiline {
			newText = "\n" + phpParameterIndentation(
				request.Document.Source,
				class,
				method,
				close.Range().Start,
			) + newParameter
		}
		return protocol.TextEdit{
			Range:   offsetRange(request, offset, offset),
			NewText: newText,
		}, true
	}

	lastParameter := parameters[len(parameters)-1]
	lastEnd := lastParameter.RangeTrimmedTrivia().End
	indent := phpLineIndentation(
		request.Document.Source,
		lastParameter.RangeTrimmedTrivia().Start,
	)
	if trailingComma := phpTrailingParameterComma(
		list,
		lastEnd,
		close.Range().Start,
	); trailingComma != nil {
		offset := trailingComma.Range().End
		newText := " " + newParameter + ","
		if multiline {
			newText = "\n" + indent + newParameter + ","
		}
		return protocol.TextEdit{
			Range:   offsetRange(request, offset, offset),
			NewText: newText,
		}, true
	}

	newText := ", " + newParameter
	if multiline {
		newText = ",\n" + indent + newParameter
	}
	return protocol.TextEdit{
		Range:   offsetRange(request, lastEnd, lastEnd),
		NewText: newText,
	}, true
}

func phpParameterListDelimiters(
	list *phpsyntax.Node,
) (*phpsyntax.Token, *phpsyntax.Token) {
	var open, close *phpsyntax.Token
	for token := range list.ChildTokens() {
		switch token.Kind() {
		case phpsyntax.TkOpenParen:
			if open == nil {
				open = token
			}
		case phpsyntax.TkCloseParen:
			close = token
		}
	}
	return open, close
}

func phpTrailingParameterComma(
	list *phpsyntax.Node,
	parameterEnd,
	closeStart uint32,
) *phpsyntax.Token {
	var result *phpsyntax.Token
	for token := range list.ChildTokens() {
		if token.Kind() == phpsyntax.TkComma &&
			token.Range().Start >= parameterEnd &&
			token.Range().End <= closeStart {
			result = token
		}
	}
	return result
}

func phpParameterIndentation(
	source string,
	class,
	method *phpsyntax.Node,
	closeOffset uint32,
) string {
	closeIndent := phpLineIndentation(source, closeOffset)
	classIndent := phpLineIndentation(
		source,
		class.RangeTrimmedTrivia().Start,
	)
	methodIndent := phpLineIndentation(
		source,
		method.RangeTrimmedTrivia().Start,
	)
	indentUnit := strings.TrimPrefix(methodIndent, classIndent)
	if indentUnit == "" {
		if strings.Contains(methodIndent, "\t") {
			indentUnit = "\t"
		} else {
			indentUnit = "    "
		}
	}
	return closeIndent + indentUnit
}

func phpClassShortName(className string) string {
	className = strings.Trim(className, `\`)
	if separator := strings.LastIndex(className, `\`); separator >= 0 {
		return className[separator+1:]
	}
	return className
}
