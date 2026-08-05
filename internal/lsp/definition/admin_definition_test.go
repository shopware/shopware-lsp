package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
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

func TestIsInComponentCall(t *testing.T) {
	provider := &AdminDefinitionProvider{}

	tests := []struct {
		name     string
		code     string
		line     uint
		col      uint
		expected bool
	}{
		{
			name:     "Component.register first arg",
			code:     `Component.register('my-component', () => import('./index'));`,
			line:     0,
			col:      22,
			expected: true,
		},
		{
			name:     "Component.extend first arg",
			code:     `Component.extend('my-component', 'parent', () => import('./index'));`,
			line:     0,
			col:      22,
			expected: true,
		},
		{
			name:     "Component.extend second arg (parent)",
			code:     `Component.extend('my-component', 'parent', () => import('./index'));`,
			line:     0,
			col:      36,
			expected: true,
		},
		{
			name:     "Shopware.Component.register",
			code:     `Shopware.Component.register('my-component', () => import('./index'));`,
			line:     0,
			col:      32,
			expected: true,
		},
		{
			name:     "Shopware.Component.extend",
			code:     `Shopware.Component.extend('my-component', 'parent', () => import('./index'));`,
			line:     0,
			col:      45,
			expected: true,
		},
		{
			name:     "not in component call",
			code:     `const name = 'my-component';`,
			line:     0,
			col:      16,
			expected: false,
		},
		{
			name:     "different function call",
			code:     `someFunc('my-component');`,
			line:     0,
			col:      12,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := parseJS(t, tt.code)
			node := findNodeAtPosition(root, tt.line, tt.col)
			if node == nil {
				t.Fatalf("Could not find node at position %d:%d", tt.line, tt.col)
			}

			result := provider.isInComponentCall(node)
			assert.Equal(t, tt.expected, result, "Node kind: %s, text: %s", node.Kind(), node.Text())
		})
	}
}

func TestExtractComponentName(t *testing.T) {
	provider := &AdminDefinitionProvider{}

	tests := []struct {
		name     string
		code     string
		line     uint
		col      uint
		expected string
	}{
		{
			name:     "single quoted string",
			code:     `Component.register('my-component', () => {});`,
			line:     0,
			col:      22,
			expected: "my-component",
		},
		{
			name:     "double quoted string",
			code:     `Component.register("my-component", () => {});`,
			line:     0,
			col:      22,
			expected: "my-component",
		},
		{
			name:     "parent component name",
			code:     `Component.extend('child', 'sw-base-component', () => {});`,
			line:     0,
			col:      30,
			expected: "sw-base-component",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := parseJS(t, tt.code)
			node := findNodeAtPosition(root, tt.line, tt.col)
			if node == nil {
				t.Fatalf("Could not find node at position %d:%d", tt.line, tt.col)
			}

			result := provider.extractComponentName(node)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizePropName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"label", "label"},
		{"position-identifier", "positionIdentifier"},
		{":label", "label"},
		{":position-identifier", "positionIdentifier"},
		{"v-bind:label", "label"},
		{"v-bind:position-identifier", "positionIdentifier"},
		{"@click", ""},     // event handler
		{"v-on:click", ""}, // event handler
		{"v-if", ""},       // directive
		{"v-for", ""},      // directive
		{"v-model", ""},    // directive
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := admin.NormalizePropName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAdminPropDefinitionUsesExactImportedTypeDeclarationRange(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	componentDir := filepath.Join(
		root, "Resources/app/administration/src/component/sw-card",
	)
	contractsPath := filepath.Join(componentDir, "contracts.ts")
	componentPath := filepath.Join(componentDir, "Card.vue")
	registrationPath := filepath.Join(componentDir, "index.ts")
	contractsSource := "export interface CardProps {\n    title: string;\n}\n"
	for _, file := range []struct {
		path   string
		source string
	}{
		{contractsPath, contractsSource},
		{componentPath, `<template><div>{{ title }}</div></template>
<script setup lang="ts">
import type { CardProps } from './contracts';
defineProps<CardProps>();
</script>`},
		{registrationPath, `Shopware.Component.register('sw-card', () => import('./Card.vue'));`},
	} {
		require.NoError(t, index.Index(indexerpkg.NewParsedFile(
			file.path, []byte(file.source),
		)))
	}

	consumerPath := filepath.Join(root, "Resources/app/administration/src/consumer.html.twig")
	source := `<sw-card :title="heading" />`
	document := lsp.NewTextDocument(uriutil.FileURI(consumerPath), source, 1)
	offset := uint32(strings.Index(source, "title") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
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
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(contractsPath), locations[0].URI)
	assert.Equal(
		t, protocol.Position{Line: 1, Character: 4},
		locations[0].Range.Start,
	)
	assert.Equal(
		t, protocol.Position{Line: 1, Character: 9},
		locations[0].Range.End,
	)
}

func TestAdminTwigBlockDefinitionNavigatesToParentDeclaration(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(root, "Resources/app/administration/src")
	parentTemplate := filepath.Join(adminRoot, "sw-card.html.twig")
	childTemplate := filepath.Join(adminRoot, "acme-card.html.twig")
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-card", Kind: admin.ComponentRegister,
		FilePath:     filepath.Join(adminRoot, "sw-card.js"),
		TemplatePath: parentTemplate,
		Blocks: []admin.TwigBlock{{
			Name: "sw_card_content", FilePath: parentTemplate, Line: 5,
			NameRange: admin.AdminSourceRange{
				StartLine: 4, StartCharacter: 9,
				EndLine: 4, EndCharacter: 24,
				Declaration: true, Identifier: true,
			},
			ScopeMembers: []admin.TwigBlockScopeMember{{
				Name: "item", FilePath: parentTemplate, Line: 3,
				NameRange: admin.AdminSourceRange{
					StartLine: 2, StartCharacter: 20,
					EndLine: 2, EndCharacter: 24,
					Declaration: true, Identifier: true,
				},
			}},
		}},
	}))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "acme-card", Kind: admin.ComponentExtend,
		TargetComponent: "sw-card", ExtendsComponent: "sw-card",
		FilePath:     filepath.Join(adminRoot, "acme-card.js"),
		TemplatePath: childTemplate,
	}))

	source := `{% block sw_card_content %}{% endblock %}`
	document := lsp.NewTextDocument(uriutil.FileURI(childTemplate), source, 1)
	offset := uint32(strings.Index(source, "sw_card_content") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(parentTemplate), locations[0].URI)
	assert.Equal(t, protocol.Position{Line: 4, Character: 9}, locations[0].Range.Start)
	assert.Equal(t, protocol.Position{Line: 4, Character: 24}, locations[0].Range.End)

	memberSource := `{% block sw_card_content %}{{ item.name }}{% endblock %}`
	memberDocument := lsp.NewTextDocument(
		uriutil.FileURI(childTemplate), memberSource, 2,
	)
	memberOffset := uint32(strings.Index(memberSource, "item") + 1)
	memberLine, memberCharacter := memberDocument.LineIndex.PositionUTF16(
		memberOffset,
	)
	memberParams := &protocol.DefinitionParams{}
	memberParams.TextDocument.URI = memberDocument.URI
	memberParams.Position.Line = int(memberLine)
	memberParams.Position.Character = int(memberCharacter)
	memberLocations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: memberParams,
			SyntaxContext: lsp.SyntaxContext{
				Document: memberDocument, DocumentContent: memberDocument.Text,
				DocumentTree: memberDocument.SyntaxTree,
				LineIndex:    memberDocument.LineIndex,
				Root:         memberDocument.SyntaxTree.Root,
				Node: memberDocument.SyntaxTree.Root.NodeAtOffset(
					memberOffset,
				),
				Token: memberDocument.SyntaxTree.Root.TokenAtOffset(
					memberOffset,
				),
			},
		},
	)
	require.Len(t, memberLocations, 1)
	assert.Equal(t, uriutil.FileURI(parentTemplate), memberLocations[0].URI)
	assert.Equal(
		t, protocol.Position{Line: 2, Character: 20},
		memberLocations[0].Range.Start,
	)
	assert.Equal(
		t, protocol.Position{Line: 2, Character: 24},
		memberLocations[0].Range.End,
	)
}

