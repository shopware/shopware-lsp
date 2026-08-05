package query

import (
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/json/syntax"
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
	case syntax.JsonObject,
		syntax.JsonArray,
		syntax.JsonString,
		syntax.JsonNumber,
		syntax.JsonBoolean,
		syntax.JsonNull:
		return true
	default:
		return false
	}
}

func Pairs(object *syntax.Node) []*syntax.Node {
	if object == nil || object.Kind() != syntax.JsonObject {
		return nil
	}
	var pairs []*syntax.Node
	cursor := object.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == syntax.JsonPair {
			pairs = append(pairs, child)
		}
	}
	return pairs
}

func PairKey(pair *syntax.Node) *syntax.Node {
	if pair == nil || pair.Kind() != syntax.JsonPair {
		return nil
	}
	cursor := pair.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == syntax.JsonString {
			return child
		}
	}
	return nil
}

func PairValue(pair *syntax.Node) *syntax.Node {
	if pair == nil || pair.Kind() != syntax.JsonPair {
		return nil
	}
	foundKey := false
	cursor := pair.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if !foundKey && child.Kind() == syntax.JsonString {
			foundKey = true
			continue
		}
		if foundKey && IsValue(child) {
			return child
		}
	}
	return nil
}

func Property(object *syntax.Node, name string) *syntax.Node {
	if object == nil || object.Kind() != syntax.JsonObject {
		return nil
	}
	cursor := object.ChildNodeCursor()
	for cursor.Next() {
		pair := cursor.Node()
		if pair.Kind() != syntax.JsonPair {
			continue
		}
		if StringValue(PairKey(pair)) == name {
			return PairValue(pair)
		}
	}
	return nil
}

func StringValue(node *syntax.Node) string {
	if node == nil || node.Kind() != syntax.JsonString {
		return ""
	}
	text := strings.TrimSpace(node.Text())
	if len(text) >= 1 && text[0] == '"' {
		text = text[1:]
	}
	if len(text) >= 1 && text[len(text)-1] == '"' {
		text = text[:len(text)-1]
	}
	return text
}

func BooleanValue(node *syntax.Node) (bool, bool) {
	if node == nil || node.Kind() != syntax.JsonBoolean {
		return false, false
	}
	value, err := strconv.ParseBool(strings.TrimSpace(node.Text()))
	return value, err == nil
}

func IntegerValue(node *syntax.Node) (int, bool) {
	if node == nil || node.Kind() != syntax.JsonNumber {
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimSpace(node.Text()))
	return value, err == nil
}

func ScalarText(node *syntax.Node) string {
	if node == nil {
		return ""
	}
	if node.Kind() == syntax.JsonString {
		return StringValue(node)
	}
	return strings.TrimSpace(node.Text())
}
