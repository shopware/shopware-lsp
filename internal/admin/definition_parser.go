package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

// ComponentDefinition holds the parsed component definition details.
type ComponentDefinition struct {
	FilePath           string
	Deprecated         string
	Props              []VueComponentProp
	ModelProp          string
	ModelEvent         string
	Emits              []string
	Events             []VueComponentEvent
	Methods            []string
	Computed           []string
	Data               []string
	Injected           []string
	Mixins             []string
	LocalComponents    []VueLocalComponent
	LocalDirectives    []VueLocalDirective
	Members            []VueComponentMember
	OpenRuntimeMembers bool
	Assignments        []VueComponentAssignment
	Slots              []VueComponentSlot
	Blocks             []TwigBlock
	TemplatePath       string
	HasTemplate        bool

	// ScriptSetupPropTypes and ScriptSetupEventTypes retain the lexical type
	// arguments of Vue compiler macros. They are resolved lazily through the
	// Administration type index so imported declarations work independently of
	// file indexing order and react to changes in their owning type file.
	ScriptSetupPropTypes    []string
	ScriptSetupEventTypes   []string
	ScriptSetupSlotTypes    []string
	ScriptSetupPropDefaults []ScriptSetupPropDefault
	ScriptSetupPropBindings []ScriptSetupPropBinding
}

// ScriptSetupPropDefault is one withDefaults entry associated with a typed
// defineProps contract. Defaults are retained separately because an imported
// prop may not be materialized until the type index is queried later.
type ScriptSetupPropDefault struct {
	Name  string
	Value string
}

// ScriptSetupPropBinding maps one public prop to the local identifier created
// by Vue's reactive destructuring syntax. Keeping the local declaration range
// separate prevents template navigation and rename from conflating a private
// alias with the public component prop.
type ScriptSetupPropBinding struct {
	PropName    string
	BindingName string
	Default     string
	Line        int
	NameRange   AdminSourceRange
}

// ParseComponentDefinition extracts a Vue component's public definition from
// an export-default object.
func ParseComponentDefinition(root *jssyntax.Node, content []byte) *ComponentDefinition {
	return ParseComponentDefinitionWithLineIndex(
		root,
		jssyntax.NewLineIndex(string(content)),
	)
}

func ParseComponentDefinitionWithLineIndex(
	root *jssyntax.Node,
	lineIndex *cst.LineIndex,
) *ComponentDefinition {
	definition := &ComponentDefinition{}
	exports := jsquery.ExportDefaults(root)
	if len(exports) == 0 {
		return definition
	}
	definition.Deprecated = JavaScriptDeprecation(exports[0])

	object := componentDefinitionObject(
		jsquery.ExportDefaultExpression(exports[0]),
	)
	if object == nil {
		return definition
	}

	parseDefinitionObject(object, definition, lineIndex)
	mergeJavaScriptEventAnnotations(
		definition,
		JavaScriptEventAnnotations(exports[0], lineIndex),
		jsquery.Property(object, "emits") != nil,
	)
	enrichLocalComponentImports(root, definition)
	definition.TemplatePath = jsquery.ImportPath(root, "template")
	return definition
}

// componentDefinitionObject unwraps the definition forms used by current
// Administration code while remaining conservative about arbitrary factory
// calls. Shopware uses both Vue's defineComponent and its own typed Meteor
// wrapper around the same Options API object.
func componentDefinitionObject(expression *jssyntax.Node) *jssyntax.Node {
	if expression == nil {
		return nil
	}
	if expression.Kind() == jssyntax.JsObject {
		return expression
	}
	if expression.Kind() != jssyntax.JsCallExpression {
		return nil
	}
	switch jsquery.CallName(expression) {
	case "defineComponent", "Vue.defineComponent",
		"Component.wrapComponentConfig",
		"Shopware.Component.wrapComponentConfig":
		return jsquery.ObjectArgument(expression, 0)
	default:
		return nil
	}
}

// ComponentDefinitionObject unwraps the component-definition expression forms
// used by Shopware and Vue. It exposes the lossless source node for editor
// features which need declaration ranges in addition to the normalized model.
func ComponentDefinitionObject(expression *jssyntax.Node) *jssyntax.Node {
	return componentDefinitionObject(expression)
}

