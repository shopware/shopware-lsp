//! Variables a template reads that nothing provides: no render call, no
//! including or extending template, no assignment, declaration or global.
//!
//! A finding needs a known context: some PHP render call passes variables
//! to the template or to a template that includes or extends it. Templates
//! rendered by a name built at runtime, by vendor code or with a variable
//! as context would otherwise report every variable.

use std::collections::{HashSet, VecDeque};
use std::rc::Rc;

use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::*;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt as _, TextRange};

use crate::context::{load_template, node_at};
use crate::inference::macro_parameter_names;
use crate::macros::{direct_child, literal_name};
use crate::narrowing::tolerates_null;
use crate::path::{clean, template_names};
use crate::php_access::{PhpAccessResolver, direct_twig_children, twig_type_declarations};
use crate::reference::TemplateReferenceKind;
use crate::template_variable::{
    first_token_offset, is_descendant_node, macro_reads_in_document,
    template_input_variables_in_document,
};

/// More templates than this around one template are not searched; nothing
/// is reported then.
const MAX_FAMILY: usize = 48;

/// Names Twig and its common extensions define themselves.
const RUNTIME_NAMES: &[&str] = &[
    "_charset", "_context", "_key", "_parent", "_self", "_seq", "_view", "varargs",
];

/// A variable read that nothing defines.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct UndefinedVariable {
    pub name: String,
    pub range: TextRange,
    /// Whether the read is inside a macro, which sees only its parameters
    /// and the globals.
    pub in_macro: bool,
}

/// What the templates around one template provide.
#[derive(Default)]
pub(crate) struct Family {
    /// Some render call passes variables: the context is known.
    pub(crate) known: bool,
    /// Too many templates, an unreadable one, or a context passed as a
    /// variable: anything may be defined.
    pub(crate) open: bool,
    pub(crate) names: HashSet<String>,
    /// The PHP file and offset of every render call that reaches the
    /// template.
    pub(crate) sources: Vec<(String, u32)>,
}

