use std::path::Path;
use std::sync::Arc;

use serde_json::Value;
use swlsp_admin::AdminComponentIndexer;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, InlayHintProvider, InlayHintRequest, TextDocument};
use swlsp_lsp_protocol::{self as protocol, InlayHintKind};
use swlsp_php::PhpIndex;
use swlsp_snippet::SnippetIndexer;
use swlsp_symfony::{RouteIndexer, ServiceIndex};
use swlsp_uriutil::file_uri;

use crate::*;

fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

fn request(document: TextDocument) -> InlayHintRequest {
    let mut params = protocol::InlayHintParams::default();
    params.text_document.uri = document.uri.clone();
    let (line, character) = document
        .line_index()
        .position_utf16(document.source().len() as u32);
    params.range.end = protocol::Position { line, character };
    InlayHintRequest {
        params,
        document: Arc::new(document),
    }
}

fn hints(provider: &dyn InlayHintProvider, request: &InlayHintRequest) -> Vec<protocol::InlayHint> {
    provider
        .get_inlay_hints(&Context::background(), request)
        .unwrap()
}

fn parts(hint: &protocol::InlayHint) -> Vec<protocol::InlayHintLabelPart> {
    match &hint.label {
        protocol::InlayHintLabel::Parts(parts) => parts.clone(),
        protocol::InlayHintLabel::String(_) => panic!("label parts expected"),
    }
}

fn labels(hints: &[protocol::InlayHint]) -> Vec<String> {
    hints
        .iter()
        .map(|hint| match &hint.label {
            protocol::InlayHintLabel::String(label) => label.clone(),
            protocol::InlayHintLabel::Parts(parts) => {
                parts.iter().map(|part| part.value.as_str()).collect()
            }
        })
        .collect()
}

fn php_index(dir: &Path, files: &[(&str, &str)]) -> PhpIndex {
    let php = PhpIndex::open(dir, None).unwrap();
    for (path, source) in files {
        php.index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .unwrap();
    }
    php
}

// -- admin parameters ---------------------------------------------------------

fn admin_index(root: &Path) -> Arc<AdminComponentIndexer> {
    Arc::new(AdminComponentIndexer::new(&p(&root.join("cache")), None).unwrap())
}

#[test]
fn admin_parameter_hints_reuse_component_signatures_in_javascript_and_twig() {
    let root = tempfile::tempdir().unwrap();
    let index = admin_index(root.path());
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    let definition_path = admin_root.join("component/sw-card/index.ts");
    let template_path = admin_root.join("component/sw-card/sw-card.html.twig");
    let definition_source = "import template from './sw-card.html.twig';
Component.register('sw-card', {
    template,
    methods: {
        save(id: string, options?: { force: boolean }): Promise<void> {
            return Promise.resolve();
        },
        submit(id: string, options: { force: boolean }) {
            this.save(id, options);
            this.save(this.product.id, { force: true });
        },
    },
});";
    let template_source = "<button @click=\"save(product.id, { force: true }); $emit('saved', product.id); value | default('fallback')\">Save</button>";
    for (path, source) in [
        (&definition_path, definition_source),
        (&template_path, template_source),
    ] {
        index
            .index(&ParsedFile::new(p(path), source.as_bytes().to_vec()))
            .unwrap();
    }
    let provider = AdminParameterProvider::new(index);
    let javascript = hints(
        &provider,
        &request(TextDocument::new(
            file_uri(&p(&definition_path)),
            definition_source,
            1,
        )),
    );
    assert_eq!(labels(&javascript), vec!["id:", "options:"]);
    assert!(javascript[0].tooltip.contains("save(id: string"));
    assert!(javascript[0].padding_right);

    let twig = hints(
        &provider,
        &request(TextDocument::new(
            file_uri(&p(&template_path)),
            template_source,
            1,
        )),
    );
    assert_eq!(labels(&twig), vec!["id:", "options:", "event:", "args:"]);
    for hint in &twig {
        assert_eq!(hint.kind, Some(InlayHintKind::PARAMETER));
    }

    let mut ranged = request(TextDocument::new(
        file_uri(&p(&template_path)),
        template_source,
        1,
    ));
    let start = template_source.find("{ force").unwrap() as u32;
    let end = start + "{ force: true }".len() as u32;
    let lines = ranged.document.line_index();
    let (start_line, start_character) = lines.position_utf16(start);
    let (end_line, end_character) = lines.position_utf16(end);
    ranged.params.range = protocol::Range {
        start: protocol::Position {
            line: start_line,
            character: start_character,
        },
        end: protocol::Position {
            line: end_line,
            character: end_character,
        },
    };
    assert_eq!(labels(&hints(&provider, &ranged)), vec!["options:"]);
}

