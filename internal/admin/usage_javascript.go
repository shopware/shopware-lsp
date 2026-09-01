package admin

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

func parseAdminJavaScriptUsages(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []AdminUsageSet {
	collector := newAdminUsageCollector(filePath, lineIndex)
	analysis := NewJavaScriptDocumentAnalysis(root)
	componentObjects := componentUsageObjects(analysis)
	for _, object := range componentObjects {
		if object.events {
			collectComponentEventObjectUsages(object.node, filePath, collector)
		}
	}
	for _, object := range componentObjects {
		collectComponentMemberObjectUsages(object.node, filePath, collector)
	}
	for _, literal := range analysis.Nodes(jssyntax.JsString) {
		if _, eventName, found := analysis.ShopwareEventBusEventAt(
			literal,
		); found && eventName != "" {
			collector.addJSString(
				AdminSymbolEventBusEvent, "", literal, false,
			)
		}
		if reference, found := JavaScriptCMSReferenceAt(literal); found {
			collector.addJSString(
				reference.Kind, "", literal, reference.Operation == "register",
			)
		}
		if reference, found := JavaScriptRegistryReferenceAt(literal); found {
			collector.addJSString(reference.Kind, "", literal, false)
		}
		switch {
		case IsServiceReference(literal):
			collector.addJSString(AdminSymbolService, "", literal, false)
		case IsStoreReference(literal):
			collector.addJSString(AdminSymbolStore, "", literal, false)
		case IsPrivilegeReference(literal):
			collector.addJSString(AdminSymbolPrivilege, "", literal, false)
		case jsquery.StringInCall(
			literal, 0, "Mixin.getByName", "Shopware.Mixin.getByName",
		):
			collector.addJSString(AdminSymbolMixin, "", literal, false)
		case jsquery.StringInCall(
			literal, 0, "Directive.getByName", "Shopware.Directive.getByName",
		):
			collector.addJSString(AdminSymbolDirective, "", literal, false)
		case jsquery.StringInCall(
			literal, 0, "Filter.getByName", "Shopware.Filter.getByName",
		):
			collector.addJSString(AdminSymbolFilter, "", literal, false)
		}
		property := jsquery.PropertyAt(literal)
		if IsJavaScriptModuleRouteReference(literal) {
			collector.addJSString(
				AdminSymbolModuleRoute, "", literal, false,
			)
		}
		if target, found := JavaScriptCMSComponentReferenceAt(literal); found {
			collector.addJSString(target.Kind, target.Owner, literal, false)
		}
		switch jsquery.PropertyName(property) {
		case "component":
			call := jsquery.CallAt(literal)
			if call != nil && (jsquery.CallName(call) == "Module.register" ||
				jsquery.CallName(call) == "Shopware.Module.register") {
				collector.addJSString(AdminSymbolComponent, "", literal, false)
			}
		}
	}

	for call := range analysis.IterateCalls() {
		name := jsquery.CallName(call)
		method := jsquery.CallMethodName(call)
		switch name {
		case "Component.register", "Shopware.Component.register":
			collector.addJSString(
				AdminSymbolComponent, "", jsquery.StringArgument(call, 0), true,
			)
		case "Component.extend", "Shopware.Component.extend":
			collector.addJSString(
				AdminSymbolComponent, "", jsquery.StringArgument(call, 0), true,
			)
			collector.addJSString(
				AdminSymbolComponent, "", jsquery.StringArgument(call, 1), false,
			)
		case "Component.override", "Shopware.Component.override":
			collector.addJSString(
				AdminSymbolComponent, "", jsquery.StringArgument(call, 0), false,
			)
		case "Mixin.register", "Shopware.Mixin.register":
			collector.addJSString(
				AdminSymbolMixin, "", jsquery.StringArgument(call, 0), true,
			)
		case "Directive.register", "Shopware.Directive.register":
			collector.addJSString(
				AdminSymbolDirective, "", jsquery.StringArgument(call, 0), true,
			)
		case "Filter.register", "Shopware.Filter.register":
			collector.addJSString(
				AdminSymbolFilter, "", jsquery.StringArgument(call, 0), true,
			)
		case "Module.register", "Shopware.Module.register":
			collector.addJSString(
				AdminSymbolModule, "", jsquery.StringArgument(call, 0), true,
			)
		case "Shopware.Store.register", "Store.register":
			collector.addStoreDeclaration(call)
		}
		if method == "addServiceProvider" {
			collector.addJSString(
				AdminSymbolService, "", jsquery.StringArgument(call, 0), true,
			)
		}
		if method == "register" && strings.Contains(call.Text(), "Service()") {
			collector.addJSString(
				AdminSymbolService, "", jsquery.StringArgument(call, 0), true,
			)
		}
	}

	applicationContainerAliases := applicationContainerConstAliasNames(
		root, analysis,
	)
	members := analysis.Nodes(jssyntax.JsMemberExpression)
	for _, member := range members {
		containerName, memberName, containerMatched :=
			"", "", false
		if potentialApplicationContainerMember(
			member, applicationContainerAliases,
		) {
			containerName, memberName, containerMatched =
				analysis.ApplicationContainerMember(member)
		}
		if containerMatched && containerName == "service" && memberName != "" {
			collector.addNode(
				AdminSymbolService, "", memberName,
				lastJSIdentifier(member), false,
			)
			continue
		}
		storeName, memberName, matched := jsquery.StoreMember(member)
		if !matched || memberName == "" {
			continue
		}
		memberNode := lastJSIdentifier(member)
		collector.addNode(
			AdminSymbolStoreMember, storeName, memberName, memberNode, false,
		)
	}

	definition := ParseComponentDefinitionWithLineIndex(root, lineIndex)
	if len(definition.Injected) > 0 {
		injected := make(map[string]bool, len(definition.Injected))
		for _, name := range definition.Injected {
			injected[name] = true
		}
		for _, member := range members {
			name, matched := jsquery.ThisMember(member)
			if !matched || !injected[name] {
				continue
			}
			collector.addNode(
				AdminSymbolService, "", name,
				jsquery.ThisMemberNameNode(member), false,
			)
		}
	}
	return collector.values()
}

// CollectJavaScriptUsages derives Administration symbol occurrences from a
// live lossless JavaScript/TypeScript CST without mutating the persistent
// index. Editor features use it to replace stale on-disk ranges for an open,
// unsaved document.
func CollectJavaScriptUsages(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []AdminUsageSet {
	if root == nil || lineIndex == nil {
		return nil
	}
	return parseAdminJavaScriptUsages(root, filePath, lineIndex)
}

type componentUsageObject struct {
	node   *jssyntax.Node
	events bool
}

func componentUsageObjects(
	analysis *JavaScriptDocumentAnalysis,
) []componentUsageObject {
	if analysis == nil {
		return nil
	}
	seen := make(map[cst.TextRange]int)
	var objects []componentUsageObject
	addObject := func(object *jssyntax.Node, events bool) {
		if object == nil {
			return
		}
		rangeValue := object.RangeTrimmedTrivia()
		if position, found := seen[rangeValue]; found {
			objects[position].events = objects[position].events || events
			return
		}
		seen[rangeValue] = len(objects)
		objects = append(objects, componentUsageObject{
			node: object, events: events,
		})
	}
	for _, export := range analysis.Nodes(jssyntax.JsExportDefault) {
		addObject(componentDefinitionObject(
			jsquery.ExportDefaultExpression(export),
		), true)
	}
	for call := range analysis.IterateCalls(
		"Component.register",
		"Shopware.Component.register",
		"Component.extend",
		"Shopware.Component.extend",
		"Component.override",
		"Shopware.Component.override",
		"Mixin.register",
		"Shopware.Mixin.register",
	) {
		callName := jsquery.CallName(call)
		argument := 1
		if strings.HasSuffix(callName, ".extend") {
			argument = 2
		}
		addObject(componentDefinitionObject(
			jsquery.ArgumentExpression(call, argument),
		), callName != "Mixin.register" && callName != "Shopware.Mixin.register")
	}
	return objects
}

func collectComponentMemberObjectUsages(
	object *jssyntax.Node,
	filePath string,
	collector *adminUsageCollector,
) {
	if object == nil || collector == nil {
		return
	}
	definition := &ComponentDefinition{FilePath: filePath}
	parseDefinitionObject(object, definition, collector.lineIndex)
	setDefinitionFilePath(definition, filePath)
	component := VueComponent{
		DefinitionPath: filePath, Props: definition.Props,
		Injected: definition.Injected, Members: definition.Members,
		LocalDirectives: definition.LocalDirectives,
	}
	for _, directive := range definition.LocalDirectives {
		style := AdminNameExact
		if directive.Shorthand {
			style = AdminNameShorthand
		} else if !directive.Quoted {
			style = AdminNameCamel
		}
		collector.addSourceRange(
			AdminSymbolDirective,
			directive.FilePath,
			directive.Name,
			directive.NameRange,
			style,
		)
	}
	for _, member := range definition.Members {
		if !member.Renameable() {
			continue
		}
		style := AdminNameExact
		if member.Shorthand {
			style = AdminNameShorthand
		}
		collector.addSourceRange(
			AdminSymbolComponentMember,
			member.SourceIdentity(),
			member.Name,
			member.NameRange,
			style,
		)
	}
	props := make(map[string]bool, len(component.Props))
	for _, prop := range component.Props {
		props[prop.Name] = true
	}
	injected := make(map[string]bool, len(component.Injected))
	for _, service := range component.Injected {
		injected[service] = true
	}
	templateMembers := make(map[string]VueComponentMember)
	for _, member := range component.TemplateMembers() {
		templateMembers[member.Name] = member
	}
	for _, expression := range jsquery.Nodes(
		object, jssyntax.JsMemberExpression,
	) {
		name, matched := jsquery.ThisMember(expression)
		if !matched || name == "" || props[name] || injected[name] {
			continue
		}
		owner := ""
		if member, found := templateMembers[name]; found && member.Renameable() {
			owner = member.SourceIdentity()
		}
		collector.addNode(
			AdminSymbolComponentMember,
			owner,
			name,
			jsquery.ThisMemberNameNode(expression),
			false,
		)
	}
}

func collectComponentEventObjectUsages(
	object *jssyntax.Node,
	filePath string,
	collector *adminUsageCollector,
) {
	props := jsquery.PropertyValue(jsquery.Property(object, "props"))
	propNames := make(map[string]bool)
	switch {
	case props == nil:
	case props.Kind() == jssyntax.JsArray:
		for _, item := range jsquery.ArrayItems(props) {
			if item.Kind() != jssyntax.JsString {
				continue
			}
			name := jsquery.StringValue(item)
			if name == "" {
				continue
			}
			propNames[name] = true
			collector.addNamedJSNode(
				AdminSymbolComponentProp,
				filePath,
				name,
				item,
				true,
			)
		}
	case props.Kind() == jssyntax.JsObject:
		for _, property := range jsquery.Properties(props) {
			name := jsquery.PropertyName(property)
			if name == "" {
				continue
			}
			propNames[name] = true
			collector.addNamedJSNode(
				AdminSymbolComponentProp,
				filePath,
				name,
				jsquery.PropertyNameNode(property),
				true,
			)
		}
	}
	for _, member := range jsquery.Nodes(object, jssyntax.JsMemberExpression) {
		name, matched := jsquery.ThisMember(member)
		if !matched || !propNames[name] {
			continue
		}
		nameNode := jsquery.ThisMemberNameNode(member)
		collector.addNamedJSNode(
			AdminSymbolComponentProp,
			filePath,
			name,
			nameNode,
			false,
		)
	}
	emits := jsquery.PropertyValue(jsquery.Property(object, "emits"))
	switch {
	case emits == nil:
	case emits.Kind() == jssyntax.JsArray:
		for _, item := range jsquery.ArrayItems(emits) {
			if item.Kind() != jssyntax.JsString {
				continue
			}
			collector.addNamedJSNode(
				AdminSymbolComponentEvent,
				filePath,
				CanonicalEventName(jsquery.StringValue(item)),
				item,
				true,
			)
		}
	case emits.Kind() == jssyntax.JsObject:
		for _, property := range jsquery.Properties(emits) {
			collector.addNamedJSNode(
				AdminSymbolComponentEvent,
				filePath,
				CanonicalEventName(jsquery.PropertyName(property)),
				jsquery.PropertyNameNode(property),
				true,
			)
		}
	}
	for call := range jsquery.IterateCalls(object) {
		switch jsquery.CallName(call) {
		case "this.$emit", "$emit", "emit", "context.emit":
		default:
			continue
		}
		argument := jsquery.ArgumentExpression(call, 0)
		if argument == nil || argument.Kind() != jssyntax.JsString {
			continue
		}
		collector.addNamedJSNode(
			AdminSymbolComponentEvent,
			filePath,
			CanonicalEventName(jsquery.StringValue(argument)),
			argument,
			false,
		)
	}
}
