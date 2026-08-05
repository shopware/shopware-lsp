package codeaction

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceTagCodeActionExpandsSelfClosingXMLService(t *testing.T) {
	provider := serviceTagCodeActionFixture(t)
	source := `<container>
  <services>
    <service id="app.subscriber" class="App\Subscriber"/>
  </services>
</container>`
	request := serviceTagCodeActionRequest(
		source,
		"file:///project/services.xml",
		"App\\Subscriber",
	)

	actions := provider.GetCodeActions(context.Background(), request)

	require.Len(t, actions, 1)
	assert.Equal(
		t,
		"Symfony: Add service tag 'kernel.event_subscriber'",
		actions[0].Title,
	)
	assert.Equal(t, protocol.CodeActionRefactorRewrite, actions[0].Kind)
	updated := applyCodeActionEdit(
		t,
		source,
		actions[0],
		request.TextDocument.URI,
		request.Document,
	)
	assert.Contains(t, updated, `<service id="app.subscriber" class="App\Subscriber">`)
	assert.Contains(t, updated, `      <tag name="kernel.event_subscriber"/>`)
	assert.Contains(t, updated, `    </service>`)
	require.Empty(
		t,
		lsp.NewTextDocument(request.Document.URI, updated, 2).ParseErrors,
	)
}

func TestServiceTagCodeActionAppendsXMLTagAndPreservesChildIndent(
	t *testing.T,
) {
	provider := serviceTagCodeActionFixture(t)
	source := `<container>
	<services>
		<service id="app.subscriber" class="App\Subscriber">
			<argument type="service" id="logger"/>
		</service>
	</services>
</container>`
	request := serviceTagCodeActionRequest(
		source,
		"file:///project/services.xml",
		"logger",
	)

	actions := provider.GetCodeActions(context.Background(), request)

	require.Len(t, actions, 1)
	updated := applyCodeActionEdit(
		t,
		source,
		actions[0],
		request.TextDocument.URI,
		request.Document,
	)
	assert.Contains(
		t,
		updated,
		"\t\t\t<tag name=\"kernel.event_subscriber\"/>\n"+
			"\t\t</service>",
	)
}

func TestServiceTagCodeActionCreatesYAMLTags(t *testing.T) {
	provider := serviceTagCodeActionFixture(t)
	source := `services:
  app.subscriber:
    class: App\Subscriber
    arguments: ['@logger']
`
	request := serviceTagCodeActionRequest(
		source,
		"file:///project/services.yaml",
		"App\\Subscriber",
	)

	actions := provider.GetCodeActions(context.Background(), request)

	require.Len(t, actions, 1)
	updated := applyCodeActionEdit(
		t,
		source,
		actions[0],
		request.TextDocument.URI,
		request.Document,
	)
	assert.Contains(
		t,
		updated,
		"    tags:\n      - kernel.event_subscriber",
	)
	require.Empty(
		t,
		lsp.NewTextDocument(request.Document.URI, updated, 2).ParseErrors,
	)
}

func TestServiceTagCodeActionUpdatesYAMLFlowForms(t *testing.T) {
	provider := serviceTagCodeActionFixture(t)
	for _, fixture := range []struct {
		name     string
		source   string
		expected string
	}{
		{
			name: "flow service",
			source: `services:
  app.subscriber: { class: App\Subscriber }
`,
			expected: `{ class: App\Subscriber, tags: [kernel.event_subscriber] }`,
		},
		{
			name: "flow tags",
			source: `services:
  app.subscriber:
    class: App\Subscriber
    tags: [app.custom]
`,
			expected: "tags: [app.custom, kernel.event_subscriber]",
		},
		{
			name: "scalar tag",
			source: `services:
  app.subscriber:
    class: App\Subscriber
    tags: app.custom
`,
			expected: "tags: [app.custom, kernel.event_subscriber]",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			request := serviceTagCodeActionRequest(
				fixture.source,
				"file:///project/services.yaml",
				"App\\Subscriber",
			)
			actions := provider.GetCodeActions(
				context.Background(),
				request,
			)
			require.Len(t, actions, 1)
			updated := applyCodeActionEdit(
				t,
				fixture.source,
				actions[0],
				request.TextDocument.URI,
				request.Document,
			)
			assert.Contains(t, updated, fixture.expected)
			require.Empty(
				t,
				lsp.NewTextDocument(
					request.Document.URI,
					updated,
					2,
				).ParseErrors,
			)
		})
	}
}

