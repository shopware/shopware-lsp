package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

func parseStringArray(node *jssyntax.Node) []string {
	if node == nil || node.Kind() != jssyntax.JsArray {
		return nil
	}
	var values []string
	for _, item := range jsquery.ArrayItems(node) {
		if item.Kind() == jssyntax.JsString {
			if value := jsquery.StringValue(item); value != "" {
				values = append(values, value)
			}
		}
	}
	return values
}

func parseEventDeclarations(
	node *jssyntax.Node,
	lineIndex *cst.LineIndex,
) []VueComponentEvent {
	if node == nil {
		return nil
	}
	var events []VueComponentEvent
	switch node.Kind() {
	case jssyntax.JsArray:
		for _, item := range jsquery.ArrayItems(node) {
			if item.Kind() != jssyntax.JsString {
				continue
			}
			name := jsquery.StringValue(item)
			if name == "" {
				continue
			}
			line, _ := lineIndex.Position(item.RangeTrimmedTrivia().Start)
			events = appendComponentEvent(events, VueComponentEvent{
				Name: name, Line: int(line) + 1,
				Documentation: JavaScriptDocumentation(item),
				NameRange: componentMemberNameRange(
					item, lineIndex,
				),
			})
		}
	case jssyntax.JsObject:
		for _, property := range jsquery.Properties(node) {
			name := jsquery.PropertyName(property)
			if name == "" {
				continue
			}
			line, _ := lineIndex.Position(property.RangeTrimmedTrivia().Start)
			events = appendComponentEvent(events, VueComponentEvent{
				Name: name, Line: int(line) + 1,
				Documentation: JavaScriptDocumentation(property),
				NameRange: componentMemberNameRange(
					jsquery.PropertyNameNode(property), lineIndex,
				),
			})
		}
	}
	return events
}

func parseEmittedEvents(
	object *jssyntax.Node,
	lineIndex *cst.LineIndex,
) []VueComponentEvent {
	var events []VueComponentEvent
	for _, call := range jsquery.Calls(object) {
		switch jsquery.CallName(call) {
		case "this.$emit", "$emit", "emit", "context.emit":
		default:
			continue
		}
		argument := jsquery.ArgumentExpression(call, 0)
		if argument == nil || argument.Kind() != jssyntax.JsString {
			continue
		}
		name := jsquery.StringValue(argument)
		if name == "" {
			continue
		}
		line, _ := lineIndex.Position(argument.RangeTrimmedTrivia().Start)
		events = appendComponentEvent(events, VueComponentEvent{
			Name: name, Line: int(line) + 1,
			NameRange: componentMemberNameRange(
				argument, lineIndex,
			),
		})
	}
	return events
}

func appendComponentEvent(
	events []VueComponentEvent,
	event VueComponentEvent,
) []VueComponentEvent {
	canonical := CanonicalEventName(event.Name)
	if canonical == "" {
		return events
	}
	for index := range events {
		if CanonicalEventName(events[index].Name) != canonical {
			continue
		}
		if events[index].FilePath == "" && event.FilePath != "" {
			events[index].FilePath = event.FilePath
		}
		if events[index].Line == 0 && event.Line != 0 {
			events[index].Line = event.Line
		}
		if events[index].Type == "" && event.Type != "" {
			events[index].Type = event.Type
		}
		if events[index].Documentation == "" && event.Documentation != "" {
			events[index].Documentation = event.Documentation
		}
		if events[index].NameRange == (AdminSourceRange{}) &&
			event.NameRange != (AdminSourceRange{}) {
			events[index].NameRange = event.NameRange
		}
		return events
	}
	return append(events, event)
}

// JavaScriptEventAnnotations extracts the legacy Shopware @event contract
// attached to an export. These annotations remain relevant for components
// which emit imported constants and therefore have no literal event name in
// their runtime object.
func JavaScriptEventAnnotations(
	node *jssyntax.Node,
	lineIndex *cst.LineIndex,
) []VueComponentEvent {
	if node == nil {
		return nil
	}
	var result []VueComponentEvent
	for element := range node.Descendants() {
		token, ok := element.(*jssyntax.Token)
		if !ok {
			continue
		}
		if !token.Kind().IsTrivia() {
			break
		}
		if token.Kind() != jssyntax.TkBlockComment ||
			!strings.HasPrefix(strings.TrimSpace(token.Text()), "/**") {
			continue
		}
		for _, event := range parseJavaScriptEventAnnotations(
			token.Text(), token.Range().Start, lineIndex,
		) {
			result = appendComponentEvent(result, event)
		}
	}
	return result
}

