package hover

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessengerSubscriberHoverShowsResolvedHandler(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/MessageSubscriberInterface.php",
		[]byte(`<?php
namespace Symfony\Component\Messenger\Handler;
interface MessageSubscriberInterface {}`),
	)))
	source := `<?php
namespace App;
use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
final class Subscriber implements MessageSubscriberInterface {
    public static function getHandledMessages(): iterable {
        yield Message::class => ['method' => 'handle'];
    }
    /** Handles the domain message. */
    public function handle(): void {}
}`
	path := "/project/src/Subscriber.php"
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	document := lsp.NewTextDocument("file://"+path, source, 1)
	offset := strings.Index(source, "'handle'") + 2
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	result, err := NewMessengerHoverProvider(phpIndex).GetHover(
		ctx,
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:  document,
				Root:      document.SyntaxTree.Root,
				Node:      node,
				LineIndex: document.LineIndex,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Symfony Messenger handler method")
	assert.Contains(t, result.Contents.Value, "App\\Subscriber::handle()")
	assert.Contains(t, result.Contents.Value, "Handles the domain message.")
}

func TestMessengerMessageHoverShowsGraphCounts(t *testing.T) {
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
      - { name: messenger.message_handler, handles: App\Message }
`
	document := lsp.NewTextDocument(
		"file:///project/config/services.yaml",
		source,
		1,
	)
	offset := strings.Index(source, "App\\Message") + 1
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	result, err := NewMessengerHoverProvider(
		phpIndex,
		messengerIndex,
	).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:  document,
				Root:      document.SyntaxTree.Root,
				Node:      node,
				LineIndex: document.LineIndex,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Symfony Messenger message")
	assert.Contains(t, result.Contents.Value, "App\\Message")
	assert.Contains(t, result.Contents.Value, "1 handler(s)")
}
