use std::path::{Path, PathBuf};
use std::sync::Arc;

use serde_json::Value;
use swlsp_admin::AdminComponentIndexer;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_symfony::{RouteIndexer, RouteUsageIndexer, ServiceIndex};
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::file_uri;

use crate::*;

fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

fn write(path: &Path, source: &str) {
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(path, source).unwrap();
}

fn parsed(path: &Path, source: &str) -> ParsedFile {
    ParsedFile::new(p(path), source.as_bytes().to_vec())
}

fn lenses_for(
    provider: &dyn CodeLensProvider,
    path: &Path,
    source: &str,
) -> Vec<protocol::CodeLens> {
    let document = TextDocument::new(file_uri(&p(path)), source, 1);
    let mut params = protocol::CodeLensParams::default();
    params.text_document.uri = document.uri.clone();
    provider
        .get_code_lenses(
            &Context::background(),
            &CodeLensRequest {
                params,
                document: Arc::new(document),
            },
        )
        .unwrap()
}

fn titles(lenses: &[protocol::CodeLens]) -> Vec<String> {
    lenses
        .iter()
        .map(|lens| lens.command.as_ref().unwrap().title.clone())
        .collect()
}

fn sorted(mut values: Vec<String>) -> Vec<String> {
    values.sort();
    values
}

fn targets(lens: &protocol::CodeLens) -> Vec<String> {
    let command = lens.command.as_ref().expect("command");
    assert_eq!(command.command, OPEN_REFERENCES_COMMAND);
    assert_eq!(command.arguments.len(), 1);
    command.arguments[0]
        .as_array()
        .expect("targets")
        .iter()
        .map(|target| target.as_str().unwrap().to_owned())
        .collect()
}

fn by_title<'a>(lenses: &'a [protocol::CodeLens], title: &str) -> &'a protocol::CodeLens {
    lenses
        .iter()
        .find(|lens| {
            lens.command
                .as_ref()
                .is_some_and(|command| command.title == title)
        })
        .unwrap_or_else(|| panic!("code lens not found: {title} in {:?}", titles(lenses)))
}

fn target(path: &Path, line: i64) -> String {
    related_target(&p(path), line)
}

fn php_index(cache: &Path) -> PhpIndex {
    PhpIndex::open(cache, None).unwrap()
}

fn service_index(root: &Path, cache: &Path, php: Option<&PhpIndex>) -> Arc<ServiceIndex> {
    let services = Arc::new(ServiceIndex::new(&p(root), cache, None).unwrap());
    if let Some(php) = php {
        services.set_php_index(Some(php.clone()));
    }
    services
}

// -- php service ----------------------------------------------------------------

#[test]
fn php_service_code_lens_links_autowired_constructor_to_prototype() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let cache = root.join(".cache");
    let php_path = root.join("src/WiredService.php");
    let service_path = root.join("config/services.yaml");
    let php_source = "<?php\nnamespace App;\n\nfinal class WiredService\n{\n    public function __construct() {}\n}\n";
    let service_source =
        "services:\n  _defaults:\n    autowire: true\n  App\\:\n    resource: ../src/\n";
    let php = php_index(&cache);
    let services = service_index(root, &cache, Some(&php));
    php.index(&parsed(&php_path, php_source)).unwrap();
    services
        .index(&parsed(&service_path, service_source))
        .unwrap();

    let provider = PhpServiceCodeLensProvider::new(Some(php), Some(services));
    let lenses = lenses_for(&provider, &php_path, php_source);
    assert_eq!(
        titles(&lenses),
        vec![
            "Open Service Definition",
            "Open autowired service definition"
        ]
    );
    assert_eq!(lenses[0].range.start.line, 3);
    assert_eq!(lenses[1].range.start.line, 5);
    for lens in &lenses {
        assert_eq!(targets(lens), vec![target(&service_path, 4)]);
    }
}

#[test]
fn php_service_code_lens_omits_constructor_for_disabled_autowire() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let cache = root.join(".cache");
    let php_path = root.join("src/ManualService.php");
    let service_path = root.join("config/services.yaml");
    let php_source = "<?php\nnamespace App;\nfinal class ManualService\n{\n    public function __construct() {}\n}\n";
    let service_source = "services:\n  App\\ManualService:\n    autowire: false\n";
    let php = php_index(&cache);
    let services = service_index(root, &cache, Some(&php));
    php.index(&parsed(&php_path, php_source)).unwrap();
    services
        .index(&parsed(&service_path, service_source))
        .unwrap();

    let provider = PhpServiceCodeLensProvider::new(Some(php), Some(services));
    let lenses = lenses_for(&provider, &php_path, php_source);
    assert_eq!(titles(&lenses), vec!["Open Service Definition"]);
}

// -- admin components -----------------------------------------------------------

