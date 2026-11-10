//! Validates the PHP method call behind a Twig attribute access.
//!
//! `{{ product.price(context) }}` and `{{ product.price }}` both end in a PHP
//! call when `price` resolves to a method: Twig tries `price()`,
//! `getPrice()`, `isPrice()` and `hasPrice()` in that order and passes the
//! arguments on. The call is only judged when the receiver's class is known
//! and has no magic `__call`; arguments of unknown type match anything.

use swlsp_parser_twig::syntax::*;
use swlsp_php_resolver::{Argument, MemberResolver, ResolvedMember, resolve_signature};
use swlsp_php_semantic::{Flags, Parameter, Snapshot, SymbolKind};
use swlsp_php_types::{Kind as TypeKind, Relations, Type};
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt as _};
use swlsp_textutil::{eq_ignore_case, lowercase};

use crate::php_access::{
    PhpAccessResolver, accessor_member_name_node, direct_twig_children, first_twig_child,
    resolve_php_members,
};

/// What is wrong with a call.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PhpCallProblemKind {
    /// The arguments match no method Twig would call.
    Arguments,
    /// `name()` is written with parentheses, which makes Twig look for a
    /// method only, but the class has just a property of that name.
    NotCallable,
}

/// An invalid PHP call in a Twig template.
#[derive(Clone, Debug)]
pub struct PhpCallProblem {
    pub kind: PhpCallProblemKind,
    /// The member name in the template.
    pub name_node: SyntaxNode,
    pub message: String,
}

/// A Twig argument as far as its type is known.
enum Passed {
    /// A value whose type is not known well enough to judge.
    Unknown,
    /// A string, number or boolean. Compiled templates do not declare
    /// strict types, so PHP converts between scalars: it only fails for a
    /// parameter that accepts no scalar at all.
    Scalar,
    Typed(Type),
}

impl PhpAccessResolver<'_> {
    /// The problem with the PHP call `accessor` makes, if it is certain.
    pub fn inspect_call(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        accessor: &SyntaxNode,
    ) -> Option<PhpCallProblem> {
        let name_node = accessor_member_name_node(accessor)?;
        let name = name_node.text_string().trim().to_owned();
        let receiver = self.accessor_receiver_type(template_path, root, accessor);
        let snapshot = self.snapshot();
        if !callable_receiver(snapshot, &receiver) {
            return None;
        }
        let arguments = call_arguments(accessor);
        let members = resolve_php_members(snapshot, &receiver, &name);
        if members.is_empty() {
            // `twig.member.missing` reports members that do not exist.
            return None;
        }
        let methods = twig_called_methods(&members, &name);
        let Some(arguments) = arguments else {
            // Without parentheses a property or constant wins and nothing
            // is called.
            let only_methods = members
                .iter()
                .all(|member| member.symbol.kind == SymbolKind::Method);
            if !only_methods || !self.plain_object(&receiver) {
                return None;
            }
            return self.arguments_problem(name_node, &methods, &[]);
        };
        if methods.is_empty() {
            return Some(PhpCallProblem {
                kind: PhpCallProblemKind::NotCallable,
                message: format!(
                    "'{name}' is a property of {}, not a method: with parentheses Twig only \
                     calls a method",
                    receiver.text()
                ),
                name_node,
            });
        }
        let passed = self.passed_arguments(template_path, root, &arguments)?;
        self.arguments_problem(name_node, &methods, &passed)
    }

    /// Without parentheses `value.name` reads an array key or a collection
    /// element before it calls anything, so only plain objects are judged.
    fn plain_object(&self, receiver: &Type) -> bool {
        let snapshot = self.snapshot();
        object_names(receiver).iter().all(|name| {
            ![
                "ArrayAccess",
                "Traversable",
                "Iterator",
                "IteratorAggregate",
            ]
            .iter()
            .any(|collection| snapshot.is_subtype_of(name, collection))
        })
    }

    /// `None` when an argument makes the call uncertain: a spread or a
    /// named argument.
    fn passed_arguments(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        arguments: &SyntaxNode,
    ) -> Option<Vec<Passed>> {
        let mut passed = Vec::new();
        for argument in arguments.children() {
            if argument.kind() != TWIG_EXPRESSION {
                return None;
            }
            let value = first_twig_child(&argument)?;
            if value.kind() == TWIG_UNARY_EXPRESSION && value.child_of_kind(TK_TRIPLE_DOT).is_some()
            {
                return None;
            }
            passed.push(match value.kind() {
                TWIG_LITERAL_STRING | TWIG_LITERAL_NUMBER | TWIG_LITERAL_BOOLEAN => Passed::Scalar,
                TWIG_LITERAL_NULL => Passed::Typed(Type::NULL),
                // An array of unknown content: it fits every array type.
                TWIG_LITERAL_ARRAY | TWIG_LITERAL_HASH => {
                    Passed::Typed(Type::array(Type::ARRAY_KEY, Type::NEVER))
                }
                _ => self.passed_expression(template_path, root, &value),
            });
        }
        Some(passed)
    }

    fn passed_expression(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        value: &SyntaxNode,
    ) -> Passed {
        let ty = self.expression_type(template_path, root, Some(value));
        match ty.kind() {
            TypeKind::String | TypeKind::Int | TypeKind::Float | TypeKind::Bool => Passed::Scalar,
            // A documented object type is trusted; anything looser (a
            // union, a nullable value, an array of something) is not.
            TypeKind::Object if !ty.name().is_empty() => Passed::Typed(ty),
            _ => Passed::Unknown,
        }
    }

    fn arguments_problem(
        &self,
        name_node: SyntaxNode,
        methods: &[&ResolvedMember],
        passed: &[Passed],
    ) -> Option<PhpCallProblem> {
        let relations = Relations {
            hierarchy: Some(&**self.snapshot()),
        };
        let arguments: Vec<Argument> = passed
            .iter()
            .map(|argument| match argument {
                Passed::Typed(ty) => Argument::of(ty.clone()),
                Passed::Unknown | Passed::Scalar => Argument::of(Type::UNKNOWN),
            })
            .collect();
        let mut mismatches = Vec::new();
        for method in methods {
            // Signatures the stub generator guessed are not evidence.
            if method.symbol.flags.has(Flags::GENERATED_STUB) {
                return None;
            }
            let resolved = resolve_signature(relations, method.symbol.clone(), &arguments);
            let mismatch = if resolved.compatible {
                scalar_mismatch(relations, &method.symbol.parameters, passed)
            } else {
                Some(resolved.mismatch)
            };
            // `None`: one method Twig may call accepts the arguments.
            mismatches.push((method, mismatch?));
        }
        let (method, mismatch) = mismatches.first()?;
        let callable = method.symbol.fully_qualified.trim_start_matches('\\');
        let mut message = format!("No matching signature for {callable}()");
        if mismatches.iter().all(|(_, other)| other == mismatch) && !mismatch.is_empty() {
            message.push_str(": ");
            message.push_str(mismatch);
        }
        Some(PhpCallProblem {
            kind: PhpCallProblemKind::Arguments,
            name_node,
            message,
        })
    }
}

