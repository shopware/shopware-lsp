package inspections

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/require"
)

func TestAppDatasetInspectionDefinition(t *testing.T) {
	inspection := NewAppDataset(nil, nil)
	definition := inspection.Definition()
	require.Equal(t, "shopware.app_dataset", definition.ID)
	ids := map[string]bool{}
	for _, problem := range definition.Problems {
		ids[string(problem.ID)] = true
	}
	require.True(t, ids["app_dataset.permission-missing"])
	require.True(t, ids["app_dataset.subscribe-without-selectors"])
}

type appDatasetTestReporter struct {
	problems []lsp.Problem
}

func (r *appDatasetTestReporter) Report(problem lsp.Problem) error {
	r.problems = append(r.problems, problem)
	return nil
}

func TestAppDatasetInspectionBindsPermissionFix(t *testing.T) {
	inspection, ok := NewAppDataset(nil, nil).(*boundInspection)
	require.True(t, ok)
	bound := inspection.bind(
		lsp.DiagnosticID("app_dataset.permission-missing"),
		map[string]any{"entity": "product", "manifest": "/app/manifest.xml"},
	)
	require.Len(t, bound, 1)
	require.Equal(t, addAppReadPermissionFixID, bound[0].ID)
	require.Empty(t, inspection.bind(
		lsp.DiagnosticID("app_dataset.subscribe-without-selectors"),
		map[string]any{"dataset": "x"},
	))
	var reporter appDatasetTestReporter
	require.NoError(t, inspection.Inspect(context.Background(), nil, &reporter))
	require.Empty(t, reporter.problems)
}
