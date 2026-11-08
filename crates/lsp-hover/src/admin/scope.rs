//! Administration template scope hovers: Vue member chains, `v-for` and
//! event locals, scoped-slot bindings, and component template members.

use std::collections::HashSet;
use std::fmt::Write as _;

use swlsp_admin::{
    ResolvedTwigScopedSlot, ResolvedTwigSlotBinding, TwigVueBinding, TwigVueBindingKind,
    VueComponentMemberKind, VueComponentSlotMember,
};
use swlsp_lsp::HoverRequest;
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_ACCESSOR, TWIG_VAR};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, SyntaxToken};

use super::component::admin_prop_markdown;
use super::{AdminHoverProvider, HoverResult, admin_hover_range, admin_template_path};
use crate::{markdown_hover, request_offset};

impl AdminHoverProvider {
    /// Go `twigVueMemberHover`: `Ok((hover, handled))`.
    pub(crate) fn twig_vue_member_hover(
        &self,
        request: &HoverRequest,
        root: &SyntaxNode,
        node: &SyntaxNode,
        offset: u32,
    ) -> swlsp_lsp::ProviderResult<(Option<protocol::Hover>, bool)> {
        let content = request.source();
        let Some(access) = swlsp_admin::twig_vue_expression_member_at_offset(root, content, offset)
        else {
            return Ok((None, false));
        };
        if access.member.is_empty() {
            return Ok((None, false));
        }
        let template_path = admin_template_path(request);
        let live_component = self
            .index
            .get_component_for_document(&template_path, Some(root), content, request.line_index())
            .ok()
            .flatten();
        let resolved_slot = self.index.resolve_twig_scoped_slot_member_for_owner(
            root,
            node,
            content,
            offset,
            &template_path,
            live_component.as_ref(),
        )?;
        let resolved_vue = self.index.resolve_twig_vue_member_for_component(
            root,
            content,
            offset,
            &template_path,
            live_component.as_ref(),
        )?;
        let range = admin_hover_range(request, access.member_range);
        if let Some(resolved_vue) = resolved_vue.as_ref()
            && resolved_slot.as_ref().is_none_or(|slot| {
                resolved_vue.binding.scope_range.len() <= slot.scoped.scope.template_range.len()
            })
        {
            if !resolved_vue.receiver_found || !resolved_vue.member_found {
                return Ok((None, true));
            }
            let mut value = format!("**property** `{}`", access.qualified_name());
            if !resolved_vue.member.type_.is_empty() {
                let _ = write!(value, ": `{}`", resolved_vue.member.type_);
            }
            let _ = write!(
                value,
                "\n\nResolved on the lexical `{}` binding in this template.",
                resolved_vue.binding.name
            );
            if !resolved_vue.receiver_type.is_empty() {
                let label = if access.receiver.is_empty() {
                    "Binding type"
                } else {
                    "Receiver type"
                };
                let _ = write!(value, "\n\n{label}: `{}`.", resolved_vue.receiver_type);
            }
            if !resolved_vue.binding.iterable.is_empty() {
                let _ = write!(value, "\n\nIterates `{}`.", resolved_vue.binding.iterable);
            }
            return Ok((Some(markdown_hover(value, range)), true));
        }
        if let Some(resolved_slot) = resolved_slot.as_ref() {
            if !resolved_slot.member_found {
                return Ok((None, true));
            }
            let mut value = format!("**slot prop** `{}`", resolved_slot.access.qualified_name());
            if !resolved_slot.member.type_.is_empty() {
                let _ = write!(value, ": `{}`", resolved_slot.member.type_);
            }
            let _ = write!(
                value,
                "\n\nExposed by scoped slot `{}`.",
                resolved_slot.scoped.qualified_name()
            );
            value.push_str(&scoped_slot_candidate_markdown(&resolved_slot.scoped));
            value.push_str(&self.scoped_slot_member_declaration_markdown(&resolved_slot.members));
            return Ok((Some(markdown_hover(value, range)), true));
        }
        let resolved_instance = self.index.resolve_twig_vue_instance_member_for_component(
            root,
            content,
            offset,
            &template_path,
            live_component.as_ref(),
        )?;
        if let Some(instance) = resolved_instance {
            if !instance.receiver_found || !instance.member_found {
                return Ok((None, true));
            }
            let mut value = format!("**property** `{}`", instance.qualified_name());
            if !instance.member.type_.is_empty() {
                let _ = write!(value, ": `{}`", instance.member.type_);
            }
            let _ = write!(
                value,
                "\n\nResolved through **{}** `{}` on Administration component `{}`.",
                instance.root_member.kind.as_str(),
                instance.root_member.name,
                instance.component.name
            );
            if !instance.receiver_type.is_empty() {
                let _ = write!(value, "\n\nReceiver type: `{}`.", instance.receiver_type);
            }
            return Ok((Some(markdown_hover(value, range)), true));
        }
        // A direct member access whose receiver shape is unknown to the
        // Administration frontend; do not misreport it as an instance member.
        Ok((None, true))
    }

