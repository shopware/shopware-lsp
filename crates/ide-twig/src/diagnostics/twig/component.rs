//! Twig component and live component diagnostics.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::ast::{AstNode, HtmlTag, TwigBlock};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TWIG_BLOCK, TWIG_COMPONENT, TWIG_FROM, TWIG_LITERAL_NAME};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::{eq_ignore_case, lowercase};
use swlsp_twigcomponent::{Index, LiveAction};

use super::{contains_fold, live_argument_attribute_segment, strings_value, uri_path};
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

const MISSING_TWIG_COMPONENT_CODE: &str = "twig.component.missing";
const MISSING_COMPONENT_BLOCK_CODE: &str = "twig.component.block.missing";
const MISSING_LIVE_ACTION_CODE: &str = "twig.component.live_action.missing";
const MISSING_LIVE_ARGUMENT_CODE: &str = "twig.component.live_argument.missing";
const MIXED_COMPONENT_SYNTAX_CODE: &str = "twig.component.mixed_syntax";
const COMPONENT_SELF_IMPORT_CODE: &str = "twig.component.self_macro_import";

pub struct TwigComponentAnalyzer {
    index: Option<Arc<Index>>,
}

impl TwigComponentAnalyzer {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

pub(crate) fn suggestions_payload(suggestions: Vec<String>) -> Option<Value> {
    let mut payload = Map::new();
    payload.insert("suggestions".into(), strings_value(suggestions));
    Some(Value::Object(payload))
}

impl DeclaredAnalyzer for TwigComponentAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "twig.component",
        description: "Twig component usages naming unknown components, blocks, live actions or arguments, and mixed component syntax.",
        domain: "symfony.twigComponents",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Twig],
        source: "twig",
        codes: &[
            MISSING_COMPONENT_BLOCK_CODE,
            MISSING_LIVE_ACTION_CODE,
            MISSING_LIVE_ARGUMENT_CODE,
            MISSING_TWIG_COMPONENT_CODE,
            MIXED_COMPONENT_SYNTAX_CODE,
            COMPONENT_SELF_IMPORT_CODE,
        ],
    };
}

impl ProblemAnalyzer for TwigComponentAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::diagnostics::inputs(&self.index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(index), Some(root)) = (&self.index, document.root()) else {
            return Ok(Vec::new());
        };
        if !lowercase(&document.uri).ends_with(".twig") {
            return Ok(Vec::new());
        }
        let names = index.names()?;
        let available: HashSet<String> = names.iter().cloned().collect();
        let mut run = Run {
            ctx,
            index,
            root: &root,
            path: uri_path(&document.uri),
            names,
            available,
            problems: Vec::new(),
        };
        run.collect_missing_components();
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        run.collect_missing_blocks()?;
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        run.collect_live_diagnostics()?;
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        let mut problems = run.problems;
        problems.extend(mixed_component_syntax_diagnostics(&root));
        problems.extend(component_self_import_diagnostics(&root));
        Ok(problems)
    }
}

pub(super) struct Run<'a> {
    pub(super) ctx: &'a Context,
    pub(super) index: &'a Index,
    pub(super) root: &'a SyntaxNode,
    pub(super) path: String,
    pub(super) names: Vec<String>,
    pub(super) available: HashSet<String>,
    pub(super) problems: Vec<Problem>,
}

