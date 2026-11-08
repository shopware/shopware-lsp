//! Port of Go `admin_completion_scope.go`.

use std::collections::{HashMap, HashSet};

use swlsp_admin::{
    AdminTwigRegistryReference, ResolvedTwigScopedSlot, TwigVueBinding, TwigVueBindingKind,
    VueComponentMember, VueComponentMemberKind, is_twig_vue_expression_at,
    twig_registry_reference_at_offset, twig_vue_binding_member_references,
    twig_vue_expression_member_at_offset, vue_builtin_members, vue_template_globals,
};
use swlsp_lsp::{CompletionRequest, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_syntax::{SyntaxNode, TextRange};

use super::AdminCompletionProvider;
use super::components::{
    append_documentation, item, mark_admin_completion_deprecated, set_markdown, snippet,
};
use crate::common::sort_completion_items;

fn range_len(range: TextRange) -> u32 {
    u32::from(range.len())
}

impl AdminCompletionProvider {
    /// Go `twigVueMemberCompletionsAt`; the flag reports whether the member
    /// context was handled.
    pub(super) fn twig_vue_member_completions_at(
        &self,
        request: &CompletionRequest,
        slot: Option<&ResolvedTwigScopedSlot>,
        offset: u32,
    ) -> (Vec<protocol::CompletionItem>, bool) {
        let (Some(index), Some(root)) = (self.index.as_ref(), request.root.as_ref()) else {
            return (Vec::new(), false);
        };
        let content = request.source();
        let Some(access) = twig_vue_expression_member_at_offset(root, content, offset) else {
            return (Vec::new(), false);
        };
        let slot_owns_root = slot.is_some_and(|slot| {
            slot.scope
                .bindings
                .iter()
                .any(|binding| binding.whole_object && binding.local_name == access.root)
        });
        let template_path = admin_template_path(&request.params.text_document.uri);
        let live_component = index
            .get_component_for_document(&template_path, Some(root), content, request.line_index())
            .ok()
            .flatten();
        let Ok(resolved_vue) = index.resolve_twig_vue_member_for_component(
            root,
            content,
            offset,
            &template_path,
            live_component.as_ref(),
        ) else {
            return (Vec::new(), true);
        };
        if let Some(resolved_vue) = resolved_vue
            && (!slot_owns_root
                || slot.is_none_or(|slot| {
                    range_len(resolved_vue.binding.scope_range)
                        <= range_len(slot.scope.template_range)
                }))
        {
            if !resolved_vue.receiver_found {
                return (Vec::new(), true);
            }
            let mut items = Vec::with_capacity(resolved_vue.receiver_members.len());
            for member in &resolved_vue.receiver_members {
                if access.receiver.is_empty()
                    && member.range == access.member_range
                    && offset >= u32::from(member.range.end())
                    && twig_vue_binding_member_references(
                        root,
                        content,
                        &resolved_vue.binding,
                        &member.name,
                    )
                    .len()
                        == 1
                {
                    continue;
                }
                let kind = if resolved_vue.members_complete
                    || !member.type_.is_empty()
                    || !member.definition_path.is_empty()
                {
                    "typed member"
                } else {
                    "observed member"
                };
                let mut detail = format!("{kind} · {}", resolved_vue.binding.name);
                if !resolved_vue.receiver_type.is_empty() {
                    detail += " · ";
                    detail += &resolved_vue.receiver_type;
                }
                if !member.type_.is_empty() {
                    detail += " · ";
                    detail += &member.type_;
                }
                let mut completion =
                    item(&member.name, protocol::CompletionItemKind::PROPERTY, detail);
                set_markdown(
                    &mut completion,
                    format!(
                        "Available on the lexical `{}` binding in this template.",
                        resolved_vue.binding.name
                    ),
                );
                items.push(completion);
            }
            sort_completion_items(&mut items);
            return (items, true);
        }
        if slot_owns_root {
            let Some(node) = request.node.as_ref() else {
                return (Vec::new(), true);
            };
            let resolved_member = index.resolve_twig_scoped_slot_member_for_owner(
                root,
                node,
                content,
                offset,
                &template_path,
                live_component.as_ref(),
            );
            let Ok(Some(resolved_member)) = resolved_member else {
                return (Vec::new(), true);
            };
            if !resolved_member.receiver_found {
                return (Vec::new(), true);
            }
            if access.receiver.is_empty() {
                if let Some(slot) = slot {
                    return (scoped_slot_contract_completions(slot), true);
                }
                return (Vec::new(), true);
            }
            let qualified = resolved_member.scoped.qualified_name();
            let mut items = Vec::with_capacity(resolved_member.receiver_members.len());
            for member in &resolved_member.receiver_members {
                let mut detail = format!("slot member · {qualified}");
                if !resolved_member.receiver_type.is_empty() {
                    detail += " · ";
                    detail += &resolved_member.receiver_type;
                }
                if !member.type_.is_empty() {
                    detail += " · ";
                    detail += &member.type_;
                }
                items.push(item(
                    &member.name,
                    protocol::CompletionItemKind::PROPERTY,
                    detail,
                ));
            }
            sort_completion_items(&mut items);
            return (items, true);
        }
        let Ok(resolved_instance) = index.resolve_twig_vue_instance_member_for_component(
            root,
            content,
            offset,
            &template_path,
            live_component.as_ref(),
        ) else {
            return (Vec::new(), true);
        };
        if let Some(resolved_instance) = resolved_instance {
            if !resolved_instance.receiver_found {
                return (Vec::new(), true);
            }
            let mut items = Vec::with_capacity(resolved_instance.receiver_members.len());
            for member in &resolved_instance.receiver_members {
                let kind = if member.type_.contains("=>") {
                    protocol::CompletionItemKind::METHOD
                } else {
                    protocol::CompletionItemKind::PROPERTY
                };
                let mut detail = format!("component member · {}", resolved_instance.access.root);
                if !resolved_instance.receiver_type.is_empty() {
                    detail += " · ";
                    detail += &resolved_instance.receiver_type;
                }
                if !member.type_.is_empty() {
                    detail += " · ";
                    detail += &member.type_;
                }
                let mut completion = item(&member.name, kind, detail);
                set_markdown(
                    &mut completion,
                    format!(
                        "Available through `{}` on Administration component `{}`.",
                        resolved_instance.qualified_name(),
                        resolved_instance.component.name
                    ),
                );
                items.push(completion);
            }
            sort_completion_items(&mut items);
            return (items, true);
        }
        // A direct member expression never falls back to unrelated Vue
        // instance members.
        (Vec::new(), true)
    }

    /// Go `twigVueBindingsAtCompletion`.
    pub(super) fn twig_vue_bindings_at_completion(
        &self,
        request: &CompletionRequest,
        offset: Option<u32>,
    ) -> Vec<TwigVueBinding> {
        let (Some(index), Some(root), Some(offset)) =
            (self.index.as_ref(), request.root.as_ref(), offset)
        else {
            return Vec::new();
        };
        let template_path = admin_template_path(&request.params.text_document.uri);
        let content = request.source();
        let live_component = index
            .get_component_for_document(&template_path, Some(root), content, request.line_index())
            .ok()
            .flatten();
        index
            .resolve_twig_vue_bindings_for_component(
                root,
                content,
                offset,
                &template_path,
                live_component.as_ref(),
            )
            .unwrap_or_default()
    }

    /// Go `scopedSlotsAtCompletion`.
    pub(super) fn scoped_slots_at_completion(
        &self,
        request: &CompletionRequest,
    ) -> Vec<ResolvedTwigScopedSlot> {
        let (Some(index), Some(root), Some(offset)) = (
            self.index.as_ref(),
            request.root.as_ref(),
            admin_completion_offset(request),
        ) else {
            return Vec::new();
        };
        let template_path = admin_template_path(&request.params.text_document.uri);
        let live_owner = index
            .get_component_for_document(
                &template_path,
                Some(root),
                request.source(),
                request.line_index(),
            )
            .ok()
            .flatten();
        index
            .resolve_twig_scoped_slots_for_owner(root, offset, &template_path, live_owner.as_ref())
            .unwrap_or_default()
    }

    /// Go `getTemplateMemberCompletions`.
    #[cfg(test)]
    pub(super) fn get_template_member_completions(
        &self,
        uri: &str,
    ) -> Vec<protocol::CompletionItem> {
        self.get_template_member_completions_at(uri, None, None)
    }

    /// Go `getTemplateMemberCompletionsAt`.
    pub(super) fn get_template_member_completions_at(
        &self,
        uri: &str,
        node: Option<&SyntaxNode>,
        document: Option<&TextDocument>,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return Vec::new();
        };
        let component = match document.and_then(|document| Some((document, document.root()?))) {
            Some((document, root)) => index.get_component_for_document(
                &path,
                Some(&root),
                document.source(),
                Some(document.line_index()),
            ),
            None => index.get_component_by_template_path(&path),
        };
        let Ok(Some(component)) = component else {
            return Vec::new();
        };
        let mut items_by_name: HashMap<String, protocol::CompletionItem> = HashMap::new();
        for member in component.template_members() {
            let mut completion = item(
                &member.name,
                admin_member_completion_kind(member.kind),
                admin_member_detail(&member),
            );
            if member.kind == VueComponentMemberKind::Method {
                snippet(&mut completion, format!("{}($0)", member.name));
            }
            set_markdown(
                &mut completion,
                format!("Provided by Administration component `{}`.", component.name),
            );
            mark_admin_completion_deprecated(&mut completion, &member.deprecated);
            if member.kind == VueComponentMemberKind::Prop
                && !completion.deprecated
                && let Some(prop) = component.component_prop(&member.name)
            {
                mark_admin_completion_deprecated(&mut completion, &prop.deprecated);
            }
            items_by_name.insert(member.name.clone(), completion);
        }
        let mut add_runtime = |member: VueComponentMember, detail: &str, documentation: &str| {
            if member.name.is_empty() || items_by_name.contains_key(&member.name) {
                return;
            }
            let mut completion = item(
                &member.name,
                admin_member_completion_kind(member.kind),
                detail,
            );
            if member.kind == VueComponentMemberKind::Method {
                snippet(&mut completion, format!("{}($0)", member.name));
            }
            set_markdown(&mut completion, documentation);
            if !member.type_.is_empty() {
                append_documentation(&mut completion, &format!("\n\nType: `{}`.", member.type_));
            }
            items_by_name.insert(member.name.clone(), completion);
        };
        for member in vue_builtin_members() {
            add_runtime(
                member,
                "Vue template instance member",
                "Available on the Administration Vue component instance.",
            );
        }
        for global in vue_template_globals() {
            add_runtime(
                global,
                "JavaScript template global",
                "Available in Administration Vue template expressions.",
            );
        }
        if let Some(node) = node
            && let Some(block_name) = twigquery::block_at(node)
                .as_ref()
                .and_then(twigquery::block_name)
            && !block_name.is_empty()
            && let Some(block) = component.component_block(&block_name)
        {
            for member in &block.scope_members {
                if member.name.is_empty() {
                    continue;
                }
                let mut completion = item(
                    &member.name,
                    protocol::CompletionItemKind::VARIABLE,
                    format!("Twig block scope • {}", block.name),
                );
                set_markdown(
                    &mut completion,
                    format!("Provided by Administration Twig block `{}`.", block.name),
                );
                if !member.type_.is_empty() {
                    append_documentation(
                        &mut completion,
                        &format!("\n\nType: `{}`.", member.type_),
                    );
                }
                // Lexical block inputs shadow equally named component members.
                items_by_name.insert(member.name.clone(), completion);
            }
        }
        let mut items: Vec<_> = items_by_name.into_values().collect();
        sort_completion_items(&mut items);
        items
    }
}

