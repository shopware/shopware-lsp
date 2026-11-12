//! Types of Twig expressions beyond plain attribute chains: variables
//! assigned with `{% set %}`, arrow function and macro parameters, index
//! lookups, operators and literals.

use std::cell::Cell;
use std::collections::HashMap;

use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::*;
use swlsp_php_resolver::MemberResolver;
use swlsp_php_types::{Kind as TypeKind, ShapeField, Type};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt as _};

use crate::macros::{direct_child, literal_name};
use crate::php_access::{
    PhpAccessResolver, direct_twig_children, first_twig_child, resolve_php_member_type,
    union_known_types,
};
use crate::template_variable::is_descendant_node;

/// How deep expression typing may recurse through assignments and nested
/// expressions before it gives up.
const MAX_DEPTH: u32 = 48;

thread_local! {
    static DEPTH: Cell<u32> = const { Cell::new(0) };
}

/// One level of expression typing; dropping it leaves the level.
pub(crate) struct DepthGuard;

/// Enters one level, or `None` when typing recursed too deep (a cycle such
/// as `{% set a = b %}{% set b = a %}` in a loop).
pub(crate) fn enter() -> Option<DepthGuard> {
    DEPTH.with(|depth| {
        if depth.get() >= MAX_DEPTH {
            return None;
        }
        depth.set(depth.get() + 1);
        Some(DepthGuard)
    })
}

impl Drop for DepthGuard {
    fn drop(&mut self) {
        DEPTH.with(|depth| depth.set(depth.get().saturating_sub(1)));
    }
}

/// One `{% set %}` of a variable.
#[derive(Clone, Debug)]
pub(crate) struct SetBinding {
    /// The whole tag, with body and `endset` for a capture.
    pub(crate) set: SyntaxNode,
    /// The assigned expression; `None` for a capture.
    pub(crate) value: Option<SyntaxNode>,
}

/// The `{% set %}` tags of one document by variable name, in source order.
pub(crate) type SetIndex = HashMap<String, Vec<SetBinding>>;

pub(crate) fn set_index(root: &SyntaxNode) -> SetIndex {
    let mut result = SetIndex::new();
    for set in twigquery::iterate_nodes(root, &[TWIG_SET]) {
        let Some(assignment) = direct_child(&set, TWIG_SET_BLOCK)
            .and_then(|block| direct_child(&block, TWIG_ASSIGNMENT))
        else {
            continue;
        };
        let names = direct_twig_children(&assignment, TWIG_LITERAL_NAME);
        let values = direct_twig_children(&assignment, TWIG_EXPRESSION);
        for (index, name) in names.iter().enumerate() {
            let (name, _) = literal_name(name);
            if name.is_empty() {
                continue;
            }
            result.entry(name).or_default().push(SetBinding {
                set: set.clone(),
                value: values.get(index).cloned(),
            });
        }
    }
    result
}

/// How an assignment relates to a later use of its variable.
#[derive(Clone, Copy, PartialEq, Eq)]
pub(crate) enum Reach {
    /// Runs on every path to the use.
    Always,
    /// Runs on some paths: it sits in a branch or loop the use is not in.
    Sometimes,
    /// Never reaches the use: another branch, a block or another macro.
    Never,
}

pub(crate) fn reach(set: &SyntaxNode, name_node: &SyntaxNode) -> Reach {
    if set.text_range().end() > name_node.text_range().start() {
        return Reach::Never;
    }
    if enclosing(set, TWIG_MACRO) != enclosing(name_node, TWIG_MACRO) {
        return Reach::Never;
    }
    let mut result = Reach::Always;
    for ancestor in set.ancestors().skip(1) {
        if is_descendant_node(name_node, &ancestor) {
            // The shared construct: a branch of it the use is not in never
            // runs together with the use.
            if matches!(ancestor.kind(), TWIG_IF | TWIG_FOR) {
                return Reach::Never;
            }
            if ancestor.kind() == BODY {
                return result;
            }
            continue;
        }
        match ancestor.kind() {
            // A block and an embed render with a copy of the context.
            TWIG_BLOCK | TWIG_EMBED | TWIG_MACRO => return Reach::Never,
            TWIG_IF | TWIG_FOR => result = Reach::Sometimes,
            _ => {}
        }
    }
    result
}

fn enclosing(node: &SyntaxNode, kind: swlsp_syntax::Kind) -> Option<SyntaxNode> {
    node.ancestors()
        .skip(1)
        .find(|ancestor| ancestor.kind() == kind)
}

