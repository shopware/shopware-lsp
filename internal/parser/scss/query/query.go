package query

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/scss/syntax"
)

func Nodes(root *syntax.Node, kinds ...syntax.Kind) []*syntax.Node {
	if root == nil {
		return nil
	}
	accepted := make(map[syntax.Kind]struct{}, len(kinds))
	for _, kind := range kinds {
		accepted[kind] = struct{}{}
	}

	var result []*syntax.Node
	for element := range root.Descendants() {
		node, ok := element.(*syntax.Node)
		if !ok {
			continue
		}
		if _, ok := accepted[node.Kind()]; ok {
			result = append(result, node)
		}
	}
	return result
}

func VariableAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.ScssVariable)
}

func VariableName(node *syntax.Node) string {
	variable := VariableAt(node)
	if variable == nil {
		return ""
	}
	for token := range variable.ChildTokens() {
		if token.Kind() == syntax.TkVariable {
			return strings.TrimPrefix(token.Text(), "$")
		}
	}
	return ""
}

func StringAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.ScssString)
}

func StringValue(node *syntax.Node) string {
	stringNode := StringAt(node)
	if stringNode == nil {
		return ""
	}
	for token := range stringNode.ChildTokens() {
		switch token.Kind() {
		case syntax.TkSingleQuotedString:
			return trimQuoted(token.Text(), '\'')
		case syntax.TkDoubleQuotedString:
			return trimQuoted(token.Text(), '"')
		}
	}
	return ""
}

func FunctionCallAt(node *syntax.Node) *syntax.Node {
	return ancestorOrSelf(node, syntax.ScssFunctionCall)
}

func FunctionName(node *syntax.Node) string {
	call := FunctionCallAt(node)
	if call == nil {
		return ""
	}
	for token := range call.ChildTokens() {
		if token.Kind() == syntax.TkIdentifier {
			return token.Text()
		}
	}
	return ""
}

func StringInFunction(node *syntax.Node, names ...string) bool {
	stringNode := StringAt(node)
	if stringNode == nil {
		return false
	}
	call := FunctionCallAt(stringNode.Parent())
	if call == nil {
		return false
	}
	name := FunctionName(call)
	for _, accepted := range names {
		if name == accepted {
			return true
		}
	}
	return false
}

func StringArgumentInFunction(node *syntax.Node, names ...string) *syntax.Node {
	if !StringInFunction(node, names...) {
		return nil
	}
	return StringAt(node)
}

func ancestorOrSelf(node *syntax.Node, kind syntax.Kind) *syntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}

func trimQuoted(text string, quote byte) string {
	if len(text) > 0 && text[0] == quote {
		text = text[1:]
	}
	if len(text) > 0 && text[len(text)-1] == quote {
		text = text[:len(text)-1]
	}
	return text
}
