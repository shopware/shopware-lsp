package admin

import (
	"path/filepath"
	"strings"
	"testing"

	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	vueparser "github.com/shopware/shopware-lsp/internal/parser/vue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseScriptSetupDefinition(t *testing.T) {
	source := `<template><LocalPanel :title="title" @save="save" /></template>
<script setup lang="ts">
import LocalPanel from './LocalPanel.vue';

interface Props {
    /** Card heading shown above the content. */
    title: string;
    count?: number;
}

const props = withDefaults(defineProps<Props>(), { count: 1 });
const emit = defineEmits<{
    /** Persists the selected record. */
    save: [id: string];
    /** Closes the panel. */
    (event: 'close'): void;
}>();
const value = defineModel<string>();
const doubled = computed(() => props.count * 2);
function save() { emit('save', '1'); }
</script>`
	parsed := vueparser.Parse(source)
	require.NotNil(t, parsed.Tree)
	require.Len(t, parsed.Sections, 2)
	definition := parseScriptSetupDefinition(
		parsed.Tree.Root,
		source,
		"/project/Resources/app/administration/src/LocalPage.vue",
		cst.NewLineIndex(source),
		parsed.Sections[1].BodyRange,
	)
	require.NotNil(t, definition)
	assert.Equal(t, []string{"Props"}, definition.ScriptSetupPropTypes)
	assert.Len(t, definition.ScriptSetupPropDefaults, 1)
	assert.Equal(t, "count", definition.ScriptSetupPropDefaults[0].Name)
	assert.Equal(t, "1", definition.ScriptSetupPropDefaults[0].Value)
	assert.Len(t, definition.ScriptSetupEventTypes, 1)

	props := make(map[string]VueComponentProp)
	for _, prop := range definition.Props {
		props[prop.Name] = prop
	}
	require.Contains(t, props, "title")
	assert.True(t, props["title"].Required)
	assert.Equal(t, "string", props["title"].Type)
	assert.Equal(
		t, "Card heading shown above the content.",
		props["title"].Documentation,
	)
	require.Contains(t, props, "count")
	assert.False(t, props["count"].Required)
	assert.Equal(t, "1", props["count"].Default)
	require.Contains(t, props, "modelValue")
	assert.Equal(t, "string", props["modelValue"].Type)

	assert.Contains(t, definition.Emits, "save")
	assert.Contains(t, definition.Emits, "close")
	saveEvent, found := componentDefinitionEvent(definition.Events, "save")
	require.True(t, found)
	assert.Equal(t, "Persists the selected record.", saveEvent.Documentation)
	closeEvent, found := componentDefinitionEvent(definition.Events, "close")
	require.True(t, found)
	assert.Equal(t, "Closes the panel.", closeEvent.Documentation)
	assert.Equal(t, "(event: 'close')", closeEvent.Type)
	assert.Contains(t, definition.Emits, "update:model-value")
	assert.Equal(t, "modelValue", definition.ModelProp)
	assert.Equal(t, "update:model-value", definition.ModelEvent)

	require.Len(t, definition.LocalComponents, 1)
	assert.Equal(t, "local-panel", definition.LocalComponents[0].Name)
	assert.Equal(t, "./LocalPanel.vue", definition.LocalComponents[0].ImportPath)
	for _, name := range []string{
		"title", "count", "modelValue", "props", "emit", "value",
		"doubled", "save", "LocalPanel",
	} {
		_, found := componentDefinitionMember(definition, name)
		assert.Truef(t, found, "expected member %s", name)
	}
}

func componentDefinitionMember(
	definition *ComponentDefinition,
	name string,
) (VueComponentMember, bool) {
	if definition == nil {
		return VueComponentMember{}, false
	}
	for _, member := range definition.Members {
		if member.Name == name {
			return member, true
		}
	}
	return VueComponentMember{}, false
}

