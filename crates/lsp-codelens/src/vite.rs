//! Vite entry lenses on entry target files.

use std::sync::Arc;

use swlsp_asset::{Index, ReferenceKind};
use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol::{self as protocol, Range};

use crate::{
    cancelled, lower_ext, related_lens, related_source_line, related_target, unique_related_targets,
};

pub struct ViteCodeLensProvider {
    index: Option<Arc<Index>>,
}

impl ViteCodeLensProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

impl CodeLensProvider for ViteCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let Some(index) = &self.index else {
            return Ok(Vec::new());
        };
        let path = swlsp_uriutil::path(&request.params.text_document.uri)?;
        if !matches!(lower_ext(&path).as_str(), ".js" | ".ts") {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        for entry in index.vite_entries_for_target(&path)? {
            cancelled(ctx)?;
            let mut targets = vec![related_target(
                &entry.file,
                related_source_line(&entry.file, entry.range.start().into()),
            )];
            for usage in index.usages(&entry.name, ReferenceKind::ViteEntry)? {
                targets.push(related_target(
                    &usage.file,
                    related_source_line(&usage.file, usage.range.start().into()),
                ));
            }
            let targets = unique_related_targets(targets);
            let mut title = format!("Vite entry '{}'", entry.name);
            if !targets.is_empty() {
                title.push_str(&format!(" · {} related", targets.len()));
            }
            result.push(related_lens(Range::default(), title, targets));
        }
        Ok(result)
    }

    fn resolve_code_lens(
        &self,
        _ctx: &Context,
        lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        Ok(Some(lens.clone()))
    }
}
