package php

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/stretchr/testify/require"
)

func TestPHPIndexPublishesHierarchyAndMembers(t *testing.T) {
	idx, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	source := []byte(`<?php
namespace App;
interface Marker {}
interface ChildMarker extends Marker {}
abstract class Base implements ChildMarker {
    protected string $name;
}
final class Concrete extends Base {
    public function name(): string { return $this->name; }
}`)
	require.NoError(t, idx.Index(indexer.NewParsedFile("/project/types.php", source)))

	class, found := idx.FindClass("App\\Concrete")
	require.True(t, found)
	require.Equal(t, semantic.ClassSymbol, class.Kind)
	require.True(t, idx.SemanticSnapshot().IsSubtypeOf("App\\Concrete", "App\\Marker"))
	require.Contains(t, idx.ClassNames(), "App\\Concrete")
	require.NotEmpty(t, idx.ClassSymbolsIn("/project/types.php"))
}

func TestPHPIndexPersistsPhpStormMetaCallContracts(t *testing.T) {
	configDir := t.TempDir()
	metaPath := filepath.Join(t.TempDir(), ".phpstorm.meta.php")
	metaSource := []byte(`<?php
namespace PHPSTORM_META;
override(\identity(0), type(0));
`)

	idx, err := NewPHPIndex(configDir)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(metaPath, metaSource)))

	assertContract := func(index *PHPIndex) {
		t.Helper()
		parsed := phpparser.Parse(`<?php
class Product {}
$product = identity(new Product());
`)
		document := index.AnalyzeDocument("/consumer.php", 1, parsed.Tree.Root)
		for _, call := range phpquery.Calls(parsed.Tree.Root) {
			if phpquery.CallMethodName(call) != "identity" {
				continue
			}
			require.Equal(t, "Product", document.TypeOf(call).Type.String())
			return
		}
		t.Fatal("identity call not found")
	}
	assertContract(idx)
	require.NoError(t, idx.Close())

	reopened, err := NewPHPIndex(configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	assertContract(reopened)
}

func TestPHPIndexReopensComposerAutoloadDevRoots(t *testing.T) {
	projectRoot := t.TempDir()
	testsRoot := filepath.Join(projectRoot, "tests", "unit")
	require.NoError(t, os.MkdirAll(testsRoot, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "composer.json"),
		[]byte(`{"autoload-dev":{"psr-4":{"App\\Tests\\":"tests/unit/"}}}`),
		0o600,
	))

	idx, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.ConfigureProject(projectRoot))

	require.True(t, idx.ShouldEnterDirectory(filepath.Join(projectRoot, "tests")))
	require.True(t, idx.ShouldEnterDirectory(testsRoot))
	require.False(t, idx.ShouldEnterDirectory(filepath.Join(projectRoot, "tests", "fixtures")))
	require.True(t, idx.ShouldIndexPath(filepath.Join(testsRoot, "ExampleTest.php")))
	require.False(t, idx.ShouldIndexPath(filepath.Join(testsRoot, "fixture.json")))
	require.False(t, idx.ShouldIndexPath(filepath.Join(projectRoot, "tests", "fixtures", "Example.php")))
}

func TestPHPIndexSelectsRuntimeStubsFromComposerAndOverrides(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "composer.json"),
		[]byte(`{
  "require": {"php": "^8.3", "ext-curl": "*"},
  "config": {"platform": {"ext-imagick": false}}
}`),
		0o600,
	))
	idx, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.ConfigureProjectWithExtensions(
		projectRoot,
		[]string{"redis"},
		nil,
	))

	snapshot := idx.SemanticSnapshot()
	require.Len(t, snapshot.Functions("strlen"), 1)
	require.Len(t, snapshot.Functions("curl_init"), 1)
	require.Len(t, snapshot.Classes("Redis"), 1)
	require.Empty(t, snapshot.Classes("Imagick"))
	require.Empty(t, snapshot.Classes("SoapClient"))
}

func TestCompareFoldMatchesCaseInsensitiveNameOrder(t *testing.T) {
	t.Parallel()
	require.Negative(t, compareFold("App\\Alpha", "app\\Beta"))
	require.Zero(t, compareFold("APP\\Service", "app\\service"))
	require.Positive(t, compareFold("App\\Zulu", "app\\Beta"))
	require.Negative(t, compareFold("Äpfel", "Öl"))
}

