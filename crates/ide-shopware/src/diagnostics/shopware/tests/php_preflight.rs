//! The rules drawn from recurring Store review findings.

use swlsp_php::PhpIndex;
use tempfile::TempDir;

use super::php_semantic::php_index_with;
use crate::diagnostics::shopware::*;
use crate::diagnostics::test_support::check_problems;

fn preflight_index() -> (TempDir, PhpIndex) {
    php_index_with(&[
        (
            "/project/vendor/Dal.php",
            r"<?php
namespace Shopware\Core\Framework\DataAbstractionLayer;
class Entity {}
class EntityCollection {}
class EntityRepository
{
    public function search(Search\Criteria $criteria, mixed $context): mixed { return null; }
    public function searchIds(Search\Criteria $criteria, mixed $context): mixed { return null; }
    public function create(array $data, mixed $context): void {}
    public function update(array $data, mixed $context): void {}
    public function upsert(array $data, mixed $context): void {}
    public function delete(array $ids, mixed $context): void {}
}",
        ),
        (
            "/project/vendor/Criteria.php",
            r"<?php
namespace Shopware\Core\Framework\DataAbstractionLayer\Search;
class Criteria
{
    public function __construct(?array $ids = null) {}
    public function setLimit(?int $limit): self { return $this; }
}",
        ),
        (
            "/project/vendor/SystemConfigService.php",
            r"<?php
namespace Shopware\Core\System\SystemConfig;
class SystemConfigService
{
    public function get(string $key, ?string $salesChannelId = null): mixed { return null; }
    public function getBool(string $key, ?string $salesChannelId = null): bool { return false; }
}",
        ),
        (
            "/project/vendor/State.php",
            r"<?php
namespace Shopware\Core\System\StateMachine\Aggregation\StateMachineState;
class StateMachineStateEntity
{
    public function getName(): string { return ''; }
    public function getTechnicalName(): string { return ''; }
}",
        ),
        (
            "/project/vendor/Messages.php",
            r"<?php
namespace Shopware\Core\Framework\MessageQueue;
interface AsyncMessageInterface {}
interface LowPriorityMessageInterface extends AsyncMessageInterface {}",
        ),
        (
            "/project/vendor/SalesChannelContext.php",
            r"<?php
namespace Shopware\Core\System\SalesChannel;
class SalesChannelContext {}",
        ),
        (
            "/project/vendor/Order.php",
            r"<?php
namespace Shopware\Core\Checkout\Order;
use Shopware\Core\Framework\DataAbstractionLayer\Entity;
use Shopware\Core\Framework\DataAbstractionLayer\EntityCollection;
class OrderEntity extends Entity {}
class OrderCollection extends EntityCollection {}",
        ),
        (
            "/project/vendor/Route.php",
            r"<?php
namespace Symfony\Component\Routing\Attribute;
#[\Attribute]
class Route
{
    public function __construct(?string $path = null, ?string $name = null, array $requirements = [], array $options = [], array $defaults = [], ?string $host = null, array|string $methods = []) {}
}",
        ),
        (
            "/project/vendor/Request.php",
            r"<?php
namespace Symfony\Component\HttpFoundation;
class Request
{
    public const METHOD_GET = 'GET';
    public const METHOD_POST = 'POST';
}",
        ),
    ])
}

fn check(uri: &str, fixture: &str) -> Vec<String> {
    let (_dir, index) = preflight_index();
    let (document, problems) =
        check_problems(&ShopwarePhpSemanticAnalyzer::new(Some(index)), uri, fixture);
    assert!(document.parse_errors().is_empty());
    problems
        .into_iter()
        .map(|problem| problem.message)
        .collect()
}

#[test]
fn searches_need_a_restricted_criteria() {
    check(
        "file:///project/src/Searches.php",
        r"<?php
namespace Acme;

use Shopware\Core\Framework\DataAbstractionLayer\EntityRepository;
use Shopware\Core\Framework\DataAbstractionLayer\Search\Criteria;

function searches(EntityRepository $repository, Criteria $given, array $ids, mixed $context): void
{
    $repository->{|shopware.php.repository.unbounded_search:search|}(new Criteria(), $context);

    $all = new Criteria();
    $repository->{|shopware.php.repository.unbounded_search:search|}($all, $context);

    $repository->search(new Criteria($ids), $context);
    $repository->search((new Criteria())->setLimit(10), $context);
    $repository->search($given, $context);
    $repository->searchIds(new Criteria(), $context);

    $limited = new Criteria();
    $limited->setLimit(50);
    $repository->search($limited, $context);

    $passed = new Criteria();
    restrict($passed);
    $repository->search($passed, $context);

    $other->search(new Criteria(), $context);
}
",
    );
}

const SMALL_TABLES: &str = r"<?php
namespace Acme;

use Shopware\Core\Checkout\Order\OrderCollection;
use Shopware\Core\Framework\DataAbstractionLayer\EntityRepository;
use Shopware\Core\Framework\DataAbstractionLayer\Search\Criteria;

class AppCollection {}
class AcmeShopFinderCollection {}

