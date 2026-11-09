//! Import usage analysis.

use rustc_hash::FxHashSet;
use swlsp_parser_php::query::{direct_child, is_constant_reference, name_value, use_items};
use swlsp_parser_php::syntax::{PHP_BLOCK, PHP_NAMESPACE, PHP_USE_DECLARATION, TK_BLOCK_COMMENT};
use swlsp_php_phpdoc::visit_reference_names;
use swlsp_php_resolver::{Import, ImportKind, parse_use_declaration};
use swlsp_php_semantic::{Document, NameKind};
use swlsp_syntax::rowan::NodeOrToken;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange};
use swlsp_textutil::lowercase;

use crate::Cancelled;

/// One import clause with exact source ranges.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Item {
    pub import: Import,
    pub range: TextRange,
    pub name_range: TextRange,
    pub used: bool,
}

/// One `use` declaration and its clauses.
#[derive(Clone, Debug)]
pub struct Declaration {
    pub node: SyntaxNode,
    pub items: Vec<Item>,
    /// Ranges of the commas separating `items`.
    pub commas: Vec<TextRange>,
}

/// A namespace region. Unsafe scopes contain a declaration the import
/// parser does not understand and must not be rewritten.
#[derive(Clone, Debug, Default)]
pub struct Scope {
    pub unsafe_scope: bool,
    pub range: TextRange,
    pub declarations: Vec<Declaration>,
}

#[derive(Clone, Debug, Default)]
pub struct Analysis {
    pub scopes: Vec<Scope>,
}

#[derive(Default)]
struct Used {
    classes: FxHashSet<String>,
    functions: FxHashSet<String>,
    constants: FxHashSet<String>,
}

impl Used {
    fn set(&mut self, kind: ImportKind) -> &mut FxHashSet<String> {
        match kind {
            ImportKind::Class => &mut self.classes,
            ImportKind::Function => &mut self.functions,
            ImportKind::Constant => &mut self.constants,
        }
    }

    /// Marks the lexical import a name relies on. Fully qualified and
    /// namespace-relative names use no import; qualified names use the class
    /// import of their first segment.
    fn mark(&mut self, name: &str, kind: ImportKind) {
        if name.starts_with('\\') || lowercase(name).starts_with("namespace\\") {
            return;
        }
        let (name, kind) = match name.find('\\') {
            Some(separator) => (&name[..separator], ImportKind::Class),
            None => (name, kind),
        };
        let key = if kind == ImportKind::Constant {
            name.to_owned()
        } else {
            lowercase(name)
        };
        self.set(kind).insert(key);
    }
}

fn contains(range: TextRange, offset: u32) -> bool {
    offset >= u32::from(range.start()) && offset < u32::from(range.end())
}

/// Uses lexical reference names, not resolved targets: an unresolved class
/// still uses its import, while a fully qualified name does not.
pub fn analyze(
    is_cancelled: &dyn Fn() -> bool,
    root: &SyntaxNode,
    document: &Document,
) -> Result<Analysis, Cancelled> {
    let mut result = Analysis {
        scopes: scopes(root),
    };
    for scope in &mut result.scopes {
        if scope.unsafe_scope || scope.declarations.is_empty() {
            continue;
        }
        let mut used = Used::default();
        for reference in &document.references {
            if is_cancelled() {
                return Err(Cancelled);
            }
            if !contains(scope.range, u32::from(reference.range.start())) {
                continue;
            }
            match reference.kind {
                NameKind::Class => used.mark(&reference.name, ImportKind::Class),
                NameKind::Function => used.mark(&reference.name, ImportKind::Function),
                NameKind::Constant => used.mark(&reference.name, ImportKind::Constant),
                _ => {}
            }
        }
        for element in root.descendants_with_tokens() {
            if is_cancelled() {
                return Err(Cancelled);
            }
            match element {
                NodeOrToken::Node(node) => {
                    if contains(scope.range, u32::from(node.range_trimmed_trivia().start()))
                        && is_constant_reference(&node)
                    {
                        used.mark(&name_value(&node), ImportKind::Constant);
                    }
                }
                NodeOrToken::Token(token) => {
                    if token.kind() == TK_BLOCK_COMMENT
                        && contains(scope.range, u32::from(token.text_range().start()))
                        && token.text().starts_with("/**")
                    {
                        visit_reference_names(token.text(), |name| {
                            used.mark(name, ImportKind::Class);
                        });
                    }
                }
            }
        }
        for declaration in &mut scope.declarations {
            for item in &mut declaration.items {
                let alias = if item.import.kind == ImportKind::Constant {
                    item.import.alias.clone()
                } else {
                    lowercase(&item.import.alias)
                };
                item.used = used.set(item.import.kind).contains(&alias);
            }
        }
    }
    if is_cancelled() {
        return Err(Cancelled);
    }
    Ok(result)
}

fn scopes(root: &SyntaxNode) -> Vec<Scope> {
    let root_range = root.text_range();
    let mut result = vec![Scope {
        range: root_range,
        ..Scope::default()
    }];
    let mut current = 0;
    for child in root.children() {
        if child.kind() == PHP_NAMESPACE {
            let child_range = child.text_range();
            result[current].range =
                TextRange::new(result[current].range.start(), child_range.start());
            if let Some(block) = direct_child(&child, PHP_BLOCK) {
                let mut scope = Scope {
                    range: block.text_range(),
                    ..Scope::default()
                };
                for node in block.children() {
                    append_declaration(&mut scope, node);
                }
                result.push(scope);
            }
            result.push(Scope {
                range: TextRange::new(child_range.end(), root_range.end()),
                ..Scope::default()
            });
            current = result.len() - 1;
            continue;
        }
        append_declaration(&mut result[current], child);
    }
    result
}

fn append_declaration(scope: &mut Scope, node: SyntaxNode) {
    let Some(parsed) = use_items(&node) else {
        if node.kind() == PHP_USE_DECLARATION {
            scope.unsafe_scope = true;
        }
        return;
    };
    let mut items = Vec::with_capacity(parsed.items.len());
    for item in parsed.items {
        let mut values = parse_use_declaration(&item.text);
        if values.len() != 1 {
            return;
        }
        items.push(Item {
            import: values.pop().unwrap_or_default(),
            range: item.range,
            name_range: item.name_range,
            used: false,
        });
    }
    scope.declarations.push(Declaration {
        node,
        items,
        commas: parsed.commas,
    });
}
