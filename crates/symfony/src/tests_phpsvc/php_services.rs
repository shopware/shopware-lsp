use std::collections::HashMap;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_syntax::{LineIndex, TextRange};

use crate::model::Service;
use crate::path_match::path_matches_config_pattern;
use crate::php_array_services::parse_php_array_service_config;
use crate::php_services::{
    parse_php_service_config_tree, parse_php_services, php_service_argument_references,
    php_service_method_references,
};
use crate::{ServiceIndex, TagMap};

fn tags(entries: &[(&str, &str)]) -> TagMap {
    entries
        .iter()
        .map(|(key, value)| ((*key).to_owned(), (*value).to_owned()))
        .collect()
}

fn by_id(services: &[Service]) -> HashMap<&str, &Service> {
    services
        .iter()
        .map(|service| (service.id.as_str(), service))
        .collect()
}

fn slice(source: &str, range: TextRange) -> &str {
    &source[usize::from(range.start())..usize::from(range.end())]
}

#[test]
fn parse_php_services_configurator() {
    let source = r#"<?php
namespace App\DependencyInjection;

use App\Contract\HandlerInterface;
use App\Service\Handler as RenamedHandler;
use App\Service\MigrationSource;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (string $env, ContainerConfigurator $container): void {
    $services = $container->services();
    $parameters = $container->parameters();

    $services->instanceof(HandlerInterface::class)->tag('app.instanceof');
    $services->defaults()->autowire()->autoconfigure()->tag('app.default');
    $handler = $services->set(HandlerInterface::class, RenamedHandler::class)
        ->tag('app.handler');
    $handler->tag('app.later');
    $services->set(MigrationSource::class . '.core')
        ->class(RenamedHandler::class);
    $services->set('removed.service', RenamedHandler::class);
    $services->remove('removed.service');
    $services->alias('app.handler_alias', HandlerInterface::class);

    $parameters->set('app.scalar', 'value');
    $container->parameters()->set('app.array', ['one', 'two']);
};
"#;
    let (services, parameters) = parse_php_services("/project/config/services.php", source);
    assert_eq!(services.len(), 3);
    let services = by_id(&services);
    let handler = services[r"App\Contract\HandlerInterface"];
    assert_eq!(handler.class, r"App\Service\Handler");
    assert_eq!(
        handler.tags,
        tags(&[("app.default", ""), ("app.handler", ""), ("app.later", "")])
    );
    assert_eq!(
        handler.instanceof_tags,
        tags(&[("app.instanceof", r"App\Contract\HandlerInterface")])
    );
    assert_eq!(handler.line, 15);
    let migration = services[r"App\Service\MigrationSource.core"];
    assert_eq!(migration.class, r"App\Service\Handler");
    assert!(migration.tags.contains_key("app.default"));
    assert_eq!(
        migration.instanceof_tags,
        tags(&[("app.instanceof", r"App\Contract\HandlerInterface")])
    );
    let alias = services["app.handler_alias"];
    assert_eq!(alias.alias_target, r"App\Contract\HandlerInterface");
    assert!(alias.class.is_empty());
    assert!(!services.contains_key("removed.service"));
    assert_eq!(parameters.len(), 2);
    assert_eq!(parameters[0].value, "value");
    assert_eq!(parameters[1].value, "['one', 'two']");
}

#[test]
fn parse_php_deprecated_service_metadata() {
    let (services, _) = parse_php_services(
        "services.php",
        r#"<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set('legacy', App\Legacy::class)
        ->deprecate('app/package', '1.0', 'Use App\Modern instead');
    $definition = $services->set('flagged', App\Flagged::class);
    $definition->deprecate('app/package', '1.0');
};"#,
    );
    assert_eq!(services.len(), 2);
    assert!(services[0].deprecated);
    assert_eq!(services[0].deprecation, r"Use App\Modern instead");
    assert!(!services[0].deprecated_range.is_empty());
    assert!(services[1].deprecated);
    assert!(services[1].deprecation.is_empty());
}

