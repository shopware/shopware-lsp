use super::*;
use std::path::Path;
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, ReferenceRequest, ReferencesProvider, SyntaxContext, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::SymbolKind;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};
use swlsp_uriutil::file_uri;

fn php_index(dir: &Path) -> PhpIndex {
    PhpIndex::open(dir, None).unwrap()
}

fn route_indexes(
    cache: &Path,
) -> (
    Arc<swlsp_symfony::RouteIndexer>,
    Arc<swlsp_symfony::RouteUsageIndexer>,
) {
    (
        Arc::new(swlsp_symfony::RouteIndexer::new(cache, None).unwrap()),
        Arc::new(swlsp_symfony::RouteUsageIndexer::new(cache, None).unwrap()),
    )
}

fn asset_index(root: &Path, cache: &Path, files: &[(&Path, &str)]) -> Arc<swlsp_asset::Index> {
    let index = Arc::new(swlsp_asset::Index::new(&p(root), cache, None).unwrap());
    for (path, source) in files {
        write(path, source);
    }
    for (path, source) in files {
        self::index(index.as_ref(), path, source);
    }
    index
}

pub(crate) fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

pub(crate) fn write(path: &Path, source: &str) {
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(path, source).unwrap();
}

pub(crate) fn index(indexer: &dyn Indexer, path: &Path, source: &str) {
    indexer
        .index(&ParsedFile::new(p(path), source.as_bytes().to_vec()))
        .unwrap();
}

pub(crate) fn document(path: &Path, source: &str, version: i32) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(file_uri(&p(path)), source, version))
}

pub(crate) fn node_at(document: &TextDocument, offset: usize) -> Option<SyntaxNode> {
    document.root()?.node_at(TextSize::from(offset as u32))
}

/// A request at byte `offset` with the deepest node at that offset.
pub(crate) fn request(
    document: &Arc<TextDocument>,
    offset: usize,
    include_declaration: bool,
) -> ReferenceRequest {
    request_with_node(
        document,
        offset,
        node_at(document, offset),
        include_declaration,
    )
}

pub(crate) fn request_with_node(
    document: &Arc<TextDocument>,
    offset: usize,
    node: Option<SyntaxNode>,
    include_declaration: bool,
) -> ReferenceRequest {
    let (line, character) = document.line_index().position_utf16(offset as u32);
    let mut params = protocol::ReferenceParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    params.context.include_declaration = include_declaration;
    ReferenceRequest {
        params,
        syntax: SyntaxContext {
            document: Some(document.clone()),
            language: document.language,
            root: document.root(),
            token: None,
            node,
        },
    }
}

pub(crate) fn references(
    provider: &dyn ReferencesProvider,
    request: &ReferenceRequest,
) -> Vec<protocol::Location> {
    provider
        .get_references(&Context::background(), request)
        .unwrap()
}

pub(crate) fn uris(locations: &[protocol::Location]) -> Vec<String> {
    locations
        .iter()
        .map(|location| location.uri.clone())
        .collect()
}

/// Asserts that the location URIs equal `expected`, ignoring order.
pub(crate) fn assert_uris_match(locations: &[protocol::Location], expected: &[String]) {
    let mut actual = uris(locations);
    let mut expected = expected.to_vec();
    actual.sort();
    expected.sort();
    assert_eq!(actual, expected);
}

#[test]
fn doctrine_dbal_references_use_unsaved_registrations() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let index = Arc::new(swlsp_doctrine::Index::new(cache.path().join("doctrine"), None).unwrap());
    let php = php_index(&cache.path().join("php"));
    let first = root.path().join("first.yaml");
    let second = root.path().join("second.yaml");
    let source = "doctrine:\n  dbal:\n    types:\n      money: App\\MoneyType\n";
    for path in [&first, &second] {
        write(path, source);
        self::index(index.as_ref(), path, source);
    }
    let provider = DoctrineReferenceProvider::new(Some(index), Some(php));
    for (name, count) in [("money", 2), ("euros", 1)] {
        let unsaved = source.replacen("money", name, 1);
        let document = document(&first, &unsaved, 2);
        let offset = unsaved.find(name).unwrap() + 1;
        let request = request_with_node(&document, offset, None, false);
        let result = references(&provider, &request);
        assert_eq!(result.len(), count, "{name}");
        let last = result.last().unwrap();
        assert_eq!(last.uri, document.uri);
        assert_eq!(
            (last.range.end.character - last.range.start.character) as usize,
            name.len()
        );
    }
}