// ParseComponentObject normalizes one live Options API object without reading
// its imported template. Callers therefore see unsaved document changes
// immediately and can decide independently whether external files are needed.
func ParseComponentObject(
	object *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) *ComponentDefinition {
	if object == nil || object.Kind() != jssyntax.JsObject {
		return nil
	}
	definition := &ComponentDefinition{FilePath: filePath}
	parseDefinitionObject(object, definition, lineIndex)
	setDefinitionFilePath(definition, filePath)
	return definition
}

func parseDefinitionObject(
	object *jssyntax.Node,
	definition *ComponentDefinition,
	lineIndex *cst.LineIndex,
) {
	for _, property := range jsquery.Properties(object) {
		name := jsquery.PropertyName(property)
		value := jsquery.PropertyValue(property)
		if name == "" && strings.HasPrefix(
			strings.TrimSpace(property.Text()), "...",
		) {
			definition.OpenRuntimeMembers = true
		}
		switch name {
		case "props":
			definition.OpenRuntimeMembers = definition.OpenRuntimeMembers ||
				value != nil && strings.Contains(value.Text(), "...")
			definition.Props = parseProps(value, lineIndex)
			definition.Members = append(
				definition.Members,
				memberDefinitions(property, definition.Props, ComponentMemberProp, lineIndex)...,
			)
		case "model":
			definition.ModelProp, definition.ModelEvent =
				parseComponentModel(value)
		case "emits":
			for _, event := range parseEventDeclarations(value, lineIndex) {
				definition.Events = appendComponentEvent(definition.Events, event)
				definition.Emits = appendUnique(definition.Emits, event.Name)
			}
		case "methods":
			definition.OpenRuntimeMembers = definition.OpenRuntimeMembers ||
				value != nil && strings.Contains(value.Text(), "...")
			definition.Methods = parseMethodNames(value)
			definition.Members = append(
				definition.Members,
				memberDefinitions(property, definition.Methods, ComponentMemberMethod, lineIndex)...,
			)
		case "computed":
			definition.OpenRuntimeMembers = definition.OpenRuntimeMembers ||
				value != nil && strings.Contains(value.Text(), "...")
			definition.Computed = parseMethodNames(value)
			definition.Members = append(
				definition.Members,
				memberDefinitions(property, definition.Computed, ComponentMemberComputed, lineIndex)...,
			)
		case "data":
			definition.OpenRuntimeMembers = definition.OpenRuntimeMembers ||
				strings.Contains(property.Text(), "...")
			definition.Data = parseDataNames(property)
			definition.Members = append(
				definition.Members,
				memberDefinitions(property, definition.Data, ComponentMemberData, lineIndex)...,
			)
		case "inject":
			definition.Injected = parseInjectNames(value)
			definition.Members = append(
				definition.Members,
				memberDefinitions(property, definition.Injected, ComponentMemberInject, lineIndex)...,
			)
		case "setup":
			definition.OpenRuntimeMembers = definition.OpenRuntimeMembers ||
				strings.Contains(property.Text(), "...")
			definition.Members = append(
				definition.Members,
				parseComponentSetupMembers(property, lineIndex)...,
			)
		case "mixins":
			definition.Mixins = parseMixinNames(value)
			if len(definition.Mixins) == 0 {
				definition.OpenRuntimeMembers = true
			}
		case "components":
			definition.LocalComponents = parseLocalComponents(value, lineIndex)
		case "directives":
			definition.LocalDirectives = parseLocalDirectives(value, lineIndex)
		case "template":
			definition.HasTemplate = true
		}
	}
	for _, event := range parseEmittedEvents(object, lineIndex) {
		definition.Events = appendComponentEvent(definition.Events, event)
		definition.Emits = appendUnique(definition.Emits, event.Name)
	}
	definition.Assignments = parseComponentAssignments(object, lineIndex)
	enrichDefinitionMemberTypes(object, definition)
	enrichDefinitionCollectionElementMembers(object, definition, lineIndex)
}

func parseComponentModel(node *jssyntax.Node) (string, string) {
	if node == nil || node.Kind() != jssyntax.JsObject {
		return "", ""
	}
	propName := jsquery.StringValue(
		jsquery.PropertyValue(jsquery.Property(node, "prop")),
	)
	eventName := jsquery.StringValue(
		jsquery.PropertyValue(jsquery.Property(node, "event")),
	)
	if propName == "" {
		propName = "value"
	}
	if eventName == "" {
		eventName = "input"
	}
	return propName, eventName
}