#[test]
fn admin_component_code_lenses_link_definitions_templates_and_extensions() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let index = Arc::new(AdminComponentIndexer::new(&p(&root.join("cache")), None).unwrap());
    let admin_root = root.join("src/Administration/Resources/app/administration/src");
    let component_dir = admin_root.join("app/component/sw-card");
    let template_path = component_dir.join("sw-card.html.twig");
    let template_source = "<div><slot name=\"actions\"></slot></div>";
    write(&template_path, template_source);
    let base_path = component_dir.join("index.js");
    let base_source = "\nimport template from './sw-card.html.twig';\nComponent.register('sw-card', {\n    template,\n    props: { title: String },\n});";
    let extension_path = admin_root.join("extension/sw-card/index.js");
    let extension_source = "Component.override('sw-card', {\n    props: { subtitle: String },\n});";
    index.index(&parsed(&base_path, base_source)).unwrap();
    index
        .index(&parsed(&extension_path, extension_source))
        .unwrap();
    let provider = AdminComponentCodeLensProvider::new(Some(index));

    let base = lenses_for(&provider, &base_path, base_source);
    assert_eq!(
        sorted(titles(&base)),
        vec!["Open component extensions", "Open component template"]
    );
    assert_eq!(
        targets(by_title(&base, "Open component template")),
        vec![target(&template_path, 1)]
    );
    assert_eq!(
        targets(by_title(&base, "Open component extensions")),
        vec![target(&extension_path, 1)]
    );

    let extension = lenses_for(&provider, &extension_path, extension_source);
    assert_eq!(titles(&extension), vec!["Open base component"]);
    assert_eq!(targets(&extension[0]), vec![target(&base_path, 3)]);

    let template = lenses_for(&provider, &template_path, template_source);
    assert_eq!(
        sorted(titles(&template)),
        vec![
            "Open component extensions",
            "Open sw-card component definition"
        ]
    );
    assert_eq!(
        targets(by_title(&template, "Open sw-card component definition")),
        vec![target(&base_path, 3)]
    );
}

// -- console commands -----------------------------------------------------------

fn console_root() -> tempfile::TempDir {
    let root = tempfile::tempdir().unwrap();
    write(&root.path().join("bin/console"), "#!/usr/bin/env php\n");
    root
}

#[test]
fn console_command_code_lenses_cover_attribute_and_legacy_declarations() {
    let root = console_root();
    let path = root.path().join("src/Commands.php");
    let source = r"<?php
namespace App\Command;

#[AsCommand(name: 'app:user:create', aliases: ['app:user:add'])]
final class CreateUserCommand {}

final class UserCommands
{
    #[AsCommand('app:user:delete')]
    public function delete(): int { return 0; }
}

final class LegacyCommand
{
    protected static $defaultName = 'app:legacy|app:old';
}

final class ConfiguredCommand
{
    protected function configure(): void
    {
        $this->setName('app:configured');
    }
}
";
    let lenses = lenses_for(
        &ConsoleCommandCodeLensProvider::new(p(root.path())),
        &path,
        source,
    );
    let names = [
        "app:user:create",
        "app:user:delete",
        "app:legacy",
        "app:configured",
    ];
    assert_eq!(
        titles(&lenses),
        names
            .iter()
            .map(|name| format!("Run {name}"))
            .collect::<Vec<_>>()
    );
    assert_eq!(
        lenses
            .iter()
            .map(|lens| lens.range.start.line)
            .collect::<Vec<_>>(),
        vec![3, 8, 14, 21]
    );
    for (lens, name) in lenses.iter().zip(names) {
        let command = lens.command.as_ref().unwrap();
        assert_eq!(command.command, RUN_SYMFONY_COMMAND_ID);
        assert_eq!(
            command.arguments,
            vec![
                Value::String(name.to_owned()),
                Value::String(file_uri(&p(&path)))
            ]
        );
    }
}

#[test]
fn console_command_code_lenses_use_unsaved_command_name() {
    let root = console_root();
    let lenses = lenses_for(
        &ConsoleCommandCodeLensProvider::new(p(root.path())),
        &root.path().join("src/DraftCommand.php"),
        "<?php\n#[AsCommand('app:unsaved')]\nfinal class DraftCommand {}\n",
    );
    assert_eq!(titles(&lenses), vec!["Run app:unsaved"]);
}

#[test]
fn console_command_code_lenses_require_workspace_console() {
    let root = tempfile::tempdir().unwrap();
    let lenses = lenses_for(
        &ConsoleCommandCodeLensProvider::new(p(root.path())),
        &root.path().join("src/DraftCommand.php"),
        "<?php\n#[AsCommand('app:unavailable')]\nfinal class DraftCommand {}\n",
    );
    assert!(lenses.is_empty());
}

// -- controller related ---------------------------------------------------------

#[test]
fn controller_related_code_lenses_link_php_and_twig_both_ways() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let cache = root.join(".cache");
    let class_path = root.join("src/NavController.php");
    let twig_path = root.join("templates/navigation.html.twig");
    let class_source = "<?php\nnamespace App\\Controller;\nclass NavController {\n    public function menu(): void {}\n}";
    let twig_source = "{{ render(controller('App\\\\Controller\\\\NavController::menu')) }}\n{{ controller('app.navigation:menu') }}";
    write(&class_path, class_source);
    write(&twig_path, twig_source);
    let php = php_index(&cache);
    php.index(&parsed(&class_path, class_source)).unwrap();
    let services = service_index(root, &cache, None);
    services
        .index(&parsed(
            &root.join("services.yaml"),
            "services:\n  app.navigation:\n    class: App\\Controller\\NavController\n",
        ))
        .unwrap();
    let usages = Arc::new(RouteUsageIndexer::new(&cache, None).unwrap());
    usages.index(&parsed(&twig_path, twig_source)).unwrap();
    let provider = ControllerRelatedCodeLensProvider::new(Some(usages), Some(services), Some(php));

    let php_lenses = lenses_for(&provider, &class_path, class_source);
    assert_eq!(titles(&php_lenses), vec!["Open 2 Twig controller usages"]);
    assert_eq!(
        targets(&php_lenses[0]),
        vec![target(&twig_path, 1), target(&twig_path, 2)]
    );

    let twig_lenses = lenses_for(&provider, &twig_path, twig_source);
    assert_eq!(
        titles(&twig_lenses),
        vec!["Open controller method", "Open controller method"]
    );
    for lens in &twig_lenses {
        assert_eq!(targets(lens), vec![target(&class_path, 4)]);
    }
    assert_eq!(
        provider
            .resolve_code_lens(&Context::background(), &twig_lenses[0])
            .unwrap(),
        None
    );
}

