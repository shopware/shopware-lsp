package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShopwareStoreComposerAnalyzer(t *testing.T) {
	description := strings.Repeat("a", 150)
	document := lsp.NewTextDocument("file:///project/custom/plugins/Acme/composer.json", `{
    "type": "shopware-platform-plugin",
    "require": {"shopware/core": "~6.7"},
    "extra": {
        "label": {"de-DE": "Acme", "en-GB": "Acme"},
        "description": {"de-DE": "`+description+`", "en-GB": "short"},
        "manufacturerLink": {"de-DE": "https://example.com", "en-GB": "https://example.com"}
    }
}`, 1)
	problems, err := NewShopwareStoreComposerAnalyzer("/project").Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 2)
	require.Equal(t, lsp.DiagnosticID("shopware.store.description"), problems[0].ID)
	require.Equal(t, lsp.DiagnosticID("shopware.store.support-link"), problems[1].ID)
}

func TestShopwareStoreComposerAnalyzerSkipsLocalPathPackages(t *testing.T) {
	const pluginComposer = `{"name": "acme/%s", "type": "shopware-platform-plugin", "extra": {}}`
	tests := []struct {
		name         string
		repositories string
		require      string
		pluginDir    string
		skipped      bool
	}{
		{
			name:         "required package from path repository",
			repositories: `[{"type": "path", "url": "custom/static-plugins/*"}]`,
			require:      `{"acme/static": "*"}`,
			pluginDir:    "custom/static-plugins/AcmeStatic",
			skipped:      true,
		},
		{
			name:         "path repository without root requirement",
			repositories: `[{"type": "path", "url": "custom/plugins/*"}]`,
			require:      `{}`,
			pluginDir:    "custom/plugins/AcmeStore",
		},
		{
			name:         "required package outside path repositories",
			repositories: `[{"type": "path", "url": "custom/static-plugins/*"}]`,
			require:      `{"acme/store": "*"}`,
			pluginDir:    "custom/plugins/AcmeStore",
		},
		{
			name:         "brace glob and keyed repositories",
			repositories: `{"local": {"type": "path", "url": "packages/{first,second}/*/"}}`,
			require:      `{"acme/nested": "*"}`,
			pluginDir:    "packages/second/AcmeNested",
			skipped:      true,
		},
		{
			name:         "non-path repository",
			repositories: `[{"type": "vcs", "url": "custom/static-plugins/*"}]`,
			require:      `{"acme/static": "*"}`,
			pluginDir:    "custom/static-plugins/AcmeStatic",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, "composer.json"),
				`{"require": `+test.require+`, "repositories": `+test.repositories+`}`)
			packageName := strings.ToLower(filepath.Base(test.pluginDir)[len("Acme"):])
			path := filepath.Join(root, filepath.FromSlash(test.pluginDir), "composer.json")
			source := strings.Replace(pluginComposer, "%s", packageName, 1)
			writeTestFile(t, path, source)

			problems, err := NewShopwareStoreComposerAnalyzer(root).Analyze(
				context.Background(),
				lsp.NewTextDocument(uriutil.FileURI(path), source, 1),
			)
			require.NoError(t, err)
			if test.skipped {
				assert.Empty(t, problems)
			} else {
				assert.NotEmpty(t, problems)
			}
		})
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
