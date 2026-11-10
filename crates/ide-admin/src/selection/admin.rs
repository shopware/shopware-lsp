//! Selection ranges of Administration documents.

use swlsp_language::LanguageId;
use swlsp_lsp::{Context, ProviderResult, SelectionRangeProvider, SelectionRangeRequest};
use swlsp_lsp_protocol as protocol;

use crate::selection::selection_ranges;

/// Selection ranges for Administration JavaScript/TypeScript, Twig and Vue
/// documents.
#[derive(Debug, Default, Clone, Copy)]
pub struct AdminSelectionRangeProvider;

impl AdminSelectionRangeProvider {
    pub fn new() -> Self {
        Self
    }
}

impl SelectionRangeProvider for AdminSelectionRangeProvider {
    fn get_selection_ranges(
        &self,
        ctx: &Context,
        request: &SelectionRangeRequest,
    ) -> ProviderResult<Vec<protocol::SelectionRange>> {
        if ctx.is_cancelled() {
            return Ok(Vec::new());
        }
        let document = &request.document;
        if !matches!(
            document.language,
            Some(LanguageId::JavaScript | LanguageId::Twig | LanguageId::Vue)
        ) {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        selection_ranges(ctx, &root, document.line_index(), &request.params.positions)
    }
}
