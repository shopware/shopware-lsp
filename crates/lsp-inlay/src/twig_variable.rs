use swlsp_language::LanguageId;
use swlsp_lsp::{Context, InlayHintProvider, InlayHintRequest, ProviderResult};
use swlsp_lsp_protocol::{self as protocol, InlayHintKind};
use swlsp_php::PhpIndex;

use crate::parts_label;

pub const BROWSE_TWIG_VARIABLES_COMMAND: &str = "shopware.symfony.twigVariables";

pub struct TwigVariableProvider {
    php: Option<PhpIndex>,
}

impl TwigVariableProvider {
    pub fn new(php: Option<PhpIndex>) -> Self {
        Self { php }
    }
}

impl InlayHintProvider for TwigVariableProvider {
    fn get_inlay_hints(
        &self,
        ctx: &Context,
        request: &InlayHintRequest,
    ) -> ProviderResult<Vec<protocol::InlayHint>> {
        let Some(php) = self.php.as_ref() else {
            return Ok(Vec::new());
        };
        let document = &request.document;
        if ctx.is_cancelled() || document.language != Some(LanguageId::Twig) {
            return Ok(Vec::new());
        }
        let start = request.params.range.start;
        if document
            .line_index()
            .offset_utf16(start.line, start.character)
            != 0
        {
            return Ok(Vec::new());
        }
        let Ok(path) = swlsp_uriutil::path(&document.uri) else {
            return Ok(Vec::new());
        };
        let variables = php.twig_template_variables(&swlsp_twig::template_names(&path))?;
        if variables.is_empty() {
            return Ok(Vec::new());
        }
        let mut lines = Vec::with_capacity(variables.len());
        let mut names = Vec::with_capacity(variables.len());
        for variable in &variables {
            let type_name = if variable.ty.is_empty() {
                "unknown"
            } else {
                &variable.ty
            };
            lines.push(format!("{}: {type_name}", variable.name));
            names.push(serde_json::Value::String(variable.name.clone()));
        }
        Ok(vec![protocol::InlayHint {
            position: protocol::Position::default(),
            label: parts_label(vec![protocol::InlayHintLabelPart {
                value: format!("Variables ({})", variables.len()),
                tooltip: "Browse typed Twig variables and insert expressions".to_string(),
                command: Some(protocol::Command {
                    title: "Browse Twig variables".to_string(),
                    command: BROWSE_TWIG_VARIABLES_COMMAND.to_string(),
                    arguments: vec![
                        serde_json::Value::String(document.uri.clone()),
                        serde_json::Value::Array(names),
                    ],
                }),
                location: None,
            }]),
            kind: Some(InlayHintKind::TYPE),
            tooltip: lines.join("\n"),
            padding_right: true,
            ..Default::default()
        }])
    }
}
