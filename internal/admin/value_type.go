package admin

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

var (
	vueJSDocTypePattern   = regexp.MustCompile(`@type\s*\{([^}]+)\}`)
	vueJSDocReturnPattern = regexp.MustCompile(`@returns?\s*\{([^}]+)\}`)
)

// enrichDefinitionMemberTypes retains type information already present in an
// Options API component. The JavaScript CST remains lossless, so lightweight
// header/value inspection can recover TypeScript annotations and JSDoc without
// requiring a second TypeScript compiler in the LSP process.
func enrichDefinitionMemberTypes(
	object *jssyntax.Node,
	definition *ComponentDefinition,
) {
	if object == nil || definition == nil {
		return
	}
	known := make(map[string]string)
	for _, prop := range definition.Props {
		known[prop.Name] = prop.Type
	}

	if data := jsquery.Property(object, "data"); data != nil {
		declared := vueMethodReturnObjectMemberTypes(data)
		for _, property := range jsquery.Properties(firstObject(data)) {
			name := jsquery.PropertyName(property)
			if name == "" {
				continue
			}
			memberType := declared[name]
			openRuntimeShape := false
			if memberType == "" {
				memberType = vuePropertyValueType(property, known)
				openRuntimeShape = vuePropertyHasInferredObjectShape(property)
			}
			setDefinitionMemberInference(
				definition,
				ComponentMemberData,
				name,
				memberType,
				vuePropertySourceExpression(property),
				openRuntimeShape,
			)
			if memberType != "" {
				known[name] = memberType
			}
		}
	}

	if computed := jsquery.Property(object, "computed"); computed != nil {
		computedObject := jsquery.PropertyValue(computed)
		for _, method := range jsquery.Properties(computedObject) {
			name := jsquery.PropertyName(method)
			if name == "" {
				continue
			}
			memberType := vueMethodReturnType(method, known)
			setDefinitionMemberInference(
				definition,
				ComponentMemberComputed,
				name,
				memberType,
				vueMethodReturnExpression(method),
				vueMethodHasInferredObjectShape(method),
			)
			setDefinitionMemberReturns(
				definition, ComponentMemberComputed, name, method,
			)
			setDefinitionMemberCMSRegistry(
				definition, ComponentMemberComputed, name, method,
			)
			if memberType != "" {
				known[name] = memberType
			}
		}
	}

	if methods := jsquery.Property(object, "methods"); methods != nil {
		methodsObject := jsquery.PropertyValue(methods)
		for _, method := range jsquery.Properties(methodsObject) {
			name := jsquery.PropertyName(method)
			if name == "" {
				continue
			}
			setDefinitionMemberInference(
				definition,
				ComponentMemberMethod,
				name,
				vueMethodSignature(method, known),
				vueMethodReturnExpression(method),
				vueMethodHasInferredObjectShape(method),
			)
			setDefinitionMemberReturns(
				definition, ComponentMemberMethod, name, method,
			)
			setDefinitionMemberCMSRegistry(
				definition, ComponentMemberMethod, name, method,
			)
		}
	}
}

func setDefinitionMemberCMSRegistry(
	definition *ComponentDefinition,
	kind VueComponentMemberKind,
	name string,
	method *jssyntax.Node,
) {
	if definition == nil || method == nil {
		return
	}
	registryKind, found := cmsRegistryKindFromExpression(method.Text())
	if !found {
		return
	}
	for index := range definition.Members {
		member := &definition.Members[index]
		if member.Kind == kind && member.Name == name {
			member.CMSRegistryKind = registryKind
			return
		}
	}
}

func setDefinitionMemberReturns(
	definition *ComponentDefinition,
	kind VueComponentMemberKind,
	name string,
	method *jssyntax.Node,
) {
	if definition == nil || method == nil {
		return
	}
	for index := range definition.Members {
		member := &definition.Members[index]
		if member.Kind != kind || member.Name != name {
			continue
		}
		member.ReturnExpressions = vueMethodReturnExpressions(method)
		member.ReturnsComplete = vueMethodReturnsComplete(method)
		return
	}
}

func vueMethodReturnObjectMemberTypes(
	method *jssyntax.Node,
) map[string]string {
	result := make(map[string]string)
	if method == nil {
		return result
	}
	_, returnType, found := vueMethodHeader(method.Text())
	if !found || returnType == "" {
		return result
	}
	for _, member := range VueTypeMembers(returnType) {
		if member.Name != "" && member.Type != "" {
			result[member.Name] = member.Type
		}
	}
	return result
}

func setDefinitionMemberInference(
	definition *ComponentDefinition,
	kind VueComponentMemberKind,
	name,
	memberType,
	sourceExpression string,
	openRuntimeShape bool,
) {
	for index := range definition.Members {
		member := &definition.Members[index]
		if member.Kind == kind && member.Name == name {
			if memberType != "" {
				member.Type = memberType
			}
			member.SourceExpression = sourceExpression
			member.OpenRuntimeShape = openRuntimeShape
			return
		}
	}
}

