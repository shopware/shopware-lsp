package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

func parseLocalComponents(
	object *jssyntax.Node,
	lineIndex *cst.LineIndex,
) []VueLocalComponent {
	if object == nil || object.Kind() != jssyntax.JsObject {
		return nil
	}
	var result []VueLocalComponent
	for _, property := range jsquery.Properties(object) {
		name := strings.TrimSpace(jsquery.PropertyName(property))
		nameNode := jsquery.PropertyNameNode(property)
		value := jsquery.PropertyValue(property)
		if name == "" || nameNode == nil {
			continue
		}
		symbol := ""
		shorthand := value == nil && isStaticVueIdentifier(name)
		if shorthand {
			// JavaScript shorthand: components: { SwCard }.
			symbol = name
		} else if value != nil && value.Kind() == jssyntax.JsIdentifier {
			symbol = strings.TrimSpace(jsquery.IdentifierText(value))
		}
		if symbol == "" {
			continue
		}
		if isStaticVueIdentifier(name) {
			name = CamelToKebab(name)
		}
		nameRange := nameNode.RangeTrimmedTrivia()
		quoted := nameNode.Kind() == jssyntax.JsString
		if quoted {
			if inner, ok := jsStringContentRange(nameNode); ok {
				nameRange = inner
			}
		}
		line := 0
		sourceRange := AdminSourceRange{
			Declaration: true,
			Identifier:  !quoted,
		}
		if lineIndex != nil {
			startLine, startCharacter := lineIndex.PositionUTF16(
				nameRange.Start,
			)
			endLine, endCharacter := lineIndex.PositionUTF16(
				nameRange.End,
			)
			line = int(startLine) + 1
			sourceRange.StartLine = int(startLine)
			sourceRange.StartCharacter = int(startCharacter)
			sourceRange.EndLine = int(endLine)
			sourceRange.EndCharacter = int(endCharacter)
		}
		result = append(result, VueLocalComponent{
			Name: name, Symbol: symbol, Line: line,
			NameRange: sourceRange, Shorthand: shorthand, Quoted: quoted,
		})
	}
	return result
}

func parseLocalDirectives(
	object *jssyntax.Node,
	lineIndex *cst.LineIndex,
) []VueLocalDirective {
	if object == nil || object.Kind() != jssyntax.JsObject {
		return nil
	}
	var result []VueLocalDirective
	for _, property := range jsquery.Properties(object) {
		name := strings.TrimSpace(jsquery.PropertyName(property))
		nameNode := jsquery.PropertyNameNode(property)
		if name == "" || nameNode == nil {
			continue
		}
		if isStaticVueIdentifier(name) {
			name = CamelToKebab(name)
		}
		nameRange := nameNode.RangeTrimmedTrivia()
		quoted := nameNode.Kind() == jssyntax.JsString
		if quoted {
			if inner, ok := jsStringContentRange(nameNode); ok {
				nameRange = inner
			}
		}
		line := 0
		sourceRange := AdminSourceRange{
			Declaration: true,
			Identifier:  !quoted,
		}
		if lineIndex != nil {
			startLine, startCharacter := lineIndex.PositionUTF16(nameRange.Start)
			endLine, endCharacter := lineIndex.PositionUTF16(nameRange.End)
			line = int(startLine) + 1
			sourceRange.StartLine = int(startLine)
			sourceRange.StartCharacter = int(startCharacter)
			sourceRange.EndLine = int(endLine)
			sourceRange.EndCharacter = int(endCharacter)
		}
		result = append(result, VueLocalDirective{
			Name: name, Line: line, NameRange: sourceRange,
			Shorthand: jsquery.PropertyValue(property) == nil,
			Quoted:    quoted,
		})
	}
	return result
}

func enrichLocalComponentImports(
	root *jssyntax.Node,
	definition *ComponentDefinition,
) {
	if root == nil || definition == nil {
		return
	}
	for index := range definition.LocalComponents {
		definition.LocalComponents[index].ImportPath = jsquery.ImportPath(
			root,
			definition.LocalComponents[index].Symbol,
		)
	}
}

