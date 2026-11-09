//! Document highlight providers.

use std::sync::Arc;

use swlsp_admin::AdminComponentIndexer;
use swlsp_lsp::{
    Context, DocumentHighlightProvider, DocumentHighlightRequest, ProviderResult, ReferenceRequest,
    ReferencesProvider,
};
use swlsp_lsp_protocol as protocol;
use swlsp_lsp_reference::AdminReferenceProvider;
use swlsp_uriutil::filepath;

#[cfg(test)]
mod tests;

/// Projects the Administration reference graph onto the current open
/// document. The reference provider owns symbol identity and live-CST
/// occurrence discovery; highlights add no parallel resolver.
pub struct AdminDocumentHighlightProvider {
    references: AdminReferenceProvider,
}

impl AdminDocumentHighlightProvider {
    pub fn new(index: Option<Arc<AdminComponentIndexer>>) -> Self {
        Self {
            references: AdminReferenceProvider::new(index),
        }
    }
}

impl DocumentHighlightProvider for AdminDocumentHighlightProvider {
    fn get_document_highlights(
        &self,
        ctx: &Context,
        request: &DocumentHighlightRequest,
    ) -> ProviderResult<Vec<protocol::DocumentHighlight>> {
        if ctx.is_cancelled() || request.document.is_none() || request.root.is_none() {
            return Ok(Vec::new());
        }
        let path = swlsp_uriutil::path(&request.params.text_document.uri)?;
        let extension = filepath::ext(&path).to_lowercase();
        if !matches!(extension.as_str(), ".js" | ".ts" | ".twig" | ".vue") {
            return Ok(Vec::new());
        }
        let mut params = protocol::ReferenceParams::default();
        params.text_document.uri = request.params.text_document.uri.clone();
        params.position = request.params.position;
        params.context.include_declaration = true;
        let locations = self.references.get_references(
            ctx,
            &ReferenceRequest {
                params,
                syntax: request.syntax.clone(),
            },
        )?;
        let clean_path = filepath::clean(&path);
        Ok(locations
            .into_iter()
            .filter(|location| {
                swlsp_uriutil::path(&location.uri)
                    .is_ok_and(|candidate| filepath::clean(&candidate) == clean_path)
            })
            .map(|location| protocol::DocumentHighlight {
                range: location.range,
                kind: Some(protocol::DocumentHighlightKind::TEXT),
            })
            .collect())
    }
}