impl Run<'_> {
    fn collect_missing_components(&mut self) {
        for usage in swlsp_twigcomponent::usages_in_twig(&self.path, self.root) {
            if self.ctx.is_cancelled() {
                return;
            }
            if self.available.contains(&usage.name) {
                continue;
            }
            self.problems.push(Problem {
                id: MISSING_TWIG_COMPONENT_CODE.into(),
                range: usage.range,
                message: format!("Twig component '{}' not found", usage.name),
                severity: Some(protocol::DiagnosticSeverity::WARNING),
                source: "twig".into(),
                payload: suggestions_payload(swlsp_suggestion::similar(&usage.name, &self.names)),
                ..Problem::default()
            });
        }
    }

    fn collect_missing_blocks(&mut self) -> Result<(), BoxError> {
        // Repeated uses of a component share a catalog lookup within this
        // analysis; the cache stays local so the next run sees updates.
        let mut block_names: HashMap<String, Vec<String>> = HashMap::new();
        for usage in swlsp_twigcomponent::block_usages_in_twig(self.root) {
            if self.ctx.is_cancelled() {
                return Ok(());
            }
            if !self.available.contains(&usage.component) {
                continue;
            }
            if !block_names.contains_key(&usage.component) {
                let blocks = self.index.blocks(&usage.component)?;
                let candidates =
                    unique_component_block_names(blocks.into_iter().map(|block| block.name));
                block_names.insert(usage.component.clone(), candidates);
            }
            let candidates = &block_names[&usage.component];
            if candidates.contains(&usage.name) {
                continue;
            }
            self.problems.push(Problem {
                id: MISSING_COMPONENT_BLOCK_CODE.into(),
                range: usage.range,
                message: format!(
                    "Block '{}' not found in Twig component '{}'",
                    usage.name, usage.component
                ),
                severity: Some(protocol::DiagnosticSeverity::WARNING),
                source: "twig".into(),
                payload: suggestions_payload(swlsp_suggestion::similar(&usage.name, candidates)),
                ..Problem::default()
            });
        }
        Ok(())
    }

    fn collect_live_diagnostics(&mut self) -> Result<(), BoxError> {
        let components = self.index.components_for_template(&self.path)?;
        if !components.iter().any(|component| component.live) {
            return Ok(());
        }
        let actions = self.index.live_actions_for_template(&self.path)?;
        self.collect_missing_live_actions(&actions);
        self.collect_missing_live_arguments(&actions);
        Ok(())
    }

    pub(super) fn collect_missing_live_actions(&mut self, actions: &[LiveAction]) {
        let action_names: Vec<String> = actions.iter().map(|action| action.name.clone()).collect();
        for reference in swlsp_twigcomponent::live_action_references_in_twig(&self.path, self.root)
        {
            if self.ctx.is_cancelled() {
                return;
            }
            if reference.name.is_empty() || contains_fold(&action_names, &reference.name) {
                continue;
            }
            self.problems.push(Problem {
                id: MISSING_LIVE_ACTION_CODE.into(),
                range: reference.range,
                message: format!(
                    "Live Action '{}' not found on this component",
                    reference.name
                ),
                severity: Some(protocol::DiagnosticSeverity::WARNING),
                source: "twig".into(),
                payload: suggestions_payload(swlsp_suggestion::similar(
                    &reference.name,
                    &action_names,
                )),
                ..Problem::default()
            });
        }
    }

    pub(super) fn collect_missing_live_arguments(&mut self, actions: &[LiveAction]) {
        for reference in
            swlsp_twigcomponent::live_action_argument_references_in_twig(&self.path, self.root)
        {
            if self.ctx.is_cancelled() {
                return;
            }
            let names: Vec<String> = actions
                .iter()
                .filter(|action| eq_ignore_case(&action.name, &reference.action))
                .flat_map(|action| action.parameters.iter().map(|p| p.name.clone()))
                .collect();
            if names.is_empty() || contains_fold(&names, &reference.name) {
                continue;
            }
            let suggestions: Vec<String> = swlsp_suggestion::similar(&reference.name, &names)
                .iter()
                .map(|value| live_argument_attribute_segment(value))
                .collect();
            self.problems.push(Problem {
                id: MISSING_LIVE_ARGUMENT_CODE.into(),
                range: reference.range,
                message: format!(
                    "Live Action '{}' has no argument named '{}'",
                    reference.action, reference.name
                ),
                severity: Some(protocol::DiagnosticSeverity::WARNING),
                source: "twig".into(),
                payload: suggestions_payload(suggestions),
                ..Problem::default()
            });
        }
    }
}

fn unique_component_block_names(values: impl Iterator<Item = String>) -> Vec<String> {
    let mut seen = HashSet::new();
    values.filter(|value| seen.insert(value.clone())).collect()
}

fn mixed_component_syntax_diagnostics(root: &SyntaxNode) -> Vec<Problem> {
    let mut result = Vec::new();
    for node in twigquery::iterate_nodes(root, &[TWIG_BLOCK]) {
        let Some(block) = TwigBlock::cast(node.clone()) else {
            continue;
        };
        if !inside_html_component(&node) {
            continue;
        }
        let Some(name) = block.name() else {
            continue;
        };
        result.push(Problem {
            id: MIXED_COMPONENT_SYNTAX_CODE.into(),
            range: name.text_range(),
            message: "Cannot use Twig block syntax inside an HTML component; use <twig:block name=\"...\"> instead".into(),
            severity: Some(protocol::DiagnosticSeverity::ERROR),
            source: "twig".into(),
            ..Problem::default()
        });
    }
    result
}

fn inside_html_component(node: &SyntaxNode) -> bool {
    node.ancestors()
        .skip(1)
        .any(|current| HtmlTag::cast(current).is_some_and(|tag| tag.is_twig_component()))
}

fn component_self_import_diagnostics(root: &SyntaxNode) -> Vec<Problem> {
    let mut result = Vec::new();
    for from in twigquery::iterate_nodes(root, &[TWIG_FROM]) {
        let Some(source) = twigquery::iterate_nodes(&from, &[TWIG_LITERAL_NAME]).next() else {
            continue;
        };
        if source.text().to_string().trim() != "_self" || !inside_twig_component(&from) {
            continue;
        }
        result.push(Problem {
            id: COMPONENT_SELF_IMPORT_CODE.into(),
            range: source.range_trimmed_trivia(),
            message: "Cannot use '_self' to import macros inside a Twig component. Use the full template path instead.".into(),
            severity: Some(protocol::DiagnosticSeverity::ERROR),
            source: "twig".into(),
            ..Problem::default()
        });
    }
    result
}

fn inside_twig_component(node: &SyntaxNode) -> bool {
    node.ancestors().skip(1).any(|current| {
        current.kind() == TWIG_COMPONENT
            || HtmlTag::cast(current).is_some_and(|tag| tag.is_twig_component())
    })
}
