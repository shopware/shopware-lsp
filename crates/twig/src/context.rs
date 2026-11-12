//! Types that reach a template from other templates: the values an
//! `include` or `embed` passes, the context of the templates that include or
//! extend it, variables its parent assigns, and macro arguments at their
//! call sites.
//!
//! Other templates are read from disk through the reference index; a hop
//! limit keeps chains of includes and cycles bounded.

use std::cell::{Cell, RefCell};
use std::time::SystemTime;

use crate::path::clean;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::*;
use swlsp_php_types::Type;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt as _, TextRange};

use crate::inference::{all_known, macro_parameter_names};
use crate::macros::{
    MacroReferenceRole, direct_child, function_name_operand, macro_references_in_document,
};
use crate::path::template_names;
use crate::php_access::{PhpAccessResolver, direct_twig_children, first_twig_child};
use crate::reference::TemplateReferenceKind;
use crate::template_variable::{
    direct_function_hash, direct_with_hash, has_token, include_function_propagates_context,
};

/// How many templates a type may be followed through.
const MAX_HOPS: u32 = 2;
/// More referencing sites than this are not followed: a template used that
/// widely gets no single type from its users.
const MAX_SITES: usize = 8;
/// Parsed templates kept per thread.
const CACHED_TEMPLATES: usize = 32;

thread_local! {
    static HOPS: Cell<u32> = const { Cell::new(0) };
    static TEMPLATES: RefCell<Vec<CachedTemplate>> = const { RefCell::new(Vec::new()) };
}

struct CachedTemplate {
    path: String,
    modified: Option<SystemTime>,
    size: u64,
    root: SyntaxNode,
}

/// One step into another template; dropping it steps back.
struct Hop;

fn hop() -> Option<Hop> {
    HOPS.with(|hops| {
        if hops.get() >= MAX_HOPS {
            return None;
        }
        hops.set(hops.get() + 1);
        Some(Hop)
    })
}

impl Drop for Hop {
    fn drop(&mut self) {
        HOPS.with(|hops| hops.set(hops.get().saturating_sub(1)));
    }
}

/// The tree of the template at `path` as it is on disk.
pub(crate) fn load_template(path: &str) -> Option<SyntaxNode> {
    let metadata = std::fs::metadata(path).ok()?;
    let (modified, size) = (metadata.modified().ok(), metadata.len());
    TEMPLATES.with(|templates| {
        let mut templates = templates.borrow_mut();
        if let Some(index) = templates.iter().position(|cached| cached.path == path) {
            let cached = templates.remove(index);
            if cached.modified == modified && cached.size == size {
                let root = cached.root.clone();
                templates.push(cached);
                return Some(root);
            }
        }
        let source = std::fs::read_to_string(path).ok()?;
        let root = swlsp_parser_twig::parse(&source).root();
        if templates.len() >= CACHED_TEMPLATES {
            templates.remove(0);
        }
        templates.push(CachedTemplate {
            path: path.to_owned(),
            modified,
            size,
            root: root.clone(),
        });
        Some(root)
    })
}

/// The node of `kinds` that covers `range` most closely.
pub(crate) fn node_at(
    root: &SyntaxNode,
    range: TextRange,
    kinds: &[swlsp_syntax::Kind],
) -> Option<SyntaxNode> {
    if !root.text_range().contains_range(range) {
        return None;
    }
    let start = match root.covering_element(range) {
        rowan::NodeOrToken::Node(node) => node,
        rowan::NodeOrToken::Token(token) => token.parent()?,
    };
    start.ancestors().find(|node| kinds.contains(&node.kind()))
}

/// The literal start of every template name an `include` or `embed` builds
/// at runtime: `'@Storefront/element/cms-element-' ~ element.type ~ '.twig'`
/// starts with `@Storefront/element/cms-element-`.
pub(crate) fn dynamic_include_prefixes(root: &SyntaxNode) -> Vec<(String, TextRange)> {
    let mut result = Vec::new();
    for tag in twigquery::iterate_nodes(
        root,
        &[
            TWIG_INCLUDE,
            SHOPWARE_TWIG_SW_INCLUDE,
            TWIG_EMBED_STARTING_BLOCK,
        ],
    ) {
        let Some(mut current) = direct_child(&tag, TWIG_EXPRESSION) else {
            continue;
        };
        let mut concatenated = false;
        // The leftmost operand of the concatenation.
        loop {
            let next = match current.kind() {
                TWIG_EXPRESSION | TWIG_PARENTHESES_EXPRESSION => current.children().next(),
                TWIG_BINARY_EXPRESSION => {
                    concatenated = true;
                    current.children().next()
                }
                _ => None,
            };
            match next {
                Some(next) => current = next,
                None => break,
            }
        }
        if concatenated
            && current.kind() == TWIG_LITERAL_STRING
            && let Some(prefix) = twigquery::string_value(&current)
            && !prefix.is_empty()
        {
            result.push((prefix, current.text_range()));
        }
    }
    result
}

