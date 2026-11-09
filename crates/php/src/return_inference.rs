//! On-demand inferred return types for published generations (see
//! [`swlsp_php_semantic::ReturnInference`]).
//!
//! Indexing persists the return types it inferred against the generation it
//! saw. Request-time analysis asks this inference instead: it analyzes the
//! declaring file from disk against the generation being queried, so an
//! inferred type always reflects the current workspace, also after the files
//! it depends on changed.

use std::sync::{Arc, Weak};

use swlsp_php_binder::Binder;
use swlsp_php_inference::Analyzer;
use swlsp_php_semantic::{Flags, ReturnInference, Snapshot, SymbolId};
use swlsp_php_types::Type;

use crate::{Inner, PhpIndex};

pub(crate) struct IndexReturnInference {
    /// Weak: the semantic store owning this inference belongs to the index.
    pub(crate) inner: Weak<Inner>,
}

impl ReturnInference for IndexReturnInference {
    fn infer_returns(
        &self,
        generation: &Arc<Snapshot>,
        path: &str,
    ) -> Option<Vec<(SymbolId, Type)>> {
        let index = PhpIndex {
            inner: self.inner.upgrade()?,
        };
        // Runtime stubs (`phpstub://`) declare their return types.
        let content = std::fs::read(path).ok()?;
        let source = String::from_utf8_lossy(&content);
        let parsed = swlsp_parser_php::parse(&source);
        let root = parsed.root();
        let document = Binder::new().bind_node(path, 0, Some(&root), &source);
        let snapshot = generation.with_declarations(&document);
        let document = swlsp_php_binder::link_owned(document, &snapshot);
        let document = Analyzer::new(Some(snapshot), index.type_extensions())
            .infer_returns_owned(document, &root, &source);
        Some(
            document
                .symbols
                .into_iter()
                .filter(|symbol| symbol.flags.has(Flags::INFERRED_RETURN))
                .map(|symbol| (symbol.id, symbol.return_type))
                .collect(),
        )
    }
}
