//! Port of `profiler_catalog_test.go` plus Go-parity helper checks.

use std::io::Write;
use std::path::Path;

use super::*;
use crate::profiler::testing::*;
use crate::profiler_serialized::profiler_twig_components;
use crate::*;

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
    // Typed serialization keeps Go's struct field order.
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

    // The command wire shape: Go field order and omitted empty members.
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
        "invalid profiler request catalog request: json: cannot unmarshal string into Go struct field ProfilerRequestCatalogRequest.limit of type int"
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
fn profiler_go_parity_helpers() {
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

    assert_eq!(go_parse_int("+12"), 12);
    assert_eq!(go_parse_int("-12"), -12);
    assert_eq!(go_parse_int("12x"), 0);
    assert_eq!(go_parse_int("99999999999999999999"), i64::MAX);
    assert_eq!(go_parse_int("-99999999999999999999"), i64::MIN);

    assert_eq!(go_lossy(b"a\xf0\x9f\x98b"), "a\u{FFFD}\u{FFFD}\u{FFFD}b");

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
        crate::profiler::normalized_profiler_base_url(" https://x.test/a/?q=1#f ").unwrap(),
        "https://x.test/a"
    );
    assert_eq!(
        crate::profiler::normalized_profiler_base_url("http://localhost:8000/").unwrap(),
        "http://localhost:8000"
    );
    assert_eq!(
        crate::profiler::normalized_profiler_base_url("https://x.test").unwrap(),
        "https://x.test"
    );
    for (value, message) in [
        ("x.test", "absolute"),
        ("mailto:x@y", "absolute"),
        ("ftp://x.test", "HTTP or HTTPS"),
        ("https://u:p@x.test", "credentials"),
    ] {
        assert!(
            crate::profiler::normalized_profiler_base_url(value)
                .unwrap_err()
                .to_string()
                .contains(message),
            "{value:?}"
        );
    }
    assert!(
        crate::profiler::normalized_profiler_base_url("https://u@x.test")
            .unwrap_err()
            .to_string()
            .contains("credentials")
    );
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
