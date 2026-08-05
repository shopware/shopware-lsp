package codeaction

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestSymfonyGeneratorCodeActionsExposeInteractiveGenerators(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })

	bundleInterface := `<?php
namespace Symfony\Component\HttpKernel\Bundle;
interface BundleInterface {}
`
	bundle := `<?php
namespace App;
class AppBundle implements \Symfony\Component\HttpKernel\Bundle\BundleInterface
{
    public function boot(): void {}
}
`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/vendor/BundleInterface.php",
		[]byte(bundleInterface),
	)))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/AppBundle.php",
		[]byte(bundle),
	)))

	provider := NewSymfonyGeneratorProvider(phpIndex, nil)
	request := symfonyGeneratorCodeActionRequest(
		"file:///project/src/AppBundle.php",
		bundle,
		"boot",
	)
	actions := provider.GetCodeActions(context.Background(), request)
	require.Len(t, actions, 2)
	assert.Equal(t, "Generate Symfony service", actions[0].Title)
	assert.Equal(t, generateSymfonyServiceAction, actions[0].Command.Command)
	assert.Equal(
		t,
		[]any{
			"file:///project/src/AppBundle.php",
			"App\\AppBundle",
		},
		actions[0].Command.Arguments,
	)
	assert.Equal(t, "Symfony: Create CompilerPass", actions[1].Title)
	assert.Equal(t, createCompilerPassAction, actions[1].Command.Command)

	classScope := symfonyGeneratorCodeActionRequest(
		"file:///project/src/AppBundle.php",
		bundle,
		"AppBundle",
	)
	classActions := provider.GetCodeActions(context.Background(), classScope)
	require.Len(t, classActions, 1)
	assert.Equal(t, "Symfony: Create CompilerPass", classActions[0].Title)
}

func TestSymfonyServiceGeneratorInfersArgumentsCallsAndTags(t *testing.T) {
	phpIndex, serviceIndex := newSymfonyGeneratorFixture(t)
	provider := NewSymfonyGeneratorProvider(phpIndex, serviceIndex)

	raw := mustGeneratorJSON(t, symfonyServiceGenerationRequest{
		ClassName: "App\\Consumer",
		Output:    "yaml",
		ClassAsID: true,
	})
	value, err := provider.generateService(context.Background(), &raw)
	require.NoError(t, err)
	result := value.(symfonyServiceGenerationResponse)

	assert.Equal(t, "yaml", result.Language)
	assert.Contains(t, result.Content, "App\\Consumer:")
	assert.Contains(
		t,
		result.Content,
		"arguments: ['@app.logger', '@?']",
	)
	assert.Contains(
		t,
		result.Content,
		"- [setLogger, ['@app.logger']]",
	)
	assert.Contains(
		t,
		result.Content,
		"- { name: kernel.event_subscriber }",
	)

	for _, output := range []string{"xml", "fluent", "php-array"} {
		raw = mustGeneratorJSON(t, symfonyServiceGenerationRequest{
			ClassName: "App\\Consumer",
			Output:    output,
			ClassAsID: false,
			ServiceID: "app.consumer",
		})
		value, err = provider.generateService(context.Background(), &raw)
		require.NoError(t, err)
		generated := value.(symfonyServiceGenerationResponse)
		assert.NotEmpty(t, generated.Content)
		switch output {
		case "xml":
			assert.Contains(
				t,
				generated.Content,
				`<service id="app.consumer" class="App\Consumer">`,
			)
			assert.Contains(
				t,
				generated.Content,
				`<argument type="service" id="app.logger"/>`,
			)
		case "fluent":
			assert.Contains(
				t,
				generated.Content,
				`$services->set('app.consumer', \App\Consumer::class)`,
			)
			assert.Contains(
				t,
				generated.Content,
				`->tag('kernel.event_subscriber');`,
			)
		case "php-array":
			assert.Contains(
				t,
				generated.Content,
				`'app.consumer' => [`,
			)
			assert.Contains(
				t,
				generated.Content,
				`'class' => \App\Consumer::class`,
			)
		}
	}
}

