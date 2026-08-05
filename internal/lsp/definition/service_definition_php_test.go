package definition

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPServiceConfigDefinitions(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })

	servicePath := filepath.Join(t.TempDir(), "definitions.php")
	config := []byte(`<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $container->services()->set('app.target', \App\Target::class)->tag('app.tag');
    $container->parameters()->set('app.parameter', 'value');
};
`)
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(servicePath, config)))

	classPath := filepath.Join(t.TempDir(), "Target.php")
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		[]byte(`<?php namespace App;
final class Target {
    public function configure(): void {}
    public static function create(): self { return new self(); }
}`),
	)))
	provider := NewServiceXMLDefinitionProvider(serviceIndex, phpIndex)

	t.Run("service helper", func(t *testing.T) {
		source := "<?php /* ContainerConfigurator */ service('app.target');"
		root := phpparser.Parse(source).Tree.Root
		node := phpquery.StringArgument(phpquery.Calls(root)[0], 0)
		locations := provider.GetDefinition(context.Background(), phpDefinitionRequest(source, root, node))
		require.Len(t, locations, 1)
		assert.Equal(t, uriutil.FileURI(servicePath), locations[0].URI)
	})

	t.Run("class constant", func(t *testing.T) {
		source := `<?php
use App\Target;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
service(Target::class);
`
		root := phpparser.Parse(source).Tree.Root
		node := phpquery.Nodes(root, phpsyntax.PhpMemberAccess)[0]
		locations := provider.GetDefinition(context.Background(), phpDefinitionRequest(source, root, node))
		require.Len(t, locations, 1)
		assert.Equal(t, uriutil.FileURI(classPath), locations[0].URI)
	})

	t.Run("Autowire service attribute", func(t *testing.T) {
		source := "<?php #[Autowire(service: 'app.target')] class Example {}"
		root := phpparser.Parse(source).Tree.Root
		node := phpquery.Nodes(root, phpsyntax.PhpString)[0]
		locations := provider.GetDefinition(
			context.Background(),
			phpDefinitionRequest(source, root, node),
		)
		require.Len(t, locations, 1)
		assert.Equal(t, uriutil.FileURI(servicePath), locations[0].URI)
	})

	t.Run("Autowire parameter attribute", func(t *testing.T) {
		source := "<?php #[Autowire(param: 'app.parameter')] class Example {}"
		root := phpparser.Parse(source).Tree.Root
		node := phpquery.Nodes(root, phpsyntax.PhpString)[0]
		locations := provider.GetDefinition(
			context.Background(),
			phpDefinitionRequest(source, root, node),
		)
		require.Len(t, locations, 1)
		assert.Equal(t, uriutil.FileURI(servicePath), locations[0].URI)
	})

	for name, source := range map[string]string{
		"AutowireServiceClosure": "<?php #[AutowireServiceClosure('app.target')] class Example {}",
		"AutowireMethodOf":       "<?php #[AutowireMethodOf(service: 'app.target')] class Example {}",
		"AutowireCallable":       "<?php #[AutowireCallable(service: 'app.target')] class Example {}",
		"AutowireLocator":        "<?php #[AutowireLocator(services: ['app.target'])] class Example {}",
	} {
		t.Run(name+" service attribute", func(t *testing.T) {
			root := phpparser.Parse(source).Tree.Root
			node := phpquery.Nodes(root, phpsyntax.PhpString)[0]
			locations := provider.GetDefinition(
				context.Background(),
				phpDefinitionRequest(source, root, node),
			)
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(servicePath), locations[0].URI)
		})
	}

	for name, source := range map[string]string{
		"tagged_iterator helper": "<?php /* ContainerConfigurator */ tagged_iterator('app.tag');",
		"TaggedIterator":         "<?php #[TaggedIterator('app.tag')] class Example {}",
		"TaggedLocator":          "<?php #[TaggedLocator(tag: 'app.tag')] class Example {}",
		"Autoconfigure":          "<?php #[Autoconfigure(tags: ['app.tag'])] class Example {}",
		"AutoconfigureTag":       "<?php #[AutoconfigureTag(name: 'app.tag')] class Example {}",
	} {
		t.Run(name+" tag definition", func(t *testing.T) {
			root := phpparser.Parse(source).Tree.Root
			node := phpquery.Nodes(root, phpsyntax.PhpString)[0]
			locations := provider.GetDefinition(
				context.Background(),
				phpDefinitionRequest(source, root, node),
			)
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(servicePath), locations[0].URI)
		})
	}

	t.Run("typed container get", func(t *testing.T) {
		source := `<?php
use Psr\Container\ContainerInterface;
function resolve(ContainerInterface $container): void {
    $container->get('app.target');
}`
		root := phpparser.Parse(source).Tree.Root
		node := phpquery.Nodes(root, phpsyntax.PhpString)[0]
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			"/project/resolve.php",
			1,
			node,
			root,
		)
		locations := provider.GetDefinition(
			ctx,
			phpDefinitionRequest(source, root, node),
		)
		require.Len(t, locations, 1)
		assert.Equal(t, uriutil.FileURI(servicePath), locations[0].URI)
	})

	for _, interfaceName := range []string{
		"ParameterBagInterface",
		"ContainerBagInterface",
	} {
		for _, methodName := range []string{"get", "has"} {
			t.Run(interfaceName+" "+methodName, func(t *testing.T) {
				source := `<?php /* ContainerConfigurator */
use Symfony\Component\DependencyInjection\ParameterBag\` +
					interfaceName + `;
final class Resolver {
    public function __construct(private ` + interfaceName + ` $bag) {}
    public function resolve(): void {
        $this->bag->` + methodName + `('app.parameter');
    }
}`
				root := phpparser.Parse(source).Tree.Root
				node := phpquery.Nodes(root, phpsyntax.PhpString)[0]
				ctx := phpIndex.AddDocumentContext(
					context.Background(),
					"/project/resolve.php",
					1,
					node,
					root,
				)
				locations := provider.GetDefinition(
					ctx,
					phpDefinitionRequest(source, root, node),
				)
				require.Len(t, locations, 1)
				assert.Equal(
					t,
					uriutil.FileURI(servicePath),
					locations[0].URI,
				)
			})
		}
	}

	for _, test := range []struct {
		name       string
		importName string
		shortName  string
		method     string
	}{
		{
			name: "DI container getParameter",
			importName: "Symfony\\Component\\DependencyInjection\\" +
				"ContainerInterface",
			shortName: "ContainerInterface",
			method:    "getParameter",
		},
		{
			name: "DI container hasParameter",
			importName: "Symfony\\Component\\DependencyInjection\\" +
				"ContainerInterface",
			shortName: "ContainerInterface",
			method:    "hasParameter",
		},
		{
			name: "parameter bag set",
			importName: "Symfony\\Component\\DependencyInjection\\" +
				"ParameterBag\\ParameterBagInterface",
			shortName: "ParameterBagInterface",
			method:    "set",
		},
		{
			name: "parameters configurator set",
			importName: "Symfony\\Component\\DependencyInjection\\" +
				"Loader\\Configurator\\ParametersConfigurator",
			shortName: "ParametersConfigurator",
			method:    "set",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "<?php\nuse " + test.importName + ";\n" +
				"function resolve(" + test.shortName + " $parameters): void {\n" +
				"    $parameters->" + test.method + "('app.parameter');\n}"
			root := phpparser.Parse(source).Tree.Root
			node := phpquery.Nodes(root, phpsyntax.PhpString)[0]
			ctx := phpIndex.AddDocumentContext(
				context.Background(),
				"/project/resolve.php",
				1,
				node,
				root,
			)
			locations := provider.GetDefinition(
				ctx,
				phpDefinitionRequest(source, root, node),
			)
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(servicePath), locations[0].URI)
		})
	}

	for name, source := range map[string]string{
		"constructor argument": `<?php return ['services' => [
    'app.consumer' => ['arguments' => ['%app.parameter%']],
]];`,
		"call argument": `<?php return ['services' => [
    'app.consumer' => ['calls' => [['configure', ['%app.parameter%']]]],
]];`,
	} {
		t.Run("PHP array "+name+" parameter", func(t *testing.T) {
			root := phpparser.Parse(source).Tree.Root
			var node *phpsyntax.Node
			for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
				if phpquery.StringValue(literal) == "%app.parameter%" {
					node = literal
					break
				}
			}
			require.NotNil(t, node)
			locations := provider.GetDefinition(
				context.Background(),
				phpDefinitionRequest(source, root, node),
			)
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(servicePath), locations[0].URI)
		})
	}

	for name, source := range map[string]string{
		"parent": `<?php return ['services' => [
    'app.consumer' => ['parent' => 'app.target'],
]];`,
		"argument service": `<?php return ['services' => [
    'app.consumer' => ['arguments' => ['@app.target']],
]];`,
		"tag": `<?php return ['services' => [
    'app.consumer' => ['tags' => ['app.tag']],
]];`,
		"class string": `<?php return ['services' => [
    'app.consumer' => ['class' => 'App\\Target'],
]];`,
		"service-level alias": `<?php return ['services' => [
    'app.alias' => '@app.target',
]];`,
		"factory service": `<?php return ['services' => [
    'app.consumer' => ['factory' => ['@app.target', 'create']],
]];`,
		"service helper": `<?php return ['services' => [
    'app.consumer' => ['arguments' => [service('app.target')]],
]];`,
		"call method": `<?php return ['services' => [
    'app.consumer' => [
        'class' => 'App\\Target',
        'calls' => [['configure', []]],
    ],
]];`,
		"factory method": `<?php return ['services' => [
    'app.consumer' => [
        'factory' => ['@app.target', 'create'],
    ],
]];`,
	} {
		t.Run("PHP array "+name+" definition", func(t *testing.T) {
			root := phpparser.Parse(source).Tree.Root
			var node *phpsyntax.Node
			for _, literal := range phpquery.Nodes(
				root,
				phpsyntax.PhpString,
			) {
				if symfony.PHPArrayServiceReferenceAt(literal) !=
					symfony.PHPConfigReferenceNone ||
					symfony.PHPConfigReferenceAt(literal) !=
						symfony.PHPConfigReferenceNone {
					node = literal
				}
				if name == "call method" || name == "factory method" {
					if _, found := symfony.PHPArrayServiceMethodAt(
						root,
						literal,
					); found {
						node = literal
					}
				}
			}
			require.NotNil(t, node)
			locations := provider.GetDefinition(
				context.Background(),
				phpDefinitionRequest(source, root, node),
			)
			require.Len(t, locations, 1)
			expectedURI := uriutil.FileURI(servicePath)
			if name == "class string" ||
				name == "call method" ||
				name == "factory method" {
				expectedURI = uriutil.FileURI(classPath)
			}
			assert.Equal(t, expectedURI, locations[0].URI)
		})
	}
}

func TestPHPDocServiceAssistantTagDefinitions(t *testing.T) {
	cacheDir := t.TempDir()
	phpIndex, err := php.NewPHPIndex(filepath.Join(cacheDir, "php"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(
		filepath.Join(cacheDir, "services"),
		cacheDir,
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	servicePath := filepath.Join(cacheDir, "services.xml")
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		servicePath,
		[]byte(`<container><services>
<service id="app.target" class="App\Target"/>
</services></container>`),
	)))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(cacheDir, "ServiceAware.php"),
		[]byte(`<?php
interface ServiceAware
{
    /** @param string $service #Service */
    public function connect(string $service): void;
}

class ServiceConsumer implements ServiceAware
{
    public function connect(string $service): void {}

    /** @param string $value #Route */
    public function route(string $value): void {}
}

/** @param string $service #Service */
function resolve_service(string $service): void {}
`),
	)))
	provider := NewServiceXMLDefinitionProvider(serviceIndex, phpIndex)
	for _, fixture := range []struct {
		name      string
		source    string
		value     string
		hasTarget bool
	}{
		{
			name:      "function",
			source:    "<?php resolve_service('app.target');",
			value:     "app.target",
			hasTarget: true,
		},
		{
			name: "inherited method contract",
			source: `<?php
$consumer = new ServiceConsumer();
$consumer->connect('app.target');
`,
			value:     "app.target",
			hasTarget: true,
		},
		{
			name: "different assistant tag",
			source: `<?php
$consumer = new ServiceConsumer();
$consumer->route('app.target');
`,
			value: "app.target",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := phpparser.Parse(fixture.source).Tree.Root
			var node *phpsyntax.Node
			for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
				if phpquery.StringValue(literal) == fixture.value {
					node = literal
					break
				}
			}
			require.NotNil(t, node)
			ctx := phpIndex.AddDocumentContext(
				context.Background(),
				"/project/src/Usage.php",
				1,
				node,
				root,
			)
			locations := provider.GetDefinition(
				ctx,
				phpDefinitionRequest(fixture.source, root, node),
			)
			if !fixture.hasTarget {
				assert.Empty(t, locations)
				return
			}
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(servicePath), locations[0].URI)
		})
	}
}

func TestPHPDocParameterAssistantTagDefinitions(t *testing.T) {
	cacheDir := t.TempDir()
	phpIndex, err := php.NewPHPIndex(filepath.Join(cacheDir, "php"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(
		filepath.Join(cacheDir, "services"),
		cacheDir,
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	parameterPath := filepath.Join(cacheDir, "parameters.php")
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		parameterPath,
		[]byte(`<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $container->parameters()->set('app.feature', true);
};`),
	)))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(cacheDir, "ParameterAware.php"),
		[]byte(`<?php
interface ParameterAware
{
    /** @param string $name #Parameter */
    public function read(string $name): void;
}
class ParameterConsumer implements ParameterAware
{
    public function read(string $name): void {}

    /** @param string $service #Service */
    public function service(string $service): void {}
}
`),
	)))
	provider := NewServiceXMLDefinitionProvider(serviceIndex, phpIndex)
	for _, fixture := range []struct {
		name      string
		method    string
		hasTarget bool
	}{
		{
			name:      "inherited method contract",
			method:    "read",
			hasTarget: true,
		},
		{
			name:   "different assistant tag",
			method: "service",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source := "<?php\n$consumer = new ParameterConsumer();\n" +
				"$consumer->" + fixture.method + "('app.feature');\n"
			root := phpparser.Parse(source).Tree.Root
			node := phpquery.Nodes(root, phpsyntax.PhpString)[0]
			ctx := phpIndex.AddDocumentContext(
				context.Background(),
				"/project/src/Usage.php",
				1,
				node,
				root,
			)
			locations := provider.GetDefinition(
				ctx,
				phpDefinitionRequest(source, root, node),
			)
			if !fixture.hasTarget {
				assert.Empty(t, locations)
				return
			}
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(parameterPath), locations[0].URI)
		})
	}
}

func TestAutowireCallableMethodDefinitions(t *testing.T) {
	cacheDir := t.TempDir()
	phpIndex, err := php.NewPHPIndex(filepath.Join(cacheDir, "php"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(
		filepath.Join(cacheDir, "services"),
		cacheDir,
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })

	classPath := filepath.Join(cacheDir, "Formatter.php")
	classSource := `<?php
namespace App;
class Formatter {
    public function format(): string { return ''; }
    private function privateMethod(): void {}
    public static function staticMethod(): void {}
}
`
	require.NoError(t, os.WriteFile(classPath, []byte(classSource), 0o644))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		[]byte(classSource),
	)))
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		filepath.Join(cacheDir, "services.xml"),
		[]byte(`<container><services>
<service id="app.formatter" class="App\Formatter"/>
</services></container>`),
	)))
	provider := NewServiceXMLDefinitionProvider(serviceIndex, phpIndex)

	for _, source := range []string{
		"<?php #[AutowireCallable(service: 'app.formatter', method: 'format')] class Consumer {}",
		`<?php
use App\Formatter as TargetFormatter;
#[AutowireCallable(service: TargetFormatter::class, method: 'format')]
class Consumer {}`,
	} {
		root := phpparser.Parse(source).Tree.Root
		var node *phpsyntax.Node
		for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
			if phpquery.StringValue(literal) == "format" {
				node = literal
				break
			}
		}
		require.NotNil(t, node)
		locations := provider.GetDefinition(
			context.Background(),
			phpDefinitionRequest(source, root, node),
		)
		require.Len(t, locations, 1)
		assert.Equal(t, uriutil.FileURI(classPath), locations[0].URI)
	}

	for _, method := range []string{"privateMethod", "staticMethod"} {
		source := "<?php #[AutowireCallable(service: 'app.formatter', method: '" +
			method + "')] class Consumer {}"
		root := phpparser.Parse(source).Tree.Root
		node := phpquery.Nodes(root, phpsyntax.PhpString)[1]
		assert.Empty(t, provider.GetDefinition(
			context.Background(),
			phpDefinitionRequest(source, root, node),
		))
	}
}

func phpDefinitionRequest(source string, root, node *phpsyntax.Node) *lsp.DefinitionRequest {
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = "file:///project/config/services.php"
	return &lsp.DefinitionRequest{
		DefinitionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Root:            root,
			Node:            node,
			DocumentContent: []byte(source),
		},
	}
}
