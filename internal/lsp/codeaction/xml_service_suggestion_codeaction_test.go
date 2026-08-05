package codeaction

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestXMLServiceSuggestionOffersCompatibleConstructorServices(
	t *testing.T,
) {
	fixture := newXMLServiceSuggestionFixture(t)
	source := `<container>
  <services>
    <service id="app.consumer" class="App\Consumer">
      <argument/>
    </service>
  </services>
</container>`
	request := serviceTagCodeActionRequest(
		source,
		"file:///project/services.xml",
		"<argument",
	)

	actions := fixture.provider.GetCodeActions(
		context.Background(),
		request,
	)

	require.Len(t, actions, 2)
	assert.Equal(
		t,
		"Symfony: Use service 'app.logger' for $logger",
		actions[0].Title,
	)
	assert.Equal(
		t,
		"Symfony: Use service 'app.secondary_logger' for $logger",
		actions[1].Title,
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
		`<argument type="service" id="app.logger"/>`,
	)
	require.Empty(
		t,
		lsp.NewTextDocument(request.Document.URI, updated, 2).ParseErrors,
	)
}

func TestXMLServiceSuggestionSupportsNamedArgumentsAndReplacesValue(
	t *testing.T,
) {
	fixture := newXMLServiceSuggestionFixture(t)
	source := `<container>
  <services>
    <service id="app.consumer" class="App\Consumer">
      <argument>literal scalar</argument>
      <argument key="$logger" on-invalid="null">%logger%</argument>
    </service>
  </services>
</container>`
	request := serviceTagCodeActionRequest(
		source,
		"file:///project/services.xml",
		"%logger%",
	)

	actions := fixture.provider.GetCodeActions(
		context.Background(),
		request,
	)

	require.Len(t, actions, 2)
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
		`<argument key="$logger" on-invalid="null" `+
			`type="service" id="app.logger"/>`,
	)
	assert.NotContains(t, updated, "%logger%")
}

func TestXMLServiceSuggestionUsesPositionalConstructorIndex(t *testing.T) {
	fixture := newXMLServiceSuggestionFixture(t)
	source := `<container>
  <services>
    <service id="app.consumer" class="App\Consumer">
      <argument type="service" id="app.logger"/>
      <argument/>
    </service>
  </services>
</container>`
	request := serviceTagCodeActionRequest(
		source,
		"file:///project/services.xml",
		"<argument/>",
	)
	offset := strings.LastIndex(source, "<argument/>")
	request.Node = request.Root.NodeAtOffset(uint32(offset))

	assert.Empty(t, fixture.provider.GetCodeActions(
		context.Background(),
		request,
	), "the second constructor parameter is scalar")
}

func TestXMLServiceSuggestionSuppressesCurrentAndInvalidTargets(
	t *testing.T,
) {
	fixture := newXMLServiceSuggestionFixture(t)
	for _, test := range []struct {
		name     string
		source   string
		needle   string
		expected int
	}{
		{
			name: "current compatible service",
			source: `<service id="app.consumer" class="App\Consumer">` +
				`<argument type="service" id="app.logger"/>` +
				`</service>`,
			needle:   "app.logger",
			expected: 1,
		},
		{
			name: "unknown class",
			source: `<service id="app.consumer" class="App\Missing">` +
				`<argument/>` +
				`</service>`,
			needle: "<argument",
		},
		{
			name:   "outside argument",
			source: `<service id="app.consumer" class="App\Consumer"/>`,
			needle: "App\\Consumer",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := serviceTagCodeActionRequest(
				test.source,
				"file:///project/services.xml",
				test.needle,
			)
			assert.Len(
				t,
				fixture.provider.GetCodeActions(
					context.Background(),
					request,
				),
				test.expected,
			)
		})
	}
}

type xmlServiceSuggestionFixture struct {
	provider     *XMLServiceSuggestionCodeActionProvider
	phpIndex     *php.PHPIndex
	serviceIndex *symfony.ServiceIndex
}

func newXMLServiceSuggestionFixture(
	t *testing.T,
) xmlServiceSuggestionFixture {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Services.php",
		[]byte(`<?php
namespace App;
interface LoggerInterface {}
class Logger implements LoggerInterface {}
class SecondaryLogger implements LoggerInterface {}
class Consumer
{
    public function __construct(
        LoggerInterface $logger,
        string $channel,
        ?LoggerInterface $fallback = null,
    ) {}
}
`),
	)))
	root := t.TempDir()
	serviceIndex, err := symfony.NewServiceIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		root+"/config/services.yaml",
		[]byte(`services:
  app.consumer:
    class: App\Consumer
  app.logger:
    class: App\Logger
  app.secondary_logger:
    class: App\SecondaryLogger
`),
	)))
	return xmlServiceSuggestionFixture{
		provider: NewXMLServiceSuggestionCodeActionProvider(
			serviceIndex,
			phpIndex,
		),
		phpIndex:     phpIndex,
		serviceIndex: serviceIndex,
	}
}