func TestParseScriptSetupRuntimeContracts(t *testing.T) {
	source := `<template><button>{{ label }}</button></template>
<script setup>
const props = defineProps({
    label: { type: String, required: true },
});
const emit = defineEmits(['submit']);
const checked = defineModel('checked', { type: Boolean, default: false });
</script>`
	parsed := vueparser.Parse(source)
	require.Len(t, parsed.Sections, 2)
	definition := parseScriptSetupDefinition(
		parsed.Tree.Root, source,
		"/project/Resources/app/administration/src/Toggle.vue",
		cst.NewLineIndex(source), parsed.Sections[1].BodyRange,
	)
	require.NotNil(t, definition)
	props := make(map[string]VueComponentProp)
	for _, prop := range definition.Props {
		props[prop.Name] = prop
	}
	assert.True(t, props["label"].Required)
	assert.Equal(t, "String", props["label"].Type)
	assert.Equal(t, "Boolean", props["checked"].Type)
	assert.Equal(t, "false", props["checked"].Default)
	assert.Contains(t, definition.Emits, "submit")
	assert.Contains(t, definition.Emits, "update:checked")
}

func TestAdminIndexerIndexesVueSFCContractAndMarkup(t *testing.T) {
	root := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	componentPath := filepath.Join(adminRoot, "component/sw-sfc-card/index.vue")
	registrationPath := filepath.Join(adminRoot, "component/sw-sfc-card/index.ts")
	source := `<template>
    <sw-button>{{ title }}</sw-button>
    <slot name="actions" :item="title" />
</template>
<script setup lang="ts">
interface Props { title: string; count?: number }
const props = defineProps<Props>();
const submit = () => props.title;
</script>`
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		componentPath, []byte(source),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`Shopware.Component.register('sw-sfc-card', () => import('./index.vue'));`),
	)))

	definition, err := index.GetComponentDefinition(componentPath)
	require.NoError(t, err)
	require.NotNil(t, definition)
	assert.Equal(t, componentPath, definition.TemplatePath)
	assert.True(t, definition.HasTemplate)
	require.Len(t, definition.Slots, 1)
	assert.Equal(t, "actions", definition.Slots[0].Name)
	assert.Equal(t, componentPath, definition.Slots[0].FilePath)
	assert.True(t, definition.Slots[0].NameRange.Declaration)
	assert.False(t, definition.Slots[0].NameRange.Identifier)
	slotRange := definition.Slots[0].NameRange
	slotLine := strings.Split(source, "\n")[slotRange.StartLine]
	assert.Equal(
		t, "actions",
		slotLine[slotRange.StartCharacter:slotRange.EndCharacter],
	)

	component, err := index.GetEffectiveComponent("sw-sfc-card")
	require.NoError(t, err)
	require.NotNil(t, component)
	assert.Equal(t, componentPath, component.DefinitionPath)
	prop, found := component.ComponentProp("title")
	require.True(t, found)
	assert.True(t, prop.Required)
	assert.Equal(t, "string", prop.Type)
	_, found = component.TemplateMember("submit")
	assert.True(t, found)

	usages, err := index.GetUsages(AdminSymbolComponent, "", "sw-button")
	require.NoError(t, err)
	require.Len(t, usages, 1)
	assert.Equal(t, componentPath, usages[0].FilePath)

	typeFile, found, err := index.adminTypeFile(componentPath)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, typeFile.Declarations, 1)
	assert.Equal(t, "Props", typeFile.Declarations[0].Name)
}

func TestAdminIndexerIndexesVueSFCOptionsAPI(t *testing.T) {
	root := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	componentPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/component/sw-options/index.vue",
	)
	source := `<template><slot name="footer" /></template>
<script lang="ts">
export default defineComponent({
    props: { title: { type: String, required: true } },
    emits: ['save'],
    methods: { submit() { this.$emit('save'); } },
});
</script>`
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		componentPath, []byte(source),
	)))
	definition, err := index.GetComponentDefinition(componentPath)
	require.NoError(t, err)
	require.NotNil(t, definition)
	require.Len(t, definition.Props, 1)
	assert.Equal(t, "title", definition.Props[0].Name)
	assert.True(t, definition.Props[0].Required)
	assert.Contains(t, definition.Emits, "save")
	assert.Contains(t, definition.Methods, "submit")
	require.Len(t, definition.Slots, 1)
	assert.Equal(t, "footer", definition.Slots[0].Name)
}

