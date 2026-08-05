package reference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessengerReferencesConnectMessagesHandlersAndDispatches(t *testing.T) {
	root := t.TempDir()
	paths := map[string]string{
		filepath.Join(root, "vendor", "MessageBusInterface.php"): `<?php
namespace Symfony\Component\Messenger;
interface MessageBusInterface {
    public function dispatch(object $message, array $stamps = []): object;
}`,
		filepath.Join(root, "src", "Message.php"): `<?php
namespace App;
class Message {}`,
		filepath.Join(root, "src", "Handler.php"): `<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
#[AsMessageHandler]
class Handler {
    public function __invoke(Message $message): void {}
    public function consume(Message $message): void {}
}`,
		filepath.Join(root, "src", "Publisher.php"): `<?php
namespace App;
use Symfony\Component\Messenger\MessageBusInterface;
function publish(MessageBusInterface $messageBus): void {
    $messageBus->dispatch(new Message());
}`,
		filepath.Join(root, "config", "services.yaml"): `services:
  App\Handler:
    tags:
      - { name: messenger.message_handler, handles: App\Message, method: consume }
`,
	}
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	messengerIndex, err := messenger.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, messengerIndex.Close()) })
	messengerIndex.SetPHPIndex(phpIndex)
	for path, source := range paths {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		parsed := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, phpIndex.Index(parsed))
		require.NoError(t, messengerIndex.Index(parsed))
	}
	configPath := filepath.Join(root, "config", "services.yaml")
	configSource := paths[configPath]
	document := lsp.NewTextDocument(
		uriutil.FileURI(configPath),
		configSource,
		1,
	)
	offset := strings.Index(configSource, "App\\Message") + 1
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Context.IncludeDeclaration = true
	locations, err := NewMessengerReferenceProvider(
		messengerIndex,
		phpIndex,
	).GetReferences(
		context.Background(),
		&lsp.ReferenceRequest{
			ReferenceParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:  document,
				Root:      document.SyntaxTree.Root,
				Node:      node,
				LineIndex: document.LineIndex,
			},
		},
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(locations), 4)
	uris := make([]string, 0, len(locations))
	for _, location := range locations {
		uris = append(uris, location.URI)
	}
	assert.Contains(t, uris, uriutil.FileURI(
		filepath.Join(root, "src", "Message.php"),
	))
	assert.Contains(t, uris, uriutil.FileURI(
		filepath.Join(root, "src", "Handler.php"),
	))
	assert.Contains(t, uris, uriutil.FileURI(
		filepath.Join(root, "src", "Publisher.php"),
	))
	assert.Contains(t, uris, uriutil.FileURI(configPath))
}
