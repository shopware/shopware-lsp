//! Call inference extension points and the
//! built-in extensions.

mod attribute_contracts;
mod builtins;
mod call_contracts;
mod faker;

use swlsp_parser_php::query;
use swlsp_php_resolver::NameContext;
use swlsp_php_semantic::{Document, Snapshot, TypeFact};
use swlsp_php_types::{Relations, Type};
use swlsp_syntax::SyntaxNode;

pub use attribute_contracts::AttributeContracts;
pub use builtins::Builtins;
pub use call_contracts::CallContracts;
pub use faker::FakerTypes;

pub(crate) use call_contracts::normalize_contract_expression;

use crate::syntax::range_text;

/// Call analysis materializes one argument list shared with signature
/// resolution.
pub type CallArgument = swlsp_php_resolver::Argument;

/// Everything an extension may inspect about one call.
#[derive(Clone, Default)]
pub struct CallContext<'a> {
    pub snapshot: Option<&'a Snapshot>,
    pub document: Option<&'a Document>,
    /// The call (or object creation) node.
    pub node: Option<&'a SyntaxNode>,
    /// Source text of the tree containing `node`.
    pub source: &'a str,
    pub name: &'a str,
    /// Unknown for function calls.
    pub receiver: Type,
    pub arguments: &'a [CallArgument],
    pub current_class: Type,
    pub is_static: bool,
}

impl<'a> CallContext<'a> {
    /// Source text of a node of the analyzed tree.
    pub fn text(&self, node: &SyntaxNode) -> &'a str {
        range_text(self.source, node.text_range())
    }

    /// The expression of the `index`-th argument of the call node.
    pub fn argument_expression(&self, index: usize) -> Option<SyntaxNode> {
        self.node
            .and_then(|node| query::argument_expression(node, index))
    }

    pub(crate) fn relations(&self) -> Relations<'a> {
        self.snapshot
            .map_or_else(Relations::default, |snapshot| snapshot.relations())
    }

    /// Name context (namespace and imports) at the call site.
    pub(crate) fn name_context(&self) -> NameContext {
        let Some(document) = self.document else {
            return NameContext::new("");
        };
        let mut context = NameContext::new(&document.namespace);
        context.set_phpdoc_aliases(document.type_aliases.clone());
        if let Some(node) = self.node
            && let Some(scope) = document.scope_at(node.text_range().start().into())
        {
            let aliases = context.phpdoc_aliases.clone();
            context = NameContext::with_imports(scope.namespace.clone(), &scope.imports);
            context.phpdoc_aliases = aliases;
        }
        context
    }
}

/// Supplies domain-specific return types without coupling the core
/// evaluator to Symfony or Shopware.
pub trait Extension: Send + Sync {
    fn infer_call(&self, context: &CallContext<'_>) -> Option<TypeFact>;

    /// Advances whenever the data the extension infers from changes (for
    /// example an index it queries). Analyses memoized per PHP index
    /// revision include it in their key, so their types follow that data.
    /// Stateless extensions keep the default.
    fn revision(&self) -> u64 {
        0
    }
}

impl<F> Extension for F
where
    F: Fn(&CallContext<'_>) -> Option<TypeFact> + Send + Sync,
{
    fn infer_call(&self, context: &CallContext<'_>) -> Option<TypeFact> {
        self(context)
    }
}
