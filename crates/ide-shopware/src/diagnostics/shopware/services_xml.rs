//! Deprecated XML configuration: the `services.xml` and `routes.xml`
//! loaded by convention, and XML files a `services.php` or `routes.php`
//! imports.

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use swlsp_lsp::{BoxError, Context, DiagnosticId, Problem, TextDocument};
use swlsp_lsp_protocol::{DiagnosticSeverity, DiagnosticTag};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_xml::query as xmlquery;
use swlsp_syntax::{TextRange, TextSize};
use swlsp_uriutil::filepath;

use crate::diagnostics::ProblemAnalyzer;

pub const SERVICES_XML_DEPRECATED_CODE: &str = "symfony.services_xml.deprecated";
pub const ROUTES_XML_DEPRECATED_CODE: &str = "symfony.routes_xml.deprecated";

/// The two configuration formats, with what is said about each.
#[derive(Clone, Copy)]
struct Format {
    routes: bool,
    code: &'static str,
    /// `services` or `routes`: the stem of the convention-loaded file.
    stem: &'static str,
    subject: &'static str,
}

const FORMATS: [Format; 2] = [
    Format {
        routes: false,
        code: SERVICES_XML_DEPRECATED_CODE,
        stem: "services",
        subject: "service",
    },
    Format {
        routes: true,
        code: ROUTES_XML_DEPRECATED_CODE,
        stem: "routes",
        subject: "route",
    },
];

/// The format of the convention-loaded `Resources/config/<stem>.<extension>`
/// at `uri`.
fn convention_loaded_format(uri: &str, extension: &str) -> Option<Format> {
    FORMATS
        .into_iter()
        .find(|format| is_convention_loaded_config(uri, &format!("{}.{extension}", format.stem)))
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ServicesXmlDeprecationPayload {
    #[serde(default)]
    pub convertible: bool,
    /// A routes file rather than a services file.
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub routes: bool,
}

/// An XML file imported by a PHP service configuration.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ServicesXmlImportPayload {
    /// The imported path as written, relative to the importing file.
    pub resource: String,
    /// A routes file rather than a services file.
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub routes: bool,
}

/// Reports `->import('file.xml')` in a convention-loaded
/// `Resources/config/services.php` or `routes.php`: Shopware 6.8 no longer
/// loads XML configuration, and nothing else points at these files.
///
/// The imported file is not read here, so whether it converts is decided
/// when the fix is built.
#[derive(Debug, Clone, Copy, Default)]
pub struct ServicesXmlImportAnalyzer;

impl ServicesXmlImportAnalyzer {
    pub fn new() -> Self {
        Self
    }
}

impl ProblemAnalyzer for ServicesXmlImportAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(0)
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        if document.language != Some(swlsp_language::LanguageId::Php)
            || !document.source().contains(".xml")
        {
            return Ok(Vec::new());
        }
        let Some(format) = convention_loaded_format(&document.uri, "php") else {
            return Ok(Vec::new());
        };
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let mut problems = Vec::new();
        for call in phpquery::nodes(&root, &[swlsp_parser_php::syntax::PHP_MEMBER_CALL]) {
            ctx.check()?;
            if !swlsp_textutil::eq_ignore_case(&phpquery::call_method_name(&call), "import") {
                continue;
            }
            // The whole argument: a path built by concatenation is not known.
            let Some(literal) = phpquery::argument_expression(&call, 0)
                .filter(|argument| argument.kind() == swlsp_parser_php::syntax::PHP_STRING)
            else {
                continue;
            };
            let resource = phpquery::string_value(&literal);
            if !is_local_xml_resource(&resource) {
                continue;
            }
            problems.push(Problem {
                id: DiagnosticId::from(format.code),
                range: phpquery::string_content_range(&literal),
                element: Some(literal.into()),
                message: format!(
                    "Symfony XML {} configuration is deprecated; convert {resource} to YAML",
                    format.subject
                ),
                severity: Some(DiagnosticSeverity::WARNING),
                source: "symfony".to_owned(),
                tags: vec![DiagnosticTag::DEPRECATED],
                payload: Some(serde_json::to_value(ServicesXmlImportPayload {
                    resource,
                    routes: format.routes,
                })?),
                ..Problem::default()
            });
        }
        Ok(problems)
    }
}

/// A single XML file below the importing file: no glob, no absolute path,
/// no bundle or parameter reference.
pub(crate) fn is_local_xml_resource(resource: &str) -> bool {
    resource.to_lowercase().ends_with(".xml")
        && !resource.starts_with(['/', '@', '%'])
        && !resource.contains(['*', '?', '[', '{', '\\', ':'])
}

/// Mirrors shopware-cli's platform-plugin validation. Only convention-loaded
/// `Resources/config/services.xml` and `routes.xml` files are flagged; arbitrary XML files may
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
        let Some(format) = convention_loaded_format(&document.uri, "xml") else {
            return Ok(Vec::new());
        };
        ctx.check()?;
        let root_name = if format.routes { "routes" } else { "container" };
        let convertible = if format.routes {
            swlsp_symfony::parse_routes_xml(document.source())
                .is_ok_and(|routes| swlsp_symfony::convert_routes_to_yaml(&routes).is_ok())
        } else {
            swlsp_symfony::parse_services_xml(document.source())
                .is_ok_and(|container| swlsp_symfony::convert_container_to_yaml(&container).is_ok())
        };

        let source_len = document.source().len() as u32;
        let mut element: swlsp_syntax::SyntaxElement = root.clone().into();
        let mut range = TextRange::new(TextSize::from(0), TextSize::from(source_len.min(1)));
        if let Some(container) = xmlquery::elements(&root, &[root_name]).into_iter().next() {
            range = container.text_range();
            if let Some(offset) = container.text().to_string().find(root_name) {
                let start = range.start() + TextSize::from(offset as u32);
                range = TextRange::new(start, start + TextSize::of(root_name));
            }
            element = container.into();
        }
        let payload = serde_json::to_value(ServicesXmlDeprecationPayload {
            convertible,
            routes: format.routes,
        })?;
        Ok(vec![Problem {
            id: DiagnosticId::from(format.code),
            range,
            element: Some(element),
            message: format!(
                "Symfony {stem}.xml is deprecated; migrate to {stem}.yaml",
                stem = format.stem
            ),
            severity: Some(DiagnosticSeverity::WARNING),
            source: "symfony".to_owned(),
            tags: vec![DiagnosticTag::DEPRECATED],
            payload: Some(payload),
            ..Problem::default()
        }])
    }
}

/// Whether `uri` is `Resources/config/<name>`, which Shopware loads without
/// being told to.
fn is_convention_loaded_config(uri: &str, name: &str) -> bool {
    let Ok(path) = swlsp_uriutil::path(uri) else {
        return false;
    };
    let normalized = filepath::to_slash(&filepath::clean(&path)).to_lowercase();
    normalized.ends_with(&format!("/resources/config/{name}"))
}
