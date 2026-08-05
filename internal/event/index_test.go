package event

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexPHPSubscribersAttributesConstantsAndDispatches(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	idx.SetPHPIndex(phpIndex)

	subscriberFile := indexer.NewParsedFile(
		"/project/src/Subscriber.php",
		[]byte(`<?php
namespace App;

use App\Event\OrderPlaced;
use Symfony\Component\EventDispatcher\Attribute\AsEventListener;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Symfony\Component\HttpKernel\KernelEvents;

class Subscriber implements EventSubscriberInterface
{
    public static function getSubscribedEvents(): array
    {
        return [
            KernelEvents::REQUEST => [
                ['onRequest', 40],
                ['auditRequest'],
            ],
            OrderPlaced::class => 'onOrder',
            'app.manual' => ['onManual', -10],
        ];
    }

    public function onRequest(\Symfony\Component\HttpKernel\Event\RequestEvent $event): void {}
    public function auditRequest(): void {}
    public function onOrder(OrderPlaced $event): void {}
    public function onManual(OrderPlaced $event): void {}
}

#[AsEventListener(event: 'app.attribute', method: 'handleAttribute', priority: 20)]
class AttributeListener
{
    public function handleAttribute(OrderPlaced $event): void {}
}

class MethodListener
{
    #[AsEventListener]
    public function onOrderPlaced(OrderPlaced $event): void {}
}

function publish($eventDispatcher): void
{
    $eventDispatcher->dispatch(new OrderPlaced());
    $eventDispatcher->dispatch(new OrderPlaced(), 'app.named');
    $eventDispatcher->dispatch('app.legacy', new OrderPlaced());
}`),
	)
	require.NoError(t, phpIndex.Index(subscriberFile))
	require.NoError(t, idx.Index(subscriberFile))

	placed, found, err := idx.GetEvent("App\\Event\\OrderPlaced")
	require.NoError(t, err)
	require.True(t, found)
	assertListener(t, placed, "App\\Subscriber", "onOrder")
	assertListener(t, placed, "App\\MethodListener", "onOrderPlaced")
	require.NotEmpty(t, placed.Dispatches())

	manual, found, err := idx.GetEvent("app.manual")
	require.NoError(t, err)
	require.True(t, found)
	listener := assertListener(t, manual, "App\\Subscriber", "onManual")
	assert.Equal(t, "-10", listener.Priority)
	assert.Equal(t, "App\\Event\\OrderPlaced", listener.EventType)

	attribute, found, err := idx.GetEvent("app.attribute")
	require.NoError(t, err)
	require.True(t, found)
	listener = assertListener(
		t,
		attribute,
		"App\\AttributeListener",
		"handleAttribute",
	)
	assert.Equal(t, "20", listener.Priority)

	named, found, err := idx.GetEvent("app.named")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "App\\Event\\OrderPlaced", named.EventType)

	legacy, found, err := idx.GetEvent("app.legacy")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "App\\Event\\OrderPlaced", legacy.EventType)

	_, found, err = idx.GetEvent("kernel.request")
	require.NoError(t, err)
	require.False(t, found, "constant must remain resolvable after later files")

	constantRoot := t.TempDir()
	constantPath := filepath.Join(
		constantRoot,
		"vendor",
		"symfony",
		"http-kernel",
		"KernelEvents.php",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(constantPath), 0o755))
	constantSource := []byte(`<?php
namespace Symfony\Component\HttpKernel;
final class KernelEvents
{
    public const REQUEST = 'kernel.request';
}`)
	require.NoError(t, os.WriteFile(constantPath, constantSource, 0o644))
	constantFile := indexer.NewParsedFile(constantPath, constantSource)
	require.NoError(t, phpIndex.Index(constantFile))
	request, found, err := idx.GetEvent("kernel.request")
	require.NoError(t, err)
	require.True(t, found)
	assertListener(t, request, "App\\Subscriber", "onRequest")
	assertListener(t, request, "App\\Subscriber", "auditRequest")
	assert.Equal(
		t,
		"Symfony\\Component\\HttpKernel\\Event\\RequestEvent",
		request.EventType,
	)
}

