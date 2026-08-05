package twig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTwigExtension(t *testing.T) {
	// Read test file
	filePath := filepath.Join("testdata", "extension.php")
	content, err := os.ReadFile(filePath)
	require.NoError(t, err)

	rootNode := phpparser.ParseBytes(content).Tree.Root

	// Parse Twig extension
	functions, filters, err := ParseTwigExtension(filePath, rootNode, content)
	require.NoError(t, err)

	// Verify functions
	require.Len(t, functions, 2)
	assert.Equal(t, "test", functions[0].Name)
	assert.Equal(t, filePath, functions[0].FilePath)

	assert.Equal(t, "test2", functions[1].Name)
	assert.Equal(t, filePath, functions[1].FilePath)

	// Verify function parameters
	require.Len(t, functions[0].Parameters, 1)
	assert.Equal(t, "$test", functions[0].Parameters[0].Name)
	assert.Equal(t, "string", functions[0].Parameters[0].Type)
	assert.False(t, functions[0].Parameters[0].Optional)

	// Verify filters
	require.Len(t, filters, 3)
	assert.Equal(t, "abs", filters[0].Name)
	assert.Equal(t, filePath, filters[0].FilePath)

	assert.Equal(t, "test", filters[1].Name)
	assert.Equal(t, filePath, filters[1].FilePath)

	assert.Equal(t, "test2", filters[2].Name)
	assert.Equal(t, filePath, filters[2].FilePath)
}

func TestParseTwigExtension2(t *testing.T) {
	// Read test file
	filePath := filepath.Join("testdata", "extension2.php")
	content, err := os.ReadFile(filePath)
	require.NoError(t, err)

	rootNode := phpparser.ParseBytes(content).Tree.Root

	// Parse Twig extension
	functions, _, err := ParseTwigExtension(filePath, rootNode, content)
	require.NoError(t, err)

	// Verify functions
	require.Len(t, functions, 2)
	assert.Equal(t, "inAppPurchase", functions[0].Name)
	assert.Equal(t, filePath, functions[0].FilePath)
	require.Len(t, functions[0].Parameters, 2)
	assert.Equal(t, "string", functions[0].Parameters[0].Type)
	assert.Equal(t, "$extensionName", functions[0].Parameters[0].Name)
	assert.Equal(t, "string", functions[0].Parameters[1].Type)
	assert.Equal(t, "$identifier", functions[0].Parameters[1].Name)

	assert.Equal(t, "allInAppPurchases", functions[1].Name)
	assert.Equal(t, filePath, functions[1].FilePath)
}

func TestParseTwigOperatorsSupportsLegacyAndExpressionParserAPIs(
	t *testing.T,
) {
	source := []byte(`<?php
use Twig\Extension\AbstractExtension;

class AppExtension extends AbstractExtension
{
    public function getOperators(): array
    {
        return [
            [
                'not' => ['precedence' => 50, 'class' => NotNode::class],
            ],
            [
                'or' => ['precedence' => 10, 'class' => OrNode::class],
            ],
        ];
    }

    public function getExpressionParsers(): array
    {
        return [
            new BinaryOperatorExpressionParser(AndNode::class, 'b-and', 18),
            new BinaryOperatorExpressionParser(
                ElvisNode::class,
                '?:',
                5,
                aliases: ['? :'],
            ),
            new BinaryOperatorExpressionParser(
                PositionalNode::class,
                'positioned',
                5,
                null,
                null,
                null,
                ['position alias'],
            ),
            new UnaryOperatorExpressionParser(NotNode::class, 'expression_not', 70),
            new UnaryOperatorExpressionParser(
                BangNode::class,
                'bang',
                70,
                null,
                null,
                ['! alias'],
            ),
            new UnrelatedParser(IgnoredNode::class, 'ignored', 10),
        ];
    }
}

class OtherExtension implements \Twig\Extension\ExtensionInterface
{
    public function getExpressionParsers(): array
    {
        if (feature_enabled()) {
            return [
                new BinaryOperatorExpressionParser(Node::class, 'inside-control-flow', 20),
            ];
        }
        return [];
    }
}

class NotAnExtension
{
    public function getExpressionParsers(): array
    {
        return [
            new BinaryOperatorExpressionParser(Node::class, 'not-an-extension', 20),
        ];
    }
}`)
	root := phpparser.ParseBytes(source).Tree.Root
	operators := ParseTwigOperators(
		"/project/AppExtension.php",
		root,
		source,
	)
	byName := make(map[string]TwigOperator, len(operators))
	names := make([]string, 0, len(operators))
	for _, operator := range operators {
		byName[operator.Name] = operator
		names = append(names, operator.Name)
		require.Equal(
			t,
			operator.Name,
			string(source[operator.Range.Start:operator.Range.End]),
		)
	}

	require.ElementsMatch(t, []string{
		"not",
		"or",
		"b-and",
		"?:",
		"? :",
		"positioned",
		"position alias",
		"expression_not",
		"bang",
		"! alias",
		"inside-control-flow",
	}, names)
	assert.True(t, byName["not"].Legacy)
	assert.True(t, byName["not"].Unary)
	assert.True(t, byName["or"].Legacy)
	assert.False(t, byName["or"].Unary)
	assert.True(t, byName["? :"].Alias)
	assert.False(t, byName["? :"].Unary)
	assert.True(t, byName["! alias"].Alias)
	assert.True(t, byName["! alias"].Unary)
	assert.NotContains(t, byName, "precedence")
	assert.NotContains(t, byName, "class")
	assert.NotContains(t, byName, "ignored")
	assert.NotContains(t, byName, "not-an-extension")
}