// -- messenger ------------------------------------------------------------------

#[test]
fn messenger_code_lenses_link_messages_handlers_and_dispatches() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let files: Vec<(PathBuf, &str)> = vec![
        (
            root.join("vendor/MessageBusInterface.php"),
            "<?php\nnamespace Symfony\\Component\\Messenger;\ninterface MessageBusInterface {\n    public function dispatch(object $message, array $stamps = []): object;\n}",
        ),
        (
            root.join("src/Message.php"),
            "<?php\nnamespace App;\nclass Message {}",
        ),
        (
            root.join("src/Handler.php"),
            "<?php\nnamespace App;\nuse Symfony\\Component\\Messenger\\Attribute\\AsMessageHandler;\n#[AsMessageHandler]\nclass Handler {\n    public function __invoke(Message $message): void {}\n}",
        ),
        (
            root.join("src/Publisher.php"),
            "<?php\nnamespace App;\nuse Symfony\\Component\\Messenger\\MessageBusInterface;\nfunction publish(MessageBusInterface $messageBus): void {\n    $messageBus->dispatch(new Message());\n}",
        ),
    ];
    let php = php_index(&root.join(".php"));
    let messenger = Arc::new(swlsp_messenger::Index::new(root.join(".messenger"), None).unwrap());
    messenger.set_php_index(php.clone());
    for (path, source) in &files {
        write(path, source);
        let file = parsed(path, source);
        php.index(&file).unwrap();
        messenger.index(&file).unwrap();
    }
    let provider = MessengerCodeLensProvider::new(Some(messenger), Some(php));
    for (index, title) in [
        (1, "Messenger · 1 handler(s) · 1 dispatch(es)"),
        (2, "Handles Messenger message"),
    ] {
        let (path, source) = &files[index];
        let lenses = lenses_for(&provider, path, source);
        let lens = lenses
            .iter()
            .find(|lens| {
                lens.command
                    .as_ref()
                    .is_some_and(|c| c.title.contains(title))
            })
            .unwrap_or_else(|| panic!("{title}: {:?}", titles(&lenses)));
        let command = lens.command.as_ref().unwrap();
        assert_eq!(command.command, OPEN_REFERENCES_COMMAND);
        assert!(!command.arguments.is_empty());
    }
}

// -- related navigation ---------------------------------------------------------

#[test]
fn related_navigation_code_lenses_link_controller_route_and_template() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let controller_path = root.join("src/Controller/ProductController.php");
    let template_path = root.join("templates/product/show.html.twig");
    let controller_source = r"<?php
namespace App\Controller;

use Symfony\Component\Routing\Attribute\Route;

final class ProductController
{
    #[Route('/products/{id}', name: 'product.show')]
    public function show(): void
    {
        $this->render('product/show.html.twig');
    }
}
";
    let template_source = "{% block content %}Product{% endblock %}\n";
    write(&controller_path, controller_source);
    write(&template_path, template_source);
    let cache = root.join(".cache");
    let php = php_index(&cache);
    let twig = Arc::new(TwigIndexer::new(&cache, None).unwrap());
    let routes = Arc::new(RouteIndexer::new(&cache, None).unwrap());
    let controller_file = parsed(&controller_path, controller_source);
    php.index(&controller_file).unwrap();
    twig.index(&controller_file).unwrap();
    routes.index(&controller_file).unwrap();
    twig.index(&parsed(&template_path, template_source))
        .unwrap();

    let provider =
        RelatedNavigationCodeLensProvider::new(Some(twig), Some(php), Some(routes), None);
    let controller = lenses_for(&provider, &controller_path, controller_source);
    assert_eq!(
        sorted(titles(&controller)),
        vec!["Open related template", "Open route definition"]
    );
    assert_eq!(
        targets(by_title(&controller, "Open related template")),
        vec![target(&template_path, 1)]
    );
    assert_eq!(
        targets(by_title(&controller, "Open route definition")),
        vec![target(&controller_path, 8)]
    );

    let template = lenses_for(&provider, &template_path, template_source);
    assert_eq!(
        titles(&template),
        vec!["Open rendering PHP location", "Open related route"]
    );
    assert_eq!(targets(&template[0]), vec![target(&controller_path, 11)]);
    assert_eq!(targets(&template[1]), vec![target(&controller_path, 8)]);
}

#[test]
fn related_navigation_code_lenses_resolve_service_controller_routes() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let controller_path = root.join("src/ProductController.php");
    let template_path = root.join("templates/product/show.html.twig");
    let service_path = root.join("config/services.yaml");
    let route_path = root.join("config/routes.yaml");
    let controller_source = "<?php\nnamespace App\\Controller;\n\nfinal class ProductController\n{\n    public function show(): void\n    {\n        $this->render('product/show.html.twig');\n    }\n}\n";
    let template_source = "Product\n";
    let service_source =
        "services:\n  app.product_controller:\n    class: App\\Controller\\ProductController\n";
    let route_source =
        "product.show:\n  path: /products/{id}\n  controller: app.product_controller:show\n";
    for (path, source) in [
        (&controller_path, controller_source),
        (&template_path, template_source),
        (&service_path, service_source),
        (&route_path, route_source),
    ] {
        write(path, source);
    }
    let cache = root.join(".cache");
    let php = php_index(&cache);
    let twig = Arc::new(TwigIndexer::new(&cache, None).unwrap());
    let routes = Arc::new(RouteIndexer::new(&cache, None).unwrap());
    let services = service_index(root, &cache, None);
    let controller_file = parsed(&controller_path, controller_source);
    php.index(&controller_file).unwrap();
    twig.index(&controller_file).unwrap();
    twig.index(&parsed(&template_path, template_source))
        .unwrap();
    services
        .index(&parsed(&service_path, service_source))
        .unwrap();
    routes.index(&parsed(&route_path, route_source)).unwrap();

    let provider =
        RelatedNavigationCodeLensProvider::new(Some(twig), Some(php), Some(routes), Some(services));
    let controller = lenses_for(&provider, &controller_path, controller_source);
    assert_eq!(
        targets(by_title(&controller, "Open route definition")),
        vec![target(&route_path, 1)]
    );
    let template = lenses_for(&provider, &template_path, template_source);
    assert_eq!(
        targets(by_title(&template, "Open related route")),
        vec![target(&route_path, 1)]
    );
}

