package lsp

import (
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	jsonsyntax "github.com/shopware/shopware-lsp/internal/parser/json/syntax"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	scssquery "github.com/shopware/shopware-lsp/internal/parser/scss/query"
	scsssyntax "github.com/shopware/shopware-lsp/internal/parser/scss/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	vuesyntax "github.com/shopware/shopware-lsp/internal/parser/vue/syntax"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/stretchr/testify/require"
)

func syntaxAtTestPosition(
	t *testing.T,
	manager *DocumentManager,
	uri string,
	line,
	character int,
) (*cst.Node, *cst.Token, *cst.Node) {
	t.Helper()
	syntax, ok := manager.SyntaxContext(uri, line, character)
	require.True(t, ok)
	return syntax.Root, syntax.Token, syntax.Node
}

func TestDocumentManagerUsesInjectedLanguageRegistry(t *testing.T) {
	registry, err := language.NewRegistry(language.Definition{
		ID:         "custom",
		Extensions: []string{".custom"},
		Parse: func(source string) language.ParseResult {
			builder := cst.NewBuilder(source)
			builder.StartNode(1)
			builder.Token(2, cst.TextRange{Start: 0, End: uint32(len(source))})
			builder.FinishNode()
			return language.ParseResult{Tree: builder.Finish()}
		},
	})
	require.NoError(t, err)

	manager := NewDocumentManagerWithRegistry(registry)
	defer manager.Close()
	manager.OpenDocument("file:///tmp/example.custom", "content", 1)

	syntax, ok := manager.SyntaxContext("file:///tmp/example.custom", 0, 2)
	require.True(t, ok)
	require.Equal(t, language.ID("custom"), syntax.Language)
	require.Equal(t, "content", syntax.Root.Text())
}

func TestDocumentManagerObserverReplaysUpdatesAndCloses(t *testing.T) {
	manager := NewDocumentManager()
	defer manager.Close()

	const uri = "file:///tmp/Resources/app/administration/src/Card.vue"
	manager.OpenDocument(uri, "<template><div /></template>", 1)

	var versions []int
	var closed []string
	manager.RegisterObserver(DocumentObserver{
		DidOpenOrChange: func(document *TextDocument) {
			versions = append(versions, document.Version)
		},
		DidClose: func(uri string) {
			closed = append(closed, uri)
		},
	})
	require.Equal(t, []int{1}, versions, "already-open documents must replay")

	manager.UpdateDocument(uri, "<template><span /></template>", 2)
	require.Equal(t, []int{1, 2}, versions)
	manager.CloseDocument(uri)
	require.Equal(t, []string{uri}, closed)
}

func TestDocumentManagerPHPUsesPureGoSyntaxTree(t *testing.T) {
	manager := NewDocumentManager()
	defer manager.Close()

	const (
		uri    = "file:///tmp/Controller.php"
		source = `<?php class Controller { public function index() { return $this->redirectToRoute('storefront.home'); } }`
	)
	manager.OpenDocument(uri, source, 1)

	document, ok := manager.GetDocument(uri)
	require.True(t, ok)
	require.NotNil(t, document.SyntaxTree)
	require.Equal(t, language.PHP, document.SyntaxLanguage)

	column := strings.Index(source, "storefront.home") + 2
	root, token, node := syntaxAtTestPosition(t, manager, uri, 0, column)
	require.NotNil(t, root)
	require.NotNil(t, token)
	require.NotNil(t, node)
	require.Equal(t, phpsyntax.TkString, token.Kind())
	require.True(t, phpquery.StringInCall(node, 0, "redirectToRoute"))
	require.Equal(t, "storefront.home", phpquery.StringValue(node))
}

func TestDocumentManagerTwigContextAtEOF(t *testing.T) {
	manager := NewDocumentManager()
	defer manager.Close()

	const (
		uri    = "file:///tmp/incomplete.twig"
		source = "{{ value|"
	)
	manager.OpenDocument(uri, source, 1)

	root, token, node := syntaxAtTestPosition(t, manager, uri, 0, len(source))
	require.NotNil(t, root)
	require.NotNil(t, token)
	require.NotNil(t, node)
	require.Equal(t, "|", token.Text())
	require.Equal(t, twigsyntax.TwigFilter, node.Kind())
	require.True(t, twigquery.IsFilterPosition(node))
}

