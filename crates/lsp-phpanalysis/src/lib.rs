//! Linked PHP semantic state shared by LSP features operating on the same
//! immutable text document (port of Go `internal/lsp/phpanalysis`).
//!
//! Go enriches `context.Context`; Rust returns an explicit [`PhpContext`]
//! (it holds syntax nodes and cannot travel in `swlsp_lsp::Context`).

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, SyntaxContext, TextDocument};
use swlsp_parser_php::syntax::{PHP_ARGUMENT_LIST, PHP_NAME};
use swlsp_php::{PhpContext, PhpIndex};
use swlsp_php_imports::Analysis;
use swlsp_php_semantic::{Document, Flags, Snapshot, SymbolKind};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

/// The document-local semantic graph and the workspace snapshot it was
/// linked against.
#[derive(Debug)]
pub struct State {
    pub document: Arc<Document>,
    pub snapshot: Arc<Snapshot>,
}

/// Performs at most one semantic analysis per PHP index, text document and
/// workspace revision; concurrent callers share it. `None` when the
/// document has no syntax tree.
pub fn for_document(
    index: &PhpIndex,
    document: &TextDocument,
) -> Result<Option<Arc<State>>, BoxError> {
    let Some(tree) = document.tree() else {
        return Ok(None);
    };
    let path = swlsp_uriutil::path(&document.uri)?;
    let revision = index.semantic_snapshot().revision;
    Ok(Some(document.memoized_analysis(
        index.identity(),
        revision,
        || {
            let semantic = Arc::new(index.analyze_tree(&path, i64::from(document.version), tree));
            let snapshot = index.semantic_snapshot().with_document(&semantic);
            State {
                document: semantic,
                snapshot,
            }
        },
    )))
}

/// The request context of `document` built from its memoized analysis, so
/// diagnostic inspections share one binder and inference pass.
pub fn context_for_document(
    index: &PhpIndex,
    document: &TextDocument,
) -> Result<Option<PhpContext>, BoxError> {
    let Some(state) = for_document(index, document)? else {
        return Ok(None);
    };
    let root = document.root();
    Ok(Some(index.analyzed_snapshot_context(
        root.as_ref(),
        state.document.clone(),
        Some(state.snapshot.clone()),
    )))
}

/// The PHP request context of `syntax` (Go's PHP context enricher): the
/// memoized document analysis linked to the request node, or a fresh unnamed
/// analysis when the document URI has no file path. `None` outside PHP
/// documents or without a tree.
///
/// Providers should call this instead of analyzing the document themselves:
/// after the first call per document version and workspace revision it only
/// rebuilds the node wrapper (well under a microsecond).
pub fn request_context(index: &PhpIndex, syntax: &SyntaxContext) -> Option<PhpContext> {
    if syntax.language != Some(LanguageId::Php) {
        return None;
    }
    let document = syntax.document.as_deref()?;
    match for_document(index, document) {
        Ok(Some(state)) => Some(index.analyzed_snapshot_context(
            syntax.node.as_ref(),
            state.document.clone(),
            Some(state.snapshot.clone()),
        )),
        Ok(None) => None,
        Err(_) => {
            let root = syntax.root.as_ref()?;
            Some(index.document_context(
                "",
                i64::from(document.version),
                syntax.node.as_ref(),
                root,
                document.source(),
            ))
        }
    }
}

/// The import analysis shared by diagnostics and explicit source actions.
/// Imports suppressed for `php.unusedImport` are reported as used.
pub fn imports(
    context: &Context,
    index: &PhpIndex,
    document: &TextDocument,
) -> Result<Analysis, BoxError> {
    context.check()?;
    if document.language != Some(LanguageId::Php) || !document.parse_errors().is_empty() {
        return Ok(Analysis::default());
    }
    let Some(state) = for_document(index, document)? else {
        return Ok(Analysis::default());
    };
    let Some(root) = document.root() else {
        return Ok(Analysis::default());
    };
    // Cancelled results are not memoized on the immutable document.
    let mut result =
        swlsp_php_imports::analyze(&|| context.is_cancelled(), &root, &state.document)?;
    let suppressed = swlsp_php_suppression::parse(document.source());
    for scope in &mut result.scopes {
        for declaration in &mut scope.declarations {
            let declaration_start = u32::from(declaration.node.range_trimmed_trivia().start());
            for item in &mut declaration.items {
                if suppressed.suppresses(u32::from(item.range.start()), "php.unusedImport")
                    || suppressed.suppresses(declaration_start, "php.unusedImport")
                {
                    item.used = true;
                }
            }
        }
    }
    Ok(result)
}

impl State {
    /// Whether every linked target of the call's name is a function or
    /// method that does not return by reference.
    pub fn call_returns_value(&self, call: &SyntaxNode) -> bool {
        let mut name = None;
        for child in call.children() {
            if child.kind() == PHP_NAME {
                name = Some(child.clone());
            }
            if child.kind() == PHP_ARGUMENT_LIST {
                break;
            }
        }
        let Some(name) = name else {
            return false;
        };
        let start = name.range_trimmed_trivia().start();
        let Some(reference) = self
            .document
            .references
            .iter()
            .find(|reference| reference.range.start() == start)
        else {
            return false;
        };
        let resolved = (!reference.resolved.is_empty()).then_some(&reference.resolved);
        let mut ids = resolved
            .into_iter()
            .chain(reference.candidate_ids())
            .peekable();
        if ids.peek().is_none() {
            return false;
        }
        ids.all(|id| {
            self.snapshot.symbol_view(id).is_some_and(|symbol| {
                matches!(symbol.kind(), SymbolKind::Method | SymbolKind::Function)
                    && !symbol.flags().has(Flags::BY_REFERENCE)
            })
        })
    }
}

#[cfg(test)]
mod tests;
