//! Result types of Twig's built-in filters and functions that depend on
//! their input: `items|first` is an element of `items`, not `mixed`.

use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::*;
use swlsp_php_types::{Kind as TypeKind, Type};
use swlsp_syntax::SyntaxNode;

use crate::inference::{all_known, without_null};
use crate::macros::direct_child;
use crate::php_access::{
    PhpAccessResolver, direct_twig_children, first_twig_child, resolve_php_member_type,
};

/// Filters that always yield a string.
const STRING_FILTERS: &[&str] = &[
    "capitalize",
    "convert_encoding",
    "date",
    "e",
    "escape",
    "format",
    "join",
    "json_encode",
    "lower",
    "nl2br",
    "number_format",
    "raw",
    "replace",
    "spaceless",
    "striptags",
    "title",
    "trim",
    "upper",
    "url_encode",
];

impl PhpAccessResolver<'_> {
    /// The result of a built-in filter whose type follows from its input,
    /// or `None` for any other filter.
    pub(crate) fn builtin_filter_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        filter: &SyntaxNode,
        name: &str,
    ) -> Option<Type> {
        let operands = direct_twig_children(filter, TWIG_OPERAND);
        let input = || {
            self.expression_type(
                template_path,
                root,
                operands.first().and_then(first_twig_child).as_ref(),
            )
        };
        let arguments: Vec<SyntaxNode> = operands
            .get(1)
            .and_then(|operand| direct_child(operand, TWIG_ARGUMENTS))
            .map(|arguments| direct_twig_children(&arguments, TWIG_EXPRESSION))
            .unwrap_or_default();
        let argument =
            |index: usize| self.expression_type(template_path, root, arguments.get(index));
        let arrow_result = |index: usize| {
            arguments
                .get(index)
                .and_then(first_twig_child)
                .filter(|arrow| arrow.kind() == TWIG_ARROW_FUNCTION)
                .and_then(|arrow| direct_child(&arrow, TWIG_EXPRESSION))
                .map(|body| self.expression_type(template_path, root, Some(&body)))
                .unwrap_or_else(Type::unknown)
        };
        Some(match name {
            "first" | "last" => {
                let input = input();
                if is_string(&input) {
                    return Some(Type::string());
                }
                let element = self.iterable_types(&input).1;
                if element.is_unknown() {
                    element
                } else {
                    Type::nullable(element)
                }
            }
            "find" => {
                let element = self.iterable_types(&input()).1;
                if element.is_unknown() {
                    element
                } else {
                    Type::nullable(element)
                }
            }
            "keys" => {
                let key = self.iterable_types(&input()).0;
                if key.is_unknown() {
                    key
                } else {
                    Type::list(key)
                }
            }
            // The same elements, possibly fewer or in another order.
            "filter" | "sort" | "shuffle" => self.collection(&input()),
            "reverse" | "slice" => {
                let input = input();
                if is_string(&input) {
                    Type::string()
                } else {
                    self.collection(&input)
                }
            }
            "map" => {
                let (key, _) = self.iterable_types(&input());
                let result = arrow_result(0);
                if key.is_unknown() || result.is_unknown() {
                    Type::unknown()
                } else {
                    Type::array(key, result)
                }
            }
            "reduce" => all_known(vec![arrow_result(0), argument(1)]),
            "merge" => {
                let (left, right) = (input(), argument(0));
                let (left_key, left_value) = self.iterable_types(&left);
                let (right_key, right_value) = self.iterable_types(&right);
                let value = all_known(vec![left_value, right_value]);
                if value.is_unknown() {
                    value
                } else if is_list(&left) && is_list(&right) {
                    Type::list(value)
                } else {
                    let key = all_known(vec![left_key, right_key]);
                    if key.is_unknown() {
                        key
                    } else {
                        Type::array(key, value)
                    }
                }
            }
            "column" => {
                let element = self.iterable_types(&input()).1;
                let name = arguments
                    .first()
                    .and_then(first_twig_child)
                    .filter(|name| name.kind() == TWIG_LITERAL_STRING)
                    .and_then(|name| twigquery::string_value(&name));
                match name {
                    Some(name) if !element.is_unknown() => {
                        let value = self.attribute_type(&element, &name);
                        if value.is_unknown() {
                            value
                        } else {
                            Type::list(value)
                        }
                    }
                    _ => Type::unknown(),
                }
            }
            "batch" => {
                let (key, value) = self.iterable_types(&input());
                if key.is_unknown() || value.is_unknown() {
                    Type::unknown()
                } else {
                    Type::list(Type::array(key, value))
                }
            }
            "default" => {
                let input = input();
                if input.is_unknown() {
                    input
                } else {
                    // Without an argument the default is an empty string.
                    let fallback = match arguments.first() {
                        Some(_) => argument(0),
                        None => Type::string(),
                    };
                    all_known(vec![without_null(&input), fallback])
                }
            }
            "split" => Type::list(Type::string()),
            "length" => Type::int(),
            "abs" | "round" => {
                let input = input();
                match input.kind() {
                    TypeKind::Int | TypeKind::LiteralInt if name == "abs" => Type::int(),
                    TypeKind::Unknown => input,
                    _ => Type::union([Type::int(), Type::float()]),
                }
            }
            _ if STRING_FILTERS.contains(&name) => {
                // An extension may replace a core filter.
                let registered = self.twig_filter_type(name);
                if registered.is_unknown() {
                    Type::string()
                } else {
                    registered
                }
            }
            _ => return None,
        })
    }

    /// The result of a built-in function whose type follows from its
    /// arguments, or `None` for any other function.
    pub(crate) fn builtin_function_type(
        &self,
        template_path: &str,
        root: &SyntaxNode,
        call: &SyntaxNode,
        name: &str,
    ) -> Option<Type> {
        let arguments: Vec<SyntaxNode> = direct_child(call, TWIG_ARGUMENTS)
            .map(|arguments| direct_twig_children(&arguments, TWIG_EXPRESSION))
            .unwrap_or_default();
        let argument =
            |index: usize| self.expression_type(template_path, root, arguments.get(index));
        Some(match name {
            "range" => Type::list(Type::int()),
            "cycle" => self.iterable_types(&argument(0)).1,
            "random" => {
                let value = argument(0);
                match value.kind() {
                    TypeKind::Unknown if arguments.is_empty() => Type::int(),
                    TypeKind::Unknown => value,
                    TypeKind::Int | TypeKind::LiteralInt => Type::int(),
                    TypeKind::String | TypeKind::LiteralString => Type::string(),
                    _ => self.iterable_types(&value).1,
                }
            }
            "max" | "min" => match arguments.len() {
                0 => Type::unknown(),
                1 => self.iterable_types(&argument(0)).1,
                count => all_known((0..count).map(argument).collect()),
            },
            "attribute" => {
                let name = arguments
                    .get(1)
                    .and_then(first_twig_child)
                    .filter(|name| name.kind() == TWIG_LITERAL_STRING)
                    .and_then(|name| twigquery::string_value(&name))?;
                self.attribute_type(&argument(0), &name)
            }
            "constant" => {
                let name = arguments
                    .first()
                    .and_then(first_twig_child)
                    .filter(|name| name.kind() == TWIG_LITERAL_STRING)
                    .and_then(|name| twigquery::string_value(&name))?;
                let name = name.replace("\\\\", "\\");
                match (arguments.len(), name.rsplit_once("::")) {
                    // `constant('NAME', object)`: a constant of its class.
                    (2, None) => resolve_php_member_type(self.snapshot(), &argument(1), &name),
                    (1, Some((class, constant))) => resolve_php_member_type(
                        self.snapshot(),
                        &Type::named(class.trim_start_matches('\\'), Vec::new()),
                        constant,
                    ),
                    (1, None) => crate::inference::all_known(
                        self.snapshot()
                            .constants(name.trim_start_matches('\\'))
                            .into_iter()
                            .map(|constant| constant.ty)
                            .collect(),
                    ),
                    _ => Type::unknown(),
                }
            }
            "block" | "parent" | "include" | "source" => Type::string(),
            _ => return None,
        })
    }

    /// `value` as the collection a filter returns: arrays stay what they
    /// are, any other iterable becomes an array of its elements.
    fn collection(&self, value: &Type) -> Type {
        match value.kind() {
            TypeKind::Array
            | TypeKind::NonEmptyArray
            | TypeKind::List
            | TypeKind::NonEmptyList
            | TypeKind::ArrayShape => value.clone(),
            _ => {
                let (key, element) = self.iterable_types(value);
                if key.is_unknown() || element.is_unknown() {
                    Type::unknown()
                } else {
                    Type::array(key, element)
                }
            }
        }
    }
}

fn is_string(value: &Type) -> bool {
    matches!(value.kind(), TypeKind::String | TypeKind::LiteralString)
}

fn is_list(value: &Type) -> bool {
    matches!(value.kind(), TypeKind::List | TypeKind::NonEmptyList)
}
