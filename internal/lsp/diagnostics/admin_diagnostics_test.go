package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseJS(t *testing.T, code string) *jssyntax.Node {
	t.Helper()
	return javascriptparser.Parse(code).Tree.Root
}

func TestAdminAnalyzer_UndefinedParent(t *testing.T) {
	tempDir := t.TempDir()

	// Create the admin indexer
	adminIndexer, err := admin.NewAdminComponentIndexer(tempDir)
	require.NoError(t, err)
	defer func() { _ = adminIndexer.Close() }()

	provider := &AdminAnalyzer{
		adminIndexer: adminIndexer,
	}

	tests := []struct {
		name            string
		code            string
		uri             string
		expectDiagCount int
		expectMessage   string
	}{
		{
			name:            "undefined parent component",
			code:            `Component.extend('my-component', 'sw-undefined-parent', () => import('./index'));`,
			uri:             "file:///project/src/Resources/app/administration/src/main.js",
			expectDiagCount: 1,
			expectMessage:   "Parent component 'sw-undefined-parent' is not registered",
		},
		{
			name:            "Component.register should not warn",
			code:            `Component.register('my-component', () => import('./index'));`,
			uri:             "file:///project/src/Resources/app/administration/src/main.js",
			expectDiagCount: 0,
		},
		{
			name:            "Shopware.Component.extend with undefined parent",
			code:            `Shopware.Component.extend('my-component', 'sw-missing', () => import('./index'));`,
			uri:             "file:///project/src/Resources/app/administration/src/main.js",
			expectDiagCount: 1,
			expectMessage:   "Parent component 'sw-missing' is not registered",
		},
		{
			name:            "non-administration file should be ignored",
			code:            `Component.extend('my-component', 'sw-undefined-parent', () => import('./index'));`,
			uri:             "file:///project/src/other/main.js",
			expectDiagCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diagnostics, err := provider.Analyze(context.Background(), diagnosticsDocument(tt.uri, []byte(tt.code)))
			require.NoError(t, err)

			assert.Len(t, diagnostics, tt.expectDiagCount)

			if tt.expectDiagCount > 0 && tt.expectMessage != "" {
				assert.Equal(t, tt.expectMessage, diagnostics[0].Message)
			}
		})
	}
}

func TestAdminAnalyzer_DefinedParent(t *testing.T) {
	tempDir := t.TempDir()

	// Create the admin indexer
	adminIndexer, err := admin.NewAdminComponentIndexer(tempDir)
	require.NoError(t, err)
	defer func() { _ = adminIndexer.Close() }()

	// First, index a parent component
	parentCode := `Component.register('sw-button', () => import('./index'));`
	parentFilePath := filepath.Join(tempDir, "src", "Resources", "app", "administration", "src", "component", "sw-button", "index.js")
	err = adminIndexer.Index(indexer.NewParsedFile(parentFilePath, []byte(parentCode)))
	require.NoError(t, err)

	provider := &AdminAnalyzer{
		adminIndexer: adminIndexer,
	}

	// Now test extending the registered component - should not produce diagnostics
	code := `Component.extend('my-button', 'sw-button', () => import('./index'));`
	uri := "file:///project/src/Resources/app/administration/src/main.js"
	diagnostics, err := provider.Analyze(context.Background(), diagnosticsDocument(uri, []byte(code)))
	require.NoError(t, err)

	assert.Empty(t, diagnostics, "Should not produce diagnostics when parent component is registered")
}

func TestAdminAnalyzerValidatesVueSFCTemplateAgainstIndexedContracts(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-button", FilePath: filepath.Join(adminRoot, "sw-button/index.ts"),
		Props: []admin.VueComponentProp{{
			Name: "label", Type: "String", Required: true,
		}},
	}))
	componentPath := filepath.Join(adminRoot, "sw-host/index.vue")
	source := `<template><sw-button>{{ titel }}</sw-button></template>
<script setup lang="ts">
const props = defineProps({ title: { type: String, required: true } });
</script>`
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		componentPath, []byte(source),
	)))
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "sw-host/index.ts"),
		[]byte(`Shopware.Component.register('sw-host', () => import('./index.vue'));`),
	)))

	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(uriutil.FileURI(componentPath), []byte(source)),
	)
	require.NoError(t, err)
	codes := make(map[lsp.DiagnosticID]bool)
	for _, problem := range problems {
		codes[problem.ID] = true
	}
	assert.True(t, codes["admin.component.missing-required-prop"])
	assert.True(t, codes["admin.component.unknown-template-member"])
}

func TestAdminAnalyzerUsesUnsavedLegacyDefinitionAndTwig(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "component/index.js")
	componentDir := filepath.Join(adminRoot, "component/sw-legacy-live")
	definitionPath := filepath.Join(componentDir, "index.js")
	templatePath := filepath.Join(componentDir, "sw-legacy-live.html.twig")
	require.NoError(t, os.MkdirAll(componentDir, 0o755))
	persistedTemplate := `<slot name="old-slot" />`
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
		require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
			path, []byte(source),
		)))
	}

	liveDefinition := `import template from './sw-legacy-live.html.twig';
export default {
    template,
    props: { liveRequired: { type: Number, required: true } },
    data() { return { liveMember: { id: 'draft' } }; },
};`
	liveDefinitionDocument := diagnosticsDocument(
		uriutil.FileURI(definitionPath), []byte(liveDefinition),
	)
	adminIndexer.UpdateLiveDocument(
		definitionPath, liveDefinitionDocument.SyntaxTree.Root,
		liveDefinition, liveDefinitionDocument.LineIndex,
	)
	liveTemplate := `<slot name="live-slot" :record="liveMember" />
{{ liveMember.id }}`
	liveTemplateDocument := diagnosticsDocument(
		uriutil.FileURI(templatePath), []byte(liveTemplate),
	)
	adminIndexer.UpdateLiveDocument(
		templatePath, liveTemplateDocument.SyntaxTree.Root,
		liveTemplate, liveTemplateDocument.LineIndex,
	)

	consumer := []byte(`<sw-legacy-live live-requird="1">
    <template #live-solt />
</sw-legacy-live>`)
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")),
			consumer,
		),
	)
	require.NoError(t, err)
	codes := make(map[lsp.DiagnosticID]bool)
	for _, problem := range problems {
		codes[problem.ID] = true
	}
	assert.True(t, codes["admin.component.missing-required-prop"])
	assert.True(t, codes["admin.component.unknown-prop"])
	assert.True(t, codes["admin.component.unknown-slot"])

	ownerProblems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(), liveTemplateDocument,
	)
	require.NoError(t, err)
	for _, problem := range ownerProblems {
		assert.NotEqual(
			t, lsp.DiagnosticID("admin.component.unknown-template-member"),
			problem.ID,
		)
	}

	adminIndexer.RemoveLiveDocument(definitionPath)
	adminIndexer.RemoveLiveDocument(templatePath)
	fallback := []byte(`<sw-legacy-live old-required="saved">
    <template #old-slot />
</sw-legacy-live>`)
	fallbackProblems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")),
			fallback,
		),
	)
	require.NoError(t, err)
	for _, problem := range fallbackProblems {
		assert.NotEqual(
			t, lsp.DiagnosticID("admin.component.missing-required-prop"),
			problem.ID,
		)
		assert.NotEqual(t, lsp.DiagnosticID("admin.component.unknown-prop"), problem.ID)
		assert.NotEqual(t, lsp.DiagnosticID("admin.component.unknown-slot"), problem.ID)
	}
}

func TestAdminAnalyzerUsesImportedScriptSetupPropContract(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	componentDir := filepath.Join(adminRoot, "component/sw-imported-card")
	componentPath := filepath.Join(componentDir, "index.vue")
	componentSource := `<template>{{ heading }}<slot name="header" :item="heading" /></template>
<script setup lang="ts">
import type { CardProps, CardSlots } from './contracts';
const { optionalTitle: heading = 'fallback' } = defineProps<CardProps>();
defineSlots<CardSlots>();
</script>`
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		componentPath, []byte(componentSource),
	)))
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(componentDir, "index.ts"),
		[]byte(`Shopware.Component.register('sw-imported-card', () => import('./index.vue'));`),
	)))
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(componentDir, "contracts.ts"),
		[]byte(`export interface CardProps { mode: string; optionalTitle?: string }
export interface CardSlots { header(props: { item: string }): unknown }`),
	)))
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(componentDir, "draft-contracts.ts"),
		[]byte(`export interface DraftProfile { city: string; zip: string }
export interface DraftProps { profile: DraftProfile }`),
	)))
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(componentDir, "DraftPanel.vue"),
		[]byte(`<template><slot /></template>
<script setup lang="ts">
defineProps<{ label: string; count?: number }>();
defineSlots<{ default(props: { item: string; active: boolean }): unknown }>();
</script>`),
	)))

	source := []byte(`<sw-imported-card mdoe="wrong">
    <template #header="{ itme }" />
    <template #haeder />
</sw-imported-card>`)
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")), source,
		),
	)
	require.NoError(t, err)
	codes := make(map[lsp.DiagnosticID]bool)
	for _, problem := range problems {
		codes[problem.ID] = true
	}
	assert.True(t, codes["admin.component.missing-required-prop"])
	assert.True(t, codes["admin.component.unknown-prop"])
	assert.True(t, codes["admin.component.unknown-slot"])
	assert.True(t, codes["admin.component.unknown-slot-prop"])

	liveComponentSource := strings.ReplaceAll(
		componentSource, "heading", "headline",
	)
	componentProblems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(componentPath), []byte(liveComponentSource),
		),
	)
	require.NoError(t, err)
	for _, problem := range componentProblems {
		assert.NotEqual(
			t, lsp.DiagnosticID("admin.component.unknown-template-member"),
			problem.ID,
		)
	}

	liveImportedSource := `<template>{{ profile.ctiy }}</template>
<script setup lang="ts">
import type { DraftProps } from './draft-contracts';
const { profile } = defineProps<DraftProps>();
</script>`
	liveImportedProblems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(componentPath), []byte(liveImportedSource),
		),
	)
	require.NoError(t, err)
	foundUnknownNested := false
	for _, problem := range liveImportedProblems {
		if problem.ID == "admin.component.unknown-vue-member" {
			foundUnknownNested = true
			assert.Contains(t, problem.Message, "profile.ctiy")
		}
	}
	assert.True(t, foundUnknownNested)

	liveLocalSource := `<template>
    <DraftPanel lable="wrong">
        <template #default="{ itme }" />
        <template #defualt />
    </DraftPanel>
</template>
<script setup lang="ts">
import DraftPanel from './DraftPanel.vue';
</script>`
	liveLocalProblems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(componentPath), []byte(liveLocalSource),
		),
	)
	require.NoError(t, err)
	liveLocalCodes := make(map[lsp.DiagnosticID]bool)
	for _, problem := range liveLocalProblems {
		liveLocalCodes[problem.ID] = true
		assert.NotEqual(t, lsp.DiagnosticID("admin.component.not-found"), problem.ID)
	}
	assert.True(t, liveLocalCodes["admin.component.missing-required-prop"])
	assert.True(t, liveLocalCodes["admin.component.unknown-prop"])
	assert.True(t, liveLocalCodes["admin.component.unknown-slot"])
	assert.True(t, liveLocalCodes["admin.component.unknown-slot-prop"])

	draftPanelPath := filepath.Join(componentDir, "DraftPanel.vue")
	liveChildSource := `<template><slot name="preview" /></template>
<script setup lang="ts">
defineProps<{ draftLabel: string }>();
defineSlots<{ preview(props: { record: { id: string } }): unknown }>();
</script>`
	liveChildDocument := diagnosticsDocument(
		uriutil.FileURI(draftPanelPath), []byte(liveChildSource),
	)
	adminIndexer.UpdateLiveDocument(
		draftPanelPath, liveChildDocument.SyntaxTree.Root, liveChildSource,
		liveChildDocument.LineIndex,
	)
	liveChildConsumer := `<template>
	    <DraftPanel draft-lable="value"><template #preveiw /></DraftPanel>
</template>
<script setup lang="ts">import DraftPanel from './DraftPanel.vue';</script>`
	liveChildProblems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(componentPath), []byte(liveChildConsumer),
		),
	)
	require.NoError(t, err)
	liveChildCodes := make(map[lsp.DiagnosticID]bool)
	for _, problem := range liveChildProblems {
		liveChildCodes[problem.ID] = true
		assert.NotEqual(t, lsp.DiagnosticID("admin.component.not-found"), problem.ID)
	}
	assert.True(t, liveChildCodes["admin.component.missing-required-prop"])
	assert.True(t, liveChildCodes["admin.component.unknown-prop"])
	assert.True(t, liveChildCodes["admin.component.unknown-slot"])

	adminIndexer.RemoveLiveDocument(draftPanelPath)
	fallbackConsumer := `<template>
	    <DraftPanel label="persisted"><template #default /></DraftPanel>
</template>
<script setup lang="ts">import DraftPanel from './DraftPanel.vue';</script>`
	fallbackProblems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(componentPath), []byte(fallbackConsumer),
		),
	)
	require.NoError(t, err)
	fallbackCodes := make(map[lsp.DiagnosticID]bool)
	for _, problem := range fallbackProblems {
		fallbackCodes[problem.ID] = true
	}
	assert.False(t, fallbackCodes["admin.component.unknown-prop"])
	assert.False(t, fallbackCodes["admin.component.unknown-slot"])
}

