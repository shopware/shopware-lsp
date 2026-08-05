package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventAndSubscriberMethodCompletionIsTypeAware(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/EventDispatcher.php",
		[]byte(eventDispatcherStubs),
	)))
	eventIndex, err := event.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, eventIndex.Close()) })
	eventIndex.SetPHPIndex(phpIndex)

	source := `<?php
namespace App;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Symfony\Contracts\EventDispatcher\EventDispatcherInterface;
class DomainEvent {}
class Subscriber implements EventSubscriberInterface {
    public static function getSubscribedEvents(): array {
        return ['app.ready' => ''];
    }
    public function onReady(DomainEvent $event): void {}
}
function publish(EventDispatcherInterface $dispatcher): void {
    $dispatcher->dispatch(new DomainEvent(), '');
}`
	path := "/project/src/EventFeature.php"
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, eventIndex.Index(parsed))
	document := lsp.NewTextDocument("file://"+path, source, 1)
	provider := NewEventCompletionProvider(eventIndex, phpIndex, nil)

	for _, test := range []struct {
		needle string
		label  string
	}{
		{"'app.ready' => ''", "onReady"},
		{"dispatch(new DomainEvent(), '')", "app.ready"},
	} {
		offset := strings.Index(source, test.needle) +
			strings.LastIndex(test.needle, "''") + 1
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			document.SyntaxTree.Root,
		)
		items := provider.GetCompletions(
			ctx,
			consoleCompletionRequest(document, node),
		)
		requireCompletion(t, items, test.label)
	}

	untyped := `<?php
function publish($messageBus): void {
    $messageBus->dispatch(new \stdClass(), '');
}`
	untypedDocument := lsp.NewTextDocument(
		"file:///project/src/Message.php",
		untyped,
		1,
	)
	untypedNode := untypedDocument.SyntaxTree.Root.NodeAtOffset(
		uint32(strings.LastIndex(untyped, "''") + 1),
	)
	untypedContext := phpIndex.AddDocumentContext(
		context.Background(),
		"/project/src/Message.php",
		1,
		untypedNode,
		untypedDocument.SyntaxTree.Root,
	)
	assert.Empty(t, provider.GetCompletions(
		untypedContext,
		consoleCompletionRequest(untypedDocument, untypedNode),
	))
}

func TestEventConfigurationCompletionForYAMLAndXML(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	eventIndex, err := event.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, eventIndex.Close()) })
	eventIndex.SetPHPIndex(phpIndex)

	listenerSource := `<?php
namespace App;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
class ConfigListener {
    public function onConfig(): void {}
}
class Subscriber implements EventSubscriberInterface {
    public static function getSubscribedEvents(): array {
        return ['app.ready' => 'handle'];
    }
    public function handle(): void {}
}`
	listenerPath := "/project/src/ConfigListener.php"
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/EventSubscriber.php",
		[]byte(`<?php
namespace Symfony\Component\EventDispatcher;
interface EventSubscriberInterface {}`),
	)))
	parsedListener := indexer.NewParsedFile(
		listenerPath,
		[]byte(listenerSource),
	)
	require.NoError(t, phpIndex.Index(parsedListener))
	require.NoError(t, eventIndex.Index(parsedListener))

	for _, test := range []struct {
		path       string
		source     string
		methodMark string
		eventMark  string
	}{
		{
			path: "/project/config/services.yaml",
			source: `services:
    App\ConfigListener:
        tags:
            - { name: kernel.event_listener, event: '', method: '' }
`,
			methodMark: "method: ''",
			eventMark:  "event: ''",
		},
		{
			path: "/project/config/services.xml",
			source: `<container><services>
  <service id="App\ConfigListener">
    <tag name="kernel.event_listener" event="" method=""/>
  </service>
</services></container>`,
			methodMark: `method=""`,
			eventMark:  `event=""`,
		},
	} {
		parsed := indexer.NewParsedFile(
			test.path,
			[]byte(test.source),
		)
		require.NoError(t, eventIndex.Index(parsed))
		document := lsp.NewTextDocument(
			"file://"+test.path,
			test.source,
			1,
		)
		provider := NewEventCompletionProvider(
			eventIndex,
			phpIndex,
			nil,
		)

		methodOffset := strings.Index(test.source, test.methodMark) +
			strings.IndexAny(test.methodMark, "'\"") + 1
		methodNode := document.SyntaxTree.Root.NodeAtOffset(
			uint32(methodOffset),
		)
		methodItems := provider.GetCompletions(
			context.Background(),
			consoleCompletionRequest(document, methodNode),
		)
		requireCompletion(t, methodItems, "onConfig")

		eventOffset := strings.Index(test.source, test.eventMark) +
			strings.IndexAny(test.eventMark, "'\"") + 1
		eventNode := document.SyntaxTree.Root.NodeAtOffset(
			uint32(eventOffset),
		)
		eventItems := provider.GetCompletions(
			context.Background(),
			consoleCompletionRequest(document, eventNode),
		)
		requireCompletion(t, eventItems, "app.ready")
	}
}

const eventDispatcherStubs = `<?php
namespace Symfony\Component\EventDispatcher;
interface EventSubscriberInterface {}
namespace Symfony\Contracts\EventDispatcher;
interface EventDispatcherInterface {
    public function dispatch(object $event, ?string $eventName = null): object;
}`
