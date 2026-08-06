package inspections

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestAdminI18nTCInspectionBuildsLosslessCalleeRewrite(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(root, "Resources/app/administration/src")
	inspection := NewAdmin(adminIndex)

	for _, test := range []struct {
		name     string
		file     string
		source   string
		expected string
	}{
		{
			name:     "JavaScript member call keeps arguments",
			file:     "component.js",
			source:   `this.$tc('translation.key', count);`,
			expected: `this.$t('translation.key', count);`,
		},
		{
			name:     "Twig call keeps formatting",
			file:     "component.html.twig",
			source:   `{{ $tc( 'translation.key' ) }}`,
			expected: `{{ $t( 'translation.key' ) }}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(adminRoot, test.file)),
				test.source,
				1,
			)
			collector := &problemCollector{}
			require.NoError(t, inspection.Inspect(
				context.Background(), document, collector,
			))
			var problem *lsp.Problem
			for index := range collector.problems {
				if collector.problems[index].ID == "admin.vue-i18n.tc-deprecated" {
					problem = &collector.problems[index]
					break
				}
			}
			require.NotNil(t, problem)
			require.Len(t, problem.Fixes, 1)
			require.Equal(t, migrateAdminI18nTCFixID, problem.Fixes[0].ID)

			fix := quickFixWithID(t, inspection, problem.Fixes[0].ID)
			plan, buildErr := fix.Build(
				context.Background(),
				fixContext(t, document, *problem, problem.Fixes[0], nil),
			)
			require.NoError(t, buildErr)
			require.Len(t, plan.Documents, 1)
			updated, applyErr := plan.Documents[0].Apply()
			require.NoError(t, applyErr)
			require.Equal(t, test.expected, updated)
			require.Empty(t, lsp.NewTextDocument(document.URI, updated, 2).ParseErrors)
		})
	}
}