func TestAdminAnalyzerReportsDeprecatedComponentsAndProps(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-legacy", FilePath: filepath.Join(adminRoot, "sw-legacy.ts"),
		Deprecated: "tag:v6.8.0 - Use mt-modern instead.",
		Props: []admin.VueComponentProp{
			{Name: "oldValue", Type: "String", Deprecated: "Use modernValue instead."},
			{Name: "active", Type: "Boolean"},
		},
	}))
	source := []byte(
		`<sw-legacy :old-value="value" :active="true" v-bind="{ oldValue: fallback }"></sw-legacy>`,
	)
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")), source,
		),
	)
	require.NoError(t, err)
	var deprecated []lsp.Problem
	for _, problem := range problems {
		if problem.ID == "admin.component.deprecated" ||
			problem.ID == "admin.component.deprecated-prop" {
			deprecated = append(deprecated, problem)
		}
	}
	require.Len(t, deprecated, 3)
	byText := make(map[string]lsp.Problem, len(deprecated))
	for _, problem := range deprecated {
		byText[string(source[problem.Range.Start:problem.Range.End])] = problem
		assert.Equal(t, protocol.DiagnosticSeverityHint, problem.Severity)
		assert.Equal(
			t, []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated}, problem.Tags,
		)
	}
	assert.Equal(t, "admin.component.deprecated", string(byText["sw-legacy"].ID))
	assert.Equal(t, "admin.component.deprecated-prop", string(byText["old-value"].ID))
	assert.Equal(t, "admin.component.deprecated-prop", string(byText["oldValue"].ID))
	assert.NotContains(t, byText, "active")
}

func TestAdminAnalyzerRequiresAllDynamicContractsToDeprecateProp(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-a", FilePath: filepath.Join(adminRoot, "a.ts"),
			Props: []admin.VueComponentProp{{
				Name: "title", Type: "String", Deprecated: "Use heading instead.",
			}},
		},
		{
			Name: "sw-b", FilePath: filepath.Join(adminRoot, "b.ts"),
			Props: []admin.VueComponentProp{{Name: "title", Type: "String"}},
		},
	} {
		require.NoError(t, adminIndexer.SaveComponent(component))
	}
	source := []byte(
		`<component :is="active ? 'sw-a' : 'sw-b'" :title="title"></component>`,
	)
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")), source,
		),
	)
	require.NoError(t, err)
	for _, problem := range problems {
		assert.NotEqual(t, "admin.component.deprecated-prop", string(problem.ID))
	}
}

func TestAdminAnalyzerReportsDeprecatedMembersInScriptAndMarkup(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(root, "Resources/app/administration/src")
	definitionPath := filepath.Join(adminRoot, "sw-card.ts")
	templatePath := filepath.Join(adminRoot, "sw-card.html.twig")
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: definitionPath,
		DefinitionPath: definitionPath, TemplatePath: templatePath,
		Methods: []string{"legacySave"},
		Members: []admin.VueComponentMember{{
			Name: "legacySave", Kind: admin.ComponentMemberMethod,
			FilePath: definitionPath, Line: 8,
			Deprecated: "Use save instead.",
		}},
	}))
	analyzer := NewAdminAnalyzer(adminIndexer)

	markup := []byte(`{{ legacySave() }}<button @click="legacySave()"></button>`)
	markupProblems, err := analyzer.Analyze(
		context.Background(),
		diagnosticsDocument(uriutil.FileURI(templatePath), markup),
	)
	require.NoError(t, err)
	var markupDeprecated []lsp.Problem
	for _, problem := range markupProblems {
		if problem.ID == "admin.component.deprecated-member" {
			markupDeprecated = append(markupDeprecated, problem)
		}
	}
	require.Len(t, markupDeprecated, 2)
	for _, problem := range markupDeprecated {
		assert.Equal(t, protocol.DiagnosticSeverityHint, problem.Severity)
		assert.Equal(
			t, []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
			problem.Tags,
		)
		assert.Equal(t, "legacySave", string(markup[problem.Range.Start:problem.Range.End]))
	}

	script := []byte(`export default {
    methods: { current() { this.legacySave(); } },
};`)
	scriptProblems, err := analyzer.Analyze(
		context.Background(),
		diagnosticsDocument(uriutil.FileURI(definitionPath), script),
	)
	require.NoError(t, err)
	var scriptDeprecated []lsp.Problem
	for _, problem := range scriptProblems {
		if problem.ID == "admin.component.deprecated-member" {
			scriptDeprecated = append(scriptDeprecated, problem)
		}
	}
	require.Len(t, scriptDeprecated, 1)
	assert.Equal(
		t, "legacySave",
		string(script[scriptDeprecated[0].Range.Start:scriptDeprecated[0].Range.End]),
	)
}

func TestAdminAnalyzerCustomDirectiveDiagnostics(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "app/directive/tooltip.directive.ts"),
		[]byte(`Shopware.Directive.register('tooltip', {});`),
	)))
	templatePath := filepath.Join(adminRoot, "consumer.html.twig")
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-consumer", FilePath: filepath.Join(adminRoot, "consumer.ts"),
		TemplatePath:    templatePath,
		LocalDirectives: []admin.VueLocalDirective{{Name: "hide"}},
	}))
	source := []byte(`<div v-tooltip.bottom="message" v-if="visible"></div>
<div v-completely-custom="value"></div>
<div v-hide="value"></div>
<div v-hied="value"></div>
<div v-tooltpi="message"></div>`)
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(templatePath),
			source,
		),
	)
	require.NoError(t, err)
	require.Len(t, problems, 2)
	byName := make(map[string]lsp.Problem, len(problems))
	for _, problem := range problems {
		byName[string(source[problem.Range.Start:problem.Range.End])] = problem
	}
	require.Contains(t, byName, "tooltpi")
	assert.Contains(t, byName["tooltpi"].Message, "v-tooltpi")
	assert.Equal(t, []string{"tooltip"}, byName["tooltpi"].Payload.(map[string]any)["suggestions"])
	require.Contains(t, byName, "hied")
	assert.Equal(t, []string{"hide"}, byName["hied"].Payload.(map[string]any)["suggestions"])

	problems, err = NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")),
			[]byte(`Shopware.Directive.getByName('tooltpi');`),
		),
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, "admin.directive.not-found", string(problems[0].ID))
	assert.Equal(t, []string{"tooltip"}, problems[0].Payload.(map[string]any)["suggestions"])
}

func TestAdminAnalyzerRegistryLookupDiagnostics(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "main.ts"),
		[]byte(`
Shopware.Component.register('sw-card', {});
Shopware.Module.register('sw-product', { routes: {} });
Shopware.Filter.register('currency', value => value);`),
	)))
	source := []byte(`
Shopware.Component.getComponentRegistry().get('sw-card');
Shopware.Module.getModuleRegistry().get('sw-product');
Shopware.Component.getComponentRegistry().has('sw-optional');
Shopware.Module.getModuleRegistry().has('sw-optional');
Shopware.Component.getComponentRegistry().get('sw-cadr');
Shopware.Module.getModuleRegistry().get('sw-prduct');
Shopware.Filter.getByName('currncy');`)
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file://"+filepath.Join(adminRoot, "consumer.ts"),
			source,
		),
	)
	require.NoError(t, err)
	require.Len(t, problems, 3)
	byID := make(map[string]lsp.Problem, len(problems))
	for _, problem := range problems {
		byID[string(problem.ID)] = problem
	}
	componentProblem := byID["admin.component.registry-not-found"]
	assert.Contains(t, componentProblem.Message, "sw-cadr")
	assert.Contains(
		t,
		componentProblem.Payload.(map[string]any)["suggestions"],
		"sw-card",
	)
	moduleProblem := byID["admin.module.not-found"]
	assert.Contains(t, moduleProblem.Message, "sw-prduct")
	assert.Contains(
		t,
		moduleProblem.Payload.(map[string]any)["suggestions"],
		"sw-product",
	)
	filterProblem := byID["admin.filter.not-found"]
	assert.Contains(t, filterProblem.Message, "currncy")
	assert.Contains(
		t,
		filterProblem.Payload.(map[string]any)["suggestions"],
		"currency",
	)
}

