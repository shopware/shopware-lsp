//! Signature help for Twig macro calls.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, SignatureHelpProvider, SignatureHelpRequest};
use swlsp_lsp_protocol::{self as protocol, MarkupContent, MarkupKind};
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::{TK_COMMA, TWIG_ARGUMENTS};
use swlsp_syntax::{SyntaxNode, TextSize};
use swlsp_textutil::eq_ignore_case;
use swlsp_twig::{Macro, MacroReference, MacroReferenceRole, TwigIndexer};
use swlsp_uriutil::filepath;

pub struct TwigMacroSignatureProvider {
    index: Arc<TwigIndexer>,
}

impl TwigMacroSignatureProvider {
    pub fn new(index: Arc<TwigIndexer>) -> Self {
        Self { index }
    }
}

impl SignatureHelpProvider for TwigMacroSignatureProvider {
    fn get_signature_help(
        &self,
        _ctx: &Context,
        request: &SignatureHelpRequest,
    ) -> ProviderResult<Option<protocol::SignatureHelp>> {
        let uri = &request.params.text_document.uri;
        let (Some(node), Some(root), Some(lines)) = (
            request.node.as_ref(),
            request.root.as_ref(),
            request.line_index(),
        ) else {
            return Ok(None);
        };
        if filepath::ext(uri).to_lowercase() != ".twig" {
            return Ok(None);
        }
        let Some(call) = twigquery::function_call_at(node) else {
            return Ok(None);
        };
        let path = swlsp_uriutil::path(uri).unwrap_or_default();
        let Some(reference) = macro_call_reference(&path, root, &call) else {
            return Ok(None);
        };
        let mut macros: Vec<Macro> = Vec::new();
        for template in &reference.templates {
            macros.extend(self.index.find_macro(template, &reference.name)?);
        }
        let current_templates = swlsp_twig::template_names(&path);
        if reference
            .templates
            .iter()
            .any(|template| current_templates.contains(template))
        {
            macros.retain(|item| item.file_path != path);
            macros.extend(
                swlsp_twig::macros_in_document(&path, root)
                    .into_iter()
                    .filter(|item| eq_ignore_case(&item.name, &reference.name)),
            );
        }
        if macros.is_empty() {
            return Ok(None);
        }
        let position = request.params.position;
        let offset = lines.offset_utf16(position.line, position.character);
        let active = macro_active_argument(&call, offset);
        let mut result = protocol::SignatureHelp {
            active_parameter: active,
            ..Default::default()
        };
        let mut seen = HashSet::new();
        for item in macros {
            let signature = item.signature();
            if !seen.insert(signature.clone()) {
                continue;
            }
            let mut information = protocol::SignatureInformation {
                label: signature,
                active_parameter: active,
                ..Default::default()
            };
            if !item.documentation.is_empty() {
                information.documentation = Some(markdown(item.documentation));
            }
            for parameter in item.parameters {
                let mut label = parameter.name;
                if !parameter.default.is_empty() {
                    label.push_str(" = ");
                    label.push_str(&parameter.default);
                }
                information.parameters.push(protocol::ParameterInformation {
                    label,
                    documentation: (!parameter.documentation.is_empty())
                        .then(|| markdown(parameter.documentation)),
                });
            }
            result.signatures.push(information);
        }
        if result.signatures.is_empty() {
            return Ok(None);
        }
        Ok(Some(result))
    }
}

pub fn markdown(value: String) -> MarkupContent {
    MarkupContent {
        kind: MarkupKind::Markdown,
        value,
    }
}

fn macro_call_reference(
    path: &str,
    root: &SyntaxNode,
    call: &SyntaxNode,
) -> Option<MacroReference> {
    let range = call.text_range();
    swlsp_twig::macro_references_in_document(path, root)
        .into_iter()
        .find(|reference| {
            reference.role == MacroReferenceRole::Usage
                && reference.range.start() >= range.start()
                && reference.range.end() <= range.end()
        })
}

fn macro_active_argument(call: &SyntaxNode, offset: u32) -> i32 {
    let Some(arguments) = call.children().find(|child| child.kind() == TWIG_ARGUMENTS) else {
        return 0;
    };
    let mut active = 0;
    for token in arguments
        .children_with_tokens()
        .filter_map(|element| element.into_token())
    {
        if token.text_range().start() >= TextSize::from(offset) {
            break;
        }
        if token.kind() == TK_COMMA {
            active += 1;
        }
    }
    active
}
