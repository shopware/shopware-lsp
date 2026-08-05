package completion

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPServiceConfigCompletions(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })

	config := []byte(`<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set('app.target')->tag('app.tag');
    $container->parameters()->set('app.parameter', 'value');
};
`)
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile("definitions.php", config)))
	provider := NewServiceCompletionProvider(serviceIndex, phpIndex)

	tests := []struct {
		source string
		label  string
	}{
		{"<?php /* ContainerConfigurator */ service('');", "app.target"},
		{"<?php /* ContainerConfigurator */ param('');", "app.parameter"},
		{"<?php /* ContainerConfigurator */ tagged_iterator('');", "app.tag"},
		{"<?php #[Autowire(service: '')] class Example {}", "app.target"},
		{"<?php #[Autowire(param: '')] class Example {}", "app.parameter"},
		{"<?php #[AutowireServiceClosure('')] class Example {}", "app.target"},
		{"<?php #[AutowireMethodOf(service: '')] class Example {}", "app.target"},
		{"<?php #[AutowireCallable(service: '')] class Example {}", "app.target"},
		{"<?php #[AutowireLocator(services: [''])] class Example {}", "app.target"},
		{"<?php #[AutowireLocator(exclude: '')] class Example {}", "app.target"},
		{"<?php #[TaggedIterator('')] class Example {}", "app.tag"},
		{"<?php #[TaggedLocator(tag: '')] class Example {}", "app.tag"},
		{"<?php #[Autoconfigure(tags: [''])] class Example {}", "app.tag"},
		{"<?php #[AutoconfigureTag(name: '')] class Example {}", "app.tag"},
	}
	for _, test := range tests {
		root := phpparser.Parse(test.source).Tree.Root
		node := phpquery.Nodes(root, phpsyntax.PhpString)[0]
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = "file:///project/config/services.php"
		items := provider.GetCompletions(context.Background(), &lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Root:            root,
				Node:            node,
				DocumentContent: []byte(test.source),
			},
		})
		labels := make([]string, 0, len(items))
		for _, item := range items {
			labels = append(labels, item.Label)
		}
		assert.Contains(t, labels, test.label)
	}

	t.Run("typed container get", func(t *testing.T) {
		source := `<?php
use Psr\Container\ContainerInterface;
function resolve(ContainerInterface $container): void {
    $container->get('');
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
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = "file:///project/resolve.php"
		items := provider.GetCompletions(ctx, &lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Root:            root,
				Node:            node,
				DocumentContent: []byte(source),
			},
		})
		var labels []string
		for _, item := range items {
			labels = append(labels, item.Label)
		}
		assert.Contains(t, labels, "app.target")
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
        $this->bag->` + methodName + `('');
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
				params := &protocol.CompletionParams{}
				params.TextDocument.URI = "file:///project/resolve.php"
				items := provider.GetCompletions(
					ctx,
					&lsp.CompletionRequest{
						CompletionParams: params,
						SyntaxContext: lsp.SyntaxContext{
							Root:            root,
							Node:            node,
							DocumentContent: []byte(source),
						},
					},
				)
				var labels []string
				for _, item := range items {
					labels = append(labels, item.Label)
				}
				assert.Contains(t, labels, "app.parameter")
				assert.NotContains(t, labels, "app.target")
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
				"    $parameters->" + test.method + "('');\n}"
			root := phpparser.Parse(source).Tree.Root
			node := phpquery.Nodes(root, phpsyntax.PhpString)[0]
			ctx := phpIndex.AddDocumentContext(
				context.Background(),
				"/project/resolve.php",
				1,
				node,
				root,
			)
			params := &protocol.CompletionParams{}
			params.TextDocument.URI = "file:///project/resolve.php"
			items := provider.GetCompletions(
				ctx,
				&lsp.CompletionRequest{
					CompletionParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Root:            root,
						Node:            node,
						DocumentContent: []byte(source),
					},
				},
			)
			var labels []string
			for _, item := range items {
				labels = append(labels, item.Label)
			}
			assert.Contains(t, labels, "app.parameter")
			assert.NotContains(t, labels, "app.target")
		})
	}
}

func TestPHPDocServiceAssistantTagCompletesCallArguments(t *testing.T) {
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
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		filepath.Join(cacheDir, "services.xml"),
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
    /** @param string $service #Service */
    public function __construct(string $service) {}

    public function connect(string $service): void {}

    /** @param string $value #Route */
    public function route(string $value): void {}
}

/** @param string $service #Service */
function resolve_service(string $service): void {}
`),
	)))
	provider := NewServiceCompletionProvider(serviceIndex, phpIndex)
	for _, fixture := range []struct {
		name   string
		source string
		found  bool
	}{
		{
			name:   "named constructor argument",
			source: "<?php new ServiceConsumer(service: 'app.<caret>');",
			found:  true,
		},
		{
			name: "inherited method contract",
			source: `<?php
$consumer = new ServiceConsumer('app.seed');
$consumer->connect('app.<caret>');
`,
			found: true,
		},
		{
			name:   "function",
			source: "<?php resolve_service('app.<caret>');",
			found:  true,
		},
		{
			name: "different assistant tag",
			source: `<?php
$consumer = new ServiceConsumer('app.seed');
$consumer->route('app.<caret>');
`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, offset := completionCaret(t, fixture.source)
			document, request := bundleResourceCompletionRequest(
				t,
				"/project/src/Usage.php",
				source,
				offset,
			)
			ctx := phpIndex.AddDocumentContext(
				context.Background(),
				"/project/src/Usage.php",
				document.Version,
				request.Node,
				request.Root,
			)
			items := provider.GetCompletions(ctx, request)
			if !fixture.found {
				assert.NotContains(t, completionLabels(items), "app.target")
				return
			}
			item := requireCompletion(t, items, "app.target")
			assert.Equal(t, "App\\Target", item.Detail)
			edit, ok := item.TextEdit.(protocol.TextEdit)
			require.True(t, ok)
			assert.Equal(t, "app.target", edit.NewText)
			assert.Equal(
				t,
				"app.",
				completionRangeText(document, edit.Range),
			)
		})
	}
}

