package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventDefinitionsNavigateToEventClassAndListenerMethod(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "Events.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	source := `<?php
namespace App;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Symfony\Contracts\EventDispatcher\EventDispatcherInterface;
class DomainEvent {}
class Subscriber implements EventSubscriberInterface {
    public static function getSubscribedEvents(): array {
        return ['app.ready' => 'onReady'];
    }
    public function onReady(DomainEvent $event): void {}
}
function publish(EventDispatcherInterface $dispatcher): void {
    $dispatcher->dispatch(new DomainEvent(), 'app.ready');
}`
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))

	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "vendor", "EventDispatcher.php"),
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
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, eventIndex.Index(parsed))

	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	provider := NewEventDefinitionProvider(eventIndex, phpIndex, nil)

	methodOffset := strings.Index(source, "'onReady'") + 2
	methodNode := document.SyntaxTree.Root.NodeAtOffset(uint32(methodOffset))
	methodContext := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		methodNode,
		document.SyntaxTree.Root,
	)
	methodLocations := provider.GetDefinition(
		methodContext,
		consoleDefinitionRequest(document, methodNode),
	)
	require.Len(t, methodLocations, 1)
	assert.Equal(t, 9, methodLocations[0].Range.Start.Line)

	eventOffset := strings.LastIndex(source, "'app.ready'") + 2
	eventNode := document.SyntaxTree.Root.NodeAtOffset(uint32(eventOffset))
	eventContext := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		eventNode,
		document.SyntaxTree.Root,
	)
	eventLocations := provider.GetDefinition(
		eventContext,
		consoleDefinitionRequest(document, eventNode),
	)
	require.GreaterOrEqual(t, len(eventLocations), 2)
	lines := make([]int, 0, len(eventLocations))
	for _, location := range eventLocations {
		assert.Equal(t, uriutil.FileURI(path), location.URI)
		lines = append(lines, location.Range.Start.Line)
	}
	assert.Contains(t, lines, 4)
	assert.Contains(t, lines, 9)
}