    pub(crate) fn twig_vue_binding_hover(&self, binding: &TwigVueBinding) -> protocol::Hover {
        let kind = if binding.kind == TwigVueBindingKind::Event {
            "event payload"
        } else {
            "v-for local"
        };
        let mut value = format!("**{kind}** `{}`", binding.name);
        if !binding.type_.is_empty() {
            let _ = write!(value, ": `{}`", binding.type_);
        }
        if binding.kind == TwigVueBindingKind::For && !binding.iterable.is_empty() {
            let _ = write!(value, "\n\nIterates `{}`.", binding.iterable);
        }
        if binding.kind == TwigVueBindingKind::Event {
            if !binding.component_name.is_empty() && !binding.event_name.is_empty() {
                let _ = write!(
                    value,
                    "\n\nEvent: `{}.{}`.",
                    binding.component_name, binding.event_name
                );
            }
            if !binding.definition_path.is_empty() {
                let path = self.make_relative_path(&binding.definition_path);
                if binding.definition_line > 0 {
                    let _ = write!(
                        value,
                        "\n\nDeclared in `{path}:{}`.",
                        binding.definition_line
                    );
                } else {
                    let _ = write!(value, "\n\nDeclared in `{path}`.");
                }
            }
        }
        markdown_hover(value, None)
    }

    pub(crate) fn scoped_slot_binding_hover(
        &self,
        resolved: &ResolvedTwigSlotBinding,
    ) -> protocol::Hover {
        let scoped = &resolved.scoped;
        let (kind, type_name, contract_name) = if resolved.binding.whole_object {
            ("slot payload", scoped.slot.payload_type.as_str(), "")
        } else {
            (
                "slot prop",
                resolved.member.type_.as_str(),
                resolved.binding.member_name.as_str(),
            )
        };
        let mut value = format!("**{kind}** `{}`", resolved.identifier);
        if !type_name.is_empty() {
            let _ = write!(value, ": `{type_name}`");
        }
        let _ = write!(value, "\n\nScoped slot: `{}`", scoped.qualified_name());
        value.push_str(&scoped_slot_candidate_markdown(scoped));
        if scoped.slot.is_dynamic_name() {
            let _ = write!(
                value,
                "\n\nDynamic slot family: `{}`.",
                scoped.slot.display_name()
            );
        }
        if !contract_name.is_empty() && resolved.identifier != contract_name {
            let _ = write!(value, "\n\nContract member: `{contract_name}`.");
        }
        let declarations: Vec<VueComponentSlotMember> = if resolved.binding.whole_object {
            scoped
                .contracts
                .iter()
                .map(|contract| VueComponentSlotMember {
                    name: scoped.scope.slot_name.clone(),
                    type_: contract.slot.payload_type.clone(),
                    file_path: contract.slot.file_path.clone(),
                    line: contract.slot.line,
                    ..VueComponentSlotMember::default()
                })
                .collect()
        } else {
            resolved.members.clone()
        };
        let declaration_markdown = self.scoped_slot_member_declaration_markdown(&declarations);
        if !declaration_markdown.is_empty() {
            value.push_str(&declaration_markdown);
        } else {
            let (mut file_path, mut line) = (scoped.slot.file_path.as_str(), scoped.slot.line);
            if resolved.member_found && !resolved.member.file_path.is_empty() {
                file_path = &resolved.member.file_path;
                line = resolved.member.line;
            }
            if file_path.is_empty() {
                return markdown_hover(value, None);
            }
            if line > 0 {
                let _ = write!(
                    value,
                    "\n\nDeclared in `{}:{line}`.",
                    self.make_relative_path(file_path)
                );
            } else {
                let _ = write!(
                    value,
                    "\n\nDeclared in `{}`.",
                    self.make_relative_path(file_path)
                );
            }
        }
        markdown_hover(value, None)
    }

