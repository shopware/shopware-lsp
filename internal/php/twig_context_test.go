package php

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainsFoldASCIIString(t *testing.T) {
	t.Parallel()
	require.True(t, containsFoldASCIIString(
		"/** @TEMPLATE without arguments */",
		"@template",
	))
	require.True(t, containsFoldASCIIString("prefix @TeMpLaTe suffix", "@template"))
	require.True(t, containsFoldASCIIString("anything", ""))
	require.False(t, containsFoldASCIIString("prefix @other suffix", "@template"))
}

func BenchmarkTwigContextCandidateMatcher(b *testing.B) {
	source := strings.Repeat("ordinary application source\n", 4096)

	b.Run("fused", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if twigContextCandidateMatcher.ContainsString(source) {
				b.Fatal("unexpected Twig context marker")
			}
		}
	})
	b.Run("separate", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if containsFoldASCIIString(source, "render") ||
				containsFoldASCIIString(source, "template") ||
				containsFoldASCIIString(source, "stream") {
				b.Fatal("unexpected Twig context marker")
			}
		}
	})
}

func TestPHPIndexCollectsTypedTwigTemplateContexts(t *testing.T) {
	index, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	source := `<?php
namespace App;

class Product {}

class ProductController
{
    public function show(Product $product)
    {
        $base = ['item' => $product];

        return $this->render(
            parameters: ['product' => $product, ...$base],
            view: 'product/show.html.twig',
        );
    }

    public function alternate(Product $product)
    {
        $context = ['secondary' => $product];

        return $this->render('product/show.html.twig', $context);
    }

    public function storefront(Product $product)
    {
        return $this->renderStorefront(
            '@Storefront/storefront/page/product.html.twig',
            ['page' => $product],
        );
    }

    #[Template('article/detail.html.twig')]
    public function article(Product $product): array
    {
        return ['article' => $product];
    }

    public function helper(Product $product)
    {
        return $this->render(
            'product/helper.html.twig',
            array_merge(['title' => 'Products'], $this->helperData($product)),
        );
    }

    private function helperData(Product $product): array
    {
        return ['related' => $product];
    }
}`
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/src/ProductController.php",
		[]byte(source),
	)))

	show, err := index.TwigTemplateVariables("product/show.html.twig")
	require.NoError(t, err)
	assertTwigVariableType(t, show, "product", "App\\Product")
	assertTwigVariableType(t, show, "item", "App\\Product")
	assertTwigVariableType(t, show, "secondary", "App\\Product")

	storefront, err := index.TwigTemplateVariables(
		"@Storefront/storefront/page/product.html.twig",
	)
	require.NoError(t, err)
	assertTwigVariableType(t, storefront, "page", "App\\Product")

	article, err := index.TwigTemplateVariables("article/detail.html.twig")
	require.NoError(t, err)
	assertTwigVariableType(t, article, "article", "App\\Product")

	helper, err := index.TwigTemplateVariables("product/helper.html.twig")
	require.NoError(t, err)
	assertTwigVariableType(t, helper, "title", `"Products"`)
	assertTwigVariableType(t, helper, "related", "App\\Product")
}