#[test]
fn parse_php_service_prototypes() {
    let source = r#"<?php
use App\Contract\Marker;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->instanceof(Marker::class)->tag('app.marker');
    $services->load('App\\', __DIR__ . '/../src/')
        ->exclude([
            __DIR__ . '/../src/Excluded',
            __DIR__ . '/../src/{Entity,Kernel.php}',
        ])
        ->tag('app.prototype');
    $services->set(null, Marker::class);
};
"#;
    let parse = swlsp_parser_php::parse(source);
    let config = parse_php_service_config_tree(
        "/project/config/services.php",
        &parse.root(),
        &LineIndex::new(source),
    );
    assert!(
        config.services.is_empty(),
        "anonymous service IDs must not be guessed"
    );
    assert_eq!(config.prototypes.len(), 1);
    let prototype = &config.prototypes[0];
    assert_eq!(prototype.namespace, "App\\");
    assert_eq!(prototype.resource, "/project/src");
    assert_eq!(
        prototype.excludes,
        vec!["/project/src/Excluded", "/project/src/{Entity,Kernel.php}"]
    );
    assert_eq!(prototype.tags, tags(&[("app.prototype", "")]));
    assert_eq!(
        prototype.instanceof_tags,
        tags(&[("app.marker", r"App\Contract\Marker")])
    );
}

#[test]
fn parse_php_services_direct_configurator_chains() {
    let source = r#"<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
use App\Service\Direct;
use App\Service\Invoked;

return static function (ContainerConfigurator $container): void {
    $definition = $container->services()->set(Direct::class);
    $definition->tag('app.direct');
    $services = $container->services();
    $services(Invoked::class)->tag('app.invoked');
    $container->services()->alias('direct.alias', Direct::class);
    $container->parameters()->set('direct.parameter', true);
};
"#;
    let (services, parameters) = parse_php_services("direct.php", source);
    assert_eq!(services.len(), 3);
    assert_eq!(services[0].id, r"App\Service\Direct");
    assert!(services[0].tags.contains_key("app.direct"));
    assert_eq!(services[1].id, r"App\Service\Invoked");
    assert!(services[1].tags.contains_key("app.invoked"));
    assert_eq!(services[2].id, "direct.alias");
    assert_eq!(parameters.len(), 1);
    assert_eq!(parameters[0].value, "true");
}

#[test]
fn parse_php_services_rejects_dynamic_and_unrelated_php() {
    let (services, parameters) = parse_php_services(
        "Factory.php",
        r#"<?php
namespace App;

final class Factory
{
    public function configure(object $container, string $id): void
    {
        $services = $container->services();
        $services->set($id);
        $services->set("interpolated.$id");
    }
}
"#,
    );
    assert!(services.is_empty());
    assert!(parameters.is_empty());
}