    fn scoped_slot_member_declaration_markdown(
        &self,
        members: &[VueComponentSlotMember],
    ) -> String {
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for member in members {
            if member.file_path.is_empty() {
                continue;
            }
            if !seen.insert(format!("{}:{}", member.file_path, member.line)) {
                continue;
            }
            let mut path = self.make_relative_path(&member.file_path);
            if member.line > 0 {
                let _ = write!(path, ":{}", member.line);
            }
            result.push(format!("`{path}`"));
        }
        if result.is_empty() {
            return String::new();
        }
        let label = if result.len() > 1 {
            "Candidate declarations: "
        } else {
            "Declared in "
        };
        format!("\n\n{label}{}.", result.join(", "))
    }

    pub(crate) fn template_member_hover(
        &self,
        request: &HoverRequest,
        node: &SyntaxNode,
    ) -> HoverResult {
        let name = if twigquery::closest_node_of_kind(node, &[TWIG_VAR]).is_some() {
            admin_hover_template_root_name(node, request.token.as_ref(), request.source())
        } else if let Some(offset) = request_offset(request) {
            swlsp_admin::expression_root_identifier_at_offset(request.source(), offset)
                .map(|(name, _)| name)
                .unwrap_or_default()
        } else {
            String::new()
        };
        if name.is_empty() {
            return Ok(None);
        }
        let Ok(path) = swlsp_uriutil::path(crate::request_uri(request)) else {
            return Ok(None);
        };
        let Some(component) = self.index.get_component_for_document(
            &path,
            request.root.as_ref(),
            request.source(),
            request.line_index(),
        )?
        else {
            return Ok(None);
        };
        if let Some((scope_member, block)) =
            swlsp_admin::twig_block_scope_member_at(&component, node, &name)
        {
            let mut value = format!("**Twig block scope** `{}`", scope_member.name);
            if !scope_member.type_.is_empty() {
                let _ = write!(value, ": `{}`", scope_member.type_);
            }
            let _ = write!(
                value,
                "\n\nProvided by Administration Twig block `{}`.",
                block.name
            );
            if !scope_member.file_path.is_empty() {
                let _ = write!(
                    value,
                    "\n\nDeclared in `{}:{}`.",
                    self.make_relative_path(&scope_member.file_path),
                    scope_member.line
                );
            }
            return Ok(Some(markdown_hover(value, None)));
        }
        let mut origin = format!("Administration component `{}`", component.name);
        let component_member = component.template_member(&name);
        let is_component_member = component_member.is_some();
        let member = match component_member {
            Some(member) => member,
            None => {
                if let Some(builtin) = swlsp_admin::vue_builtin_member(&name) {
                    origin = "the Administration Vue component instance".to_owned();
                    builtin
                } else if let Some(global) = swlsp_admin::vue_template_global(&name) {
                    origin = "the JavaScript template runtime".to_owned();
                    global
                } else {
                    return Ok(None);
                }
            }
        };
        let mut value = format!("**{}** `{}`", member.kind.as_str(), member.name);
        if !member.type_.is_empty() {
            let _ = write!(value, ": `{}`", member.type_);
        }
        let _ = write!(value, "\n\nProvided by {origin}.");
        if is_component_member && member.kind == VueComponentMemberKind::Prop {
            if let Some(prop) = component.props.iter().find(|prop| prop.name == member.name) {
                value = admin_prop_markdown(&component.name, prop);
            }
        } else if !member.deprecated.is_empty() {
            let _ = write!(value, "\n\n**Deprecated:** {}", member.deprecated);
        }
        Ok(Some(markdown_hover(value, None)))
    }
}

pub(crate) fn scoped_slot_candidate_markdown(resolved: &ResolvedTwigScopedSlot) -> String {
    let names = resolved.component_names();
    if names.len() <= 1 {
        return String::new();
    }
    format!(
        "\n\nDynamic component candidates: `{}`.",
        names.join("`, `")
    )
}

fn admin_hover_template_root_name(
    node: &SyntaxNode,
    token: Option<&SyntaxToken>,
    content: &str,
) -> String {
    let Some(token) = token else {
        return String::new();
    };
    if let Some(accessor) = twigquery::closest_node_of_kind(node, &[TWIG_ACCESSOR]) {
        let start = usize::from(accessor.range_trimmed_trivia().start());
        let end = usize::from(token.text_range().start());
        if start < end && end <= content.len() && content.as_bytes()[start..end].contains(&b'.') {
            return String::new();
        }
    }
    token.text().trim().to_owned()
}