func TestPHPIndexRebuildsSemanticDocumentsOnDemand(t *testing.T) {
	configDir := t.TempDir()
	path := filepath.Join(t.TempDir(), "Product.php")
	source := []byte(`<?php
namespace App;
/** @final */
class Product {
    #[\Deprecated]
    public string $legacy;

    /** @deprecated */
    public function name(): string { return 'product'; }
}`)
	require.NoError(t, os.WriteFile(path, source, 0o600))

	idx, err := NewPHPIndex(configDir)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, source)))
	document, ok := idx.SemanticDocument(path)
	require.True(t, ok)
	require.NotEmpty(t, document.Symbols)
	require.NotEmpty(t, document.Scopes)
	require.Positive(t, document.TypeFactCount())
	require.True(
		t,
		idx.FindMethods("App\\Product", "name")[0].
			Flags.Has(semantic.DeprecatedFlag),
	)
	require.True(
		t,
		idx.FindProperties("App\\Product", "legacy")[0].
			Flags.Has(semantic.DeprecatedFlag),
	)
	require.True(
		t,
		idx.SemanticSnapshot().Classes("App\\Product")[0].
			Flags.Has(semantic.SoftFinalFlag),
	)
	require.NoError(t, idx.Close())

	reopened, err := NewPHPIndex(configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	document, ok = reopened.SemanticDocument(path)
	require.True(t, ok)
	require.NotEmpty(t, document.Symbols)
	require.NotEmpty(t, document.Scopes)
	require.Positive(t, document.TypeFactCount())
	require.Len(t, reopened.SemanticSnapshot().Classes("App\\Product"), 1)
	require.True(
		t,
		reopened.FindMethods("App\\Product", "name")[0].
			Flags.Has(semantic.DeprecatedFlag),
	)
	require.True(
		t,
		reopened.FindProperties("App\\Product", "legacy")[0].
			Flags.Has(semantic.DeprecatedFlag),
	)
	require.True(
		t,
		reopened.SemanticSnapshot().Classes("App\\Product")[0].
			Flags.Has(semantic.SoftFinalFlag),
	)
}

func TestPHPIndexPersistsCompactWorkspaceGraph(t *testing.T) {
	configDir := t.TempDir()
	path := filepath.Join(t.TempDir(), "Product.php")
	source := []byte(`<?php
namespace App;
class Product {
    public function label(string $prefix): string {
        $suffix = 'product';
        return $prefix . $suffix;
    }
}`)
	require.NoError(t, os.WriteFile(path, source, 0o600))

	idx, err := NewPHPIndex(configDir)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, source)))

	graphValues, err := idx.workspaceGraphs.GetValuesByPath(path)
	require.NoError(t, err)
	require.Len(t, graphValues, 1)
	persistedGraph, err := graphValues[0].decode()
	require.NoError(t, err)
	graph := persistedGraph.Document()
	require.Empty(t, graph.Scopes)
	require.Zero(t, graph.TypeFactCount())
	require.Empty(t, graph.TypeAliases)
	require.Empty(t, graph.Issues)
	for _, symbol := range graph.Symbols {
		require.NotEqual(t, semantic.ParameterSymbol, symbol.Kind)
		require.NotEqual(t, semantic.LocalSymbol, symbol.Kind)
		require.NotEqual(t, semantic.ClosureSymbol, symbol.Kind)
	}
	for _, reference := range graph.References {
		require.NotEqual(t, semantic.VariableName, reference.Kind)
	}

	require.NoError(t, idx.Close())
	reopened, err := NewPHPIndex(configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	require.Len(t, reopened.FindMethods("App\\Product", "label"), 1)
	restored, ok := reopened.SemanticDocument(path)
	require.True(t, ok)
	require.NotEmpty(t, restored.Scopes)
	require.Positive(t, restored.TypeFactCount())
	hasLocal := false
	for _, symbol := range restored.Symbols {
		if symbol.Kind == semantic.LocalSymbol && symbol.Name == "$suffix" {
			hasLocal = true
			break
		}
	}
	require.True(t, hasLocal)
}

func TestPHPIndexPersistsConstantArrayMetadata(t *testing.T) {
	configDir := t.TempDir()
	path := "/project/HttpClientInterface.php"
	source := []byte(`<?php
namespace Symfony\Contracts\HttpClient;
interface HttpClientInterface {
    public const OPTIONS_DEFAULTS = [
        'timeout' => null,
        'headers' => [],
    ];
}`)
	idx, err := NewPHPIndex(configDir)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, source)))
	constants := idx.FindConstants(
		"Symfony\\Contracts\\HttpClient\\HttpClientInterface",
		"OPTIONS_DEFAULTS",
	)
	require.Len(t, constants, 1)
	require.Len(t, constants[0].ConstantArray(), 2)
	require.Equal(t, "timeout", constants[0].ConstantArray()[0].Key)
	require.NoError(t, idx.Close())

	reopened, err := NewPHPIndex(configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	constants = reopened.FindConstants(
		"Symfony\\Contracts\\HttpClient\\HttpClientInterface",
		"OPTIONS_DEFAULTS",
	)
	require.Len(t, constants, 1)
	require.Len(t, constants[0].ConstantArray(), 2)
	require.Equal(t, "array", constants[0].ConstantArray()[1].Type.String())
	require.Equal(t, "[]", constants[0].ConstantArray()[1].Value)
}

