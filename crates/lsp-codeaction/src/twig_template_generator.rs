//! Go `twig_template_generator_codeaction.go`: the Twig Extends and Block
//! Overwrite generators backed by the template and inheritance indexes.

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    ActionProvider, BoxError, CodeActionRequest, CommandFunc, CommandProvider, Context,
};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_php_types::{equal_fold, go_to_lower};
use swlsp_twig::{TwigFile, TwigIndexer};

use crate::admin_twig_override::nullable;
use crate::shared::{escape_twig_single_quoted, is_administration_twig_path};

pub const GENERATE_TWIG_EXTENDS_ACTION: &str = "shopware.symfony.generateTwigExtends";
pub const GENERATE_TWIG_BLOCKS_ACTION: &str = "shopware.symfony.generateTwigBlocks";

pub const TWIG_EXTENDS_CANDIDATES_COMMAND: &str = "shopware/symfony/twig/extends/candidates";
pub const GENERATE_TWIG_EXTENDS_COMMAND: &str = "shopware/symfony/twig/extends/generate";
pub const TWIG_BLOCK_CANDIDATES_COMMAND: &str = "shopware/symfony/twig/blocks/candidates";
pub const GENERATE_TWIG_BLOCKS_COMMAND: &str = "shopware/symfony/twig/blocks/generate";

#[derive(Clone)]
pub struct TwigTemplateGeneratorProvider {
    twig_index: Option<Arc<TwigIndexer>>,
}

impl TwigTemplateGeneratorProvider {
    pub fn new(twig_index: Option<Arc<TwigIndexer>>) -> Self {
        Self { twig_index }
    }
}

impl ActionProvider for TwigTemplateGeneratorProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_REWRITE]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        let Some(twig_index) = self.twig_index.as_ref() else {
            return Vec::new();
        };
        let Some(document) = request.document.as_ref() else {
            return Vec::new();
        };
        if ctx.is_cancelled() || document.language != Some(LanguageId::Twig) {
            return Vec::new();
        }
        let path = twig_generator_path(&document.uri);
        if is_administration_twig_path(&path) {
            return Vec::new();
        }
        let current = match document.tree() {
            Some(tree) => swlsp_twig::parse_twig_tree(&path, Some(tree), document.line_index()),
            None => swlsp_twig::parse_twig(&path, document.source()),
        };
        let uri = &request.params.text_document.uri;
        let action = |title: &str, command: &str| protocol::CodeAction {
            title: title.to_owned(),
            kind: Some(CodeActionKind::REFACTOR_REWRITE),
            command: Some(protocol::CommandAction {
                title: title.to_owned(),
                command: command.to_owned(),
                arguments: vec![Value::from(uri.clone())],
            }),
            ..protocol::CodeAction::default()
        };
        if current.extends_file.is_empty() {
            if matches!(twig_index.has_other_template_file(&path), Ok(true)) {
                return vec![action(
                    "Symfony: Add Twig extends",
                    GENERATE_TWIG_EXTENDS_ACTION,
                )];
            }
            return Vec::new();
        }
        match self.twig_block_candidates(&current) {
            Ok(blocks) if !blocks.is_empty() => vec![action(
                "Symfony: Override Twig blocks",
                GENERATE_TWIG_BLOCKS_ACTION,
            )],
            _ => Vec::new(),
        }
    }
}

