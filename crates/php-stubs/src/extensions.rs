//! Runtime extension bundle selection.

use std::collections::{BTreeSet, HashSet};

use swlsp_php_project::normalize_extension;
use swlsp_php_semantic::Symbol;

const ALWAYS_LOADED_EXTENSIONS: [&str; 9] = [
    "core",
    "date",
    "filter",
    "hash",
    "json",
    "pcre",
    "random",
    "reflection",
    "spl",
];

fn extension_dependencies(extension: &str) -> &'static [&'static str] {
    match extension {
        "dom" | "simplexml" | "xml" | "xmlreader" | "xmlwriter" => &["libxml"],
        "xsl" => &["dom", "libxml"],
        "pdo_sqlite" => &["pdo", "sqlite3"],
        _ => &[],
    }
}

/// The deterministic runtime bundle closure for a project. PHP core bundles
/// are always present; requested extensions bring in narrow declaration
/// dependencies such as libxml and PDO. Extensions whose dependency is
/// disabled are dropped.
pub fn selected_extensions<S: AsRef<str>>(requested: &[S], disabled_lists: &[&[S]]) -> Vec<String> {
    let mut disabled: HashSet<String> = disabled_lists
        .iter()
        .flat_map(|list| list.iter())
        .map(|extension| normalize_extension(extension.as_ref()))
        .filter(|extension| !extension.is_empty())
        .collect();
    for extension in ALWAYS_LOADED_EXTENSIONS {
        disabled.remove(extension);
    }
    let mut selection = Selection {
        disabled,
        selected: BTreeSet::new(),
        visiting: HashSet::new(),
    };
    for extension in ALWAYS_LOADED_EXTENSIONS {
        selection.add(extension);
    }
    for extension in requested {
        selection.add(extension.as_ref());
    }
    selection.selected.into_iter().collect()
}

struct Selection {
    disabled: HashSet<String>,
    selected: BTreeSet<String>,
    visiting: HashSet<String>,
}

impl Selection {
    fn add(&mut self, value: &str) -> bool {
        let extension = normalize_extension(value);
        if extension.is_empty() {
            return false;
        }
        if self.selected.contains(&extension) {
            return true;
        }
        if self.disabled.contains(&extension) || self.visiting.contains(&extension) {
            return false;
        }
        self.visiting.insert(extension.clone());
        for dependency in extension_dependencies(&extension) {
            if !self.add(dependency) {
                self.visiting.remove(&extension);
                return false;
            }
        }
        self.visiting.remove(&extension);
        self.selected.insert(extension);
        true
    }
}

/// Drops declarations owned by an unselected generated bundle. Symbols
/// without a known bundle (hand-written overlay additions) are kept.
pub(crate) fn filter_stub_symbols(symbols: Vec<Symbol>, extensions: &[String]) -> Vec<Symbol> {
    let selected: HashSet<String> = extensions
        .iter()
        .map(|extension| normalize_extension(extension))
        .collect();
    symbols
        .into_iter()
        .filter(|symbol| {
            crate::extension_for_symbol(&symbol.fully_qualified)
                .is_none_or(|extension| selected.contains(extension))
        })
        .collect()
}
