//! Go `twig_translation_extract_codeaction.go` and
//! `twig_translation_extract_plan.go`: extracts static Twig text into a
//! translation resource. The server owns selection validation, domain
//! inference, escaping and resource edits; clients only prompt.

use std::collections::{BTreeMap, HashSet};
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_language::LanguageId;
use swlsp_lsp::{
    ActionProvider, BoxError, CodeActionRequest, CommandFunc, CommandProvider, Context,
    DocumentSnapshot, WorkspaceEditHost,
};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_parser_twig::syntax::{HTML_STRING_INNER, HTML_TEXT};
use swlsp_php_types::{equal_fold, go_to_lower};
use swlsp_rewrite::{Builder, DocumentPlan, HandleError, WorkspacePlan};
use swlsp_syntax::{LineIndex, SyntaxNode, SyntaxNodeExt, TextRange, TextSize};
use swlsp_translation::{Index, Insertion, insertion_for_source, twig_default_domain_before};
use swlsp_uriutil::{file_uri, filepath};

use crate::admin_twig_override::nullable;
use crate::shared::escape_twig_single_quoted;
use crate::twig_template_generator::decode_request;

pub const EXTRACT_TWIG_TRANSLATION_ACTION: &str = "shopware.symfony.extractTwigTranslation";
pub const PREPARE_TWIG_TRANSLATION_EXTRACTION_COMMAND: &str =
    "shopware/symfony/translation/extract/prepare";
pub const GENERATE_TWIG_TRANSLATION_EXTRACTION_COMMAND: &str =
    "shopware/symfony/translation/extract/generate";

#[derive(Clone)]
pub struct TwigTranslationExtractProvider {
    index: Option<Arc<Index>>,
    pub(crate) host: Option<Arc<dyn WorkspaceEditHost>>,
}

impl TwigTranslationExtractProvider {
    pub fn new(index: Option<Arc<Index>>, host: Option<Arc<dyn WorkspaceEditHost>>) -> Self {
        Self { index, host }
    }
}

impl ActionProvider for TwigTranslationExtractProvider {
    fn code_action_kinds(&self) -> Vec<CodeActionKind> {
        vec![CodeActionKind::REFACTOR_EXTRACT]
    }

    fn get_code_actions(
        &self,
        ctx: &Context,
        request: &CodeActionRequest,
    ) -> Vec<protocol::CodeAction> {
        if ctx.is_cancelled() || self.index.is_none() {
            return Vec::new();
        }
        let Some(document) = request.document.as_ref() else {
            return Vec::new();
        };
        if document.language != Some(LanguageId::Twig) {
            return Vec::new();
        }
        let Some(root) = document.root() else {
            return Vec::new();
        };
        let range = request.params.range;
        if twig_translation_extraction(document.source(), &root, document.line_index(), range)
            .is_none()
        {
            return Vec::new();
        }
        let title = "Symfony: Extract Twig translation";
        vec![protocol::CodeAction {
            title: title.to_owned(),
            kind: Some(CodeActionKind::REFACTOR_EXTRACT),
            command: Some(protocol::CommandAction {
                title: title.to_owned(),
                command: EXTRACT_TWIG_TRANSLATION_ACTION.to_owned(),
                arguments: vec![
                    Value::from(request.params.text_document.uri.clone()),
                    serde_json::to_value(range).unwrap_or(Value::Null),
                ],
            }),
            ..protocol::CodeAction::default()
        }]
    }
}

impl CommandProvider for TwigTranslationExtractProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let prepare = self.clone();
        let generate = self.clone();
        let prepare: CommandFunc =
            Arc::new(move |ctx, raw| Ok(serde_json::to_value(prepare.prepare(ctx, raw)?)?));
        let generate: CommandFunc =
            Arc::new(move |ctx, raw| Ok(serde_json::to_value(generate.generate(ctx, raw)?)?));
        BTreeMap::from([
            (
                PREPARE_TWIG_TRANSLATION_EXTRACTION_COMMAND.to_owned(),
                prepare,
            ),
            (
                GENERATE_TWIG_TRANSLATION_EXTRACTION_COMMAND.to_owned(),
                generate,
            ),
        ])
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(default)]
pub(crate) struct TwigTranslationExtractionRequest {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub version: Option<i32>,
    #[serde(skip_serializing_if = "is_false", deserialize_with = "nullable")]
    pub preview: bool,
    #[serde(
        rename = "targetUris",
        skip_serializing_if = "Vec::is_empty",
        deserialize_with = "nullable"
    )]
    pub target_uris: Vec<String>,
    #[serde(rename = "fileUri", deserialize_with = "nullable")]
    pub file_uri: String,
    #[serde(deserialize_with = "nullable")]
    pub source: String,
    #[serde(deserialize_with = "nullable")]
    pub range: protocol::Range,
    #[serde(
        skip_serializing_if = "String::is_empty",
        deserialize_with = "nullable"
    )]
    pub key: String,
    #[serde(
        skip_serializing_if = "String::is_empty",
        deserialize_with = "nullable"
    )]
    pub domain: String,
}

