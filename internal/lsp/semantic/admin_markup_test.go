package semantic

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestAdminMarkupSemanticTokensColorComponentsPropsEventsAndSlots(
	t *testing.T,
) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		filepath.Join(adminRoot, "component/sw-card/index.ts"),
		[]byte(`Shopware.Directive.register('tooltip', {});
		Shopware.Component.register('sw-card', {
			props: { title: String, modelValue: String }, emits: ['update:modelValue'],
		});`),
	)))
	component, err := adminIndex.GetEffectiveComponent("sw-card")
	require.NoError(t, err)
	require.NotNil(t, component)
	require.Contains(t, component.Emits, "update:modelValue")
	require.NoError(t, adminIndex.SaveComponentDefinition(
		"sw-card",
		admin.ComponentDefinition{
			FilePath: component.FilePath,
			Props:    component.Props,
			Emits:    component.Emits,
			Events:   component.Events,
			Slots: []admin.VueComponentSlot{
				{Name: "footer"}, {NamePrefix: "column-"},
			},
		},
	))
	source := `<sw-card v-tooltip.bottom="tip" :title.sync="title" v-model.trim="value" @update:model-value.stop="save" class="plain" v-if="visible" @click="click">
	<template #footer>Content</template><template #column-name>Column</template><template #missing>Ignored</template>
</sw-card>
<sw-unknown title="ignored" />
<div class="plain" v-hide="hidden"></div>`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "component.html.twig")),
		source,
		1,
	)
	templatePath, err := uriutil.Path(document.URI)
	require.NoError(t, err)
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name: "sw-owner", FilePath: filepath.Join(adminRoot, "owner.ts"),
		TemplatePath:    templatePath,
		LocalDirectives: []admin.VueLocalDirective{{Name: "hide"}},
	}))
	tokens, err := NewAdminMarkupProvider(adminIndex).GetSemanticTokens(
		context.Background(),
		&lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)

	expected := []struct {
		text      string
		tokenType uint32
	}{
		{"sw-card", protocol.SemanticTokenClass},
		{"v-tooltip.bottom", protocol.SemanticTokenFunction},
		{":title.sync", protocol.SemanticTokenProperty},
		{"v-model.trim", protocol.SemanticTokenProperty},
		{"@update:model-value.stop", protocol.SemanticTokenFunction},
		{"#footer", protocol.SemanticTokenProperty},
		{"#column-name", protocol.SemanticTokenProperty},
		{"sw-card", protocol.SemanticTokenClass},
		{"v-hide", protocol.SemanticTokenFunction},
	}
	require.Len(t, tokens, len(expected))
	for index, current := range expected {
		require.Equal(t, current.tokenType, tokens[index].Type)
		require.Equal(
			t,
			current.text,
			string(document.Text[tokens[index].Range.Start:tokens[index].Range.End]),
		)
	}
}

func TestAdminMarkupSemanticTokensResolveTemplateScopedComponent(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "component/sw-wrapper/index.ts")
	templatePath := filepath.Join(
		adminRoot, "component/sw-wrapper/sw-wrapper.html.twig",
	)
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		definitionPath,
		[]byte(`
import MtCard from '@shopware-ag/meteor-component-library/dist/esm/MtCard';
import template from './sw-wrapper.html.twig';
export default Shopware.Component.wrapComponentConfig({
    template,
    components: { 'mt-card-original': MtCard },
    props: { title: String },
});
`),
	)))
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name: "mt-card", FilePath: filepath.Join(root, "meteor/MtCard.d.ts"),
		Props: []admin.VueComponentProp{{Name: "title", Type: "String"}},
	}))
	source := `<mt-card-original title="Card"></mt-card-original><mt-not-local />`
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	tokens, err := NewAdminMarkupProvider(adminIndex).GetSemanticTokens(
		context.Background(), &lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)
	require.Len(t, tokens, 3)
	require.Equal(t, protocol.SemanticTokenClass, tokens[0].Type)
	require.Equal(t, "mt-card-original", string(
		document.Text[tokens[0].Range.Start:tokens[0].Range.End],
	))
	require.Equal(t, protocol.SemanticTokenProperty, tokens[1].Type)
	require.Equal(t, "title", string(
		document.Text[tokens[1].Range.Start:tokens[1].Range.End],
	))
	require.Equal(t, protocol.SemanticTokenClass, tokens[2].Type)
	require.Equal(t, "mt-card-original", string(
		document.Text[tokens[2].Range.Start:tokens[2].Range.End],
	))
}