func TestTwigIndexerPersistsAndClearsOperators(t *testing.T) {
	cache := t.TempDir()
	filePath := filepath.Join(t.TempDir(), "AppExtension.php")
	source := []byte(`<?php
class AppExtension extends \Twig\Extension\AbstractExtension
{
    public function getExpressionParsers(): array
    {
        return [
            new BinaryOperatorExpressionParser(Node::class, 'custom-op', 20, aliases: ['custom alias']),
        ];
    }
}`)
	idx, err := NewTwigIndexer(cache)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(filePath, source)))
	operators, err := idx.GetAllTwigOperators()
	require.NoError(t, err)
	require.Len(t, operators, 2)
	require.NoError(t, idx.Close())

	restored, err := NewTwigIndexer(cache)
	require.NoError(t, err)
	restoredOperators, err := restored.GetAllTwigOperators()
	require.NoError(t, err)
	require.ElementsMatch(t, operators, restoredOperators)
	require.NoError(t, restored.Index(indexer.NewParsedFile(
		filePath,
		[]byte(`<?php class AppExtension {}`),
	)))
	restoredOperators, err = restored.GetAllTwigOperators()
	require.NoError(t, err)
	require.Empty(t, restoredOperators)
	require.NoError(t, restored.Close())
}

func TestParseTwigExtensionAttributesWithoutAbstractExtension(t *testing.T) {
	content := []byte(`<?php
namespace App\Twig;
use Twig\Attribute\AsTwigFilter as Filter;
use Twig\Attribute\AsTwigFunction;
use Twig\DeprecatedCallableInfo;

class AppExtension
{
    #[AsTwigFunction(
        name: 'product_url',
        deprecationInfo: new DeprecatedCallableInfo('app', '2.0', 'new_url'),
    )]
    public function productUrl(string $id, bool $absolute = false): string
    {
        return '';
    }

    #[Filter('price')]
    public function price(float $value): string
    {
        return '';
    }
}`)
	root := phpparser.ParseBytes(content).Tree.Root
	functions, filters, err := ParseTwigExtension(
		"AppExtension.php",
		root,
		content,
	)
	require.NoError(t, err)
	require.Len(t, functions, 1)
	require.Len(t, filters, 1)

	assert.Equal(t, "product_url", functions[0].Name)
	assert.Equal(t, "App\\Twig\\AppExtension::productUrl", functions[0].Method)
	assert.Equal(t, "product_url($id, $absolute)", functions[0].Usage)
	require.Len(t, functions[0].Parameters, 2)
	assert.True(t, functions[0].Parameters[1].Optional)
	assert.True(t, functions[0].Deprecated)
	assert.Contains(t, functions[0].Deprecation, "app 2.0")
	assert.Contains(t, functions[0].Deprecation, "new_url")

	assert.Equal(t, "price", filters[0].Name)
	assert.Equal(t, "App\\Twig\\AppExtension::price", filters[0].Method)
	assert.Equal(t, "price($value)", filters[0].Usage)
}