func TestIndexServiceListenerTags(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/config/services.php",
		[]byte(`<?php
use App\Listener\ConfigListener;
$services->set(ConfigListener::class)
    ->tag('kernel.event_listener', [
        'event' => 'app.php',
        'method' => 'onPHP',
        'priority' => 100,
    ]);`),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/config/services.yaml",
		[]byte(`services:
    App\Listener\YamlListener:
        tags:
            - { name: kernel.event_listener, event: app.yaml, method: onYaml, priority: 10 }
`),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/config/services.xml",
		[]byte(`<container><services>
  <service id="app.xml_listener" class="App\Listener\XmlListener">
    <tag name="kernel.event_listener" event="app.xml" method="onXml" priority="-5"/>
  </service>
</services></container>`),
	)))

	phpEvent, found, err := idx.GetEvent("app.php")
	require.NoError(t, err)
	require.True(t, found)
	listener := assertListener(
		t,
		phpEvent,
		"App\\Listener\\ConfigListener",
		"onPHP",
	)
	assert.Equal(t, "100", listener.Priority)

	yamlEvent, found, err := idx.GetEvent("app.yaml")
	require.NoError(t, err)
	require.True(t, found)
	listener = assertListener(
		t,
		yamlEvent,
		"App\\Listener\\YamlListener",
		"onYaml",
	)
	assert.Equal(t, "10", listener.Priority)

	xmlEvent, found, err := idx.GetEvent("app.xml")
	require.NoError(t, err)
	require.True(t, found)
	listener = assertListener(
		t,
		xmlEvent,
		"App\\Listener\\XmlListener",
		"onXml",
	)
	assert.Equal(t, "-5", listener.Priority)
}

func TestEventDispatcherCallReturnsConcreteEventType(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	phpIndex.RegisterTypeExtension(NewPHPTypeExtension())
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/EventDispatcher.php",
		[]byte(`<?php
namespace Symfony\Contracts\EventDispatcher;
interface EventDispatcherInterface {
    public function dispatch(object $event, ?string $eventName = null): object;
}`),
	)))
	source := `<?php
namespace App;
use Symfony\Contracts\EventDispatcher\EventDispatcherInterface;
class DomainEvent {}
function publish(EventDispatcherInterface $dispatcher): void {
    $modern = $dispatcher->dispatch(new DomainEvent(), 'app.modern');
    $legacy = $dispatcher->dispatch('app.legacy', new DomainEvent());
}`
	parsed := indexer.NewParsedFile(
		"/project/src/Publisher.php",
		[]byte(source),
	)
	document := phpIndex.AnalyzeDocument(
		parsed.Path,
		1,
		parsed.SyntaxTree().Root,
	)
	var dispatches int
	for _, call := range phpquery.Calls(parsed.SyntaxTree().Root) {
		if !strings.EqualFold(phpquery.CallMethodName(call), "dispatch") {
			continue
		}
		dispatches++
		assert.Equal(
			t,
			"App\\DomainEvent",
			document.TypeOf(call).Type.String(),
		)
	}
	assert.Equal(t, 2, dispatches)
}

func TestIndexSkipsMessengerDispatches(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/src/Publisher.php",
		[]byte(`<?php
namespace App;
use Symfony\Contracts\EventDispatcher\EventDispatcherInterface;
use Symfony\Component\Messenger\MessageBusInterface;
class DomainEvent {}
class Command {}
function publish(
    EventDispatcherInterface $eventDispatcher,
    MessageBusInterface $dispatcher,
): void {
    $eventDispatcher->dispatch (new DomainEvent());
    $dispatcher->dispatch(new Command());
}`),
	)))
	events, err := idx.GetEvents()
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "App\\DomainEvent", events[0].Name)
}

func TestIndexClearsPersistedPathAfterCandidateRemoval(t *testing.T) {
	configDir := t.TempDir()
	path := "/project/src/Listener.php"
	idx, err := NewIndex(configDir)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte(`<?php
namespace App;
#[\Symfony\Component\EventDispatcher\Attribute\AsEventListener(event: 'app.ready')]
class Listener { public function __invoke(): void {} }`),
	)))
	_, found, err := idx.GetEvent("app.ready")
	require.NoError(t, err)
	require.True(t, found)
	require.NoError(t, idx.Close())

	reopened, err := NewIndex(configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	require.NoError(t, reopened.Index(indexer.NewParsedFile(
		path,
		[]byte(`<?php namespace App; class Listener {}`),
	)))
	_, found, err = reopened.GetEvent("app.ready")
	require.NoError(t, err)
	require.False(t, found)
}

func assertListener(
	t *testing.T,
	event Event,
	class,
	method string,
) Occurrence {
	t.Helper()
	for _, listener := range event.Listeners() {
		if listener.Class == class && listener.Method == method {
			return listener
		}
	}
	t.Fatalf(
		"listener %s::%s missing from %#v",
		class,
		method,
		event.Listeners(),
	)
	return Occurrence{}
}