// -- route endpoints ------------------------------------------------------------

#[test]
fn route_endpoint_code_lenses_cover_php_yaml_and_xml() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = root.path();
    let php = php_index(cache.path());
    let services = service_index(root, cache.path(), Some(&php));
    let controller_path = root.join("src/CatalogController.php");
    let controller_source = "<?php\nnamespace App\\Controller;\nfinal class CatalogController {\n    public function show(): void {}\n    public function create(): void {}\n    public function __invoke(): void {}\n}\n";
    let service_path = root.join("config/services.yaml");
    let service_source =
        "services:\n  app.catalog_controller:\n    class: App\\Controller\\CatalogController\n";
    write(&controller_path, controller_source);
    write(&service_path, service_source);
    php.index(&parsed(&controller_path, controller_source))
        .unwrap();
    services
        .index(&parsed(&service_path, service_source))
        .unwrap();

    let provider = RouteEndpointCodeLensProvider::new(Some(services), Some(php.clone()));
    let yaml_source = r"catalog.show:
  path: /catalog/{id}
  methods: [GET]
  controller: App\Controller\CatalogController::show
catalog.create:
  path: /catalog
  methods: GET|POST
  controller: app.catalog_controller:create
catalog.invoke:
  path: /catalog/invoke
  controller: App\Controller\CatalogController
catalog.no_controller:
  path: /catalog/orphan
_internal:
  path: /_internal
  controller: App\Controller\CatalogController::show
";
    let yaml = lenses_for(&provider, &root.join("config/routes.yaml"), yaml_source);
    assert_eq!(
        titles(&yaml),
        vec![
            "GET /catalog/{id} · catalog.show",
            "GET|POST /catalog · catalog.create",
            "ANY /catalog/invoke · catalog.invoke",
        ]
    );
    assert_eq!(
        yaml.iter()
            .map(|lens| lens.range.start.line)
            .collect::<Vec<_>>(),
        vec![0, 4, 8]
    );
    assert_eq!(targets(&yaml[0]), vec![target(&controller_path, 4)]);
    assert_eq!(targets(&yaml[1]), vec![target(&controller_path, 5)]);
    assert_eq!(targets(&yaml[2]), vec![target(&controller_path, 6)]);

    let xml_source = r#"<routes>
  <route id="catalog.xml" path="/catalog.xml"
         methods="PUT, PATCH"
         controller="app.catalog_controller:show"/>
  <route id="_internal" path="/_internal"
         controller="App\Controller\CatalogController::show"/>
</routes>"#;
    let xml = lenses_for(&provider, &root.join("config/routes.xml"), xml_source);
    assert_eq!(titles(&xml), vec!["PUT|PATCH /catalog.xml · catalog.xml"]);
    assert_eq!(targets(&xml[0]), vec![target(&controller_path, 4)]);

    let php_path = root.join("src/EndpointController.php");
    let php_source = r"<?php
namespace App\Controller;
use Symfony\Component\Routing\Attribute\Route;
use Symfony\Component\HttpFoundation\Request;
final class EndpointController {
    #[Route('/endpoint', name: 'endpoint.show', methods: [Request::METHOD_PATCH])]
    public function show(): void {}
}
";
    write(&php_path, php_source);
    php.index(&parsed(&php_path, php_source)).unwrap();
    let php_lenses = lenses_for(&provider, &php_path, php_source);
    assert_eq!(titles(&php_lenses), vec!["PATCH /endpoint · endpoint.show"]);
    assert_eq!(php_lenses[0].range.start.line, 5);
    assert_eq!(targets(&php_lenses[0]), vec![target(&php_path, 7)]);
}

#[test]
fn route_endpoint_code_lenses_use_unsaved_route_metadata() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = root.path();
    let php = php_index(cache.path());
    let controller_path = root.join("src/CatalogController.php");
    let controller_source = "<?php\nnamespace App\\Controller;\nfinal class CatalogController {\n    public function show(): void {}\n}";
    write(&controller_path, controller_source);
    php.index(&parsed(&controller_path, controller_source))
        .unwrap();
    let provider = RouteEndpointCodeLensProvider::new(None, Some(php));
    let lenses = lenses_for(
        &provider,
        &root.join("config/routes.yaml"),
        "catalog.show:\n  path: /unsaved\n  methods: [HEAD]\n  controller: App\\Controller\\CatalogController::show\n",
    );
    assert_eq!(titles(&lenses), vec!["HEAD /unsaved · catalog.show"]);
}

// -- route resources ------------------------------------------------------------

