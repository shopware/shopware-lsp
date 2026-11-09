use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_php::PhpIndex;
use tempfile::TempDir;

use crate::shopware::php_semantic::same_php_namespace_package;
use crate::shopware::*;
use crate::test_support::check_problems;

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
    let (document, _) = check_problems(
        &ShopwarePhpSemanticAnalyzer::new(Some(index)),
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

class InternalConsumer extends {|shopware.php.internal.class_extension:InternalBase|} {}
class Decorator extends {|shopware.php.decorator.concrete_extension:Core|} {}

class FrequentTask extends ScheduledTask
{
    public static function getDefaultInterval(): int
    {
        return {|shopware.php.scheduled_task.interval:299|};
    }
}

class PlainService
{
    public function __construct(SessionInterface $session)
    {
        $session->{|shopware.php.session.constructor:get|}('constructor');
    }
}

class PaymentHandler extends AbstractPaymentHandler
{
    public function pay(SessionInterface $session): void
    {
        $session->{|shopware.php.session.payment_handler:get|}('payment');
    }
}

#[Route(defaults: [PlatformRequest::ATTRIBUTE_ROUTE_SCOPE => [StoreApiRouteScope::ID]])]
class StoreAPIController
{
    public function action(SessionInterface $session): void
    {
        $session->{|shopware.php.session.store_api:get|}('store-api');
    }
}

function run(EntityRepository $repository, InternalService $internal): void
{
    foreach ([1] as $id) {
        $repository->{|shopware.php.repository.in_loop:search|}($id);
    }
    while (false) {
        $repository->{|shopware.php.repository.in_loop:search|}('again');
    }
    $repository->search('outside');
    foreach ($repository->search('source') as $entity) {
    }
    for ($result = $repository->search('init'); $repository->{|shopware.php.repository.in_loop:search|}('condition'); $repository->{|shopware.php.repository.in_loop:search|}('update')) {
    }
    while (false) {
        foreach ($repository->{|shopware.php.repository.in_loop:search|}('nested source') as $entity) {
        }
    }

    {|shopware.php.internal.function_call:\Shopware\Core\Internal\internal_helper|}();
    $internal->{|shopware.php.internal.method_call:secret|}();
    (new UserEntity())->{|shopware.php.user.store_token:getStoreToken|}();
}
",
    );
    assert!(document.parse_errors().is_empty());
}

#[test]
fn shopware_php_semantic_analyzer_allows_safe_and_same_package_usage() {
    let (_dir, index) = semantic_test_index();
    let analyzer = ShopwarePhpSemanticAnalyzer::new(Some(index));
    check_problems(
        &analyzer,
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
    check_problems(
        &analyzer,
        "file:///project/src/ShopwareInternalUse.php",
        r"<?php
namespace Shopware\Core\DifferentPackage;
use Shopware\Core\Internal\InternalService;
function useInternalMethod(InternalService $service): void { $service->secret(); }
",
    );
}

/// Suppression comments are applied by the server to every inspection
/// (`swlsp_lsp::Suppressions`), not by analyzers.
#[test]
fn shopware_php_semantic_analyzer_leaves_suppression_to_the_server() {
    let (_dir, index) = semantic_test_index();
    check_problems(
        &ShopwarePhpSemanticAnalyzer::new(Some(index)),
        "file:///project/src/Suppressed.php",
        r"<?php
use Shopware\Core\System\User\UserEntity;

/** @phpstan-ignore-next-line shopware.php.user.store_token */
(new UserEntity())->{|shopware.php.user.store_token:getStoreToken|}();
",
    );
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