func TestAdminIndexerResolvesImportedScriptSetupContractsLazily(t *testing.T) {
	root := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	componentPath := filepath.Join(adminRoot, "component/sw-imported-card/index.vue")
	registrationPath := filepath.Join(adminRoot, "component/sw-imported-card/index.ts")
	typesPath := filepath.Join(adminRoot, "component/sw-imported-card/contracts.ts")
	liveTypesPath := filepath.Join(
		adminRoot, "component/sw-imported-card/draft-contracts.ts",
	)
	componentSource := `<template>
    <p>interface Bogus { ignored: string }</p>
	<slot :title="heading" />
	<slot name="header" />
</template>
<script setup lang="ts">
import type { CardProps, CardEvents, CardSlots } from './contracts';
const { title: heading, count = 1, mode: displayMode } = defineProps<CardProps>();
const emit = defineEmits<CardEvents>();
defineSlots<CardSlots>();
</script>`

	// Deliberately index the dependent SFC first. Contract correctness must not
	// depend on the scanner's parallel file ordering.
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		componentPath, []byte(componentSource),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`Shopware.Component.register('sw-imported-card', () => import('./index.vue'));`),
	)))
	indexTypes := func(extra string) {
		t.Helper()
		source := `export interface BaseProps {
    /** Heading displayed above the imported card. */
    title: string;
    count?: number;
` + extra + `}
export interface VariantProps { mode: 'small' | 'large'; ignored: string }
export interface Flags { enabled: boolean; internal: string; requiredFlag?: boolean }
export type CardProps = Partial<BaseProps> & Pick<VariantProps, 'mode'> & Omit<Flags, 'internal' | 'requiredFlag'> & Required<Pick<Flags, 'requiredFlag'>>;
export interface CardEvents {
    /** Persists the selected record. */
    save: [id: string];
    /** Closes the card with an optional reason. */
    (event: 'close', reason: string): void;
}

export interface HeaderPayload { item: string; selected?: boolean }
export interface CardSlots {
    default(props: { title: string }): unknown;
    header(props: HeaderPayload): unknown;
    footer(): unknown;
}
`
		require.NoError(t, index.Index(indexerpkg.NewParsedFile(
			typesPath, []byte(source),
		)))
	}
	indexTypes("")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		liveTypesPath, []byte(`export interface DraftProfile { city: string; zip: string }
export interface DraftRow { label: string; active: boolean }
export interface LiveProps { profile: DraftProfile; rows: DraftRow[] }
`),
	)))

	definition, err := index.GetComponentDefinition(componentPath)
	require.NoError(t, err)
	require.NotNil(t, definition)
	require.Len(t, definition.ScriptSetupPropBindings, 3)
	props := make(map[string]VueComponentProp)
	for _, prop := range definition.Props {
		props[prop.Name] = prop
	}
	require.Contains(t, props, "title")
	assert.False(t, props["title"].Required)
	assert.Equal(t, "string", props["title"].Type)
	assert.Equal(
		t, "Heading displayed above the imported card.",
		props["title"].Documentation,
	)
	assert.Equal(t, typesPath, props["title"].FilePath)
	assert.NotEqual(t, AdminSourceRange{}, props["title"].NameRange)
	assert.True(t, props["title"].NameRange.Declaration)
	require.Contains(t, props, "count")
	assert.False(t, props["count"].Required)
	assert.Equal(t, "1", props["count"].Default)
	require.Contains(t, props, "mode")
	assert.True(t, props["mode"].Required)
	assert.Equal(t, "'small' | 'large'", props["mode"].Type)
	require.Contains(t, props, "enabled")
	assert.True(t, props["enabled"].Required)
	require.Contains(t, props, "requiredFlag")
	assert.True(t, props["requiredFlag"].Required)
	assert.NotContains(t, props, "ignored")
	assert.NotContains(t, props, "internal")
	assert.Contains(t, definition.Emits, "save")
	assert.Contains(t, definition.Emits, "close")
	resolvedEvents := make(map[string]VueComponentEvent)
	for _, event := range definition.Events {
		resolvedEvents[CanonicalEventName(event.Name)] = event
	}
	for _, eventName := range []string{"save", "close"} {
		event, eventFound := resolvedEvents[eventName]
		require.True(t, eventFound, eventName)
		assert.Equal(t, typesPath, event.FilePath, eventName)
		assert.True(t, event.NameRange.Declaration, eventName)
		assert.Greater(
			t, event.NameRange.EndCharacter,
			event.NameRange.StartCharacter,
			eventName,
		)
	}
	assert.Equal(
		t, "Persists the selected record.",
		resolvedEvents["save"].Documentation,
	)
	assert.Equal(
		t, "Closes the card with an optional reason.",
		resolvedEvents["close"].Documentation,
	)
	_, found := componentDefinitionMember(definition, "title")
	assert.True(t, found)
	heading, found := componentDefinitionMember(definition, "heading")
	require.True(t, found)
	assert.Equal(t, "string", heading.Type)
	assert.Equal(t, componentPath, heading.FilePath)
	assert.NotEqual(t, AdminSourceRange{}, heading.NameRange)
	displayMode, found := componentDefinitionMember(definition, "displayMode")
	require.True(t, found)
	assert.Equal(t, "'small' | 'large'", displayMode.Type)

	liveSource := strings.ReplaceAll(componentSource, "heading", "headline")
	liveSource = strings.Replace(liveSource, "count = 1", "count = 2", 1)
	liveParsed := vueparser.Parse(liveSource)
	liveComponent, err := index.GetComponentForDocument(
		componentPath, liveParsed.Tree.Root, liveSource,
		cst.NewLineIndex(liveSource),
	)
	require.NoError(t, err)
	require.NotNil(t, liveComponent)
	_, found = liveComponent.TemplateMember("headline")
	assert.True(t, found)
	_, found = liveComponent.TemplateMember("heading")
	assert.False(t, found)
	liveCount, found := liveComponent.ComponentProp("count")
	require.True(t, found)
	assert.Equal(t, "2", liveCount.Default)
	persistedComponent, err := index.GetEffectiveComponent("sw-imported-card")
	require.NoError(t, err)
	require.NotNil(t, persistedComponent)
	_, found = persistedComponent.TemplateMember("heading")
	assert.True(t, found, "request-local overlay must not mutate the index")
	_, found = persistedComponent.TemplateMember("headline")
	assert.False(t, found)

	// A newly added import and a local declaration must both participate in
	// request-local type resolution before the Vue document is reindexed.
	liveImportedSource := `<template>
    <p>{{ profile.city }}</p>
    <p>{{ localState.flag }}</p>
    <p v-for="row in rows">{{ row.label }}</p>
</template>
<script setup lang="ts">
import type { LiveProps } from './draft-contracts';
interface LocalDraft { localState: { flag: boolean; note: string } }
const { profile, rows } = defineProps<LiveProps>();
const { localState } = defineProps<LocalDraft>();
</script>`
	liveImportedParsed := vueparser.Parse(liveImportedSource)
	liveImported, err := index.GetComponentForDocument(
		componentPath, liveImportedParsed.Tree.Root, liveImportedSource,
		cst.NewLineIndex(liveImportedSource),
	)
	require.NoError(t, err)
	require.NotNil(t, liveImported)
	profile, found := liveImported.TemplateMember("profile")
	require.True(t, found)
	assert.Equal(t, "DraftProfile", profile.Type)
	assert.Equal(t, liveTypesPath, profile.TypeContextPath)
	_, found = liveImported.TemplateMember("heading")
	assert.False(t, found)

	cityOffset := uint32(strings.Index(liveImportedSource, "profile.city") + len("profile."))
	city, err := index.ResolveTwigVueInstanceMemberForComponent(
		liveImportedParsed.Tree.Root, []byte(liveImportedSource), cityOffset,
		componentPath, liveImported,
	)
	require.NoError(t, err)
	require.NotNil(t, city)
	require.True(t, city.MemberFound)
	assert.Equal(t, "string", city.Member.Type)
	assert.Equal(t, liveTypesPath, city.Member.DefinitionPath)

	flagOffset := uint32(strings.Index(liveImportedSource, "localState.flag") + len("localState."))
	flag, err := index.ResolveTwigVueInstanceMemberForComponent(
		liveImportedParsed.Tree.Root, []byte(liveImportedSource), flagOffset,
		componentPath, liveImported,
	)
	require.NoError(t, err)
	require.NotNil(t, flag)
	require.True(t, flag.MemberFound)
	assert.Equal(t, "boolean", flag.Member.Type)
	assert.Equal(t, componentPath, flag.Member.DefinitionPath)

	rowOffset := uint32(strings.Index(liveImportedSource, "row.label") + len("row."))
	row, err := index.ResolveTwigVueMemberForComponent(
		liveImportedParsed.Tree.Root, []byte(liveImportedSource), rowOffset,
		componentPath, liveImported,
	)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.True(t, row.MemberFound)
	assert.Equal(t, "string", row.Member.Type)
	assert.Equal(t, liveTypesPath, row.Member.DefinitionPath)

	typeFile, found, err := index.adminTypeFile(componentPath)
	require.NoError(t, err)
	require.True(t, found)
	assert.Empty(t, typeFile.Declarations, "template text must not become TypeScript declarations")
	require.Len(t, typeFile.Imports, 3)

	// Reindex only the imported type owner. Lazy materialization must expose
	// the new prop without touching the Vue component again.
	indexTypes("    subtitle?: boolean;\n")
	component, err := index.GetEffectiveComponent("sw-imported-card")
	require.NoError(t, err)
	require.NotNil(t, component)
	subtitle, found := component.ComponentProp("subtitle")
	require.True(t, found)
	assert.False(t, subtitle.Required)
	assert.Equal(t, "boolean", subtitle.Type)
	assert.Equal(t, typesPath, subtitle.FilePath)
	assert.NotEqual(t, AdminSourceRange{}, subtitle.NameRange)
	event, found := component.ComponentEvent("save")
	require.True(t, found)
	assert.Equal(t, "[id: string]", event.Type)
	assert.Equal(t, "Persists the selected record.", event.Documentation)
	assert.Equal(t, typesPath, event.FilePath)
	closeEvent, found := component.ComponentEvent("close")
	require.True(t, found)
	assert.Contains(t, closeEvent.Type, "reason: string")
	assert.Equal(
		t, "Closes the card with an optional reason.",
		closeEvent.Documentation,
	)
	assert.Equal(t, typesPath, closeEvent.FilePath)
	slots := make(map[string]VueComponentSlot)
	for _, slot := range component.Slots {
		slots[slot.Name] = slot
	}
	for _, name := range []string{"default", "header", "footer"} {
		require.Contains(t, slots, name)
	}
	assert.True(t, slots["default"].MembersComplete)
	defaultTitle, found := slots["default"].Member("title")
	require.True(t, found)
	assert.Equal(t, "string", defaultTitle.Type)
	assert.True(t, slots["header"].MembersComplete)
	assert.Equal(t, componentPath, slots["header"].FilePath)
	assert.True(t, slots["header"].NameRange.Declaration)
	assert.False(t, slots["header"].NameRange.Identifier)
	headerItem, found := slots["header"].Member("item")
	require.True(t, found)
	assert.Equal(t, "string", headerItem.Type)
	assert.Equal(t, typesPath, headerItem.FilePath)
	assert.True(t, headerItem.NameRange.Declaration)
	assert.True(t, headerItem.NameRange.Identifier)
	assert.Greater(
		t, headerItem.NameRange.EndCharacter,
		headerItem.NameRange.StartCharacter,
	)
	assert.Equal(t, typesPath, slots["footer"].FilePath)
	assert.True(t, slots["footer"].NameRange.Declaration)
	assert.True(t, slots["footer"].NameRange.Identifier)
	assert.Greater(
		t, slots["footer"].NameRange.EndCharacter,
		slots["footer"].NameRange.StartCharacter,
	)
}

