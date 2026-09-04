package diagnostics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestXMLServiceArgumentDiagnostics(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.xml",
		`<container><services>
  <defaults autowire="false"/>
  <service id="app.complete" class="App\NeedsArguments">
    <argument type="service" id="app.logger"/>
    <argument key="$name">name</argument>
  </service>
  <service id="app.missing" class="App\NeedsArguments">
    <argument type="service" id="app.logger"/>
  </service>
  <service id="app.autowired" class="App\NeedsArguments" autowire="true"/>
  <service id="app.factory" class="App\NeedsArguments"><factory service="factory"/></service>
</services></container>`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, missingServiceArgumentsCode, result[0].ID)
	assert.Contains(t, result[0].Message, "$name")
	data := result[0].Payload.(map[string]any)
	assert.Equal(t, "xml", data["format"])
	arguments := data["missingArguments"].([]map[string]any)
	require.Len(t, arguments, 1)
	assert.Equal(t, "$name", arguments[0]["name"])
	assert.Equal(t, "?", arguments[0]["suggestedService"])
}

func TestYAMLServiceArgumentDiagnostics(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.yaml",
		`services:
  _defaults:
    autowire: true

  app.autowired:
    class: App\NeedsArguments

  app.complete:
    class: App\NeedsArguments
    autowire: false
    arguments: ['@app.logger', 'name']

  app.missing:
    class: App\NeedsArguments
    autowire: false
    arguments:
      $logger: '@app.logger'

  app.unsupported:
    class: App\NeedsArguments
    autowire: false
    factory: ['@factory', create]
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, missingServiceArgumentsCode, result[0].ID)
	assert.Contains(t, result[0].Message, "$name")
	assert.NotContains(t, result[0].Message, "$logger")
}

func TestServiceArgumentDiagnosticsSuggestUniqueTypedService(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.yaml",
		`services:
  app.consumer:
    class: App\NeedsArguments
    arguments: []
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	data := result[0].Payload.(map[string]any)
	arguments := data["missingArguments"].([]map[string]any)
	require.Len(t, arguments, 2)
	assert.Equal(t, "$logger", arguments[0]["name"])
	assert.Equal(t, "app.logger", arguments[0]["suggestedService"])
	assert.Equal(t, "$name", arguments[1]["name"])
	assert.Equal(t, "?", arguments[1]["suggestedService"])
}

func TestYAMLServiceNamedArgumentDiagnostics(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	source := `services:
  app.consumer:
    class: App\NeedsArguments
    arguments:
      $logger: '@app.logger'
      $name: product
      $nam: typo
`
	document := lsp.NewTextDocument(
		"file:///project/services.yaml",
		source,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, unknownServiceNamedArgumentCode, result[0].ID)
	assert.Equal(t, "Symfony: named argument does not exists", result[0].Message)
	assert.Equal(t, "$nam", problemRangeText(document, result[0].Range))
	data := result[0].Payload.(map[string]any)
	assert.Contains(t, data["suggestions"], "$name")
}

func TestYAMLServiceNamedArgumentDiagnosticsSupportInheritedConstructor(
	t *testing.T,
) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.yaml",
		`services:
  app.consumer:
    class: App\ChildArguments
    arguments:
      $logger: '@app.logger'
      $name: product
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestYAMLServiceNamedArgumentDiagnosticsSkipFactoryAndUnknownClass(
	t *testing.T,
) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.yaml",
		`services:
  app.factory:
    class: App\NeedsArguments
    factory: ['@factory', create]
    arguments:
      $missing: value
  app.unknown:
    class: App\Missing
    arguments:
      $missing: value
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestYAMLServiceArgumentTypeDiagnostics(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	source := `services:
  app.consumer:
    class: App\NeedsArguments
    arguments:
      $logger: '@app.wrong'
      $name: product
`
	document := lsp.NewTextDocument(
		"file:///project/services.yaml",
		source,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, invalidServiceArgumentTypeCode, result[0].ID)
	assert.Equal(
		t,
		"Expect instance of: App\\LoggerInterface",
		result[0].Message,
	)
	assert.Equal(t, "@app.wrong", problemRangeText(document, result[0].Range))
	data := result[0].Payload.(map[string]any)
	assert.Contains(t, data["suggestions"], "@app.logger")
}

func TestXMLServiceArgumentTypeDiagnostics(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	source := `<container><services>
  <service id="app.consumer" class="App\NeedsArguments">
    <argument type="service" id="app.wrong"/>
    <argument>product</argument>
  </service>
</services></container>`
	document := lsp.NewTextDocument(
		"file:///project/services.xml",
		source,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, invalidServiceArgumentTypeCode, result[0].ID)
	assert.Equal(t, "app.wrong", problemRangeText(document, result[0].Range))
	data := result[0].Payload.(map[string]any)
	assert.Contains(t, data["suggestions"], "app.logger")
}

