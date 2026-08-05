package twig

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/stretchr/testify/require"
)

func TestTwigIndexerUsesPureGoParserThroughFileScanner(t *testing.T) {
	tempDir := t.TempDir()
	templatePath := filepath.Join(
		tempDir,
		"src",
		"Storefront",
		"Resources",
		"views",
		"storefront",
		"page",
		"example.html.twig",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(templatePath), 0755))
	require.NoError(t, os.WriteFile(
		templatePath,
		[]byte(`{% block page_example %}content{% endblock %}`),
		0644,
	))

	twigIndexer, err := NewTwigIndexer(filepath.Join(tempDir, "cache"))
	require.NoError(t, err)
	defer func() { require.NoError(t, twigIndexer.Close()) }()

	fileScanner, err := indexer.NewFileScanner(tempDir, filepath.Join(tempDir, "scanner.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, fileScanner.Close()) }()
	fileScanner.AddIndexer(twigIndexer)

	require.NoError(t, fileScanner.IndexFiles(context.Background(), []string{templatePath}))

	files, err := twigIndexer.GetTwigFilesByRelPath("@Storefront/storefront/page/example.html.twig")
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Contains(t, files[0].Blocks, "page_example")

	hashes, err := twigIndexer.GetTwigBlockHashes("page_example")
	require.NoError(t, err)
	require.Len(t, hashes, 1)
	require.Equal(t, templatePath, hashes[0].AbsolutePath)
}

func TestTwigIndexerStoresSymfonyTemplateAliases(t *testing.T) {
	idx, err := NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	path := "/project/MyBundle/src/Resources/views/card.html.twig"
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte("card"))))
	for _, name := range []string{
		"card.html.twig",
		"@Storefront/card.html.twig",
		"@MyBundle/card.html.twig",
	} {
		files, queryErr := idx.GetTwigFilesByRelPath(name)
		require.NoError(t, queryErr)
		require.Len(t, files, 1, name)
		require.Equal(t, path, files[0].Path)
	}
	file, found, err := idx.GetTwigFileByPath(path)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, path, file.Path)
}

func TestTwigIndexerStoresAndRestoresMacrosAndUsages(t *testing.T) {
	cacheDir := t.TempDir()
	idx, err := NewTwigIndexer(cacheDir)
	require.NoError(t, err)

	macroPath := "/project/templates/macros/forms.html.twig"
	pagePath := "/project/templates/page.html.twig"
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		macroPath,
		[]byte(`{% macro input(name, value = '') %}{% endmacro %}`),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		pagePath,
		[]byte(`{% import 'macros/forms.html.twig' as forms %}
{{ forms.input('email') }}
`),
	)))

	macros, err := idx.FindMacro("macros/forms.html.twig", "input")
	require.NoError(t, err)
	require.Len(t, macros, 1)
	require.Equal(t, "input(name, value = '')", macros[0].Signature())
	usages, err := idx.GetMacroUsages(
		"macros/forms.html.twig",
		"input",
	)
	require.NoError(t, err)
	require.Len(t, usages, 1)
	require.Equal(t, pagePath, usages[0].FilePath)
	require.NoError(t, idx.Close())

	restored, err := NewTwigIndexer(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	macros, err = restored.FindMacro("macros/forms.html.twig", "input")
	require.NoError(t, err)
	require.Len(t, macros, 1)
	usages, err = restored.GetMacroUsages(
		"macros/forms.html.twig",
		"input",
	)
	require.NoError(t, err)
	require.Len(t, usages, 1)
}

func TestTwigIndexerStoresRestoresAndClearsTemplateReferences(t *testing.T) {
	cacheDir := t.TempDir()
	index, err := NewTwigIndexer(cacheDir)
	require.NoError(t, err)

	targetPath := filepath.Join(
		t.TempDir(),
		"templates",
		"layout",
		"base.html.twig",
	)
	twigUsagePath := filepath.Join(t.TempDir(), "templates", "page.html.twig")
	phpUsagePath := filepath.Join(t.TempDir(), "PageController.php")
	require.NoError(t, index.Index(indexer.NewParsedFile(
		targetPath,
		[]byte(`{% block body %}{% endblock %}`),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		twigUsagePath,
		[]byte(`{% extends 'layout/base.html.twig' %}`),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		phpUsagePath,
		[]byte(`<?php $this->render('layout/base.html.twig');`),
	)))

	references, err := index.GetTemplateReferences("layout/base.html.twig")
	require.NoError(t, err)
	require.Len(t, references, 2)
	require.ElementsMatch(t, []string{
		phpUsagePath,
		twigUsagePath,
	}, []string{
		references[0].FilePath,
		references[1].FilePath,
	})

	require.NoError(t, index.Close())
	restored, err := NewTwigIndexer(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	references, err = restored.GetTemplateReferences("layout/base.html.twig")
	require.NoError(t, err)
	require.Len(t, references, 2)

	require.NoError(t, restored.Index(indexer.NewParsedFile(
		twigUsagePath,
		[]byte(`no template reference`),
	)))
	references, err = restored.GetTemplateReferences("layout/base.html.twig")
	require.NoError(t, err)
	require.Len(t, references, 1)
	require.Equal(t, phpUsagePath, references[0].FilePath)
}

func TestTwigIndexerStoresRestoresAndClearsConstantReferences(t *testing.T) {
	cacheDir := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/CardSuite.php",
		[]byte(`<?php
namespace App;
class CardSuite {
    public const CLUBS = 'clubs';
}`),
	)))

	idx, err := NewTwigIndexer(cacheDir)
	require.NoError(t, err)
	idx.SetDependencies(phpIndex, nil)
	directPath := "/project/templates/direct.html.twig"
	objectPath := "/project/templates/object.html.twig"
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		directPath,
		[]byte(`{{ constant('App\\CardSuite::CLUBS') }}`),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		objectPath,
		[]byte(`{# @var suite \App\CardSuite #}
{{ constant('CLUBS', suite) }}`),
	)))

	target := ConstantReference{
		Class: "App\\CardSuite",
		Name:  "CLUBS",
	}
	references, err := idx.GetConstantReferences(target)
	require.NoError(t, err)
	require.Len(t, references, 2)
	require.ElementsMatch(t, []string{
		directPath,
		objectPath,
	}, []string{
		references[0].FilePath,
		references[1].FilePath,
	})
	require.NoError(t, idx.Close())

	restored, err := NewTwigIndexer(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restored.SetDependencies(phpIndex, nil)
	references, err = restored.GetConstantReferences(target)
	require.NoError(t, err)
	require.Len(t, references, 2)

	require.NoError(t, restored.Index(indexer.NewParsedFile(
		objectPath,
		[]byte(`no constant reference`),
	)))
	references, err = restored.GetConstantReferences(target)
	require.NoError(t, err)
	require.Len(t, references, 1)
	require.Equal(t, directPath, references[0].FilePath)
}