type componentSetupBinding struct {
	kind             VueComponentMemberKind
	memberType       string
	sourceExpression string
	openRuntimeShape bool
	cmsRegistryKind  AdminCMSRegistrationKind
}

// parseComponentSetupMembers extracts the values which a Composition API
// setup function explicitly exposes through a statically returned object.
// Locals which are not returned remain implementation details, and spreads or
// arbitrary return expressions are deliberately ignored.
func parseComponentSetupMembers(
	property *jssyntax.Node,
	lineIndex *cst.LineIndex,
) []VueComponentMember {
	setup := componentSetupFunction(property)
	if setup == nil {
		return nil
	}
	bindings := componentSetupBindings(setup)
	positions := make(map[string]int)
	var result []VueComponentMember
	add := func(member VueComponentMember) {
		if member.Name == "" {
			return
		}
		if index, found := positions[member.Name]; found {
			// Prefer the declaration carrying the stronger static contract. This
			// also handles an early return followed by the normal setup return.
			current := result[index]
			if current.Type != "" && member.Type == "" {
				member.Type = current.Type
			}
			if current.SourceExpression != "" && member.SourceExpression == "" {
				member.SourceExpression = current.SourceExpression
			}
			if current.CMSRegistryKind != "" && member.CMSRegistryKind == "" {
				member.CMSRegistryKind = current.CMSRegistryKind
			}
			member.OpenRuntimeShape = member.OpenRuntimeShape || current.OpenRuntimeShape
			result[index] = member
			return
		}
		positions[member.Name] = len(result)
		result = append(result, member)
	}

	for _, returned := range componentSetupReturnedObjects(setup) {
		for _, returnedProperty := range jsquery.Properties(returned) {
			if strings.HasPrefix(
				compactJavaScriptText(returnedProperty.Text()), "...",
			) {
				continue
			}
			name := strings.TrimSpace(jsquery.PropertyName(returnedProperty))
			if name == "" {
				continue
			}
			line := 0
			if lineIndex != nil {
				lineNumber, _ := lineIndex.Position(
					returnedProperty.RangeTrimmedTrivia().Start,
				)
				line = int(lineNumber) + 1
			}
			member := VueComponentMember{
				Name: name, Kind: ComponentMemberData, Line: line,
				Deprecated: JavaScriptDeprecation(returnedProperty),
			}
			bindingName := name
			value := jsquery.PropertyValue(returnedProperty)
			member.NameRange = componentMemberNameRange(
				jsquery.PropertyNameNode(returnedProperty), lineIndex,
			)
			if value != nil && value.Kind() == jssyntax.JsIdentifier {
				bindingName = strings.TrimSpace(jsquery.IdentifierText(value))
			}
			member.BindingName = bindingName
			member.Shorthand = value == nil && isStaticVueIdentifier(name)
			if binding, found := bindings[bindingName]; found {
				member.Kind = binding.kind
				member.Type = binding.memberType
				member.SourceExpression = binding.sourceExpression
				member.OpenRuntimeShape = binding.openRuntimeShape
				member.CMSRegistryKind = binding.cmsRegistryKind
			} else if returnedProperty.Kind() == jssyntax.JsMethod {
				member.Kind = ComponentMemberMethod
				member.Type = vueMethodSignature(returnedProperty, nil)
				member.SourceExpression = vueMethodReturnExpression(returnedProperty)
				member.CMSRegistryKind, _ =
					cmsRegistryKindFromExpression(returnedProperty.Text())
			} else if value != nil {
				member.Type = vueExpressionType(value, nil)
				member.SourceExpression = trimVueSourceExpression(value.Text())
				member.OpenRuntimeShape = value.Kind() == jssyntax.JsObject
			}
			add(member)
		}
	}
	return result
}

func componentSetupFunction(property *jssyntax.Node) *jssyntax.Node {
	if property == nil {
		return nil
	}
	if property.Kind() == jssyntax.JsMethod {
		return property
	}
	value := jsquery.PropertyValue(property)
	if value == nil {
		return nil
	}
	switch value.Kind() {
	case jssyntax.JsFunction, jssyntax.JsArrowFunction:
		return value
	default:
		return nil
	}
}

