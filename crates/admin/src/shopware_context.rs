//! `Shopware.Context` members (port of Go `shopware_context.go`).

use crate::*;

const SHOPWARE_CONTEXT_TYPE: &str = "ContextState";

/// A statically named member access rooted at `Shopware.Context`; returns
/// the receiver path and the selected member.
pub fn javascript_shopware_context_member(node: &SyntaxNode) -> Option<(Vec<String>, String)> {
    javascript_static_member(node, "Shopware.Context")
}

/// The identifier owned by a recognized `Shopware.Context` member access.
pub fn javascript_shopware_context_member_name_node(node: &SyntaxNode) -> Option<SyntaxNode> {
    javascript_static_member_name_node(node, "Shopware.Context")
}

impl AdminComponentIndexer {
    /// The receiver contract of a static `Shopware.Context` access, resolved
    /// from the indexed `ContextState` declaration. The root stays open.
    pub fn resolve_shopware_context(
        &self,
        receiver: &str,
        context_path: &str,
    ) -> IndexResult<VueTypeShape> {
        let Some(segments) = static_expression_path(receiver) else {
            return Ok(VueTypeShape::default());
        };
        let mut shape = self.resolve_vue_type(SHOPWARE_CONTEXT_TYPE, context_path, &[])?;
        if segments.is_empty() {
            shape.complete = false;
            return Ok(shape);
        }
        let mut current_context = context_path.to_owned();
        for segment in &segments {
            let member = twig_vue_member_named(&shape.members, &segment.name);
            let Some(member) = member.filter(|member| !member.type_.is_empty()) else {
                return Ok(VueTypeShape::default());
            };
            if !member.definition_path.is_empty() {
                current_context = member.definition_path.clone();
            }
            if !member.nested_members.is_empty() || member.nested_complete {
                shape = VueTypeShape {
                    type_: member.type_.clone(),
                    members: member.nested_members.clone(),
                    complete: member.nested_complete,
                };
            } else {
                shape = self.resolve_vue_type(&member.type_, &current_context, &[])?;
            }
        }
        Ok(shape)
    }

    pub(crate) fn resolve_shopware_context_expression_type(
        &self,
        expression: &str,
        context_path: &str,
    ) -> IndexResult<Option<ResolvedVueExpressionType>> {
        let Some((segments, incomplete)) =
            javascript_static_expression(expression, "Shopware.Context")
        else {
            return Ok(None);
        };
        if incomplete {
            return Ok(None);
        }
        let names = static_expression_segment_names(&segments);
        if names.len() != segments.len() {
            return Ok(None);
        }
        let Some((last, receiver)) = names.split_last() else {
            return Ok(Some(ResolvedVueExpressionType {
                type_: SHOPWARE_CONTEXT_TYPE.to_owned(),
                context_path: context_path.to_owned(),
                ..Default::default()
            }));
        };
        let shape = self.resolve_shopware_context(&receiver.join("."), context_path)?;
        let Some(member) =
            twig_vue_member_named(&shape.members, last).filter(|member| !member.type_.is_empty())
        else {
            return Ok(None);
        };
        let resolved_context = if member.definition_path.is_empty() {
            context_path.to_owned()
        } else {
            member.definition_path.clone()
        };
        Ok(Some(ResolvedVueExpressionType {
            type_: member.type_.clone(),
            context_path: resolved_context,
            ..Default::default()
        }))
    }
}