func TestDocumentManagerTwigContextUsesUTF16Columns(t *testing.T) {
	manager := NewDocumentManager()
	defer manager.Close()

	const (
		uri    = "file:///tmp/unicode.twig"
		source = "{{ path('😀route') }}"
	)
	manager.OpenDocument(uri, source, 1)

	// Nine ASCII UTF-16 units precede the emoji and the emoji itself consumes
	// two units, so column 11 is the beginning of "route".
	_, token, node := syntaxAtTestPosition(t, manager, uri, 0, 11)
	require.NotNil(t, token)
	require.NotNil(t, node)
	require.Equal(t, "route", token.Text())
	require.True(t, twigquery.StringInFunction(node, "path"))
}

func TestDocumentManagerJSONUsesPureGoSyntaxTree(t *testing.T) {
	manager := NewDocumentManager()
	defer manager.Close()

	const (
		uri    = "file:///tmp/config.json"
		source = `{"emoji":"😀","enabled":true}`
	)
	manager.OpenDocument(uri, source, 1)

	document, ok := manager.GetDocument(uri)
	require.True(t, ok)
	require.NotNil(t, document.SyntaxTree)
	require.Equal(t, language.JSON, document.SyntaxLanguage)

	// The emoji consumes two UTF-16 code units. Column 25 is inside "true".
	root, token, node := syntaxAtTestPosition(t, manager, uri, 0, 25)
	require.NotNil(t, root)
	require.NotNil(t, token)
	require.NotNil(t, node)
	require.Equal(t, "true", token.Text())
	require.Equal(t, jsonsyntax.JsonBoolean, node.Kind())
}

func TestDocumentManagerHTMLUsesTwigSyntaxTree(t *testing.T) {
	manager := NewDocumentManager()
	defer manager.Close()

	const (
		uri    = "file:///tmp/page.html"
		source = `<div data-controller="hello"></div>`
	)
	manager.OpenDocument(uri, source, 1)

	document, ok := manager.GetDocument(uri)
	require.True(t, ok)
	require.NotNil(t, document.SyntaxTree)
	require.Equal(t, language.Twig, document.SyntaxLanguage)
}

func TestDocumentManagerVueUsesMixedEmbeddedSyntaxTree(t *testing.T) {
	manager := NewDocumentManager()
	defer manager.Close()

	const (
		uri    = "file:///tmp/Card.vue"
		source = `<template><sw-card>{{ title }}</sw-card></template>
<script setup>const title = ref('Card');</script>`
	)
	manager.OpenDocument(uri, source, 1)
	document, ok := manager.GetDocument(uri)
	require.True(t, ok)
	require.Equal(t, language.Vue, document.SyntaxLanguage)
	require.Equal(t, vuesyntax.VueDocument, document.SyntaxTree.Root.Kind())

	templateOffset := strings.Index(source, "title")
	templateNode := document.SyntaxTree.Root.NodeAtOffset(uint32(templateOffset))
	require.Equal(t, language.Twig, EffectiveSyntaxLanguage(language.Vue, templateNode))
	require.NotNil(t, twigquery.ClosestNodeOfKind(templateNode, twigsyntax.TwigLiteralName))

	scriptOffset := strings.LastIndex(source, "title")
	scriptNode := document.SyntaxTree.Root.NodeAtOffset(uint32(scriptOffset))
	require.Equal(t, language.JavaScript, EffectiveSyntaxLanguage(language.Vue, scriptNode))
	require.Equal(t, jssyntax.JsIdentifier, scriptNode.Kind())
}

func TestDocumentManagerSCSSUsesPureGoSyntaxTree(t *testing.T) {
	manager := NewDocumentManager()
	defer manager.Close()

	const (
		uri    = "file:///tmp/theme.scss"
		source = `.button { color: $sw-color-brand-primary; }`
	)
	manager.OpenDocument(uri, source, 1)

	document, ok := manager.GetDocument(uri)
	require.True(t, ok)
	require.NotNil(t, document.SyntaxTree)
	require.Equal(t, language.SCSS, document.SyntaxLanguage)

	root, token, node := syntaxAtTestPosition(t, manager, uri, 0, 30)
	require.NotNil(t, root)
	require.NotNil(t, token)
	require.NotNil(t, node)
	require.Equal(t, "$sw-color-brand-primary", token.Text())
	require.Equal(t, scsssyntax.ScssVariable, node.Kind())
	require.Equal(t, "sw-color-brand-primary", scssquery.VariableName(node))
}

