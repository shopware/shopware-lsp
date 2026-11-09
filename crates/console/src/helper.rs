use std::cmp::Ordering;
use std::collections::HashSet;
use std::sync::Mutex;

use swlsp_parser_php::query;
use swlsp_php::{PhpContext, PhpIndex};
use swlsp_php_semantic::{Flags, SymbolKind};
use swlsp_php_types::Kind;
use swlsp_syntax::{SyntaxNode, TextRange};
use swlsp_textutil::lowercase;

pub const HELPER_INTERFACE: &str = "Symfony\\Component\\Console\\Helper\\HelperInterface";
pub const HELPER_SET_CLASS: &str = "Symfony\\Component\\Console\\Helper\\HelperSet";
pub const COMMAND_CLASS: &str = "Symfony\\Component\\Console\\Command\\Command";

/// A concrete Console helper whose `getName()` returns a string literal.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Helper {
    pub name: String,
    pub class: String,
    pub file: String,
    pub range: TextRange,
    pub summary: String,
}

/// A helper name passed to `HelperSet::get()/has()` or
/// `Command::getHelper()`.
#[derive(Clone, Debug)]
pub struct HelperReference {
    pub name: String,
    pub range: TextRange,
    pub call: SyntaxNode,
}

/// Caches helper discovery per semantic workspace revision; completion asks
/// repeatedly while typing.
pub struct HelperCatalog {
    index: PhpIndex,
    cache: Mutex<Option<(u64, Vec<Helper>)>>,
}

impl HelperCatalog {
    pub fn new(index: PhpIndex) -> Self {
        Self {
            index,
            cache: Mutex::new(None),
        }
    }

    pub fn helpers(&self) -> Vec<Helper> {
        let revision = self.index.semantic_snapshot().revision;
        let mut cache = self
            .cache
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        match cache.as_ref() {
            Some((cached, helpers)) if *cached == revision => helpers.clone(),
            _ => {
                let helpers = helpers(&self.index);
                *cache = Some((revision, helpers.clone()));
                helpers
            }
        }
    }
}

/// Concrete helpers from the shared semantic snapshot. Only a `getName()`
/// declared by the class itself that returns a direct string literal counts,
/// so completion stays deterministic.
pub fn helpers(index: &PhpIndex) -> Vec<Helper> {
    let snapshot = index.semantic_snapshot();
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for class in snapshot.global_symbols() {
        if class.kind != SymbolKind::Class
            || class.flags.has(Flags::ABSTRACT)
            || !snapshot.is_subtype_of(&class.fully_qualified, HELPER_INTERFACE)
        {
            continue;
        }
        for method in index.find_methods(&class.fully_qualified, "getName") {
            // Inherited implementations do not define another helper key.
            if method.container != class.id {
                continue;
            }
            for literal in method.literal_returns() {
                if literal.ty.kind() != Kind::LiteralString || literal.value.trim().is_empty() {
                    continue;
                }
                let key = (lowercase(&literal.value), lowercase(&class.fully_qualified));
                if !seen.insert(key) {
                    continue;
                }
                let range = if class.selection_range.is_empty() {
                    class.range
                } else {
                    class.selection_range
                };
                let summary = if class.doc_summary().is_empty() {
                    method.doc_summary()
                } else {
                    class.doc_summary()
                };
                result.push(Helper {
                    name: literal.value.to_string(),
                    class: class.fully_qualified.to_string(),
                    file: class.path.to_string(),
                    range,
                    summary: summary.to_owned(),
                });
            }
        }
    }
    result.sort_by(compare_helpers);
    result
}

/// Orders helpers by name, class, file and position. Names and classes compare
/// case-insensitively; when two differ only in case, the field still decides
/// (as equal) instead of falling through to the next one.
fn compare_helpers(left: &Helper, right: &Helper) -> Ordering {
    if left.name != right.name {
        return lowercase(&left.name).cmp(&lowercase(&right.name));
    }
    if left.class != right.class {
        return lowercase(&left.class).cmp(&lowercase(&right.class));
    }
    if left.file != right.file {
        return left.file.cmp(&right.file);
    }
    left.range.start().cmp(&right.range.start())
}

pub fn helper_reference_at(node: &SyntaxNode) -> Option<HelperReference> {
    let literal = query::string_at(node)?;
    let call = query::call_at(&literal)?;
    if query::argument_index(&call, &literal) != Some(0)
        || query::argument_expression(&call, 0).as_ref() != Some(&literal)
    {
        return None;
    }
    if !matches!(
        query::call_method_name(&call).to_lowercase().as_str(),
        "get" | "has" | "gethelper"
    ) {
        return None;
    }
    Some(HelperReference {
        name: query::string_value(&literal),
        range: query::string_content_range(&literal),
        call,
    })
}

/// Whether the receiver is a `HelperSet` (`get`/`has`) or a `Command`
/// (`getHelper`).
pub fn validate_helper_reference(
    index: &PhpIndex,
    context: Option<&PhpContext>,
    reference: &HelperReference,
) -> bool {
    let target = match query::call_method_name(&reference.call)
        .to_lowercase()
        .as_str()
    {
        "get" | "has" => HELPER_SET_CLASS,
        "gethelper" => COMMAND_CLASS,
        _ => return false,
    };
    index.is_method_called_on_class(context, &reference.call, target)
}
