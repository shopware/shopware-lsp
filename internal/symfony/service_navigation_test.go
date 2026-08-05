package symfony

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
)

func TestServiceConfigurationInDocumentCoversPHPXMLAndYAMLRelationships(
	t *testing.T,
) {
	tests := []struct {
		name   string
		path   string
		source string
	}{
		{
			name: "xml",
			path: "/project/config/services.xml",
			source: `<container>
  <services>
    <service id="base" class="App\Base"/>
    <service id="abstract" class="App\AbstractService"/>
    <service id="decorator" class="App\Decorator" decorates="base" parent="abstract"/>
    <prototype namespace="App\" resource="../src" exclude="../src/Excluded"/>
  </services>
</container>`,
		},
		{
			name: "yaml",
			path: "/project/config/services.yaml",
			source: `services:
  base:
    class: App\Base
  abstract:
    class: App\AbstractService
  decorator:
    class: App\Decorator
    decorates: base
    parent: abstract
  'App\':
    resource: '../src'
    exclude: '../src/Excluded'
`,
		},
		{
			name: "php",
			path: "/project/config/services.php",
			source: `<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set('base', App\Base::class);
    $services->set('abstract', App\AbstractService::class);
    $services->set('decorator', App\Decorator::class)
        ->decorate('base')
        ->parent('abstract');
    $services->load('App\\', '../src')->exclude('../src/Excluded');
};
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed := indexer.NewParsedFile(
				test.path,
				[]byte(test.source),
			)
			config, err := ServiceConfigurationInDocument(
				test.path,
				parsed.SyntaxTree(),
				parsed.LineIndex(),
			)
			require.NoError(t, err)
			require.Len(t, config.Services, 3)
			decorator := requireServiceDefinition(
				t,
				config.Services,
				"decorator",
			)
			assert.Equal(t, "base", decorator.Decorates)
			assert.Equal(t, "abstract", decorator.Parent)
			assert.NotZero(t, decorator.IDRange.Len())
			assert.NotZero(t, decorator.DecoratesRange.Len())
			assert.NotZero(t, decorator.ParentRange.Len())
			require.Len(t, config.Prototypes, 1)
			prototype := config.Prototypes[0]
			assert.Equal(t, "App\\", prototype.Namespace)
			assert.Equal(t, "/project/src", prototype.Resource)
			assert.Equal(
				t,
				[]string{"/project/src/Excluded"},
				prototype.Excludes,
			)
			assert.NotZero(t, prototype.NamespaceRange.Len())
			assert.NotZero(t, prototype.ResourceRange.Len())
		})
	}
}

func TestServiceIndexPersistsRelationshipsAndExpandsYAMLPrototype(
	t *testing.T,
) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	classPath := filepath.Join(root, "src", "Included.php")
	classSource := "<?php\nnamespace App;\nclass Included {}\n"
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		[]byte(classSource),
	)))

	serviceIndex, err := NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)
	configPath := filepath.Join(root, "config", "services.yaml")
	source := `services:
  base:
    class: App\Base
  decorator:
    class: App\Decorator
    decorates: base
  'App\':
    resource: '../src'
`
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		configPath,
		[]byte(source),
	)))
	definitions, err := serviceIndex.ServiceDefinitions()
	require.NoError(t, err)
	decorator := requireServiceDefinition(t, definitions, "decorator")
	assert.Equal(t, "base", decorator.Decorates)
	declarations, err := serviceIndex.ServiceDeclarations("App\\Included")
	require.NoError(t, err)
	require.Len(t, declarations, 1)
	assert.Equal(t, configPath, declarations[0].Path)
	assert.Equal(t, 7, declarations[0].Line)
}

func requireServiceDefinition(
	t *testing.T,
	services []Service,
	id string,
) Service {
	t.Helper()
	for _, service := range services {
		if service.ID == id {
			return service
		}
	}
	require.FailNow(t, "service definition not found", "id: %s", id)
	return Service{}
}