func TestKebabToCamel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"label", "label"},
		{"position-identifier", "positionIdentifier"},
		{"my-prop-name", "myPropName"},
		{"a-b-c", "aBC"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := kebabToCamel(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAdminTemplateMemberDefinitionUsesOwningFile(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	templatePath := filepath.Join(
		root,
		"src/Resources/app/administration/src/component/sw-child/sw-child.html.twig",
	)
	parentDefinition := filepath.Join(
		root,
		"src/Resources/app/administration/src/component/sw-parent/index.js",
	)
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name:         "sw-child",
		FilePath:     filepath.Join(filepath.Dir(templatePath), "index.js"),
		TemplatePath: templatePath,
		Props: []admin.VueComponentProp{{
			Name: "parentLabel", Type: "String", FilePath: parentDefinition, Line: 8,
		}},
	}))
	source := "{{ parentLabel }}"
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	offset := uint32(strings.Index(source, "parentLabel") + 1)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(parentDefinition), locations[0].URI)
	assert.Equal(t, 7, locations[0].Range.Start.Line)

	attributeSource := `<div :title="parentLabel"></div>`
	attributeDocument := lsp.NewTextDocument(
		uriutil.FileURI(templatePath), attributeSource, 2,
	)
	attributeOffset := uint32(
		strings.LastIndex(attributeSource, "parentLabel") + 1,
	)
	line, character := attributeDocument.LineIndex.PositionUTF16(attributeOffset)
	attributeParams := &protocol.DefinitionParams{}
	attributeParams.TextDocument.URI = attributeDocument.URI
	attributeParams.Position.Line = int(line)
	attributeParams.Position.Character = int(character)
	attributeLocations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: attributeParams,
			SyntaxContext: lsp.SyntaxContext{
				Document: attributeDocument, DocumentContent: attributeDocument.Text,
				DocumentTree: attributeDocument.SyntaxTree,
				LineIndex:    attributeDocument.LineIndex,
				Root:         attributeDocument.SyntaxTree.Root,
				Node: attributeDocument.SyntaxTree.Root.NodeAtOffset(
					attributeOffset,
				),
				Token: attributeDocument.SyntaxTree.Root.TokenAtOffset(
					attributeOffset,
				),
			},
		},
	)
	require.Len(t, attributeLocations, 1)
	assert.Equal(t, uriutil.FileURI(parentDefinition), attributeLocations[0].URI)
	assert.Equal(t, 7, attributeLocations[0].Range.Start.Line)
}

