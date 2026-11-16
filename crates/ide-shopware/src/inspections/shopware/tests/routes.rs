use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::Inspection;
use swlsp_php::PhpIndex;

use super::super::new_shopware_routes;
use super::super::routes::ROUTE_CONSTANT_FIX_ID;
use crate::inspections::test_support::{check_diagnostics, check_fix};

const SYMFONY: &str = r"<?php
namespace Symfony\Component\Routing\Attribute;
#[\Attribute]
class Route
{
    public function __construct(string|array|null $path = null, ?string $name = null, array $requirements = [], array $options = [], array $defaults = [], ?string $host = null, array|string $methods = []) {}
}
";

const SHOPWARE: &str = r"<?php
namespace Shopware\Core {
    class PlatformRequest
    {
        public const ATTRIBUTE_ROUTE_SCOPE = '_routeScope';
        public const ATTRIBUTE_LOGIN_REQUIRED = '_loginRequired';
        public const ATTRIBUTE_ACL = '_acl';
        public const ATTRIBUTE_CONTEXT_OBJECT = 'sw-context';
        public const HEADER_CONTEXT_TOKEN = '_other';
    }
}
namespace Shopware\Core\Framework\Routing {
    class ApiRouteScope { public const ID = 'api'; }
    class StoreApiRouteScope { public const ID = 'store-api'; }
}
namespace Shopware\Storefront\Framework\Routing {
    class StorefrontRouteScope { public const ID = 'storefront'; }
}
";

fn inspection(shopware: bool) -> (tempfile::TempDir, Arc<dyn Inspection>) {
    let cache = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(cache.path().join("php-index"), None).unwrap();
    let mut files = vec![("/project/vendor/Route.php", SYMFONY)];
    if shopware {
        files.push(("/project/vendor/Shopware.php", SHOPWARE));
    }
    for (path, source) in files {
        php.index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .unwrap();
    }
    (cache, new_shopware_routes(Some(php)))
}

const URI: &str = "file:///project/custom/plugins/Swag/src/Controller/ExampleController.php";

const HEADER: &str = r"<?php
namespace Swag\Controller;

use Shopware\Core\Framework\Routing\ApiRouteScope;
use Shopware\Core\Framework\Routing\StoreApiRouteScope;
use Shopware\Core\PlatformRequest;
use Shopware\Storefront\Framework\Routing\StorefrontRouteScope;
use Symfony\Component\Routing\Attribute\Route;
";

fn strip(fixture: &str) -> String {
    let mut text = fixture.to_owned();
    while let Some(start) = text.find("{|") {
        let colon = start + text[start..].find(':').unwrap();
        text.replace_range(start..=colon, "");
        let end = text.find("|}").unwrap();
        text.replace_range(end..end + 2, "");
    }
    text
}

#[test]
fn routes_follow_the_conventions_of_shopware() {
    let body = r"
#[Route(defaults: [PlatformRequest::ATTRIBUTE_ROUTE_SCOPE => [StorefrontRouteScope::ID]])]
class StorefrontController
{
    #[Route(path: '/example', name: 'frontend.example.page', methods: ['GET'])]
    public function fine(): void {}

    #[Route(path: '/widgets/example', name: 'widgets.example', methods: ['GET'])]
    #[Route(path: '/pay', name: 'payment.example', methods: ['POST'])]
    public function alsoFine(): void {}

    #[Route(path: '/example/list', name: {|shopware.php.route.name_prefix:'swag.example.list'|}, methods: ['GET'])]
    public function named(): void {}

    #[{|shopware.php.route.methods_missing:Route|}(path: '/example/any', name: 'frontend.example.any')]
    public function anyMethod(): void {}

    public function noRoute(): void {}
}

class Unscoped
{
    #[Route(path: '/unscoped', name: 'frontend.unscoped', methods: ['GET'])]
    public function {|shopware.php.route.scope_missing:show|}(): void {}

    #[Route(path: '/scoped', name: 'frontend.scoped', defaults: [PlatformRequest::ATTRIBUTE_ROUTE_SCOPE => [StorefrontRouteScope::ID]], methods: ['GET'])]
    public function scoped(): void {}
}

#[Route(defaults: [PlatformRequest::ATTRIBUTE_ROUTE_SCOPE => [ApiRouteScope::ID]], methods: ['POST'])]
class ApiController
{
    #[Route(path: '/api/_action/swag/run', name: 'api.action.swag.run')]
    public function fine(): void {}

    #[Route(path: {|shopware.php.route.scope_path:'/swag/run'|}, name: 'swag.run')]
    public function outside(): void {}

    #[Route(path: '/sw-domain-hash.html', name: 'api.hash')]
    public function hash(): void {}
}

#[Route(path: '/store-api/swag', defaults: [PlatformRequest::ATTRIBUTE_ROUTE_SCOPE => [StoreApiRouteScope::ID]])]
class StoreApiController
{
    #[Route(path: '/list', name: 'store-api.swag.list', methods: ['GET'])]
    public function prefixed(): void {}
}

#[Route(defaults: [PlatformRequest::ATTRIBUTE_ROUTE_SCOPE => [StoreApiRouteScope::ID]])]
class OtherStoreApiController
{
    #[Route(path: {|shopware.php.route.scope_path:'/swag/list'|}, name: 'store-api.swag.other', methods: ['GET'])]
    public function outside(): void {}
}

#[Route(defaults: [{|shopware.php.route.attribute_literal:'_routeScope'|} => [{|shopware.php.route.attribute_literal:'storefront'|}], {|shopware.php.route.attribute_literal:'_loginRequired'|} => true, 'auth_required' => false, 'sw-context' => 1, '_other' => 1])]
class Literals
{
    #[Route(path: '/literal', name: 'frontend.literal', defaults: [{|shopware.php.route.attribute_literal:'_acl'|} => ['storefront']], methods: ['GET'])]
    public function show(): void {}
}
";
    let fixture = format!("{HEADER}{body}");
    let (_cache, shopware) = inspection(true);
    let (_, problems) = check_diagnostics(shopware.as_ref(), URI, &fixture);
    let message = |code: &str| {
        problems
            .iter()
            .find(|problem| problem.id.as_str() == code)
            .map(|problem| problem.message.clone())
            .unwrap()
    };
    assert_eq!(
        message("shopware.php.route.name_prefix"),
        "The storefront router only adds the sales channel URL to routes named frontend.*, widgets.* or payment.*; links to 'swag.example.list' miss it"
    );
    assert_eq!(
        message("shopware.php.route.attribute_literal"),
        "Use the constant PlatformRequest::ATTRIBUTE_ROUTE_SCOPE for '_routeScope'"
    );
    // The conventions are Shopware's own: nothing is said without it.
    let (_cache, symfony) = inspection(false);
    check_diagnostics(symfony.as_ref(), URI, &strip(&fixture));
}

