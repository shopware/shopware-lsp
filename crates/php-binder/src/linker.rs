//! Resolves references against one immutable workspace generation.

use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Document, NameKind, Reference, Snapshot, SymbolKind};

/// Resolves references of a copy of `document`.
pub fn link(document: &Document, snapshot: &Snapshot) -> Document {
    link_owned(document.clone(), snapshot)
}

/// Resolves references in a document exclusively owned by the caller, so
/// the indexing pipeline can avoid deep-cloning the document.
pub fn link_owned(mut document: Document, snapshot: &Snapshot) -> Document {
    let mut references = std::mem::take(&mut document.references);
    for reference in &mut references {
        reference.clear_candidate_ids();
        reference.resolved = Default::default();
        match reference.kind {
            NameKind::Class => {
                let names = reference.qualified_names().to_vec();
                for name in &names {
                    snapshot.visit_class_views(name, |candidate| {
                        reference.add_candidate(candidate.symbol_id());
                        true
                    });
                }
            }
            NameKind::Function | NameKind::Constant => {
                let is_function = reference.kind == NameKind::Function;
                let names = reference.qualified_names().to_vec();
                for name in &names {
                    let mut found = false;
                    let mut visit = |candidate: swlsp_php_semantic::SymbolView<'_>| {
                        found = true;
                        reference.add_candidate(candidate.symbol_id());
                        true
                    };
                    if is_function {
                        snapshot.visit_function_views(name, &mut visit);
                    } else {
                        snapshot.visit_constant_views(name, &mut visit);
                    }
                    if found {
                        break;
                    }
                }
            }
            NameKind::Variable => append_local_candidate_ids(&document, reference),
            NameKind::Member => {
                let resolver = MemberResolver::new(snapshot);
                let receiver = reference.receiver.clone();
                let name = reference.name.clone();
                let target_kind = reference.target_kind;
                let visit = |candidate: swlsp_php_semantic::SymbolView<'_>| {
                    reference.add_candidate(candidate.symbol_id());
                    true
                };
                match target_kind {
                    SymbolKind::Method => {
                        resolver.visit_method_ids(&receiver, &name, visit);
                    }
                    SymbolKind::ClassConstant => {
                        resolver.visit_constant_ids(&receiver, &name, visit);
                    }
                    _ => {
                        resolver.visit_property_ids(&receiver, &name, visit);
                    }
                }
            }
        }
        if reference.resolved.is_empty() && reference.candidate_ids().len() == 1 {
            let only = reference.candidate_ids()[0].clone();
            reference.resolved = only;
            reference.clear_candidate_ids();
        }
    }
    document.references = references;
    document
}

fn append_local_candidate_ids(document: &Document, reference: &mut Reference) {
    let mut scope = reference.scope;
    if scope as usize >= document.scopes.len() {
        return;
    }
    let name = reference.name.clone();
    loop {
        let Some(current) = document.scopes.get(scope as usize) else {
            return;
        };
        if current.has_symbol(&document.symbols, &name) {
            for id in current.symbol_ids(&document.symbols, &name) {
                reference.add_candidate(id.clone());
            }
            return;
        }
        if current.id == current.parent {
            return;
        }
        scope = current.parent;
    }
}
