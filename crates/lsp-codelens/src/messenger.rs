//! Messenger message and handler navigation lenses.

use std::sync::Arc;

use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_messenger::{Index, Occurrence, OccurrenceKind};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::SymbolKind;
use swlsp_uriutil::filepath;

use crate::{
    cancelled, related_lens, related_protocol_range, related_source_line, related_target,
    sort_related_code_lenses, unique_related_targets,
};

pub struct MessengerCodeLensProvider {
    index: Option<Arc<Index>>,
    php: Option<PhpIndex>,
}

impl MessengerCodeLensProvider {
    pub fn new(index: Option<Arc<Index>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl CodeLensProvider for MessengerCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let (Some(index), Some(php)) = (&self.index, &self.php) else {
            return Ok(Vec::new());
        };
        let document = &request.document;
        if document.root().is_none() {
            return Ok(Vec::new());
        }
        let path = swlsp_uriutil::path(&request.params.text_document.uri)?;
        if !filepath::ext(&path).eq_ignore_ascii_case(".php") {
            return Ok(Vec::new());
        }
        let Some(context) = swlsp_lsp_phpanalysis::for_document(php, document)? else {
            return Ok(Vec::new());
        };
        let lines = document.line_index();
        let mut result = Vec::new();
        for symbol in &context.document.symbols {
            cancelled(ctx)?;
            match symbol.kind {
                SymbolKind::Class | SymbolKind::Interface | SymbolKind::Enum => {
                    let Some(message) = index.message(&symbol.fully_qualified)? else {
                        continue;
                    };
                    let targets = messenger_occurrence_targets(&message.occurrences);
                    if targets.is_empty() {
                        continue;
                    }
                    result.push(related_lens(
                        related_protocol_range(symbol.selection_range, lines),
                        format!(
                            "Messenger · {} handler(s) · {} dispatch(es)",
                            message.handlers().len(),
                            message.dispatches().len()
                        ),
                        targets,
                    ));
                }
                SymbolKind::Method => {
                    let Some(class) = context.snapshot.symbol(symbol.container.as_str()) else {
                        continue;
                    };
                    let messages =
                        index.messages_for_handler(&class.fully_qualified, &symbol.name)?;
                    if messages.is_empty() {
                        continue;
                    }
                    let mut targets = Vec::new();
                    for message in &messages {
                        if let Some(declaration) = php.find_class(&message.name) {
                            let range = if declaration.selection_range.is_empty() {
                                declaration.range
                            } else {
                                declaration.selection_range
                            };
                            targets.push(related_target(
                                &declaration.path,
                                related_source_line(&declaration.path, range.start().into()),
                            ));
                        }
                        targets.extend(messenger_occurrence_targets(&message.dispatches()));
                    }
                    let targets = unique_related_targets(targets);
                    if targets.is_empty() {
                        continue;
                    }
                    let title = if messages.len() > 1 {
                        format!("Handles {} Messenger messages", messages.len())
                    } else {
                        "Handles Messenger message".to_owned()
                    };
                    result.push(related_lens(
                        related_protocol_range(symbol.selection_range, lines),
                        title,
                        targets,
                    ));
                }
                _ => {}
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

fn messenger_occurrence_targets(occurrences: &[Occurrence]) -> Vec<String> {
    unique_related_targets(
        occurrences
            .iter()
            .map(|occurrence| {
                let range = if occurrence.kind == OccurrenceKind::Handler
                    && !occurrence.handler_range.is_empty()
                {
                    occurrence.handler_range
                } else if !occurrence.message_range.is_empty() {
                    occurrence.message_range
                } else {
                    occurrence.range
                };
                related_target(
                    &occurrence.file,
                    related_source_line(&occurrence.file, range.start().into()),
                )
            })
            .collect(),
    )
}