#[test]
fn shopware_own_bundles_need_no_scope_attribute() {
    let (_cache, inspection) = inspection(true);
    check_diagnostics(
        inspection.as_ref(),
        "file:///project/vendor/shopware/core/Installer/Controller/StartController.php",
        "<?php\nnamespace Shopware\\Core\\Installer\\Controller;\n\nuse Symfony\\Component\\Routing\\Attribute\\Route;\n\nclass StartController\n{\n    #[Route(path: '/installer', name: 'installer.start', methods: ['GET'])]\n    public function start(): void {}\n}\n",
    );
}

#[test]
fn the_constant_fix_imports_the_class_it_names() {
    let (_cache, inspection) = inspection(true);
    let route = "#[Route(path: '/a', name: 'frontend.a', defaults: [@KEY@ => [@VALUE@]], methods: ['GET'])]\n    public function show(): void {}\n}\n";
    let class = |imports: &str, key: &str, scope: &str| {
        format!(
            "<?php\nnamespace Swag\\Controller;\n\n{imports}use Symfony\\Component\\Routing\\Attribute\\Route;\n\nclass ExampleController\n{{\n    {}",
            route.replace("@KEY@", key).replace("@VALUE@", scope)
        )
    };
    let key = "{|shopware.php.route.attribute_literal:'_routeScope'|}";
    let scope = "{|shopware.php.route.attribute_literal:'storefront'|}";
    for (before, after) in [
        // The class is imported with the others.
        (
            class("", key, "StorefrontRouteScope::ID"),
            class(
                "",
                "PlatformRequest::ATTRIBUTE_ROUTE_SCOPE",
                "StorefrontRouteScope::ID",
            )
            .replace(
                "use Symfony\\Component\\Routing\\Attribute\\Route;",
                "use Symfony\\Component\\Routing\\Attribute\\Route;\nuse Shopware\\Core\\PlatformRequest;",
            ),
        ),
        // An existing import is used.
        (
            class("use Shopware\\Core\\PlatformRequest;\n", key, "'x'"),
            class(
                "use Shopware\\Core\\PlatformRequest;\n",
                "PlatformRequest::ATTRIBUTE_ROUTE_SCOPE",
                "'x'",
            ),
        ),
        // Another class has the short name: the qualified name is written.
        (
            class("use Swag\\Other\\PlatformRequest;\n", key, "'x'"),
            class(
                "use Swag\\Other\\PlatformRequest;\n",
                "\\Shopware\\Core\\PlatformRequest::ATTRIBUTE_ROUTE_SCOPE",
                "'x'",
            ),
        ),
        // An alias is the name the file knows the class by.
        (
            class("use Shopware\\Core\\PlatformRequest as Platform;\n", key, "'x'"),
            class(
                "use Shopware\\Core\\PlatformRequest as Platform;\n",
                "Platform::ATTRIBUTE_ROUTE_SCOPE",
                "'x'",
            ),
        ),
        (
            class(
                "use Shopware\\Core\\PlatformRequest;\n",
                "PlatformRequest::ATTRIBUTE_ROUTE_SCOPE",
                scope,
            ),
            class(
                "use Shopware\\Core\\PlatformRequest;\n",
                "PlatformRequest::ATTRIBUTE_ROUTE_SCOPE",
                "StorefrontRouteScope::ID",
            )
            .replace(
                "use Symfony\\Component\\Routing\\Attribute\\Route;",
                "use Symfony\\Component\\Routing\\Attribute\\Route;\nuse Shopware\\Storefront\\Framework\\Routing\\StorefrontRouteScope;",
            ),
        ),
    ] {
        let presentation = check_fix(
            inspection.as_ref(),
            URI,
            &before,
            ROUTE_CONSTANT_FIX_ID,
            &after,
        );
        assert!(presentation.preferred);
    }
}
