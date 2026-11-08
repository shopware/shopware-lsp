//! Port of Go `admin_completion_slots.go`.

use std::collections::{HashMap, HashSet};

use swlsp_admin::{VueComponent, VueComponentSlot};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::SyntaxNode;

use super::AdminCompletionProvider;
use super::components::{item, set_markdown};

impl AdminCompletionProvider {
    /// Slot names of `component_name` (Go `getSlotCompletions`).
    #[cfg(test)]
    pub(super) fn get_slot_completions(
        &self,
        component_name: &str,
        node: Option<&SyntaxNode>,
        template_path: &str,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        match index.get_component_for_template_tag_shared(template_path, component_name, None) {
            Ok(Some(component)) => {
                self.get_slot_completions_for_components(&[VueComponent::clone(&component)], node)
            }
            _ => Vec::new(),
        }
    }

    pub(super) fn get_slot_completions_for_components(
        &self,
        components: &[VueComponent],
        node: Option<&SyntaxNode>,
    ) -> Vec<protocol::CompletionItem> {
        if components.is_empty() {
            return Vec::new();
        }
        // A slot shorthand already followed by `>` only needs the name.
        let has_closing_bracket = node.is_some_and(|node| {
            let text = node.text().to_string();
            text.starts_with('#') && text.contains('>')
        });

        let mut counts: HashMap<&str, usize> = HashMap::new();
        let mut slots: HashMap<&str, &VueComponentSlot> = HashMap::new();
        for component in components {
            let mut seen = HashSet::new();
            for slot in &component.slots {
                if slot.is_dynamic_name() || slot.name.is_empty() || !seen.insert(&slot.name) {
                    continue;
                }
                *counts.entry(&slot.name).or_default() += 1;
                slots.insert(&slot.name, slot);
            }
        }
        let mut names: Vec<&str> = slots
            .keys()
            .copied()
            .filter(|name| counts[name] == components.len())
            .collect();
        names.sort_unstable();
        let mut component_names: Vec<&str> = components
            .iter()
            .map(|component| component.name.as_str())
            .collect();
        component_names.sort_unstable();

        let mut items = Vec::with_capacity(names.len());
        for name in names {
            let slot = slots[name];
            let detail = if components.len() > 1 {
                "slot • all dynamic candidates"
            } else {
                "slot"
            };
            let mut completion = item(&slot.name, protocol::CompletionItemKind::PROPERTY, detail);
            if has_closing_bracket {
                completion.insert_text = slot.name.clone();
                completion.insert_text_format = Some(protocol::InsertTextFormat::PLAIN_TEXT);
            } else {
                completion.insert_text = format!("{}>$0</template>", slot.name);
                completion.insert_text_format = Some(protocol::InsertTextFormat::SNIPPET);
            }
            let mut doc = format!("**Slot:** `{}`\n\n", slot.name);
            if component_names.len() == 1 {
                doc += &format!("**Component:** `{}`", component_names[0]);
            } else {
                doc += &format!("**Components:** `{}`", component_names.join("`, `"));
            }
            set_markdown(&mut completion, doc);
            items.push(completion);
        }
        items
    }
}

/// Go `uniqueSortedStrings`: distinct non-empty values, sorted.
pub(super) fn unique_sorted_strings(values: Vec<String>) -> Vec<String> {
    let mut result: Vec<String> = values
        .into_iter()
        .filter(|value| !value.is_empty())
        .collect();
    result.sort_unstable();
    result.dedup();
    result
}
