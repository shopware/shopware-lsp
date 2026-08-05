package completion

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfiguredServiceMethodCompletionAcrossYAMLAndXML(t *testing.T) {
	phpIndex := configuredMethodCompletionPHPIndex(t)
	provider := NewServiceCompletionProvider(nil, phpIndex)
	for _, fixture := range []struct {
		name     string
		file     string
		source   string
		label    string
		replaced string
	}{
		{
			name: "YAML call",
			file: "services.yaml",
			source: `services:
  app.consumer:
    class: App\Configured
    calls:
      - [setLog<caret>, []]
`,
			label:    "setLogger",
			replaced: "setLog",
		},
		{
			name: "YAML empty call",
			file: "services.yaml",
			source: `services:
  app.consumer:
    class: App\Configured
    calls:
      - ['<caret>', []]
`,
			label: "inherited",
		},
		{
			name: "YAML unquoted empty call",
			file: "services.yaml",
			source: `services:
  app.consumer:
    class: App\Configured
    calls:
      - [<caret>, []]
`,
			label: "inherited",
		},
		{
			name: "YAML tag callback",
			file: "services.yaml",
			source: `services:
  app.consumer:
    class: App\Configured
    tags:
      - { name: kernel.event_listener, method: onKernel<caret> }
`,
			label:    "onKernelRequest",
			replaced: "onKernel",
		},
		{
			name: "YAML service factory tuple",
			file: "services.yaml",
			source: `services:
  app.builder:
    class: App\Builder
  app.product:
    class: App\Product
    factory: ['@app.builder', cre<caret>]
`,
			label:    "create",
			replaced: "cre",
		},
		{
			name: "YAML class factory string",
			file: "services.yaml",
			source: `services:
  app.product:
    class: App\Product
    factory: 'App\Builder::bu<caret>'
`,
			label:    "build",
			replaced: "bu",
		},
		{
			name: "YAML legacy factory",
			file: "services.yaml",
			source: `services:
  app.builder:
    class: App\Builder
  app.product:
    class: App\Product
    factory_service: app.builder
    factory_method: cre<caret>
`,
			label:    "create",
			replaced: "cre",
		},
		{
			name: "XML call",
			file: "services.xml",
			source: `<container><services>
  <service id="app.consumer" class="App\Configured">
    <call method="setLog<caret>"/>
  </service>
</services></container>`,
			label:    "setLogger",
			replaced: "setLog",
		},
		{
			name: "XML tag callback",
			file: "services.xml",
			source: `<container><services>
  <service id="app.consumer" class="App\Configured">
    <tag name="kernel.event_listener" method="onKernel<caret>"/>
  </service>
</services></container>`,
			label:    "onKernelRequest",
			replaced: "onKernel",
		},
		{
			name: "XML service factory",
			file: "services.xml",
			source: `<container><services>
  <service id="app.builder" class="App\Builder"/>
  <service id="app.product" class="App\Product">
    <factory service="app.builder" method="cre<caret>"/>
  </service>
</services></container>`,
			label:    "create",
			replaced: "cre",
		},
		{
			name: "XML class factory",
			file: "services.xml",
			source: `<container><services>
  <service id="app.product" class="App\Product">
    <factory class="App\Builder" method="bu<caret>"/>
  </service>
</services></container>`,
			label:    "build",
			replaced: "bu",
		},
		{
			name: "XML legacy factory",
			file: "services.xml",
			source: `<container><services>
  <service id="app.builder" class="App\Builder"/>
  <service id="app.product" class="App\Product"
           factory-service="app.builder" factory-method="cre<caret>"/>
</services></container>`,
			label:    "create",
			replaced: "cre",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, offset := completionCaret(t, fixture.source)
			document, request := bundleResourceCompletionRequest(
				t,
				filepath.Join(t.TempDir(), fixture.file),
				source,
				offset,
			)
			items := provider.GetCompletions(context.Background(), request)
			item := requireCompletion(t, items, fixture.label)
			assert.Equal(t, int(protocol.MethodCompletion), item.Kind)
			edit, ok := item.TextEdit.(protocol.TextEdit)
			require.True(t, ok)
			assert.Equal(t, fixture.label, edit.NewText)
			assert.Equal(
				t,
				fixture.replaced,
				completionRangeText(document, edit.Range),
			)
		})
	}
}

func TestConfiguredServiceMethodCompletionFiltersNonPublicAndMagicMethods(
	t *testing.T,
) {
	phpIndex := configuredMethodCompletionPHPIndex(t)
	provider := NewServiceCompletionProvider(nil, phpIndex)
	source, offset := completionCaret(t, `services:
  app.consumer:
    class: App\Configured
    calls:
      - ['<caret>', []]
`)
	_, request := bundleResourceCompletionRequest(
		t,
		filepath.Join(t.TempDir(), "services.yaml"),
		source,
		offset,
	)
	labels := completionLabels(
		provider.GetCompletions(context.Background(), request),
	)
	assert.Contains(t, labels, "setLogger")
	assert.Contains(t, labels, "inherited")
	assert.NotContains(t, labels, "hidden")
	assert.NotContains(t, labels, "__construct")
}

func configuredMethodCompletionPHPIndex(t *testing.T) *php.PHPIndex {
	t.Helper()
	index, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		filepath.Join(t.TempDir(), "Configured.php"),
		[]byte(`<?php
namespace App;
class ParentConfigured
{
    public function inherited(): void {}
}
class Configured extends ParentConfigured
{
    public function __construct() {}
    public function setLogger(): void {}
    public function onKernelRequest(): void {}
    protected function hidden(): void {}
}
class Builder
{
    public function create(): Product { return new Product(); }
    public static function build(): Product { return new Product(); }
    private function secret(): void {}
}
class Product {}
`),
	)))
	return index
}