func TestDocumentManagerSCSSContextAtIncompleteFeature(t *testing.T) {
	manager := NewDocumentManager()
	defer manager.Close()

	const (
		uri    = "file:///tmp/features.scss"
		source = `display: feature("ACCESSIBILITY`
	)
	manager.OpenDocument(uri, source, 1)

	_, _, node := syntaxAtTestPosition(t, manager, uri, 0, len(source))
	require.NotNil(t, node)
	require.True(t, scssquery.StringInFunction(node, "feature"))
	require.Equal(t, "ACCESSIBILITY", scssquery.StringValue(node))
}

func TestDocumentManagerYAMLUsesPureGoSyntaxTree(t *testing.T) {
	manager := NewDocumentManager()
	defer manager.Close()

	const (
		uri    = "file:///tmp/services.yaml"
		source = "services:\n  App\\Service\\Example:\n    class: App\\Service\\Concrete\n"
	)
	manager.OpenDocument(uri, source, 1)

	document, ok := manager.GetDocument(uri)
	require.True(t, ok)
	require.NotNil(t, document.SyntaxTree)
	require.Equal(t, language.YAML, document.SyntaxLanguage)

	root, token, node := syntaxAtTestPosition(t, manager, uri, 2, 17)
	require.NotNil(t, root)
	require.NotNil(t, token)
	require.NotNil(t, node)
	require.Equal(t, "App\\Service\\Concrete", token.Text())
	require.Equal(t, yamlsyntax.YamlScalar, node.Kind())
	require.Equal(t, []string{"services", `App\Service\Example`, "class"}, yamlquery.PairPath(node))
}

func TestDocumentManagerYAMLContextAtEmptyValue(t *testing.T) {
	manager := NewDocumentManager()
	defer manager.Close()

	const (
		uri    = "file:///tmp/services.yml"
		source = "services:\n  App\\Service\\Example:\n    class: "
	)
	manager.OpenDocument(uri, source, 1)

	_, _, node := syntaxAtTestPosition(t, manager, uri, 2, len("    class: "))
	require.NotNil(t, node)
	require.Equal(t, []string{"services", `App\Service\Example`, "class"}, yamlquery.PairPath(node))
}

func TestDocumentManagerXMLUsesPureGoSyntaxTree(t *testing.T) {
	manager := NewDocumentManager()
	defer manager.Close()

	const (
		uri    = "file:///tmp/services.xml"
		source = `<container><service id="App\Service"/></container>`
	)
	manager.OpenDocument(uri, source, 1)

	document, ok := manager.GetDocument(uri)
	require.True(t, ok)
	require.NotNil(t, document.SyntaxTree)
	require.Equal(t, language.XML, document.SyntaxLanguage)

	root, token, node := syntaxAtTestPosition(t, manager, uri, 0, 30)
	require.NotNil(t, root)
	require.NotNil(t, token)
	require.NotNil(t, node)
	require.Equal(t, `"App\Service"`, token.Text())
	require.Equal(t, xmlsyntax.XmlAttributeValue, node.Kind())
	require.Equal(t, `App\Service`, xmlquery.AttributeValue(node))
}

func TestDocumentManagerXMLContextAtIncompleteAttribute(t *testing.T) {
	manager := NewDocumentManager()
	defer manager.Close()

	const (
		uri    = "file:///tmp/services.xml"
		source = `<argument type="service" id="App\Service`
	)
	manager.OpenDocument(uri, source, 1)

	_, _, node := syntaxAtTestPosition(t, manager, uri, 0, len(source))
	require.NotNil(t, node)
	require.Equal(t, "id", xmlquery.AttributeName(node))
	require.Equal(t, `App\Service`, xmlquery.AttributeValue(node))
}