impl PhpAccessResolver<'_> {
    /// The variables `root` reads that nothing defines, in source order.
    pub fn undefined_variables(
        &self,
        template_path: &str,
        root: &SyntaxNode,
    ) -> Vec<UndefinedVariable> {
        let declared = twig_type_declarations(root);
        let mut family: Option<Rc<Family>> = None;
        let mut result = Vec::new();
        let mut reads = template_input_variables_in_document(template_path, root);
        reads.extend(macro_reads_in_document(template_path, root));
        let mut reported = HashSet::new();
        // A loop whose node the surrounding HTML cut short still binds its
        // variable for the rest of its body.
        let loop_names = loop_variable_names(root);
        for input in reads {
            let name = input.name.as_str();
            if RUNTIME_NAMES.contains(&name) || declared.contains_key(name) {
                continue;
            }
            let Some(node) = node_at(root, input.range, &[TWIG_LITERAL_NAME]) else {
                continue;
            };
            if !is_variable_read(&node)
                || is_bound_around(&node, name)
                || tolerates_null(&node)
                || loop_names.contains(name)
            {
                continue;
            }
            if !self.global_type(name).is_unknown() || self.is_global(name) {
                continue;
            }
            let in_macro = node
                .ancestors()
                .any(|ancestor| ancestor.kind() == TWIG_MACRO);
            if !in_macro {
                let family = family.get_or_insert_with(|| self.family_of(template_path, root));
                if !family.known || family.open || family.names.contains(name) {
                    continue;
                }
            }
            if !reported.insert(input.range) {
                continue;
            }
            result.push(UndefinedVariable {
                name: input.name,
                range: input.range,
                in_macro,
            });
        }
        result.sort_by_key(|variable| variable.range.start());
        result
    }

    fn is_global(&self, name: &str) -> bool {
        self.twig
            .is_some_and(|twig| !twig.get_globals(name).unwrap_or_default().is_empty())
    }

    /// [`Self::family`], computed once for the prepared document.
    pub(crate) fn family_of(&self, template_path: &str, root: &SyntaxNode) -> Rc<Family> {
        if !self.is_prepared(root) {
            return Rc::new(self.family(template_path, root));
        }
        if let Some(family) = self.family.borrow().as_ref() {
            return family.clone();
        }
        let family = Rc::new(self.family(template_path, root));
        *self.family.borrow_mut() = Some(family.clone());
        family
    }

    /// What the render calls for `template_path` and the templates that
    /// include or extend it, and the parents it extends, provide.
    fn family(&self, template_path: &str, root: &SyntaxNode) -> Family {
        let mut family = Family::default();
        let (Some(php), Some(twig)) = (self.php, self.twig) else {
            family.open = true;
            return family;
        };
        let own_path = clean(template_path);
        let dynamic = twig.dynamic_include_prefixes();
        let mut seen: HashSet<String> = HashSet::from([own_path.clone()]);
        // Templates that hand their context down to this one.
        let mut providers: VecDeque<String> = VecDeque::from([own_path.clone()]);
        while let Some(path) = providers.pop_front() {
            let names = template_names(&path);
            // A template some include may name at runtime, or one a tag
            // renders with variables of its own, has unknown providers.
            if names.iter().any(|name| {
                rendered_by_tag(name) || dynamic.iter().any(|prefix| matches_prefix(name, prefix))
            }) {
                family.open = true;
            }
            // Keep going: the render calls still tell who renders the
            // template, even when what they pass is not fully known.
            family.open |= php.twig_template_context_open(&names);
            let rendered = php.twig_template_variables(&names).unwrap_or_default();
            family.known |= !rendered.is_empty();
            for variable in rendered {
                // The merged list keeps one render call per name.
                let sources = php
                    .twig_template_variable_sources(&variable.name, &names)
                    .unwrap_or_default();
                for source in sources {
                    let source = (source.file, source.range.start);
                    if !family.sources.contains(&source) {
                        family.sources.push(source);
                    }
                }
                family.names.insert(variable.name);
            }
            if path != own_path {
                let Some(other) = load_template(&path) else {
                    family.open = true;
                    return family;
                };
                family.open |= passes_unknown_context(&other);
                family.names.extend(bound_names(&other));
            }
            for reference in twig.get_template_references(&names).unwrap_or_default() {
                if !matches!(
                    reference.kind,
                    TemplateReferenceKind::Include
                        | TemplateReferenceKind::Embed
                        | TemplateReferenceKind::Extends
                ) {
                    continue;
                }
                let referencing = clean(&reference.file_path);
                if seen.insert(referencing.clone()) {
                    providers.push_back(referencing);
                }
            }
            if seen.len() > MAX_FAMILY {
                family.open = true;
                return family;
            }
        }
        // The parents: what they assign is visible in the blocks.
        let mut current = root.clone();
        for _ in 0..MAX_FAMILY {
            let Some(parent) = self.parent_template(&current) else {
                break;
            };
            if !seen.insert(clean(&parent)) {
                break;
            }
            let Some(parent_root) = load_template(&parent) else {
                family.open = true;
                break;
            };
            family.names.extend(bound_names(&parent_root));
            current = parent_root;
        }
        family
    }

    /// The path of the single template `root` extends.
    fn parent_template(&self, root: &SyntaxNode) -> Option<String> {
        let extends = root
            .children()
            .find(|child| matches!(child.kind(), TWIG_EXTENDS | SHOPWARE_TWIG_SW_EXTENDS))?;
        let name = direct_child(&extends, TWIG_EXPRESSION)
            .and_then(|expression| expression.children().next())
            .filter(|parent| parent.kind() == TWIG_LITERAL_STRING)
            .and_then(|parent| twigquery::string_value(&parent))?;
        let parents = self
            .twig?
            .get_twig_files_for_reference(Some(&extends), &name)
            .ok()?;
        match parents.as_slice() {
            [parent] => Some(parent.path.clone()),
            _ => None,
        }
    }
}

/// Templates a Shopware tag renders with variables of its own: `sw_icon`
/// passes the icon name, pack and style options.
const TAG_RENDERED_TEMPLATES: &[&str] = &["storefront/utilities/icon.html.twig"];

fn rendered_by_tag(name: &str) -> bool {
    let name = without_bundle(name);
    TAG_RENDERED_TEMPLATES
        .iter()
        .any(|template| name.ends_with(template))
}

/// `name` without a leading `@Bundle/`.
fn without_bundle(name: &str) -> &str {
    match name.strip_prefix('@') {
        Some(rest) => rest.split_once('/').map_or(name, |(_, path)| path),
        None => name,
    }
}

fn matches_prefix(name: &str, prefix: &str) -> bool {
    without_bundle(name).starts_with(without_bundle(prefix))
}