/// Go `adminCompletionOffset`: the byte offset of the request position.
pub(super) fn admin_completion_offset(request: &CompletionRequest) -> Option<u32> {
    let line_index = request.line_index()?;
    Some(line_index.offset_utf16(
        request.params.position.line,
        request.params.position.character,
    ))
}

/// Go `adminTemplatePath`: the file path of `uri`, empty when invalid.
pub(super) fn admin_template_path(uri: &str) -> String {
    swlsp_uriutil::path(uri).unwrap_or_default()
}

/// Go `adminTwigVueExpressionAtCompletion`.
pub(super) fn admin_twig_vue_expression_at_completion(request: &CompletionRequest) -> bool {
    let (Some(node), Some(offset)) = (request.node.as_ref(), admin_completion_offset(request))
    else {
        return false;
    };
    is_twig_vue_expression_at(node, offset)
}

/// Go `scopedSlotContractCompletions`.
pub(super) fn scoped_slot_contract_completions(
    resolved: &ResolvedTwigScopedSlot,
) -> Vec<protocol::CompletionItem> {
    let qualified = resolved.qualified_name();
    let mut items = Vec::with_capacity(resolved.slot.members.len());
    for member in &resolved.slot.members {
        let mut detail = format!("slot prop · {qualified}");
        if !member.type_.is_empty() {
            detail += " · ";
            detail += &member.type_;
        }
        let mut completion = item(&member.name, protocol::CompletionItemKind::PROPERTY, detail);
        set_markdown(
            &mut completion,
            format!("Exposed by scoped slot `{qualified}`."),
        );
        if resolved.slot.is_dynamic_name() {
            append_documentation(
                &mut completion,
                &format!(
                    "\n\nProvided by dynamic family `{}`.",
                    resolved.slot.display_name()
                ),
            );
        }
        items.push(completion);
    }
    sort_completion_items(&mut items);
    items
}