func TestAdminIndexerResolvesUnsavedLocalVueComponentContract(t *testing.T) {
	root := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	componentDir := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/component/sw-live-owner",
	)
	ownerPath := filepath.Join(componentDir, "index.vue")
	childPath := filepath.Join(componentDir, "DraftCard.vue")
	childSource := `<template><slot :item="label" /></template>
<script setup lang="ts">
defineProps<{ label: string }>();
defineSlots<{ default(props: { item: string; count?: number }): unknown }>();
</script>`
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		childPath, []byte(childSource),
	)))
	persistedOwner := `<template><div /></template><script setup lang="ts"></script>`
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		ownerPath, []byte(persistedOwner),
	)))

	liveSource := `<template>
    <DraftCard :label="title">
        <template #default="slotProps">{{ slotProps.item }}</template>
    </DraftCard>
    <component :is="'draft-card'" :label="title" />
</template>
<script setup lang="ts">
import DraftCard from './DraftCard.vue';
const title = 'Draft';
</script>`
	parsed := vueparser.Parse(liveSource)
	liveOwner, err := index.GetComponentForDocument(
		ownerPath, parsed.Tree.Root, liveSource, cst.NewLineIndex(liveSource),
	)
	require.NoError(t, err)
	require.NotNil(t, liveOwner)
	_, found := liveOwner.LocalComponent("draft-card")
	require.True(t, found)

	child, found, err := index.GetComponentForTemplateTag(
		ownerPath, "draft-card", liveOwner,
	)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, child)
	label, found := child.ComponentProp("label")
	require.True(t, found)
	assert.True(t, label.Required)
	assert.Equal(t, childPath, label.FilePath)
	childSlot, found := child.ComponentSlot("default")
	require.True(t, found)
	_, found = childSlot.Member("item")
	assert.True(t, found)

	_, persistedFound, err := index.GetComponentForTemplateTag(
		ownerPath, "draft-card",
	)
	require.NoError(t, err)
	assert.False(t, persistedFound, "the open owner must not leak into the index")

	itemOffset := uint32(strings.Index(liveSource, "slotProps.item") + len("slotProps."))
	scopes := TwigScopedSlotsAtOffset(parsed.Tree.Root, itemOffset)
	require.NotEmpty(t, scopes)
	slotStartTag := TwigScopedSlotStartingTag(
		parsed.Tree.Root, scopes[len(scopes)-1],
	)
	require.NotNil(t, slotStartTag)
	consumers, consumerComplete, err := index.ResolveTwigSlotConsumerComponents(
		ownerPath, slotStartTag, liveOwner,
	)
	require.NoError(t, err)
	require.True(t, consumerComplete)
	require.Len(t, consumers, 1)
	resolvedItem, err := index.ResolveTwigScopedSlotMemberForOwner(
		parsed.Tree.Root, parsed.Tree.Root.NodeAtOffset(itemOffset),
		[]byte(liveSource), itemOffset, ownerPath, liveOwner,
	)
	require.NoError(t, err)
	require.NotNil(t, resolvedItem)
	require.True(t, resolvedItem.MemberFound)
	assert.Equal(t, "string", resolvedItem.Member.Type)
	assert.Equal(t, childPath, resolvedItem.Member.FilePath)

	dynamicTag := twigquery.StartingHTMLTagAt(
		parsed.Tree.Root.NodeAtOffset(uint32(strings.Index(liveSource, ":is="))),
	)
	require.NotNil(t, dynamicTag)
	selector, found := TwigDynamicComponentSelector(dynamicTag)
	require.True(t, found)
	_, dynamicComponents, complete, err :=
		index.ResolveDynamicComponentContractsForOwner(
			ownerPath, selector, liveOwner, dynamicTag,
		)
	require.NoError(t, err)
	require.True(t, complete)
	require.Len(t, dynamicComponents, 1)
	assert.Equal(t, "draft-card", dynamicComponents[0].Name)
	_, found = dynamicComponents[0].ComponentProp("label")
	assert.True(t, found)
}

