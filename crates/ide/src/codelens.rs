//! What the code lens providers of the domains share.
//!
//! Providers work with byte ranges of the open document snapshot and convert to
//! UTF-16 protocol positions only when building a lens. Navigation lenses use
//! the `shopware.openReferences` client command with one argument: the sorted,
//! deduplicated list of `file://...#line` targets.

use std::cmp::Ordering;
use std::collections::HashSet;

use serde_json::Value;
use swlsp_lsp::{BoxError, Context};
use swlsp_lsp_protocol::{self as protocol, Position, Range};
use swlsp_syntax::{LineIndex, TextRange};
use swlsp_uriutil::filepath;

/// The client command every navigation lens executes.
pub const OPEN_REFERENCES_COMMAND: &str = "shopware.openReferences";

pub fn cancelled(ctx: &Context) -> Result<(), BoxError> {
    if ctx.is_cancelled() {
        return Err(Box::new(swlsp_lsp::Cancelled));
    }
    Ok(())
}

pub fn related_lens(
    range: Range,
    title: impl Into<String>,
    targets: Vec<String>,
) -> protocol::CodeLens {
    protocol::CodeLens {
        range,
        command: Some(protocol::Command {
            title: title.into(),
            command: OPEN_REFERENCES_COMMAND.to_owned(),
            arguments: vec![Value::Array(
                targets.into_iter().map(Value::String).collect(),
            )],
        }),
        data: None,
    }
}

pub fn related_protocol_range(range: TextRange, lines: &LineIndex) -> Range {
    let (start_line, start_character) = lines.position_utf16(range.start().into());
    let (end_line, end_character) = lines.position_utf16(range.end().into());
    Range {
        start: Position {
            line: start_line,
            character: start_character,
        },
        end: Position {
            line: end_line,
            character: end_character,
        },
    }
}

pub fn line_range(line: u32) -> Range {
    let position = Position { line, character: 0 };
    Range {
        start: position,
        end: position,
    }
}

pub fn related_target(path: &str, line: i64) -> String {
    if path.is_empty() {
        return String::new();
    }
    let line = line.max(1);
    swlsp_uriutil::file_uri_with_fragment(path, &line.to_string())
}

/// The one-based line of `offset` in the file on disk (1 when unreadable).
pub fn related_source_line(path: &str, offset: u32) -> i64 {
    let Ok(source) = std::fs::read(path) else {
        return 1;
    };
    let lines = LineIndex::new(String::from_utf8_lossy(&source).into_owned());
    i64::from(lines.position_utf16(offset).0) + 1
}

pub fn unique_related_targets(targets: Vec<String>) -> Vec<String> {
    let mut seen = HashSet::with_capacity(targets.len());
    let mut result: Vec<String> = targets
        .into_iter()
        .filter(|target| !target.is_empty() && seen.insert(target.clone()))
        .collect();
    result.sort();
    result
}

pub fn same_path(left: &str, right: &str) -> bool {
    filepath::clean(left) == filepath::clean(right)
}

pub fn lower_ext(path: &str) -> String {
    filepath::ext(path).to_ascii_lowercase()
}

pub fn title(lens: &protocol::CodeLens) -> &str {
    lens.command
        .as_ref()
        .map(|command| command.title.as_str())
        .unwrap_or_default()
}

pub fn by_line(left: &protocol::CodeLens, right: &protocol::CodeLens) -> Ordering {
    left.range.start.line.cmp(&right.range.start.line)
}

pub fn by_position(left: &protocol::CodeLens, right: &protocol::CodeLens) -> Ordering {
    by_line(left, right).then(left.range.start.character.cmp(&right.range.start.character))
}

pub fn by_line_title(left: &protocol::CodeLens, right: &protocol::CodeLens) -> Ordering {
    by_line(left, right).then_with(|| title(left).cmp(title(right)))
}

/// Line, character, then title.
pub fn by_position_title(left: &protocol::CodeLens, right: &protocol::CodeLens) -> Ordering {
    by_position(left, right).then_with(|| title(left).cmp(title(right)))
}

pub fn sort_related_code_lenses(result: &mut [protocol::CodeLens]) {
    result.sort_by(by_position_title);
}

pub fn counted_title(
    singular: &str,
    plural_prefix: &str,
    plural_suffix: &str,
    count: usize,
) -> String {
    if count == 1 {
        singular.to_owned()
    } else {
        format!("{plural_prefix}{count}{plural_suffix}")
    }
}

#[cfg(test)]
#[path = "codelens_tests.rs"]
mod tests;

/// Fixtures for the code lens tests of the domain crates.
#[cfg(feature = "testing")]
pub mod testing {
    use std::path::Path;
    use std::sync::Arc;

    use swlsp_indexer::ParsedFile;
    use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, TextDocument};
    use swlsp_lsp_protocol as protocol;
    use swlsp_php::PhpIndex;
    use swlsp_symfony::ServiceIndex;
    use swlsp_uriutil::file_uri;

    use super::{OPEN_REFERENCES_COMMAND, related_target};

    pub fn p(path: &Path) -> String {
        path.to_string_lossy().into_owned()
    }

    pub fn write(path: &Path, source: &str) {
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(path, source).unwrap();
    }

    pub fn parsed(path: &Path, source: &str) -> ParsedFile {
        ParsedFile::new(p(path), source.as_bytes().to_vec())
    }

    pub fn lenses_for(
        provider: &dyn CodeLensProvider,
        path: &Path,
        source: &str,
    ) -> Vec<protocol::CodeLens> {
        let document = TextDocument::new(file_uri(&p(path)), source, 1);
        let mut params = protocol::CodeLensParams::default();
        params.text_document.uri = document.uri.clone();
        provider
            .get_code_lenses(
                &Context::background(),
                &CodeLensRequest {
                    params,
                    document: Arc::new(document),
                },
            )
            .unwrap()
    }

    pub fn titles(lenses: &[protocol::CodeLens]) -> Vec<String> {
        lenses
            .iter()
            .map(|lens| lens.command.as_ref().unwrap().title.clone())
            .collect()
    }

    pub fn sorted(mut values: Vec<String>) -> Vec<String> {
        values.sort();
        values
    }

    pub fn targets(lens: &protocol::CodeLens) -> Vec<String> {
        let command = lens.command.as_ref().expect("command");
        assert_eq!(command.command, OPEN_REFERENCES_COMMAND);
        assert_eq!(command.arguments.len(), 1);
        command.arguments[0]
            .as_array()
            .expect("targets")
            .iter()
            .map(|target| target.as_str().unwrap().to_owned())
            .collect()
    }

    pub fn by_title<'a>(lenses: &'a [protocol::CodeLens], title: &str) -> &'a protocol::CodeLens {
        lenses
            .iter()
            .find(|lens| {
                lens.command
                    .as_ref()
                    .is_some_and(|command| command.title == title)
            })
            .unwrap_or_else(|| panic!("code lens not found: {title} in {:?}", titles(lenses)))
    }

    pub fn target(path: &Path, line: i64) -> String {
        related_target(&p(path), line)
    }

    pub fn php_index(cache: &Path) -> PhpIndex {
        PhpIndex::open(cache, None).unwrap()
    }

    pub fn service_index(root: &Path, cache: &Path, php: Option<&PhpIndex>) -> Arc<ServiceIndex> {
        let services = Arc::new(ServiceIndex::new(&p(root), cache, None).unwrap());
        if let Some(php) = php {
            services.set_php_index(Some(php.clone()));
        }
        services
    }
}
