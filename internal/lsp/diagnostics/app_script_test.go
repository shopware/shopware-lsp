package diagnostics

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/appscript"
	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestAppScriptAnalyzerChecksHookServicesAndManifestPermissions(t *testing.T) {
	cache := t.TempDir()
	hooks, err := appscript.NewIndex(cache)
	require.NoError(t, err)
	defer func() { _ = hooks.Close() }()
	extensions, err := extension.NewExtensionIndexer(cache)
	require.NoError(t, err)
	defer func() { _ = extensions.Close() }()

	for path, source := range map[string]string{
		"/core/RepositoryFacadeHookFactory.php": `<?php
class RepositoryFacadeHookFactory extends HookServiceFactory {
    public function getName(): string { return 'repository'; }
}`,
		"/core/ProductHook.php": `<?php
class ProductHook extends Hook {
    public const HOOK_NAME = 'product-written';
    public static function getServiceIds(): array { return [RepositoryFacadeHookFactory::class]; }
    public function getName(): string { return self::HOOK_NAME; }
}`,
	} {
		require.NoError(t, hooks.Index(indexer.NewParsedFile(path, []byte(source))))
	}

	appRoot := filepath.Join(cache, "AcmeApp")
	manifestPath := filepath.Join(appRoot, "manifest.xml")
	manifest := `<manifest>
    <meta><name>AcmeApp</name></meta>
    <permissions><read>product</read></permissions>
</manifest>`
	require.NoError(t, extensions.Index(
		indexer.NewParsedFile(manifestPath, []byte(manifest)),
	))

	scriptPath := filepath.Join(
		appRoot,
		"Resources",
		"scripts",
		"product-written",
		"script.twig",
	)
	source := `{% set orders = services.repository.search('order', hook.criteria) %}
{% do services.cart.add('x') %}`
	document := lsp.NewTextDocument(uriutil.FileURI(scriptPath), source, 1)
	problems, err := NewAppScriptAnalyzer(hooks, extensions).Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 2)
	require.Equal(t, lsp.DiagnosticID("app_script.service-unavailable"), problems[0].ID)
	require.Equal(t, lsp.DiagnosticID("app_script.permission-missing"), problems[1].ID)
}
