//! Parameter names for Administration calls whose callable contract is
//! already understood by [`AdminSignatureProvider`]. It intentionally adds no
//! independent type-resolution rules.

use std::collections::HashSet;
use std::sync::Arc;

use crate::signature::AdminSignatureProvider;
use swlsp_admin::AdminComponentIndexer;
use swlsp_language::LanguageId;
use swlsp_lsp::{Context, InlayHintProvider, InlayHintRequest, ProviderResult, TextDocument};
use swlsp_lsp_protocol::{self as protocol, InlayHintKind};
use swlsp_parser_js::query as jsquery;
use swlsp_syntax::{SyntaxNodeExt, TextRange};
use swlsp_textutil::lowercase;
use unicode_general_category::{GeneralCategory, get_general_category};

use crate::inlay::{inlay_hint_byte_range, position};

pub struct AdminParameterProvider {
    signatures: AdminSignatureProvider,
}

impl AdminParameterProvider {
    pub fn new(index: Arc<AdminComponentIndexer>) -> Self {
        Self {
            signatures: AdminSignatureProvider::new(index),
        }
    }

    fn hints_for(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        language: Option<LanguageId>,
        range: protocol::Range,
    ) -> ProviderResult<Vec<protocol::InlayHint>> {
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        if language == Some(LanguageId::Vue) {
            let mut hints = self.hints_for(ctx, document, Some(LanguageId::JavaScript), range)?;
            hints.extend(self.hints_for(ctx, document, Some(LanguageId::Twig), range)?);
            return Ok(deduplicate_admin_parameter_hints(hints));
        }
        let path = swlsp_uriutil::path(&document.uri)?;
        if !lowercase(&path.replace('\\', "/")).contains("/resources/app/administration/") {
            return Ok(Vec::new());
        }
        let (range_start, range_end) = inlay_hint_byte_range(document, range);
        let mut result = Vec::new();
        match language {
            Some(LanguageId::JavaScript) => {
                for call in jsquery::iter_calls(&root, &[]) {
                    if ctx.is_cancelled() {
                        return Ok(result);
                    }
                    let argument_ranges: Vec<TextRange> = jsquery::iter_arguments(&call)
                        .map(|argument| {
                            argument
                                .children()
                                .next()
                                .unwrap_or(argument)
                                .range_trimmed_trivia()
                        })
                        .collect();
                    if argument_ranges.is_empty()
                        || !admin_inlay_range_contains_argument(
                            &argument_ranges,
                            range_start,
                            range_end,
                        )
                    {
                        continue;
                    }
                    let start = u32::from(call.text_range().start());
                    let help = self.signatures.get_signature_help_at_offset(
                        ctx,
                        document,
                        Some(call.clone()),
                        start,
                    )?;
                    for (index, range) in argument_ranges.into_iter().enumerate() {
                        append_admin_parameter_hint(
                            &mut result,
                            document,
                            help.as_ref(),
                            index,
                            range,
                            range_start,
                            range_end,
                        );
                    }
                }
            }
            Some(LanguageId::Twig) => {
                for call in swlsp_admin::twig_vue_calls(&root, document.source()) {
                    if ctx.is_cancelled() {
                        return Ok(result);
                    }
                    if call.call.filter
                        || call.arguments.is_empty()
                        || !admin_inlay_range_contains_argument(
                            &call.arguments,
                            range_start,
                            range_end,
                        )
                    {
                        continue;
                    }
                    let help = self.signatures.get_signature_help_at_offset(
                        ctx,
                        document,
                        None,
                        call.open_paren + 1,
                    )?;
                    for (index, range) in call.arguments.iter().enumerate() {
                        append_admin_parameter_hint(
                            &mut result,
                            document,
                            help.as_ref(),
                            index,
                            *range,
                            range_start,
                            range_end,
                        );
                    }
                }
            }
            _ => return Ok(Vec::new()),
        }
        result.sort_by(|left, right| {
            left.position
                .line
                .cmp(&right.position.line)
                .then(left.position.character.cmp(&right.position.character))
                .then_with(|| inlay_label_text(&left.label).cmp(inlay_label_text(&right.label)))
        });
        Ok(deduplicate_admin_parameter_hints(result))
    }
}

impl InlayHintProvider for AdminParameterProvider {
    fn get_inlay_hints(
        &self,
        ctx: &Context,
        request: &InlayHintRequest,
    ) -> ProviderResult<Vec<protocol::InlayHint>> {
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        self.hints_for(
            ctx,
            &request.document,
            request.document.language,
            request.params.range,
        )
    }
}