func TestAdminAnalyzerCMSRegistryDiagnostics(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "cms.ts"),
		[]byte(`
Shopware.Component.register('sw-cms-el-hero', {});
cmsService.registerCmsElement({ name: 'hero', component: 'sw-cms-el-hero' });
cmsService.registerCmsBlock({ name: 'hero-grid', slots: { content: { type: 'hero' } } });`),
	)))
	source := []byte(`
cmsService.getCmsElementConfigByName('hero');
cmsService.getCmsBlockConfigByName('hero-grid');
cmsService.getCmsElementConfigByName('herp');
cmsService.getCmsBlockConfigByName('hero-gird');
cmsService.registerCmsBlock({ name: 'other', slots: { content: { type: 'herp' } } });
cmsService.registerCmsElement({ name: 'other', component: 'sw-cms-el-herp' });`)
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")), source,
		),
	)
	require.NoError(t, err)
	require.Len(t, problems, 4)
	counts := make(map[lsp.DiagnosticID]int)
	for _, problem := range problems {
		counts[problem.ID]++
		assert.NotEmpty(t, problem.Payload.(map[string]any)["suggestions"])
	}
	assert.Equal(t, 2, counts["admin.cms-element.not-found"])
	assert.Equal(t, 1, counts["admin.cms-block.not-found"])
	assert.Equal(t, 1, counts["admin.component.registry-not-found"])
}

func TestExtractStringContent(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name:     "single quoted string",
			code:     `'hello'`,
			expected: "hello",
		},
		{
			name:     "double quoted string",
			code:     `"world"`,
			expected: "world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stringNodes := jsquery.Nodes(parseJS(t, tt.code), jssyntax.JsString)
			require.NotEmpty(t, stringNodes, "Should find string node")
			result := jsquery.StringValue(stringNodes[0])
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAdminAnalyzer_MissingRequiredProps(t *testing.T) {
	tempDir := t.TempDir()

	// Create the admin indexer
	adminIndexer, err := admin.NewAdminComponentIndexer(tempDir)
	require.NoError(t, err)
	defer func() { _ = adminIndexer.Close() }()

	// Register component with inline definition that has required props
	compCode := `
Component.register('sw-button', {
	props: {
		label: {
			type: String,
			required: true,
		},
		disabled: {
			type: Boolean,
			required: false,
		},
		variant: {
			type: String,
			required: true,
		},
	},
});
`
	compFilePath := filepath.Join(tempDir, "src", "Resources", "app", "administration", "src", "component", "sw-button", "index.js")
	err = adminIndexer.Index(indexer.NewParsedFile(compFilePath, []byte(compCode)))
	require.NoError(t, err)

	// Verify component was indexed correctly
	comps, err := adminIndexer.GetComponentWithDefinition("sw-button")
	require.NoError(t, err)
	require.Len(t, comps, 1, "Component should be indexed")
	require.Len(t, comps[0].Props, 3, "Component should have 3 props")

	provider := &AdminAnalyzer{
		adminIndexer: adminIndexer,
	}

	tests := []struct {
		name            string
		twigCode        string
		expectDiagCount int
		expectProps     []string // props that should be reported as missing
	}{
		{
			name:            "missing all required props",
			twigCode:        `<sw-button></sw-button>`,
			expectDiagCount: 2,
			expectProps:     []string{"label", "variant"},
		},
		{
			name:            "missing one required prop",
			twigCode:        `<sw-button label="Click me"></sw-button>`,
			expectDiagCount: 1,
			expectProps:     []string{"variant"},
		},
		{
			name:            "all required props present",
			twigCode:        `<sw-button label="Click me" variant="primary"></sw-button>`,
			expectDiagCount: 0,
		},
		{
			name:            "required prop with Vue binding",
			twigCode:        `<sw-button :label="buttonLabel" variant="primary"></sw-button>`,
			expectDiagCount: 0,
		},
		{
			name:            "required prop with v-bind",
			twigCode:        `<sw-button v-bind:label="buttonLabel" variant="primary"></sw-button>`,
			expectDiagCount: 0,
		},
		{
			name:            "required props in object v-bind",
			twigCode:        `<sw-button v-bind="{ label: buttonLabel, variant }"></sw-button>`,
			expectDiagCount: 0,
		},
		{
			name:            "missing prop remains provable in exact object v-bind",
			twigCode:        `<sw-button v-bind="{ label: buttonLabel }"></sw-button>`,
			expectDiagCount: 1,
			expectProps:     []string{"variant"},
		},
		{
			name:            "empty object v-bind does not hide required props",
			twigCode:        `<sw-button v-bind="{}"></sw-button>`,
			expectDiagCount: 2,
			expectProps:     []string{"label", "variant"},
		},
		{
			name:            "runtime object v-bind is conservative",
			twigCode:        `<sw-button v-bind="filteredAttributes"></sw-button>`,
			expectDiagCount: 0,
		},
		{
			name:            "spread object v-bind is conservative",
			twigCode:        `<sw-button v-bind="{ label, ...forwarded }"></sw-button>`,
			expectDiagCount: 0,
		},
		{
			name:            "non-required prop missing is ok",
			twigCode:        `<sw-button label="Click" variant="primary"></sw-button>`,
			expectDiagCount: 0,
		},
		{
			name:            "unknown Shopware component warns",
			twigCode:        `<sw-butotn required-prop="value"></sw-butotn>`,
			expectDiagCount: 1,
		},
		{
			name:            "unknown custom element remains conservative",
			twigCode:        `<plugin-unknown required-prop="value"></plugin-unknown>`,
			expectDiagCount: 0,
		},
		{
			name:            "standard HTML elements ignored",
			twigCode:        `<div class="test"></div>`,
			expectDiagCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri := "file:///project/src/Resources/app/administration/src/views/test.html.twig"
			diagnostics, err := provider.Analyze(context.Background(), diagnosticsDocument(uri, []byte(tt.twigCode)))
			require.NoError(t, err)

			assert.Len(t, diagnostics, tt.expectDiagCount, "Unexpected number of diagnostics")

			// Check that the expected props are reported
			for _, expectedProp := range tt.expectProps {
				found := false
				for _, diag := range diagnostics {
					if data, ok := diag.Payload.(map[string]any); ok {
						if data["propName"] == expectedProp {
							found = true
							break
						}
					}
				}
				assert.True(t, found, "Expected diagnostic for missing prop '%s'", expectedProp)
			}
		})
	}
}

func TestAdminAnalyzer_UnknownShopwareComponent(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(
		filepath.Join(root, "cache"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "component/sw-button/index.js"),
		[]byte(`Shopware.Component.register('sw-button', {});`),
	)))

	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file://"+filepath.Join(adminRoot, "view.html.twig"),
			[]byte(`<sw-butotn></sw-butotn><plugin-widget></plugin-widget>`),
		),
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, "admin.component.not-found", string(problems[0].ID))
	assert.Contains(t, problems[0].Message, "sw-butotn")
	assert.Contains(
		t,
		problems[0].Payload.(map[string]any)["suggestions"],
		"sw-button",
	)
}

func TestAdminAnalyzer_UnknownShopwareComponentNeedsIndexedCatalog(
	t *testing.T,
) {
	adminIndexer, err := admin.NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/Resources/app/administration/src/view.html.twig",
			[]byte(`<sw-unknown></sw-unknown>`),
		),
	)
	require.NoError(t, err)
	assert.Empty(t, problems)
}

func TestAdminAnalyzer_OptionsAPILocalComponentIsTemplateScoped(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(
		filepath.Join(root, "cache"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(
		adminRoot, "component/mt-card/index.ts",
	)
	templatePath := filepath.Join(
		adminRoot, "component/mt-card/mt-card.html.twig",
	)
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		definitionPath,
		[]byte(`
import { MtCard } from '@shopware-ag/meteor-component-library';
import template from './mt-card.html.twig';
export default Shopware.Component.wrapComponentConfig({
    template,
    components: { 'mt-card-original': MtCard },
});
`),
	)))

	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file://"+templatePath,
			[]byte(`<mt-card-original></mt-card-original><mt-other-original></mt-other-original>`),
		),
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, "admin.component.not-found", string(problems[0].ID))
	assert.Contains(t, problems[0].Message, "mt-other-original")
}

func TestAdminAnalyzer_KebabCaseProps(t *testing.T) {
	tempDir := t.TempDir()

	// Create the admin indexer
	adminIndexer, err := admin.NewAdminComponentIndexer(tempDir)
	require.NoError(t, err)
	defer func() { _ = adminIndexer.Close() }()

	compCode := `
Component.register('mt-card', {
	props: {
		positionIdentifier: {
			type: String,
			required: true,
		},
	},
});
`
	compFilePath := filepath.Join(tempDir, "src", "Resources", "app", "administration", "src", "component", "mt-card", "index.js")
	err = adminIndexer.Index(indexer.NewParsedFile(compFilePath, []byte(compCode)))
	require.NoError(t, err)

	provider := &AdminAnalyzer{
		adminIndexer: adminIndexer,
	}

	tests := []struct {
		name            string
		twigCode        string
		expectDiagCount int
	}{
		{
			name:            "camelCase prop in template",
			twigCode:        `<mt-card positionIdentifier="test"></mt-card>`,
			expectDiagCount: 0,
		},
		{
			name:            "kebab-case prop in template",
			twigCode:        `<mt-card position-identifier="test"></mt-card>`,
			expectDiagCount: 0,
		},
		{
			name:            "kebab-case with Vue binding",
			twigCode:        `<mt-card :position-identifier="myVar"></mt-card>`,
			expectDiagCount: 0,
		},
		{
			name:            "missing prop should warn",
			twigCode:        `<mt-card></mt-card>`,
			expectDiagCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri := "file:///project/src/Resources/app/administration/src/views/test.html.twig"
			diagnostics, err := provider.Analyze(context.Background(), diagnosticsDocument(uri, []byte(tt.twigCode)))
			require.NoError(t, err)

			assert.Len(t, diagnostics, tt.expectDiagCount, "Unexpected number of diagnostics")
		})
	}
}

func TestAdminAnalyzer_InheritedRequiredProp(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root,
		"src/Resources/app/administration/src/component",
	)
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "sw-parent", "index.js"),
		[]byte(`Component.register('sw-parent', {
    props: { parentLabel: { type: String, required: true } },
});`),
	)))
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "sw-child", "index.js"),
		[]byte(`Component.extend('sw-child', 'sw-parent', {
    props: { childLabel: String },
});`),
	)))

	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/Resources/app/administration/src/view.html.twig",
			[]byte(`<sw-child child-label="Child"></sw-child>`),
		),
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, "admin.component.missing-required-prop", string(problems[0].ID))
	assert.Equal(t, "parentLabel", problems[0].Payload.(map[string]any)["propName"])
}

