package inspections

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/require"
)

func TestShopwareRouteAnnotationMigrationQuickFixes(t *testing.T) {
	phpIndex := migrationInspectionPHPIndex(t)
	for _, test := range []struct {
		name     string
		docblock string
		expected string
		removed  string
	}{
		{
			name: "captcha",
			docblock: `    /**
     * @Route("/contact", defaults={"XmlHttpRequest"=true})
     * @Captcha
     */`,
			expected: `@Route("/contact", defaults={"XmlHttpRequest"=true, "_captcha"=true})`,
			removed:  "@Captcha",
		},
		{
			name: "login required",
			docblock: `    /**
     * @LoginRequired
     * @Route("/account")
     */`,
			expected: `@Route("/account", defaults={"_loginRequired"=true})`,
			removed:  "@LoginRequired",
		},
		{
			name: "route scope without route",
			docblock: `    /**
     * @RouteScope(scopes={"storefront", "api"})
     */`,
			expected: `@Route(defaults={"_routeScope"={"storefront", "api"}})`,
			removed:  "@RouteScope",
		},
		{
			name: "route scope with route",
			docblock: `    /**
     * @Route("/storefront", methods={"GET"})
     * @RouteScope(scopes={"storefront"})
     */`,
			expected: `@Route("/storefront", methods={"GET"}, defaults={"_routeScope"={"storefront"}})`,
			removed:  "@RouteScope",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := `<?php
class Controller
{
` + test.docblock + `
    public function action(): void {}
}
`
			document := lsp.NewTextDocument(
				"file:///project/src/Controller.php",
				source,
				1,
			)
			updated := applyOnlyMigrationFix(
				t,
				NewShopwareMigration(phpIndex, migrationInspectionVersion(6, 5)),
				document,
				routeAnnotationMigrationFixID,
			)
			require.Contains(t, updated, test.expected)
			require.NotContains(t, updated, test.removed)
		})
	}
}