fn is_false(value: &bool) -> bool {
    !*value
}

#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub(crate) struct TwigTranslationExtractionPreparation {
    #[serde(rename = "workspaceEdits")]
    pub workspace_edits: bool,
    pub text: String,
    pub range: protocol::Range,
    #[serde(rename = "defaultKey", skip_serializing_if = "String::is_empty")]
    pub default_key: String,
    #[serde(rename = "defaultDomain")]
    pub default_domain: String,
    pub domains: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub(crate) struct TwigTranslationExtractionTarget {
    #[serde(rename = "fileUri")]
    pub file_uri: String,
    pub file: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub locale: String,
    pub format: String,
    pub line: u32,
    pub character: u32,
    #[serde(rename = "newText")]
    pub new_text: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub(crate) struct TwigTranslationExtractionEdits {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub edit: Option<protocol::WorkspaceEdit>,
    pub replacement: String,
    pub range: protocol::Range,
    pub targets: Vec<TwigTranslationExtractionTarget>,
}

#[derive(Debug, Clone, Default)]
pub(crate) struct TwigTranslationSelection {
    pub text: String,
    pub start: u32,
    pub end: u32,
    pub active_domain: String,
}

impl TwigTranslationExtractProvider {
    fn translation_index(&self) -> Result<&Index, BoxError> {
        self.index
            .as_deref()
            .ok_or_else(|| "translation index is unavailable".into())
    }

    pub(crate) fn prepare(
        &self,
        ctx: &Context,
        raw: Option<&Value>,
    ) -> Result<TwigTranslationExtractionPreparation, BoxError> {
        let (params, selection) = self.decode_twig_translation_extraction(ctx, raw)?;
        let mut domains = self.translation_index()?.domains()?;
        append_unique_fold(&mut domains, &selection.active_domain);
        domains.sort_by_key(|domain| go_to_lower(domain));
        Ok(TwigTranslationExtractionPreparation {
            workspace_edits: true,
            text: selection.text.clone(),
            range: extraction_protocol_range(&params.source, &selection),
            default_key: default_translation_key(&selection.text),
            default_domain: selection.active_domain,
            domains,
        })
    }

    pub(crate) fn generate(
        &self,
        ctx: &Context,
        raw: Option<&Value>,
    ) -> Result<TwigTranslationExtractionEdits, BoxError> {
        let (mut params, selection) = self.decode_twig_translation_extraction(ctx, raw)?;
        let key = params.key.trim().to_owned();
        let domain = params.domain.trim().to_owned();
        if key.is_empty() {
            return Err("translation key must not be empty".into());
        }
        if key.contains(['\r', '\n', '\0']) {
            return Err("translation key contains invalid characters".into());
        }
        if domain.is_empty() || domain.contains(['\r', '\n', '\0']) {
            return Err("translation domain must not be empty".into());
        }
        params.key = key.clone();
        params.domain = domain.clone();
        let index = self.translation_index()?;
        if index.has_message(&domain, &key)? {
            return Err(format!(
                "translation key {} already exists in domain {}",
                swlsp_uriutil::go_quote(&key),
                swlsp_uriutil::go_quote(&domain)
            )
            .into());
        }
        let insertions = index.insertion_targets(&domain)?;
        if insertions.is_empty() {
            return Err(format!(
                "domain {} has no writable YAML or XLIFF resources",
                swlsp_uriutil::go_quote(&domain)
            )
            .into());
        }
        let mut targets = Vec::with_capacity(insertions.len());
        for mut insertion in insertions {
            // Older clients still receive preview positions, calculated from
            // current snapshots.
            if !params.preview && params.target_uris.is_empty() {
                let snapshot = self
                    .host()?
                    .resolve_document(ctx, &file_uri(&insertion.file))?;
                let Some(mut computed) = insertion_for_source(
                    &insertion.file,
                    snapshot.document.source(),
                    &key,
                    &selection.text,
                ) else {
                    continue;
                };
                computed.locale = insertion.locale.clone();
                insertion = computed;
            }
            targets.push(TwigTranslationExtractionTarget {
                file_uri: file_uri(&insertion.file),
                file: filepath::base(&insertion.file),
                locale: insertion.locale,
                format: insertion.format,
                line: insertion.line,
                character: insertion.character,
                new_text: insertion.new_text,
            });
        }
        let mut replacement = format!("{{{{ '{}'|trans", escape_twig_single_quoted(&key));
        if !equal_fold(&domain, &selection.active_domain) {
            replacement.push_str(&format!("({{}}, '{}')", escape_twig_single_quoted(&domain)));
        }
        replacement.push_str(" }}");
        let edit = if params.target_uris.is_empty() {
            None
        } else {
            Some(self.extraction_edit(ctx, &params, &selection, &replacement, &targets)?)
        };
        Ok(TwigTranslationExtractionEdits {
            edit,
            replacement,
            range: extraction_protocol_range(&params.source, &selection),
            targets,
        })
    }

    fn host(&self) -> Result<&dyn WorkspaceEditHost, BoxError> {
        self.host
            .as_deref()
            .ok_or_else(|| "workspace edit host is unavailable".into())
    }

    fn decode_twig_translation_extraction(
        &self,
        ctx: &Context,
        raw: Option<&Value>,
    ) -> Result<(TwigTranslationExtractionRequest, TwigTranslationSelection), BoxError> {
        let params: TwigTranslationExtractionRequest = decode_request(raw)?;
        ctx.check()?;
        if params.file_uri.trim().is_empty() {
            return Err("missing Twig file URI".into());
        }
        let extension = go_to_lower(filepath::ext(&params.file_uri));
        if extension != ".twig" && extension != ".html" {
            return Err("translation extraction requires a Twig template".into());
        }
        let host = self.host()?;
        let snapshot = host.resolve_document(ctx, &params.file_uri)?;
        check_snapshot(&snapshot, &params)?;
        let Some(root) = snapshot.document.root() else {
            return Err("parse Twig template".into());
        };
        let Some(mut selection) = twig_translation_extraction(
            &params.source,
            &root,
            snapshot.document.line_index(),
            params.range,
        ) else {
            return Err("select static Twig text or an HTML attribute value".into());
        };
        selection.active_domain = twig_default_domain_before(&params.source, selection.start);
        if selection.active_domain.is_empty() {
            selection.active_domain = "messages".to_owned();
        }
        Ok((params, selection))
    }

    /// Go `extractionEdit`: the final plan is built after locale selection,
    /// with no disk writes or positions carried across the prompt.
    fn extraction_edit(
        &self,
        ctx: &Context,
        params: &TwigTranslationExtractionRequest,
        selection: &TwigTranslationSelection,
        replacement: &str,
        targets: &[TwigTranslationExtractionTarget],
    ) -> Result<protocol::WorkspaceEdit, BoxError> {
        let host = self.host()?;
        let source = host.resolve_document(ctx, &params.file_uri)?;
        check_snapshot(&source, params)?;
        let mut builder = Builder::new(source.document.source());
        builder.replace_range(
            TextRange::new(
                TextSize::from(selection.start),
                TextSize::from(selection.end),
            ),
            replacement,
        )?;
        let edits = builder.finish()?;
        let mut plan = WorkspacePlan {
            documents: vec![
                DocumentPlan::new(
                    params.file_uri.clone(),
                    source.version,
                    source.document.source_arc().clone(),
                    edits,
                )
                .with_line_index(source.document.line_index_arc().clone()),
            ],
            ..WorkspacePlan::default()
        };
        let mut allowed: HashSet<&str> = targets
            .iter()
            .map(|target| target.file_uri.as_str())
            .collect();
        for uri in &params.target_uris {
            if !allowed.remove(uri.as_str()) {
                return Err("invalid or duplicate translation target".into());
            }
            let target = host.resolve_document(ctx, uri)?;
            let file = swlsp_uriutil::path(uri)?;
            let Some(insertion) = insertion_for_source(
                &file,
                target.document.source(),
                &params.key,
                &selection.text,
            ) else {
                return Err("translation target does not support insertion".into());
            };
            plan.documents
                .push(translation_insertion_plan(uri, &target, &insertion)?);
        }
        host.workspace_edit(ctx, &plan)
    }
}

fn check_snapshot(
    snapshot: &DocumentSnapshot,
    params: &TwigTranslationExtractionRequest,
) -> Result<(), BoxError> {
    if snapshot.document.source() != params.source
        || params
            .version
            .is_some_and(|version| snapshot.version != Some(version))
    {
        return Err(Box::new(HandleError::Stale));
    }
    Ok(())
}

fn translation_insertion_plan(
    uri: &str,
    target: &DocumentSnapshot,
    insertion: &Insertion,
) -> Result<DocumentPlan, BoxError> {
    let document = &target.document;
    let offset = document
        .line_index()
        .offset_utf16(insertion.line, insertion.character);
    let mut builder = Builder::new(document.source());
    builder.insert(offset, insertion.new_text.clone())?;
    let edits = builder.finish()?;
    Ok(
        DocumentPlan::new(uri, target.version, document.source_arc().clone(), edits)
            .with_line_index(document.line_index_arc().clone()),
    )
}

/// Go `twigTranslationExtraction`: the trimmed static text selected by
/// `range` (or the whole text node at a caret).
pub(crate) fn twig_translation_extraction(
    source: &str,
    root: &SyntaxNode,
    line_index: &LineIndex,
    range: protocol::Range,
) -> Option<TwigTranslationSelection> {
    let mut start = line_index.offset_utf16(range.start.line, range.start.character);
    let mut end = line_index.offset_utf16(range.end.line, range.end.character);
    let length = source.len() as u32;
    if end < start || start > length || end > length {
        return None;
    }
    if start == end {
        let container = twig_extraction_container(root.node_at(TextSize::from(start)))?;
        start = u32::from(container.text_range().start());
        end = u32::from(container.text_range().end());
    } else {
        let container = twig_extraction_container(root.node_at(TextSize::from(start)))?;
        let end_container = twig_extraction_container(root.node_at(TextSize::from(end - 1)));
        if end_container.as_ref() != Some(&container)
            || start < u32::from(container.text_range().start())
            || end > u32::from(container.text_range().end())
        {
            return None;
        }
    }
    let raw_text = source.get(start as usize..end as usize)?;
    let trimmed = raw_text.trim();
    if trimmed.is_empty()
        || trimmed.contains("{{")
        || trimmed.contains("{%")
        || trimmed.contains("{#")
    {
        return None;
    }
    let leading = raw_text.len() - raw_text.trim_start().len();
    start += leading as u32;
    end = start + trimmed.len() as u32;
    Some(TwigTranslationSelection {
        text: trimmed.to_owned(),
        start,
        end,
        active_domain: String::new(),
    })
}

fn twig_extraction_container(node: Option<SyntaxNode>) -> Option<SyntaxNode> {
    node?
        .ancestors()
        .find(|current| matches!(current.kind(), HTML_TEXT | HTML_STRING_INNER))
}

fn extraction_protocol_range(
    source: &str,
    selection: &TwigTranslationSelection,
) -> protocol::Range {
    let line_index = LineIndex::new(source.to_owned());
    let (start_line, start_character) = line_index.position_utf16(selection.start);
    let (end_line, end_character) = line_index.position_utf16(selection.end);
    protocol::Range {
        start: protocol::Position {
            line: start_line,
            character: start_character,
        },
        end: protocol::Position {
            line: end_line,
            character: end_character,
        },
    }
}

fn default_translation_key(value: &str) -> String {
    if value.chars().count() >= 15 {
        return String::new();
    }
    go_to_lower(&value.replace(' ', "."))
}

fn append_unique_fold(values: &mut Vec<String>, value: &str) {
    if value.trim().is_empty() || values.iter().any(|candidate| equal_fold(candidate, value)) {
        return;
    }
    values.push(value.to_owned());
}

#[cfg(test)]
#[path = "tests/twig_translation_extract.rs"]
mod tests;
