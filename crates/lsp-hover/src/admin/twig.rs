//! Administration hover in Twig templates and Vue `<template>` sections.

use std::fmt::Write as _;

use swlsp_admin::{AdminSymbolKind, VueComponent, VueDynamicComponentCandidate};
use swlsp_lsp::HoverRequest;
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_VAR;
use swlsp_syntax::{SyntaxNode, TextRange};
use swlsp_uriutil::filepath;

use super::{AdminHoverProvider, HoverResult, admin_hover_range, admin_template_path};
use crate::{markdown_hover, request_byte_range, request_offset};

impl AdminHoverProvider {
    pub(crate) fn twig_hover(&self, request: &HoverRequest, node: &SyntaxNode) -> HoverResult {
        let template_path = admin_template_path(request);
        let live_owner = self
            .index
            .get_component_for_document(
                &template_path,
                request.root.as_ref(),
                request.source(),
                request.line_index(),
            )
            .ok()
            .flatten();
        let owner = live_owner.as_ref();
        if let Some(token) = request.token.as_ref()
            && let Some(block_name) = swlsp_admin::twig_block_name_at(node, token)
        {
            return Ok(self.parent_block_hover(
                request,
                &template_path,
                &block_name,
                token.text_range(),
            ));
        }
        let mut vue_expression = false;
        if let (Some(root), Some(offset)) = (request.root.as_ref(), request_offset(request)) {
            if let Some(directive) = swlsp_admin::twig_directive_at_offset(root, offset) {
                let mut hover =
                    self.directive_hover_for_template(&directive.name, &template_path)?;
                if let Some(hover) = hover.as_mut() {
                    hover.range = admin_hover_range(request, directive.range);
                }
                return Ok(hover);
            }
            if let Some(reference) = swlsp_admin::twig_registry_reference_at_offset(root, offset)
                && !reference.name.is_empty()
            {
                match reference.kind {
                    AdminSymbolKind::Privilege => return self.privilege_hover(&reference.name),
                    AdminSymbolKind::ModuleRoute => {
                        return self.module_route_hover(&reference.name);
                    }
                    _ => {}
                }
            }
            if let Some(candidate) = admin_dynamic_component_candidate_at(node, offset) {
                return Ok(self.dynamic_component_hover(
                    request,
                    &candidate,
                    &template_path,
                    owner,
                ));
            }
            if let Some((start_tag, field)) =
                swlsp_admin::twig_component_object_binding_field_at_offset(root, offset)
            {
                return self.component_prop_hover_by_name(
                    Some(&start_tag),
                    &swlsp_admin::normalize_prop_name(&field.name),
                    &template_path,
                    owner,
                );
            }
            let (member_hover, handled) =
                self.twig_vue_member_hover(request, root, node, offset)?;
            if handled {
                return Ok(member_hover);
            }
            let content = request.source();
            let resolved_slot = self.index.resolve_twig_scoped_slot_binding_for_owner(
                root,
                node,
                content,
                offset,
                &template_path,
                owner,
            )?;
            let resolved_vue = self.index.resolve_twig_vue_binding_for_component(
                root,
                content,
                offset,
                &template_path,
                owner,
            )?;
            if let Some(resolved_vue) = resolved_vue.as_ref()
                && resolved_slot.as_ref().is_none_or(|slot| {
                    resolved_vue.scope_range.len() <= slot.scoped.scope.template_range.len()
                })
            {
                return Ok(Some(self.twig_vue_binding_hover(resolved_vue)));
            }
            if let Some(resolved_slot) = resolved_slot.as_ref() {
                return Ok(Some(self.scoped_slot_binding_hover(resolved_slot)));
            }
            vue_expression = swlsp_admin::is_twig_vue_expression_at(node, offset);
        }
        if twigquery::closest_node_of_kind(node, &[TWIG_VAR]).is_some() || vue_expression {
            return self.template_member_hover(request, node);
        }
        if let Some(attribute) = twigquery::html_attribute_at(node) {
            let attribute_name = twigquery::html_attribute_name(&attribute).unwrap_or_default();
            if swlsp_admin::normalize_model_argument(&attribute_name).is_some() {
                return self.component_model_hover(&attribute, &template_path, owner);
            }
            if attribute_name.starts_with('#')
                || attribute_name.starts_with("v-slot:")
                || attribute_name == "v-slot"
            {
                return self.component_slot_hover(&attribute, &template_path, owner);
            }
            if !swlsp_admin::normalize_event_name(&attribute_name).is_empty() {
                return self.component_event_hover(&attribute, &template_path, owner);
            }
            return self.component_prop_hover(&attribute, &template_path, owner);
        }
        let Some(start_tag) = twigquery::starting_html_tag_at(node) else {
            return Ok(None);
        };
        let Some(token) = request.token.as_ref() else {
            return Ok(None);
        };
        let component_name = twigquery::html_tag_name(&start_tag).unwrap_or_default();
        if component_name != token.text() || component_name.is_empty() {
            return Ok(None);
        }
        let component =
            match self
                .index
                .get_component_for_template_tag(&template_path, &component_name, owner)
            {
                Ok(Some(component)) => component,
                _ => return Ok(None),
            };
        Ok(Some(markdown_hover(
            self.build_hover_content(std::slice::from_ref(&component)),
            request_byte_range(request, Some(token.text_range())),
        )))
    }

    fn parent_block_hover(
        &self,
        request: &HoverRequest,
        template_path: &str,
        block_name: &str,
        range: TextRange,
    ) -> Option<protocol::Hover> {
        if template_path.is_empty() || block_name.is_empty() {
            return None;
        }
        let parent = self
            .index
            .get_parent_component_for_template(template_path)
            .ok()
            .flatten()?;
        let block = parent.component_block(block_name)?;
        let mut markdown = format!("**Administration Twig block:** `{}`\n\n", block.name);
        let _ = write!(markdown, "**Parent component:** `{}`", parent.name);
        if !block.file_path.is_empty() {
            let _ = write!(
                markdown,
                "\n\n**Source:** `{}:{}`",
                filepath::to_slash(&block.file_path),
                block.line
            );
        }
        if !block.deprecated.is_empty() {
            let _ = write!(markdown, "\n\n**Deprecated:** {}", block.deprecated);
        }
        Some(markdown_hover(markdown, admin_hover_range(request, range)))
    }

    fn dynamic_component_hover(
        &self,
        request: &HoverRequest,
        candidate: &VueDynamicComponentCandidate,
        template_path: &str,
        owner: Option<&VueComponent>,
    ) -> Option<protocol::Hover> {
        let component = self
            .index
            .get_component_for_template_tag(template_path, &candidate.name, owner)
            .ok()
            .flatten()?;
        Some(markdown_hover(
            format!(
                "**Vue dynamic component selector**\n\n{}",
                self.build_hover_content(std::slice::from_ref(&component))
            ),
            admin_hover_range(request, candidate.range),
        ))
    }
}

fn admin_dynamic_component_candidate_at(
    node: &SyntaxNode,
    offset: u32,
) -> Option<VueDynamicComponentCandidate> {
    let start_tag = twigquery::starting_html_tag_at(node)?;
    let selector = swlsp_admin::twig_dynamic_component_selector(&start_tag)?;
    selector.candidate_at(offset).cloned()
}
