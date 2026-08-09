package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

func parseProps(node *jssyntax.Node, lineIndex *cst.LineIndex) []VueComponentProp {
	if node == nil {
		return nil
	}
	if node.Kind() == jssyntax.JsArray {
		var props []VueComponentProp
		for _, item := range jsquery.ArrayItems(node) {
			if item.Kind() == jssyntax.JsString {
				if name := jsquery.StringValue(item); name != "" {
					line := 0
					if lineIndex != nil {
						lineNumber, _ := lineIndex.Position(
							item.RangeTrimmedTrivia().Start,
						)
						line = int(lineNumber) + 1
					}
					props = append(props, VueComponentProp{
						Name: name, Line: line,
						Documentation: JavaScriptDocumentation(item),
						NameRange:     componentMemberNameRange(item, lineIndex),
						Deprecated:    JavaScriptDeprecation(item),
					})
				}
			}
		}
		return props
	}
	if node.Kind() != jssyntax.JsObject {
		return nil
	}

	var props []VueComponentProp
	for _, property := range jsquery.Properties(node) {
		name := jsquery.PropertyName(property)
		if name == "" {
			continue
		}
		line := 0
		if lineIndex != nil {
			lineNumber, _ := lineIndex.Position(property.RangeTrimmedTrivia().Start)
			line = int(lineNumber) + 1
		}
		prop := VueComponentProp{
			Name: name, Line: line,
			Documentation: JavaScriptDocumentation(property),
			NameRange: componentMemberNameRange(
				jsquery.PropertyNameNode(property), lineIndex,
			),
			Deprecated: JavaScriptDeprecation(property),
		}
		value := jsquery.PropertyValue(property)
		if value == nil {
			props = append(props, prop)
			continue
		}

		if value.Kind() == jssyntax.JsIdentifier {
			prop.Type = strings.TrimSpace(value.Text())
		} else if value.Kind() == jssyntax.JsObject {
			if typeProperty := jsquery.Property(value, "type"); typeProperty != nil {
				prop.Type = parseVuePropType(value, typeProperty)
			}
			if required := jsquery.Property(value, "required"); required != nil {
				prop.Required = strings.TrimSpace(nodeText(jsquery.PropertyValue(required))) == "true"
			}
			if defaultProperty := jsquery.Property(value, "default"); defaultProperty != nil {
				defaultValue := jsquery.PropertyValue(defaultProperty)
				prop.Default = strings.TrimSpace(nodeText(defaultValue))
				if defaultValue != nil && defaultValue.Kind() == jssyntax.JsString {
					prop.AllowedValues = appendAllowedValue(
						prop.AllowedValues, jsquery.StringValue(defaultValue),
					)
				}
			}
			if validValues := jsquery.Property(value, "validValues"); validValues != nil {
				values, complete := parseVuePropValidValues(
					jsquery.PropertyValue(validValues),
				)
				for _, allowed := range values {
					prop.AllowedValues = appendAllowedValue(prop.AllowedValues, allowed)
				}
				prop.AllowedValuesComplete = complete
			}
			if validator := jsquery.Property(value, "validator"); validator != nil {
				values, complete := parseVuePropValidatorValues(
					validator, prop.Type,
				)
				for _, allowed := range values {
					prop.AllowedValues = appendAllowedValue(prop.AllowedValues, allowed)
				}
				prop.AllowedValuesComplete = prop.AllowedValuesComplete || complete
			}
			// A closed domain containing only string literals is also a complete
			// runtime string contract. Some legacy Administration components omit
			// `type: String` and rely exclusively on their validator; retaining the
			// proven type lets markup features behave like the explicitly typed
			// equivalent without guessing from weak defaults or open validators.
			if prop.Type == "" && prop.AllowedValuesComplete &&
				len(prop.AllowedValues) > 0 {
				prop.Type = "String"
			}
		}
		props = append(props, prop)
	}
	return props
}