func TestPHPIndexPersistsLiteralMethodReturns(t *testing.T) {
	configDir := t.TempDir()
	source := []byte(`<?php
namespace App;
class Helper {
    public function getName(): string {
        return 'question';
    }
}`)
	idx, err := NewPHPIndex(configDir)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/Helper.php",
		source,
	)))
	methods := idx.FindMethods("App\\Helper", "getName")
	require.Len(t, methods, 1)
	require.Len(t, methods[0].LiteralReturns(), 1)
	require.Equal(t, "question", methods[0].LiteralReturns()[0].Value)
	require.NoError(t, idx.Close())

	reopened, err := NewPHPIndex(configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	methods = reopened.FindMethods("App\\Helper", "getName")
	require.Len(t, methods, 1)
	require.Len(t, methods[0].LiteralReturns(), 1)
	require.Equal(t, `"question"`, methods[0].LiteralReturns()[0].Type.String())
}

func TestPHPIndexPersistsClassConstantMethodReturns(t *testing.T) {
	configDir := t.TempDir()
	source := []byte(`<?php
namespace App;
class Helper {
    private const NAME = 'question';
    public function getName(): string {
        return self::NAME;
    }
}`)
	idx, err := NewPHPIndex(configDir)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/Helper.php",
		source,
	)))
	methods := idx.FindMethods("App\\Helper", "getName")
	require.Len(t, methods, 1)
	require.Len(t, methods[0].ConstantReturns(), 1)
	require.Equal(t, "self", methods[0].ConstantReturns()[0].Receiver)
	require.Equal(t, "NAME", methods[0].ConstantReturns()[0].Name)
	require.NoError(t, idx.Close())

	reopened, err := NewPHPIndex(configDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	methods = reopened.FindMethods("App\\Helper", "getName")
	require.Len(t, methods, 1)
	require.Len(t, methods[0].ConstantReturns(), 1)
	require.Equal(t, "self", methods[0].ConstantReturns()[0].Receiver)
	require.Equal(t, "NAME", methods[0].ConstantReturns()[0].Name)
}

func TestSemanticContextResolvesCallReceiver(t *testing.T) {
	idx, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	source := `<?php
namespace App;
use Shopware\Core\System\SystemConfig\SystemConfigService;
class Demo {
    public function __construct(private SystemConfigService $config) {}
    public function run(): void { $this->config->get('key'); }
}`
	root := phpparser.Parse(source).Tree.Root
	calls := phpquery.Calls(root, "$this->config->get")
	require.Len(t, calls, 1)
	ctx := idx.AddDocumentContext(
		context.Background(),
		"/project/Demo.php",
		1,
		calls[0],
		root,
	)
	require.True(t, idx.IsMethodCalledOnClass(
		ctx,
		calls[0],
		[]byte(source),
		"Shopware\\Core\\System\\SystemConfig\\SystemConfigService",
	))
}

func TestPHPIndexResolvesReferencesWhenTargetArrivesLater(t *testing.T) {
	idx, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	consumer := []byte(`<?php
namespace App;
class Consumer {
    public function run(): void {
        (new Service())->execute();
    }
}`)
	require.NoError(t, idx.Index(indexer.NewParsedFile("/project/Consumer.php", consumer)))
	service := []byte(`<?php
namespace App;
class Service {
    public function execute(): void {}
}`)
	require.NoError(t, idx.Index(indexer.NewParsedFile("/project/Service.php", service)))

	class, found := idx.FindClass("App\\Service")
	require.True(t, found)
	require.NotEmpty(t, idx.SemanticSnapshot().ReferencesTo(class.ID))
	methods := idx.SemanticSnapshot().Members(class.ID, "execute")
	require.Len(t, methods, 1)
	require.NotEmpty(t, idx.SemanticSnapshot().ReferencesTo(methods[0].ID))
}

func TestPHPIndexPublishesScannerBatchAsOneGeneration(t *testing.T) {
	idx, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	idx.BeginIndexingBatch([]string{
		"/project/Consumer.php",
		"/project/Service.php",
	})
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/Consumer.php",
		[]byte(`<?php
namespace App;
class Consumer {
    public function run(Service $service): void { $service->execute(); }
}`),
	)))
	require.Empty(t, idx.SemanticSnapshot().Classes("App\\Consumer"))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/Service.php",
		[]byte(`<?php
namespace App;
class Service {
    public function execute(): void {}
}`),
	)))
	require.NoError(t, idx.EndIndexingBatch())

	snapshot := idx.SemanticSnapshot()
	require.Equal(t, uint64(1), snapshot.Revision)
	require.Len(t, snapshot.Classes("App\\Consumer"), 1)
	services := snapshot.Classes("App\\Service")
	require.Len(t, services, 1)
	require.NotEmpty(t, snapshot.ReferencesTo(services[0].ID))
}