#[test]
fn route_resource_code_lenses() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let config_dir = root.join("config");
    let controller_dir = root.join("src/Controller");
    let route_file = config_dir.join("catalog.yaml");
    write(&route_file, "catalog: {}\n");
    let controllers: Vec<PathBuf> = ["CatalogController.php", "ProductController.php"]
        .iter()
        .map(|name| {
            let path = controller_dir.join(name);
            write(&path, "<?php");
            path
        })
        .collect();
    let provider = RouteResourceCodeLensProvider::new(None, None);
    let fixtures: Vec<(PathBuf, &str, &str, u32, Vec<PathBuf>)> = vec![
        (
            config_dir.join("routes.yaml"),
            "catalog:\n  resource: catalog.yaml\n",
            "Open routing resource",
            1,
            vec![route_file.clone()],
        ),
        (
            config_dir.join("routes.yaml"),
            "controllers:\n  resource:\n    path: ../src/Controller/\n    namespace: App\\Controller\n  type: attribute\n",
            "Open 2 matching route files",
            2,
            controllers.clone(),
        ),
        (
            config_dir.join("routes.xml"),
            r#"<routes><import resource="../src/Controller/" type="attribute"/></routes>"#,
            "Open 2 matching route files",
            0,
            controllers.clone(),
        ),
        (
            config_dir.join("routes.php"),
            "<?php\nuse Symfony\\Component\\Routing\\Loader\\Configurator\\RoutingConfigurator;\nreturn static function (RoutingConfigurator $routes): void {\n    $routes->import('../src/Controller/', 'attribute');\n};",
            "Open 2 matching route files",
            3,
            controllers.clone(),
        ),
    ];
    for (path, source, title, line, expected) in fixtures {
        let lenses = lenses_for(&provider, &path, source);
        assert_eq!(lenses.len(), 1, "{title}");
        let lens = by_title(&lenses, title);
        assert_eq!(lens.range.start.line, line);
        assert_eq!(
            sorted(targets(lens)),
            sorted(expected.iter().map(|path| target(path, 1)).collect())
        );
    }
}

#[test]
fn route_resource_code_lens_ignores_service_resources() {
    let root = tempfile::tempdir().unwrap();
    let lenses = lenses_for(
        &RouteResourceCodeLensProvider::new(None, None),
        &root.path().join("services.yaml"),
        "services:\n  App\\:\n    resource: ../src/\n",
    );
    assert!(lenses.is_empty());
}

#[test]
fn route_resource_code_lens_links_imported_file_back_to_imports() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let config_dir = root.join("config");
    let controller_path = root.join("src/Controller/CatalogController.php");
    write(&controller_path, "<?php final class CatalogController {}");
    let index = Arc::new(RouteIndexer::new(root.join(".cache"), None).unwrap());
    let mut expected = Vec::new();
    for (name, source) in [
        (
            "routes.yaml",
            "controllers:\n  resource: ../src/Controller/\n  type: attribute\n",
        ),
        (
            "admin_routes.xml",
            "<routes>\n  <import resource=\"../src/Controller/\" type=\"attribute\"/>\n</routes>",
        ),
    ] {
        let config_path = config_dir.join(name);
        write(&config_path, source);
        index.index(&parsed(&config_path, source)).unwrap();
        expected.push(target(&config_path, 2));
    }
    let lenses = lenses_for(
        &RouteResourceCodeLensProvider::new(Some(index), None),
        &controller_path,
        "<?php final class CatalogController {}",
    );
    assert_eq!(lenses.len(), 1);
    let lens = by_title(&lenses, "Open 2 routing imports");
    assert_eq!(lens.range.start.line, 0);
    assert_eq!(sorted(targets(lens)), sorted(expected));
}

#[test]
fn route_resource_code_lens_supports_legacy_bundle_resources() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let php = php_index(&root.join(".php-cache"));
    let interface_path = root.join("BundleInterface.php");
    let interface_source =
        "<?php\nnamespace Symfony\\Component\\HttpKernel\\Bundle;\ninterface BundleInterface {}\n";
    let bundle_root = root.join("vendor/acme/foo-bundle");
    let bundle_path = bundle_root.join("FooBundle.php");
    let bundle_source = "<?php\nnamespace Acme\\Foo;\nfinal class FooBundle implements \\Symfony\\Component\\HttpKernel\\Bundle\\BundleInterface {}\n";
    for (path, source) in [
        (&interface_path, interface_source),
        (&bundle_path, bundle_source),
    ] {
        write(path, source);
        php.index(&parsed(path, source)).unwrap();
    }
    let controller_path = bundle_root.join("Controller/CatalogController.php");
    let controller_source = "<?php final class CatalogController {}";
    write(&controller_path, controller_source);
    let routes = Arc::new(RouteIndexer::new(root.join(".route-cache"), None).unwrap());
    let config_path = root.join("config/routes.yaml");
    let config_source = "controllers:\n  resource: '@FooBundle/Controller/'\n  type: attribute\n";
    write(&config_path, config_source);
    routes.index(&parsed(&config_path, config_source)).unwrap();
    let provider = RouteResourceCodeLensProvider::new(Some(routes), Some(php));

    let forward = lenses_for(&provider, &config_path, config_source);
    assert_eq!(forward.len(), 1);
    assert_eq!(
        targets(by_title(&forward, "Open routing resource")),
        vec![target(&controller_path, 1)]
    );
    let reverse = lenses_for(&provider, &controller_path, controller_source);
    assert_eq!(reverse.len(), 1);
    assert_eq!(
        targets(by_title(&reverse, "Open routing import")),
        vec![target(&config_path, 2)]
    );
}

// -- serializer -----------------------------------------------------------------

