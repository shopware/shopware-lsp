package admin

import (
	"strings"

	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

func JavaScriptSymbolAt(node *jssyntax.Node) (AdminSymbolTarget, bool) {
	if node == nil {
		return AdminSymbolTarget{}, false
	}
	if containerName, memberName, matched :=
		JavaScriptApplicationContainerMember(node); matched &&
		containerName == "service" && memberName != "" {
		return AdminSymbolTarget{
			Kind: AdminSymbolService, Name: memberName,
		}, true
	}
	if storeName, memberName, matched := jsquery.StoreMember(node); matched && memberName != "" {
		return AdminSymbolTarget{
			Kind: AdminSymbolStoreMember, Owner: storeName, Name: memberName,
		}, true
	}
	if IsServiceReference(node) {
		return stringTarget(AdminSymbolService, node)
	}
	if IsStoreReference(node) {
		return stringTarget(AdminSymbolStore, node)
	}
	if IsPrivilegeReference(node) {
		return stringTarget(AdminSymbolPrivilege, node)
	}
	if _, eventName, found := JavaScriptShopwareEventBusEventAt(node); found &&
		eventName != "" {
		return AdminSymbolTarget{
			Kind: AdminSymbolEventBusEvent, Name: eventName,
		}, true
	}
	if IsJavaScriptModuleRouteReference(node) {
		return stringTarget(AdminSymbolModuleRoute, node)
	}
	if reference, found := JavaScriptCMSReferenceAt(node); found {
		return reference.AdminSymbolTarget, true
	}
	if target, found := JavaScriptCMSComponentReferenceAt(node); found {
		return target, true
	}
	if reference, found := JavaScriptRegistryReferenceAt(node); found {
		return reference.AdminSymbolTarget, true
	}
	if jsquery.StringInCall(
		node, 0, "Mixin.getByName", "Shopware.Mixin.getByName",
	) {
		return stringTarget(AdminSymbolMixin, node)
	}
	if jsquery.StringInCall(
		node, 0, "Directive.getByName", "Shopware.Directive.getByName",
	) {
		return stringTarget(AdminSymbolDirective, node)
	}
	if jsquery.StringInCall(
		node, 0, "Filter.getByName", "Shopware.Filter.getByName",
	) {
		return stringTarget(AdminSymbolFilter, node)
	}
	if literal := jsquery.StringAt(node); literal != nil {
		call := jsquery.CallAt(literal)
		name := jsquery.CallName(literal)
		argument := jsquery.StringArgumentIndex(literal)
		switch name {
		case "Component.register", "Shopware.Component.register":
			if argument == 0 {
				return stringTarget(AdminSymbolComponent, literal)
			}
		case "Component.extend", "Shopware.Component.extend":
			if argument == 0 || argument == 1 {
				return stringTarget(AdminSymbolComponent, literal)
			}
		case "Component.override", "Shopware.Component.override":
			if argument == 0 {
				return stringTarget(AdminSymbolComponent, literal)
			}
		case "Mixin.register", "Shopware.Mixin.register":
			if argument == 0 {
				return stringTarget(AdminSymbolMixin, literal)
			}
		case "Directive.register", "Shopware.Directive.register":
			if argument == 0 {
				return stringTarget(AdminSymbolDirective, literal)
			}
		case "Filter.register", "Shopware.Filter.register":
			if argument == 0 {
				return stringTarget(AdminSymbolFilter, literal)
			}
		case "Module.register", "Shopware.Module.register":
			if argument == 0 {
				return stringTarget(AdminSymbolModule, literal)
			}
		case "Shopware.Store.register", "Store.register":
			if argument == 0 {
				return stringTarget(AdminSymbolStore, literal)
			}
		}
		if call != nil && jsquery.CallMethodName(call) == "addServiceProvider" && argument == 0 {
			return stringTarget(AdminSymbolService, literal)
		}
		if call != nil && jsquery.CallMethodName(call) == "register" &&
			strings.Contains(call.Text(), "Service()") && argument == 0 {
			return stringTarget(AdminSymbolService, literal)
		}
		property := jsquery.PropertyAt(literal)
		switch jsquery.PropertyName(property) {
		case "id":
			if call != nil && (jsquery.CallName(call) == "Shopware.Store.register" ||
				jsquery.CallName(call) == "Store.register") {
				return stringTarget(AdminSymbolStore, literal)
			}
		case "component":
			if call != nil && (jsquery.CallName(call) == "Module.register" ||
				jsquery.CallName(call) == "Shopware.Module.register") {
				return stringTarget(AdminSymbolComponent, literal)
			}
		}
	}
	return AdminSymbolTarget{}, false
}

// JavaScriptCMSComponentReferenceAt recognizes the concrete Vue component
// links owned directly by a CMS element or block registration. Nested config
// objects may also contain fields named `component`, so ownership by the
// registration object is required before treating a string as a symbol.
func JavaScriptCMSComponentReferenceAt(
	node *jssyntax.Node,
) (AdminSymbolTarget, bool) {
	literal := jsquery.StringAt(node)
	if literal == nil {
		return AdminSymbolTarget{}, false
	}
	_, config, found := cmsRegistrationConfigAt(literal)
	if !found {
		return AdminSymbolTarget{}, false
	}
	property := jsquery.PropertyAt(literal)
	if property == nil || property.Parent() != config {
		return AdminSymbolTarget{}, false
	}
	switch jsquery.PropertyName(property) {
	case "component", "configComponent", "previewComponent":
	default:
		return AdminSymbolTarget{}, false
	}
	name := jsquery.StringValue(literal)
	if name == "" {
		return AdminSymbolTarget{}, false
	}
	return AdminSymbolTarget{Kind: AdminSymbolComponent, Name: name}, true
}

// JavaScriptCMSReferenceAt recognizes Shopping Experiences registry
// declarations, lookups and block-slot element references.
func JavaScriptCMSReferenceAt(
	node *jssyntax.Node,
) (JavaScriptRegistryReference, bool) {
	literal := jsquery.StringAt(node)
	if literal == nil {
		return JavaScriptRegistryReference{}, false
	}
	call := jsquery.CallAt(literal)
	if call == nil {
		return JavaScriptRegistryReference{}, false
	}
	method := jsquery.CallMethodName(call)
	if jsquery.StringArgumentIndex(literal) == 0 {
		var kind AdminSymbolKind
		switch method {
		case "getCmsElementConfigByName":
			kind = AdminSymbolCMSElement
		case "getCmsBlockConfigByName":
			kind = AdminSymbolCMSBlock
		}
		if kind != "" {
			name := jsquery.StringValue(literal)
			if name != "" {
				return JavaScriptRegistryReference{
					AdminSymbolTarget: AdminSymbolTarget{Kind: kind, Name: name},
					Operation:         "lookup",
				}, true
			}
		}
	}
	kind, config, found := cmsRegistrationConfigAt(literal)
	if !found {
		return JavaScriptRegistryReference{}, false
	}
	property := jsquery.PropertyAt(literal)
	if property == nil {
		return JavaScriptRegistryReference{}, false
	}
	name := jsquery.StringValue(literal)
	if name == "" {
		return JavaScriptRegistryReference{}, false
	}
	if property.Parent() == config && jsquery.PropertyName(property) == "name" {
		return JavaScriptRegistryReference{
			AdminSymbolTarget: AdminSymbolTarget{
				Kind: cmsAdminSymbolKind(kind), Name: name,
			},
			Operation: "register",
		}, true
	}
	if kind == AdminCMSBlock {
		if cmsBlockSlotElementReference(property, config) {
			return JavaScriptRegistryReference{
				AdminSymbolTarget: AdminSymbolTarget{
					Kind: AdminSymbolCMSElement, Name: name,
				},
				Operation: "slot",
			}, true
		}
	}
	return JavaScriptRegistryReference{}, false
}

func JavaScriptCMSCompletionKindAt(
	node *jssyntax.Node,
) (AdminCMSRegistrationKind, bool) {
	literal := jsquery.StringAt(node)
	if literal == nil {
		return "", false
	}
	call := jsquery.CallAt(literal)
	if call == nil {
		return "", false
	}
	if jsquery.StringArgumentIndex(literal) == 0 {
		switch jsquery.CallMethodName(call) {
		case "getCmsElementConfigByName":
			return AdminCMSElement, true
		case "getCmsBlockConfigByName":
			return AdminCMSBlock, true
		}
	}
	kind, config, found := cmsRegistrationConfigAt(literal)
	if !found || kind != AdminCMSBlock {
		return "", false
	}
	property := jsquery.PropertyAt(literal)
	if cmsBlockSlotElementReference(property, config) {
		return AdminCMSElement, true
	}
	return "", false
}

func cmsBlockSlotElementReference(
	property,
	config *jssyntax.Node,
) bool {
	if property == nil || config == nil {
		return false
	}
	slotsObject := jsquery.PropertyValue(jsquery.Property(config, "slots"))
	if slotsObject == nil || slotsObject.Kind() != jssyntax.JsObject {
		return false
	}
	if property.Parent() == slotsObject {
		return true
	}
	if jsquery.PropertyName(property) != "type" {
		return false
	}
	slotObject := property.Parent()
	if slotObject == nil || slotObject.Kind() != jssyntax.JsObject {
		return false
	}
	slotProperty := slotObject.Parent()
	return slotProperty != nil && slotProperty.Parent() == slotsObject
}

func cmsRegistrationConfigAt(
	node *jssyntax.Node,
) (AdminCMSRegistrationKind, *jssyntax.Node, bool) {
	call := jsquery.CallAt(node)
	if call == nil {
		return "", nil, false
	}
	var kind AdminCMSRegistrationKind
	switch jsquery.CallMethodName(call) {
	case "registerCmsElement":
		kind = AdminCMSElement
	case "registerCmsBlock":
		kind = AdminCMSBlock
	default:
		return "", nil, false
	}
	config := jsquery.ObjectArgument(call, 0)
	if config == nil {
		return "", nil, false
	}
	return kind, config, true
}

func cmsAdminSymbolKind(kind AdminCMSRegistrationKind) AdminSymbolKind {
	if kind == AdminCMSBlock {
		return AdminSymbolCMSBlock
	}
	return AdminSymbolCMSElement
}

// JavaScriptComponentPropAt recognizes a component prop declaration. Runtime
// `this.prop` references are resolved against the effective component by the
// indexer because the member expression alone does not prove it is a prop.
func JavaScriptComponentPropAt(node *jssyntax.Node) (string, bool) {
	if node == nil {
		return "", false
	}
	offset := node.Range().Start
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() != jssyntax.JsProperty ||
			jsquery.PropertyName(current) != "props" {
			continue
		}
		value := jsquery.PropertyValue(current)
		if value == nil {
			return "", false
		}
		switch value.Kind() {
		case jssyntax.JsArray:
			for _, item := range jsquery.ArrayItems(value) {
				if item.Kind() != jssyntax.JsString ||
					!rangeContains(item.Range(), offset) {
					continue
				}
				name := jsquery.StringValue(item)
				return name, name != ""
			}
		case jssyntax.JsObject:
			for _, property := range jsquery.Properties(value) {
				nameNode := jsquery.PropertyNameNode(property)
				if nameNode == nil || !rangeContains(nameNode.Range(), offset) {
					continue
				}
				name := jsquery.PropertyName(property)
				return name, name != ""
			}
		}
		return "", false
	}
	return "", false
}

