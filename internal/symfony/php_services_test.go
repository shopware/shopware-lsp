package symfony

import (
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePHPServices(t *testing.T) {
	source := []byte(`<?php
namespace App\DependencyInjection;

use App\Contract\HandlerInterface;
use App\Service\Handler as RenamedHandler;
use App\Service\MigrationSource;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (string $env, ContainerConfigurator $container): void {
    $services = $container->services();
    $parameters = $container->parameters();

    $services->instanceof(HandlerInterface::class)->tag('app.instanceof');
    $services->defaults()->autowire()->autoconfigure()->tag('app.default');
    $handler = $services->set(HandlerInterface::class, RenamedHandler::class)
        ->tag('app.handler');
    $handler->tag('app.later');
    $services->set(MigrationSource::class . '.core')
        ->class(RenamedHandler::class);
    $services->set('removed.service', RenamedHandler::class);
    $services->remove('removed.service');
    $services->alias('app.handler_alias', HandlerInterface::class);

    $parameters->set('app.scalar', 'value');
    $container->parameters()->set('app.array', ['one', 'two']);
};
`)

	services, parameters, err := ParsePHPServices("/project/config/services.php", source)
	require.NoError(t, err)
	require.Len(t, services, 3)

	byID := make(map[string]Service, len(services))
	for _, service := range services {
		byID[service.ID] = service
	}
	handler := byID["App\\Contract\\HandlerInterface"]
	assert.Equal(t, "App\\Service\\Handler", handler.Class)
	assert.Equal(t, map[string]string{
		"app.default": "",
		"app.handler": "",
		"app.later":   "",
	}, handler.Tags)
	assert.Equal(t, map[string]string{"app.instanceof": "App\\Contract\\HandlerInterface"}, handler.InstanceofTags)
	assert.Equal(t, 15, handler.Line)

	migration := byID["App\\Service\\MigrationSource.core"]
	assert.Equal(t, "App\\Service\\Handler", migration.Class)
	assert.Contains(t, migration.Tags, "app.default")
	assert.Equal(t, map[string]string{"app.instanceof": "App\\Contract\\HandlerInterface"}, migration.InstanceofTags)

	alias := byID["app.handler_alias"]
	assert.Equal(t, "App\\Contract\\HandlerInterface", alias.AliasTarget)
	assert.Empty(t, alias.Class)
	_, removed := byID["removed.service"]
	assert.False(t, removed)

	require.Len(t, parameters, 2)
	assert.Equal(t, "value", parameters[0].Value)
	assert.Equal(t, "['one', 'two']", parameters[1].Value)
}

func TestParsePHPDeprecatedServiceMetadata(t *testing.T) {
	services, _, err := ParsePHPServices(
		"services.php",
		[]byte(`<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set('legacy', App\Legacy::class)
        ->deprecate('app/package', '1.0', 'Use App\Modern instead');
    $definition = $services->set('flagged', App\Flagged::class);
    $definition->deprecate('app/package', '1.0');
};`),
	)
	require.NoError(t, err)
	require.Len(t, services, 2)
	assert.True(t, services[0].Deprecated)
	assert.Equal(t, "Use App\\Modern instead", services[0].Deprecation)
	assert.NotZero(t, services[0].DeprecatedRange.Len())
	assert.True(t, services[1].Deprecated)
	assert.Empty(t, services[1].Deprecation)
}

func TestParsePHPServicePrototypes(t *testing.T) {
	source := []byte(`<?php
use App\Contract\Marker;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->instanceof(Marker::class)->tag('app.marker');
    $services->load('App\\', __DIR__ . '/../src/')
        ->exclude([
            __DIR__ . '/../src/Excluded',
            __DIR__ . '/../src/{Entity,Kernel.php}',
        ])
        ->tag('app.prototype');
    $services->set(null, Marker::class);
};
`)

	result := phpparser.ParseBytes(source)
	config, err := parsePHPServiceConfigTree(
		"/project/config/services.php",
		result.Tree.Root,
		phpsyntax.NewLineIndex(string(source)),
	)
	require.NoError(t, err)
	assert.Empty(t, config.Services, "anonymous service IDs must not be guessed")
	require.Len(t, config.Prototypes, 1)
	prototype := config.Prototypes[0]
	assert.Equal(t, "App\\", prototype.Namespace)
	assert.Equal(t, "/project/src", prototype.Resource)
	assert.Equal(t, []string{
		"/project/src/Excluded",
		"/project/src/{Entity,Kernel.php}",
	}, prototype.Excludes)
	assert.Equal(t, map[string]string{"app.prototype": ""}, prototype.Tags)
	assert.Equal(t, map[string]string{"app.marker": "App\\Contract\\Marker"}, prototype.InstanceofTags)
}

func TestParsePHPServicesDirectConfiguratorChains(t *testing.T) {
	source := []byte(`<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
use App\Service\Direct;
use App\Service\Invoked;

return static function (ContainerConfigurator $container): void {
    $definition = $container->services()->set(Direct::class);
    $definition->tag('app.direct');
    $services = $container->services();
    $services(Invoked::class)->tag('app.invoked');
    $container->services()->alias('direct.alias', Direct::class);
    $container->parameters()->set('direct.parameter', true);
};
`)

	services, parameters, err := ParsePHPServices("direct.php", source)
	require.NoError(t, err)
	require.Len(t, services, 3)
	assert.Equal(t, "App\\Service\\Direct", services[0].ID)
	assert.Contains(t, services[0].Tags, "app.direct")
	assert.Equal(t, "App\\Service\\Invoked", services[1].ID)
	assert.Contains(t, services[1].Tags, "app.invoked")
	assert.Equal(t, "direct.alias", services[2].ID)
	require.Len(t, parameters, 1)
	assert.Equal(t, "true", parameters[0].Value)
}

func TestParsePHPServicesRejectsDynamicAndUnrelatedPHP(t *testing.T) {
	source := []byte(`<?php
namespace App;

final class Factory
{
    public function configure(object $container, string $id): void
    {
        $services = $container->services();
        $services->set($id);
        $services->set("interpolated.$id");
    }
}
`)

	services, parameters, err := ParsePHPServices("Factory.php", source)
	require.NoError(t, err)
	assert.Empty(t, services)
	assert.Empty(t, parameters)
}

func TestParsePHPServicesToleratesIncompleteInput(t *testing.T) {
	source := []byte(`<?php
use App\Service\Complete;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set(Complete::class)->tag('complete');
    $services->set(
`)

	services, parameters, err := ParsePHPServices("services.php", source)
	require.NoError(t, err)
	require.Len(t, services, 1)
	assert.Equal(t, "App\\Service\\Complete", services[0].ID)
	assert.Contains(t, services[0].Tags, "complete")
	assert.Empty(t, parameters)
}

func TestParsePHPArrayServices(t *testing.T) {
	source := []byte(`<?php
namespace Symfony\Component\DependencyInjection\Loader\Configurator;

use App\Contract\HandlerInterface;
use App\Service\Decorator;
use App\Service\Handler;

return App::config([
    'parameters' => [
        'app.transport' => 'async',
    ],
    'services' => [
        '_defaults' => [
            'tags' => ['app.default'],
        ],
        '_instanceof' => [
            HandlerInterface::class => [
                'tags' => [['name' => 'app.handler']],
            ],
        ],
        'app.target' => [
            'class' => Handler::class,
            'tags' => ['app.explicit'],
        ],
        'app.alias' => '@app.target',
        'app.decorator' => [
            'class' => Decorator::class,
            'decorates' => 'app.target',
            'parent' => 'app.abstract',
            'deprecated' => 'Use app.target instead.',
        ],
        'App\\Feature\\' => [
            'resource' => '../src/Feature',
            'exclude' => ['../src/Feature/Excluded'],
            'tags' => ['app.prototype'],
        ],
    ],
]);
`)
	services, parameters, err := ParsePHPServices(
		"/project/config/services.php",
		source,
	)
	require.NoError(t, err)
	require.Len(t, services, 3)
	byID := make(map[string]Service, len(services))
	for _, service := range services {
		byID[service.ID] = service
	}
	target := byID["app.target"]
	assert.Equal(t, "App\\Service\\Handler", target.Class)
	assert.Contains(t, target.Tags, "app.default")
	assert.Contains(t, target.Tags, "app.explicit")
	assert.Equal(
		t,
		map[string]string{
			"app.handler": "App\\Contract\\HandlerInterface",
		},
		target.InstanceofTags,
	)

	alias := byID["app.alias"]
	assert.Equal(t, "app.target", alias.AliasTarget)

	decorator := byID["app.decorator"]
	assert.Equal(t, "App\\Service\\Decorator", decorator.Class)
	assert.Equal(t, "app.target", decorator.Decorates)
	assert.Equal(t, "app.abstract", decorator.Parent)
	assert.True(t, decorator.Deprecated)
	assert.Equal(t, "Use app.target instead.", decorator.Deprecation)

	require.Len(t, parameters, 1)
	assert.Equal(t, "app.transport", parameters[0].Name)
	assert.Equal(t, "async", parameters[0].Value)

	result := phpparser.ParseBytes(source)
	config := parsePHPArrayServiceConfig(
		"/project/config/services.php",
		result.Tree.Root,
		phpsyntax.NewLineIndex(string(source)),
	)
	require.Len(t, config.Prototypes, 1)
	prototype := config.Prototypes[0]
	assert.Equal(t, "App\\Feature\\", prototype.Namespace)
	assert.Equal(t, "/project/src/Feature", prototype.Resource)
	assert.Equal(
		t,
		[]string{"/project/src/Feature/Excluded"},
		prototype.Excludes,
	)
	assert.Contains(t, prototype.Tags, "app.default")
	assert.Contains(t, prototype.Tags, "app.prototype")
}

func TestPHPServiceArgumentReferencesConfigurator(t *testing.T) {
	source := []byte(`<?php
use App\Service\Consumer;
use App\Service\Logger;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set(Consumer::class)->args([
        service(Logger::class),
        '@app.logger',
    ]);
    $definition = $services->set('app.named', Consumer::class);
    $definition->arg('$logger', ref('app.logger'));
    $services->set('app.class', Consumer::class)->arg(0, Logger::class);
};
`)
	result := phpparser.ParseBytes(source)
	references, err := PHPServiceArgumentReferences(
		"/project/config/services.php",
		result.Tree.Root,
		phpsyntax.NewLineIndex(string(source)),
	)
	require.NoError(t, err)
	require.Len(t, references, 4)

	assert.Equal(t, "App\\Service\\Consumer", references[0].OwnerServiceID)
	assert.Equal(t, "App\\Service\\Logger", references[0].ServiceID)
	assert.Equal(t, 0, references[0].ParameterIndex)
	assert.Equal(t, "php_helper_expression", references[0].Replacement)
	assert.Equal(
		t,
		"Logger::class",
		string(source[references[0].Range.Start:references[0].Range.End]),
	)

	assert.Equal(t, "app.logger", references[1].ServiceID)
	assert.Equal(t, 1, references[1].ParameterIndex)
	assert.Equal(t, "php_raw_string", references[1].Replacement)
	assert.Equal(
		t,
		"@app.logger",
		string(source[references[1].Range.Start:references[1].Range.End]),
	)

	assert.Equal(t, "app.named", references[2].OwnerServiceID)
	assert.Equal(t, "$logger", references[2].ParameterName)
	assert.Equal(t, "php_helper_string", references[2].Replacement)
	assert.Equal(
		t,
		"app.logger",
		string(source[references[2].Range.Start:references[2].Range.End]),
	)

	assert.Equal(t, "app.class", references[3].OwnerServiceID)
	assert.Equal(t, "App\\Service\\Logger", references[3].ServiceID)
	assert.Equal(t, "php_raw_expression", references[3].Replacement)
	assert.Equal(
		t,
		"Logger::class",
		string(source[references[3].Range.Start:references[3].Range.End]),
	)
}

func TestPHPServiceArgumentReferencesLegacyArray(t *testing.T) {
	source := []byte(`<?php
namespace App\Config;

use App\Service\Logger;

return [
    'services' => [
        'App\Service\Consumer' => [
            'arguments' => [
                service('app.logger'),
                '$logger' => '@app.named_logger',
                2 => Logger::class,
            ],
        ],
    ],
];
`)
	result := phpparser.ParseBytes(source)
	references, err := PHPServiceArgumentReferences(
		"/project/config/services.php",
		result.Tree.Root,
		phpsyntax.NewLineIndex(string(source)),
	)
	require.NoError(t, err)
	require.Len(t, references, 3)

	for _, reference := range references {
		assert.Equal(t, "App\\Service\\Consumer", reference.OwnerClass)
		assert.Equal(t, "php", reference.Format)
	}
	assert.Equal(t, "app.logger", references[0].ServiceID)
	assert.Equal(t, 0, references[0].ParameterIndex)
	assert.Equal(t, "$logger", references[1].ParameterName)
	assert.Equal(t, -1, references[1].ParameterIndex)
	assert.Equal(t, "app.named_logger", references[1].ServiceID)
	assert.Equal(t, 2, references[2].ParameterIndex)
	assert.Equal(t, "App\\Service\\Logger", references[2].ServiceID)
}

func TestPHPServiceArgumentReferencesDirectConfiguratorChain(t *testing.T) {
	source := []byte(`<?php
namespace Symfony\Component\DependencyInjection\Loader\Configurator;

$container->services()
    ->set('app.consumer', \App\Service\Consumer::class)
    ->args([service('app.logger')]);
`)
	result := phpparser.ParseBytes(source)
	references, err := PHPServiceArgumentReferences(
		"/project/config/services.php",
		result.Tree.Root,
		phpsyntax.NewLineIndex(string(source)),
	)
	require.NoError(t, err)
	require.Len(t, references, 1)
	assert.Equal(t, "app.consumer", references[0].OwnerServiceID)
	assert.Equal(t, "App\\Service\\Consumer", references[0].OwnerClass)
	assert.Equal(t, "app.logger", references[0].ServiceID)
	assert.Equal(t, 0, references[0].ParameterIndex)
}

func TestPHPServiceMethodReferences(t *testing.T) {
	for _, fixture := range []struct {
		name          string
		source        string
		wantOwners    []string
		wantClasses   []string
		wantMethods   []string
		wantFragments []string
	}{
		{
			name: "configurator callback",
			source: `<?php
use App\Service\Consumer;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $definition = $services->set('app.consumer', Consumer::class);
    $definition->call('setLogger', []);
    $services->set(Consumer::class)->call('initialize');
};
`,
			wantOwners:    []string{"app.consumer", `App\Service\Consumer`},
			wantClasses:   []string{`App\Service\Consumer`, `App\Service\Consumer`},
			wantMethods:   []string{"setLogger", "initialize"},
			wantFragments: []string{"setLogger", "initialize"},
		},
		{
			name: "direct configurator chain",
			source: `<?php
namespace Symfony\Component\DependencyInjection\Loader\Configurator;

$container->services()
    ->set('app.consumer', \App\Service\Consumer::class)
    ->call('setLogger', []);
`,
			wantOwners:    []string{"app.consumer"},
			wantClasses:   []string{`App\Service\Consumer`},
			wantMethods:   []string{"setLogger"},
			wantFragments: []string{"setLogger"},
		},
		{
			name: "native array mapping and tuple",
			source: `<?php
use App\Service\Consumer;
use Symfony\Config\FrameworkConfig;

return FrameworkConfig::config([
    'services' => [
        Consumer::class => [
            'calls' => [
                'setLogger' => [],
                ['initialize', [], true],
            ],
        ],
    ],
]);
`,
			wantOwners:    []string{`App\Service\Consumer`, `App\Service\Consumer`},
			wantClasses:   []string{`App\Service\Consumer`, `App\Service\Consumer`},
			wantMethods:   []string{"setLogger", "initialize"},
			wantFragments: []string{"setLogger", "initialize"},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source := []byte(fixture.source)
			result := phpparser.ParseBytes(source)
			references, err := PHPServiceMethodReferences(
				"/project/config/services.php",
				result.Tree.Root,
				phpsyntax.NewLineIndex(fixture.source),
			)
			require.NoError(t, err)
			require.Len(t, references, len(fixture.wantMethods))
			for index, reference := range references {
				assert.Equal(t, fixture.wantOwners[index], reference.OwnerServiceID)
				assert.Equal(t, fixture.wantClasses[index], reference.OwnerClass)
				assert.Equal(t, fixture.wantMethods[index], reference.MethodName)
				assert.Equal(t, "php", reference.Format)
				assert.Equal(
					t,
					fixture.wantFragments[index],
					string(source[reference.Range.Start:reference.Range.End]),
				)
			}
		})
	}
}

func BenchmarkParsePHPServices(b *testing.B) {
	source := benchmarkPHPServicesSource()
	b.ReportAllocs()
	for b.Loop() {
		_, _, _ = ParsePHPServices("services.php", source)
	}
}

func BenchmarkParsePHPServicesTree(b *testing.B) {
	source := benchmarkPHPServicesSource()
	root := phpparser.ParseBytes(source).Tree.Root
	lineIndex := phpsyntax.NewLineIndex(string(source))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = ParsePHPServicesTree("services.php", root, lineIndex)
	}
}

func benchmarkPHPServicesSource() []byte {
	return []byte(`<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
use App\Service\One;
use App\Service\Two;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->defaults()->autowire()->autoconfigure();
    $services->set(One::class)->tag('app.one');
    $services->set(Two::class)->arg('$one', service(One::class));
    $services->alias('app.two', Two::class);
    $container->parameters()->set('app.enabled', true);
};
`)
}