#[test]
fn serializer_code_lens_links_class_to_deserialize_uses() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let model_path = root.join("src/Model.php");
    let handler_path = root.join("src/Handler.php");
    let model_source = "<?php\nnamespace App;\nclass Model {}\n";
    let handler_source = "<?php\n$serializer->deserialize($data, 'App\\Model', 'json');\n";
    write(&model_path, model_source);
    write(&handler_path, handler_source);
    let php = php_index(&root.join(".php"));
    let serializer =
        Arc::new(swlsp_serializer::Index::new(root.join(".serializer"), None).unwrap());
    php.index(&parsed(&model_path, model_source)).unwrap();
    serializer
        .index(&parsed(&handler_path, handler_source))
        .unwrap();
    let lenses = lenses_for(
        &SerializerCodeLensProvider::new(Some(serializer), Some(php)),
        &model_path,
        model_source,
    );
    assert_eq!(titles(&lenses), vec!["1 serializer use(s)"]);
    assert_eq!(
        lenses[0].command.as_ref().unwrap().command,
        OPEN_REFERENCES_COMMAND
    );
    // Serializer targets keep the zero-based line.
    assert_eq!(
        targets(&lenses[0]),
        vec![swlsp_uriutil::file_uri_with_fragment(
            &p(&handler_path),
            "1"
        )]
    );
}

// -- service related ------------------------------------------------------------

#[test]
fn service_related_code_lenses_across_php_xml_and_yaml() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let cache = root.join(".cache");
    let class_path = root.join("src/Services.php");
    let xml_path = root.join("config/services.xml");
    let yaml_path = root.join("config/services.yaml");
    let php_path = root.join("config/services.php");
    let class_source = "<?php\nnamespace App;\nclass Base {}\nclass AbstractService {}\nclass Decorator {}\nclass PHPDecorator {}\n";
    let xml_source = "<container>\n  <services>\n    <service id=\"base\" class=\"App\\Base\"/>\n    <service id=\"abstract\" class=\"App\\AbstractService\"/>\n  </services>\n</container>\n";
    let yaml_source = "services:\n  yaml.decorator:\n    class: App\\Decorator\n    decorates: base\n    parent: abstract\n  'App\\':\n    resource: '../src'\n";
    let php_source = r"<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set('php.decorator', App\PHPDecorator::class)
        ->decorate('base')
        ->parent('abstract');
};
";
    for (path, source) in [
        (&class_path, class_source),
        (&xml_path, xml_source),
        (&yaml_path, yaml_source),
        (&php_path, php_source),
    ] {
        write(path, source);
    }
    let php = php_index(&cache);
    php.index(&parsed(&class_path, class_source)).unwrap();
    let services = service_index(root, &cache, Some(&php));
    for (path, source) in [
        (&xml_path, xml_source),
        (&yaml_path, yaml_source),
        (&php_path, php_source),
    ] {
        services.index(&parsed(path, source)).unwrap();
    }
    let provider = ServiceRelatedCodeLensProvider::new(Some(services), Some(php));

    let xml = lenses_for(&provider, &xml_path, xml_source);
    assert_eq!(xml.len(), 2);
    let expected = sorted(vec![target(&yaml_path, 2), target(&php_path, 6)]);
    assert_eq!(
        sorted(targets(by_title(&xml, "Open 2 decorating services"))),
        expected
    );
    assert_eq!(
        sorted(targets(by_title(&xml, "Open 2 child services"))),
        expected
    );

    let yaml = lenses_for(&provider, &yaml_path, yaml_source);
    assert_eq!(yaml.len(), 3);
    assert_eq!(
        targets(by_title(&yaml, "Open decorated service")),
        vec![target(&xml_path, 3)]
    );
    assert_eq!(
        targets(by_title(&yaml, "Open parent service")),
        vec![target(&xml_path, 4)]
    );
    assert_eq!(
        sorted(targets(by_title(&yaml, "Open 4 prototype classes"))),
        sorted((3..=6).map(|line| target(&class_path, line)).collect())
    );

    let php_lenses = lenses_for(&provider, &php_path, php_source);
    assert_eq!(php_lenses.len(), 2);
    assert_eq!(
        targets(by_title(&php_lenses, "Open decorated service")),
        vec![target(&xml_path, 3)]
    );
    assert_eq!(
        targets(by_title(&php_lenses, "Open parent service")),
        vec![target(&xml_path, 4)]
    );
}

// -- symfony config -------------------------------------------------------------

#[test]
fn symfony_config_code_lenses_link_roots_and_resources() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let declaration_path = root.join("src/DependencyInjection/Configuration.php");
    let resource_path = root.join("config/packages/legacy.yaml");
    let declaration_source = r"<?php
namespace App\DependencyInjection;
use Symfony\Component\Config\Definition\Builder\TreeBuilder;
final class Configuration {
    public function getConfigTreeBuilder(): TreeBuilder {
        return new TreeBuilder('app_root');
    }
}
";
    write(&declaration_path, declaration_source);
    write(&resource_path, "framework: {}\n");
    let index = Arc::new(swlsp_symfonyconfig::Index::new(root.join(".cache"), None).unwrap());
    index
        .index(&parsed(&declaration_path, declaration_source))
        .unwrap();
    let provider = SymfonyConfigCodeLensProvider::new(Some(index));
    for (name, source, root_line, resource_line) in [
        (
            "app.yaml",
            "when@prod:\n  app_root: {}\n  imports:\n    - resource: legacy.yaml\n",
            1,
            3,
        ),
        (
            "app.php",
            "<?php\nreturn [\n    'app_root' => [],\n    'imports' => [['resource' => 'legacy.yaml']],\n];\n",
            2,
            3,
        ),
    ] {
        let lenses = lenses_for(&provider, &root.join("config/packages").join(name), source);
        assert_eq!(lenses.len(), 2, "{name}");
        let root_lens = by_title(&lenses, "Open configuration declaration");
        assert_eq!(root_lens.range.start.line, root_line);
        assert_eq!(targets(root_lens), vec![target(&declaration_path, 6)]);
        let resource_lens = by_title(&lenses, "Open configuration resource");
        assert_eq!(resource_lens.range.start.line, resource_line);
        assert_eq!(targets(resource_lens), vec![target(&resource_path, 1)]);
    }
}

// -- twig -----------------------------------------------------------------------

