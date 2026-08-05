package codelens

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminComponentCodeLensesLinkDefinitionsTemplatesAndExtensions(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })

	adminRoot := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src",
	)
	componentDir := filepath.Join(adminRoot, "app/component/sw-card")
	require.NoError(t, os.MkdirAll(componentDir, 0o755))
	templatePath := filepath.Join(componentDir, "sw-card.html.twig")
	templateSource := `<div><slot name="actions"></slot></div>`
	require.NoError(t, os.WriteFile(
		templatePath, []byte(templateSource), 0o600,
	))
	basePath := filepath.Join(componentDir, "index.js")
	baseSource := `
import template from './sw-card.html.twig';
Component.register('sw-card', {
    template,
    props: { title: String },
});`
	extensionPath := filepath.Join(adminRoot, "extension/sw-card/index.js")
	extensionSource := `Component.override('sw-card', {
    props: { subtitle: String },
});`
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		basePath, []byte(baseSource),
	)))
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		extensionPath, []byte(extensionSource),
	)))
	provider := NewAdminComponentCodeLensProvider(adminIndex)
	baseLenses := relatedCodeLensesFor(t, provider, basePath, baseSource)
	assert.ElementsMatch(t, []string{
		"Open component extensions",
		"Open component template",
	}, relatedLensTitles(baseLenses))
	assert.Equal(t, []string{
		relatedTarget(templatePath, 1),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, baseLenses, "Open component template"),
	))
	assert.Equal(t, []string{
		relatedTarget(extensionPath, 1),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(t, baseLenses, "Open component extensions"),
	))

	extensionLenses := relatedCodeLensesFor(
		t, provider, extensionPath, extensionSource,
	)
	assert.Equal(t, []string{"Open base component"}, relatedLensTitles(extensionLenses))
	assert.Equal(t, []string{
		relatedTarget(basePath, 3),
	}, relatedLensTargets(t, extensionLenses[0]))

	templateLenses := relatedCodeLensesFor(
		t, provider, templatePath, templateSource,
	)
	assert.ElementsMatch(t, []string{
		"Open sw-card component definition",
		"Open component extensions",
	}, relatedLensTitles(templateLenses))
	assert.Equal(t, []string{
		relatedTarget(basePath, 3),
	}, relatedLensTargets(
		t,
		relatedLensByTitle(
			t, templateLenses, "Open sw-card component definition",
		),
	))
}