fn scoped_slot_local_completions(
    resolved: &ResolvedTwigScopedSlot,
) -> Vec<protocol::CompletionItem> {
    let qualified = resolved.qualified_name();
    let mut items = Vec::with_capacity(resolved.scope.bindings.len());
    let mut seen = HashSet::new();
    for binding in &resolved.scope.bindings {
        if binding.local_name.is_empty() || !seen.insert(binding.local_name.as_str()) {
            continue;
        }
        let mut detail = format!("slot local · {qualified}");
        let member_type = if binding.whole_object {
            resolved.slot.payload_type.as_str()
        } else {
            resolved
                .slot
                .member(&binding.member_name)
                .map_or("", |member| member.type_.as_str())
        };
        if !member_type.is_empty() {
            detail += " · ";
            detail += member_type;
        }
        let mut completion = item(
            &binding.local_name,
            protocol::CompletionItemKind::VARIABLE,
            detail,
        );
        set_markdown(
            &mut completion,
            format!("Lexically bound by scoped slot `{qualified}`."),
        );
        if resolved.slot.is_dynamic_name() {
            append_documentation(
                &mut completion,
                &format!(
                    "\n\nProvided by dynamic family `{}`.",
                    resolved.slot.display_name()
                ),
            );
        }
        items.push(completion);
    }
    sort_completion_items(&mut items);
    items
}