func vuePropertyHasInferredObjectShape(property *jssyntax.Node) bool {
	if property == nil ||
		vueJSDocTypePattern.MatchString(property.Text()) ||
		vueTypeAssertion(property.Text()) != "" {
		return false
	}
	value := jsquery.PropertyValue(property)
	return value != nil && value.Kind() == jssyntax.JsObject
}

func vueMethodHasInferredObjectShape(method *jssyntax.Node) bool {
	if method == nil || vueJSDocReturnPattern.MatchString(method.Text()) {
		return false
	}
	_, returnType, found := vueMethodHeader(method.Text())
	if found && returnType != "" {
		return false
	}
	return vueRuntimeObjectLiteralExpression(vueMethodReturnExpression(method))
}

func vueRuntimeObjectLiteralExpression(value string) bool {
	value = trimVueSourceExpression(value)
	if value == "" || vueTypeAssertion(value) != "" {
		return false
	}
	for len(value) >= 2 && value[0] == '(' &&
		matchingSlotDelimiter(value, 0, '(', ')') == len(value)-1 {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	return strings.HasPrefix(value, "{")
}

func vuePropertySourceExpression(property *jssyntax.Node) string {
	value := jsquery.PropertyValue(property)
	if value == nil {
		return ""
	}
	return trimVueSourceExpression(value.Text())
}

func vuePropertyValueType(
	property *jssyntax.Node,
	known map[string]string,
) string {
	if property == nil {
		return ""
	}
	if match := vueJSDocTypePattern.FindStringSubmatch(property.Text()); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	if asserted := vueTypeAssertion(property.Text()); asserted != "" {
		return asserted
	}
	return vueExpressionType(jsquery.PropertyValue(property), known)
}

func vueExpressionType(
	node *jssyntax.Node,
	known map[string]string,
) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case jssyntax.JsString:
		return "string"
	case jssyntax.JsNumber:
		return "number"
	case jssyntax.JsBoolean:
		return "boolean"
	case jssyntax.JsNull:
		return "null"
	case jssyntax.JsArray:
		var elementTypes []string
		seen := make(map[string]bool)
		for _, item := range jsquery.ArrayItems(node) {
			itemType := vueExpressionType(item, known)
			if itemType == "" || seen[itemType] {
				continue
			}
			seen[itemType] = true
			elementTypes = append(elementTypes, itemType)
		}
		if len(elementTypes) == 0 {
			return "Array"
		}
		return "Array<" + strings.Join(elementTypes, " | ") + ">"
	case jssyntax.JsObject:
		var fields []string
		for _, property := range jsquery.Properties(node) {
			name := jsquery.PropertyName(property)
			if name == "" {
				continue
			}
			fieldType := vuePropertyValueType(property, known)
			if fieldType == "" {
				fieldType = "unknown"
			}
			fields = append(fields, name+": "+fieldType)
		}
		if len(fields) == 0 {
			return "Object"
		}
		return "{ " + strings.Join(fields, "; ") + " }"
	case jssyntax.JsMemberExpression, jssyntax.JsIdentifier:
		value := compactVueExpression(node.Text())
		if strings.HasPrefix(value, "this.") {
			name := strings.TrimPrefix(value, "this.")
			if !strings.ContainsAny(name, ".[()?") {
				return known[name]
			}
		}
	case jssyntax.JsArrowFunction, jssyntax.JsFunction:
		return "Function"
	}
	return ""
}

func vueTypeAssertion(value string) string {
	value = strings.TrimSpace(value)
	position := -1
	state := slotScanState{}
	for index := 0; index+4 <= len(value); index++ {
		if state.topLevel() && value[index:index+4] == " as " {
			position = index + 4
		}
		state.consume(value[index])
	}
	if position < 0 || position >= len(value) {
		return ""
	}
	asserted := strings.TrimSpace(value[position:])
	asserted = strings.TrimSuffix(asserted, ",")
	return strings.TrimSpace(asserted)
}

func vueMethodReturnType(
	method *jssyntax.Node,
	known map[string]string,
) string {
	if method == nil {
		return ""
	}
	method = vueMethodImplementation(method)
	text := method.Text()
	if match := vueJSDocReturnPattern.FindStringSubmatch(text); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	if _, returnType, found := vueMethodHeader(text); found && returnType != "" {
		return returnType
	}
	expressions := vueMethodReturnExpressions(method)
	if len(expressions) == 0 {
		return ""
	}
	var result string
	for _, expression := range expressions {
		inferred := vueExpressionTextType(expression, known)
		if inferred == "" {
			// One unresolved branch makes the complete return contract open. A
			// known fallback must not become the type of the whole function.
			return ""
		}
		result = mergeVueTypes(result, inferred)
	}
	return result
}