// JavaScriptDeprecation returns the normalized text of a leading JSDoc
// @deprecated tag. Only trivia before the declaration's first syntax token is
// inspected, so comments inside a prop value or function body cannot leak onto
// the declaration.
func JavaScriptDeprecation(node *jssyntax.Node) string {
	if node == nil {
		return ""
	}
	for element := range node.Descendants() {
		token, ok := element.(*jssyntax.Token)
		if !ok {
			continue
		}
		if !token.Kind().IsTrivia() {
			break
		}
		switch token.Kind() {
		case jssyntax.TkBlockComment, jssyntax.TkLineComment:
			if deprecation := parseJavaScriptDeprecation(token.Text()); deprecation != "" {
				return deprecation
			}
		}
	}
	return ""
}

// JavaScriptDocumentation returns the public description from the leading
// JSDoc attached to a declaration. Contract metadata represented elsewhere on
// VueComponentProp is omitted so completion and hover do not repeat it.
func JavaScriptDocumentation(node *jssyntax.Node) string {
	if node == nil {
		return ""
	}
	for element := range node.Descendants() {
		token, ok := element.(*jssyntax.Token)
		if !ok {
			continue
		}
		if !token.Kind().IsTrivia() {
			break
		}
		if token.Kind() != jssyntax.TkBlockComment {
			continue
		}
		if documentation := parseJavaScriptDocumentation(token.Text()); documentation != "" {
			return documentation
		}
	}
	return ""
}

func parseJavaScriptDocumentation(comment string) string {
	comment = strings.TrimSpace(comment)
	if !strings.HasPrefix(comment, "/**") {
		return ""
	}
	comment = strings.TrimPrefix(comment, "/**")
	comment = strings.TrimSuffix(comment, "*/")
	lines := strings.Split(comment, "\n")
	result := make([]string, 0, len(lines))
	collecting := true
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		if strings.HasPrefix(line, "@description") {
			collecting = true
			line = strings.TrimSpace(strings.TrimPrefix(line, "@description"))
		} else if strings.HasPrefix(line, "@") {
			collecting = false
			continue
		}
		if collecting {
			result = append(result, line)
		}
	}
	for len(result) > 0 && result[0] == "" {
		result = result[1:]
	}
	for len(result) > 0 && result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}

func parseJavaScriptDeprecation(comment string) string {
	comment = strings.TrimSpace(comment)
	comment = strings.TrimPrefix(comment, "/*")
	comment = strings.TrimSuffix(comment, "*/")
	lines := strings.Split(comment, "\n")
	var result []string
	found := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "//"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		if !found {
			position := strings.Index(line, "@deprecated")
			if position < 0 {
				continue
			}
			found = true
			line = strings.TrimSpace(line[position+len("@deprecated"):])
		} else if strings.HasPrefix(line, "@") {
			break
		}
		if line != "" {
			result = append(result, line)
		}
	}
	return strings.Join(result, " ")
}

func parseVuePropValidValues(node *jssyntax.Node) ([]string, bool) {
	if node == nil || node.Kind() != jssyntax.JsArray {
		return nil, false
	}
	items := jsquery.ArrayItems(node)
	values := make([]string, 0, len(items))
	complete := true
	for _, item := range items {
		if item == nil || item.Kind() != jssyntax.JsString {
			complete = false
			continue
		}
		values = appendAllowedValue(values, jsquery.StringValue(item))
	}
	return values, complete && len(values) > 0
}

