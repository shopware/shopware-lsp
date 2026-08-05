package reference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventReferencesConnectListenersAndDispatchSites(t *testing.T) {
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
	provider := NewEventReferenceProvider(eventIndex)

	eventOffset := strings.LastIndex(source, "'app.ready'") + 2
	eventNode := document.SyntaxTree.Root.NodeAtOffset(uint32(eventOffset))
	eventContext := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		eventNode,
		document.SyntaxTree.Root,
	)
	eventLocations, err := provider.GetReferences(
		eventContext,
		eventReferenceRequest(document, eventNode),
	)
	require.NoError(t, err)
	require.Len(t, eventLocations, 2)

	methodOffset := strings.Index(source, "function onReady") +
		len("function ")
	methodNode := document.SyntaxTree.Root.NodeAtOffset(uint32(methodOffset))
	methodLocations, err := provider.GetReferences(
		context.Background(),
		eventReferenceRequest(document, methodNode),
	)
	require.NoError(t, err)
	require.Len(t, methodLocations, 2)
	lines := []int{
		methodLocations[0].Range.Start.Line,
		methodLocations[1].Range.Start.Line,
	}
	assert.ElementsMatch(t, []int{7, 12}, lines)
}

func eventReferenceRequest(
	document *lsp.TextDocument,
	node *cst.Node,
) *lsp.ReferenceRequest {
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	return &lsp.ReferenceRequest{
		ReferenceParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	}
}
