//! Missing Messenger messages and handler methods.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_messenger::{Reference, ReferenceRole, public_handler_methods, reference_at};
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_parser_xml::syntax::XML_ATTRIBUTE;
use swlsp_parser_yaml::syntax::YAML_SCALAR;
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};

use super::{lower_ext, symfony_warning};
use crate::common::value_node_text_range;
use crate::{DeclaredAnalyzer, ProblemAnalyzer};

pub const MISSING_MESSENGER_MESSAGE_CODE: &str = "symfony.messenger.message.missing";
pub const MISSING_MESSENGER_HANDLER_METHOD_CODE: &str = "symfony.messenger.handler_method.missing";

pub struct MessengerAnalyzer {
    php_index: Option<PhpIndex>,
}

impl MessengerAnalyzer {
    pub fn new(php_index: Option<PhpIndex>) -> Self {
        Self { php_index }
    }

    fn handler_diagnostic(&self, index: &PhpIndex, reference: &Reference) -> Option<Problem> {
        if reference.name.is_empty()
            || reference.class.is_empty()
            || !index
                .find_methods(&reference.class, &reference.name)
                .is_empty()
        {
            return None;
        }
        index.find_class(&reference.class)?;
        let names: Vec<String> = public_handler_methods(Some(index), &reference.class)
            .iter()
            .map(|method| method.name.to_string())
            .collect();
        Some(symfony_warning(
            MISSING_MESSENGER_HANDLER_METHOD_CODE,
            value_node_text_range(Some(&reference.node), &reference.name),
            format!(
                "Messenger handler method '{}::{}' not found",
                reference.class, reference.name
            ),
            swlsp_suggestion::similar(&reference.name, &names),
        ))
    }
}

impl DeclaredAnalyzer for MessengerAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "symfony.messenger",
        description: "Messenger handlers for unknown messages and handler methods that do not exist.",
        domain: "symfony.messenger",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php, LanguageId::Xml, LanguageId::Yaml],
        source: "symfony",
        codes: &[
            MISSING_MESSENGER_HANDLER_METHOD_CODE,
            MISSING_MESSENGER_MESSAGE_CODE,
        ],
    };
}

impl ProblemAnalyzer for MessengerAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::inputs(&self.php_index))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(index), Some(root)) = (&self.php_index, document.root()) else {
            return Ok(Vec::new());
        };
        let path = swlsp_uriutil::path(&document.uri).unwrap_or_default();
        let validation_context = if lower_ext(&path) == ".php" {
            swlsp_lsp_phpanalysis::context_for_document(index, document)?
        } else {
            None
        };
        let mut result = Vec::new();
        for node in messenger_diagnostic_nodes(&path, &root) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let Some(reference) = reference_at(validation_context.as_ref(), &path, &root, &node)
            else {
                continue;
            };
            match reference.role {
                ReferenceRole::Message => {
                    if reference.name.is_empty() || index.find_class(&reference.name).is_some() {
                        continue;
                    }
                    result.push(symfony_warning(
                        MISSING_MESSENGER_MESSAGE_CODE,
                        value_node_text_range(Some(&reference.node), &reference.name),
                        format!("Messenger message class '{}' not found", reference.name),
                        swlsp_suggestion::similar(&reference.name, &index.class_names_view()),
                    ));
                }
                ReferenceRole::HandlerMethod => {
                    result.extend(self.handler_diagnostic(index, &reference));
                }
                ReferenceRole::None => {}
            }
        }
        Ok(result)
    }
}

fn messenger_diagnostic_nodes(path: &str, root: &SyntaxNode) -> Vec<SyntaxNode> {
    let kind = match lower_ext(path).as_str() {
        ".php" => PHP_STRING,
        ".yaml" | ".yml" => YAML_SCALAR,
        ".xml" => XML_ATTRIBUTE,
        _ => return Vec::new(),
    };
    root.descendants_of_kinds(&[kind])
}