#[test]
fn admin_parameter_hints_reuse_pinia_action_signatures() {
    let root = tempfile::tempdir().unwrap();
    let index = admin_index(root.path());
    let path = root
        .path()
        .join("src/Administration/Resources/app/administration/src/store/session.ts");
    let source = "Shopware.Store.register('session', {
    actions: {
        login(user: string, remember?: boolean): Promise<boolean> {
            return Promise.resolve(true);
        },
    },
});
Shopware.Store.get('session').login('admin', true);";
    index
        .index(&ParsedFile::new(p(&path), source.as_bytes().to_vec()))
        .unwrap();
    let result = hints(
        &AdminParameterProvider::new(index),
        &request(TextDocument::new(file_uri(&p(&path)), source, 1)),
    );
    assert_eq!(labels(&result), vec!["user:", "remember:"]);
}

#[test]
fn admin_parameter_hint_skips_ambiguous_overload_names() {
    let parameter = |label: &str| protocol::ParameterInformation {
        label: label.to_string(),
        documentation: None,
    };
    let help = protocol::SignatureHelp {
        signatures: vec![
            protocol::SignatureInformation {
                parameters: vec![parameter("value: string")],
                ..Default::default()
            },
            protocol::SignatureInformation {
                parameters: vec![parameter("item: number")],
                ..Default::default()
            },
        ],
        ..Default::default()
    };
    assert!(crate::admin_parameter::admin_parameter_hint(Some(&help), 0).is_none());
}

// -- PHPUnit ------------------------------------------------------------------

#[test]
fn phpunit_provider_inlay_hints_support_docblocks_and_attributes() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        dir.path(),
        &[(
            "/project/vendor/phpunit/TestCase.php",
            r"<?php namespace PHPUnit\Framework; abstract class TestCase {}",
        )],
    );
    let source = r"<?php
namespace App\Tests;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;

final class ProductTest extends TestCase
{
    /** @dataProvider productCases */
    public function testProduct(string $name, int $stock): void {}

    #[DataProvider('priceCases')]
    public function testPrice(int $gross, int $net): void {}

    public static function productCases(): iterable
    {
        yield ['Hat', 4];
        yield 'empty' => ['Shoe', 0];
    }

    public static function priceCases(): iterable
    {
        yield [119, 100];
    }
}
";
    let result = hints(
        &PhpUnitProviderProvider::new(Some(php)),
        &request(TextDocument::new(
            "file:///project/tests/ProductTest.php",
            source,
            1,
        )),
    );
    assert_eq!(result.len(), 6);
    for hint in &result {
        let parts = parts(hint);
        assert_eq!(parts.len(), 1);
        assert!(parts[0].location.is_some());
        assert_eq!(hint.kind, Some(InlayHintKind::PARAMETER));
    }
    assert_eq!(
        labels(&result),
        vec!["$name:", "$stock:", "$name:", "$stock:", "$gross:", "$net:"]
    );
}

// -- route controllers --------------------------------------------------------

struct ControllerFixture {
    _root: tempfile::TempDir,
    provider: RouteControllerProvider,
}

fn route_controller_provider() -> ControllerFixture {
    let root = tempfile::tempdir().unwrap();
    let class_path = root.path().join("src/ProductController.php");
    let class_source = r"<?php
namespace App\Controller;
class ProductController
{
    public function show(): void {}
    public function __invoke(): void {}
    private function hidden(): void {}
}
";
    std::fs::create_dir_all(class_path.parent().unwrap()).unwrap();
    std::fs::write(&class_path, class_source).unwrap();
    let php = php_index(
        &root.path().join("php-cache"),
        &[(&p(&class_path), class_source)],
    );
    let services =
        ServiceIndex::new(&p(root.path()), root.path().join("symfony-cache"), None).unwrap();
    services
        .index(&ParsedFile::new(
            "/project/config/services.yaml",
            b"services:\n  app.product_controller:\n    class: App\\Controller\\ProductController\n"
                .to_vec(),
        ))
        .unwrap();
    ControllerFixture {
        _root: root,
        provider: RouteControllerProvider::new(Some(Arc::new(services)), Some(php)),
    }
}