#[test]
fn environment_references_include_declaration_on_request() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let env_path = root.path().join(".env");
    let yaml_path = root.path().join("config/services.yaml");
    let php_path = root.path().join("src/Config.php");
    let files = [
        (&env_path, "DATABASE_URL=mysql://localhost\n"),
        (&yaml_path, "database: '%env(resolve:DATABASE_URL)%'\n"),
        (
            &php_path,
            "<?php\n#[Autowire('%env(DATABASE_URL)%')]\n#[Autowire(env: 'resolve:DATABASE_URL')]\nclass Config {}\nenv('string:DATABASE_URL');\n",
        ),
    ];
    let index = Arc::new(swlsp_environment::Index::new(cache.path(), None).unwrap());
    for (path, source) in files {
        write(path, source);
        self::index(index.as_ref(), path, source);
    }
    let source = files[1].1;
    let document = document(&yaml_path, source, 1);
    let offset = source.find("DATABASE_URL").unwrap() + 2;
    let request = request_with_node(&document, offset, None, true);
    let locations = references(&EnvironmentReferenceProvider::new(Some(index)), &request);
    assert_eq!(locations.len(), 5);
    let found = uris(&locations);
    for path in [&env_path, &yaml_path, &php_path] {
        assert!(found.contains(&file_uri(&p(path))), "{path:?}");
    }
}

#[test]
fn event_references_connect_listeners_and_dispatch_sites() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let path = root.path().join("src/Events.php");
    let source = r"<?php
namespace App;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Symfony\Contracts\EventDispatcher\EventDispatcherInterface;
class DomainEvent {}
class Subscriber implements EventSubscriberInterface {
    public static function getSubscribedEvents(): array {
        return ['app.ready' => 'onReady'];
    }
    public function onReady(DomainEvent $event): void {}
}
function publish(EventDispatcherInterface $dispatcher): void {
    $dispatcher->dispatch(new DomainEvent(), 'app.ready');
}";
    write(&path, source);
    let php = php_index(&cache.path().join("php"));
    self::index(
        &php,
        &root.path().join("vendor/EventDispatcher.php"),
        r"<?php
namespace Symfony\Component\EventDispatcher;
interface EventSubscriberInterface {}
namespace Symfony\Contracts\EventDispatcher;
interface EventDispatcherInterface {
    public function dispatch(object $event, ?string $eventName = null): object;
}",
    );
    let events = Arc::new(swlsp_event::Index::new(cache.path().join("events"), None).unwrap());
    events.set_php_index(php.clone());
    self::index(&php, &path, source);
    self::index(events.as_ref(), &path, source);

    let document = document(&path, source, 1);
    let provider = EventReferenceProvider::new(Some(events.clone()), Some(php));
    let event_offset = source.rfind("'app.ready'").unwrap() + 2;
    let event_locations = references(&provider, &request(&document, event_offset, false));
    assert_eq!(event_locations.len(), 2);

    let method_offset = source.find("function onReady").unwrap() + "function ".len();
    let method_locations = references(
        &EventReferenceProvider::new(Some(events), None),
        &request(&document, method_offset, false),
    );
    assert_eq!(method_locations.len(), 2);
    let mut lines: Vec<u32> = method_locations
        .iter()
        .map(|location| location.range.start.line)
        .collect();
    lines.sort();
    assert_eq!(lines, [7, 12]);
}

