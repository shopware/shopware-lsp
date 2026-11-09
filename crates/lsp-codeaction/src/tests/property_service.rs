//! "Add Property Service" action tests.

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context, TextDocument};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_php::PhpIndex;
use swlsp_symfony::ServiceIndex;

use super::{PropertyServiceCodeActionProvider, property_service_candidates};
use crate::test_support::{apply_code_action_edit, path_string, request_at};

const URI: &str = "file:///project/src/Service/Consumer.php";

struct Fixture {
    _dirs: Vec<tempfile::TempDir>,
    provider: PropertyServiceCodeActionProvider,
    php_index: PhpIndex,
}

fn fixture() -> Fixture {
    let root = tempfile::tempdir().unwrap();
    let php_cache = tempfile::tempdir().unwrap();
    let service_cache = tempfile::tempdir().unwrap();
    let php_index = PhpIndex::open(php_cache.path(), None).unwrap();
    for (path, source) in [
        (
            "/project/src/Repository/ProductRepository.php",
            r"<?php
namespace App\Repository;
class ProductRepository
{
    public function findAll(): array { return []; }
}
",
        ),
        (
            "/project/src/Service/BaseConsumer.php",
            r"<?php
namespace App\Service;
class BaseConsumer
{
    protected \App\Repository\ProductRepository $productRepository;
}
",
        ),
        (
            "/project/src/Service/Factory.php",
            r"<?php
namespace App\Service;
class FoobarLongClassNameServiceFactory {}
",
        ),
        (
            "/vendor/Psr/Log/LoggerInterface.php",
            r"<?php
namespace Psr\Log;
interface LoggerInterface
{
    public function info(string $message): void;
}
",
        ),
        (
            "/vendor/Symfony/UrlGeneratorInterface.php",
            r"<?php
namespace Symfony\Component\Routing\Generator;
interface UrlGeneratorInterface
{
    public function generate(string $name): string;
}
",
        ),
    ] {
        php_index
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }
    let root_path = path_string(root.path());
    let services = Arc::new(ServiceIndex::new(&root_path, service_cache.path(), None).unwrap());
    services
        .index(&ParsedFile::new(
            format!("{root_path}/config/services.yaml"),
            b"services:\n  app.consumer:\n    class: App\\Service\\Consumer\n".to_vec(),
        ))
        .unwrap();
    Fixture {
        _dirs: vec![root, php_cache, service_cache],
        provider: PropertyServiceCodeActionProvider::new(Some(php_index.clone()), Some(services)),
        php_index,
    }
}

fn request(source: &str, needle: &str) -> CodeActionRequest {
    let offset = source.rfind(needle).unwrap() as u32;
    request_at(URI, source, offset, offset + needle.len() as u32)
}

fn run(fixture: &Fixture, source: &str, needle: &str) -> Vec<protocol::CodeAction> {
    fixture
        .provider
        .get_code_actions(&Context::background(), &request(source, needle))
}

fn apply(source: &str, action: &protocol::CodeAction) -> String {
    let updated = apply_code_action_edit(source, action, URI);
    assert!(
        TextDocument::new(URI, &updated, 2)
            .parse_errors()
            .is_empty(),
        "{updated}"
    );
    updated
}

const SIMPLE: &str = r"<?php
namespace App\Service;
class Consumer
{
    public function run(): void
    {
        $this->productRepository->findAll();
    }
}";

#[test]
fn creates_promoted_constructor() {
    let fixture = fixture();
    let actions = run(&fixture, SIMPLE, "productRepository");
    assert_eq!(actions.len(), 1);
    assert_eq!(
        actions[0].title,
        "Symfony: Inject ProductRepository as $productRepository"
    );
    assert_eq!(actions[0].kind, Some(CodeActionKind::REFACTOR_REWRITE));
    let updated = apply(SIMPLE, &actions[0]);
    assert!(updated.contains("use App\\Repository\\ProductRepository;"));
    assert!(updated.contains(
        "public function __construct(
        private readonly ProductRepository $productRepository,
    ) {
    }"
    ));
    assert!(updated.contains("$this->productRepository->findAll();"));
}

#[test]
fn extends_promoted_constructor_before_optional() {
    let fixture = fixture();
    let source = r"<?php
namespace App\Service;
use Psr\Log\LoggerInterface;
class Consumer
{
    public function __construct(
        private readonly LoggerInterface $logger,
        private ?string $name = null,
    ) {
    }

    public function run(): void
    {
        $this->productRepository->findAll();
    }
}";
    let actions = run(&fixture, source, "productRepository");
    assert_eq!(actions.len(), 1);
    let updated = apply(source, &actions[0]);
    assert!(updated.contains(
        "private readonly LoggerInterface $logger,
        private readonly ProductRepository $productRepository,
        private ?string $name = null,"
    ));
}

