//! Port of Go `admin_completion_provider.go`.

use std::sync::Arc;

use swlsp_admin::AdminComponentIndexer;
use swlsp_language::LanguageId;
use swlsp_lsp::{CompletionProvider, CompletionRequest, Context, effective_syntax_language};
use swlsp_lsp_protocol as protocol;

use crate::common::go_to_lower;

/// Completions for Shopware Administration Vue components.
#[derive(Clone, Default)]
pub struct AdminCompletionProvider {
    pub(super) index: Option<Arc<AdminComponentIndexer>>,
}

impl AdminCompletionProvider {
    pub fn new(index: Arc<AdminComponentIndexer>) -> Self {
        Self { index: Some(index) }
    }
}

impl CompletionProvider for AdminCompletionProvider {
    fn get_completions(
        &self,
        _ctx: &Context,
        request: &CompletionRequest,
    ) -> Vec<protocol::CompletionItem> {
        let uri = request.params.text_document.uri.as_str();
        let ext = go_to_lower(swlsp_uriutil::filepath::ext(uri));
        let language_at_cursor = effective_syntax_language(request.language, request.node.as_ref());

        if ext == ".js"
            || ext == ".ts"
            || ext == ".vue" && language_at_cursor == Some(LanguageId::JavaScript)
        {
            if request.node.is_none() {
                return Vec::new();
            }
            return self.js_completions(request);
        }

        if (ext == ".twig" || ext == ".vue" && language_at_cursor == Some(LanguageId::Twig))
            && request.node.is_some()
            && uri.contains("Resources/app/administration")
        {
            return self.twig_completions(request);
        }
        Vec::new()
    }

    fn trigger_characters(&self) -> Vec<String> {
        ["'", "\"", "<", " ", "#", ".", "-"]
            .into_iter()
            .map(String::from)
            .collect()
    }
}