// JavaScriptComponentEventAt recognizes the static event tokens that form a
// component's public event contract. The owning component is resolved from the
// file by AdminComponentIndexer.JavaScriptSymbolAt.
func JavaScriptComponentEventAt(node *jssyntax.Node) (string, bool) {
	if node == nil {
		return "", false
	}
	literal := jsquery.StringAt(node)
	if literal != nil && jsquery.StringArgumentIndex(literal) == 0 {
		switch jsquery.CallName(literal) {
		case "this.$emit", "$emit", "emit", "context.emit":
			name := CanonicalEventName(jsquery.StringValue(literal))
			return name, name != ""
		}
	}
	offset := node.Range().Start
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() != jssyntax.JsProperty ||
			jsquery.PropertyName(current) != "emits" {
			continue
		}
		value := jsquery.PropertyValue(current)
		if value == nil {
			return "", false
		}
		switch value.Kind() {
		case jssyntax.JsArray:
			if literal == nil || !rangeContains(value.Range(), offset) {
				return "", false
			}
			name := CanonicalEventName(jsquery.StringValue(literal))
			return name, name != ""
		case jssyntax.JsObject:
			for _, property := range jsquery.Properties(value) {
				nameNode := jsquery.PropertyNameNode(property)
				if nameNode == nil || !rangeContains(nameNode.Range(), offset) {
					continue
				}
				name := CanonicalEventName(jsquery.PropertyName(property))
				return name, name != ""
			}
		}
		return "", false
	}
	return "", false
}