#[test]
fn yaml_route_controller_inlay_hints_resolve_class_service_and_invoker() {
    let fixture = route_controller_provider();
    let document = TextDocument::new(
        "file:///project/config/routes.yaml",
        r"product.show:
  path: /products/{id}
  controller: App\Controller\ProductController::show
product.invoke:
  path: /products
  defaults:
    _controller: app.product_controller
",
        1,
    );
    let result = hints(&fixture.provider, &request(document));
    assert_eq!(
        labels(&result),
        vec!["→ ProductController::show", "→ ProductController::__invoke"]
    );
    assert_eq!(result[0].position.line, 2);
    assert_eq!(result[1].position.line, 6);
    assert_eq!(result[0].kind, Some(InlayHintKind::TYPE));
    assert!(result[0].padding_left);
    let parts = parts(&result[0]);
    assert_eq!(parts.len(), 1);
    let location = parts[0].location.as_ref().unwrap();
    assert!(location.uri.starts_with("file"));
    assert_eq!(location.range.start.line, 4);
    assert_eq!(
        result[0].tooltip,
        "Route \"product.show\"\nApp\\Controller\\ProductController::show"
    );
    assert_eq!(
        result[1].tooltip,
        "Route \"product.invoke\"\napp.product_controller → App\\Controller\\ProductController::__invoke"
    );
}

#[test]
fn xml_route_controller_inlay_hints_resolve_attribute_and_default() {
    let fixture = route_controller_provider();
    let document = TextDocument::new(
        "file:///project/config/routes.xml",
        r#"<routes>
  <route id="product.show" path="/products/{id}" controller="app.product_controller:show"/>
  <route id="product.invoke" path="/products">
    <default key="_controller">App\Controller\ProductController</default>
  </route>
</routes>"#,
        1,
    );
    let result = hints(&fixture.provider, &request(document));
    assert_eq!(
        labels(&result),
        vec!["→ ProductController::show", "→ ProductController::__invoke"]
    );
    assert_eq!(result[0].position.line, 1);
    assert_eq!(result[1].position.line, 3);
    assert!(result[0].tooltip.contains("app.product_controller:show → "));
    assert!(result[1].tooltip.contains("Route \"product.invoke\""));
}

#[test]
fn php_route_controller_inlay_hints_resolve_implicit_attribute_controller() {
    let fixture = route_controller_provider();
    let source = r"<?php
namespace App\Controller;

final class ProductController
{
    #[Route('/products/{id}', name: 'product.show')]
    public function show(): void {}

    #[Route('/products', name: 'product.invoke')]
    public function __invoke(): void {}
}
";
    let document = TextDocument::new("file:///project/src/ProductController.php", source, 1);
    let line_length = document.line_index().line_utf16_length(5);
    let result = hints(&fixture.provider, &request(document));
    assert_eq!(
        labels(&result),
        vec!["→ ProductController::show", "→ ProductController::__invoke"]
    );
    assert_eq!(result[0].position.line, 5);
    assert_eq!(result[1].position.line, 8);
    assert_eq!(result[0].position.character, line_length);
}

#[test]
fn route_controller_inlay_hints_respect_range_and_require_public_method() {
    let fixture = route_controller_provider();
    let document = TextDocument::new(
        "file:///project/config/routes.yaml",
        r"product.show:
  path: /products/{id}
  controller: App\Controller\ProductController::show
product.hidden:
  path: /hidden
  controller: App\Controller\ProductController::hidden
product.invoke:
  path: /products
  controller: app.product_controller
",
        1,
    );
    let mut request = request(document);
    request.params.range.start.line = 5;
    request.params.range.end.line = 8;
    request.params.range.end.character = 80;
    let result = hints(&fixture.provider, &request);
    assert_eq!(labels(&result), vec!["→ ProductController::__invoke"]);
    assert_eq!(result[0].position.line, 8);
}