func TestTwigIndexerStoresRestoresAndClearsPHPUsages(t *testing.T) {
	cacheDir := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/Product.php",
		[]byte(`<?php
namespace App;
class Product {
    public string $name;
    public function getNumber(): string { return ''; }
}
enum Status { case ACTIVE; }
`),
	)))

	idx, err := NewTwigIndexer(cacheDir)
	require.NoError(t, err)
	idx.SetDependencies(phpIndex, nil)
	propertyPath := "/project/templates/property.html.twig"
	methodPath := "/project/templates/method.html.twig"
	classPath := "/project/templates/class.html.twig"
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		propertyPath,
		[]byte(`{# @var product \App\Product #} {{ product.name }}`),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		methodPath,
		[]byte(`{# @var product \App\Product #} {{ product.number }}`),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		classPath,
		[]byte(`{{ enum('App\\Status') }}`),
	)))

	propertyTarget := PHPUsageReference{
		Class:  "App\\Product",
		Member: "name",
		Kind:   semantic.PropertySymbol,
	}
	methodTarget := PHPUsageReference{
		Class:  "App\\Product",
		Member: "getNumber",
		Kind:   semantic.MethodSymbol,
	}
	classTarget := PHPUsageReference{Class: "App\\Status"}
	for _, target := range []PHPUsageReference{
		propertyTarget,
		methodTarget,
		classTarget,
	} {
		references, queryErr := idx.GetPHPUsageReferences(target)
		require.NoError(t, queryErr)
		require.Len(t, references, 1)
	}
	require.NoError(t, idx.Close())

	restored, err := NewTwigIndexer(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	restored.SetDependencies(phpIndex, nil)
	references, err := restored.GetPHPUsageReferences(methodTarget)
	require.NoError(t, err)
	require.Len(t, references, 1)
	require.Equal(t, methodPath, references[0].FilePath)

	require.NoError(t, restored.Index(indexer.NewParsedFile(
		methodPath,
		[]byte(`no PHP usage`),
	)))
	references, err = restored.GetPHPUsageReferences(methodTarget)
	require.NoError(t, err)
	require.Empty(t, references)
}

func TestTwigIndexerStoresRestoresAndClearsExtensionUsages(t *testing.T) {
	cacheDir := t.TempDir()
	idx, err := NewTwigIndexer(cacheDir)
	require.NoError(t, err)
	functionPath := "/project/templates/function.html.twig"
	filterPath := "/project/templates/filter.html.twig"
	testPath := "/project/templates/test.html.twig"
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		functionPath,
		[]byte(`{{ form_start() }}`),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		filterPath,
		[]byte(`{{ value|trans }}`),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		testPath,
		[]byte(`{% if value is positive %}{% endif %}`),
	)))

	functions, err := idx.GetExtensionUsages(
		ExtensionFunctionUsage,
		"form_start",
	)
	require.NoError(t, err)
	require.Len(t, functions, 1)
	filters, err := idx.GetExtensionUsages(
		ExtensionFilterUsage,
		"trans",
	)
	require.NoError(t, err)
	require.Len(t, filters, 1)
	tests, err := idx.GetExtensionUsages(
		ExtensionTestUsage,
		"positive",
	)
	require.NoError(t, err)
	require.Len(t, tests, 1)
	require.NoError(t, idx.Close())

	restored, err := NewTwigIndexer(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	functions, err = restored.GetExtensionUsages(
		ExtensionFunctionUsage,
		"form_start",
	)
	require.NoError(t, err)
	require.Len(t, functions, 1)
	tests, err = restored.GetExtensionUsages(
		ExtensionTestUsage,
		"positive",
	)
	require.NoError(t, err)
	require.Len(t, tests, 1)

	require.NoError(t, restored.Index(indexer.NewParsedFile(
		functionPath,
		[]byte(`no function usage`),
	)))
	functions, err = restored.GetExtensionUsages(
		ExtensionFunctionUsage,
		"form_start",
	)
	require.NoError(t, err)
	require.Empty(t, functions)
}
