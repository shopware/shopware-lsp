//! Twig `constant()` and enum references (port of
//! `twig_constant_reference.go`).

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{Context, ProviderResult, ReferenceRequest, ReferencesProvider};
use swlsp_lsp_protocol as protocol;
use swlsp_php::{PhpIndex, symbol_at};
use swlsp_php_semantic::{Snapshot, Symbol, SymbolKind};
use swlsp_syntax::LineIndex;
use swlsp_twig::{
    ConstantReference, PhpAccessResolver, TwigIndexer, constant_reference_key,
    constant_references_at, constant_references_in_document,
};
use swlsp_uriutil::filepath;

use crate::{FileLines, lower_ext, php_semantic_state, request_offset, unique_locations};

pub struct TwigConstantReferenceProvider {
    index: Option<Arc<TwigIndexer>>,
    php: Option<PhpIndex>,
}

impl TwigConstantReferenceProvider {
    pub fn new(index: Option<Arc<TwigIndexer>>, php: Option<PhpIndex>) -> Self {
        Self { index, php }
    }
}

impl ReferencesProvider for TwigConstantReferenceProvider {
    fn get_references(
        &self,
        _ctx: &Context,
        request: &ReferenceRequest,
    ) -> ProviderResult<Vec<protocol::Location>> {
        let (
            Some(index),
            Some(php),
            Some(node),
            Some(root),
            Some(line_index),
            Some(_),
            Some(offset),
        ) = (
            &self.index,
            &self.php,
            &request.node,
            &request.root,
            request.line_index(),
            &request.document,
            request_offset(request),
        )
        else {
            return Ok(Vec::new());
        };
        let Ok(path) = swlsp_uriutil::path(&request.params.text_document.uri) else {
            return Ok(Vec::new());
        };
        let (targets, from_twig) = match lower_ext(&path).as_str() {
            ".twig" => {
                let resolver = PhpAccessResolver::new(Some(php), Some(index));
                (constant_references_at(&path, root, node, &resolver), true)
            }
            ".php" => {
                let Some((document, snapshot)) = php_semantic_state(php, request, &path) else {
                    return Ok(Vec::new());
                };
                let Some(target) = symbol_at(&document, &snapshot, offset)
                    .and_then(|symbol| constant_target_from_symbol(&snapshot, &symbol))
                else {
                    return Ok(Vec::new());
                };
                (vec![target], false)
            }
            _ => return Ok(Vec::new()),
        };
        if targets.is_empty() {
            return Ok(Vec::new());
        }

        let current = if from_twig {
            let resolver = PhpAccessResolver::new(Some(php), Some(index));
            constant_references_in_document(&path, root, &resolver)
        } else {
            Vec::new()
        };
        let clean_path = filepath::clean(&path);
        let mut files = FileLines::default();
        let mut result = Vec::new();
        let mut seen_targets = HashSet::new();
        for target in &targets {
            let key = constant_reference_key(target);
            if key.is_empty() || !seen_targets.insert(key.clone()) {
                continue;
            }
            for reference in index.get_constant_references(target)? {
                if from_twig && filepath::clean(&reference.file_path) == clean_path {
                    continue;
                }
                result.extend(files.location(&reference.file_path, reference.range));
            }
            for reference in &current {
                if constant_reference_key(reference) != key {
                    continue;
                }
                result.extend(reference_location(
                    &mut files,
                    reference,
                    &clean_path,
                    line_index,
                ));
            }
            if from_twig && request.params.context.include_declaration {
                for symbol in reference_constant_symbols(php, target) {
                    result.extend(files.location(&symbol.path, symbol.selection_range));
                }
            }
        }
        Ok(unique_locations(result))
    }
}

fn reference_location(
    files: &mut FileLines,
    reference: &ConstantReference,
    clean_current_path: &str,
    current_lines: &LineIndex,
) -> Option<protocol::Location> {
    if filepath::clean(&reference.file_path) == clean_current_path {
        return Some(protocol::Location {
            uri: swlsp_uriutil::file_uri(&reference.file_path),
            range: swlsp_lsp::protocol_range(current_lines, reference.range),
        });
    }
    files.location(&reference.file_path, reference.range)
}

fn constant_target_from_symbol(snapshot: &Snapshot, symbol: &Symbol) -> Option<ConstantReference> {
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
            let container = snapshot.symbol(&symbol.container)?;
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

fn reference_constant_symbols(index: &PhpIndex, target: &ConstantReference) -> Vec<Symbol> {
    if target.class.is_empty() {
        index.find_global_constants(&target.name)
    } else {
        index.find_constants(&target.class, &target.name)
    }
}
