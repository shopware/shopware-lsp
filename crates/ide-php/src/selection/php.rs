//! Selection ranges of PHP documents.

use swlsp_language::LanguageId;
use swlsp_lsp::{Context, ProviderResult, SelectionRangeProvider, SelectionRangeRequest};
use swlsp_lsp_protocol as protocol;

use crate::selection::selection_ranges;

/// Selection ranges for PHP documents.
#[derive(Debug, Default, Clone, Copy)]
pub struct PhpSelectionRangeProvider;

impl PhpSelectionRangeProvider {
    pub fn new() -> Self {
        Self
    }
}

impl SelectionRangeProvider for PhpSelectionRangeProvider {
    fn get_selection_ranges(
        &self,
        ctx: &Context,
        request: &SelectionRangeRequest,
    ) -> ProviderResult<Vec<protocol::SelectionRange>> {
        ctx.check()?;
        let document = &request.document;
        if document.language != Some(LanguageId::Php) {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        selection_ranges(ctx, &root, document.line_index(), &request.params.positions)
    }
}
