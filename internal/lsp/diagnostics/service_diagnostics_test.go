package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceDiagnosticsForYAMLAndXML(t *testing.T) {
	provider := serviceDiagnosticsFixture(t)
	tests := []struct {
		name   string
		uri    string
		source string
	}{
		{
			name: "YAML",
			uri:  "file:///project/services.yaml",
			source: `parameters:
  local.parameter: value
services:
  App\Local:
    class: App\Existing
    arguments:
      - '@app.existing'
      - '%app.existing%'
      - '@app.missing'
      - '%app.missing%'
      - '@?app.optional'
  app.class_check:
    class: App\Missing
`,
		},
		{
			name: "XML",
			uri:  "file:///project/services.xml",
			source: `<container>
<parameters><parameter key="local.parameter">value</parameter></parameters>
<services>
  <service id="local.service" class="App\Existing">
    <argument type="service" id="app.existing"/>
    <argument>%app.existing%</argument>
    <argument type="service" id="app.missing"/>
    <argument>%app.missing%</argument>
    <argument type="service" id="app.optional" on-invalid="ignore"/>
  </service>
  <alias id="local.alias" service="local.service"/>
  <service id="class.check" class="App\Missing"/>
</services>
</container>`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(test.uri, test.source, 1)
			result, err := provider.Analyze(context.Background(), document)
			require.NoError(t, err)
			assertDiagnosticCodes(t, result,
				missingServiceCode,
				missingParameterCode,
				missingClassCode,
			)
		})
	}
}

func TestServiceDiagnosticsForPHPAttributesAndTypedContainer(t *testing.T) {
	provider := serviceDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/Example.php",
		`<?php
namespace App;
use Psr\Container\ContainerInterface;

class Other { public function get(string $id): mixed {} }

class Example {
    #[\Symfony\Component\DependencyInjection\Attribute\Autowire(service: 'app.missing')]
    #[\Symfony\Component\DependencyInjection\Attribute\Autowire(param: 'app.missing')]
    #[\Symfony\Component\DependencyInjection\Attribute\AutowireServiceClosure('app.closure_missing')]
    #[\Symfony\Component\DependencyInjection\Attribute\AutowireMethodOf(service: 'app.method_missing')]
    #[\Symfony\Component\DependencyInjection\Attribute\AutowireCallable(service: 'app.callable_missing')]
    #[\Symfony\Component\DependencyInjection\Attribute\AutowireLocator(
        services: ['app.locator_missing', 'app.existing'],
        exclude: 'app.exclude_missing',
    )]
    public function run(ContainerInterface $container, Other $other): void {
        $container->get('app.missing');
        $container->get('app.existing');
        $other->get('ignored.missing');
    }
}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	assertDiagnosticCodes(t, result,
		missingServiceCode,
		missingServiceCode,
		missingServiceCode,
		missingServiceCode,
		missingServiceCode,
		missingServiceCode,
		missingServiceCode,
		missingParameterCode,
	)
	for _, diagnostic := range result {
		assert.NotContains(t, diagnostic.Message, "ignored.missing")
	}
}

func TestServiceDiagnosticsDistinguishParameterBagsFromServiceContainers(
	t *testing.T,
) {
	provider := serviceDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/Example.php",
		`<?php /* ContainerConfigurator */
use Psr\Container\ContainerInterface;
use Symfony\Component\DependencyInjection\ContainerInterface as DIContainer;
use Symfony\Component\DependencyInjection\ParameterBag\ContainerBagInterface;
use Symfony\Component\DependencyInjection\ParameterBag\ParameterBagInterface;

function resolve(
    ParameterBagInterface $parameters,
    ContainerBagInterface $containerBag,
    ContainerInterface $services,
    DIContainer $diContainer,
): void {
    $parameters->get('app.service');
    $containerBag->has('app.service');
    $services->get('app.parameter');
    $diContainer->getParameter('app.service');
    $diContainer->hasParameter('app.parameter');
}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	assertDiagnosticCodes(t, result,
		missingParameterCode,
		missingParameterCode,
		missingServiceCode,
		missingParameterCode,
	)
}

func TestServiceDiagnosticsForPHPArrayReferences(t *testing.T) {
	provider := serviceDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/config/services.php",
		`<?php
return [
    'services' => [
        'app.consumer' => [
            'class' => 'App\\MissingArrayClass',
            'parent' => 'app.missing_parent',
            'arguments' => [
                '%app.missing_parameter%',
                '@app.missing_service',
            ],
            'calls' => [['configure', ['%app.existing%']]],
        ],
        'app.alias' => '@app.missing_alias',
    ],
];
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	assertDiagnosticCodes(
		t,
		result,
		missingServiceCode,
		missingParameterCode,
		missingServiceCode,
		missingClassCode,
		missingServiceCode,
	)
}

func TestServiceDiagnosticsIncludeTypoSuggestions(t *testing.T) {
	provider := serviceDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/services.yaml",
		`services:
  app.consumer:
    arguments:
      - '@app.existng'
      - '%app.existng%'
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)
	for _, diagnostic := range result {
		assert.Contains(t, problemSuggestionStrings(diagnostic), "app.existing")
		assert.Equal(
			t,
			"app.existng",
			problemRangeText(document, diagnostic.Range),
		)
	}
}

