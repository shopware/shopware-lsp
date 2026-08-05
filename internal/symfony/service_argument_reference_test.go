package symfony

import (
	"strings"
	"testing"

	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYAMLServiceArgumentReferences(t *testing.T) {
	source := `services:
  app.consumer:
    class: App\Consumer
    arguments:
      - scalar
      - '@app.logger'
      - '@?app.optional'
  app.named:
    class: App\Named
    arguments:
      $logger: '@app.logger'
      2: '@app.third'
  app.factory:
    class: App\FactoryProduct
    factory: ['@factory', create]
    arguments: ['@app.ignored']
    calls:
      - [setLogger, ['@app.logger']]
  app.called:
    class: App\Called
    calls:
      - [setLogger, [scalar, '@app.logger']]
`
	references := YAMLServiceArgumentReferences(
		yamlparser.Parse(source).Tree.Root,
	)
	require.Len(t, references, 6)
	assert.Equal(t, "app.consumer", references[0].OwnerServiceID)
	assert.Equal(t, `App\Consumer`, references[0].OwnerClass)
	assert.Equal(t, "app.logger", references[0].ServiceID)
	assert.Equal(t, 1, references[0].ParameterIndex)
	assert.Equal(
		t,
		"@app.logger",
		source[references[0].Range.Start:references[0].Range.End],
	)
	assert.Equal(t, "app.optional", references[1].ServiceID)
	assert.Equal(t, "$logger", references[2].ParameterName)
	assert.Equal(t, -1, references[2].ParameterIndex)
	assert.Equal(t, 2, references[3].ParameterIndex)
	assert.Equal(t, "setLogger", references[4].MethodName)
	assert.Equal(t, 0, references[4].ParameterIndex)
	assert.Equal(t, "setLogger", references[5].MethodName)
	assert.Equal(t, 1, references[5].ParameterIndex)
}

func TestYAMLServiceMethodReferences(t *testing.T) {
	source := `services:
  app.called:
    class: App\Called
    calls:
      - [setLogger, ['@app.logger']]
      - [initialize]
    tags:
      - { name: kernel.event_listener, method: onKernelRequest }
  app.factory:
    class: App\Product
    factory: ['@app.builder', create]
  app.static_factory:
    class: App\Product
    factory: 'App\Factory::build'
  app.legacy_factory:
    class: App\Product
    factory_service: app.builder
    factory_method: legacyCreate
`
	references := YAMLServiceMethodReferences(
		yamlparser.Parse(source).Tree.Root,
	)
	require.Len(t, references, 6)
	assert.Equal(t, "app.called", references[0].OwnerServiceID)
	assert.Equal(t, `App\Called`, references[0].OwnerClass)
	assert.Equal(t, "setLogger", references[0].MethodName)
	assert.Equal(
		t,
		"setLogger",
		source[references[0].Range.Start:references[0].Range.End],
	)
	assert.Equal(t, "initialize", references[1].MethodName)
	assert.Equal(t, "onKernelRequest", references[2].MethodName)
	assert.Equal(t, "create", references[3].MethodName)
	assert.True(t, references[3].Factory)
	serviceID, className := references[3].Receiver()
	assert.Equal(t, "app.builder", serviceID)
	assert.Empty(t, className)
	assert.Equal(t, "build", references[4].MethodName)
	serviceID, className = references[4].Receiver()
	assert.Empty(t, serviceID)
	assert.Equal(t, `App\Factory`, className)
	assert.Equal(
		t,
		"build",
		source[references[4].Range.Start:references[4].Range.End],
	)
	assert.Equal(t, "legacyCreate", references[5].MethodName)
	serviceID, _ = references[5].Receiver()
	assert.Equal(t, "app.builder", serviceID)
}

func TestYAMLServiceMethodReferenceAtIncludesEmptyScalars(t *testing.T) {
	for _, fixture := range []struct {
		source string
		needle string
	}{
		{
			source: `services:
  app.called:
    class: App\Called
    calls:
      - ['', []]
`,
			needle: "''",
		},
		{
			source: `services:
  app.called:
    class: App\Called
    calls:
      - [, []]
`,
			needle: "[,",
		},
		{
			source: `services:
  app.factory:
    class: App\Product
    factory: ['@app.builder', '']
`,
			needle: "''",
		},
		{
			source: `services:
  app.factory:
    class: App\Product
    factory: 'app.builder:'
`,
			needle: ":'",
		},
	} {
		offset := uint32(strings.Index(fixture.source, fixture.needle) + 1)
		reference, found := YAMLServiceMethodReferenceAt(
			yamlparser.Parse(fixture.source).Tree.Root,
			offset,
		)
		require.True(t, found)
		assert.Empty(t, reference.MethodName)
	}
}

func TestXMLServiceArgumentReferences(t *testing.T) {
	source := `<container><services>
  <service id="app.consumer" class="App\Consumer">
    <argument>scalar</argument>
    <argument type="service" id="app.logger"/>
    <argument type="service" id="app.named" key="$named"/>
    <argument type="service" id="app.third" key="3"/>
  </service>
  <service id="app.factory" class="App\FactoryProduct">
    <factory service="factory"/>
    <argument type="service" id="app.ignored"/>
    <call method="setLogger">
      <argument type="service" id="app.logger"/>
    </call>
  </service>
  <service id="app.called" class="App\Called">
    <call method="setLogger">
      <argument>scalar</argument>
      <argument type="service" id="app.logger"/>
    </call>
  </service>
</services></container>`
	references := XMLServiceArgumentReferences(
		xmlparser.Parse(source).Tree.Root,
	)
	require.Len(t, references, 5)
	assert.Equal(t, "app.logger", references[0].ServiceID)
	assert.Equal(t, 1, references[0].ParameterIndex)
	assert.Equal(
		t,
		"app.logger",
		source[references[0].Range.Start:references[0].Range.End],
	)
	assert.Equal(t, "$named", references[1].ParameterName)
	assert.Equal(t, -1, references[1].ParameterIndex)
	assert.Equal(t, 3, references[2].ParameterIndex)
	assert.Equal(t, "setLogger", references[3].MethodName)
	assert.Equal(t, 0, references[3].ParameterIndex)
	assert.Equal(t, "setLogger", references[4].MethodName)
	assert.Equal(t, 1, references[4].ParameterIndex)
}

func TestXMLServiceMethodReferences(t *testing.T) {
	source := `<container><services>
  <service id="app.called" class="App\Called">
    <call method="setLogger"/>
    <call method="initialize"/>
    <tag name="kernel.event_listener" method="onKernelRequest"/>
  </service>
  <service id="app.factory" class="App\Product">
    <factory service="app.builder" method="create"/>
  </service>
  <service id="app.static_factory" class="App\Product">
    <factory class="App\Factory" method="build"/>
  </service>
  <service id="app.legacy_factory" class="App\Product"
           factory-service="app.builder" factory-method="legacyCreate"/>
</services></container>`
	references := XMLServiceMethodReferences(
		xmlparser.Parse(source).Tree.Root,
	)
	require.Len(t, references, 6)
	assert.Equal(t, "app.called", references[0].OwnerServiceID)
	assert.Equal(t, `App\Called`, references[0].OwnerClass)
	assert.Equal(t, "setLogger", references[0].MethodName)
	assert.Equal(
		t,
		"setLogger",
		source[references[0].Range.Start:references[0].Range.End],
	)
	assert.Equal(t, "initialize", references[1].MethodName)
	assert.Equal(t, "onKernelRequest", references[2].MethodName)
	serviceID, className := references[3].Receiver()
	assert.Equal(t, "app.builder", serviceID)
	assert.Empty(t, className)
	serviceID, className = references[4].Receiver()
	assert.Empty(t, serviceID)
	assert.Equal(t, `App\Factory`, className)
	serviceID, _ = references[5].Receiver()
	assert.Equal(t, "app.builder", serviceID)
}

func TestXMLServiceMethodReferenceAtIncludesEmptyAttributes(t *testing.T) {
	source := `<container><services>
  <service id="app.called" class="App\Called">
    <call method=""/>
  </service>
</services></container>`
	offset := uint32(strings.Index(source, `method=""`) + len(`method="`))
	reference, found := XMLServiceMethodReferenceAt(
		xmlparser.Parse(source).Tree.Root,
		offset,
	)
	require.True(t, found)
	assert.Empty(t, reference.MethodName)
}
