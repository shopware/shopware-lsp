package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
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