// -- route paths --------------------------------------------------------------

fn route_index(dir: &Path, path: &str, source: &str) -> Arc<RouteIndexer> {
    let routes = RouteIndexer::new(dir, None).unwrap();
    routes
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
    Arc::new(routes)
}

#[test]
fn route_path_inlay_hints_resolve_php_and_twig_route_names() {
    let dir = tempfile::tempdir().unwrap();
    let routes = route_index(
        dir.path(),
        "/project/config/routes.yaml",
        r"product.show:
  path: /products/{id}
  controller: App\Controller\ProductController::show
  methods: [GET, HEAD]
",
    );
    let provider = RoutePathProvider::new(routes, None);
    for document in [
        TextDocument::new(
            "file:///project/src/ProductController.php",
            "<?php $this->redirectToRoute('product.show');",
            1,
        ),
        TextDocument::new(
            "file:///project/templates/product.html.twig",
            "{{ path('product.show', {id: product.id}) }}",
            1,
        ),
    ] {
        let result = hints(&provider, &request(document));
        assert_eq!(result.len(), 1);
        let parts = parts(&result[0]);
        assert_eq!(parts.len(), 1);
        assert_eq!(parts[0].value, "→ /products/{id}");
        assert_eq!(
            parts[0].location.as_ref().unwrap().uri,
            "file:///project/config/routes.yaml"
        );
        assert!(result[0].tooltip.contains("Route \"product.show\""));
        assert!(result[0].tooltip.contains("GET|HEAD /products/{id}"));
        assert!(result[0].padding_left);
    }
}

#[test]
fn route_path_inlay_hints_use_typed_php_generator_receivers() {
    let dir = tempfile::tempdir().unwrap();
    let routes = route_index(
        &dir.path().join("routes"),
        "/project/config/routes.yaml",
        "product.show:\n  path: /products/{id}\n",
    );
    let php = php_index(
        &dir.path().join("php"),
        &[
            (
                "/project/vendor/AbstractController.php",
                r"<?php
namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {
    protected function redirectToRoute(string $route): mixed {}
}
",
            ),
            (
                "/project/vendor/UrlGeneratorInterface.php",
                r"<?php
namespace Symfony\Component\Routing\Generator;
interface UrlGeneratorInterface {
    public function generate(string $name): string;
}
",
            ),
        ],
    );
    let source = r"<?php
namespace App;
use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;
use Symfony\Component\Routing\Generator\UrlGeneratorInterface;
final class Other { public function generate(string $name): string {} }
final class ProductController extends AbstractController {
    public function show(UrlGeneratorInterface $router, Other $other): void {
        $this->redirectToRoute('product.show');
        $router->generate('product.show');
        $other->generate('product.show');
    }
}
";
    let result = hints(
        &RoutePathProvider::new(routes, Some(php)),
        &request(TextDocument::new(
            "file:///project/src/ProductController.php",
            source,
            1,
        )),
    );
    assert_eq!(
        labels(&result),
        vec!["→ /products/{id}", "→ /products/{id}"]
    );
}

#[test]
fn route_path_inlay_hints_ignore_unknown_dynamic_and_unrelated_strings() {
    let dir = tempfile::tempdir().unwrap();
    let routes = route_index(
        dir.path(),
        "/project/config/routes.yaml",
        "known:\n  path: /known\n",
    );
    let result = hints(
        &RoutePathProvider::new(routes, None),
        &request(TextDocument::new(
            "file:///project/templates/page.html.twig",
            "{{ path(routeName) }} {{ path('missing') }} {{ 'known' }}",
            1,
        )),
    );
    assert!(result.is_empty());
}

// -- service arguments --------------------------------------------------------

struct ServiceFixture {
    _dir: tempfile::TempDir,
    provider: ServiceArgumentProvider,
}

fn service_argument_provider() -> ServiceFixture {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        dir.path(),
        &[
            (
                "/project/src/LoggerInterface.php",
                "<?php\nnamespace App;\ninterface LoggerInterface {}\n",
            ),
            (
                "/project/src/Consumer.php",
                r"<?php
namespace App;
class Consumer
{
    public function __construct(
        LoggerInterface $logger,
        string $channel,
    ) {}

    public function setLogger(
        LoggerInterface $logger,
        int $priority,
    ): void {}
}
",
            ),
            (
                "/project/src/ChildConsumer.php",
                "<?php\nnamespace App;\nclass ChildConsumer extends Consumer {}\n",
            ),
        ],
    );
    ServiceFixture {
        _dir: dir,
        provider: ServiceArgumentProvider::new(None, Some(php)),
    }
}

