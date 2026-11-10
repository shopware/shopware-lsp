//! The store-review rules about contexts, system config, route listeners,
//! Symfony HTTP objects and the local disk.

use swlsp_php::PhpIndex;
use tempfile::TempDir;

use super::php_semantic::php_index_with;
use crate::diagnostics::shopware::*;
use crate::diagnostics::test_support::check_problems;

fn review_index() -> (TempDir, PhpIndex) {
    php_index_with(&[
        (
            "/project/vendor/Context.php",
            r"<?php
namespace Shopware\Core\Framework;
class Context
{
    public static function createDefaultContext(): self { return new self(); }
    public static function createCLIContext(): self { return new self(); }
}",
        ),
        (
            "/project/vendor/SystemConfigService.php",
            r"<?php
namespace Shopware\Core\System\SystemConfig;
class SystemConfigService
{
    public function get(string $key, ?string $salesChannelId = null): mixed { return null; }
    public function getString(string $key, ?string $salesChannelId = null): string { return ''; }
    public function set(string $key, mixed $value, ?string $salesChannelId = null): void {}
}",
        ),
        (
            "/project/vendor/SalesChannelContext.php",
            r"<?php
namespace Shopware\Core\System\SalesChannel;
class SalesChannelEntity { public function getId(): string { return ''; } }
class SalesChannelContext
{
    public function getSalesChannelId(): string { return ''; }
    public function getSalesChannel(): SalesChannelEntity { return new SalesChannelEntity(); }
}",
        ),
        (
            "/project/vendor/HttpFoundation.php",
            r"<?php
namespace Symfony\Component\HttpFoundation;
class Cookie
{
    public function __construct(string $name, ?string $value = null, int $expire = 0, ?string $path = '/', ?string $domain = null, ?bool $secure = null, bool $httpOnly = true) {}
    public static function create(string $name, ?string $value = null, int $expire = 0, ?string $path = '/', ?string $domain = null, ?bool $secure = null): static { return new static($name); }
    public function withSecure(bool $secure = true): static { return $this; }
    public function withValue(?string $value): static { return $this; }
}
class Response
{
    public const HTTP_NO_CONTENT = 204;
    public function __construct(?string $content = '', int $status = 200, array $headers = []) {}
}
class JsonResponse extends Response
{
    public function __construct(mixed $data = null, int $status = 200, array $headers = []) {}
}
class RedirectResponse extends Response
{
    public function __construct(string $url, int $status = 302) {}
}
class ParameterBag { public function get(string $key): mixed { return null; } }
class Request { public ParameterBag $attributes; }",
        ),
        (
            "/project/vendor/HttpKernel.php",
            r"<?php
namespace Symfony\Component\HttpKernel\Event;
use Symfony\Component\HttpFoundation\Request;
class RequestEvent { public function getRequest(): Request { return new Request(); } }
class ResponseEvent { public function getRequest(): Request { return new Request(); } }",
        ),
        (
            "/project/vendor/KernelEvents.php",
            r"<?php
namespace Symfony\Component\HttpKernel;
final class KernelEvents
{
    public const REQUEST = 'kernel.request';
    public const RESPONSE = 'kernel.response';
    public const EXCEPTION = 'kernel.exception';
}",
        ),
        (
            "/project/vendor/AsEventListener.php",
            r"<?php
namespace Symfony\Component\EventDispatcher\Attribute;
#[\Attribute]
class AsEventListener
{
    public function __construct(public ?string $event = null, public ?string $method = null) {}
}",
        ),
        (
            "/project/vendor/Filesystem.php",
            r"<?php
namespace Symfony\Component\Filesystem;
class Filesystem
{
    public function dumpFile(string $filename, string $content): void {}
    public function copy(string $originFile, string $targetFile): void {}
    public function exists(string $files): bool { return true; }
}",
        ),
        (
            "/project/vendor/ZipArchive.php",
            r"<?php
class ZipArchive
{
    public const CREATE = 1;
    public const OVERWRITE = 8;
    public const RDONLY = 16;
    public function open(string $filename, int $flags = 0): bool { return true; }
}",
        ),
    ])
}

fn check(uri: &str, fixture: &str) -> Vec<String> {
    let (_dir, index) = review_index();
    let (document, problems) =
        check_problems(&ShopwarePhpSemanticAnalyzer::new(Some(index)), uri, fixture);
    assert!(document.parse_errors().is_empty());
    problems
        .into_iter()
        .map(|problem| problem.message)
        .collect()
}