func TestPHPDocParameterAssistantTagCompletesCallArguments(t *testing.T) {
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
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		filepath.Join(cacheDir, "parameters.php"),
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
/** @param string $name #Parameter */
function resolve_parameter(string $name): void {}
`),
	)))
	provider := NewServiceCompletionProvider(serviceIndex, phpIndex)
	for _, fixture := range []struct {
		name   string
		source string
		found  bool
	}{
		{
			name:   "named function argument",
			source: "<?php resolve_parameter(name: 'app.<caret>');",
			found:  true,
		},
		{
			name: "inherited method contract",
			source: `<?php
$consumer = new ParameterConsumer();
$consumer->read('app.<caret>');
`,
			found: true,
		},
		{
			name: "different assistant tag",
			source: `<?php
$consumer = new ParameterConsumer();
$consumer->service('app.<caret>');
`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, offset := completionCaret(t, fixture.source)
			document, request := bundleResourceCompletionRequest(
				t,
				"/project/src/Usage.php",
				source,
				offset,
			)
			ctx := phpIndex.AddDocumentContext(
				context.Background(),
				"/project/src/Usage.php",
				document.Version,
				request.Node,
				request.Root,
			)
			items := provider.GetCompletions(ctx, request)
			if !fixture.found {
				assert.NotContains(t, completionLabels(items), "app.feature")
				return
			}
			item := requireCompletion(t, items, "app.feature")
			assert.Equal(t, int(protocol.ReferenceCompletion), item.Kind)
			edit, ok := item.TextEdit.(protocol.TextEdit)
			require.True(t, ok)
			assert.Equal(t, "app.feature", edit.NewText)
			assert.Equal(
				t,
				"app.",
				completionRangeText(document, edit.Range),
			)
		})
	}
}

func TestAutowireCallableMethodCompletions(t *testing.T) {
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

	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		filepath.Join(cacheDir, "Formatter.php"),
		[]byte(`<?php
namespace App;
class Formatter {
    public function format(): string { return ''; }
    public function process(): void {}
    private function privateMethod(): void {}
    public static function staticMethod(): void {}
}
`),
	)))
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		filepath.Join(cacheDir, "services.xml"),
		[]byte(`<container><services>
<service id="app.formatter" class="App\Formatter"/>
<service id="app.formatter_alias" alias="app.formatter"/>
</services></container>`),
	)))
	provider := NewServiceCompletionProvider(serviceIndex, phpIndex)

	for _, source := range []string{
		"<?php #[AutowireCallable(service: 'app.formatter_alias', method: '')] class Consumer {}",
		`<?php
use App\Formatter as TargetFormatter;
#[AutowireCallable(service: TargetFormatter::class, method: '')]
class Consumer {}`,
	} {
		root := phpparser.Parse(source).Tree.Root
		var node *phpsyntax.Node
		for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
			if phpquery.StringValue(literal) == "" {
				node = literal
				break
			}
		}
		require.NotNil(t, node)
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = "file:///project/src/Consumer.php"
		lineIndex := phpsyntax.NewLineIndex(source)
		items := provider.GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Root:            root,
					Node:            node,
					DocumentContent: []byte(source),
					LineIndex:       lineIndex,
				},
			},
		)
		var labels []string
		for _, item := range items {
			labels = append(labels, item.Label)
		}
		assert.Contains(t, labels, "format")
		assert.Contains(t, labels, "process")
		assert.NotContains(t, labels, "privateMethod")
		assert.NotContains(t, labels, "staticMethod")
	}
}

func TestWhenAttributeEnvironmentCompletions(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	provider := NewServiceCompletionProvider(serviceIndex, phpIndex)

	for _, source := range []string{
		"<?php #[When('')] class Example {}",
		"<?php #[When(env: '')] class Example {}",
	} {
		root := phpparser.Parse(source).Tree.Root
		node := phpquery.Nodes(root, phpsyntax.PhpString)[0]
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = "file:///project/src/Example.php"
		items := provider.GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Root:            root,
					Node:            node,
					DocumentContent: []byte(source),
				},
			},
		)
		var labels []string
		for _, item := range items {
			labels = append(labels, item.Label)
		}
		assert.ElementsMatch(
			t,
			[]string{"prod", "dev", "test", "never"},
			labels,
		)
	}
}

func TestPHPArrayServiceParameterCompletions(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	serviceIndex, err := symfony.NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		"parameters.php",
		[]byte(`<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;
return static function (ContainerConfigurator $container): void {
    $container->parameters()->set('app.parameter', true);
    $container->services()->set('app.target', \App\Target::class)->tag('app.tag');
};`),
	)))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Target.php",
		[]byte(`<?php namespace App;
class Target {
    public function configure(): void {}
    public static function create(): self { return new self(); }
    private function hidden(): void {}
}`),
	)))
	provider := NewServiceCompletionProvider(serviceIndex, phpIndex)

	for _, source := range []string{
		`<?php return ['services' => [
    'app.consumer' => ['arguments' => ['%app.']],
]];`,
		`<?php return ['services' => [
    'app.consumer' => ['calls' => [['configure', ['%app.']]]],
]];`,
	} {
		document := lsp.NewTextDocument(
			"file:///project/config/services.php",
			source,
			1,
		)
		offset := uint32(strings.Index(source, "%app.") + len("%app."))
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		items := provider.GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Root:            document.SyntaxTree.Root,
					Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
					DocumentContent: document.Text,
					LineIndex:       document.LineIndex,
				},
			},
		)
		require.Len(t, items, 1)
		assert.Equal(t, "%app.parameter%", items[0].Label)
		edit, ok := items[0].TextEdit.(protocol.TextEdit)
		require.True(t, ok)
		assert.Equal(t, "%app.parameter%", edit.NewText)
	}

	for _, test := range []struct {
		source     string
		label      string
		prohibited string
	}{
		{
			source: `<?php return ['services' => [
    'app.consumer' => ['parent' => ''],
]];`,
			label: "app.target",
		},
		{
			source: `<?php return ['services' => [
    'app.consumer' => ['arguments' => ['@']],
]];`,
			label: "app.target",
		},
		{
			source: `<?php return ['services' => [
    'app.consumer' => ['tags' => ['']],
]];`,
			label: "app.tag",
		},
		{
			source: `<?php return ['services' => [
    'app.consumer' => ['class' => ''],
]];`,
			label: "App\\Target",
		},
		{
			source: `<?php return ['services' => [
    'app.alias' => '@',
]];`,
			label: "app.target",
		},
		{
			source: `<?php return ['services' => [
    'app.consumer' => ['factory' => ['@', 'create']],
]];`,
			label: "app.target",
		},
		{
			source: `<?php return ['services' => [
    'app.consumer' => ['arguments' => [service('')]],
]];`,
			label: "app.target",
		},
		{
			source: `<?php return ['services' => [
    'app.consumer' => [
        'class' => 'App\\Target',
        'calls' => [['', []]],
    ],
]];`,
			label:      "configure",
			prohibited: "create",
		},
		{
			source: `<?php return ['services' => [
    'app.consumer' => [
        'factory' => ['@app.target', ''],
    ],
]];`,
			label: "create",
		},
	} {
		root := phpparser.Parse(test.source).Tree.Root
		var target *phpsyntax.Node
		for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
			if symfony.PHPArrayServiceReferenceAt(literal) !=
				symfony.PHPConfigReferenceNone ||
				symfony.PHPConfigReferenceAt(literal) !=
					symfony.PHPConfigReferenceNone {
				target = literal
			}
			if test.label == "configure" || test.label == "create" {
				if _, found := symfony.PHPArrayServiceMethodAt(
					root,
					literal,
				); found {
					target = literal
				}
			}
		}
		require.NotNil(t, target)
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = "file:///project/config/services.php"
		items := provider.GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Root:            root,
					Node:            target,
					DocumentContent: []byte(test.source),
				},
			},
		)
		var labels []string
		for _, item := range items {
			labels = append(labels, item.Label)
		}
		assert.Contains(t, labels, test.label)
		if test.prohibited != "" {
			assert.NotContains(t, labels, test.prohibited)
		}
	}
}
