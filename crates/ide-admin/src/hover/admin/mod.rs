//! Shopware Administration hover for Vue components, registries, and
//! template expressions in JavaScript and Twig.

mod component;
mod javascript;
mod registry;
mod scope;
mod twig;

use std::sync::Arc;

use swlsp_admin::AdminComponentIndexer;
use swlsp_language::LanguageId;
use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult, effective_syntax_language};
use swlsp_lsp_protocol as protocol;
use swlsp_uriutil::filepath;

use crate::hover::{lower_ext, request_uri};

pub(crate) type HoverResult = ProviderResult<Option<protocol::Hover>>;

pub struct AdminHoverProvider {
    pub(crate) index: Arc<AdminComponentIndexer>,
    pub(crate) project_root: String,
}

impl AdminHoverProvider {
    pub fn new(project_root: impl Into<String>, index: Arc<AdminComponentIndexer>) -> Self {
        Self {
            index,
            project_root: project_root.into(),
        }
    }

    /// `path` relative to the project root, without slash
    /// normalization, else unchanged.
    pub(crate) fn make_relative_path(&self, path: &str) -> String {
        if self.project_root.is_empty() {
            return path.to_owned();
        }
        filepath::rel(&self.project_root, path).unwrap_or_else(|_| path.to_owned())
    }
}

impl HoverProvider for AdminHoverProvider {
    fn get_hover(&self, _ctx: &Context, request: &HoverRequest) -> HoverResult {
        let uri = request_uri(request);
        let extension = lower_ext(uri);
        let language = effective_syntax_language(request.language, request.node.as_ref());
        if extension == ".js"
            || extension == ".ts"
            || extension == ".vue" && language == Some(LanguageId::JavaScript)
        {
            let Some(node) = request.node.as_ref() else {
                return Ok(None);
            };
            return self.javascript_hover(request, node);
        }
        if extension == ".twig" || extension == ".vue" && language == Some(LanguageId::Twig) {
            let Some(node) = request.node.as_ref() else {
                return Ok(None);
            };
            if uri.contains("Resources/app/administration") {
                return self.twig_hover(request, node);
            }
        }
        Ok(None)
    }
}

pub(crate) fn sections_hover(sections: Vec<String>) -> Option<protocol::Hover> {
    if sections.is_empty() {
        return None;
    }
    Some(crate::hover::markdown_hover(
        sections.join("\n\n---\n\n"),
        None,
    ))
}

/// The protocol range of a hover span; `None` for an empty span.
pub(crate) fn admin_hover_range(
    request: &HoverRequest,
    range: swlsp_syntax::TextRange,
) -> Option<protocol::Range> {
    let line_index = request.line_index()?;
    if range.is_empty() {
        return None;
    }
    Some(crate::hover::hover_protocol_range(range, line_index))
}

pub(crate) fn admin_template_path(request: &HoverRequest) -> String {
    swlsp_uriutil::path(request_uri(request)).unwrap_or_default()
}
