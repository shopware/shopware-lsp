package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

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