func TestParseTwigTestsFromRegistriesAndAttributes(t *testing.T) {
	content := []byte(`<?php
namespace App\Twig;

use Twig\Attribute\AsTwigTest;
use Twig\Extension\AbstractExtension;
use Twig\TwigTest;
use App\Twig\Node\PositiveNode;

class AppExtension extends AbstractExtension
{
    public function getTests(): array
    {
        return [
            new TwigTest('positive', $this->positive(...)),
            new \Twig_SimpleTest('legacy_even', [$this, 'even']),
            new \Twig_Test('legacy_test', [$this, 'even']),
            new TwigTest('node_positive', null, [
                'node_class' => PositiveNode::class,
            ]),
        ];
    }

    public function positive(int $value): bool { return $value > 0; }
    public function even(int $value): bool { return $value % 2 === 0; }
}

class AttributeTests
{
    #[AsTwigTest('uuid')]
    public function isUuid(string $value): bool { return true; }
}`)
	root := phpparser.ParseBytes(content).Tree.Root
	tests := ParseTwigTests(
		"/project/src/AppExtension.php",
		root,
		content,
	)
	require.Len(t, tests, 5)
	byName := make(map[string]TwigTest, len(tests))
	for _, twigTest := range tests {
		byName[twigTest.Name] = twigTest
	}
	assert.Equal(t, "$this->positive", byName["positive"].Method)
	assert.Equal(t, "positive($value)", byName["positive"].Usage)
	require.Len(t, byName["positive"].Parameters, 1)
	assert.Equal(t, "int", byName["positive"].Parameters[0].Type)
	assert.Equal(t, "$this->even", byName["legacy_even"].Method)
	assert.Equal(t, "$this->even", byName["legacy_test"].Method)
	assert.Equal(
		t,
		"App\\Twig\\Node\\PositiveNode::compile",
		byName["node_positive"].Method,
	)
	assert.Equal(t, "node_positive()", byName["node_positive"].Usage)
	assert.Equal(
		t,
		"App\\Twig\\AttributeTests::isUuid",
		byName["uuid"].Method,
	)
	assert.Equal(t, "uuid($value)", byName["uuid"].Usage)
}

func TestTwigIndexerPersistsAndClearsTests(t *testing.T) {
	cache := t.TempDir()
	filePath := filepath.Join(t.TempDir(), "AppExtension.php")
	source := []byte(`<?php
class AppExtension extends \Twig\Extension\AbstractExtension
{
    public function getTests(): array
    {
        return [new \Twig\TwigTest('positive', $this->positive(...))];
    }

    public function positive(int $value): bool { return $value > 0; }
}`)
	idx, err := NewTwigIndexer(cache)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(filePath, source)))
	tests, err := idx.GetAllTwigTests()
	require.NoError(t, err)
	require.Len(t, tests, 1)
	assert.Equal(t, "positive", tests[0].Name)
	require.NoError(t, idx.Close())

	restored, err := NewTwigIndexer(cache)
	require.NoError(t, err)
	restoredTests, err := restored.GetAllTwigTests()
	require.NoError(t, err)
	require.Equal(t, tests, restoredTests)
	require.NoError(t, restored.Index(indexer.NewParsedFile(
		filePath,
		[]byte(`<?php class AppExtension {}`),
	)))
	restoredTests, err = restored.GetAllTwigTests()
	require.NoError(t, err)
	require.Empty(t, restoredTests)
	require.NoError(t, restored.Close())
}

