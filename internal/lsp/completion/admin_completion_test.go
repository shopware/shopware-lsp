package completion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseJS(t *testing.T, code string) *jssyntax.Node {
	t.Helper()
	return javascriptparser.Parse(code).Tree.Root
}

func findNodeAtPosition(root *jssyntax.Node, line, col uint) *jssyntax.Node {
	offset := jssyntax.NewLineIndex(root.Text()).Offset(uint32(line), uint32(col))
	return root.NodeAtOffset(offset)
}

func TestIsInExtendParentArgument(t *testing.T) {
	provider := &AdminCompletionProvider{}

	tests := []struct {
		name     string
		code     string
		line     uint
		col      uint
		expected bool
	}{
		{
			name:     "second argument in Component.extend",
			code:     `Component.extend('my-component', 'sw-base', () => import('./index'));`,
			line:     0,
			col:      35, // Inside 'sw-base'
			expected: true,
		},
		{
			name:     "second argument in Shopware.Component.extend",
			code:     `Shopware.Component.extend('my-component', 'sw-base', () => import('./index'));`,
			line:     0,
			col:      45, // Inside 'sw-base'
			expected: true,
		},
		{
			name:     "first argument should not match",
			code:     `Component.extend('my-component', 'sw-base', () => import('./index'));`,
			line:     0,
			col:      20, // Inside 'my-component'
			expected: false,
		},
		{
			name:     "Component.register should not match",
			code:     `Component.register('my-component', () => import('./index'));`,
			line:     0,
			col:      22, // Inside 'my-component'
			expected: false,
		},
		{
			name: "second argument with destructured Component",
			code: `const { Component } = Shopware;
Component.extend('foo', 'bar', {});`,
			line:     1,
			col:      26, // Inside 'bar'
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := parseJS(t, tt.code)
			node := findNodeAtPosition(root, tt.line, tt.col)
			if node == nil {
				t.Fatalf("Could not find node at position %d:%d", tt.line, tt.col)
			}

			result := provider.isInExtendParentArgument(node)
			assert.Equal(t, tt.expected, result, "Node kind: %s, text: %s", node.Kind(), node.Text())
		})
	}
}

func TestIsSecondStringArgument(t *testing.T) {
	provider := &AdminCompletionProvider{}

	code := `Component.extend('first', 'second', () => {});`

	root := parseJS(t, code)

	// Find the 'second' string node (around col 28)
	secondNode := findNodeAtPosition(root, 0, 28)
	assert.NotNil(t, secondNode)

	result := provider.isSecondStringArgument(secondNode)
	assert.True(t, result)

	// Find the 'first' string node (around col 20)
	firstNode := findNodeAtPosition(root, 0, 20)
	assert.NotNil(t, firstNode)

	result = provider.isSecondStringArgument(firstNode)
	assert.False(t, result)
}

func TestGetSlotCompletions(t *testing.T) {
	// This is a unit test for the slot completion logic
	// It tests that the completion items are correctly generated from slots

	provider := &AdminCompletionProvider{
		adminIndexer: nil, // We'll test the slot parsing logic separately
	}

	// Test that getSlotCompletions returns empty when indexer is nil
	items := provider.getSlotCompletions("sw-card", nil, nil)
	assert.Empty(t, items)
}

func TestAdminVueSFCCompletionRoutesEmbeddedTemplateAndScript(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: filepath.Join(adminRoot, "sw-card/index.ts"),
		Props: []admin.VueComponentProp{{Name: "title", Type: "String"}},
	}))
	provider := NewAdminCompletionProvider(index)
	source := `<template><sw-card :tit /></template>
<script setup>
Shopware.Component.getComponentRegistry().get('sw-');
</script>`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "Card.vue")), source, 1,
	)
	complete := func(marker string) []string {
		t.Helper()
		offset := uint32(strings.Index(source, marker) + len(marker))
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		nodeOffset := offset - 1
		items := provider.GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, Language: document.SyntaxLanguage,
					DocumentContent: document.Text,
					DocumentTree:    document.SyntaxTree,
					LineIndex:       document.LineIndex, Root: document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(nodeOffset),
					Token: document.SyntaxTree.Root.TokenAtOffset(nodeOffset),
				},
			},
		)
		return completionLabels(items)
	}
	assert.Contains(t, complete(":tit"), ":title")
	assert.Contains(t, complete("'sw-"), "sw-card")
}

func TestAdminFilterCompletionUsesLiveRegistry(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "app/filter/currency.ts")
	persisted := `Shopware.Filter.register('currency', (value: number): string => String(value));`
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath, []byte(persisted),
	)))
	provider := NewAdminCompletionProvider(index)
	complete := func() []protocol.CompletionItem {
		source := `Shopware.Filter.getByName('curr')`
		document := lsp.NewTextDocument(
			uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")), source, 1,
		)
		offset := uint32(strings.Index(source, "curr") + 2)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		return provider.GetCompletions(context.Background(), &lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, Language: document.SyntaxLanguage,
				DocumentContent: document.Text, DocumentTree: document.SyntaxTree,
				LineIndex: document.LineIndex, Root: document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		})
	}
	items := complete()
	labels := completionLabels(items)
	assert.Contains(t, labels, "currency")
	assert.Equal(t, 1, strings.Count(strings.Join(labels, "\x00"), "currency"))
	for _, item := range items {
		if item.Label == "currency" {
			assert.Contains(t, item.Detail, "(value: number) => string")
		}
	}

	live := `Shopware.Filter.register('date', (value: Date): string => value.toISOString());`
	index.UpdateLiveDocument(
		registrationPath, parseJS(t, live), live, jssyntax.NewLineIndex(live),
	)
	labels = completionLabels(complete())
	assert.Contains(t, labels, "date")
	assert.NotContains(t, labels, "currency")
}

func TestAdminCompletionUsesImportedScriptSetupProps(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	componentDir := filepath.Join(adminRoot, "component/sw-imported-card")
	componentPath := filepath.Join(componentDir, "index.vue")
	componentSource := `<template>{{ hea }}<slot name="header" :item="heading" /></template>
<script setup lang="ts">
import type { CardProps, CardSlots } from './contracts';
const { optionalTitle: heading = 'fallback' } = defineProps<CardProps>();
defineSlots<CardSlots>();
</script>`
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		componentPath,
		[]byte(componentSource),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(componentDir, "index.ts"),
		[]byte(`Shopware.Component.register('sw-imported-card', () => import('./index.vue'));`),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(componentDir, "contracts.ts"),
		[]byte(`export interface CardProps { mode: 'small' | 'large'; optionalTitle?: string }
export interface HeaderPayload { item: string; count?: number }
export interface CardSlots { header(props: HeaderPayload): unknown }`),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(componentDir, "draft-contracts.ts"),
		[]byte(`export interface DraftProfile { city: string; zip: string }
export interface DraftRow { label: string; active: boolean }
export interface DraftProps { profile: DraftProfile; rows: DraftRow[] }`),
	)))
	draftPanelPath := filepath.Join(componentDir, "DraftPanel.vue")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		draftPanelPath,
		[]byte(`<template><slot :item="label" /></template>
<script setup lang="ts">
defineProps<{ label: string; count?: number }>();
defineSlots<{ default(props: { item: string; active: boolean }): unknown }>();
</script>`),
	)))

	complete := func(path, source, marker string) []string {
		t.Helper()
		document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
		offset := uint32(strings.Index(source, marker) + len(marker))
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		items := NewAdminCompletionProvider(index).GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, Language: document.SyntaxLanguage,
					DocumentContent: document.Text, DocumentTree: document.SyntaxTree,
					LineIndex: document.LineIndex, Root: document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(offset - 1),
					Token: document.SyntaxTree.Root.TokenAtOffset(offset - 1),
				},
			},
		)
		return completionLabels(items)
	}
	consumerPath := filepath.Join(adminRoot, "consumer.vue")
	labels := complete(
		consumerPath, `<template><sw-imported-card :mo /></template>`, ":mo",
	)
	assert.Contains(t, labels, ":mode")
	assert.Contains(t, labels, ":optional-title")
	slotNames := complete(
		consumerPath,
		`<template><sw-imported-card><template #hea /></sw-imported-card></template>`,
		"#hea",
	)
	assert.Contains(t, slotNames, "header")
	slotMembers := complete(
		consumerPath,
		`<template><sw-imported-card><template #header="{ it }" /></sw-imported-card></template>`,
		"{ it",
	)
	assert.Contains(t, slotMembers, "item")
	localBindings := complete(componentPath, componentSource, "{{ hea")
	assert.Contains(t, localBindings, "heading")
	liveSource := strings.ReplaceAll(componentSource, "heading", "headline")
	liveBindings := complete(componentPath, liveSource, "{{ hea")
	assert.Contains(t, liveBindings, "headline")
	assert.NotContains(t, liveBindings, "heading")
	liveImportedSource := `<template>
    {{ profile. }}
    <div v-for="row in rows">{{ row. }}</div>
</template>
<script setup lang="ts">
import type { DraftProps } from './draft-contracts';
const { profile, rows } = defineProps<DraftProps>();
</script>`
	nestedProfile := complete(componentPath, liveImportedSource, "profile.")
	assert.Contains(t, nestedProfile, "city")
	assert.Contains(t, nestedProfile, "zip")
	nestedRow := complete(componentPath, liveImportedSource, "row.")
	assert.Contains(t, nestedRow, "label")
	assert.Contains(t, nestedRow, "active")

	liveLocalBase := `<template>%s</template>
<script setup lang="ts">
import DraftPanel from './DraftPanel.vue';
const title = 'Draft';
</script>`
	localTags := complete(
		componentPath, fmt.Sprintf(liveLocalBase, `<Dra />`), "<Dra",
	)
	assert.Contains(t, localTags, "draft-panel")
	localProps := complete(
		componentPath, fmt.Sprintf(liveLocalBase, `<DraftPanel :la />`), ":la",
	)
	assert.Contains(t, localProps, "label")
	assert.Contains(t, localProps, "count")
	localSlots := complete(
		componentPath,
		fmt.Sprintf(
			liveLocalBase,
			`<DraftPanel><template #de /></DraftPanel>`,
		),
		"#de",
	)
	assert.Contains(t, localSlots, "default")
	localSlotMembers := complete(
		componentPath,
		fmt.Sprintf(
			liveLocalBase,
			`<DraftPanel><template #default="{ it }" /></DraftPanel>`,
		),
		"{ it",
	)
	assert.Contains(t, localSlotMembers, "item")
	assert.Contains(t, localSlotMembers, "active")
	dynamicProps := complete(
		componentPath,
		fmt.Sprintf(
			liveLocalBase,
			`<component :is="'draft-panel'" :la />`,
		),
		":la",
	)
	assert.Contains(t, dynamicProps, "label")

	liveChildSource := `<template><slot name="preview" :record="draftRecord" /></template>
<script setup lang="ts">
defineProps<{ draftLabel: string; enabled?: boolean }>();
defineSlots<{ preview(props: { record: { id: string } }): unknown }>();
</script>`
	liveChildDocument := lsp.NewTextDocument(
		uriutil.FileURI(draftPanelPath), liveChildSource, 2,
	)
	index.UpdateLiveDocument(
		draftPanelPath, liveChildDocument.SyntaxTree.Root, liveChildSource,
		liveChildDocument.LineIndex,
	)
	liveChildProps := complete(
		componentPath,
		fmt.Sprintf(liveLocalBase, `<DraftPanel :dra />`),
		":dra",
	)
	assert.Contains(t, liveChildProps, "draft-label")
	assert.NotContains(t, liveChildProps, "label")
	liveChildSlots := complete(
		componentPath,
		fmt.Sprintf(
			liveLocalBase,
			`<DraftPanel><template #pre /></DraftPanel>`,
		),
		"#pre",
	)
	assert.Contains(t, liveChildSlots, "preview")
	liveChildSlotMembers := complete(
		componentPath,
		fmt.Sprintf(
			liveLocalBase,
			`<DraftPanel><template #preview="{ rec }" /></DraftPanel>`,
		),
		"{ rec",
	)
	assert.Contains(t, liveChildSlotMembers, "record")

	index.RemoveLiveDocument(draftPanelPath)
	fallbackProps := complete(
		componentPath,
		fmt.Sprintf(liveLocalBase, `<DraftPanel :la />`),
		":la",
	)
	assert.Contains(t, fallbackProps, "label")
	assert.NotContains(t, fallbackProps, "draft-label")
}

