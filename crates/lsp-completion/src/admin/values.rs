//! Completions for Administration prop values, object bindings and dynamic
//! component attributes.

use std::collections::{BTreeMap, BTreeSet, HashMap, HashSet};

use swlsp_admin::{
    VueComponent, VueComponentProp, VueDynamicComponentSelector, VueObjectBindingField,
    normalize_prop_name, static_component_name_for_tag,
    twig_component_object_binding_value_at_offset, twig_dynamic_component_selector,
    vue_prop_allowed_values, vue_static_string_literal,
};
use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::ast::{AstNode, HtmlStartingTag};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax as twigsyntax;
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};

use super::AdminCompletionProvider;
use super::components::{
    common_admin_prop_deprecation, item, mark_admin_completion_deprecated, set_markdown, snippet,
};
use crate::common::{protocol_range, sort_completion_items, text_edit};

fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end.max(start)))
}

impl AdminCompletionProvider {
    /// Slot completions from the component contract owning a slot
    /// directive. Closed dynamic selectors contribute only shared slots.
    pub(super) fn get_resolved_slot_completions(
        &self,
        node: Option<&SyntaxNode>,
        template_path: &str,
        live_owner: Option<&VueComponent>,
    ) -> Option<Vec<protocol::CompletionItem>> {
        let index = self.index.as_ref()?;
        let node = node?;
        let attribute = twigquery::html_attribute_at(node)?;
        let attribute_name = twigquery::html_attribute_name(&attribute).unwrap_or_default();
        if !attribute_name.starts_with('#') && !attribute_name.starts_with("v-slot") {
            return None;
        }
        let Some(start_tag) = twigquery::starting_html_tag_at(&attribute) else {
            return Some(Vec::new());
        };
        match index.resolve_twig_slot_consumer_components(template_path, &start_tag, live_owner) {
            Ok((components, true)) if !components.is_empty() => {
                Some(self.get_slot_completions_for_components(&components, Some(node)))
            }
            _ => Some(Vec::new()),
        }
    }

    /// Allowed values of the component prop whose value is at `offset`.
    pub(super) fn component_prop_value_completions(
        &self,
        request: &CompletionRequest,
        offset: u32,
        template_path: &str,
        live_owner: Option<&VueComponent>,
    ) -> Option<Vec<protocol::CompletionItem>> {
        self.index.as_ref()?;
        let root = request.root.as_ref()?;
        let line_index = request.line_index()?;
        let (start_tag, prop_name, value_range) =
            match component_prop_value_context(root, request.source(), offset) {
                Some(found) => found,
                None => {
                    let (start_tag, field, value_range) =
                        twig_component_object_binding_value_at_offset(root, offset)?;
                    (start_tag, normalize_prop_name(&field.name), value_range)
                }
            };
        let components = self.components_for_prop_value(&start_tag, template_path, live_owner);
        if components.is_empty() {
            return None;
        }
        let mut common: Option<HashSet<String>> = None;
        let mut complete = true;
        for component in &components {
            let Some(prop) = component.component_prop(&prop_name) else {
                return Some(Vec::new());
            };
            let (values, values_complete) = vue_prop_allowed_values(prop);
            if values.is_empty() {
                continue;
            }
            complete = complete && values_complete;
            let current: HashSet<String> = values
                .into_iter()
                .filter(|value| !value.is_empty())
                .collect();
            match common.as_mut() {
                None => common = Some(current),
                Some(common) => common.retain(|value| current.contains(value)),
            }
        }
        let Some(common) = common else {
            return Some(Vec::new());
        };
        let edit_range = protocol_range(value_range, line_index);
        let mut detail = if complete {
            format!("component prop value • {prop_name}")
        } else {
            format!("known component prop value • {prop_name}")
        };
        if components.len() > 1 {
            detail += " • all dynamic candidates";
        }
        let values: BTreeSet<String> = common.into_iter().collect();
        let mut items: Vec<_> = values
            .into_iter()
            .map(|value| {
                let mut completion = item(&value, protocol::CompletionItemKind::VALUE, &detail);
                completion.text_edit = Some(text_edit(edit_range, value));
                completion
            })
            .collect();
        sort_completion_items(&mut items);
        Some(items)
    }