func TestSymfonyServiceGeneratorUsesUnsavedPHPOverlay(t *testing.T) {
	phpIndex, serviceIndex := newSymfonyGeneratorFixture(t)
	provider := NewSymfonyGeneratorProvider(phpIndex, serviceIndex)
	source := `<?php
namespace App;
class UnsavedConsumer
{
    public function __construct(LoggerInterface $logger) {}
}
`
	raw := mustGeneratorJSON(t, symfonyServiceGenerationRequest{
		ClassName: "App\\UnsavedConsumer",
		Output:    "yaml",
		ClassAsID: true,
		FileURI:   "file:///project/src/UnsavedConsumer.php",
		Source:    source,
		Version:   7,
	})
	value, err := provider.generateService(context.Background(), &raw)
	require.NoError(t, err)
	result := value.(symfonyServiceGenerationResponse)
	assert.Contains(t, result.Content, "App\\UnsavedConsumer:")
	assert.Contains(t, result.Content, "arguments: ['@app.logger']")
}

func TestSymfonyServiceDefinitionCollectorGeneratesMultipleClassesAndChoices(
	t *testing.T,
) {
	phpIndex, serviceIndex := newSymfonyGeneratorFixture(t)
	provider := NewSymfonyGeneratorProvider(phpIndex, serviceIndex)

	result, err := provider.CollectServiceDefinitions(
		context.Background(),
		SymfonyServiceDefinitionCollectionRequest{
			ClassNames: "App\\Consumer, App\\Logger, App\\Missing",
			Output:     "yaml",
			ClassAsID:  true,
		},
	)
	require.NoError(t, err)
	require.Len(t, result.Definitions, 3)

	consumer := result.Definitions[0]
	assert.Equal(t, "App\\Consumer", consumer.ClassName)
	assert.Equal(t, "yaml", consumer.Language)
	assert.Empty(t, consumer.Error)
	require.Len(t, consumer.Suggestions, 2)
	assert.Equal(t, "__construct", consumer.Suggestions[0].Method)
	assert.Equal(t, "logger", consumer.Suggestions[0].Parameter)
	assert.Equal(t, "App\\LoggerInterface", consumer.Suggestions[0].Type)
	assert.Equal(
		t,
		[]string{"app.logger", "app.secondary_logger"},
		consumer.Suggestions[0].Services,
	)
	assert.Contains(t, consumer.Content, "App\\Consumer:")
	assert.Contains(t, consumer.Content, "arguments: ['@app.logger', '@?']")
	assert.Contains(t, consumer.Content, "# Possible services per parameter:")
	assert.Contains(
		t,
		consumer.Content,
		"$logger [App\\LoggerInterface] => app.logger, app.secondary_logger",
	)
	assert.Contains(
		t,
		consumer.Content,
		"$logger (setLogger) [App\\LoggerInterface]",
	)

	logger := result.Definitions[1]
	assert.Equal(t, "App\\Logger", logger.ClassName)
	assert.Equal(t, "App\\Logger: ~", logger.Content)
	assert.Empty(t, logger.Suggestions)

	missing := result.Definitions[2]
	assert.Equal(t, "App\\Missing", missing.ClassName)
	assert.Contains(t, missing.Error, "was not found")
	assert.Empty(t, missing.Content)
}

