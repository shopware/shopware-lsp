//! Administration component name, tag and prop completions.

use std::collections::HashSet;

use swlsp_admin::{
    VueComponent, VueComponentProp, VueDynamicComponentSelector, camel_to_kebab,
    canonical_event_name, is_component_tag, static_component_name_for_tag, vue_prop_allowed_values,
    vue_prop_value_type,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax as twigsyntax;
use swlsp_syntax::{SyntaxNode, SyntaxToken};

use super::AdminCompletionProvider;
use super::slots::unique_sorted_strings;
use crate::common::sort_completion_items;
use swlsp_textutil::lowercase;

/// A completion item with a label, kind and detail.
pub(super) fn item(
    label: impl Into<String>,
    kind: protocol::CompletionItemKind,
    detail: impl Into<String>,
) -> protocol::CompletionItem {
    protocol::CompletionItem {
        label: label.into(),
        kind: Some(kind),
        detail: detail.into(),
        ..Default::default()
    }
}

/// Sets Markdown documentation.
pub(super) fn set_markdown(item: &mut protocol::CompletionItem, value: impl Into<String>) {
    item.documentation = Some(protocol::MarkupContent {
        kind: protocol::MarkupKind::Markdown,
        value: value.into(),
    });
}

pub(super) fn documentation_value(item: &protocol::CompletionItem) -> &str {
    item.documentation
        .as_ref()
        .map_or("", |documentation| documentation.value.as_str())
}

/// Appends `suffix` to the item's documentation, if it has one.
pub(super) fn append_documentation(item: &mut protocol::CompletionItem, suffix: &str) {
    if let Some(documentation) = item.documentation.as_mut() {
        documentation.value.push_str(suffix);
    }
}

pub(super) fn snippet(item: &mut protocol::CompletionItem, insert_text: String) {
    item.insert_text = insert_text;
    item.insert_text_format = Some(protocol::InsertTextFormat::SNIPPET);
}

impl AdminCompletionProvider {
    /// Whether the position is in an HTML tag name.
    pub(super) fn is_in_html_tag_name(
        &self,
        node: Option<&SyntaxNode>,
        token: Option<&SyntaxToken>,
        content: &str,
    ) -> bool {
        let (Some(_), Some(token)) = (node, token) else {
            return false;
        };
        if let Some(parent) = token.parent()
            && (parent.kind() == twigsyntax::HTML_STARTING_TAG
                || parent.kind() == twigsyntax::HTML_ENDING_TAG)
        {
            for child in parent
                .children_with_tokens()
                .filter_map(|child| child.into_token())
            {
                if (child.kind() == twigsyntax::TK_WORD
                    || child.kind() == twigsyntax::TK_TWIG_COMPONENT_NAME)
                    && child.text_range() == token.text_range()
                {
                    return true;
                }
            }
        }
        if token.kind() == twigsyntax::TK_LESS_THAN
            || token.kind() == twigsyntax::TK_LESS_THAN_SLASH
        {
            return true;
        }
        let bytes = content.as_bytes();
        let end = usize::from(token.text_range().end());
        let before = if end < bytes.len() {
            &bytes[..end]
        } else {
            bytes
        };
        if let Some(last_lt) = before.iter().rposition(|byte| *byte == b'<') {
            let partial = &before[last_lt + 1..];
            return !partial
                .iter()
                .any(|byte| matches!(byte, b'>' | b' ' | b'\t' | b'\n' | b'\r'));
        }
        false
    }

    /// Component tag completions for the template at `template_path`.
    #[cfg(test)]
    pub(super) fn get_component_tag_completions(
        &self,
        template_path: &str,
    ) -> Vec<protocol::CompletionItem> {
        self.get_component_tag_completions_for_owner(template_path, None)
    }

    pub(super) fn get_component_tag_completions_for_owner(
        &self,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        let Ok(mut component_names) = index.get_all_component_names() else {
            return Vec::new();
        };
        let fetched;
        let mut owner = owner;
        if !template_path.is_empty() {
            if owner.is_none() {
                fetched = index
                    .get_component_by_template_path(template_path)
                    .ok()
                    .flatten();
                owner = fetched.as_ref();
            }
            if let Some(owner) = owner {
                component_names.extend(
                    owner
                        .local_components
                        .iter()
                        .map(|local| local.name.clone()),
                );
            }
        }
        let component_names = unique_sorted_strings(component_names);

        let mut items = Vec::with_capacity(component_names.len() + 1);
        for name in component_names {
            let mut completion = protocol::CompletionItem {
                label: name.clone(),
                kind: Some(protocol::CompletionItemKind::CLASS),
                ..Default::default()
            };
            snippet(&mut completion, format!("{name}>$0</{name}>"));
            if let Ok(Some(component)) =
                index.get_component_for_template_tag_shared(template_path, &name, owner)
            {
                let mut doc = String::from("**Shopware Admin Component**\n\n");
                if !component.extends_component.is_empty() {
                    doc += &format!("**Extends:** `{}`\n\n", component.extends_component);
                }
                if !component.props.is_empty() {
                    doc += "**Props:** ";
                    let names: Vec<&str> = component
                        .props
                        .iter()
                        .map(|prop| prop.name.as_str())
                        .collect();
                    doc += &names.join(", ");
                    doc += "\n";
                }
                set_markdown(&mut completion, doc);
                mark_admin_completion_deprecated(&mut completion, &component.deprecated);
            }
            items.push(completion);
        }

        let mut template = item(
            "template",
            protocol::CompletionItemKind::CLASS,
            "slot template",
        );
        snippet(&mut template, "template #".to_owned());
        set_markdown(
            &mut template,
            "**Vue Slot Template**\n\nUsed to fill named slots in parent components.\n\nExample: `<template #default>...</template>`",
        );
        items.push(template);
        items
    }

    /// Whether `node` is the parent argument of
    /// `Component.extend('name', '<caret>', ...)`.
    pub(super) fn is_in_extend_parent_argument(&self, node: &SyntaxNode) -> bool {
        if !self.is_second_string_argument(node) {
            return false;
        }
        let name = jsquery::call_name(node).unwrap_or_default();
        name == "Component.extend" || name == "Shopware.Component.extend"
    }

    pub(super) fn is_second_string_argument(&self, node: &SyntaxNode) -> bool {
        jsquery::string_at(node).is_some() && jsquery::string_argument_index(node) == Some(1)
    }

    /// Every registered component name.
    pub(super) fn get_component_completions(&self) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        let Ok(component_names) = index.get_all_component_names() else {
            return Vec::new();
        };
        let mut items = Vec::with_capacity(component_names.len());
        for name in component_names {
            let mut completion = protocol::CompletionItem {
                label: name.clone(),
                kind: Some(protocol::CompletionItemKind::CLASS),
                ..Default::default()
            };
            if let Ok(Some(component)) = index.get_effective_component(&name) {
                let mut doc = String::from("**Shopware Admin Component**\n\n");
                if !component.extends_component.is_empty() {
                    doc += &format!("**Extends:** `{}`\n\n", component.extends_component);
                }
                if !component.file_path.is_empty() {
                    doc += &format!(
                        "**Registered in:** `{}`\n",
                        swlsp_uriutil::filepath::base(&component.file_path)
                    );
                }
                set_markdown(&mut completion, doc);
                mark_admin_completion_deprecated(&mut completion, &component.deprecated);
            }
            items.push(completion);
        }
        items
    }

    /// Component names a dynamic `<component :is>` selector can resolve to.
    pub(super) fn get_dynamic_component_completions(
        &self,
        template_path: &str,
        selector: &VueDynamicComponentSelector,
        offset: u32,
        owner: Option<&VueComponent>,
    ) -> Vec<protocol::CompletionItem> {
        let mut items = self.get_component_completions();
        let mut seen: HashSet<String> = items.iter().map(|item| item.label.clone()).collect();
        if !template_path.is_empty()
            && let Some(index) = self.index.as_ref()
        {
            let fetched;
            let mut owner = owner;
            if owner.is_none() {
                fetched = index
                    .get_component_by_template_path(template_path)
                    .ok()
                    .flatten();
                owner = fetched.as_ref();
            }
            if let Some(owner) = owner {
                for local in &owner.local_components {
                    if local.name.is_empty() || !seen.insert(local.name.clone()) {
                        continue;
                    }
                    items.push(item(
                        &local.name,
                        protocol::CompletionItemKind::CLASS,
                        "Template-local Administration component",
                    ));
                }
            }
        }
        let mut inside_literal = selector.candidate_at(offset).is_some();
        if !inside_literal && offset > 0 {
            inside_literal = selector.candidate_at(offset - 1).is_some();
        }
        for completion in &mut items {
            completion.detail = if completion.deprecated {
                "Deprecated Vue dynamic component".to_owned()
            } else {
                "Vue dynamic component".to_owned()
            };
            completion.insert_text = completion.label.clone();
            completion.insert_text_format = Some(protocol::InsertTextFormat::PLAIN_TEXT);
            if selector.attribute_name != "is" && !inside_literal {
                completion.insert_text = format!("'{}'", completion.label);
            }
        }
        sort_completion_items(&mut items);
        items
    }

    /// The component name of the tag whose attribute is being completed.
    pub(super) fn get_component_name_for_attribute_completion(
        &self,
        node: Option<&SyntaxNode>,
    ) -> String {
        if !self.is_in_html_attribute_completion_position(node) {
            return String::new();
        }
        let Some(start_tag) = node.and_then(twigquery::starting_html_tag_at) else {
            return String::new();
        };
        let component_name = twigquery::html_tag_name(&start_tag).unwrap_or_default();
        if is_component_tag(&component_name) {
            return component_name;
        }
        static_component_name_for_tag(&start_tag).unwrap_or_default()
    }

    /// Whether `node` is a position where an attribute name can be completed.
    pub(super) fn is_in_html_attribute_completion_position(
        &self,
        node: Option<&SyntaxNode>,
    ) -> bool {
        let Some(node) = node else {
            return false;
        };
        let Some(start_tag) = twigquery::starting_html_tag_at(node) else {
            return false;
        };
        if node.kind() == twigsyntax::HTML_STARTING_TAG
            || twigquery::html_attribute_at(node).is_some()
            || node.kind() == twigsyntax::ERROR
        {
            return true;
        }
        let range = node.text_range();
        for child in start_tag
            .children_with_tokens()
            .filter_map(|child| child.into_token())
        {
            if (child.kind() == twigsyntax::TK_WORD
                || child.kind() == twigsyntax::TK_TWIG_COMPONENT_NAME)
                && range.start() >= child.text_range().start()
                && range.end() <= child.text_range().end()
            {
                return false;
            }
        }
        true
    }

    /// Prop completions of `component_name`.
    #[cfg(test)]
    pub(super) fn get_component_prop_completions(
        &self,
        component_name: &str,
        template_path: &str,
    ) -> Vec<protocol::CompletionItem> {
        self.get_component_prop_completions_for_owner(component_name, template_path, None)
    }

    pub(super) fn get_component_prop_completions_for_owner(
        &self,
        component_name: &str,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        let Ok(Some(component)) =
            index.get_component_for_template_tag_shared(template_path, component_name, owner)
        else {
            return Vec::new();
        };
        let mut items = Vec::new();
        for prop in &component.props {
            let attribute_name = camel_to_kebab(&prop.name);
            let mut completion = item(
                &attribute_name,
                protocol::CompletionItemKind::PROPERTY,
                &prop.type_,
            );
            let mut doc = prop.documentation.trim().to_owned();
            if !doc.is_empty() {
                doc += "\n\n";
            }
            if !prop.type_.is_empty() {
                doc += &format!("**Type:** `{}`\n\n", prop.type_);
            }
            if prop.required {
                doc += "**Required**\n\n";
            }
            if !prop.default.is_empty() {
                doc += &format!("**Default:** `{}`\n", prop.default);
            }
            let (values, complete) = vue_prop_allowed_values(prop);
            if !values.is_empty() {
                let label = if complete {
                    "**Allowed values:** "
                } else {
                    "**Known values:** "
                };
                doc += label;
                doc += &admin_completion_value_list(&values);
                doc += "\n";
            }
            if !doc.is_empty() {
                set_markdown(&mut completion, doc.clone());
            }
            mark_admin_completion_deprecated(&mut completion, &prop.deprecated);
            items.push(completion);

            let mut binding = item(
                format!(":{attribute_name}"),
                protocol::CompletionItemKind::PROPERTY,
                format!("{} (v-bind)", prop.type_),
            );
            snippet(&mut binding, format!(":{attribute_name}=\"$0\""));
            if !doc.is_empty() {
                set_markdown(&mut binding, doc);
            }
            mark_admin_completion_deprecated(&mut binding, &prop.deprecated);
            items.push(binding);
        }

        for event in component.component_events() {
            let event_name = canonical_event_name(&event.name);
            if event_name.is_empty() {
                continue;
            }
            let mut detail = String::from("event");
            if !event.type_.is_empty() {
                detail += " • ";
                detail += &event.type_;
            }
            let mut completion = item(
                format!("@{event_name}"),
                protocol::CompletionItemKind::EVENT,
                detail,
            );
            snippet(&mut completion, format!("@{event_name}=\"$0\""));
            let mut documentation = event.documentation.trim().to_owned();
            if !event.type_.is_empty() {
                if !documentation.is_empty() {
                    documentation += "\n\n";
                }
                documentation += &format!("**Payload:** `{}`", event.type_);
            }
            if !documentation.is_empty() {
                set_markdown(&mut completion, documentation);
            }
            items.push(completion);
        }

        for model in component.component_models() {
            let mut detail = format!("v-model • {} / {}", model.prop_name, model.event_name);
            let value_type = vue_prop_value_type(&model.prop.type_);
            if !value_type.is_empty() {
                detail += " • ";
                detail += &value_type;
            }
            let mut completion = item(
                &model.attribute_name,
                protocol::CompletionItemKind::PROPERTY,
                detail,
            );
            snippet(&mut completion, format!("{}=\"$0\"", model.attribute_name));
            set_markdown(
                &mut completion,
                format!(
                    "Two-way binding for prop `{}` through event `{}`.",
                    model.prop_name, model.event_name
                ),
            );
            mark_admin_completion_deprecated(&mut completion, &model.prop.deprecated);
            items.push(completion);
        }
        items
    }
}

