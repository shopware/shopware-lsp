//! Document formatting providers.

use swlsp_language::LanguageId;
use swlsp_lsp::{Context, DocumentFormattingProvider, DocumentFormattingRequest, ProviderResult};
use swlsp_parser_twig::formatter::{self, Options};
use swlsp_uriutil::filepath;

/// Formats Twig documents with the native Twig formatter. Administration
/// templates keep `{% block %}` children flush; Storefront templates indent
/// them.
#[derive(Debug, Default, Clone, Copy)]
pub struct TwigProvider;

impl TwigProvider {
    pub fn new() -> Self {
        Self
    }
}

impl DocumentFormattingProvider for TwigProvider {
    fn format_document(
        &self,
        ctx: &Context,
        request: &DocumentFormattingRequest,
    ) -> ProviderResult<Option<String>> {
        let document = &request.document;
        if document.language != Some(LanguageId::Twig) {
            return Ok(None);
        }
        let Some(tree) = document.tree() else {
            return Ok(None);
        };
        ctx.check()?;
        let path = swlsp_uriutil::path(&document.uri)?;
        let options = &request.params.options;
        let formatted = formatter::format_tree(
            tree,
            Options {
                insert_spaces: options.insert_spaces,
                tab_size: options.tab_size as usize,
                twig_block_indent_children: !is_administration_template(&path),
            },
        );
        Ok(Some(formatted))
    }
}

fn is_administration_template(path: &str) -> bool {
    filepath::to_slash(&filepath::clean(path)).contains("/Resources/app/administration/")
}

#[cfg(test)]
mod tests;
