package admin

import (
	"path/filepath"
	"strings"
	"testing"

	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVueTemplateRuntimeCatalog(t *testing.T) {
	builtin, found := VueBuiltinMember("$t")
	require.True(t, found)
	assert.Equal(t, ComponentMemberMethod, builtin.Kind)
	assert.Contains(t, builtin.Type, "key: string")

	object, found := VueTemplateGlobal("Object")
	require.True(t, found)
	assert.Equal(t, ComponentMemberData, object.Kind)
	assert.Equal(t, "ObjectConstructor", object.Type)
	_, found = VueTemplateGlobal("runtimePluginValue")
	assert.False(t, found)
}

func TestEffectiveComponentInfersLegacyJavaScriptReturnChains(t *testing.T) {
	rootDir := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(rootDir, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		rootDir, "src/Administration/Resources/app/administration/src",
	)
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "app/composables/use-context.ts"),
		[]byte(`export interface ContextState {
    app: { environment: null | string };
    api: { languageId: null | string };
}`),
	)))
	schemaPath := filepath.Join(adminRoot, "entity-schema-definition.d.ts")
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		schemaPath,
		[]byte(`
declare namespace EntitySchema {
    interface order {
        id: string;
        transactions: EntityCollection<'order_transaction'>;
    }
    interface order_transaction {
        id: string;
        amount: { total: number; tax: number };
    }
    interface media { id: string; fileName: string; }
    interface promotion { id: string; name: string; }
}
`),
	)))
	storePath := filepath.Join(adminRoot, "app/store/marketing.store.ts")
	storeSource := `
type MarketingState = {
    activeCampaign: Entity<'promotion'> | null;
};
Shopware.Store.register({
    id: 'marketing',
	state: (): MarketingState => ({
		activeCampaign: null,
		loading: { products: false },
    }),
    getters: {
        campaignName(): string { return this.activeCampaign?.name ?? ''; },
    },
    actions: {
        selectCampaign(campaign: Entity<'promotion'>): void {},
    },
});`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		storePath, []byte(storeSource),
	)))
	stores, err := idx.GetStore("marketing")
	require.NoError(t, err)
	require.Len(t, stores, 1)
	assert.Equal(t, "MarketingState", stores[0].StateType)
	activeCampaignStoreMember, found := stores[0].Member("activeCampaign")
	require.True(t, found)
	assert.Equal(
		t, "Entity<'promotion'> | null", activeCampaignStoreMember.Type,
	)

	definitionPath := filepath.Join(adminRoot, "component/sw-legacy/index.js")
	templatePath := filepath.Join(
		adminRoot, "component/sw-legacy/sw-legacy.html.twig",
	)
	source := `
	export default {
	    props: {
	        order: { type: Object as PropType<Entity<'order'>>, required: true },
	    },
    inject: ['repositoryFactory'],
		data() {
		return {
			mediaItems: [],
			nullableMediaItems: null,
			callbackMediaItems: null,
			runtimeError: null,
			selectedMedia: null,
			mediaById: {} as Record<string, Entity<'media'>>,
		};
	},
    computed: {
        transactions() { return this.order.transactions; },
        lastTransaction() { return this.transactions.last(); },
        mediaRepository() { return this.repositoryFactory.create('media'); },
        newMedia() { return this.mediaRepository.create(); },
		filteredMediaItems() { return this.mediaItems.filter((item) => item.fileName); },
		mediaFileNames() { return this.mediaItems.map((item) => item.fileName); },
		mediaCards() {
			return this.mediaItems.map((item) => ({
				id: item.id,
				label: item.fileName,
			}));
		},
		safeMediaCards() {
			return this.nullableMediaItems?.map((item) => {
				return { ...item, label: item.fileName };
			}) ?? [];
		},
		dynamicMediaCards() { return this.mediaItems.map(this.createMediaCard); },
		mediaValues() { return Object.values(this.mediaById); },
		mediaKeys() { return Object.keys(this.mediaById).filter((key) => key); },
		mediaEntries() { return Object.entries(this.mediaById); },
		mediaEntryNames() {
			return Object.entries(this.mediaById).map((entry) => entry[1].fileName);
		},
		rebuiltMediaById() { return Object.fromEntries(Object.entries(this.mediaById)); },
		firstMappedMedia() { return this.mediaById[this.selectedId]; },
		firstLoadedMedia() { return this.mediaItems[0]; },
		staticMediaCards() { return [{ id: 'example', label: 'Example' }]; },
		loading() { return Shopware.Store.get('marketing').loading; },
        activeCampaign() { return Shopware.Store.get('marketing').activeCampaign; },
		languageId() { return Shopware.Context.api.languageId; },
    },
	methods: {
        getLastTransaction() { return this.transactions.last(); },
		getSlots() { return this.$slots; },
		async loadMedia() {
			const mediaItems = await this.mediaRepository.search(this.criteria);
			const selectedMedia = await this.mediaRepository.get(this.selectedId);
			this.mediaItems = mediaItems;
			this.nullableMediaItems = mediaItems;
			this.selectedMedia = selectedMedia;
		},
		loadMediaCallback() {
			this.mediaRepository.search(this.criteria).then((mediaItems) => {
				this.callbackMediaItems = mediaItems;
			});
		},
		setRuntimeError() {
			this.runtimeError = { title: 'Failed', message: 'Try again' };
		},
    },
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, source), syntax.NewLineIndex(source),
	)
	parsedNullable, found := func() (VueComponentMember, bool) {
		for _, member := range definition.Members {
			if member.Name == "nullableMediaItems" {
				return member, true
			}
		}
		return VueComponentMember{}, false
	}()
	require.True(t, found)
	require.Equal(t, "null", parsedNullable.Type)
	require.Equal(t, "null", parsedNullable.SourceExpression)
	setDefinitionFilePath(definition, definitionPath)
	definition.TemplatePath = templatePath
	require.NoError(t, idx.SaveComponentDefinition(
		normalizeDefinitionPath(definitionPath), *definition,
	))
	require.NoError(t, idx.SaveComponent(VueComponent{
		Name: "sw-legacy", FilePath: definitionPath,
		DefinitionPath: definitionPath, TemplatePath: templatePath,
	}))

	component, err := idx.GetEffectiveComponent("sw-legacy")
	require.NoError(t, err)
	require.NotNil(t, component)
	memberType := func(name string) string {
		member, found := component.TemplateMember(name)
		require.True(t, found, name)
		return member.Type
	}
	assert.Equal(
		t, "EntityCollection<'order_transaction'>",
		memberType("transactions"),
	)
	assert.Equal(
		t, "Entity<'order_transaction'> | null",
		memberType("lastTransaction"),
	)
	assert.Equal(
		t, "() => Entity<'order_transaction'> | null",
		memberType("getLastTransaction"),
	)
	assert.Equal(
		t, "() => Record<string, Function | undefined>",
		memberType("getSlots"),
	)
	assert.Equal(t, "Repository<'media'>", memberType("mediaRepository"))
	assert.Equal(t, "Entity<'media'>", memberType("newMedia"))
	assert.Equal(
		t, "Array<Entity<'media'>>", memberType("filteredMediaItems"),
	)
	assert.Equal(t, "Array<string>", memberType("mediaFileNames"))
	assert.Equal(
		t, "Array<{ id: string; label: string }>", memberType("mediaCards"),
	)
	assert.Equal(
		t,
		"Array<Entity<'media'> & { label: string }>",
		memberType("safeMediaCards"),
	)
	assert.Equal(t, "Array<unknown>", memberType("dynamicMediaCards"))
	assert.Equal(t, "Array<Entity<'media'>>", memberType("mediaValues"))
	assert.Equal(t, "Array<string>", memberType("mediaKeys"))
	assert.Equal(
		t, "Array<[string, Entity<'media'>]>", memberType("mediaEntries"),
	)
	assert.Equal(t, "Array<string>", memberType("mediaEntryNames"))
	assert.Equal(
		t, "Record<string, Entity<'media'>>", memberType("rebuiltMediaById"),
	)
	assert.Equal(t, "Entity<'media'>", memberType("firstMappedMedia"))
	assert.Equal(t, "Entity<'media'>", memberType("firstLoadedMedia"))
	assert.Equal(
		t,
		"Array<{ id: string; label: string }>",
		memberType("staticMediaCards"),
	)
	assert.Equal(t, "EntityCollection<'media'>", memberType("mediaItems"))
	assert.Equal(
		t, "EntityCollection<'media'> | null",
		memberType("nullableMediaItems"),
	)
	assert.Equal(
		t, "EntityCollection<'media'> | null",
		memberType("callbackMediaItems"),
	)
	assert.Equal(t, "Entity<'media'> | null", memberType("selectedMedia"))
	runtimeError, found := component.TemplateMember("runtimeError")
	require.True(t, found)
	assert.Contains(t, runtimeError.Type, "title: string")
	assert.True(t, runtimeError.OpenRuntimeShape)
	assert.Equal(
		t, "Entity<'promotion'> | null", memberType("activeCampaign"),
	)
	assert.Equal(t, "null | string", memberType("languageId"))
	loading, found := component.TemplateMember("loading")
	require.True(t, found)
	assert.True(t, loading.OpenRuntimeShape)

	markup := `<div :title="lastTransaction.amount.total"></div>`
	twigRoot := twigparser.Parse(markup).Tree.Root
	offset := uint32(strings.Index(markup, "amount.total") + len("amount.to"))
	resolved, err := idx.ResolveTwigVueInstanceMember(
		twigRoot, []byte(markup), offset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	require.True(t, resolved.MemberFound)
	assert.Equal(t, "number", resolved.Member.Type)
	assert.Equal(t, schemaPath, resolved.Member.DefinitionPath)

	markup = `<div v-for="media in mediaItems" :title="media.fileName"></div>`
	twigRoot = twigparser.Parse(markup).Tree.Root
	offset = uint32(strings.Index(markup, "fileName") + 2)
	localResolved, err := idx.ResolveTwigVueMember(
		twigRoot, []byte(markup), offset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, localResolved)
	require.True(t, localResolved.MemberFound)
	assert.Equal(t, "string", localResolved.Member.Type)
	assert.Equal(t, schemaPath, localResolved.Member.DefinitionPath)

	markup = `<div v-for="card in safeMediaCards" :title="card.fileName">{{ card.label }}</div>`
	twigRoot = twigparser.Parse(markup).Tree.Root
	offset = uint32(strings.Index(markup, "card.label") + len("card.la"))
	projected, err := idx.ResolveTwigVueMember(
		twigRoot, []byte(markup), offset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, projected)
	require.True(t, projected.MemberFound)
	assert.Equal(t, "string", projected.Member.Type)

	markup = `<div v-for="(slot, name, index) in getSlots()">{{ name.length }} {{ index.toFixed() }}</div>`
	twigRoot = twigparser.Parse(markup).Tree.Root
	nameOffset := uint32(strings.Index(markup, "name.length") + 1)
	nameBinding, err := idx.ResolveTwigVueBinding(
		twigRoot, []byte(markup), nameOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, nameBinding)
	assert.Equal(t, "string", nameBinding.Type)
	indexOffset := uint32(strings.Index(markup, "index.toFixed") + 1)
	indexBinding, err := idx.ResolveTwigVueBinding(
		twigRoot, []byte(markup), indexOffset, templatePath,
	)
	require.NoError(t, err)
	require.NotNil(t, indexBinding)
	assert.Equal(t, "number", indexBinding.Type)
}

func TestStaticComponentExpressionParsingStaysConservative(t *testing.T) {
	segments, found := vueStaticThisExpression(
		`this.orders?.last?.().transactions.first()`,
	)
	require.True(t, found)
	require.Len(t, segments, 4)
	assert.Equal(t, "orders", segments[0].Name)
	assert.True(t, segments[1].Called)
	assert.True(t, segments[3].Called)

	segments, found = vueStaticThisExpression(`this.orders[index].name`)
	require.True(t, found)
	require.Len(t, segments, 3)
	assert.True(t, segments[1].Indexed)
	assert.Equal(t, "index", segments[1].IndexExpression)
	_, found = vueStaticThisExpression(`this.orders[this.sorter]().name`)
	assert.False(t, found)

	store, storeSegments, found := vueStaticStoreExpression(
		`Shopware.Store.get('marketing')?.activeCampaign.name`,
	)
	require.True(t, found)
	assert.Equal(t, "marketing", store)
	require.Len(t, storeSegments, 2)
	assert.Equal(t, "activeCampaign", storeSegments[0].Name)
	templateSegments, found := vueStaticTemplateExpression(
		`page.sections.filter((section) => section.name)`,
	)
	require.True(t, found)
	require.Len(t, templateSegments, 3)
	assert.Equal(t, "page", templateSegments[0].Name)
	assert.Equal(t, "filter", templateSegments[2].Name)
	assert.True(t, templateSegments[2].Called)
	assert.Equal(
		t, "(section) => section.name", templateSegments[2].Arguments,
	)

	templateSegments, found = vueStaticTemplateExpression(
		`page.sections?.map((section) => section.name) ?? []`,
	)
	require.True(t, found)
	require.Len(t, templateSegments, 3)
	assert.Equal(t, "map", templateSegments[2].Name)
	assert.True(t, templateSegments[2].Optional)
}

func TestMergeVueAssignmentTypes(t *testing.T) {
	assert.Equal(
		t, "EntityCollection<'media'>",
		mergeVueAssignmentTypes(
			"Array", []string{"Array", "EntityCollection<'media'>"},
		),
	)
	assert.Equal(
		t, "Entity<'media'> | null",
		mergeVueAssignmentTypes("null", []string{"Entity<'media'> | null"}),
	)
	assert.Empty(t, mergeVueAssignmentTypes("Array", []string{"Array"}))
	assert.Empty(t, mergeVueAssignmentTypes(
		"Array", []string{"Array<unknown>"},
	))
}

func TestEffectiveComponentRefinesEmptyArrayFromLiteralWrites(t *testing.T) {
	rootDir := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(rootDir, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	definitionPath := filepath.Join(
		rootDir,
		"src/Administration/Resources/app/administration/src/component/view/index.js",
	)
	templatePath := filepath.Join(filepath.Dir(definitionPath), "view.html.twig")
	code := `export default {
	data() { return { items: [] }; },
	methods: {
		load() {
			this.items = [
				{ id: 'one', label: 'One' },
				{ id: 'two', label: 'Two' },
			];
		},
	},
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), syntax.NewLineIndex(code),
	)
	setDefinitionFilePath(definition, definitionPath)
	definition.TemplatePath = templatePath
	require.NoError(t, idx.SaveComponentDefinition(
		normalizeDefinitionPath(definitionPath), *definition,
	))
	require.NoError(t, idx.SaveComponent(VueComponent{
		Name: "sw-view", FilePath: definitionPath,
		DefinitionPath: definitionPath, TemplatePath: templatePath,
	}))

	component, err := idx.GetEffectiveComponent("sw-view")
	require.NoError(t, err)
	require.NotNil(t, component)
	items, found := component.TemplateMember("items")
	require.True(t, found)
	assert.Equal(t, "Array<{ id: string; label: string }>", items.Type)
	shape, err := idx.ResolveVueType(items.Type, definitionPath)
	require.NoError(t, err)
	assert.Contains(t, vueMemberNames(shape.Members), "length")
}