// parseVuePropValidatorValues recognizes the closed runtime-validator form
// used by Shopware's Options API components:
//
//	validator(value) { return ['small', 'large'].includes(value); }
//	validator(value) {
//	    if (!value.length) { return true; }
//	    return ['small', 'large'].includes(value);
//	}
//	validator(value) {
//	    const values = ['small', 'large'];
//	    return values.includes(value);
//	}
//
// The accepted expression deliberately stays narrow. Boolean combinations,
// imported or mutable arrays, computed values, and mixed literal arrays remain
// open so a validator can never accidentally turn into an unsafe markup
// diagnostic.
func parseVuePropValidatorValues(
	validator *jssyntax.Node,
	propType string,
) ([]string, bool) {
	if validator == nil {
		return nil, false
	}
	callable := validator
	if validator.Kind() == jssyntax.JsProperty {
		callable = jsquery.PropertyValue(validator)
	}
	if callable == nil {
		return nil, false
	}
	parameter := callbackFirstParameter(callable)
	if parameter == "" {
		return nil, false
	}
	callableSource := callable.Text()
	arrowFunction := callable.Kind() == jssyntax.JsArrowFunction
	acceptsEmptyString := false
	if VuePropValueType(propType) == "string" {
		callableSource, acceptsEmptyString =
			stripVueValidatorEmptyStringGuard(
				callableSource, parameter, arrowFunction,
			)
	}
	localName := ""
	var localValues []string
	localValuesComplete := false
	if stripped, name, values, complete, found :=
		stripVueValidatorLocalStringArray(callableSource, arrowFunction); found {
		callableSource = stripped
		localName = name
		localValues = values
		localValuesComplete = complete
	}
	expression, found := vueValidatorReturnExpression(
		callableSource, arrowFunction,
	)
	if !found {
		return nil, false
	}
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, false
	}
	var values []string
	complete := false
	var tail string
	if localName != "" {
		if !strings.HasPrefix(expression, localName) {
			return nil, false
		}
		tail = strings.TrimSpace(expression[len(localName):])
		values = localValues
		complete = localValuesComplete
	} else {
		if expression[0] != '[' {
			return nil, false
		}
		arrayEnd := matchingSlotDelimiter(expression, 0, '[', ']')
		if arrayEnd < 0 {
			return nil, false
		}
		tail = strings.TrimSpace(expression[arrayEnd+1:])
		values, complete = parseVueValidatorLiteralStringArray(
			expression[:arrayEnd+1],
		)
	}
	if !strings.HasPrefix(tail, ".includes") {
		return nil, false
	}
	tail = strings.TrimSpace(strings.TrimPrefix(tail, ".includes"))
	if tail == "" || tail[0] != '(' {
		return nil, false
	}
	callEnd := matchingSlotDelimiter(tail, 0, '(', ')')
	if callEnd < 0 || strings.TrimSpace(tail[1:callEnd]) != parameter ||
		strings.TrimSpace(strings.TrimSuffix(
			strings.TrimSpace(tail[callEnd+1:]), ";",
		)) != "" {
		return nil, false
	}
	if acceptsEmptyString {
		values = appendAllowedValue(values, "")
	}
	return values, complete
}

func parseVueValidatorLiteralStringArray(source string) ([]string, bool) {
	parsed := javascriptparser.Parse(source)
	if parsed.Tree == nil || parsed.Tree.Root == nil {
		return nil, false
	}
	arrays := jsquery.Nodes(parsed.Tree.Root, jssyntax.JsArray)
	if len(arrays) != 1 {
		return nil, false
	}
	return parseVuePropValidValues(arrays[0])
}

// stripVueValidatorLocalStringArray resolves only an immutable literal array
// declared as the validator body's first statement and consumed by its sole
// return. Keeping the declaration local, const, and immediately followed by
// return rules out imports, reassignment, mutation, and intervening effects.
// The return receiver is checked by parseVuePropValidatorValues after the
// declaration is removed.
func stripVueValidatorLocalStringArray(
	value string,
	arrowFunction bool,
) (string, string, []string, bool, bool) {
	value = strings.TrimSpace(value)
	bodyOpen, bodyClose, found := vueValidatorBodyRange(value, arrowFunction)
	if !found {
		return value, "", nil, false, false
	}
	body := value[bodyOpen+1 : bodyClose]
	declarationStart := 0
	for declarationStart < len(body) &&
		isJavaScriptSpace(body[declarationStart]) {
		declarationStart++
	}
	if !strings.HasPrefix(body[declarationStart:], "const") {
		return value, "", nil, false, false
	}
	cursor := declarationStart + len("const")
	if cursor >= len(body) || !isJavaScriptSpace(body[cursor]) {
		return value, "", nil, false, false
	}
	for cursor < len(body) && isJavaScriptSpace(body[cursor]) {
		cursor++
	}
	if cursor >= len(body) || !isVueIdentifierStart(body[cursor]) {
		return value, "", nil, false, false
	}
	nameStart := cursor
	cursor++
	for cursor < len(body) && isVueIdentifierPart(body[cursor]) {
		cursor++
	}
	name := body[nameStart:cursor]
	for cursor < len(body) && isJavaScriptSpace(body[cursor]) {
		cursor++
	}
	if cursor >= len(body) || body[cursor] != '=' ||
		cursor+1 < len(body) && (body[cursor+1] == '=' || body[cursor+1] == '>') {
		return value, "", nil, false, false
	}
	cursor++
	for cursor < len(body) && isJavaScriptSpace(body[cursor]) {
		cursor++
	}
	if cursor >= len(body) || body[cursor] != '[' {
		return value, "", nil, false, false
	}
	arrayStart := cursor
	arrayEnd := matchingSlotDelimiter(body, arrayStart, '[', ']')
	if arrayEnd < 0 {
		return value, "", nil, false, false
	}
	values, complete := parseVueValidatorLiteralStringArray(
		body[arrayStart : arrayEnd+1],
	)
	cursor = arrayEnd + 1
	hasLineTerminator := false
	for cursor < len(body) && isJavaScriptSpace(body[cursor]) {
		hasLineTerminator = hasLineTerminator ||
			body[cursor] == '\n' || body[cursor] == '\r'
		cursor++
	}
	if cursor < len(body) && body[cursor] == ';' {
		cursor++
	} else if !hasLineTerminator {
		return value, "", nil, false, false
	}
	for cursor < len(body) && isJavaScriptSpace(body[cursor]) {
		cursor++
	}
	if !strings.HasPrefix(body[cursor:], "return") ||
		cursor+len("return") < len(body) &&
			isVueIdentifierPart(body[cursor+len("return")]) {
		return value, "", nil, false, false
	}
	stripped := value[:bodyOpen+1] + body[:declarationStart] + body[cursor:] +
		value[bodyClose:]
	return stripped, name, values, complete, true
}

