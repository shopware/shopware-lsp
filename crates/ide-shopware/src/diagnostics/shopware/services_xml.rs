//! Deprecated `services.xml` service definitions.

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use swlsp_lsp::{BoxError, Context, DiagnosticId, Problem, TextDocument};
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};
use swlsp_parser_xml::query as xmlquery;
use swlsp_syntax::{TextRange, TextSize};
use swlsp_uriutil::filepath;

use crate::diagnostics::ProblemAnalyzer;

pub const SERVICES_XML_DEPRECATED_CODE: &str = "symfony.services_xml.deprecated";

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ServicesXmlDeprecationPayload {
    #[serde(default)]
    pub convertible: bool,
}

/// Mirrors shopware-cli's platform-plugin validation. Only convention-loaded
/// `Resources/config/services.xml` files are flagged; arbitrary XML files may
/// be loaded explicitly and cannot be renamed without updating their loader.
#[derive(Debug, Clone, Copy, Default)]
pub struct ServicesXmlDeprecationAnalyzer;

impl ServicesXmlDeprecationAnalyzer {
    pub fn new() -> Self {
        Self
    }
}

impl ProblemAnalyzer for ServicesXmlDeprecationAnalyzer {
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
        if !is_convention_loaded_services_xml(&document.uri) {
            return Ok(Vec::new());
        }
        ctx.check()?;
        let convertible = swlsp_symfony::parse_services_xml(document.source())
            .is_ok_and(|container| swlsp_symfony::convert_container_to_yaml(&container).is_ok());

        let source_len = document.source().len() as u32;
        let mut element: swlsp_syntax::SyntaxElement = root.clone().into();
        let mut range = TextRange::new(TextSize::from(0), TextSize::from(source_len.min(1)));
        if let Some(container) = xmlquery::elements(&root, &["container"]).into_iter().next() {
            range = container.text_range();
            if let Some(offset) = container.text().to_string().find("container") {
                let start = range.start() + TextSize::from(offset as u32);
                range = TextRange::new(start, start + TextSize::of("container"));
            }
            element = container.into();
        }
        let payload = serde_json::to_value(ServicesXmlDeprecationPayload { convertible })?;
        Ok(vec![Problem {
            id: DiagnosticId::from(SERVICES_XML_DEPRECATED_CODE),
            range,
            element: Some(element),
            message: "Symfony services.xml is deprecated; migrate to services.yaml".to_owned(),
            severity: Some(DiagnosticSeverity::WARNING),
            source: "symfony".to_owned(),
            tags: vec![DiagnosticTag::DEPRECATED],
            payload: Some(payload),
            ..Problem::default()
        }])
    }
}

fn is_convention_loaded_services_xml(uri: &str) -> bool {
    let Ok(path) = swlsp_uriutil::path(uri) else {
        return false;
    };
    if !swlsp_textutil::eq_ignore_case(&filepath::base(&path), "services.xml") {
        return false;
    }
    let normalized = filepath::to_slash(&filepath::clean(&path)).to_lowercase();
    normalized.ends_with("/resources/config/services.xml")
}