/// The parameter names of a macro, in order.
pub(crate) fn macro_parameter_names(macro_node: &SyntaxNode) -> Vec<String> {
    let Some(arguments) = direct_child(macro_node, TWIG_MACRO_STARTING_BLOCK)
        .and_then(|start| direct_child(&start, TWIG_ARGUMENTS))
    else {
        return Vec::new();
    };
    arguments
        .children()
        .filter_map(|argument| match argument.kind() {
            TWIG_NAMED_ARGUMENT => argument
                .child_token_of_kind(TK_WORD)
                .map(|token| token.text().to_owned()),
            TWIG_EXPRESSION => first_twig_child(&argument)
                .filter(|name| name.kind() == TWIG_LITERAL_NAME)
                .map(|name| literal_name(&name).0),
            _ => None,
        })
        .collect()
}

/// The parameter names of an arrow function, in order.
fn arrow_parameter_names(arrow: &SyntaxNode) -> Vec<String> {
    direct_child(arrow, TWIG_ARGUMENTS)
        .map(|arguments| {
            twigquery::iterate_nodes(&arguments, &[TWIG_LITERAL_NAME])
                .map(|name| literal_name(&name).0)
                .collect()
        })
        .unwrap_or_default()
}

/// The filter an arrow function is an argument of, with its name.
fn arrow_filter(arrow: &SyntaxNode) -> Option<(SyntaxNode, String)> {
    let filter = arrow.ancestors().skip(1).find(|ancestor| {
        ancestor.kind() == TWIG_FILTER
            && direct_twig_children(ancestor, TWIG_OPERAND)
                .get(1)
                .is_some_and(|name| is_descendant_node(arrow, name))
    })?;
    let name = twigquery::filter_name(&filter)?;
    Some((filter, name))
}