class Lists
{
    /**
     * @param EntityRepository<AppCollection> $typed
     * @param EntityRepository<AcmeShopFinderCollection> $finders
     */
    public function __construct(
        private EntityRepository $appRepository,
        private EntityRepository $salesChannelRepository,
        private EntityRepository $productRepository,
        private EntityRepository $countryRepository,
        private EntityRepository $acmeShopFinderRepository,
        private EntityRepository $repository,
        private EntityRepository $typed,
        private EntityRepository $finders,
    ) {}

    public function all(mixed $context, EntityRepository $languageRepository): void
    {
        // Configuration tables, by the name of the repository.
        $this->appRepository->search(new Criteria(), $context);
        $this->salesChannelRepository->search(new Criteria(), $context);
        $languageRepository->search(new Criteria(), $context);
        // By the collection the repository is typed with.
        $this->typed->search(new Criteria(), $context);

        // Shop data, tables of the project and unknown tables.
        $this->productRepository->{|shopware.php.repository.unbounded_search:search|}(new Criteria(), $context);
        $this->repository->{|shopware.php.repository.unbounded_search:search|}(new Criteria(), $context);
        COUNTRY
        FINDER_NAME
        FINDER_TYPE
    }

    /** @param EntityRepository<OrderCollection> $appRepository */
    public function typeWins(EntityRepository $appRepository, mixed $context): void
    {
        $appRepository->{|shopware.php.repository.unbounded_search:search|}(new Criteria(), $context);
    }
}
";

#[test]
fn small_configuration_tables_may_be_loaded_in_full() {
    let reported = |call: &str| {
        call.replace(
            "->search(",
            "->{|shopware.php.repository.unbounded_search:search|}(",
        )
    };
    let country = "$this->countryRepository->search(new Criteria(), $context);";
    let finder_name = "$this->acmeShopFinderRepository->search(new Criteria(), $context);";
    let finder_type = "$this->finders->search(new Criteria(), $context);";
    let fixture = |country: &str, name: &str, typed: &str| {
        SMALL_TABLES
            .replace("COUNTRY", country)
            .replace("FINDER_NAME", name)
            .replace("FINDER_TYPE", typed)
    };
    let uri = "file:///project/src/Lists.php";
    // Built in: countries are small, the project's own table is unknown.
    check(
        uri,
        &fixture(country, &reported(finder_name), &reported(finder_type)),
    );

    // The project adds its table and takes countries out.
    let (_dir, index) = preflight_index();
    let entities =
        swlsp_shopware::small_entities(&["acme_shop_finder".to_owned()], &["country".to_owned()]);
    let analyzer = ShopwarePhpSemanticAnalyzer::new(Some(index))
        .with_small_entities(std::sync::Arc::new(entities));
    check_problems(
        &analyzer,
        uri,
        &fixture(&reported(country), finder_name, finder_type),
    );
}

#[test]
fn system_config_is_not_read_while_a_service_is_built() {
    check(
        "file:///project/src/Config.php",
        r"<?php
namespace Acme;

use Shopware\Core\System\SystemConfig\SystemConfigService;

class Service
{
    private bool $enabled;

    public function __construct(private SystemConfigService $config)
    {
        $this->enabled = $config->{|shopware.php.system_config.constructor:getBool|}('Acme.config.enabled');
    }

    public function run(): void
    {
        $this->config->get('Acme.config.enabled');
    }
}
",
    );
}

#[test]
fn states_are_compared_by_technical_name() {
    check(
        "file:///project/src/States.php",
        r"<?php
namespace Acme;

use Shopware\Core\System\StateMachine\Aggregation\StateMachineState\StateMachineStateEntity;

function states(StateMachineStateEntity $state, object $other): void
{
    if ($state->{|shopware.php.state.translated_name:getName|}() === 'Paid') {}
    if ('Open' != ($state->{|shopware.php.state.translated_name:getName|}())) {}
    if ($state->getTechnicalName() === 'paid') {}
    echo $state->getName();
    if ($state->getName() === $other->getName()) {}
    if ($other->getName() === 'Paid') {}
}
",
    );
}

#[test]
fn queue_messages_carry_ids() {
    let messages = check(
        "file:///project/src/Messages.php",
        r"<?php
namespace Acme;

use Shopware\Core\Checkout\Order\OrderCollection;
use Shopware\Core\Checkout\Order\OrderEntity;
use Shopware\Core\Framework\MessageQueue\AsyncMessageInterface;
use Shopware\Core\Framework\MessageQueue\LowPriorityMessageInterface;
use Shopware\Core\System\SalesChannel\SalesChannelContext;

class ExportOrder implements AsyncMessageInterface
{
    public ?{|shopware.php.message.entity_payload:OrderCollection|} $orders = null;

    public function __construct(
        public readonly {|shopware.php.message.entity_payload:OrderEntity|} $order,
        public readonly {|shopware.php.message.entity_payload:SalesChannelContext|} $context,
        public readonly string $orderId,
    ) {}

    public function with(OrderEntity $order): void {}
}

class Cleanup implements LowPriorityMessageInterface
{
    public function __construct(public readonly {|shopware.php.message.entity_payload:OrderEntity|} $order) {}
}

class Sync
{
    public function __construct(public readonly OrderEntity $order) {}
}
",
    );
    assert_eq!(
        messages[0],
        "A queue message is serialized and handled later: carry the ID instead of OrderCollection, which may be stale or too large by then"
    );
}

