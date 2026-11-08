//! Exact Twig function, filter and test name occurrences (port of
//! `extension_usage.go`).

use std::collections::HashSet;

use serde::{Deserialize, Serialize};
use swlsp_parser_twig::ast::{AstNode, TwigFilter as TwigFilterNode};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::*;
use swlsp_syntax::{SyntaxNode, TextRange};

use crate::macros::{function_name_operand, literal_names};
use crate::template_variable::is_descendant_node;
use crate::test_expression::{is_test_function_call, test_expressions};

/// Persisted as its Go `uint8` value.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, PartialOrd, Ord)]
#[repr(u8)]
pub enum ExtensionUsageKind {
    #[default]
    Function = 1,
    Filter = 2,
    Test = 3,
}

impl Serialize for ExtensionUsageKind {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_u8(*self as u8)
    }
}

impl<'de> Deserialize<'de> for ExtensionUsageKind {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        Ok(match u8::deserialize(deserializer)? {
            2 => Self::Filter,
            3 => Self::Test,
            _ => Self::Function,
        })
    }
}

/// One exact Twig function, filter, or test name occurrence.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ExtensionUsage {
    pub kind: ExtensionUsageKind,
    pub name: String,
    pub file_path: String,
    pub range: TextRange,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ExtensionUsageCatalog {
    pub key: String,
    pub usages: Vec<ExtensionUsage>,
}

pub fn extension_usage_key(kind: ExtensionUsageKind, name: &str) -> String {
    let name = swlsp_php_types::go_to_lower(name.trim());
    if name.is_empty() {
        return String::new();
    }
    format!("{}\0{name}", kind as u8)
}

pub fn extension_usages_in_document(file_path: &str, root: &SyntaxNode) -> Vec<ExtensionUsage> {
    let mut result = Vec::new();
    for call in twigquery::iterate_nodes(root, &[TWIG_FUNCTION_CALL]) {
        if twig_function_call_is_filter(&call) || is_test_function_call(&call) {
            continue;
        }
        let Some(operand) = function_name_operand(&call) else {
            continue;
        };
        if twigquery::iterate_nodes(&operand, &[TWIG_ACCESSOR])
            .next()
            .is_some()
        {
            continue;
        }
        let names = literal_names(Some(&operand));
        if names.len() != 1 {
            continue;
        }
        result.push(ExtensionUsage {
            kind: ExtensionUsageKind::Function,
            name: names[0].name.clone(),
            file_path: file_path.to_owned(),
            range: names[0].range,
        });
    }
    for filter_node in twigquery::iterate_nodes(root, &[TWIG_FILTER]) {
        let Some(name) = twigquery::filter_name(&filter_node).filter(|name| !name.is_empty())
        else {
            continue;
        };
        let Some(operand) = TwigFilterNode::cast(filter_node).and_then(|filter| filter.filter())
        else {
            continue;
        };
        let name_range = literal_names(Some(operand.syntax()))
            .into_iter()
            .find(|candidate| swlsp_php_types::equal_fold(&candidate.name, &name))
            .map(|candidate| candidate.range)
            .unwrap_or_default();
        if name_range.is_empty() {
            continue;
        }
        result.push(ExtensionUsage {
            kind: ExtensionUsageKind::Filter,
            name,
            file_path: file_path.to_owned(),
            range: name_range,
        });
    }
    for apply in twigquery::iterate_nodes(root, &[TWIG_APPLY_STARTING_BLOCK]) {
        if let Some(first) = literal_names(Some(&apply)).into_iter().next() {
            result.push(ExtensionUsage {
                kind: ExtensionUsageKind::Filter,
                name: first.name,
                file_path: file_path.to_owned(),
                range: first.range,
            });
        }
    }
    for expression in test_expressions(root) {
        result.push(ExtensionUsage {
            kind: ExtensionUsageKind::Test,
            name: expression.name,
            file_path: file_path.to_owned(),
            range: expression.range,
        });
    }
    unique_extension_usages(result)
}

pub fn extension_usage_at(
    file_path: &str,
    root: &SyntaxNode,
    offset: u32,
) -> Option<ExtensionUsage> {
    static MEMO: crate::DocumentMemo<Vec<ExtensionUsage>> = crate::DocumentMemo::new(4);
    MEMO.get_or_compute(root, file_path, || {
        extension_usages_in_document(file_path, root)
    })
    .iter()
    .find(|usage| {
        offset >= u32::from(usage.range.start()) && offset <= u32::from(usage.range.end())
    })
    .cloned()
}

fn twig_function_call_is_filter(call: &SyntaxNode) -> bool {
    twigquery::closest_node_of_kind(call, &[TWIG_FILTER])
        .and_then(|filter| TwigFilterNode::cast(filter)?.filter())
        .is_some_and(|operand| is_descendant_node(call, operand.syntax()))
}

pub(crate) fn unique_extension_usages(usages: Vec<ExtensionUsage>) -> Vec<ExtensionUsage> {
    let mut seen = HashSet::with_capacity(usages.len());
    let mut result: Vec<ExtensionUsage> = usages
        .into_iter()
        .filter(|usage| {
            let key = extension_usage_key(usage.kind, &usage.name);
            !key.is_empty() && seen.insert((key, usage.file_path.clone(), usage.range))
        })
        .collect();
    // Go sorts unstably by path and start only; the kind and name keep
    // same-position usages deterministic.
    result.sort_by(|left, right| {
        left.file_path
            .cmp(&right.file_path)
            .then(left.range.start().cmp(&right.range.start()))
            .then(left.kind.cmp(&right.kind))
            .then_with(|| left.name.cmp(&right.name))
            .then(left.range.end().cmp(&right.range.end()))
    });
    result
}
