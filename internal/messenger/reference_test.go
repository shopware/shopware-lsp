package messenger

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlerMethodReferenceAtSupportsReturnAndYieldForms(t *testing.T) {
	source := `<?php
namespace App;
use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
final class Subscriber implements MessageSubscriberInterface
{
    public static function getHandledMessages(): iterable
    {
        yield FirstMessage::class => ['method' => 'handleFirst'];
        yield SecondMessage::class => 'handleSecond';
        return [
            ThirdMessage::class => 'handleThird',
            FourthMessage::class => ['method' => 'handleFourth'],
        ];
    }

    public function handleFirst(): void {}
    public function handleSecond(): void {}
    public function handleThird(): void {}
    public function handleFourth(): void {}
}`
	document := lsp.NewTextDocument("file:///project/Subscriber.php", source, 1)
	for _, name := range []string{
		"handleFirst",
		"handleSecond",
		"handleThird",
		"handleFourth",
	} {
		offset := strings.Index(source, "'"+name+"'") + 2
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
		reference, found := HandlerMethodReferenceAt(
			context.Background(),
			document.SyntaxTree.Root,
			node,
		)
		require.True(t, found, name)
		assert.Equal(t, name, reference.Name)
		assert.Equal(t, "App\\Subscriber", reference.Class)
	}
}

func TestHandlerMethodReferenceAtRejectsMessagesAndNestedClosures(t *testing.T) {
	source := `<?php
namespace App;
use Symfony\Component\Messenger\Handler\MessageSubscriberInterface;
final class Subscriber implements MessageSubscriberInterface
{
    public static function getHandledMessages(): iterable
    {
        yield 'App\Message' => ['method' => 'handle'];
        $factory = static function (): array {
            return ['App\Message' => 'nested'];
        };
    }

    public function handle(): void {}
}`
	document := lsp.NewTextDocument("file:///project/Subscriber.php", source, 1)
	for _, value := range []string{"App\\Message", "nested"} {
		offset := strings.LastIndex(source, "'"+value+"'") + 2
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
		_, found := HandlerMethodReferenceAt(
			context.Background(),
			document.SyntaxTree.Root,
			node,
		)
		assert.False(t, found, value)
	}
}

func TestPublicHandlerMethodsFiltersNonPublicAndSpecialMethods(t *testing.T) {
	// The provider-level tests exercise the semantic index. Keep this unit
	// test focused on the reference detector's class shape.
	source := `<?php
class Other {
    public static function getHandledMessages(): iterable {
        return [Message::class => 'handle'];
    }
}`
	document := lsp.NewTextDocument("file:///project/Other.php", source, 1)
	literals := phpquery.Nodes(
		document.SyntaxTree.Root,
		phpsyntax.PhpString,
	)
	require.Len(t, literals, 1)
	_, found := HandlerMethodReferenceAt(
		context.Background(),
		document.SyntaxTree.Root,
		literals[0],
	)
	assert.False(t, found)
}

func TestReferenceAtRecognizesModernHandlerConfiguration(t *testing.T) {
	tests := []struct {
		path      string
		source    string
		marker    string
		role      ReferenceRole
		name      string
		className string
	}{
		{
			path: "/project/src/Handler.php",
			source: `<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
#[AsMessageHandler(handles: Message::class, method: 'consume')]
class Handler { public function consume(Message $message): void {} }`,
			marker:    "Message::class",
			role:      ReferenceMessage,
			name:      "App\\Message",
			className: "App\\Handler",
		},
		{
			path: "/project/config/services.yaml",
			source: `services:
  App\Handler:
    tags:
      - { name: messenger.message_handler, handles: App\Message, method: consume }
`,
			marker:    "App\\Message",
			role:      ReferenceMessage,
			name:      "App\\Message",
			className: "App\\Handler",
		},
		{
			path: "/project/config/services.xml",
			source: `<container><services>
  <service id="App\Handler">
    <tag name="messenger.message_handler" handles="App\Message" method="consume"/>
  </service>
</services></container>`,
			marker:    "consume",
			role:      ReferenceHandlerMethod,
			name:      "consume",
			className: "App\\Handler",
		},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file://"+test.path,
				test.source,
				1,
			)
			offset := strings.Index(test.source, test.marker) + 1
			node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
			reference, found := ReferenceAt(
				context.Background(),
				test.path,
				document.SyntaxTree.Root,
				node,
			)
			require.True(t, found)
			assert.Equal(t, test.role, reference.Role)
			assert.Equal(t, test.name, reference.Name)
			assert.Equal(t, test.className, reference.Class)
		})
	}
}
