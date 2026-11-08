use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;

use crate::php_context::*;

#[test]
fn php_config_reference_at_classifies_helpers() {
    let root = swlsp_parser_php::parse(
        "<?php\nservice('app.service');\nparam('app.parameter');\ntagged_iterator('app.tag');\n$services->alias('alias', 'target');\n",
    )
    .root();
    let calls = phpquery::calls(&root, &[]);
    assert_eq!(calls.len(), 4);
    let expected = [
        PhpConfigReferenceKind::Service,
        PhpConfigReferenceKind::Parameter,
        PhpConfigReferenceKind::Tag,
        PhpConfigReferenceKind::Service,
    ];
    for (call, expected) in calls.iter().zip(expected) {
        let last = phpquery::arguments(call).count() - 1;
        let string = phpquery::string_argument(call, last).unwrap();
        assert_eq!(php_config_reference_at(&string), expected);
    }
}

#[test]
fn php_attribute_reference_at_classifies_attributes() {
    let root = swlsp_parser_php::parse(
        r"<?php
#[Autowire(service: 'app.service')]
#[Autowire(param: 'app.parameter')]
#[Autowire('@app.positional')]
#[Autowire('%app.positional_parameter%')]
#[Autowire('@')]
#[Autowire('%')]
#[AutowireLocator(['app.locator', 'app.other'])]
#[AutowireLocator(services: ['app.named'])]
#[AutowireLocator(exclude: 'app.excluded')]
#[AutowireServiceClosure('app.closure')]
#[AutowireServiceClosure(service: 'app.named_closure')]
#[AutowireMethodOf('app.method_of')]
#[AutowireMethodOf(service: 'app.named_method_of')]
#[AutowireCallable(service: 'app.callable')]
#[TaggedIterator('app.iterator')]
#[TaggedLocator(tag: 'app.locator_tag')]
#[Autoconfigure(['app.auto_one', 'app.auto_two'])]
#[Autoconfigure(tags: ['app.auto_named'])]
#[AutoconfigureTag('app.configure_tag')]
#[AutoconfigureTag(name: 'app.named_configure_tag')]
#[AsDecorator(decorates: 'app.decorated')]
class Example {}
",
    )
    .root();
    let class = phpquery::classes(&root).remove(0);
    let mut references = Vec::new();
    for attribute in phpquery::attributes(&class) {
        for string in phpquery::nodes(&attribute, &[PHP_STRING]) {
            references.push(php_attribute_reference_at(&string));
        }
    }
    use PhpConfigReferenceKind::{Parameter as P, Service as S, Tag as T};
    assert_eq!(
        references,
        vec![
            S, P, S, P, S, P, S, S, S, S, S, S, S, S, S, T, T, T, T, T, T, T, S
        ]
    );
}

#[test]
fn php_autowire_callable_method_at_reads_receiver() {
    let root = swlsp_parser_php::parse(
        r"<?php
#[AutowireCallable(service: 'app.formatter', method: 'format')]
#[AutowireCallable(service: Formatter::class, method: '')]
#[AutowireCallable(service: 'app.formatter', label: 'not-a-method')]
class Example {}
",
    )
    .root();
    let strings = phpquery::nodes(&root, &[PHP_STRING]);
    assert_eq!(strings.len(), 5);
    let service = php_autowire_callable_method_at(&strings[1]).unwrap();
    assert_eq!(service.service, "app.formatter");
    assert!(service.class.is_empty());
    assert_eq!(service.method, "format");
    let class = php_autowire_callable_method_at(&strings[2]).unwrap();
    assert_eq!(class.class, "Formatter");
    assert!(class.service.is_empty());
    assert!(class.method.is_empty());
    assert!(php_autowire_callable_method_at(&strings[4]).is_none());
}

#[test]
fn php_when_environment_at_recognizes_env() {
    let root = swlsp_parser_php::parse(
        "<?php\n#[When('dev')]\n#[When(env: 'test')]\n#[Other('prod')]\nclass Example {}\n",
    )
    .root();
    let strings = phpquery::nodes(&root, &[PHP_STRING]);
    assert_eq!(strings.len(), 3);
    assert!(php_when_environment_at(&strings[0]));
    assert!(php_when_environment_at(&strings[1]));
    assert!(!php_when_environment_at(&strings[2]));
}

#[test]
fn php_parameter_access_types_by_method() {
    let mut bag = php_parameter_bag_types();
    let mut get = php_parameter_access_types("get");
    bag.sort();
    get.sort();
    assert_eq!(bag, get);
    assert_eq!(
        php_parameter_access_types("getParameter"),
        vec![PHP_DI_CONTAINER_INTERFACE]
    );
    let mut set = php_parameter_access_types("set");
    set.sort();
    let mut expected = vec![PHP_PARAMETER_BAG_INTERFACE, PHP_PARAMETERS_CONFIGURATOR];
    expected.sort();
    assert_eq!(set, expected);
    assert!(php_parameter_read_access_types("set").is_empty());
    assert!(!php_parameter_read_access_types("hasParameter").is_empty());
}