#[test]
fn yaml_service_argument_inlay_hints() {
    let fixture = service_argument_provider();
    let result = hints(
        &fixture.provider,
        &request(TextDocument::new(
            "file:///project/config/services.yaml",
            r"services:
  app.consumer:
    class: App\Consumer
    arguments: ['@app.logger', '%app.channel%']
    calls:
      - [setLogger, ['@app.logger', 10]]
",
            1,
        )),
    );
    assert_eq!(
        labels(&result),
        vec!["LoggerInterface", "channel", "LoggerInterface", "priority"]
    );
    assert_eq!(result[0].tooltip, "$logger: App\\LoggerInterface");
    assert_eq!(result[0].kind, Some(InlayHintKind::PARAMETER));
    assert!(result[0].padding_left);
}

#[test]
fn yaml_service_argument_inlay_hints_resolve_named_and_inherited_parameters() {
    let fixture = service_argument_provider();
    let result = hints(
        &fixture.provider,
        &request(TextDocument::new(
            "file:///project/config/services.yaml",
            r"services:
  App\ChildConsumer:
    arguments:
      $channel: storefront
      $logger: '@app.logger'
",
            1,
        )),
    );
    assert_eq!(labels(&result), vec!["channel", "LoggerInterface"]);
}

#[test]
fn xml_service_argument_inlay_hints() {
    let fixture = service_argument_provider();
    let result = hints(
        &fixture.provider,
        &request(TextDocument::new(
            "file:///project/config/services.xml",
            r#"<container>
  <services>
    <service id="app.consumer" class="App\Consumer">
      <argument type="service" id="app.logger"/>
      <argument>%app.channel%</argument>
      <call method="setLogger">
        <argument type="service" id="app.logger"/>
        <argument>10</argument>
      </call>
    </service>
  </services>
</container>"#,
            1,
        )),
    );
    assert_eq!(
        labels(&result),
        vec!["LoggerInterface", "channel", "LoggerInterface", "priority"]
    );
    for hint in &result {
        assert!(hint.position.line > 2);
    }
}

#[test]
fn service_argument_inlay_hints_respect_requested_range() {
    let fixture = service_argument_provider();
    let mut request = request(TextDocument::new(
        "file:///project/config/services.yaml",
        r"services:
  app.consumer:
    class: App\Consumer
    arguments:
      - '@app.logger'
      - '%app.channel%'
",
        1,
    ));
    request.params.range.start.line = 5;
    request.params.range.end.line = 5;
    request.params.range.end.character = 80;
    let result = hints(&fixture.provider, &request);
    assert_eq!(labels(&result), vec!["channel"]);
}

// -- snippets -----------------------------------------------------------------

