//! Missing Twig templates referenced from Twig and PHP.

use std::collections::HashSet;
use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_NAME, PHP_STRING};
use swlsp_php::{PhpIndex, assistant_argument_tags, guessed_controller_template};
use swlsp_php_semantic::{Document, Snapshot};
use swlsp_php_types::Type;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::eq_ignore_case;
use swlsp_twig::{TemplateReferenceKind, TwigIndexer};

use super::{lower_ext, strings_value, uri_path};
use crate::diagnostics::LookupError;
use crate::diagnostics::ProblemAnalyzer;

const MISSING_TEMPLATE_CODE: &str = "twig.template.missing";

struct Candidate {
    name: String,
    node: Option<SyntaxNode>,
}

struct Group {
    candidates: Vec<Candidate>,
    range: TextRange,
}

pub struct TemplateAnalyzer {
    twig_index: Option<Arc<TwigIndexer>>,
    php_index: Option<PhpIndex>,
}

impl TemplateAnalyzer {
    pub fn new(twig_index: Option<Arc<TwigIndexer>>, php_index: Option<PhpIndex>) -> Self {
        Self {
            twig_index,
            php_index,
        }
    }
}

impl ProblemAnalyzer for TemplateAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(
            crate::diagnostics::inputs(&self.twig_index)
                + crate::diagnostics::inputs(&self.php_index),
        )
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(twig_index), Some(root)) = (&self.twig_index, document.root()) else {
            return Ok(Vec::new());
        };
        let mut groups = Vec::new();
        match lower_ext(&document.uri).as_str() {
            ".twig" => {
                for target_group in swlsp_twig::twig_template_target_groups(&root) {
                    if !target_group.exact
                        || target_group.ignore_missing
                        || target_group.targets.is_empty()
                    {
                        continue;
                    }
                    let candidates: Vec<Candidate> = target_group
                        .targets
                        .iter()
                        .filter(|target| !target.template.is_empty())
                        .map(|target| Candidate {
                            name: target.template.clone(),
                            node: root.node_at(target.range.start()),
                        })
                        .collect();
                    if !candidates.is_empty() {
                        groups.push(Group {
                            candidates,
                            range: target_group.range,
                        });
                    }
                }
            }
            ".php" => {
                for (name, range) in self.php_candidates(document, &root)? {
                    groups.push(Group {
                        candidates: vec![Candidate { name, node: None }],
                        range,
                    });
                }
            }
            _ => return Ok(Vec::new()),
        }

        let mut result = Vec::new();
        let mut seen = HashSet::new();
        let mut candidate_names: Option<Vec<String>> = None;
        for group in groups {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let mut found = false;
            for candidate in &group.candidates {
                let files = twig_index
                    .get_twig_files_for_reference(candidate.node.as_ref(), &candidate.name)
                    .map_err(|error| {
                        LookupError::new(format!("query Twig template {:?}", candidate.name), error)
                    })?;
                if !files.is_empty() {
                    found = true;
                    break;
                }
            }
            if found {
                continue;
            }
            if candidate_names.is_none() {
                candidate_names = Some(
                    twig_index
                        .get_all_template_files()
                        .map_err(|error| LookupError::new("query Twig templates", error))?,
                );
            }
            let missing_names: Vec<String> =
                group.candidates.iter().map(|c| c.name.clone()).collect();
            let key = format!(
                "{}:{}:{}",
                u32::from(group.range.start()),
                u32::from(group.range.end()),
                missing_names.join("\x00")
            );
            if !seen.insert(key) {
                continue;
            }
            let message = if missing_names.len() > 1 {
                format!(
                    "None of the fallback templates were found: '{}'",
                    missing_names.join("', '")
                )
            } else {
                format!("Template '{}' not found", missing_names[0])
            };
            let suggestions =
                similar_template_names(&missing_names, candidate_names.as_deref().unwrap_or(&[]));
            let mut payload = Map::new();
            payload.insert(
                "templateName".into(),
                Value::String(missing_names[0].clone()),
            );
            payload.insert("suggestions".into(), strings_value(suggestions));
            if missing_names.len() > 1 {
                payload.insert("templateNames".into(), strings_value(missing_names));
            }
            result.push(Problem {
                id: MISSING_TEMPLATE_CODE.into(),
                range: group.range,
                message,
                source: "twig".into(),
                severity: Some(protocol::DiagnosticSeverity::ERROR),
                payload: Some(Value::Object(payload)),
                ..Problem::default()
            });
        }
        Ok(result)
    }
}