    fn components_for_prop_value(
        &self,
        start_tag: &SyntaxNode,
        template_path: &str,
        live_owner: Option<&VueComponent>,
    ) -> Vec<VueComponent> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        if let Some(selector) = twig_dynamic_component_selector(start_tag) {
            return match index.resolve_dynamic_component_contracts_for_owner(
                template_path,
                &selector,
                live_owner,
                Some(start_tag),
            ) {
                Ok((_, components, true)) => components,
                _ => Vec::new(),
            };
        }
        let Some(name) = static_component_name_for_tag(start_tag) else {
            return Vec::new();
        };
        match index.get_component_for_template_tag(template_path, &name, live_owner) {
            Ok(Some(component)) => vec![component],
            _ => Vec::new(),
        }
    }

    /// Prop name completions for keys of an object `v-bind`.
    pub(super) fn object_binding_prop_completions(
        &self,
        start_tag: &SyntaxNode,
        fields: &[VueObjectBindingField],
        template_path: &str,
        live_owner: Option<&VueComponent>,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        let components = if let Some(selector) = twig_dynamic_component_selector(start_tag) {
            match index.resolve_dynamic_component_contracts_for_owner(
                template_path,
                &selector,
                live_owner,
                Some(start_tag),
            ) {
                Ok((_, components, true)) => components,
                _ => return Vec::new(),
            }
        } else {
            let Some(name) = static_component_name_for_tag(start_tag) else {
                return Vec::new();
            };
            match index.get_component_for_template_tag(template_path, &name, live_owner) {
                Ok(Some(component)) => vec![component],
                _ => return Vec::new(),
            }
        };
        if components.is_empty() {
            return Vec::new();
        }
        let present: HashSet<String> = fields
            .iter()
            .map(|field| normalize_prop_name(&field.name))
            .collect();
        let mut common: BTreeMap<String, Vec<VueComponentProp>> = BTreeMap::new();
        for (index, component) in components.iter().enumerate() {
            let mut current: HashMap<&str, &VueComponentProp> = HashMap::new();
            for prop in &component.props {
                current.insert(&prop.name, prop);
            }
            if index == 0 {
                for (name, prop) in current {
                    common.insert(name.to_owned(), vec![prop.clone()]);
                }
                continue;
            }
            common.retain(|name, values| match current.get(name.as_str()) {
                Some(prop) => {
                    values.push((*prop).clone());
                    true
                }
                None => false,
            });
        }
        let mut items = Vec::with_capacity(common.len());
        for (name, props) in &common {
            if present.contains(name) {
                continue;
            }
            let mut types: Vec<&str> = Vec::new();
            let mut required = true;
            for prop in props {
                let type_name = prop.type_.trim();
                if !type_name.is_empty() && !types.contains(&type_name) {
                    types.push(type_name);
                }
                required = required && prop.required;
            }
            let mut detail = types.join(" | ");
            if components.len() > 1 {
                detail = format!("{detail} • all dynamic candidates")
                    .trim()
                    .to_owned();
            }
            let mut completion = item(name, protocol::CompletionItemKind::PROPERTY, detail);
            snippet(&mut completion, format!("{name}: $0"));
            let mut documentation = format!("Forward component prop `{name}` through `v-bind`.");
            if required {
                documentation += "\n\nRequired by every candidate contract.";
            }
            set_markdown(&mut completion, documentation);
            mark_admin_completion_deprecated(
                &mut completion,
                &common_admin_prop_deprecation(props),
            );
            items.push(completion);
        }
        sort_completion_items(&mut items);
        items
    }

    /// Attribute completions for a dynamic `<component :is>` tag.
    pub(super) fn dynamic_component_attribute_completions(
        &self,
        node: Option<&SyntaxNode>,
        template_path: &str,
        live_owner: Option<&VueComponent>,
    ) -> Option<Vec<protocol::CompletionItem>> {
        let index = self.index.as_ref()?;
        let start_tag = twigquery::starting_html_tag_at(node?)?;
        let selector = twig_dynamic_component_selector(&start_tag)?;
        let components = match index.resolve_dynamic_component_contracts_for_owner(
            template_path,
            &selector,
            live_owner,
            Some(&start_tag),
        ) {
            Ok((_, components, true)) if !components.is_empty() => components,
            _ => return Some(Vec::new()),
        };
        let mut common: BTreeMap<String, protocol::CompletionItem> = BTreeMap::new();
        for (index, component) in components.iter().enumerate() {
            let items = self.get_component_prop_completions_for_owner(
                &component.name,
                template_path,
                live_owner,
            );
            let mut current: BTreeMap<String, protocol::CompletionItem> = BTreeMap::new();
            for item in items {
                current.insert(item.label.clone(), item);
            }
            if index == 0 {
                common = current;
                continue;
            }
            common.retain(|label, _| current.contains_key(label));
        }
        let mut result: Vec<_> = common
            .into_values()
            .map(|mut item| {
                item.detail = format!("{} • all dynamic candidates", item.detail)
                    .trim()
                    .to_owned();
                item
            })
            .collect();
        sort_completion_items(&mut result);
        Some(result)
    }
}