// JavaScriptRegistryReferenceAt recognizes static component and module
// registry lookups. Dynamic names are intentionally excluded because they
// cannot be resolved safely by the index.
func JavaScriptRegistryReferenceAt(
	node *jssyntax.Node,
) (JavaScriptRegistryReference, bool) {
	literal := jsquery.StringAt(node)
	if literal == nil || jsquery.StringArgumentIndex(literal) != 0 {
		return JavaScriptRegistryReference{}, false
	}
	name := jsquery.StringValue(literal)
	if name == "" {
		return JavaScriptRegistryReference{}, false
	}
	callName := jsquery.CallName(literal)
	operation := jsquery.CallMethodName(literal)
	var kind AdminSymbolKind
	switch callName {
	case "Shopware.Component.getComponentRegistry().get",
		"Component.getComponentRegistry().get",
		"Shopware.Component.getComponentRegistry().has",
		"Component.getComponentRegistry().has":
		kind = AdminSymbolComponent
	case "Shopware.Module.getModuleRegistry().get",
		"Module.getModuleRegistry().get",
		"Shopware.Module.getModuleRegistry().has",
		"Module.getModuleRegistry().has":
		kind = AdminSymbolModule
	case "Shopware.Directive.getByName", "Directive.getByName":
		kind = AdminSymbolDirective
	case "Shopware.Filter.getByName", "Filter.getByName":
		kind = AdminSymbolFilter
	default:
		return JavaScriptRegistryReference{}, false
	}
	return JavaScriptRegistryReference{
		AdminSymbolTarget: AdminSymbolTarget{Kind: kind, Name: name},
		Operation:         operation,
	}, true
}

// IsJavaScriptModuleRouteReference recognizes the static route identities used
// by Shopware's module metadata and Vue Router location objects.
func IsJavaScriptModuleRouteReference(node *jssyntax.Node) bool {
	if jsquery.StringAt(node) == nil {
		return false
	}
	propertyName := jsquery.PropertyName(jsquery.PropertyAt(node))
	if propertyName == "parentPath" {
		return true
	}
	if propertyName == "name" {
		switch jsquery.CallName(node) {
		case "router.push", "this.$router.push", "$router.push",
			"router.replace", "this.$router.replace", "$router.replace",
			"router.resolve", "this.$router.resolve", "$router.resolve":
			return true
		}
		return hasJavaScriptPropertyAncestor(node, "redirect")
	}
	if propertyName == "path" &&
		hasJavaScriptPropertyAncestor(node, "navigation") {
		return true
	}
	return propertyName == "to" &&
		hasJavaScriptPropertyAncestor(node, "settingsItem")
}

func hasJavaScriptPropertyAncestor(node *jssyntax.Node, name string) bool {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == jssyntax.JsProperty &&
			jsquery.PropertyName(current) == name {
			return true
		}
	}
	return false
}