func TestAdminCompletionComposesUnsavedLegacyDefinitionAndTwig(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "component/index.js")
	componentDir := filepath.Join(adminRoot, "component/sw-legacy-live")
	definitionPath := filepath.Join(componentDir, "index.js")
	templatePath := filepath.Join(componentDir, "sw-legacy-live.html.twig")
	require.NoError(t, os.MkdirAll(componentDir, 0o755))
	persistedTemplate := `<slot name="old-slot" />{{ oldMember }}`
	persistedDefinition := `import template from './sw-legacy-live.html.twig';
export default {
    template,
    props: { oldRequired: { type: String, required: true } },
    data() { return { oldMember: 'old' }; },
};`
	registration := `Shopware.Component.register(
    'sw-legacy-live', () => import('./sw-legacy-live'),
);`
	files := map[string]string{
		registrationPath: registration,
		definitionPath:   persistedDefinition,
		templatePath:     persistedTemplate,
	}
	for path, source := range files {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	for path, source := range files {
		require.NoError(t, index.Index(indexerpkg.NewParsedFile(
			path, []byte(source),
		)))
	}

	complete := func(path, source, marker string) []string {
		t.Helper()
		document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
		offset := uint32(strings.Index(source, marker) + len(marker))
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		items := NewAdminCompletionProvider(index).GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, Language: document.SyntaxLanguage,
					DocumentContent: document.Text, DocumentTree: document.SyntaxTree,
					LineIndex: document.LineIndex, Root: document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(offset - 1),
					Token: document.SyntaxTree.Root.TokenAtOffset(offset - 1),
				},
			},
		)
		return completionLabels(items)
	}
	consumerPath := filepath.Join(adminRoot, "consumer.html.twig")
	persistedProps := complete(
		consumerPath, `<sw-legacy-live :old />`, ":old",
	)
	assert.Contains(t, persistedProps, ":old-required")

	liveDefinition := `import template from './sw-legacy-live.html.twig';
export default {
    template,
    props: {
        liveRequired: { type: Number, required: true },
        liveOptional: Boolean,
    },
    data() { return { liveMember: { id: 'draft' } }; },
};`
	liveDefinitionDocument := lsp.NewTextDocument(
		uriutil.FileURI(definitionPath), liveDefinition, 2,
	)
	index.UpdateLiveDocument(
		definitionPath, liveDefinitionDocument.SyntaxTree.Root,
		liveDefinition, liveDefinitionDocument.LineIndex,
	)
	liveProps := complete(
		consumerPath, `<sw-legacy-live :liv />`, ":liv",
	)
	assert.Contains(t, liveProps, ":live-required")
	assert.Contains(t, liveProps, ":live-optional")
	assert.NotContains(t, liveProps, ":old-required")
	liveMembers := complete(templatePath, `{{ liveM }}`, "liveM")
	assert.Contains(t, liveMembers, "liveMember")
	assert.NotContains(t, liveMembers, "oldMember")

	liveTemplate := `<slot name="live-slot" :record="liveMember" />`
	liveTemplateDocument := lsp.NewTextDocument(
		uriutil.FileURI(templatePath), liveTemplate, 3,
	)
	index.UpdateLiveDocument(
		templatePath, liveTemplateDocument.SyntaxTree.Root,
		liveTemplate, liveTemplateDocument.LineIndex,
	)
	liveSlots := complete(
		consumerPath,
		`<sw-legacy-live><template #liv /></sw-legacy-live>`,
		"#liv",
	)
	assert.Contains(t, liveSlots, "live-slot")
	assert.NotContains(t, liveSlots, "old-slot")

	index.RemoveLiveDocument(definitionPath)
	index.RemoveLiveDocument(templatePath)
	fallbackComponent, err := index.GetEffectiveComponent("sw-legacy-live")
	require.NoError(t, err)
	require.NotNil(t, fallbackComponent)
	_, fallbackSlotFound := fallbackComponent.ComponentSlot("old-slot")
	require.True(t, fallbackSlotFound)
	fallbackProps := complete(
		consumerPath, `<sw-legacy-live :old />`, ":old",
	)
	assert.Contains(t, fallbackProps, ":old-required")
	fallbackSlots := complete(
		consumerPath,
		`<sw-legacy-live><template #old /></sw-legacy-live>`,
		"#old",
	)
	assert.Contains(t, fallbackSlots, "old-slot")
}

func TestAdminCompletionUsesUnsavedMixinAndDirectiveAcrossDocuments(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registryPath := filepath.Join(adminRoot, "app/runtime.ts")
	componentPath := filepath.Join(adminRoot, "component/sw-live-card/index.ts")
	persistedRegistry := `
Shopware.Mixin.register('shared-contract', {
    props: { oldLabel: String },
});
Shopware.Directive.register('old-focus', {});`
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registryPath, []byte(persistedRegistry),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		componentPath,
		[]byte(`Shopware.Component.register('sw-live-card', {
    mixins: [Shopware.Mixin.getByName('shared-contract')],
});`),
	)))

	complete := func(path, source, marker string) []string {
		t.Helper()
		document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
		offset := uint32(strings.Index(source, marker) + len(marker))
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		items := NewAdminCompletionProvider(index).GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root:  document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(offset - 1),
					Token: document.SyntaxTree.Root.TokenAtOffset(offset - 1),
				},
			},
		)
		return completionLabels(items)
	}
	consumerPath := filepath.Join(adminRoot, "consumer.html.twig")
	persistedProps := complete(consumerPath, `<sw-live-card :old />`, ":old")
	assert.Contains(t, persistedProps, ":old-label")
	persistedDirectives := complete(
		consumerPath, `<div v-old></div>`, "v-old",
	)
	assert.Contains(t, persistedDirectives, "v-old-focus")

	liveRegistry := `
Shopware.Mixin.register('shared-contract', {
    props: { liveLabel: { type: String, required: true } },
});
Shopware.Directive.register('live-focus', {});`
	liveDocument := lsp.NewTextDocument(
		uriutil.FileURI(registryPath), liveRegistry, 2,
	)
	index.UpdateLiveDocument(
		registryPath, liveDocument.SyntaxTree.Root, liveRegistry,
		liveDocument.LineIndex,
	)
	liveProps := complete(consumerPath, `<sw-live-card :liv />`, ":liv")
	assert.Contains(t, liveProps, ":live-label")
	assert.NotContains(t, liveProps, ":old-label")
	liveDirectives := complete(
		consumerPath, `<div v-liv></div>`, "v-liv",
	)
	assert.Contains(t, liveDirectives, "v-live-focus")
	assert.NotContains(t, liveDirectives, "v-old-focus")

	index.RemoveLiveDocument(registryPath)
	fallbackProps := complete(consumerPath, `<sw-live-card :old />`, ":old")
	assert.Contains(t, fallbackProps, ":old-label")
	fallbackDirectives := complete(
		consumerPath, `<div v-old></div>`, "v-old",
	)
	assert.Contains(t, fallbackDirectives, "v-old-focus")
}

func TestAdminSlotCompletionsUseDynamicComponentIntersection(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-card-a", FilePath: filepath.Join(adminRoot, "a/index.ts"),
			Slots: []admin.VueComponentSlot{{Name: "header"}, {Name: "a-only"}},
		},
		{
			Name: "sw-card-b", FilePath: filepath.Join(adminRoot, "b/index.ts"),
			Slots: []admin.VueComponentSlot{{Name: "header"}, {Name: "b-only"}},
		},
	} {
		require.NoError(t, index.SaveComponent(component))
	}
	source := `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #hea></template></component>`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")), source, 1,
	)
	offset := uint32(strings.Index(source, "#hea") + len("#hea"))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	items := NewAdminCompletionProvider(index).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset - 1),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset - 1),
			},
		},
	)
	labels := completionLabels(items)
	require.Contains(t, labels, "header")
	assert.NotContains(t, labels, "a-only")
	assert.NotContains(t, labels, "b-only")
}