func vueMethodDeclaredReturnType(method *jssyntax.Node) string {
	if method == nil {
		return ""
	}
	method = vueMethodImplementation(method)
	_, returnType, found := vueMethodHeader(method.Text())
	if !found {
		return ""
	}
	return strings.TrimSpace(returnType)
}

// vueMethodReturnExpression returns one unconditional top-level return value.
// Branch-dependent and multiple returns are intentionally ignored: retaining
// one of them as authoritative would make markup diagnostics unsound.
func vueMethodReturnExpression(method *jssyntax.Node) string {
	expressions := vueMethodReturnExpressions(method)
	if len(expressions) != 1 {
		return ""
	}
	return expressions[0]
}

// vueMethodReturnExpressions collects returns owned by the method while
// excluding callbacks and nested function declarations. Keeping every branch
// prevents a fallback such as `return false` from being treated as the whole
// return type when an earlier branch returns an array or object.
func vueMethodReturnExpressions(method *jssyntax.Node) []string {
	if method == nil {
		return nil
	}
	method = vueMethodImplementation(method)
	var block *jssyntax.Node
	cursor := method.ChildNodeCursor()
	for cursor.Next() {
		if child := cursor.Node(); child.Kind() == jssyntax.JsBlock {
			block = child
			break
		}
	}
	if block != nil {
		var result []string
		var collect func(*jssyntax.Node) bool
		collect = func(node *jssyntax.Node) bool {
			cursor := node.ChildNodeCursor()
			for cursor.Next() {
				child := cursor.Node()
				switch child.Kind() {
				case jssyntax.JsArrowFunction, jssyntax.JsFunction:
					// This return belongs to a callback or nested function.
					continue
				case jssyntax.JsReturnStatement:
					expression := vueReturnStatementExpression(child)
					if expression == "" {
						return false
					}
					result = append(result, expression)
				case jssyntax.JsExpressionStatement:
					// The deliberately small JavaScript parser represents switch
					// case bodies as expression statements. Recover their return
					// values losslessly while still excluding nested functions.
					loose := vueLooseReturnExpressions(child)
					if len(loose) > 0 {
						result = append(result, loose...)
						continue
					}
					if !collect(child) {
						return false
					}
				default:
					if !collect(child) {
						return false
					}
				}
			}
			return true
		}
		if !collect(block) {
			return nil
		}
		return result
	}

	// Expression-bodied computed properties are also common in typed wrapper
	// configurations. The parser keeps their arrow body losslessly.
	if method.Kind() != jssyntax.JsArrowFunction {
		return nil
	}
	text := method.Text()
	arrow := strings.Index(text, "=>")
	if arrow < 0 {
		return nil
	}
	body := strings.TrimSpace(text[arrow+2:])
	if body == "" || body[0] == '{' {
		return nil
	}
	return []string{trimVueSourceExpression(body)}
}

func vueLooseReturnExpressions(node *jssyntax.Node) []string {
	if node == nil {
		return nil
	}
	tokens := vueSyntaxTokens(node)
	var result []string
	for index, token := range tokens {
		if token.Kind() != jssyntax.TkKeyword || token.Text() != "return" {
			continue
		}
		end := node.Range().End
		for next := index + 1; next < len(tokens); next++ {
			if tokens[next].Kind() == jssyntax.TkSemicolon {
				end = tokens[next].Range().Start
				break
			}
		}
		start := token.Range().End
		if start > end || start < node.Range().Start || end > node.Range().End {
			continue
		}
		text := node.Text()
		relativeStart := start - node.Range().Start
		relativeEnd := end - node.Range().Start
		if relativeEnd > uint32(len(text)) {
			continue
		}
		if expression := trimVueSourceExpression(
			text[relativeStart:relativeEnd],
		); expression != "" {
			result = append(result, expression)
		}
	}
	return result
}

// vueMethodReturnsComplete proves the deliberately small control-flow shapes
// used by Administration component selectors: an expression-bodied arrow, an
// unconditional final return, or a terminal switch with a default branch in
// which every case path reaches a return. Anything more involved stays open.
func vueMethodReturnsComplete(method *jssyntax.Node) bool {
	if method == nil {
		return false
	}
	method = vueMethodImplementation(method)
	block := directJavaScriptBlock(method)
	if block == nil {
		return method.Kind() == jssyntax.JsArrowFunction &&
			len(vueMethodReturnExpressions(method)) == 1
	}
	tokens := vueControlTokens(block)
	if len(tokens) == 0 {
		return false
	}
	if vueTokensEndWithTopLevelReturn(tokens) {
		return true
	}
	return vueTokensEndWithCompleteSwitch(tokens)
}

type vueControlToken struct {
	text  string
	kind  jssyntax.Kind
	depth int
}

