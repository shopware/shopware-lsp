package symfony

import (
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYAMLServiceNamedArguments(t *testing.T) {
	source := `services:
  app.consumer:
    class: App\Consumer
    arguments:
      '$logger': '@logger'
      $name: product
  app.factory:
    class: App\FactoryProduct
    factory: ['@factory', create]
    arguments:
      $value: product
  App\ImplicitConsumer:
    arguments:
      $enabled: true
  global.consumer:
    class: GlobalConsumer
    arguments:
      $value: true
`
	root := yamlparser.Parse(source).Tree.Root
	arguments := YAMLServiceNamedArguments(root)
	require.Len(t, arguments, 5)

	assert.Equal(t, "$logger", arguments[0].Name)
	assert.Equal(t, "app.consumer", arguments[0].ServiceID)
	assert.Equal(t, `App\Consumer`, arguments[0].ClassName)
	assert.Equal(t, []string{"$logger", "$name"}, arguments[0].Existing)
	assert.Equal(t, "$logger", source[arguments[0].Range.Start:arguments[0].Range.End])
	assert.True(t, arguments[0].Complete)

	assert.True(t, arguments[2].HasFactory)
	assert.Equal(t, `App\FactoryProduct`, arguments[2].ClassName)
	assert.Equal(t, `App\ImplicitConsumer`, arguments[3].ClassName)
	assert.Equal(t, "GlobalConsumer", arguments[4].ClassName)
}

func TestYAMLServiceNamedArgumentAtSupportsIncompleteKey(t *testing.T) {
	source := `services:
  app.consumer:
    class: App\Consumer
    arguments:
      $log`
	root := yamlparser.Parse(source).Tree.Root
	offset := uint32(strings.Index(source, "$log") + len("$log"))
	context, found := YAMLServiceNamedArgumentAt(root, offset)
	require.True(t, found)
	assert.Equal(t, "$log", context.Name)
	assert.False(t, context.Complete)
	assert.Equal(t, `App\Consumer`, context.ClassName)
	assert.Equal(t, "$log", source[context.Range.Start:context.Range.End])
}

func TestYAMLServiceNamedArgumentIgnoresDefaultsAndNonArgumentKeys(
	t *testing.T,
) {
	source := `services:
  _defaults:
    bind:
      $logger: '@logger'
  app.consumer:
    class: App\Consumer
    calls:
      - [setLogger, {$logger: '@logger'}]
`
	root := yamlparser.Parse(source).Tree.Root
	assert.Empty(t, YAMLServiceNamedArguments(root))
}

func TestYAMLServiceDefaultBindingAtSupportsNamedAndTypedKeys(t *testing.T) {
	source := `services:
  _defaults:
    bind:
      $proxyUrl: '%env(string:PROXY_URL)%'
      'string $defaultUri': '%env(string:DEFAULT_URI)%'
      Psr\Log\LoggerInterface $logger: '@logger'
`
	root := yamlparser.Parse(source).Tree.Root
	for _, expected := range []struct {
		needle   string
		name     string
		typeName string
	}{
		{"$proxyUrl", "$proxyUrl", ""},
		{"$defaultUri", "$defaultUri", "string"},
		{"$logger", "$logger", `Psr\Log\LoggerInterface`},
	} {
		offset := uint32(strings.Index(source, expected.needle) + 2)
		binding, found := YAMLServiceDefaultBindingAt(root, offset)
		require.True(t, found)
		assert.Equal(t, expected.name, binding.Name)
		assert.Equal(t, expected.typeName, binding.Type)
		assert.Contains(
			t,
			source[binding.Range.Start:binding.Range.End],
			expected.needle,
		)
	}
}

func TestYAMLServiceDefaultBindingAtIgnoresOtherBindMappings(t *testing.T) {
	source := `framework:
  bind:
    string $value: example
services:
  app.consumer:
    arguments:
      $value: example
`
	root := yamlparser.Parse(source).Tree.Root
	for start := 0; ; {
		index := strings.Index(source[start:], "$value")
		if index < 0 {
			break
		}
		offset := uint32(start + index + 2)
		_, found := YAMLServiceDefaultBindingAt(root, offset)
		assert.False(t, found)
		start += index + len("$value")
	}
}

func TestResolveYAMLServiceNamedArgumentClassFollowsAlias(t *testing.T) {
	serviceIndex, err := NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		"/project/services.yaml",
		[]byte(`services:
  app.consumer:
    alias: app.concrete
  app.concrete:
    class: App\Consumer
`),
	)))

	className, found, err := ResolveYAMLServiceNamedArgumentClass(
		serviceIndex,
		YAMLServiceNamedArgument{ServiceID: "app.consumer"},
	)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, `App\Consumer`, className)
}
