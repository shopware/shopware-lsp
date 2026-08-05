package query

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
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

func RootValue(root *syntax.Node) *syntax.Node {
	if root == nil {
		return nil
	}
	cursor := root.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == syntax.YamlDocument {
			documentCursor := child.ChildNodeCursor()
			for documentCursor.Next() {
				documentChild := documentCursor.Node()
				if IsValue(documentChild) {
					return documentChild
				}
			}
		}
		if IsValue(child) {
			return child
		}
	}
	return nil
}

func IsValue(node *syntax.Node) bool {
	if node == nil {
		return false
	}
	switch node.Kind() {
	case syntax.YamlMapping,
		syntax.YamlFlowMapping,
		syntax.YamlSequence,
		syntax.YamlFlowSequence,
		syntax.YamlScalar,
		syntax.YamlNull:
		return true
	default:
		return false
	}
}

func IsMapping(node *syntax.Node) bool {
	return node != nil &&
		(node.Kind() == syntax.YamlMapping || node.Kind() == syntax.YamlFlowMapping)
}

func IsSequence(node *syntax.Node) bool {
	return node != nil &&
		(node.Kind() == syntax.YamlSequence || node.Kind() == syntax.YamlFlowSequence)
}

func IsNull(node *syntax.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind() == syntax.YamlNull {
		return true
	}
	token := scalarToken(node)
	if token == nil || token.Kind() != syntax.TkPlainScalar {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(token.Text())) {
	case "~", "null":
		return true
	default:
		return false
	}
}

func Pairs(mapping *syntax.Node) []*syntax.Node {
	if !IsMapping(mapping) {
		return nil
	}
	var pairs []*syntax.Node
	cursor := mapping.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == syntax.YamlPair {
			pairs = append(pairs, child)
		}
	}
	return pairs
}

func PairKey(pair *syntax.Node) *syntax.Node {
	if pair == nil || pair.Kind() != syntax.YamlPair {
		return nil
	}
	cursor := pair.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == syntax.YamlScalar {
			return child
		}
	}
	return nil
}

func PairValue(pair *syntax.Node) *syntax.Node {
	if pair == nil || pair.Kind() != syntax.YamlPair {
		return nil
	}
	foundKey := false
	var firstValue *syntax.Node
	cursor := pair.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if !foundKey && child.Kind() == syntax.YamlScalar {
			foundKey = true
			continue
		}
		if foundKey && IsValue(child) {
			if firstValue == nil {
				firstValue = child
				continue
			}
			if isCollectionDecorator(firstValue) {
				return child
			}
		}
	}
	return firstValue
}

func Property(mapping *syntax.Node, name string) *syntax.Node {
	pair := PropertyPair(mapping, name)
	return PairValue(pair)
}

func PropertyPair(mapping *syntax.Node, name string) *syntax.Node {
	if !IsMapping(mapping) {
		return nil
	}
	cursor := mapping.ChildNodeCursor()
	for cursor.Next() {
		pair := cursor.Node()
		if pair.Kind() != syntax.YamlPair {
			continue
		}
		if ScalarValue(PairKey(pair)) == name {
			return pair
		}
	}
	return nil
}

func Items(sequence *syntax.Node) []*syntax.Node {
	if !IsSequence(sequence) {
		return nil
	}
	var items []*syntax.Node
	cursor := sequence.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == syntax.YamlSequenceItem {
			items = append(items, child)
		}
	}
	return items
}

func ItemValue(item *syntax.Node) *syntax.Node {
	if item == nil || item.Kind() != syntax.YamlSequenceItem {
		return nil
	}
	var firstValue *syntax.Node
	cursor := item.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if IsValue(child) {
			if firstValue == nil {
				firstValue = child
				continue
			}
			if isCollectionDecorator(firstValue) {
				return child
			}
		}
	}
	return firstValue
}

func ScalarValue(node *syntax.Node) string {
	token := scalarToken(node)
	if token == nil {
		return ""
	}

	text := token.Text()
	switch token.Kind() {
	case syntax.TkSingleQuotedScalar:
		if len(text) >= 2 && text[0] == '\'' && text[len(text)-1] == '\'' {
			return text[1 : len(text)-1]
		}
		return strings.TrimPrefix(text, "'")
	case syntax.TkDoubleQuotedScalar:
		if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
			return text[1 : len(text)-1]
		}
		return strings.TrimPrefix(text, "\"")
	case syntax.TkBlockScalar:
		return text
	default:
		return strings.TrimSpace(text)
	}
}

func RawText(node *syntax.Node) string {
	if node == nil {
		return ""
	}
	return strings.TrimSpace(node.Text())
}

func AncestorPair(node *syntax.Node) *syntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == syntax.YamlPair {
			return current
		}
	}
	return nil
}

func PairPath(node *syntax.Node) []string {
	pair := AncestorPair(node)
	if pair == nil {
		return nil
	}

	var reversed []string
	for current := pair; current != nil; {
		reversed = append(reversed, ScalarValue(PairKey(current)))
		current = ancestorPairAbove(current)
	}

	path := make([]string, len(reversed))
	for index := range reversed {
		path[len(reversed)-index-1] = reversed[index]
	}
	return path
}

func Contains(ancestor, descendant *syntax.Node) bool {
	if ancestor == nil || descendant == nil {
		return false
	}
	for current := descendant; current != nil; current = current.Parent() {
		if current == ancestor {
			return true
		}
	}
	return false
}

func scalarToken(node *syntax.Node) *syntax.Token {
	if node == nil || node.Kind() != syntax.YamlScalar {
		return nil
	}
	cursor := node.ChildTokenCursor()
	for cursor.Next() {
		token := cursor.Token()
		switch token.Kind() {
		case syntax.TkPlainScalar,
			syntax.TkSingleQuotedScalar,
			syntax.TkDoubleQuotedScalar,
			syntax.TkBlockScalar:
			return token
		}
	}
	return nil
}

func ancestorPairAbove(node *syntax.Node) *syntax.Node {
	if node == nil {
		return nil
	}
	for current := node.Parent(); current != nil; current = current.Parent() {
		if current.Kind() == syntax.YamlPair {
			return current
		}
	}
	return nil
}

func isCollectionDecorator(node *syntax.Node) bool {
	if node == nil || node.Kind() != syntax.YamlScalar {
		return false
	}
	value := ScalarValue(node)
	return len(value) >= 2 &&
		(value[0] == '&' || value[0] == '!') &&
		!strings.ContainsAny(value, " \t")
}
