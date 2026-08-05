package symfony

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceParsersPreserveEffectiveAutowireConfiguration(t *testing.T) {
	tests := map[string]struct {
		source string
		parse  func(string, []byte) ([]Service, []Parameter, error)
	}{
		"yaml": {
			source: `services:
  _defaults:
    autowire: true
  App\DefaultService: ~
  App\DisabledService:
    autowire: false
`,
			parse: ParseYAMLServices,
		},
		"xml": {
			source: `<container>
  <services>
    <defaults autowire="true"/>
    <service id="App\DefaultService"/>
    <service id="App\DisabledService" autowire="false"/>
  </services>
</container>`,
			parse: ParseXMLServices,
		},
		"php fluent": {
			source: `<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->defaults()->autowire();
    $services->set(App\DefaultService::class);
    $services->set(App\DisabledService::class)->autowire(false);
};`,
			parse: ParsePHPServices,
		},
		"php array": {
			source: `<?php
return [
    'services' => [
        '_defaults' => ['autowire' => true],
        App\DefaultService::class => [],
        App\DisabledService::class => ['autowire' => false],
    ],
];`,
			parse: ParsePHPServices,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			services, _, err := test.parse(
				filepath.Join("/project/config", "services."+name),
				[]byte(test.source),
			)
			require.NoError(t, err)
			byID := make(map[string]Service, len(services))
			for _, service := range services {
				byID[service.ID] = service
			}
			enabled := byID["App\\DefaultService"]
			assert.True(t, enabled.AutowireSet)
			assert.True(t, enabled.Autowire)
			disabled := byID["App\\DisabledService"]
			assert.True(t, disabled.AutowireSet)
			assert.False(t, disabled.Autowire)
		})
	}
}

func TestServiceParsersApplyAutowireDefaultsToPrototypes(t *testing.T) {
	path := "/project/config/services"
	tests := map[string]struct {
		source string
		parse  func(string, string) []ServicePrototype
	}{
		"yaml": {
			source: `services:
  _defaults:
    autowire: true
  App\:
    resource: ../src/
`,
			parse: func(path, source string) []ServicePrototype {
				result := yamlparser.Parse(source)
				_, _, prototypes, err := parseYAMLServiceConfigTree(
					path+".yaml",
					result.Tree,
					yamlsyntax.NewLineIndex(source),
				)
				require.NoError(t, err)
				return prototypes
			},
		},
		"xml": {
			source: `<container><services>
  <defaults autowire="true"/>
  <prototype namespace="App\" resource="../src/"/>
</services></container>`,
			parse: func(path, source string) []ServicePrototype {
				tree := xmlparser.Parse(source).Tree
				_, _, prototypes, err := parseXMLServiceConfigTree(
					path+".xml",
					tree,
					xmlsyntax.NewLineIndex(source),
				)
				require.NoError(t, err)
				return prototypes
			},
		},
		"php fluent": {
			source: `<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->defaults()->autowire();
    $services->load('App\\', '../src/');
};`,
			parse: func(path, source string) []ServicePrototype {
				tree := phpparser.Parse(source).Tree
				config, err := parsePHPServiceConfigTree(
					path+".php",
					tree.Root,
					phpsyntax.NewLineIndex(source),
				)
				require.NoError(t, err)
				return config.Prototypes
			},
		},
		"php array": {
			source: `<?php
return ['services' => [
    '_defaults' => ['autowire' => true],
    'App\\' => ['resource' => '../src/'],
]];`,
			parse: func(path, source string) []ServicePrototype {
				tree := phpparser.Parse(source).Tree
				config, err := parsePHPServiceConfigTree(
					path+".php",
					tree.Root,
					phpsyntax.NewLineIndex(source),
				)
				require.NoError(t, err)
				return config.Prototypes
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			prototypes := test.parse(path, test.source)
			require.Len(t, prototypes, 1)
			assert.True(t, prototypes[0].AutowireSet)
			assert.True(t, prototypes[0].Autowire)
		})
	}
}

func TestAutowiredServiceUsageResolvesParentsAndExplicitOverrides(
	t *testing.T,
) {
	root := t.TempDir()
	serviceIndex, err := NewServiceIndex(root, filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	path := filepath.Join(root, "config", "services.yaml")
	source := `services:
  app.parent:
    class: App\ParentService
    autowire: true
  app.child:
    class: App\ChildService
    parent: app.parent
  app.disabled:
    class: App\DisabledService
    parent: app.parent
    autowire: false
`
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))

	locations, err := serviceIndex.
		GetAutowiredServicesUsageByClassName("App\\ChildService")
	require.NoError(t, err)
	assert.Equal(t, []Location{{Path: path, Line: 5}}, locations)

	locations, err = serviceIndex.
		GetAutowiredServicesUsageByClassName("App\\DisabledService")
	require.NoError(t, err)
	assert.Empty(t, locations)
}

func TestExplicitServiceAutowireOverridesGeneratedPrototype(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := NewServiceIndex(root, cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)

	phpPath := filepath.Join(root, "src", "Services.php")
	phpSource := `<?php
namespace App;
class WiredService {}
class ManualService {}
`
	servicePath := filepath.Join(root, "config", "services.yaml")
	serviceSource := `services:
  _defaults:
    autowire: true
  App\:
    resource: ../src/
  App\ManualService:
    autowire: false
`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		servicePath,
		[]byte(serviceSource),
	)))

	locations, err := serviceIndex.
		GetAutowiredServicesUsageByClassName("App\\WiredService")
	require.NoError(t, err)
	assert.Equal(t, []Location{{Path: servicePath, Line: 4}}, locations)

	locations, err = serviceIndex.
		GetAutowiredServicesUsageByClassName("App\\ManualService")
	require.NoError(t, err)
	assert.Empty(t, locations)
}