#[test]
fn parse_php_services_tolerates_incomplete_input() {
    let (services, parameters) = parse_php_services(
        "services.php",
        r#"<?php
use App\Service\Complete;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set(Complete::class)->tag('complete');
    $services->set(
"#,
    );
    assert_eq!(services.len(), 1);
    assert_eq!(services[0].id, r"App\Service\Complete");
    assert!(services[0].tags.contains_key("complete"));
    assert!(parameters.is_empty());
}

#[test]
fn parse_php_array_services() {
    let source = r#"<?php
namespace Symfony\Component\DependencyInjection\Loader\Configurator;

use App\Contract\HandlerInterface;
use App\Service\Decorator;
use App\Service\Handler;

return App::config([
    'parameters' => [
        'app.transport' => 'async',
    ],
    'services' => [
        '_defaults' => [
            'tags' => ['app.default'],
        ],
        '_instanceof' => [
            HandlerInterface::class => [
                'tags' => [['name' => 'app.handler']],
            ],
        ],
        'app.target' => [
            'class' => Handler::class,
            'tags' => ['app.explicit'],
        ],
        'app.alias' => '@app.target',
        'app.decorator' => [
            'class' => Decorator::class,
            'decorates' => 'app.target',
            'parent' => 'app.abstract',
            'deprecated' => 'Use app.target instead.',
        ],
        'App\\Feature\\' => [
            'resource' => '../src/Feature',
            'exclude' => ['../src/Feature/Excluded'],
            'tags' => ['app.prototype'],
        ],
    ],
]);
"#;
    let (services, parameters) = parse_php_services("/project/config/services.php", source);
    assert_eq!(services.len(), 3);
    let services = by_id(&services);
    let target = services["app.target"];
    assert_eq!(target.class, r"App\Service\Handler");
    assert!(target.tags.contains_key("app.default"));
    assert!(target.tags.contains_key("app.explicit"));
    assert_eq!(
        target.instanceof_tags,
        tags(&[("app.handler", r"App\Contract\HandlerInterface")])
    );
    assert_eq!(services["app.alias"].alias_target, "app.target");
    let decorator = services["app.decorator"];
    assert_eq!(decorator.class, r"App\Service\Decorator");
    assert_eq!(decorator.decorates, "app.target");
    assert_eq!(decorator.parent, "app.abstract");
    assert!(decorator.deprecated);
    assert_eq!(decorator.deprecation, "Use app.target instead.");
    assert_eq!(parameters.len(), 1);
    assert_eq!(parameters[0].name, "app.transport");
    assert_eq!(parameters[0].value, "async");

    let parse = swlsp_parser_php::parse(source);
    let config = parse_php_array_service_config(
        "/project/config/services.php",
        &parse.root(),
        &LineIndex::new(source),
    );
    assert_eq!(config.prototypes.len(), 1);
    let prototype = &config.prototypes[0];
    assert_eq!(prototype.namespace, "App\\Feature\\");
    assert_eq!(prototype.resource, "/project/src/Feature");
    assert_eq!(prototype.excludes, vec!["/project/src/Feature/Excluded"]);
    assert!(prototype.tags.contains_key("app.default"));
    assert!(prototype.tags.contains_key("app.prototype"));
}

fn argument_references(source: &str) -> Vec<crate::ServiceArgumentReference> {
    let parse = swlsp_parser_php::parse(source);
    php_service_argument_references(
        "/project/config/services.php",
        &parse.root(),
        &LineIndex::new(source),
    )
}

#[test]
fn php_service_argument_references_configurator() {
    let source = r#"<?php
use App\Service\Consumer;
use App\Service\Logger;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set(Consumer::class)->args([
        service(Logger::class),
        '@app.logger',
    ]);
    $definition = $services->set('app.named', Consumer::class);
    $definition->arg('$logger', ref('app.logger'));
    $services->set('app.class', Consumer::class)->arg(0, Logger::class);
};
"#;
    let references = argument_references(source);
    assert_eq!(references.len(), 4);
    assert_eq!(references[0].owner_service_id, r"App\Service\Consumer");
    assert_eq!(references[0].service_id, r"App\Service\Logger");
    assert_eq!(references[0].parameter_index, 0);
    assert_eq!(references[0].replacement, "php_helper_expression");
    assert_eq!(slice(source, references[0].range), "Logger::class");
    assert_eq!(references[1].service_id, "app.logger");
    assert_eq!(references[1].parameter_index, 1);
    assert_eq!(references[1].replacement, "php_raw_string");
    assert_eq!(slice(source, references[1].range), "@app.logger");
    assert_eq!(references[2].owner_service_id, "app.named");
    assert_eq!(references[2].parameter_name, "$logger");
    assert_eq!(references[2].replacement, "php_helper_string");
    assert_eq!(slice(source, references[2].range), "app.logger");
    assert_eq!(references[3].owner_service_id, "app.class");
    assert_eq!(references[3].service_id, r"App\Service\Logger");
    assert_eq!(references[3].replacement, "php_raw_expression");
    assert_eq!(slice(source, references[3].range), "Logger::class");
}

#[test]
fn php_service_argument_references_legacy_array() {
    let references = argument_references(
        r#"<?php
namespace App\Config;

use App\Service\Logger;

return [
    'services' => [
        'App\Service\Consumer' => [
            'arguments' => [
                service('app.logger'),
                '$logger' => '@app.named_logger',
                2 => Logger::class,
            ],
        ],
    ],
];
"#,
    );
    assert_eq!(references.len(), 3);
    for reference in &references {
        assert_eq!(reference.owner_class, r"App\Service\Consumer");
        assert_eq!(reference.format, "php");
    }
    assert_eq!(references[0].service_id, "app.logger");
    assert_eq!(references[0].parameter_index, 0);
    assert_eq!(references[1].parameter_name, "$logger");
    assert_eq!(references[1].parameter_index, -1);
    assert_eq!(references[1].service_id, "app.named_logger");
    assert_eq!(references[2].parameter_index, 2);
    assert_eq!(references[2].service_id, r"App\Service\Logger");
}