fn similar_template_names(missing_names: &[String], candidates: &[String]) -> Vec<String> {
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for name in missing_names {
        for candidate in swlsp_suggestion::similar_templates(name, candidates) {
            // Template suggestions are loader names; a filesystem path can
            // never be one, whatever an index key contains.
            if candidate.starts_with('/') {
                continue;
            }
            if seen.insert(candidate.clone()) {
                result.push(candidate);
            }
        }
    }
    result
}

impl TemplateAnalyzer {
    fn php_candidates(
        &self,
        document: &TextDocument,
        root: &SyntaxNode,
    ) -> Result<Vec<(String, TextRange)>, BoxError> {
        let path = uri_path(&document.uri);
        let references = swlsp_twig::php_template_references(&path, root);
        let mut analysis = None;
        if let Some(php_index) = &self.php_index {
            analysis = swlsp_lsp_phpanalysis::for_document(php_index, document)?;
        }
        let mut result = Vec::with_capacity(references.len());
        for reference in references {
            if reference.kind == TemplateReferenceKind::Render {
                let node = root.node_at(reference.range.start());
                let supported = is_supported_php_call(
                    node.as_ref(),
                    analysis.as_ref().map(|state| &*state.document),
                    analysis.as_ref().map(|state| &*state.snapshot),
                );
                if !supported {
                    continue;
                }
            }
            result.push((reference.template, reference.range));
        }
        if let (Some(php_index), Some(state)) = (&self.php_index, &analysis) {
            let validation =
                php_index.analyzed_document_context(Some(root), state.document.clone());
            for literal in phpquery::nodes(root, &[PHP_STRING]) {
                if assistant_argument_tags(&validation, &literal, &["Template"]).is_none() {
                    continue;
                }
                let name = phpquery::string_value(&literal);
                if name.is_empty() {
                    continue;
                }
                result.push((name, phpquery::string_content_range(&literal)));
            }
        }
        result.extend(implicit_php_template_candidates(root));
        Ok(result)
    }
}

fn implicit_php_template_candidates(root: &SyntaxNode) -> Vec<(String, TextRange)> {
    let mut result = Vec::new();
    for class in phpquery::classes(root) {
        for method in phpquery::methods(&class) {
            let name = guessed_controller_template(root, &method);
            if name.is_empty() {
                continue;
            }
            for attribute in phpquery::attributes(&method) {
                if !is_template_attribute(&attribute)
                    || phpquery::arguments(&attribute).next().is_some()
                {
                    continue;
                }
                let Some(name_node) = phpquery::direct_child(&attribute, PHP_NAME) else {
                    continue;
                };
                result.push((name.clone(), name_node.range_trimmed_trivia()));
            }
        }
    }
    result
}

fn is_template_attribute(attribute: &SyntaxNode) -> bool {
    let full = phpquery::attribute_name(attribute);
    let name = full.strip_prefix('\\').unwrap_or(&full);
    let name = match name.rfind('\\') {
        Some(index) => &name[index + 1..],
        None => name,
    };
    eq_ignore_case(name, "Template")
}

fn is_supported_php_call(
    node: Option<&SyntaxNode>,
    document: Option<&Document>,
    snapshot: Option<&Snapshot>,
) -> bool {
    let Some(node) = node else {
        return false;
    };
    if phpquery::attribute_at(node).is_some() {
        return true;
    }
    let call = phpquery::call_at(node);
    let name = call
        .as_ref()
        .map(phpquery::call_method_name)
        .unwrap_or_default();
    if name == "renderStorefront" {
        return true;
    }
    let receiver = call.as_ref().and_then(phpquery::call_receiver);
    let (Some(receiver), Some(document), Some(snapshot)) = (receiver, document, snapshot) else {
        return false;
    };
    let receiver_type = document.type_of(&receiver).ty;
    if receiver_type.is_unknown() {
        return false;
    }
    php_template_receiver_supported(snapshot, &receiver_type, &name)
}

fn php_template_receiver_supported(snapshot: &Snapshot, receiver: &Type, method: &str) -> bool {
    let mut targets = vec!["Symfony\\Bundle\\FrameworkBundle\\Controller\\AbstractController"];
    if method == "render" {
        targets.push("Twig\\Environment");
    }
    targets.iter().any(|target| {
        snapshot
            .relations()
            .is_subtype(receiver, &Type::named(target, Vec::new()))
    })
}
