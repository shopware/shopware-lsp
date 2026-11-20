//! Document formatting providers.

use swlsp_language::LanguageId;
use swlsp_lsp::{
    Context, DocumentFormattingProvider, DocumentFormattingRequest,
    DocumentRangeFormattingProvider, DocumentRangeFormattingRequest, ProviderResult,
};
use swlsp_php_format::Options;

/// Formats PHP documents to PER Coding Style 2.0. The style fixes the
/// indentation at four spaces, so the editor's `tabSize` and `insertSpaces`
/// are not used. A document with syntax errors, or with markup outside PHP
/// tags, is left as it is.
#[derive(Debug, Default, Clone, Copy)]
pub struct PhpProvider {
    options: Options,
}

impl PhpProvider {
    pub fn new(options: Options) -> Self {
        Self { options }
    }
}

impl DocumentFormattingProvider for PhpProvider {
    fn format_document(
        &self,
        ctx: &Context,
        request: &DocumentFormattingRequest,
    ) -> ProviderResult<Option<String>> {
        let document = &request.document;
        if document.language != Some(LanguageId::Php) {
            return Ok(None);
        }
        let Some(root) = document.root() else {
            return Ok(None);
        };
        ctx.check()?;
        Ok(swlsp_php_format::try_format_tree(
            document.source(),
            &root,
            !document.parse_errors().is_empty(),
            self.options,
        )
        .ok())
    }
}

/// Formats the selected lines of a PHP document. A change that cannot stand
/// without one outside the selection, like the braces put around a body,
/// takes the construct around it along.
impl DocumentRangeFormattingProvider for PhpProvider {
    fn format_range(
        &self,
        ctx: &Context,
        request: &DocumentRangeFormattingRequest,
    ) -> ProviderResult<Option<String>> {
        let document = &request.document;
        if document.language != Some(LanguageId::Php) {
            return Ok(None);
        }
        let Some(root) = document.root() else {
            return Ok(None);
        };
        ctx.check()?;
        Ok(swlsp_php_format::try_format_range(
            document.source(),
            &root,
            !document.parse_errors().is_empty(),
            request.range.clone(),
            self.options,
        )
        .ok())
    }
}

#[cfg(test)]
mod tests;