#[test]
fn preserves_traditional_injection_style() {
    let fixture = fixture();
    let source = r"<?php
namespace App\Service;
class Consumer
{
    private string $name;

    public function __construct(string $name = '')
    {
        $this->name = $name;
    }

    public function run(): void
    {
        $this->productRepository->findAll();
    }
}";
    let actions = run(&fixture, source, "productRepository");
    assert_eq!(actions.len(), 1);
    let updated = apply(source, &actions[0]);
    assert!(updated.contains("private ProductRepository $productRepository;"));
    assert!(updated.contains(
        "public function __construct(ProductRepository $productRepository, string $name = '')"
    ));
    assert!(updated.contains(
        "{
        $this->productRepository = $productRepository;
        $this->name = $name;
    }"
    ));
}

#[test]
fn handles_readonly_class() {
    let fixture = fixture();
    let source = r"<?php
namespace App\Service;
readonly class Consumer
{
    public function run(): void
    {
        $this->productRepository->findAll();
    }
}";
    let actions = run(&fixture, source, "productRepository");
    assert_eq!(actions.len(), 1);
    let updated = apply_code_action_edit(source, &actions[0], URI);
    assert!(updated.contains("private ProductRepository $productRepository,"));
    assert!(!updated.contains("private readonly ProductRepository $productRepository,"));
}

#[test]
fn respects_php_language_level() {
    for (name, version, expected, unexpected) in [
        (
            "PHP 7.4 traditional injection",
            "7.4",
            "private ProductRepository $productRepository;\n\n    public function __construct(",
            "private readonly ProductRepository",
        ),
        (
            "PHP 8.0 promotion without readonly",
            "8.0",
            "private ProductRepository $productRepository,",
            "private readonly ProductRepository",
        ),
    ] {
        let fixture = fixture();
        let project_root = tempfile::tempdir().unwrap();
        std::fs::write(
            project_root.path().join("composer.json"),
            format!(r#"{{"require":{{"php":"{version}"}}}}"#),
        )
        .unwrap();
        fixture
            .php_index
            .configure_project(project_root.path())
            .unwrap();
        let actions = run(&fixture, SIMPLE, "productRepository");
        assert_eq!(actions.len(), 1, "{name}");
        let updated = apply(SIMPLE, &actions[0]);
        assert!(updated.contains(expected), "{name}: {updated}");
        assert!(!updated.contains(unexpected), "{name}");
    }
}

#[test]
fn uses_alias_and_called_method() {
    let fixture = fixture();
    let source = r"<?php
namespace App\Service;
class Consumer
{
    public function run(): string
    {
        return $this->router->generate('home');
    }
}";
    let actions = run(&fixture, source, "router");
    assert_eq!(actions.len(), 1);
    assert_eq!(
        actions[0].title,
        "Symfony: Inject UrlGeneratorInterface as $router"
    );
    let updated = apply(source, &actions[0]);
    assert!(updated.contains("use Symfony\\Component\\Routing\\Generator\\UrlGeneratorInterface;"));
    assert!(updated.contains("private readonly UrlGeneratorInterface $router,"));
}

#[test]
fn rejects_declared_inherited_and_non_service_properties() {
    let fixture = fixture();
    for (name, class_name, declaration, expression, needle) in [
        (
            "declared property",
            "Consumer",
            "private \\App\\Repository\\ProductRepository $productRepository;",
            "$this->productRepository->findAll();",
            "productRepository->",
        ),
        (
            "inherited property",
            "Consumer extends BaseConsumer",
            "",
            "$this->productRepository->findAll();",
            "productRepository",
        ),
        (
            "different receiver",
            "Consumer",
            "",
            "$other->productRepository->findAll();",
            "productRepository",
        ),
        (
            "short property",
            "Consumer",
            "",
            "$this->em->flush();",
            "em",
        ),
        (
            "class is not a service",
            "NotAService",
            "",
            "$this->productRepository->findAll();",
            "productRepository",
        ),
    ] {
        let source = format!(
            "<?php
namespace App\\Service;
class {class_name}
{{
    {declaration}
    public function run(): void
    {{
        {expression}
    }}
}}"
        );
        assert!(run(&fixture, &source, needle).is_empty(), "{name}");
    }
}

#[test]
fn candidate_normalization_and_ranking() {
    let fixture = fixture();
    let candidates = property_service_candidates(Some(&fixture.php_index), "fooBarLogger", "info");
    assert!(!candidates.is_empty());
    assert_eq!(candidates[0].class_name, "Psr\\Log\\LoggerInterface");
    let candidates =
        property_service_candidates(Some(&fixture.php_index), "longClassNameServiceFactory", "");
    assert!(!candidates.is_empty());
    assert_eq!(
        candidates[0].class_name,
        "App\\Service\\FoobarLongClassNameServiceFactory"
    );
    assert!(property_service_candidates(Some(&fixture.php_index), "serviceFactory", "").is_empty());
}