func TestAdminDynamicComponentCompletionUsesRouterViewRouteContracts(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	templatePath := filepath.Join(adminRoot, "module/sw-shell/template.html.twig")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "module/sw-shell/index.js"),
		[]byte(`
import template from './template.html.twig';
Shopware.Component.register('sw-shell', { template });
Shopware.Module.register('sw-shell', {
    routes: {
        index: {
            component: 'sw-shell',
            children: {
                general: { component: 'sw-shell-general' },
                history: { component: 'sw-shell-history' },
            },
        },
    },
});`),
	)))
	for _, name := range []string{"sw-shell-general", "sw-shell-history"} {
		require.NoError(t, index.SaveComponent(admin.VueComponent{
			Name: name,
			FilePath: filepath.Join(
				adminRoot, "component", name, "index.js",
			),
			Props: []admin.VueComponentProp{
				{Name: "account", Type: "Account", Required: true},
				{Name: strings.TrimPrefix(name, "sw-shell-"), Type: "Boolean"},
			},
		}))
	}

	source := `<router-view v-slot="{ Component: view }">` +
		`<component :is="view" acc /></router-view>`
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	offset := uint32(strings.Index(source, " acc") + len(" acc"))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	items := NewAdminCompletionProvider(index).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset - 1),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset - 1),
			},
		},
	)
	labels := completionLabels(items)
	require.Contains(t, labels, "account")
	assert.NotContains(t, labels, "general")
	assert.NotContains(t, labels, "history")
}

func TestJavaScriptParserBuildsFunctionNode(t *testing.T) {
	code := `function test() { return 42; }`
	root := parseJS(t, code)
	assert.NotNil(t, root)
	assert.NotEmpty(t, jsquery.Nodes(root, jssyntax.JsFunction))
}

func TestAdminTemplateMemberCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	templatePath := filepath.Join(
		root,
		"src/Resources/app/administration/src/component/sw-card/sw-card.html.twig",
	)
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name:         "sw-card",
		FilePath:     filepath.Join(filepath.Dir(templatePath), "index.js"),
		TemplatePath: templatePath,
		Props: []admin.VueComponentProp{
			{Name: "title", Type: "String"},
		},
		Data:     []string{"isLoading"},
		Computed: []string{"cardClasses"},
		Methods:  []string{"saveCard"},
		Injected: []string{"repositoryFactory"},
	}))
	source := "{{ tit }}"
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	offset := uint32(strings.Index(source, "tit") + 1)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	items := NewAdminCompletionProvider(index).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	labels := make(map[string]protocol.CompletionItem)
	for _, item := range items {
		labels[item.Label] = item
	}
	for _, name := range []string{
		"title", "isLoading", "cardClasses", "saveCard", "repositoryFactory",
	} {
		assert.Contains(t, labels, name)
	}
	assert.Equal(t, "saveCard($0)", labels["saveCard"].InsertText)
}

func TestAdminCompositionSetupMemberCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	definitionPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/component/sw-setup/index.ts",
	)
	templatePath := filepath.Join(
		filepath.Dir(definitionPath), "sw-setup.html.twig",
	)
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		definitionPath,
		[]byte(`import template from './sw-setup.html.twig';
Shopware.Component.register('sw-setup', Shopware.Component.wrapComponentConfig({
    template,
    setup() {
        const count = ref<number>(0);
        const products = computed((): Product[] => []);
        const save = (id: string): Promise<void> => persist(id);
        const internalTitle: Ref<string> = ref('Fallback');
        const hidden = ref(false);
        return { count, products, save, title: internalTitle };
    },
		}));`),
	)))
	component, err := index.GetEffectiveComponent("sw-setup")
	require.NoError(t, err)
	require.NotNil(t, component)
	assert.Equal(t, templatePath, component.TemplatePath)
	require.Len(t, component.TemplateMembers(), 4)

	source := "{{ cou }}"
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	offset := uint32(strings.Index(source, "cou") + 1)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	items := NewAdminCompletionProvider(index).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	labels := make(map[string]protocol.CompletionItem)
	for _, item := range items {
		labels[item.Label] = item
	}
	assert.Equal(t, "data • number", labels["count"].Detail)
	assert.Equal(t, "computed • Product[]", labels["products"].Detail)
	assert.Equal(t, "method • (id: string) => Promise<void>", labels["save"].Detail)
	assert.Equal(t, "save($0)", labels["save"].InsertText)
	assert.Equal(t, "data • string", labels["title"].Detail)
	assert.NotContains(t, labels, "hidden")
}

func TestAdminScopedSlotContractAndLexicalCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	consumerTemplate := filepath.Join(adminRoot, "view/consumer.html.twig")
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-grid", FilePath: filepath.Join(adminRoot, "grid/index.js"),
		Slots: []admin.VueComponentSlot{
			{
				Name: "result-item", PayloadType: "{ item: Entity; index: number }",
				Members: []admin.VueComponentSlotMember{
					{Name: "item", Type: "Entity"},
					{Name: "index", Type: "number"},
				},
			},
			{
				NamePrefix: "column-",
				Members: []admin.VueComponentSlotMember{
					{Name: "item", Type: "Entity"},
					{Name: "isInlineEdit", Type: "boolean"},
				},
			},
		},
	}))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-consumer", FilePath: filepath.Join(adminRoot, "consumer/index.js"),
		TemplatePath: consumerTemplate,
		Props:        []admin.VueComponentProp{{Name: "title", Type: "String"}},
		Members: []admin.VueComponentMember{{
			Name: "dynamicCard", Kind: admin.ComponentMemberComputed,
			ReturnExpressions: []string{"'sw-card-a'", "'sw-card-b'"},
			ReturnsComplete:   true,
		}},
	}))
	provider := NewAdminCompletionProvider(index)

	completionLabels := func(source, marker string, markerEnd bool) map[string]protocol.CompletionItem {
		t.Helper()
		document := lsp.NewTextDocument(
			uriutil.FileURI(consumerTemplate), source, 1,
		)
		offset := strings.Index(source, marker)
		require.NotEqual(t, -1, offset)
		if markerEnd {
			offset += len(marker)
		} else {
			offset++
		}
		line, character := document.LineIndex.PositionUTF16(uint32(offset))
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		nodeOffset := uint32(offset)
		if nodeOffset > 0 {
			nodeOffset--
		}
		items := provider.GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root: document.SyntaxTree.Root,
					Node: document.SyntaxTree.Root.NodeAtOffset(nodeOffset),
					Token: document.SyntaxTree.Root.TokenAtOffset(
						nodeOffset,
					),
				},
			},
		)
		labels := make(map[string]protocol.CompletionItem, len(items))
		for _, item := range items {
			labels[item.Label] = item
		}
		return labels
	}

	bindingSource := `<sw-grid><template #result-item="{ it }">x</template></sw-grid>`
	binding := completionLabels(bindingSource, "{ it", true)
	require.Contains(t, binding, "item")
	require.Contains(t, binding, "index")
	assert.Contains(t, binding["item"].Detail, "Entity")

	bodySource := `<sw-grid><template #result-item="{ item: row, index }">{{ ro }}</template></sw-grid>`
	body := completionLabels(bodySource, "{{ ro", true)
	require.Contains(t, body, "row")
	require.Contains(t, body, "index")
	require.Contains(t, body, "title")
	assert.Contains(t, body["row"].Detail, "Entity")

	attributeSource := `<sw-grid><template #result-item="{ item: row }"><span :title="ro"></span></template></sw-grid>`
	attribute := completionLabels(attributeSource, `:title="ro`, true)
	require.Contains(t, attribute, "row")
	assert.Contains(t, attribute, "title")

	plainAttributeSource := `<div :title="ti"></div>`
	plainAttribute := completionLabels(
		plainAttributeSource, `:title="ti`, true,
	)
	assert.Contains(t, plainAttribute, "title")

	outsideSource := `<sw-grid><template #result-item="{ item: row }">x</template></sw-grid>{{ ro }}`
	outside := completionLabels(outsideSource, "</sw-grid>{{ ro", true)
	assert.NotContains(t, outside, "row")
	assert.Contains(t, outside, "title")

	defaultSource := `<router-view v-slot="{ Component }">{{ Com }}</router-view>`
	defaultSlot := completionLabels(defaultSource, "{{ Com", true)
	require.Contains(t, defaultSlot, "Component")
	assert.Contains(t, defaultSlot["Component"].Detail, "router-view.default")

	dynamicSource := `<sw-grid><template #[columnName]="{ item }">{{ it }}</template></sw-grid>`
	dynamicSlot := completionLabels(dynamicSource, "{{ it", true)
	require.Contains(t, dynamicSlot, "item")
	assert.Contains(t, dynamicSlot["item"].Detail, "sw-grid.<dynamic>")

	familyBindingSource := `<sw-grid><template #column-name="{ it }">x</template></sw-grid>`
	familyBinding := completionLabels(familyBindingSource, "{ it", true)
	require.Contains(t, familyBinding, "item")
	require.Contains(t, familyBinding, "isInlineEdit")
	assert.Contains(t, familyBinding["item"].Detail, "Entity")

	familyBodySource := `<sw-grid><template #column-name="{ item: row, isInlineEdit }">{{ ro }}</template></sw-grid>`
	familyBody := completionLabels(familyBodySource, "{{ ro", true)
	require.Contains(t, familyBody, "row")
	require.Contains(t, familyBody, "isInlineEdit")
	assert.Contains(t, familyBody["row"].Detail, "Entity")

	wholeObjectSource := `<sw-grid><template #result-item="props"><span :title="props.item"></span><span :title="props."></span></template></sw-grid>`
	wholeObject := completionLabels(wholeObjectSource, `:title="props.`, true)
	require.Contains(t, wholeObject, "item")
	require.Contains(t, wholeObject, "index")
	assert.Contains(t, wholeObject["item"].Detail, "Entity")
	assert.NotContains(t, wholeObject, "title")

	wholeObjectShadowedSource := `<sw-grid><template #result-item="props"><div v-for="props in rows"><span :title="props.name"></span><span :data-value="props."></span></div></template></sw-grid>`
	wholeObjectShadowed := completionLabels(
		wholeObjectShadowedSource, `:data-value="props.`, true,
	)
	require.Contains(t, wholeObjectShadowed, "name")
	assert.NotContains(t, wholeObjectShadowed, "item")
	assert.Contains(t, wholeObjectShadowed["name"].Detail, "observed member")

	slotShadowsLoopSource := `<div v-for="item in items"><sw-grid><template #result-item="{ item }">{{ it }}</template></sw-grid></div>`
	slotShadowsLoop := completionLabels(
		slotShadowsLoopSource, "{{ it", true,
	)
	require.Contains(t, slotShadowsLoop, "item")
	assert.Contains(t, slotShadowsLoop["item"].Detail, "slot local")
	assert.Contains(t, slotShadowsLoop["item"].Detail, "Entity")

	loopShadowsSlotSource := `<sw-grid><template #result-item="{ item }"><div v-for="item in children">{{ it }}</div></template></sw-grid>`
	loopShadowsSlot := completionLabels(
		loopShadowsSlotSource, "{{ it", true,
	)
	require.Contains(t, loopShadowsSlot, "item")
	assert.Contains(t, loopShadowsSlot["item"].Detail, "v-for local")

	nestedSlotSource := `<sw-grid><template #result-item="props"><sw-grid><template #result-item="{ item: innerItem }">{{ pr }}</template></sw-grid></template></sw-grid>`
	nestedSlot := completionLabels(nestedSlotSource, "{{ pr", true)
	require.Contains(t, nestedSlot, "props")
	require.Contains(t, nestedSlot, "innerItem")
	assert.Contains(t, nestedSlot["props"].Detail, "slot local")
	assert.Contains(t, nestedSlot["innerItem"].Detail, "Entity")

	for _, component := range []admin.VueComponent{
		{
			Name: "sw-card-a", FilePath: filepath.Join(adminRoot, "a/index.ts"),
			Slots: []admin.VueComponentSlot{{
				Name: "row", MembersComplete: true,
				Members: []admin.VueComponentSlotMember{
					{Name: "item", Type: "Product"},
					{Name: "onlyA", Type: "string"},
				},
			}},
		},
		{
			Name: "sw-card-b", FilePath: filepath.Join(adminRoot, "b/index.ts"),
			Slots: []admin.VueComponentSlot{{
				Name: "row", MembersComplete: true,
				Members: []admin.VueComponentSlotMember{{Name: "item", Type: "Category"}},
			}},
		},
	} {
		require.NoError(t, index.SaveComponent(component))
	}
	dynamicBindingSource := `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="{ it }">x</template></component>`
	dynamicBinding := completionLabels(dynamicBindingSource, "{ it", true)
	require.Contains(t, dynamicBinding, "item")
	assert.Contains(t, dynamicBinding["item"].Detail, "Product | Category")
	assert.NotContains(t, dynamicBinding, "onlyA")

	dynamicBodySource := `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="{ item: row }">{{ ro }}</template></component>`
	dynamicBody := completionLabels(dynamicBodySource, "{{ ro", true)
	require.Contains(t, dynamicBody, "row")
	assert.Contains(t, dynamicBody["row"].Detail, "Product | Category")

	dynamicObjectSource := `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="props">{{ props. }}</template></component>`
	dynamicObject := completionLabels(dynamicObjectSource, "props.", true)
	require.Contains(t, dynamicObject, "item")
	assert.NotContains(t, dynamicObject, "onlyA")

	inferredDynamicSource := `<component :is="dynamicCard"><template #row="{ it }">x</template></component>`
	inferredDynamic := completionLabels(inferredDynamicSource, "{ it", true)
	require.Contains(t, inferredDynamic, "item")
	assert.NotContains(t, inferredDynamic, "onlyA")
}

func TestAdminVueForAndEventLexicalCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	templatePath := filepath.Join(
		root, "src/Resources/app/administration/src/view.html.twig",
	)
	definitionPath := filepath.Join(
		filepath.Dir(templatePath), "index.ts",
	)
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		definitionPath,
		[]byte(`interface Row { id: string; name: string; active: boolean; child: { label: string; count: number }; }`),
	)))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-view", TemplatePath: templatePath,
		FilePath: definitionPath, DefinitionPath: definitionPath,
		Props: []admin.VueComponentProp{
			{Name: "items", Type: "Array as PropType<Row[]>", FilePath: definitionPath},
			{
				Name: "itemsById", Type: "Record<string, Row>",
				FilePath: definitionPath,
			},
			{
				Name: "typedItems",
				Type: "Array as PropType<Array<{ id: string; name: string; active: boolean }>>",
			},
			{Name: "title", Type: "String"},
		},
	}))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "mt-switch",
		Events: []admin.VueComponentEvent{{
			Name: "update:modelValue", Type: "(value: boolean) => any",
		}},
	}))
	provider := NewAdminCompletionProvider(index)
	complete := func(source, marker string) map[string]protocol.CompletionItem {
		t.Helper()
		document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
		offset := uint32(strings.Index(source, marker) + len(marker))
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		nodeOffset := offset
		if nodeOffset > 0 {
			nodeOffset--
		}
		items := provider.GetCompletions(context.Background(), &lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(nodeOffset),
				Token: document.SyntaxTree.Root.TokenAtOffset(nodeOffset),
			},
		})
		result := make(map[string]protocol.CompletionItem, len(items))
		for _, item := range items {
			result[item.Label] = item
		}
		return result
	}

	loop := complete(
		`<div v-for="(item, index) in items">{{ it }}</div>`,
		"{{ it",
	)
	require.Contains(t, loop, "item")
	require.Contains(t, loop, "index")
	require.Contains(t, loop, "title")
	assert.Contains(t, loop["item"].Detail, "v-for local")
	assert.Contains(t, loop["item"].Detail, "items")
	assert.Contains(t, loop["item"].Detail, "Row")

	members := complete(
		`<div v-for="item in items"><span :title="item.name"></span><span :data-value="item."></span></div>`,
		`:data-value="item.`,
	)
	require.Contains(t, members, "name")
	assert.Contains(t, members["name"].Detail, "typed member")
	assert.Contains(t, members["name"].Detail, "Row")
	assert.NotContains(t, members, "title")

	typedMembers := complete(
		`<div v-for="item in typedItems"><span :title="item."></span></div>`,
		`:title="item.`,
	)
	for _, name := range []string{"id", "name", "active"} {
		require.Contains(t, typedMembers, name)
	}
	assert.Contains(t, typedMembers["id"].Detail, "string")
	assert.Contains(t, typedMembers["active"].Detail, "boolean")
	typedArraySurface := complete(
		`<span :title="typedItems."></span>`, `typedItems.`,
	)
	for _, name := range []string{"length", "map", "filter"} {
		require.Contains(t, typedArraySurface, name)
	}
	assert.NotContains(t, typedArraySurface, "id")

	namedMembers := complete(
		`<div v-for="item in items"><span :title="item."></span></div>`,
		`:title="item.`,
	)
	for _, name := range []string{"id", "name", "active"} {
		require.Contains(t, namedMembers, name)
	}
	assert.Contains(t, namedMembers["id"].Detail, "string")

	nestedMembers := complete(
		`<div v-for="item in items"><span :title="item.child."></span></div>`,
		`:title="item.child.`,
	)
	require.Contains(t, nestedMembers, "label")
	require.Contains(t, nestedMembers, "count")
	assert.Contains(t, nestedMembers["label"].Detail, "string")
	assert.NotContains(t, nestedMembers, "id")

	recordMembers := complete(
		`<div v-for="(item, itemId, index) in itemsById"><span :title="item."></span><span :data-key="itemId."></span><span :data-index="index."></span></div>`,
		`:title="item.`,
	)
	for _, name := range []string{"id", "name", "active", "child"} {
		require.Contains(t, recordMembers, name)
	}
	recordKeyMembers := complete(
		`<div v-for="(item, itemId, index) in itemsById"><span :data-key="itemId."></span></div>`,
		`itemId.`,
	)
	require.Contains(t, recordKeyMembers, "length")
	require.Contains(t, recordKeyMembers, "toLowerCase")
	recordIndexMembers := complete(
		`<div v-for="(item, itemId, index) in itemsById"><span :data-index="index."></span></div>`,
		`index.`,
	)
	require.Contains(t, recordIndexMembers, "toFixed")
	assert.NotContains(t, recordIndexMembers, "name")

	handler := complete(
		`<div v-for="item in items"><mt-switch @update:model-value="$ev" /></div>`,
		`="$ev`,
	)
	require.Contains(t, handler, "$event")
	require.Contains(t, handler, "item")
	assert.Contains(t, handler["$event"].Detail, "boolean")
	assert.Contains(t, handler["$event"].Detail, "mt-switch.update:model-value")

	iterable := complete(`<div v-for="item in tit"></div>`, "in tit")
	assert.NotContains(t, iterable, "item")
	assert.Contains(t, iterable, "title")
}

func TestAdminComponentEntityMemberCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	templatePath := filepath.Join(adminRoot, "component/view.html.twig")
	definitionPath := filepath.Join(adminRoot, "component/index.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "entity-schema-definition.d.ts"),
		[]byte(`declare namespace EntitySchema {
interface cms_page { id: string; name: string; sections: EntityCollection<'cms_section'>; }
interface cms_section { id: string; name: string; position: number; }
}`),
	)))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-cms-page", TemplatePath: templatePath,
		FilePath: definitionPath, DefinitionPath: definitionPath,
		Members: []admin.VueComponentMember{
			{
				Name: "page", Kind: admin.ComponentMemberProp,
				Type: "Entity<'cms_page'>", FilePath: definitionPath,
			},
			{
				Name: "sectionsById", Kind: admin.ComponentMemberComputed,
				Type:     "Record<string, Entity<'cms_section'>>",
				FilePath: definitionPath,
			},
			{
				Name: "sectionGroups", Kind: admin.ComponentMemberComputed,
				Type:     "Record<string, EntityCollection<'cms_section'>>",
				FilePath: definitionPath,
			},
		},
	}))
	provider := NewAdminCompletionProvider(index)
	complete := func(source, marker string) map[string]protocol.CompletionItem {
		t.Helper()
		document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
		offset := uint32(strings.Index(source, marker) + len(marker))
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		items := provider.GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root: document.SyntaxTree.Root,
					Node: document.SyntaxTree.Root.NodeAtOffset(offset - 1),
				},
			},
		)
		result := make(map[string]protocol.CompletionItem, len(items))
		for _, item := range items {
			result[item.Label] = item
		}
		return result
	}

	page := complete(`<div :title="page."></div>`, `page.`)
	for _, name := range []string{"id", "name", "sections", "getEntityName"} {
		assert.Contains(t, page, name)
	}
	assert.Contains(t, page["sections"].Detail, "EntityCollection<'cms_section'>")

	collection := complete(`<div :title="page.sections."></div>`, `page.sections.`)
	assert.Contains(t, collection, "total")
	assert.Contains(t, collection, "first")
	assert.NotContains(t, collection, "position")
	calledElement := complete(
		`<div :title="page.sections.first()."></div>`,
		`page.sections.first().`,
	)
	for _, name := range []string{"id", "name", "position"} {
		assert.Contains(t, calledElement, name)
	}
	assert.NotContains(t, calledElement, "total")
	indexedElement := complete(
		`<div :title="page.sections[0]."></div>`,
		`page.sections[0].`,
	)
	for _, name := range []string{"id", "name", "position"} {
		assert.Contains(t, indexedElement, name)
	}
	assert.NotContains(t, indexedElement, "total")
	dynamicIndexedElement := complete(
		`<div v-for="(section, index) in page.sections" :title="page.sections[index]."></div>`,
		`page.sections[index].`,
	)
	for _, name := range []string{"id", "name", "position"} {
		assert.Contains(t, dynamicIndexedElement, name)
	}
	arithmeticIndexedElement := complete(
		`<div v-for="(section, index) in page.sections" :title="page.sections[index - 1]."></div>`,
		`page.sections[index - 1].`,
	)
	for _, name := range []string{"id", "name", "position"} {
		assert.Contains(t, arithmeticIndexedElement, name)
	}
	indexedRecordMember := complete(
		`<div :title="sectionsById[currentGroup]."></div>`,
		`sectionsById[currentGroup].`,
	)
	for _, name := range []string{"id", "name", "position"} {
		assert.Contains(t, indexedRecordMember, name)
	}

	section := complete(
		`<div v-for="section in page.sections" :title="section."></div>`,
		`section.`,
	)
	for _, name := range []string{"id", "name", "position"} {
		assert.Contains(t, section, name)
	}
	mappedName := complete(
		`<div v-for="name in page.sections?.map((section) => section.name) ?? []" :title="name."></div>`,
		`name.`,
	)
	for _, name := range []string{"length", "toLowerCase", "trim"} {
		assert.Contains(t, mappedName, name)
	}
	assert.NotContains(t, mappedName, "position")
	projected := complete(
		`<div v-for="card in page.sections.map((section) => ({ id: section.id, label: section.name }))" :title="card."></div>`,
		`card.`,
	)
	assert.Contains(t, projected, "id")
	assert.Contains(t, projected, "label")
	assert.NotContains(t, projected, "position")
	objectValues := complete(
		`<div v-for="section in Object.values(sectionsById)" :title="section."></div>`,
		`section.`,
	)
	for _, name := range []string{"id", "name", "position"} {
		assert.Contains(t, objectValues, name)
	}
	indexedRecord := complete(
		`<div v-for="section in sectionGroups[currentGroup]" :title="section."></div>`,
		`section.`,
	)
	for _, name := range []string{"id", "name", "position"} {
		assert.Contains(t, indexedRecord, name)
	}
	literalValues := complete(
		`<div v-for="label in ['primary', 'fallback']" :title="label."></div>`,
		`label.`,
	)
	assert.Contains(t, literalValues, "length")
	assert.Contains(t, literalValues, "toUpperCase")
}

func TestAdminPropCompletionUsesTemplateAttributeCase(t *testing.T) {
	index, err := admin.NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name:     "mt-card",
		FilePath: "/project/Resources/app/administration/src/mt-card/index.js",
		Props: []admin.VueComponentProp{{
			Name: "positionIdentifier", Type: "String",
			Documentation: "Stable extension position used by the card.",
		}, {
			Name: "modelValue", Type: "String",
		}, {
			Name: "checked", Type: "Boolean",
		}},
		Events: []admin.VueComponentEvent{
			{
				Name: "update:modelValue", Type: "(value: string) => void",
				Documentation: "Updates the public model value.",
			},
			{Name: "update:checked", Type: "(value: boolean) => void"},
		},
	}))
	items := NewAdminCompletionProvider(index).
		getComponentPropCompletions("mt-card")
	labels := make(map[string]protocol.CompletionItem)
	for _, item := range items {
		labels[item.Label] = item
	}
	assert.Contains(t, labels, "position-identifier")
	assert.Contains(t, labels, ":position-identifier")
	assert.NotContains(t, labels, "positionIdentifier")
	assert.Contains(
		t, labels["position-identifier"].Documentation.Value,
		"Stable extension position used by the card.",
	)
	assert.Contains(
		t, labels[":position-identifier"].Documentation.Value,
		"Stable extension position used by the card.",
	)
	require.Contains(t, labels, "@update:model-value")
	assert.Contains(t, labels["@update:model-value"].Detail, "value: string")
	assert.Contains(
		t, labels["@update:model-value"].Documentation.Value,
		"Updates the public model value.",
	)
	assert.Contains(
		t, labels["@update:model-value"].Documentation.Value,
		"**Payload:** `(value: string) => void`",
	)
	require.Contains(t, labels, "v-model")
	assert.Equal(t, `v-model="$0"`, labels["v-model"].InsertText)
	assert.Contains(t, labels["v-model"].Detail, "modelValue")
	require.Contains(t, labels, "v-model:checked")
	assert.Contains(t, labels["v-model:checked"].Detail, "boolean")
	assert.NotContains(t, labels, "v-model:model-value")
}

func TestAdminCompletionsExposeComponentAndPropDeprecations(t *testing.T) {
	index, err := admin.NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	templatePath := "/project/Resources/app/administration/src/consumer.html.twig"
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-legacy", FilePath: "/project/sw-legacy/index.ts",
		DefinitionPath: "/project/sw-legacy/index.ts",
		TemplatePath:   templatePath,
		Deprecated:     "tag:v6.8.0 - Use mt-modern instead.",
		Props: []admin.VueComponentProp{
			{Name: "oldValue", Type: "String", Deprecated: "Use modernValue instead."},
			{Name: "active", Type: "Boolean"},
			{Name: "modelValue", Type: "String", Deprecated: "Use value instead."},
		},
		Events:  []admin.VueComponentEvent{{Name: "update:modelValue"}},
		Methods: []string{"legacySave", "save"},
		Members: []admin.VueComponentMember{
			{
				Name: "legacySave", Kind: admin.ComponentMemberMethod,
				Deprecated: "Use save instead.",
			},
			{Name: "save", Kind: admin.ComponentMemberMethod},
		},
	}))
	provider := NewAdminCompletionProvider(index)

	byLabel := func(items []protocol.CompletionItem) map[string]protocol.CompletionItem {
		result := make(map[string]protocol.CompletionItem, len(items))
		for _, item := range items {
			result[item.Label] = item
		}
		return result
	}
	tags := byLabel(provider.getComponentTagCompletions(templatePath))
	require.Contains(t, tags, "sw-legacy")
	assert.True(t, tags["sw-legacy"].Deprecated)
	assert.Contains(t, tags["sw-legacy"].Documentation.Value, "Use mt-modern")

	registry := byLabel(provider.getComponentCompletions())
	require.Contains(t, registry, "sw-legacy")
	assert.True(t, registry["sw-legacy"].Deprecated)

	props := byLabel(provider.getComponentPropCompletions("sw-legacy", templatePath))
	for _, label := range []string{"old-value", ":old-value", "v-model"} {
		require.Contains(t, props, label)
		assert.True(t, props[label].Deprecated, label)
		assert.Contains(t, props[label].Documentation.Value, "Deprecated", label)
	}
	assert.False(t, props["active"].Deprecated)
	assert.NotContains(t, props["active"].Documentation.Value, "Deprecated")

	for _, members := range []map[string]protocol.CompletionItem{
		byLabel(provider.getTemplateMemberCompletions(uriutil.FileURI(templatePath))),
		byLabel(func() []protocol.CompletionItem {
			items, found := provider.getThisMemberCompletions(
				uriutil.FileURI("/project/sw-legacy/index.ts"),
			)
			require.True(t, found)
			return items
		}()),
	} {
		require.Contains(t, members, "legacySave")
		assert.True(t, members["legacySave"].Deprecated)
		assert.Contains(t, members["legacySave"].Documentation.Value, "Use save")
		require.Contains(t, members, "save")
		assert.False(t, members["save"].Deprecated)
	}
}

func TestAdminCompletionOffersParentTwigBlocksWithDeprecation(t *testing.T) {
	index, err := admin.NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := "/project/Resources/app/administration/src"
	parentTemplate := filepath.Join(adminRoot, "sw-card.html.twig")
	childTemplate := filepath.Join(adminRoot, "acme-card.html.twig")
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-card", Kind: admin.ComponentRegister,
		FilePath:     filepath.Join(adminRoot, "sw-card.js"),
		TemplatePath: parentTemplate,
		Blocks: []admin.TwigBlock{
			{
				Name: "sw_card_legacy", FilePath: parentTemplate, Line: 3,
				Deprecated: "Use sw_card_content instead.",
			},
			{Name: "sw_card_content", FilePath: parentTemplate, Line: 7},
		},
	}))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "acme-card", Kind: admin.ComponentExtend,
		TargetComponent: "sw-card", ExtendsComponent: "sw-card",
		FilePath:     filepath.Join(adminRoot, "acme-card.js"),
		TemplatePath: childTemplate,
	}))

	source := `{% block sw_card_ %}{% endblock %}`
	document := lsp.NewTextDocument(uriutil.FileURI(childTemplate), source, 1)
	offset := uint32(strings.Index(source, "sw_card_") + len("sw_card_"))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	items := NewAdminCompletionProvider(index).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset - 1),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset - 1),
			},
		},
	)
	byLabel := make(map[string]protocol.CompletionItem, len(items))
	for _, item := range items {
		byLabel[item.Label] = item
	}
	require.Contains(t, byLabel, "sw_card_legacy")
	assert.True(t, byLabel["sw_card_legacy"].Deprecated)
	assert.Contains(t, byLabel["sw_card_legacy"].Documentation.Value, "Use sw_card_content")
	require.Contains(t, byLabel, "sw_card_content")
	assert.False(t, byLabel["sw_card_content"].Deprecated)
}

func TestAdminCompletionIncludesTemplateScopedLocalComponents(t *testing.T) {
	index, err := admin.NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	templatePath := "/project/Resources/app/administration/src/sw-wrapper/template.html.twig"
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name:         "sw-wrapper",
		FilePath:     "/project/Resources/app/administration/src/sw-wrapper/index.js",
		TemplatePath: templatePath,
		LocalComponents: []admin.VueLocalComponent{{
			Name: "mt-card-original", Symbol: "MtCard",
			FilePath: "/project/Resources/app/administration/src/sw-wrapper/index.js",
			Line:     7,
		}},
	}))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name:     "mt-card",
		FilePath: "/project/Resources/app/administration/src/mt-card/index.js",
		Props: []admin.VueComponentProp{{
			Name: "title", Type: "String",
		}},
	}))
	provider := NewAdminCompletionProvider(index)

	localTagLabels := make([]string, 0)
	for _, item := range provider.getComponentTagCompletions(templatePath) {
		localTagLabels = append(localTagLabels, item.Label)
	}
	assert.Contains(t, localTagLabels, "mt-card-original")

	otherTagLabels := make([]string, 0)
	for _, item := range provider.getComponentTagCompletions(
		"/project/Resources/app/administration/src/other/template.html.twig",
	) {
		otherTagLabels = append(otherTagLabels, item.Label)
	}
	assert.NotContains(t, otherTagLabels, "mt-card-original")

	propLabels := make([]string, 0)
	for _, item := range provider.getComponentPropCompletions(
		"mt-card-original", templatePath,
	) {
		propLabels = append(propLabels, item.Label)
	}
	assert.Contains(t, propLabels, "title")
}

