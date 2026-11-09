//! Administration completions in Twig templates.

use swlsp_admin::{
    AdminSymbolKind, is_twig_block_name_completion_at, is_twig_vue_expression_at,
    twig_component_object_binding_key_context_at_offset,
};
use swlsp_lsp::CompletionRequest;
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax as twigsyntax;

use super::AdminCompletionProvider;
use super::components::{
    append_documentation, item, mark_admin_completion_deprecated, set_markdown,
};
use super::scope::{
    admin_completion_offset, admin_template_path, admin_twig_registry_reference_at_completion,
    admin_twig_vue_expression_at_completion, merge_admin_completions,
    merge_admin_lexical_completions, scoped_slot_contract_completions,
};
use super::values::admin_dynamic_component_selector_at_completion;
use crate::common::sort_completion_items;

impl AdminCompletionProvider {
    /// Completions in Administration Twig templates.
    pub(super) fn twig_completions(
        &self,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        let node = request.node.as_ref();
        let content = request.source();
        let offset = admin_completion_offset(request);
        let uri = request.params.text_document.uri.as_str();
        let template_path = admin_template_path(uri);
        let live_owner = index
            .get_component_for_document(
                &template_path,
                request.root.as_ref(),
                content,
                request.line_index(),
            )
            .ok()
            .flatten();
        let live_owner = live_owner.as_ref();
        if let (Some(node), Some(offset)) = (node, offset)
            && is_twig_block_name_completion_at(node, offset)
        {
            return self.get_parent_block_completions(&template_path);
        }

        if let Some(reference) = admin_twig_registry_reference_at_completion(request) {
            match reference.kind {
                AdminSymbolKind::Privilege => return self.get_privilege_completions(),
                AdminSymbolKind::ModuleRoute => return self.get_module_route_completions(),
                _ => {}
            }
        }
        if let Some(offset) = offset
            && let Some(items) =
                self.component_prop_value_completions(request, offset, &template_path, live_owner)
        {
            return items;
        }
        if let Some(selector) = admin_dynamic_component_selector_at_completion(request, offset) {
            return self.get_dynamic_component_completions(
                &template_path,
                &selector,
                offset.unwrap_or_default(),
                live_owner,
            );
        }
        if let Some(offset) = offset
            && let Some(root) = request.root.as_ref()
            && let Some((start_tag, fields)) =
                twig_component_object_binding_key_context_at_offset(root, offset)
        {
            return self.object_binding_prop_completions(
                &start_tag,
                &fields,
                &template_path,
                live_owner,
            );
        }

        let resolved_slots = self.scoped_slots_at_completion(request);
        let resolved_slot = resolved_slots.last();
        let vue_bindings = self.twig_vue_bindings_at_completion(request, offset);
        if let (Some(node), Some(offset)) = (node, offset)
            && is_twig_vue_expression_at(node, offset)
        {
            let (members, handled) =
                self.twig_vue_member_completions_at(request, resolved_slot, offset);
            if handled {
                return members;
            }
        }
        let template_members = || {
            merge_admin_lexical_completions(
                self.get_template_member_completions_at(uri, node, request.document.as_deref()),
                &resolved_slots,
                &vue_bindings,
            )
        };
        if let Some(resolved) = resolved_slot {
            // An unavailable position is treated as offset zero.
            let raw_offset = offset.unwrap_or_default();
            if resolved.scope.is_binding_offset(raw_offset) {
                return scoped_slot_contract_completions(resolved);
            }
            if node.is_some_and(|node| is_twig_vue_expression_at(node, raw_offset)) {
                return template_members();
            }
        }

        if node
            .and_then(|node| twigquery::closest_node_of_kind(node, &[twigsyntax::TWIG_VAR]))
            .is_some()
            || admin_twig_vue_expression_at_completion(request)
        {
            return template_members();
        }

        if self.is_in_html_tag_name(node, request.token.as_ref(), content) {
            return self.get_component_tag_completions_for_owner(&template_path, live_owner);
        }

        if let Some(items) = self.get_resolved_slot_completions(node, &template_path, live_owner) {
            return items;
        }
        if let Some(items) =
            self.dynamic_component_attribute_completions(node, &template_path, live_owner)
        {
            return items;
        }

        // Custom directives are valid on native elements and components; on
        // a component they merge with its prop/event/model contract.
        if self.is_in_html_attribute_completion_position(node) {
            let directives = self.get_directive_completions(true, &template_path);
            let component_name = self.get_component_name_for_attribute_completion(node);
            if !component_name.is_empty() {
                return merge_admin_completions(
                    self.get_component_prop_completions_for_owner(
                        &component_name,
                        &template_path,
                        live_owner,
                    ),
                    directives,
                );
            }
            return directives;
        }
        Vec::new()
    }

    /// Block names of the parent templates of `template_path`.
    pub(super) fn get_parent_block_completions(
        &self,
        template_path: &str,
    ) -> Vec<protocol::CompletionItem> {
        let Some(index) = self.index.as_ref() else {
            return Vec::new();
        };
        if template_path.is_empty() {
            return Vec::new();
        }
        let Ok(Some(parent)) = index.get_parent_component_for_template(template_path) else {
            return Vec::new();
        };
        let mut items = Vec::with_capacity(parent.blocks.len());
        for block in &parent.blocks {
            if block.name.is_empty() {
                continue;
            }
            let mut completion = item(
                &block.name,
                protocol::CompletionItemKind::METHOD,
                format!("Administration Twig block · {}", parent.name),
            );
            set_markdown(
                &mut completion,
                format!(
                    "Extensibility block declared by Administration component `{}`.",
                    parent.name
                ),
            );
            if !block.file_path.is_empty() {
                append_documentation(
                    &mut completion,
                    &format!(
                        "\n\n**Source:** `{}`",
                        swlsp_uriutil::filepath::to_slash(&block.file_path)
                    ),
                );
            }
            mark_admin_completion_deprecated(&mut completion, &block.deprecated);
            items.push(completion);
        }
        sort_completion_items(&mut items);
        items
    }
}