func enrichDefinitionCollectionElementMembers(
	object *jssyntax.Node,
	definition *ComponentDefinition,
	lineIndex *cst.LineIndex,
) {
	if object == nil || definition == nil {
		return
	}
	for _, call := range jsquery.Calls(object) {
		if jsquery.CallMethodName(call) != "forEach" {
			continue
		}
		receiver, found := staticCallbackCallReceiver(call, "forEach")
		if !found {
			continue
		}
		collectionName, found := directVueInstanceReference(receiver)
		if !found {
			continue
		}
		callback := jsquery.ArgumentExpression(call, 0)
		parameter := callbackFirstParameter(callback)
		if parameter == "" {
			continue
		}
		for _, statement := range jsquery.Nodes(
			callback, jssyntax.JsExpressionStatement,
		) {
			name, expression, assigned := directNamedMemberAssignment(
				statement.Text(), parameter,
			)
			if !assigned {
				continue
			}
			memberType := vueExpressionTextType(expression, nil)
			if memberType == "" {
				memberType = "unknown"
			}
			line := 0
			if lineIndex != nil {
				lineNumber, _ := lineIndex.Position(
					statement.RangeTrimmedTrivia().Start,
				)
				line = int(lineNumber) + 1
			}
			appendDefinitionElementMember(
				definition, collectionName, VueComponentElementMember{
					Name: name, Type: memberType, Line: line,
				},
			)
		}
	}
}

func directVueInstanceReference(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "this.") {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(value, "this."))
	return name, isStaticVueIdentifier(name)
}

func directNamedMemberAssignment(
	value,
	receiver string,
) (string, string, bool) {
	value = strings.TrimSpace(value)
	prefix := receiver + "."
	if !strings.HasPrefix(value, prefix) {
		return "", "", false
	}
	cursor := len(prefix)
	if cursor >= len(value) || !isVueIdentifierStart(value[cursor]) {
		return "", "", false
	}
	start := cursor
	cursor++
	for cursor < len(value) && isVueIdentifierPart(value[cursor]) {
		cursor++
	}
	name := value[start:cursor]
	for cursor < len(value) && isJavaScriptSpace(value[cursor]) {
		cursor++
	}
	if cursor >= len(value) || value[cursor] != '=' ||
		cursor+1 < len(value) && (value[cursor+1] == '=' || value[cursor+1] == '>') {
		return "", "", false
	}
	expression := trimVueSourceExpression(value[cursor+1:])
	return name, expression, expression != ""
}

func appendDefinitionElementMember(
	definition *ComponentDefinition,
	collectionName string,
	elementMember VueComponentElementMember,
) {
	for memberIndex := range definition.Members {
		member := &definition.Members[memberIndex]
		if member.Name != collectionName {
			continue
		}
		for elementIndex := range member.ElementMembers {
			if member.ElementMembers[elementIndex].Name == elementMember.Name {
				member.ElementMembers[elementIndex] = elementMember
				return
			}
		}
		member.ElementMembers = append(member.ElementMembers, elementMember)
		return
	}
}

func parseComponentAssignments(
	object *jssyntax.Node,
	lineIndex *cst.LineIndex,
) []VueComponentAssignment {
	initializers := componentConstInitializers(object)
	var result []VueComponentAssignment
	for _, statement := range jsquery.Nodes(
		object, jssyntax.JsExpressionStatement,
	) {
		target, expression, found := directVueInstanceAssignment(statement.Text())
		if !found {
			continue
		}
		if local, localFound := visibleComponentConstInitializer(
			statement, expression, initializers,
		); localFound {
			expression = local
		} else if callback, callbackFound := componentPromiseCallbackExpression(
			statement, expression,
		); callbackFound {
			expression = callback
		}
		line, _ := lineIndex.Position(statement.RangeTrimmedTrivia().Start)
		result = append(result, VueComponentAssignment{
			Target: target, Expression: expression, Line: int(line) + 1,
		})
	}
	return result
}

type componentConstInitializer struct {
	name       string
	expression string
	start      uint32
	function   *jssyntax.Node
	block      *jssyntax.Node
}

func componentConstInitializers(
	object *jssyntax.Node,
) []componentConstInitializer {
	var result []componentConstInitializer
	for _, declaration := range jsquery.Nodes(
		object, jssyntax.JsVariableDeclaration,
	) {
		name, expression, found := directComponentConstInitializer(
			declaration.Text(),
		)
		if !found {
			continue
		}
		function := closestJavaScriptFunctionScope(declaration)
		block := closestJavaScriptBlockScope(declaration, function)
		if function == nil || block == nil {
			continue
		}
		result = append(result, componentConstInitializer{
			name: name, expression: expression,
			start:    declaration.RangeTrimmedTrivia().Start,
			function: function, block: block,
		})
	}
	return result
}

