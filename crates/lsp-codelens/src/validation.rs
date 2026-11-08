//! Constraint ↔ validator and validator-message translation lenses.

use std::sync::Arc;

use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_PROPERTY_DECLARATION;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::SymbolKind;
use swlsp_translation::{Index, PhpReferenceKind};

use crate::{
    cancelled, related_lens, related_protocol_range, related_source_line, related_target,
    sort_related_code_lenses, unique_related_targets,
};

pub struct ValidationCodeLensProvider {
    php: Option<PhpIndex>,
    translations: Option<Arc<Index>>,
}

impl ValidationCodeLensProvider {
    pub fn new(php: Option<PhpIndex>, translations: Option<Arc<Index>>) -> Self {
        Self { php, translations }
    }
}

impl CodeLensProvider for ValidationCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let Some(php) = &self.php else {
            return Ok(Vec::new());
        };
        let document = &request.document;
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let uri = &request.params.text_document.uri;
        if !uri.to_ascii_lowercase().ends_with(".php") {
            return Ok(Vec::new());
        }
        swlsp_uriutil::path(uri)?;
        let Some(context) = swlsp_lsp_phpanalysis::context_for_document(php, document)? else {
            return Ok(Vec::new());
        };
        let lines = document.line_index();
        let mut result = Vec::new();
        for class in &context.document.symbols {
            if class.kind != SymbolKind::Class && class.kind != SymbolKind::Interface {
                continue;
            }
            let Some(counterpart) = swlsp_validation::counterpart_class(php, class) else {
                continue;
            };
            let title = if class.fully_qualified.ends_with("Validator") {
                "Open constraint"
            } else {
                "Open validator"
            };
            result.push(related_lens(
                related_protocol_range(class.selection_range, lines),
                title,
                vec![related_target(
                    &counterpart.path,
                    related_source_line(
                        &counterpart.path,
                        counterpart.selection_range.start().into(),
                    ),
                )],
            ));
        }
        if let Some(translations) = &self.translations {
            for reference in swlsp_translation::php_references(&root) {
                cancelled(ctx)?;
                if reference.php_kind != PhpReferenceKind::ValidatorMessage
                    || reference
                        .container
                        .as_ref()
                        .is_none_or(|container| container.kind() != PHP_PROPERTY_DECLARATION)
                    || !swlsp_translation::validate_php_reference(
                        Some(&context),
                        &reference,
                        Some(php),
                    )
                {
                    continue;
                }
                let targets = unique_related_targets(
                    translations
                        .messages_for("validators", &reference.key)?
                        .iter()
                        .map(|message| related_target(&message.file, i64::from(message.line) + 1))
                        .collect(),
                );
                if targets.is_empty() {
                    continue;
                }
                let Some(node) = reference.node.as_ref() else {
                    continue;
                };
                let title = if targets.len() > 1 {
                    format!("Open {} validator translations", targets.len())
                } else {
                    "Open validator translation".to_owned()
                };
                result.push(related_lens(
                    related_protocol_range(phpquery::string_content_range(node), lines),
                    title,
                    targets,
                ));
            }
        }
        sort_related_code_lenses(&mut result);
        Ok(result)
    }

    fn resolve_code_lens(
        &self,
        _ctx: &Context,
        lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        Ok(Some(lens.clone()))
    }
}