func TestServiceArgumentTypeDiagnosticsAcceptSubtypeAndUnion(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.yaml",
		`services:
  app.consumer:
    class: App\NeedsArguments
    arguments:
      $logger: '@app.logger'
      $name: product
  app.union:
    class: App\UnionArguments
    arguments:
      $dependency: '@app.logger'
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestServiceArgumentTypeDiagnosticsUseUnsavedLocalServiceClass(
	t *testing.T,
) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.yaml",
		`services:
  app.local_wrong:
    class: App\WrongLogger
  app.consumer:
    class: App\NeedsArguments
    arguments:
      $logger: '@app.local_wrong'
      $name: product
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, invalidServiceArgumentTypeCode, result[0].ID)
}

func TestYAMLServiceCallArgumentTypeDiagnostics(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.yaml",
		`services:
  app.consumer:
    class: App\NeedsArguments
    factory: ['@factory', create]
    calls:
      - [setLogger, ['@app.wrong']]
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, invalidServiceArgumentTypeCode, result[0].ID)
	assert.Contains(t, result[0].Message, "LoggerInterface")
}

func TestXMLServiceCallArgumentTypeDiagnostics(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.xml",
		`<container><services>
  <service id="app.consumer" class="App\NeedsArguments">
    <factory service="factory"/>
    <call method="setLogger">
      <argument type="service" id="app.wrong"/>
    </call>
  </service>
</services></container>`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, invalidServiceArgumentTypeCode, result[0].ID)
	assert.Contains(t, result[0].Message, "LoggerInterface")
}

func TestYAMLConfiguredServiceMethodDiagnostics(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.yaml",
		`services:
  app.consumer:
    class: App\NeedsArguments
    factory: ['@factory', create]
    calls:
      - [setLogger, ['@app.logger']]
      - setLoggr: []
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	missing := problemsWithCode(result, missingConfiguredServiceMethodCode)
	require.Len(t, missing, 1)
	assert.Equal(t, "Missing Method", missing[0].Message)
	assert.Equal(t, "setLoggr", problemRangeText(document, missing[0].Range))
	assert.Contains(
		t,
		missing[0].Payload.(map[string]any)["suggestions"],
		"setLogger",
	)
}

func TestXMLConfiguredServiceMethodDiagnostics(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.xml",
		`<container><services>
  <service id="app.consumer" class="App\NeedsArguments">
    <factory service="factory"/>
    <call method="setLogger"/>
    <call method="setLoggr"/>
  </service>
</services></container>`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	missing := problemsWithCode(result, missingConfiguredServiceMethodCode)
	require.Len(t, missing, 1)
	assert.Equal(t, "Missing Method", missing[0].Message)
	assert.Equal(t, "setLoggr", problemRangeText(document, missing[0].Range))
}

func TestPHPConfiguredServiceMethodDiagnostics(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	for _, fixture := range []struct {
		name   string
		source string
	}{
		{
			name: "configurator",
			source: `<?php
use App\NeedsArguments;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set('app.consumer', NeedsArguments::class)
        ->call('setLogger', [])
        ->call('setLoggr', []);
};
`,
		},
		{
			name: "native array mapping",
			source: `<?php
use App\NeedsArguments;
use Symfony\Config\ServicesConfig;

return ServicesConfig::config([
    'services' => [
        NeedsArguments::class => [
            'calls' => [
                'setLogger' => [],
                'setLoggr' => [],
            ],
        ],
    ],
]);
`,
		},
		{
			name: "native array tuple",
			source: `<?php
return [
    'services' => [
        'App\\NeedsArguments' => [
            'calls' => [
                ['setLogger', []],
                ['setLoggr', [], true],
            ],
        ],
    ],
];
`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/services.php",
				fixture.source,
				1,
			)
			result, err := provider.Analyze(context.Background(), document)
			require.NoError(t, err)
			missing := problemsWithCode(
				result,
				missingConfiguredServiceMethodCode,
			)
			require.Len(t, missing, 1)
			assert.Equal(
				t,
				"setLoggr",
				problemRangeText(document, missing[0].Range),
			)
			assert.Contains(
				t,
				missing[0].Payload.(map[string]any)["suggestions"],
				"setLogger",
			)
		})
	}
}

func TestConfiguredServiceMethodDiagnosticsUseInheritedMethods(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.php",
		`<?php
return [
    'services' => [
        'App\\ChildArguments' => [
            'calls' => ['setLogger' => []],
        ],
    ],
];
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, problemsWithCode(result, missingConfiguredServiceMethodCode))
}

func TestConfiguredServiceMethodDiagnosticsSkipUnknownClasses(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.yaml",
		`services:
  app.unknown:
    class: App\Unknown
    calls:
      - missing: []
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, problemsWithCode(result, missingConfiguredServiceMethodCode))
}

