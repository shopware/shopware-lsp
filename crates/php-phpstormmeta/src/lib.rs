//! Parses the declarative subset of `.phpstorm.meta.php` files into typed
//! semantic call contracts.

use std::collections::HashMap;

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{
    PHP_FUNCTION_CALL, PHP_NAME, PHP_NAMED_ARGUMENT, PHP_NUMBER, PHP_SCOPED_ACCESS,
    PHP_SCOPED_CALL, PHP_STRING,
};
use swlsp_php_semantic::{
    CallContract, CallMapEntry, CallReturnContract, CallReturnKind, CallTarget, CallValue,
    CallValueKind, ExpectedArgumentContract,
};
use swlsp_textutil::{eq_ignore_case, lowercase};

use swlsp_syntax::SyntaxNode;

#[cfg(test)]
mod tests;

const NAMESPACE: &str = "PHPSTORM_META";

/// Extracts supported metadata declarations in source order. Unknown and
/// malformed directives are ignored so a newer PhpStorm metadata dialect
/// never prevents the containing PHP file from being indexed.
pub fn parse(root: &SyntaxNode) -> Vec<CallContract> {
    if !eq_ignore_case(&query::namespace(root), NAMESPACE) {
        return Vec::new();
    }
    let mut extractor = ContractExtractor::default();
    query::visit(root, &[PHP_FUNCTION_CALL], |call| {
        extractor.visit(call);
        true
    });
    extractor.contracts
}

#[derive(Default)]
struct ContractExtractor {
    argument_sets: HashMap<String, Vec<CallValue>>,
    contracts: Vec<CallContract>,
}

/// The lowercased callee name without a leading namespace separator.
fn callee(call: &SyntaxNode) -> String {
    let name = query::call_method_name(call);
    lowercase(name.strip_prefix('\\').unwrap_or(&name))
}

impl ContractExtractor {
    fn visit(&mut self, call: &SyntaxNode) {
        let contract = match callee(call).as_str() {
            "override" => parse_override(call),
            "expectedarguments" => self.parse_expected_arguments(call),
            "expectedreturnvalues" => self.parse_expected_return_values(call),
            "registerargumentsset" => {
                self.parse_argument_set(call);
                return;
            }
            "exitpoint" => self.parse_exit_point(call),
            _ => None,
        };
        if let Some(contract) = contract {
            self.contracts.push(contract);
        }
    }

    fn parse_expected_arguments(&self, call: &SyntaxNode) -> Option<CallContract> {
        let (target, _, _) = parse_target(query::argument_expression(call, 0).as_ref())?;
        let argument = parse_u16(query::argument_expression(call, 1).as_ref())?;
        let values = self.call_values(call, 2);
        if values.is_empty() {
            return None;
        }
        Some(CallContract {
            target,
            expected_arguments: vec![ExpectedArgumentContract { argument, values }],
            ..CallContract::default()
        })
    }

    fn parse_expected_return_values(&self, call: &SyntaxNode) -> Option<CallContract> {
        let (target, _, _) = parse_target(query::argument_expression(call, 0).as_ref())?;
        let values = self.call_values(call, 1);
        if values.is_empty() {
            return None;
        }
        Some(CallContract {
            target,
            expected_return_values: values,
            ..CallContract::default()
        })
    }

    fn parse_argument_set(&mut self, call: &SyntaxNode) {
        let Some(name_node) = query::argument_expression(call, 0) else {
            return;
        };
        if name_node.kind() != PHP_STRING {
            return;
        }
        let name = query::string_value(&name_node);
        let values = self.call_values(call, 1);
        if !name.is_empty() && !values.is_empty() {
            self.argument_sets.insert(name, values);
        }
    }

    fn parse_exit_point(&self, call: &SyntaxNode) -> Option<CallContract> {
        let target_node = query::argument_expression(call, 0);
        let (target, _, _) = parse_target(target_node.as_ref())?;
        let target_node = target_node?;
        let mut conditions = Vec::new();
        for (index, argument) in query::arguments(&target_node).enumerate() {
            let Some(expression) = argument_expression(&argument) else {
                continue;
            };
            if query::node_text(&expression)
                .trim()
                .to_uppercase()
                .ends_with("ANY_ARGUMENT")
            {
                continue;
            }
            let values = self.expression_values(&expression);
            if values.is_empty() {
                // An unsupported predicate must not be widened into an
                // unconditional exit contract.
                return None;
            }
            conditions.push(ExpectedArgumentContract {
                argument: index as u16,
                values,
            });
        }
        Some(CallContract {
            target,
            exit_point: true,
            exit_arguments: conditions,
            ..CallContract::default()
        })
    }

    fn call_values(&self, call: &SyntaxNode, start: usize) -> Vec<CallValue> {
        let mut result = Vec::new();
        for argument in query::arguments(call).skip(start) {
            if let Some(expression) = argument_expression(&argument) {
                result.extend(self.expression_values(&expression));
            }
        }
        result
    }