#[test]
fn php_service_argument_references_direct_configurator_chain() {
    let references = argument_references(
        r#"<?php
namespace Symfony\Component\DependencyInjection\Loader\Configurator;

$container->services()
    ->set('app.consumer', \App\Service\Consumer::class)
    ->args([service('app.logger')]);
"#,
    );
    assert_eq!(references.len(), 1);
    assert_eq!(references[0].owner_service_id, "app.consumer");
    assert_eq!(references[0].owner_class, r"App\Service\Consumer");
    assert_eq!(references[0].service_id, "app.logger");
    assert_eq!(references[0].parameter_index, 0);
}

#[test]
fn php_service_method_references_fixtures() {
    struct Fixture {
        name: &'static str,
        source: &'static str,
        owners: &'static [&'static str],
        classes: &'static [&'static str],
        methods: &'static [&'static str],
    }
    let fixtures = [
        Fixture {
            name: "configurator callback",
            source: r#"<?php
use App\Service\Consumer;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $definition = $services->set('app.consumer', Consumer::class);
    $definition->call('setLogger', []);
    $services->set(Consumer::class)->call('initialize');
};
"#,
            owners: &["app.consumer", r"App\Service\Consumer"],
            classes: &[r"App\Service\Consumer", r"App\Service\Consumer"],
            methods: &["setLogger", "initialize"],
        },
        Fixture {
            name: "direct configurator chain",
            source: r#"<?php
namespace Symfony\Component\DependencyInjection\Loader\Configurator;

$container->services()
    ->set('app.consumer', \App\Service\Consumer::class)
    ->call('setLogger', []);
"#,
            owners: &["app.consumer"],
            classes: &[r"App\Service\Consumer"],
            methods: &["setLogger"],
        },
        Fixture {
            name: "native array mapping and tuple",
            source: r#"<?php
use App\Service\Consumer;
use Symfony\Config\FrameworkConfig;

return FrameworkConfig::config([
    'services' => [
        Consumer::class => [
            'calls' => [
                'setLogger' => [],
                ['initialize', [], true],
            ],
        ],
    ],
]);
"#,
            owners: &[r"App\Service\Consumer", r"App\Service\Consumer"],
            classes: &[r"App\Service\Consumer", r"App\Service\Consumer"],
            methods: &["setLogger", "initialize"],
        },
    ];
    for fixture in fixtures {
        let parse = swlsp_parser_php::parse(fixture.source);
        let references = php_service_method_references(
            "/project/config/services.php",
            &parse.root(),
            &LineIndex::new(fixture.source),
        );
        assert_eq!(references.len(), fixture.methods.len(), "{}", fixture.name);
        for (index, reference) in references.iter().enumerate() {
            assert_eq!(
                reference.owner_service_id, fixture.owners[index],
                "{}",
                fixture.name
            );
            assert_eq!(
                reference.owner_class, fixture.classes[index],
                "{}",
                fixture.name
            );
            assert_eq!(
                reference.method_name, fixture.methods[index],
                "{}",
                fixture.name
            );
            assert_eq!(reference.format, "php");
            assert_eq!(
                slice(fixture.source, reference.range),
                fixture.methods[index],
                "{}",
                fixture.name
            );
        }
    }
}

// -- ServiceIndex with PHP configuration --

fn temp_path(dir: &tempfile::TempDir) -> String {
    dir.path().to_str().unwrap().to_owned()
}

