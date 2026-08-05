package hover

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventHoverShowsTypeListenersAndDispatches(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/EventDispatcher.php",
		[]byte(`<?php
namespace Symfony\Component\EventDispatcher;
interface EventSubscriberInterface {}
namespace Symfony\Contracts\EventDispatcher;
interface EventDispatcherInterface {
    public function dispatch(object $event, ?string $eventName = null): object;
}`),
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
        return ['app.ready' => ['onReady', 20]];
    }
    public function onReady(DomainEvent $event): void {}
}
function publish(EventDispatcherInterface $dispatcher): void {
    $dispatcher->dispatch(new DomainEvent(), 'app.ready');
}`
	path := "/project/src/Events.php"
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, eventIndex.Index(parsed))

	document := lsp.NewTextDocument("file://"+path, source, 1)
	offset := strings.LastIndex(source, "'app.ready'") + 2
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
	result, err := NewEventHoverProvider(
		"/project",
		eventIndex,
		phpIndex,
		nil,
	).GetHover(ctx, &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Symfony event")
	assert.Contains(t, result.Contents.Value, "App\\DomainEvent")
	assert.Contains(t, result.Contents.Value, "App\\Subscriber::onReady()")
	assert.Contains(t, result.Contents.Value, "priority `20`")
	assert.Contains(t, result.Contents.Value, "1 indexed dispatch site")
}
