package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminBlockOverrideAnalyzerReportsUnsupportedStructures(t *testing.T) {
	source := `<template>
    <sw-block extends="outer">
        <div><sw-block extends="nested" /></div>
        <sw-block-parent v-if="showParent" />
        <template v-for="item in items"><sw-block-parent /></template>
    </sw-block>
    <sw-block v-if="enabled" extends="conditional" />
    <section v-for="item in items"><sw-block extends="repeated" /></section>
</template>`
	problems, err := NewAdminBlockOverrideAnalyzer().Analyze(
		context.Background(),
		adminBlockOverrideDocument("component.vue", source),
	)
	require.NoError(t, err)
	require.Len(t, problems, 5)

	expected := map[lsp.DiagnosticID]string{
		AdminBlockOverrideNestedCode:      "sw-block",
		AdminBlockOverrideConditionalCode: "sw-block",
		AdminBlockOverrideRepeatedCode:    "sw-block",
		AdminBlockParentConditionalCode:   "sw-block-parent",
		AdminBlockParentRepeatedCode:      "sw-block-parent",
	}
	for _, problem := range problems {
		want, found := expected[problem.ID]
		require.True(t, found, "unexpected problem %s", problem.ID)
		assert.Equal(t, want, source[problem.Range.Start:problem.Range.End])
		assert.NotEmpty(t, problem.Message)
		delete(expected, problem.ID)
	}
	assert.Empty(t, expected)
}

func TestAdminBlockOverrideAnalyzerAllowsStaticSiblingDeclarations(t *testing.T) {
	source := `<template>
    <sw-block extends="first">
        <div><section><sw-block-parent /></section></div>
    </sw-block>
    <div v-show="visible"><sw-block extends="second" /></div>
    <sw-block name="ordinary"><sw-block-parent /></sw-block>
</template>`
	problems, err := NewAdminBlockOverrideAnalyzer().Analyze(
		context.Background(),
		adminBlockOverrideDocument("component.vue", source),
	)
	require.NoError(t, err)
	assert.Empty(t, problems)
}

func TestAdminBlockOverrideAnalyzerReportsAncestorAndTwigControlFlow(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		source string
		ids    []lsp.DiagnosticID
	}{
		{
			name: "Vue ancestor controls",
			file: "component.vue",
			source: `<template>
    <section v-if="enabled"><div><sw-block extends="conditional" /></div></section>
    <template v-for="item in items"><div><sw-block-parent /></div></template>
</template>`,
			ids: []lsp.DiagnosticID{
				AdminBlockOverrideConditionalCode,
				AdminBlockParentRepeatedCode,
			},
		},
		{
			name: "Twig control flow",
			file: "component.html.twig",
			source: `{% if enabled %}<sw-block extends="conditional" />{% endif %}
{% for item in items %}<sw-block-parent />{% endfor %}`,
			ids: []lsp.DiagnosticID{
				AdminBlockOverrideConditionalCode,
				AdminBlockParentRepeatedCode,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			problems, err := NewAdminBlockOverrideAnalyzer().Analyze(
				context.Background(),
				adminBlockOverrideDocument(test.file, test.source),
			)
			require.NoError(t, err)
			require.Len(t, problems, len(test.ids))
			for index, id := range test.ids {
				assert.Equal(t, id, problems[index].ID)
			}
		})
	}
}

func TestAdminBlockOverrideAnalyzerReportsVueControlDirectives(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		id   lsp.DiagnosticID
	}{
		{
			name: "override v-if",
			tag:  `<sw-block v-if="enabled" extends="example" />`,
			id:   AdminBlockOverrideConditionalCode,
		},
		{
			name: "override v-else-if",
			tag:  `<sw-block v-else-if="enabled" extends="example" />`,
			id:   AdminBlockOverrideConditionalCode,
		},
		{
			name: "override v-else",
			tag:  `<sw-block v-else extends="example" />`,
			id:   AdminBlockOverrideConditionalCode,
		},
		{
			name: "override v-for",
			tag:  `<sw-block v-for="item in items" extends="example" />`,
			id:   AdminBlockOverrideRepeatedCode,
		},
		{
			name: "parent v-if",
			tag:  `<sw-block-parent v-if="enabled" />`,
			id:   AdminBlockParentConditionalCode,
		},
		{
			name: "parent v-else-if",
			tag:  `<sw-block-parent v-else-if="enabled" />`,
			id:   AdminBlockParentConditionalCode,
		},
		{
			name: "parent v-else",
			tag:  `<sw-block-parent v-else />`,
			id:   AdminBlockParentConditionalCode,
		},
		{
			name: "parent v-for",
			tag:  `<sw-block-parent v-for="item in items" />`,
			id:   AdminBlockParentRepeatedCode,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := "<template>" + test.tag + "</template>"
			problems, err := NewAdminBlockOverrideAnalyzer().Analyze(
				context.Background(),
				adminBlockOverrideDocument("component.vue", source),
			)
			require.NoError(t, err)
			require.Len(t, problems, 1)
			assert.Equal(t, test.id, problems[0].ID)
		})
	}
}

func TestAdminBlockOverrideAnalyzerHandlesIncompleteNestedDeclaration(t *testing.T) {
	source := `<template><sw-block extends="outer"><div><sw-block extends="inner"`
	document := adminBlockOverrideDocument("component.vue", source)
	require.NotEmpty(t, document.ParseErrors)
	problems, err := NewAdminBlockOverrideAnalyzer().Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, AdminBlockOverrideNestedCode, problems[0].ID)
}

func TestAdminBlockOverrideAnalyzerIgnoresFilesOutsideAdministration(t *testing.T) {
	document := lsp.NewTextDocument(
		"file:///project/templates/component.vue",
		`<template><sw-block v-if="enabled" extends="conditional" /></template>`,
		1,
	)
	problems, err := NewAdminBlockOverrideAnalyzer().Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	assert.Empty(t, problems)
}

func adminBlockOverrideDocument(name, source string) *lsp.TextDocument {
	return lsp.NewTextDocument(
		uriutil.FileURI(
			"/project/src/Resources/app/administration/src/"+name,
		),
		source,
		1,
	)
}