func directComponentConstInitializer(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "const") ||
		len(value) == len("const") || !isJavaScriptSpace(value[len("const")]) {
		return "", "", false
	}
	cursor := len("const")
	for cursor < len(value) && isJavaScriptSpace(value[cursor]) {
		cursor++
	}
	if cursor >= len(value) || !isVueIdentifierStart(value[cursor]) {
		return "", "", false
	}
	start := cursor
	cursor++
	for cursor < len(value) && isVueIdentifierPart(value[cursor]) {
		cursor++
	}
	name := value[start:cursor]
	state := slotScanState{}
	for ; cursor < len(value); cursor++ {
		current := value[cursor]
		if state.topLevel() && current == ',' {
			// Multiple declarators require binding-aware parsing; skip them.
			return "", "", false
		}
		if state.topLevel() && current == '=' &&
			(cursor+1 >= len(value) || value[cursor+1] != '>') {
			right := value[cursor+1:]
			rightState := slotScanState{}
			for index := range len(right) {
				if rightState.topLevel() && right[index] == ',' {
					return "", "", false
				}
				rightState.consume(right[index])
			}
			expression := trimVueSourceExpression(right)
			return name, expression, expression != ""
		}
		state.consume(current)
	}
	return "", "", false
}

func visibleComponentConstInitializer(
	use *jssyntax.Node,
	identifier string,
	initializers []componentConstInitializer,
) (string, bool) {
	identifier = strings.TrimSpace(identifier)
	if !isStaticVueIdentifier(identifier) || use == nil {
		return "", false
	}
	function := closestJavaScriptFunctionScope(use)
	if function == nil {
		return "", false
	}
	blocks := visibleJavaScriptBlockScopes(use, function)
	useStart := use.RangeTrimmedTrivia().Start
	bestDepth := len(blocks) + 1
	var best componentConstInitializer
	found := false
	for _, initializer := range initializers {
		depth, visible := blocks[initializer.block]
		if initializer.name != identifier || initializer.function != function ||
			!visible || initializer.start >= useStart {
			continue
		}
		if !found || depth < bestDepth ||
			depth == bestDepth && initializer.start > best.start {
			best = initializer
			bestDepth = depth
			found = true
		}
	}
	return best.expression, found
}

func closestJavaScriptFunctionScope(node *jssyntax.Node) *jssyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		switch current.Kind() {
		case jssyntax.JsMethod, jssyntax.JsFunction, jssyntax.JsArrowFunction:
			return current
		}
	}
	return nil
}

func closestJavaScriptBlockScope(
	node,
	function *jssyntax.Node,
) *jssyntax.Node {
	for current := node.Parent(); current != nil; current = current.Parent() {
		if current.Kind() == jssyntax.JsBlock {
			return current
		}
		if current == function {
			break
		}
	}
	return nil
}

func visibleJavaScriptBlockScopes(
	node,
	function *jssyntax.Node,
) map[*jssyntax.Node]int {
	result := make(map[*jssyntax.Node]int)
	depth := 0
	for current := node.Parent(); current != nil; current = current.Parent() {
		if current.Kind() == jssyntax.JsBlock {
			result[current] = depth
			depth++
		}
		if current == function {
			break
		}
	}
	return result
}

func isStaticVueIdentifier(value string) bool {
	if value == "" || !isVueIdentifierStart(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !isVueIdentifierPart(value[index]) {
			return false
		}
	}
	return true
}

func componentPromiseCallbackExpression(
	use *jssyntax.Node,
	identifier string,
) (string, bool) {
	identifier = strings.TrimSpace(identifier)
	if !isStaticVueIdentifier(identifier) || use == nil {
		return "", false
	}
	function := closestJavaScriptFunctionScope(use)
	if function == nil || function.Kind() != jssyntax.JsArrowFunction ||
		callbackFirstParameter(function) != identifier {
		return "", false
	}
	argument, call, argumentIndex := callbackArgumentCall(function)
	if argument == nil || call == nil || argumentIndex != 0 ||
		jsquery.CallMethodName(call) != "then" {
		return "", false
	}
	receiver, found := staticCallbackCallReceiver(call, "then")
	if !found {
		return "", false
	}
	return "await " + receiver, true
}

