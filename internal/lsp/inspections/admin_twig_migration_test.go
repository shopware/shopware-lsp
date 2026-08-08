package inspections

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/shopware"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestAdminTwigMigrationInspectionBuildsVersionedFix(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-text-field", FilePath: filepath.Join(root, "sw-text-field.ts"),
		Deprecated: "Use mt-text-field instead.",
	}))
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root, "src/Resources/app/administration/src/component.html.twig",
		)),
		`<sw-text-field :value="computedName"><template #label>{{ $t('name') }}</template></sw-text-field>`,
		1,
	)
	inspection := NewAdmin(index, knownAdminMigrationVersion(6, 7, 0))
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(context.Background(), document, collector))

	problem := problemWithID(t, collector.problems, "admin.twig.migration.text-field")
	for _, candidate := range collector.problems {
		require.NotEqual(t, lsp.DiagnosticID("admin.component.deprecated"), candidate.ID)
	}
	require.Len(t, problem.Fixes, 1)
	require.Equal(t, migrateAdminTwigComponentFixID, problem.Fixes[0].ID)
	fix := quickFixWithID(t, inspection, problem.Fixes[0].ID)
	plan, err := fix.Build(
		context.Background(),
		fixContext(t, document, problem, problem.Fixes[0], nil),
	)
	require.NoError(t, err)
	require.Len(t, plan.Documents, 1)
	updated, err := plan.Documents[0].Apply()
	require.NoError(t, err)
	require.Equal(
		t,
		`<mt-text-field :model-value="computedName" :label="$t('name')"></mt-text-field>`,
		updated,
	)
	require.Empty(t, lsp.NewTextDocument(document.URI, updated, 2).ParseErrors)
}

func TestAdminTwigMigrationInspectionReportsUnsafeWithoutFix(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root, "src/Resources/app/administration/src/component.html.twig",
		)),
		`<sw-card aiBadge>Content</sw-card>`,
		1,
	)
	inspection := NewAdmin(index, knownAdminMigrationVersion(6, 7, 0))
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(context.Background(), document, collector))
	problem := problemWithID(t, collector.problems, "admin.twig.migration.card")
	require.Empty(t, problem.Fixes)
}

func TestAdminTwigMigrationInspectionOwnsMissingLegacyComponent(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "unrelated-component", FilePath: filepath.Join(root, "component.ts"),
	}))
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root, "src/Resources/app/administration/src/component.html.twig",
		)),
		`<sw-loader />`,
		1,
	)
	inspection := NewAdmin(index, knownAdminMigrationVersion(6, 8, 0))
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(context.Background(), document, collector))
	problemWithID(t, collector.problems, "admin.twig.migration.loader")
	for _, problem := range collector.problems {
		require.NotEqual(t, lsp.DiagnosticID("admin.component.not-found"), problem.ID)
	}
}

func TestAdminTwigMigrationInspectionRequiresKnownTarget(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root, "src/Resources/app/administration/src/component.html.twig",
		)),
		`<sw-loader />`,
		1,
	)
	for _, version := range []shopware.ResolvedVersion{
		{}, knownAdminMigrationVersion(6, 6, 9),
	} {
		inspection := NewAdmin(index, version)
		collector := &problemCollector{}
		require.NoError(t, inspection.Inspect(context.Background(), document, collector))
		for _, problem := range collector.problems {
			require.NotEqual(t, lsp.DiagnosticID("admin.twig.migration.loader"), problem.ID)
		}
	}
}

func problemWithID(t *testing.T, problems []lsp.Problem, id lsp.DiagnosticID) lsp.Problem {
	t.Helper()
	for _, problem := range problems {
		if problem.ID == id {
			return problem
		}
	}
	require.FailNow(t, "problem not found", string(id))
	return lsp.Problem{}
}

func knownAdminMigrationVersion(major, minor, patch int) shopware.ResolvedVersion {
	return shopware.ResolvedVersion{
		Known: true,
		Version: project.Version{
			Major: major, Minor: minor, Patch: patch,
		},
	}
}