#[test]
fn default_contexts_are_reported_once_a_cli_context_exists() {
    check(
        "file:///project/src/Contexts.php",
        r"<?php
namespace Acme;

use Shopware\Core\Framework\Context;

Context::{|shopware.php.context.default:createDefaultContext|}();
\Shopware\Core\Framework\Context::{|shopware.php.context.default:createDefaultContext|}();
Context::createCLIContext();
Other::createDefaultContext();
",
    );
    // Before 6.6 there is nothing to replace it with on the command line.
    let (_dir, index) = php_index_with(&[(
        "/project/vendor/Context.php",
        r"<?php
namespace Shopware\Core\Framework;
class Context { public static function createDefaultContext(): self { return new self(); } }",
    )]);
    check_problems(
        &ShopwarePhpSemanticAnalyzer::new(Some(index)),
        "file:///project/src/Contexts.php",
        "<?php\n\\Shopware\\Core\\Framework\\Context::createDefaultContext();\n",
    );
}

#[test]
fn system_config_reads_forward_the_sales_channel() {
    let messages = check(
        "file:///project/src/Config.php",
        r"<?php
namespace Acme;

use Shopware\Core\System\SalesChannel\SalesChannelContext;
use Shopware\Core\System\SystemConfig\SystemConfigService;

class Reader
{
    public function __construct(private SystemConfigService $config) {}

    public function scoped(SalesChannelContext $context, string $id, ?string $maybe): void
    {
        $this->config->{|shopware.php.system_config.sales_channel:get|}('Acme.config.key');
        $this->config->getString('Acme.config.key', $context->getSalesChannelId());
        $this->config->get('Acme.config.key', $context->getSalesChannel()->getId());
        $this->config->get('Acme.config.key', salesChannelId: $id);
        $this->config->get('Acme.config.key', {|shopware.php.system_config.sales_channel:null|});
        $this->config->get('Acme.config.key', {|shopware.php.system_config.sales_channel:$maybe|});
        $this->config->set('Acme.config.key', 1);
        $read = fn () => $this->config->{|shopware.php.system_config.sales_channel:getString|}('Acme.config.key');
    }

    public function unscoped(): void
    {
        $this->config->get('Acme.config.key');
    }
}
",
    );
    assert_eq!(
        messages[0],
        "SystemConfigService::get() reads the global value; pass the sales channel ID of the SalesChannelContext"
    );
}

#[test]
fn kernel_listeners_checking_the_route_should_use_the_route_event() {
    let messages = check(
        "file:///project/src/Listeners.php",
        r"<?php
namespace Acme;

use Symfony\Component\EventDispatcher\Attribute\AsEventListener;
use Symfony\Component\HttpKernel\Event\RequestEvent;
use Symfony\Component\HttpKernel\Event\ResponseEvent;
use Symfony\Component\HttpKernel\KernelEvents;

class Subscriber
{
    public static function getSubscribedEvents(): array
    {
        return [
            KernelEvents::REQUEST => 'onRequest',
            'kernel.response' => ['onResponse', 10],
            ResponseEvent::class => [['onLate', -10]],
            KernelEvents::EXCEPTION => 'onException',
        ];
    }

    public function {|shopware.php.listener.route_condition:onRequest|}(RequestEvent $event): void
    {
        if ($event->getRequest()->attributes->get('_route') !== 'frontend.home.page') {
            return;
        }
    }

    public function {|shopware.php.listener.route_condition:onResponse|}(ResponseEvent $event): void
    {
        if ('frontend.detail.page' != $event->getRequest()->attributes->get('_route')) {
            return;
        }
    }

    public function {|shopware.php.listener.route_condition:onLate|}(ResponseEvent $event): void
    {
        if ($event->getRequest()->attributes->get('_route') !== 'frontend.cart.page') {
            return;
        }
    }

    public function onException(object $event): void
    {
        if ($event->getRequest()->attributes->get('_route') !== 'frontend.home.page') {
            return;
        }
    }

    public function unrelated(RequestEvent $event): void
    {
        if ($event->getRequest()->attributes->get('_route') !== 'frontend.home.page') {
            return;
        }
    }
}

#[AsEventListener(event: 'kernel.request', method: 'handle')]
class ClassListener
{
    public function {|shopware.php.listener.route_condition:handle|}(RequestEvent $event): void
    {
        if ($event->getRequest()->attributes->get('_route') !== 'api.action.sync') {
            return;
        }
    }
}

class MethodListener
{
    #[AsEventListener]
    public function {|shopware.php.listener.route_condition:byParameter|}(RequestEvent $event): void
    {
        if ($event->getRequest()->attributes->get('_route') !== 'frontend.home.page') {
            return;
        }
    }

    #[AsEventListener(event: KernelEvents::RESPONSE)]
    public function allRoutes(ResponseEvent $event): void
    {
        if ($event->getRequest()->attributes->get('_route') === 'frontend.home.page') {
            return;
        }
    }
}
",
    );
    assert_eq!(
        messages[..2],
        [
            "Listen to frontend.home.page.request instead of kernel.request; the listener then only runs for that route",
            "Listen to frontend.detail.page.response instead of kernel.response; the listener then only runs for that route",
        ]
    );
}