func TestAdminMarkupSemanticTokensUseUnsavedLocalVueComponent(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	componentDir := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/component/sw-live-owner",
	)
	ownerPath := filepath.Join(componentDir, "index.vue")
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		ownerPath,
		[]byte(`<template><div /></template><script setup lang="ts"></script>`),
	)))
	require.NoError(t, adminIndex.Index(indexer.NewParsedFile(
		filepath.Join(componentDir, "DraftPanel.vue"),
		[]byte(`<template><slot /></template>
<script setup lang="ts">
defineProps<{ label: string }>();
defineSlots<{ default(props: { item: string }): unknown }>();
</script>`),
	)))
	source := `<template><DraftPanel :label="title"><template #default="props">{{ props.item }}</template></DraftPanel></template>
<script setup lang="ts">
import DraftPanel from './DraftPanel.vue';
const title = 'Draft';
</script>`
	document := lsp.NewTextDocument(uriutil.FileURI(ownerPath), source, 2)
	tokens, err := NewAdminMarkupProvider(adminIndex).GetSemanticTokens(
		context.Background(), &lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)
	seen := make(map[string]uint32)
	for _, token := range tokens {
		seen[string(document.Text[token.Range.Start:token.Range.End])] = token.Type
	}
	require.Equal(t, protocol.SemanticTokenClass, seen["DraftPanel"])
	require.Equal(t, protocol.SemanticTokenProperty, seen[":label"])
	require.Equal(t, protocol.SemanticTokenProperty, seen["#default"])
	require.Equal(t, protocol.SemanticTokenProperty, seen["item"])
}

func TestAdminMarkupSemanticTokensResolveDynamicComponentContract(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	templatePath := filepath.Join(adminRoot, "consumer.html.twig")
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: filepath.Join(adminRoot, "sw-card/index.ts"),
		Props: []admin.VueComponentProp{{Name: "title", Type: "String"}},
	}))
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name: "sw-host", FilePath: filepath.Join(adminRoot, "sw-host/index.ts"),
		TemplatePath: templatePath,
		Members: []admin.VueComponentMember{
			{
				Name: "dynamicCard", Kind: admin.ComponentMemberComputed,
				ReturnExpressions: []string{"'sw-card'"}, ReturnsComplete: true,
			},
			{Name: "heading", Kind: admin.ComponentMemberData, Type: "string"},
		},
	}))
	source := `<component :is="'sw-card'" :title="title" />`
	document := lsp.NewTextDocument(
		uriutil.FileURI(templatePath), source, 1,
	)
	tokens, err := NewAdminMarkupProvider(adminIndex).GetSemanticTokens(
		context.Background(), &lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)
	require.Len(t, tokens, 2)
	require.Equal(t, protocol.SemanticTokenClass, tokens[0].Type)
	require.Equal(t, "sw-card", string(
		document.Text[tokens[0].Range.Start:tokens[0].Range.End],
	))
	require.Equal(t, protocol.SemanticTokenProperty, tokens[1].Type)
	require.Equal(t, ":title", string(
		document.Text[tokens[1].Range.Start:tokens[1].Range.End],
	))

	inferredSource := `<component :is="dynamicCard" v-bind="{ title: heading }" />`
	document = lsp.NewTextDocument(uriutil.FileURI(templatePath), inferredSource, 1)
	tokens, err = NewAdminMarkupProvider(adminIndex).GetSemanticTokens(
		context.Background(), &lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)
	require.Len(t, tokens, 3)
	require.Equal(t, protocol.SemanticTokenVariable, tokens[0].Type)
	require.Equal(t, "dynamicCard", string(
		document.Text[tokens[0].Range.Start:tokens[0].Range.End],
	))
	require.Equal(t, protocol.SemanticTokenProperty, tokens[1].Type)
	require.Equal(t, "title", string(
		document.Text[tokens[1].Range.Start:tokens[1].Range.End],
	))
	require.Equal(t, protocol.SemanticTokenVariable, tokens[2].Type)
	require.Equal(t, "heading", string(
		document.Text[tokens[2].Range.Start:tokens[2].Range.End],
	))
}