func TestAdminAnalyzer_StaticPropTypes(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	definitionPath := filepath.Join(
		root,
		"src/Resources/app/administration/src/component/sw-field/index.js",
	)
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		definitionPath,
		[]byte(`Component.register('sw-field', {
    props: { count: Number, active: Boolean, label: String },
		});`),
	)))
	analyzer := NewAdminAnalyzer(adminIndexer)
	for _, test := range []struct {
		name   string
		markup string
		count  int
	}{
		{"number must be bound", `<sw-field count="5"></sw-field>`, 1},
		{"bound number", `<sw-field :count="5"></sw-field>`, 0},
		{"boolean presence", `<sw-field active></sw-field>`, 0},
		{"boolean empty", `<sw-field active=""></sw-field>`, 0},
		{"boolean string must be bound", `<sw-field active="false"></sw-field>`, 1},
		{"string remains static", `<sw-field label="Name"></sw-field>`, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			problems, err := analyzer.Analyze(
				context.Background(),
				diagnosticsDocument(
					"file:///project/Resources/app/administration/src/view.html.twig",
					[]byte(test.markup),
				),
			)
			require.NoError(t, err)
			assert.Len(t, problems, test.count)
			if test.count != 0 {
				assert.Equal(t, "admin.component.static-prop-type", string(problems[0].ID))
			}
		})
	}
}

func TestAdminAnalyzer_StaticPropAllowedValues(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-label", FilePath: filepath.Join(root, "sw-label/index.js"),
		Props: []admin.VueComponentProp{
			{
				Name: "variant", Type: "String",
				AllowedValues:         []string{"", "primary", "secondary"},
				AllowedValuesComplete: true,
			},
			{Name: "size", Type: `"small" | "large"`},
			{Name: "legacy", Type: `"known" | LegacyValue`},
		},
	}))
	analyzer := NewAdminAnalyzer(adminIndexer)
	for _, test := range []struct {
		name, markup string
		count        int
	}{
		{"allowed value", `<sw-label variant="primary" />`, 0},
		{"allowed empty value", `<sw-label variant="" />`, 0},
		{"invalid options value", `<sw-label variant="primry" />`, 1},
		{"invalid literal union value", `<sw-label size="medium" />`, 1},
		{"invalid empty literal union value", `<sw-label size="" />`, 1},
		{"invalid bound literal union value", `<sw-label :size="'medium'" />`, 1},
		{"valid bound literal union value", `<sw-label :size="'small'" />`, 0},
		{"invalid object-bound literal union value", `<sw-label v-bind="{ size: 'medium' }" />`, 1},
		{"bound value is runtime", `<sw-label :variant="selectedVariant" />`, 0},
		{"partial union stays conservative", `<sw-label legacy="other" />`, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			problems, analyzeErr := analyzer.Analyze(
				context.Background(), diagnosticsDocument(
					"file:///project/Resources/app/administration/src/view.html.twig",
					[]byte(test.markup),
				),
			)
			require.NoError(t, analyzeErr)
			require.Len(t, problems, test.count)
			if test.count > 0 {
				assert.Equal(
					t, "admin.component.invalid-prop-value",
					string(problems[0].ID),
				)
				payload := problems[0].Payload.(map[string]any)
				assert.NotEmpty(t, payload["allowedValues"])
				if test.name != "invalid empty literal union value" {
					assert.NotEmpty(t, payload["suggestions"])
				}
			}
		})
	}
}

func TestAdminAnalyzer_DynamicComponentContracts(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	definitionPath := filepath.Join(
		root,
		"src/Resources/app/administration/src/component/sw-field/index.js",
	)
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-field", FilePath: definitionPath,
		Props: []admin.VueComponentProp{
			{Name: "title", Type: "String", Required: true},
			{Name: "count", Type: "Number"},
			{Name: "account", Type: "Object"},
		},
	}))
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-other", FilePath: filepath.Join(root, "sw-other/index.js"),
		Props: []admin.VueComponentProp{{Name: "account", Type: "Object"}},
	}))
	templatePath := "/project/Resources/app/administration/src/view.html.twig"
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-host", FilePath: filepath.Join(root, "sw-host/index.js"),
		TemplatePath: templatePath,
		Members: []admin.VueComponentMember{{
			Name: "dynamicField", Kind: admin.ComponentMemberComputed,
			ReturnExpressions: []string{"'sw-field'", "'sw-other'"},
			ReturnsComplete:   true,
		}},
	}))
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(
			root,
			"src/Resources/app/administration/src/module/sw-host/routes.js",
		),
		[]byte(`Shopware.Module.register('sw-host', {
    routes: {
        index: {
            component: 'sw-host',
            children: {
                field: { component: 'sw-field' },
                other: { component: 'sw-other' },
            },
        },
    },
		});`),
	)))
	routes, routeErr := adminIndexer.GetAllModuleRoutes()
	require.NoError(t, routeErr)
	require.Len(t, routes, 3)
	owner, ownerErr := adminIndexer.GetComponentByTemplatePath(templatePath)
	require.NoError(t, ownerErr)
	require.NotNil(t, owner)
	assert.Equal(t, "sw-host", owner.Name)
	routeMarkup := `<router-view v-slot="{ Component }"><component :is="Component" /></router-view>`
	routeDocument := diagnosticsDocument(
		uriutil.FileURI(templatePath), []byte(routeMarkup),
	)
	var routeStartTag *twigsyntax.Node
	var routeSelector admin.VueDynamicComponentSelector
	for _, startTag := range twigquery.Nodes(
		routeDocument.SyntaxTree.Root, twigsyntax.HtmlStartingTag,
	) {
		if selector, dynamic := admin.TwigDynamicComponentSelector(startTag); dynamic {
			routeStartTag = startTag
			routeSelector = selector
			break
		}
	}
	require.NotNil(t, routeStartTag)
	resolvedRouteSelector, routeComponents, routeComplete, resolveErr :=
		adminIndexer.ResolveDynamicComponentContracts(
			templatePath, routeSelector, routeStartTag,
		)
	require.NoError(t, resolveErr)
	require.True(t, routeComplete)
	assert.Equal(t, []string{"sw-field", "sw-other"}, resolvedRouteSelector.Names())
	assert.Len(t, routeComponents, 2)
	analyzer := NewAdminAnalyzer(adminIndexer)
	for _, test := range []struct {
		name, markup, id string
		count            int
	}{
		{
			"single selector enforces required props",
			`<component :is="'sw-field'" />`,
			"admin.component.missing-required-prop", 1,
		},
		{
			"single selector validates prop types",
			`<component :is="'sw-field'" title="Field" count="5" />`,
			"admin.component.static-prop-type", 1,
		},
		{
			"finite union validates every possible component contract",
			`<component :is="active ? 'sw-field' : 'sw-other'" />`,
			"admin.component.missing-required-prop", 1,
		},
		{
			"missing selector candidate",
			`<component :is="'sw-fiel'" />`,
			"admin.component.not-found", 1,
		},
		{
			"inferred union validates every possible component contract",
			`<component :is="dynamicField" />`,
			"admin.component.missing-required-prop", 1,
		},
		{
			"router-view route contract validates required props",
			`<router-view v-slot="{ Component }"><component :is="Component" /></router-view>`,
			"admin.component.missing-required-prop", 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			problems, analyzeErr := analyzer.Analyze(
				context.Background(),
				diagnosticsDocument(
					uriutil.FileURI(templatePath),
					[]byte(test.markup),
				),
			)
			require.NoError(t, analyzeErr)
			require.Len(t, problems, test.count)
			if test.count > 0 {
				assert.Equal(t, test.id, string(problems[0].ID))
			}
		})
	}
}

func TestAdminAnalyzer_BoundPropTypes(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	templatePath := filepath.Join(
		root,
		"Resources/app/administration/src/component/sw-host/view.html.twig",
	)
	definitionPath := filepath.Join(filepath.Dir(templatePath), "index.ts")
	fieldPath := filepath.Join(
		root,
		"Resources/app/administration/src/component/sw-field/index.ts",
	)
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-field", FilePath: fieldPath, DefinitionPath: fieldPath,
		Props: []admin.VueComponentProp{
			{Name: "count", Type: "Number"},
			{Name: "title", Type: "String"},
			{Name: "active", Type: "Boolean"},
			{Name: "rows", Type: "Array as PropType<Row[]>"},
			{Name: "config", Type: "Object as PropType<Config>"},
			{Name: "callback", Type: "Function"},
		},
	}))
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-host", DefinitionPath: definitionPath,
		FilePath: definitionPath, TemplatePath: templatePath,
		Members: []admin.VueComponentMember{
			{Name: "count", Kind: admin.ComponentMemberData, Type: "number"},
			{Name: "title", Kind: admin.ComponentMemberData, Type: "string"},
			{Name: "rows", Kind: admin.ComponentMemberData, Type: "Row[]"},
			{Name: "config", Kind: admin.ComponentMemberData, Type: "{ enabled: boolean }"},
			{Name: "callback", Kind: admin.ComponentMemberMethod, Type: "() => void"},
			{Name: "maybeTitle", Kind: admin.ComponentMemberData, Type: "string | null"},
			{Name: "mutableValue", Kind: admin.ComponentMemberData, Type: "boolean"},
		},
		Assignments: []admin.VueComponentAssignment{{
			Target: "mutableValue", Expression: "nextValue",
		}},
	}))
	analyzer := NewAdminAnalyzer(adminIndexer)
	for _, test := range []struct {
		name, markup     string
		count            int
		expected, actual string
	}{
		{name: "literal mismatch", markup: `<sw-field :count="'wrong'" />`, count: 1, expected: "number", actual: "string"},
		{name: "component member mismatch", markup: `<sw-field :active="title" />`, count: 1, expected: "boolean", actual: "string"},
		{name: "array mismatch", markup: `<sw-field :rows="title" />`, count: 1, expected: "Row[]", actual: "string"},
		{name: "typed object runtime mismatch", markup: `<sw-field :config="rows" />`, count: 1, expected: "Config", actual: "Row[]"},
		{name: "function mismatch", markup: `<sw-field :callback="count" />`, count: 1, expected: "Function", actual: "number"},
		{name: "compatible members", markup: `<sw-field :count="count" :title="title" :rows="rows" :config="config" :callback="callback" />`},
		{name: "compatible object binding members", markup: `<sw-field v-bind="{ count, title, rows, config, callback }" />`},
		{name: "object binding member mismatch", markup: `<sw-field v-bind="{ count: title }" />`, count: 1, expected: "number", actual: "string"},
		{name: "object binding spread keeps static siblings", markup: `<sw-field v-bind="{ ...runtimeProps, count }" />`},
		{name: "nullable union stays conservative", markup: `<sw-field :title="maybeTitle" />`},
		{name: "unresolved expression stays conservative", markup: `<sw-field :count="runtimeValue" />`},
		{name: "mutable untyped data stays conservative", markup: `<sw-field :count="mutableValue" />`},
	} {
		t.Run(test.name, func(t *testing.T) {
			problems, analyzeErr := analyzer.Analyze(
				context.Background(), diagnosticsDocument(
					"file://"+templatePath, []byte(test.markup),
				),
			)
			require.NoError(t, analyzeErr)
			require.Len(t, problems, test.count)
			if test.count == 0 {
				return
			}
			problem := problems[0]
			assert.Equal(
				t, "admin.component.bound-prop-type", string(problem.ID),
			)
			payload := problem.Payload.(map[string]any)
			assert.Equal(t, test.expected, payload["expectedType"])
			assert.Equal(t, test.actual, payload["actualType"])
		})
	}
}