/// The innermost node at `offset`.
pub(crate) fn node_at_offset(
    root: &SyntaxNode,
    offset: swlsp_syntax::TextSize,
) -> Option<SyntaxNode> {
    root.token_at_offset(offset).right_biased()?.parent()
}

/// The comment that marks a template as a parent only, never rendered
/// itself.
pub const ABSTRACT_TEMPLATE_MARKER: &str = "@twigstan-abstract";

/// Whether the template at `path` is marked abstract.
pub fn template_file_is_abstract(path: &str) -> bool {
    load_template(path).is_some_and(|root| template_is_abstract(&root))
}

/// Whether a comment of the template marks it abstract.
pub fn template_is_abstract(root: &SyntaxNode) -> bool {
    twigquery::iterate_nodes(root, &[TWIG_COMMENT])
        .any(|comment| comment.text_string().contains(ABSTRACT_TEMPLATE_MARKER))
}

/// The tags and calls that render another template.
const INCLUDE_KINDS: &[swlsp_syntax::Kind] = &[
    TWIG_INCLUDE,
    SHOPWARE_TWIG_SW_INCLUDE,
    TWIG_EMBED,
    TWIG_FUNCTION_CALL,
    TWIG_EXTENDS,
    SHOPWARE_TWIG_SW_EXTENDS,
];

