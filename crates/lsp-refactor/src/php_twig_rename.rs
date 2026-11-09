//! Augments the PHP semantic rename with typed Twig member, constant, enum-case
//! and class-name usages.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, RenameProvider, RenameRequest, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::{PhpIndex, symbol_at};
use swlsp_php_semantic::{Document, Snapshot, Symbol, SymbolKind};
use swlsp_syntax::{LineIndex, TextRange};
use swlsp_textutil::eq_ignore_case;
use swlsp_twig::{
    ConstantReference, TwigIndexer, php_usage_target_for_symbol, twig_attribute_name,
};
use swlsp_uriutil::file_uri;

use crate::{lower_ext, protocol_range};

pub struct PhpTwigRenameProvider {
    base: Option<Arc<dyn RenameProvider>>,
    index: Option<Arc<TwigIndexer>>,
    php_index: Option<PhpIndex>,
}

impl PhpTwigRenameProvider {
    pub fn new(
        base: Option<Arc<dyn RenameProvider>>,
        index: Option<Arc<TwigIndexer>>,
        php_index: Option<PhpIndex>,
    ) -> Self {
        Self {
            base,
            index,
            php_index,
        }
    }
}

fn edit_key(uri: &str, range: &protocol::Range) -> String {
    format!(
        "{uri}:{}:{}:{}:{}",
        range.start.line, range.start.character, range.end.line, range.end.character
    )
}

impl RenameProvider for PhpTwigRenameProvider {
    fn rename(
        &self,
        ctx: &Context,
        request: &RenameRequest,
    ) -> ProviderResult<Option<protocol::WorkspaceEdit>> {
        let Some(base) = &self.base else {
            return Ok(None);
        };
        let edit = base.rename(ctx, request)?;
        let Some(mut edit) = edit else {
            return Ok(None);
        };
        let (Some(index), Some(php_index), Some(document), Some(root)) = (
            &self.index,
            &self.php_index,
            request.syntax.document.as_deref(),
            request.syntax.root.as_ref(),
        ) else {
            return Ok(Some(edit));
        };
        if lower_ext(&request.params.text_document.uri) != ".php" {
            return Ok(Some(edit));
        }
        let Ok(path) = swlsp_uriutil::path(&request.params.text_document.uri) else {
            return Ok(Some(edit));
        };
        let (semantic, snapshot) = php_semantic_state(php_index, document, root, &path);
        let offset = document.line_index().offset_utf16(
            request.params.position.line,
            request.params.position.character,
        );
        let Some(symbol) = symbol_at(&semantic, &snapshot, offset) else {
            return Ok(Some(edit));
        };
        let new_name = request.params.new_name.trim();
        let new_name = new_name.strip_prefix('$').unwrap_or(new_name);
        if new_name.is_empty() {
            return Ok(Some(edit));
        }

        let mut seen: HashSet<String> = edit
            .changes
            .iter()
            .flat_map(|(uri, edits)| edits.iter().map(move |edit| edit_key(uri, &edit.range)))
            .collect();
        let mut cache = LocationCache::default();
        let mut add = |edit: &mut protocol::WorkspaceEdit,
                       file_path: &str,
                       range: TextRange,
                       replacement: &dyn Fn(&str) -> String| {
            let Some((uri, protocol_range, source)) = cache.location(file_path, range) else {
                return;
            };
            if !seen.insert(edit_key(&uri, &protocol_range)) {
                return;
            }
            let text = replacement(&source);
            edit.changes
                .entry(uri)
                .or_default()
                .push(protocol::TextEdit::new(protocol_range, text));
        };

        if let Some(target) = php_usage_target_for_symbol(&snapshot, &symbol) {
            for reference in index.get_php_usage_references(&target)? {
                add(
                    &mut edit,
                    &reference.file_path,
                    reference.range,
                    &|source| renamed_twig_php_usage(source, &symbol, new_name),
                );
            }
        }
        if let Some(constant) = twig_constant_rename_target(&snapshot, &symbol) {
            for reference in index.get_constant_references(&constant)? {
                add(
                    &mut edit,
                    &reference.file_path,
                    reference.range,
                    &|source| {
                        if symbol.kind == SymbolKind::GlobalConstant {
                            replace_twig_qualified_name_tail(source, new_name)
                        } else {
                            replace_twig_scoped_member_name(source, new_name)
                        }
                    },
                );
            }
        }
        Ok(Some(edit))
    }
}

