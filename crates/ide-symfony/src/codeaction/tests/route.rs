//! Route attribute and route action parameter tests.

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{ActionProvider, CodeActionRequest, Context, SyntaxContext, TextDocument};
use swlsp_lsp_protocol::{self as protocol, CodeActionKind};
use swlsp_php::{NameResolver, PhpIndex};
use swlsp_syntax::{SyntaxNodeExt, TextSize};

use super::*;

/// A request whose node is taken at the needle offset, the range spans the needle.
fn needle_request(uri: &str, source: &str, needle: &str) -> CodeActionRequest {
    let document = Arc::new(TextDocument::new(uri, source, 1));
    let offset = source.find(needle).unwrap() as u32;
    let (line, character) = document.line_index().position_utf16(offset);
    let root = document.root().unwrap();
    let node = root.node_at(TextSize::from(offset));
    CodeActionRequest {
        params: protocol::CodeActionParams {
            text_document: protocol::TextDocumentIdentifier {
                uri: document.uri.clone(),
            },
            range: protocol::Range {
                start: protocol::Position { line, character },
                end: protocol::Position {
                    line,
                    character: character + needle.len() as u32,
                },
            },
            context: protocol::CodeActionContext::default(),
        },
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document),
            root: Some(root),
            token: None,
            node,
        },
    }
}

fn attribute_request(source: &str, method: &str) -> CodeActionRequest {
    needle_request("file:///project/Controller.php", source, method)
}

fn parameter_request(source: &str, method: &str) -> CodeActionRequest {
    needle_request("file:///project/src/TestCommand.php", source, method)
}

fn attribute_fixture() -> (tempfile::TempDir, RouteAttributeCodeActionProvider) {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/vendor/SymfonyRouteStubs.php".to_owned(),
            br"<?php
namespace Symfony\Component\Routing\Attribute;
class Route {}
namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {}
namespace Symfony\Component\HttpKernel\Attribute;
class AsController {}
"
            .to_vec(),
        ))
        .unwrap();
    (dir, RouteAttributeCodeActionProvider::new(Some(index)))
}

fn edits(request: &CodeActionRequest, action: &protocol::CodeAction) -> Vec<protocol::TextEdit> {
    action.edit.as_ref().unwrap().changes[&request.params.text_document.uri].clone()
}

#[test]
fn route_attribute_controller_recognition() {
    for (name, source) in [
        (
            "abstract controller",
            r"<?php
namespace App\Controller;
use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;
class TestController extends AbstractController
{
    public function indexAction() {}
}",
        ),
        (
            "AsController attribute",
            r"<?php
namespace App\Endpoint;
use Symfony\Component\HttpKernel\Attribute\AsController;
#[AsController]
class Endpoint
{
    public function indexAction() {}
}",
        ),
        (
            "route on another method",
            r"<?php
namespace App\Endpoint;
use Symfony\Component\Routing\Attribute\Route;
class Endpoint
{
    #[Route('/other')]
    public function otherAction() {}
    public function indexAction() {}
}",
        ),
        (
            "route on class",
            r"<?php
namespace App\Endpoint;
use Symfony\Component\Routing\Attribute\Route;
#[Route('/api')]
class Endpoint
{
    public function indexAction() {}
}",
        ),
    ] {
        let (_dir, provider) = attribute_fixture();
        let actions = provider.get_code_actions(
            &Context::background(),
            &attribute_request(source, "indexAction"),
        );
        assert_eq!(actions.len(), 1, "{name}");
        assert_eq!(actions[0].title, "Symfony: Add Route attribute");
        assert_eq!(actions[0].kind, Some(CodeActionKind::REFACTOR_REWRITE));
    }
}

#[test]
fn route_attribute_rejects_invalid_targets() {
    for (name, source, method) in [
        (
            "existing route",
            r"<?php
namespace App\Controller;
use Symfony\Component\Routing\Attribute\Route;
class TestController
{
    #[Route('/')]
    public function indexAction() {}
}",
            "indexAction",
        ),
        (
            "private method",
            r"<?php
namespace App\Controller;
class TestController
{
    private function helperMethod() {}
}",
            "helperMethod",
        ),
        (
            "static method",
            r"<?php
namespace App\Controller;
class TestController
{
    public static function staticMethod() {}
}",
            "staticMethod",
        ),
        (
            "non controller",
            r"<?php
namespace App\Service;
class TestService
{
    public function doSomething() {}
}",
            "doSomething",
        ),
    ] {
        let (_dir, provider) = attribute_fixture();
        let actions =
            provider.get_code_actions(&Context::background(), &attribute_request(source, method));
        assert!(actions.is_empty(), "{name}");
    }
}

#[test]
fn route_attribute_adds_import_path_and_name() {
    let source = r"<?php
namespace App\Controller\Admin;
use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;
class UserController extends AbstractController
{
    public function editAction() {}
}";
    let request = attribute_request(source, "editAction");
    let (_dir, provider) = attribute_fixture();
    let actions = provider.get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 1);
    let edits = edits(&request, &actions[0]);
    assert_eq!(edits.len(), 2);
    assert_eq!(
        edits[0].new_text,
        "#[Route('/admin/user/edit', name: 'app_admin_user_edit')]\n    "
    );
    assert!(
        edits[1]
            .new_text
            .contains("use Symfony\\Component\\Routing\\Attribute\\Route;")
    );
}