#[test]
fn controller_references_link_php_method_and_equivalent_twig_usages() {
    let root = tempfile::tempdir().unwrap();
    let cache = root.path().join(".cache");
    let class_path = root.path().join("src/NavController.php");
    let twig_path = root.path().join("templates/navigation.html.twig");
    let class_source = r"<?php
namespace App\Controller;
class NavController {
    public function menu(): void {}
}";
    let twig_source = r"{{ controller('App\\Controller\\NavController::menu') }}
{{ controller('app.navigation:menu') }}";
    write(&class_path, class_source);
    write(&twig_path, twig_source);
    let php = php_index(&cache);
    self::index(&php, &class_path, class_source);
    let services =
        Arc::new(swlsp_symfony::ServiceIndex::new(&p(root.path()), &cache, None).unwrap());
    self::index(
        services.as_ref(),
        &root.path().join("services.yaml"),
        "services:\n  app.navigation:\n    class: App\\Controller\\NavController\n",
    );
    let usages = Arc::new(swlsp_symfony::RouteUsageIndexer::new(&cache, None).unwrap());
    self::index(usages.as_ref(), &twig_path, twig_source);
    let provider =
        ControllerReferenceProvider::new(Some(usages), Some(services), Some(php.clone()));

    let twig_document = document(&twig_path, twig_source, 1);
    let literals = swlsp_parser_twig::query::nodes(
        &twig_document.root().unwrap(),
        &[swlsp_parser_twig::syntax::TWIG_LITERAL_STRING],
    );
    assert_eq!(literals.len(), 2);
    let twig_locations = references(
        &provider,
        &request_with_node(&twig_document, 0, Some(literals[0].clone()), true),
    );
    assert_eq!(
        uris(&twig_locations),
        [
            file_uri(&p(&class_path)),
            file_uri(&p(&twig_path)),
            file_uri(&p(&twig_path)),
        ]
    );

    let php_document = document(&class_path, class_source, 1);
    let method = php
        .semantic_snapshot()
        .symbols_in(&p(&class_path))
        .into_iter()
        .find(|symbol| symbol.kind == SymbolKind::Method)
        .unwrap();
    let php_locations = references(
        &provider,
        &request_with_node(
            &php_document,
            u32::from(method.selection_range.start()) as usize,
            php_document.root(),
            false,
        ),
    );
    assert_eq!(php_locations.len(), 2);
}

#[test]
fn messenger_references_connect_messages_handlers_and_dispatches() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let config_path = root.path().join("config/services.yaml");
    let config_source = "services:
  App\\Handler:
    tags:
      - { name: messenger.message_handler, handles: App\\Message, method: consume }
";
    let paths = [
        (
            root.path().join("vendor/MessageBusInterface.php"),
            r"<?php
namespace Symfony\Component\Messenger;
interface MessageBusInterface {
    public function dispatch(object $message, array $stamps = []): object;
}",
        ),
        (
            root.path().join("src/Message.php"),
            "<?php\nnamespace App;\nclass Message {}",
        ),
        (
            root.path().join("src/Handler.php"),
            r"<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
#[AsMessageHandler]
class Handler {
    public function __invoke(Message $message): void {}
    public function consume(Message $message): void {}
}",
        ),
        (
            root.path().join("src/Publisher.php"),
            r"<?php
namespace App;
use Symfony\Component\Messenger\MessageBusInterface;
function publish(MessageBusInterface $messageBus): void {
    $messageBus->dispatch(new Message());
}",
        ),
        (config_path.clone(), config_source),
    ];
    let php = php_index(&cache.path().join("php"));
    let messenger =
        Arc::new(swlsp_messenger::Index::new(cache.path().join("messenger"), None).unwrap());
    messenger.set_php_index(php.clone());
    for (path, source) in &paths {
        write(path, source);
        self::index(&php, path, source);
        self::index(messenger.as_ref(), path, source);
    }
    let document = document(&config_path, config_source, 1);
    let offset = config_source.find("App\\Message").unwrap() + 1;
    let locations = references(
        &MessengerReferenceProvider::new(Some(messenger), Some(php)),
        &request(&document, offset, true),
    );
    assert!(locations.len() >= 4, "{locations:#?}");
    let found = uris(&locations);
    for path in ["src/Message.php", "src/Handler.php", "src/Publisher.php"] {
        assert!(
            found.contains(&file_uri(&p(&root.path().join(path)))),
            "{path}"
        );
    }
    assert!(found.contains(&file_uri(&p(&config_path))));
}

