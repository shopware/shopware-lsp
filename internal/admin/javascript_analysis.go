package admin

import (
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

// JavaScriptDocumentAnalysis owns the immutable whole-tree query index used by
// diagnostics which inspect several JavaScript relationships in one document.
// Interactive single-position features can continue using the standalone
// helpers without paying to build a complete index.
type JavaScriptDocumentAnalysis struct {
	index             *jsquery.NodeIndex
	variableBindings  map[*jssyntax.Node]map[string]javaScriptVariableBinding
	constInitializers map[*jssyntax.Node]javaScriptConstInitializer
}

type javaScriptConstInitializer struct {
	name       string
	expression string
}

func NewJavaScriptDocumentAnalysis(
	root *jssyntax.Node,
) *JavaScriptDocumentAnalysis {
	analysis := &JavaScriptDocumentAnalysis{
		index:             jsquery.NewNodeIndex(root),
		variableBindings:  make(map[*jssyntax.Node]map[string]javaScriptVariableBinding),
		constInitializers: make(map[*jssyntax.Node]javaScriptConstInitializer),
	}
	for _, declaration := range analysis.index.Nodes(
		jssyntax.JsVariableDeclaration,
	) {
		text := declaration.Text()
		bindings := javaScriptVariableBindings(text)
		if len(bindings) != 0 {
			byName := make(map[string]javaScriptVariableBinding, len(bindings))
			for _, binding := range bindings {
				byName[binding.localName] = binding
			}
			analysis.variableBindings[declaration] = byName
		}
		name, expression, found := directComponentConstInitializer(text)
		if found {
			analysis.constInitializers[declaration] = javaScriptConstInitializer{
				name: name, expression: expression,
			}
		}
	}
	return analysis
}

func (analysis *JavaScriptDocumentAnalysis) variableBinding(
	declaration *jssyntax.Node,
	name string,
) (javaScriptVariableBinding, bool) {
	if analysis == nil {
		return javaScriptVariableBinding{}, false
	}
	binding, found := analysis.variableBindings[declaration][name]
	return binding, found
}

func (analysis *JavaScriptDocumentAnalysis) constInitializer(
	declaration *jssyntax.Node,
) (string, string, bool) {
	if analysis == nil {
		return "", "", false
	}
	initializer, found := analysis.constInitializers[declaration]
	return initializer.name, initializer.expression, found
}

func (analysis *JavaScriptDocumentAnalysis) Nodes(
	kinds ...jssyntax.Kind,
) []*jssyntax.Node {
	if analysis == nil {
		return nil
	}
	return analysis.index.Nodes(kinds...)
}

func (analysis *JavaScriptDocumentAnalysis) Calls(
	names ...string,
) []*jssyntax.Node {
	if analysis == nil {
		return nil
	}
	return analysis.index.Calls(names...)
}

func (analysis *JavaScriptDocumentAnalysis) ShopwareUtilsMember(
	node *jssyntax.Node,
) (receiver []string, memberName string, matched bool) {
	if analysis == nil {
		return JavaScriptShopwareUtilsMember(node)
	}
	return javaScriptShopwareUtilsMember(node, analysis)
}

func (analysis *JavaScriptDocumentAnalysis) ShopwareUtilsMemberNameNode(
	node *jssyntax.Node,
) *jssyntax.Node {
	if analysis == nil {
		return JavaScriptShopwareUtilsMemberNameNode(node)
	}
	return javaScriptShopwareUtilsMemberNameNode(node, analysis)
}

func (analysis *JavaScriptDocumentAnalysis) ShopwareEventBusEventAt(
	node *jssyntax.Node,
) (operation, eventName string, matched bool) {
	if analysis == nil {
		return JavaScriptShopwareEventBusEventAt(node)
	}
	return javaScriptShopwareEventBusEventAt(node, analysis)
}

func (analysis *JavaScriptDocumentAnalysis) ApplicationContainerMember(
	node *jssyntax.Node,
) (containerName, memberName string, matched bool) {
	if analysis == nil {
		return JavaScriptApplicationContainerMember(node)
	}
	return javaScriptApplicationContainerMember(node, analysis)
}

func (analysis *JavaScriptDocumentAnalysis) ApplicationContainerMemberNameNode(
	node *jssyntax.Node,
) *jssyntax.Node {
	if analysis == nil {
		return JavaScriptApplicationContainerMemberNameNode(node)
	}
	return javaScriptApplicationContainerMemberNameNode(node, analysis)
}
