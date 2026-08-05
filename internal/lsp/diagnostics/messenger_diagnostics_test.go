package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessengerSubscriberDiagnosticsSuggestHandlerMethod(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/MessageSubscriberInterface.php",
		[]byte(`<?php
namespace Symfony\Component\Messenger\Handler;
interface MessageSubscriberInterface {}`),
	)))
	source := []byte(`<?php
namespace App;
use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
final class Subscriber implements MessageSubscriberInterface {
    public static function getHandledMessages(): iterable {
        return [Message::class => 'handel'];
    }
    public function handle(): void {}
}`)
	path := "/project/src/Subscriber.php"
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(path, source)))
	result, err := NewMessengerAnalyzer(phpIndex).Analyze(
		context.Background(),
		diagnosticsDocument("file://"+path, source),
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, missingMessengerHandlerMethodCode, result[0].ID)
	assert.Contains(t, result[0].Message, "App\\Subscriber::handel")
	assert.Equal(
		t,
		[]string{"handle"},
		result[0].Payload.(map[string]any)["suggestions"],
	)
}

func TestMessengerConfigurationDiagnosticsReportMessageAndMethodTypos(
	t *testing.T,
) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Handler.php",
		[]byte(`<?php
namespace App;
class Message {}
class Handler {
    public function consume(Message $message): void {}
}`),
	)))
	source := []byte(`services:
  App\Handler:
    tags:
      - { name: messenger.message_handler, handles: App\Mesage, method: consum }
`)
	result, err := NewMessengerAnalyzer(phpIndex).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/config/services.yaml",
			source,
		),
	)
	require.NoError(t, err)
	require.Len(t, result, 2)
	codes := []any{result[0].ID, result[1].ID}
	assert.ElementsMatch(t, []any{
		missingMessengerMessageCode,
		missingMessengerHandlerMethodCode,
	}, codes)
	for _, diagnostic := range result {
		assert.NotEmpty(t, diagnostic.Payload)
	}
}