func componentSetupReturnedObjects(setup *jssyntax.Node) []*jssyntax.Node {
	var result []*jssyntax.Node
	for _, statement := range jsquery.Nodes(setup, jssyntax.JsReturnStatement) {
		if closestJavaScriptFunction(statement) != setup {
			continue
		}
		for _, object := range jsquery.Nodes(statement, jssyntax.JsObject) {
			if closestJavaScriptFunction(object) == setup &&
				componentSetupObjectIsDirectReturn(statement, object) {
				result = append(result, object)
				break
			}
		}
	}
	if len(result) == 0 && setup.Kind() == jssyntax.JsArrowFunction {
		for _, object := range jsquery.Nodes(setup, jssyntax.JsObject) {
			if closestJavaScriptFunction(object) == setup &&
				componentSetupObjectIsDirectArrowBody(setup, object) {
				result = append(result, object)
				break
			}
		}
	}
	return result
}

func componentSetupObjectIsDirectReturn(
	statement,
	object *jssyntax.Node,
) bool {
	if statement == nil || object == nil {
		return false
	}
	statementRange := statement.Range()
	objectRange := object.RangeTrimmedTrivia()
	if objectRange.Start < statementRange.Start || objectRange.End > statementRange.End {
		return false
	}
	text := statement.Text()
	start := int(objectRange.Start - statementRange.Start)
	end := int(objectRange.End - statementRange.Start)
	if start < 0 || end < start || end > len(text) {
		return false
	}
	before := strings.TrimSpace(text[:start])
	if !strings.HasPrefix(before, "return") {
		return false
	}
	before = strings.TrimSpace(strings.TrimPrefix(before, "return"))
	after := strings.TrimSpace(text[end:])
	return strings.Trim(before, "( \t\r\n") == "" &&
		strings.Trim(after, "); \t\r\n") == ""
}

func componentSetupObjectIsDirectArrowBody(
	setup,
	object *jssyntax.Node,
) bool {
	if setup == nil || object == nil {
		return false
	}
	setupRange := setup.Range()
	objectRange := object.RangeTrimmedTrivia()
	if objectRange.Start < setupRange.Start || objectRange.End > setupRange.End {
		return false
	}
	text := setup.Text()
	start := int(objectRange.Start - setupRange.Start)
	end := int(objectRange.End - setupRange.Start)
	if start < 0 || end < start || end > len(text) {
		return false
	}
	before := text[:start]
	arrow := strings.LastIndex(before, "=>")
	if arrow < 0 {
		return false
	}
	before = strings.TrimSpace(before[arrow+2:])
	after := strings.TrimSpace(text[end:])
	return strings.Trim(before, "( \t\r\n") == "" &&
		strings.Trim(after, "),; \t\r\n") == ""
}

func componentSetupBindings(
	setup *jssyntax.Node,
) map[string]componentSetupBinding {
	bindings := make(map[string]componentSetupBinding)
	known := make(map[string]string)
	for _, declaration := range jsquery.Nodes(
		setup, jssyntax.JsVariableDeclaration,
	) {
		if closestJavaScriptFunction(declaration) != setup {
			continue
		}
		nameNode := firstDirectIdentifier(declaration)
		if nameNode == nil {
			continue
		}
		name := strings.TrimSpace(jsquery.IdentifierText(nameNode))
		if name == "" {
			continue
		}
		kind := componentSetupBindingKind(declaration)
		memberType, sourceExpression, openRuntimeShape :=
			componentSetupBindingInference(declaration, kind, known)
		cmsRegistryKind, _ := cmsRegistryKindFromExpression(declaration.Text())
		bindings[name] = componentSetupBinding{
			kind: kind, memberType: memberType,
			sourceExpression: sourceExpression,
			openRuntimeShape: openRuntimeShape,
			cmsRegistryKind:  cmsRegistryKind,
		}
		if memberType != "" {
			known[name] = memberType
		}
	}
	for _, function := range jsquery.Nodes(setup, jssyntax.JsFunction) {
		if closestJavaScriptFunction(function) != setup {
			continue
		}
		nameNode := firstDirectIdentifier(function)
		if nameNode == nil {
			continue
		}
		name := strings.TrimSpace(jsquery.IdentifierText(nameNode))
		if name == "" {
			continue
		}
		cmsRegistryKind, _ := cmsRegistryKindFromExpression(function.Text())
		bindings[name] = componentSetupBinding{
			kind:             ComponentMemberMethod,
			memberType:       vueMethodSignature(function, known),
			sourceExpression: vueMethodReturnExpression(function),
			cmsRegistryKind:  cmsRegistryKind,
		}
	}
	return bindings
}

