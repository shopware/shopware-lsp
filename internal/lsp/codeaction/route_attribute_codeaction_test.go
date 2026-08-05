package codeaction

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteAttributeCodeActionControllerRecognition(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		source string
	}{
		{
			name: "abstract controller",
			source: `<?php
namespace App\Controller;
use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;
class TestController extends AbstractController
{
    public function indexAction() {}
}`,
		},
		{
			name: "AsController attribute",
			source: `<?php
namespace App\Endpoint;
use Symfony\Component\HttpKernel\Attribute\AsController;
#[AsController]
class Endpoint
{
    public function indexAction() {}
}`,
		},
		{
			name: "route on another method",
			source: `<?php
namespace App\Endpoint;
use Symfony\Component\Routing\Attribute\Route;
class Endpoint
{
    #[Route('/other')]
    public function otherAction() {}
    public function indexAction() {}
}`,
		},
		{
			name: "route on class",
			source: `<?php
namespace App\Endpoint;
use Symfony\Component\Routing\Attribute\Route;
#[Route('/api')]
class Endpoint
{
    public function indexAction() {}
}`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			provider := routeAttributeCodeActionFixture(t)
			request := routeAttributeCodeActionRequest(
				fixture.source,
				"indexAction",
			)
			actions := provider.GetCodeActions(
				context.Background(),
				request,
			)
			require.Len(t, actions, 1)
			assert.Equal(
				t,
				"Symfony: Add Route attribute",
				actions[0].Title,
			)
			assert.Equal(
				t,
				protocol.CodeActionRefactorRewrite,
				actions[0].Kind,
			)
		})
	}
}

func TestRouteAttributeCodeActionRejectsInvalidTargets(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		source string
		method string
	}{
		{
			name: "existing route",
			source: `<?php
namespace App\Controller;
use Symfony\Component\Routing\Attribute\Route;
class TestController
{
    #[Route('/')]
    public function indexAction() {}
}`,
			method: "indexAction",
		},
		{
			name: "private method",
			source: `<?php
namespace App\Controller;
class TestController
{
    private function helperMethod() {}
}`,
			method: "helperMethod",
		},
		{
			name: "static method",
			source: `<?php
namespace App\Controller;
class TestController
{
    public static function staticMethod() {}
}`,
			method: "staticMethod",
		},
		{
			name: "non controller",
			source: `<?php
namespace App\Service;
class TestService
{
    public function doSomething() {}
}`,
			method: "doSomething",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			actions := routeAttributeCodeActionFixture(t).
				GetCodeActions(
					context.Background(),
					routeAttributeCodeActionRequest(
						fixture.source,
						fixture.method,
					),
				)
			assert.Empty(t, actions)
		})
	}
}

func TestRouteAttributeCodeActionAddsImportPathAndName(t *testing.T) {
	source := `<?php
namespace App\Controller\Admin;
use Symfony\Bundle\FrameworkBundle\Controller\AbstractController;
class UserController extends AbstractController
{
    public function editAction() {}
}`
	request := routeAttributeCodeActionRequest(source, "editAction")
	actions := routeAttributeCodeActionFixture(t).GetCodeActions(
		context.Background(),
		request,
	)

	require.Len(t, actions, 1)
	edits := actions[0].Edit.Changes[request.TextDocument.URI]
	require.Len(t, edits, 2)
	assert.Equal(
		t,
		"#[Route('/admin/user/edit', name: 'app_admin_user_edit')]\n    ",
		edits[0].NewText,
	)
	assert.Contains(
		t,
		edits[1].NewText,
		"use Symfony\\Component\\Routing\\Attribute\\Route;",
	)
}

func TestRouteAttributeCodeActionReusesAliasAndAvoidsConflict(t *testing.T) {
	for _, fixture := range []struct {
		name       string
		importLine string
		prefix     string
		editCount  int
	}{
		{
			name: "existing alias",
			importLine: "use Symfony\\Component\\Routing\\Attribute\\" +
				"Route as RoutingRoute;",
			prefix:    "#[RoutingRoute(",
			editCount: 1,
		},
		{
			name:       "short name conflict",
			importLine: "use App\\Metadata\\Route;",
			prefix: "#[\\Symfony\\Component\\Routing\\Attribute\\" +
				"Route(",
			editCount: 1,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source := `<?php
namespace App\Controller;
` + fixture.importLine + `
class ProductController
{
    public function showAction() {}
}`
			request := routeAttributeCodeActionRequest(
				source,
				"showAction",
			)
			actions := routeAttributeCodeActionFixture(t).
				GetCodeActions(context.Background(), request)
			require.Len(t, actions, 1)
			edits := actions[0].Edit.Changes[request.TextDocument.URI]
			require.Len(t, edits, fixture.editCount)
			assert.True(t, strings.HasPrefix(
				edits[0].NewText,
				fixture.prefix,
			))
		})
	}
}

func TestGeneratedRoutePathAndName(t *testing.T) {
	for _, fixture := range []struct {
		class  string
		method string
		path   string
		name   string
	}{
		{
			class:  `App\Controller\TestController`,
			method: "indexAction",
			path:   "/test",
			name:   "app_test_index",
		},
		{
			class:  `App\Controller\ProductController`,
			method: "showAction",
			path:   "/product/show",
			name:   "app_product_show",
		},
		{
			class:  `App\Controller\Admin\UserController`,
			method: "editAction",
			path:   "/admin/user/edit",
			name:   "app_admin_user_edit",
		},
	} {
		assert.Equal(
			t,
			fixture.path,
			generatedRoutePath(fixture.class, fixture.method),
		)
		assert.Equal(
			t,
			fixture.name,
			generatedRouteName(fixture.class, fixture.method),
		)
	}
}

func routeAttributeCodeActionFixture(
	t *testing.T,
) *RouteAttributeCodeActionProvider {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/vendor/SymfonyRouteStubs.php",
		[]byte(`<?php
namespace Symfony\Component\Routing\Attribute;
class Route {}
namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {}
namespace Symfony\Component\HttpKernel\Attribute;
class AsController {}
`),
	)))
	return NewRouteAttributeCodeActionProvider(phpIndex)
}

func routeAttributeCodeActionRequest(
	source,
	methodName string,
) *lsp.CodeActionRequest {
	document := lsp.NewTextDocument(
		"file:///project/Controller.php",
		source,
		1,
	)
	offset := strings.Index(source, methodName)
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CodeActionParams{
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      int(line),
				Character: int(character),
			},
			End: protocol.Position{
				Line:      int(line),
				Character: int(character + uint32(len(methodName))),
			},
		},
	}
	params.TextDocument.URI = document.URI
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