func TestParseDeprecatedTwigExtensionCallables(t *testing.T) {
	content := []byte(`<?php
namespace App\Twig;
use Twig\DeprecatedCallableInfo;
use Twig\Extension\AbstractExtension;
use Twig\TwigFilter;
use Twig\TwigFunction;

class AppExtension extends AbstractExtension
{
    public function getFunctions(): array
    {
        return [
            new TwigFunction(
                'legacy_option',
                $this->active(...),
                ['deprecated' => '1.2', 'alternative' => 'modern'],
            ),
            new TwigFunction(
                'legacy_info',
                $this->active(...),
                ['deprecation_info' => new DeprecatedCallableInfo('app', '2.0', 'modern')],
            ),
            new TwigFunction('legacy_trigger', $this->legacyTrigger(...)),
            new TwigFunction('legacy_method_trigger', $this->legacyMethodTrigger(...)),
            new TwigFunction('legacy_generated', $this->legacyGenerated(...)),
            new TwigFunction('argument_warning', $this->argumentWarning(...)),
            new TwigFunction('active', $this->active(...)),
        ];
    }

    public function getFilters(): array
    {
        return [
            new TwigFilter('legacy_filter', $this->legacyFilter(...)),
        ];
    }

    public function active(): string { return ''; }

    public function legacyTrigger(): string
    {
        trigger_deprecation(
            'app',
            '1.0',
            'The "legacy_trigger" Twig function is deprecated. Use modern instead.',
        );
        return '';
    }

    public function legacyMethodTrigger(): string
    {
        $info = new DeprecatedCallableInfo('app', '1.0');
        $info->triggerDeprecation(__FILE__, __LINE__);
        return '';
    }

    public function legacyGenerated(): string
    {
        Feature::triggerDeprecationOrThrow(
            'v2',
            Feature::deprecatedMethodMessage(self::class, __METHOD__, 'v2', 'modern'),
        );
        return '';
    }

    public function argumentWarning(mixed $value): string
    {
        if (!is_string($value)) {
            Feature::triggerDeprecationOrThrow(
                'v2',
                'Passing a non-string value is deprecated.',
            );
        }
        return '';
    }

    public function legacyFilter(string $value): string
    {
        Feature::triggerDeprecationOrThrow(
            'v2',
            'The "legacy_filter" Twig filter is deprecated. Use active_filter instead.',
        );
        return $value;
    }
}`)
	root := phpparser.ParseBytes(content).Tree.Root
	functions, filters, err := ParseTwigExtension(
		"AppExtension.php",
		root,
		content,
	)
	require.NoError(t, err)
	require.Len(t, functions, 7)
	require.Len(t, filters, 1)

	byFunction := make(map[string]TwigFunction)
	for _, function := range functions {
		byFunction[function.Name] = function
	}
	assert.True(t, byFunction["legacy_option"].Deprecated)
	assert.Contains(t, byFunction["legacy_option"].Deprecation, "modern")
	assert.NotZero(t, byFunction["legacy_option"].DeprecatedRange.Len())
	assert.True(t, byFunction["legacy_info"].Deprecated)
	assert.Contains(t, byFunction["legacy_info"].Deprecation, "app 2.0")
	assert.Contains(t, byFunction["legacy_info"].Deprecation, "modern")
	assert.True(t, byFunction["legacy_trigger"].Deprecated)
	assert.Contains(
		t,
		byFunction["legacy_trigger"].Deprecation,
		"legacy_trigger",
	)
	assert.True(t, byFunction["legacy_method_trigger"].Deprecated)
	assert.Empty(t, byFunction["legacy_method_trigger"].Deprecation)
	assert.True(t, byFunction["legacy_generated"].Deprecated)
	assert.Contains(
		t,
		byFunction["legacy_generated"].Deprecation,
		"modern",
	)
	assert.False(
		t,
		byFunction["argument_warning"].Deprecated,
	)
	assert.False(t, byFunction["active"].Deprecated)
	assert.True(t, filters[0].Deprecated)
	assert.Contains(t, filters[0].Deprecation, "active_filter")
}

func TestParseDeprecatedLegacyTwigExtensionInterface(t *testing.T) {
	content := []byte(`<?php
use Twig\TwigFunction;
class LegacyExtension implements \Twig_ExtensionInterface
{
    public function getFunctions(): array
    {
        return [
            new TwigFunction(
                'legacy_callable_info',
                [self::class, 'legacyCallableInfo'],
            ),
        ];
    }

    public static function legacyCallableInfo(): string
    {
        $info = new \Twig\DeprecatedCallableInfo('twig/twig', '3.12');
        $info->triggerDeprecation(__FILE__, __LINE__);
        return '';
    }
}`)
	root := phpparser.ParseBytes(content).Tree.Root
	functions, filters, err := ParseTwigExtension(
		"LegacyExtension.php",
		root,
		content,
	)
	require.NoError(t, err)
	require.Len(t, functions, 1)
	assert.Equal(t, "legacy_callable_info", functions[0].Name)
	assert.True(t, functions[0].Deprecated)
	assert.Empty(t, functions[0].Deprecation)
	assert.Empty(t, filters)
}

func TestParseTwigExtensionNotExtending(t *testing.T) {
	// Create a temporary file that doesn't extend AbstractExtension
	tmpFile := filepath.Join(t.TempDir(), "not_extension.php")
	content := []byte(`<?php
namespace App\Twig;

class NotExtension
{
    public function test() {}
}`)

	err := os.WriteFile(tmpFile, content, 0644)
	require.NoError(t, err)

	rootNode := phpparser.ParseBytes(content).Tree.Root

	// Parse Twig extension
	functions, filters, err := ParseTwigExtension(tmpFile, rootNode, content)
	require.NoError(t, err)
	assert.Nil(t, functions, "Should return nil functions for non-extension classes")
	assert.Nil(t, filters, "Should return nil filters for non-extension classes")
}