func TestAdminDefinitionNavigatesImportedScriptSetupProp(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	componentDir := filepath.Join(adminRoot, "component/sw-imported-card")
	componentPath := filepath.Join(componentDir, "index.vue")
	typesPath := filepath.Join(componentDir, "contracts.ts")
	draftTypesPath := filepath.Join(componentDir, "draft-contracts.ts")
	componentSource := `<template>{{ heading }}<slot name="header" :item="heading" /></template>
<script setup lang="ts">
import type { CardProps, CardSlots } from './contracts';
const { mode: heading } = defineProps<CardProps>();
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
		typesPath,
		[]byte("export interface CardProps {\n    mode: 'small' | 'large';\n}\nexport interface HeaderPayload {\n    item: string;\n}\nexport interface CardSlots { header(props: HeaderPayload): unknown }"),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		draftTypesPath,
		[]byte(`export interface DraftProfile { city: string; zip: string }
export interface DraftProps { profile: DraftProfile }`),
	)))
	localChildPath := filepath.Join(componentDir, "DraftPanel.vue")
	localChildSource := `<template><slot /></template>
<script setup lang="ts">
defineProps<{ label: string; count?: number }>();
</script>`
	require.NoError(t, os.MkdirAll(componentDir, 0o755))
	require.NoError(t, os.WriteFile(localChildPath, []byte(localChildSource), 0o644))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		localChildPath, []byte(localChildSource),
	)))

	source := `<sw-imported-card :mode="'small'" />`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")), source, 1,
	)
	offset := uint32(strings.Index(source, ":mode") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(typesPath), locations[0].URI)
	assert.Equal(t, 1, locations[0].Range.Start.Line)

	slotSource := `<sw-imported-card><template #header="{ item: row }">{{ row }}</template></sw-imported-card>`
	slotDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "slot-consumer.html.twig")),
		slotSource, 1,
	)
	slotOffset := uint32(strings.LastIndex(slotSource, "row") + 1)
	slotLine, slotCharacter := slotDocument.LineIndex.PositionUTF16(slotOffset)
	slotParams := &protocol.DefinitionParams{}
	slotParams.TextDocument.URI = slotDocument.URI
	slotParams.Position.Line = int(slotLine)
	slotParams.Position.Character = int(slotCharacter)
	slotLocations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: slotParams,
			SyntaxContext: lsp.SyntaxContext{
				Document: slotDocument, DocumentContent: slotDocument.Text,
				DocumentTree: slotDocument.SyntaxTree,
				LineIndex:    slotDocument.LineIndex,
				Root:         slotDocument.SyntaxTree.Root,
				Node:         slotDocument.SyntaxTree.Root.NodeAtOffset(slotOffset),
				Token:        slotDocument.SyntaxTree.Root.TokenAtOffset(slotOffset),
			},
		},
	)
	require.Len(t, slotLocations, 1)
	assert.Equal(t, uriutil.FileURI(typesPath), slotLocations[0].URI)
	assert.Equal(t, 4, slotLocations[0].Range.Start.Line)

	liveComponentSource := strings.ReplaceAll(
		componentSource, "heading", "headline",
	)
	componentDocument := lsp.NewTextDocument(
		uriutil.FileURI(componentPath), liveComponentSource, 1,
	)
	localOffset := uint32(strings.Index(liveComponentSource, "headline }}") + 1)
	localLine, localCharacter := componentDocument.LineIndex.PositionUTF16(localOffset)
	localParams := &protocol.DefinitionParams{}
	localParams.TextDocument.URI = componentDocument.URI
	localParams.Position.Line = int(localLine)
	localParams.Position.Character = int(localCharacter)
	localLocations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: localParams,
			SyntaxContext: lsp.SyntaxContext{
				Document: componentDocument, Language: componentDocument.SyntaxLanguage,
				DocumentContent: componentDocument.Text,
				DocumentTree:    componentDocument.SyntaxTree,
				LineIndex:       componentDocument.LineIndex,
				Root:            componentDocument.SyntaxTree.Root,
				Node:            componentDocument.SyntaxTree.Root.NodeAtOffset(localOffset),
				Token:           componentDocument.SyntaxTree.Root.TokenAtOffset(localOffset),
			},
		},
	)
	require.Len(t, localLocations, 1)
	assert.Equal(t, uriutil.FileURI(componentPath), localLocations[0].URI)
	assert.Equal(t, 3, localLocations[0].Range.Start.Line)

	liveImportedSource := `<template>{{ profile.city }}</template>
<script setup lang="ts">
import type { DraftProps } from './draft-contracts';
const { profile } = defineProps<DraftProps>();
</script>`
	liveImportedDocument := lsp.NewTextDocument(
		uriutil.FileURI(componentPath), liveImportedSource, 2,
	)
	cityOffset := uint32(strings.Index(liveImportedSource, "profile.city") + len("profile."))
	cityLine, cityCharacter := liveImportedDocument.LineIndex.PositionUTF16(cityOffset)
	cityParams := &protocol.DefinitionParams{}
	cityParams.TextDocument.URI = liveImportedDocument.URI
	cityParams.Position.Line = int(cityLine)
	cityParams.Position.Character = int(cityCharacter)
	cityLocations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: cityParams,
			SyntaxContext: lsp.SyntaxContext{
				Document:        liveImportedDocument,
				Language:        liveImportedDocument.SyntaxLanguage,
				DocumentContent: liveImportedDocument.Text,
				DocumentTree:    liveImportedDocument.SyntaxTree,
				LineIndex:       liveImportedDocument.LineIndex,
				Root:            liveImportedDocument.SyntaxTree.Root,
				Node:            liveImportedDocument.SyntaxTree.Root.NodeAtOffset(cityOffset),
				Token:           liveImportedDocument.SyntaxTree.Root.TokenAtOffset(cityOffset),
			},
		},
	)
	require.Len(t, cityLocations, 1)
	assert.Equal(t, uriutil.FileURI(draftTypesPath), cityLocations[0].URI)
	assert.Equal(t, 0, cityLocations[0].Range.Start.Line)

	liveLocalSource := `<template><DraftPanel :label="title" /></template>
<script setup lang="ts">
import DraftPanel from './DraftPanel.vue';
const title = 'Draft';
</script>`
	definitionAt := func(marker string, inside int) []protocol.Location {
		t.Helper()
		document := lsp.NewTextDocument(
			uriutil.FileURI(componentPath), liveLocalSource, 3,
		)
		offset := uint32(strings.Index(liveLocalSource, marker) + inside)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.DefinitionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		return NewAdminDefinitionProvider(index).GetDefinition(
			context.Background(),
			&lsp.DefinitionRequest{
				DefinitionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, Language: document.SyntaxLanguage,
					DocumentContent: document.Text, DocumentTree: document.SyntaxTree,
					LineIndex: document.LineIndex, Root: document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
					Token: document.SyntaxTree.Root.TokenAtOffset(offset),
				},
			},
		)
	}
	localTagLocations := definitionAt("DraftPanel", 1)
	require.Len(t, localTagLocations, 1)
	assert.Equal(t, uriutil.FileURI(localChildPath), localTagLocations[0].URI)
	localPropLocations := definitionAt(":label", 2)
	require.Len(t, localPropLocations, 1)
	assert.Equal(t, uriutil.FileURI(localChildPath), localPropLocations[0].URI)
}

func TestAdminDefinitionUsesUnsavedLegacyDefinitionAndTwig(t *testing.T) {
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
	persistedTemplate := `<slot name="old-slot" />`
	persistedDefinition := `import template from './sw-legacy-live.html.twig';
export default { template, props: { oldRequired: String } };`
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

	liveDefinition := `import template from './sw-legacy-live.html.twig';
export default {
    template,
    props: {
        liveRequired: { type: Number, required: true },
    },
};`
	liveDefinitionDocument := lsp.NewTextDocument(
		uriutil.FileURI(definitionPath), liveDefinition, 2,
	)
	index.UpdateLiveDocument(
		definitionPath, liveDefinitionDocument.SyntaxTree.Root,
		liveDefinition, liveDefinitionDocument.LineIndex,
	)
	liveTemplate := `
<slot name="live-slot" :record="value" />`
	liveTemplateDocument := lsp.NewTextDocument(
		uriutil.FileURI(templatePath), liveTemplate, 3,
	)
	index.UpdateLiveDocument(
		templatePath, liveTemplateDocument.SyntaxTree.Root,
		liveTemplate, liveTemplateDocument.LineIndex,
	)

	consumerPath := filepath.Join(adminRoot, "consumer.html.twig")
	consumerSource := `<sw-legacy-live :live-required="1">
    <template #live-slot />
</sw-legacy-live>`
	definitionAt := func(marker string, inside int) []protocol.Location {
		t.Helper()
		document := lsp.NewTextDocument(
			uriutil.FileURI(consumerPath), consumerSource, 1,
		)
		offset := uint32(strings.Index(consumerSource, marker) + inside)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.DefinitionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		return NewAdminDefinitionProvider(index).GetDefinition(
			context.Background(),
			&lsp.DefinitionRequest{
				DefinitionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, Language: document.SyntaxLanguage,
					DocumentContent: document.Text, DocumentTree: document.SyntaxTree,
					LineIndex: document.LineIndex, Root: document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
					Token: document.SyntaxTree.Root.TokenAtOffset(offset),
				},
			},
		)
	}
	propLocations := definitionAt("live-required", 2)
	require.Len(t, propLocations, 1)
	assert.Equal(t, uriutil.FileURI(definitionPath), propLocations[0].URI)
	assert.Equal(t, 4, propLocations[0].Range.Start.Line)
	slotLocations := definitionAt("live-slot", 2)
	require.Len(t, slotLocations, 1)
	assert.Equal(t, uriutil.FileURI(templatePath), slotLocations[0].URI)
	assert.Equal(t, 1, slotLocations[0].Range.Start.Line)
}