impl PhpAccessResolver<'_> {
    /// The type of a name bound inside the template: an arrow function or
    /// macro parameter, or a variable assigned with `{% set %}`. `None` when
    /// the name is not bound locally; an unknown type when it is but its
    /// value cannot be typed.
    pub(crate) fn local_name_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        name_node: &SyntaxNode,
        name: &str,
    ) -> Option<Type> {
        if let Some(parameter) = self.arrow_parameter_type(template_path, root, name_node, name) {
            return Some(parameter);
        }
        if let Some(scoped) = self.scoped_hash_type(template_path, root, name_node, name) {
            return Some(scoped);
        }
        if let Some(assigned) = self.assigned_type(template_path, root, name_node, name) {
            return Some(assigned);
        }
        let macro_node = enclosing(name_node, TWIG_MACRO)?;
        macro_parameter_names(&macro_node)
            .iter()
            .position(|parameter| parameter == name)
            .map(|index| self.macro_parameter_type(template_path, root, &macro_node, index))
    }

    fn arrow_parameter_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        name_node: &SyntaxNode,
        name: &str,
    ) -> Option<Type> {
        for arrow in name_node.ancestors().skip(1) {
            if arrow.kind() != TWIG_ARROW_FUNCTION {
                continue;
            }
            let names = arrow_parameter_names(&arrow);
            let Some(index) = names.iter().position(|parameter| parameter == name) else {
                continue;
            };
            let Some((filter, filter_name)) = arrow_filter(&arrow) else {
                return Some(Type::unknown());
            };
            let input = direct_twig_children(&filter, TWIG_OPERAND)
                .first()
                .and_then(first_twig_child);
            let (key, value) =
                self.iterable_types(&self.expression_type(template_path, root, input.as_ref()));
            // `sort` compares two elements and `reduce` carries a result;
            // the others receive the element and then its key.
            return Some(match (filter_name.as_str(), index) {
                ("sort", _) => value,
                ("reduce", 0) => Type::unknown(),
                ("reduce", 1) => value,
                ("reduce", _) => key,
                (_, 0) => value,
                (_, 1) => key,
                _ => Type::unknown(),
            });
        }
        None
    }

    /// The type `name` has right after `node`, when an assignment up to and
    /// including `node` reaches there.
    pub(crate) fn assigned_type_after(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        node: &SyntaxNode,
        name: &str,
    ) -> Option<Type> {
        let sets = set_index(root);
        let binding = sets.get(name)?.iter().rev().find(|binding| {
            binding.set.parent().as_ref() == node.parent().as_ref()
                && binding.set.text_range().end() <= node.text_range().end()
        })?;
        Some(match &binding.value {
            Some(value) => self.expression_type(template_path, root, Some(value)),
            None => self.captured_type(),
        })
    }

    /// The type of a name inside `{% with {…} %}` or the blocks of
    /// `{% embed … with {…} %}`: the value the hash gives it. With `only`
    /// nothing else but the globals is visible there.
    fn scoped_hash_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        name_node: &SyntaxNode,
        name: &str,
    ) -> Option<Type> {
        for scope in name_node.ancestors().skip(1) {
            let header = match scope.kind() {
                TWIG_WITH => direct_child(&scope, TWIG_WITH_STARTING_BLOCK),
                TWIG_EMBED => direct_child(&scope, TWIG_EMBED_STARTING_BLOCK),
                _ => continue,
            };
            let Some(header) = header else {
                continue;
            };
            if is_descendant_node(name_node, &header) {
                continue;
            }
            let hash = twigquery::iterate_nodes(&header, &[TWIG_LITERAL_HASH]).next();
            let value = hash.as_ref().and_then(|hash| {
                direct_child(hash, TWIG_LITERAL_HASH_ITEMS)?
                    .children()
                    .filter(|pair| pair.kind() == TWIG_LITERAL_HASH_PAIR)
                    .find(|pair| {
                        direct_child(pair, TWIG_LITERAL_HASH_KEY)
                            .and_then(|key| hash_key(&key))
                            .is_some_and(|key| key == name)
                    })
                    .and_then(|pair| direct_child(&pair, TWIG_EXPRESSION))
            });
            if let Some(value) = value {
                return Some(self.expression_type(template_path, root, Some(&value)));
            }
            if crate::template_variable::has_token(&header, TK_ONLY) {
                return Some(self.global_type(name));
            }
        }
        None
    }

    fn assigned_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        name_node: &SyntaxNode,
        name: &str,
    ) -> Option<Type> {
        let prepared = self.prepared_sets(root);
        let computed;
        let sets = match &prepared {
            Some(sets) => sets.as_ref(),
            None => {
                computed = set_index(root);
                &computed
            }
        };
        let bindings = sets.get(name)?;
        let mut values = Vec::new();
        let mut bound = false;
        for binding in bindings.iter().rev() {
            let reach = reach(&binding.set, name_node);
            if reach == Reach::Never {
                continue;
            }
            bound = true;
            values.push(self.binding_type(template_path, root, name, binding));
            if reach == Reach::Always {
                return Some(all_known(values));
            }
        }
        if !bound {
            return None;
        }
        // Assigned on some paths only: elsewhere the variable keeps the
        // value it had before, which must be known as well.
        let outer = if enclosing(name_node, TWIG_MACRO).is_some() {
            Type::unknown()
        } else {
            self.root_name_type(template_path, root, name)
        };
        values.push(outer);
        Some(all_known(values))
    }

    /// The type one assignment gives `name`, computed once per document: a
    /// variable reassigned in many branches would otherwise be typed again
    /// for every path through them.
    fn binding_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        name: &str,
        binding: &SetBinding,
    ) -> Type {
        let Some(value) = &binding.value else {
            return self.captured_type();
        };
        if !self.is_prepared(root) {
            return self.expression_type(template_path, root, Some(value));
        }
        let key = (u32::from(binding.set.text_range().start()), name.to_owned());
        if let Some(known) = self.bindings.borrow().get(&key) {
            return known.clone();
        }
        let result = self.expression_type(template_path, root, Some(value));
        self.bindings.borrow_mut().insert(key, result.clone());
        result
    }

    /// What `{% set name %}…{% endset %}` assigns: the captured markup.
    fn captured_type(&self) -> Type {
        const MARKUP: &str = "Twig\\Markup";
        if self.snapshot().classes(MARKUP).is_empty() {
            Type::string()
        } else {
            Type::named(MARKUP, Vec::new())
        }
    }

    /// The type of the member `name` of `receiver`: a PHP property or
    /// method, an array element or a shape field.
    pub(crate) fn attribute_type(&self, receiver: &Type, name: &str) -> Type {
        match receiver.kind() {
            TypeKind::Union => {
                let members: Vec<&Type> = receiver
                    .arguments()
                    .iter()
                    .filter(|member| member.kind() != TypeKind::Null)
                    .collect();
                if members.iter().all(|member| {
                    matches!(member.kind(), TypeKind::Object | TypeKind::Intersection)
                }) {
                    return resolve_php_member_type(self.snapshot(), receiver, name);
                }
                all_known(
                    members
                        .into_iter()
                        .map(|member| self.attribute_type(member, name))
                        .collect(),
                )
            }
            TypeKind::ArrayShape => {
                match receiver.fields().iter().find(|field| &*field.name == name) {
                    Some(field) if field.optional => Type::nullable(field.ty.clone()),
                    Some(field) => field.ty.clone(),
                    None => Type::unknown(),
                }
            }
            TypeKind::Array
            | TypeKind::NonEmptyArray
            | TypeKind::List
            | TypeKind::NonEmptyList
            | TypeKind::Iterable => self.iterable_types(receiver).1,
            _ => resolve_php_member_type(self.snapshot(), receiver, name),
        }
    }

    /// `value[index]`.
    pub(crate) fn index_lookup_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        lookup: &SyntaxNode,
    ) -> Type {
        let receiver =
            direct_child(lookup, TWIG_OPERAND).and_then(|operand| first_twig_child(&operand));
        let receiver = self.expression_type(template_path, root, receiver.as_ref());
        if receiver.is_unknown() {
            return receiver;
        }
        let key = direct_child(lookup, TWIG_INDEX)
            .and_then(|index| direct_child(&index, TWIG_EXPRESSION))
            .and_then(|expression| first_twig_child(&expression))
            .filter(|key| key.kind() == TWIG_LITERAL_STRING)
            .and_then(|key| twigquery::string_value(&key));
        self.offset_type(&receiver, key.as_deref())
    }

    /// The type of `receiver[key]`; `key` is the literal string key, if the
    /// lookup uses one.
    fn offset_type(&self, receiver: &Type, key: Option<&str>) -> Type {
        match receiver.kind() {
            TypeKind::Union => all_known(
                receiver
                    .arguments()
                    .iter()
                    .filter(|member| member.kind() != TypeKind::Null)
                    .map(|member| self.offset_type(member, key))
                    .collect(),
            ),
            TypeKind::Object => {
                let offset_get: Vec<Type> = MemberResolver::new(self.snapshot())
                    .methods(receiver, "offsetGet")
                    .into_iter()
                    .map(|member| member.ty)
                    .collect();
                match (offset_get.is_empty(), key) {
                    (false, _) => all_known(offset_get),
                    // Twig falls back to the attribute of that name.
                    (true, Some(key)) => self.attribute_type(receiver, key),
                    (true, None) => Type::unknown(),
                }
            }
            TypeKind::ArrayShape => match key {
                Some(key) => self.attribute_type(receiver, key),
                None => self.iterable_types(receiver).1,
            },
            TypeKind::String | TypeKind::LiteralString => Type::string(),
            _ => self.iterable_types(receiver).1,
        }
    }

    /// `condition ? then : else` and `value ?: else`.
    pub(crate) fn conditional_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        conditional: &SyntaxNode,
    ) -> Type {
        let parts = direct_twig_children(conditional, TWIG_EXPRESSION);
        let elvis = conditional
            .children_with_tokens()
            .filter_map(|element| element.into_token())
            .skip_while(|token| token.kind() != TK_QUESTION_MARK)
            .nth(1)
            .is_some_and(|token| token.kind() == TK_COLON);
        let types: Vec<Type> = match (parts.as_slice(), elvis) {
            ([value, fallback], true) => vec![
                without_null(&self.expression_type(template_path, root, Some(value))),
                self.expression_type(template_path, root, Some(fallback)),
            ],
            ([_, then, otherwise], _) => vec![
                self.expression_type(template_path, root, Some(then)),
                self.expression_type(template_path, root, Some(otherwise)),
            ],
            // `condition ? then` yields an empty string otherwise.
            ([_, then], false) => vec![
                self.expression_type(template_path, root, Some(then)),
                Type::string(),
            ],
            _ => return Type::unknown(),
        };
        all_known(types)
    }

    /// `left <operator> right`.
    pub(crate) fn binary_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        binary: &SyntaxNode,
    ) -> Type {
        let operator = binary
            .children_with_tokens()
            .filter_map(|element| element.into_token())
            .find(|token| !matches!(token.kind(), TK_WHITESPACE | TK_LINE_BREAK));
        let Some(operator) = operator else {
            return Type::unknown();
        };
        let operands = crate::narrowing::binary_operands(binary);
        let operand = |index: usize| self.expression_type(template_path, root, operands.get(index));
        match operator.text() {
            "??" => all_known(vec![without_null(&operand(0)), operand(1)]),
            "~" => Type::string(),
            "+" | "-" | "*" | "**" | "%" => match (operand(0).kind(), operand(1).kind()) {
                (TypeKind::Int | TypeKind::LiteralInt, TypeKind::Int | TypeKind::LiteralInt) => {
                    Type::int()
                }
                _ => Type::union([Type::int(), Type::float()]),
            },
            "/" => Type::union([Type::int(), Type::float()]),
            "//" => Type::int(),
            ".." => Type::list(Type::int()),
            // `a or b` and `a and b` yield booleans in Twig, like every
            // comparison and test.
            _ => Type::bool(),
        }
    }

    /// `not value`, `-value` and `+value`.
    pub(crate) fn unary_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        unary: &SyntaxNode,
    ) -> Type {
        let negation = unary
            .children_with_tokens()
            .filter_map(|element| element.into_token())
            .any(|token| token.kind() == TK_NOT);
        if negation {
            return Type::bool();
        }
        let operand = direct_child(unary, TWIG_EXPRESSION);
        match self
            .expression_type(template_path, root, operand.as_ref())
            .kind()
        {
            TypeKind::Int | TypeKind::LiteralInt => Type::int(),
            TypeKind::Float | TypeKind::LiteralFloat => Type::float(),
            _ => Type::union([Type::int(), Type::float()]),
        }
    }

    /// `[a, b]`.
    pub(crate) fn array_literal_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        array: &SyntaxNode,
    ) -> Type {
        let elements: Vec<Type> = direct_child(array, TWIG_LITERAL_ARRAY_INNER)
            .map(|inner| direct_twig_children(&inner, TWIG_EXPRESSION))
            .unwrap_or_default()
            .iter()
            .map(|element| self.expression_type(template_path, root, Some(element)))
            .collect();
        if elements.is_empty() || elements.iter().any(Type::is_unknown) {
            return Type::list(Type::mixed());
        }
        Type::list(union_known_types(elements))
    }

    /// `{key: value}`: a shape when every key is a literal.
    pub(crate) fn hash_literal_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        hash: &SyntaxNode,
    ) -> Type {
        let open = Type::array(Type::string(), Type::mixed());
        let Some(items) = direct_child(hash, TWIG_LITERAL_HASH_ITEMS) else {
            return open;
        };
        let mut fields = Vec::new();
        for item in items.children() {
            if item.kind() != TWIG_LITERAL_HASH_PAIR {
                // A spread or a malformed entry.
                return open;
            }
            let Some(key) =
                direct_child(&item, TWIG_LITERAL_HASH_KEY).and_then(|key| hash_key(&key))
            else {
                return open;
            };
            let value = match direct_child(&item, TWIG_EXPRESSION) {
                Some(value) => self.expression_type(template_path, root, Some(&value)),
                // `{name}` is short for `{name: name}`.
                None => Type::unknown(),
            };
            let value = if value.is_unknown() {
                Type::mixed()
            } else {
                value
            };
            fields.push(ShapeField::new(&key, value));
        }
        if fields.is_empty() {
            return open;
        }
        Type::array_shape(fields, false)
    }
}