// stripVueValidatorEmptyStringGuard accepts only Shopware's exact leading
// String-prop guard. The caller checks the runtime prop type before invoking
// this helper because `!value.length` would otherwise also accept arbitrary
// zero-length arrays or objects. Comments, alternate conditions, extra guard
// statements, and non-leading branches remain unsupported and therefore open.
func stripVueValidatorEmptyStringGuard(
	value,
	parameter string,
	arrowFunction bool,
) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || parameter == "" {
		return value, false
	}
	bodyOpen, bodyClose, found := vueValidatorBodyRange(value, arrowFunction)
	if !found {
		return value, false
	}
	body := value[bodyOpen+1 : bodyClose]
	guardStart := 0
	for guardStart < len(body) && isJavaScriptSpace(body[guardStart]) {
		guardStart++
	}
	if !strings.HasPrefix(body[guardStart:], "if") {
		return value, false
	}
	cursor := guardStart + len("if")
	for cursor < len(body) && isJavaScriptSpace(body[cursor]) {
		cursor++
	}
	if cursor >= len(body) || body[cursor] != '(' {
		return value, false
	}
	conditionEnd := matchingSlotDelimiter(body, cursor, '(', ')')
	if conditionEnd < 0 || compactVueValidatorCode(
		body[cursor+1:conditionEnd],
	) != "!"+parameter+".length" {
		return value, false
	}
	cursor = conditionEnd + 1
	for cursor < len(body) && isJavaScriptSpace(body[cursor]) {
		cursor++
	}
	if cursor >= len(body) || body[cursor] != '{' {
		return value, false
	}
	guardEnd := matchingSlotDelimiter(body, cursor, '{', '}')
	if guardEnd < 0 || strings.TrimSuffix(compactVueValidatorCode(
		body[cursor+1:guardEnd],
	), ";") != "returntrue" {
		return value, false
	}
	return value[:bodyOpen+1] + body[:guardStart] + body[guardEnd+1:] +
		value[bodyClose:], true
}

func vueValidatorBodyRange(
	value string,
	arrowFunction bool,
) (int, int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, false
	}
	var bodyOpen int
	if arrowFunction {
		arrow := strings.Index(value, "=>")
		if arrow < 0 {
			return 0, 0, false
		}
		bodyOpen = arrow + len("=>")
		for bodyOpen < len(value) && isJavaScriptSpace(value[bodyOpen]) {
			bodyOpen++
		}
		if bodyOpen >= len(value) || value[bodyOpen] != '{' {
			return 0, 0, false
		}
	} else {
		bodyOpen = strings.IndexByte(value, '{')
		if bodyOpen < 0 {
			return 0, 0, false
		}
	}
	bodyClose := matchingSlotDelimiter(value, bodyOpen, '{', '}')
	if bodyClose < 0 || strings.TrimSpace(value[bodyClose+1:]) != "" {
		return 0, 0, false
	}
	return bodyOpen, bodyClose, true
}

