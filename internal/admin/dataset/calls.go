package dataset

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

// SelectorMode describes whether a dataset call's selectors can be read
// literally.
type SelectorMode int

const (
	SelectorsAbsent SelectorMode = iota
	SelectorsLiteral
	SelectorsDynamic
)

// Call is one data.subscribe or data.get invocation.
type Call struct {
	Method        string
	ID            string
	Selectors     []string
	SelectorsMode SelectorMode
	IDRange       cst.TextRange
	Range         cst.TextRange
	Node          *cst.Node
}

type sdkBinding struct {
	data       map[string]bool
	namespaces map[string]bool
}

// Calls finds literal Admin SDK dataset reads. Receiver names other than
// data are recognized when imported from the Meteor Admin SDK.
func Calls(root *cst.Node) []Call {
	if root == nil {
		return nil
	}
	binding := sdkImportsFrom(root)
	var result []Call
	for _, call := range jsquery.Calls(root) {
		method, matched := datasetMethod(call, binding)
		if !matched {
			continue
		}
		result = append(result, describeCall(call, method))
	}
	return result
}

func datasetMethod(call *cst.Node, binding sdkBinding) (string, bool) {
	method := jsquery.CallMethodName(call)
	if method != "subscribe" && method != "get" {
		return "", false
	}
	callee := jsquery.CallCallee(call)
	if callee == nil || callee.Kind() != jssyntax.JsMemberExpression {
		return "", false
	}
	names := memberIdentifiers(callee)
	if len(names) < 2 || names[len(names)-1] != method {
		return "", false
	}
	if len(names) == 2 && (names[0] == "data" || binding.data[names[0]]) {
		return method, true
	}
	if names[len(names)-2] == "data" && binding.namespaces[names[0]] {
		return method, true
	}
	return "", false
}

func describeCall(call *cst.Node, method string) Call {
	described := Call{
		Method:        method,
		SelectorsMode: SelectorsAbsent,
		Range:         call.RangeTrimmedTrivia(),
		Node:          call,
	}
	switch method {
	case "subscribe":
		describeSubscribe(&described, call)
	default:
		describeGet(&described, call)
	}
	if described.IDRange == (cst.TextRange{}) {
		described.IDRange = described.Range
	}
	return described
}

func describeSubscribe(described *Call, call *cst.Node) {
	if id, idRange, literal := literalArgumentString(call, 0); literal {
		described.ID = id
		described.IDRange = idRange
	}
	options := jsquery.ArgumentExpression(call, 2)
	if options == nil {
		return
	}
	applySelectorOptions(described, options)
}

func describeGet(described *Call, call *cst.Node) {
	options := jsquery.ArgumentExpression(call, 0)
	if options == nil || options.Kind() != jssyntax.JsObject {
		described.SelectorsMode = SelectorsDynamic
		return
	}
	if id, idRange, literal := literalPropertyString(options, "id"); literal {
		described.ID = id
		described.IDRange = idRange
	}
	applySelectorProperty(described, options)
}

func applySelectorOptions(described *Call, options *cst.Node) {
	if options.Kind() != jssyntax.JsObject {
		described.SelectorsMode = SelectorsDynamic
		return
	}
	applySelectorProperty(described, options)
}

func applySelectorProperty(described *Call, object *cst.Node) {
	property := jsquery.Property(object, "selectors")
	if property == nil {
		described.SelectorsMode = SelectorsAbsent
		return
	}
	values, literal := literalStringArray(jsquery.PropertyValue(property))
	if !literal {
		described.SelectorsMode = SelectorsDynamic
		return
	}
	described.Selectors = values
	described.SelectorsMode = SelectorsLiteral
}

func literalArgumentString(call *cst.Node, index int) (string, cst.TextRange, bool) {
	return literalStringRange(jsquery.ArgumentExpression(call, index))
}

func literalPropertyString(
	object *cst.Node,
	name string,
) (string, cst.TextRange, bool) {
	return literalStringRange(jsquery.PropertyValue(jsquery.Property(object, name)))
}

