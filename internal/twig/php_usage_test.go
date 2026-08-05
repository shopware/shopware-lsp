package twig

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPUsageReferencesInDocumentResolvesMembersAndClasses(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	phpPath := filepath.Join(t.TempDir(), "Bar.php")
	phpSource := `<?php
namespace Foo;
class Bar {
    public string $RestVar;
    public function __construct(public float $primaryValue) {}
    public function getFoo(): string { return ''; }
    public const CLUBS = 'clubs';
}
enum CardSuite { case HEARTS; }
`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))

	templatePath := "/project/templates/usage.html.twig"
	source := `{# @var bar \Foo\Bar #}
{{ bar.RestVar }}
{{ bar.primaryValue }}
{{ bar.foo }}
{{ bar.getFoo() }}
{{ bar.CLUBS }}
{{ enum('Foo\\CardSuite') }}
{# @var \Foo\Bar $other #}
{# @var \Foo\Bar third #}`
	root := twigparser.Parse(source).Tree.Root
	references := PHPUsageReferencesInDocument(
		templatePath,
		root,
		PHPAccessResolver{PHP: phpIndex},
	)

	var properties, methods, constants, classes []PHPUsageReference
	for _, reference := range references {
		switch {
		case reference.Member == "":
			classes = append(classes, reference)
		case reference.Kind == semantic.PropertySymbol:
			properties = append(properties, reference)
		case reference.Kind == semantic.MethodSymbol:
			methods = append(methods, reference)
		case reference.Kind == semantic.ClassConstantSymbol:
			constants = append(constants, reference)
		}
	}
	require.Len(t, properties, 2)
	assert.ElementsMatch(
		t,
		[]string{"RestVar", "primaryValue"},
		[]string{properties[0].Access, properties[1].Access},
	)
	require.Len(t, methods, 2)
	assert.ElementsMatch(
		t,
		[]string{"foo", "getFoo"},
		[]string{methods[0].Access, methods[1].Access},
	)
	require.Len(t, constants, 1)
	assert.Equal(t, "CLUBS", constants[0].Access)

	var classNames []string
	for _, reference := range classes {
		classNames = append(classNames, reference.Class)
		assert.Equal(
			t,
			reference.Access,
			source[reference.Range.Start:reference.Range.End],
		)
	}
	assert.ElementsMatch(
		t,
		[]string{"Foo\\Bar", "Foo\\CardSuite", "Foo\\Bar", "Foo\\Bar"},
		classNames,
	)
}

func TestPHPUsageReferencesInDocumentUsesTypesTagLoopDeclarations(
	t *testing.T,
) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Product.php",
		[]byte(`<?php
namespace App;
class Product {
    public string $name;
}`),
	)))
	source := `{% types { products: 'App\\Product[]' } %}
{% for product in products %}{{ product.name }}{% endfor %}`
	references := PHPUsageReferencesInDocument(
		"/project/templates/product.html.twig",
		twigparser.Parse(source).Tree.Root,
		PHPAccessResolver{PHP: phpIndex},
	)
	var classFound, propertyFound bool
	for _, reference := range references {
		switch {
		case reference.Member == "" &&
			reference.Class == "App\\Product":
			classFound = true
			assert.Equal(t, `App\\Product`, reference.Access)
		case reference.Kind == semantic.PropertySymbol &&
			reference.Member == "name":
			propertyFound = true
		}
	}
	assert.True(t, classFound)
	assert.True(t, propertyFound)
}

func TestPHPUsageTargetForSymbolUsesDeclaringClass(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/Classes.php",
		[]byte(`<?php
namespace App;
class ParentType { public function getName(): string { return ''; } }
class ChildType extends ParentType {}
`),
	)))
	methods := phpIndex.FindMethods("App\\ChildType", "getName")
	require.Len(t, methods, 1)
	target, found := PHPUsageTargetForSymbol(
		phpIndex.SemanticSnapshot(),
		methods[0],
	)
	require.True(t, found)
	assert.Equal(t, "App\\ParentType", target.Class)
	assert.Equal(t, "getName", target.Member)
	assert.Equal(t, semantic.MethodSymbol, target.Kind)
}

func TestExtensionUsagesLinkTwigFunctionsAndFiltersToMethods(
	t *testing.T,
) {
	cacheDir := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	extensionPath := filepath.Join(t.TempDir(), "AppExtension.php")
	extensionSource := `<?php
namespace App\Twig;
use Twig\Attribute\AsTwigFilter;
use Twig\Attribute\AsTwigFunction;

class AppExtension {
    #[AsTwigFunction('product_number_function')]
    public function formatProductNumberFunction(string $number): string {
        return $number;
    }

    #[AsTwigFilter('product_number_filter')]
    public function formatProductNumberFilter(string $number): string {
        return $number;
    }
}`
	parsed := indexer.NewParsedFile(
		extensionPath,
		[]byte(extensionSource),
	)
	require.NoError(t, phpIndex.Index(parsed))
	twigIndex, err := NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	twigIndex.SetDependencies(phpIndex, nil)
	require.NoError(t, twigIndex.Index(parsed))

	source := `{{ product_number_function('123') }}
{{ value|product_number_filter }}`
	templatePath := "/project/templates/extensions.html.twig"
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		templatePath,
		[]byte(source),
	)))
	for _, methodName := range []string{
		"formatProductNumberFunction",
		"formatProductNumberFilter",
	} {
		methods := phpIndex.FindMethods(
			"App\\Twig\\AppExtension",
			methodName,
		)
		require.Len(t, methods, 1)
		usages, usageErr := twigIndex.GetExtensionUsagesForPHPSymbol(
			phpIndex,
			methods[0],
		)
		require.NoError(t, usageErr)
		require.Len(t, usages, 1)
	}
}