func TestAdminScopedSlotBindingDefinitionUsesContractMember(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	declarationPath := filepath.Join(adminRoot, "meteor/MtButton.d.ts")
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "mt-button", FilePath: declarationPath,
		Slots: []admin.VueComponentSlot{{
			Name: "iconFront", FilePath: declarationPath, Line: 10,
			Members: []admin.VueComponentSlotMember{{
				Name: "size", Type: "number", FilePath: declarationPath, Line: 12,
				NameRange: admin.AdminSourceRange{
					StartLine: 11, StartCharacter: 8,
					EndLine: 11, EndCharacter: 12,
					Declaration: true, Identifier: true,
				},
			}},
		}},
	}))
	source := `<mt-button><template #iconFront="{ size: iconSize }">{{ iconSize }}</template></mt-button>`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "view.html.twig")), source, 1,
	)
	offset := uint32(strings.LastIndex(source, "iconSize") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(declarationPath), locations[0].URI)
	assert.Equal(t, 11, locations[0].Range.Start.Line)
	assert.Equal(t, 8, locations[0].Range.Start.Character)
	assert.Equal(t, 12, locations[0].Range.End.Character)

	wholeObjectSource := `<mt-button><template #iconFront="props">{{ props.size }}</template></mt-button>`
	wholeDocument := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "whole.html.twig")),
		wholeObjectSource,
		1,
	)
	wholeOffset := uint32(strings.LastIndex(wholeObjectSource, "size") + 1)
	wholeLine, wholeCharacter := wholeDocument.LineIndex.PositionUTF16(
		wholeOffset,
	)
	wholeParams := &protocol.DefinitionParams{}
	wholeParams.TextDocument.URI = wholeDocument.URI
	wholeParams.Position.Line = int(wholeLine)
	wholeParams.Position.Character = int(wholeCharacter)
	wholeLocations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: wholeParams,
			SyntaxContext: lsp.SyntaxContext{
				Document: wholeDocument, DocumentContent: wholeDocument.Text,
				DocumentTree: wholeDocument.SyntaxTree,
				LineIndex:    wholeDocument.LineIndex,
				Root:         wholeDocument.SyntaxTree.Root,
				Node: wholeDocument.SyntaxTree.Root.NodeAtOffset(
					wholeOffset,
				),
				Token: wholeDocument.SyntaxTree.Root.TokenAtOffset(
					wholeOffset,
				),
			},
		},
	)
	require.Len(t, wholeLocations, 1)
	assert.Equal(t, uriutil.FileURI(declarationPath), wholeLocations[0].URI)
	assert.Equal(t, 11, wholeLocations[0].Range.Start.Line)
	assert.Equal(t, 8, wholeLocations[0].Range.Start.Character)
	assert.Equal(t, 12, wholeLocations[0].Range.End.Character)
}

func TestAdminScopedSlotDefinitionUsesEveryDynamicCandidate(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	firstPath := filepath.Join(adminRoot, "a/card.twig")
	secondPath := filepath.Join(adminRoot, "b/card.twig")
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-card-a", FilePath: filepath.Join(adminRoot, "a/index.ts"),
			Slots: []admin.VueComponentSlot{{
				Name: "row", Members: []admin.VueComponentSlotMember{{
					Name: "item", Type: "Product", FilePath: firstPath, Line: 4,
				}},
			}},
		},
		{
			Name: "sw-card-b", FilePath: filepath.Join(adminRoot, "b/index.ts"),
			Slots: []admin.VueComponentSlot{{
				Name: "row", Members: []admin.VueComponentSlotMember{{
					Name: "item", Type: "Category", FilePath: secondPath, Line: 8,
				}},
			}},
		},
	} {
		require.NoError(t, index.SaveComponent(component))
	}
	provider := NewAdminDefinitionProvider(index)
	for _, test := range []struct {
		name, source, needle string
	}{
		{
			name:   "destructured local",
			source: `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="{ item: row }">{{ row }}</template></component>`,
			needle: "{{ row",
		},
		{
			name:   "whole object member",
			source: `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="props">{{ props.item }}</template></component>`,
			needle: "item",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")),
				test.source, 1,
			)
			offset := uint32(strings.Index(test.source, test.needle) + len(test.needle))
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.DefinitionParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			locations := provider.GetDefinition(
				context.Background(),
				&lsp.DefinitionRequest{
					DefinitionParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root:  document.SyntaxTree.Root,
						Node:  document.SyntaxTree.Root.NodeAtOffset(offset - 1),
						Token: document.SyntaxTree.Root.TokenAtOffset(offset - 1),
					},
				},
			)
			require.Len(t, locations, 2)
			assert.ElementsMatch(t, []string{
				uriutil.FileURI(firstPath), uriutil.FileURI(secondPath),
			}, []string{locations[0].URI, locations[1].URI})
		})
	}
}

func TestAdminDynamicSlotFamilyDefinition(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(root, "src/Resources/app/administration/src")
	templatePath := filepath.Join(adminRoot, "grid/sw-grid.html.twig")
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-grid", TemplatePath: templatePath,
		Slots: []admin.VueComponentSlot{{
			NamePrefix: "column-", FilePath: templatePath, Line: 27,
			Members: []admin.VueComponentSlotMember{{
				Name: "item", FilePath: templatePath, Line: 28,
			}},
		}},
	}))
	source := `<sw-grid><template #column-name="{ item }">{{ item }}</template></sw-grid>`
	provider := NewAdminDefinitionProvider(index)
	for _, test := range []struct {
		name, needle string
		line         int
	}{
		{name: "slot name", needle: "column-name", line: 26},
		{name: "payload member", needle: "{{ item", line: 27},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")),
				source, 1,
			)
			offset := uint32(strings.Index(source, test.needle) + len(test.needle))
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.DefinitionParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			locations := provider.GetDefinition(
				context.Background(),
				&lsp.DefinitionRequest{
					DefinitionParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root: document.SyntaxTree.Root,
						Node: document.SyntaxTree.Root.NodeAtOffset(
							offset - 1,
						),
						Token: document.SyntaxTree.Root.TokenAtOffset(
							offset - 1,
						),
					},
				},
			)
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(templatePath), locations[0].URI)
			assert.Equal(t, test.line, locations[0].Range.Start.Line)
		})
	}
}

func TestAdminSlotDefinitionResolvesDynamicComponentOwner(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	firstPath := filepath.Join(adminRoot, "a/card.html.twig")
	secondPath := filepath.Join(adminRoot, "b/card.html.twig")
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-card-a", FilePath: filepath.Join(adminRoot, "a/index.ts"),
			TemplatePath: firstPath,
			Slots:        []admin.VueComponentSlot{{Name: "header", FilePath: firstPath, Line: 4}},
		},
		{
			Name: "sw-card-b", FilePath: filepath.Join(adminRoot, "b/index.ts"),
			TemplatePath: secondPath,
			Slots:        []admin.VueComponentSlot{{Name: "header", FilePath: secondPath, Line: 8}},
		},
	} {
		require.NoError(t, index.SaveComponent(component))
	}
	source := `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #header /></component>`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")), source, 1,
	)
	offset := uint32(strings.Index(source, "header") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 2)
	assert.ElementsMatch(t, []string{
		uriutil.FileURI(firstPath), uriutil.FileURI(secondPath),
	}, []string{locations[0].URI, locations[1].URI})
}