func TestAdminAnalyzer_UnknownComponentContractAttributes(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	templatePath := filepath.Join(
		root, "Resources/app/administration/src/component/sw-host/view.html.twig",
	)
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-field", FilePath: filepath.Join(root, "sw-field.ts"),
		Props: []admin.VueComponentProp{
			{Name: "label", Type: "String"},
			{Name: "isLoading", Type: "Boolean"},
			{Name: "count", Type: "Number"},
			{Name: "checked", Type: "Boolean"},
		},
		Events: []admin.VueComponentEvent{
			{Name: "itemClick"}, {Name: "update:checked"},
		},
	}))
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-secondary", FilePath: filepath.Join(root, "sw-secondary.ts"),
		Props: []admin.VueComponentProp{{Name: "secondary", Type: "String"}},
	}))
	analyzer := NewAdminAnalyzer(adminIndexer)
	for _, test := range []struct {
		name,
		markup,
		code,
		rangeText,
		suggestion string
		count int
	}{
		{
			name: "static prop typo", markup: `<sw-field lable="Title" />`,
			code: "admin.component.unknown-prop", rangeText: "lable",
			suggestion: "label", count: 1,
		},
		{
			name:   "bound prop typo preserves prefix and modifier",
			markup: `<sw-field :is-laoding.sync="ready" />`,
			code:   "admin.component.unknown-prop", rangeText: "is-laoding",
			suggestion: "is-loading", count: 1,
		},
		{
			name:   "long bound prop typo",
			markup: `<sw-field v-bind:lable.prop="title" />`,
			code:   "admin.component.unknown-prop", rangeText: "lable",
			suggestion: "label", count: 1,
		},
		{
			name:   "object binding prop typo",
			markup: `<sw-field v-bind="{ lable: title }" />`,
			code:   "admin.component.unknown-prop", rangeText: "lable",
			suggestion: "label", count: 1,
		},
		{
			name:   "quoted object binding keeps kebab spelling",
			markup: `<sw-field v-bind="{ 'is-laoding': ready }" />`,
			code:   "admin.component.unknown-prop", rangeText: "is-laoding",
			suggestion: "is-loading", count: 1,
		},
		{
			name:   "event typo preserves listener modifier",
			markup: `<sw-field @item-clik.stop="select" />`,
			code:   "admin.component.unknown-event", rangeText: "item-clik",
			suggestion: "item-click", count: 1,
		},
		{
			name:   "long event typo",
			markup: `<sw-field v-on:itemClik.once="select" />`,
			code:   "admin.component.unknown-event", rangeText: "itemClik",
			suggestion: "item-click", count: 1,
		},
		{
			name:   "named model typo preserves modifier",
			markup: `<sw-field v-model:cheked.trim="checked" />`,
			code:   "admin.component.unknown-model", rangeText: "cheked",
			suggestion: "checked", count: 1,
		},
		{
			name:   "known named model",
			markup: `<sw-field v-model:checked.trim="checked" />`,
		},
		{
			name:   "known and fallthrough attributes",
			markup: `<sw-field label="Title" @item-click="select" @click="open" class="field" :data-test="id" aria-label="Field" v-if="visible" />`,
		},
		{
			name:   "distant custom attribute remains conservative",
			markup: `<sw-field mystery-attribute="value" @runtime-event="run" />`,
		},
		{
			name:   "resolved dynamic contracts use their prop union",
			markup: `<component :is="condition ? 'sw-field' : 'sw-secondary'" :lable="title" />`,
			code:   "admin.component.unknown-prop", rangeText: "lable",
			suggestion: "label", count: 1,
		},
		{
			name:   "prop valid on one resolved dynamic candidate",
			markup: `<component :is="condition ? 'sw-field' : 'sw-secondary'" :label="title" />`,
		},
		{
			name:   "runtime dynamic contract remains conservative",
			markup: `<component :is="runtimeComponent" :lable="title" @item-clik="select" />`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := diagnosticsDocument(
				"file://"+templatePath, []byte(test.markup),
			)
			problems, analyzeErr := analyzer.Analyze(
				context.Background(), document,
			)
			require.NoError(t, analyzeErr)
			require.Len(t, problems, test.count)
			if test.count == 0 {
				return
			}
			problem := problems[0]
			assert.Equal(t, test.code, string(problem.ID))
			assert.Equal(
				t, test.rangeText,
				string(document.Text[problem.Range.Start:problem.Range.End]),
			)
			assert.Contains(
				t, problem.Payload.(map[string]any)["suggestions"],
				test.suggestion,
			)
		})
	}
}

func TestAdminAnalyzer_UnknownComponentSlotNames(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	templatePath := filepath.Join(
		root, "Resources/app/administration/src/component/sw-host/view.html.twig",
	)
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: filepath.Join(root, "sw-card.ts"),
		Slots: []admin.VueComponentSlot{
			{Name: "header"}, {Name: "actions"},
			{NamePrefix: "column-"},
		},
	}))
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-secondary", FilePath: filepath.Join(root, "sw-secondary.ts"),
		Slots: []admin.VueComponentSlot{{Name: "actions"}, {Name: "footer"}},
	}))
	analyzer := NewAdminAnalyzer(adminIndexer)
	for _, test := range []struct {
		name,
		markup,
		rangeText,
		suggestion string
		count int
	}{
		{
			name:      "template shorthand typo",
			markup:    `<sw-card><template #heder>Title</template></sw-card>`,
			rangeText: "heder", suggestion: "header", count: 1,
		},
		{
			name:      "long slot typo preserves modifier",
			markup:    `<sw-card><template v-slot:actons.stop>Actions</template></sw-card>`,
			rangeText: "actons", suggestion: "actions", count: 1,
		},
		{
			name:      "direct component slot typo",
			markup:    `<sw-card #heder>Title</sw-card>`,
			rangeText: "heder", suggestion: "header", count: 1,
		},
		{
			name:   "known exact and dynamic family slots",
			markup: `<sw-card><template #header>Title</template><template #column-name>Cell</template></sw-card>`,
		},
		{
			name:   "distant runtime slot remains conservative",
			markup: `<sw-card><template #plugin-content>Content</template></sw-card>`,
		},
		{
			name:      "resolved dynamic owner typo",
			markup:    `<component :is="condition ? 'sw-card' : 'sw-secondary'"><template #heder>Title</template></component>`,
			rangeText: "heder", suggestion: "header", count: 1,
		},
		{
			name:   "slot valid on one resolved dynamic owner",
			markup: `<component :is="condition ? 'sw-card' : 'sw-secondary'"><template #header>Title</template></component>`,
		},
		{
			name:   "runtime dynamic owner remains conservative",
			markup: `<component :is="runtimeComponent"><template #heder>Title</template></component>`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := diagnosticsDocument(
				"file://"+templatePath, []byte(test.markup),
			)
			problems, analyzeErr := analyzer.Analyze(
				context.Background(), document,
			)
			require.NoError(t, analyzeErr)
			require.Len(t, problems, test.count)
			if test.count == 0 {
				return
			}
			problem := problems[0]
			assert.Equal(t, "admin.component.unknown-slot", string(problem.ID))
			assert.Equal(
				t, test.rangeText,
				string(document.Text[problem.Range.Start:problem.Range.End]),
			)
			assert.Contains(
				t, problem.Payload.(map[string]any)["suggestions"],
				test.suggestion,
			)
		})
	}
}

func TestAdminAnalyzer_ModelBindings(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	templatePath := filepath.Join(
		root, "Resources/app/administration/src/component/sw-host/view.html.twig",
	)
	hostPath := filepath.Join(filepath.Dir(templatePath), "index.ts")
	fieldPath := filepath.Join(
		root, "Resources/app/administration/src/component/sw-field/index.ts",
	)
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-field", FilePath: fieldPath, DefinitionPath: fieldPath,
		Props: []admin.VueComponentProp{
			{Name: "modelValue", Type: "Boolean", Required: true},
			{Name: "value", Type: "String", Required: true},
		},
		Events: []admin.VueComponentEvent{
			{Name: "update:modelValue", Type: "(value: boolean) => void"},
			{Name: "update:value", Type: "(value: string) => void"},
		},
	}))
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-host", FilePath: hostPath, DefinitionPath: hostPath,
		TemplatePath: templatePath,
		Members: []admin.VueComponentMember{
			{Name: "enabled", Kind: admin.ComponentMemberData, Type: "boolean"},
			{Name: "title", Kind: admin.ComponentMemberData, Type: "string"},
			{Name: "getValue", Kind: admin.ComponentMemberMethod, Type: "() => boolean"},
		},
	}))
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-partial-model", FilePath: fieldPath + ".partial",
		Props: []admin.VueComponentProp{{
			Name: "element", Type: "Object", Required: true,
		}},
	}))
	analyzer := NewAdminAnalyzer(adminIndexer)
	for _, test := range []struct {
		name, markup, id string
	}{
		{
			name:   "compatible default and named models satisfy required props",
			markup: `<sw-field v-model="enabled" v-model:value="title" />`,
		},
		{
			name: "named model supplies its prop without a declared update event",
			markup: `<component :is="'sw-partial-model'" ` +
				`v-model:element="title" />`,
		},
		{
			name:   "default model type mismatch",
			markup: `<sw-field v-model="title" v-model:value="title" />`,
			id:     "admin.component.model-type",
		},
		{
			name:   "named model type mismatch",
			markup: `<sw-field v-model="enabled" v-model:value="enabled" />`,
			id:     "admin.component.model-type",
		},
		{
			name:   "literal is not writable",
			markup: `<sw-field v-model="true" v-model:value="title" />`,
			id:     "admin.component.model-not-assignable",
		},
		{
			name:   "call is not writable",
			markup: `<sw-field v-model="getValue()" v-model:value="title" />`,
			id:     "admin.component.model-not-assignable",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			problems, analyzeErr := analyzer.Analyze(
				context.Background(), diagnosticsDocument(
					"file://"+templatePath, []byte(test.markup),
				),
			)
			require.NoError(t, analyzeErr)
			if test.id == "" {
				assert.Empty(t, problems)
				return
			}
			require.Len(t, problems, 1)
			assert.Equal(t, test.id, string(problems[0].ID))
		})
	}
}