func TestAdminIndexerLiveDocumentsComposeAcrossVueAndTypeScript(t *testing.T) {
	root := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	componentDir := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/component/live-stack",
	)
	ownerPath := filepath.Join(componentDir, "Owner.vue")
	childPath := filepath.Join(componentDir, "DraftCard.vue")
	typesPath := filepath.Join(componentDir, "draft-contract.ts")

	persistedTypes := `export interface DraftProps { persistedLabel: string }`
	persistedChild := `<template><slot /></template>
<script setup lang="ts">
import type { DraftProps } from './draft-contract';
defineProps<DraftProps>();
</script>`
	for path, source := range map[string]string{
		typesPath: persistedTypes,
		childPath: persistedChild,
		ownerPath: `<template><div /></template>`,
	} {
		require.NoError(t, index.Index(indexerpkg.NewParsedFile(
			path, []byte(source),
		)))
	}

	ownerSource := `<template><DraftCard /></template>
<script setup lang="ts">import DraftCard from './DraftCard.vue';</script>`
	ownerDocument := vueparser.Parse(ownerSource)
	owner, err := index.GetComponentForDocument(
		ownerPath, ownerDocument.Tree.Root, ownerSource,
		cst.NewLineIndex(ownerSource),
	)
	require.NoError(t, err)
	require.NotNil(t, owner)

	resolveChild := func() VueComponent {
		t.Helper()
		child, found, resolveErr := index.GetComponentForTemplateTag(
			ownerPath, "DraftCard", owner,
		)
		require.NoError(t, resolveErr)
		require.True(t, found)
		require.NotNil(t, child)
		return *child
	}
	persisted := resolveChild()
	_, found := persisted.ComponentProp("persistedLabel")
	require.True(t, found)

	// Opening the child and its imported contract creates one composed live
	// generation even though the parent request owns neither document.
	childDocument := vueparser.Parse(persistedChild)
	index.UpdateLiveDocument(
		childPath, childDocument.Tree.Root, persistedChild,
		cst.NewLineIndex(persistedChild),
	)
	liveTypes := `export interface DraftProps { liveLabel: string; count?: number }`
	index.UpdateLiveDocument(
		typesPath, nil, liveTypes, cst.NewLineIndex(liveTypes),
	)
	live := resolveChild()
	_, found = live.ComponentProp("liveLabel")
	require.True(t, found)
	_, found = live.ComponentProp("count")
	require.True(t, found)
	_, found = live.ComponentProp("persistedLabel")
	require.False(t, found)

	// Type overlays are resolved lazily. Editing only the imported .ts buffer
	// must update the open child contract without touching the child snapshot.
	renamedTypes := `export interface DraftProps { renamedLabel: string }`
	index.UpdateLiveDocument(
		typesPath, nil, renamedTypes, cst.NewLineIndex(renamedTypes),
	)
	renamed := resolveChild()
	_, found = renamed.ComponentProp("renamedLabel")
	require.True(t, found)
	_, found = renamed.ComponentProp("liveLabel")
	require.False(t, found)

	// Editing the child itself replaces rather than accumulates its public API.
	changedChildSource := `<template><slot name="body" :item="childOnly" /></template>
<script setup lang="ts">defineProps<{ childOnly: boolean }>();</script>`
	changedChild := vueparser.Parse(changedChildSource)
	index.UpdateLiveDocument(
		childPath, changedChild.Tree.Root, changedChildSource,
		cst.NewLineIndex(changedChildSource),
	)
	changed := resolveChild()
	childOnly, found := changed.ComponentProp("childOnly")
	require.True(t, found)
	require.Equal(t, "boolean", childOnly.Type)
	_, found = changed.ComponentProp("renamedLabel")
	require.False(t, found)
	_, found = changed.ComponentSlot("body")
	require.True(t, found)

	index.RemoveLiveDocument(childPath)
	index.RemoveLiveDocument(typesPath)
	fallback := resolveChild()
	_, found = fallback.ComponentProp("persistedLabel")
	require.True(t, found)
	_, found = fallback.ComponentProp("childOnly")
	require.False(t, found)
}
