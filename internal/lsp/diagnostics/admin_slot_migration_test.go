package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/require"
)

func TestAdminSlotMigrationAnalyzerBuildsCombinedReplacement(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/Resources/app/administration/component.html.twig",
		`<template slot="actions" slot-scope="{ item }"><button/></template>`,
		1,
	)
	problems, err := NewAdminSlotMigrationAnalyzer().Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	payload := problems[0].Payload.(map[string]any)
	require.Equal(t, `<template #actions="{ item }">`, payload["replacement"])
}

func TestAdminSlotMigrationAnalyzerOnlyScansVueTemplateSection(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/Resources/app/administration/component.vue",
		`<script>
const example = '<template slot="ignored">';
</script>
<template>
    <template slot="actions" slot-scope="{ item }"><button/></template>
</template>`,
		1,
	)
	problems, err := NewAdminSlotMigrationAnalyzer().Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0].Payload.(map[string]any)["replacement"], "#actions")
}