func parseJavaScriptEventAnnotations(
	comment string,
	commentStart uint32,
	lineIndex *cst.LineIndex,
) []VueComponentEvent {
	var result []VueComponentEvent
	lineStart := 0
	for lineStart <= len(comment) {
		lineEnd := strings.IndexByte(comment[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(comment)
		} else {
			lineEnd += lineStart
		}
		line := comment[lineStart:lineEnd]
		marker := len(line) - len(strings.TrimLeft(line, " \t"))
		if marker < len(line) && line[marker] == '*' {
			marker++
			marker += len(line[marker:]) - len(strings.TrimLeft(line[marker:], " \t"))
		}
		if marker < len(line) && strings.HasPrefix(line[marker:], "@event") {
			tailStart := marker + len("@event")
			if tailStart == len(line) || line[tailStart] == ' ' ||
				line[tailStart] == '\t' {
				tail := line[tailStart:]
				trimmed := strings.TrimLeft(tail, " \t")
				leading := len(tail) - len(trimmed)
				nameEnd := strings.IndexAny(trimmed, " \t\r")
				if nameEnd < 0 {
					nameEnd = len(trimmed)
				}
				name := strings.TrimSpace(trimmed[:nameEnd])
				if name != "" {
					absoluteStart := commentStart + uint32(
						lineStart+tailStart+leading,
					)
					eventType, documentation :=
						parseJavaScriptEventAnnotationContract(
							strings.TrimSpace(trimmed[nameEnd:]),
						)
					lineNumber := 0
					if lineIndex != nil {
						value, _ := lineIndex.Position(absoluteStart)
						lineNumber = int(value) + 1
					}
					result = appendComponentEvent(result, VueComponentEvent{
						Name: name, Type: eventType,
						Documentation: documentation, Line: lineNumber,
						NameRange: sourceRangeAt(
							lineIndex, absoluteStart,
							absoluteStart+uint32(len(name)), false,
						),
					})
				}
			}
		}
		if lineEnd == len(comment) {
			break
		}
		lineStart = lineEnd + 1
	}
	return result
}

func parseJavaScriptEventAnnotationContract(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	if value[0] == '{' {
		if close := strings.IndexByte(value, '}'); close > 0 {
			contract := strings.TrimSpace(value[1:close])
			if colon := strings.IndexByte(contract, ':'); colon >= 0 {
				contract = strings.TrimSpace(contract[:colon])
			}
			return contract, strings.TrimSpace(value[close+1:])
		}
	}
	if value[0] == '(' {
		if close := strings.IndexByte(value, ')'); close > 0 {
			return strings.TrimSpace(value[1:close]),
				strings.TrimSpace(value[close+1:])
		}
	}
	fields := strings.Fields(value)
	if len(fields) == 1 {
		return fields[0], ""
	}
	if len(fields) == 2 && fields[0] == fields[1] {
		return fields[0], ""
	}
	return "", value
}

func mergeJavaScriptEventAnnotations(
	definition *ComponentDefinition,
	annotations []VueComponentEvent,
	hasExplicitEmits bool,
) {
	if definition == nil {
		return
	}
	for _, annotation := range annotations {
		_, found := componentDefinitionEvent(definition.Events, annotation.Name)
		if hasExplicitEmits && !found {
			// An explicit emits contract wins over possibly stale component-level
			// annotations. Matching annotations may still supply payload metadata.
			continue
		}
		definition.Events = appendComponentEvent(definition.Events, annotation)
		definition.Emits = appendUnique(definition.Emits, annotation.Name)
	}
}

func componentDefinitionEvent(
	events []VueComponentEvent,
	name string,
) (VueComponentEvent, bool) {
	canonical := CanonicalEventName(name)
	for _, event := range events {
		if CanonicalEventName(event.Name) == canonical {
			return event, true
		}
	}
	return VueComponentEvent{}, false
}

func parseMethodNames(node *jssyntax.Node) []string {
	if node == nil || node.Kind() != jssyntax.JsObject {
		return nil
	}
	var methods []string
	for _, property := range jsquery.Properties(node) {
		if name := jsquery.PropertyName(property); name != "" {
			methods = append(methods, name)
		}
	}
	return methods
}

func nodeText(node *jssyntax.Node) string {
	if node == nil {
		return ""
	}
	return node.Text()
}
