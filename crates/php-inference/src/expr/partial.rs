//! The closure of a partial application (`f(?, 1)`, `f(a: ?)`, `f(1, ...)`):
//! the parameters the call leaves open, in the order PHP gives them.

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_semantic::{Flags, Symbol};
use swlsp_php_types::{CallableParameter, Type};
use swlsp_syntax::SyntaxNode;
use swlsp_textutil::eq_ignore_case;

use crate::syntax::has_direct_token;

fn open(parameter: &swlsp_php_semantic::Parameter, required: bool) -> CallableParameter {
    CallableParameter {
        name: parameter.name.as_str().into(),
        ty: parameter.ty.clone(),
        optional: !required && parameter.optional,
        variadic: !required && parameter.flags.has(Flags::VARIADIC),
        by_reference: parameter.flags.has(Flags::BY_REFERENCE),
    }
}

/// The parameters of `symbol` the call `node` leaves open; `None` when the
/// call spreads a value, whose length is not known.
fn open_parameters(symbol: &Symbol, node: &SyntaxNode) -> Option<Vec<CallableParameter>> {
    let parameters = &symbol.parameters;
    let variadic = parameters
        .last()
        .filter(|parameter| parameter.flags.has(Flags::VARIADIC));
    let mut given = vec![false; parameters.len()];
    let mut result = Vec::new();
    let mut position = 0usize;
    for argument in query::arguments(node) {
        let placeholder = query::is_placeholder_argument(&argument);
        if query::is_variadic_placeholder(&argument) {
            // Every parameter that is not given, as it is declared.
            for (index, parameter) in parameters.iter().enumerate() {
                if !std::mem::replace(&mut given[index], true) {
                    result.push(open(parameter, false));
                }
            }
            continue;
        }
        if argument.kind() == PHP_NAMED_ARGUMENT {
            let name = query::argument_name(&argument);
            let Some(index) = parameters.iter().position(|parameter| {
                eq_ignore_case(parameter.name.as_str().trim_start_matches('$'), &name)
            }) else {
                continue;
            };
            if placeholder && !given[index] {
                result.push(open(&parameters[index], true));
            }
            given[index] = true;
            continue;
        }
        if has_direct_token(&argument, TK_ELLIPSIS) {
            return None;
        }
        match parameters.get(position) {
            Some(parameter) if !parameter.flags.has(Flags::VARIADIC) => {
                if placeholder {
                    result.push(open(parameter, true));
                }
                given[position] = true;
            }
            // A placeholder past the declared parameters is one value of
            // the variadic parameter, named after it and its place.
            _ => {
                if let (true, Some(rest)) = (placeholder, variadic) {
                    let place = position + 1 - parameters.len();
                    let mut parameter = open(rest, true);
                    parameter.name = format!("{}{place}", rest.name.as_str()).as_str().into();
                    result.push(parameter);
                }
            }
        }
        position += 1;
    }
    Some(result)
}

/// The callable a partial application of `symbol` by the call `node` is.
pub(crate) fn partial_callable(symbol: &Symbol, node: &SyntaxNode) -> Type {
    let result = if symbol.return_type.is_unknown() {
        Type::MIXED
    } else {
        symbol.return_type.clone()
    };
    Type::callable(open_parameters(symbol, node).unwrap_or_default(), result)
}