#[test]
fn twig_code_lens_links_references_inheritance_and_blocks() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let base_path = root.join("templates/base.html.twig");
    let child_path = root.join("templates/child.html.twig");
    let grand_path = root.join("templates/grand.html.twig");
    let page_path = root.join("templates/page.html.twig");
    let base_source = "{% block title %}Base{% endblock %}\n{% block body %}Body{% endblock %}\n";
    let child_source = "{% extends 'base.html.twig' %}\n{% block title %}Child{% endblock %}\n";
    let grand_source = "{% extends 'child.html.twig' %}\n{% block title %}Grand{% endblock %}\n{% block body %}Grand body{% endblock %}\n";
    let page_source = "{% include 'base.html.twig' %}\n";
    let sources = [
        (&base_path, base_source),
        (&child_path, child_source),
        (&grand_path, grand_source),
        (&page_path, page_source),
    ];
    for (path, source) in sources {
        write(path, source);
    }
    let twig = Arc::new(TwigIndexer::new(root.join(".cache"), None).unwrap());
    for (path, source) in sources {
        twig.index(&parsed(path, source)).unwrap();
    }
    let provider = TwigCodeLensProvider::new(Some(twig));

    let base = lenses_for(&provider, &base_path, base_source);
    assert_eq!(
        targets(by_title(&base, "Open extending template")),
        vec![target(&child_path, 1)]
    );
    assert_eq!(
        targets(by_title(&base, "Open template reference")),
        vec![target(&page_path, 1)]
    );
    let title_implementations = by_title(&base, "Open 2 block implementations");
    assert_eq!(title_implementations.range.start.line, 0);
    assert_eq!(
        targets(title_implementations),
        vec![target(&child_path, 2), target(&grand_path, 2)]
    );
    let body_implementation = by_title(&base, "Open block implementation");
    assert_eq!(body_implementation.range.start.line, 1);
    assert_eq!(targets(body_implementation), vec![target(&grand_path, 3)]);

    let child = lenses_for(&provider, &child_path, child_source);
    assert_eq!(
        targets(by_title(&child, "Open extending template")),
        vec![target(&grand_path, 1)]
    );
    assert_eq!(
        targets(by_title(&child, "Open parent block")),
        vec![target(&base_path, 1)]
    );
    assert_eq!(
        targets(by_title(&child, "Open block implementation")),
        vec![target(&grand_path, 2)]
    );

    let grand = lenses_for(&provider, &grand_path, grand_source);
    assert_eq!(
        targets(by_title(&grand, "Open 2 parent blocks")),
        vec![target(&base_path, 1), target(&child_path, 2)]
    );
    assert_eq!(
        targets(by_title(&grand, "Open parent block")),
        vec![target(&base_path, 2)]
    );

    let unsaved = lenses_for(
        &provider,
        &grand_path,
        "{% extends 'base.html.twig' %}\n{% block title %}Grand{% endblock %}\n",
    );
    assert_eq!(
        targets(by_title(&unsaved, "Open parent block")),
        vec![target(&base_path, 1)]
    );
}

#[test]
fn twig_code_lens_links_same_name_template_overrides() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let first = root.join("app/templates/card.html.twig");
    let second = root.join("plugin/templates/card.html.twig");
    let twig = Arc::new(TwigIndexer::new(root.join(".cache"), None).unwrap());
    for path in [&first, &second] {
        write(path, "card");
        twig.index(&parsed(path, "card")).unwrap();
    }
    let lenses = lenses_for(&TwigCodeLensProvider::new(Some(twig)), &first, "card");
    assert_eq!(titles(&lenses), vec!["Open template override"]);
    assert_eq!(targets(&lenses[0]), vec![target(&second, 1)]);
}

// -- twig components ------------------------------------------------------------

#[test]
fn twig_component_related_code_lens_links_template_class_usages_and_blocks() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let cache = root.join(".cache");
    let class_path = root.join("src/Twig/Components/Alert.php");
    let template_path = root.join("templates/components/alert.html.twig");
    let usage_path = root.join("templates/page.html.twig");
    let class_source = r"<?php
namespace App\Twig\Components;

use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;

#[AsTwigComponent(name: 'Alert', template: 'components/alert.html.twig')]
final class Alert {}
";
    let template_source = "{% block headline %}Default headline{% endblock %}";
    let usage_source = "{{ component('Alert') }}\n<twig:Alert>\n    <twig:block name=\"headline\">Custom headline</twig:block>\n</twig:Alert>\n";
    let files = [
        (&class_path, class_source),
        (&template_path, template_source),
        (&usage_path, usage_source),
    ];
    for (path, source) in files {
        write(path, source);
    }
    let php = php_index(&cache);
    let twig = Arc::new(TwigIndexer::new(&cache, None).unwrap());
    let components = Arc::new(swlsp_twigcomponent::Index::new(&cache, None).unwrap());
    components.set_dependencies(Some(php.clone()), None, Some(twig.clone()));
    for (path, source) in files {
        let file = parsed(path, source);
        twig.index(&file).unwrap();
        components.index(&file).unwrap();
        if path.extension().is_some_and(|ext| ext == "php") {
            php.index(&file).unwrap();
        }
    }
    let provider = TwigComponentRelatedCodeLensProvider::new(Some(components), Some(php));
    let template = lenses_for(&provider, &template_path, template_source);
    assert_eq!(titles(&template), vec!["Open UX component"]);
    assert_eq!(
        targets(&template[0]),
        vec![
            target(&class_path, 7),
            target(&usage_path, 1),
            target(&usage_path, 2),
        ]
    );
    let usage = lenses_for(&provider, &usage_path, usage_source);
    assert_eq!(titles(&usage), vec!["Open component block"]);
    assert_eq!(usage[0].range.start.line, 2);
    assert_eq!(targets(&usage[0]), vec![target(&template_path, 1)]);
}

