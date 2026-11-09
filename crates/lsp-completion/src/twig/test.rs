//! Completion of test names after `is` / `is not`.

use std::collections::HashMap;

use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};

use super::completion::{
    TwigCallableCompletionDeprecation, TwigCompletionProvider,
    apply_twig_callable_completion_deprecation,
};
use super::{markdown, request_offset};
use swlsp_textutil::lowercase;

const TWIG_BUILTIN_TESTS: [&str; 15] = [
    "constant",
    "defined",
    "divisible by",
    "empty",
    "even",
    "false",
    "iterable",
    "mapping",
    "none",
    "null",
    "odd",
    "same as",
    "sequence",
    "string",
    "true",
];

impl TwigCompletionProvider {
    pub(crate) fn twig_test_completions(
        &self,
        request: &CompletionRequest,
    ) -> Option<Vec<protocol::CompletionItem>> {
        let root = request.root.as_ref()?;
        let offset = request_offset(request)?;
        let position = swlsp_twig::test_completion_at(root, request.source().as_bytes(), offset)?;
        let prefix = lowercase(&position.name);
        let mut items_by_name: HashMap<String, protocol::CompletionItem> = HashMap::new();
        if self.include_builtin_completions {
            for name in TWIG_BUILTIN_TESTS {
                if name.starts_with(&prefix) {
                    items_by_name.insert(
                        name.to_string(),
                        protocol::CompletionItem {
                            label: name.to_string(),
                            insert_text: name.to_string(),
                            kind: Some(CompletionItemKind::FUNCTION),
                            detail: "Built-in Twig test".to_string(),
                            ..Default::default()
                        },
                    );
                }
            }
        }
        if let Some(index) = &self.twig_indexer {
            struct Entry {
                status: TwigCallableCompletionDeprecation,
                name: String,
                usage: String,
                file: String,
            }
            let mut entries: HashMap<String, Entry> = HashMap::new();
            for test in index.get_all_twig_tests().unwrap_or_default() {
                let key = lowercase(&test.name);
                if test.name.contains('*') || !key.starts_with(&prefix) {
                    continue;
                }
                let entry = entries.entry(key).or_insert_with(|| Entry {
                    status: TwigCallableCompletionDeprecation {
                        all: true,
                        message: String::new(),
                    },
                    name: test.name.clone(),
                    usage: test.usage.clone(),
                    file: swlsp_uriutil::filepath::base(&test.file_path),
                });
                entry.status.all = entry.status.all && test.deprecated;
                if entry.status.message.is_empty() {
                    entry.status.message = test.deprecation;
                }
            }
            for (key, entry) in entries {
                let mut item = protocol::CompletionItem {
                    label: entry.name.clone(),
                    insert_text: entry.name,
                    kind: Some(CompletionItemKind::FUNCTION),
                    detail: "Custom Twig test".to_string(),
                    ..Default::default()
                };
                if !entry.usage.is_empty() {
                    let mut value = format!("`{}`", entry.usage);
                    if !entry.file.is_empty() {
                        value.push_str(&format!("\n\nDeclared in `{}`.", entry.file));
                    }
                    item.documentation = markdown(value);
                }
                apply_twig_callable_completion_deprecation(&mut item, "test", &entry.status);
                items_by_name.insert(key, item);
            }
        }
        let mut items: Vec<protocol::CompletionItem> = items_by_name.into_values().collect();
        items.sort_by(|left, right| {
            lowercase(&left.label)
                .cmp(&lowercase(&right.label))
                .then_with(|| left.label.cmp(&right.label))
        });
        Some(items)
    }
}