impl CommandProvider for TwigTemplateGeneratorProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let mut result: BTreeMap<String, CommandFunc> = BTreeMap::new();
        let mut add = |name: &str,
                       handler: fn(
            &TwigTemplateGeneratorProvider,
            &Context,
            Option<&Value>,
        ) -> Result<Value, BoxError>| {
            let provider = self.clone();
            result.insert(
                name.to_owned(),
                Arc::new(move |ctx, args| handler(&provider, ctx, args)),
            );
        };
        add(TWIG_EXTENDS_CANDIDATES_COMMAND, |p, ctx, raw| {
            Ok(serde_json::to_value(
                p.get_twig_extends_candidates(ctx, raw)?,
            )?)
        });
        add(GENERATE_TWIG_EXTENDS_COMMAND, |p, ctx, raw| {
            Ok(serde_json::to_value(p.generate_twig_extends(ctx, raw)?)?)
        });
        add(TWIG_BLOCK_CANDIDATES_COMMAND, |p, ctx, raw| {
            Ok(serde_json::to_value(
                p.get_twig_block_candidates(ctx, raw)?,
            )?)
        });
        add(GENERATE_TWIG_BLOCKS_COMMAND, |p, ctx, raw| {
            Ok(serde_json::to_value(p.generate_twig_blocks(ctx, raw)?)?)
        });
        result
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(default)]
pub(crate) struct TwigTemplateGeneratorRequest {
    #[serde(rename = "fileUri", deserialize_with = "nullable")]
    pub file_uri: String,
    #[serde(
        skip_serializing_if = "String::is_empty",
        deserialize_with = "nullable"
    )]
    pub source: String,
    #[serde(
        skip_serializing_if = "String::is_empty",
        deserialize_with = "nullable"
    )]
    pub template: String,
    #[serde(
        rename = "selectedBlocks",
        skip_serializing_if = "Vec::is_empty",
        deserialize_with = "nullable"
    )]
    pub selected_blocks: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub(crate) struct TwigTemplateCandidatesResponse {
    pub templates: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub(crate) struct TwigBlockCandidatesResponse {
    pub parent: String,
    pub blocks: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub(crate) struct TwigTemplateGenerationResponse {
    pub content: String,
}

impl TwigTemplateGeneratorProvider {
    pub(crate) fn get_twig_extends_candidates(
        &self,
        ctx: &Context,
        raw: Option<&Value>,
    ) -> Result<TwigTemplateCandidatesResponse, BoxError> {
        let params = decode_twig_template_generator_request(ctx, raw)?;
        let templates = self.twig_extends_candidates(&twig_generator_path(&params.file_uri))?;
        Ok(TwigTemplateCandidatesResponse { templates })
    }

    pub(crate) fn generate_twig_extends(
        &self,
        ctx: &Context,
        raw: Option<&Value>,
    ) -> Result<TwigTemplateGenerationResponse, BoxError> {
        let params = decode_twig_template_generator_request(ctx, raw)?;
        let path = twig_generator_path(&params.file_uri);
        let current = swlsp_twig::parse_twig(&path, &params.source);
        if !current.extends_file.is_empty() {
            return Err(format!(
                "template already extends {}",
                swlsp_uriutil::go_quote(&current.extends_file)
            )
            .into());
        }
        let templates = self.twig_extends_candidates(&path)?;
        let trimmed = params.template.trim();
        let selected = trimmed.strip_prefix('/').unwrap_or(trimmed);
        if !templates.iter().any(|template| template == selected) {
            return Err(format!(
                "twig template {} is no longer available",
                swlsp_uriutil::go_quote(&params.template)
            )
            .into());
        }
        Ok(TwigTemplateGenerationResponse {
            content: format!(
                "{{% extends '{}' %}}\n",
                escape_twig_single_quoted(selected)
            ),
        })
    }

    pub(crate) fn get_twig_block_candidates(
        &self,
        ctx: &Context,
        raw: Option<&Value>,
    ) -> Result<TwigBlockCandidatesResponse, BoxError> {
        let params = decode_twig_template_generator_request(ctx, raw)?;
        let current =
            swlsp_twig::parse_twig(&twig_generator_path(&params.file_uri), &params.source);
        let blocks = self.twig_block_candidates(&current)?;
        Ok(TwigBlockCandidatesResponse {
            parent: current.extends_file,
            blocks,
        })
    }

    pub(crate) fn generate_twig_blocks(
        &self,
        ctx: &Context,
        raw: Option<&Value>,
    ) -> Result<TwigTemplateGenerationResponse, BoxError> {
        let params = decode_twig_template_generator_request(ctx, raw)?;
        let current =
            swlsp_twig::parse_twig(&twig_generator_path(&params.file_uri), &params.source);
        let available = self.twig_block_candidates(&current)?;
        let available_set: HashMap<String, &String> = available
            .iter()
            .map(|name| (go_to_lower(name), name))
            .collect();
        let mut seen = HashSet::new();
        let mut selected: Vec<String> = Vec::new();
        for name in &params.selected_blocks {
            let key = go_to_lower(name.trim());
            if seen.contains(&key) {
                continue;
            }
            let Some(canonical) = available_set.get(&key) else {
                return Err(format!(
                    "twig block {} is no longer available from {}",
                    swlsp_uriutil::go_quote(name),
                    current.extends_file
                )
                .into());
            };
            selected.push((*canonical).clone());
            seen.insert(key);
        }
        if selected.is_empty() {
            return Err("select at least one Twig block".into());
        }
        selected.sort_by_key(|name| go_to_lower(name));
        let blocks: Vec<String> = selected
            .iter()
            .enumerate()
            .map(|(index, name)| {
                let body = if index == 0 { "$0" } else { "" };
                format!("{{% block {name} %}}\n    {body}\n{{% endblock %}}")
            })
            .collect();
        Ok(TwigTemplateGenerationResponse {
            content: blocks.join("\n\n") + "\n",
        })
    }

    fn twig_extends_candidates(&self, current_path: &str) -> Result<Vec<String>, BoxError> {
        let Some(twig_index) = self.twig_index.as_ref() else {
            return Err("twig template generator is unavailable".into());
        };
        let templates = twig_index.get_all_template_files()?;
        let current_names: HashSet<String> = swlsp_twig::template_names(current_path)
            .iter()
            .map(|name| go_to_lower(name))
            .collect();
        let mut unique: HashMap<String, String> = HashMap::new();
        for template in templates {
            let trimmed = template.trim();
            let template = trimmed.strip_prefix('/').unwrap_or(trimmed);
            if template.is_empty() {
                continue;
            }
            let key = go_to_lower(template);
            if current_names.contains(&key) {
                continue;
            }
            unique.insert(key, template.to_owned());
        }
        let mut result: Vec<(String, String)> = unique.into_iter().collect();
        result.sort_by(|left, right| left.0.cmp(&right.0));
        Ok(result.into_iter().map(|(_, template)| template).collect())
    }

    fn twig_block_candidates(&self, current: &TwigFile) -> Result<Vec<String>, BoxError> {
        let Some(twig_index) = self.twig_index.as_ref() else {
            return Err("twig template generator is unavailable".into());
        };
        if current.extends_file.is_empty() {
            return Ok(Vec::new());
        }
        let blocks = twig_index.get_template_blocks(&[current.extends_file.as_str()])?;
        let mut unique: HashMap<String, String> = HashMap::new();
        for block in blocks {
            if current
                .blocks
                .keys()
                .any(|name| equal_fold(name, &block.name))
            {
                continue;
            }
            unique.entry(go_to_lower(&block.name)).or_insert(block.name);
        }
        let mut result: Vec<(String, String)> = unique.into_iter().collect();
        result.sort_by(|left, right| left.0.cmp(&right.0));
        Ok(result.into_iter().map(|(_, name)| name).collect())
    }
}

/// Go `decodeTwigTemplateGeneratorRequest`.
fn decode_twig_template_generator_request(
    ctx: &Context,
    raw: Option<&Value>,
) -> Result<TwigTemplateGeneratorRequest, BoxError> {
    let params: TwigTemplateGeneratorRequest = decode_request(raw)?;
    ctx.check()?;
    if params.file_uri.trim().is_empty() {
        return Err("missing Twig file URI".into());
    }
    Ok(params)
}

/// Go `decodeSymfonyGeneratorRequest`, where a JSON `null` decodes to the
/// zero value like `encoding/json`.
pub(crate) fn decode_request<T: serde::de::DeserializeOwned + Default>(
    raw: Option<&Value>,
) -> Result<T, BoxError> {
    match raw {
        Some(Value::Null) => Ok(T::default()),
        _ => crate::shared::decode_generator_request(raw),
    }
}

fn twig_generator_path(uri: &str) -> String {
    swlsp_uriutil::path(uri).unwrap_or_else(|_| uri.to_owned())
}

#[cfg(test)]
#[path = "tests/twig_template_generator.rs"]
mod tests;
