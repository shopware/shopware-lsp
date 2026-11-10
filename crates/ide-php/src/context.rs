//! The request context of PHP documents.

use swlsp_lsp::{Context, SyntaxContext};
use swlsp_php::PhpIndex;

/// The request's linked PHP analysis. Providers read the memoized analysis
/// from the document themselves; the enricher runs it once per request up
/// front and publishes the state as a context value.
pub fn enrich_php_context(ctx: &Context, index: &PhpIndex, syntax: &SyntaxContext) -> Context {
    let Some(document) = syntax.document.as_deref() else {
        return ctx.clone();
    };
    match swlsp_lsp_phpanalysis::for_document(index, document) {
        Ok(Some(state)) => ctx.with_value(state),
        _ => ctx.clone(),
    }
}