fn admin_completion_value_list(values: &[String]) -> String {
    values
        .iter()
        .map(|value| {
            let display = if value.is_empty() {
                "(empty)"
            } else {
                value.as_str()
            };
            format!("`{}`", display.replace('`', "\\`"))
        })
        .collect::<Vec<_>>()
        .join(", ")
}

/// Marks `item` deprecated and appends the deprecation `message` to its
/// documentation.
pub(super) fn mark_admin_completion_deprecated(item: &mut protocol::CompletionItem, message: &str) {
    let message = message.trim();
    if message.is_empty() {
        return;
    }
    item.deprecated = true;
    if item.detail.is_empty() {
        item.detail = "Deprecated Administration API".to_owned();
    } else if !lowercase(&item.detail).starts_with("deprecated") {
        item.detail = format!("Deprecated • {}", item.detail);
    }
    let deprecation = format!("**Deprecated:** {message}");
    if documentation_value(item).is_empty() {
        set_markdown(item, deprecation);
    } else if let Some(documentation) = item.documentation.as_mut() {
        documentation.value = format!("{deprecation}\n\n{}", documentation.value);
    }
}

/// The deprecation message shared by every prop in `props`, or empty.
pub(super) fn common_admin_prop_deprecation(props: &[VueComponentProp]) -> String {
    if props.is_empty() {
        return String::new();
    }
    let mut seen = HashSet::new();
    let mut messages = Vec::new();
    for prop in props {
        let message = prop.deprecated.trim();
        if message.is_empty() {
            return String::new();
        }
        if seen.insert(message) {
            messages.push(message);
        }
    }
    messages.join(" / ")
}