impl PhpAccessResolver<'_> {
    /// The resolver for another template, sharing the indexes.
    fn in_template(&self, root: &SyntaxNode) -> Self {
        self.clone().for_document(root)
    }

    /// The type of `name` where `anchor` stands in its template, as if the
    /// name were read there.
    pub(crate) fn name_type_at(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        anchor: &SyntaxNode,
        name: &str,
    ) -> Type {
        if let Some(local) = self.loop_variable_type(template_path, root, anchor, name) {
            return local;
        }
        if let Some(local) = self.local_name_type(template_path, root, anchor, name) {
            return local;
        }
        self.context_name_type(template_path, root, anchor, name)
    }

    /// The type `name` gets from the templates around this one: a variable
    /// its parent assigns, or what every template that includes or extends
    /// it provides. Unknown as soon as one of them provides nothing known.
    pub(crate) fn inherited_name_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        name: &str,
    ) -> Type {
        let Some(twig) = self.twig else {
            return Type::unknown();
        };
        let Some(_hop) = hop() else {
            return Type::unknown();
        };
        let assigned = self.parent_assigned_type(root, name);
        if !assigned.is_unknown() {
            return assigned;
        }
        let references = twig
            .get_template_references(&template_names(template_path))
            .unwrap_or_default();
        let sites: Vec<_> = references
            .into_iter()
            .filter(|reference| {
                matches!(
                    reference.kind,
                    TemplateReferenceKind::Include
                        | TemplateReferenceKind::Embed
                        | TemplateReferenceKind::Extends
                )
            })
            .collect();
        if sites.is_empty() || sites.len() > MAX_SITES {
            return Type::unknown();
        }
        let own_path = clean(template_path);
        let mut values = Vec::new();
        for site in sites {
            let other_root = if clean(&site.file_path) == own_path {
                Some(root.clone())
            } else {
                load_template(&site.file_path)
            };
            let Some(other_root) = other_root else {
                return Type::unknown();
            };
            let Some(tag) = node_at(&other_root, site.range, INCLUDE_KINDS) else {
                return Type::unknown();
            };
            let other = self.in_template(&other_root);
            values.push(other.provided_type(&site.file_path, &other_root, &tag, name));
        }
        all_known(values)
    }

    /// What the tag or call `tag`, which renders another template, provides
    /// for `name` there.
    fn provided_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        tag: &SyntaxNode,
        name: &str,
    ) -> Type {
        let (hash, has_with, passes_context) = match tag.kind() {
            TWIG_EXTENDS | SHOPWARE_TWIG_SW_EXTENDS => {
                // The child's own context, and what it assigns at the top.
                let assigned = self.top_level_assigned_type(template_path, root, name);
                if !assigned.is_unknown() {
                    return assigned;
                }
                return self.root_name_type(template_path, root, name);
            }
            TWIG_FUNCTION_CALL => {
                let callee = twigquery::function_name(tag).unwrap_or_default();
                if !matches!(callee.as_str(), "include" | "sw_include") {
                    return Type::unknown();
                }
                let arguments = direct_child(tag, TWIG_ARGUMENTS)
                    .map(|arguments| arguments.children().count())
                    .unwrap_or_default();
                let hash = direct_function_hash(tag);
                // A second argument that is no literal hash is not followed.
                let has_with = arguments > 1 && hash.is_some();
                if arguments > 1 && hash.is_none() && !has_named_second(tag) {
                    return Type::unknown();
                }
                (hash, has_with, include_function_propagates_context(tag))
            }
            _ => {
                let header = match tag.kind() {
                    TWIG_EMBED => match direct_child(tag, TWIG_EMBED_STARTING_BLOCK) {
                        Some(header) => header,
                        None => return Type::unknown(),
                    },
                    _ => tag.clone(),
                };
                let with = direct_child(&header, TWIG_INCLUDE_WITH);
                let hash = direct_with_hash(&header);
                if with.is_some() && hash.is_none() {
                    // `with variable`: not followed.
                    return Type::unknown();
                }
                (hash, with.is_some(), !has_token(&header, TK_ONLY))
            }
        };
        if has_with && let Some(value) = hash.as_ref().and_then(|hash| hash_value(hash, name)) {
            return self.expression_type(template_path, root, Some(&value));
        }
        if !passes_context {
            return Type::unknown();
        }
        self.name_type_at(template_path, root, tag, name)
    }

    /// The type of the last assignment to `name` at the top level of the
    /// document: what a child template assigns before its parent renders.
    fn top_level_assigned_type(&self, template_path: &str, root: &SyntaxNode, name: &str) -> Type {
        let last = root
            .children()
            .filter(|child| child.kind() == TWIG_SET)
            .last();
        match last {
            // Read where the last assignment ends: every earlier one counts.
            Some(last) => self
                .assigned_type_after(template_path, root, &last, name)
                .unwrap_or_else(Type::unknown),
            None => Type::unknown(),
        }
    }

    /// The type a parent template assigns to `name`, followed up the
    /// `extends` chain.
    fn parent_assigned_type(&self, root: &SyntaxNode, name: &str) -> Type {
        let Some(twig) = self.twig else {
            return Type::unknown();
        };
        let Some(extends) = root
            .children()
            .find(|child| matches!(child.kind(), TWIG_EXTENDS | SHOPWARE_TWIG_SW_EXTENDS))
        else {
            return Type::unknown();
        };
        let Some(parent_name) = direct_child(&extends, TWIG_EXPRESSION)
            .and_then(|expression| first_twig_child(&expression))
            .filter(|parent| parent.kind() == TWIG_LITERAL_STRING)
            .and_then(|parent| twigquery::string_value(&parent))
        else {
            return Type::unknown();
        };
        let parents = twig
            .get_twig_files_for_reference(Some(&extends), &parent_name)
            .unwrap_or_default();
        // An ambiguous parent (the same name in several bundles) is not
        // followed.
        let [parent] = parents.as_slice() else {
            return Type::unknown();
        };
        let Some(_hop) = hop() else {
            return Type::unknown();
        };
        let Some(parent_root) = load_template(&parent.path) else {
            return Type::unknown();
        };
        let other = self.in_template(&parent_root);
        let assigned = other.top_level_assigned_type(&parent.path, &parent_root, name);
        if !assigned.is_unknown() {
            return assigned;
        }
        other.parent_assigned_type(&parent_root, name)
    }

    /// The type of the `index`-th parameter of a macro: what its call sites
    /// pass, and its default where a call passes nothing.
    pub(crate) fn macro_parameter_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        macro_node: &SyntaxNode,
        index: usize,
    ) -> Type {
        let Some(_hop) = hop() else {
            return Type::unknown();
        };
        let Some(macro_name) = direct_child(macro_node, TWIG_MACRO_STARTING_BLOCK)
            .and_then(|start| start.child_token_of_kind(TK_WORD))
            .map(|token| token.text().to_owned())
        else {
            return Type::unknown();
        };
        let parameters = macro_parameter_names(macro_node);
        let Some(parameter) = parameters.get(index) else {
            return Type::unknown();
        };
        let own_names = template_names(template_path);
        let own_path = clean(template_path);
        // The calls in this document, as it is now.
        let mut calls: Vec<(String, SyntaxNode, SyntaxNode)> =
            macro_references_in_document(template_path, root)
                .into_iter()
                .filter(|reference| {
                    reference.role == MacroReferenceRole::Usage
                        && reference.name == macro_name
                        && reference
                            .templates
                            .iter()
                            .any(|template| own_names.contains(template))
                })
                .filter_map(|reference| node_at(root, reference.range, &[TWIG_FUNCTION_CALL]))
                .map(|call| (template_path.to_owned(), root.clone(), call))
                .collect();
        // The calls in other templates, as indexed.
        if let Some(twig) = self.twig {
            for template in &own_names {
                for usage in twig
                    .get_macro_usages(template, &macro_name)
                    .unwrap_or_default()
                {
                    if clean(&usage.file_path) == own_path {
                        continue;
                    }
                    let Some(other_root) = load_template(&usage.file_path) else {
                        return Type::unknown();
                    };
                    // An import names the macro without calling it.
                    if let Some(call) = node_at(&other_root, usage.range, &[TWIG_FUNCTION_CALL]) {
                        calls.push((usage.file_path, other_root, call));
                    }
                }
            }
        }
        calls.dedup_by(|left, right| left.0 == right.0 && left.2 == right.2);
        if calls.is_empty() || calls.len() > MAX_SITES {
            return Type::unknown();
        }
        let default = macro_parameter_default(macro_node, index);
        let mut values = Vec::new();
        for (path, call_root, call) in calls {
            // The reference names the macro; the call around it has the
            // arguments.
            if function_name_operand(&call).is_none() {
                continue;
            }
            let other = self.in_template(&call_root);
            let value = match call_argument(&call, index, parameter) {
                Some(argument) => other.expression_type(&path, &call_root, Some(&argument)),
                None => match &default {
                    Some(default) => self.expression_type(template_path, root, Some(default)),
                    None => Type::null(),
                },
            };
            values.push(value);
        }
        all_known(values)
    }
}