func TestAdminThisMemberDefinitionUsesInheritedOrigin(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	definitionPath := filepath.Join(
		root,
		"src/Resources/app/administration/src/component/sw-child/index.js",
	)
	parentPath := filepath.Join(
		root,
		"src/Resources/app/administration/src/component/sw-parent/index.js",
	)
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-child", FilePath: definitionPath, DefinitionPath: definitionPath,
		Computed: []string{"parentTitle"},
		Members: []admin.VueComponentMember{{
			Name: "parentTitle", Kind: admin.ComponentMemberComputed,
			FilePath: parentPath, Line: 12,
		}},
	}))
	source := `export default { computed: { value() { return this.parentTitle; } } };`
	document := lsp.NewTextDocument(uriutil.FileURI(definitionPath), source, 1)
	offset := uint32(strings.Index(source, "parentTitle") + 1)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(parentPath), locations[0].URI)
	assert.Equal(t, 11, locations[0].Range.Start.Line)
}

func TestAdminRuntimeRegistryDefinitions(t *testing.T) {
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
    actions: {
        load() {},
    },
});
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product',
    roles: {
        viewer: {
            privileges: ['product:read'],
        },
    },
});
`),
	)))
	provider := NewAdminDefinitionProvider(index)
	documentPath := filepath.Join(filepath.Dir(registrationPath), "consumer.ts")

	tests := []struct {
		name         string
		source       string
		needle       string
		expectedLine int
	}{
		{"service", `Shopware.Service('acl')`, "acl", 1},
		{"store", `Shopware.Store.get('profile')`, "profile", 2},
		{"store unregister", `Shopware.Store.unregister('profile')`, "profile", 2},
		{"store member", `Shopware.Store.get('profile').load()`, "load", 4},
		{"privilege role", `this.acl.can('product.viewer')`, "product.viewer", 10},
		{"permission", `const route = { privilege: 'product:read' }`, "product:read", 11},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(documentPath), test.source, 1,
			)
			offset := uint32(strings.LastIndex(test.source, test.needle) + 1)
			params := &protocol.DefinitionParams{}
			params.TextDocument.URI = document.URI
			locations := provider.GetDefinition(
				context.Background(),
				&lsp.DefinitionRequest{
					DefinitionParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root:  document.SyntaxTree.Root,
						Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
						Token: document.SyntaxTree.Root.TokenAtOffset(offset),
					},
				},
			)
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(registrationPath), locations[0].URI)
			assert.Equal(t, test.expectedLine, locations[0].Range.Start.Line)
		})
	}
}

func TestAdminApplicationContainerDefinitions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	globalPath := filepath.Join(adminRoot, "global.types.ts")
	globalSource := `export interface SubContainer<T extends string> { $list(): string[]; }
declare global {
    interface FactoryContainer extends SubContainer<'factory'> {
        locale: LocaleFactory;
    }
    interface ServiceContainer extends SubContainer<'service'> {
        acl: AclService;
    }
}`
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		globalPath, []byte(globalSource),
	)))
	servicePath := filepath.Join(adminRoot, "services.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		servicePath,
		[]byte(`Shopware.Application.addServiceProvider('acl', factory);`),
	)))
	provider := NewAdminDefinitionProvider(index)
	documentPath := filepath.Join(adminRoot, "consumer.ts")
	definition := func(source, needle string) []protocol.Location {
		t.Helper()
		document := lsp.NewTextDocument(
			uriutil.FileURI(documentPath), source, 1,
		)
		offset := strings.LastIndex(source, needle)
		require.NotEqual(t, -1, offset)
		params := &protocol.DefinitionParams{}
		params.TextDocument.URI = document.URI
		return provider.GetDefinition(
			context.Background(),
			&lsp.DefinitionRequest{
				DefinitionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root:  document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(uint32(offset + 1)),
					Token: document.SyntaxTree.Root.TokenAtOffset(uint32(offset + 1)),
				},
			},
		)
	}

	factoryLocations := definition(
		`Application.getContainer('factory').locale`, "locale",
	)
	require.Len(t, factoryLocations, 1)
	assert.Equal(t, uriutil.FileURI(globalPath), factoryLocations[0].URI)
	assert.Equal(t, 3, factoryLocations[0].Range.Start.Line)
	assert.Equal(t, 8, factoryLocations[0].Range.Start.Character)
	assert.Equal(t, 14, factoryLocations[0].Range.End.Character)

	serviceLocations := definition(`function run() {
    const services = Shopware.Application.getContainer('service');
    return services.acl;
}`, "acl")
	require.Len(t, serviceLocations, 1)
	assert.Equal(t, uriutil.FileURI(servicePath), serviceLocations[0].URI)
	assert.Equal(t, 0, serviceLocations[0].Range.Start.Line)
}

func TestAdminShopwareContextDefinitionUsesNestedTypeRange(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	contextPath := filepath.Join(adminRoot, "app/composables/use-context.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		contextPath,
		[]byte(`export interface ContextState {
    app: { environment: null | string };
    api: {
        languageId: null | string;
    };
}`),
	)))
	documentPath := filepath.Join(adminRoot, "module/example/index.ts")
	source := `const languageId = Shopware.Context.api.languageId;`
	document := lsp.NewTextDocument(uriutil.FileURI(documentPath), source, 1)
	offset := uint32(strings.LastIndex(source, "languageId") + 1)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(contextPath), locations[0].URI)
	assert.Equal(t, 3, locations[0].Range.Start.Line)
	assert.Equal(t, 8, locations[0].Range.Start.Character)
	assert.Equal(t, 18, locations[0].Range.End.Character)
}

func TestAdminShopwareUtilsDefinitionUsesExportRange(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	utilPath := filepath.Join(adminRoot, "core/service/util.service.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		utilPath,
		[]byte(`export const format = { date };
export default { format };
function date(value: string): string { return value; }`),
	)))
	documentPath := filepath.Join(adminRoot, "module/example/index.ts")
	source := `const { date: formatDate } = Shopware.Utils.format;
formatDate('2026-01-01');`
	document := lsp.NewTextDocument(uriutil.FileURI(documentPath), source, 1)
	offset := uint32(strings.LastIndex(source, "formatDate") + 1)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(utilPath), locations[0].URI)
	assert.Equal(t, 0, locations[0].Range.Start.Line)
	assert.Equal(t, 24, locations[0].Range.Start.Character)
	assert.Equal(t, 28, locations[0].Range.End.Character)
}

func TestAdminShopwareEventBusDefinitionUsesEventKeyRange(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	utilPath := filepath.Join(adminRoot, "core/service/util.service.ts")
	eventBusPath := filepath.Join(
		adminRoot, "core/service/utils/eventBus.utils.ts",
	)
	eventBusSource := `interface Events extends Record<string | symbol, unknown> {
    'save-event': { id: string };
}
const emitter = mitt<Events>();
export default emitter;`
	for path, source := range map[string]string{
		utilPath: `import EventBus from './utils/eventBus.utils';
export default { EventBus };`,
		eventBusPath: eventBusSource,
	} {
		require.NoError(t, index.Index(indexerpkg.NewParsedFile(
			path, []byte(source),
		)))
	}
	documentPath := filepath.Join(adminRoot, "module/example/index.ts")
	source := `const { EventBus } = Shopware.Utils;
