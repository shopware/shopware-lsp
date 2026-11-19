//! Document formatting providers.

use swlsp_format::{Language, Options};
use swlsp_lsp::{Context, DocumentFormattingProvider, DocumentFormattingRequest, ProviderResult};

/// Formats JSON, JavaScript, TypeScript, CSS and SCSS documents with Biome's
/// formatters. A document with syntax errors is left as it is.
#[derive(Debug, Default, Clone, Copy)]
pub struct BiomeProvider;

impl DocumentFormattingProvider for BiomeProvider {
    fn format_document(
        &self,
        ctx: &Context,
        request: &DocumentFormattingRequest,
    ) -> ProviderResult<Option<String>> {
        let document = &request.document;
        let Some(language) = Language::from_path(&document.uri) else {
            return Ok(None);
        };
        ctx.check()?;
        let options = &request.params.options;
        Ok(swlsp_format::format(
            language,
            document.source(),
            Options::editor(options.tab_size, options.insert_spaces),
        ))
    }
}

#[cfg(test)]
mod tests;