/// The key and value names of every `for` loop of the document.
fn loop_variable_names(root: &SyntaxNode) -> HashSet<String> {
    let mut result = HashSet::new();
    for header in twigquery::iterate_nodes(root, &[TWIG_FOR_BLOCK]) {
        let Some(in_offset) = first_token_offset(&header, TK_IN) else {
            continue;
        };
        for name in direct_twig_children(&header, TWIG_LITERAL_NAME) {
            if u32::from(name.text_range().start()) < in_offset {
                result.insert(literal_name(&name).0);
            }
        }
    }
    result
}

/// Whether the name is read as a variable: not the name of a test after
/// `is`, not a filter of `{% apply %}`.
fn is_variable_read(node: &SyntaxNode) -> bool {
    if node
        .ancestors()
        .any(|ancestor| ancestor.kind() == TWIG_APPLY_STARTING_BLOCK)
    {
        return false;
    }
    let Some(expression) = node
        .parent()
        .filter(|parent| parent.kind() == TWIG_EXPRESSION)
    else {
        return true;
    };
    let Some(binary) = expression
        .parent()
        .filter(|parent| parent.kind() == TWIG_BINARY_EXPRESSION)
    else {
        return true;
    };
    let is_test = binary
        .children_with_tokens()
        .filter_map(|element| element.into_token())
        .any(|token| matches!(token.kind(), TK_IS | TK_IS_NOT));
    !(is_test && binary.children().last().as_ref() == Some(&expression))
}

/// Whether a construct around the read binds `name`: an arrow function
/// parameter, the hash of `with` or `embed`, a macro parameter.
fn is_bound_around(node: &SyntaxNode, name: &str) -> bool {
    for ancestor in node.ancestors().skip(1) {
        match ancestor.kind() {
            TWIG_ARROW_FUNCTION => {
                let bound = direct_child(&ancestor, TWIG_ARGUMENTS).is_some_and(|arguments| {
                    twigquery::iterate_nodes(&arguments, &[TWIG_LITERAL_NAME])
                        .any(|parameter| literal_name(&parameter).0 == name)
                });
                if bound {
                    return true;
                }
            }
            TWIG_WITH | TWIG_EMBED => {
                let header = direct_child(&ancestor, TWIG_WITH_STARTING_BLOCK)
                    .or_else(|| direct_child(&ancestor, TWIG_EMBED_STARTING_BLOCK));
                if let Some(header) = header
                    && !is_descendant_node(node, &header)
                    && hash_keys(&header).any(|key| key == name)
                {
                    return true;
                }
            }
            TWIG_MACRO => {
                return macro_parameter_names(&ancestor)
                    .iter()
                    .any(|parameter| parameter == name);
            }
            _ => {}
        }
    }
    false
}

fn hash_keys(node: &SyntaxNode) -> impl Iterator<Item = String> {
    twigquery::iterate_nodes(node, &[TWIG_LITERAL_HASH_KEY])
        .map(|key| {
            key.text_string()
                .trim()
                .trim_matches(['\'', '"'])
                .to_owned()
        })
        .collect::<Vec<_>>()
        .into_iter()
}

/// Every name a template may hand to the templates it renders or that
/// extend it: what it assigns, loops over, declares, and every hash key.
fn bound_names(root: &SyntaxNode) -> HashSet<String> {
    let mut result: HashSet<String> = twig_type_declarations(root).into_keys().collect();
    for assignment in twigquery::iterate_nodes(root, &[TWIG_ASSIGNMENT]) {
        let equal = first_token_offset(&assignment, TK_EQUAL);
        for name in direct_twig_children(&assignment, TWIG_LITERAL_NAME) {
            if equal.is_none_or(|equal| u32::from(name.text_range().start()) < equal) {
                result.insert(literal_name(&name).0);
            }
        }
    }
    for header in twigquery::iterate_nodes(root, &[TWIG_FOR_BLOCK]) {
        let Some(in_offset) = first_token_offset(&header, TK_IN) else {
            continue;
        };
        for name in direct_twig_children(&header, TWIG_LITERAL_NAME) {
            if u32::from(name.text_range().start()) < in_offset {
                result.insert(literal_name(&name).0);
            }
        }
    }
    result.extend(hash_keys(root));
    result
}

/// Whether a tag passes a context that is not a literal hash:
/// `include 'x' with variables`.
fn passes_unknown_context(root: &SyntaxNode) -> bool {
    twigquery::iterate_nodes(root, &[TWIG_INCLUDE_WITH]).any(|with| {
        direct_child(&with, TWIG_EXPRESSION)
            .and_then(|expression| expression.children().next())
            .is_none_or(|value| value.kind() != TWIG_LITERAL_HASH)
    })
}