func TestCompilerPassGeneratorCreatesFileAndRegistersBundle(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })

	bundleInterface := `<?php
namespace Symfony\Component\HttpKernel\Bundle;
interface BundleInterface {}
`
	bundle := `<?php

namespace Acme\Demo;

use Symfony\Component\HttpKernel\Bundle\Bundle;

class DemoBundle extends Bundle
{
}
`
	bundleBase := `<?php
namespace Symfony\Component\HttpKernel\Bundle;
class Bundle implements BundleInterface {}
`
	for path, source := range map[string]string{
		"/vendor/BundleInterface.php": bundleInterface,
		"/vendor/Bundle.php":          bundleBase,
		"/project/DemoBundle.php":     bundle,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}

	root := t.TempDir()
	bundlePath := filepath.Join(root, "DemoBundle.php")
	provider := NewSymfonyGeneratorProvider(phpIndex, nil)
	raw := mustGeneratorJSON(t, compilerPassCreationRequest{
		BundleURI:   uriutil.FileURI(bundlePath),
		BundleClass: "Acme\\Demo\\DemoBundle",
		ClassName:   "CollectWidgetsPass",
		Source:      bundle,
		Version:     4,
	})
	value, err := provider.createCompilerPass(context.Background(), &raw)
	require.NoError(t, err)
	result := value.(compilerPassCreationResponse)

	assert.Equal(
		t,
		uriutil.FileURI(filepath.Join(
			root,
			"DependencyInjection",
			"Compiler",
			"CollectWidgetsPass.php",
		)),
		result.FileURI,
	)
	assert.Contains(
		t,
		result.FileContent,
		"namespace Acme\\Demo\\DependencyInjection\\Compiler;",
	)
	assert.Contains(
		t,
		result.FileContent,
		"class CollectWidgetsPass implements CompilerPassInterface",
	)
	assert.Contains(
		t,
		result.BundleContent,
		"use Acme\\Demo\\DependencyInjection\\Compiler\\CollectWidgetsPass;",
	)
	assert.Contains(
		t,
		result.BundleContent,
		"use Symfony\\Component\\DependencyInjection\\ContainerBuilder;",
	)
	assert.Contains(
		t,
		result.BundleContent,
		"public function build(ContainerBuilder $container): void",
	)
	assert.Contains(
		t,
		result.BundleContent,
		"parent::build($container);",
	)
	assert.Contains(
		t,
		result.BundleContent,
		"$container->addCompilerPass(new CollectWidgetsPass());",
	)
	assert.Empty(t, phpparser.Parse(result.BundleContent).Errors)
	assert.Empty(t, phpparser.Parse(result.FileContent).Errors)
}

func TestCompilerPassGeneratorUsesExistingBuildParameter(t *testing.T) {
	source := `<?php

namespace App;

use Symfony\Component\DependencyInjection\ContainerBuilder;

class AppBundle
{
    public function build(ContainerBuilder $builder): void
    {
        parent::build($builder);
    }
}
`
	updated, err := addCompilerPassToBundle(
		"file:///project/src/AppBundle.php",
		source,
		1,
		"App\\AppBundle",
		"App\\DependencyInjection\\Compiler\\CollectPass",
		"CollectPass",
	)
	require.NoError(t, err)
	assert.Contains(
		t,
		updated,
		"use App\\DependencyInjection\\Compiler\\CollectPass;",
	)
	assert.Contains(
		t,
		updated,
		"$builder->addCompilerPass(new CollectPass());",
	)
	assert.Equal(
		t,
		1,
		strings.Count(
			updated,
			"use Symfony\\Component\\DependencyInjection\\ContainerBuilder;",
		),
	)
	assert.Empty(t, phpparser.Parse(updated).Errors)
}

func newSymfonyGeneratorFixture(
	t *testing.T,
) (*php.PHPIndex, *symfony.ServiceIndex) {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		"/vendor/EventSubscriberInterface.php": `<?php
namespace Symfony\Component\EventDispatcher;
interface EventSubscriberInterface {}
`,
		"/project/src/LoggerInterface.php": `<?php
namespace App;
interface LoggerInterface {}
`,
		"/project/src/Logger.php": `<?php
namespace App;
class Logger implements LoggerInterface {}
`,
		"/project/src/Consumer.php": `<?php
namespace App;
class Consumer implements \Symfony\Component\EventDispatcher\EventSubscriberInterface
{
    public function __construct(LoggerInterface $logger, string $channel) {}
    public function setLogger(LoggerInterface $logger): void {}
}
`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	root := t.TempDir()
	serviceIndex, err := symfony.NewServiceIndex(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	serviceIndex.SetPHPIndex(phpIndex)
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		filepath.Join(root, "services.yaml"),
		[]byte(`services:
  app.logger:
    class: App\Logger
  app.secondary_logger:
    class: App\Logger
`),
	)))
	return phpIndex, serviceIndex
}

func symfonyGeneratorCodeActionRequest(
	uri,
	source,
	needle string,
) *lsp.CodeActionRequest {
	document := lsp.NewTextDocument(uri, source, 1)
	offset := strings.Index(source, needle)
	if offset < 0 {
		offset = 0
	}
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CodeActionParams{}
	params.TextDocument.URI = uri
	params.Range = protocol.Range{
		Start: protocol.Position{
			Line:      int(line),
			Character: int(character),
		},
		End: protocol.Position{
			Line:      int(line),
			Character: int(character),
		},
	}
	return &lsp.CodeActionRequest{
		CodeActionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				uint32(offset),
			),
		},
	}
}

func mustGeneratorJSON(t *testing.T, value interface{}) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return raw
}
