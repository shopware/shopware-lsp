//! Live component event references.

use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_textutil::eq_ignore_case;
use swlsp_twigcomponent::{
    Index, LiveListener, live_event_reference_at_php, live_event_reference_at_twig,
    live_event_references_in_php, live_event_references_in_twig, live_listener_at_php,
    live_listeners_in_php,
};

use crate::reference::{FileLines, document_location, lower_ext, request_offset, unique_locations};

pub struct LiveComponentEventReferenceProvider {
    index: Option<Arc<Index>>,
}

impl LiveComponentEventReferenceProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

impl ReferencesProvider for LiveComponentEventReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (Some(index), Some(root), Some(line_index), Some(offset)) = (
            &self.index,
            &request.root,
            request.line_index(),
            request_offset(request),
        ) else {
            return Ok(Vec::new());
        };
        let path = swlsp_uriutil::path(&request.params.text_document.uri).unwrap_or_default();
        let extension = lower_ext(&path);
        let event = match extension.as_str() {
            ".php" => live_event_reference_at_php(&path, root, offset)
                .map(|reference| reference.name)
                .or_else(|| live_listener_at_php(&path, root, offset).map(|listener| listener.name))
                .unwrap_or_default(),
            ".twig" => live_event_reference_at_twig(&path, root, offset)
                .map(|reference| reference.name)
                .unwrap_or_default(),
            _ => return Ok(Vec::new()),
        };
        if event.is_empty() {
            return Ok(Vec::new());
        }

        let mut files = FileLines::default();
        let mut result = Vec::new();
        for reference in index.live_event_references(&event)? {
            if reference.file == path {
                continue;
            }
            result.extend(files.location(&reference.file, reference.range));
        }
        let current = if extension == ".php" {
            live_event_references_in_php(&path, root)
        } else {
            live_event_references_in_twig(&path, root)
        };
        for reference in current {
            if eq_ignore_case(&reference.name, &event) {
                result.push(document_location(request, line_index, reference.range));
            }
        }
        if request.params.context.include_declaration {
            let mut listeners = index.live_listeners()?;
            if extension == ".php" {
                listeners =
                    merge_current_live_listeners(listeners, live_listeners_in_php(&path, root));
            }
            for listener in &listeners {
                if !eq_ignore_case(&listener.name, &event) {
                    continue;
                }
                if listener.file == path {
                    result.push(document_location(request, line_index, listener.range));
                    continue;
                }
                result.extend(files.location(&listener.file, listener.range));
            }
        }
        Ok(unique_locations(result))
    }
}

fn merge_current_live_listeners(
    indexed: Vec<LiveListener>,
    current: Vec<LiveListener>,
) -> Vec<LiveListener> {
    let current_file = current.first().map(|listener| listener.file.clone());
    let mut result: Vec<LiveListener> = indexed
        .into_iter()
        .filter(|listener| {
            listener.file.is_empty() || current_file.as_deref() != Some(listener.file.as_str())
        })
        .collect();
    result.extend(current);
    result
}
