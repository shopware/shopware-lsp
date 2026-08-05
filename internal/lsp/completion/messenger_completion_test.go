package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessengerSubscriberCompletionIncludesPublicHandlerMethods(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/MessageSubscriberInterface.php",
		[]byte(messengerSubscriberInterfaceStub),
	)))
	source := `<?php
namespace App;
use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
final class Subscriber implements MessageSubscriberInterface {
    public static function getHandledMessages(): iterable {
        yield Message::class => ['method' => ''];
    }
    public function handleFirst(): void {}
    public function handleSecond(): void {}
    protected function hidden(): void {}
    private function secret(): void {}
    public function __toString(): string { return ''; }
}`
	path := "/project/src/Subscriber.php"
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	document := lsp.NewTextDocument("file://"+path, source, 1)
	offset := strings.Index(source, "'method' => ''") + len("'method' => '")
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	items := NewMessengerCompletionProvider(phpIndex).GetCompletions(
		ctx,
		consoleCompletionRequest(document, node),
	)
	requireCompletion(t, items, "handleFirst")
	requireCompletion(t, items, "handleSecond")
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}
	assert.NotContains(t, labels, "hidden")
	assert.NotContains(t, labels, "secret")
	assert.NotContains(t, labels, "__toString")
	assert.NotContains(t, labels, "getHandledMessages")
}

func TestMessengerConfigurationCompletionIncludesMessagesAndMethods(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	messengerIndex, err := messenger.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, messengerIndex.Close()) })
	messengerIndex.SetPHPIndex(phpIndex)
	handlerSource := `<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
class Message {}
#[AsMessageHandler]
class Handler {
    public function __invoke(Message $message): void {}
    public function consume(Message $message): void {}
}`
	parsed := indexer.NewParsedFile(
		"/project/src/Handler.php",
		[]byte(handlerSource),
	)
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, messengerIndex.Index(parsed))
	source := `services:
  App\Handler:
    tags:
      - { name: messenger.message_handler, handles: '', method: '' }
`
	path := "/project/config/services.yaml"
	document := lsp.NewTextDocument("file://"+path, source, 1)
	provider := NewMessengerCompletionProvider(phpIndex, messengerIndex)
	for _, test := range []struct {
		marker string
		label  string
	}{
		{"handles: ''", "App\\Message"},
		{"method: ''", "consume"},
	} {
		offset := strings.Index(source, test.marker) +
			strings.LastIndex(test.marker, "''") + 1
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
		items := provider.GetCompletions(
			context.Background(),
			consoleCompletionRequest(document, node),
		)
		requireCompletion(t, items, test.label)
	}
}

const messengerSubscriberInterfaceStub = `<?php
namespace Symfony\Component\Messenger\Handler;
interface MessageSubscriberInterface {
    public static function getHandledMessages(): iterable;
}`