func directJavaScriptBlock(method *jssyntax.Node) *jssyntax.Node {
	if method == nil {
		return nil
	}
	cursor := method.ChildNodeCursor()
	for cursor.Next() {
		if child := cursor.Node(); child.Kind() == jssyntax.JsBlock {
			return child
		}
	}
	return nil
}

func vueControlTokens(block *jssyntax.Node) []vueControlToken {
	if block == nil {
		return nil
	}
	depth := 0
	var result []vueControlToken
	for _, token := range vueSyntaxTokens(block) {
		switch token.Kind() {
		case jssyntax.TkWhitespace, jssyntax.TkLineBreak,
			jssyntax.TkLineComment, jssyntax.TkBlockComment:
			continue
		case jssyntax.TkOpenBrace:
			depth++
			result = append(result, vueControlToken{
				text: token.Text(), kind: token.Kind(), depth: depth,
			})
		case jssyntax.TkCloseBrace:
			result = append(result, vueControlToken{
				text: token.Text(), kind: token.Kind(), depth: depth,
			})
			depth--
		default:
			result = append(result, vueControlToken{
				text: token.Text(), kind: token.Kind(), depth: depth,
			})
		}
	}
	return result
}

func vueSyntaxTokens(node *jssyntax.Node) []*jssyntax.Token {
	var result []*jssyntax.Token
	var visit func(*jssyntax.Node)
	visit = func(current *jssyntax.Node) {
		for element := range current.ChildElements() {
			switch child := element.(type) {
			case *jssyntax.Node:
				visit(child)
			case *jssyntax.Token:
				result = append(result, child)
			}
		}
	}
	if node != nil {
		visit(node)
	}
	return result
}

func vueTokensEndWithTopLevelReturn(tokens []vueControlToken) bool {
	returnIndex := -1
	for index, token := range tokens {
		if token.kind == jssyntax.TkKeyword && token.text == "return" &&
			token.depth == 1 {
			returnIndex = index
		}
	}
	if returnIndex < 0 {
		return false
	}
	for index := returnIndex + 1; index < len(tokens); index++ {
		token := tokens[index]
		if token.depth > 1 || token.kind == jssyntax.TkSemicolon ||
			token.kind == jssyntax.TkCloseBrace && token.depth == 1 {
			continue
		}
		// Tokens in the return expression are expected before its semicolon.
		// Once that semicolon is seen, no further top-level statement is safe.
		semicolon := false
		for before := returnIndex + 1; before < index; before++ {
			if tokens[before].kind == jssyntax.TkSemicolon &&
				tokens[before].depth == 1 {
				semicolon = true
				break
			}
		}
		if semicolon {
			return false
		}
	}
	return true
}

func vueTokensEndWithCompleteSwitch(tokens []vueControlToken) bool {
	switchIndex := -1
	for index, token := range tokens {
		if token.kind == jssyntax.TkKeyword && token.text == "switch" &&
			token.depth == 1 {
			switchIndex = index
		}
	}
	if switchIndex < 0 {
		return false
	}
	open := -1
	close := -1
	for index := switchIndex + 1; index < len(tokens); index++ {
		if open < 0 && tokens[index].kind == jssyntax.TkOpenBrace &&
			tokens[index].depth == 2 {
			open = index
			continue
		}
		if open >= 0 && tokens[index].kind == jssyntax.TkCloseBrace &&
			tokens[index].depth == 2 {
			close = index
			break
		}
	}
	if open < 0 || close < 0 {
		return false
	}
	for index := close + 1; index < len(tokens); index++ {
		if tokens[index].kind != jssyntax.TkSemicolon &&
			(tokens[index].kind != jssyntax.TkCloseBrace ||
				tokens[index].depth != 1) {
			return false
		}
	}
	var entries []int
	hasDefault := false
	for index := open + 1; index < close; index++ {
		token := tokens[index]
		if token.depth != 2 || token.kind != jssyntax.TkKeyword ||
			(token.text != "case" && token.text != "default") {
			continue
		}
		entries = append(entries, index)
		hasDefault = hasDefault || token.text == "default"
	}
	if !hasDefault || len(entries) == 0 {
		return false
	}
	for _, entry := range entries {
		returned := false
		for index := entry + 1; index < close; index++ {
			token := tokens[index]
			if token.depth == 2 && token.kind == jssyntax.TkKeyword {
				if token.text == "return" || token.text == "throw" {
					returned = true
					break
				}
				if token.text == "break" {
					break
				}
			}
		}
		if !returned {
			return false
		}
	}
	return true
}

func vueReturnStatementExpression(statement *jssyntax.Node) string {
	if statement == nil || statement.Kind() != jssyntax.JsReturnStatement {
		return ""
	}
	statementRange := statement.Range()
	for token := range statement.ChildTokens() {
		if token.Kind() != jssyntax.TkKeyword || token.Text() != "return" {
			continue
		}
		start := token.Range().End
		if start < statementRange.Start || start > statementRange.End {
			return ""
		}
		relative := start - statementRange.Start
		text := statement.Text()
		if relative > uint32(len(text)) {
			return ""
		}
		return trimVueSourceExpression(text[relative:])
	}
	return ""
}