fn admin_inlay_range_contains_argument(arguments: &[TextRange], start: u32, end: u32) -> bool {
    arguments.iter().any(|argument| {
        let argument_start = u32::from(argument.start());
        !argument.is_empty() && argument_start >= start && argument_start <= end
    })
}

fn append_admin_parameter_hint(
    result: &mut Vec<protocol::InlayHint>,
    document: &TextDocument,
    help: Option<&protocol::SignatureHelp>,
    argument_index: usize,
    argument_range: TextRange,
    range_start: u32,
    range_end: u32,
) {
    let start = u32::from(argument_range.start());
    if argument_range.is_empty()
        || u32::from(argument_range.end()) as usize > document.source().len()
        || start < range_start
        || start > range_end
    {
        return;
    }
    let Some((name, tooltip)) = admin_parameter_hint(help, argument_index) else {
        return;
    };
    let argument = document
        .source()
        .get(usize::from(argument_range.start())..usize::from(argument_range.end()))
        .unwrap_or_default();
    if admin_argument_has_parameter_name(argument, &name) {
        return;
    }
    result.push(protocol::InlayHint {
        position: position(document, start),
        label: format!("{name}:").into(),
        kind: Some(InlayHintKind::PARAMETER),
        tooltip,
        padding_right: true,
        ..Default::default()
    });
}

pub(crate) fn admin_parameter_hint(
    help: Option<&protocol::SignatureHelp>,
    argument_index: usize,
) -> Option<(String, String)> {
    let help = help?;
    if help.signatures.is_empty() {
        return None;
    }
    let mut name = String::new();
    let mut details: Vec<String> = Vec::new();
    let mut seen = HashSet::new();
    for information in &help.signatures {
        let parameter = admin_signature_parameter_at(&information.parameters, argument_index)?;
        let candidate = admin_parameter_name(&parameter.label)?;
        if !name.is_empty() && candidate != name {
            return None;
        }
        name = candidate;
        let mut detail = parameter.label.clone();
        if !information.label.is_empty() {
            detail.push_str(" — ");
            detail.push_str(&information.label);
        }
        if seen.insert(detail.clone()) {
            details.push(detail);
        }
    }
    if name.is_empty() {
        return None;
    }
    Some((name, details.join("\n")))
}

fn admin_signature_parameter_at(
    parameters: &[protocol::ParameterInformation],
    argument_index: usize,
) -> Option<&protocol::ParameterInformation> {
    if let Some(parameter) = parameters.get(argument_index) {
        return Some(parameter);
    }
    parameters
        .last()
        .filter(|parameter| parameter.label.trim().starts_with("..."))
}

fn is_letter(value: char) -> bool {
    matches!(
        get_general_category(value),
        GeneralCategory::UppercaseLetter
            | GeneralCategory::LowercaseLetter
            | GeneralCategory::TitlecaseLetter
            | GeneralCategory::ModifierLetter
            | GeneralCategory::OtherLetter
    )
}

fn is_digit(value: char) -> bool {
    get_general_category(value) == GeneralCategory::DecimalNumber
}

fn admin_parameter_name(label: &str) -> Option<String> {
    let colon = label.find(':')?;
    if colon == 0 {
        return None;
    }
    let name = label[..colon].trim();
    let name = name.strip_prefix("...").unwrap_or(name);
    let name = name.strip_suffix('?').unwrap_or(name);
    let name = name.trim();
    if name.is_empty() {
        return None;
    }
    for (index, value) in name.chars().enumerate() {
        let valid =
            value == '_' || value == '$' || is_letter(value) || (index > 0 && is_digit(value));
        if !valid {
            return None;
        }
    }
    Some(name.to_string())
}

fn admin_argument_has_parameter_name(argument: &str, name: &str) -> bool {
    let value = argument.trim();
    value == name && admin_parameter_name(&format!("{value}: unknown")).is_some()
}

fn deduplicate_admin_parameter_hints(hints: Vec<protocol::InlayHint>) -> Vec<protocol::InlayHint> {
    let mut seen = HashSet::new();
    hints
        .into_iter()
        .filter(|hint| {
            seen.insert((
                inlay_label_text(&hint.label).to_string(),
                hint.position.line,
                hint.position.character,
            ))
        })
        .collect()
}

fn inlay_label_text(label: &protocol::InlayHintLabel) -> &str {
    match label {
        protocol::InlayHintLabel::String(text) => text,
        protocol::InlayHintLabel::Parts(_) => "",
    }
}
