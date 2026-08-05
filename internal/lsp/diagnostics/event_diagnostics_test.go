package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventDiagnosticsReportMissingEventsAndListenerMethods(t *testing.T) {
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
        return ['app.ready' => 'onRady'];
    }
    public function onReady(DomainEvent $event): void {}
}
function publish(EventDispatcherInterface $dispatcher): void {
    $dispatcher->dispatch(new DomainEvent(), 'app.ready');
    $dispatcher->dispatch(new DomainEvent(), 'app.raedy');
}`
	path := filepath.Join(t.TempDir(), "Events.php")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, eventIndex.Index(parsed))

	result, err := NewEventAnalyzer(
		eventIndex,
		phpIndex,
		nil,
	).Analyze(
		context.Background(),
		diagnosticsDocument("file://"+path, []byte(source)),
	)
	require.NoError(t, err)
	require.Len(t, result, 2)
	codes := make([]any, 0, len(result))
	for _, diagnostic := range result {
		codes = append(codes, diagnostic.ID)
		assert.NotEmpty(t, diagnostic.Payload)
		if diagnostic.ID == missingListenerMethodCode {
			data := diagnostic.Payload.(map[string]any)
			assert.Equal(t, "onRady", data["methodName"])
			assert.NotContains(t, data, "classURI")
			assert.NotContains(t, data, "insertLine")
			assert.Equal(
				t,
				[]string{"App\\DomainEvent"},
				data["eventTypes"],
			)
		}
	}
	assert.ElementsMatch(t, []any{
		missingEventCode,
		missingListenerMethodCode,
	}, codes)
}

func TestEventDiagnosticsConfiguredListenerMethodsAndCreateData(t *testing.T) {
	for _, fixture := range []struct {
		name      string
		extension string
		source    string
	}{
		{
			name:      "YAML",
			extension: ".yaml",
			source: `services:
  app.listener:
    class: App\Listener
    tags:
      - { name: kernel.event_listener, event: App\DomainEvent, method: onMissing }
`,
		},
		{
			name:      "XML",
			extension: ".xml",
			source: `<container><services>
  <service id="app.listener" class="App\Listener">
    <tag name="kernel.event_listener" event="App\DomainEvent" method="onMissing"/>
  </service>
</services></container>`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			phpIndex, err := php.NewPHPIndex(t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
			classPath := filepath.Join(root, "Listener.php")
			classSource := `<?php
namespace App;
class DomainEvent {}
class Listener
{
    public function onExisting(DomainEvent $event): void {}
}
`
			require.NoError(t, os.WriteFile(
				classPath,
				[]byte(classSource),
				0o644,
			))
			require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
				classPath,
				[]byte(classSource),
			)))
			eventIndex, err := event.NewIndex(t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, eventIndex.Close()) })
			eventIndex.SetPHPIndex(phpIndex)
			configPath := filepath.Join(
				root,
				"services"+fixture.extension,
			)
			require.NoError(t, eventIndex.Index(indexer.NewParsedFile(
				configPath,
				[]byte(fixture.source),
			)))
			document := diagnosticsDocument(
				uriutil.FileURI(configPath),
				[]byte(fixture.source),
			)

			result, err := NewEventAnalyzer(
				eventIndex,
				phpIndex,
				nil,
			).Analyze(context.Background(), document)
			require.NoError(t, err)
			missing := problemsWithCode(
				result,
				missingListenerMethodCode,
			)
			require.Len(t, missing, 1)
			assert.Equal(
				t,
				"onMissing",
				problemRangeText(document, missing[0].Range),
			)
			data := missing[0].Payload.(map[string]any)
			assert.Equal(
				t,
				[]string{"App\\DomainEvent"},
				data["eventTypes"],
			)
			assert.NotContains(t, data, "classURI")
		})
	}
}

func TestEventDiagnosticsClassLevelAttributeMethod(t *testing.T) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	eventIndex, err := event.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, eventIndex.Close()) })
	eventIndex.SetPHPIndex(phpIndex)
	path := filepath.Join(root, "AttributedListener.php")
	source := `<?php
namespace App;
use Symfony\Component\EventDispatcher\Attribute\AsEventListener;
class DomainEvent {}
#[AsEventListener(event: DomainEvent::class, method: 'onMissing')]
class AttributedListener
{
}
`
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, eventIndex.Index(parsed))
	document := diagnosticsDocument(uriutil.FileURI(path), []byte(source))

	result, err := NewEventAnalyzer(
		eventIndex,
		phpIndex,
		nil,
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	missing := problemsWithCode(result, missingListenerMethodCode)
	require.Len(t, missing, 1)
	data := missing[0].Payload.(map[string]any)
	assert.Equal(t, "onMissing", data["methodName"])
	assert.Equal(
		t,
		[]string{"App\\DomainEvent"},
		data["eventTypes"],
	)
	assert.NotContains(t, data, "classURI")
}
