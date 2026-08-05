package codelens

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

func TestMessengerCodeLensesLinkMessagesHandlersAndDispatches(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
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
}`,
		filepath.Join(root, "src", "Publisher.php"): `<?php
namespace App;
use Symfony\Component\Messenger\MessageBusInterface;
function publish(MessageBusInterface $messageBus): void {
    $messageBus->dispatch(new Message());
}`,
	}
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	messengerIndex, err := messenger.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, messengerIndex.Close()) })
	messengerIndex.SetPHPIndex(phpIndex)
	for path, source := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		parsed := indexer.NewParsedFile(path, []byte(source))
		require.NoError(t, phpIndex.Index(parsed))
		require.NoError(t, messengerIndex.Index(parsed))
	}
	provider := NewMessengerCodeLensProvider(messengerIndex, phpIndex)
	tests := []struct {
		path  string
		title string
	}{
		{
			path:  filepath.Join(root, "src", "Message.php"),
			title: "Messenger · 1 handler(s) · 1 dispatch(es)",
		},
		{
			path:  filepath.Join(root, "src", "Handler.php"),
			title: "Handles Messenger message",
		},
	}
	for _, test := range tests {
		document := lsp.NewTextDocument(
			uriutil.FileURI(test.path),
			files[test.path],
			1,
		)
		params := &protocol.CodeLensParams{}
		params.TextDocument.URI = document.URI
		lenses, lensErr := provider.GetCodeLenses(
			context.Background(),
			&lsp.CodeLensRequest{
				CodeLensParams: params,
				Document:       document,
			},
		)
		require.NoError(t, lensErr)
		var found bool
		for _, lens := range lenses {
			if lens.Command != nil &&
				strings.Contains(lens.Command.Title, test.title) {
				found = true
				assert.Equal(
					t,
					"shopware.openReferences",
					lens.Command.Command,
				)
				assert.NotEmpty(t, lens.Command.Arguments)
				break
			}
		}
		assert.True(t, found, test.title)
	}
}