/// The request's PHP analysis: the memoized document analysis, falling back to the indexed
/// document or a fresh analysis.
fn php_semantic_state(
    index: &PhpIndex,
    document: &TextDocument,
    root: &swlsp_syntax::SyntaxNode,
    path: &str,
) -> (Arc<Document>, Arc<Snapshot>) {
    if let Ok(Some(state)) = swlsp_lsp_phpanalysis::for_document(index, document) {
        return (state.document.clone(), state.snapshot.clone());
    }
    if let Some(semantic) = index.semantic_document(path) {
        return (Arc::new(semantic), index.semantic_snapshot());
    }
    let semantic = Arc::new(index.analyze_document(
        path,
        i64::from(document.version),
        root,
        document.source(),
    ));
    let snapshot = index.semantic_snapshot().with_document(&semantic);
    (semantic, snapshot)
}

fn twig_constant_rename_target(snapshot: &Snapshot, symbol: &Symbol) -> Option<ConstantReference> {
    match symbol.kind {
        SymbolKind::GlobalConstant => Some(ConstantReference {
            name: symbol
                .fully_qualified
                .strip_prefix('\\')
                .unwrap_or(&symbol.fully_qualified)
                .to_owned(),
            ..ConstantReference::default()
        }),
        SymbolKind::ClassConstant | SymbolKind::EnumCase => {
            let container = snapshot.symbol(symbol.container.as_str())?;
            if !container.is_class_like() {
                return None;
            }
            Some(ConstantReference {
                class: container.fully_qualified.to_string(),
                name: symbol.name.to_string(),
                ..ConstantReference::default()
            })
        }
        _ => None,
    }
}

fn renamed_twig_php_usage(current: &str, symbol: &Symbol, new_name: &str) -> String {
    match symbol.kind {
        SymbolKind::Method => {
            if eq_ignore_case(current, &symbol.name) {
                return new_name.to_owned();
            }
            let shortcut = twig_attribute_name(&symbol.name);
            if !shortcut.is_empty() && eq_ignore_case(current, &shortcut) {
                let renamed = twig_attribute_name(new_name);
                if !renamed.is_empty() {
                    return renamed;
                }
                return new_name.to_owned();
            }
            current.to_owned()
        }
        SymbolKind::ClassConstant | SymbolKind::EnumCase => {
            replace_twig_scoped_member_name(current, new_name)
        }
        SymbolKind::Property => new_name.to_owned(),
        _ if symbol.is_class_like() => replace_twig_qualified_name_tail(current, new_name),
        _ => current.to_owned(),
    }
}

fn replace_twig_scoped_member_name(current: &str, new_name: &str) -> String {
    if let Some(separator) = current.rfind("::") {
        return format!("{}{new_name}", &current[..separator + 2]);
    }
    replace_twig_qualified_name_tail(current, new_name)
}

fn replace_twig_qualified_name_tail(current: &str, new_name: &str) -> String {
    let (current, suffix) = match current.strip_suffix("[]") {
        Some(stripped) => (stripped, "[]"),
        None => (current, ""),
    };
    if let Some(separator) = current.rfind('\\') {
        return format!("{}{new_name}{suffix}", &current[..=separator]);
    }
    format!("{new_name}{suffix}")
}

/// Disk sources of Twig files, read once per request.
#[derive(Default)]
struct LocationCache {
    files: HashMap<String, Option<(String, LineIndex)>>,
}

impl LocationCache {
    fn location(
        &mut self,
        path: &str,
        range: TextRange,
    ) -> Option<(String, protocol::Range, String)> {
        let file = self.files.entry(path.to_owned()).or_insert_with(|| {
            let content = std::fs::read(path).ok()?;
            let source = String::from_utf8_lossy(&content).into_owned();
            let line_index = LineIndex::new(source.clone());
            Some((source, line_index))
        });
        let (source, line_index) = file.as_ref()?;
        let text = source.get(usize::from(range.start())..usize::from(range.end()))?;
        Some((
            file_uri(path),
            protocol_range(line_index, range),
            text.to_owned(),
        ))
    }
}