#[test]
fn route_references_connect_names_and_concrete_twig_html_urls() {
    let cache = tempfile::tempdir().unwrap();
    let (routes, usages) = route_indexes(cache.path());
    let project = Path::new("/project");
    let route_path = project.join("config/routes.yaml");
    let first = project.join("templates/first.twig");
    let second = project.join("templates/second.twig");
    let third = project.join("templates/third.twig");
    let fourth = project.join("templates/fourth.twig");
    let second_source = r#"<a href="/products/42">Product</a>"#;
    self::index(
        routes.as_ref(),
        &route_path,
        "product.show:\n    path: /products/{id}\n",
    );
    for (path, source) in [
        (&first, "{{ path('product.show', {'id': product.id}) }}"),
        (&second, second_source),
        (
            &third,
            r#"<a href="https://shop.example/products/foo.bar?preview=1#details">Product</a>"#,
        ),
        (
            &fourth,
            "{% if app.request.attributes.get('_route') == 'product.show' %}{% endif %}",
        ),
    ] {
        self::index(usages.as_ref(), path, source);
    }
    let document = document(&second, second_source, 1);
    let offset = second_source.find("/products/42").unwrap() + 2;
    let locations = references(
        &RouteReferenceProvider::new(routes, usages, None),
        &request(&document, offset, true),
    );
    assert_uris_match(
        &locations,
        &[&route_path, &first, &second, &third, &fourth].map(|path| file_uri(&p(path))),
    );
}

#[test]
fn route_references_preserve_repeated_same_file_occurrences() {
    let cache = tempfile::tempdir().unwrap();
    let (routes, usages) = route_indexes(cache.path());
    let route_path = Path::new("/project/config/routes.yaml");
    let template = Path::new("/project/templates/repeated.twig");
    self::index(
        routes.as_ref(),
        route_path,
        "product.show:\n    path: /products/{id}\n",
    );
    self::index(
        usages.as_ref(),
        template,
        "{{ path('product.show') }}\n{{ path('product.show') }}",
    );
    let indexed = routes.get_route("product.show").unwrap();
    let locations = RouteReferenceProvider::new(routes, usages, None)
        .references_for_routes(&indexed, false)
        .unwrap();
    assert_eq!(locations.len(), 2);
    assert_eq!(locations[0].uri, file_uri(&p(template)));
    assert_eq!(locations[1].uri, file_uri(&p(template)));
    assert_eq!(locations[0].range.start.line, 0);
    assert_eq!(locations[1].range.start.line, 1);
}

#[test]
fn serializer_references_connect_class_and_deserialize_targets() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let model_path = root.path().join("src/Model.php");
    let handler_path = root.path().join("src/Handler.php");
    let model_source = "<?php\nnamespace App;\nclass Model {}\n";
    let handler_source = r"<?php
namespace App;
function load($serializer): void {
    $serializer->deserialize($data, Model::class, 'json');
    $serializer->deserialize($data, 'App\Model[]', 'json');
}
";
    let php = php_index(&cache.path().join("php"));
    let serializer =
        Arc::new(swlsp_serializer::Index::new(cache.path().join("serializer"), None).unwrap());
    for (path, source) in [(&model_path, model_source), (&handler_path, handler_source)] {
        write(path, source);
        self::index(&php, path, source);
        self::index(serializer.as_ref(), path, source);
    }
    let document = document(&model_path, model_source, 1);
    let offset = model_source.find("Model").unwrap() + 2;
    let locations = references(
        &SerializerReferenceProvider::new(Some(serializer), Some(php)),
        &request(&document, offset, true),
    );
    assert_eq!(
        uris(&locations),
        [
            file_uri(&p(&model_path)),
            file_uri(&p(&handler_path)),
            file_uri(&p(&handler_path)),
        ]
    );
}