func TestPHPIndexResolvesAssignedTwigContextVariableTypes(t *testing.T) {
	index, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	root := t.TempDir()

	controller := `<?php
namespace App;

class ProductController
{
    public function __construct(private ProductLoader $loader) {}

    public function show()
    {
        $page = $this->loader->load();

        return $this->render('product/show.html.twig', [
            'page' => $page,
        ]);
    }
}`
	controllerPath := filepath.Join(root, "src", "ProductController.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(controllerPath), 0o755))
	require.NoError(t, os.WriteFile(
		controllerPath,
		[]byte(controller),
		0o644,
	))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		controllerPath,
		[]byte(controller),
	)))
	loader := `<?php
namespace App;

class Product {}

class ProductLoader
{
    public function load(): Product
    {
        return new Product();
    }
}`
	loaderPath := filepath.Join(root, "src", "ProductLoader.php")
	require.NoError(t, os.WriteFile(loaderPath, []byte(loader), 0o644))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		loaderPath,
		[]byte(loader),
	)))
	methods := index.FindMethods("App\\ProductLoader", "load")
	require.Len(t, methods, 1)
	require.Equal(t, "App\\Product", methods[0].ReturnType.String())
	controllerRoot := phpparser.Parse(controller).Tree.Root
	document := index.AnalyzeDocument(
		controllerPath,
		0,
		controllerRoot,
	)
	assignments := phpquery.Nodes(
		controllerRoot,
		phpsyntax.PhpAssignmentExpression,
	)
	require.NotEmpty(t, assignments)
	assignmentNodes := directNodesForTwigContext(assignments[0])
	require.GreaterOrEqual(t, len(assignmentNodes), 2)
	require.Equal(
		t,
		"App\\Product",
		document.TypeOf(assignmentNodes[len(assignmentNodes)-1]).Type.String(),
	)

	variables, err := index.TwigTemplateVariables(
		"product/show.html.twig",
	)
	require.NoError(t, err)
	assertTwigVariableType(t, variables, "page", "App\\Product")
}

func TestPHPIndexCollectsTemplateAnnotationAndGuessedAttributeContext(t *testing.T) {
	index, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	source := `<?php
namespace App\Controller\Admin;

class Product {}

class ProductController
{
    /**
     * @Template("product/legacy.html.twig")
     */
    public function legacyAction(Product $product): array
    {
        return ['legacy' => $product];
    }

    #[Template]
    public function showAction(Product $product): array
    {
        return ['product' => $product];
    }
}`
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/src/Controller/Admin/ProductController.php",
		[]byte(source),
	)))

	legacy, err := index.TwigTemplateVariables("product/legacy.html.twig")
	require.NoError(t, err)
	assertTwigVariableType(
		t,
		legacy,
		"legacy",
		"App\\Controller\\Admin\\Product",
	)

	guessed, err := index.TwigTemplateVariables("admin/product/show.html.twig")
	require.NoError(t, err)
	assertTwigVariableType(
		t,
		guessed,
		"product",
		"App\\Controller\\Admin\\Product",
	)
}

func TestPHPIndexCollectsTwigFormViewTypes(t *testing.T) {
	index, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	source := `<?php
namespace App\Controller;

use App\Form\CheckoutType;
use App\Form\SearchType;

class CheckoutController
{
    public function checkout()
    {
        $form = $this->createForm(CheckoutType::class);
        $view = $form->createView();

        return $this->render('checkout.html.twig', [
            'checkout' => $view,
            'search' => $this->createForm(SearchType::class)->createView(),
        ]);
    }
}`
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/src/CheckoutController.php",
		[]byte(source),
	)))

	variables, err := index.TwigTemplateVariables("checkout.html.twig")
	require.NoError(t, err)
	assertTwigVariableFormTypes(
		t,
		variables,
		"checkout",
		"App\\Form\\CheckoutType",
	)
	assertTwigVariableFormTypes(
		t,
		variables,
		"search",
		"App\\Form\\SearchType",
	)
}

func assertTwigVariableType(
	t *testing.T,
	variables []TwigTemplateVariable,
	name,
	expected string,
) {
	t.Helper()
	for _, variable := range variables {
		if variable.Name == name {
			assert.Equal(t, expected, variable.Type)
			return
		}
	}
	t.Fatalf("Twig variable %q not found in %#v", name, variables)
}

func assertTwigVariableFormTypes(
	t *testing.T,
	variables []TwigTemplateVariable,
	name string,
	expected ...string,
) {
	t.Helper()
	for _, variable := range variables {
		if variable.Name == name {
			assert.Equal(t, expected, variable.FormTypes)
			return
		}
	}
	t.Fatalf("Twig variable %q not found in %#v", name, variables)
}