#[test]
fn service_index_indexes_and_clears_php_config() {
    let config_dir = tempfile::tempdir().unwrap();
    let root = tempfile::tempdir().unwrap();
    let index = ServiceIndex::new(&temp_path(&root), config_dir.path(), None).unwrap();
    let other = tempfile::tempdir().unwrap();
    let path = format!("{}/DependencyInjection/messenger.php", temp_path(&other));
    let config = r#"<?php
use App\Message\MessageHandler;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $container->services()->set(MessageHandler::class)->tag('messenger.message_handler');
    $container->parameters()->set('app.transport', 'async');
};
"#;
    index
        .index(&ParsedFile::new(path.clone(), config.as_bytes().to_vec()))
        .unwrap();
    let service = index
        .get_service_by_id(r"App\Message\MessageHandler")
        .unwrap()
        .unwrap();
    assert_eq!(service.path, path);
    assert!(service.tags.contains_key("messenger.message_handler"));
    let parameter = index
        .get_parameter_by_name("app.transport")
        .unwrap()
        .unwrap();
    assert_eq!(parameter.value, "async");

    // Replacing a former configurator with normal PHP must remove its stale
    // service and parameter records.
    index
        .index(&ParsedFile::new(path, b"<?php return 1;".to_vec()))
        .unwrap();
    assert!(
        index
            .get_service_by_id(r"App\Message\MessageHandler")
            .unwrap()
            .is_none()
    );
    assert!(
        index
            .get_parameter_by_name("app.transport")
            .unwrap()
            .is_none()
    );
    index.close().unwrap();
}

#[test]
fn service_index_ignores_ordinary_php() {
    let root = tempfile::tempdir().unwrap();
    let config = tempfile::tempdir().unwrap();
    let index = ServiceIndex::new(&temp_path(&root), config.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "src/Service.php",
            b"<?php\nfinal class Service {}\n".to_vec(),
        ))
        .unwrap();
    assert!(index.get_all_services().unwrap().is_empty());
    index.close().unwrap();
}

#[test]
fn service_index_persists_php_array_config() {
    let root = tempfile::tempdir().unwrap();
    let config_dir = tempfile::tempdir().unwrap();
    let path = format!("{}/config/services.php", temp_path(&root));
    let source = r#"<?php
return [
    'parameters' => ['app.parameter' => 'value'],
    'services' => [
        'app.target' => [
            'class' => 'App\\Target',
            'tags' => ['app.tag'],
        ],
        'app.alias' => '@app.target',
    ],
];
"#;
    let index = ServiceIndex::new(&temp_path(&root), config_dir.path(), None).unwrap();
    index
        .index(&ParsedFile::new(path.clone(), source.as_bytes().to_vec()))
        .unwrap();
    index.close().unwrap();
    drop(index);

    let restored = ServiceIndex::new(&temp_path(&root), config_dir.path(), None).unwrap();
    let target = restored.get_service_by_id("app.target").unwrap().unwrap();
    assert_eq!(target.class, r"App\Target");
    assert!(target.tags.contains_key("app.tag"));
    assert_eq!(
        restored
            .get_service_by_id("app.alias")
            .unwrap()
            .unwrap()
            .alias_target,
        "app.target"
    );
    assert_eq!(
        restored
            .get_parameter_by_name("app.parameter")
            .unwrap()
            .unwrap()
            .value,
        "value"
    );

    restored
        .index(&ParsedFile::new(path, b"<?php return 1;".to_vec()))
        .unwrap();
    assert!(restored.get_service_by_id("app.target").unwrap().is_none());
    assert!(
        restored
            .get_parameter_by_name("app.parameter")
            .unwrap()
            .is_none()
    );
    restored.close().unwrap();
}

#[test]
fn service_index_resolves_instanceof_tags() {
    let config_dir = tempfile::tempdir().unwrap();
    let root = tempfile::tempdir().unwrap();
    let php = swlsp_php::PhpIndex::open(config_dir.path(), None).unwrap();
    let index = ServiceIndex::new(&temp_path(&root), config_dir.path(), None).unwrap();
    index.set_php_index(Some(php.clone()));
    php.index(&ParsedFile::new(
        "src/Listing.php",
        br"<?php
namespace App\Listing;
interface FilterHandler {}
abstract class AbstractHandler implements FilterHandler {}
final class ProductHandler extends AbstractHandler {}
final class OtherHandler {}
"
        .to_vec(),
    ))
    .unwrap();
    index
        .index(&ParsedFile::new(
            "config/listing.php",
            br"<?php
use App\Listing\FilterHandler;
use App\Listing\OtherHandler;
use App\Listing\ProductHandler;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->instanceof(FilterHandler::class)->tag('app.filter_handler');
    $services->set(ProductHandler::class);
    $services->set(OtherHandler::class);
};
"
            .to_vec(),
        ))
        .unwrap();
    assert_eq!(
        index.get_services_by_tag("app.filter_handler").unwrap(),
        vec![r"App\Listing\ProductHandler"]
    );
    let service = index
        .get_service_by_id(r"App\Listing\ProductHandler")
        .unwrap()
        .unwrap();
    assert!(service.tags.contains_key("app.filter_handler"));
    let other = index
        .get_service_by_id(r"App\Listing\OtherHandler")
        .unwrap()
        .unwrap();
    assert!(!other.tags.contains_key("app.filter_handler"));
    index.close().unwrap();
    php.close().unwrap();
}