// vueMethodImplementation unwraps property-valued functions and Vue's common
// computed `{ get(), set() }` form. Without this step an arrow inside the
// getter body could be mistaken for the computed property's own expression.
func vueMethodImplementation(method *jssyntax.Node) *jssyntax.Node {
	if method == nil || method.Kind() != jssyntax.JsProperty {
		return method
	}
	value := jsquery.PropertyValue(method)
	if value == nil {
		return method
	}
	if value.Kind() == jssyntax.JsObject {
		if getter := jsquery.Property(value, "get"); getter != nil {
			return getter
		}
		return method
	}
	switch value.Kind() {
	case jssyntax.JsArrowFunction, jssyntax.JsFunction:
		return value
	default:
		return method
	}
}

func trimVueSourceExpression(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ",")
	value = strings.TrimSuffix(value, ";")
	return strings.TrimSpace(value)
}

func vueMethodSignature(
	method *jssyntax.Node,
	known map[string]string,
) string {
	if method == nil {
		return ""
	}
	parameters, returnType, found := vueMethodHeader(method.Text())
	if !found {
		return ""
	}
	if returnType == "" {
		returnType = vueMethodReturnType(method, known)
	}
	if returnType == "" {
		returnType = "unknown"
	}
	return "(" + parameters + ") => " + returnType
}