fn twig_vue_binding_completions(bindings: &[&TwigVueBinding]) -> Vec<protocol::CompletionItem> {
    let mut items = Vec::with_capacity(bindings.len());
    for binding in bindings {
        if binding.name.is_empty() {
            continue;
        }
        let mut detail = String::from("v-for local");
        let mut documentation = "Introduced by the enclosing `v-for` expression.";
        if binding.kind == TwigVueBindingKind::Event {
            detail = String::from("event payload");
            if !binding.component_name.is_empty() && !binding.event_name.is_empty() {
                detail += &format!(" · {}.{}", binding.component_name, binding.event_name);
            }
            documentation = "Implicit payload of the current Vue event handler.";
        } else if !binding.iterable.is_empty() {
            detail += " · ";
            detail += &binding.iterable;
        }
        if !binding.type_.is_empty() {
            detail += " · ";
            detail += &binding.type_;
        }
        let mut completion = item(
            &binding.name,
            protocol::CompletionItemKind::VARIABLE,
            detail,
        );
        set_markdown(&mut completion, documentation);
        items.push(completion);
    }
    sort_completion_items(&mut items);
    items
}

/// Go `mergeAdminLexicalCompletions`: Vue's lexical shadowing. Outer
/// component members come first, then increasingly smaller v-for, event and
/// v-slot scopes, so the innermost binding owns a duplicate label.
///
/// Go groups bindings through maps and orders equally sized scopes by map
/// iteration (random); Rust keeps the binding order for those ties.
pub(super) fn merge_admin_lexical_completions(
    base: Vec<protocol::CompletionItem>,
    slots: &[ResolvedTwigScopedSlot],
    bindings: &[TwigVueBinding],
) -> Vec<protocol::CompletionItem> {
    let mut layers: Vec<(u32, Vec<protocol::CompletionItem>)> = slots
        .iter()
        .map(|slot| {
            (
                range_len(slot.scope.template_range),
                scoped_slot_local_completions(slot),
            )
        })
        .collect();
    let mut groups: Vec<((TwigVueBindingKind, TextRange), Vec<&TwigVueBinding>)> = Vec::new();
    for binding in bindings {
        let key = (binding.kind, binding.scope_range);
        match groups.iter_mut().find(|(existing, _)| *existing == key) {
            Some((_, values)) => values.push(binding),
            None => groups.push((key, vec![binding])),
        }
    }
    for ((_, scope), values) in &groups {
        layers.push((range_len(*scope), twig_vue_binding_completions(values)));
    }
    layers.sort_by_key(|layer| std::cmp::Reverse(layer.0));
    let mut result = base;
    for (_, items) in layers {
        result = merge_admin_completions(result, items);
    }
    result
}