func TestAdminMarkupSemanticTokensUseFiniteDynamicContractIntersection(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-card", FilePath: filepath.Join(adminRoot, "sw-card/index.ts"),
			Props: []admin.VueComponentProp{
				{Name: "title", Type: "String"},
				{Name: "cardOnly", Type: "String"},
			},
		},
		{
			Name: "sw-panel", FilePath: filepath.Join(adminRoot, "sw-panel/index.ts"),
			Props: []admin.VueComponentProp{{Name: "title", Type: "String"}},
		},
	} {
		require.NoError(t, adminIndex.SaveComponent(component))
	}
	source := `<component :is="active ? 'sw-card' : 'sw-panel'" :title="title" :card-only="value" />`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")), source, 1,
	)
	tokens, err := NewAdminMarkupProvider(adminIndex).GetSemanticTokens(
		context.Background(), &lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)
	require.Len(t, tokens, 3)
	require.Equal(t, []string{"sw-card", "sw-panel", ":title"}, []string{
		string(document.Text[tokens[0].Range.Start:tokens[0].Range.End]),
		string(document.Text[tokens[1].Range.Start:tokens[1].Range.End]),
		string(document.Text[tokens[2].Range.Start:tokens[2].Range.End]),
	})
	require.Equal(t, protocol.SemanticTokenProperty, tokens[2].Type)
}

func TestAdminMarkupSemanticTokensUseDynamicOwnerSlotIntersection(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
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
			Slots: []admin.VueComponentSlot{{Name: "header"}},
		},
	} {
		require.NoError(t, adminIndex.SaveComponent(component))
	}
	source := `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #header /><template #a-only /></component>`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")), source, 1,
	)
	tokens, err := NewAdminMarkupProvider(adminIndex).GetSemanticTokens(
		context.Background(), &lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)
	var texts []string
	for _, token := range tokens {
		texts = append(texts, string(
			document.Text[token.Range.Start:token.Range.End],
		))
	}
	require.Contains(t, texts, "#header")
	require.NotContains(t, texts, "#a-only")
}

func TestAdminMarkupSemanticTokensColorDynamicScopedSlotPayload(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	templatePath := filepath.Join(adminRoot, "consumer.html.twig")
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-card-a", FilePath: filepath.Join(adminRoot, "a/index.ts"),
			Slots: []admin.VueComponentSlot{{
				Name: "row", Members: []admin.VueComponentSlotMember{{Name: "item"}},
			}},
		},
		{
			Name: "sw-card-b", FilePath: filepath.Join(adminRoot, "b/index.ts"),
			Slots: []admin.VueComponentSlot{{
				Name: "row", Members: []admin.VueComponentSlotMember{{Name: "item"}},
			}},
		},
		{
			Name: "sw-host", FilePath: filepath.Join(adminRoot, "host/index.ts"),
			TemplatePath: templatePath,
		},
	} {
		require.NoError(t, adminIndex.SaveComponent(component))
	}
	source := `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="props">{{ props.item }}</template></component>`
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	tokens, err := NewAdminMarkupProvider(adminIndex).GetSemanticTokens(
		context.Background(), &lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)
	typesByText := make(map[string][]uint32)
	for _, token := range tokens {
		text := string(document.Text[token.Range.Start:token.Range.End])
		typesByText[text] = append(typesByText[text], token.Type)
	}
	require.Contains(t, typesByText["props"], uint32(protocol.SemanticTokenVariable))
	require.Contains(t, typesByText["item"], uint32(protocol.SemanticTokenProperty))
}

func TestAdminMarkupSemanticTokensIgnoreStorefrontTwig(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "templates/card.html.twig")),
		`<sw-card />`,
		1,
	)
	tokens, err := NewAdminMarkupProvider(adminIndex).GetSemanticTokens(
		context.Background(),
		&lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)
	require.Empty(t, tokens)
}