#[test]
fn get_routes_do_not_change_data() {
    check(
        "file:///project/src/Controller.php",
        r"<?php
namespace Acme;

use Shopware\Core\Framework\DataAbstractionLayer\EntityRepository;
use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\Routing\Attribute\Route;

#[Route(defaults: ['_routeScope' => ['storefront']])]
class StorefrontController
{
    public function __construct(private EntityRepository $repository) {}

    #[Route(path: '/acme/confirm/{id}', name: 'frontend.acme.confirm', methods: ['GET'])]
    public function confirm(string $id, mixed $context): void
    {
        $this->repository->{|shopware.php.route.get_mutation:update|}([['id' => $id]], $context);
        $this->repository->{|shopware.php.route.get_mutation:delete|}([['id' => $id]], $context);
        // Logging a visit is not a business operation.
        $this->repository->create([['id' => $id]], $context);
    }

    #[Route(path: '/acme/confirm/{id}', name: 'frontend.acme.confirm.post', methods: [Request::METHOD_POST])]
    public function confirmPost(string $id, mixed $context): void
    {
        $this->repository->update([['id' => $id]], $context);
    }

    #[Route(path: '/acme/both', name: 'frontend.acme.both', methods: ['GET', 'POST'])]
    public function both(string $id, mixed $context): void
    {
        $this->repository->update([['id' => $id]], $context);
    }

    public function helper(string $id, mixed $context): void
    {
        $this->repository->delete([['id' => $id]], $context);
    }
}
",
    );
}

#[test]
fn debug_output_working_directory_and_empty_catch_blocks() {
    let messages = check(
        "file:///project/src/Leftovers.php",
        r"<?php
namespace Acme;

function leftovers(array $data): string
{
    {|shopware.php.function.debug_output:print_r|}($data);
    {|shopware.php.function.debug_output:var_export|}($data, false);
    {|shopware.php.function.debug_output:\phpinfo|}();
    $text = print_r($data, true) . var_export($data, return: true);
    $path = {|shopware.php.filesystem.getcwd:getcwd|}() . '/files';

    try {
        risky();
    } catch (\Throwable $e) {|shopware.php.catch.empty:{
    }|}

    try {
        risky();
    } catch (\Exception) {
        // The export is optional.
    }

    try {
        risky();
    } catch (NotFoundException) {
    }

    try {
        risky();
    } catch (\Throwable $e) {
        log($e);
    }

    return $text . $path;
}
",
    );
    assert_eq!(messages.len(), 5);
}

#[test]
fn admin_api_write_routes_name_their_privileges() {
    let messages = check(
        "file:///project/src/ApiController.php",
        r"<?php
namespace Acme;

use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\Routing\Attribute\Route;

#[Route(defaults: ['_routeScope' => ['api']])]
class ApiController
{
    #[Route(path: '/api/_action/acme/import', name: 'api.action.acme.import', methods: ['POST'])]
    public function {|shopware.php.route.acl_missing:import|}(): void {}

    #[Route(path: '/api/_action/acme/remove', name: 'api.action.acme.remove', methods: [Request::METHOD_GET, 'DELETE'])]
    public function {|shopware.php.route.acl_missing:remove|}(): void {}

    #[Route(path: '/api/_action/acme/export', name: 'api.action.acme.export', defaults: ['_acl' => ['acme_export:create']], methods: ['POST'])]
    public function export(): void {}

    // Opted out: the route states that it needs no privilege.
    #[Route(path: '/api/_action/acme/preview', name: 'api.action.acme.preview', defaults: ['_acl' => []], methods: ['POST'])]
    public function preview(): void {}

    #[Route(path: '/api/_action/acme/status', name: 'api.action.acme.status', methods: ['GET'])]
    public function status(): void {}

    #[Route(path: '/api/_action/acme/any', name: 'api.action.acme.any')]
    public function anyMethod(): void {}

    public function helper(): void {}
}

#[Route(defaults: ['_routeScope' => ['api'], '_acl' => ['acme:update']])]
class GuardedController
{
    #[Route(path: '/api/_action/acme/sync', name: 'api.action.acme.sync', methods: ['POST'])]
    public function sync(): void {}
}

#[Route(defaults: ['_routeScope' => ['storefront']])]
class StorefrontController
{
    #[Route(path: '/acme/save', name: 'frontend.acme.save', methods: ['POST'])]
    public function save(): void {}
}

class MethodScoped
{
    #[Route(path: '/api/_action/acme/run', name: 'api.action.acme.run', defaults: ['_routeScope' => ['api']], methods: ['POST'])]
    public function {|shopware.php.route.acl_missing:run|}(): void {}
}
",
    );
    assert_eq!(
        messages[0],
        "This Admin API route accepts writes from any authenticated user; name the privileges it needs in the _acl route default, or set it to [] when it needs none"
    );
}