// VueCallableReturnType extracts the result of a statically declared function
// or TypeScript method signature. It intentionally does not infer arbitrary
// runtime calls; callers use it only after resolving a named callable member.
func VueCallableReturnType(value string) string {
	value = strings.TrimSpace(value)
	for len(value) >= 2 && value[0] == '(' &&
		matchingSlotDelimiter(value, 0, '(', ')') == len(value)-1 {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	if value == "" || value[0] != '(' {
		return ""
	}
	close := matchingSlotDelimiter(value, 0, '(', ')')
	if close < 0 {
		return ""
	}
	cursor := close + 1
	for cursor < len(value) && isJavaScriptSpace(value[cursor]) {
		cursor++
	}
	switch {
	case cursor+1 < len(value) && value[cursor:cursor+2] == "=>":
		cursor += 2
	case cursor < len(value) && value[cursor] == ':':
		cursor++
	default:
		return ""
	}
	return strings.TrimSpace(value[cursor:])
}

// VueCallableSignature decomposes the callable spellings retained by the
// Administration index. It accepts both arrow-function types such as
// `(value: string) => boolean` and TypeScript method types such as
// `(value: string): boolean`. Parameter splitting remains delimiter-aware so
// nested object, tuple, generic, and callback types stay intact.
func VueCallableSignature(
	value string,
) (parameters []string, returnType string, found bool) {
	value = strings.TrimSpace(value)
	for len(value) >= 2 && value[0] == '(' &&
		matchingSlotDelimiter(value, 0, '(', ')') == len(value)-1 {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	if value == "" || value[0] != '(' {
		return nil, "", false
	}
	close := matchingSlotDelimiter(value, 0, '(', ')')
	if close < 0 {
		return nil, "", false
	}
	cursor := close + 1
	for cursor < len(value) && isJavaScriptSpace(value[cursor]) {
		cursor++
	}
	switch {
	case cursor+1 < len(value) && value[cursor:cursor+2] == "=>":
		cursor += 2
	case cursor < len(value) && value[cursor] == ':':
		cursor++
	default:
		return nil, "", false
	}
	for _, parameter := range splitSlotTopLevel(value[1:close], ',') {
		parameter = strings.TrimSpace(parameter)
		if parameter != "" {
			parameters = append(parameters, parameter)
		}
	}
	return parameters, strings.TrimSpace(value[cursor:]), true
}

// VuePropValueType normalizes the runtime and TypeScript spellings retained
// for a Vue prop into the value type accepted by a template binding. It keeps
// custom structural names intact while translating Vue constructors and
// unwrapping PropType assertions.
func VuePropValueType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runtimeType := value
	if position := strings.Index(runtimeType, " as "); position >= 0 {
		runtimeType = strings.TrimSpace(runtimeType[:position])
	}
	if asserted := vueTypeAssertion(value); asserted != "" {
		if returnType := VueCallableReturnType(asserted); returnType != "" &&
			(runtimeType == "Object" || runtimeType == "Array") {
			// Legacy Options API commonly spells a value contract as
			// `Object as () => Config`. The callable describes the prop
			// constructor, not the value passed by template consumers.
			value = returnType
		} else {
			value = asserted
		}
	}
	for {
		inner, matched := adminTypeGenericInner(value, "PropType")
		if !matched {
			break
		}
		value = strings.TrimSpace(inner)
	}
	if len(value) >= 2 && value[0] == '[' &&
		matchingSlotDelimiter(value, 0, '[', ']') == len(value)-1 {
		var result string
		for _, entry := range splitSlotTopLevel(value[1:len(value)-1], ',') {
			result = mergeVueTypes(result, VuePropValueType(entry))
		}
		return result
	}
	switch value {
	case "String":
		return "string"
	case "Number":
		return "number"
	case "Boolean":
		return "boolean"
	case "Array":
		return "Array"
	case "Object":
		return "object"
	case "Function":
		return "Function"
	default:
		return value
	}
}

// VuePropAllowedValues returns statically known string values for a public
// component prop. The boolean is true only when the values form a closed
// domain suitable for diagnostics. A generated TypeScript union may still
// yield useful completion candidates while remaining open because one branch
// is an unresolved alias.
func VuePropAllowedValues(prop VueComponentProp) ([]string, bool) {
	values := make([]string, 0, len(prop.AllowedValues))
	for _, value := range prop.AllowedValues {
		values = appendAllowedValue(values, value)
	}
	typeValues, typeComplete := vueStringLiteralUnionValues(
		VuePropValueType(prop.Type),
	)
	for _, value := range typeValues {
		values = appendAllowedValue(values, value)
	}
	return values,
		len(values) > 0 && (prop.AllowedValuesComplete || typeComplete)
}

// VueStaticStringLiteral returns a direct JavaScript string literal together
// with the byte range of its content inside expression. Keeping the quotes out
// of the range lets completion and typo fixes replace a value without turning
// a bound Vue expression into an identifier.
func VueStaticStringLiteral(
	expression string,
) (value string, contentStart, contentEnd uint32, found bool) {
	trimmedLeft := strings.TrimLeftFunc(expression, unicode.IsSpace)
	leading := len(expression) - len(trimmedLeft)
	trimmed := strings.TrimRightFunc(trimmedLeft, unicode.IsSpace)
	value, found = decodeAdminTypeStringLiteral(trimmed)
	if !found {
		return "", 0, 0, false
	}
	return value,
		uint32(leading + 1),
		uint32(leading + len(trimmed) - 1),
		true
}

func vueStringLiteralUnionValues(value string) ([]string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	branches := splitAdminTypeTopLevel(value, '|')
	values := make([]string, 0, len(branches))
	complete := true
	for _, branch := range branches {
		branch = trimAdminTypeParentheses(strings.TrimSpace(branch))
		switch branch {
		case "null", "undefined", "void", "never":
			continue
		}
		literal, found := decodeAdminTypeStringLiteral(branch)
		if !found {
			complete = false
			continue
		}
		values = appendAllowedValue(values, literal)
	}
	return values, complete && len(values) > 0
}

func appendAllowedValue(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func decodeAdminTypeStringLiteral(value string) (string, bool) {
	if len(value) < 2 ||
		(value[0] != '\'' && value[0] != '"' && value[0] != '`') ||
		value[len(value)-1] != value[0] {
		return "", false
	}
	inner := value[1 : len(value)-1]
	// A template-literal type such as `${string}px` describes an open pattern,
	// not the one literal spelling `${string}px`.
	if value[0] == '`' && strings.Contains(inner, "${") {
		return "", false
	}
	var result strings.Builder
	for index := 0; index < len(inner); index++ {
		current := inner[index]
		if current != '\\' {
			result.WriteByte(current)
			continue
		}
		if index+1 >= len(inner) {
			return "", false
		}
		index++
		escaped := inner[index]
		switch escaped {
		case 'n':
			result.WriteByte('\n')
		case 'r':
			result.WriteByte('\r')
		case 't':
			result.WriteByte('\t')
		case 'b':
			result.WriteByte('\b')
		case 'f':
			result.WriteByte('\f')
		case 'v':
			result.WriteByte('\v')
		case 'x', 'u':
			digits := 2
			if escaped == 'u' {
				digits = 4
			}
			if index+digits >= len(inner) {
				return "", false
			}
			parsed, err := strconv.ParseUint(
				inner[index+1:index+1+digits], 16, 32,
			)
			if err != nil {
				return "", false
			}
			result.WriteRune(rune(parsed))
			index += digits
		default:
			result.WriteByte(escaped)
		}
	}
	return result.String(), true
}

// VueTypesProvablyIncompatible reports only closed, category-level mismatches
// suitable for template diagnostics. Unknown aliases and top types stay
// conservative; a custom TypeScript name may still alias a primitive. The
// runtime constructor before a PropType assertion provides a safe fallback for
// Object, Array, and Function props whose structural declaration is imported.
func VueTypesProvablyIncompatible(expected, actual string) bool {
	expectedKinds, expectedKnown := vueComparableTypeKinds(expected, true)
	actualKinds, actualKnown := vueComparableTypeKinds(actual, false)
	if !expectedKnown || !actualKnown || len(expectedKinds) == 0 ||
		len(actualKinds) == 0 {
		return false
	}
	for actualKind := range actualKinds {
		if expectedKinds[actualKind] {
			// A union carrying at least one compatible alternative is not a
			// provable runtime mismatch at this expression site.
			return false
		}
	}
	return true
}

// VueTypeAllowsUndefined reports whether a top-level TypeScript union permits
// an omitted/undefined value. It is used for APIs such as mitt.emit where the
// payload argument becomes optional only for events whose value includes
// undefined or void.
func VueTypeAllowsUndefined(value string) bool {
	for _, branch := range splitAdminTypeTopLevel(value, '|') {
		switch trimAdminTypeParentheses(strings.TrimSpace(branch)) {
		case "undefined", "void":
			return true
		}
	}
	return false
}

// VueModelExpressionAssignable reports whether a static template expression
// can be the write target of v-model. Optional chains, literals, operators,
// and a terminal call are not assignable; identifiers and named/indexed member
// chains are. A call may appear before the final property because JavaScript
// permits assignments such as repository.get(id).name = value.
func VueModelExpressionAssignable(expression string) bool {
	expression = trimVueSourceExpression(expression)
	switch expression {
	case "true", "false", "null", "undefined":
		return false
	}
	if vueStaticLiteralType(expression) != "" {
		return false
	}
	path, matched := vueStaticTemplateExpression(expression)
	if !matched || len(path) == 0 {
		return false
	}
	for index, segment := range path {
		if segment.Optional || segment.Called && index == len(path)-1 {
			return false
		}
	}
	return true
}

// VueEventPayloadType returns the first payload parameter exposed by an
// indexed component event signature.
func VueEventPayloadType(eventType string) string {
	return eventPayloadType(eventType)
}

func vueComparableTypeKinds(
	value string,
	useRuntimeFallback bool,
) (map[string]bool, bool) {
	original := strings.TrimSpace(value)
	if useRuntimeFallback {
		if kind, known := vuePropRuntimeComparableKind(original); known {
			return map[string]bool{kind: true}, true
		}
	}
	value = VuePropValueType(original)
	result := make(map[string]bool)
	for _, branch := range splitAdminTypeTopLevel(value, '|') {
		kind, known := vueComparableTypeKind(branch)
		if !known {
			if useRuntimeFallback {
				kind, known = vuePropRuntimeComparableKind(original)
			}
			if !known {
				return nil, false
			}
		}
		result[kind] = true
	}
	return result, len(result) > 0
}

func vuePropRuntimeComparableKind(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if position := strings.Index(value, " as "); position >= 0 {
		value = strings.TrimSpace(value[:position])
	}
	switch value {
	case "String":
		return "string", true
	case "Number":
		return "number", true
	case "Boolean":
		return "boolean", true
	case "Array":
		return "array", true
	case "Object":
		return "object", true
	case "Function":
		return "function", true
	default:
		return "", false
	}
}

func vueComparableTypeKind(value string) (string, bool) {
	value = trimAdminTypeParentheses(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "readonly ")
	switch value {
	case "", "any", "unknown", "never", "null", "undefined", "void":
		return "", false
	case "string", "String":
		return "string", true
	case "number", "Number":
		return "number", true
	case "boolean", "Boolean":
		return "boolean", true
	case "object", "Object":
		return "object", true
	case "Function":
		return "function", true
	}
	if len(value) >= 2 && (value[0] == '\'' || value[0] == '"' ||
		value[0] == '`') && value[len(value)-1] == value[0] {
		return "string", true
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return "number", true
	}
	if strings.HasPrefix(value, "(") &&
		(VueCallableReturnType(value) != "" || strings.Contains(value, "=>")) {
		return "function", true
	}
	if strings.HasPrefix(value, "Array<") ||
		strings.HasPrefix(value, "ReadonlyArray<") ||
		strings.HasSuffix(value, "[]") ||
		len(value) >= 2 && value[0] == '[' &&
			matchingSlotDelimiter(value, 0, '[', ']') == len(value)-1 {
		return "array", true
	}
	if strings.HasPrefix(value, "Record<") || strings.HasPrefix(value, "{") {
		return "object", true
	}
	return "", false
}

func vueMethodHeader(value string) (parameters, returnType string, found bool) {
	open := strings.IndexByte(value, '(')
	if open < 0 {
		return "", "", false
	}
	close := matchingSlotDelimiter(value, open, '(', ')')
	if close < 0 {
		return "", "", false
	}
	parameters = strings.TrimSpace(value[open+1 : close])
	cursor := close + 1
	for cursor < len(value) && isJavaScriptSpace(value[cursor]) {
		cursor++
	}
	if cursor >= len(value) || value[cursor] != ':' {
		return parameters, "", true
	}
	cursor++
	start := cursor
	var angle, square, round int
	for cursor < len(value) {
		if angle == 0 && square == 0 && round == 0 &&
			cursor+1 < len(value) && value[cursor:cursor+2] == "=>" {
			return parameters, strings.TrimSpace(value[start:cursor]), true
		}
		switch value[cursor] {
		case '<':
			angle++
		case '>':
			if angle > 0 {
				angle--
			}
		case '[':
			square++
		case ']':
			if square > 0 {
				square--
			}
		case '(':
			round++
		case ')':
			if round > 0 {
				round--
			}
		case '{':
			if angle == 0 && square == 0 && round == 0 {
				if close := balancedBraceEnd(value, cursor); close > cursor {
					after := close + 1
					for after < len(value) && isJavaScriptSpace(value[after]) {
						after++
					}
					if after < len(value) && (value[after] == '{' ||
						strings.HasPrefix(value[after:], "=>")) {
						return parameters, strings.TrimSpace(
							value[start : close+1],
						), true
					}
				}
				return parameters, strings.TrimSpace(value[start:cursor]), true
			}
		}
		cursor++
	}
	return parameters, strings.TrimSpace(value[start:]), true
}

func vueExpressionTextType(value string, known map[string]string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if whenTrue, whenFalse, conditional := splitVueConditionalExpression(value); conditional {
		trueType := vueExpressionTextType(whenTrue, known)
		falseType := vueExpressionTextType(whenFalse, known)
		if trueType == "" || falseType == "" {
			return ""
		}
		return mergeVueTypes(trueType, falseType)
	}
	if value == "null" {
		return "null"
	}
	if value == "undefined" {
		return "undefined"
	}
	if indexVueTopLevelOperator(value, "=>") >= 0 {
		return "Function"
	}
	if value == "true" || value == "false" ||
		strings.HasPrefix(value, "!") && !strings.HasPrefix(value, "!=") ||
		vueHasTopLevelComparison(value) {
		return "boolean"
	}
	for constructor, resultType := range map[string]string{
		"Boolean": "boolean",
		"Number":  "number",
		"String":  "string",
	} {
		if strings.HasPrefix(value, constructor+"(") {
			return resultType
		}
	}
	if strings.HasPrefix(value, "this.") {
		name := strings.TrimPrefix(value, "this.")
		if !strings.ContainsAny(name, ".[( ?:+-*/") && known[name] != "" {
			memberType := known[name]
			return memberType
		}
	}
	switch value[0] {
	case '\'', '"', '`':
		return "string"
	case '[':
		return "Array"
	case '{':
		return "Object"
	}
	if value[0] >= '0' && value[0] <= '9' || value[0] == '-' {
		return "number"
	}
	return ""
}

func vueHasTopLevelComparison(value string) bool {
	for _, operator := range []string{"===", "!==", "==", "!=", ">=", "<=", ">", "<"} {
		position := indexVueTopLevelOperator(value, operator)
		if position < 0 {
			continue
		}
		if operator == ">" && position > 0 && value[position-1] == '=' {
			continue
		}
		if operator == "<" && position+1 < len(value) && value[position+1] == '=' {
			continue
		}
		return true
	}
	return false
}

func splitVueConditionalExpression(
	value string,
) (whenTrue, whenFalse string, found bool) {
	state := slotScanState{}
	question := -1
	nested := 0
	for index := 0; index < len(value); index++ {
		if state.topLevel() {
			switch value[index] {
			case '?':
				if index+1 < len(value) && (value[index+1] == '?' ||
					value[index+1] == '.') ||
					index > 0 && value[index-1] == '?' {
					break
				}
				if question < 0 {
					question = index
				} else {
					nested++
				}
			case ':':
				if question < 0 {
					break
				}
				if nested > 0 {
					nested--
					break
				}
				whenTrue = strings.TrimSpace(value[question+1 : index])
				whenFalse = strings.TrimSpace(value[index+1:])
				return whenTrue, whenFalse,
					whenTrue != "" && whenFalse != ""
			}
		}
		state.consume(value[index])
	}
	return "", "", false
}

func compactVueExpression(value string) string {
	return strings.Join(strings.Fields(value), "")
}

// VueTypeMembers extracts direct fields from an inline TypeScript object type.
func VueTypeMembers(value string) []TwigVueMember {
	open := strings.IndexByte(value, '{')
	if open < 0 {
		return nil
	}
	fields := parseTypeDeclarationFields(value, open)
	result := make([]TwigVueMember, 0, len(fields))
	for _, field := range fields {
		if field.name == "" {
			continue
		}
		result = append(result, TwigVueMember{
			Name: field.name, Type: strings.TrimSpace(field.value),
			Optional: field.optional,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}