func componentSetupBindingKind(
	declaration *jssyntax.Node,
) VueComponentMemberKind {
	compact := compactJavaScriptText(declaration.Text())
	if strings.Contains(compact, "=computed(") ||
		strings.Contains(compact, "=computed<") {
		return ComponentMemberComputed
	}
	initializer := strings.TrimSpace(componentSetupInitializer(declaration))
	functionInitializer := strings.HasPrefix(initializer, "function") ||
		strings.HasPrefix(initializer, "async function") ||
		indexVueTopLevelOperator(initializer, "=>") >= 0
	if functionInitializer {
		return ComponentMemberMethod
	}
	return ComponentMemberData
}

func componentSetupBindingInference(
	declaration *jssyntax.Node,
	kind VueComponentMemberKind,
	known map[string]string,
) (memberType, sourceExpression string, openRuntimeShape bool) {
	if declaration == nil {
		return "", "", false
	}
	storeKind := AdminStoreState
	switch kind {
	case ComponentMemberComputed:
		storeKind = AdminStoreGetter
	case ComponentMemberMethod:
		storeKind = AdminStoreAction
	}
	memberType = unwrapVueSetupRefType(
		setupStoreBindingType(declaration, storeKind),
	)
	initializer := componentSetupInitializer(declaration)
	if initializer == "" {
		return memberType, "", false
	}
	sourceExpression = initializer
	compactInitializer := compactJavaScriptText(initializer)
	for _, call := range jsquery.Calls(declaration) {
		callName := jsquery.CallName(call)
		if callName != "ref" && callName != "shallowRef" &&
			callName != "reactive" && callName != "computed" {
			continue
		}
		if !strings.HasPrefix(compactInitializer, callName+"(") &&
			!strings.HasPrefix(compactInitializer, callName+"<") {
			continue
		}
		argument := jsquery.ArgumentExpression(call, 0)
		if argument == nil {
			break
		}
		if callName == "computed" {
			sourceExpression = vueMethodReturnExpression(argument)
			if inferred := vueMethodReturnType(argument, known); inferred != "" {
				memberType = inferred
			} else if inferred := vueExpressionTextType(
				unwrapComponentSetupExpression(sourceExpression), known,
			); inferred != "" {
				memberType = inferred
			}
			openRuntimeShape = vueRuntimeObjectLiteralExpression(sourceExpression)
		} else {
			sourceExpression = trimVueSourceExpression(argument.Text())
			if inferred := vueExpressionType(argument, known); inferred != "" {
				memberType = inferred
			}
			openRuntimeShape = argument.Kind() == jssyntax.JsObject
		}
		break
	}
	if kind == ComponentMemberMethod {
		for _, function := range jsquery.Nodes(
			declaration, jssyntax.JsArrowFunction, jssyntax.JsFunction,
		) {
			memberType = vueMethodSignature(function, known)
			sourceExpression = vueMethodReturnExpression(function)
			openRuntimeShape = vueRuntimeObjectLiteralExpression(sourceExpression)
			break
		}
	}
	return memberType, sourceExpression, openRuntimeShape
}

func unwrapComponentSetupExpression(value string) string {
	value = trimVueSourceExpression(value)
	for len(value) >= 2 && value[0] == '(' &&
		matchingSlotDelimiter(value, 0, '(', ')') == len(value)-1 {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}

func componentSetupInitializer(declaration *jssyntax.Node) string {
	if declaration == nil {
		return ""
	}
	text := strings.TrimSpace(declaration.Text())
	equals := strings.IndexByte(text, '=')
	if equals < 0 {
		return ""
	}
	return trimVueSourceExpression(text[equals+1:])
}

func unwrapVueSetupRefType(value string) string {
	value = strings.TrimSpace(value)
	for _, wrapper := range []string{
		"Ref", "ShallowRef", "ComputedRef", "WritableComputedRef",
	} {
		prefix := wrapper + "<"
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		close := matchingSlotDelimiter(value, len(wrapper), '<', '>')
		if close == len(value)-1 {
			return strings.TrimSpace(value[len(prefix):close])
		}
	}
	return value
}

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
	for _, call := range jsquery.Calls(
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
