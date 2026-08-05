package phprewrite

import (
	"testing"

	"github.com/stretchr/testify/require"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
)

func TestPHPDocAnnotationReplaceAndRemove(t *testing.T) {
	t.Parallel()
	source := `<?php
final class Controller
{
    /**
     * Description.
     * @Route(
     *     "/demo",
     *     defaults={"value"=")"}
     * )
     * @Captcha
     */
    public function demo(): void {}
}`
	editor, root := testEditor(t, source)
	method := phpquery.Methods(phpquery.Classes(root)[0])[0]
	replaced, err := editor.ReplacePHPDocAnnotation(
		method,
		"Route",
		`@Route("/demo", defaults={"_captcha"=true})`,
	)
	require.NoError(t, err)
	require.True(t, replaced)
	removed, err := editor.RemovePHPDocAnnotation(method, "Captcha")
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, `<?php
final class Controller
{
    /**
     * Description.
     * @Route("/demo", defaults={"_captcha"=true})
     */
    public function demo(): void {}
}`, applyTestEditor(t, source, editor))
}

func TestPHPDocAnnotationSupportsQualifiedNames(t *testing.T) {
	t.Parallel()
	source := `<?php
/** @not-an-annotation */
final class Controller
{
    /**
     * @Shopware\Storefront\Framework\Routing\Annotation\RouteScope(scopes={"storefront"})
     */
    public function demo(): void {}
}`
	editor, root := testEditor(t, source)
	method := phpquery.Methods(phpquery.Classes(root)[0])[0]
	removed, err := editor.RemovePHPDocAnnotation(
		method,
		`Shopware\Storefront\Framework\Routing\Annotation\RouteScope`,
	)
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, `<?php
/** @not-an-annotation */
final class Controller
{
    /**
     */
    public function demo(): void {}
}`, applyTestEditor(t, source, editor))
}

func TestPHPDocAnnotationRejectsMalformedAndInlineAnnotations(t *testing.T) {
	t.Parallel()
	tests := []string{
		`<?php /**
 * @Route(defaults={"broken"=true}
 */
function demo(): void {}`,
		`<?php /**
 * @Route("/demo") trailing text
 */
function demo(): void {}`,
	}
	for _, source := range tests {
		editor, root := testEditor(t, source)
		function := phpquery.Functions(root)[0]
		replaced, err := editor.ReplacePHPDocAnnotation(function, "Route", `@Route("/new")`)
		require.NoError(t, err)
		require.False(t, replaced)
		require.Equal(t, source, applyTestEditor(t, source, editor))
	}
}
