package symfony

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseControllerReference(t *testing.T) {
	tests := []struct {
		value  string
		target string
		method string
		ok     bool
	}{
		{`App\Controller\ProductController::show`, `App\Controller\ProductController`, "show", true},
		{`app.product_controller:show`, "app.product_controller", "show", true},
		{`App\Controller\ProductController`, `App\Controller\ProductController`, "__invoke", true},
		{"Bundle:Controller:action", "", "", false},
	}
	for _, test := range tests {
		reference, ok := ParseControllerReference(test.value)
		assert.Equal(t, test.ok, ok, test.value)
		assert.Equal(t, test.target, reference.Target, test.value)
		assert.Equal(t, test.method, reference.Method, test.value)
	}
}

func TestYAMLAndXMLControllerReferences(t *testing.T) {
	yamlRoot := yamlparser.Parse(`product.show:
  path: /product
  controller: App\Controller\ProductController::show
product.legacy:
  defaults:
    _controller: app.product_controller:legacy
`).Tree.Root
	var yamlReferences []ControllerReference
	for _, scalar := range yamlquery.Nodes(yamlRoot, yamlsyntax.YamlScalar) {
		if reference, _, ok := YAMLControllerReference(scalar); ok {
			yamlReferences = append(yamlReferences, reference)
		}
	}
	require.Len(t, yamlReferences, 2)

	xmlRoot := xmlparser.Parse(`<routes>
<route id="product.show" controller="App\Controller\ProductController::show"/>
<route id="product.legacy"><default key="_controller">app.product_controller:legacy</default></route>
</routes>`).Tree.Root
	var xmlReferences []ControllerReference
	for _, node := range xmlquery.Nodes(
		xmlRoot,
		xmlsyntax.XmlAttribute,
		xmlsyntax.XmlText,
	) {
		if reference, _, ok := XMLControllerReference(node); ok {
			xmlReferences = append(xmlReferences, reference)
		}
	}
	// Querying every node can encounter the same attribute/text through its
	// descendants, so validate the distinct values.
	values := make(map[string]struct{})
	for _, reference := range xmlReferences {
		values[reference.Value] = struct{}{}
	}
	assert.Contains(t, values, `App\Controller\ProductController::show`)
	assert.Contains(t, values, `app.product_controller:legacy`)
}

func TestResolveControllerReferenceForClassAndService(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/ProductController.php",
		[]byte(`<?php
namespace App\Controller;
class ProductController {
    public function show(): void {}
    public function __invoke(): void {}
    private function hidden(): void {}
}`),
	)))

	serviceIndex, err := NewServiceIndex(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		"/project/services.yaml",
		[]byte(`services:
  app.product_controller:
    class: App\Controller\ProductController
`),
	)))

	for _, value := range []string{
		`App\Controller\ProductController::show`,
		`app.product_controller:show`,
		`App\Controller\ProductController`,
	} {
		reference, ok := ParseControllerReference(value)
		require.True(t, ok)
		resolution, resolveErr := ResolveControllerReference(
			reference,
			serviceIndex,
			phpIndex,
		)
		require.NoError(t, resolveErr)
		assert.True(t, resolution.TargetExists, value)
		assert.True(t, resolution.ClassFound, value)
		assert.True(t, resolution.MethodFound, value)
	}

	reference, _ := ParseControllerReference(
		`App\Controller\ProductController::hidden`,
	)
	resolution, err := ResolveControllerReference(
		reference,
		serviceIndex,
		phpIndex,
	)
	require.NoError(t, err)
	assert.True(t, resolution.ClassFound)
	assert.True(t, resolution.MethodDeclared)
	assert.False(t, resolution.MethodFound)
}

func TestResolveLegacyTwigControllerReference(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/NavController.php",
		[]byte(`<?php
namespace Acme\DemoBundle\Controller\Admin;
class NavController {
    public function showAction(): void {}
}`),
	)))
	reference, ok := ParseTwigControllerReference(
		"DemoBundle:Admin/Nav:show",
	)
	require.True(t, ok)
	resolution, err := ResolveControllerReference(
		reference,
		nil,
		phpIndex,
	)
	require.NoError(t, err)
	require.True(t, resolution.ClassFound)
	require.True(t, resolution.MethodFound)
	assert.Equal(
		t,
		`Acme\DemoBundle\Controller\Admin\NavController`,
		resolution.Class.FullyQualified,
	)
	assert.Equal(t, "showAction", resolution.Method.Name)
}