func TestServiceTagCodeActionOffersEveryMissingInferredTag(t *testing.T) {
	provider := serviceTagCodeActionFixture(t)
	source := `services:
  app.multi:
    class: App\MultiExtension
    tags:
      - kernel.event_subscriber
`
	request := serviceTagCodeActionRequest(
		source,
		"file:///project/services.yaml",
		"App\\MultiExtension",
	)

	actions := provider.GetCodeActions(context.Background(), request)

	require.Len(t, actions, 1)
	assert.Equal(
		t,
		"Symfony: Add service tag 'twig.extension'",
		actions[0].Title,
	)
	updated := applyCodeActionEdit(
		t,
		source,
		actions[0],
		request.TextDocument.URI,
		request.Document,
	)
	assert.Contains(
		t,
		updated,
		"      - kernel.event_subscriber\n"+
			"      - twig.extension",
	)
}

func TestServiceTagCodeActionRejectsUnsupportedTargets(t *testing.T) {
	provider := serviceTagCodeActionFixture(t)
	for _, fixture := range []struct {
		name   string
		uri    string
		source string
		needle string
	}{
		{
			name:   "already tagged",
			uri:    "file:///project/services.xml",
			source: `<service id="app.subscriber" class="App\Subscriber"><tag name="kernel.event_subscriber"/></service>`,
			needle: "App\\Subscriber",
		},
		{
			name: "unrelated class",
			uri:  "file:///project/services.yaml",
			source: `services:
  app.plain:
    class: App\PlainService
`,
			needle: "App\\PlainService",
		},
		{
			name: "alias",
			uri:  "file:///project/services.yaml",
			source: `services:
  app.alias:
    alias: app.subscriber
`,
			needle: "app.subscriber",
		},
		{
			name:   "outside service",
			uri:    "file:///project/services.yaml",
			source: "value: App\\Subscriber\n",
			needle: "App\\Subscriber",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			request := serviceTagCodeActionRequest(
				fixture.source,
				fixture.uri,
				fixture.needle,
			)
			assert.Empty(t, provider.GetCodeActions(
				context.Background(),
				request,
			))
		})
	}
}

func serviceTagCodeActionFixture(
	t *testing.T,
) *ServiceTagCodeActionProvider {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		"/vendor/EventSubscriberInterface.php": `<?php
namespace Symfony\Component\EventDispatcher;
interface EventSubscriberInterface {}
`,
		"/vendor/ExtensionInterface.php": `<?php
namespace Twig\Extension;
interface ExtensionInterface {}
`,
		"/project/src/Services.php": `<?php
namespace App;
use Symfony\Component\EventDispatcher\EventSubscriberInterface;
use Twig\Extension\ExtensionInterface;
class Subscriber implements EventSubscriberInterface {}
class MultiExtension implements EventSubscriberInterface, ExtensionInterface {}
class PlainService {}
`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	return NewServiceTagCodeActionProvider(phpIndex)
}

func serviceTagCodeActionRequest(
	source,
	uri,
	needle string,
) *lsp.CodeActionRequest {
	document := lsp.NewTextDocument(uri, source, 1)
	offset := strings.Index(source, needle)
	params := &protocol.CodeActionParams{}
	params.TextDocument.URI = uri
	if offset >= 0 {
		line, character := document.LineIndex.PositionUTF16(
			uint32(offset),
		)
		params.Range = protocol.Range{
			Start: protocol.Position{
				Line:      int(line),
				Character: int(character),
			},
			End: protocol.Position{
				Line:      int(line),
				Character: int(character) + len(needle),
			},
		}
	}
	return &lsp.CodeActionRequest{
		CodeActionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				uint32(max(offset, 0)),
			),
		},
	}
}