func callbackArgumentCall(
	function *jssyntax.Node,
) (*jssyntax.Node, *jssyntax.Node, int) {
	for current := function.Parent(); current != nil; current = current.Parent() {
		if current.Kind() != jssyntax.JsArgument {
			continue
		}
		list := current.Parent()
		if list == nil || list.Kind() != jssyntax.JsArgumentList {
			return nil, nil, -1
		}
		call := list.Parent()
		if call == nil || call.Kind() != jssyntax.JsCallExpression {
			return nil, nil, -1
		}
		for index, argument := range jsquery.Arguments(call) {
			if argument == current {
				return current, call, index
			}
		}
		return nil, nil, -1
	}
	return nil, nil, -1
}

func callbackFirstParameter(function *jssyntax.Node) string {
	if function == nil {
		return ""
	}
	text := strings.TrimSpace(function.Text())
	parameters := ""
	if function.Kind() == jssyntax.JsArrowFunction {
		arrow := strings.Index(text, "=>")
		if arrow < 0 {
			return ""
		}
		left := strings.TrimSpace(text[:arrow])
		if strings.HasPrefix(left, "async ") {
			left = strings.TrimSpace(strings.TrimPrefix(left, "async "))
		}
		if strings.HasPrefix(left, "(") &&
			matchingSlotDelimiter(left, 0, '(', ')') == len(left)-1 {
			parameters = strings.TrimSpace(left[1 : len(left)-1])
		} else {
			parameters = left
		}
	} else {
		var found bool
		parameters, _, found = vueMethodHeader(text)
		if !found {
			return ""
		}
	}
	parts := splitAdminTypeTopLevel(parameters, ',')
	if len(parts) == 0 {
		return ""
	}
	parameter := strings.TrimSpace(parts[0])
	if strings.HasPrefix(parameter, "...") {
		parameter = strings.TrimSpace(strings.TrimPrefix(parameter, "..."))
	}
	if parameter == "" || !isVueIdentifierStart(parameter[0]) {
		return ""
	}
	cursor := 1
	for cursor < len(parameter) && isVueIdentifierPart(parameter[cursor]) {
		cursor++
	}
	if cursor < len(parameter) && !isJavaScriptSpace(parameter[cursor]) &&
		parameter[cursor] != ':' && parameter[cursor] != '?' &&
		parameter[cursor] != '=' {
		return ""
	}
	return parameter[:cursor]
}

func staticCallbackCallReceiver(
	call *jssyntax.Node,
	method string,
) (string, bool) {
	if call == nil || method == "" {
		return "", false
	}
	cursor := call.ChildNodeCursor()
	if !cursor.Next() {
		return "", false
	}
	callee := cursor.Node()
	if callee.Kind() != jssyntax.JsMemberExpression {
		return "", false
	}
	text := strings.TrimSpace(callee.Text())
	end := len(text)
	for end > 0 && isJavaScriptSpace(text[end-1]) {
		end--
	}
	start := end
	for start > 0 && isVueIdentifierPart(text[start-1]) {
		start--
	}
	if text[start:end] != method {
		return "", false
	}
	separator := start
	for separator > 0 && isJavaScriptSpace(text[separator-1]) {
		separator--
	}
	if separator == 0 || text[separator-1] != '.' {
		return "", false
	}
	separator--
	if separator > 0 && text[separator-1] == '?' {
		separator--
	}
	receiver := strings.TrimSpace(text[:separator])
	return receiver, receiver != ""
}

func directVueInstanceAssignment(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "this.") {
		return "", "", false
	}
	cursor := len("this.")
	if cursor >= len(value) || !isVueIdentifierStart(value[cursor]) {
		return "", "", false
	}
	start := cursor
	cursor++
	for cursor < len(value) && isVueIdentifierPart(value[cursor]) {
		cursor++
	}
	target := value[start:cursor]
	for cursor < len(value) && isJavaScriptSpace(value[cursor]) {
		cursor++
	}
	if cursor >= len(value) || value[cursor] != '=' ||
		cursor+1 < len(value) && (value[cursor+1] == '=' || value[cursor+1] == '>') {
		return "", "", false
	}
	expression := trimVueSourceExpression(value[cursor+1:])
	if expression == "" {
		return "", "", false
	}
	return target, expression, true
}
