use super::*;

fn contracts(source: &str) -> Vec<CallContract> {
    parse(&swlsp_parser_php::root(source))
}

fn value(kind: CallValueKind, value: &str, expression: &str) -> CallValue {
    CallValue {
        kind,
        value: value.into(),
        expression: expression.into(),
    }
}

#[test]
fn parse_override_return_contracts() {
    let result = contracts(
        r"<?php
namespace PHPSTORM_META {
    override(\array_pop(0), elementType(0));
    override(\DOMNode::appendChild(0), type(0));
}",
    );
    assert_eq!(
        result,
        vec![
            CallContract {
                target: CallTarget::function("array_pop"),
                return_contract: CallReturnContract {
                    kind: CallReturnKind::ArgumentElementType,
                    argument: 0,
                    map: Vec::new(),
                },
                ..CallContract::default()
            },
            CallContract {
                target: CallTarget::method("DOMNode", "appendChild"),
                return_contract: CallReturnContract {
                    kind: CallReturnKind::ArgumentType,
                    argument: 0,
                    map: Vec::new(),
                },
                ..CallContract::default()
            },
        ]
    );
}

#[test]
fn parse_ignores_unsupported_and_malformed_overrides() {
    let result = contracts(
        r"<?php
namespace PHPSTORM_META;
override(\mapped(0), map(['' => '@']));
override(\missingRule(0));
override($dynamic, type(0));
override(\invalidIndex(0), type('first'));
override(\valid(0), type(2));
",
    );
    assert_eq!(
        result,
        vec![
            CallContract {
                target: CallTarget::function("mapped"),
                return_contract: CallReturnContract {
                    kind: CallReturnKind::ArgumentMap,
                    argument: 0,
                    map: vec![CallMapEntry {
                        key: value(CallValueKind::String, "", "''"),
                        result: value(CallValueKind::String, "@", "'@'"),
                    }],
                },
                ..CallContract::default()
            },
            CallContract {
                target: CallTarget::function("valid"),
                return_contract: CallReturnContract {
                    kind: CallReturnKind::ArgumentType,
                    argument: 2,
                    map: Vec::new(),
                },
                ..CallContract::default()
            },
        ]
    );
}

#[test]
fn parse_expected_values_argument_sets_and_exit_points() {
    let result = contracts(
        r"<?php
namespace PHPSTORM_META;
registerArgumentsSet('modes', \MODE_ONE, \MODE_TWO, 'custom');
expectedArguments(\configure(), 1, argumentsSet('modes'), \MODE_THREE);
expectedReturnValues(\mode(), argumentsSet('modes'));
exitPoint(\dd());
exitPoint(\trigger_error(ANY_ARGUMENT, \E_USER_ERROR));
",
    );
    assert_eq!(result.len(), 4);
    assert_eq!(result[0].target, CallTarget::function("configure"));
    assert_eq!(result[0].expected_arguments[0].argument, 1);
    let labels: Vec<&str> = result[0].expected_arguments[0]
        .values
        .iter()
        .map(CallValue::label)
        .collect();
    assert_eq!(
        labels,
        ["\\MODE_ONE", "\\MODE_TWO", "custom", "\\MODE_THREE"]
    );
    assert_eq!(result[1].target, CallTarget::function("mode"));
    assert_eq!(result[1].expected_return_values.len(), 3);
    assert_eq!(result[2].target, CallTarget::function("dd"));
    assert!(result[2].exit_point);
    assert_eq!(result[3].target, CallTarget::function("trigger_error"));
    assert!(result[3].exit_point);
    assert_eq!(result[3].exit_arguments[0].argument, 1);
    assert_eq!(
        result[3].exit_arguments[0].values[0].label(),
        "\\E_USER_ERROR"
    );
}

#[test]
fn parse_requires_phpstorm_meta_namespace() {
    let result = contracts(
        r"<?php
namespace App;
override(\array_pop(0), elementType(0));
",
    );
    assert!(result.is_empty());
}

#[test]
fn parse_u16_rejects_signs_and_overflow() {
    let result = contracts(
        r"<?php
namespace PHPSTORM_META;
override(\a(0), type(65536));
override(\b(0), type(1_0));
override(\c(0), type(0x1));
",
    );
    assert_eq!(result.len(), 1);
    assert_eq!(result[0].target, CallTarget::function("b"));
    assert_eq!(result[0].return_contract.argument, 10);
}
