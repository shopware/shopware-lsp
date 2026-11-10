use super::*;
use crate::analytics::profiler::testing::*;
use crate::analytics::profiler_serialized::profiler_twig_components;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::Context;
use swlsp_php::PhpIndex;
use swlsp_symfony::{RouteIndexer, ServiceIndex};
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::file_uri;

fn route_entry<'a>(entries: &'a [RouteCatalogEntry], name: &str) -> &'a RouteCatalogEntry {
    entries
        .iter()
        .find(|entry| entry.name == name)
        .unwrap_or_else(|| panic!("route catalog entry {name:?} not found in {entries:#?}"))
}

fn service_entry<'a>(entries: &'a [ServiceLocatorEntry], id: &str) -> &'a ServiceLocatorEntry {
    entries
        .iter()
        .find(|entry| entry.id == id)
        .unwrap_or_else(|| panic!("service {id:?} not found in {entries:#?}"))
}

pub(crate) fn ctx() -> Context {
    Context::background()
}

pub(crate) fn path_string(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

/// Writes `source` below `root` and returns the absolute path.
pub(crate) fn write_fixture(root: &Path, relative: &str, source: &str) -> String {
    let path: PathBuf = root.join(relative);
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(&path, source).unwrap();
    path_string(&path)
}

pub(crate) fn parsed(path: &str) -> ParsedFile {
    ParsedFile::new(path, std::fs::read(path).unwrap())
}

pub(crate) fn uri(path: &str) -> String {
    file_uri(path)
}

pub(crate) fn open_php(cache: &Path) -> PhpIndex {
    PhpIndex::open(cache, None).unwrap()
}

pub(crate) fn open_twig(cache: &Path) -> Arc<TwigIndexer> {
    Arc::new(TwigIndexer::new(cache, None).unwrap())
}

pub(crate) fn open_routes(cache: &Path) -> Arc<RouteIndexer> {
    Arc::new(RouteIndexer::new(cache, None).unwrap())
}

pub(crate) fn open_services(root: &Path, cache: &Path, php: &PhpIndex) -> Arc<ServiceIndex> {
    let services = Arc::new(ServiceIndex::new(&path_string(root), cache, None).unwrap());
    services.set_php_index(Some(php.clone()));
    services
}

pub(crate) fn close(indexer: &dyn Indexer) {
    indexer.close().unwrap();
}

/// Parses an expected wire response for comparison with a command result.
pub(crate) fn json_value(text: &str) -> serde_json::Value {
    serde_json::from_str(text).unwrap()
}

fn profiler_fixture_content(controller: &str, route: &str, templates: &[&str]) -> Vec<u8> {
    let mut content = String::new();
    content.push_str(&profiler_serialized_fixture("_controller", controller));
    content.push_str(&profiler_serialized_fixture("_route", route));
    content.push_str(&format!("\"template_paths\"a:{}:{{", templates.len()));
    for template in templates {
        let path = format!("/templates/{template}");
        content.push_str(&format!(
            "s:{}:\"{template}\";s:{}:\"{path}\";",
            template.len(),
            path.len()
        ));
    }
    content.push('}');
    content.push('\0');
    content.into_bytes()
}

fn profiler_serialized_fixture(key: &str, value: &str) -> String {
    format!("\"{key}\";s:{}:\"{value}\";", value.len())
}

fn profiler_mail_fixture(title: &str) -> Vec<u8> {
    format!(
        "s:50:\"\0Symfony\\Component\\Mime\\Header\\AbstractHeader\0name\";s:7:\"Subject\";s:55:\"\0Symfony\\Component\\Mime\\Header\\UnstructuredHeader\0value\";s:{}:\"{title}\";}}",
        title.len()
    )
    .into_bytes()
}

fn php_serialized_string(value: &str) -> String {
    format!("s:{}:\"{value}\";", value.len())
}

fn profiler_twig_component_fixture() -> Vec<u8> {
    let component = |name: &str, class: &str, template: &str, render_count: i64| {
        format!(
            "a:4:{{{}{}{}{}{}{}s:12:\"render_count\";i:{render_count};}}",
            php_serialized_string("name"),
            php_serialized_string(name),
            php_serialized_string("class"),
            php_serialized_string(class),
            php_serialized_string("template"),
            php_serialized_string(template),
        )
    };
    format!(
        "a:1:{{s:14:\"twig_component\";O:1:\"C\":1:{{s:4:\"data\";O:1:\"D\":1:{{s:4:\"data\";a:5:{{i:0;a:1:{{s:10:\"components\";a:1:{{i:1;i:2;}}}}i:1;a:0:{{}}i:2;a:2:{{s:5:\"Alert\";a:1:{{i:1;i:3;}}s:9:\"Anon:Card\";a:1:{{i:1;i:4;}}}}i:3;{}i:4;{}}}}}}}}}",
        component(
            "Alert",
            r"App\Twig\Components\Alert",
            "components/alert.html.twig",
            3
        ),
        component(
            "Anon:Card",
            r"Symfony\UX\TwigComponent\AnonymousComponent",
            "components/anonymous.html.twig",
            5
        ),
    )
    .into_bytes()
}

fn write_profiler_fixture(directory: &Path, hash: &str, content: &[u8], compressed: bool) {
    let path = directory.join(&hash[4..6]).join(&hash[2..4]).join(hash);
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    if !compressed {
        std::fs::write(&path, content).unwrap();
        return;
    }
    let file = std::fs::File::create(&path).unwrap();
    let mut writer = flate2::write::GzEncoder::new(file, flate2::Compression::default());
    writer.write_all(content).unwrap();
    writer.finish().unwrap();
}

#[test]
fn route_catalog_resolves_controllers_templates_and_filters() {
    let root_dir = tempfile::tempdir().unwrap();
    let cache_dir = tempfile::tempdir().unwrap();
    let (root, cache) = (root_dir.path(), cache_dir.path());
    let php = open_php(cache);
    let routes = open_routes(cache);
    let services = open_services(root, cache, &php);
    let twig = open_twig(cache);

    let controller_path = write_fixture(
        root,
        "src/Controller/ProductController.php",
        r"<?php
namespace App\Controller;

final class ProductController
{
    #[Template('product/attribute.html.twig')]
    #[Route('/products/{id}', name: 'product.show', methods: [Request::METHOD_GET])]
    public function show(): array
    {
        return $this->render('product/show.html.twig');
    }
}
",
    );
    let controller_file = parsed(&controller_path);
    php.index(&controller_file).unwrap();
    routes.index(&controller_file).unwrap();
    twig.index(&controller_file).unwrap();

    let service_path = path_string(&root.join("config/services.yaml"));
    let service_file = ParsedFile::new(
        service_path,
        b"services:\n  app.product_controller:\n    class: App\\Controller\\ProductController\n"
            .to_vec(),
    );
    let route_file = ParsedFile::new(
        path_string(&root.join("config/routes.yaml")),
        b"product.service:\n  path: /service/products/{id}\n  methods: [POST]\n  controller: app.product_controller:show\n".to_vec(),
    );
    services.index(&service_file).unwrap();
    routes.index(&route_file).unwrap();

    let provider = RouteCatalogProvider::new(
        &path_string(root),
        Some(routes.clone()),
        Some(services.clone()),
        Some(php.clone()),
        Some(twig.clone()),
    );
    let entries = provider
        .catalog(&ctx(), &RouteCatalogRequest::default())
        .unwrap();
    assert_eq!(entries.len(), 2);
    let direct = route_entry(&entries, "product.show");
    assert_eq!(direct.path, "/products/{id}");
    assert_eq!(direct.methods, ["GET"]);
    assert_eq!(
        direct.resolved_controller,
        r"App\Controller\ProductController::show"
    );
    assert_eq!(direct.source_uri, uri(&controller_path));
    assert_eq!(direct.source_line, 7);
    assert_eq!(direct.controller_uri, uri(&controller_path));
    assert_eq!(direct.controller_line, 8);
    assert_eq!(
        direct.templates,
        ["product/attribute.html.twig", "product/show.html.twig"]
    );

    let service = route_entry(&entries, "product.service");
    assert_eq!(service.methods, ["POST"]);
    assert_eq!(
        service.resolved_controller,
        r"App\Controller\ProductController::show"
    );
    assert_eq!(
        service.templates,
        ["product/attribute.html.twig", "product/show.html.twig"]
    );

    for request in [
        RouteCatalogRequest {
            url_path: "https://shop.test/products/42?preview=1".to_owned(),
            ..RouteCatalogRequest::default()
        },
        RouteCatalogRequest {
            controller: "ProductController::SHOW".to_owned(),
            ..RouteCatalogRequest::default()
        },
        RouteCatalogRequest {
            file_glob: "src/**/ProductController.php".to_owned(),
            ..RouteCatalogRequest::default()
        },
    ] {
        assert!(!provider.catalog(&ctx(), &request).unwrap().is_empty());
    }
    let filtered = provider
        .catalog(
            &ctx(),
            &RouteCatalogRequest {
                route_name: "PRODUCT.SERVICE".to_owned(),
                file_glob: "src/Controller/*.php".to_owned(),
                ..RouteCatalogRequest::default()
            },
        )
        .unwrap();
    assert_eq!(filtered.len(), 1);
    assert_eq!(filtered[0].name, "product.service");

    let commands = swlsp_lsp::CommandProvider::commands(&provider, &ctx());
    let value = commands[LIST_ROUTES_COMMAND](
        &ctx(),
        Some(&serde_json::json!({"RouteName": "product.show"})),
    )
    .unwrap();
    assert_eq!(
        value,
        json_value(&format!(
            r#"[{{"name":"product.show","path":"/products/{{id}}","methods":["GET"],"controller":"App\\Controller\\ProductController::show","resolvedController":"App\\Controller\\ProductController::show","sourceUri":"{0}","sourceLine":7,"controllerUri":"{0}","controllerLine":8,"templates":["product/attribute.html.twig","product/show.html.twig"]}}]"#,
            uri(&controller_path)
        ))
    );
}

#[test]
fn service_locator_finds_ids_classes_aliases_prototypes_and_cache() {
    let root_dir = tempfile::tempdir().unwrap();
    let root = root_dir.path();
    let cache = root.join(".cache");
    let class_path = write_fixture(
        root,
        "src/Services.php",
        "<?php\nnamespace App;\n\nclass Service {}\nclass Other {}\n",
    );
    let config_path = write_fixture(
        root,
        "config/services.yaml",
        r"services:
  _defaults:
    autowire: true

  'App\':
    resource: '../src'

  app.explicit:
    class: App\Service
    tags: [app.catalog]

  app.alias: '@app.explicit'
",
    );
    let class_request = ServiceLocatorRequest {
        identifier: r"\App\Service".to_owned(),
    };
    let entries = {
        let php = open_php(&cache);
        php.index(&parsed(&class_path)).unwrap();
        let services = open_services(root, &cache, &php);
        services.index(&parsed(&config_path)).unwrap();
        let provider = ServiceLocatorProvider::new(Some(services.clone()), Some(php.clone()));
        let entries = provider.locate(&ctx(), &class_request).unwrap();
        assert_eq!(entries.len(), 3);

        let explicit = service_entry(&entries, "app.explicit");
        assert_eq!(explicit.class_name, r"App\Service");
        assert_eq!(explicit.resolved_class, r"App\Service");
        assert!(explicit.autowire);
        assert!(explicit.autowire_configured);
        assert_eq!(explicit.tags, ["app.catalog"]);
        assert_eq!(explicit.class_file_uri, uri(&class_path));
        assert!(explicit.class_line > 0);
        assert_eq!(explicit.definitions.len(), 1);
        let definition = &explicit.definitions[0];
        assert_eq!(definition.source, "explicit");
        assert_eq!(definition.file_uri, uri(&config_path));
        assert!(definition.source_line > 0);
        assert!(definition.end_line >= definition.source_line);
        assert!(definition.preview.contains("app.explicit:"));
        assert!(definition.preview.contains("tags:"));

        let alias = service_entry(&entries, "app.alias");
        assert_eq!(alias.alias_target, "app.explicit");
        assert_eq!(alias.resolved_class, r"App\Service");
        assert_eq!(alias.definitions.len(), 1);
        assert_eq!(alias.definitions[0].source, "explicit");
        assert!(alias.definitions[0].preview.contains("app.alias:"));

        let prototype = service_entry(&entries, r"App\Service");
        assert_eq!(prototype.resolved_class, r"App\Service");
        assert_eq!(prototype.definitions.len(), 1);
        assert_eq!(prototype.definitions[0].source, "prototype");
        assert!(prototype.definitions[0].preview.contains(r"'App\':"));
        assert!(prototype.definitions[0].preview.contains("resource:"));

        let by_id = provider
            .locate(
                &ctx(),
                &ServiceLocatorRequest {
                    identifier: "app.alias".to_owned(),
                },
            )
            .unwrap();
        assert_eq!(by_id, std::slice::from_ref(alias));

        let error = provider
            .locate(
                &ctx(),
                &ServiceLocatorRequest {
                    identifier: "app.missing".to_owned(),
                },
            )
            .unwrap_err();
        assert_eq!(error.to_string(), r#"service "app.missing" was not found"#);

        let commands = swlsp_lsp::CommandProvider::commands(&provider, &ctx());
        let error = commands[LOCATE_SERVICES_COMMAND](&ctx(), None).unwrap_err();
        assert_eq!(error.to_string(), "service identifier is required");

        close(services.as_ref());
        close(&php);
        entries
    };
    let php = open_php(&cache);
    let services = open_services(root, &cache, &php);
    let restored = ServiceLocatorProvider::new(Some(services.clone()), Some(php.clone()))
        .locate(&ctx(), &class_request)
        .unwrap();
    assert_eq!(entries, restored);
    close(services.as_ref());
    close(&php);
}

#[test]
fn profiler_catalog_reads_local_profiles_filters_and_resolves_controllers() {
    let root_dir = tempfile::tempdir().unwrap();
    let cache_dir = tempfile::tempdir().unwrap();
    let root = root_dir.path();
    let cache = cache_dir.path();
    let php = open_php(cache);
    let twig = open_twig(cache);

    let controller_source = r"<?php
namespace App\Controller;

final class ProductController
{
    #[Template('product/attribute.html.twig')]
    public function show(): array
    {
        return $this->render('product/show.html.twig');
    }
}
";
    let component_source = r"<?php
namespace App\Twig\Components;

final class Alert {}
";
    let controller_path = write_fixture(
        root,
        "src/Controller/ProductController.php",
        controller_source,
    );
    let component_path = write_fixture(root, "src/Twig/Components/Alert.php", component_source);
    let component_template_path = write_fixture(
        root,
        "templates/components/anonymous.html.twig",
        "<article>Anonymous</article>\n",
    );
    php.index(&parsed(&controller_path)).unwrap();
    twig.index(&parsed(&controller_path)).unwrap();
    php.index(&parsed(&component_path)).unwrap();
    twig.index(&parsed(&component_template_path)).unwrap();

    let profiler_directory = root.join("var/cache/dev/profiler");
    std::fs::create_dir_all(&profiler_directory).unwrap();
    let index_path = path_string(&profiler_directory.join("index.csv"));
    std::fs::write(
        &index_path,
        [
            "18e6b8,127.0.0.1,GET,http://localhost/app_dev.php/products/1,1700000000,,200",
            "a1b2c3,127.0.0.1,POST,https://shop.test/products/2,1800000000,,404",
            r#"broken,"unterminated"#,
            "abcdef,127.0.0.1,GET,relative-url,1600000000,,204",
        ]
        .join("\n"),
    )
    .unwrap();
    let mut raw_content = profiler_fixture_content(
        r"App\Controller\ProductController::show",
        "product.show",
        &[
            "@WebProfiler/Toolbar/toolbar.html.twig",
            "product/show.html.twig",
            "layout.html.twig",
            "ignored-fourth.html.twig",
        ],
    );
    raw_content.extend(profiler_mail_fixture("Time for Symfony Mailer!"));
    raw_content.extend(profiler_twig_component_fixture());
    write_profiler_fixture(&profiler_directory, "18e6b8", &raw_content, false);
    write_profiler_fixture(&profiler_directory, "a1b2c3", &raw_content, true);
    write_profiler_fixture(&profiler_directory, "abcdef", &[0x1f, 0x8b, 0x08], false);

    let provider =
        ProfilerCatalogProvider::new(&path_string(root), Some(php.clone()), Some(twig.clone()));
    let entries = provider
        .catalog(&ctx(), &ProfilerRequestCatalogRequest::default())
        .unwrap();
    assert_eq!(entries.len(), 3);
    let newest = &entries[0];
    assert_eq!(newest.hash, "a1b2c3");
    assert_eq!(newest.method, "POST");
    assert_eq!(newest.status_code, 404);
    assert_eq!(newest.timestamp, 1_800_000_000);
    assert_eq!(newest.profiler_url, "https://shop.test/_profiler/a1b2c3");
    assert_eq!(newest.controller, r"App\Controller\ProductController::show");
    assert_eq!(newest.route, "product.show");
    assert_eq!(newest.entry_view, "product/show.html.twig");
    assert_eq!(
        newest.static_templates,
        ["product/attribute.html.twig", "product/show.html.twig"]
    );
    assert_eq!(
        newest.rendered_templates,
        [
            "@WebProfiler/Toolbar/toolbar.html.twig",
            "product/show.html.twig",
            "layout.html.twig",
        ]
    );
    assert_eq!(
        newest.mail_messages,
        [ProfilerMailMessage {
            title: "Time for Symfony Mailer!".to_owned(),
            panel: "mailer".to_owned(),
        }]
    );
    assert_eq!(
        newest.twig_components,
        [
            ProfilerRuntimeTwigComponent {
                name: "Anon:Card".to_owned(),
                class: r"Symfony\UX\TwigComponent\AnonymousComponent".to_owned(),
                template: "components/anonymous.html.twig".to_owned(),
                render_count: 5,
                file_uri: uri(&component_template_path),
                source_line: 1,
            },
            ProfilerRuntimeTwigComponent {
                name: "Alert".to_owned(),
                class: r"App\Twig\Components\Alert".to_owned(),
                template: "components/alert.html.twig".to_owned(),
                render_count: 3,
                file_uri: uri(&component_path),
                source_line: 4,
            },
        ]
    );
    assert_eq!(newest.controller_file_uri, uri(&controller_path));
    assert_eq!(newest.controller_line, 7);
    assert_eq!(newest.index_file_uri, uri(&index_path));
    assert_eq!(
        entries[1].profiler_url,
        "http://localhost/app_dev.php/_profiler/18e6b8"
    );
    assert_eq!(entries[2].profiler_url, "_profiler/abcdef");
    assert!(entries[2].controller.is_empty());

    let filtered = provider
        .catalog(
            &ctx(),
            &ProfilerRequestCatalogRequest {
                url: "PRODUCTS/1".to_owned(),
                hash: "18E6".to_owned(),
                controller: "productcontroller".to_owned(),
                route: "PRODUCT.SHOW".to_owned(),
                limit: 1,
                base_url: "https://debug.example.test/app_dev.php/".to_owned(),
                ..ProfilerRequestCatalogRequest::default()
            },
        )
        .unwrap();
    assert_eq!(filtered.len(), 1);
    assert_eq!(filtered[0].hash, "18e6b8");
    assert_eq!(
        filtered[0].profiler_url,
        "https://debug.example.test/app_dev.php/_profiler/18e6b8"
    );

    let relative_override = provider
        .catalog(
            &ctx(),
            &ProfilerRequestCatalogRequest {
                index_path: "var/cache/dev/profiler/index.csv".to_owned(),
                hash: "abcdef".to_owned(),
                ..ProfilerRequestCatalogRequest::default()
            },
        )
        .unwrap();
    assert_eq!(relative_override.len(), 1);
    // Typed serialization keeps the declared field order.
    assert_eq!(
        serde_json::to_string(&relative_override).unwrap(),
        format!(
            r#"[{{"hash":"abcdef","method":"GET","url":"relative-url","statusCode":204,"timestamp":1600000000,"profilerUrl":"_profiler/abcdef","indexFileUri":"{}"}}]"#,
            uri(&index_path)
        )
    );

    let error = provider
        .catalog(
            &ctx(),
            &ProfilerRequestCatalogRequest {
                index_path: "../outside/index.csv".to_owned(),
                ..ProfilerRequestCatalogRequest::default()
            },
        )
        .unwrap_err();
    assert!(error.to_string().contains("outside workspace"), "{error}");
    let error = provider
        .catalog(
            &ctx(),
            &ProfilerRequestCatalogRequest {
                base_url: "file:///tmp/profiler".to_owned(),
                ..ProfilerRequestCatalogRequest::default()
            },
        )
        .unwrap_err();
    assert!(error.to_string().contains("HTTP(S)"), "{error}");

    // The command wire shape: declared field order and omitted empty members.
    let commands = swlsp_lsp::CommandProvider::commands(&provider, &ctx());
    let value = commands[LIST_PROFILER_REQUESTS_COMMAND](
        &ctx(),
        Some(&serde_json::json!({"HASH": "abcdef", "limit": null})),
    )
    .unwrap();
    assert_eq!(
        value,
        json_value(&format!(
            r#"[{{"hash":"abcdef","method":"GET","url":"relative-url","statusCode":204,"timestamp":1600000000,"profilerUrl":"_profiler/abcdef","indexFileUri":"{}"}}]"#,
            uri(&index_path)
        ))
    );
    let error =
        commands[LIST_PROFILER_REQUESTS_COMMAND](&ctx(), Some(&serde_json::json!({"limit": "1"})))
            .unwrap_err();
    assert_eq!(
        error.to_string(),
        "invalid profiler request catalog request: member \"limit\" must be an integer, found string"
    );
}

#[test]
fn profiler_catalog_reports_missing_or_empty_indexes() {
    let root_dir = tempfile::tempdir().unwrap();
    let root = root_dir.path();
    let provider = ProfilerCatalogProvider::new(&path_string(root), None, None);
    let error = provider
        .catalog(&ctx(), &ProfilerRequestCatalogRequest::default())
        .unwrap_err();
    assert!(
        error
            .to_string()
            .contains("no local Symfony profiler index"),
        "{error}"
    );
    write_fixture(root, "var/cache/test/profiler/index.csv", "invalid\n");
    let error = provider
        .catalog(&ctx(), &ProfilerRequestCatalogRequest::default())
        .unwrap_err();
    assert!(
        error
            .to_string()
            .contains("no profiler requests were found"),
        "{error}"
    );
}

#[test]
fn profiler_parsing_helpers_use_serialized_byte_lengths_and_safe_urls() {
    let controller = r"App\Controller\ÜnicodeController::show";
    let content = profiler_serialized_fixture("_controller", controller);
    assert_eq!(
        profiler_serialized_string(content.as_bytes(), "_controller"),
        controller
    );
    assert!(
        profiler_serialized_string(br#""_controller";s:999:"short";"#, "_controller").is_empty()
    );
    assert_eq!(
        profiler_url(
            "",
            "https://shop.test/index.php/products?preview=1",
            "abcdef"
        ),
        "https://shop.test/index.php/_profiler/abcdef"
    );
    assert_eq!(profiler_url("", "not a URL", "abcdef"), "_profiler/abcdef");
    let mut mails = profiler_mail_fixture("Order confirmed ✓");
    mails.extend(profiler_mail_fixture("Order confirmed ✓"));
    mails.extend_from_slice(
        b"AbstractHeader\x00name\";s:7:\"Subject\";UnstructuredHeader\x00value\";s:999:\"broken\";",
    );
    assert_eq!(
        profiler_mail_messages(&mails),
        [ProfilerMailMessage {
            title: "Order confirmed ✓".to_owned(),
            panel: "mailer".to_owned(),
        }]
    );
    assert_eq!(
        profiler_twig_components(&profiler_twig_component_fixture()).len(),
        2
    );
}

#[test]
fn profiler_parsing_helpers() {
    assert_eq!(
        parse_csv_record(r#"a,"b ""c""",d"#),
        Some(vec!["a".into(), r#"b "c""#.into(), "d".into()])
    );
    assert_eq!(parse_csv_record(r#"a,"b"#), None);
    assert_eq!(parse_csv_record(r#"a,b"c"#), None);
    assert_eq!(parse_csv_record(r#""a"x,b"#), None);
    assert_eq!(
        parse_csv_record("a,,"),
        Some(vec!["a".into(), "".into(), "".into()])
    );
    assert_eq!(parse_csv_record(r#""a""#), Some(vec!["a".into()]));

    assert_eq!(parse_decimal_saturating("+12"), 12);
    assert_eq!(parse_decimal_saturating("-12"), -12);
    assert_eq!(parse_decimal_saturating("12x"), 0);
    assert_eq!(parse_decimal_saturating("99999999999999999999"), i64::MAX);
    assert_eq!(parse_decimal_saturating("-99999999999999999999"), i64::MIN);

    assert_eq!(
        utf8_lossy_per_byte(b"a\xf0\x9f\x98b"),
        "a\u{FFFD}\u{FFFD}\u{FFFD}b"
    );

    assert_eq!(
        profiler_url(
            "",
            "https://user:pa%20ss@Shop.test:8080/app_test.php?x#y",
            "abc123"
        ),
        "https://user:pa%20ss@shop.test:8080/app_test.php/_profiler/abc123"
    );
    assert_eq!(
        profiler_url("", "http://shop.test/a b/index.php/x?", "abc123"),
        "http://shop.test/a%20b/index.php/_profiler/abc123"
    );
    assert_eq!(
        profiler_url("", "https://shop.test/de/product?x=1#y", "abc123"),
        "https://shop.test/_profiler/abc123"
    );
    assert_eq!(
        profiler_url("", "ftp://shop.test/x", "abc123"),
        "_profiler/abc123"
    );
    assert_eq!(
        profiler_url("", "http://a:b:c/x", "abc123"),
        "_profiler/abc123"
    );
    assert_eq!(
        profiler_url("", "http://[::1]:80/x", "abc123"),
        "http://[::1]/_profiler/abc123"
    );
    assert_eq!(
        profiler_url("", "http://[::1]:8000/x", "abc123"),
        "http://[::1]:8000/_profiler/abc123"
    );
    assert_eq!(
        profiler_url("", "http://[1.2.3.4]/x", "abc123"),
        "_profiler/abc123"
    );
    assert_eq!(
        crate::analytics::profiler::normalized_profiler_base_url(" https://x.test/a/?q=1#f ")
            .unwrap(),
        "https://x.test/a"
    );
    assert_eq!(
        crate::analytics::profiler::normalized_profiler_base_url("http://localhost:8000/").unwrap(),
        "http://localhost:8000"
    );
    assert_eq!(
        crate::analytics::profiler::normalized_profiler_base_url("https://x.test").unwrap(),
        "https://x.test"
    );
    for (value, message) in [
        ("x.test", "absolute"),
        ("mailto:x@y", "absolute"),
        ("ftp://x.test", "HTTP or HTTPS"),
        ("https://u:p@x.test", "credentials"),
    ] {
        assert!(
            crate::analytics::profiler::normalized_profiler_base_url(value)
                .unwrap_err()
                .to_string()
                .contains(message),
            "{value:?}"
        );
    }
    assert!(
        crate::analytics::profiler::normalized_profiler_base_url("https://u@x.test")
            .unwrap_err()
            .to_string()
            .contains("credentials")
    );
}
