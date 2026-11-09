//! The edit collector and class-reference imports.

use rustc_hash::FxHashMap;
use swlsp_parser_php::query::{class_name, classes, namespace, nodes, use_declarations};
use swlsp_parser_php::syntax::{PHP_NAMESPACE, TK_OPEN_TAG};
use swlsp_php_resolver::{ImportKind, parse_use_declaration};
use swlsp_textutil::{eq_ignore_case, lowercase};

use swlsp_rewrite::{Builder, Edit, RewriteError, SyntaxRange};
use swlsp_syntax::{SyntaxNode, TextRange, TextSize};

/// A rejected or failed PHP rewrite.
#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
pub enum Error {
    /// The requested edit is not applicable to the source, described as
    /// `operation: reason` (e.g. "set PHP parent: parent is empty").
    #[error("{0}")]
    Invalid(String),
    /// The edit set is invalid (overlapping or out-of-range edits).
    #[error(transparent)]
    Rewrite(#[from] RewriteError),
}

pub(crate) fn invalid<T>(message: impl Into<String>) -> Result<T, Error> {
    Err(Error::Invalid(message.into()))
}

pub(crate) type Result<T, E = Error> = std::result::Result<T, E>;

/// Collects related PHP edits against one immutable source snapshot.
/// Callers may combine imports, list edits, class edits, and PHPDoc edits
/// before validating the complete set with [`Editor::finish`].
pub struct Editor<'s> {
    pub(crate) source: &'s str,
    root: Option<SyntaxNode>,
    pub(crate) builder: Builder,
    /// Lowercase alias to target.
    imports: FxHashMap<String, String>,
    /// Lowercase target to alias.
    import_aliases: FxHashMap<String, String>,
    planned_imports: FxHashMap<String, String>,
    planned_aliases: FxHashMap<String, String>,
    planned_import_count: usize,
    has_use_declarations: bool,
}

impl<'s> Editor<'s> {
    pub fn new(source: &'s str, root: Option<&SyntaxNode>) -> Self {
        let mut editor = Editor {
            source,
            root: root.cloned(),
            builder: Builder::new(source),
            imports: FxHashMap::default(),
            import_aliases: FxHashMap::default(),
            planned_imports: FxHashMap::default(),
            planned_aliases: FxHashMap::default(),
            planned_import_count: 0,
            has_use_declarations: false,
        };
        let Some(root) = root else {
            return editor;
        };
        let declarations = use_declarations(root);
        editor.has_use_declarations = !declarations.is_empty();
        for declaration in &declarations {
            for imported in parse_use_declaration(&declaration.text().to_string()) {
                if imported.kind != ImportKind::Class {
                    continue;
                }
                let target = imported.target.trim_matches('\\');
                editor
                    .imports
                    .insert(lowercase(&imported.alias), target.to_owned());
                editor
                    .import_aliases
                    .insert(lowercase(target), imported.alias.clone());
            }
        }
        editor
    }

    pub fn finish(&self) -> Result<Vec<Edit>> {
        Ok(self.builder.finish()?)
    }

    pub fn replace_range(&mut self, range: TextRange, text: impl Into<String>) -> Result<()> {
        Ok(self.builder.replace_range(range, text)?)
    }

    pub fn insert(&mut self, offset: u32, text: impl Into<String>) -> Result<()> {
        Ok(self.builder.insert(offset, text)?)
    }

    pub fn delete(&mut self, element: &impl SyntaxRange) -> Result<()> {
        Ok(self.builder.delete(element)?)
    }

