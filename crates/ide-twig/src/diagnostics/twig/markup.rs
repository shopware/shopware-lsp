//! Storefront markup mistakes one template proves: output escaped for the
//! wrong context, and HTML that browsers read differently than written.
//!
//! Administration templates are Vue templates and follow other rules.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, DiagnosticId, InspectionScope, InspectionSpec, Problem, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{
    HTML_ATTRIBUTE, HTML_ATTRIBUTE_LIST, HTML_STARTING_TAG, HTML_STRING_INNER, TWIG_EXPRESSION,
    TWIG_FILTER, TWIG_VAR,
};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::lowercase;

use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

const RAW_ATTRIBUTE_CODE: &str = "twig.raw.attribute";
const RAW_TRANSLATION_CODE: &str = "twig.raw.translation";
const SANITIZE_ATTRIBUTE_CODE: &str = "twig.sanitize.attribute";
const TARGET_BLANK_CODE: &str = "twig.html.target_blank";
const BUTTON_TYPE_CODE: &str = "twig.html.button_type";
const DUPLICATE_ATTRIBUTE_CODE: &str = "twig.html.duplicate_attribute";
const IMAGE_ALT_CODE: &str = "twig.html.img_alt";

#[derive(Debug, Default, Clone, Copy)]
pub struct TwigMarkupAnalyzer;

impl TwigMarkupAnalyzer {
    pub fn new() -> Self {
        Self
    }
}

impl DeclaredAnalyzer for TwigMarkupAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "twig.markup",
        description: "Storefront templates printing unescaped or wrongly escaped output (raw or sw_sanitize in an attribute, raw after trans), and HTML that needs an attribute it lacks: links opening a new window, buttons without a type, images without alt text, and attributes written twice.",
        domain: "twig",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Twig],
        source: "shopware-lsp",
        codes: &[
            RAW_ATTRIBUTE_CODE,
            RAW_TRANSLATION_CODE,
            SANITIZE_ATTRIBUTE_CODE,
            TARGET_BLANK_CODE,
            BUTTON_TYPE_CODE,
            DUPLICATE_ATTRIBUTE_CODE,
            IMAGE_ALT_CODE,
        ],
    };
}

impl ProblemAnalyzer for TwigMarkupAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(0)
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let uri = lowercase(&document.uri);
        if !uri.ends_with(".twig") || uri.contains("/app/administration/") {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let mut problems = Vec::new();
        for node in twigquery::iterate_nodes(&root, &[TWIG_FILTER, HTML_STARTING_TAG]) {
            ctx.check()?;
            if node.kind() == TWIG_FILTER {
                problems.extend(filter_problem(&node));
            } else {
                tag_problems(&node, &mut problems);
            }
        }
        problems.sort_by_key(|problem| problem.range.start());
        Ok(problems)
    }
}

fn problem(code: &str, range: TextRange, message: &str) -> Problem {
    Problem {
        id: DiagnosticId::from(code),
        range,
        message: message.to_owned(),
        severity: Some(protocol::DiagnosticSeverity::WARNING),
        source: "shopware-lsp".to_owned(),
        ..Problem::default()
    }
}

/// The last filter of a printed expression decides how it is escaped.
fn filter_problem(filter: &SyntaxNode) -> Option<Problem> {
    let printed = filter
        .parent()
        .filter(|parent| parent.kind() == TWIG_EXPRESSION)?
        .parent()
        .filter(|parent| parent.kind() == TWIG_VAR)?;
    let name = twigquery::filter_name(filter)?;
    let in_attribute = printed
        .ancestors()
        .any(|ancestor| ancestor.kind() == HTML_STRING_INNER);
    // The name of the filter: its last operand.
    let range = filter.children().last().map_or_else(
        || filter.range_trimmed_trivia(),
        |name| name.range_trimmed_trivia(),
    );
    match (name.as_str(), in_attribute) {
        ("raw", true) => Some(problem(
            RAW_ATTRIBUTE_CODE,
            range,
            "raw in an attribute value lets the value close the attribute; let Twig escape it, \
             or use |escape('html_attr')",
        )),
        ("sw_sanitize", true) => Some(problem(
            SANITIZE_ATTRIBUTE_CODE,
            range,
            "sw_sanitize cleans HTML for element content and does not escape quotes; in an \
             attribute let Twig escape the value, or use |striptags",
        )),
        ("raw", false) => {
            let translated = filter
                .children()
                .next()
                .and_then(|operand| operand.children().next())
                .filter(|inner| inner.kind() == TWIG_FILTER)
                .and_then(|inner| twigquery::filter_name(&inner))
                .is_some_and(|inner| inner == "trans");
            translated.then(|| {
                problem(
                    RAW_TRANSLATION_CODE,
                    range,
                    "A snippet printed with raw outputs whatever a translation contains; use \
                     |sw_sanitize to keep its formatting",
                )
            })
        }
        _ => None,
    }
}