#[test]
fn asset_references_connect_declaration_and_twig_usages() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let asset_path = root.path().join("public/build/app.css");
    let first = root.path().join("templates/first.html.twig");
    let second = root.path().join("templates/second.html.twig");
    let first_source = "{{ asset('build/app.css') }}";
    let index = asset_index(
        root.path(),
        cache.path(),
        &[
            (&asset_path, "body{}"),
            (&first, first_source),
            (&second, r#"<link rel="stylesheet" href="/build/app.css">"#),
        ],
    );
    let document = document(&first, first_source, 1);
    let offset = first_source.find("build/app.css").unwrap() + 2;
    let locations = references(
        &AssetReferenceProvider::new(Some(index), None),
        &request(&document, offset, true),
    );
    assert_eq!(locations.len(), 3);
    assert_eq!(locations[0].uri, file_uri(&p(&asset_path)));
    assert_uris_match(
        &locations[1..],
        &[file_uri(&p(&first)), file_uri(&p(&second))],
    );
}

#[test]
fn vite_references_connect_target_config_and_twig_usages() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let target = root.path().join("assets/app.js");
    let config = root.path().join("vite.config.js");
    let first = root.path().join("templates/first.html.twig");
    let second = root.path().join("templates/second.html.twig");
    let first_source = "{{ vite_entry_script_tags('app') }}";
    let index = asset_index(
        root.path(),
        cache.path(),
        &[
            (&target, "console.log('app');"),
            (
                &config,
                "export default defineConfig({\n  build: {rollupOptions: {input: {app: './assets/app.js'}}}\n});",
            ),
            (&first, first_source),
            (&second, "{{ vite_entry_link_tags('app') }}"),
        ],
    );
    let document = document(&first, first_source, 1);
    let offset = first_source.find("app").unwrap() + 1;
    let locations = references(
        &AssetReferenceProvider::new(Some(index), None),
        &request(&document, offset, true),
    );
    assert_uris_match(
        &locations,
        &[&target, &config, &first, &second].map(|path| file_uri(&p(path))),
    );
}

#[test]
fn asset_package_references_connect_config_and_qualified_usages() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let config = root
        .path()
        .join("src/Administration/Resources/config/routes.xml");
    let first = root.path().join("templates/first.html.twig");
    let second = root.path().join("templates/second.html.twig");
    let first_source = "{{ asset('administration/app.js', '@Administration') }}";
    let index = asset_index(
        root.path(),
        cache.path(),
        &[
            (&config, "<routes/>"),
            (&first, first_source),
            (
                &second,
                "{{ asset('administration/other.js', '@Administration') }}",
            ),
        ],
    );
    let document = document(&first, first_source, 1);
    let offset = first_source.find("@Administration").unwrap() + 2;
    let locations = references(
        &AssetReferenceProvider::new(Some(index), None),
        &request(&document, offset, true),
    );
    assert_eq!(locations.len(), 3);
    assert_eq!(locations[0].uri, file_uri(&p(&config)));
    assert_uris_match(
        &locations[1..],
        &[file_uri(&p(&first)), file_uri(&p(&second))],
    );
}

#[test]
fn importmap_references_connect_mapping_target_and_twig_usages() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let importmap = root.path().join("importmap.php");
    let target = root.path().join("assets/app.js");
    let first = root.path().join("templates/first.html.twig");
    let second = root.path().join("templates/second.html.twig");
    let first_source = "{{ importmap('app') }}";
    let index = asset_index(
        root.path(),
        cache.path(),
        &[
            (
                &importmap,
                "<?php\nreturn [\n    'app' => ['path' => './assets/app.js', 'entrypoint' => true],\n];",
            ),
            (&target, "console.log('app')"),
            (&first, first_source),
            (
                &second,
                "{% block javascripts %}{{ importmap('app') }}{% endblock %}",
            ),
        ],
    );
    let document = document(&first, first_source, 1);
    let offset = first_source.find("app").unwrap() + 1;
    let locations = references(
        &AssetReferenceProvider::new(Some(index), None),
        &request(&document, offset, true),
    );
    assert_eq!(locations.len(), 3);
    assert_eq!(locations[0].uri, file_uri(&p(&target)));
    assert_uris_match(
        &locations[1..],
        &[file_uri(&p(&first)), file_uri(&p(&second))],
    );
}
