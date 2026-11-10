//! Request-scoped PHP analysis state.

use std::sync::{Arc, Mutex};
use swlsp_syntax::SyntaxNodeExt as _;

use swlsp_indexer::ParsedFile;
use swlsp_parser_php::query;
use swlsp_parser_php::syntax::PHP_USE_DECLARATION;
use swlsp_php_resolver::NameContext;
use swlsp_php_semantic::{Document, Snapshot, Symbol};
use swlsp_php_types::Type;
use swlsp_syntax::SyntaxNode;

use crate::{PhpIndex, lock};

/// The project-facing wrapper around the semantic PHP name resolver, built
/// from one file's namespace and `use` declarations.
#[derive(Clone, Debug)]
pub struct NameResolver {
    context: NameContext,
}

impl NameResolver {
    pub fn new(root: &SyntaxNode) -> Self {
        let mut context = NameContext::new(&query::namespace(root));
        query::visit(root, &[PHP_USE_DECLARATION], |declaration| {
            context.add_use_declaration(&declaration.text_string());
            true
        });
        NameResolver { context }
    }

    pub fn namespace(&self) -> &str {
        &self.context.namespace
    }

    pub fn resolve(&self, name: &str) -> String {
        self.context.resolve_class(name)
    }
}

/// The semantic state of one request: the analyzed document, the overlay
/// snapshot that contains it, the node the request is about, and the class
/// enclosing that node.
pub struct PhpContext {
    pub inside_class: Option<Symbol>,
    pub node: Option<SyntaxNode>,
    pub document: Arc<Document>,
    pub snapshot: Arc<Snapshot>,
    resolver: Mutex<Option<(SyntaxNode, Arc<NameResolver>)>>,
}

impl std::fmt::Debug for PhpContext {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("PhpContext")
            .field("path", &self.document.path)
            .field("node", &self.node)
            .finish()
    }
}

impl PhpContext {
    /// The import resolver for `root`, shared across repeated lookups in the
    /// same tree (building one walks the whole tree).
    pub fn name_resolver(&self, root: &SyntaxNode) -> Arc<NameResolver> {
        let mut cached = lock(&self.resolver);
        if let Some((cached_root, resolver)) = cached.as_ref()
            && cached_root == root
        {
            return resolver.clone();
        }
        let resolver = Arc::new(NameResolver::new(root));
        *cached = Some((root.clone(), resolver.clone()));
        resolver
    }
}

/// The import resolver for `root`, reused through the request context when
/// one is available.
pub fn name_resolver_for(context: Option<&PhpContext>, root: &SyntaxNode) -> Arc<NameResolver> {
    match context {
        Some(context) => context.name_resolver(root),
        None => Arc::new(NameResolver::new(root)),
    }
}

impl PhpIndex {
    /// Analyzes the tree of `node` (unnamed, version 0) and returns its
    /// request context.
    pub fn context_for_node(&self, node: &SyntaxNode, source: &str) -> PhpContext {
        let root = node.ancestors().last().unwrap_or_else(|| node.clone());
        self.document_context("", 0, Some(node), &root, source)
    }

    /// Analyzes an unsaved document and returns the request context of
    /// `node`. `source` is the complete text of `root`'s tree.
    pub fn document_context(
        &self,
        path: &str,
        version: i64,
        node: Option<&SyntaxNode>,
        root: &SyntaxNode,
        source: &str,
    ) -> PhpContext {
        let document = self.analyze_document(path, version, root, source);
        self.analyzed_document_context(node, Arc::new(document))
    }

    /// The request context of `node` in a file prepared by the scanner,
    /// sharing the semantic document of every indexer preparing it.
    pub fn parsed_file_context(&self, file: &ParsedFile, node: Option<&SyntaxNode>) -> PhpContext {
        self.analyzed_document_context(node, self.analyze_parsed_file(file))
    }

    /// The request context of an already linked document.
    pub fn analyzed_document_context(
        &self,
        node: Option<&SyntaxNode>,
        document: Arc<Document>,
    ) -> PhpContext {
        let snapshot = self.semantic_snapshot().with_document(&document);
        self.analyzed_snapshot_context(node, document, Some(snapshot))
    }

    /// The request context of a document and its already constructed overlay
    /// snapshot, so provider fan-out shares one analysis.
    pub fn analyzed_snapshot_context(
        &self,
        node: Option<&SyntaxNode>,
        document: Arc<Document>,
        snapshot: Option<Arc<Snapshot>>,
    ) -> PhpContext {
        let snapshot =
            snapshot.unwrap_or_else(|| self.semantic_snapshot().with_document(&document));
        let inside_class = node.and_then(|node| {
            let offset = node.text_range().start();
            document
                .symbols
                .iter()
                .find(|symbol| symbol.is_class_like() && symbol.range.contains(offset))
                .cloned()
        });
        PhpContext {
            inside_class,
            node: node.cloned(),
            document,
            snapshot,
            resolver: Mutex::new(None),
        }
    }

    /// Whether the call containing `node` invokes one of `method_names`.
    pub fn is_method_called_name(&self, node: &SyntaxNode, method_names: &[&str]) -> bool {
        query::call_at(node)
            .is_some_and(|call| method_names.contains(&query::call_method_name(&call).as_str()))
    }

    /// Whether the call containing `node` has a receiver whose inferred type
    /// is `class_name` or a subtype of it.
    pub fn is_method_called_on_class(
        &self,
        context: Option<&PhpContext>,
        node: &SyntaxNode,
        class_name: &str,
    ) -> bool {
        let Some(call) = query::call_at(node) else {
            return false;
        };
        let Some(context) = context else {
            return false;
        };
        let Some(receiver) = call.children().next() else {
            return false;
        };
        let receiver_type = context.document.type_of(&receiver).ty;
        if receiver_type.is_unknown() {
            return false;
        }
        context
            .snapshot
            .relations()
            .is_subtype(&receiver_type, &Type::named(class_name, Vec::new()))
    }
}
