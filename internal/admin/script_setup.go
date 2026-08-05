package admin

import (
	"path/filepath"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

type scriptSetupMacro struct {
	nameStart int
	typeStart int
	typeEnd   int
	argsStart int
	argsEnd   int
}

// parseScriptSetupDefinition normalizes Vue compiler macros and the bindings
// exposed by <script setup>. The returned definition deliberately uses the
// same component contract as the Options API index so every downstream LSP
// feature can remain independent of the component authoring style.
func parseScriptSetupDefinition(
	root *jssyntax.Node,
	source,
	filePath string,
	lineIndex *cst.LineIndex,
	bodyRange cst.TextRange,
) *ComponentDefinition {
	program := scriptSetupProgram(root, bodyRange)
	if program == nil {
		return nil
	}
	if lineIndex == nil {
		lineIndex = cst.NewLineIndex(source)
	}
	definition := &ComponentDefinition{FilePath: filePath}

	for _, call := range jsquery.Calls(program, "defineProps") {
		if closestJavaScriptFunction(call) != nil {
			continue
		}
		props := parseProps(jsquery.ArgumentExpression(call, 0), lineIndex)
		definition.Props = overlayScriptSetupProps(definition.Props, props)
	}
	for _, macro := range scriptSetupMacros(source, bodyRange, "defineProps") {
		if macro.typeStart < 0 {
			continue
		}
		typeExpression := strings.TrimSpace(
			source[macro.typeStart:macro.typeEnd],
		)
		definition.ScriptSetupPropTypes = appendUnique(
			definition.ScriptSetupPropTypes, typeExpression,
		)
		props := scriptSetupTypedProps(
			source, typeExpression, filePath, lineIndex,
		)
		definition.Props = overlayScriptSetupProps(definition.Props, props)
	}
	definition.ScriptSetupPropDefaults = applyScriptSetupDefaults(
		program, definition.Props,
	)
	definition.ScriptSetupPropBindings = scriptSetupReactivePropBindings(
		program, lineIndex,
	)
	for _, binding := range definition.ScriptSetupPropBindings {
		if binding.Default == "" {
			continue
		}
		definition.ScriptSetupPropDefaults = overlayScriptSetupPropDefaults(
			definition.ScriptSetupPropDefaults,
			[]ScriptSetupPropDefault{{
				Name: binding.PropName, Value: binding.Default,
			}},
		)
	}
	applyScriptSetupDefaultsToProps(
		definition.Props, definition.ScriptSetupPropDefaults,
	)

	for _, call := range jsquery.Calls(program, "defineEmits") {
		if closestJavaScriptFunction(call) != nil {
			continue
		}
		for _, event := range parseEventDeclarations(
			jsquery.ArgumentExpression(call, 0), lineIndex,
		) {
			definition.Events = appendComponentEvent(definition.Events, event)
			definition.Emits = appendUnique(definition.Emits, event.Name)
		}
	}
	for _, macro := range scriptSetupMacros(source, bodyRange, "defineEmits") {
		if macro.typeStart < 0 {
			continue
		}
		typeExpression := strings.TrimSpace(
			source[macro.typeStart:macro.typeEnd],
		)
		definition.ScriptSetupEventTypes = appendUnique(
			definition.ScriptSetupEventTypes, typeExpression,
		)
		for _, event := range scriptSetupTypedEvents(
			source, macro.typeStart, macro.typeEnd, filePath, lineIndex,
		) {
			definition.Events = appendComponentEvent(definition.Events, event)
			definition.Emits = appendUnique(definition.Emits, event.Name)
		}
	}
	for _, macro := range scriptSetupMacros(source, bodyRange, "defineSlots") {
		if macro.typeStart < 0 {
			continue
		}
		definition.ScriptSetupSlotTypes = appendUnique(
			definition.ScriptSetupSlotTypes,
			strings.TrimSpace(source[macro.typeStart:macro.typeEnd]),
		)
	}

	for _, macro := range scriptSetupMacros(source, bodyRange, "defineModel") {
		prop, event := scriptSetupModel(source, macro, filePath, lineIndex)
		if prop.Name == "" {
			continue
		}
		definition.Props = overlayScriptSetupProps(
			definition.Props, []VueComponentProp{prop},
		)
		definition.Events = appendComponentEvent(definition.Events, event)
		definition.Emits = appendUnique(definition.Emits, event.Name)
		if prop.Name == "modelValue" && definition.ModelProp == "" {
			definition.ModelProp = prop.Name
			definition.ModelEvent = event.Name
		}
	}

	definition.Members = append(
		definition.Members,
		scriptSetupPropMembers(program, definition.Props, source, lineIndex)...,
	)
	definition.Members = overlayScriptSetupMembers(
		definition.Members,
		scriptSetupBindings(program, lineIndex),
	)
	importMembers, localComponents := scriptSetupImports(
		program, filePath, lineIndex,
	)
	definition.Members = overlayScriptSetupMembers(
		definition.Members, importMembers,
	)
	definition.LocalComponents = localComponents
	populateScriptSetupLegacyMembers(definition)
	setDefinitionFilePath(definition, filePath)
	return definition
}

func scriptSetupProgram(
	root *jssyntax.Node,
	bodyRange cst.TextRange,
) *jssyntax.Node {
	for _, program := range jsquery.Nodes(root, jssyntax.JsProgram) {
		rangeValue := program.Range()
		if rangeValue.Start >= bodyRange.Start && rangeValue.End <= bodyRange.End {
			return program
		}
	}
	return nil
}

func scriptSetupMacros(
	source string,
	bodyRange cst.TextRange,
	name string,
) []scriptSetupMacro {
	start := int(bodyRange.Start)
	end := int(bodyRange.End)
	if start < 0 || end < start || end > len(source) || name == "" {
		return nil
	}
	var result []scriptSetupMacro
	for cursor := start; cursor < end; {
		relative := strings.Index(source[cursor:end], name)
		if relative < 0 {
			break
		}
		position := cursor + relative
		cursor = position + len(name)
		if position > start && isVueIdentifierPart(source[position-1]) ||
			cursor < end && isVueIdentifierPart(source[cursor]) ||
			!adminTypeCodePosition(source, position) {
			continue
		}
		macro := scriptSetupMacro{
			nameStart: position, typeStart: -1, typeEnd: -1,
			argsStart: -1, argsEnd: -1,
		}
		for cursor < end && isJavaScriptSpace(source[cursor]) {
			cursor++
		}
		if cursor < end && source[cursor] == '<' {
			close := matchingSlotDelimiter(source, cursor, '<', '>')
			if close < 0 || close >= end {
				continue
			}
			macro.typeStart, macro.typeEnd = cursor+1, close
			cursor = close + 1
			for cursor < end && isJavaScriptSpace(source[cursor]) {
				cursor++
			}
		}
		if cursor >= end || source[cursor] != '(' {
			continue
		}
		close := matchingSlotDelimiter(source, cursor, '(', ')')
		if close < 0 || close >= end {
			continue
		}
		macro.argsStart, macro.argsEnd = cursor+1, close
		result = append(result, macro)
		cursor = close + 1
	}
	return result
}

func scriptSetupTypedProps(
	source,
	typeExpression,
	filePath string,
	lineIndex *cst.LineIndex,
) []VueComponentProp {
	fields := scriptSetupTypeFields(source, typeExpression)
	props := make([]VueComponentProp, 0, len(fields))
	for _, field := range fields {
		if field.name == "" {
			continue
		}
		line, _ := lineIndex.Position(uint32(field.offset))
		props = append(props, VueComponentProp{
			Name: field.name, Type: strings.TrimSpace(field.value),
			Documentation: adminTypeFieldDocumentation(source, field.offset),
			Required:      !field.optional, FilePath: filePath, Line: int(line) + 1,
			NameRange: typeDeclarationFieldNameRange(field, lineIndex, 0),
		})
	}
	return props
}

func scriptSetupTypeFields(source, expression string) []typeDeclarationField {
	expression = strings.TrimSpace(expression)
	if open := strings.IndexByte(expression, '{'); open >= 0 {
		absolute := strings.Index(source, expression)
		if absolute >= 0 {
			return parseTypeDeclarationFields(source, absolute+open)
		}
		return parseTypeDeclarationFields(expression, open)
	}
	name, _ := parseAdminNamedType(expression)
	if name == "" {
		return nil
	}
	for _, match := range adminTypeDeclarationPattern.FindAllStringSubmatchIndex(
		source, -1,
	) {
		if len(match) < 6 || source[match[4]:match[5]] != name ||
			!adminTypeCodePosition(source, match[0]) {
			continue
		}
		cursor := match[5]
		_, cursor = parseAdminTypeParameters(source, cursor)
		if source[match[2]:match[3]] == "type" {
			equals := indexAdminTypeToken(source, cursor, '=')
			if equals < 0 {
				continue
			}
			cursor = equals + 1
		}
		open := indexAdminTypeToken(source, cursor, '{')
		if open >= 0 {
			return parseTypeDeclarationFields(source, open)
		}
	}
	return nil
}

func applyScriptSetupDefaults(
	program *jssyntax.Node,
	props []VueComponentProp,
) []ScriptSetupPropDefault {
	positions := make(map[string]int, len(props))
	for index := range props {
		positions[props[index].Name] = index
	}
	var result []ScriptSetupPropDefault
	resultPositions := make(map[string]int)
	for _, call := range jsquery.Calls(program, "withDefaults") {
		if closestJavaScriptFunction(call) != nil {
			continue
		}
		defaults := jsquery.ObjectArgument(call, 1)
		for _, property := range jsquery.Properties(defaults) {
			name := jsquery.PropertyName(property)
			value := strings.TrimSpace(nodeText(jsquery.PropertyValue(property)))
			if name == "" || value == "" {
				continue
			}
			entry := ScriptSetupPropDefault{Name: name, Value: value}
			if resultIndex, found := resultPositions[name]; found {
				result[resultIndex] = entry
			} else {
				resultPositions[name] = len(result)
				result = append(result, entry)
			}
			index, found := positions[name]
			if !found {
				continue
			}
			props[index].Required = false
			props[index].Default = value
		}
	}
	return result
}

func applyScriptSetupDefaultsToProps(
	props []VueComponentProp,
	defaults []ScriptSetupPropDefault,
) {
	values := make(map[string]string, len(defaults))
	for _, value := range defaults {
		values[value.Name] = value.Value
	}
	for propIndex := range props {
		if value := values[props[propIndex].Name]; value != "" {
			props[propIndex].Default = value
			props[propIndex].Required = false
		}
	}
}

func scriptSetupReactivePropBindings(
	program *jssyntax.Node,
	lineIndex *cst.LineIndex,
) []ScriptSetupPropBinding {
	if program == nil {
		return nil
	}
	var result []ScriptSetupPropBinding
	positions := make(map[string]int)
	for _, declaration := range jsquery.Nodes(
		program, jssyntax.JsVariableDeclaration,
	) {
		declarationText := compactJavaScriptText(declaration.Text())
		if closestJavaScriptFunction(declaration) != nil ||
			(!strings.Contains(declarationText, "defineProps(") &&
				!strings.Contains(declarationText, "defineProps<")) {
			continue
		}
		pattern := scriptSetupDestructuringObject(declaration)
		if pattern == nil {
			continue
		}
		text := pattern.Text()
		leading := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
		if len(text)-leading < 2 || text[leading] != '{' {
			continue
		}
		close := strings.LastIndexByte(text, '}')
		if close <= leading {
			continue
		}
		for _, segment := range splitTopLevelDeclarations(
			text[leading+1:close],
			int(pattern.Range().Start)+leading+1,
		) {
			binding, found := scriptSetupReactivePropBinding(
				segment, lineIndex,
			)
			if !found {
				continue
			}
			if index, exists := positions[binding.BindingName]; exists {
				result[index] = binding
			} else {
				positions[binding.BindingName] = len(result)
				result = append(result, binding)
			}
		}
	}
	return result
}

func scriptSetupReactivePropBinding(
	segment declarationSegment,
	lineIndex *cst.LineIndex,
) (ScriptSetupPropBinding, bool) {
	raw := segment.text
	leading := len(raw) - len(strings.TrimLeftFunc(raw, unicode.IsSpace))
	value := strings.TrimSpace(raw)
	if value == "" || strings.HasPrefix(value, "...") {
		return ScriptSetupPropBinding{}, false
	}
	colon := indexSlotTopLevel(value, ':')
	equals := indexSlotTopLevel(value, '=')
	publicEnd := len(value)
	if colon >= 0 {
		publicEnd = colon
	} else if equals >= 0 {
		publicEnd = equals
	}
	propName := strings.Trim(strings.TrimSpace(value[:publicEnd]), "'\"")
	if !isAdminTypeIdentifier(propName) {
		return ScriptSetupPropBinding{}, false
	}
	bindingName := propName
	defaultValue := ""
	bindingStart := 0
	if colon >= 0 {
		right := strings.TrimSpace(value[colon+1:])
		rightLeading := len(value[colon+1:]) - len(strings.TrimLeftFunc(
			value[colon+1:], unicode.IsSpace,
		))
		bindingStart = colon + 1 + rightLeading
		if rightEquals := indexSlotTopLevel(right, '='); rightEquals >= 0 {
			bindingName = strings.TrimSpace(right[:rightEquals])
			defaultValue = strings.TrimSpace(right[rightEquals+1:])
		} else {
			bindingName = right
		}
	} else {
		bindingStart = strings.Index(value, propName)
		if equals >= 0 {
			defaultValue = strings.TrimSpace(value[equals+1:])
		}
	}
	if !isAdminTypeIdentifier(bindingName) {
		return ScriptSetupPropBinding{}, false
	}
	absoluteStart := segment.offset + leading + bindingStart
	line := 0
	nameRange := AdminSourceRange{}
	if lineIndex != nil {
		lineValue, _ := lineIndex.Position(uint32(absoluteStart))
		line = int(lineValue) + 1
		nameRange = sourceRangeAt(
			lineIndex, uint32(absoluteStart),
			uint32(absoluteStart+len(bindingName)), true,
		)
	}
	return ScriptSetupPropBinding{
		PropName: propName, BindingName: bindingName, Default: defaultValue,
		Line: line, NameRange: nameRange,
	}, true
}

func scriptSetupTypedEvents(
	source string,
	start,
	end int,
	filePath string,
	lineIndex *cst.LineIndex,
) []VueComponentEvent {
	if start < 0 || end < start || end > len(source) {
		return nil
	}
	typeExpression := source[start:end]
	seen := make(map[string]bool)
	var result []VueComponentEvent
	add := func(
		name,
		eventType string,
		documentation string,
		offset int,
		nameRange AdminSourceRange,
	) {
		name = strings.TrimSpace(strings.Trim(name, "'\""))
		if name == "" || seen[CanonicalEventName(name)] {
			return
		}
		seen[CanonicalEventName(name)] = true
		line, _ := lineIndex.Position(uint32(offset))
		result = append(result, VueComponentEvent{
			Name: name, Type: strings.TrimSpace(eventType),
			Documentation: documentation,
			FilePath:      filePath, Line: int(line) + 1,
			NameRange: nameRange,
		})
	}
	if open := strings.IndexByte(typeExpression, '{'); open >= 0 {
		for _, field := range parseTypeDeclarationFields(
			typeExpression, open,
		) {
			add(
				field.name, field.value,
				adminTypeFieldDocumentation(typeExpression, field.offset),
				start+field.offset,
				typeDeclarationFieldNameRange(field, lineIndex, start),
			)
		}
	}
	for cursor := 0; cursor < len(typeExpression); {
		colon := strings.IndexByte(typeExpression[cursor:], ':')
		if colon < 0 {
			break
		}
		colon += cursor
		value := strings.TrimLeft(typeExpression[colon+1:], " \t\r\n")
		leading := len(typeExpression[colon+1:]) - len(value)
		if len(value) > 2 && (value[0] == '\'' || value[0] == '"') {
			if close := strings.IndexByte(value[1:], value[0]); close >= 0 {
				nameStart := start + colon + 1 + leading + 1
				eventType := ""
				documentation := ""
				if open := strings.LastIndex(typeExpression[:colon], "("); open >= 0 {
					if signatureEnd := matchingSlotDelimiter(
						typeExpression, open, '(', ')',
					); signatureEnd > open {
						eventType = strings.TrimSpace(
							typeExpression[open : signatureEnd+1],
						)
						documentation = adminTypeFieldDocumentation(
							typeExpression, open,
						)
					}
				}
				add(
					value[1:1+close], eventType, documentation, nameStart,
					sourceRangeAt(
						lineIndex, uint32(nameStart),
						uint32(nameStart+close), false,
					),
				)
			}
		}
		cursor = colon + 1
	}
	return result
}

func scriptSetupModel(
	source string,
	macro scriptSetupMacro,
	filePath string,
	lineIndex *cst.LineIndex,
) (VueComponentProp, VueComponentEvent) {
	name := "modelValue"
	arguments := splitSlotTopLevel(
		source[macro.argsStart:macro.argsEnd], ',',
	)
	option := ""
	if len(arguments) > 0 {
		first := strings.TrimSpace(arguments[0])
		if len(first) >= 2 && (first[0] == '\'' || first[0] == '"') &&
			first[len(first)-1] == first[0] {
			name = first[1 : len(first)-1]
			if len(arguments) > 1 {
				option = strings.TrimSpace(arguments[1])
			}
		} else {
			option = first
		}
	}
	line, _ := lineIndex.Position(uint32(macro.nameStart))
	prop := VueComponentProp{
		Name: name, FilePath: filePath, Line: int(line) + 1,
	}
	if len(arguments) > 0 {
		first := arguments[0]
		trimmed := strings.TrimSpace(first)
		if len(trimmed) >= 2 &&
			(trimmed[0] == '\'' || trimmed[0] == '"') &&
			trimmed[len(trimmed)-1] == trimmed[0] {
			leading := strings.Index(first, trimmed)
			start := macro.argsStart + leading + 1
			prop.NameRange = sourceRangeAt(
				lineIndex, uint32(start), uint32(start+len(name)), false,
			)
		}
	}
	if macro.typeStart >= 0 {
		prop.Type = strings.TrimSpace(source[macro.typeStart:macro.typeEnd])
	}
	if open := strings.IndexByte(option, '{'); open >= 0 {
		for _, field := range parseTypeDeclarationFields(option, open) {
			switch field.name {
			case "type":
				if prop.Type == "" {
					prop.Type = meteorRuntimeType(field.value)
				}
			case "required":
				prop.Required = strings.TrimSpace(field.value) == "true"
			case "default":
				prop.Default = strings.TrimSpace(field.value)
				prop.Required = false
			}
		}
	}
	event := VueComponentEvent{
		Name: "update:" + CamelToKebab(name), Type: prop.Type,
		FilePath: filePath, Line: prop.Line, NameRange: prop.NameRange,
	}
	if event.NameRange == (AdminSourceRange{}) {
		event.NameRange = sourceRangeAt(
			lineIndex, uint32(macro.nameStart),
			uint32(macro.nameStart+len("defineModel")), true,
		)
	}
	return prop, event
}

func overlayScriptSetupProps(
	base,
	overlay []VueComponentProp,
) []VueComponentProp {
	result := append([]VueComponentProp(nil), base...)
	positions := make(map[string]int, len(result))
	for index := range result {
		positions[result[index].Name] = index
	}
	for _, prop := range overlay {
		if prop.Name == "" {
			continue
		}
		if index, found := positions[prop.Name]; found {
			current := result[index]
			if prop.Documentation == "" {
				prop.Documentation = current.Documentation
			}
			if prop.Type == "" {
				prop.Type = current.Type
			}
			if prop.Default == "" {
				prop.Default = current.Default
			}
			prop.Required = prop.Required || current.Required
			if prop.FilePath == "" {
				prop.FilePath = current.FilePath
			}
			if prop.Line == 0 {
				prop.Line = current.Line
			}
			if prop.NameRange == (AdminSourceRange{}) {
				prop.NameRange = current.NameRange
			}
			result[index] = prop
			continue
		}
		positions[prop.Name] = len(result)
		result = append(result, prop)
	}
	return result
}

func overlayScriptSetupPropDefaults(
	base,
	overlay []ScriptSetupPropDefault,
) []ScriptSetupPropDefault {
	result := append([]ScriptSetupPropDefault(nil), base...)
	positions := make(map[string]int, len(result))
	for index := range result {
		positions[result[index].Name] = index
	}
	for _, value := range overlay {
		if value.Name == "" || value.Value == "" {
			continue
		}
		if index, found := positions[value.Name]; found {
			result[index] = value
			continue
		}
		positions[value.Name] = len(result)
		result = append(result, value)
	}
	return result
}

func overlayScriptSetupPropBindings(
	base,
	overlay []ScriptSetupPropBinding,
) []ScriptSetupPropBinding {
	result := append([]ScriptSetupPropBinding(nil), base...)
	positions := make(map[string]int, len(result))
	for index := range result {
		positions[result[index].BindingName] = index
	}
	for _, binding := range overlay {
		if binding.PropName == "" || binding.BindingName == "" {
			continue
		}
		if index, found := positions[binding.BindingName]; found {
			result[index] = binding
			continue
		}
		positions[binding.BindingName] = len(result)
		result = append(result, binding)
	}
	return result
}

func scriptSetupPropMembers(
	program *jssyntax.Node,
	props []VueComponentProp,
	source string,
	lineIndex *cst.LineIndex,
) []VueComponentMember {
	locations := make(map[string]*jssyntax.Node)
	for _, call := range jsquery.Calls(program, "defineProps") {
		for _, property := range jsquery.Properties(
			jsquery.ArgumentExpression(call, 0),
		) {
			locations[jsquery.PropertyName(property)] =
				jsquery.PropertyNameNode(property)
		}
	}
	var result []VueComponentMember
	for _, prop := range props {
		member := VueComponentMember{
			Name: prop.Name, Kind: ComponentMemberProp, Type: prop.Type,
			Line: prop.Line, BindingName: prop.Name,
		}
		if node := locations[prop.Name]; node != nil {
			member.NameRange = componentMemberNameRange(node, lineIndex)
		} else if offset := scriptSetupTypePropertyOffset(source, prop); offset >= 0 {
			member.NameRange = sourceRangeAt(
				lineIndex, uint32(offset), uint32(offset+len(prop.Name)), true,
			)
		}
		result = append(result, member)
	}
	return result
}

func scriptSetupTypePropertyOffset(source string, prop VueComponentProp) int {
	if prop.Line <= 0 || prop.Name == "" {
		return -1
	}
	lines := strings.Split(source, "\n")
	if prop.Line > len(lines) {
		return -1
	}
	lineStart := 0
	for index := 1; index < prop.Line; index++ {
		lineStart += len(lines[index-1]) + 1
	}
	if relative := strings.Index(lines[prop.Line-1], prop.Name); relative >= 0 {
		return lineStart + relative
	}
	return -1
}

func scriptSetupBindings(
	program *jssyntax.Node,
	lineIndex *cst.LineIndex,
) []VueComponentMember {
	known := make(map[string]string)
	var result []VueComponentMember
	for _, declaration := range jsquery.Nodes(
		program, jssyntax.JsVariableDeclaration,
	) {
		if closestJavaScriptFunction(declaration) != nil {
			continue
		}
		if nameNode := firstDirectIdentifier(declaration); nameNode != nil {
			name := strings.TrimSpace(jsquery.IdentifierText(nameNode))
			if name == "" {
				continue
			}
			kind := componentSetupBindingKind(declaration)
			memberType, expression, open := componentSetupBindingInference(
				declaration, kind, known,
			)
			line, _ := lineIndex.Position(nameNode.RangeTrimmedTrivia().Start)
			result = append(result, VueComponentMember{
				Name: name, BindingName: name, Kind: kind, Type: memberType,
				Line:             int(line) + 1,
				NameRange:        componentMemberNameRange(nameNode, lineIndex),
				SourceExpression: expression, OpenRuntimeShape: open,
			})
			if memberType != "" {
				known[name] = memberType
			}
			continue
		}
		pattern := scriptSetupDestructuringObject(declaration)
		for _, property := range jsquery.Properties(pattern) {
			name := strings.TrimSpace(jsquery.PropertyName(property))
			if name == "" || strings.HasPrefix(
				compactJavaScriptText(property.Text()), "...",
			) {
				continue
			}
			nameNode := jsquery.PropertyNameNode(property)
			bindingName := name
			if value := jsquery.PropertyValue(property); value != nil &&
				value.Kind() == jssyntax.JsIdentifier {
				bindingName = strings.TrimSpace(jsquery.IdentifierText(value))
			}
			line, _ := lineIndex.Position(property.RangeTrimmedTrivia().Start)
			result = append(result, VueComponentMember{
				Name: bindingName, BindingName: bindingName,
				Kind: ComponentMemberData, Line: int(line) + 1,
				NameRange: componentMemberNameRange(nameNode, lineIndex),
				Shorthand: valueIsNil(property),
			})
		}
	}
	for _, function := range jsquery.Nodes(program, jssyntax.JsFunction) {
		if closestJavaScriptFunction(function) != nil {
			continue
		}
		nameNode := firstDirectIdentifier(function)
		if nameNode == nil {
			continue
		}
		name := strings.TrimSpace(jsquery.IdentifierText(nameNode))
		line, _ := lineIndex.Position(nameNode.RangeTrimmedTrivia().Start)
		result = append(result, VueComponentMember{
			Name: name, BindingName: name, Kind: ComponentMemberMethod,
			Type: vueMethodSignature(function, known), Line: int(line) + 1,
			NameRange:        componentMemberNameRange(nameNode, lineIndex),
			SourceExpression: vueMethodReturnExpression(function),
		})
	}
	return result
}

func scriptSetupDestructuringObject(
	declaration *jssyntax.Node,
) *jssyntax.Node {
	if declaration == nil {
		return nil
	}
	text := declaration.Text()
	equals := indexSlotTopLevel(text, '=')
	if equals < 0 {
		return nil
	}
	cutoff := declaration.Range().Start + uint32(equals)
	var result *jssyntax.Node
	for _, object := range jsquery.Nodes(declaration, jssyntax.JsObject) {
		rangeValue := object.RangeTrimmedTrivia()
		if rangeValue.End > cutoff {
			continue
		}
		if result == nil || rangeValue.Start < result.RangeTrimmedTrivia().Start {
			result = object
		}
	}
	return result
}

func valueIsNil(property *jssyntax.Node) bool {
	return jsquery.PropertyValue(property) == nil
}

func scriptSetupImports(
	program *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) ([]VueComponentMember, []VueLocalComponent) {
	var members []VueComponentMember
	var components []VueLocalComponent
	for _, statement := range jsquery.Nodes(program, jssyntax.JsImportStatement) {
		path := jsquery.ImportPath(statement, "")
		for _, binding := range scriptSetupImportBindings(statement.Text()) {
			if binding == "" {
				continue
			}
			rangeValue := statement.RangeTrimmedTrivia()
			relative := strings.Index(statement.Text(), binding)
			if relative >= 0 {
				rangeValue.Start = statement.Range().Start + uint32(relative)
				rangeValue.End = rangeValue.Start + uint32(len(binding))
			}
			line, _ := lineIndex.Position(rangeValue.Start)
			nameRange := sourceRangeAt(
				lineIndex, rangeValue.Start, rangeValue.End, true,
			)
			members = append(members, VueComponentMember{
				Name: binding, BindingName: binding, Kind: ComponentMemberData,
				Line: int(line) + 1, NameRange: nameRange,
			})
			if !scriptSetupComponentImport(binding, path) {
				continue
			}
			components = append(components, VueLocalComponent{
				Name: CamelToKebab(binding), Symbol: binding, ImportPath: path,
				FilePath: filePath, Line: int(line) + 1,
				NameRange: nameRange,
			})
		}
	}
	return members, components
}

func scriptSetupImportBindings(statement string) []string {
	statement = strings.TrimSpace(statement)
	if !strings.HasPrefix(statement, "import") {
		return nil
	}
	specifier := strings.TrimSpace(strings.TrimPrefix(statement, "import"))
	if strings.HasPrefix(specifier, "type ") {
		return nil
	}
	if from := strings.LastIndex(specifier, " from "); from >= 0 {
		specifier = strings.TrimSpace(specifier[:from])
	} else {
		return nil
	}
	var result []string
	if open := strings.IndexByte(specifier, '{'); open >= 0 {
		close := matchingSlotDelimiter(specifier, open, '{', '}')
		if close > open {
			for _, raw := range splitSlotTopLevel(specifier[open+1:close], ',') {
				parts := strings.Fields(strings.TrimSpace(raw))
				if len(parts) == 0 || parts[0] == "type" {
					continue
				}
				name := parts[0]
				if len(parts) >= 3 && parts[len(parts)-2] == "as" {
					name = parts[len(parts)-1]
				}
				result = appendUnique(result, name)
			}
		}
		specifier = strings.TrimSpace(strings.TrimSuffix(specifier[:open], ","))
	}
	if strings.HasPrefix(specifier, "*") {
		parts := strings.Fields(specifier)
		if len(parts) >= 3 && parts[len(parts)-2] == "as" {
			result = appendUnique(result, parts[len(parts)-1])
		}
	} else if name := strings.TrimSpace(specifier); isStaticVueIdentifier(name) {
		result = appendUnique(result, name)
	}
	return result
}

func scriptSetupComponentImport(binding, importPath string) bool {
	if binding == "" || importPath == "" ||
		binding[0] < 'A' || binding[0] > 'Z' {
		return false
	}
	ext := strings.ToLower(filepath.Ext(importPath))
	return ext == ".vue" || ext == "" && importPath != "vue"
}

func overlayScriptSetupMembers(
	base,
	overlay []VueComponentMember,
) []VueComponentMember {
	result := append([]VueComponentMember(nil), base...)
	positions := make(map[string]int, len(result))
	for index := range result {
		positions[result[index].Name] = index
	}
	for _, member := range overlay {
		if member.Name == "" {
			continue
		}
		if index, found := positions[member.Name]; found {
			current := result[index]
			if current.Type != "" && member.Type == "" {
				member.Type = current.Type
			}
			if current.Kind == ComponentMemberProp {
				member.Kind = current.Kind
			}
			if member.NameRange == (AdminSourceRange{}) {
				member.NameRange = current.NameRange
			}
			result[index] = member
			continue
		}
		positions[member.Name] = len(result)
		result = append(result, member)
	}
	return result
}

func populateScriptSetupLegacyMembers(definition *ComponentDefinition) {
	if definition == nil {
		return
	}
	for _, member := range definition.Members {
		switch member.Kind {
		case ComponentMemberData:
			definition.Data = appendUnique(definition.Data, member.Name)
		case ComponentMemberComputed:
			definition.Computed = appendUnique(definition.Computed, member.Name)
		case ComponentMemberMethod:
			definition.Methods = appendUnique(definition.Methods, member.Name)
		}
	}
}

func sourceRangeAt(
	lineIndex *cst.LineIndex,
	start,
	end uint32,
	identifier bool,
) AdminSourceRange {
	if lineIndex == nil {
		return AdminSourceRange{}
	}
	startLine, startCharacter := lineIndex.PositionUTF16(start)
	endLine, endCharacter := lineIndex.PositionUTF16(end)
	return AdminSourceRange{
		StartLine: int(startLine), StartCharacter: int(startCharacter),
		EndLine: int(endLine), EndCharacter: int(endCharacter),
		Declaration: true, Identifier: identifier,
	}
}