    fn expression_values(&self, expression: &SyntaxNode) -> Vec<CallValue> {
        if expression.kind() == PHP_FUNCTION_CALL {
            let name = query::call_method_name(expression);
            if eq_ignore_case(name.strip_prefix('\\').unwrap_or(&name), "argumentsSet") {
                return match query::argument_expression(expression, 0) {
                    Some(name_node) if name_node.kind() == PHP_STRING => self
                        .argument_sets
                        .get(&query::string_value(&name_node))
                        .cloned()
                        .unwrap_or_default(),
                    _ => Vec::new(),
                };
            }
        }
        parse_call_value(Some(expression)).into_iter().collect()
    }
}

/// The value expression of one argument node (`query::argument_expression`
/// without re-walking the argument list, which is quadratic for the long
/// value lists of `expectedArguments`).
fn argument_expression(argument: &SyntaxNode) -> Option<SyntaxNode> {
    let named = argument.kind() == PHP_NAMED_ARGUMENT;
    argument
        .children()
        .find(|child| !(named && child.kind() == PHP_NAME))
}

fn parse_override(call: &SyntaxNode) -> Option<CallContract> {
    let (target, selector, _) = parse_target(query::argument_expression(call, 0).as_ref())?;
    let rule = query::argument_expression(call, 1)?;
    if rule.kind() != PHP_FUNCTION_CALL {
        return None;
    }
    let return_contract = match callee(&rule).as_str() {
        "type" => CallReturnContract {
            kind: CallReturnKind::ArgumentType,
            argument: parse_argument_index(&rule)?,
            map: Vec::new(),
        },
        "elementtype" => CallReturnContract {
            kind: CallReturnKind::ArgumentElementType,
            argument: parse_argument_index(&rule)?,
            map: Vec::new(),
        },
        "map" => {
            let entries = parse_map_entries(&rule);
            if entries.is_empty() {
                return None;
            }
            CallReturnContract {
                kind: CallReturnKind::ArgumentMap,
                argument: selector,
                map: entries,
            }
        }
        _ => return None,
    };
    Some(CallContract {
        target,
        return_contract,
        ..CallContract::default()
    })
}

/// The call target plus the optional argument selector written inside it
/// (`\foo(0)`); a missing selector is reported as `(0, false)`.
fn parse_target(node: Option<&SyntaxNode>) -> Option<(CallTarget, u16, bool)> {
    let node = node?;
    let selector = parse_u16(query::argument_expression(node, 0).as_ref());
    let (selector, has_selector) = (selector.unwrap_or(0), selector.is_some());
    let target = match node.kind() {
        kind if kind == PHP_FUNCTION_CALL => {
            let name = query::call_method_name(node);
            if name.is_empty() {
                return None;
            }
            CallTarget::function(&name)
        }
        kind if kind == PHP_SCOPED_CALL => {
            let receiver = query::call_receiver(node)?;
            if receiver.kind() != PHP_NAME {
                return None;
            }
            let class_name = query::name_value(&receiver);
            let method_name = query::call_method_name(node);
            if class_name.is_empty() || method_name.is_empty() {
                return None;
            }
            CallTarget::method(&class_name, &method_name)
        }
        _ => return None,
    };
    Some((target, selector, has_selector))
}

fn parse_argument_index(call: &SyntaxNode) -> Option<u16> {
    parse_u16(query::argument_expression(call, 0).as_ref())
}

/// A base-10 `uint16` literal (`strconv.ParseUint(text, 10, 16)` after
/// removing digit separators).
fn parse_u16(expression: Option<&SyntaxNode>) -> Option<u16> {
    let expression = expression?;
    if expression.kind() != PHP_NUMBER {
        return None;
    }
    let text = query::node_text(expression).trim().replace('_', "");
    if text.is_empty() || !text.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    text.parse().ok()
}

fn parse_map_entries(call: &SyntaxNode) -> Vec<CallMapEntry> {
    let Some(array) = query::argument_expression(call, 0).and_then(|node| query::array_at(&node))
    else {
        return Vec::new();
    };
    query::array_items(&array)
        .iter()
        .filter_map(|item| {
            let key = parse_call_value(query::array_item_key(item).as_ref())?;
            let result = parse_call_value(query::array_item_value(item).as_ref())?;
            Some(CallMapEntry { key, result })
        })
        .collect()
}

fn parse_call_value(node: Option<&SyntaxNode>) -> Option<CallValue> {
    let node = node?;
    let text = query::node_text(node);
    let expression = text.trim();
    if expression.is_empty() {
        return None;
    }
    let trimmed = expression.strip_prefix('\\').unwrap_or(expression);
    let (kind, value) = match node.kind() {
        kind if kind == PHP_STRING => (CallValueKind::String, query::string_value(node)),
        kind if kind == PHP_NUMBER => (CallValueKind::Number, expression.to_owned()),
        kind if kind == PHP_NAME => {
            let name = query::name_value(node);
            let name = name.strip_prefix('\\').unwrap_or(&name).to_owned();
            (CallValueKind::Constant, name)
        }
        kind if kind == PHP_SCOPED_ACCESS => (CallValueKind::ClassConstant, trimmed.to_owned()),
        _ if expression.contains("::") && !expression.contains(['|', '&']) => {
            (CallValueKind::ClassConstant, trimmed.to_owned())
        }
        _ => (CallValueKind::Expression, expression.to_owned()),
    };
    Some(CallValue {
        kind,
        value: value.into(),
        expression: expression.into(),
    })
}
