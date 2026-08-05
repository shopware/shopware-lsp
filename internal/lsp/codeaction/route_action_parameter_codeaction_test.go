package codeaction

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteActionParameterCodeActionRecognizesRoutes(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		source string
		method string
	}{
		{
			name: "method attribute",
			source: `<?php
use Symfony\Component\Routing\Attribute\Route;
class TestController
{
    #[Route('/test')]
    public function index(): void {}
}`,
			method: "index",
		},
		{
			name: "method annotation",
			source: `<?php
use Symfony\Component\Routing\Annotation\Route as RoutingRoute;
class TestController
{
    /** @RoutingRoute("/test") */
    public function index(): void {}
}`,
			method: "index",
		},
		{
			name: "invokable class attribute",
			source: `<?php
use Symfony\Component\Routing\Attribute\Route;
#[Route('/test')]
class TestController
{
    public function __invoke(): void {}
}`,
			method: "__invoke",
		},
		{
			name: "invokable class annotation",
			source: `<?php
use Sensio\Bundle\FrameworkExtraBundle\Configuration\Route;
/** @Route("/test") */
class TestController
{
    public function __invoke(): void {}
}`,
			method: "__invoke",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			request := routeActionParameterRequest(
				fixture.source,
				fixture.method,
			)
			actions := NewRouteActionParameterCodeActionProvider().
				GetCodeActions(context.Background(), request)

			require.Len(t, actions, 2)
			assert.Equal(
				t,
				"Symfony: Add Request parameter to route action",
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

func TestRouteActionParameterCodeActionRejectsInvalidTargets(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		source string
		method string
	}{
		{
			name: "private route",
			source: `<?php
use Symfony\Component\Routing\Attribute\Route;
class TestController
{
    #[Route('/test')]
    private function index(): void {}
}`,
			method: "index",
		},
		{
			name: "method without route",
			source: `<?php
class TestController
{
    public function index(): void {}
}`,
			method: "index",
		},
		{
			name: "non invoke with class route",
			source: `<?php
use Symfony\Component\Routing\Attribute\Route;
#[Route('/test')]
class TestController
{
    public function index(): void {}
}`,
			method: "index",
		},
		{
			name: "all types exist",
			source: `<?php
use Symfony\Component\Routing\Attribute\Route;
use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\Security\Core\User\UserInterface;
class TestController
{
    #[Route('/test')]
    public function index(
        Request $request,
        UserInterface $user,
    ): void {}
}`,
			method: "index",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			actions := NewRouteActionParameterCodeActionProvider().
				GetCodeActions(
					context.Background(),
					routeActionParameterRequest(
						fixture.source,
						fixture.method,
					),
				)
			assert.Empty(t, actions)
		})
	}
}

func TestRouteActionParameterCodeActionFiltersAndAddsParameters(t *testing.T) {
	source := `<?php
namespace App\Controller;
use Symfony\Component\Routing\Attribute\Route;
use Symfony\Component\HttpFoundation\Request as HttpRequest;
class TestController
{
    #[Route('/test')]
    public function index(
        HttpRequest $request,
        string $slug,
        ?string $format = null,
    ): void {}
}`
	request := routeActionParameterRequest(source, "index")
	existingTypes := phpExistingParameterTypes(
		phpquery.MethodAt(request.Node),
		php.NewNameResolver(request.Root),
	)
	require.Contains(
		t,
		existingTypes,
		strings.ToLower(
			"Symfony\\Component\\HttpFoundation\\Request",
		),
	)
	actions := NewRouteActionParameterCodeActionProvider().
		GetCodeActions(context.Background(), request)

	require.Len(t, actions, 1)
	assert.Equal(
		t,
		"Symfony: Add UserInterface parameter to route action",
		actions[0].Title,
	)
	edits := actions[0].Edit.Changes[request.TextDocument.URI]
	require.Len(t, edits, 2)
	assert.Equal(
		t,
		"UserInterface $user,\n        ",
		edits[0].NewText,
	)
	assert.Contains(
		t,
		edits[1].NewText,
		"use Symfony\\Component\\Security\\Core\\User\\UserInterface;",
	)
}

func TestPHPDocHasResolvedRouteAnnotation(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/Controller.php",
		`<?php
namespace App;
use Symfony\Component\Routing\Annotation\Route as RoutingRoute;
class Controller
{
    /**
     * @param string $name
     * @RoutingRoute("/test")
     */
    public function index(string $name): void {}
}`,
		1,
	)
	resolver := php.NewNameResolver(document.SyntaxTree.Root)
	methodOffset := strings.Index(document.Source, "function index")
	method := phpquery.MethodAt(
		document.SyntaxTree.Root.NodeAtOffset(uint32(methodOffset)),
	)

	assert.True(t, hasPHPRouteMarker(method, resolver))
}

func routeActionParameterRequest(
	source,
	methodName string,
) *lsp.CodeActionRequest {
	return commandInvokeParameterRequest(source, methodName)
}