    /// The shortest unambiguous reference to `class_name`, scheduling a use
    /// declaration when one is useful. Conflicting aliases fall back to a
    /// fully qualified reference rather than changing existing imports.
    pub fn class_reference(&mut self, class_name: &str) -> Result<String> {
        let class_name = class_name.trim().trim_matches('\\');
        if class_name.is_empty() {
            return invalid("add PHP class reference: class name is empty");
        }
        let (short_name, class_namespace) = split_class_name(class_name);
        let alias_key = lowercase(short_name);
        let class_key = lowercase(class_name);
        if let Some(alias) = self.import_aliases.get(&class_key) {
            return Ok(alias.clone());
        }
        if let Some(alias) = self.planned_aliases.get(&class_key) {
            return Ok(alias.clone());
        }
        for imports in [&self.imports, &self.planned_imports] {
            if let Some(target) = imports.get(&alias_key) {
                if eq_ignore_case(target, class_name) {
                    return Ok(short_name.to_owned());
                }
                return Ok(format!("\\{class_name}"));
            }
        }
        let current_namespace = self
            .root
            .as_ref()
            .map(|root| namespace(root).trim_matches('\\').to_owned())
            .unwrap_or_default();
        if eq_ignore_case(&current_namespace, class_namespace) {
            return Ok(short_name.to_owned());
        }
        if self.local_class_name_conflicts(short_name, class_name, &current_namespace) {
            return Ok(format!("\\{class_name}"));
        }
        let Some(root) = &self.root else {
            return Ok(format!("\\{class_name}"));
        };
        let offset = import_insertion_offset(root);
        let prefix = if !self.has_use_declarations && self.planned_import_count == 0 {
            "\n\nuse "
        } else {
            "\nuse "
        };
        self.builder
            .insert(offset, format!("{prefix}{class_name};"))?;
        self.planned_imports
            .insert(alias_key, class_name.to_owned());
        self.planned_aliases
            .insert(class_key, short_name.to_owned());
        self.planned_import_count += 1;
        Ok(short_name.to_owned())
    }

    fn local_class_name_conflicts(
        &self,
        short_name: &str,
        class_name: &str,
        namespace: &str,
    ) -> bool {
        let Some(root) = &self.root else {
            return false;
        };
        classes(root).iter().any(|class| {
            if !eq_ignore_case(&self::class_name(class), short_name) {
                return false;
            }
            let local_name = if namespace.is_empty() {
                short_name.to_owned()
            } else {
                format!("{namespace}\\{short_name}")
            };
            !eq_ignore_case(&local_name, class_name)
        })
    }
}

fn split_class_name(class_name: &str) -> (&str, &str) {
    match class_name.rfind('\\') {
        Some(separator) => (&class_name[separator + 1..], &class_name[..separator]),
        None => (class_name, ""),
    }
}

fn import_insertion_offset(root: &SyntaxNode) -> u32 {
    let result = use_declarations(root)
        .iter()
        .map(|declaration| u32::from(declaration.text_range().end()))
        .max()
        .unwrap_or(0);
    if result != 0 {
        return result;
    }
    if let Some(namespace) = nodes(root, &[PHP_NAMESPACE]).first() {
        let text = namespace.text().to_string();
        if let Some(end) = text.find([';', '{']) {
            return u32::from(namespace.text_range().start()) + end as u32 + 1;
        }
    }
    if let Some(open_tag) = root.first_token()
        && open_tag.kind() == TK_OPEN_TAG
    {
        return u32::from(open_tag.text_range().end());
    }
    0
}

pub(crate) fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end))
}

pub(crate) fn start(value: TextRange) -> u32 {
    u32::from(value.start())
}

pub(crate) fn end(value: TextRange) -> u32 {
    u32::from(value.end())
}

pub(crate) fn line_start(source: &str, offset: u32) -> u32 {
    let offset = (offset as usize).min(source.len());
    source.as_bytes()[..offset]
        .iter()
        .rposition(|&byte| byte == b'\n')
        .map_or(0, |index| index as u32 + 1)
}

/// The text between `start` and `end` when it consists only of spaces, tabs,
/// and carriage returns.
pub(crate) fn whitespace_prefix(source: &str, start: u32, end: u32) -> Option<&str> {
    if start > end || end as usize > source.len() {
        return None;
    }
    let value = source.get(start as usize..end as usize)?;
    value
        .bytes()
        .all(|byte| matches!(byte, b' ' | b'\t' | b'\r'))
        .then_some(value)
}
