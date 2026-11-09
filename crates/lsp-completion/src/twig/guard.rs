//! Completion of types and callables in `{% guard %}` tags.

use std::collections::HashMap;

use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_twig::GuardKind;

use super::completion::{
    TwigCallableCompletionDeprecation, TwigCompletionProvider,
    apply_twig_callable_completion_deprecation,
};
use super::{request_offset, sort_by_lower_label};
use swlsp_textutil::lowercase;

struct TwigGuardCallable {
    name: String,
    deprecated: bool,
    deprecation: String,
}

impl TwigCompletionProvider {
    pub(crate) fn twig_guard_completions(
        &self,
        request: &CompletionRequest,
    ) -> Option<Vec<protocol::CompletionItem>> {
        self.twig_indexer.as_ref()?;
        let offset = request_offset(request)?;
        let guard = swlsp_twig::guard_completion_at(request.source().as_bytes(), offset)?;
        if !guard.callable {
            return Some(twig_guard_type_completions(&guard.prefix));
        }
        let prefix = lowercase(&guard.prefix);
        let mut statuses: HashMap<String, TwigCallableCompletionDeprecation> = HashMap::new();
        let mut display_names: HashMap<String, String> = HashMap::new();
        for callable in self.twig_guard_callables(guard.kind) {
            let key = lowercase(&callable.name);
            if callable.name.contains('*') || !key.starts_with(&prefix) {
                continue;
            }
            let status = statuses.entry(key.clone()).or_insert_with(|| {
                display_names.insert(key.clone(), callable.name.clone());
                TwigCallableCompletionDeprecation {
                    all: true,
                    message: String::new(),
                }
            });
            status.all = status.all && callable.deprecated;
            if status.message.is_empty() {
                status.message = callable.deprecation;
            }
        }
        let kind = guard.kind.as_str();
        let mut items: Vec<protocol::CompletionItem> = statuses
            .iter()
            .map(|(key, status)| {
                let name = display_names[key].clone();
                let mut item = protocol::CompletionItem {
                    label: name.clone(),
                    insert_text: name,
                    kind: Some(CompletionItemKind::FUNCTION),
                    detail: format!("Twig {kind}"),
                    ..Default::default()
                };
                apply_twig_callable_completion_deprecation(&mut item, kind, status);
                item
            })
            .collect();
        // Keys are lowercase labels, so the order is total.
        sort_by_lower_label(&mut items);
        Some(items)
    }

    fn twig_guard_callables(&self, kind: GuardKind) -> Vec<TwigGuardCallable> {
        let Some(index) = &self.twig_indexer else {
            return Vec::new();
        };
        macro_rules! collect {
            ($values:expr) => {
                $values
                    .unwrap_or_default()
                    .into_iter()
                    .map(|value| TwigGuardCallable {
                        name: value.name,
                        deprecated: value.deprecated,
                        deprecation: value.deprecation,
                    })
                    .collect()
            };
        }
        match kind {
            GuardKind::Function => collect!(index.get_all_twig_functions()),
            GuardKind::Filter => collect!(index.get_all_twig_filters()),
            GuardKind::Test => collect!(index.get_all_twig_tests()),
            GuardKind::None => Vec::new(),
        }
    }
}

fn twig_guard_type_completions(prefix: &str) -> Vec<protocol::CompletionItem> {
    let prefix = lowercase(prefix);
    [GuardKind::Function, GuardKind::Filter, GuardKind::Test]
        .into_iter()
        .map(GuardKind::as_str)
        .filter(|name| name.starts_with(&prefix))
        .map(|name| protocol::CompletionItem {
            label: name.to_string(),
            insert_text: name.to_string(),
            kind: Some(CompletionItemKind::KEYWORD),
            detail: "Twig guard type".to_string(),
            ..Default::default()
        })
        .collect()
}