EventBus.on('save-event', handler);`
	document := lsp.NewTextDocument(uriutil.FileURI(documentPath), source, 1)
	offset := uint32(strings.LastIndex(source, "save-event") + 1)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(eventBusPath), locations[0].URI)
	declarationOffset := uint32(strings.Index(eventBusSource, "save-event"))
	lineIndex := jssyntax.NewLineIndex(eventBusSource)
	line, character := lineIndex.PositionUTF16(declarationOffset)
	assert.Equal(t, int(line), locations[0].Range.Start.Line)
	assert.Equal(t, int(character), locations[0].Range.Start.Character)
	assert.Equal(t, int(character)+len("save-event"), locations[0].Range.End.Character)
}

func TestAdminVueForAndEventLexicalDefinitions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	eventPath := filepath.Join(root, "meteor/MtSwitch.d.ts")
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "mt-switch",
		Events: []admin.VueComponentEvent{{
			Name: "update:modelValue", Type: "(value: boolean) => any",
			FilePath: eventPath, Line: 22,
		}},
	}))
	provider := NewAdminDefinitionProvider(index)
	for _, test := range []struct {
		name, source, marker string
		expectedURI          string
		expectedLine         int
		expectedCharacter    int
	}{
		{
			name:         "v-for local",
			source:       `<div v-for="(item, index) in items">{{ item.name }}</div>`,
			marker:       "item.name",
			expectedLine: 0, expectedCharacter: 13,
		},
		{
			name:   "event payload",
			source: `<mt-switch @update:model-value="save($event)" />`,
			marker: "$event", expectedURI: uriutil.FileURI(eventPath),
			expectedLine: 21,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(
					root, "Resources/app/administration/view.html.twig",
				)),
				test.source,
				1,
			)
			offset := uint32(strings.LastIndex(test.source, test.marker) + 1)
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.DefinitionParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			locations := provider.GetDefinition(
				context.Background(),
				&lsp.DefinitionRequest{
					DefinitionParams: params,
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
			require.Len(t, locations, 1)
			expectedURI := test.expectedURI
			if expectedURI == "" {
				expectedURI = document.URI
			}
			assert.Equal(t, expectedURI, locations[0].URI)
			assert.Equal(t, test.expectedLine, locations[0].Range.Start.Line)
			assert.Equal(
				t, test.expectedCharacter,
				locations[0].Range.Start.Character,
			)
		})
	}
}

func TestAdminModelDefinitionReturnsPropAndEvent(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	propPath := filepath.Join(root, "component/props.ts")
	eventPath := filepath.Join(root, "component/events.ts")
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "mt-switch",
		Props: []admin.VueComponentProp{{
			Name: "checked", Type: "Boolean", FilePath: propPath, Line: 7,
		}},
		Events: []admin.VueComponentEvent{{
			Name: "update:checked", Type: "(value: boolean) => void",
			FilePath: eventPath, Line: 12,
		}},
	}))
	source := `<mt-switch v-model:checked="enabled" />`
	templatePath := filepath.Join(
		root, "Resources/app/administration/view.html.twig",
	)
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	offset := uint32(strings.Index(source, "checked") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 2)
	assert.Equal(t, uriutil.FileURI(propPath), locations[0].URI)
	assert.Equal(t, 6, locations[0].Range.Start.Line)
	assert.Equal(t, uriutil.FileURI(eventPath), locations[1].URI)
	assert.Equal(t, 11, locations[1].Range.Start.Line)
}

func TestAdminNestedVueMemberDefinitionUsesTypeDeclaration(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "Resources/app/administration/src/component/sw-view",
	)
	definitionPath := filepath.Join(adminRoot, "index.ts")
	templatePath := filepath.Join(adminRoot, "view.html.twig")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		definitionPath,
		[]byte("interface Manufacturer { name: string; }\n"+
			"interface Product { manufacturer: Manufacturer; getManufacturer(): Manufacturer; }"),
	)))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-view", FilePath: definitionPath,
		DefinitionPath: definitionPath, TemplatePath: templatePath,
		Members: []admin.VueComponentMember{
			{
				Name: "productsById", Kind: admin.ComponentMemberComputed,
				Type: "Record<string, Product>", FilePath: definitionPath,
			},
			{
				Name: "selectedProduct", Kind: admin.ComponentMemberProp,
				Type: "Product", FilePath: definitionPath,
			},
		},
	}))
	source := `<div v-for="product in Object.values(productsById)">{{ product.manufacturer.name }}</div>`
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	offset := uint32(strings.Index(source, "name }}") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(definitionPath), locations[0].URI)
	assert.Equal(t, 0, locations[0].Range.Start.Line)

	indexedSource := `<div :title="productsById[selectedProduct.manufacturer.name].manufacturer.name"></div>`
	indexedDocument := lsp.NewTextDocument(
		uriutil.FileURI(templatePath), indexedSource, 1,
	)
	indexedOffset := uint32(strings.LastIndex(indexedSource, "name\"") + 1)
	indexedLine, indexedCharacter := indexedDocument.LineIndex.PositionUTF16(
		indexedOffset,
	)
	indexedParams := &protocol.DefinitionParams{}
	indexedParams.TextDocument.URI = indexedDocument.URI
	indexedParams.Position.Line = int(indexedLine)
	indexedParams.Position.Character = int(indexedCharacter)
	indexedLocations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: indexedParams,
			SyntaxContext: lsp.SyntaxContext{
				Document:        indexedDocument,
				DocumentContent: indexedDocument.Text,
				DocumentTree:    indexedDocument.SyntaxTree,
				LineIndex:       indexedDocument.LineIndex,
				Root:            indexedDocument.SyntaxTree.Root,
				Node: indexedDocument.SyntaxTree.Root.NodeAtOffset(
					indexedOffset,
				),
				Token: indexedDocument.SyntaxTree.Root.TokenAtOffset(
					indexedOffset,
				),
			},
		},
	)
	require.Len(t, indexedLocations, 1)
	assert.Equal(
		t, uriutil.FileURI(definitionPath), indexedLocations[0].URI,
	)
	assert.Equal(t, 0, indexedLocations[0].Range.Start.Line)

	instanceSource := `<div :title="selectedProduct.getManufacturer().name"></div>`
	instanceDocument := lsp.NewTextDocument(
		uriutil.FileURI(templatePath), instanceSource, 1,
	)
	instanceOffset := uint32(strings.Index(instanceSource, "name\"") + 1)
	instanceLine, instanceCharacter := instanceDocument.LineIndex.PositionUTF16(
		instanceOffset,
	)
	instanceParams := &protocol.DefinitionParams{}
	instanceParams.TextDocument.URI = instanceDocument.URI
	instanceParams.Position.Line = int(instanceLine)
	instanceParams.Position.Character = int(instanceCharacter)
	instanceLocations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: instanceParams,
			SyntaxContext: lsp.SyntaxContext{
				Document:        instanceDocument,
				DocumentContent: instanceDocument.Text,
				DocumentTree:    instanceDocument.SyntaxTree,
				LineIndex:       instanceDocument.LineIndex,
				Root:            instanceDocument.SyntaxTree.Root,
				Node: instanceDocument.SyntaxTree.Root.NodeAtOffset(
					instanceOffset,
				),
			},
		},
	)
	require.Len(t, instanceLocations, 1)
	assert.Equal(t, uriutil.FileURI(definitionPath), instanceLocations[0].URI)
	assert.Equal(t, 0, instanceLocations[0].Range.Start.Line)
}

func TestAdminTwigPrivilegeDefinition(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "privileges.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product', roles: { viewer: { privileges: ['product:read'] } },
});`),
	)))
	source := `<mt-button :disabled="acl.can('product.viewer')" />`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "view.html.twig")), source, 1,
	)
	offset := uint32(strings.Index(source, "product.viewer") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root: document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(registrationPath), locations[0].URI)
	assert.Equal(t, 1, locations[0].Range.Start.Line)
}

