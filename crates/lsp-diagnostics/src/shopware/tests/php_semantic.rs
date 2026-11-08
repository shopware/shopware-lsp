use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_php::PhpIndex;
use tempfile::TempDir;

use super::{analyze, counts, highlight, highlights, problem_counts};
use crate::shopware::php_semantic::same_php_namespace_package;
use crate::shopware::*;
use crate::test_support::document;

pub(super) fn php_index_with(fixtures: &[(&str, &str)]) -> (TempDir, PhpIndex) {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    for (path, source) in fixtures {
        index
            .index(&ParsedFile::new(*path, source.as_bytes().to_vec()))
            .unwrap();
    }
    (dir, index)
}

fn semantic_test_index() -> (TempDir, PhpIndex) {
    php_index_with(&[
        (
            "/project/vendor/EntityRepository.php",
            r"<?php
namespace Shopware\Core\Framework\DataAbstractionLayer;
class EntityRepository { public function search(mixed $criteria): void {} }",
        ),
        (
            "/project/vendor/SessionInterface.php",
            r"<?php
namespace Symfony\Component\HttpFoundation\Session;
interface SessionInterface { public function get(string $key): mixed; }",
        ),
        (
            "/project/vendor/PlatformRequest.php",
            r"<?php
namespace Shopware\Core;
class PlatformRequest { public const ATTRIBUTE_ROUTE_SCOPE = '_routeScope'; }",
        ),
        (
            "/project/vendor/StoreApiRouteScope.php",
            r"<?php
namespace Shopware\Core\Framework\Routing;
class StoreApiRouteScope { public const ID = 'store-api'; }",
        ),
        (
            "/project/vendor/PaymentHandler.php",
            r"<?php
namespace Shopware\Core\Checkout\Payment\Cart\PaymentHandler;
abstract class AbstractPaymentHandler {}",
        ),
        (
            "/project/vendor/ScheduledTask.php",
            r"<?php
namespace Shopware\Core\Framework\MessageQueue\ScheduledTask;
abstract class ScheduledTask { abstract public static function getDefaultInterval(): int; }",
        ),
        (
            "/project/vendor/UserEntity.php",
            r"<?php
namespace Shopware\Core\System\User;
class UserEntity { public function getStoreToken(): string { return ''; } }",
        ),
        (
            "/project/vendor/InternalBase.php",
            r"<?php
namespace Shopware\Core\Internal;
/** @internal */
class InternalBase {}",
        ),
        (
            "/project/vendor/InternalService.php",
            r"<?php
namespace Shopware\Core\Internal;
class InternalService {
    /** @internal */
    public function secret(): void {}
}",
        ),
        (
            "/project/vendor/InternalFunction.php",
            r"<?php
namespace Shopware\Core\Internal;
/** @internal */
function internal_helper(): void {}",
        ),
        (
            "/project/vendor/AbstractCore.php",
            r"<?php
namespace Shopware\Core\Test\Decorator;
abstract class AbstractCore { abstract public function getDecorated(): AbstractCore; }",
        ),
        (
            "/project/vendor/Core.php",
            r"<?php
namespace Shopware\Core\Test\Decorator;
class Core extends AbstractCore { public function getDecorated(): AbstractCore { return $this; } }",
        ),
    ])
}