/// Whether the second argument of an `include()` call is named, so no
/// positional variables hash exists.
fn has_named_second(call: &SyntaxNode) -> bool {
    direct_child(call, TWIG_ARGUMENTS)
        .and_then(|arguments| arguments.children().nth(1))
        .is_some_and(|argument| argument.kind() == TWIG_NAMED_ARGUMENT)
}

/// The value expression of `key` in a literal hash.
fn hash_value(hash: &SyntaxNode, key: &str) -> Option<SyntaxNode> {
    let items = direct_child(hash, TWIG_LITERAL_HASH_ITEMS)?;
    direct_twig_children(&items, TWIG_LITERAL_HASH_PAIR)
        .into_iter()
        .find(|pair| {
            direct_child(pair, TWIG_LITERAL_HASH_KEY).is_some_and(|name| {
                let text = name.text_string();
                text.trim().trim_matches(['\'', '"']) == key
            })
        })
        .and_then(|pair| direct_child(&pair, TWIG_EXPRESSION))
}

/// The expression a call passes for the `index`-th parameter, by position
/// or by name.
fn call_argument(call: &SyntaxNode, index: usize, parameter: &str) -> Option<SyntaxNode> {
    let arguments = direct_child(call, TWIG_ARGUMENTS)?;
    let mut position = 0;
    for argument in arguments.children() {
        match argument.kind() {
            TWIG_NAMED_ARGUMENT => {
                let named = argument
                    .child_token_of_kind(TK_WORD)
                    .is_some_and(|token| token.text() == parameter);
                if named {
                    return direct_child(&argument, TWIG_EXPRESSION);
                }
            }
            TWIG_EXPRESSION => {
                if position == index {
                    return Some(argument);
                }
                position += 1;
            }
            _ => {}
        }
    }
    None
}

/// The default value of the `index`-th parameter of a macro.
fn macro_parameter_default(macro_node: &SyntaxNode, index: usize) -> Option<SyntaxNode> {
    let arguments = direct_child(macro_node, TWIG_MACRO_STARTING_BLOCK)
        .and_then(|start| direct_child(&start, TWIG_ARGUMENTS))?;
    let parameter = arguments
        .children()
        .filter(|argument| matches!(argument.kind(), TWIG_NAMED_ARGUMENT | TWIG_EXPRESSION))
        .nth(index)?;
    (parameter.kind() == TWIG_NAMED_ARGUMENT)
        .then(|| direct_child(&parameter, TWIG_EXPRESSION))
        .flatten()
}