/// A static or bound string prop value
/// under `offset`.
fn component_prop_value_context(
    root: &SyntaxNode,
    content: &str,
    offset: u32,
) -> Option<(SyntaxNode, String, TextRange)> {
    for start_tag in twigquery::iterate_nodes(root, &[twigsyntax::HTML_STARTING_TAG]) {
        let tag_range = start_tag.text_range();
        if offset < u32::from(tag_range.start()) || offset > u32::from(tag_range.end()) {
            continue;
        }
        let Some(tag) = HtmlStartingTag::cast(start_tag.clone()) else {
            continue;
        };
        let selector = twig_dynamic_component_selector(&start_tag);
        for attribute in tag.attributes() {
            let name = twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default();
            let bound = name.starts_with(':') || name.starts_with("v-bind:");
            if name.is_empty()
                || name.starts_with('@')
                || name.starts_with('#')
                || name.starts_with("v-") && !bound
            {
                continue;
            }
            if selector
                .as_ref()
                .is_some_and(|selector: &VueDynamicComponentSelector| {
                    name == selector.attribute_name
                })
            {
                continue;
            }
            let Some(value) = attribute.value() else {
                continue;
            };
            let mut value_text = String::new();
            let value_range = if let Some(inner) = value.inner() {
                let inner_range = inner.syntax().text_range();
                if bound {
                    let Some((_, content_start, content_end)) =
                        vue_static_string_literal(&inner.syntax().text().to_string())
                    else {
                        continue;
                    };
                    let start = u32::from(inner_range.start());
                    range(start + content_start, start + content_end)
                } else {
                    value_text = inner.syntax().text().to_string();
                    inner_range
                }
            } else {
                if bound {
                    continue;
                }
                let (Some(opening), Some(closing)) = (value.opening_quote(), value.closing_quote())
                else {
                    continue;
                };
                range(
                    u32::from(opening.text_range().end()),
                    u32::from(closing.text_range().start()),
                )
            };
            if offset < u32::from(value_range.start())
                || offset > u32::from(value_range.end())
                || value_text.contains("{{")
                || value_text.contains("{%")
                || value_text.contains("{#")
                || usize::from(value_range.end()) > content.len()
            {
                continue;
            }
            let prop_name = normalize_prop_name(&name);
            if prop_name.is_empty() {
                continue;
            }
            return Some((start_tag, prop_name, value_range));
        }
    }
    None
}

/// The dynamic component selector of the tag at the request position.
pub(super) fn admin_dynamic_component_selector_at_completion(
    request: &CompletionRequest,
    offset: Option<u32>,
) -> Option<VueDynamicComponentSelector> {
    let offset = offset?;
    let node = request.node.as_ref()?;
    let attribute = twigquery::html_attribute_at(node)?;
    let start_tag = twigquery::starting_html_tag_at(node)?;
    let selector = twig_dynamic_component_selector(&start_tag)?;
    if twigquery::html_attribute_name(&attribute).unwrap_or_default() != selector.attribute_name {
        return None;
    }
    let expression = selector.expression_range;
    if !expression.is_empty()
        && (offset < u32::from(expression.start()) || offset > u32::from(expression.end()))
    {
        return None;
    }
    Some(selector)
}