func TestAdminAnalyzerUnknownWholeSlotObjectMember(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-inherit-wrapper", FilePath: filepath.Join(root, "wrapper.js"),
		Slots: []admin.VueComponentSlot{{
			Name: "content", MembersComplete: true,
			Members: []admin.VueComponentSlotMember{
				{Name: "currentValue", Type: "string"},
				{Name: "isInherited", Type: "boolean"},
			},
		}},
	}))
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-forwarding-wrapper", FilePath: filepath.Join(root, "forward.js"),
		Slots: []admin.VueComponentSlot{{
			Name: "content", MembersComplete: false,
			Members: []admin.VueComponentSlotMember{{Name: "known"}},
		}},
	}))
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-card-a", FilePath: filepath.Join(root, "a.js"),
			Slots: []admin.VueComponentSlot{{
				Name: "content", MembersComplete: true,
				Members: []admin.VueComponentSlotMember{
					{Name: "currentValue", Type: "string"},
					{Name: "onlyA", Type: "boolean"},
				},
			}},
		},
		{
			Name: "sw-card-b", FilePath: filepath.Join(root, "b.js"),
			Slots: []admin.VueComponentSlot{{
				Name: "content", MembersComplete: true,
				Members: []admin.VueComponentSlotMember{{
					Name: "currentValue", Type: "number",
				}},
			}},
		},
	} {
		require.NoError(t, adminIndexer.SaveComponent(component))
	}
	analyzer := NewAdminAnalyzer(adminIndexer)
	for _, test := range []struct {
		name, source string
		count        int
	}{
		{
			name:   "known member",
			source: `<sw-inherit-wrapper><template #content="props">{{ props.currentValue }}</template></sw-inherit-wrapper>`,
		},
		{
			name:   "unknown member",
			source: `<sw-inherit-wrapper><template #content="props">{{ props.curentValue }}</template></sw-inherit-wrapper>`,
			count:  1,
		},
		{
			name:   "unknown destructured member",
			source: `<sw-inherit-wrapper><template #content="{ curentValue }">{{ curentValue }}</template></sw-inherit-wrapper>`,
			count:  1,
		},
		{
			name:   "nested loop shadows slot local",
			source: `<sw-inherit-wrapper><template #content="props"><div v-for="props in rows">{{ props.anything }}</div></template></sw-inherit-wrapper>`,
		},
		{
			name:   "runtime forwarding is incomplete",
			source: `<sw-forwarding-wrapper><template #content="props">{{ props.anything }}</template></sw-forwarding-wrapper>`,
		},
		{
			name: "dynamic common member",
			source: `<component :is="active ? 'sw-card-a' : 'sw-card-b'">` +
				`<template #content="props">{{ props.currentValue }}</template></component>`,
		},
		{
			name: "dynamic candidate-only member",
			source: `<component :is="active ? 'sw-card-a' : 'sw-card-b'">` +
				`<template #content="props">{{ props.onlyA }}</template></component>`,
			count: 1,
		},
		{
			name: "runtime dynamic remains conservative",
			source: `<component :is="runtimeComponent">` +
				`<template #content="props">{{ props.anything }}</template></component>`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			problems, analyzeErr := analyzer.Analyze(
				context.Background(),
				diagnosticsDocument(
					"file:///project/Resources/app/administration/src/view.html.twig",
					[]byte(test.source),
				),
			)
			require.NoError(t, analyzeErr)
			require.Len(t, problems, test.count)
			if test.count == 0 {
				return
			}
			assert.Equal(
				t, "admin.component.unknown-slot-prop", string(problems[0].ID),
			)
			payload := problems[0].Payload.(map[string]any)
			if strings.Contains(test.name, "unknown") {
				assert.Equal(t, "curentValue", payload["memberName"])
				assert.Contains(t, payload["suggestions"], "currentValue")
			} else {
				assert.Equal(t, "onlyA", payload["memberName"])
				assert.ElementsMatch(
					t, []string{"sw-card-a", "sw-card-b"},
					payload["componentNames"],
				)
			}
		})
	}
}

func TestAdminAnalyzerUnknownTypedVueBindingMember(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root, "Resources/app/administration/src/component/sw-view",
	)
	definitionPath := filepath.Join(adminRoot, "index.ts")
	templatePath := filepath.Join(adminRoot, "view.html.twig")
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		definitionPath,
		[]byte(`
interface Manufacturer { name: string; }
interface Product { id: string; name: string; manufacturer: Manufacturer; getManufacturer(): Manufacturer; }
`),
	)))
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-view", FilePath: definitionPath,
		DefinitionPath: definitionPath, TemplatePath: templatePath,
		Members: []admin.VueComponentMember{
			{
				Name: "products", Kind: admin.ComponentMemberComputed,
				Type: "Product[]", FilePath: definitionPath,
			},
			{
				Name: "productsById", Kind: admin.ComponentMemberComputed,
				Type: "Record<string, Product>", FilePath: definitionPath,
			},
			{
				Name: "unknownRows", Kind: admin.ComponentMemberComputed,
				Type: "Array", FilePath: definitionPath,
			},
			{
				Name: "selectedProduct", Kind: admin.ComponentMemberProp,
				Type: "Product", FilePath: definitionPath,
			},
			{
				Name: "cards", Kind: admin.ComponentMemberComputed,
				Type:     "Array<{ id: string; label: string }>",
				FilePath: definitionPath,
			},
		},
	}))
	analyzer := NewAdminAnalyzer(adminIndexer)
	for _, test := range []struct {
		name, source string
		count        int
		suggestion   string
	}{
		{
			name:   "known nested member",
			source: `<div v-for="product in products">{{ product.manufacturer.name }}</div>`,
		},
		{
			name:   "known mapped primitive member",
			source: `<div v-for="name in products?.map((product) => product.name) ?? []">{{ name.length }}</div>`,
		},
		{
			name:       "unknown mapped projection member",
			source:     `<div v-for="card in products.map((product) => ({ label: product.name }))">{{ card.lable }}</div>`,
			count:      1,
			suggestion: "label",
		},
		{
			name:   "unknown direct member",
			source: `<div v-for="product in products">{{ product.manufaturer.name }}</div>`,
			count:  1, suggestion: "manufacturer",
		},
		{
			name:   "unknown nested member",
			source: `<div v-for="product in products">{{ product.manufacturer.naem }}</div>`,
			count:  1, suggestion: "name",
		},
		{
			name:       "unknown record value member",
			source:     `<div v-for="(product, productId, index) in productsById">{{ product.naem }}</div>`,
			count:      1,
			suggestion: "name",
		},
		{
			name:   "known record key and index members",
			source: `<div v-for="(product, productId, index) in productsById">{{ productId.length }} {{ index.toFixed() }}</div>`,
		},
		{
			name:       "unknown Object.values record member",
			source:     `<div v-for="product in Object.values(productsById)">{{ product.naem }}</div>`,
			count:      1,
			suggestion: "name",
		},
		{
			name:   "known Object.keys and literal members",
			source: `<div v-for="productId in Object.keys(productsById)">{{ productId.length }}</div><div v-for="label in ['first']">{{ label.toUpperCase() }}</div>`,
		},
		{
			name:   "known indexed array and Record members",
			source: `<div v-for="(product, index) in products">{{ products[0].manufacturer.name }} {{ products[index].name }} {{ products[index - 1].name }} {{ productsById[selectedProduct.name].name }}</div>`,
		},
		{
			name:       "unknown v-for indexed array member",
			source:     `<div v-for="(product, index) in products">{{ products[index].naem }}</div>`,
			count:      1,
			suggestion: "name",
		},
		{
			name:       "unknown indexed Record member",
			source:     `<div>{{ productsById[selectedProduct.name].naem }}</div>`,
			count:      1,
			suggestion: "name",
		},
		{
			name:   "known component member",
			source: `<div :title="selectedProduct.manufacturer.name"></div>`,
		},
		{
			name:   "known inline object array surface and element",
			source: `<div>{{ cards.length }} {{ cards[0].id }}</div>`,
		},
		{
			name:   "unknown component member",
			source: `<div :title="selectedProduct.naem"></div>`,
			count:  1, suggestion: "name",
		},
		{
			name:   "unknown nested component member",
			source: `<div :title="selectedProduct.manufacturer.naem"></div>`,
			count:  1, suggestion: "name",
		},
		{
			name:   "known called component member",
			source: `<div :title="selectedProduct.getManufacturer().name"></div>`,
		},
		{
			name:   "unknown called component method",
			source: `<div :title="selectedProduct.getManufacturr().name"></div>`,
			count:  1, suggestion: "getManufacturer",
		},
		{
			name:   "unknown called component result member",
			source: `<div :title="selectedProduct.getManufacturer().naem"></div>`,
			count:  1, suggestion: "name",
		},
		{
			name:   "untyped observed binding remains conservative",
			source: `<div v-for="row in unknownRows">{{ row.anything }}</div>`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			problems, analyzeErr := analyzer.Analyze(
				context.Background(), diagnosticsDocument(
					"file://"+templatePath, []byte(test.source),
				),
			)
			require.NoError(t, analyzeErr)
			require.Len(t, problems, test.count)
			if test.count == 0 {
				return
			}
			assert.Equal(
				t, "admin.component.unknown-vue-member",
				string(problems[0].ID),
			)
			payload := problems[0].Payload.(map[string]any)
			assert.Contains(t, payload["suggestions"], test.suggestion)
		})
	}
}

func TestAdminAnalyzerKeepsInferredRuntimeObjectShapesOpen(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	templatePath := filepath.Join(
		root, "Resources/app/administration/src/component/sw-view/view.html.twig",
	)
	definitionPath := filepath.Join(filepath.Dir(templatePath), "index.ts")
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-view", DefinitionPath: definitionPath,
		TemplatePath: templatePath,
		Members: []admin.VueComponentMember{
			{
				Name: "page", Kind: admin.ComponentMemberData,
				Type: "{ sections: Array }", FilePath: definitionPath,
				OpenRuntimeShape: true,
			},
			{
				Name: "declared", Kind: admin.ComponentMemberProp,
				Type: "{ title: string }", FilePath: definitionPath,
			},
		},
	}))

	source := []byte(`<div>{{ page.locked }} {{ declared.titel }}</div>`)
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument("file://"+templatePath, source),
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, "admin.component.unknown-vue-member", string(problems[0].ID))
	assert.Contains(t, problems[0].Message, "titel")
	assert.NotContains(t, problems[0].Message, "locked")
}

func TestAdminAnalyzer_UnknownThisMember(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	definitionPath := filepath.Join(
		root,
		"src/Resources/app/administration/src/component/sw-card/index.js",
	)
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: definitionPath, DefinitionPath: definitionPath,
		Props:    []admin.VueComponentProp{{Name: "title"}},
		Injected: []string{"repositoryFactory"},
	}))
	source := []byte(`export default {
    props: { title: String },
    methods: {
        freshMethod() {},
        run() {
            this.title;
            this.repositoryFactory.create('product');
            this.freshMethod();
            this.$emit('save');
			this.freshMethd;
        },
    },
};`)
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument("file://"+definitionPath, source),
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, "admin.component.unknown-instance-member", string(problems[0].ID))
	assert.Contains(t, problems[0].Message, "freshMethd")
	assert.Contains(
		t, problems[0].Payload.(map[string]any)["suggestions"], "freshMethod",
	)
}

