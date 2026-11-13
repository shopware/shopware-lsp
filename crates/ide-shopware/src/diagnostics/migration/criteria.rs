//! Criteria ID-filter check.

use std::sync::{Arc, LazyLock};

use regex::Regex;
use serde_json::{Map, Value, json};
use swlsp_lsp::{BoxError, Context, DiagnosticId, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{
    PHP_ARGUMENT, PHP_ARGUMENT_LIST, PHP_EXPRESSION_STATEMENT, PHP_MEMBER_CALL,
};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

use crate::diagnostics::ProblemAnalyzer;
use crate::diagnostics::common::{ancestor_php_node, short_php_name};

// ASCII whitespace only, so Unicode spaces do not widen the match.
static CRITERIA_RECEIVER_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(
        r"^[\t\n\f\r ]*\$([A-Za-z_][A-Za-z0-9_]*)[\t\n\f\r ]*->[\t\n\f\r ]*addFilter[\t\n\f\r ]*\(",
    )
    .expect("criteria receiver pattern")
});
static CRITERIA_ASSIGNMENT_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(
        r"^[\t\n\f\r ]*\$([A-Za-z_][A-Za-z0-9_]*)[\t\n\f\r ]*=[\t\n\f\r ]*new[\t\n\f\r ]+\\?(?:[A-Za-z_][A-Za-z0-9_]*\\)*Criteria[\t\n\f\r ]*\([\t\n\f\r ]*\)[\t\n\f\r ]*;?[\t\n\f\r ]*$",
    )
    .expect("criteria assignment pattern")
});

/// Reports `new EqualsFilter('id', ...)` that should pass IDs to the Criteria
/// constructor.
#[derive(Debug, Default, Clone, Copy)]
pub struct ShopwareCriteriaAnalyzer;

impl ShopwareCriteriaAnalyzer {
    pub fn new() -> Self {
        Self
    }
}

impl ProblemAnalyzer for ShopwareCriteriaAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(0)
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        if !swlsp_textutil::lowercase(&document.uri).ends_with(".php") {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        for creation in phpquery::object_creations(&root, &[]) {
            ctx.check()?;
            let class_name = phpquery::object_class_name(&creation);
            let class = short_php_name(&class_name);
            if class != "EqualsFilter" && class != "EqualsAnyFilter" {
                continue;
            }
            let Some(id) = phpquery::string_argument(&creation, 0) else {
                continue;
            };
            if phpquery::string_value(&id) != "id" {
                continue;
            }
            // Only a filter added on its own restricts the search to the
            // IDs: inside NotFilter, OrFilter or MultiFilter, or as a post
            // filter, it means something the constructor cannot say.
            let Some(call) = add_filter_call(&creation) else {
                continue;
            };
            let payload = criteria_fix_payload(document, &creation, &call, class == "EqualsFilter");
            result.push(Problem {
                id: DiagnosticId::from("shopware.criteria.id-filter"),
                range: phpquery::string_content_range(&id),
                message:
                    "Pass entity IDs to the Criteria constructor instead of filtering the id field"
                        .to_owned(),
                severity: Some(DiagnosticSeverity::WARNING),
                source: "shopware-lsp".to_owned(),
                payload: Some(Value::Object(payload)),
                ..Problem::default()
            });
        }
        Ok(result)
    }
}

/// The `->addFilter(...)` call `filter` is a direct argument of.
fn add_filter_call(filter: &SyntaxNode) -> Option<SyntaxNode> {
    let argument = filter.parent().filter(|node| node.kind() == PHP_ARGUMENT)?;
    let list = argument
        .parent()
        .filter(|node| node.kind() == PHP_ARGUMENT_LIST)?;
    let call = list
        .parent()
        .filter(|node| node.kind() == PHP_MEMBER_CALL)?;
    phpquery::call_method_name(&call)
        .eq_ignore_ascii_case("addFilter")
        .then_some(call)
}

fn criteria_fix_payload(
    document: &TextDocument,
    filter: &SyntaxNode,
    call: &SyntaxNode,
    single_id: bool,
) -> Map<String, Value> {
    let unsafe_payload = || {
        let mut map = Map::new();
        map.insert("safe".to_owned(), Value::Bool(false));
        map
    };
    let (Some(function), Some(statement)) = (
        phpquery::function_like_at(filter),
        ancestor_php_node(filter, PHP_EXPRESSION_STATEMENT),
    ) else {
        return unsafe_payload();
    };
    let statement_text = statement.text().to_string();
    let Some(receiver) = CRITERIA_RECEIVER_PATTERN
        .captures(&statement_text)
        .and_then(|captures| captures.get(1))
    else {
        return unsafe_payload();
    };
    // The statement is removed: it must add nothing but this filter.
    if phpquery::arguments(call).count() != 1 {
        return unsafe_payload();
    }
    let value = phpquery::argument_value_text(filter, 1);
    let value = value.trim();
    if value.is_empty() {
        return unsafe_payload();
    }
    // The constructor takes a list of IDs.
    let value = if single_id {
        format!("[{value}]")
    } else {
        value.to_owned()
    };
    let mut matching_criteria: Option<SyntaxNode> = None;
    for candidate in phpquery::object_creations(&function, &[]) {
        if short_php_name(&phpquery::object_class_name(&candidate)) != "Criteria"
            || phpquery::arguments(&candidate).next().is_some()
        {
            continue;
        }
        let Some(assignment) = ancestor_php_node(&candidate, PHP_EXPRESSION_STATEMENT) else {
            continue;
        };
        let assignment_text = assignment.text().to_string();
        let Some(variable) = CRITERIA_ASSIGNMENT_PATTERN
            .captures(&assignment_text)
            .and_then(|captures| captures.get(1))
        else {
            continue;
        };
        if variable.as_str() != receiver.as_str() {
            continue;
        }
        // A filter added under a condition, or in a loop, does not always
        // apply: the constructor would.
        if matching_criteria.is_some()
            || assignment.parent().map(|node| node.text_range())
                != statement.parent().map(|node| node.text_range())
        {
            return unsafe_payload();
        }
        matching_criteria = Some(candidate);
    }
    let Some(matching_criteria) = matching_criteria else {
        return unsafe_payload();
    };
    let criteria_range = matching_criteria.range_trimmed_trivia();
    let filter_range = statement.range_trimmed_trivia();
    let length = document.source().len();
    if u32::from(criteria_range.end()) as usize > length
        || u32::from(filter_range.end()) as usize > length
    {
        return unsafe_payload();
    }
    let Value::Object(map) = json!({
        "safe": true,
        "criteriaStart": u32::from(criteria_range.start()),
        "criteriaEnd": u32::from(criteria_range.end()),
        "filterStart": u32::from(filter_range.start()),
        "filterEnd": u32::from(filter_range.end()),
        "argument": value,
    }) else {
        unreachable!("json object literal");
    };
    map
}
