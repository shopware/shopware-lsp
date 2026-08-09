package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

func parseTwigVueForBindings(
	value string,
	base uint32,
	scopeRange,
	expressionRange cst.TextRange,
) []TwigVueBinding {
	separatorStart, separatorEnd := twigVueForSeparator(value)
	if separatorStart < 0 {
		return nil
	}
	leftStart, leftEnd := trimByteRange(value, 0, separatorStart)
	if leftStart >= leftEnd {
		return nil
	}
	if value[leftStart] == '(' && value[leftEnd-1] == ')' &&
		matchingSlotDelimiter(value, leftStart, '(', ')') == leftEnd-1 {
		leftStart++
		leftEnd--
		leftStart, leftEnd = trimByteRange(value, leftStart, leftEnd)
	}
	iterableStart, iterableEnd := trimByteRange(
		value, separatorEnd, len(value),
	)
	iterable := ""
	if iterableStart < iterableEnd {
		iterable = value[iterableStart:iterableEnd]
	}
	var result []TwigVueBinding
	for ordinal, part := range splitTwigVueTopLevelRanges(
		value, leftStart, leftEnd, ',',
	) {
		start, end := trimByteRange(value, part.Start, part.End)
		if start >= end || !isSlotIdentifier(value[start:end]) {
			continue
		}
		result = append(result, TwigVueBinding{
			Name: value[start:end], Kind: TwigVueBindingFor, Ordinal: ordinal,
			DeclarationRange: cst.TextRange{
				Start: base + uint32(start), End: base + uint32(end),
			},
			ScopeRange: scopeRange, ExpressionRange: expressionRange,
			Iterable: iterable,
		})
	}
	return result
}

func twigVueForSeparator(value string) (int, int) {
	state := slotScanState{}
	for index := 0; index < len(value); index++ {
		if state.topLevel() {
			for _, word := range []string{"in", "of"} {
				end := index + len(word)
				if end > len(value) || value[index:end] != word ||
					index == 0 || !isSlotSpace(value[index-1]) ||
					end == len(value) || !isSlotSpace(value[end]) {
					continue
				}
				return index, end
			}
		}
		state.consume(value[index])
	}
	return -1, -1
}

func trimByteRange(value string, start, end int) (int, int) {
	for start < end && isSlotSpace(value[start]) {
		start++
	}
	for end > start && isSlotSpace(value[end-1]) {
		end--
	}
	return start, end
}

func splitTwigVueTopLevelRanges(
	value string,
	start,
	end int,
	separator byte,
) []struct{ Start, End int } {
	var result []struct{ Start, End int }
	partStart := start
	state := slotScanState{}
	for index := start; index < end; index++ {
		if value[index] == separator && state.topLevel() {
			result = append(result, struct{ Start, End int }{partStart, index})
			partStart = index + 1
			continue
		}
		state.consume(value[index])
	}
	return append(result, struct{ Start, End int }{partStart, end})
}

func eventPayloadType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "[") {
		close := matchingSlotDelimiter(value, 0, '[', ']')
		if close < 0 {
			return ""
		}
		parameters := splitSlotTopLevel(value[1:close], ',')
		if len(parameters) == 0 {
			return ""
		}
		return vueEventParameterType(parameters[0])
	}
	if strings.HasPrefix(value, "(") {
		close := matchingSlotDelimiter(value, 0, '(', ')')
		if close < 0 {
			return ""
		}
		parameters := splitSlotTopLevel(value[1:close], ',')
		if len(parameters) == 0 {
			return ""
		}
		parameterIndex := 0
		if len(parameters) > 1 && vueEventDiscriminatorParameter(parameters[0]) {
			parameterIndex = 1
		}
		return vueEventParameterType(parameters[parameterIndex])
	}
	switch trimAdminTypeParentheses(value) {
	case "void", "undefined", "never":
		return ""
	}
	// Legacy @event annotations describe the payload directly rather than as a
	// callable signature. Retain that source-owned type for template bindings.
	return value
}

func vueEventParameterType(parameter string) string {
	parameter = strings.TrimSpace(parameter)
	rest := strings.HasPrefix(parameter, "...")
	if colon := indexSlotTopLevel(parameter, ':'); colon >= 0 {
		parameter = strings.TrimSpace(parameter[colon+1:])
	}
	if rest && strings.HasSuffix(parameter, "[]") {
		parameter = strings.TrimSpace(strings.TrimSuffix(parameter, "[]"))
	}
	return parameter
}

func vueEventDiscriminatorParameter(parameter string) bool {
	parameter = strings.TrimSpace(parameter)
	colon := indexSlotTopLevel(parameter, ':')
	if colon < 0 {
		return false
	}
	name := strings.TrimSpace(strings.TrimSuffix(parameter[:colon], "?"))
	switch name {
	case "event", "evt", "e":
	default:
		return false
	}
	eventType := strings.TrimSpace(parameter[colon+1:])
	return len(eventType) >= 2 &&
		(eventType[0] == '\'' || eventType[0] == '"') &&
		eventType[len(eventType)-1] == eventType[0]
}

