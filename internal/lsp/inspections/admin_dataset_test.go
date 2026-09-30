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

func TestDatasetReadPermissionFixAddsMissingEntities(t *testing.T) {
	source := "data.subscribe('order', (order) => order, { selectors: ['language.name'] });\n"
	script := lsp.NewTextDocument(
		"file:///project/custom/apps/Demo/Resources/app/administration/src/main.js",
		source,
		1,
	)
	start := uint32(strings.Index(source, "order"))
	rng := cst.TextRange{Start: start, End: start + uint32(len("order"))}
	problem := lsp.Problem{
		ID:      "admin.dataset.permission-missing",
		Range:   rng,
		Element: script.SyntaxTree.Root.DescendantForRange(rng),
	}
	manifestPath := filepath.Join(t.TempDir(), "manifest.xml")
	manifestURI := uriutil.FileURI(manifestPath)
	manifestSource := `<?xml version="1.0"?>
<manifest>
    <permissions>
        <crud>order</crud>
    </permissions>
</manifest>
`
	manifest := lsp.NewTextDocument(manifestURI, manifestSource, 3)
	version := manifest.Version
	bound := lsp.BindFix(addDatasetReadPermissionsFixID, datasetPermissionPayload{
		Entities: []string{"language", "order"},
		Manifest: manifestPath,
	})
	plan, err := (datasetReadPermissionFix{}).Build(
		context.Background(),
		fixContext(t, script, problem, bound, staticDocumentResolver{
			manifestURI: {Document: manifest, Version: &version},
		}),
	)
	require.NoError(t, err)
	updated, err := plan.Documents[0].Apply()
	require.NoError(t, err)
	require.Contains(t, updated, "<crud>order</crud>\n        <read>language</read>\n")
	require.NotContains(t, updated, "<read>order</read>")
	require.Empty(t, lsp.NewTextDocument(manifestURI, updated, 4).ParseErrors)
}

func TestDatasetReadPermissionFixCreatesPermissions(t *testing.T) {
	source := "data.subscribe('order');\n"
	script := lsp.NewTextDocument(
		"file:///project/custom/apps/Demo/Resources/app/administration/src/main.js",
		source,
		1,
	)
	start := uint32(strings.Index(source, "order"))
	rng := cst.TextRange{Start: start, End: start + uint32(len("order"))}
	problem := lsp.Problem{
		ID:      "admin.dataset.permission-missing",
		Range:   rng,
		Element: script.SyntaxTree.Root.DescendantForRange(rng),
	}
	manifestPath := filepath.Join(t.TempDir(), "manifest.xml")
	manifestURI := uriutil.FileURI(manifestPath)
	manifest := lsp.NewTextDocument(manifestURI, "<manifest>\n    <meta/>\n</manifest>\n", 3)
	version := manifest.Version
	bound := lsp.BindFix(addDatasetReadPermissionsFixID, datasetPermissionPayload{
		Entities: []string{"language", "order"},
		Manifest: manifestPath,
	})
	plan, err := (datasetReadPermissionFix{}).Build(
		context.Background(),
		fixContext(t, script, problem, bound, staticDocumentResolver{
			manifestURI: {Document: manifest, Version: &version},
		}),
	)
	require.NoError(t, err)
	updated, err := plan.Documents[0].Apply()
	require.NoError(t, err)
	require.Contains(t, updated, "<read>language</read>\n        <read>order</read>")
	require.Empty(t, lsp.NewTextDocument(manifestURI, updated, 4).ParseErrors)
}