/// A statically written attribute.
struct Attribute {
    name: String,
    node: SyntaxNode,
}

impl Attribute {
    /// The value when it contains no Twig.
    fn static_value(&self) -> Option<String> {
        let Some(value) = self
            .node
            .descendants()
            .find(|node| node.kind() == HTML_STRING_INNER)
        else {
            // A boolean attribute.
            return Some(String::new());
        };
        value
            .children()
            .next()
            .is_none()
            .then(|| value.text().to_string())
    }
}

fn tag_problems(tag: &SyntaxNode, problems: &mut Vec<Problem>) {
    let Some(name) = twigquery::html_tag_name(tag).map(|name| lowercase(&name)) else {
        return;
    };
    let list = tag
        .children()
        .find(|child| child.kind() == HTML_ATTRIBUTE_LIST);
    // Twig between the attributes may add any of them.
    let dynamic = list
        .as_ref()
        .is_some_and(|list| list.children().any(|child| child.kind() != HTML_ATTRIBUTE));
    let attributes: Vec<Attribute> = list
        .iter()
        .flat_map(SyntaxNode::children)
        .filter(|child| child.kind() == HTML_ATTRIBUTE)
        .filter_map(|node| {
            let name = twigquery::html_attribute_name(&node)?;
            // A name built with Twig is not known.
            (!name.contains(['{', '%'])).then(|| Attribute {
                name: lowercase(&name),
                node,
            })
        })
        .collect();
    for (index, attribute) in attributes.iter().enumerate() {
        if attributes[..index]
            .iter()
            .any(|earlier| earlier.name == attribute.name)
        {
            problems.push(problem(
                DUPLICATE_ATTRIBUTE_CODE,
                attribute.node.range_trimmed_trivia(),
                &format!(
                    "The attribute {} is written twice; browsers keep the first",
                    attribute.name
                ),
            ));
        }
    }
    if dynamic {
        return;
    }
    let find = |name: &str| attributes.iter().find(|attribute| attribute.name == name);
    let tag_range = tag
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .find(|token| lowercase(token.text()) == name)
        .map_or_else(|| tag.range_trimmed_trivia(), |token| token.text_range());
    match name.as_str() {
        "button" if find("type").is_none() => problems.push(problem(
            BUTTON_TYPE_CODE,
            tag_range,
            "A button without a type submits the form around it; write type=\"button\" or \
             type=\"submit\"",
        )),
        "img" if find("alt").is_none() => problems.push(problem(
            IMAGE_ALT_CODE,
            tag_range,
            "An image needs an alt attribute; use alt=\"\" when it is decorative",
        )),
        "a" => {
            let Some(target) = find("target") else {
                return;
            };
            if target
                .static_value()
                .is_none_or(|value| lowercase(value.trim()) != "_blank")
            {
                return;
            }
            let safe = match find("rel") {
                None => false,
                // A dynamic value may contain it.
                Some(rel) => rel.static_value().is_none_or(|value| {
                    lowercase(&value)
                        .split_whitespace()
                        .any(|token| token == "noopener" || token == "noreferrer")
                }),
            };
            if !safe {
                problems.push(problem(
                    TARGET_BLANK_CODE,
                    target.node.range_trimmed_trivia(),
                    "A link opening a new window should carry rel=\"noopener\" so the opened \
                     page cannot reach this one",
                ));
            }
        }
        _ => {}
    }
}