#[test]
fn snippet_preview_inlay_hints_prefer_english_and_navigate() {
    let dir = tempfile::tempdir().unwrap();
    let index = SnippetIndexer::new(dir.path(), None).unwrap();
    for (path, source) in [
        (
            "/project/src/Resources/snippet/de-DE/messages.json",
            r#"{"checkout":{"finish":"Bestellung abschliessen"}}"#,
        ),
        (
            "/project/src/Resources/snippet/en-GB/messages.json",
            r#"{"checkout":{"finish":"Complete order"}}"#,
        ),
        (
            "/project/src/Resources/app/administration/src/module/snippet/en-GB.json",
            r#"{"admin":{"title":"Administration title"}}"#,
        ),
    ] {
        index
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }
    let provider = SnippetPreviewProvider::new(Arc::new(index));
    for (name, uri, source) in [
        (
            "twig frontend",
            "file:///project/templates/checkout.html.twig",
            "{{ 'checkout.finish'|trans }}",
        ),
        (
            "twig admin",
            "file:///project/src/Resources/app/administration/card.html.twig",
            "{{ $tc('admin.title') }}",
        ),
        (
            "twig admin bound attribute",
            "file:///project/src/Resources/app/administration/card.html.twig",
            "<mt-card :title=\"$t('admin.title')\" />",
        ),
        (
            "javascript admin",
            "file:///project/src/Resources/app/administration/card.js",
            "this.$tc('admin.title');",
        ),
        (
            "javascript injected translator",
            "file:///project/src/Resources/app/administration/service.ts",
            "translator.$t('admin.title');",
        ),
        (
            "javascript snippet service",
            "file:///project/src/Resources/app/administration/service.js",
            "Shopware.Snippet.tc('admin.title');",
        ),
        (
            "javascript module metadata",
            "file:///project/src/Resources/app/administration/module.js",
            "Module.register('demo', { title: 'admin.title' });",
        ),
    ] {
        let result = hints(&provider, &request(TextDocument::new(uri, source, 1)));
        assert_eq!(result.len(), 1, "{name}");
        let parts = parts(&result[0]);
        assert_eq!(parts.len(), 1, "{name}");
        let location = parts[0].location.as_ref().expect(name);
        if name == "twig frontend" {
            assert_eq!(parts[0].value, "→ Complete order");
            assert!(location.uri.ends_with("/messages.json"));
        } else {
            assert_eq!(parts[0].value, "→ Administration title", "{name}");
        }
        assert!(result[0].padding_left);
    }
}

// -- twig variables -----------------------------------------------------------

#[test]
fn twig_variable_inlay_hint_summarizes_controller_context() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        dir.path(),
        &[
            (
                "/project/src/Product.php",
                "<?php\nnamespace App;\nclass Product {}\n",
            ),
            (
                "/project/src/ProductController.php",
                r"<?php
namespace App;
class ProductController
{
    public function show(Product $product): mixed
    {
        return $this->render('product/show.html.twig', [
            'product' => $product,
            'count' => 1,
        ]);
    }
}
",
            ),
        ],
    );
    let result = hints(
        &TwigVariableProvider::new(Some(php)),
        &request(TextDocument::new(
            "file:///project/templates/product/show.html.twig",
            "{{ product }}",
            1,
        )),
    );
    assert_eq!(result.len(), 1);
    assert_eq!(labels(&result), vec!["Variables (2)"]);
    assert!(result[0].tooltip.contains("count: 1"));
    assert!(result[0].tooltip.contains("product: App\\Product"));
    assert!(result[0].padding_right);
    let parts = parts(&result[0]);
    assert_eq!(parts.len(), 1);
    assert_eq!(
        parts[0].tooltip,
        "Browse typed Twig variables and insert expressions"
    );
    let command = parts[0].command.as_ref().unwrap();
    assert_eq!(command.command, BROWSE_TWIG_VARIABLES_COMMAND);
    assert_eq!(command.title, "Browse Twig variables");
    assert_eq!(
        command.arguments,
        vec![
            Value::from("file:///project/templates/product/show.html.twig"),
            serde_json::json!(["count", "product"]),
        ]
    );
}

#[test]
fn twig_variable_inlay_hint_only_appears_at_document_top() {
    let dir = tempfile::tempdir().unwrap();
    let php = php_index(
        dir.path(),
        &[(
            "/project/src/Controller.php",
            "<?php\nfunction page() {\n    return render('page.html.twig', ['value' => 1]);\n}\n",
        )],
    );
    let mut request = request(TextDocument::new(
        "file:///project/templates/page.html.twig",
        "\n{{ value }}",
        1,
    ));
    request.params.range.start.line = 1;
    assert!(hints(&TwigVariableProvider::new(Some(php)), &request).is_empty());
}

#[test]
fn php_index_backed_hints_are_empty_without_php_index() {
    let php_request = request(TextDocument::new(
        "file:///project/tests/ProductTest.php",
        "<?php\nfinal class ProductTest extends \\PHPUnit\\Framework\\TestCase {}\n",
        1,
    ));
    assert!(hints(&PhpUnitProviderProvider::new(None), &php_request).is_empty());
    let twig_request = request(TextDocument::new(
        "file:///project/templates/product.html.twig",
        "{{ product }}",
        1,
    ));
    assert!(hints(&TwigVariableProvider::new(None), &twig_request).is_empty());
}
