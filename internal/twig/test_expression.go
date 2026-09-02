package twig

import (
	"bytes"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// TestExpression is one name on the right-hand side of Twig's `is` or
// `is not` operator.
type TestExpression struct {
	Name  string
	Node  *twigsyntax.Node
	Range cst.TextRange
}

// TestExpressions returns exact test-name occurrences from a parsed Twig
// document. Both bare tests and tests with arguments are supported.
func TestExpressions(root *twigsyntax.Node) []TestExpression {
	if root == nil {
		return nil
	}
	var result []TestExpression
	for node := range twigquery.IterateNodes(
		root,
		twigsyntax.TwigBinaryExpression,
	) {
		expression, ok := twigast.CastTwigBinaryExpression(node)
		if !ok || !twigIsOperator(expression.Operator()) {
			continue
		}
		rhs, ok := expression.RhsExpression()
		if !ok {
			continue
		}
		nameNode := firstTwigTestName(rhs.Syntax())
		if nameNode == nil {
			continue
		}
		name := strings.TrimSpace(nameNode.Text())
		nameRange := nameNode.RangeTrimmedTrivia()
		if name == "" || nameRange.Len() == 0 {
			continue
		}
		result = append(result, TestExpression{
			Name:  name,
			Node:  nameNode,
			Range: nameRange,
		})
	}
	return result
}

// TestExpressionAt returns the test name under offset.
func TestExpressionAt(
	root *twigsyntax.Node,
	offset uint32,
) (TestExpression, bool) {
	for _, expression := range TestExpressions(root) {
		if expression.Range.Contains(offset) ||
			offset == expression.Range.End {
			return expression, true
		}
	}
	return TestExpression{}, false
}

// IsTestFunctionCall reports whether a Twig function-call-shaped node is the
// right-hand test operand rather than a regular Twig function invocation.
func IsTestFunctionCall(call *twigsyntax.Node) bool {
	if call == nil || call.Kind() != twigsyntax.TwigFunctionCall {
		return false
	}
	binary := twigquery.ClosestNodeOfKind(
		call,
		twigsyntax.TwigBinaryExpression,
	)
	if binary == nil {
		return false
	}
	for _, expression := range TestExpressions(binary) {
		for current := expression.Node; current != nil; current = current.Parent() {
			if current == call {
				return true
			}
		}
	}
	return false
}

// TestCompletionAt recognizes a cursor in a test name or immediately after an
// incomplete `is` / `is not` operator. The latter position has no RHS node, so
// the source between the operator and cursor must contain trivia only.
func TestCompletionAt(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
) (TestExpression, bool) {
	if root == nil || uint64(offset) > uint64(len(content)) {
		return TestExpression{}, false
	}
	for _, expression := range TestExpressions(root) {
		if offset < expression.Range.Start ||
			offset > expression.Range.End {
			continue
		}
		prefixEnd := offset
		if prefixEnd > uint32(len(content)) {
			prefixEnd = uint32(len(content))
		}
		expression.Name = string(
			content[expression.Range.Start:prefixEnd],
		)
		return expression, true
	}

	var closest *twigsyntax.Token
	for node := range twigquery.IterateNodes(
		root,
		twigsyntax.TwigBinaryExpression,
	) {
		expression, ok := twigast.CastTwigBinaryExpression(node)
		if !ok {
			continue
		}
		operator := expression.Operator()
		if !twigIsOperator(operator) || operator.Range().End > offset {
			continue
		}
		if _, hasRHS := expression.RhsExpression(); hasRHS {
			continue
		}
		if !twigTestTrivia(content[operator.Range().End:offset]) {
			continue
		}
		if closest == nil || operator.Range().End > closest.Range().End {
			closest = operator
		}
	}
	if closest == nil {
		return TestExpression{}, false
	}
	return TestExpression{
		Range: cst.TextRange{Start: offset, End: offset},
	}, true
}

func twigIsOperator(token *twigsyntax.Token) bool {
	return token != nil &&
		(token.Kind() == twigsyntax.TkIs ||
			token.Kind() == twigsyntax.TkIsNot)
}

func firstTwigTestName(root *twigsyntax.Node) *twigsyntax.Node {
	for name := range twigquery.IterateNodes(
		root,
		twigsyntax.TwigLiteralName,
	) {
		return name
	}
	return nil
}

func twigTestTrivia(content []byte) bool {
	return len(bytes.Trim(content, " \t\r\n")) == 0
}