func compactVueValidatorCode(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	for index := 0; index < len(value); index++ {
		if isJavaScriptSpace(value[index]) {
			continue
		}
		result.WriteByte(value[index])
	}
	return result.String()
}

func vueValidatorReturnExpression(
	value string,
	arrowFunction bool,
) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if arrowFunction {
		arrow := strings.Index(value, "=>")
		if arrow < 0 {
			return "", false
		}
		body := strings.TrimSpace(value[arrow+2:])
		if body == "" {
			return "", false
		}
		if body[0] != '{' {
			return trimVueSourceExpression(body), true
		}
		if close := matchingSlotDelimiter(body, 0, '{', '}'); close != len(body)-1 {
			return "", false
		}
		return vueValidatorBlockReturn(body[1 : len(body)-1])
	}
	open := strings.IndexByte(value, '{')
	if open < 0 {
		return "", false
	}
	close := matchingSlotDelimiter(value, open, '{', '}')
	if close < 0 || strings.TrimSpace(value[close+1:]) != "" {
		return "", false
	}
	return vueValidatorBlockReturn(value[open+1 : close])
}

func vueValidatorBlockReturn(body string) (string, bool) {
	returnAt := -1
	for cursor := 0; cursor+len("return") <= len(body); cursor++ {
		if body[cursor:cursor+len("return")] != "return" ||
			cursor > 0 && isVueIdentifierPart(body[cursor-1]) ||
			cursor+len("return") < len(body) &&
				isVueIdentifierPart(body[cursor+len("return")]) ||
			!adminTypeCodePosition(body, cursor) {
			continue
		}
		if returnAt >= 0 {
			return "", false
		}
		returnAt = cursor
	}
	if returnAt < 0 {
		return "", false
	}
	prefix := strings.TrimSpace(body[:returnAt])
	if prefix != "" {
		return "", false
	}
	expression := strings.TrimSpace(body[returnAt+len("return"):])
	if expression == "" {
		return "", false
	}
	return expression, true
}

// parseVuePropType preserves both Vue's runtime constructor and its optional
// TypeScript assertion. The deliberately small JavaScript CST keeps `Object`
// as the property value in `Object as PropType<Foo>`; scanning to the
// top-level comma recovers the complete, lossless declaration for hover and
// future type analytics without teaching the general expression parser the
// entire TypeScript type grammar.
func parseVuePropType(
	config *jssyntax.Node,
	typeProperty *jssyntax.Node,
) string {
	if config == nil || typeProperty == nil {
		return ""
	}
	source := config.Text()
	start := int(typeProperty.RangeTrimmedTrivia().Start - config.Range().Start)
	if start < 0 || start >= len(source) {
		return ""
	}
	colon := strings.IndexByte(source[start:], ':')
	if colon < 0 {
		return ""
	}
	start += colon + 1
	for start < len(source) && isJavaScriptSpace(source[start]) {
		start++
	}
	end := start
	var round, square, curly, angle int
	var quote byte
	for end < len(source) {
		current := source[end]
		if quote != 0 {
			if current == '\\' {
				end += 2
				continue
			}
			if current == quote {
				quote = 0
			}
			end++
			continue
		}
		switch current {
		case '\'', '"', '`':
			quote = current
		case '(':
			round++
		case ')':
			if round > 0 {
				round--
			}
		case '[':
			square++
		case ']':
			if square > 0 {
				square--
			}
		case '{':
			curly++
		case '}':
			if curly == 0 && round == 0 && square == 0 && angle == 0 {
				return strings.TrimSpace(source[start:end])
			}
			if curly > 0 {
				curly--
			}
		case '<':
			angle++
		case '>':
			if angle > 0 {
				angle--
			}
		case ',':
			if round == 0 && square == 0 && curly == 0 && angle == 0 {
				return strings.TrimSpace(source[start:end])
			}
		}
		end++
	}
	return strings.TrimSpace(source[start:end])
}

func isJavaScriptSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}
