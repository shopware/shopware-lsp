package diagnostics

import (
	"context"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/phpanalysis"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const (
	invokableCommandReturnTypeCode  lsp.DiagnosticID = "symfony.console.invoke.return_type"
	invokableCommandReturnValueCode lsp.DiagnosticID = "symfony.console.invoke.return_value"

	invokableCommandReturnTypeMessage  = "Symfony: Consider adding int return type to command __invoke()"
	invokableCommandReturnValueMessage = "Symfony: Command must return an integer value"
)

const asCommandAttribute = "Symfony\\Component\\Console\\Attribute\\AsCommand"

// InvokableCommandAnalyzer ports Symfony's AsCommand __invoke()
// return-type and return-value inspections.
type InvokableCommandAnalyzer struct {
	phpIndex *php.PHPIndex
}

func NewInvokableCommandAnalyzer(
	phpIndex *php.PHPIndex,
) *InvokableCommandAnalyzer {
	return &InvokableCommandAnalyzer{phpIndex: phpIndex}
}

func (p *InvokableCommandAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.phpIndex == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		document.LineIndex == nil ||
		strings.ToLower(filepath.Ext(document.URI)) != ".php" ||
		!strings.Contains(document.Source, "AsCommand") {
		return nil, nil
	}
	analysis, err := phpanalysis.ForDocument(p.phpIndex, document)
	if err != nil {
		return nil, err
	}
	if analysis == nil {
		return nil, nil
	}
	semanticDocument := analysis.Document
	resolver := php.NewNameResolver(document.SyntaxTree.Root)
	var result []lsp.Problem
	for _, class := range phpquery.Classes(document.SyntaxTree.Root) {
		if ctx.Err() != nil {
			return nil, nil
		}
		if !hasResolvedAsCommandAttribute(class, resolver) {
			continue
		}
		for _, method := range phpquery.Methods(class) {
			if !strings.EqualFold(phpquery.MethodName(method), "__invoke") {
				continue
			}
			nativeReturn := phpquery.MethodReturnType(method)
			if !nativeReturnIncludesInt(nativeReturn) {
				result = append(result, lsp.Problem{
					Range:    methodNameRange(method),
					Message:  invokableCommandReturnTypeMessage,
					Severity: protocol.DiagnosticSeverityHint,
					Source:   "symfony",
					ID:       invokableCommandReturnTypeCode,
				})
			}
			if nativeReturnIncludesInt(nativeReturn) {
				continue
			}
			for _, phpReturn := range phpquery.Nodes(
				method,
				phpsyntax.PhpReturnStatement,
			) {
				if phpquery.FunctionLikeAt(phpReturn) != method {
					continue
				}
				expression := directReturnExpression(phpReturn)
				if expression != nil && typeIncludesInt(
					semanticDocument.TypeOf(expression).Type,
				) {
					continue
				}
				rng := phpReturn.RangeTrimmedTrivia()
				if expression != nil {
					rng = expression.RangeTrimmedTrivia()
				}
				result = append(result, lsp.Problem{
					Range:    rng,
					Message:  invokableCommandReturnValueMessage,
					Severity: protocol.DiagnosticSeverityWarning,
					Source:   "symfony",
					ID:       invokableCommandReturnValueCode,
				})
			}
		}
	}
	return result, nil
}

func hasResolvedAsCommandAttribute(
	class *phpsyntax.Node,
	resolver *php.NameResolver,
) bool {
	for _, attribute := range phpquery.Attributes(class) {
		name := strings.TrimPrefix(
			resolver.Resolve(phpquery.AttributeName(attribute)),
			"\\",
		)
		if strings.EqualFold(name, asCommandAttribute) {
			return true
		}
	}
	return false
}

func nativeReturnIncludesInt(source string) bool {
	for _, part := range strings.FieldsFunc(source, func(value rune) bool {
		return value != '\\' && value != '_' && !unicode.IsLetter(value)
	}) {
		if strings.EqualFold(part, "int") ||
			strings.EqualFold(part, "integer") {
			return true
		}
	}
	return false
}

func typeIncludesInt(value types.Type) bool {
	switch value.Kind() {
	case types.IntKind, types.LiteralIntKind:
		return true
	case types.UnionKind, types.IntersectionKind:
		for _, member := range value.Arguments() {
			if typeIncludesInt(member) {
				return true
			}
		}
	}
	return false
}

func methodNameRange(method *phpsyntax.Node) cst.TextRange {
	name := phpquery.DirectChild(method, phpsyntax.PhpName)
	if name != nil {
		return name.RangeTrimmedTrivia()
	}
	return method.RangeTrimmedTrivia()
}

func directReturnExpression(phpReturn *phpsyntax.Node) *phpsyntax.Node {
	if phpReturn == nil {
		return nil
	}
	for child := range phpReturn.ChildNodes() {
		return child
	}
	return nil
}
