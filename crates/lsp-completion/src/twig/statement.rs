//! Go `twig_statement_completion.go`: `if member` / `for item in member`
//! statements from typed PHP members.

use std::collections::HashMap;

use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Flags, Symbol, SymbolKind, Visibility};
use swlsp_php_types::{Kind as TypeKind, Type, equal_fold};
use swlsp_twig::PhpAccessResolver;

use super::completion::{TwigCompletionProvider, effective_twig_globals};
use super::request_offset;
use super::tag::twig_tag_completion_prefix;
use crate::common::go_to_lower;

impl TwigCompletionProvider {
    pub(crate) fn twig_statement_completions(
        &self,
        request: &CompletionRequest,
    ) -> Option<Vec<protocol::CompletionItem>> {
        let php = self.php_index.as_ref()?;
        request.root.as_ref()?;
        let offset = request_offset(request)?;
        let prefix = twig_tag_completion_prefix(request.source().as_bytes(), offset)?;
        let if_completions = equal_fold(&prefix, "if");
        let for_completions = !prefix.is_empty() && "for".starts_with(&go_to_lower(&prefix));
        if !if_completions && !for_completions {
            return None;
        }
        let resolver = PhpAccessResolver::new(Some(php), self.twig_indexer.as_deref());
        let snapshot = php.semantic_snapshot();
        let variable_types = self.typed_twig_root_types(request);
        let mut variable_names: Vec<&String> = variable_types.keys().collect();
        // Go sorts map keys by lowercase only; ties are ordered by name.
        variable_names.sort_by(|left, right| {
            go_to_lower(left)
                .cmp(&go_to_lower(right))
                .then_with(|| left.cmp(right))
        });
        let mut items: HashMap<String, protocol::CompletionItem> = HashMap::new();
        for variable_name in variable_names {
            let receiver = &variable_types[variable_name];
            if receiver.is_unknown() {
                continue;
            }
            for member in MemberResolver::new(&snapshot).all(receiver) {
                let Some(name) = twig_statement_member_name(&member.symbol) else {
                    continue;
                };
                let path = format!("{variable_name}.{name}");
                let (label, detail) = if if_completions && twig_statement_boolean_type(&member.ty) {
                    (format!("if {path}"), "Twig condition from typed PHP member")
                } else if for_completions {
                    let (_, element) = resolver.iterable_types(&member.ty);
                    if element.is_unknown() {
                        continue;
                    }
                    (
                        format!("for {} in {path}", singular_twig_variable(&name)),
                        "Twig loop from typed PHP iterable",
                    )
                } else {
                    continue;
                };
                let item = protocol::CompletionItem {
                    label: label.clone(),
                    insert_text: label.clone(),
                    kind: Some(CompletionItemKind::SNIPPET),
                    detail: detail.to_string(),
                    deprecated: member.symbol.flags.has(Flags::DEPRECATED),
                    ..Default::default()
                };
                items.insert(go_to_lower(&label), item);
            }
        }
        if items.is_empty() {
            return None;
        }
        let mut keyed: Vec<(String, protocol::CompletionItem)> = items.into_iter().collect();
        keyed.sort_by(|left, right| left.0.cmp(&right.0));
        Some(keyed.into_iter().map(|(_, item)| item).collect())
    }

    fn typed_twig_root_types(&self, request: &CompletionRequest) -> HashMap<String, Type> {
        let mut result = request
            .root
            .as_ref()
            .map(swlsp_twig::twig_type_annotations)
            .unwrap_or_default();
        let mut controller_types: Vec<(String, Vec<Type>)> = Vec::new();
        for variable in self.template_variables(&request.params.text_document.uri) {
            if variable.name.is_empty() || result.contains_key(&variable.name) {
                continue;
            }
            if let Ok(value) = swlsp_php_types::parse(&variable.ty)
                && !value.is_unknown()
            {
                match controller_types
                    .iter_mut()
                    .find(|(name, _)| *name == variable.name)
                {
                    Some((_, candidates)) => candidates.push(value),
                    None => controller_types.push((variable.name.clone(), vec![value])),
                }
            }
        }
        for (name, mut candidates) in controller_types {
            match candidates.len() {
                0 => {}
                1 => {
                    result.insert(name, candidates.remove(0));
                }
                _ => {
                    result.insert(name, Type::union(candidates));
                }
            }
        }
        if let Some(index) = &self.twig_indexer {
            for global in effective_twig_globals(index.get_all_globals().unwrap_or_default()) {
                if global.name.is_empty() || result.contains_key(&global.name) {
                    continue;
                }
                if let Ok(value) = swlsp_php_types::parse(&global.ty)
                    && !value.is_unknown()
                {
                    result.insert(global.name, value);
                }
            }
        }
        result
    }
}

fn twig_statement_member_name(symbol: &Symbol) -> Option<String> {
    if symbol.visibility != Visibility::Public || symbol.flags.has(Flags::STATIC) {
        return None;
    }
    match symbol.kind {
        SymbolKind::Property => {
            let name = symbol.name.strip_prefix('$').unwrap_or(&symbol.name);
            (!name.is_empty()).then(|| name.to_string())
        }
        SymbolKind::Method => {
            if go_to_lower(&symbol.name).starts_with("set") || symbol.name.starts_with("__") {
                return None;
            }
            if symbol
                .parameters
                .iter()
                .any(|parameter| !parameter.optional && !parameter.flags.has(Flags::VARIADIC))
            {
                return None;
            }
            let attribute = swlsp_twig::twig_attribute_name(&symbol.name);
            if !attribute.is_empty() {
                return Some(attribute);
            }
            (!symbol.name.is_empty()).then(|| format!("{}()", symbol.name))
        }
        _ => None,
    }
}

fn twig_statement_boolean_type(value: &Type) -> bool {
    match value.kind() {
        TypeKind::Bool | TypeKind::True | TypeKind::False => true,
        TypeKind::Union | TypeKind::Intersection => {
            let mut found_boolean = false;
            for member in value.arguments() {
                if member.kind() == TypeKind::Null {
                    continue;
                }
                if !twig_statement_boolean_type(member) {
                    return false;
                }
                found_boolean = true;
            }
            found_boolean
        }
        _ => false,
    }
}

fn singular_twig_variable(name: &str) -> String {
    let name = name.strip_suffix("()").unwrap_or(name);
    let lower = go_to_lower(name);
    let bytes = name.as_bytes();
    let cut = |end: usize| String::from_utf8_lossy(&bytes[..end]).into_owned();
    if lower.ends_with("ies") && name.len() > 3 {
        return cut(name.len() - 3) + "y";
    }
    if lower.ends_with("sses") && name.len() > 4 {
        return cut(name.len() - 2);
    }
    if lower.ends_with('s') && !lower.ends_with("ss") && name.len() > 1 {
        return cut(name.len() - 1);
    }
    "item".to_string()
}