func nativeEventPayloadType(name string) string {
	switch CanonicalEventName(name) {
	case "click", "dblclick", "mousedown", "mouseup", "mousemove",
		"mouseenter", "mouseleave", "contextmenu":
		return "MouseEvent"
	case "keydown", "keyup", "keypress":
		return "KeyboardEvent"
	case "focus", "blur", "focusin", "focusout":
		return "FocusEvent"
	case "drag", "dragstart", "dragend", "dragenter", "dragleave",
		"dragover", "drop":
		return "DragEvent"
	case "input", "change", "submit", "reset":
		return "Event"
	default:
		return ""
	}
}

// VueIterableElementType extracts the value type exposed by a Vue v-for from
// the runtime/TypeScript spellings commonly used in Administration props.
// Plain Array has no element information and deliberately returns unknown.
func VueIterableElementType(value string) string {
	value = strings.TrimSpace(value)
	if union := splitAdminTypeTopLevel(value, '|'); len(union) > 1 {
		var elementType string
		for _, branch := range union {
			branch = strings.TrimSpace(branch)
			if branch == "null" || branch == "undefined" || branch == "never" {
				continue
			}
			candidate := VueIterableElementType(branch)
			if candidate == "" {
				return ""
			}
			if elementType != "" && elementType != candidate {
				return ""
			}
			elementType = candidate
		}
		return elementType
	}
	value = normalizeVueIterableType(value)
	if value == "number" {
		return "number"
	}
	if value == "string" {
		return "string"
	}
	if strings.HasSuffix(value, "[]") {
		return strings.TrimSpace(strings.TrimSuffix(value, "[]"))
	}
	if name, arguments := parseAdminNamedType(value); len(arguments) == 2 {
		shortName := name
		if separator := strings.LastIndexByte(shortName, '.'); separator >= 0 {
			shortName = shortName[separator+1:]
		}
		switch shortName {
		case "Record":
			return strings.TrimSpace(arguments[1])
		case "Map", "ReadonlyMap":
			return "[" + strings.TrimSpace(arguments[0]) + ", " +
				strings.TrimSpace(arguments[1]) + "]"
		}
	}
	for _, constructor := range []string{
		"Array", "ReadonlyArray", "Iterable", "Set", "EntityCollection",
		"EntitySchema.EntityCollection",
	} {
		prefix := constructor + "<"
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		open := len(constructor)
		close := matchingSlotDelimiter(value, open, '<', '>')
		if close == len(value)-1 {
			element := strings.TrimSpace(value[open+1 : close])
			if strings.HasSuffix(constructor, "EntityCollection") {
				return "Entity<" + element + ">"
			}
			return element
		}
	}
	if len(value) >= 2 && value[0] == '[' &&
		matchingSlotDelimiter(value, 0, '[', ']') == len(value)-1 {
		items := splitSlotTopLevel(value[1:len(value)-1], ',')
		var types []string
		seen := make(map[string]bool)
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			types = append(types, item)
		}
		return strings.Join(types, " | ")
	}
	if strings.HasPrefix(value, "{") {
		var elementType string
		for _, member := range VueTypeMembers(value) {
			elementType = mergeVueTypes(elementType, member.Type)
		}
		return elementType
	}
	return ""
}

// VueIterableBindingType models Vue's v-for binding order. Arrays, strings,
// numeric ranges, and generic iterables expose (value, index), while objects
// and Records expose (value, key, index).
func VueIterableBindingType(value string, ordinal int) string {
	if ordinal <= 0 {
		return VueIterableElementType(value)
	}
	if ordinal >= 2 {
		return "number"
	}
	if keyType := vueIterableKeyType(value); keyType != "" {
		return keyType
	}
	if VueIterableElementType(value) != "" {
		return "number"
	}
	return ""
}

func vueIterableKeyType(value string) string {
	value = strings.TrimSpace(value)
	if union := splitAdminTypeTopLevel(value, '|'); len(union) > 1 {
		var keyType string
		for _, branch := range union {
			branch = strings.TrimSpace(branch)
			if branch == "null" || branch == "undefined" || branch == "never" {
				continue
			}
			candidate := vueIterableKeyType(branch)
			if candidate == "" {
				return ""
			}
			keyType = mergeVueTypes(keyType, candidate)
		}
		return keyType
	}
	value = normalizeVueIterableType(value)
	if name, arguments := parseAdminNamedType(value); len(arguments) == 2 {
		shortName := name
		if separator := strings.LastIndexByte(shortName, '.'); separator >= 0 {
			shortName = shortName[separator+1:]
		}
		if shortName == "Record" {
			return strings.TrimSpace(arguments[0])
		}
	}
	if strings.HasPrefix(value, "{") {
		return "string"
	}
	return ""
}

func normalizeVueIterableType(value string) string {
	value = strings.TrimSpace(value)
	if arrow := strings.LastIndex(value, "=>"); arrow >= 0 {
		value = strings.TrimSpace(value[arrow+2:])
	}
	for {
		open := strings.LastIndex(value, "PropType<")
		if open < 0 {
			break
		}
		angle := open + len("PropType")
		close := matchingSlotDelimiter(value, angle, '<', '>')
		if close < 0 {
			break
		}
		value = strings.TrimSpace(value[angle+1 : close])
	}
	value = strings.TrimSpace(strings.TrimPrefix(value, "readonly "))
	for len(value) >= 2 && value[0] == '(' &&
		matchingSlotDelimiter(value, 0, '(', ')') == len(value)-1 {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}