/// Go `mergeAdminCompletions`: overlays `right` by label.
pub(super) fn merge_admin_completions(
    left: Vec<protocol::CompletionItem>,
    right: Vec<protocol::CompletionItem>,
) -> Vec<protocol::CompletionItem> {
    let mut by_label: HashMap<String, protocol::CompletionItem> =
        HashMap::with_capacity(left.len() + right.len());
    for item in left.into_iter().chain(right) {
        by_label.insert(item.label.clone(), item);
    }
    let mut items: Vec<_> = by_label.into_values().collect();
    sort_completion_items(&mut items);
    items
}

/// Go `adminTwigRegistryReferenceAtCompletion`.
pub(super) fn admin_twig_registry_reference_at_completion(
    request: &CompletionRequest,
) -> Option<AdminTwigRegistryReference> {
    let root = request.root.as_ref()?;
    let offset = admin_completion_offset(request)?;
    twig_registry_reference_at_offset(root, offset)
}

/// Go `adminMemberCompletionKind`.
pub(super) fn admin_member_completion_kind(
    kind: VueComponentMemberKind,
) -> protocol::CompletionItemKind {
    match kind {
        VueComponentMemberKind::Method => protocol::CompletionItemKind::METHOD,
        VueComponentMemberKind::Computed => protocol::CompletionItemKind::PROPERTY,
        VueComponentMemberKind::Inject => protocol::CompletionItemKind::REFERENCE,
        _ => protocol::CompletionItemKind::VARIABLE,
    }
}

/// Go `adminMemberDetail`.
pub(super) fn admin_member_detail(member: &VueComponentMember) -> String {
    let mut detail = member.kind.as_str().to_owned();
    if !member.type_.is_empty() {
        detail += " • ";
        detail += &member.type_;
    }
    detail
}
