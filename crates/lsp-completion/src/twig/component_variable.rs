//! Go `twig_component_variable_completion.go`: props, `this` and
//! `computed` inside Twig component templates.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_ACCESSOR, TWIG_VAR};
use swlsp_php::PhpIndex;
use swlsp_php_types::Type;
use swlsp_twigcomponent::{Component, Index, Prop};

use super::completion::{
    compact_twig_accessor, is_twig_identifier, twig_member_type, twig_php_member_completions,
};
use super::lower_ext;
use crate::common::{component_prop_detail, go_to_lower, sort_completion_items};

pub struct TwigComponentVariableCompletionProvider {
    index: Option<Arc<Index>>,
    php_index: Option<PhpIndex>,
}

impl TwigComponentVariableCompletionProvider {
    pub fn new(index: Option<Arc<Index>>, php_index: Option<PhpIndex>) -> Self {
        Self { index, php_index }
    }

    fn member_completions(
        &self,
        index: &Index,
        request: &CompletionRequest,
        components: &[Component],
        props: &[Prop],
    ) -> Option<Vec<protocol::CompletionItem>> {
        let php = self.php_index.as_ref()?;
        let accessor = twigquery::closest_node_of_kind(request.node.as_ref()?, &[TWIG_ACCESSOR])?;
        let compact = compact_twig_accessor(&accessor.text().to_string());
        let parts: Vec<&str> = compact.split('.').collect();
        if parts.len() < 2 {
            return None;
        }
        if !parts[..parts.len() - 1]
            .iter()
            .all(|part| is_twig_identifier(part))
        {
            return None;
        }
        if parts[0] == "computed" {
            let path = swlsp_uriutil::path(&request.params.text_document.uri).ok()?;
            let computed = index.computed_for_template(&path).ok()?;
            let mut items: Vec<protocol::CompletionItem> = computed
                .into_iter()
                .map(|prop| protocol::CompletionItem {
                    label: prop.name,
                    kind: Some(CompletionItemKind::PROPERTY),
                    detail: prop.ty,
                    ..Default::default()
                })
                .collect();
            sort_completion_items(&mut items);
            return Some(items);
        }
        let mut receiver = Type::unknown();
        if parts[0] == "this" {
            if let Some(component) = components
                .iter()
                .find(|component| !component.class.is_empty())
            {
                receiver = Type::named(&component.class, Vec::new());
            }
        } else if let Some(prop) = props
            .iter()
            .find(|prop| prop.name == parts[0] && !prop.ty.is_empty())
            && let Ok(parsed) = swlsp_php_types::parse(&prop.ty)
        {
            receiver = parsed;
        }
        if receiver.is_unknown() {
            return None;
        }
        let snapshot = php.semantic_snapshot();
        for segment in &parts[1..parts.len() - 1] {
            receiver = twig_member_type(&snapshot, &receiver, segment);
            if receiver.is_unknown() {
                return Some(Vec::new());
            }
        }
        Some(twig_php_member_completions(&snapshot, &receiver))
    }
}

impl CompletionProvider for TwigComponentVariableCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = &self.index else {
            return Vec::new();
        };
        let (Some(node), Some(root)) = (request.node.as_ref(), request.root.as_ref()) else {
            return Vec::new();
        };
        if lower_ext(&request.params.text_document.uri) != ".twig"
            || twigquery::closest_node_of_kind(node, &[TWIG_VAR]).is_none()
        {
            return Vec::new();
        }
        let Ok(path) = swlsp_uriutil::path(&request.params.text_document.uri) else {
            return Vec::new();
        };
        let Ok((components, props)) = index.context_for_template(&path, Some(root)) else {
            return Vec::new();
        };
        if components.is_empty() {
            return Vec::new();
        }
        if let Some(items) = self.member_completions(index, request, &components, &props) {
            return items;
        }
        let mut items: HashMap<String, protocol::CompletionItem> = HashMap::new();
        for prop in &props {
            items.insert(
                go_to_lower(&prop.name),
                protocol::CompletionItem {
                    label: prop.name.clone(),
                    kind: Some(CompletionItemKind::VARIABLE),
                    detail: component_prop_detail(prop),
                    ..Default::default()
                },
            );
        }
        if !index
            .computed_for_template(&path)
            .unwrap_or_default()
            .is_empty()
        {
            items.insert(
                "computed".to_string(),
                protocol::CompletionItem {
                    label: "computed".to_string(),
                    kind: Some(CompletionItemKind::VARIABLE),
                    detail: "cached component getters".to_string(),
                    ..Default::default()
                },
            );
        }
        if let Some(component) = components
            .iter()
            .find(|component| !component.class.is_empty())
        {
            items.insert(
                "this".to_string(),
                protocol::CompletionItem {
                    label: "this".to_string(),
                    kind: Some(CompletionItemKind::VARIABLE),
                    detail: component.class.clone(),
                    ..Default::default()
                },
            );
        }
        // Keys are lowercase labels, so the order is total.
        let mut result: Vec<protocol::CompletionItem> = items.into_values().collect();
        sort_completion_items(&mut result);
        result
    }

    fn trigger_characters(&self) -> Vec<String> {
        vec![".".into()]
    }
}
