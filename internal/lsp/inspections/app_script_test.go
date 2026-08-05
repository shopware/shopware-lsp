package inspections

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestAppReadPermissionFixUpdatesManifestStructurally(t *testing.T) {
	for _, test := range []struct {
		name     string
		manifest string
		expect   string
	}{
		{
			name: "creates permissions",
			manifest: `<?xml version="1.0"?>
<manifest>
    <meta/>
</manifest>
`,
			expect: "    <permissions>\n        <read>product</read>\n    </permissions>\n",
		},
		{
			name: "appends read permission",
			manifest: `<?xml version="1.0"?>
<manifest>
    <permissions>
        <read>order</read>
    </permissions>
</manifest>
`,
			expect: "        <read>order</read>\n        <read>product</read>\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			scriptSource := "{{ services.repository.search('product') }}\n"
			script := lsp.NewTextDocument(
				"file:///project/Resources/scripts/hook/script.twig",
				scriptSource,
				1,
			)
			start := uint32(strings.Index(scriptSource, "product"))
			rng := cst.TextRange{Start: start, End: start + uint32(len("product"))}
			problem := lsp.Problem{
				ID:      "app_script.permission-missing",
				Range:   rng,
				Element: script.SyntaxTree.Root.DescendantForRange(rng),
			}
			manifestPath := filepath.Join(t.TempDir(), "manifest.xml")
			manifestURI := uriutil.FileURI(manifestPath)
			manifest := lsp.NewTextDocument(manifestURI, test.manifest, 3)
			version := manifest.Version
			bound := lsp.BindFix(addAppReadPermissionFixID, appPermissionPayload{
				Entity:   "product",
				Manifest: manifestPath,
			})
			plan, err := (appReadPermissionFix{}).Build(
				context.Background(),
				fixContext(t, script, problem, bound, staticDocumentResolver{
					manifestURI: {Document: manifest, Version: &version},
				}),
			)
			require.NoError(t, err)
			require.Len(t, plan.Documents, 1)
			updated, err := plan.Documents[0].Apply()
			require.NoError(t, err)
			require.Contains(t, updated, test.expect)
			require.Empty(t, lsp.NewTextDocument(manifestURI, updated, 4).ParseErrors)
		})
	}
}
