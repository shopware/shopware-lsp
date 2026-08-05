package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessengerSubscriberDefinitionNavigatesHandlerMethod(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "Subscriber.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	source := `<?php
namespace App;
use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
final class Subscriber implements MessageSubscriberInterface {
    public static function getHandledMessages(): iterable {
        return [Message::class => 'handle'];
    }
    public function handle(): void {}
}`
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for currentPath, currentSource := range map[string]string{
		filepath.Join(root, "vendor", "MessageSubscriberInterface.php"): `<?php
namespace Symfony\Component\Messenger\Handler;
interface MessageSubscriberInterface {
    public static function getHandledMessages(): iterable;
}`,
		path: source,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			currentPath,
			[]byte(currentSource),
		)))
	}
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := strings.Index(source, "'handle'") + 2
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	locations := NewMessengerDefinitionProvider(phpIndex).GetDefinition(
		ctx,
		consoleDefinitionRequest(document, node),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(path), locations[0].URI)
	assert.Equal(t, 7, locations[0].Range.Start.Line)
}

func TestMessengerConfigurationDefinitionNavigatesMessageAndMethod(t *testing.T) {
	root := t.TempDir()
	handlerPath := filepath.Join(root, "src", "Handler.php")
	configPath := filepath.Join(root, "config", "services.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(handlerPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	handlerSource := `<?php
namespace App;
class Message {}
class Handler {
    public function consume(Message $message): void {}
}`
	configSource := `services:
  App\Handler:
    tags:
      - { name: messenger.message_handler, handles: App\Message, method: consume }
`
	require.NoError(t, os.WriteFile(handlerPath, []byte(handlerSource), 0o644))
	require.NoError(t, os.WriteFile(configPath, []byte(configSource), 0o644))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		handlerPath,
		[]byte(handlerSource),
	)))
	messengerIndex, err := messenger.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, messengerIndex.Close()) })
	document := lsp.NewTextDocument(
		uriutil.FileURI(configPath),
		configSource,
		1,
	)
	provider := NewMessengerDefinitionProvider(phpIndex, messengerIndex)
	for _, test := range []struct {
		marker string
		line   int
	}{
		{"App\\Message", 2},
		{"consume", 4},
	} {
		offset := strings.Index(configSource, test.marker) + 1
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
		locations := provider.GetDefinition(
			context.Background(),
			consoleDefinitionRequest(document, node),
		)
		require.Len(t, locations, 1, test.marker)
		assert.Equal(t, test.line, locations[0].Range.Start.Line)
	}
}
