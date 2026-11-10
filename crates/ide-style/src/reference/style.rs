//! CSS class references across SCSS, Twig, HTML and Vue.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_style::{ClassOccurrence, Index, class_at, class_declarations, class_usages};
use swlsp_syntax::{LineIndex, TextRange, TextSize};

use crate::reference::{lower_ext, request_offset, sort_locations};

pub struct StyleClassReferenceProvider {
    index: Option<Arc<Index>>,
}

impl StyleClassReferenceProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

impl ReferencesProvider for StyleClassReferenceProvider {
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
        let Ok(path) = swlsp_uriutil::path(&request.params.text_document.uri) else {
            return Ok(Vec::new());
        };
        if !matches!(
            lower_ext(&path).as_str(),
            ".scss" | ".twig" | ".html" | ".vue"
        ) {
            return Ok(Vec::new());
        }
        let Some(current) = class_at(&path, root, offset) else {
            return Ok(Vec::new());
        };
        if current.name.is_empty() {
            return Ok(Vec::new());
        }
        let mut occurrences = without_path(index.usages(&current.name)?, &path);
        occurrences.extend(
            class_usages(&path, root)
                .into_iter()
                .filter(|usage| usage.name == current.name),
        );
        if request.params.context.include_declaration {
            occurrences.extend(without_path(index.declarations(&current.name)?, &path));
            occurrences.extend(
                class_declarations(&path, root)
                    .into_iter()
                    .filter(|declaration| declaration.name == current.name),
            );
        }
        let mut result = Vec::with_capacity(occurrences.len());
        let mut seen = HashSet::with_capacity(occurrences.len());
        for occurrence in &occurrences {
            let location = occurrence_location(occurrence, &path, line_index);
            if seen.insert((location.uri.clone(), location.range.start)) {
                result.push(location);
            }
        }
        sort_locations(&mut result);
        Ok(result)
    }
}

fn without_path(occurrences: Vec<ClassOccurrence>, path: &str) -> Vec<ClassOccurrence> {
    occurrences
        .into_iter()
        .filter(|occurrence| occurrence.file != path)
        .collect()
}

fn occurrence_location(
    occurrence: &ClassOccurrence,
    current_path: &str,
    current_lines: &LineIndex,
) -> protocol::Location {
    let uri = swlsp_uriutil::file_uri(&occurrence.file);
    if occurrence.file == current_path {
        let start = occurrence.range.start;
        let end = occurrence.range.end.max(start);
        return protocol::Location {
            uri,
            range: swlsp_lsp::protocol_range(
                current_lines,
                TextRange::new(TextSize::from(start), TextSize::from(end)),
            ),
        };
    }
    protocol::Location {
        uri,
        range: protocol::Range {
            start: protocol::Position {
                line: occurrence.start.line,
                character: occurrence.start.character,
            },
            end: protocol::Position {
                line: occurrence.end.line,
                character: occurrence.end.character,
            },
        },
    }
}
