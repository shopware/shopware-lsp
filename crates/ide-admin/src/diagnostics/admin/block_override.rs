//! `<sw-block extends>` / `<sw-block-parent>` placement rules.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_twig::ast::{AstNode, HtmlStartingTag};
use swlsp_parser_twig::{query as twigquery, syntax as twigsyntax};
use swlsp_syntax::{SyntaxNode, TextRange};
use swlsp_textutil::lowercase;
use swlsp_uriutil::filepath;

use crate::diagnostics::ProblemAnalyzer;

pub const ADMIN_BLOCK_OVERRIDE_NESTED_CODE: &str = "admin.block-override.nested";
pub const ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE: &str = "admin.block-override.conditional";
pub const ADMIN_BLOCK_OVERRIDE_REPEATED_CODE: &str = "admin.block-override.repeated";
pub const ADMIN_BLOCK_PARENT_CONDITIONAL_CODE: &str = "admin.block-parent.conditional";
pub const ADMIN_BLOCK_PARENT_REPEATED_CODE: &str = "admin.block-parent.repeated";

#[derive(Clone, Copy, Debug, Default)]
pub struct AdminBlockOverrideAnalyzer;

impl AdminBlockOverrideAnalyzer {
    pub fn new() -> Self {
        Self
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum MarkupKind {
    Other,
    Override,
    Parent,
}

const CONDITIONAL: u8 = 1;
const REPEATED: u8 = 2;

#[derive(Clone)]
struct MarkupTag {
    node: SyntaxNode,
    name_range: TextRange,
    kind: MarkupKind,
    control: u8,
}

impl ProblemAnalyzer for AdminBlockOverrideAnalyzer {
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
        if !document.uri.contains("Resources/app/administration") {
            return Ok(Vec::new());
        }
        if !matches!(
            lowercase(filepath::ext(&document.uri)).as_str(),
            ".twig" | ".vue"
        ) {
            return Ok(Vec::new());
        }

        let mut tags_by_node: HashMap<SyntaxNode, MarkupTag> = HashMap::new();
        let mut targets = Vec::new();
        for node in twigquery::iterate_nodes(&root, &[twigsyntax::HTML_STARTING_TAG]) {
            ctx.check()?;
            let Some(tag) = markup_tag_at(&node) else {
                continue;
            };
            tags_by_node.insert(tag.node.clone(), tag.clone());
            if tag.kind != MarkupKind::Other {
                targets.push(tag);
            }
        }

        let mut problems = Vec::with_capacity(targets.len());
        for tag in &targets {
            ctx.check()?;
            let (mut control, nested) = ancestor_context(tag, &tags_by_node);
            control |= tag.control;
            match tag.kind {
                MarkupKind::Override => {
                    if nested {
                        problems.push(markup_problem(
                            tag,
                            ADMIN_BLOCK_OVERRIDE_NESTED_CODE,
                            "Nested <sw-block extends> declarations are not supported",
                        ));
                    }
                    if control & CONDITIONAL != 0 {
                        problems.push(markup_problem(
                            tag,
                            ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE,
                            "Conditional <sw-block extends> declarations are not supported; override blocks must be statically present",
                        ));
                    }
                    if control & REPEATED != 0 {
                        problems.push(markup_problem(
                            tag,
                            ADMIN_BLOCK_OVERRIDE_REPEATED_CODE,
                            "Repeated <sw-block extends> declarations are not supported; override blocks must not be placed in a loop",
                        ));
                    }
                }
                MarkupKind::Parent => {
                    if control & CONDITIONAL != 0 {
                        problems.push(markup_problem(
                            tag,
                            ADMIN_BLOCK_PARENT_CONDITIONAL_CODE,
                            "Conditional <sw-block-parent> usage is not supported; parent rendering must be statically present",
                        ));
                    }
                    if control & REPEATED != 0 {
                        problems.push(markup_problem(
                            tag,
                            ADMIN_BLOCK_PARENT_REPEATED_CODE,
                            "Repeated <sw-block-parent> usage is not supported; parent rendering must not be placed in a loop",
                        ));
                    }
                }
                MarkupKind::Other => {}
            }
        }
        Ok(problems)
    }
}

fn markup_tag_at(start_node: &SyntaxNode) -> Option<MarkupTag> {
    let starting = HtmlStartingTag::cast(start_node.clone())?;
    let name = starting.name()?;
    let tag = starting.html_tag()?;
    let mut control = 0;
    let mut has_extends = false;
    for attribute in starting.attributes() {
        let attribute_name =
            lowercase(&twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default());
        match attribute_name.as_str() {
            "extends" => has_extends = true,
            "v-if" | "v-else-if" | "v-else" => control |= CONDITIONAL,
            "v-for" => control |= REPEATED,
            _ => {}
        }
    }
    let kind = match lowercase(name.text()).as_str() {
        "sw-block" if has_extends => MarkupKind::Override,
        "sw-block-parent" => MarkupKind::Parent,
        _ => MarkupKind::Other,
    };
    Some(MarkupTag {
        node: tag.syntax().clone(),
        name_range: name.text_range(),
        kind,
        control,
    })
}

fn ancestor_context(tag: &MarkupTag, tags_by_node: &HashMap<SyntaxNode, MarkupTag>) -> (u8, bool) {
    let mut control = 0;
    let mut nested = false;
    for ancestor in tag.node.ancestors().skip(1) {
        let kind = ancestor.kind();
        if kind == twigsyntax::HTML_TAG {
            let Some(parent) = tags_by_node.get(&ancestor) else {
                continue;
            };
            control |= parent.control;
            if parent.kind == MarkupKind::Override {
                nested = true;
            }
        } else if kind == twigsyntax::TWIG_IF || kind == twigsyntax::TWIG_IF_BLOCK {
            control |= CONDITIONAL;
        } else if kind == twigsyntax::TWIG_FOR
            || kind == twigsyntax::TWIG_FOR_BLOCK
            || kind == twigsyntax::TWIG_FOR_ELSE_BLOCK
        {
            control |= REPEATED;
        }
    }
    (control, nested)
}

fn markup_problem(tag: &MarkupTag, id: &str, message: &str) -> Problem {
    Problem {
        id: id.into(),
        range: tag.name_range,
        message: message.to_owned(),
        source: "shopware-lsp".into(),
        severity: Some(DiagnosticSeverity::ERROR),
        ..Problem::default()
    }
}
