//! The closure of a partial application (`f(?, 1)`, `f(a: ?)`, `f(1, ...)`):
//! the parameters the call leaves open, in the order PHP gives them.

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::{Argument, resolve_signature};
use swlsp_php_semantic::{Flags, Symbol};
use swlsp_php_types::{CallableParameter, Relations, Type};
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

/// The arguments of a partial application as the signature check sees
/// them. `arguments` are the given ones, a `?` among them as a value of
/// unknown type; `rest` says whether a `...` stands for the others.
pub(crate) struct PartialArguments<'a> {
    pub(crate) arguments: &'a [Argument],
    pub(crate) rest: bool,
}

impl PartialArguments<'_> {
    /// Why PHP rejects this partial application of `symbol`; `None` when
    /// it accepts it.
    pub(crate) fn mismatch(&self, relations: Relations<'_>, symbol: &Symbol) -> Option<String> {
        let parameters = &symbol.parameters;
        let positional = self
            .arguments
            .iter()
            .take_while(|argument| argument.name.is_empty())
            .count();
        let variadic = parameters
            .last()
            .is_some_and(|parameter| parameter.flags.has(Flags::VARIADIC));
        // A call of a user function may pass more than is declared; a
        // partial application may not.
        if !variadic && positional > parameters.len() {
            return Some(format!(
                "partial application expects at most {} arguments, {positional} given",
                parameters.len()
            ));
        }
        let mut arguments = self.arguments.to_vec();
        if self.rest {
            // `...` stands for every parameter that is not given.
            let named = |parameter: &swlsp_php_semantic::Parameter| {
                let name = parameter.name.as_str().trim_start_matches('$');
                self.arguments
                    .iter()
                    .any(|argument| argument.name.trim_start_matches('$') == name)
            };
            for parameter in parameters.iter().skip(positional) {
                if !parameter.optional && !parameter.flags.has(Flags::VARIADIC) && !named(parameter)
                {
                    arguments.push(Argument {
                        name: parameter.name.as_str().to_owned(),
                        ..Argument::default()
                    });
                }
            }
        }
        let resolved = resolve_signature(relations, symbol.clone(), &arguments);
        (!resolved.compatible).then_some(resolved.mismatch)
    }
}
