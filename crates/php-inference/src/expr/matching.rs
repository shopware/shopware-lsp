//! `match` expressions.

use swlsp_parser_php::syntax::*;
use swlsp_php_types::Type;
use swlsp_syntax::SyntaxNode;

use crate::analyzer::Fs;
use crate::env::Env;
use crate::syntax::{direct_nodes, first_node, is_true_condition, match_arm_is_default};
use crate::types_util::join_types;

impl Fs<'_, '_, '_> {
    pub(crate) fn infer_match(&mut self, node: &SyntaxNode, env: &Env) -> Type {
        let mut results = Vec::new();
        let children = direct_nodes(node);
        let mut match_true = false;
        let mut selector: Option<SyntaxNode> = None;
        if let Some(first) = children.first()
            && first.kind() != PHP_MATCH_ARM
        {
            self.infer(first, env);
            match_true = is_true_condition(self.src(), first);
            selector = if first.kind() == PHP_PARENTHESIZED {
                first_node(first)
            } else {
                Some(first.clone())
            };
        }
        let mut remaining = env.fork();
        for arm in &children {
            if arm.kind() != PHP_MATCH_ARM {
                continue;
            }
            let arm_nodes = direct_nodes(arm);
            let Some(result_node) = arm_nodes.last() else {
                continue;
            };
            let mut arm_env = env.clone();
            if match_true || selector.is_some() {
                if match_arm_is_default(arm) {
                    arm_env = remaining.clone();
                } else {
                    let mut has_condition = false;
                    for condition in &arm_nodes[..arm_nodes.len() - 1] {
                        let (matched, unmatched) = if match_true {
                            self.condition_environments(Some(condition), &remaining)
                        } else {
                            let selector = selector.clone().unwrap_or_else(|| condition.clone());
                            self.match_condition_environments(&selector, condition, &remaining)
                        };
                        if has_condition {
                            arm_env = self.join(&arm_env, &matched);
                        } else {
                            arm_env = matched;
                            has_condition = true;
                        }
                        remaining = unmatched;
                    }
                }
            }
            results.push(self.infer(result_node, &arm_env));
        }
        join_types(self.relations, &results)
    }

    fn match_condition_environments(
        &mut self,
        selector: &SyntaxNode,
        condition: &SyntaxNode,
        remaining: &Env,
    ) -> (Env, Env) {
        if let Some(narrowed) =
            self.narrow_class_identity(Some(selector), Some(condition), "===", remaining)
        {
            return narrowed;
        }
        if let Some(constraint) = self.condition_literal_type(Some(condition), remaining) {
            return self.narrow_literal(Some(selector), &constraint, "===", remaining);
        }
        self.infer(condition, remaining);
        (remaining.fork(), remaining.fork())
    }
}
