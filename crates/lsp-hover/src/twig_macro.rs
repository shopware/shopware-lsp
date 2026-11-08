//! Twig macro hover.

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php_types::equal_fold;
use swlsp_syntax::SyntaxNode;
use swlsp_twig::{Macro, MacroReference, TwigIndexer};
use swlsp_uriutil::filepath;

use crate::{
    escape_markdown, hover_protocol_range, lower_ext, markdown_hover, request_offset, request_uri,
};

pub struct TwigMacroHoverProvider {
    root: String,
    index: Option<Arc<TwigIndexer>>,
}

impl TwigMacroHoverProvider {
    pub fn new(root: impl Into<String>, index: Option<Arc<TwigIndexer>>) -> Self {
        Self {
            root: root.into(),
            index,
        }
    }
}

fn macros(
    index: &TwigIndexer,
    path: &str,
    root: &SyntaxNode,
    reference: &MacroReference,
) -> Vec<Macro> {
    let mut result = Vec::new();
    for template in &reference.templates {
        if let Ok(found) = index.find_macro(template, &reference.name) {
            result.extend(found);
        }
    }
    let current_templates = swlsp_twig::template_names(path);
    if reference
        .templates
        .iter()
        .any(|template| current_templates.contains(template))
    {
        result.retain(|current| current.file_path != path);
        for current in swlsp_twig::macros_in_document(path, root) {
            if equal_fold(&current.name, &reference.name) {
                result.push(current);
            }
        }
    }
    result
}

impl HoverProvider for TwigMacroHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(index) = self.index.as_deref() else {
            return Ok(None);
        };
        let (Some(_node), Some(root), Some(line_index), Some(offset)) = (
            request.node.as_ref(),
            request.root.as_ref(),
            request.line_index(),
            request_offset(request),
        ) else {
            return Ok(None);
        };
        if lower_ext(request_uri(request)) != ".twig" {
            return Ok(None);
        }
        let path = swlsp_uriutil::path(request_uri(request)).unwrap_or_default();
        let Some(reference) = swlsp_twig::macro_reference_at(&path, root, offset) else {
            return Ok(None);
        };
        let found = macros(index, &path, root, &reference);
        let Some(current) = found.first() else {
            return Ok(None);
        };
        let mut markdown = format!("**Twig macro** `{}`", escape_markdown(&current.signature()));
        if !current.documentation.is_empty() {
            markdown.push_str("\n\n");
            markdown.push_str(&current.documentation);
        }
        let display = filepath::rel(&self.root, &current.file_path)
            .unwrap_or_else(|_| current.file_path.clone());
        if !display.is_empty() {
            let _ = write!(
                markdown,
                "\n\nDeclared in `{}`",
                escape_markdown(&filepath::to_slash(&display))
            );
        }
        let mut usage_count = 0;
        for template in &reference.templates {
            if let Ok(usages) = index.get_macro_usages(template, &reference.name) {
                usage_count += usages.len();
            }
        }
        if usage_count != 0 {
            let _ = write!(markdown, "\n\n{usage_count} indexed use(s)");
        }
        Ok(Some(markdown_hover(
            markdown,
            Some(hover_protocol_range(reference.range, line_index)),
        )))
    }
}