#[test]
fn service_index_expands_php_prototypes() {
    let root = tempfile::tempdir().unwrap();
    let project_root = temp_path(&root);
    let config_dir = tempfile::tempdir().unwrap();
    let php = swlsp_php::PhpIndex::open(config_dir.path(), None).unwrap();
    let index = ServiceIndex::new(&project_root, config_dir.path(), None).unwrap();
    index.set_php_index(Some(php.clone()));
    let index_php = |relative: &str, source: &str| {
        php.index(&ParsedFile::new(
            format!("{project_root}/{relative}"),
            source.as_bytes().to_vec(),
        ))
        .unwrap();
    };
    index_php("src/Marker.php", "<?php namespace App; interface Marker {}");
    index_php(
        "src/Included.php",
        "<?php namespace App; final class Included implements Marker {}",
    );
    index_php(
        "src/Explicit.php",
        "<?php namespace App; final class Explicit {}",
    );
    index_php(
        "src/AbstractService.php",
        "<?php namespace App; abstract class AbstractService implements Marker {}",
    );
    index_php("src/Reusable.php", "<?php namespace App; trait Reusable {}");
    index_php("src/Status.php", "<?php namespace App; enum Status {}");
    index_php(
        "src/Excluded/Hidden.php",
        "<?php namespace App\\Excluded; final class Hidden {}",
    );

    let config_path = format!("{project_root}/config/services.php");
    let config = r#"<?php
use App\Marker;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->instanceof(Marker::class)->tag('app.marker');
    $services->load('App\\', __DIR__ . '/../src/')
        ->exclude(__DIR__ . '/../src/Excluded')
        ->tag('app.prototype');
    $services->set(\App\Explicit::class);
};
"#;
    index
        .index(&ParsedFile::new(
            config_path.clone(),
            config.as_bytes().to_vec(),
        ))
        .unwrap();
    assert_eq!(
        index.get_all_services().unwrap(),
        vec![r"App\Explicit", r"App\Included"]
    );
    let service = index.get_service_by_id(r"App\Included").unwrap().unwrap();
    assert_eq!(service.path, config_path);
    assert!(service.tags.contains_key("app.prototype"));
    assert!(service.tags.contains_key("app.marker"));
    assert_eq!(
        index.get_services_by_tag("app.prototype").unwrap(),
        vec![r"App\Included"]
    );
    assert_eq!(
        index.get_services_by_tag("app.marker").unwrap(),
        vec![r"App\Included"]
    );

    // Prototype expansion follows the PHP index revision.
    index_php("src/Added.php", "<?php namespace App; final class Added {}");
    assert_eq!(
        index.get_all_services().unwrap(),
        vec![r"App\Added", r"App\Explicit", r"App\Included"]
    );
    php.removed_files(&[format!("{project_root}/src/Added.php")])
        .unwrap();
    assert_eq!(
        index.get_all_services().unwrap(),
        vec![r"App\Explicit", r"App\Included"]
    );

    index
        .index(&ParsedFile::new(config_path, b"<?php return 1;".to_vec()))
        .unwrap();
    assert!(index.get_all_services().unwrap().is_empty());
    index.close().unwrap();
    php.close().unwrap();
}

#[test]
fn php_prototype_path_patterns() {
    assert!(path_matches_config_pattern(
        "/project/src/Entity/Product.php",
        "/project/src/{Entity,Repository}"
    ));
    assert!(path_matches_config_pattern(
        "/project/src/Kernel.php",
        "/project/src/{Entity,Kernel.php}"
    ));
    assert!(path_matches_config_pattern(
        "/project/src/Domain/Tests/ProductTest.php",
        "/project/src/**/Tests/*Test.php"
    ));
    assert!(!path_matches_config_pattern(
        "/project/src/Domain/Product.php",
        "/project/src/**/Tests/*Test.php"
    ));
}