#[test]
fn route_attribute_reuses_alias_and_avoids_conflict() {
    for (name, import_line, prefix, edit_count) in [
        (
            "existing alias",
            "use Symfony\\Component\\Routing\\Attribute\\Route as RoutingRoute;",
            "#[RoutingRoute(",
            1,
        ),
        (
            "short name conflict",
            "use App\\Metadata\\Route;",
            "#[\\Symfony\\Component\\Routing\\Attribute\\Route(",
            1,
        ),
    ] {
        let source = format!(
            "<?php\nnamespace App\\Controller;\n{import_line}\nclass ProductController\n{{\n    public function showAction() {{}}\n}}"
        );
        let request = attribute_request(&source, "showAction");
        let (_dir, provider) = attribute_fixture();
        let actions = provider.get_code_actions(&Context::background(), &request);
        assert_eq!(actions.len(), 1, "{name}");
        let edits = edits(&request, &actions[0]);
        assert_eq!(edits.len(), edit_count, "{name}");
        assert!(edits[0].new_text.starts_with(prefix), "{name}");
    }
}

#[test]
fn generated_route_path_and_name() {
    for (class, method, path, name) in [
        (
            r"App\Controller\TestController",
            "indexAction",
            "/test",
            "app_test_index",
        ),
        (
            r"App\Controller\ProductController",
            "showAction",
            "/product/show",
            "app_product_show",
        ),
        (
            r"App\Controller\Admin\UserController",
            "editAction",
            "/admin/user/edit",
            "app_admin_user_edit",
        ),
    ] {
        assert_eq!(generated_route_path(class, method), path);
        assert_eq!(generated_route_name(class, method), name);
    }
}

#[test]
fn route_action_parameter_recognizes_routes() {
    for (name, source, method) in [
        (
            "method attribute",
            r"<?php
use Symfony\Component\Routing\Attribute\Route;
class TestController
{
    #[Route('/test')]
    public function index(): void {}
}",
            "index",
        ),
        (
            "method annotation",
            r#"<?php
use Symfony\Component\Routing\Annotation\Route as RoutingRoute;
class TestController
{
    /** @RoutingRoute("/test") */
    public function index(): void {}
}"#,
            "index",
        ),
        (
            "invokable class attribute",
            r"<?php
use Symfony\Component\Routing\Attribute\Route;
#[Route('/test')]
class TestController
{
    public function __invoke(): void {}
}",
            "__invoke",
        ),
        (
            "invokable class annotation",
            r#"<?php
use Sensio\Bundle\FrameworkExtraBundle\Configuration\Route;
/** @Route("/test") */
class TestController
{
    public function __invoke(): void {}
}"#,
            "__invoke",
        ),
    ] {
        let actions = RouteActionParameterCodeActionProvider::new()
            .get_code_actions(&Context::background(), &parameter_request(source, method));
        assert_eq!(actions.len(), 2, "{name}");
        assert_eq!(
            actions[0].title,
            "Symfony: Add Request parameter to route action"
        );
        assert_eq!(actions[0].kind, Some(CodeActionKind::REFACTOR_REWRITE));
    }
}

#[test]
fn route_action_parameter_rejects_invalid_targets() {
    for (name, source) in [
        (
            "private route",
            r"<?php
use Symfony\Component\Routing\Attribute\Route;
class TestController
{
    #[Route('/test')]
    private function index(): void {}
}",
        ),
        (
            "method without route",
            r"<?php
class TestController
{
    public function index(): void {}
}",
        ),
        (
            "non invoke with class route",
            r"<?php
use Symfony\Component\Routing\Attribute\Route;
#[Route('/test')]
class TestController
{
    public function index(): void {}
}",
        ),
        (
            "all types exist",
            r"<?php
use Symfony\Component\Routing\Attribute\Route;
use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\Security\Core\User\UserInterface;
class TestController
{
    #[Route('/test')]
    public function index(
        Request $request,
        UserInterface $user,
    ): void {}
}",
        ),
    ] {
        let actions = RouteActionParameterCodeActionProvider::new()
            .get_code_actions(&Context::background(), &parameter_request(source, "index"));
        assert!(actions.is_empty(), "{name}");
    }
}

#[test]
fn route_action_parameter_filters_and_adds_parameters() {
    let source = r"<?php
namespace App\Controller;
use Symfony\Component\Routing\Attribute\Route;
use Symfony\Component\HttpFoundation\Request as HttpRequest;
class TestController
{
    #[Route('/test')]
    public function index(
        HttpRequest $request,
        string $slug,
        ?string $format = null,
    ): void {}
}";
    let request = parameter_request(source, "index");
    let method = phpquery::method_at(request.node.as_ref().unwrap()).unwrap();
    let existing =
        php_existing_parameter_types(&method, &NameResolver::new(request.root.as_ref().unwrap()));
    assert!(existing.contains("symfony\\component\\httpfoundation\\request"));
    let actions = RouteActionParameterCodeActionProvider::new()
        .get_code_actions(&Context::background(), &request);
    assert_eq!(actions.len(), 1);
    assert_eq!(
        actions[0].title,
        "Symfony: Add UserInterface parameter to route action"
    );
    let edits = edits(&request, &actions[0]);
    assert_eq!(edits.len(), 2);
    assert_eq!(edits[0].new_text, "UserInterface $user,\n        ");
    assert!(
        edits[1]
            .new_text
            .contains("use Symfony\\Component\\Security\\Core\\User\\UserInterface;")
    );
}

#[test]
fn php_doc_has_resolved_route_annotation() {
    let source = r#"<?php
namespace App;
use Symfony\Component\Routing\Annotation\Route as RoutingRoute;
class Controller
{
    /**
     * @param string $name
     * @RoutingRoute("/test")
     */
    public function index(string $name): void {}
}"#;
    let document = TextDocument::new("file:///project/Controller.php", source, 1);
    let root = document.root().unwrap();
    let resolver = NameResolver::new(&root);
    let offset = source.find("function index").unwrap() as u32;
    let method = phpquery::method_at(&root.node_at(TextSize::from(offset)).unwrap());
    assert!(has_php_route_marker(method.as_ref(), &resolver));
}
