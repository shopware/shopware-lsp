package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShopwareRouteAnnotationMigrations(t *testing.T) {
	phpIndex := migrationTestPHPIndex(t)
	document := lsp.NewTextDocument(
		"file:///project/src/Controller.php",
		`<?php
/**
 * @RouteScope(scopes={"storefront", "api"})
 */
class Controller
{
    /**
     * @Route("/contact", defaults={"XmlHttpRequest"=true})
     * @Captcha
     */
    public function contact(): void {}

    /**
     * @LoginRequired
     * @Route("/account")
     */
    public function account(): void {}

    /**
     * @Route("/complete", defaults={"_captcha"=true})
     * @Captcha
     */
    public function complete(): void {}
}
`,
		1,
	)
	problems, err := NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 5, 0),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, problems, 3)

	assert.Equal(t, routeAnnotationDefaultsCode, problems[0].ID)
	routeScope := problems[0].Payload.(ShopwareMigrationPayload)
	assert.Equal(t, "route-scope", routeScope.Kind)
	assert.Equal(t, `@Route(defaults={"_routeScope"={"storefront", "api"}})`, routeScope.Replacement)

	captcha := problems[1].Payload.(ShopwareMigrationPayload)
	assert.Equal(t, "captcha", captcha.Kind)
	assert.Equal(t, `@Route("/contact", defaults={"XmlHttpRequest"=true, "_captcha"=true})`, captcha.Replacement)

	login := problems[2].Payload.(ShopwareMigrationPayload)
	assert.Equal(t, "login-required", login.Kind)
	assert.Equal(t, `@Route("/account", defaults={"_loginRequired"=true})`, login.Replacement)

	problems, err = NewShopwareMigrationAnalyzer(
		phpIndex,
		resolvedShopwareMigrationVersion(6, 4, 20),
	).Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, problems)
}

func TestDoctrineRouteDefaultRewriteHandlesNestedValues(t *testing.T) {
	updated, changed, safe := addDoctrineRouteDefault(
		`@Route("/demo", methods={"GET", "POST"}, defaults={"nested"={"a", "b"}})`,
		"_loginRequired",
		"true",
	)
	assert.True(t, safe)
	assert.True(t, changed)
	assert.Equal(
		t,
		`@Route("/demo", methods={"GET", "POST"}, defaults={"nested"={"a", "b"}, "_loginRequired"=true})`,
		updated,
	)
}
