//! Events a listener subscribes to by a string where a class constant
//! holds the name.
//!
//! `'kernel.request'` is `KernelEvents::REQUEST`, `'product.written'` is
//! `ProductEvents::PRODUCT_WRITTEN_EVENT`, and an event named by its class
//! is `SomeEvent::class`. The constant survives a rename and leads to the
//! declaration; the string does neither.

use std::collections::HashMap;
use std::sync::{Arc, Mutex, PoisonError};

use serde_json::json;
use swlsp_event::{ReferenceOrigin, ReferenceRole};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Snapshot, SymbolKind};
use swlsp_php_types::Kind as TypeKind;
use swlsp_syntax::{SyntaxElement, SyntaxNodeExt};
use swlsp_textutil::lowercase;

use super::event::event_document_references;
use super::{lower_ext, problem};
use crate::diagnostics::ProblemAnalyzer;

pub const EVENT_NAME_LITERAL_CODE: &str = "symfony.event.name_literal";

/// The constants that name an event: `(class, constant)` per event name in
/// lower case.
type Constants = HashMap<String, Vec<(String, String)>>;

pub struct EventConstantAnalyzer {
    php_index: Option<PhpIndex>,
    /// The constants of the PHP index revision they were read from.
    constants: Mutex<Option<(u64, Arc<Constants>)>>,
}

impl EventConstantAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self {
            php_index,
            constants: Mutex::new(None),
        }
    }

    fn constants(&self, revision: u64, snapshot: &Snapshot) -> Arc<Constants> {
        let mut cached = self
            .constants
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        if let Some((known, constants)) = cached.as_ref()
            && *known == revision
        {
            return constants.clone();
        }
        let constants = Arc::new(event_constants(snapshot));
        *cached = Some((revision, constants.clone()));
        constants
    }
}

/// The string constants of every class named `…Events` or `…Event`: the
/// classes frameworks collect event names in, and event classes that name
/// themselves.
fn event_constants(snapshot: &Snapshot) -> Constants {
    let mut constants = Constants::new();
    for class in snapshot.global_class_views() {
        let name = class.name();
        if !(name.ends_with("Events") || name.ends_with("Event")) {
            continue;
        }
        let qualified = class.fully_qualified().trim_start_matches('\\').to_owned();
        for member in snapshot.members_of(class.id()) {
            if member.kind != SymbolKind::ClassConstant
                || member.ty.kind() != TypeKind::LiteralString
                || member.ty.name().is_empty()
            {
                continue;
            }
            constants
                .entry(lowercase(member.ty.name()))
                .or_default()
                .push((qualified.clone(), member.name.to_string()));
        }
    }
    constants
}

/// The one constant to name an event by: of a `…Events` class when there
/// is one, and none when several classes claim the name.
fn preferred(candidates: &[(String, String)]) -> Option<&(String, String)> {
    let collections: Vec<&(String, String)> = candidates
        .iter()
        .filter(|(class, _)| class.ends_with("Events"))
        .collect();
    match (collections.as_slice(), candidates) {
        ([only], _) => Some(only),
        ([], [only]) => Some(only),
        _ => None,
    }
}

impl ProblemAnalyzer for EventConstantAnalyzer {
    fn input_revision(&self, _document: &TextDocument) -> Option<u64> {
        Some(crate::diagnostics::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php) = &self.php_index else {
            return Ok(Vec::new());
        };
        let source = document.source();
        if lower_ext(&document.uri) != ".php"
            || !(source.contains("getSubscribedEvents") || source.contains("AsEventListener"))
        {
            return Ok(Vec::new());
        }
        let Some(context) = swlsp_lsp_phpanalysis::context_for_document(php, document)? else {
            return Ok(Vec::new());
        };
        let references = event_document_references(Some(&context), document);
        if references.is_empty() {
            return Ok(Vec::new());
        }
        let Some(state) = swlsp_lsp_phpanalysis::for_document(php, document)? else {
            return Ok(Vec::new());
        };
        let constants = self.constants(php.revision(), &state.snapshot);
        let mut result = Vec::new();
        for reference in references {
            ctx.check()?;
            if reference.role != ReferenceRole::Event
                || reference.origin != ReferenceOrigin::Listener
                || reference.node.kind() != PHP_STRING
                || reference.name.is_empty()
            {
                continue;
            }
            let name = reference.name.trim_start_matches('\\');
            let (class, constant) = if let Some((class, constant)) = constants
                .get(&lowercase(name))
                .and_then(|candidates| preferred(candidates))
            {
                (class.clone(), constant.clone())
            } else if name.contains('\\')
                && let Some(class) = state.snapshot.classes(name).first()
            {
                // An event known by its class.
                (
                    class.fully_qualified.trim_start_matches('\\').to_owned(),
                    "class".to_owned(),
                )
            } else {
                continue;
            };
            let short = class.rsplit('\\').next().unwrap_or(&class);
            let mut found = problem(
                EVENT_NAME_LITERAL_CODE,
                reference.node.range_trimmed_trivia(),
                format!("Use {short}::{constant} for the event '{name}'"),
                DiagnosticSeverity::HINT,
            );
            found.payload = Some(json!({ "class": class, "constant": constant }));
            found.element = Some(SyntaxElement::Node(reference.node.clone()));
            result.push(found);
        }
        Ok(result)
    }
}