#[test]
fn twig_component_related_code_lens_skips_unrelated_twig() {
    let root = tempfile::tempdir().unwrap();
    let components =
        Arc::new(swlsp_twigcomponent::Index::new(root.path().join(".cache"), None).unwrap());
    let lenses = lenses_for(
        &TwigComponentRelatedCodeLensProvider::new(Some(components), None),
        &root.path().join("templates/page.html.twig"),
        "<h1>Page</h1>",
    );
    assert!(lenses.is_empty());
}

// -- validation -----------------------------------------------------------------

#[test]
fn validation_code_lens_links_constraint_and_validator() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let stub_path = root.join("vendor/Validator.php");
    let constraint_path = root.join("src/UniqueName.php");
    let validator_path = root.join("src/UniqueNameValidator.php");
    let stub_source = "<?php\nnamespace Symfony\\Component\\Validator;\nclass Constraint {}\ninterface ConstraintValidatorInterface {}\n";
    let constraint_source = "<?php\nnamespace App\\Validation;\nclass UniqueName extends \\Symfony\\Component\\Validator\\Constraint {}\n";
    let validator_source = "<?php\nnamespace App\\Validation;\nclass UniqueNameValidator implements \\Symfony\\Component\\Validator\\ConstraintValidatorInterface {}\n";
    let php = php_index(&root.join(".php"));
    for (path, source) in [
        (&stub_path, stub_source),
        (&constraint_path, constraint_source),
        (&validator_path, validator_source),
    ] {
        write(path, source);
        php.index(&parsed(path, source)).unwrap();
    }
    let provider = ValidationCodeLensProvider::new(Some(php), None);
    let constraint = lenses_for(&provider, &constraint_path, constraint_source);
    assert_eq!(titles(&constraint), vec!["Open validator"]);
    assert_eq!(targets(&constraint[0]), vec![target(&validator_path, 3)]);
    let validator = lenses_for(&provider, &validator_path, validator_source);
    assert_eq!(titles(&validator), vec!["Open constraint"]);
    assert_eq!(targets(&validator[0]), vec![target(&constraint_path, 3)]);
}

#[test]
fn validation_code_lens_links_constraint_message_to_translations() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path();
    let stub_path = root.join("vendor/Constraint.php");
    let constraint_path = root.join("src/UniqueName.php");
    let translation_path = root.join("translations/validators.en.yaml");
    let stub_source = "<?php\nnamespace Symfony\\Component\\Validator;\nclass Constraint {}\n";
    let constraint_source = "<?php\nnamespace App\\Validation;\nfinal class UniqueName extends \\Symfony\\Component\\Validator\\Constraint\n{\n    public string $message = 'validator.unique_name';\n}\n";
    let translation_source = "validator.unique_name: 'The name is already used.'\n";
    for (path, source) in [
        (&stub_path, stub_source),
        (&constraint_path, constraint_source),
        (&translation_path, translation_source),
    ] {
        write(path, source);
    }
    let cache = root.join(".cache");
    let php = php_index(&cache);
    let translations = Arc::new(swlsp_translation::Index::new(&cache, None).unwrap());
    for (path, source) in [
        (&stub_path, stub_source),
        (&constraint_path, constraint_source),
    ] {
        php.index(&parsed(path, source)).unwrap();
    }
    translations
        .index(&parsed(&translation_path, translation_source))
        .unwrap();
    let lenses = lenses_for(
        &ValidationCodeLensProvider::new(Some(php), Some(translations)),
        &constraint_path,
        constraint_source,
    );
    assert_eq!(titles(&lenses), vec!["Open validator translation"]);
    assert_eq!(lenses[0].range.start.line, 4);
    assert_eq!(targets(&lenses[0]), vec![target(&translation_path, 1)]);
}

// -- vite -----------------------------------------------------------------------

#[test]
fn vite_code_lens_links_entry_target_to_config_and_twig_usages() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let root = root.path();
    let target_path = root.join("assets/app.js");
    let config_path = root.join("vite.config.js");
    let template_path = root.join("templates/base.html.twig");
    let target_source = "console.log('app');";
    let index = Arc::new(swlsp_asset::Index::new(&p(root), cache.path(), None).unwrap());
    for (path, source) in [
        (&target_path, target_source),
        (
            &config_path,
            "export default defineConfig({\n  build: {rollupOptions: {input: {app: './assets/app.js'}}}\n});",
        ),
        (&template_path, "{{ vite_entry_script_tags('app') }}"),
    ] {
        write(path, source);
        index.index(&parsed(path, source)).unwrap();
    }
    let lenses = lenses_for(
        &ViteCodeLensProvider::new(Some(index)),
        &target_path,
        target_source,
    );
    assert_eq!(titles(&lenses), vec!["Vite entry 'app' · 2 related"]);
    let all = targets(&lenses[0]).join("");
    assert!(all.contains(&file_uri(&p(&config_path))));
    assert!(all.contains(&file_uri(&p(&template_path))));
}

// -- protocol shape -------------------------------------------------------------

#[test]
fn related_lens_serializes_command_arguments() {
    let lens = related_lens(line_range(2), "Open", vec!["file:///a#1".to_owned()]);
    assert_eq!(
        serde_json::to_string(&lens).unwrap(),
        r#"{"range":{"start":{"line":2,"character":0},"end":{"line":2,"character":0}},"command":{"title":"Open","command":"shopware.openReferences","arguments":[["file:///a#1"]]}}"#
    );
    assert_eq!(related_target("", 3), "");
    assert!(related_target("/a", 0).ends_with("#1"));
}