func TestConfiguredFactoryMethodDiagnosticsUseFactoryReceiver(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	for _, fixture := range []struct {
		name   string
		uri    string
		source string
	}{
		{
			name: "YAML",
			uri:  "file:///project/services.yaml",
			source: `services:
  app.factory:
    class: App\Factory
  app.product:
    class: App\WrongLogger
    factory: ['@app.factory', creat]
`,
		},
		{
			name: "XML",
			uri:  "file:///project/services.xml",
			source: `<container><services>
  <service id="app.factory" class="App\Factory"/>
  <service id="app.product" class="App\WrongLogger">
    <factory service="app.factory" method="creat"/>
  </service>
</services></container>`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				fixture.uri,
				fixture.source,
				1,
			)
			result, err := provider.Analyze(
				context.Background(),
				document,
			)
			require.NoError(t, err)
			missing := problemsWithCode(
				result,
				missingConfiguredServiceMethodCode,
			)
			require.Len(t, missing, 1)
			assert.Equal(
				t,
				"creat",
				problemRangeText(document, missing[0].Range),
			)
			data := missing[0].Payload.(map[string]any)
			assert.Equal(t, `App\Factory`, data["className"])
			assert.Contains(t, data["suggestions"], "create")
		})
	}
}

func TestConfiguredServiceMethodDiagnosticIncludesCreateTarget(t *testing.T) {
	root := t.TempDir()
	classPath := filepath.Join(root, "Called.php")
	classSource := `<?php
namespace App;
class Called
{
    public function existing(): void {}
}
`
	require.NoError(t, os.WriteFile(
		classPath,
		[]byte(classSource),
		0o644,
	))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		classPath,
		[]byte(classSource),
	)))
	document := lsp.NewTextDocument(
		"file://"+filepath.Join(root, "services.yaml"),
		`services:
  app.called:
    class: App\Called
    calls:
      - [initialize, []]
`,
		1,
	)

	result, err := NewServiceArgumentAnalyzer(nil, phpIndex).
		Analyze(context.Background(), document)
	require.NoError(t, err)
	missing := problemsWithCode(
		result,
		missingConfiguredServiceMethodCode,
	)
	require.Len(t, missing, 1)
	data := missing[0].Payload.(map[string]any)
	assert.Equal(t, "initialize", data["methodName"])
	assert.NotContains(t, data, "classURI")
	assert.NotContains(t, data, "insertLine")
	assert.NotContains(t, data, "insertCharacter")
}

func TestPHPServiceArgumentTypeDiagnostics(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	source := `<?php
namespace Symfony\Component\DependencyInjection\Loader\Configurator;

$container->services()
    ->set('app.consumer', \App\NeedsArguments::class)
    ->args([service('app.wrong')]);
`
	document := lsp.NewTextDocument(
		"file:///project/services.php",
		source,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, invalidServiceArgumentTypeCode, result[0].ID)
	assert.Equal(
		t,
		"Expect instance of: App\\LoggerInterface",
		result[0].Message,
	)
	assert.Equal(t, "app.wrong", problemRangeText(document, result[0].Range))
	data := result[0].Payload.(map[string]any)
	assert.Contains(t, data["suggestions"], "app.logger")
}

func TestPHPLegacyArrayServiceArgumentTypeDiagnostics(t *testing.T) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/legacy_services.php",
		`<?php
namespace Symfony\Component\DependencyInjection\Loader\Configurator;

return [
    'services' => [
        'App\NeedsArguments' => [
            'arguments' => [
                '$logger' => '@app.wrong',
            ],
        ],
    ],
];
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, invalidServiceArgumentTypeCode, result[0].ID)
	data := result[0].Payload.(map[string]any)
	assert.Contains(t, data["suggestions"], "@app.logger")
}

func TestPHPServiceArgumentTypeDiagnosticsFormatsExpressionSuggestion(
	t *testing.T,
) {
	provider := serviceArgumentDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.php",
		`<?php
use App\NeedsArguments;
use App\WrongLogger;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $services = $container->services();
    $services->set(WrongLogger::class);
    $services->set(NeedsArguments::class)->arg(0, WrongLogger::class);
};
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 1)
	data := result[0].Payload.(map[string]any)
	assert.Contains(t, data["suggestions"], "service('app.logger')")
}

func serviceArgumentDiagnosticsFixture(
	t *testing.T,
) *ServiceArgumentAnalyzer {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/Services.php",
		[]byte(`<?php
namespace App;
interface LoggerInterface {}
interface OtherInterface {}
class Logger implements LoggerInterface {}
class WrongLogger {}
class NeedsArguments {
    public function __construct(
        LoggerInterface $logger,
        string $name,
        ?int $optional = null,
    ) {}
    public function setLogger(LoggerInterface $logger): void {}
}
class Factory {
    public function create(): WrongLogger { return new WrongLogger(); }
}
class ChildArguments extends NeedsArguments {}
class UnionArguments {
    public function __construct(
        LoggerInterface|OtherInterface $dependency,
    ) {}
}`),
	)))

	serviceIndex, err := symfony.NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		"/project/indexed.yaml",
		[]byte(`services:
  app.logger:
    class: App\Logger
  app.wrong:
    class: App\WrongLogger
`),
	)))
	services, err := serviceIndex.GetServicesByType("App\\LoggerInterface")
	require.NoError(t, err)
	require.Len(t, services, 1, fmt.Sprintf("%#v", services))
	assert.Equal(t, "app.logger", services[0].ID)
	return NewServiceArgumentAnalyzer(serviceIndex, phpIndex)
}