#[test]
fn shopware_php_semantic_analyzer_reports_type_aware_rules() {
    let (_dir, index) = semantic_test_index();
    let document = document(
        "file:///project/src/Consumer.php",
        r"<?php
namespace App\Plugin;

use Shopware\Core\Checkout\Payment\Cart\PaymentHandler\AbstractPaymentHandler;
use Shopware\Core\Framework\DataAbstractionLayer\EntityRepository;
use Shopware\Core\Framework\MessageQueue\ScheduledTask\ScheduledTask;
use Shopware\Core\Internal\InternalBase;
use Shopware\Core\Internal\InternalService;
use Shopware\Core\Framework\Routing\StoreApiRouteScope;
use Shopware\Core\PlatformRequest;
use Shopware\Core\System\User\UserEntity;
use Shopware\Core\Test\Decorator\Core;
use Symfony\Component\HttpFoundation\Session\SessionInterface;
use Symfony\Component\Routing\Attribute\Route;

class InternalConsumer extends InternalBase {}
class Decorator extends Core {}

class FrequentTask extends ScheduledTask
{
    public static function getDefaultInterval(): int
    {
        return 299;
    }
}

class PlainService
{
    public function __construct(SessionInterface $session)
    {
        $session->get('constructor');
    }
}

class PaymentHandler extends AbstractPaymentHandler
{
    public function pay(SessionInterface $session): void
    {
        $session->get('payment');
    }
}

#[Route(defaults: [PlatformRequest::ATTRIBUTE_ROUTE_SCOPE => [StoreApiRouteScope::ID]])]
class StoreAPIController
{
    public function action(SessionInterface $session): void
    {
        $session->get('store-api');
    }
}

function run(EntityRepository $repository, InternalService $internal): void
{
    foreach ([1] as $id) {
        $repository->search($id);
    }
    while (false) {
        $repository->search('again');
    }
    $repository->search('outside');
    foreach ($repository->search('source') as $entity) {
    }
    for ($result = $repository->search('init'); $repository->search('condition'); $repository->search('update')) {
    }
    while (false) {
        foreach ($repository->search('nested source') as $entity) {
        }
    }

    \Shopware\Core\Internal\internal_helper();
    $internal->secret();
    (new UserEntity())->getStoreToken();
}
",
    );
    assert!(document.parse_errors().is_empty());
    let problems = analyze(&ShopwarePhpSemanticAnalyzer::new(Some(index)), &document);
    assert_eq!(
        problem_counts(&problems),
        counts(&[
            (SHOPWARE_PHP_REPOSITORY_IN_LOOP_CODE, 5),
            (SHOPWARE_PHP_INTERNAL_CLASS_EXTENSION_CODE, 1),
            (SHOPWARE_PHP_INTERNAL_FUNCTION_CALL_CODE, 1),
            (SHOPWARE_PHP_INTERNAL_METHOD_CALL_CODE, 1),
            (SHOPWARE_PHP_SESSION_CONSTRUCTOR_CODE, 1),
            (SHOPWARE_PHP_SESSION_PAYMENT_HANDLER_CODE, 1),
            (SHOPWARE_PHP_SESSION_STORE_API_CODE, 1),
            (SHOPWARE_PHP_SCHEDULED_TASK_INTERVAL_CODE, 1),
            (SHOPWARE_PHP_USER_STORE_TOKEN_CODE, 1),
            (SHOPWARE_PHP_CONCRETE_DECORATOR_EXTENSION_CODE, 1),
        ]),
        "{:?}",
        highlights(&document, &problems)
    );
    for problem in &problems {
        let highlighted = highlight(&document, problem);
        assert!(!highlighted.is_empty());
        match problem.id.as_str() {
            SHOPWARE_PHP_INTERNAL_CLASS_EXTENSION_CODE => assert_eq!(highlighted, "InternalBase"),
            SHOPWARE_PHP_CONCRETE_DECORATOR_EXTENSION_CODE => assert_eq!(highlighted, "Core"),
            SHOPWARE_PHP_REPOSITORY_IN_LOOP_CODE => {
                let line = &document.source()[usize::from(problem.range.start())..];
                let line = &line[..line.find(')').unwrap()];
                assert!(
                    !["search('source'", "search('init'"].contains(&line),
                    "{line}"
                );
            }
            _ => {}
        }
    }
}

#[test]
fn shopware_php_semantic_analyzer_allows_safe_and_same_package_usage() {
    let (_dir, index) = semantic_test_index();
    let analyzer = ShopwarePhpSemanticAnalyzer::new(Some(index));
    let safe = document(
        "file:///project/src/Safe.php",
        r"<?php
namespace Shopware\Core\Internal;

use Shopware\Core\Framework\DataAbstractionLayer\EntityRepository;
use Shopware\Core\Framework\MessageQueue\ScheduledTask\ScheduledTask;
use Symfony\Component\HttpFoundation\Session\SessionInterface;

class Task extends ScheduledTask
{
    public static function getDefaultInterval(): int { return 300; }
}

function safe(EntityRepository $repository, InternalService $internal, SessionInterface $session): void
{
    $repository->search('outside');
    internal_helper();
    $internal->secret();
    $session->get('ordinary-method');
}
",
    );
    let problems = analyze(&analyzer, &safe);
    assert!(problems.is_empty(), "{:?}", highlights(&safe, &problems));

    let shopware = document(
        "file:///project/src/ShopwareInternalUse.php",
        r"<?php
namespace Shopware\Core\DifferentPackage;
use Shopware\Core\Internal\InternalService;
function useInternalMethod(InternalService $service): void { $service->secret(); }
",
    );
    let problems = analyze(&analyzer, &shopware);
    assert!(
        problems.is_empty(),
        "{:?}",
        highlights(&shopware, &problems)
    );
}

#[test]
fn shopware_php_semantic_analyzer_honors_rule_suppression() {
    let (_dir, index) = semantic_test_index();
    let document = document(
        "file:///project/src/Suppressed.php",
        r"<?php
use Shopware\Core\System\User\UserEntity;

/** @phpstan-ignore-next-line shopware.php.user.store_token */
(new UserEntity())->getStoreToken();
",
    );
    assert!(analyze(&ShopwarePhpSemanticAnalyzer::new(Some(index)), &document).is_empty());
}

#[test]
fn same_php_namespace_package_accepts_sibling_subtrees() {
    assert!(same_php_namespace_package(
        r"Shopware\Core\Checkout\Cart\Hook",
        r"Shopware\Core\Framework\Script\Execution",
    ));
    assert!(same_php_namespace_package(
        r"Shopware\Core\Framework",
        r"Shopware\Core\Framework\Script",
    ));
    assert!(same_php_namespace_package(
        r"Shopware\Core",
        r"Shopware\Core"
    ));
    assert!(!same_php_namespace_package(
        r"Swag\MyPlugin\Service",
        r"Shopware\Core\Framework\Script\Execution",
    ));
    assert!(!same_php_namespace_package(
        r"Shopware\Storefront\Framework",
        r"Shopware\Core\Framework",
    ));
    assert!(!same_php_namespace_package("Shopware", "Swag"));
    assert!(!same_php_namespace_package("", r"Shopware\Core"));
}