func TestAdminAnalyzerUnknownTemplateRootMemberWithSuggestion(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	templatePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/component/sw-card/sw-card.html.twig",
	)
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-card", TemplatePath: templatePath,
		FilePath: filepath.Join(filepath.Dir(templatePath), "index.ts"),
		Members: []admin.VueComponentMember{
			{Name: "rows", Kind: admin.ComponentMemberData, Type: "Array<Row>"},
			{Name: "getSlots", Kind: admin.ComponentMemberMethod, Type: "() => Array<string>"},
		},
		Blocks: []admin.TwigBlock{{
			Name:         "sw_card_row",
			ScopeMembers: []admin.TwigBlockScopeMember{{Name: "item"}},
		}},
	}))
	analyzer := NewAdminAnalyzer(adminIndexer)
	for _, test := range []struct {
		name, source, suggestion string
		count                    int
	}{
		{
			name: "component typo", source: `{{ getSlos() }}`,
			count: 1, suggestion: "getSlots",
		},
		{
			name: "global typo", source: `{{ Objcet.keys(rows) }}`,
			count: 1, suggestion: "Object",
		},
		{
			name:   "known runtime scope",
			source: `{{ getSlots() }} {{ Object.keys(rows) }} {{ $t('key') }}`,
		},
		{
			name:   "lexical locals",
			source: `<div v-for="row in rows" @click="getSlots($event)">{{ row.name }}</div>`,
		},
		{
			name:   "arbitrary plugin global remains conservative",
			source: `{{ runtimePluginValue() }}`,
		},
		{
			name:   "inherited block local",
			source: `{% block sw_card_row %}{{ item.name }}{% endblock %}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			problems, analyzeErr := analyzer.Analyze(
				context.Background(),
				diagnosticsDocument("file://"+templatePath, []byte(test.source)),
			)
			require.NoError(t, analyzeErr)
			require.Len(t, problems, test.count)
			if test.count == 0 {
				return
			}
			assert.Equal(
				t, "admin.component.unknown-template-member",
				string(problems[0].ID),
			)
			assert.Contains(
				t,
				problems[0].Payload.(map[string]any)["suggestions"],
				test.suggestion,
			)
		})
	}
}

func TestAdminAnalyzerKeepsRuntimeOpenTemplateScopeConservative(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	templatePath := filepath.Join(
		root, "Resources/app/administration/src/sw-flow.html.twig",
	)
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-flow", TemplatePath: templatePath,
		FilePath:           filepath.Join(filepath.Dir(templatePath), "index.ts"),
		OpenRuntimeMembers: true,
		Members: []admin.VueComponentMember{{
			Name: "flowId", Kind: admin.ComponentMemberProp,
		}},
	}))
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file://"+templatePath,
			[]byte(`{{ flow.name }} {{ Objcet.keys(flow) }}`),
		),
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(
		t, "admin.component.unknown-template-member", string(problems[0].ID),
	)
	assert.Contains(
		t, problems[0].Payload.(map[string]any)["suggestions"], "Object",
	)
}

func TestAdminAnalyzerTreatsSetupReturnsAsInstanceMembers(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	definitionPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/component/sw-setup/index.ts",
	)
	source := []byte(`Shopware.Component.register('sw-setup', Shopware.Component.wrapComponentConfig({
    setup() {
        const parent = computed(() => null);
        const open = () => true;
        return { parent, open };
    },
    render() {
        this.parent;
        this.open();
        this.typoMember;
    },
}));`)
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		definitionPath, source,
	)))

	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument("file://"+definitionPath, source),
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, "admin.component.unknown-instance-member", string(problems[0].ID))
	assert.Contains(t, problems[0].Message, "typoMember")
}

func TestAdminAnalyzerReportsUnknownTwigPrivilegeWithSuggestion(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "privileges.ts"),
		[]byte(`Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product', roles: { viewer: { privileges: ['product:read'] } },
});`),
	)))
	source := []byte(`<div>
    <mt-button :disabled="acl.can('admin')" />
    <mt-button :disabled="acl.can('product.viwer')" />
</div>`)
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file://"+filepath.Join(adminRoot, "view.html.twig"),
			source,
		),
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, "admin.privilege.not-found", string(problems[0].ID))
	assert.Equal(t, "product.viwer", string(
		source[problems[0].Range.Start:problems[0].Range.End],
	))
	assert.Contains(
		t,
		problems[0].Payload.(map[string]any)["suggestions"],
		"product.viewer",
	)
}

func TestAdminAnalyzerReportsUnknownAdministrationModuleRoutes(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "module", "sw-product", "index.js"),
		[]byte(`Module.register('sw-product', {
    routes: { detail: { path: 'detail/:id', component: 'sw-product-detail' } },
});`),
	)))
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "module", "sw-profile", "index.js"),
		[]byte(`const options = {
    routeMiddleware(next, currentRoute) {
        const name = 'sw.profile.index.mfa';
        currentRoute.children.push({ name, path: 'mfa' });
        next(currentRoute);
    },
};
Module.register('sw-profile-extension', options);`),
	)))

	tests := []struct {
		name   string
		path   string
		source string
	}{
		{
			name:   "JavaScript router location",
			path:   "component.js",
			source: `this.$router.push({ name: 'sw.product.detial' });`,
		},
		{
			name:   "Twig router link",
			path:   "component.html.twig",
			source: `<router-link :to="{ name: 'sw.product.detial' }" />`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			problems, analyzeErr := NewAdminAnalyzer(adminIndexer).Analyze(
				context.Background(),
				diagnosticsDocument(
					"file://"+filepath.Join(adminRoot, test.path),
					[]byte(test.source),
				),
			)
			require.NoError(t, analyzeErr)
			require.Len(t, problems, 1)
			assert.Equal(
				t,
				"admin.module-route.not-found",
				string(problems[0].ID),
			)
			assert.Equal(t, "sw.product.detial", test.source[problems[0].Range.Start:problems[0].Range.End])
			assert.Contains(
				t,
				problems[0].Payload.(map[string]any)["suggestions"],
				"sw.product.detail",
			)
		})
	}
	for _, source := range []string{
		`this.$router.push({ name: 'sw.product.detail' });`,
		`<router-link :to="{ name: 'sw.profile.index.mfa' }" />`,
	} {
		problems, analyzeErr := NewAdminAnalyzer(adminIndexer).Analyze(
			context.Background(),
			diagnosticsDocument(
				"file://"+filepath.Join(adminRoot, "known-route.html.twig"),
				[]byte(source),
			),
		)
		require.NoError(t, analyzeErr)
		for _, problem := range problems {
			assert.NotEqual(
				t, "admin.module-route.not-found", string(problem.ID), source,
			)
		}
	}

	definitionSource := `Module.register('sw-other', {
    name: 'Not a route',
    routes: { detail: { path: 'detail/:id' } },
});`
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file://"+filepath.Join(adminRoot, "module", "sw-other", "index.js"),
			[]byte(definitionSource),
		),
	)
	require.NoError(t, err)
	assert.Empty(t, problems)
}

func TestAdminAnalyzer_UnknownThisMemberSuppressesSpreadGeneratedScope(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	definitionPath := filepath.Join(
		root,
		"src/Resources/app/administration/src/component/sw-card/index.js",
	)
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: definitionPath, DefinitionPath: definitionPath,
	}))
	source := []byte(`export default {
    computed: { ...mapPropertyErrors('product', ['name']) },
    methods: { run() { return this.productNameError; } },
};`)
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument("file://"+definitionPath, source),
	)
	require.NoError(t, err)
	assert.Empty(t, problems)
}

func TestCamelToKebab(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"positionIdentifier", "position-identifier"},
		{"myPropName", "my-prop-name"},
		{"simple", "simple"},
		{"ABC", "a-b-c"},
		{"camelCase", "camel-case"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := camelToKebab(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAdminAnalyzer_BlockReferences(t *testing.T) {
	tempDir := t.TempDir()

	// Create the admin indexer
	adminIndexer, err := admin.NewAdminComponentIndexer(tempDir)
	require.NoError(t, err)
	defer func() { _ = adminIndexer.Close() }()

	// Setup paths
	parentCompPath := filepath.Join(tempDir, "src", "Resources", "app", "administration", "src", "component", "sw-page", "index.js")
	parentTemplatePath := filepath.Join(tempDir, "src", "Resources", "app", "administration", "src", "component", "sw-page", "sw-page.html.twig")
	extendCompPath := filepath.Join(tempDir, "src", "Resources", "app", "administration", "src", "extension", "my-custom-page", "index.js")
	extendTemplatePath := filepath.Join(tempDir, "src", "Resources", "app", "administration", "src", "extension", "my-custom-page", "my-custom-page.html.twig")

	// Save parent component directly with blocks already populated
	parentComp := admin.VueComponent{
		Name:         "sw-page",
		FilePath:     parentCompPath,
		TemplatePath: parentTemplatePath,
		Blocks: []admin.TwigBlock{
			{Name: "sw_page_content", Line: 10},
			{Name: "sw_page_smart_bar", Line: 20},
			{Name: "sw_page_actions", Line: 30},
		},
	}
	err = adminIndexer.SaveComponent(parentComp)
	require.NoError(t, err)

	// Save extended component that extends sw-page
	extendComp := admin.VueComponent{
		Name:             "my-custom-page",
		FilePath:         extendCompPath,
		TemplatePath:     extendTemplatePath,
		ExtendsComponent: "sw-page",
	}
	err = adminIndexer.SaveComponent(extendComp)
	require.NoError(t, err)

	provider := &AdminAnalyzer{
		adminIndexer: adminIndexer,
	}

	tests := []struct {
		name             string
		twigCode         string
		expectDiagCount  int
		expectBlockNames []string // block names that should be reported as invalid
	}{
		{
			name:            "valid block reference",
			twigCode:        `{% block sw_page_content %}<div>Custom content</div>{% endblock %}`,
			expectDiagCount: 0,
		},
		{
			name:             "likely misspelled block reference",
			twigCode:         `{% block sw_page_contnet %}<div>Custom content</div>{% endblock %}`,
			expectDiagCount:  1,
			expectBlockNames: []string{"sw_page_contnet"},
		},
		{
			name:            "multiple valid blocks",
			twigCode:        `{% block sw_page_content %}content{% endblock %}{% block sw_page_smart_bar %}bar{% endblock %}`,
			expectDiagCount: 0,
		},
		{
			name:            "new extension block is allowed",
			twigCode:        `{% block sw_page_content %}content{% endblock %}{% block invalid_block %}invalid{% endblock %}`,
			expectDiagCount: 0,
		},
		{
			name:            "multiple new extension blocks are allowed",
			twigCode:        `{% block invalid_one %}one{% endblock %}{% block invalid_two %}two{% endblock %}`,
			expectDiagCount: 0,
		},
	}

	// Verify setup: GetComponentByTemplatePath should find the extended component
	compByPath, err := adminIndexer.GetComponentByTemplatePath(extendTemplatePath)
	require.NoError(t, err)
	require.NotNil(t, compByPath, "GetComponentByTemplatePath should find the component")
	require.Equal(t, "my-custom-page", compByPath.Name)
	require.Equal(t, "sw-page", compByPath.ExtendsComponent)

	// Verify parent has blocks
	parentComps, err := adminIndexer.GetComponent("sw-page")
	require.NoError(t, err)
	require.Len(t, parentComps, 1, "Parent component should be found")
	require.Len(t, parentComps[0].Blocks, 3, "Parent component should have 3 blocks")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use the extended component template path as URI
			uri := "file://" + extendTemplatePath
			diagnostics, err := provider.Analyze(context.Background(), diagnosticsDocument(uri, []byte(tt.twigCode)))
			require.NoError(t, err)

			assert.Len(t, diagnostics, tt.expectDiagCount, "Unexpected number of diagnostics")

			// Check that the expected blocks are reported
			for _, expectedBlock := range tt.expectBlockNames {
				found := false
				for _, diag := range diagnostics {
					if data, ok := diag.Payload.(map[string]any); ok {
						if data["blockName"] == expectedBlock {
							found = true
							assert.Equal(t, "admin.component.block-not-found", string(diag.ID))
							break
						}
					}
				}
				assert.True(t, found, "Expected diagnostic for invalid block '%s'", expectedBlock)
			}
		})
	}
}

func TestAdminAnalyzer_BlockReferences_NoOverride(t *testing.T) {
	tempDir := t.TempDir()

	// Create the admin indexer
	adminIndexer, err := admin.NewAdminComponentIndexer(tempDir)
	require.NoError(t, err)
	defer func() { _ = adminIndexer.Close() }()

	// Setup paths
	compPath := filepath.Join(tempDir, "src", "Resources", "app", "administration", "src", "component", "my-component", "index.js")
	templatePath := filepath.Join(tempDir, "src", "Resources", "app", "administration", "src", "component", "my-component", "my-component.html.twig")

	// Save a regular component (not an override - no ExtendsComponent)
	comp := admin.VueComponent{
		Name:         "my-component",
		FilePath:     compPath,
		TemplatePath: templatePath,
		// Note: ExtendsComponent is empty - this is a regular component
	}
	err = adminIndexer.SaveComponent(comp)
	require.NoError(t, err)

	provider := &AdminAnalyzer{
		adminIndexer: adminIndexer,
	}

	// Regular components (not extending others) should not produce block diagnostics
	// because they define their own blocks
	twigCode := `{% block any_block_name %}content{% endblock %}`

	uri := "file://" + templatePath
	diagnostics, err := provider.Analyze(context.Background(), diagnosticsDocument(uri, []byte(twigCode)))
	require.NoError(t, err)

	assert.Empty(t, diagnostics, "Regular components should not produce block reference diagnostics")
}

func TestAdminAnalyzerOverrideBlocksReportDeprecationAndTypo(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(root, "Resources/app/administration/src")
	baseTemplate := filepath.Join(adminRoot, "sw-card.html.twig")
	overrideTemplate := filepath.Join(adminRoot, "acme-card.html.twig")
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-card", Kind: admin.ComponentRegister,
		FilePath:     filepath.Join(adminRoot, "sw-card.js"),
		TemplatePath: baseTemplate,
		Blocks: []admin.TwigBlock{
			{
				Name: "sw_card_legacy", FilePath: baseTemplate, Line: 4,
				Deprecated: "Use sw_card_content instead.",
			},
			{Name: "sw_card_content", FilePath: baseTemplate, Line: 8},
		},
	}))
	require.NoError(t, adminIndexer.SaveComponent(admin.VueComponent{
		Name: "sw-card", Kind: admin.ComponentOverride,
		TargetComponent: "sw-card",
		FilePath:        filepath.Join(adminRoot, "zz-acme-card.js"),
		TemplatePath:    overrideTemplate,
	}))

	source := []byte(`{% block sw_card_legacy %}{% endblock %}
{% block sw_card_contnet %}{% endblock %}`)
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(uriutil.FileURI(overrideTemplate), source),
	)
	require.NoError(t, err)
	require.Len(t, problems, 2)
	byID := make(map[lsp.DiagnosticID]lsp.Problem, len(problems))
	for _, problem := range problems {
		byID[problem.ID] = problem
	}
	deprecated := byID["admin.component.deprecated-block"]
	assert.Equal(t, protocol.DiagnosticSeverityHint, deprecated.Severity)
	assert.Equal(
		t, []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
		deprecated.Tags,
	)
	assert.Contains(t, deprecated.Message, "Use sw_card_content instead")
	missing := byID["admin.component.block-not-found"]
	require.NotNil(t, missing.Payload)
	assert.Contains(
		t, missing.Payload.(map[string]any)["suggestions"],
		"sw_card_content",
	)
}

func TestAdminAnalyzerReportsClosedApplicationContainerTypos(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(root, "Resources/app/administration/src")
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "global.types.ts"),
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
	source := []byte(`