func TestAdminBuiltinPrivilegeHasNoDefinition(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	locations := NewAdminDefinitionProvider(index).privilegeDefinition(
		admin.AdminPrivilegeAdministrator,
	)
	assert.Empty(t, locations)
}

func TestAdminTwigModuleRouteDefinition(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "module/sw-product/index.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`Shopware.Module.register('sw-product', {
    routes: {
        detail: { path: 'detail', component: 'sw-product-detail' },
    },
});`),
	)))
	source := `<router-link :to="{ name: 'sw.product.detail' }" />`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "view.html.twig")), source, 1,
	)
	offset := uint32(strings.Index(source, "sw.product.detail") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root: document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(registrationPath), locations[0].URI)
	assert.Equal(t, 2, locations[0].Range.Start.Line)
}

func TestAdminServiceDefinitionUsesImplementation(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "main.ts")
	implementationPath := filepath.Join(adminRoot, "acl.service.ts")
	require.NoError(t, os.MkdirAll(filepath.Dir(implementationPath), 0o755))
	require.NoError(t, os.WriteFile(implementationPath, nil, 0o644))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`
import AclService from './acl.service';
Shopware.Application.addServiceProvider('acl', () => new AclService());
`),
	)))

	source := `Shopware.Service('acl')`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")), source, 1,
	)
	offset := uint32(strings.Index(source, "acl") + 1)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(implementationPath), locations[0].URI)
	assert.Equal(t, 0, locations[0].Range.Start.Line)
}

func TestAdminImportedSetupStoreMemberDefinition(t *testing.T) {
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
function setCurrentUser() {}
export default function useSession() {
    return { currentUser, setCurrentUser };
}`),
	)))

	source := `Shopware.Store.get('session').setCurrentUser()`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")), source, 1,
	)
	offset := uint32(strings.Index(source, "setCurrentUser") + 1)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(factoryPath), locations[0].URI)
	assert.Equal(t, 2, locations[0].Range.Start.Line)
}

func TestAdminComponentAndModuleRegistryDefinitions(t *testing.T) {
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
    routes: { index: { path: 'index', component: 'sw-card' } },
});`),
	)))
	provider := NewAdminDefinitionProvider(index)
	for _, test := range []struct {
		name, source, needle string
		line                 int
	}{
		{
			"component registry", `Shopware.Component.getComponentRegistry().has('sw-card')`,
			"sw-card", 0,
		},
		{
			"module registry", `Module.getModuleRegistry().get('sw-product')`,
			"sw-product", 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")),
				test.source,
				1,
			)
			offset := uint32(strings.Index(test.source, test.needle) + 1)
			params := &protocol.DefinitionParams{}
			params.TextDocument.URI = document.URI
			locations := provider.GetDefinition(
				context.Background(),
				&lsp.DefinitionRequest{
					DefinitionParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root:  document.SyntaxTree.Root,
						Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
						Token: document.SyntaxTree.Root.TokenAtOffset(offset),
					},
				},
			)
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(registrationPath), locations[0].URI)
			assert.Equal(t, test.line, locations[0].Range.Start.Line)
		})
	}
}

func TestAdminDirectiveDefinitionsFromJavaScriptAndTwig(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "app/directive/tooltip.directive.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`Shopware.Directive.register('tooltip', {});`),
	)))
	provider := NewAdminDefinitionProvider(index)
	for _, test := range []struct {
		source, needle, extension string
	}{
		{`Shopware.Directive.getByName('tooltip')`, "tooltip", ".ts"},
		{`<div v-tooltip.bottom="message"></div>`, "tooltip", ".html.twig"},
	} {
		document := lsp.NewTextDocument(
			uriutil.FileURI(filepath.Join(adminRoot, "consumer"+test.extension)),
			test.source,
			1,
		)
		offset := uint32(strings.Index(test.source, test.needle) + 1)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.DefinitionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		locations := provider.GetDefinition(
			context.Background(),
			&lsp.DefinitionRequest{
				DefinitionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root:  document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
					Token: document.SyntaxTree.Root.TokenAtOffset(offset),
				},
			},
		)
		require.Len(t, locations, 1)
		assert.Equal(t, uriutil.FileURI(registrationPath), locations[0].URI)
		assert.Equal(t, 0, locations[0].Range.Start.Line)
	}
}

func TestAdminFilterDefinition(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "app/filter/currency.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`Shopware.Filter.register('currency', value => value);`),
	)))
	source := `Shopware.Filter.getByName('currency')`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")), source, 1,
	)
	offset := uint32(strings.Index(source, "currency") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(registrationPath), locations[0].URI)
}

func TestAdminCMSRegistryDefinitions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	elementPath := filepath.Join(adminRoot, "cms/hero.ts")
	blockPath := filepath.Join(adminRoot, "cms/hero-grid.ts")
	componentPath := filepath.Join(adminRoot, "component/sw-cms-el-hero/index.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		elementPath,
		[]byte(`Shopware.Service('cmsService').registerCmsElement({ name: 'hero' });`),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		blockPath,
		[]byte(`Shopware.Service('cmsService').registerCmsBlock({ name: 'hero-grid', slots: { content: { type: 'hero' } } });`),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		componentPath,
		[]byte(`Shopware.Component.register('sw-cms-el-hero', { props: ['config'] });`),
	)))
	provider := NewAdminDefinitionProvider(index)
	for _, test := range []struct {
		source, needle, target string
	}{
		{
			`cmsService.getCmsElementConfigByName('hero')`,
			"hero", elementPath,
		},
		{
			`cmsService.getCmsBlockConfigByName('hero-grid')`,
			"hero-grid", blockPath,
		},
		{
			`cmsService.registerCmsBlock({ name: 'other', slots: { content: { type: 'hero' } } })`,
			"hero", elementPath,
		},
		{
			`cmsService.registerCmsElement({ name: 'other', component: 'sw-cms-el-hero' })`,
			"sw-cms-el-hero", componentPath,
		},
	} {
		document := lsp.NewTextDocument(
			uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")),
			test.source,
			1,
		)
		offset := uint32(strings.LastIndex(test.source, test.needle) + 1)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.DefinitionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		locations := provider.GetDefinition(
			context.Background(),
			&lsp.DefinitionRequest{
				DefinitionParams: params,
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
		require.Len(t, locations, 1)
		assert.Equal(t, uriutil.FileURI(test.target), locations[0].URI)
	}
}

func TestAdminDirectiveDefinitionPrefersTemplateLocalDeclaration(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	globalPath := filepath.Join(adminRoot, "app/directive/hide.ts")
	localPath := filepath.Join(adminRoot, "component/sw-owner/index.ts")
	templatePath := filepath.Join(
		adminRoot, "component/sw-owner/sw-owner.html.twig",
	)
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		globalPath, []byte(`Shopware.Directive.register('hide', {});`),
	)))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-owner", FilePath: localPath, DefinitionPath: localPath,
		TemplatePath: templatePath,
		LocalDirectives: []admin.VueLocalDirective{{
			Name: "hide", FilePath: localPath, Line: 7,
		}},
	}))
	source := `<div v-hide="hidden"></div>`
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	offset := uint32(strings.Index(source, "hide") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := NewAdminDefinitionProvider(index).GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(localPath), locations[0].URI)
	assert.Equal(t, 6, locations[0].Range.Start.Line)
}

func TestAdminMarkupEventAndInheritedSlotDefinitions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	eventPath := filepath.Join(adminRoot, "component/sw-parent/index.js")
	parentTemplate := filepath.Join(
		adminRoot, "component/sw-parent/sw-parent.html.twig",
	)
	childTemplate := filepath.Join(
		adminRoot, "component/sw-child/sw-child.html.twig",
	)
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-child", FilePath: eventPath, DefinitionPath: eventPath,
		TemplatePath: childTemplate,
		Events: []admin.VueComponentEvent{{
			Name: "update:modelValue", FilePath: eventPath, Line: 11,
			NameRange: admin.AdminSourceRange{
				StartLine: 10, StartCharacter: 8,
				EndLine: 10, EndCharacter: 25,
				Declaration: true,
			},
		}},
		Slots: []admin.VueComponentSlot{{
			Name: "header", FilePath: parentTemplate, Line: 7,
			NameRange: admin.AdminSourceRange{
				StartLine: 6, StartCharacter: 12,
				EndLine: 6, EndCharacter: 18,
				Declaration: true,
			},
		}},
	}))
	provider := NewAdminDefinitionProvider(index)
	for _, test := range []struct {
		name, source, needle, expectedPath string
		expectedLine                       int
	}{
		{
			"event with modifier",
			`<sw-child @update:model-value.stop="onUpdate" />`,
			"update:model-value", eventPath, 10,
		},
		{
			"inherited slot owner",
			`<sw-child><template #header></template></sw-child>`,
			"header", parentTemplate, 6,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(childTemplate), test.source, 1,
			)
			offset := uint32(strings.Index(test.source, test.needle) + 1)
			params := &protocol.DefinitionParams{}
			params.TextDocument.URI = document.URI
			locations := provider.GetDefinition(
				context.Background(),
				&lsp.DefinitionRequest{
					DefinitionParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root:  document.SyntaxTree.Root,
						Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
						Token: document.SyntaxTree.Root.TokenAtOffset(offset),
					},
				},
			)
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(test.expectedPath), locations[0].URI)
			assert.Equal(t, test.expectedLine, locations[0].Range.Start.Line)
			if test.name == "event with modifier" {
				assert.Equal(t, 8, locations[0].Range.Start.Character)
				assert.Equal(t, 25, locations[0].Range.End.Character)
			} else {
				assert.Equal(t, 12, locations[0].Range.Start.Character)
				assert.Equal(t, 18, locations[0].Range.End.Character)
			}
		})
	}
}

