//! Go `twig_include_parameter_completion.go`: input names in include and
//! embed parameter hashes.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_lsp::{CompletionProvider, CompletionRequest, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};
use swlsp_php::PhpIndex;
use swlsp_syntax::SyntaxNode;
use swlsp_twig::TwigIndexer;

use super::{markdown, request_offset};
use crate::common::go_to_lower;
use swlsp_gosort::sort_slice;

pub struct TwigIncludeParameterCompletionProvider {
    twig_index: Option<Arc<TwigIndexer>>,
    php_index: Option<PhpIndex>,
}

#[derive(Clone, Default)]
struct IncludeParameterCandidate {
    name: String,
    file: String,
    php_type: String,
    php_file: String,
}

impl TwigIncludeParameterCompletionProvider {
    pub fn new(twig_index: Option<Arc<TwigIndexer>>, php_index: Option<PhpIndex>) -> Self {
        Self {
            twig_index,
            php_index,
        }
    }

    fn parameter_candidates(
        &self,
        twig_index: &TwigIndexer,
        uri: &str,
        root: &SyntaxNode,
        template: &str,
    ) -> Vec<IncludeParameterCandidate> {
        let mut variables = twig_index
            .get_template_variables(&[template])
            .unwrap_or_default();
        let current_path = swlsp_uriutil::path(uri).unwrap_or_default();
        if template_matches_path(template, &current_path) {
            let overlay = swlsp_twig::template_input_variables_in_document(&current_path, root);
            variables.retain(|variable| variable.file_path != current_path);
            variables.extend(overlay);
        }

        let mut candidates: HashMap<String, IncludeParameterCandidate> = HashMap::new();
        for variable in variables {
            candidates
                .entry(variable.name.clone())
                .or_insert(IncludeParameterCandidate {
                    name: variable.name,
                    file: variable.file_path,
                    ..Default::default()
                });
        }
        if let Some(php) = &self.php_index {
            for variable in php.twig_template_variables(&[template]).unwrap_or_default() {
                let candidate = candidates.entry(variable.name.clone()).or_default();
                candidate.name = variable.name;
                candidate.php_type = variable.ty;
                candidate.php_file = variable.file;
            }
        }
        // Go sorts map values by lowercase name only; visiting names in byte
        // order first keeps case variants deterministic.
        let mut keyed: Vec<(String, IncludeParameterCandidate)> = candidates.into_iter().collect();
        keyed.sort_by(|left, right| left.0.cmp(&right.0));
        let mut keyed: Vec<(String, IncludeParameterCandidate)> = keyed
            .into_iter()
            .map(|(_, candidate)| (go_to_lower(&candidate.name), candidate))
            .collect();
        sort_slice(&mut keyed, |left, right| left.0 < right.0);
        keyed.into_iter().map(|(_, candidate)| candidate).collect()
    }
}

impl CompletionProvider for TwigIncludeParameterCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let Some(twig_index) = &self.twig_index else {
            return Vec::new();
        };
        let Some(root) = request.root.as_ref() else {
            return Vec::new();
        };
        if !go_to_lower(&request.params.text_document.uri).ends_with(".twig") {
            return Vec::new();
        }
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let node = request.node.as_ref();
        let Some(include_context) = swlsp_twig::include_parameter_context_at(root, node, offset)
        else {
            return Vec::new();
        };
        let current_name = swlsp_twig::include_parameter_at(root, node, offset)
            .map(|parameter| parameter.name)
            .unwrap_or_default();
        let existing: HashSet<&String> = include_context
            .existing
            .iter()
            .filter(|name| **name != current_name)
            .collect();

        let candidates = self.parameter_candidates(
            twig_index,
            &request.params.text_document.uri,
            root,
            &include_context.template,
        );
        let mut items = Vec::with_capacity(candidates.len());
        for candidate in candidates {
            if existing.contains(&candidate.name) {
                continue;
            }
            let detail = if candidate.php_type.is_empty() {
                "Twig template input".to_string()
            } else {
                candidate.php_type.clone()
            };
            let documentation = if !candidate.file.is_empty() {
                format!(
                    "Read by `{}` in `{}`.",
                    candidate.name,
                    swlsp_uriutil::filepath::base(&candidate.file)
                )
            } else if !candidate.php_file.is_empty() {
                format!(
                    "PHP context value provided to `{}` by `{}`.",
                    include_context.template,
                    swlsp_uriutil::filepath::base(&candidate.php_file)
                )
            } else {
                format!("Input for `{}`.", include_context.template)
            };
            items.push(protocol::CompletionItem {
                label: candidate.name,
                kind: Some(CompletionItemKind::VARIABLE),
                detail,
                documentation: markdown(documentation),
                ..Default::default()
            });
        }
        items
    }

    fn trigger_characters(&self) -> Vec<String> {
        vec!["{".into(), ",".into(), "'".into(), "\"".into()]
    }
}

fn template_matches_path(template: &str, path: &str) -> bool {
    swlsp_twig::template_names(path)
        .iter()
        .any(|name| name == template)
}