Application.getContainer('factory').loacle;
Application.getContainer('factory').module;
Application.getContainer('service').al;
`)
	path := filepath.Join(adminRoot, "consumer.ts")
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(uriutil.FileURI(path), source),
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	problem := problems[0]
	assert.Equal(
		t, lsp.DiagnosticID("admin.application-container.unknown-member"),
		problem.ID,
	)
	assert.Equal(t, "loacle", string(source[problem.Range.Start:problem.Range.End]))
	assert.Contains(
		t, problem.Payload.(map[string]any)["suggestions"], "locale",
	)
}

func TestAdminAnalyzerReportsClosedShopwareContextTypos(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(root, "Resources/app/administration/src")
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "app/composables/use-context.ts"),
		[]byte(`export interface ContextState {
    app: { environment: null | string };
    api: { languageId: null | string; versionId: null | string };
}`),
	)))
	source := []byte(`
Shopware.Context.api.langaugeId;
Shopware.Context.api.versionId;
Shopware.Context.customRuntimeHelper;
`)
	path := filepath.Join(adminRoot, "consumer.ts")
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(uriutil.FileURI(path), source),
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	problem := problems[0]
	assert.Equal(
		t, lsp.DiagnosticID("admin.shopware-context.unknown-member"), problem.ID,
	)
	assert.Equal(
		t, "langaugeId", string(source[problem.Range.Start:problem.Range.End]),
	)
	assert.Contains(
		t, problem.Payload.(map[string]any)["suggestions"], "languageId",
	)
}

func TestAdminAnalyzerReportsClosedShopwareUtilsTypos(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(root, "Resources/app/administration/src")
	require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "core/service/util.service.ts"),
		[]byte(`export const format = { date, fileSize };
export default { createId, format };
function createId(): string { return 'id'; }
function date(value: string): string { return value; }
function fileSize(bytes: number): string { return String(bytes); }`),
	)))
	source := []byte(`
Shopware.Utils.creatId();
Shopware.Utils.format.dtae('2026-01-01');
Shopware.Utils.format.fileSize(42);
const utils = Shopware.Utils;
utils.creatId();
const { format } = Shopware.Utils;
format.dtae('2026-01-01');
`)
	path := filepath.Join(adminRoot, "consumer.ts")
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(uriutil.FileURI(path), source),
	)
	require.NoError(t, err)
	require.Len(t, problems, 4)
	byMember := make(map[string]lsp.Problem)
	for _, problem := range problems {
		require.Equal(
			t, lsp.DiagnosticID("admin.shopware-utils.unknown-member"), problem.ID,
		)
		byMember[problem.Payload.(map[string]any)["memberName"].(string)] = problem
	}
	assert.Contains(t, byMember["creatId"].Payload.(map[string]any)["suggestions"], "createId")
	assert.Contains(t, byMember["dtae"].Payload.(map[string]any)["suggestions"], "date")
}

func TestAdminAnalyzerValidatesKnownShopwareEventBusPayloads(t *testing.T) {
	root := t.TempDir()
	adminIndexer, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndexer.Close()) })
	adminRoot := filepath.Join(root, "Resources/app/administration/src")
	for path, source := range map[string]string{
		filepath.Join(adminRoot, "core/service/util.service.ts"): `
import EventBus from './utils/eventBus.utils';
export default { EventBus };`,
		filepath.Join(adminRoot, "core/service/utils/eventBus.utils.ts"): `
interface Events extends Record<string | symbol, unknown> {
    save: string;
    done: undefined;
}
const emitter = mitt<Events>();
export default emitter;`,
	} {
		require.NoError(t, adminIndexer.Index(indexer.NewParsedFile(
			path, []byte(source),
		)))
	}
	source := []byte(`
Shopware.Utils.EventBus.emit('save');
Shopware.Utils.EventBus.emit('save', 42);
Shopware.Utils.EventBus.emit('save', 'id');
Shopware.Utils.EventBus.emit('done');
Shopware.Utils.EventBus.on('save');
Shopware.Utils.EventBus.on('save', 'not-a-handler');
Shopware.Utils.EventBus.off('save');
Shopware.Utils.EventBus.off('save', 42);
Shopware.Utils.EventBus.emit('extension-event', 42);
const { EventBus } = Shopware.Utils;
EventBus.emit('save', false);
`)
	path := filepath.Join(adminRoot, "consumer.ts")
	problems, err := NewAdminAnalyzer(adminIndexer).Analyze(
		context.Background(),
		diagnosticsDocument(uriutil.FileURI(path), source),
	)
	require.NoError(t, err)
	require.Len(t, problems, 6)
	counts := make(map[lsp.DiagnosticID]int)
	for _, problem := range problems {
		counts[problem.ID]++
		payload := problem.Payload.(map[string]any)
		assert.Equal(t, "save", payload["eventName"])
	}
	assert.Equal(t, 1, counts["admin.event-bus.missing-payload"])
	assert.Equal(t, 2, counts["admin.event-bus.payload-type"])
	assert.Equal(t, 1, counts["admin.event-bus.missing-handler"])
	assert.Equal(t, 2, counts["admin.event-bus.handler-type"])

	for _, problem := range problems {
		selected := string(source[problem.Range.Start:problem.Range.End])
		switch problem.ID {
		case "admin.event-bus.missing-payload",
			"admin.event-bus.missing-handler":
			assert.Equal(t, "save", selected)
		case "admin.event-bus.payload-type":
			assert.Contains(t, []string{"42", "false"}, selected)
		case "admin.event-bus.handler-type":
			assert.Contains(t, []string{"'not-a-handler'", "42"}, selected)
		}
	}
}
