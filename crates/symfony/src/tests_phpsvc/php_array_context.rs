use std::collections::HashMap;

use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_syntax::SyntaxNode;

use crate::php_array_context::*;
use crate::php_context::{PhpConfigReferenceKind, php_config_reference_at};

fn string_literal(root: &SyntaxNode, value: &str) -> SyntaxNode {
    phpquery::nodes(root, &[PHP_STRING])
        .into_iter()
        .find(|literal| phpquery::string_value(literal) == value)
        .unwrap_or_else(|| panic!("string {value}"))
}

#[test]
fn php_array_service_reference_at_classifies_values() {
    let source = r"<?php
return [
    'services' => [
        'app.consumer' => [
            'decorates' => 'app.inner',
            'parent' => 'app.parent',
            'class' => 'App\\Consumer',
            'arguments' => [
                '%app.parameter%',
                '@app.logger',
                'App\\Dependency',
                service('app.helper'),
            ],
            'calls' => [['setLogger', ['%app.call_parameter%']]],
            'tags' => ['app.tag', ['name' => 'app.named_tag']],
            'factory' => ['@app.factory', 'create'],
        ],
        'app.alias' => '@app.target',
    ],
];
";
    let root = swlsp_parser_php::parse(source).root();
    use PhpConfigReferenceKind as Kind;
    let mut expected: HashMap<&str, Kind> = HashMap::from([
        ("app.inner", Kind::Service),
        ("app.parent", Kind::Service),
        (r"App\\Consumer", Kind::Class),
        ("%app.parameter%", Kind::Parameter),
        ("@app.logger", Kind::Service),
        (r"App\\Dependency", Kind::Class),
        ("%app.call_parameter%", Kind::Parameter),
        ("app.tag", Kind::Tag),
        ("app.named_tag", Kind::Tag),
        ("@app.factory", Kind::Service),
        ("@app.target", Kind::Service),
    ]);
    for literal in phpquery::nodes(&root, &[PHP_STRING]) {
        let value = phpquery::string_value(&literal);
        if let Some(kind) = expected.remove(value.as_str()) {
            assert_eq!(php_array_service_reference_at(&literal), kind, "{value}");
        }
    }
    assert!(expected.is_empty(), "{expected:?}");
    let helper = string_literal(&root, "app.helper");
    assert!(php_array_service_context_at(&helper));
    assert_eq!(php_config_reference_at(&helper), Kind::Service);
}

#[test]
fn php_array_service_reference_supports_app_config() {
    let root = swlsp_parser_php::parse(
        r"<?php
namespace Symfony\Component\DependencyInjection\Loader\Configurator;
App::config([
    'services' => [
        'app.consumer' => [
            'arguments' => ['%app.parameter%'],
        ],
    ],
]);
",
    )
    .root();
    let target = string_literal(&root, "%app.parameter%");
    assert_eq!(
        php_array_service_reference_at(&target),
        PhpConfigReferenceKind::Parameter
    );
}

#[test]
fn php_array_service_reference_rejects_ordinary_arrays() {
    let root = swlsp_parser_php::parse(
        r"<?php
function data(): array {
    return [
        'services' => [
            'app.consumer' => [
                'arguments' => ['%not.a.parameter%'],
            ],
        ],
    ];
}
",
    )
    .root();
    let target = string_literal(&root, "%not.a.parameter%");
    assert_eq!(
        php_array_service_reference_at(&target),
        PhpConfigReferenceKind::None
    );
}

#[test]
fn php_array_service_method_at_resolves_receivers() {
    let root = swlsp_parser_php::parse(
        r"<?php
use App\Service\Consumer;
return [
    'services' => [
        'app.consumer' => [
            'class' => Consumer::class,
            'calls' => [['configure', []]],
            'factory' => ['@app.factory', 'create'],
        ],
    ],
];
",
    )
    .root();
    let call = php_array_service_method_at(&root, &string_literal(&root, "configure")).unwrap();
    assert_eq!(call.class, r"\App\Service\Consumer");
    assert_eq!(call.service, "app.consumer");
    assert!(!call.allow_static);
    let factory = php_array_service_method_at(&root, &string_literal(&root, "create")).unwrap();
    assert_eq!(factory.service, "app.factory");
    assert!(factory.allow_static);
}