func literalStringRange(node *cst.Node) (string, cst.TextRange, bool) {
	value, literal := literalJavaScriptString(node)
	if !literal {
		return "", cst.TextRange{}, false
	}
	return value, stringContentRange(node), true
}

func literalStringArray(node *cst.Node) ([]string, bool) {
	if node == nil || node.Kind() != jssyntax.JsArray {
		return nil, false
	}
	items := jsquery.ArrayItems(node)
	values := make([]string, 0, len(items))
	for _, item := range items {
		value, literal := literalJavaScriptString(item)
		if !literal {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

func stringContentRange(node *cst.Node) cst.TextRange {
	rangeValue := node.RangeTrimmedTrivia()
	text := node.Text()
	if len(text) >= 2 {
		quote := text[0]
		if (quote == '\'' || quote == '"' || quote == '`') && text[len(text)-1] == quote {
			rangeValue.Start++
			rangeValue.End--
		}
	}
	return rangeValue
}

func memberIdentifiers(node *cst.Node) []string {
	var names []string
	var walk func(*cst.Node)
	walk = func(current *cst.Node) {
		if current == nil {
			return
		}
		if current.Kind() == jssyntax.JsIdentifier {
			names = append(names, jsquery.IdentifierText(current))
			return
		}
		if current.Kind() != jssyntax.JsMemberExpression {
			return
		}
		cursor := current.ChildNodeCursor()
		for cursor.Next() {
			child := cursor.Node()
			if child.Kind() == jssyntax.JsIdentifier ||
				child.Kind() == jssyntax.JsMemberExpression {
				walk(child)
			}
		}
	}
	walk(node)
	return names
}

func sdkImportsFrom(root *cst.Node) sdkBinding {
	binding := sdkBinding{
		data:       map[string]bool{},
		namespaces: map[string]bool{},
	}
	for _, statement := range jsquery.Nodes(root, jssyntax.JsImportStatement) {
		module := ""
		for _, literal := range jsquery.Nodes(statement, jssyntax.JsString) {
			module = jsquery.StringValue(literal)
			break
		}
		if !adminSDKModule(module) {
			continue
		}
		bindSDKImport(importTokenTexts(statement), &binding)
	}
	return binding
}

func adminSDKModule(module string) bool {
	module = strings.ToLower(strings.TrimSpace(module))
	return strings.Contains(module, "meteor-admin-sdk") ||
		strings.Contains(module, "admin-extension-sdk") ||
		strings.Contains(module, "@shopware-ag/admin-sdk")
}

func importTokenTexts(statement *cst.Node) []string {
	var tokens []string
	for element := range statement.Descendants() {
		token, ok := element.(*cst.Token)
		if !ok || token.Kind().IsTrivia() {
			continue
		}
		tokens = append(tokens, token.Text())
	}
	return tokens
}

func bindSDKImport(tokens []string, binding *sdkBinding) {
	if len(tokens) >= 2 && tokens[1] == "type" {
		return
	}
	for index := 0; index+2 < len(tokens); index++ {
		if tokens[index] == "*" && tokens[index+1] == "as" && isBindingName(tokens[index+2]) {
			binding.namespaces[tokens[index+2]] = true
		}
	}
	if len(tokens) >= 2 && tokens[0] == "import" && isBindingName(tokens[1]) {
		binding.namespaces[tokens[1]] = true
	}
	inBrace := false
	for index := 0; index < len(tokens); index++ {
		switch tokens[index] {
		case "{":
			inBrace = true
		case "}":
			inBrace = false
		default:
			if !inBrace || !isBindingName(tokens[index]) || tokens[index] == "as" {
				continue
			}
			imported := tokens[index]
			local := imported
			if index+2 < len(tokens) && tokens[index+1] == "as" && isBindingName(tokens[index+2]) {
				local = tokens[index+2]
				index += 2
			}
			if imported == "data" {
				binding.data[local] = true
			}
		}
	}
}

func isBindingName(token string) bool {
	if token == "" || token == "as" || token == "from" || token == "import" || token == "type" {
		return false
	}
	first := token[0]
	return first == '_' || first == '$' ||
		(first >= 'a' && first <= 'z') ||
		(first >= 'A' && first <= 'Z')
}