func TestAdminMarkupSemanticTokensColorVueLexicalBindings(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	source := `<div v-for="(item, index) in items"><button @click="save(item, $event)">{{ item.name }} {{ item.children[0].name }} {{ index }}</button></div>`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root, "Resources/app/administration/view.html.twig",
		)),
		source,
		1,
	)
	tokens, err := NewAdminMarkupProvider(adminIndex).GetSemanticTokens(
		context.Background(),
		&lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)
	require.Len(t, tokens, 10)
	expected := []struct {
		text      string
		tokenType uint32
	}{
		{"item", protocol.SemanticTokenVariable},
		{"index", protocol.SemanticTokenVariable},
		{"item", protocol.SemanticTokenVariable},
		{"$event", protocol.SemanticTokenVariable},
		{"item", protocol.SemanticTokenVariable},
		{"name", protocol.SemanticTokenProperty},
		{"item", protocol.SemanticTokenVariable},
		{"children", protocol.SemanticTokenProperty},
		{"name", protocol.SemanticTokenProperty},
		{"index", protocol.SemanticTokenVariable},
	}
	for index, token := range tokens {
		require.Equal(t, expected[index].tokenType, token.Type)
		require.Equal(
			t, expected[index].text,
			string(document.Text[token.Range.Start:token.Range.End]),
		)
	}
}

func TestAdminMarkupSemanticTokensColorComponentSetupMembers(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	templatePath := filepath.Join(
		root,
		"Resources/app/administration/src/component/sw-setup/view.html.twig",
	)
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name: "sw-setup", FilePath: filepath.Join(filepath.Dir(templatePath), "index.ts"),
		TemplatePath: templatePath,
		Members: []admin.VueComponentMember{
			{Name: "count", Kind: admin.ComponentMemberData, Type: "number"},
			{Name: "detail", Kind: admin.ComponentMemberData, Type: "{ name: string }"},
			{Name: "save", Kind: admin.ComponentMemberMethod, Type: "(count: number) => void"},
		},
	}))
	source := `<button @click="save(count)">{{ count }} {{ detail.name }}</button>`
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	tokens, err := NewAdminMarkupProvider(adminIndex).GetSemanticTokens(
		context.Background(), &lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)

	expected := []struct {
		text      string
		tokenType uint32
	}{
		{"save", protocol.SemanticTokenFunction},
		{"count", protocol.SemanticTokenVariable},
		{"count", protocol.SemanticTokenVariable},
		{"detail", protocol.SemanticTokenVariable},
		{"name", protocol.SemanticTokenProperty},
	}
	require.Len(t, tokens, len(expected))
	for index, token := range tokens {
		require.Equal(t, expected[index].tokenType, token.Type)
		require.Equal(
			t, expected[index].text,
			string(document.Text[token.Range.Start:token.Range.End]),
		)
	}
}

func TestAdminMarkupSemanticTokensColorWholeSlotObjectMembers(t *testing.T) {
	root := t.TempDir()
	adminIndex, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminIndex.Close()) })
	require.NoError(t, adminIndex.SaveComponent(admin.VueComponent{
		Name: "sw-inherit-wrapper", FilePath: filepath.Join(root, "index.js"),
		Slots: []admin.VueComponentSlot{{
			Name: "content",
			Members: []admin.VueComponentSlotMember{{
				Name: "currentValue", Type: "string",
			}},
		}},
	}))
	source := `<sw-inherit-wrapper><template #content="props">{{ props.currentValue }}</template></sw-inherit-wrapper>`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(
			root, "Resources/app/administration/view.html.twig",
		)),
		source,
		1,
	)
	tokens, err := NewAdminMarkupProvider(adminIndex).GetSemanticTokens(
		context.Background(),
		&lsp.SemanticTokensRequest{Document: document},
	)
	require.NoError(t, err)
	found := false
	for _, token := range tokens {
		text := string(document.Text[token.Range.Start:token.Range.End])
		if text == "currentValue" {
			found = true
			require.Equal(t, protocol.SemanticTokenProperty, token.Type)
		}
	}
	require.True(t, found)
}