func TestAdminDynamicComponentSelectorAndContractCompletions(t *testing.T) {
	index, err := admin.NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	templatePath := "/project/Resources/app/administration/src/consumer.html.twig"
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: "/project/sw-card/index.ts",
		Props: []admin.VueComponentProp{
			{Name: "title", Type: "String"}, {Name: "cardOnly", Type: "String"},
			{Name: "variant", Type: `"primary" | "secondary"`},
		},
	}))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-panel", FilePath: "/project/sw-panel/index.ts",
		Props: []admin.VueComponentProp{
			{Name: "title", Type: "String"}, {Name: "panelOnly", Type: "String"},
			{Name: "variant", Type: `"primary" | "tertiary"`},
		},
	}))
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-cms-el-config-hero", FilePath: "/project/cms/hero.ts",
			Props: []admin.VueComponentProp{
				{Name: "element", Type: "Object"},
				{Name: "heroOnly", Type: "Boolean"},
			},
		},
		{
			Name: "sw-cms-el-config-text", FilePath: "/project/cms/text.ts",
			Props: []admin.VueComponentProp{
				{Name: "element", Type: "Object"},
				{Name: "textOnly", Type: "Boolean"},
			},
		},
	} {
		require.NoError(t, index.SaveComponent(component))
	}
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		"/project/Resources/app/administration/src/cms/registry.ts",
		[]byte(`
cmsService.registerCmsElement({
    name: 'hero', configComponent: 'sw-cms-el-config-hero',
});
cmsService.registerCmsElement({
    name: 'text', configComponent: 'sw-cms-el-config-text',
});`),
	)))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-host", FilePath: "/project/sw-host/index.ts",
		TemplatePath: templatePath,
		Members: []admin.VueComponentMember{
			{
				Name: "dynamicCard", Kind: admin.ComponentMemberComputed,
				ReturnExpressions: []string{"'sw-card'", "'sw-panel'"},
				ReturnsComplete:   true,
			},
			{
				Name: "cmsElements", Kind: admin.ComponentMemberComputed,
				CMSRegistryKind: admin.AdminCMSElement,
			},
		},
	}))
	provider := NewAdminCompletionProvider(index)
	complete := func(source, marker string) map[string]protocol.CompletionItem {
		t.Helper()
		document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
		offset := uint32(strings.Index(source, marker) + len(marker))
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		nodeOffset := offset
		if nodeOffset > 0 {
			nodeOffset--
		}
		items := provider.GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root:  document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(nodeOffset),
					Token: document.SyntaxTree.Root.TokenAtOffset(nodeOffset),
				},
			},
		)
		result := make(map[string]protocol.CompletionItem, len(items))
		for _, item := range items {
			result[item.Label] = item
		}
		return result
	}

	selectorItems := complete(`<component :is="'sw-ca'" />`, "sw-ca")
	require.Contains(t, selectorItems, "sw-card")
	assert.Equal(t, "sw-card", selectorItems["sw-card"].InsertText)
	assert.Contains(t, selectorItems["sw-card"].Detail, "dynamic component")

	contractItems := complete(`<component :is="'sw-card'"  />`, `" `)
	assert.Contains(t, contractItems, "title")
	assert.Contains(t, contractItems, ":title")

	unionItems := complete(
		`<component :is="active ? 'sw-card' : 'sw-panel'"  />`, `" `,
	)
	assert.Contains(t, unionItems, "title")
	assert.NotContains(t, unionItems, "card-only")
	assert.NotContains(t, unionItems, "panel-only")

	inferredItems := complete(
		`<component :is="dynamicCard"  />`, `" `,
	)
	assert.Contains(t, inferredItems, "title")
	assert.NotContains(t, inferredItems, "card-only")
	assert.NotContains(t, inferredItems, "panel-only")

	owner, ownerErr := index.GetComponentByTemplatePath(templatePath)
	require.NoError(t, ownerErr)
	require.NotNil(t, owner)
	cmsMember, cmsMemberFound := owner.TemplateMember("cmsElements")
	require.True(t, cmsMemberFound)
	require.Equal(t, admin.AdminCMSElement, cmsMember.CMSRegistryKind)
	registrations, registrationsErr := index.GetAllCMSRegistrationsByKind(
		admin.AdminCMSElement,
	)
	require.NoError(t, registrationsErr)
	require.Len(t, registrations, 2)
	resolvedCMS, cmsContracts, cmsComplete, cmsErr :=
		index.ResolveDynamicComponentContracts(
			templatePath,
			admin.VueDynamicComponentSelector{
				Expression: "cmsElements[element.type].configComponent",
			},
		)
	require.NoError(t, cmsErr)
	require.True(t, cmsComplete, resolvedCMS.Names())
	require.Len(t, cmsContracts, 2)
	cmsItems := complete(
		`<component :is="cmsElements[element.type].configComponent"  />`, `" `,
	)
	assert.Contains(t, cmsItems, "element")
	assert.NotContains(t, cmsItems, "hero-only")
	assert.NotContains(t, cmsItems, "text-only")

	objectItems := complete(
		`<sw-card v-bind="{ cardOnly: existing, ti }" />`, ", ti",
	)
	assert.Contains(t, objectItems, "title")
	assert.NotContains(t, objectItems, "cardOnly")
	assert.Equal(t, "title: $0", objectItems["title"].InsertText)

	dynamicObjectItems := complete(
		`<component :is="dynamicCard" v-bind="{ ti }" />`, "ti",
	)
	assert.Contains(t, dynamicObjectItems, "title")
	assert.NotContains(t, dynamicObjectItems, "cardOnly")
	assert.NotContains(t, dynamicObjectItems, "panelOnly")

	staticValueItems := complete(
		`<sw-card variant="sec" />`, `variant="sec`,
	)
	assert.Contains(t, staticValueItems, "primary")
	assert.Contains(t, staticValueItems, "secondary")
	assert.NotContains(t, staticValueItems, "tertiary")
	valueEdit, ok := staticValueItems["secondary"].TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "secondary", valueEdit.NewText)
	emptyValueItems := complete(
		`<sw-card variant="" />`, `variant="`,
	)
	assert.Contains(t, emptyValueItems, "primary")
	assert.Contains(t, emptyValueItems, "secondary")
	boundValueItems := complete(
		`<sw-card :variant="'sec'" />`, `:variant="'sec`,
	)
	assert.Contains(t, boundValueItems, "primary")
	assert.Contains(t, boundValueItems, "secondary")
	boundEdit, ok := boundValueItems["secondary"].TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "secondary", boundEdit.NewText)
	objectBoundValueItems := complete(
		`<sw-card v-bind="{ variant: 'sec' }" />`, `variant: 'sec`,
	)
	assert.Contains(t, objectBoundValueItems, "primary")
	assert.Contains(t, objectBoundValueItems, "secondary")
	assert.NotContains(t, objectBoundValueItems, "tertiary")
	objectBoundEdit, ok :=
		objectBoundValueItems["secondary"].TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "secondary", objectBoundEdit.NewText)

	dynamicValueItems := complete(
		`<component :is="dynamicCard" variant="p" />`, `variant="p`,
	)
	assert.Contains(t, dynamicValueItems, "primary")
	assert.NotContains(t, dynamicValueItems, "secondary")
	assert.NotContains(t, dynamicValueItems, "tertiary")
	dynamicObjectBoundValueItems := complete(
		`<component :is="dynamicCard" v-bind="{ variant: 'p' }" />`,
		`variant: 'p`,
	)
	assert.Contains(t, dynamicObjectBoundValueItems, "primary")
	assert.NotContains(t, dynamicObjectBoundValueItems, "secondary")
	assert.NotContains(t, dynamicObjectBoundValueItems, "tertiary")
}

func TestAdminThisMemberCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	definitionPath := filepath.Join(
		root,
		"src/Resources/app/administration/src/component/sw-card/index.js",
	)
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: definitionPath, DefinitionPath: definitionPath,
		Props: []admin.VueComponentProp{{Name: "title", Type: "String"}},
		Data:  []string{"isLoading"}, Computed: []string{"cardClasses"},
		Methods: []string{"saveCard"}, Injected: []string{"repositoryFactory"},
	}))
	source := `export default { methods: { run() { return this.; } } };`
	document := lsp.NewTextDocument(uriutil.FileURI(definitionPath), source, 1)
	offset := uint32(strings.Index(source, "this.") + len("this."))
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	items := NewAdminCompletionProvider(index).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset - 1),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset - 1),
			},
		},
	)
	labels := make(map[string]protocol.CompletionItem)
	for _, item := range items {
		labels[item.Label] = item
	}
	for _, name := range []string{
		"title", "isLoading", "cardClasses", "saveCard", "repositoryFactory",
		"$emit", "$router", "$t",
	} {
		assert.Contains(t, labels, name)
	}
	assert.Equal(t, "saveCard($0)", labels["saveCard"].InsertText)
}

func TestAdminRuntimeRegistryCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	registrationPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/main.ts",
	)
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`
Shopware.Application.addServiceProvider('acl', factory);
Shopware.Store.register('profile', {
    state() { return { currentUser: null }; },
    getters: { displayName() {} },
    actions: { load() {} },
});
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product',
    roles: { viewer: { privileges: ['product:read'] } },
});
`),
	)))
	provider := NewAdminCompletionProvider(index)
	documentPath := filepath.Join(filepath.Dir(registrationPath), "consumer.ts")

	tests := []struct {
		name     string
		source   string
		needle   string
		expected []string
	}{
		{
			name: "service lookup", source: `Shopware.Service('a')`, needle: "a",
			expected: []string{"acl"},
		},
		{
			name: "component injection", source: `export default { inject: ['a'] }`, needle: "a",
			expected: []string{"acl"},
		},
		{
			name: "store lookup", source: `Shopware.Store.get('p')`, needle: "p",
			expected: []string{"profile"},
		},
		{
			name: "store unregister", source: `Shopware.Store.unregister('p')`, needle: "p",
			expected: []string{"profile"},
		},
		{
			name: "store member", source: `Shopware.Store.get('profile').`, needle: ".",
			expected: []string{"currentUser", "displayName", "load"},
		},
		{
			name: "privilege role", source: `this.acl.can('product.v')`, needle: "product.v",
			expected: []string{"product.viewer", "product:read"},
		},
		{
			name: "privilege service", source: `Shopware.Service('privileges').getPrivileges('product')`, needle: "product')",
			expected: []string{"product.viewer", "product:read"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(documentPath), test.source, 1,
			)
			offset := strings.LastIndex(test.source, test.needle)
			require.NotEqual(t, -1, offset)
			params := &protocol.CompletionParams{}
			params.TextDocument.URI = document.URI
			items := provider.GetCompletions(
				context.Background(),
				&lsp.CompletionRequest{
					CompletionParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root: document.SyntaxTree.Root,
						Node: document.SyntaxTree.Root.NodeAtOffset(
							uint32(offset),
						),
						Token: document.SyntaxTree.Root.TokenAtOffset(
							uint32(offset),
						),
					},
				},
			)
			labels := make(map[string]protocol.CompletionItem)
			for _, item := range items {
				labels[item.Label] = item
			}
			for _, label := range test.expected {
				assert.Contains(t, labels, label)
			}
			if test.name == "store member" {
				assert.Equal(t, "load($0)", labels["load"].InsertText)
				assert.Contains(t, labels["currentUser"].Detail, "null")
				assert.Contains(t, labels["load"].Detail, "() => unknown")
			}
		})
	}
}

func TestAdminApplicationContainerCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	globalPath := filepath.Join(adminRoot, "global.types.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		globalPath,
		[]byte(`
export interface SubContainer<T extends string> { $list(): string[]; }
declare global {
    interface FactoryContainer extends SubContainer<'factory'> {
        locale: LocaleFactory;
        module: ModuleFactory;
    }
    interface ServiceContainer extends SubContainer<'service'> {
        acl: AclService;
    }
}`),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "services.ts"),
		[]byte(`Shopware.Application.addServiceProvider('runtimeService', factory);`),
	)))
	provider := NewAdminCompletionProvider(index)
	documentPath := filepath.Join(adminRoot, "consumer.ts")
	complete := func(source, needle string) map[string]protocol.CompletionItem {
		t.Helper()
		document := lsp.NewTextDocument(
			uriutil.FileURI(documentPath), source, 1,
		)
		offset := strings.LastIndex(source, needle)
		require.NotEqual(t, -1, offset)
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		items := provider.GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root:  document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(uint32(offset)),
					Token: document.SyntaxTree.Root.TokenAtOffset(uint32(offset)),
				},
			},
		)
		result := make(map[string]protocol.CompletionItem, len(items))
		for _, item := range items {
			result[item.Label] = item
		}
		return result
	}

	containerNames := complete(`Application.getContainer('fa')`, "fa")
	for _, name := range []string{"factory", "service", "init", "init-pre", "init-post"} {
		assert.Contains(t, containerNames, name)
	}
	factories := complete(`Application.getContainer('factory').`, ".")
	for _, name := range []string{"$list", "locale", "module"} {
		assert.Contains(t, factories, name)
	}
	assert.Equal(t, "$list($0)", factories["$list"].InsertText)
	services := complete(`function run() {
    const container = Shopware.Application.getContainer('service');
    return container.;
}`, ".;")
	for _, name := range []string{"$list", "acl", "runtimeService"} {
		assert.Contains(t, services, name)
	}
}

func TestAdminShopwareContextCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "app/composables/use-context.ts"),
		[]byte(`export interface ContextState {
    app: { config: { version: null | string; shopId: null | string } };
    api: { languageId: null | string; versionId: null | string };
}`),
	)))
	provider := NewAdminCompletionProvider(index)
	documentPath := filepath.Join(adminRoot, "module/example/index.ts")
	complete := func(source string) map[string]protocol.CompletionItem {
		t.Helper()
		document := lsp.NewTextDocument(
			uriutil.FileURI(documentPath), source, 1,
		)
		offset := uint32(strings.LastIndex(source, "."))
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		items := provider.GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root:  document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
					Token: document.SyntaxTree.Root.TokenAtOffset(offset),
				},
			},
		)
		result := make(map[string]protocol.CompletionItem, len(items))
		for _, item := range items {
			result[item.Label] = item
		}
		return result
	}

	rootMembers := complete(`Shopware.Context.`)
	assert.Contains(t, rootMembers, "app")
	assert.Contains(t, rootMembers, "api")
	apiMembers := complete(`Shopware.Context.api.`)
	assert.Contains(t, apiMembers, "languageId")
	assert.Contains(t, apiMembers, "versionId")
	assert.Contains(t, apiMembers["languageId"].Detail, "null | string")
	configMembers := complete(`Shopware.Context.app.config.`)
	assert.Contains(t, configMembers, "version")
	assert.Contains(t, configMembers, "shopId")
}

func TestAdminShopwareUtilsCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "core/service/util.service.ts"),
		[]byte(`export const format = { date, fileSize };
export default { createId, format };
function createId(): string { return 'id'; }
function date(value: string): string { return value; }
function fileSize(bytes: number): string { return String(bytes); }`),
	)))
	provider := NewAdminCompletionProvider(index)
	documentPath := filepath.Join(adminRoot, "module/example/index.ts")
	complete := func(source string) map[string]protocol.CompletionItem {
		t.Helper()
		document := lsp.NewTextDocument(
			uriutil.FileURI(documentPath), source, 1,
		)
		offset := uint32(strings.LastIndex(source, "."))
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		items := provider.GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root:  document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
					Token: document.SyntaxTree.Root.TokenAtOffset(offset),
				},
			},
		)
		result := make(map[string]protocol.CompletionItem, len(items))
		for _, item := range items {
			result[item.Label] = item
		}
		return result
	}

	rootMembers := complete(`Shopware.Utils.`)
	assert.Contains(t, rootMembers, "createId")
	assert.Contains(t, rootMembers, "format")
	assert.Equal(t, "createId($0)", rootMembers["createId"].InsertText)
	formatMembers := complete(`Shopware.Utils.format.`)
	assert.Contains(t, formatMembers, "date")
	assert.Contains(t, formatMembers, "fileSize")
	assert.Contains(t, formatMembers["date"].Detail, "(value: string) => string")
	aliasMembers := complete(`const format = Shopware.Utils.format; format.`)
	assert.Contains(t, aliasMembers, "date")
	assert.Contains(t, aliasMembers, "fileSize")
	destructuredMembers := complete(`const { format } = Shopware.Utils; format.`)
	assert.Contains(t, destructuredMembers, "date")
	assert.Contains(t, destructuredMembers, "fileSize")
}

func TestAdminShopwareEventBusEventCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	for path, source := range map[string]string{
		filepath.Join(adminRoot, "core/service/util.service.ts"): `
import EventBus from './utils/eventBus.utils';
export default { EventBus };
`,
		filepath.Join(adminRoot, "core/service/utils/eventBus.utils.ts"): `
interface Events extends Record<string | symbol, unknown> {
    'save-event': { id: string };
    telemetry: undefined;
}
const emitter = mitt<Events>();
export default emitter;
`,
	} {
		require.NoError(t, index.Index(indexerpkg.NewParsedFile(
			path, []byte(source),
		)))
	}
	provider := NewAdminCompletionProvider(index)
	documentPath := filepath.Join(adminRoot, "module/example/index.ts")
	complete := func(source, marker string) map[string]protocol.CompletionItem {
		t.Helper()
		document := lsp.NewTextDocument(
			uriutil.FileURI(documentPath), source, 1,
		)
		offset := uint32(strings.LastIndex(source, marker) + len(marker))
		params := &protocol.CompletionParams{}
		params.TextDocument.URI = document.URI
		items := provider.GetCompletions(
			context.Background(),
			&lsp.CompletionRequest{
				CompletionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root:  document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
					Token: document.SyntaxTree.Root.TokenAtOffset(offset),
				},
			},
		)
		result := make(map[string]protocol.CompletionItem, len(items))
		for _, item := range items {
			result[item.Label] = item
		}
		return result
	}

	for _, test := range []struct {
		name, source, marker string
	}{
		{"direct", `Shopware.Utils.EventBus.on('sav', handler);`, "sav"},
		{"destructured", `const { EventBus } = Shopware.Utils;
EventBus.off('tel', handler);`, "tel"},
		{"alias", `const bus = Shopware.Utils.EventBus;
bus.emit('sav', payload);`, "sav"},
	} {
		t.Run(test.name, func(t *testing.T) {
			items := complete(test.source, test.marker)
			assert.Contains(t, items, "save-event")
			assert.Contains(t, items, "telemetry")
			assert.Contains(t, items["save-event"].Detail, "{ id: string }")
		})
	}
}

func TestAdminTwigPrivilegeCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "privileges.ts"),
		[]byte(`Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product',
    roles: { viewer: { privileges: ['product:read'] } },
});`),
	)))
	source := `<mt-button :disabled="acl.can('product.v')" />`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "view.html.twig")), source, 1,
	)
	offset := uint32(strings.Index(source, "product.v") + len("product.v"))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	items := NewAdminCompletionProvider(index).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root: document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(offset - 1),
			},
		},
	)
	labels := make(map[string]bool, len(items))
	adminDetail := ""
	for _, item := range items {
		labels[item.Label] = true
		if item.Label == admin.AdminPrivilegeAdministrator {
			adminDetail = item.Detail
		}
	}
	assert.True(t, labels[admin.AdminPrivilegeAdministrator])
	assert.Contains(t, adminDetail, "Built-in")
	assert.True(t, labels["product.viewer"])
	assert.True(t, labels["product:read"])
}

func TestAdminTwigModuleRouteCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "module/sw-product/index.ts"),
		[]byte(`Shopware.Module.register('sw-product', {
    routes: {
        detail: { path: 'detail', component: 'sw-product-detail' },
        create: { path: 'create', component: 'sw-product-create' },
    },
});`),
	)))
	source := `<router-link :to="{ name: 'sw.product.d' }" />`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "view.html.twig")), source, 1,
	)
	offset := uint32(strings.Index(source, "sw.product.d") + len("sw.product.d"))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	items := NewAdminCompletionProvider(index).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root: document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(offset - 1),
			},
		},
	)
	labels := make(map[string]bool, len(items))
	for _, item := range items {
		labels[item.Label] = true
	}
	assert.True(t, labels["sw.product.detail"])
	assert.True(t, labels["sw.product.create"])
}

func TestAdminImportedSetupStoreMemberCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src",
	)
	storePath := filepath.Join(adminRoot, "app/store/session.store.ts")
	factoryPath := filepath.Join(adminRoot, "app/composables/use-session.ts")
	require.NoError(t, os.MkdirAll(filepath.Dir(factoryPath), 0o755))
	require.NoError(t, os.WriteFile(factoryPath, nil, 0o644))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		storePath,
		[]byte(`
import useSession from '../composables/use-session';
export default Shopware.Store.register('session', useSession);`),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		factoryPath,
		[]byte(`
const currentUser = ref(null);
const userPending = computed(() => !currentUser.value);
function setCurrentUser() {}
export default function useSession() {
    return { currentUser, userPending, setCurrentUser };
}`),
	)))

	source := `Shopware.Store.get('session').`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")), source, 1,
	)
	offset := uint32(strings.LastIndex(source, "."))
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	items := NewAdminCompletionProvider(index).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	labels := make(map[string]protocol.CompletionItem)
	for _, item := range items {
		labels[item.Label] = item
	}
	assert.Contains(t, labels, "currentUser")
	assert.Contains(t, labels, "userPending")
	assert.Contains(t, labels, "setCurrentUser")
	assert.Equal(t, "setCurrentUser($0)", labels["setCurrentUser"].InsertText)
}

func TestAdminComponentAndModuleRegistryCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "main.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`
Shopware.Component.register('sw-card', { props: { title: String } });
Shopware.Module.register('sw-product', {
    title: 'sw-product.general.mainMenuItemGeneral',
    routes: { index: { path: 'index', component: 'sw-card' } },
});`),
	)))
	provider := NewAdminCompletionProvider(index)
	for _, test := range []struct {
		name, source, needle, expected string
	}{
		{
			"component get", `Shopware.Component.getComponentRegistry().get('sw-ca')`,
			"sw-ca", "sw-card",
		},
		{
			"component has", `Component.getComponentRegistry().has('sw-ca')`,
			"sw-ca", "sw-card",
		},
		{
			"module get", `Shopware.Module.getModuleRegistry().get('sw-pro')`,
			"sw-pro", "sw-product",
		},
		{
			"module has", `Module.getModuleRegistry().has('sw-pro')`,
			"sw-pro", "sw-product",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")),
				test.source,
				1,
			)
			offset := uint32(strings.Index(test.source, test.needle) + 1)
			params := &protocol.CompletionParams{}
			params.TextDocument.URI = document.URI
			items := provider.GetCompletions(
				context.Background(),
				&lsp.CompletionRequest{
					CompletionParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root:  document.SyntaxTree.Root,
						Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
						Token: document.SyntaxTree.Root.TokenAtOffset(offset),
					},
				},
			)
			labels := make(map[string]protocol.CompletionItem, len(items))
			for _, item := range items {
				labels[item.Label] = item
			}
			require.Contains(t, labels, test.expected)
			if test.expected == "sw-product" {
				assert.Contains(t, labels[test.expected].Detail, "Administration module")
			}
		})
	}
}

func TestAdminDirectiveCompletionsInJavaScriptAndTwig(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "app/directive/tooltip.directive.ts"),
		[]byte(`Shopware.Directive.register('tooltip', {});`),
	)))
	provider := NewAdminCompletionProvider(index)
	for _, test := range []struct {
		name, source, needle, expected, extension string
	}{
		{
			"registry lookup", `Shopware.Directive.getByName('too')`,
			"too", "tooltip", ".ts",
		},
		{
			"native markup", `<div v-too="message"></div>`,
			"v-too", "v-tooltip", ".html.twig",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(adminRoot, "consumer"+test.extension)),
				test.source,
				1,
			)
			offset := uint32(strings.Index(test.source, test.needle) + 2)
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.CompletionParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			items := provider.GetCompletions(
				context.Background(),
				&lsp.CompletionRequest{
					CompletionParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root:  document.SyntaxTree.Root,
						Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
						Token: document.SyntaxTree.Root.TokenAtOffset(offset),
					},
				},
			)
			labels := make(map[string]protocol.CompletionItem, len(items))
			for _, item := range items {
				labels[item.Label] = item
			}
			require.Contains(t, labels, test.expected)
			assert.Contains(t, labels[test.expected].Detail, "Vue directive")
		})
	}
	assert.Contains(t, provider.GetTriggerCharacters(), "-")
}

func TestAdminCMSRegistryCompletions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "cms.ts"),
		[]byte(`
Shopware.Component.register('sw-cms-el-hero', {});
Shopware.Component.register('sw-cms-el-config-hero', {});
Shopware.Component.register('sw-cms-el-preview-hero', {});
Shopware.Service('cmsService').registerCmsElement({
    name: 'hero', label: 'Hero', component: 'sw-cms-el-hero',
    configComponent: 'sw-cms-el-config-hero',
    previewComponent: 'sw-cms-el-preview-hero',
});
Shopware.Service('cmsService').registerCmsBlock({ name: 'hero-grid', slots: { content: { type: 'hero' } } });`),
	)))
	provider := NewAdminCompletionProvider(index)
	for _, test := range []struct {
		name, source, needle, expected, detail string
	}{
		{
			"element lookup",
			`Shopware.Service('cmsService').getCmsElementConfigByName('he')`,
			"he", "hero", "Shopware CMS",
		},
		{
			"block lookup",
			`Shopware.Service('cmsService').getCmsBlockConfigByName('hero-g')`,
			"hero-g", "hero-grid", "Shopware CMS",
		},
		{
			"block slot element",
			`cmsService.registerCmsBlock({ name: 'other', slots: { content: { type: 'he' } } })`,
			"he", "hero", "Shopware CMS",
		},
		{
			"element component link",
			`cmsService.registerCmsElement({ name: 'other', component: 'sw-cms-el-he' })`,
			"sw-cms-el-he", "sw-cms-el-hero", "",
		},
		{
			"element config component link",
			`cmsService.registerCmsElement({ name: 'other', configComponent: 'sw-cms-el-config-he' })`,
			"sw-cms-el-config-he", "sw-cms-el-config-hero", "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")),
				test.source,
				1,
			)
			offset := uint32(strings.LastIndex(test.source, test.needle) + len(test.needle))
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.CompletionParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			items := provider.GetCompletions(
				context.Background(),
				&lsp.CompletionRequest{
					CompletionParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree,
						LineIndex:    document.LineIndex,
						Root:         document.SyntaxTree.Root,
						Node:         document.SyntaxTree.Root.NodeAtOffset(offset),
						Token:        document.SyntaxTree.Root.TokenAtOffset(offset),
					},
				},
			)
			labels := make(map[string]protocol.CompletionItem, len(items))
			for _, item := range items {
				labels[item.Label] = item
			}
			require.Contains(t, labels, test.expected)
			if test.detail != "" {
				assert.Contains(t, labels[test.expected].Detail, test.detail)
			}
		})
	}
}

func TestAdminDirectiveCompletionsAreTemplateScoped(t *testing.T) {
	index, err := admin.NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	ownerTemplate := "/project/Resources/app/administration/src/owner/template.html.twig"
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-owner", TemplatePath: ownerTemplate,
		FilePath: "/project/Resources/app/administration/src/owner/index.js",
		LocalDirectives: []admin.VueLocalDirective{{
			Name: "hide", FilePath: "/project/Resources/app/administration/src/owner/index.js",
			Line: 4,
		}},
	}))
	provider := NewAdminCompletionProvider(index)
	labels := func(path string) []string {
		items := provider.getDirectiveCompletions(true, path)
		result := make([]string, 0, len(items))
		for _, item := range items {
			result = append(result, item.Label)
		}
		return result
	}
	assert.Contains(t, labels(ownerTemplate), "v-hide")
	assert.NotContains(
		t,
		labels("/project/Resources/app/administration/src/other/template.html.twig"),
		"v-hide",
	)
}

func TestAdminTemplateCompletionsIncludeRuntimeScope(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	templatePath := filepath.Join(
		root, "src/Administration/Resources/app/administration/src/sw-card.html.twig",
	)
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-card", TemplatePath: templatePath,
		FilePath: filepath.Join(filepath.Dir(templatePath), "index.ts"),
		Members: []admin.VueComponentMember{{
			Name: "title", Kind: admin.ComponentMemberProp, Type: "string",
		}},
		Blocks: []admin.TwigBlock{{
			Name: "sw_card_row",
			ScopeMembers: []admin.TwigBlockScopeMember{{
				Name: "item", Type: "Product",
			}},
		}},
	}))
	source := `{{ tit }}`
	_, request := twigCompletionAt(
		uriutil.FileURI(templatePath), source,
		strings.Index(source, "tit")+len("tit"),
	)
	items := NewAdminCompletionProvider(index).GetCompletions(
		context.Background(), request,
	)
	byName := make(map[string]protocol.CompletionItem, len(items))
	for _, item := range items {
		byName[item.Label] = item
	}
	require.Contains(t, byName, "title")
	require.Contains(t, byName, "$t")
	require.Contains(t, byName, "Object")
	assert.Contains(t, byName["$t"].Detail, "Vue template instance")
	assert.Contains(t, byName["$t"].Documentation.Value, "component instance")
	assert.Contains(t, byName["Object"].Detail, "JavaScript template global")
	assert.Contains(t, byName["Object"].Documentation.Value, "ObjectConstructor")

	blockSource := `{% block sw_card_row %}{{ it }}{% endblock %}`
	_, blockRequest := twigCompletionAt(
		uriutil.FileURI(templatePath), blockSource,
		strings.Index(blockSource, "it")+len("it"),
	)
	blockItems := NewAdminCompletionProvider(index).GetCompletions(
		context.Background(), blockRequest,
	)
	blockByName := make(map[string]protocol.CompletionItem, len(blockItems))
	for _, item := range blockItems {
		blockByName[item.Label] = item
	}
	require.Contains(t, blockByName, "item")
	assert.Contains(t, blockByName["item"].Detail, "Twig block scope")
	assert.Contains(t, blockByName["item"].Documentation.Value, "Product")
}
