package admin

import (
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
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
