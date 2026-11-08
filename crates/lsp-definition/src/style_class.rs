use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{Context, DefinitionRequest, GotoDefinitionProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_style::{ClassOccurrence, ClassOccurrenceKind, Index};
use swlsp_syntax::LineIndex;

use crate::common::{ext_lower, protocol_range, request_offset, request_uri};

pub struct StyleClassDefinitionProvider {
    index: Option<Arc<Index>>,
}

impl StyleClassDefinitionProvider {
    pub fn new(index: Option<Arc<Index>>) -> Self {
        Self { index }
    }
}

impl GotoDefinitionProvider for StyleClassDefinitionProvider {
    fn get_definition(
        &self,
        _ctx: &Context,
        request: &DefinitionRequest,
    ) -> Vec<protocol::Location> {
        let (Some(index), Some(root), Some(lines)) =
            (&self.index, request.root.as_ref(), request.line_index())
        else {
            return Vec::new();
        };
        let Ok(path) = swlsp_uriutil::path(request_uri(request)) else {
            return Vec::new();
        };
        if !matches!(
            ext_lower(&path).as_str(),
            ".scss" | ".twig" | ".html" | ".vue"
        ) {
            return Vec::new();
        }
        let Some(offset) = request_offset(request) else {
            return Vec::new();
        };
        let Some(current) = swlsp_style::class_at(&path, root, offset) else {
            return Vec::new();
        };
        if current.kind != ClassOccurrenceKind::Usage || current.name.is_empty() {
            return Vec::new();
        }
        let Ok(declarations) = index.declarations(&current.name) else {
            return Vec::new();
        };
        let mut declarations: Vec<ClassOccurrence> = declarations
            .into_iter()
            .filter(|declaration| declaration.file != path)
            .collect();
        declarations.extend(
            swlsp_style::class_declarations(&path, root)
                .into_iter()
                .filter(|declaration| declaration.name == current.name),
        );
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for declaration in &declarations {
            let location = location(declaration, &path, lines);
            if !seen.insert((
                location.uri.clone(),
                location.range.start.line,
                location.range.start.character,
            )) {
                continue;
            }
            result.push(location);
        }
        result.sort_by(|left, right| {
            left.uri
                .cmp(&right.uri)
                .then(left.range.start.line.cmp(&right.range.start.line))
                .then(left.range.start.character.cmp(&right.range.start.character))
        });
        result
    }
}

fn location(
    occurrence: &ClassOccurrence,
    current_path: &str,
    current_lines: &LineIndex,
) -> protocol::Location {
    let uri = swlsp_uriutil::file_uri(&occurrence.file);
    if occurrence.file == current_path {
        return protocol::Location {
            uri,
            range: protocol_range(current_lines, occurrence.range.into()),
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
