package messenger

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexModernHandlersAndTypedDispatches(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	idx.SetPHPIndex(phpIndex)

	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/MessageBusInterface.php",
		[]byte(`<?php
namespace Symfony\Component\Messenger;
interface MessageBusInterface {
    public function dispatch(object $message, array $stamps = []): object;
}`),
	)))
	source := `<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
use Symfony\Component\Messenger\MessageBusInterface;

class FirstMessage {}
class SecondMessage {}
class ThirdMessage {}

#[AsMessageHandler]
final class UnionHandler {
    public function __invoke(FirstMessage|SecondMessage $message): void {}
}

#[AsMessageHandler(handles: ThirdMessage::class, method: 'handle')]
final class ExplicitHandler {
    public function handle(ThirdMessage $message): void {}
}

final class MethodHandler {
    #[AsMessageHandler]
    public function consume(ThirdMessage $message): void {}
}

function publish(MessageBusInterface $messageBus, FirstMessage $message): void {
    $messageBus->dispatch(new FirstMessage());
    $messageBus->dispatch($message);
}`
	path := "/project/src/Messenger.php"
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, idx.Index(parsed))

	first := requireMessage(t, idx, "App\\FirstMessage")
	assertHandler(t, first, "App\\UnionHandler", "__invoke")
	require.Len(t, first.Dispatches(), 2)

	second := requireMessage(t, idx, "App\\SecondMessage")
	assertHandler(t, second, "App\\UnionHandler", "__invoke")

	third := requireMessage(t, idx, "App\\ThirdMessage")
	assertHandler(t, third, "App\\ExplicitHandler", "handle")
	assertHandler(t, third, "App\\MethodHandler", "consume")
}

func TestIndexServiceHandlerTagsAcrossFormats(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	idx.SetPHPIndex(phpIndex)

	handlerSource := `<?php
namespace App;
class InferredMessage {}
class InferredHandler {
    public function __invoke(InferredMessage $message): void {}
}`
	handler := indexer.NewParsedFile(
		"/project/src/InferredHandler.php",
		[]byte(handlerSource),
	)
	require.NoError(t, phpIndex.Index(handler))
	require.NoError(t, idx.Index(handler))

	for _, file := range []*indexer.ParsedFile{
		indexer.NewParsedFile(
			"/project/config/services.php",
			[]byte(`<?php
use App\PhpHandler;
use App\PhpMessage;
$services->set(PhpHandler::class)
    ->tag('messenger.message_handler', [
        'handles' => PhpMessage::class,
        'method' => 'process',
        'priority' => 10,
    ]);`),
		),
		indexer.NewParsedFile(
			"/project/config/services.yaml",
			[]byte(`services:
  App\YamlHandler:
    tags:
      - { name: messenger.message_handler, handles: App\YamlMessage, method: consume }
  App\InferredHandler:
    tags: [messenger.message_handler]
`),
		),
		indexer.NewParsedFile(
			"/project/config/services.xml",
			[]byte(`<container><services>
  <service id="App\XmlHandler">
    <tag name="messenger.message_handler" handles="App\XmlMessage" method="run"/>
  </service>
</services></container>`),
		),
	} {
		require.NoError(t, idx.Index(file))
	}

	assertHandler(
		t,
		requireMessage(t, idx, "App\\PhpMessage"),
		"App\\PhpHandler",
		"process",
	)
	assertHandler(
		t,
		requireMessage(t, idx, "App\\YamlMessage"),
		"App\\YamlHandler",
		"consume",
	)
	assertHandler(
		t,
		requireMessage(t, idx, "App\\XmlMessage"),
		"App\\XmlHandler",
		"run",
	)
	assertHandler(
		t,
		requireMessage(t, idx, "App\\InferredMessage"),
		"App\\InferredHandler",
		"__invoke",
	)
}

func TestIndexRestoresAndRemovesStaleMessengerData(t *testing.T) {
	cache := t.TempDir()
	path := "/project/src/Handler.php"
	source := []byte(`<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
class Message {}
#[AsMessageHandler]
class Handler {
    public function __invoke(Message $message): void {}
}`)
	idx, err := NewIndex(cache)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, source)))
	require.Contains(t, mustMessageNames(t, idx), "App\\Message")
	require.NoError(t, idx.Close())

	restored, err := NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	require.Contains(t, mustMessageNames(t, restored), "App\\Message")
	require.NoError(t, restored.Index(indexer.NewParsedFile(
		path,
		[]byte("<?php namespace App; class Handler {}"),
	)))
	assert.Empty(t, mustMessageNames(t, restored))
}

func requireMessage(
	t *testing.T,
	idx *Index,
	name string,
) Message {
	t.Helper()
	message, found, err := idx.GetMessage(name)
	require.NoError(t, err)
	require.True(t, found, name)
	return message
}

func assertHandler(
	t *testing.T,
	message Message,
	className,
	methodName string,
) Occurrence {
	t.Helper()
	for _, handler := range message.Handlers() {
		if handler.Class == className && handler.Method == methodName {
			return handler
		}
	}
	require.Failf(
		t,
		"handler not found",
		"%s::%s for %s; got %#v",
		className,
		methodName,
		message.Name,
		message.Handlers(),
	)
	return Occurrence{}
}

func mustMessageNames(t *testing.T, idx *Index) []string {
	t.Helper()
	names, err := idx.MessageNames()
	require.NoError(t, err)
	return names
}
