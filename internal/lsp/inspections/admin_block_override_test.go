package inspections

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminInspectionDeclaresAndReportsBlockOverrideProblems(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	inspection := NewAdmin(index)

	expected := map[lsp.DiagnosticID]struct{}{
		diagnostics.AdminBlockOverrideNestedCode:      {},
		diagnostics.AdminBlockOverrideConditionalCode: {},
		diagnostics.AdminBlockOverrideRepeatedCode:    {},
		diagnostics.AdminBlockParentConditionalCode:   {},
		diagnostics.AdminBlockParentRepeatedCode:      {},
	}
	for _, problem := range inspection.Definition().Problems {
		if _, found := expected[problem.ID]; !found {
			continue
		}
		assert.Equal(t, protocol.DiagnosticSeverityError, problem.DefaultSeverity)
		assert.False(t, problem.DisabledByDefault)
		delete(expected, problem.ID)
	}
	assert.Empty(t, expected)

	path := filepath.Join(
		root,
		"src/Resources/app/administration/src/component.vue",
	)
	document := lsp.NewTextDocument(
		uriutil.FileURI(path),
		`<template><sw-block v-if="enabled" extends="conditional" /></template>`,
		1,
	)
	collector := &problemCollector{}
	require.NoError(t, inspection.Inspect(context.Background(), document, collector))
	require.Len(t, collector.problems, 1)
	assert.Equal(
		t,
		diagnostics.AdminBlockOverrideConditionalCode,
		collector.problems[0].ID,
	)
	assert.Empty(t, collector.problems[0].Fixes)
}
