package snippet

import (
	"sort"
	"strings"

	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

// AdminJavaScriptStringReference reports whether node belongs to a static
// Administration snippet reference. Besides translation calls, Shopware
// module manifests store snippet keys directly in title, description, and
// navigation label properties.
func AdminJavaScriptStringReference(node *jssyntax.Node) bool {
	stringNode := jsquery.StringAt(node)
	if stringNode == nil || !isStaticAdminJavaScriptString(stringNode) {
		return false
	}
	if jsquery.StringArgumentIndex(node) == 0 &&
		isAdminJavaScriptSnippetCall(jsquery.CallAt(node)) {
		return true
	}
	return isAdminModuleSnippetProperty(stringNode)
}

// AdminJavaScriptStringReferences returns complete static keys from every
// supported Administration JavaScript translation call and module manifest.
func AdminJavaScriptStringReferences(root *jssyntax.Node) []*jssyntax.Node {
	var result []*jssyntax.Node
	for call := range jsquery.IterateCalls(root) {
		if !isAdminJavaScriptSnippetCall(call) {
			continue
		}
		argument := jsquery.ArgumentExpression(call, 0)
		if argument == nil || argument.Kind() != jssyntax.JsString ||
			!isStaticAdminJavaScriptString(argument) {
			continue
		}
		result = append(result, argument)
	}
	for _, stringNode := range jsquery.Nodes(root, jssyntax.JsString) {
		if isStaticAdminJavaScriptString(stringNode) &&
			isAdminModuleSnippetProperty(stringNode) {
			result = append(result, stringNode)
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		return result[left].Range().Start < result[right].Range().Start
	})
	return result
}

func isAdminModuleSnippetProperty(stringNode *jssyntax.Node) bool {
	property := jsquery.PropertyAt(stringNode)
	if property == nil || jsquery.PropertyValue(property) != stringNode {
		return false
	}
	call := jsquery.CallAt(stringNode)
	if call == nil {
		return false
	}
	switch jsquery.CallName(call) {
	case "Module.register", "Shopware.Module.register":
	default:
		return false
	}
	config := jsquery.ArgumentExpression(call, 1)
	if config == nil || config.Kind() != jssyntax.JsObject {
		return false
	}
	propertyName := jsquery.PropertyName(property)
	if property.Parent() == config {
		return propertyName == "title" || propertyName == "description"
	}
	if propertyName != "label" {
		return false
	}
	for current := property.Parent(); current != nil && current != config; current = current.Parent() {
		if current.Kind() == jssyntax.JsProperty &&
			current.Parent() == config &&
			jsquery.PropertyName(current) == "navigation" {
			return true
		}
	}
	return false
}

func isStaticAdminJavaScriptString(node *jssyntax.Node) bool {
	text := strings.TrimSpace(node.Text())
	if len(text) < 2 || text[0] != '`' {
		return true
	}
	for offset := 1; offset+1 < len(text); offset++ {
		if text[offset] != '$' || text[offset+1] != '{' {
			continue
		}
		backslashes := 0
		for cursor := offset - 1; cursor >= 0 && text[cursor] == '\\'; cursor-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return false
		}
	}
	return true
}

func isAdminJavaScriptSnippetCall(call *jssyntax.Node) bool {
	if call == nil {
		return false
	}
	switch jsquery.CallMethodName(call) {
	case "$t", "$tc":
		return true
	}
	switch jsquery.CallName(call) {
	case "Shopware.Snippet.t", "Shopware.Snippet.tc":
		return true
	default:
		return false
	}
}