/// The argument list when `accessor` is called with parentheses.
fn call_arguments(accessor: &SyntaxNode) -> Option<SyntaxNode> {
    let operand = accessor
        .parent()
        .filter(|node| node.kind() == TWIG_OPERAND)?;
    let call = operand
        .parent()
        .filter(|node| node.kind() == TWIG_FUNCTION_CALL)?;
    if direct_twig_children(&call, TWIG_OPERAND).first() != Some(&operand) {
        return None;
    }
    direct_twig_children(&call, TWIG_ARGUMENTS)
        .into_iter()
        .next()
}

/// The class names of an object type or a union of them; `null` is
/// ignored (Twig yields nothing for a missing value).
fn object_names(receiver: &Type) -> Vec<String> {
    match receiver.kind() {
        TypeKind::Object => vec![receiver.name().trim_matches('\\').to_owned()],
        TypeKind::Union => receiver.arguments().iter().flat_map(object_names).collect(),
        _ => Vec::new(),
    }
}

/// Whether every alternative of `receiver` is a known class without a
/// magic `__call`, so the methods found are the methods there are.
fn callable_receiver(snapshot: &Snapshot, receiver: &Type) -> bool {
    fn known(snapshot: &Snapshot, value: &Type) -> bool {
        match value.kind() {
            TypeKind::Null => true,
            TypeKind::Union => value.arguments().iter().all(|value| known(snapshot, value)),
            TypeKind::Object => {
                let name = value.name().trim_matches('\\');
                !name.is_empty()
                    && !snapshot.classes(name).is_empty()
                    && !eq_ignore_case(name, "stdClass")
                    && MemberResolver::new(snapshot)
                        .methods(&Type::named(name, Vec::new()), "__call")
                        .is_empty()
            }
            _ => false,
        }
    }
    !receiver.is_unknown() && known(snapshot, receiver) && !object_names(receiver).is_empty()
}

/// The methods Twig may call for `name`: per declaring class the first of
/// `name`, `getName`, `isName` and `hasName` that exists.
fn twig_called_methods<'m>(members: &'m [ResolvedMember], name: &str) -> Vec<&'m ResolvedMember> {
    let name = lowercase(name);
    let priority = |member: &ResolvedMember| {
        let method = lowercase(&member.symbol.name);
        ["", "get", "is", "has"]
            .iter()
            .position(|prefix| method.strip_prefix(prefix) == Some(name.as_str()))
            .unwrap_or(usize::MAX)
    };
    let methods: Vec<&ResolvedMember> = members
        .iter()
        .filter(|member| member.symbol.kind == SymbolKind::Method)
        .collect();
    methods
        .iter()
        .filter(|member| {
            let own = priority(member);
            !methods.iter().any(|other| {
                other.symbol.container == member.symbol.container && priority(other) < own
            })
        })
        .copied()
        .collect()
}

/// The first scalar argument passed to a parameter that accepts no scalar.
fn scalar_mismatch(
    relations: Relations<'_>,
    parameters: &[Parameter],
    passed: &[Passed],
) -> Option<String> {
    for (index, argument) in passed.iter().enumerate() {
        if !matches!(argument, Passed::Scalar) {
            continue;
        }
        let position = match parameters.get(index) {
            Some(_) => index,
            // Further arguments go to a variadic parameter, or nowhere.
            None => match parameters.last() {
                Some(last) if last.flags.has(Flags::VARIADIC) => parameters.len() - 1,
                _ => continue,
            },
        };
        let parameter = &parameters[position];
        let expected = &parameter.ty;
        if expected.is_unknown()
            || [Type::STRING, Type::INT, Type::FLOAT, Type::BOOL]
                .iter()
                .any(|scalar| relations.is_assignable_to(scalar, expected))
        {
            continue;
        }
        return Some(format!(
            "parameter #{} (${}) expects {}, a scalar value given",
            position + 1,
            parameter.name.trim_start_matches('$'),
            expected.text()
        ));
    }
    None
}
