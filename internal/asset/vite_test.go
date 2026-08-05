package asset

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseViteConfigDirectVariablesAndSpreads(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   map[string]string
	}{
		{
			name: "direct",
			source: `export default defineConfig({
  build: {
    rollupOptions: {
      input: {
        app: './assets/app.js',
        'admin/main': './assets/admin.ts'
      }
    }
  }
});`,
			want: map[string]string{
				"app":        "assets/app.js",
				"admin/main": "assets/admin.ts",
			},
		},
		{
			name: "variable",
			source: `const entries = {
  app: './assets/app.js',
  admin: './assets/admin.js'
};
export default defineConfig({
  build: {rollupOptions: {input: entries}}
});`,
			want: map[string]string{
				"app":   "assets/app.js",
				"admin": "assets/admin.js",
			},
		},
		{
			name: "spreads",
			source: `const legacyEntries = {
  'global/main': './assets/main.js'
};
const vueEntries = {
  'vue/app': './assets/vue/app.ts'
};
export default defineConfig({
  build: {rollupOptions: {input: {
    ...legacyEntries,
    ...vueEntries,
    extra: './assets/extra.js'
  }}}
});`,
			want: map[string]string{
				"global/main": "assets/main.js",
				"vue/app":     "assets/vue/app.ts",
				"extra":       "assets/extra.js",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := "/project/vite.config.ts"
			parsed := indexer.NewParsedFile(path, []byte(test.source))
			resources := parseViteConfig(path, parsed.SyntaxTree())
			actual := make(map[string]string)
			for _, resource := range resources {
				require.Equal(t, ViteEntry, resource.Kind)
				actual[resource.Name] = filepath.ToSlash(
					stringsTrimProject(resource.Target),
				)
				assert.Equal(
					t,
					resource.Name,
					test.source[resource.Range.Start:resource.Range.End],
				)
			}
			assert.Equal(t, test.want, actual)
		})
	}
}

func TestIndexPersistsViteEntriesAndTwigUsages(t *testing.T) {
	root := t.TempDir()
	cache := t.TempDir()
	configPath := filepath.Join(root, "vite.config.js")
	templatePath := filepath.Join(root, "templates", "base.html.twig")
	config := `export default defineConfig({
  build: {rollupOptions: {input: {app: './assets/app.js'}}}
});`
	template := `{{ vite_entry_script_tags('app') }}`
	index, err := NewIndex(root, cache)
	require.NoError(t, err)
	require.NoError(t, index.Index(indexer.NewParsedFile(
		configPath,
		[]byte(config),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		templatePath,
		[]byte(template),
	)))
	names, err := index.ViteEntryNames()
	require.NoError(t, err)
	assert.Equal(t, []string{"app"}, names)
	usages, err := index.Usages("app", ViteEntryReference)
	require.NoError(t, err)
	require.Len(t, usages, 1)
	require.NoError(t, index.Close())

	restored, err := NewIndex(root, cache)
	require.NoError(t, err)
	names, err = restored.ViteEntryNames()
	require.NoError(t, err)
	assert.Equal(t, []string{"app"}, names)
	require.NoError(t, restored.Index(indexer.NewParsedFile(
		configPath,
		[]byte("export default {};"),
	)))
	names, err = restored.ViteEntryNames()
	require.NoError(t, err)
	assert.Empty(t, names)
	require.NoError(t, restored.Close())
}

func stringsTrimProject(path string) string {
	relative, err := filepath.Rel("/project", path)
	if err != nil {
		return path
	}
	return relative
}