#[test]
fn symfony_cookies_need_the_secure_flag() {
    check(
        "file:///project/src/Cookies.php",
        r"<?php
namespace Acme;

use Symfony\Component\HttpFoundation\Cookie;

function cookies(Cookie $existing, bool $secure): void
{
    new {|shopware.php.cookie.symfony_insecure:Cookie|}('a');
    new {|shopware.php.cookie.symfony_insecure:Cookie|}('a', 'b', 0, '/', null, false);
    new {|shopware.php.cookie.symfony_insecure:Cookie|}('a', secure: $secure);
    new {|shopware.php.cookie.symfony_insecure:Cookie|}('a', httpOnly: true);
    new Cookie('a', 'b', 0, '/', null, true);
    new Cookie('a', secure: true);
    Cookie::{|shopware.php.cookie.symfony_insecure:create|}('a');
    Cookie::create('a', secure: true);
    Cookie::create('a')->withSecure();
    Cookie::create('a')->withValue('b')->withSecure(true);
    Cookie::{|shopware.php.cookie.symfony_insecure:create|}('a')->withSecure(false);
    Cookie::create('a', secure: true)->withSecure({|shopware.php.cookie.symfony_insecure:false|});
    $existing->withSecure({|shopware.php.cookie.symfony_insecure:$secure|});
    $existing->withSecure();
    $other->withSecure(false);
}
",
    );
}

#[test]
fn responses_need_a_body_or_a_status_without_one() {
    check(
        "file:///project/src/Responses.php",
        r"<?php
namespace Acme;

use Symfony\Component\HttpFoundation\JsonResponse;
use Symfony\Component\HttpFoundation\RedirectResponse;
use Symfony\Component\HttpFoundation\Response;

function responses(string $content, array $arguments): void
{
    new {|shopware.php.response.empty:Response|}();
    new {|shopware.php.response.empty:Response|}('');
    new {|shopware.php.response.empty:Response|}(null, 200);
    new {|shopware.php.response.empty:Response|}(status: 500);
    new {|shopware.php.response.empty:JsonResponse|}(null);
    new Response('', 204);
    new Response(null, Response::HTTP_NO_CONTENT);
    new Response(status: 304);
    new Response($content);
    new Response('ok');
    new Response(...$arguments);
    new JsonResponse();
    new JsonResponse([]);
    new RedirectResponse('/');
}
",
    );
}

#[test]
fn files_are_written_through_flysystem_or_to_the_temporary_directory() {
    let messages = check(
        "file:///project/src/Files.php",
        r"<?php
namespace Acme;

use Symfony\Component\Filesystem\Filesystem;

function files(Filesystem $filesystem, \ZipArchive $zip, string $path, string $tempFile): void
{
    {|shopware.php.filesystem.local_write:file_put_contents|}($path, 'data');
    {|shopware.php.filesystem.local_write:\mkdir|}(__DIR__ . '/cache');
    {|shopware.php.filesystem.local_write:unlink|}($path);
    {|shopware.php.filesystem.local_write:rmdir|}($path);
    {|shopware.php.filesystem.local_write:symlink|}($tempFile, $path);
    {|shopware.php.filesystem.local_write:rename|}($tempFile, $path);
    {|shopware.php.filesystem.local_write:fopen|}($path, 'wb');
    fopen($path, 'r');
    fopen('php://stdout', 'w');
    fopen(STDERR, 'w');
    file_put_contents(sys_get_temp_dir() . '/export.csv', 'data');
    file_put_contents($tempFile, 'data');
    rename($tempFile, sys_get_temp_dir() . '/done');
    file_get_contents($path);
    Helper\mkdir($path);

    $filesystem->{|shopware.php.filesystem.local_write:dumpFile|}($path, 'data');
    $filesystem->{|shopware.php.filesystem.local_write:copy|}($tempFile, $path);
    $filesystem->dumpFile($tempFile, 'data');
    $filesystem->exists($path);

    $zip->{|shopware.php.filesystem.local_write:open|}($path, \ZipArchive::CREATE | \ZipArchive::OVERWRITE);
    $zip->open($path, \ZipArchive::RDONLY);
    $zip->open($path);
    $zip->open($tempFile, \ZipArchive::CREATE);
    $other->open($path, 1);
}
",
    );
    assert_eq!(
        messages[0],
        "file_put_contents() writes to the local disk; use Flysystem, or a path below sys_get_temp_dir() for temporary files"
    );
}