/// The literal name of a hash key: a bare word or a plain string.
fn hash_key(key: &SyntaxNode) -> Option<String> {
    if let Some(string) = key
        .descendants()
        .find(|node| node.kind() == TWIG_LITERAL_STRING)
    {
        return twigquery::string_is_static(&string)
            .then(|| twigquery::string_value(&string))
            .flatten();
    }
    if key.children().next().is_some() {
        // A computed key: `(expression)`.
        return None;
    }
    let text = key.text_string();
    let text = text.trim();
    (!text.is_empty()
        && text
            .chars()
            .all(|character| character.is_alphanumeric() || character == '_'))
    .then(|| text.to_owned())
}

/// The union of `values`, or unknown when any of them is unknown: a partly
/// known type would report members the unknown part may well have.
pub(crate) fn all_known(values: Vec<Type>) -> Type {
    if values.is_empty() || values.iter().any(Type::is_unknown) {
        return Type::unknown();
    }
    union_known_types(values)
}

/// `value` without `null`.
pub(crate) fn without_null(value: &Type) -> Type {
    match value.kind() {
        TypeKind::Null => Type::unknown(),
        TypeKind::Union => union_known_types(
            value
                .arguments()
                .iter()
                .filter(|member| member.kind() != TypeKind::Null)
                .cloned()
                .collect(),
        ),
        _ => value.clone(),
    }
}

/// Whether `value` can be `null`.
pub fn is_nullable(value: &Type) -> bool {
    match value.kind() {
        TypeKind::Null => true,
        TypeKind::Union => value
            .arguments()
            .iter()
            .any(|member| member.kind() == TypeKind::Null),
        _ => false,
    }
}
