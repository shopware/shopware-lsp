package admin

import (
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

type AdminSymbolKind string

const (
	AdminSymbolComponent       AdminSymbolKind = "component"
	AdminSymbolService         AdminSymbolKind = "service"
	AdminSymbolStore           AdminSymbolKind = "store"
	AdminSymbolStoreMember     AdminSymbolKind = "store_member"
	AdminSymbolPrivilege       AdminSymbolKind = "privilege"
	AdminSymbolMixin           AdminSymbolKind = "mixin"
	AdminSymbolDirective       AdminSymbolKind = "directive"
	AdminSymbolFilter          AdminSymbolKind = "filter"
	AdminSymbolCMSElement      AdminSymbolKind = "cms_element"
	AdminSymbolCMSBlock        AdminSymbolKind = "cms_block"
	AdminSymbolModule          AdminSymbolKind = "module"
	AdminSymbolModuleRoute     AdminSymbolKind = "module_route"
	AdminSymbolComponentProp   AdminSymbolKind = "component_prop"
	AdminSymbolComponentEvent  AdminSymbolKind = "component_event"
	AdminSymbolComponentModel  AdminSymbolKind = "component_model"
	AdminSymbolComponentSlot   AdminSymbolKind = "component_slot"
	AdminSymbolComponentMember AdminSymbolKind = "component_member"
	AdminSymbolEventBusEvent   AdminSymbolKind = "event_bus_event"
)

// adminDynamicComponentUsageOwner is reserved for template usages whose
// concrete component owner is inferred from a dynamic selector at query time.
// It cannot collide with a Vue component name because NUL is not a valid tag
// character.
const adminDynamicComponentUsageOwner = "\x00dynamic-component"

type AdminSymbolTarget struct {
	Kind  AdminSymbolKind
	Owner string
	Name  string
}

// JavaScriptRegistryReference describes a static registry lookup such as
// Component.getComponentRegistry().get('sw-card'). Keeping this context in the
// Administration package gives completion, navigation, hover, diagnostics,
// references, and rename one conservative definition of registry strings.
type JavaScriptRegistryReference struct {
	AdminSymbolTarget
	Operation string
}

type AdminSourceRange struct {
	StartLine                int
	StartCharacter           int
	EndLine                  int
	EndCharacter             int
	Declaration              bool
	Identifier               bool
	NameStyle                AdminNameStyle
	DynamicComponentSelector string
	DynamicRouterView        bool
}

// JavaScriptFilterNameForCallee resolves the filter behind a callable
// expression. It supports both an immediate getByName invocation and a
// lexically visible const binding initialized from that lookup. Mutable or
// reassigned bindings remain intentionally unresolved.
func JavaScriptFilterNameForCallee(
	callee *jssyntax.Node,
) (string, bool) {
	if callee == nil {
		return "", false
	}
	if name, found := javaScriptFilterLookupName(callee); found {
		return name, true
	}
	identifier := jsquery.IdentifierText(callee)
	if identifier == "" || callee.Kind() != jssyntax.JsIdentifier {
		return "", false
	}
	root := callee
	for root.Parent() != nil {
		root = root.Parent()
	}
	expression, found := visibleJavaScriptConstInitializer(
		callee, identifier, root,
	)
	if !found {
		return "", false
	}
	parsed := javascriptparser.Parse(expression)
	if parsed.Tree == nil || parsed.Tree.Root == nil {
		return "", false
	}
	return javaScriptFilterLookupName(parsed.Tree.Root)
}

func visibleJavaScriptConstInitializer(
	use *jssyntax.Node,
	identifier string,
	root *jssyntax.Node,
) (string, bool) {
	return visibleJavaScriptConstInitializerIndexed(use, identifier, root, nil)
}

func visibleJavaScriptConstInitializerIndexed(
	use *jssyntax.Node,
	identifier string,
	root *jssyntax.Node,
	analysis *JavaScriptDocumentAnalysis,
) (string, bool) {
	_, expression, found := visibleJavaScriptConstDeclarationIndexed(
		use, identifier, root, analysis,
	)
	return expression, found
}

func visibleJavaScriptConstDeclaration(
	use *jssyntax.Node,
	identifier string,
	root *jssyntax.Node,
) (*jssyntax.Node, string, bool) {
	return visibleJavaScriptConstDeclarationIndexed(use, identifier, root, nil)
}

func visibleJavaScriptConstDeclarationIndexed(
	use *jssyntax.Node,
	identifier string,
	root *jssyntax.Node,
	analysis *JavaScriptDocumentAnalysis,
) (*jssyntax.Node, string, bool) {
	if use == nil || root == nil || !isStaticVueIdentifier(identifier) {
		return nil, "", false
	}
	useFunction := closestJavaScriptFunctionScope(use)
	useBlocks := visibleJavaScriptBlockScopes(use, useFunction)
	useStart := use.RangeTrimmedTrivia().Start
	bestDepth := len(useBlocks) + 1
	bestStart := uint32(0)
	var best string
	var bestDeclaration *jssyntax.Node
	found := false
	var declarations []*jssyntax.Node
	if analysis != nil {
		declarations = analysis.Nodes(jssyntax.JsVariableDeclaration)
	} else {
		declarations = jsquery.Nodes(root, jssyntax.JsVariableDeclaration)
	}
	for _, declaration := range declarations {
		var name, expression string
		var parsed bool
		if analysis != nil {
			name, expression, parsed = analysis.constInitializer(declaration)
		} else {
			name, expression, parsed = directComponentConstInitializer(
				declaration.Text(),
			)
		}
		if !parsed || name != identifier ||
			closestJavaScriptFunctionScope(declaration) != useFunction {
			continue
		}
		start := declaration.RangeTrimmedTrivia().Start
		if start >= useStart {
			continue
		}
		block := closestJavaScriptBlockScope(declaration, useFunction)
		depth := len(useBlocks)
		if block != nil {
			var visible bool
			depth, visible = useBlocks[block]
			if !visible {
				continue
			}
		}
		if !found || depth < bestDepth ||
			depth == bestDepth && start > bestStart {
			best = expression
			bestDeclaration = declaration
			bestDepth = depth
			bestStart = start
			found = true
		}
	}
	return bestDeclaration, best, found
}

func javaScriptFilterLookupName(root *jssyntax.Node) (string, bool) {
	for _, literal := range jsquery.Nodes(root, jssyntax.JsString) {
		reference, found := JavaScriptRegistryReferenceAt(literal)
		if found && reference.Kind == AdminSymbolFilter &&
			reference.Operation == "getByName" {
			return reference.Name, true
		}
	}
	return "", false
}

type AdminNameStyle uint8

const (
	AdminNameExact AdminNameStyle = iota
	AdminNameCamel
	AdminNameShorthand
)

type AdminUsageSet struct {
	Kind        AdminSymbolKind
	Owner       string
	Name        string
	FilePath    string
	Occurrences []AdminSourceRange
}

func AdminUsageKey(kind AdminSymbolKind, owner, name string) string {
	return string(kind) + "\x00" + owner + "\x00" + name
}

func parseAdminJavaScriptUsages(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []AdminUsageSet {
	collector := newAdminUsageCollector(filePath, lineIndex)
	collectComponentEventUsages(root, filePath, collector)
	collectComponentMemberUsages(root, filePath, collector)
	for _, literal := range jsquery.Nodes(root, jssyntax.JsString) {
		if _, eventName, found := JavaScriptShopwareEventBusEventAt(
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

	for _, call := range jsquery.Calls(root) {
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

	applicationContainerAliases := applicationContainerConstAliasNames(root)
	for _, member := range jsquery.Nodes(root, jssyntax.JsMemberExpression) {
		containerName, memberName, containerMatched :=
			"", "", false
		if potentialApplicationContainerMember(
			member, applicationContainerAliases,
		) {
			containerName, memberName, containerMatched =
				JavaScriptApplicationContainerMember(member)
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
		for _, member := range jsquery.Nodes(root, jssyntax.JsMemberExpression) {
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

func collectComponentMemberUsages(
	root *jssyntax.Node,
	filePath string,
	collector *adminUsageCollector,
) {
	if root == nil || collector == nil {
		return
	}
	objects := make(map[string]*jssyntax.Node)
	addObject := func(object *jssyntax.Node) {
		if object == nil {
			return
		}
		rangeValue := object.RangeTrimmedTrivia()
		key := strconv.FormatUint(uint64(rangeValue.Start), 10) + ":" +
			strconv.FormatUint(uint64(rangeValue.End), 10)
		objects[key] = object
	}
	for _, export := range jsquery.ExportDefaults(root) {
		addObject(componentDefinitionObject(
			jsquery.ExportDefaultExpression(export),
		))
	}
	for _, call := range jsquery.Calls(
		root,
		"Component.register",
		"Shopware.Component.register",
		"Component.extend",
		"Shopware.Component.extend",
		"Component.override",
		"Shopware.Component.override",
		"Mixin.register",
		"Shopware.Mixin.register",
	) {
		argument := 1
		if strings.HasSuffix(jsquery.CallName(call), ".extend") {
			argument = 2
		}
		addObject(componentDefinitionObject(
			jsquery.ArgumentExpression(call, argument),
		))
	}
	for _, object := range objects {
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
		for _, expression := range jsquery.Nodes(
			object, jssyntax.JsMemberExpression,
		) {
			name, matched := jsquery.ThisMember(expression)
			if !matched || name == "" {
				continue
			}
			if _, prop := component.ComponentProp(name); prop {
				continue
			}
			injected := false
			for _, service := range component.Injected {
				if service == name {
					injected = true
					break
				}
			}
			if injected {
				continue
			}
			owner := ""
			if member, found := component.TemplateMember(name); found &&
				member.Renameable() {
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
}

func collectComponentEventUsages(
	root *jssyntax.Node,
	filePath string,
	collector *adminUsageCollector,
) {
	if root == nil || collector == nil {
		return
	}
	objects := make(map[string]*jssyntax.Node)
	addObject := func(object *jssyntax.Node) {
		if object == nil {
			return
		}
		rangeValue := object.RangeTrimmedTrivia()
		key := strconv.FormatUint(uint64(rangeValue.Start), 10) + ":" +
			strconv.FormatUint(uint64(rangeValue.End), 10)
		objects[key] = object
	}
	for _, export := range jsquery.ExportDefaults(root) {
		addObject(componentDefinitionObject(
			jsquery.ExportDefaultExpression(export),
		))
	}
	for _, call := range jsquery.Calls(
		root,
		"Component.register",
		"Shopware.Component.register",
		"Component.extend",
		"Shopware.Component.extend",
		"Component.override",
		"Shopware.Component.override",
	) {
		argument := 1
		if strings.HasSuffix(jsquery.CallName(call), ".extend") {
			argument = 2
		}
		addObject(componentDefinitionObject(
			jsquery.ArgumentExpression(call, argument),
		))
	}
	for _, object := range objects {
		collectComponentEventObjectUsages(object, filePath, collector)
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
		collector.addNamedJSNode(
			AdminSymbolComponentEvent,
			filePath,
			CanonicalEventName(jsquery.StringValue(argument)),
			argument,
			false,
		)
	}
}

func parseAdminTwigUsages(
	root *twigsyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []AdminUsageSet {
	collector := newAdminUsageCollector(filePath, lineIndex)
	content := []byte(root.Text())
	shorthandMemberRanges := make(map[cst.TextRange]bool)
	for _, reference := range TwigRegistryReferences(root) {
		collector.addRange(
			reference.Kind,
			"",
			reference.Name,
			reference.Range,
			false,
			false,
		)
	}
	for _, node := range twigquery.Nodes(root, twigsyntax.HtmlStartingTag) {
		tag, ok := twigast.CastHtmlStartingTag(node)
		if !ok || tag.Name() == nil {
			continue
		}
		name := tag.Name().Text()
		selector, dynamic := TwigDynamicComponentSelector(node)
		dynamicUsage := dynamic && len(staticComponentContractNames(node)) == 0
		dynamicRouterView := dynamicUsage &&
			twigDynamicComponentUsesRouterView(node, selector)
		if dynamic {
			for _, candidate := range selector.Candidates {
				collector.addRange(
					AdminSymbolComponent,
					"",
					candidate.Name,
					candidate.Range,
					false,
					false,
				)
			}
		}
		contractNames := staticComponentContractNames(node)
		if IsComponentTag(name) {
			collector.addRange(
				AdminSymbolComponent, "", name, tag.Name().Range(), false, false,
			)
		}
		for _, attribute := range tag.Attributes() {
			nameToken := attribute.Name()
			if nameToken == nil {
				continue
			}
			attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
			if directive, found := VueDirectiveReferenceForAttribute(
				attributeName, nameToken.Range(),
			); found {
				collector.addRange(
					AdminSymbolDirective, "", directive.Name,
					directive.Range, false, false,
				)
			}
			if attributeName == "v-bind" {
				if value, valueOK := attribute.Value(); valueOK {
					if inner, innerOK := value.GetInner(); innerOK {
						fields, _ := VueObjectBindingFields(
							inner.Syntax().Text(), inner.Syntax().Range().Start,
						)
						for _, field := range fields {
							if field.Shorthand {
								shorthandMemberRanges[field.ExpressionRange] = true
							}
							propName := NormalizePropName(field.Name)
							if propName == "" {
								continue
							}
							for _, contractName := range contractNames {
								style := AdminNameExact
								identifier := true
								if field.Shorthand {
									style = AdminNameShorthand
								} else if field.Name != propName {
									identifier = false
								}
								collector.addStyledRange(
									AdminSymbolComponentProp,
									contractName,
									propName,
									field.NameRange,
									false,
									identifier,
									style,
								)
							}
							if dynamicUsage {
								style := AdminNameExact
								identifier := true
								if field.Shorthand {
									style = AdminNameShorthand
								} else if field.Name != propName {
									identifier = false
								}
								collector.addDynamicStyledRange(
									AdminSymbolComponentProp,
									propName,
									field.NameRange,
									false,
									identifier,
									style,
									selector.Expression,
									dynamicRouterView,
								)
							}
						}
					}
				}
			}
			if len(contractNames) > 0 &&
				(!dynamic || attributeName != selector.AttributeName) {
				for _, contractName := range contractNames {
					if argument, model := NormalizeModelArgument(attributeName); model {
						modelName := "v-model"
						if argument != "" {
							modelName += ":" + CamelToKebab(argument)
						}
						collector.addRange(
							AdminSymbolComponentModel,
							contractName,
							modelName,
							mustVueAttributeArgumentRange(
								nameToken.Range(), attributeName,
							),
							false,
							false,
						)
					}
					if event, found := VueEventReferenceForAttribute(
						attributeName, nameToken.Range(),
					); found {
						collector.addRange(
							AdminSymbolComponentEvent,
							contractName,
							event.Name,
							event.Range,
							false,
							false,
						)
					}
					if prop, found := VuePropReferenceForAttribute(
						attributeName, nameToken.Range(),
					); found {
						collector.addRange(
							AdminSymbolComponentProp,
							contractName,
							prop.Name,
							prop.Range,
							false,
							false,
						)
					}
				}
			}
			if dynamicUsage && attributeName != selector.AttributeName {
				if argument, model := NormalizeModelArgument(attributeName); model {
					modelName := "v-model"
					if argument != "" {
						modelName += ":" + CamelToKebab(argument)
					}
					collector.addDynamicRange(
						AdminSymbolComponentModel,
						modelName,
						mustVueAttributeArgumentRange(
							nameToken.Range(), attributeName,
						),
						selector.Expression,
						dynamicRouterView,
					)
				}
				if event, found := VueEventReferenceForAttribute(
					attributeName, nameToken.Range(),
				); found {
					collector.addDynamicRange(
						AdminSymbolComponentEvent,
						event.Name,
						event.Range,
						selector.Expression,
						dynamicRouterView,
					)
				}
				if prop, found := VuePropReferenceForAttribute(
					attributeName, nameToken.Range(),
				); found {
					collector.addDynamicRange(
						AdminSymbolComponentProp,
						prop.Name,
						prop.Range,
						selector.Expression,
						dynamicRouterView,
					)
				}
			}
			if slotName := NormalizeSlotName(attributeName); slotName != "" &&
				attributeName != "v-slot" {
				ownerTag := TwigSlotOwnerStartingTag(node)
				owners := staticComponentContractNames(ownerTag)
				if len(owners) == 0 && ownerTag == nil {
					if owner := parentComponentName(node); owner != "" {
						owners = []string{owner}
					}
				}
				for _, owner := range owners {
					collector.addRange(
						AdminSymbolComponentSlot,
						owner,
						slotName,
						mustVueAttributeArgumentRange(nameToken.Range(), attributeName),
						false,
						false,
					)
				}
				if len(owners) == 0 {
					if slotSelector, slotDynamic :=
						TwigDynamicComponentSelector(ownerTag); slotDynamic {
						collector.addDynamicRange(
							AdminSymbolComponentSlot,
							slotName,
							mustVueAttributeArgumentRange(
								nameToken.Range(), attributeName,
							),
							slotSelector.Expression,
							twigDynamicComponentUsesRouterView(
								ownerTag, slotSelector,
							),
						)
					}
				}
			}
		}
		if name == "slot" {
			collectSlotDeclaration(tag, filePath, collector)
		}
	}
	for _, node := range twigquery.Nodes(root, twigsyntax.HtmlEndingTag) {
		tag, ok := twigast.CastHtmlEndingTag(node)
		if !ok || tag.Name() == nil {
			continue
		}
		name := tag.Name().Text()
		if IsComponentTag(name) {
			collector.addRange(
				AdminSymbolComponent, "", name, tag.Name().Range(), false, false,
			)
		}
	}
	for _, identifier := range TwigVueExpressionRootIdentifiers(root, content) {
		if twigVueRootIdentifierIsLocal(root, content, identifier) {
			continue
		}
		style := AdminNameExact
		if shorthandMemberRanges[identifier.Range] {
			style = AdminNameShorthand
		}
		collector.addStyledRange(
			AdminSymbolComponentMember, "", identifier.Name,
			identifier.Range, false, true, style,
		)
	}
	return collector.values()
}

// CollectTwigUsages derives Administration symbol occurrences from a live
// lossless Twig CST without mutating the persistent index.
func CollectTwigUsages(
	root *twigsyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []AdminUsageSet {
	if root == nil || lineIndex == nil {
		return nil
	}
	return parseAdminTwigUsages(root, filePath, lineIndex)
}

func staticComponentContractNames(startTag *twigsyntax.Node) []string {
	if startTag == nil {
		return nil
	}
	if name := twigquery.HTMLTagName(startTag); IsComponentTag(name) {
		return []string{name}
	}
	selector, dynamic := TwigDynamicComponentSelector(startTag)
	if !dynamic || !selector.Complete {
		return nil
	}
	var result []string
	for _, name := range selector.Names() {
		if IsComponentTag(name) {
			result = append(result, name)
		}
	}
	return result
}

func twigVueRootIdentifierIsLocal(
	root *twigsyntax.Node,
	content []byte,
	identifier TwigVueMember,
) bool {
	return TwigVueRootIdentifierIsLocal(root, content, identifier)
}

// TwigVueRootIdentifierIsLocal reports whether identifier resolves to a
// lexical v-for, event, or scoped-slot binding instead of the owning component
// instance. Semantic diagnostics use it to avoid shadowing false positives.
func TwigVueRootIdentifierIsLocal(
	root *twigsyntax.Node,
	content []byte,
	identifier TwigVueMember,
) bool {
	if binding, found := TwigVueBindingAtOffset(
		root, content, identifier.Range.Start,
	); found && binding != nil {
		return true
	}
	for _, scope := range TwigScopedSlotsAtOffset(
		root, identifier.Range.Start,
	) {
		for _, binding := range scope.Bindings {
			if binding.LocalName == identifier.Name {
				return true
			}
		}
	}
	return false
}

func collectSlotDeclaration(
	tag twigast.HtmlStartingTag,
	filePath string,
	collector *adminUsageCollector,
) {
	for _, attribute := range tag.Attributes() {
		if twigquery.HTMLAttributeName(attribute.Syntax()) != "name" {
			continue
		}
		value, ok := attribute.Value()
		if !ok {
			return
		}
		inner, ok := value.GetInner()
		if !ok {
			return
		}
		name := strings.TrimSpace(inner.Syntax().Text())
		if name == "" || strings.ContainsAny(name, "{}%") {
			return
		}
		collector.addRange(
			AdminSymbolComponentSlot,
			filePath,
			name,
			inner.Syntax().RangeTrimmedTrivia(),
			true,
			false,
		)
		return
	}
}

func mustVueAttributeArgumentRange(
	rangeValue cst.TextRange,
	attributeName string,
) cst.TextRange {
	resolved, found := vueAttributeArgumentRange(attributeName, rangeValue)
	if !found {
		return rangeValue
	}
	return resolved
}

func parentComponentName(node *twigsyntax.Node) string {
	for current := node.Parent(); current != nil; current = current.Parent() {
		tag, ok := twigast.CastHtmlTag(current)
		if !ok || tag.Name() == nil {
			continue
		}
		starting, startFound := tag.StartingTag()
		if !startFound {
			continue
		}
		if name, found := StaticComponentNameForTag(starting.Syntax()); found {
			return name
		}
	}
	return ""
}

// JavaScriptSymbolAt resolves registry-backed Administration symbols at a
// cursor. It is shared by references and rename so both operations use the
// same conservative context rules as completion and definition.
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

func TwigSymbolAt(node *twigsyntax.Node) (AdminSymbolTarget, bool) {
	if node == nil {
		return AdminSymbolTarget{}, false
	}
	root := node
	for root.Parent() != nil {
		root = root.Parent()
	}
	return TwigSymbolAtOffset(root, node.Range().Start)
}

// TwigDirectiveReferences returns every custom directive attribute in one
// Administration template. Vue built-ins are excluded because they are
// language syntax rather than registry symbols.
func TwigDirectiveReferences(
	root *twigsyntax.Node,
) []VueDirectiveReference {
	if root == nil {
		return nil
	}
	var result []VueDirectiveReference
	for _, attributeNode := range twigquery.Nodes(
		root, twigsyntax.HtmlAttribute,
	) {
		attribute, ok := twigast.CastHtmlAttribute(attributeNode)
		if !ok || attribute.Name() == nil {
			continue
		}
		reference, found := VueDirectiveReferenceForAttribute(
			twigquery.HTMLAttributeName(attributeNode),
			attribute.Name().Range(),
		)
		if found {
			result = append(result, reference)
		}
	}
	return result
}

func TwigDirectiveAtOffset(
	root *twigsyntax.Node,
	offset uint32,
) (VueDirectiveReference, bool) {
	for _, reference := range TwigDirectiveReferences(root) {
		if rangeContains(reference.Range, offset) {
			return reference, true
		}
	}
	return VueDirectiveReference{}, false
}

func TwigSymbolAtOffset(
	root *twigsyntax.Node,
	offset uint32,
) (AdminSymbolTarget, bool) {
	if root == nil {
		return AdminSymbolTarget{}, false
	}
	if reference, found := TwigRegistryReferenceAtOffset(root, offset); found &&
		reference.Name != "" {
		return AdminSymbolTarget{
			Kind: reference.Kind,
			Name: reference.Name,
		}, true
	}
	for _, node := range twigquery.Nodes(root, twigsyntax.HtmlStartingTag) {
		tag, ok := twigast.CastHtmlStartingTag(node)
		if !ok || tag.Name() == nil {
			continue
		}
		tagName := tag.Name().Text()
		selector, dynamic := TwigDynamicComponentSelector(node)
		if dynamic {
			if candidate, candidateFound := selector.CandidateAt(offset); candidateFound {
				return AdminSymbolTarget{
					Kind: AdminSymbolComponent, Name: candidate.Name,
				}, true
			}
		}
		contractName := tagName
		if resolvedName, found := StaticComponentNameForTag(node); found {
			contractName = resolvedName
		}
		if IsComponentTag(tagName) && rangeContains(tag.Name().Range(), offset) {
			return AdminSymbolTarget{
				Kind: AdminSymbolComponent, Name: tagName,
			}, true
		}
		for _, attribute := range tag.Attributes() {
			nameToken := attribute.Name()
			if nameToken == nil {
				continue
			}
			attributeName := twigquery.HTMLAttributeName(attribute.Syntax())
			if directive, found := VueDirectiveReferenceForAttribute(
				attributeName, nameToken.Range(),
			); found && rangeContains(directive.Range, offset) {
				return AdminSymbolTarget{
					Kind: AdminSymbolDirective, Name: directive.Name,
				}, true
			}
			if attributeName == "v-bind" && IsComponentTag(contractName) {
				if value, valueOK := attribute.Value(); valueOK {
					if inner, innerOK := value.GetInner(); innerOK {
						fields, _ := VueObjectBindingFields(
							inner.Syntax().Text(), inner.Syntax().Range().Start,
						)
						for _, field := range fields {
							if rangeContains(field.NameRange, offset) {
								return AdminSymbolTarget{
									Kind:  AdminSymbolComponentProp,
									Owner: contractName,
									Name:  NormalizePropName(field.Name),
								}, true
							}
						}
					}
				}
			}
			if !rangeContains(nameToken.Range(), offset) {
				continue
			}
			if IsComponentTag(contractName) &&
				(!dynamic || attributeName != selector.AttributeName) {
				if argument, model := NormalizeModelArgument(attributeName); model {
					name := "v-model"
					if argument != "" {
						name += ":" + CamelToKebab(argument)
					}
					return AdminSymbolTarget{
						Kind:  AdminSymbolComponentModel,
						Owner: contractName,
						Name:  name,
					}, true
				}
				if eventName := NormalizeEventName(attributeName); eventName != "" {
					return AdminSymbolTarget{
						Kind:  AdminSymbolComponentEvent,
						Owner: contractName,
						Name:  eventName,
					}, true
				}
			}
			if slotName := NormalizeSlotName(attributeName); slotName != "" &&
				attributeName != "v-slot" {
				owner := contractName
				if !IsComponentTag(owner) {
					owner = parentComponentName(node)
				}
				if owner != "" {
					return AdminSymbolTarget{
						Kind:  AdminSymbolComponentSlot,
						Owner: owner,
						Name:  slotName,
					}, true
				}
			}
			if IsComponentTag(contractName) &&
				(!dynamic || attributeName != selector.AttributeName) {
				if propName := NormalizePropName(attributeName); propName != "" {
					return AdminSymbolTarget{
						Kind:  AdminSymbolComponentProp,
						Owner: contractName,
						Name:  propName,
					}, true
				}
			}
		}
		if tagName == "slot" {
			for _, attribute := range tag.Attributes() {
				if twigquery.HTMLAttributeName(attribute.Syntax()) != "name" {
					continue
				}
				value, valueOK := attribute.Value()
				if !valueOK {
					continue
				}
				inner, innerOK := value.GetInner()
				if !innerOK || !rangeContains(inner.Syntax().Range(), offset) {
					continue
				}
				name := strings.TrimSpace(inner.Syntax().Text())
				if name != "" {
					return AdminSymbolTarget{
						Kind: AdminSymbolComponentSlot,
						Name: name,
					}, true
				}
			}
		}
	}
	for _, node := range twigquery.Nodes(root, twigsyntax.HtmlEndingTag) {
		tag, ok := twigast.CastHtmlEndingTag(node)
		if ok && tag.Name() != nil && IsComponentTag(tag.Name().Text()) &&
			rangeContains(tag.Name().Range(), offset) {
			return AdminSymbolTarget{
				Kind: AdminSymbolComponent, Name: tag.Name().Text(),
			}, true
		}
	}
	return AdminSymbolTarget{}, false
}

func stringTarget(kind AdminSymbolKind, node *jssyntax.Node) (AdminSymbolTarget, bool) {
	name := jsquery.StringValue(node)
	if name == "" {
		return AdminSymbolTarget{}, false
	}
	return AdminSymbolTarget{Kind: kind, Name: name}, true
}

type adminUsageCollector struct {
	filePath  string
	lineIndex *cst.LineIndex
	sets      map[string]*AdminUsageSet
	seen      map[string]bool
}

func newAdminUsageCollector(
	filePath string,
	lineIndex *cst.LineIndex,
) *adminUsageCollector {
	return &adminUsageCollector{
		filePath: filePath, lineIndex: lineIndex,
		sets: make(map[string]*AdminUsageSet), seen: make(map[string]bool),
	}
}

func (collector *adminUsageCollector) addStoreDeclaration(call *jssyntax.Node) {
	if literal := jsquery.StringArgument(call, 0); literal != nil {
		collector.addJSString(AdminSymbolStore, "", literal, true)
		return
	}
	object := jsquery.ObjectArgument(call, 0)
	id := jsquery.PropertyValue(jsquery.Property(object, "id"))
	collector.addJSString(AdminSymbolStore, "", id, true)
}

func (collector *adminUsageCollector) addJSString(
	kind AdminSymbolKind,
	owner string,
	node *jssyntax.Node,
	declaration bool,
) {
	if node == nil || node.Kind() != jssyntax.JsString {
		return
	}
	name := jsquery.StringValue(node)
	if name == "" {
		return
	}
	rangeValue, ok := jsStringContentRange(node)
	if !ok {
		return
	}
	collector.addRange(kind, owner, name, rangeValue, declaration, false)
}

func (collector *adminUsageCollector) addNamedJSNode(
	kind AdminSymbolKind,
	owner,
	name string,
	node *jssyntax.Node,
	declaration bool,
) {
	if node == nil || name == "" {
		return
	}
	style := AdminNameExact
	original := strings.TrimSpace(node.Text())
	if node.Kind() == jssyntax.JsString {
		original = jsquery.StringValue(node)
	}
	if kind == AdminSymbolComponentEvent &&
		original != CanonicalEventName(original) {
		style = AdminNameCamel
	}
	if node.Kind() == jssyntax.JsString {
		if rangeValue, ok := jsStringContentRange(node); ok {
			collector.addStyledRange(
				kind, owner, name, rangeValue, declaration, false, style,
			)
		}
		return
	}
	collector.addStyledRange(
		kind,
		owner,
		name,
		node.RangeTrimmedTrivia(),
		declaration,
		true,
		style,
	)
}

func (collector *adminUsageCollector) addNode(
	kind AdminSymbolKind,
	owner,
	name string,
	node *jssyntax.Node,
	declaration bool,
) {
	if node == nil || name == "" {
		return
	}
	collector.addRange(
		kind, owner, name, node.RangeTrimmedTrivia(), declaration, true,
	)
}

func (collector *adminUsageCollector) addRange(
	kind AdminSymbolKind,
	owner,
	name string,
	rangeValue cst.TextRange,
	declaration bool,
	identifier bool,
) {
	collector.addStyledRange(
		kind,
		owner,
		name,
		rangeValue,
		declaration,
		identifier,
		AdminNameExact,
	)
}

func (collector *adminUsageCollector) addStyledRange(
	kind AdminSymbolKind,
	owner,
	name string,
	rangeValue cst.TextRange,
	declaration bool,
	identifier bool,
	nameStyle AdminNameStyle,
) {
	collector.addRangeWithDynamicSelector(
		kind, owner, name, rangeValue, declaration, identifier, nameStyle, "", false,
	)
}

func (collector *adminUsageCollector) addDynamicRange(
	kind AdminSymbolKind,
	name string,
	rangeValue cst.TextRange,
	selector string,
	routerView bool,
) {
	collector.addDynamicStyledRange(
		kind, name, rangeValue, false, false, AdminNameExact, selector, routerView,
	)
}

func (collector *adminUsageCollector) addDynamicStyledRange(
	kind AdminSymbolKind,
	name string,
	rangeValue cst.TextRange,
	declaration bool,
	identifier bool,
	nameStyle AdminNameStyle,
	selector string,
	routerView bool,
) {
	collector.addRangeWithDynamicSelector(
		kind,
		adminDynamicComponentUsageOwner,
		name,
		rangeValue,
		declaration,
		identifier,
		nameStyle,
		selector,
		routerView,
	)
}

func (collector *adminUsageCollector) addRangeWithDynamicSelector(
	kind AdminSymbolKind,
	owner,
	name string,
	rangeValue cst.TextRange,
	declaration bool,
	identifier bool,
	nameStyle AdminNameStyle,
	dynamicSelector string,
	dynamicRouterView bool,
) {
	if name == "" || rangeValue.End <= rangeValue.Start || collector.lineIndex == nil {
		return
	}
	key := AdminUsageKey(kind, owner, name)
	dedupKey := key + "\x00" + strconv.FormatUint(uint64(rangeValue.Start), 10) +
		"\x00" + strconv.FormatUint(uint64(rangeValue.End), 10)
	if collector.seen[dedupKey] {
		return
	}
	collector.seen[dedupKey] = true
	set := collector.sets[key]
	if set == nil {
		set = &AdminUsageSet{
			Kind: kind, Owner: owner, Name: name, FilePath: collector.filePath,
		}
		collector.sets[key] = set
	}
	startLine, startCharacter := collector.lineIndex.PositionUTF16(rangeValue.Start)
	endLine, endCharacter := collector.lineIndex.PositionUTF16(rangeValue.End)
	set.Occurrences = append(set.Occurrences, AdminSourceRange{
		StartLine: int(startLine), StartCharacter: int(startCharacter),
		EndLine: int(endLine), EndCharacter: int(endCharacter),
		Declaration: declaration, Identifier: identifier,
		NameStyle:                nameStyle,
		DynamicComponentSelector: dynamicSelector,
		DynamicRouterView:        dynamicRouterView,
	})
}

func (collector *adminUsageCollector) addSourceRange(
	kind AdminSymbolKind,
	owner,
	name string,
	rangeValue AdminSourceRange,
	nameStyle AdminNameStyle,
) {
	if collector == nil || name == "" ||
		rangeValue.EndLine < rangeValue.StartLine ||
		rangeValue.EndLine == rangeValue.StartLine &&
			rangeValue.EndCharacter <= rangeValue.StartCharacter {
		return
	}
	key := AdminUsageKey(kind, owner, name)
	dedupKey := key + "\x00" + strconv.Itoa(rangeValue.StartLine) + ":" +
		strconv.Itoa(rangeValue.StartCharacter) + ":" +
		strconv.Itoa(rangeValue.EndLine) + ":" +
		strconv.Itoa(rangeValue.EndCharacter)
	if collector.seen[dedupKey] {
		return
	}
	collector.seen[dedupKey] = true
	set := collector.sets[key]
	if set == nil {
		set = &AdminUsageSet{
			Kind: kind, Owner: owner, Name: name, FilePath: collector.filePath,
		}
		collector.sets[key] = set
	}
	rangeValue.Declaration = true
	rangeValue.NameStyle = nameStyle
	set.Occurrences = append(set.Occurrences, rangeValue)
}

func (collector *adminUsageCollector) values() []AdminUsageSet {
	result := make([]AdminUsageSet, 0, len(collector.sets))
	for _, set := range collector.sets {
		result = append(result, *set)
	}
	return result
}

func jsStringContentRange(node *jssyntax.Node) (cst.TextRange, bool) {
	if node == nil {
		return cst.TextRange{}, false
	}
	for element := range node.Descendants() {
		token, ok := element.(*jssyntax.Token)
		if !ok || (token.Kind() != jssyntax.TkString &&
			token.Kind() != jssyntax.TkTemplate) {
			continue
		}
		rangeValue := token.Range()
		text := token.Text()
		if len(text) >= 2 && (text[0] == '\'' || text[0] == '"' || text[0] == '`') &&
			text[len(text)-1] == text[0] {
			rangeValue.Start++
			rangeValue.End--
		}
		return rangeValue, true
	}
	return cst.TextRange{}, false
}

func lastJSIdentifier(node *jssyntax.Node) *jssyntax.Node {
	var result *jssyntax.Node
	if node == nil {
		return nil
	}
	for child := range node.ChildNodes() {
		if child.Kind() == jssyntax.JsIdentifier {
			result = child
		}
	}
	return result
}

func rangeContains(rangeValue cst.TextRange, offset uint32) bool {
	return offset >= rangeValue.Start && offset <= rangeValue.End
}