func TestAdminDynamicComponentSelectorAndPropDefinitions(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "component/sw-card/index.ts")
	panelDefinitionPath := filepath.Join(adminRoot, "component/sw-panel/index.ts")
	templatePath := filepath.Join(adminRoot, "consumer.html.twig")
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: definitionPath,
		DefinitionPath: definitionPath,
		Props: []admin.VueComponentProp{{
			Name: "title", FilePath: definitionPath, Line: 5,
		}},
	}))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-panel", FilePath: panelDefinitionPath,
		DefinitionPath: panelDefinitionPath,
		Props: []admin.VueComponentProp{{
			Name: "title", FilePath: panelDefinitionPath, Line: 8,
		}},
	}))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-host", FilePath: filepath.Join(adminRoot, "sw-host/index.ts"),
		TemplatePath: templatePath,
		Members: []admin.VueComponentMember{{
			Name: "dynamicCard", Kind: admin.ComponentMemberComputed,
			ReturnExpressions: []string{"'sw-card'", "'sw-panel'"},
			ReturnsComplete:   true,
		}},
	}))
	source := `<component :is="'sw-card'" :title="title" />`
	provider := NewAdminDefinitionProvider(index)
	for _, test := range []struct {
		needle string
		line   int
	}{
		{"sw-card", 0},
		{":title", 4},
	} {
		document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
		offset := uint32(strings.Index(source, test.needle) + 1)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.DefinitionParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		locations := provider.GetDefinition(
			context.Background(),
			&lsp.DefinitionRequest{
				DefinitionParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root:  document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
					Token: document.SyntaxTree.Root.TokenAtOffset(offset),
				},
			},
		)
		require.Len(t, locations, 1)
		assert.Equal(t, uriutil.FileURI(definitionPath), locations[0].URI)
		assert.Equal(t, test.line, locations[0].Range.Start.Line)
	}

	unionSource := `<component :is="active ? 'sw-card' : 'sw-panel'" :title="title" />`
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), unionSource, 1)
	offset := uint32(strings.Index(unionSource, ":title") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations := provider.GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 2)
	assert.ElementsMatch(t, []string{
		uriutil.FileURI(definitionPath), uriutil.FileURI(panelDefinitionPath),
	}, []string{locations[0].URI, locations[1].URI})

	inferredSource := `<component :is="dynamicCard" :title="title" />`
	document = lsp.NewTextDocument(uriutil.FileURI(templatePath), inferredSource, 1)
	offset = uint32(strings.Index(inferredSource, ":title") + 1)
	line, character = document.LineIndex.PositionUTF16(offset)
	params = &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations = provider.GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 2)
	assert.ElementsMatch(t, []string{
		uriutil.FileURI(definitionPath), uriutil.FileURI(panelDefinitionPath),
	}, []string{locations[0].URI, locations[1].URI})

	objectSource := `<component :is="dynamicCard" v-bind="{ title: heading }" />`
	document = lsp.NewTextDocument(uriutil.FileURI(templatePath), objectSource, 1)
	offset = uint32(strings.Index(objectSource, "title") + 1)
	line, character = document.LineIndex.PositionUTF16(offset)
	params = &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	locations = provider.GetDefinition(
		context.Background(),
		&lsp.DefinitionRequest{
			DefinitionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.Len(t, locations, 2)
	assert.ElementsMatch(t, []string{
		uriutil.FileURI(definitionPath), uriutil.FileURI(panelDefinitionPath),
	}, []string{locations[0].URI, locations[1].URI})
}
