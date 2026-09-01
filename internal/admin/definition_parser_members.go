package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

func memberDefinitions[T interface{ ~string | VueComponentProp }](
	property *jssyntax.Node,
	values []T,
	kind VueComponentMemberKind,
	lineIndex *cst.LineIndex,
) []VueComponentMember {
	type declaration struct {
		line        int
		nameRange   AdminSourceRange
		bindingName string
		shorthand   bool
		deprecated  string
	}
	locations := make(map[string]declaration)
	defaultLine := 0
	if property != nil {
		line, _ := lineIndex.Position(property.RangeTrimmedTrivia().Start)
		defaultLine = int(line) + 1
	}
	for _, child := range jsquery.Properties(firstObject(property)) {
		name := jsquery.PropertyName(child)
		if name == "" {
			continue
		}
		line, _ := lineIndex.Position(child.RangeTrimmedTrivia().Start)
		value := jsquery.PropertyValue(child)
		bindingName := name
		if value != nil && value.Kind() == jssyntax.JsIdentifier {
			bindingName = strings.TrimSpace(jsquery.IdentifierText(value))
		}
		locations[name] = declaration{
			line: int(line) + 1,
			nameRange: componentMemberNameRange(
				jsquery.PropertyNameNode(child), lineIndex,
			),
			bindingName: bindingName,
			shorthand:   value == nil && child.Kind() == jssyntax.JsProperty,
			deprecated:  JavaScriptDeprecation(child),
		}
	}
	members := make([]VueComponentMember, 0, len(values))
	for _, value := range values {
		var name, memberType, deprecated string
		switch current := any(value).(type) {
		case string:
			name = current
		case VueComponentProp:
			name = current.Name
			memberType = current.Type
			deprecated = current.Deprecated
		}
		if name == "" {
			continue
		}
		location := locations[name]
		if deprecated == "" {
			deprecated = location.deprecated
		}
		line := location.line
		if line == 0 {
			line = defaultLine
		}
		members = append(members, VueComponentMember{
			Name: name, Kind: kind, Type: memberType, Line: line,
			NameRange: location.nameRange, BindingName: location.bindingName,
			Shorthand: location.shorthand, Deprecated: deprecated,
		})
	}
	return members
}

func componentMemberNameRange(
	node *jssyntax.Node,
	lineIndex *cst.LineIndex,
) AdminSourceRange {
	if node == nil || lineIndex == nil {
		return AdminSourceRange{}
	}
	rangeValue := node.RangeTrimmedTrivia()
	identifier := node.Kind() != jssyntax.JsString
	if !identifier {
		if inner, ok := jsStringContentRange(node); ok {
			rangeValue = inner
		}
	}
	startLine, startCharacter := lineIndex.PositionUTF16(rangeValue.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rangeValue.End)
	return AdminSourceRange{
		StartLine: int(startLine), StartCharacter: int(startCharacter),
		EndLine: int(endLine), EndCharacter: int(endCharacter),
		Declaration: true, Identifier: identifier,
	}
}

func firstObject(node *jssyntax.Node) *jssyntax.Node {
	if node == nil {
		return nil
	}
	if value := jsquery.PropertyValue(node); value != nil && value.Kind() == jssyntax.JsObject {
		return value
	}
	objects := jsquery.Nodes(node, jssyntax.JsObject)
	if len(objects) == 0 {
		return nil
	}
	return objects[0]
}

func parseDataNames(property *jssyntax.Node) []string {
	object := firstObject(property)
	if object == nil {
		return nil
	}
	return parseMethodNames(object)
}

func parseInjectNames(node *jssyntax.Node) []string {
	if node == nil {
		return nil
	}
	if node.Kind() == jssyntax.JsArray {
		return parseStringArray(node)
	}
	if node.Kind() == jssyntax.JsObject {
		return parseMethodNames(node)
	}
	return nil
}

func parseMixinNames(node *jssyntax.Node) []string {
	if node == nil {
		return nil
	}
	var names []string
	for call := range jsquery.IterateCalls(
		node,
		"Mixin.getByName",
		"Shopware.Mixin.getByName",
	) {
		if name := jsquery.StringValue(jsquery.StringArgument(call, 0)); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func setDefinitionFilePath(definition *ComponentDefinition, filePath string) {
	if definition == nil {
		return
	}
	definition.FilePath = filePath
	for index := range definition.Props {
		definition.Props[index].FilePath = filePath
	}
	for index := range definition.Members {
		definition.Members[index].FilePath = filePath
		for elementIndex := range definition.Members[index].ElementMembers {
			definition.Members[index].ElementMembers[elementIndex].FilePath = filePath
		}
		if definition.Members[index].Type != "" &&
			definition.Members[index].TypeContextPath == "" {
			definition.Members[index].TypeContextPath = filePath
		}
	}
	for index := range definition.Assignments {
		definition.Assignments[index].FilePath = filePath
	}
	for index := range definition.Events {
		definition.Events[index].FilePath = filePath
	}
	for index := range definition.LocalComponents {
		definition.LocalComponents[index].FilePath = filePath
	}
	for index := range definition.LocalDirectives {
		definition.LocalDirectives[index].FilePath = filePath
	}
}
