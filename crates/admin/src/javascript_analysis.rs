//! Whole-document JavaScript query index.

use crate::*;

/// The immutable whole-tree query index used by diagnostics which inspect
/// several JavaScript relationships in one document. Single-position
/// features keep using the standalone helpers (`None` analysis).
pub struct JavaScriptDocumentAnalysis {
    index: jsquery::NodeIndex,
    variable_bindings: HashMap<SyntaxNode, HashMap<String, JavaScriptVariableBinding>>,
    const_initializers: HashMap<SyntaxNode, (String, String)>,
}

impl JavaScriptDocumentAnalysis {
    pub fn new(root: &SyntaxNode) -> Self {
        let index = jsquery::NodeIndex::new(root);
        let mut variable_bindings = HashMap::new();
        let mut const_initializers = HashMap::new();
        for declaration in index.nodes_of_kind(jssyntax::JS_VARIABLE_DECLARATION) {
            let text = declaration.text().to_string();
            let bindings = javascript_variable_bindings(&text);
            if !bindings.is_empty() {
                let by_name: HashMap<String, JavaScriptVariableBinding> = bindings
                    .into_iter()
                    .map(|binding| (binding.local_name.clone(), binding))
                    .collect();
                variable_bindings.insert(declaration.clone(), by_name);
            }
            if let Some(initializer) = direct_component_const_initializer(&text) {
                const_initializers.insert(declaration.clone(), initializer);
            }
        }
        Self {
            index,
            variable_bindings,
            const_initializers,
        }
    }

    pub(crate) fn variable_binding(
        &self,
        declaration: &SyntaxNode,
        name: &str,
    ) -> Option<&JavaScriptVariableBinding> {
        self.variable_bindings.get(declaration)?.get(name)
    }

    /// The `(name, expression)` of a direct `const` initializer.
    pub(crate) fn const_initializer(&self, declaration: &SyntaxNode) -> Option<(String, String)> {
        self.const_initializers.get(declaration).cloned()
    }

    pub fn nodes(&self, kinds: &[Kind]) -> Vec<SyntaxNode> {
        self.index.nodes(kinds)
    }

    pub fn calls(&self, names: &[&str]) -> Vec<SyntaxNode> {
        self.index.calls(names)
    }

    pub fn iterate_calls<'s>(
        &'s self,
        names: &'s [&'s str],
    ) -> impl Iterator<Item = &'s SyntaxNode> + 's {
        self.index.iter_calls(names)
    }

    pub fn shopware_utils_member(&self, node: &SyntaxNode) -> Option<(Vec<String>, String)> {
        javascript_shopware_utils_member_impl(node, Some(self))
    }

    pub fn shopware_utils_member_name_node(&self, node: &SyntaxNode) -> Option<SyntaxNode> {
        javascript_shopware_utils_member_name_node_impl(node, Some(self))
    }

    /// The `(operation, event name)` of a Shopware event-bus call.
    pub fn shopware_event_bus_event_at(&self, node: &SyntaxNode) -> Option<(String, String)> {
        javascript_shopware_event_bus_event_at_impl(node, Some(self))
    }

    pub fn application_container_member(&self, node: &SyntaxNode) -> Option<(String, String)> {
        javascript_application_container_member_impl(node, Some(self))
    }

    pub fn application_container_member_name_node(&self, node: &SyntaxNode) -> Option<SyntaxNode> {
        javascript_application_container_member_name_node_impl(node, Some(self))
    }
}