func TestServiceDiagnosticsValidatePHPDocAssistantReferences(t *testing.T) {
	provider := serviceDiagnosticsFixture(t)
	document := lsp.NewTextDocument(
		"file:///project/AssistantUsage.php",
		`<?php
namespace App;
resolve_service('app.existng');
resolve_parameter('app.parametr');
resolve_class('App\Exsting');
resolve_interface('App\Existing');
resolve_class('App\Existing');
resolve_interface('App\ExistingInterface');
resolve_class_or_interface('App\ExistingInterface');
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 4)

	byText := make(map[string]lsp.Problem, len(result))
	for _, diagnostic := range result {
		byText[problemRangeText(document, diagnostic.Range)] = diagnostic
	}
	assert.Equal(t, missingServiceCode, byText["app.existng"].ID)
	assert.Contains(
		t,
		problemSuggestionStrings(byText["app.existng"]),
		"app.existing",
	)
	assert.Equal(t, missingParameterCode, byText["app.parametr"].ID)
	assert.Contains(
		t,
		problemSuggestionStrings(byText["app.parametr"]),
		"app.parameter",
	)
	assert.Equal(t, missingClassCode, byText["App\\Exsting"].ID)
	assert.Contains(
		t,
		problemSuggestionStrings(byText["App\\Exsting"]),
		"App\\Existing",
	)
	assert.Equal(t, missingClassCode, byText["App\\Existing"].ID)
	assert.Contains(t, byText["App\\Existing"].Message, "Interface")
	assert.Contains(
		t,
		problemSuggestionStrings(byText["App\\Existing"]),
		"App\\ExistingInterface",
	)
}

func TestServiceDiagnosticsMarkDeprecatedServicesAndClasses(t *testing.T) {
	provider := serviceDiagnosticsFixture(t)
	tests := []struct {
		name   string
		uri    string
		source string
		count  int
	}{
		{
			name: "YAML",
			uri:  "file:///project/services.yaml",
			source: `services:
  app.consumer:
    class: App\Legacy
    arguments:
      - '@app.deprecated'
      - '@?app.deprecated'
      - '@?app.runtime_only'
`,
			count: 5,
		},
		{
			name: "XML",
			uri:  "file:///project/services.xml",
			source: `<container><services>
<service id="app.consumer" class="App\Legacy">
  <argument type="service" id="app.deprecated"/>
  <argument type="service" id="app.deprecated" on-invalid="ignore"/>
  <argument type="service" id="app.runtime_only" on-invalid="ignore"/>
</service>
</services></container>`,
			count: 5,
		},
		{
			name: "PHP",
			uri:  "file:///project/Consumer.php",
			source: `<?php
use Psr\Container\ContainerInterface;
use Symfony\Component\DependencyInjection\Attribute\Autowire;
class Consumer {
    #[Autowire(service: 'app.deprecated')]
    public function __construct(private ContainerInterface $container) {}
    public function resolve(): mixed {
        return $this->container->get('app.deprecated');
    }
}`,
			count: 4,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(test.uri, test.source, 1)
			result, err := provider.Analyze(
				context.Background(),
				document,
			)
			require.NoError(t, err)
			require.Len(t, result, test.count)
			for _, diagnostic := range result {
				assert.Contains(t, []lsp.DiagnosticID{
					deprecatedServiceCode,
					deprecatedClassCode,
				}, diagnostic.ID)
				assert.Equal(
					t,
					protocol.DiagnosticSeverityHint,
					diagnostic.Severity,
				)
				assert.Equal(
					t,
					[]protocol.DiagnosticTag{
						protocol.DiagnosticTagDeprecated,
					},
					diagnostic.Tags,
				)
				assert.NotContains(t, diagnostic.Message, "%service_id%")
			}
		})
	}
}

func serviceDiagnosticsFixture(t *testing.T) *ServiceAnalyzer {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/Existing.php",
		[]byte(`<?php
namespace App;
class Existing {}
interface ExistingInterface {}
/** @deprecated Use Modern instead. */
class Legacy {}

/** @param string $service #Service */
function resolve_service(string $service): void {}
/** @param string $parameter #Parameter */
function resolve_parameter(string $parameter): void {}
/** @param string $class #Class */
function resolve_class(string $class): void {}
/** @param string $interface #Interface */
function resolve_interface(string $interface): void {}
/** @param string $type #ClassInterface */
function resolve_class_or_interface(string $type): void {}
`),
	)))

	serviceIndex, err := symfony.NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		"/project/indexed_services.yaml",
		[]byte(`parameters:
  app.existing: value
  app.parameter: value
services:
  app.existing:
    class: App\Existing
  app.service:
    class: App\Existing
  app.deprecated:
    class: App\Legacy
    deprecated: 'The %service_id% service is deprecated; use app.existing.'
`),
	)))
	return NewServiceAnalyzer(serviceIndex, phpIndex)
}

func assertDiagnosticCodes(
	t *testing.T,
	diagnostics []lsp.Problem,
	expected ...lsp.DiagnosticID,
) {
	t.Helper()
	var actual []lsp.DiagnosticID
	for _, diagnostic := range diagnostics {
		actual = append(actual, diagnostic.ID)
	}
	assert.ElementsMatch(t, expected, actual)
}

func problemSuggestionStrings(problem lsp.Problem) []string {
	payload, _ := problem.Payload.(map[string]any)
	values, _ := payload["suggestions"].([]string)
	return values
}
