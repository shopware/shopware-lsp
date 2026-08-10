package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/require"
)

func TestShopwarePHPSemanticAnalyzerReportsTypeAwareRules(t *testing.T) {
	phpIndex := shopwarePHPSemanticTestIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/Consumer.php",
		`<?php
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

    \Shopware\Core\Internal\internal_helper();
    $internal->secret();
    (new UserEntity())->getStoreToken();
}
`,
		1,
	)
	require.Empty(t, document.ParseErrors)
	problems, err := NewShopwarePHPSemanticAnalyzer(phpIndex).Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Equal(t, map[lsp.DiagnosticID]int{
		ShopwarePHPRepositoryInLoopCode:           2,
		ShopwarePHPInternalClassExtensionCode:     1,
		ShopwarePHPInternalFunctionCallCode:       1,
		ShopwarePHPInternalMethodCallCode:         1,
		ShopwarePHPSessionConstructorCode:         1,
		ShopwarePHPSessionPaymentHandlerCode:      1,
		ShopwarePHPSessionStoreAPICode:            1,
		ShopwarePHPScheduledTaskIntervalCode:      1,
		ShopwarePHPUserStoreTokenCode:             1,
		ShopwarePHPConcreteDecoratorExtensionCode: 1,
	}, problemCounts(problems), problemHighlights(document, problems))
	for _, problem := range problems {
		highlight := document.Source[problem.Range.Start:problem.Range.End]
		require.NotEmpty(t, highlight)
		switch problem.ID {
		case ShopwarePHPInternalClassExtensionCode:
			require.Equal(t, "InternalBase", highlight)
		case ShopwarePHPConcreteDecoratorExtensionCode:
			require.Equal(t, "Core", highlight)
		}
	}
}

func TestShopwarePHPSemanticAnalyzerAllowsSafeAndSamePackageUsage(t *testing.T) {
	phpIndex := shopwarePHPSemanticTestIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/Safe.php",
		`<?php
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
`,
		1,
	)
	problems, err := NewShopwarePHPSemanticAnalyzer(phpIndex).Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Empty(t, problems, problemHighlights(document, problems))

	shopwareDocument := lsp.NewTextDocument(
		"file:///project/src/ShopwareInternalUse.php",
		`<?php
namespace Shopware\Core\DifferentPackage;
use Shopware\Core\Internal\InternalService;
function useInternalMethod(InternalService $service): void { $service->secret(); }
`,
		1,
	)
	problems, err = NewShopwarePHPSemanticAnalyzer(phpIndex).Analyze(
		context.Background(),
		shopwareDocument,
	)
	require.NoError(t, err)
	require.Empty(t, problems, problemHighlights(shopwareDocument, problems))
}

func TestShopwarePHPSemanticAnalyzerHonorsRuleSuppression(t *testing.T) {
	phpIndex := shopwarePHPSemanticTestIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/Suppressed.php",
		`<?php
use Shopware\Core\System\User\UserEntity;

/** @phpstan-ignore-next-line shopware.php.user.store_token */
(new UserEntity())->getStoreToken();
`,
		1,
	)
	problems, err := NewShopwarePHPSemanticAnalyzer(phpIndex).Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Empty(t, problems)
}

func shopwarePHPSemanticTestIndex(t *testing.T) *php.PHPIndex {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	fixtures := map[string]string{
		"/project/vendor/EntityRepository.php": `<?php
namespace Shopware\Core\Framework\DataAbstractionLayer;
class EntityRepository { public function search(mixed $criteria): void {} }`,
		"/project/vendor/SessionInterface.php": `<?php
namespace Symfony\Component\HttpFoundation\Session;
interface SessionInterface { public function get(string $key): mixed; }`,
		"/project/vendor/PlatformRequest.php": `<?php
namespace Shopware\Core;
class PlatformRequest { public const ATTRIBUTE_ROUTE_SCOPE = '_routeScope'; }`,
		"/project/vendor/StoreApiRouteScope.php": `<?php
namespace Shopware\Core\Framework\Routing;
class StoreApiRouteScope { public const ID = 'store-api'; }`,
		"/project/vendor/PaymentHandler.php": `<?php
namespace Shopware\Core\Checkout\Payment\Cart\PaymentHandler;
abstract class AbstractPaymentHandler {}`,
		"/project/vendor/ScheduledTask.php": `<?php
namespace Shopware\Core\Framework\MessageQueue\ScheduledTask;
abstract class ScheduledTask { abstract public static function getDefaultInterval(): int; }`,
		"/project/vendor/UserEntity.php": `<?php
namespace Shopware\Core\System\User;
class UserEntity { public function getStoreToken(): string { return ''; } }`,
		"/project/vendor/InternalBase.php": `<?php
namespace Shopware\Core\Internal;
/** @internal */
class InternalBase {}`,
		"/project/vendor/InternalService.php": `<?php
namespace Shopware\Core\Internal;
class InternalService {
    /** @internal */
    public function secret(): void {}
}`,
		"/project/vendor/InternalFunction.php": `<?php
namespace Shopware\Core\Internal;
/** @internal */
function internal_helper(): void {}`,
		"/project/vendor/AbstractCore.php": `<?php
namespace Shopware\Core\Test\Decorator;
abstract class AbstractCore { abstract public function getDecorated(): AbstractCore; }`,
		"/project/vendor/Core.php": `<?php
namespace Shopware\Core\Test\Decorator;
class Core extends AbstractCore { public function getDecorated(): AbstractCore { return $this; } }`,
	}
	for path, source := range fixtures {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, []byte(source))))
	}
	return phpIndex
}
