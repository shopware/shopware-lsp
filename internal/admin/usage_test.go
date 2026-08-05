package admin

import (
	"path/filepath"
	"strings"
	"testing"

	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminUsageIndexTracksRegistryReferencesAcrossLanguages(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	jsPath := filepath.Join(adminRoot, "main.ts")
	twigPath := filepath.Join(adminRoot, "component.html.twig")
	jsSource := `
Shopware.Component.register('sw-card', definition);
Shopware.Component.extend('sw-child', 'sw-card', definition);
Shopware.Component.getComponentRegistry().has('sw-card');
Shopware.Application.addServiceProvider('acl', factory);
Shopware.Service('acl');
Shopware.Store.register('session', { actions: { login() {} } });
Shopware.Store.get('session').login();
Shopware.Store.unregister('session');
Shopware.Mixin.register('listing', definition);
Shopware.Mixin.getByName('listing');
Shopware.Directive.register('tooltip', definition);
Shopware.Directive.getByName('tooltip');
Shopware.Filter.register('currency', value => value);
Shopware.Filter.getByName('currency');
Shopware.Module.register('sw-product', { routes: {} });
Shopware.Module.getModuleRegistry().get('sw-product');
this.$router.resolve({ name: 'sw.product.detail' });
export default {
    inject: ['acl'],
    methods: { check() { return this.acl.can('product.viewer'); } },
};`
	twigSource := `<sw-card v-tooltip.bottom="title" :disabled="acl.can('product.viewer')"><router-link :to="{ name: 'sw.product.detail' }" /></sw-card>`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(jsPath, []byte(jsSource))))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(twigPath, []byte(twigSource))))

	assertUsageOccurrenceCount(t, idx, AdminSymbolComponent, "", "sw-card", 5)
	assertUsageOccurrenceCount(t, idx, AdminSymbolComponent, "", "sw-child", 1)
	assertUsageOccurrenceCount(t, idx, AdminSymbolService, "", "acl", 4)
	assertUsageOccurrenceCount(t, idx, AdminSymbolStore, "", "session", 3)
	assertUsageOccurrenceCount(t, idx, AdminSymbolStoreMember, "session", "login", 1)
	assertUsageOccurrenceCount(t, idx, AdminSymbolMixin, "", "listing", 2)
	assertUsageOccurrenceCount(t, idx, AdminSymbolDirective, "", "tooltip", 3)
	assertUsageOccurrenceCount(t, idx, AdminSymbolFilter, "", "currency", 2)
	assertUsageOccurrenceCount(t, idx, AdminSymbolModule, "", "sw-product", 2)
	assertUsageOccurrenceCount(t, idx, AdminSymbolPrivilege, "", "product.viewer", 2)
	assertUsageOccurrenceCount(t, idx, AdminSymbolModuleRoute, "", "sw.product.detail", 2)

	sets, err := idx.GetUsages(AdminSymbolComponent, "", "sw-card")
	require.NoError(t, err)
	var declarations int
	for _, set := range sets {
		for _, occurrence := range set.Occurrences {
			if occurrence.Declaration {
				declarations++
			}
		}
	}
	assert.Equal(t, 1, declarations)

	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		twigPath, []byte(`<div />`),
	)))
	assertUsageOccurrenceCount(t, idx, AdminSymbolComponent, "", "sw-card", 3)
	assertUsageOccurrenceCount(t, idx, AdminSymbolDirective, "", "tooltip", 2)
	require.NoError(t, idx.RemovedFiles([]string{jsPath}))
	assertUsageOccurrenceCount(t, idx, AdminSymbolComponent, "", "sw-card", 0)
	assertUsageOccurrenceCount(t, idx, AdminSymbolDirective, "", "tooltip", 0)
	assertUsageOccurrenceCount(t, idx, AdminSymbolFilter, "", "currency", 0)
}

func TestJavaScriptFilterNameForCalleeHonorsConstScope(t *testing.T) {
	source := `
const format = Shopware.Filter.getByName('currency');
format(1);
function inner() {
    const format = Filter.getByName('date');
    format('2026-01-01');
}
format(2);
let mutable = Filter.getByName('asset');
mutable('/icon.svg');`
	root := javascriptparser.Parse(source).Tree.Root
	var calls []*jssyntax.Node
	var mutable []*jssyntax.Node
	for _, call := range jsquery.Calls(root) {
		callee := jsquery.CallCallee(call)
		switch jsquery.IdentifierText(callee) {
		case "format":
			calls = append(calls, call)
		case "mutable":
			mutable = append(mutable, call)
		}
	}
	require.Len(t, calls, 3)
	for index, expected := range []string{"currency", "date", "currency"} {
		name, found := JavaScriptFilterNameForCallee(jsquery.CallCallee(calls[index]))
		require.True(t, found)
		assert.Equal(t, expected, name)
	}
	require.Len(t, mutable, 1)
	_, found := JavaScriptFilterNameForCallee(jsquery.CallCallee(mutable[0]))
	assert.False(t, found)
}

func TestCollectJavaScriptUsagesIndexesShopwareEventBusEvents(t *testing.T) {
	source := `
Shopware.Utils.EventBus.on('save', handler);
const { EventBus } = Shopware.Utils;
EventBus.emit('save', payload);
EventBus.off('save', handler);
other.EventBus.emit('save');
`
	root := javascriptparser.Parse(source).Tree.Root
	sets := CollectJavaScriptUsages(
		root, "/project/consumer.ts", jssyntax.NewLineIndex(source),
	)
	var eventSet *AdminUsageSet
	for index := range sets {
		if sets[index].Kind == AdminSymbolEventBusEvent &&
			sets[index].Name == "save" {
			eventSet = &sets[index]
			break
		}
	}
	require.NotNil(t, eventSet)
	assert.Len(t, eventSet.Occurrences, 3)
	for _, occurrence := range eventSet.Occurrences {
		assert.False(t, occurrence.Declaration)
	}
}

func TestAdminServiceUsagesIncludeApplicationContainerMembers(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	path := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/services.ts",
	)
	source := `
Shopware.Application.addServiceProvider('acl', factory);
Shopware.Application.getContainer('service').acl.can('read');
function run() {
    const services = Application.getContainer('service');
    return services.acl;
}
Application.getContainer('factory').locale;
`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(path, []byte(source))))
	assertUsageOccurrenceCount(t, idx, AdminSymbolService, "", "acl", 3)

	parsed := javascriptparser.Parse(source)
	offset := strings.LastIndex(source, "acl")
	target, found, err := idx.JavaScriptSymbolAt(
		path, parsed.Tree.Root.NodeAtOffset(uint32(offset+1)),
	)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, AdminSymbolTarget{
		Kind: AdminSymbolService, Name: "acl",
	}, target)
}

func TestAdminUsageIndexScopesLocalDirectivesToOwningTemplate(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	globalPath := filepath.Join(adminRoot, "app/directive/hide.ts")
	definitionPath := filepath.Join(adminRoot, "component/sw-owner/index.ts")
	ownerTemplate := filepath.Join(
		adminRoot, "component/sw-owner/sw-owner.html.twig",
	)
	otherTemplate := filepath.Join(adminRoot, "component/other.html.twig")
	definitionSource := `export default {
    directives: { hide: {} },
};`
	for path, source := range map[string]string{
		globalPath:     `Shopware.Directive.register('hide', {});`,
		definitionPath: definitionSource,
		ownerTemplate:  `<div v-hide="hidden"></div>`,
		otherTemplate:  `<div v-hide="hidden"></div>`,
	} {
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(path, []byte(source))))
	}
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, definitionSource), jssyntax.NewLineIndex(definitionSource),
	)
	setDefinitionFilePath(definition, definitionPath)
	require.NoError(t, idx.SaveComponent(VueComponent{
		Name: "sw-owner", FilePath: definitionPath,
		DefinitionPath: definitionPath, TemplatePath: ownerTemplate,
		LocalDirectives: definition.LocalDirectives,
	}))

	localTarget, found, err := idx.TwigSymbolAt(
		ownerTemplate,
		twigparser.Parse(`<div v-hide="hidden"></div>`).Tree.Root,
		uint32(len(`<div v-`)),
	)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, definitionPath, localTarget.Owner)
	localSets, err := idx.GetSymbolUsages(localTarget)
	require.NoError(t, err)
	assert.Equal(t, 2, adminUsageSetOccurrenceCount(localSets))

	globalTarget, found, err := idx.TwigSymbolAt(
		otherTemplate,
		twigparser.Parse(`<div v-hide="hidden"></div>`).Tree.Root,
		uint32(len(`<div v-`)),
	)
	require.NoError(t, err)
	require.True(t, found)
	assert.Empty(t, globalTarget.Owner)
	globalSets, err := idx.GetSymbolUsages(globalTarget)
	require.NoError(t, err)
	assert.Equal(t, 2, adminUsageSetOccurrenceCount(globalSets))
}

func TestAdminUsageIndexTracksCMSRegistries(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	path := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/cms.ts",
	)
	source := `
Shopware.Service('cmsService').registerCmsElement({
    name: 'hero', component: 'sw-cms-el-hero',
    configComponent: 'sw-cms-el-config-hero',
    previewComponent: 'sw-cms-el-preview-hero',
    defaultConfig: { card: { component: 'nested-component' } },
});
Shopware.Service('cmsService').registerCmsBlock({
    name: 'hero-grid', component: 'sw-cms-block-hero-grid',
    slots: { content: { type: 'hero' } },
});
Shopware.Service('cmsService').getCmsElementConfigByName('hero');
Shopware.Service('cmsService').getCmsBlockConfigByName('hero-grid');`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(path, []byte(source))))
	assertUsageOccurrenceCount(t, idx, AdminSymbolCMSElement, "", "hero", 3)
	assertUsageOccurrenceCount(t, idx, AdminSymbolCMSBlock, "", "hero-grid", 2)
	for _, name := range []string{
		"sw-cms-el-hero", "sw-cms-el-config-hero",
		"sw-cms-el-preview-hero", "sw-cms-block-hero-grid",
	} {
		assertUsageOccurrenceCount(t, idx, AdminSymbolComponent, "", name, 1)
	}

	for _, test := range []struct {
		needle string
		kind   AdminSymbolKind
		name   string
	}{
		{"'hero', component", AdminSymbolCMSElement, "hero"},
		{"type: 'hero'", AdminSymbolCMSElement, "hero"},
		{"ByName('hero-grid'", AdminSymbolCMSBlock, "hero-grid"},
		{"component: 'sw-cms-el-hero'", AdminSymbolComponent, "sw-cms-el-hero"},
		{"configComponent: 'sw-cms-el-config-hero'", AdminSymbolComponent, "sw-cms-el-config-hero"},
		{"previewComponent: 'sw-cms-el-preview-hero'", AdminSymbolComponent, "sw-cms-el-preview-hero"},
		{"component: 'sw-cms-block-hero-grid'", AdminSymbolComponent, "sw-cms-block-hero-grid"},
	} {
		offset := strings.Index(source, test.needle)
		require.NotEqual(t, -1, offset)
		node := javascriptparser.Parse(source).Tree.Root.NodeAtOffset(
			uint32(offset + strings.Index(test.needle, "'") + 1),
		)
		target, found := JavaScriptSymbolAt(node)
		require.True(t, found, test.needle)
		assert.Equal(t, test.kind, target.Kind)
		assert.Equal(t, test.name, target.Name)
	}

	nestedOffset := strings.Index(source, "nested-component") + 1
	require.Positive(t, nestedOffset)
	nestedNode := javascriptparser.Parse(source).Tree.Root.NodeAtOffset(
		uint32(nestedOffset),
	)
	_, found := JavaScriptCMSComponentReferenceAt(nestedNode)
	assert.False(t, found)
}

func TestAdminUsageIndexTracksSourceOwnedComponentEventsAndSlots(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "component/sw-card/index.ts")
	templatePath := filepath.Join(
		adminRoot, "component/sw-card/sw-card.html.twig",
	)
	consumerPath := filepath.Join(adminRoot, "consumer.html.twig")
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		definitionPath,
		[]byte(`export default {
    emits: ['update:modelValue'],
    methods: { update(value) { this.$emit('update:modelValue', value); } },
};`),
	)))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		templatePath,
		[]byte(`<div><slot name="header" /></div>`),
	)))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		consumerPath,
		[]byte(`<sw-card @update:model-value.stop="update"><template #header /></sw-card>`),
	)))

	assertUsageOccurrenceCount(
		t, idx, AdminSymbolComponentEvent, definitionPath,
		"update:model-value", 2,
	)
	assertUsageOccurrenceCount(
		t, idx, AdminSymbolComponentEvent, "sw-card",
		"update:model-value", 1,
	)
	assertUsageOccurrenceCount(
		t, idx, AdminSymbolComponentSlot, templatePath, "header", 1,
	)
	assertUsageOccurrenceCount(
		t, idx, AdminSymbolComponentSlot, "sw-card", "header", 1,
	)

	eventSets, err := idx.GetUsages(
		AdminSymbolComponentEvent,
		definitionPath,
		"update:model-value",
	)
	require.NoError(t, err)
	require.Len(t, eventSets, 1)
	declarations := 0
	camelSpellings := 0
	for _, occurrence := range eventSets[0].Occurrences {
		if occurrence.Declaration {
			declarations++
		}
		if occurrence.NameStyle == AdminNameCamel {
			camelSpellings++
		}
	}
	assert.Equal(t, 1, declarations)
	assert.Equal(t, 2, camelSpellings)
}

func TestAdminUsageIndexTracksFiniteDynamicComponentContracts(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	templatePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/consumer.html.twig",
	)
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		templatePath,
		[]byte(`<component :is="active ? 'sw-card' : 'sw-panel'" :title="title" @save="save" />`),
	)))
	for _, owner := range []string{"sw-card", "sw-panel"} {
		assertUsageOccurrenceCount(
			t, idx, AdminSymbolComponentProp, owner, "title", 1,
		)
		assertUsageOccurrenceCount(
			t, idx, AdminSymbolComponentEvent, owner, "save", 1,
		)
	}
}

func TestAdminUsageIndexResolvesInferredDynamicComponentContractsAtQueryTime(
	t *testing.T,
) {
	root := t.TempDir()
	cachePath := filepath.Join(root, "cache")
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	templatePath := filepath.Join(
		adminRoot, "component/sw-host/sw-host.html.twig",
	)
	cardPath := filepath.Join(adminRoot, "component/sw-card/index.ts")
	panelPath := filepath.Join(adminRoot, "component/sw-panel/index.ts")
	idx, err := NewAdminComponentIndexer(cachePath)
	require.NoError(t, err)
	for _, component := range []VueComponent{
		{
			Name: "sw-card", FilePath: cardPath, DefinitionPath: cardPath,
			Props: []VueComponentProp{
				{Name: "title", FilePath: cardPath},
				{Name: "checked", FilePath: cardPath},
			},
			Events: []VueComponentEvent{
				{Name: "save", FilePath: cardPath},
				{Name: "update:checked", FilePath: cardPath},
			},
			Slots: []VueComponentSlot{{
				Name: "header", FilePath: cardPath,
			}},
		},
		{
			Name: "sw-panel", FilePath: panelPath, DefinitionPath: panelPath,
			Props: []VueComponentProp{
				{Name: "title", FilePath: panelPath},
				{Name: "checked", FilePath: panelPath},
			},
			Events: []VueComponentEvent{
				{Name: "save", FilePath: panelPath},
				{Name: "update:checked", FilePath: panelPath},
			},
			Slots: []VueComponentSlot{{
				Name: "header", FilePath: panelPath,
			}},
		},
		{
			Name: "sw-host", FilePath: filepath.Join(
				adminRoot, "component/sw-host/index.ts",
			),
			TemplatePath: templatePath,
			Members: []VueComponentMember{{
				Name: "dynamicCard", Kind: ComponentMemberComputed,
				ReturnExpressions: []string{"'sw-card'", "'sw-panel'"},
				ReturnsComplete:   true,
			}},
		},
	} {
		require.NoError(t, idx.SaveComponent(component))
	}
	source := `<component :is="dynamicCard" v-bind="{ title }" @save="save" v-model:checked="checked"><template #header /></component>` +
		`<component :is="runtimeComponent" :title="title" />`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		templatePath, []byte(source),
	)))

	raw, err := idx.GetUsages(
		AdminSymbolComponentProp,
		adminDynamicComponentUsageOwner,
		"title",
	)
	require.NoError(t, err)
	require.Len(t, raw, 1)
	require.Len(t, raw[0].Occurrences, 2)
	assert.Equal(
		t, "dynamicCard",
		raw[0].Occurrences[0].DynamicComponentSelector,
	)
	assert.Equal(t, AdminNameShorthand, raw[0].Occurrences[0].NameStyle)
	assert.Equal(
		t, "runtimeComponent",
		raw[0].Occurrences[1].DynamicComponentSelector,
	)

	assertDynamicUsage := func(
		target AdminSymbolTarget,
		wantKind AdminSymbolKind,
	) {
		sets, usageErr := idx.GetSymbolUsages(target)
		require.NoError(t, usageErr)
		require.Equal(t, 1, adminUsageSetOccurrenceCount(sets), target)
		require.Len(t, sets, 1)
		assert.Equal(t, wantKind, sets[0].Kind)
		assert.Equal(t, adminDynamicComponentUsageOwner, sets[0].Owner)
	}
	assertDynamicUsage(AdminSymbolTarget{
		Kind: AdminSymbolComponentProp, Owner: cardPath, Name: "title",
	}, AdminSymbolComponentProp)
	assertDynamicUsage(AdminSymbolTarget{
		Kind: AdminSymbolComponentEvent, Owner: cardPath, Name: "save",
	}, AdminSymbolComponentEvent)
	assertDynamicUsage(AdminSymbolTarget{
		Kind: AdminSymbolComponentSlot, Owner: cardPath, Name: "header",
	}, AdminSymbolComponentSlot)
	assertDynamicUsage(AdminSymbolTarget{
		Kind: AdminSymbolComponentProp, Owner: cardPath, Name: "checked",
	}, AdminSymbolComponentModel)
	assertDynamicUsage(AdminSymbolTarget{
		Kind:  AdminSymbolComponentEvent,
		Owner: cardPath,
		Name:  "update:checked",
	}, AdminSymbolComponentModel)

	require.NoError(t, idx.Close())
	reopened, err := NewAdminComponentIndexer(cachePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	sets, err := reopened.GetSymbolUsages(AdminSymbolTarget{
		Kind: AdminSymbolComponentProp, Owner: cardPath, Name: "title",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, adminUsageSetOccurrenceCount(sets))
}

func TestAdminUsageIndexResolvesRouterViewDynamicComponentContracts(
	t *testing.T,
) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(
		adminRoot, "module/sw-account/index.ts",
	)
	templatePath := filepath.Join(
		adminRoot, "module/sw-account/sw-account.html.twig",
	)
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		definitionPath,
		[]byte(`
import template from './sw-account.html.twig';
Shopware.Component.register('sw-account', { template });
Shopware.Module.register('sw-account', {
    routes: {
        index: {
            component: 'sw-account',
            children: {
                general: { component: 'sw-account-general' },
                history: { component: 'sw-account-history' },
            },
        },
    },
});`),
	)))
	generalPath := filepath.Join(
		adminRoot, "component/sw-account-general/index.ts",
	)
	for _, component := range []VueComponent{
		{
			Name: "sw-account-general", FilePath: generalPath,
			DefinitionPath: generalPath,
			Props: []VueComponentProp{{
				Name: "account", FilePath: generalPath,
			}},
		},
		{
			Name: "sw-account-history",
			FilePath: filepath.Join(
				adminRoot, "component/sw-account-history/index.ts",
			),
		},
	} {
		require.NoError(t, idx.SaveComponent(component))
	}
	source := `<router-view v-slot="{ Component: view }"><component :is="view" :account="account" /></router-view>` +
		`<router-view v-slot="route"><component :is="route.Component" :account="account" /></router-view>`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		templatePath, []byte(source),
	)))
	raw, err := idx.GetUsages(
		AdminSymbolComponentProp,
		adminDynamicComponentUsageOwner,
		"account",
	)
	require.NoError(t, err)
	require.Len(t, raw, 1)
	require.Len(t, raw[0].Occurrences, 2)
	assert.True(t, raw[0].Occurrences[0].DynamicRouterView)
	assert.True(t, raw[0].Occurrences[1].DynamicRouterView)
	sets, err := idx.GetSymbolUsages(AdminSymbolTarget{
		Kind: AdminSymbolComponentProp, Owner: generalPath, Name: "account",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, adminUsageSetOccurrenceCount(sets))
}

func TestAdminUsageIndexTracksComponentObjectBindings(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	templatePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/consumer.html.twig",
	)
	source := `<sw-card v-bind="{ title, 'is-disabled': disabled, ...attrs }" />`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		templatePath, []byte(source),
	)))

	assertUsageOccurrenceCount(
		t, idx, AdminSymbolComponentProp, "sw-card", "title", 1,
	)
	assertUsageOccurrenceCount(
		t, idx, AdminSymbolComponentProp, "sw-card", "isDisabled", 1,
	)
	sets, err := idx.GetUsages(
		AdminSymbolComponentProp, "sw-card", "title",
	)
	require.NoError(t, err)
	require.Len(t, sets, 1)
	require.Len(t, sets[0].Occurrences, 1)
	assert.Equal(t, AdminNameShorthand, sets[0].Occurrences[0].NameStyle)
	memberSets, err := idx.GetUsages(
		AdminSymbolComponentMember, "", "title",
	)
	require.NoError(t, err)
	require.Len(t, memberSets, 1)
	require.Len(t, memberSets[0].Occurrences, 1)
	assert.Equal(
		t, AdminNameShorthand, memberSets[0].Occurrences[0].NameStyle,
	)

	tree := twigparser.Parse(source).Tree.Root
	for _, test := range []struct {
		needle string
		name   string
	}{
		{"title", "title"},
		{"is-disabled", "isDisabled"},
	} {
		offset := uint32(strings.Index(source, test.needle) + 1)
		target, found := TwigSymbolAtOffset(tree, offset)
		require.True(t, found)
		assert.Equal(t, AdminSymbolComponentProp, target.Kind)
		assert.Equal(t, "sw-card", target.Owner)
		assert.Equal(t, test.name, target.Name)
	}
}

func TestAdminSymbolTargetsUseSemanticContexts(t *testing.T) {
	jsCases := []struct {
		source string
		needle string
		kind   AdminSymbolKind
		owner  string
		name   string
	}{
		{`Shopware.Component.extend('sw-child', 'sw-card', value)`, "sw-card", AdminSymbolComponent, "", "sw-card"},
		{`Shopware.Application.addServiceProvider('acl', factory)`, "acl", AdminSymbolService, "", "acl"},
		{`Shopware.Store.register({ id: 'session' })`, "session", AdminSymbolStore, "", "session"},
		{`Shopware.Store.get('session').login()`, "login", AdminSymbolStoreMember, "session", "login"},
		{`Shopware.Mixin.getByName('listing')`, "listing", AdminSymbolMixin, "", "listing"},
		{`Shopware.Component.getComponentRegistry().has('sw-card')`, "sw-card", AdminSymbolComponent, "", "sw-card"},
		{`Module.getModuleRegistry().get('sw-product')`, "sw-product", AdminSymbolModule, "", "sw-product"},
		{`acl.can('product.viewer')`, "product.viewer", AdminSymbolPrivilege, "", "product.viewer"},
		{`this.$router.resolve({ name: 'sw.product.detail' })`, "sw.product.detail", AdminSymbolModuleRoute, "", "sw.product.detail"},
	}
	for _, test := range jsCases {
		t.Run(test.source, func(t *testing.T) {
			result := javascriptparser.Parse(test.source)
			offset := uint32(strings.Index(test.source, test.needle) + 1)
			target, found := JavaScriptSymbolAt(result.Tree.Root.NodeAtOffset(offset))
			require.True(t, found)
			assert.Equal(t, test.kind, target.Kind)
			assert.Equal(t, test.owner, target.Owner)
			assert.Equal(t, test.name, target.Name)
		})
	}

	twigSource := `<sw-card :disabled="acl.can('product.viewer')"></sw-card>`
	twigResult := twigparser.Parse(twigSource)
	for _, offset := range []int{
		strings.Index(twigSource, "sw-card") + 1,
		strings.LastIndex(twigSource, "sw-card") + 1,
	} {
		target, found := TwigSymbolAtOffset(twigResult.Tree.Root, uint32(offset))
		require.True(t, found)
		assert.Equal(t, AdminSymbolComponent, target.Kind)
		assert.Equal(t, "sw-card", target.Name)
	}
	privilegeOffset := uint32(strings.Index(twigSource, "product.viewer") + 1)
	privilegeTarget, found := TwigSymbolAtOffset(
		twigResult.Tree.Root,
		privilegeOffset,
	)
	require.True(t, found)
	assert.Equal(t, AdminSymbolPrivilege, privilegeTarget.Kind)
	assert.Equal(t, "product.viewer", privilegeTarget.Name)

	markupSource := `<sw-card @update:model-value.stop="update" v-model:checked.trim="selected"><template #header /></sw-card>`
	markupResult := twigparser.Parse(markupSource)
	for _, test := range []struct {
		needle string
		kind   AdminSymbolKind
		owner  string
		name   string
	}{
		{"update:model-value", AdminSymbolComponentEvent, "sw-card", "update:model-value"},
		{"checked", AdminSymbolComponentModel, "sw-card", "v-model:checked"},
		{"header", AdminSymbolComponentSlot, "sw-card", "header"},
	} {
		offset := uint32(strings.Index(markupSource, test.needle) + 1)
		target, targetFound := TwigSymbolAtOffset(
			markupResult.Tree.Root,
			offset,
		)
		require.True(t, targetFound)
		assert.Equal(t, test.kind, target.Kind)
		assert.Equal(t, test.owner, target.Owner)
		assert.Equal(t, test.name, target.Name)
	}
}

func TestJavaScriptComponentEventContexts(t *testing.T) {
	for _, test := range []struct {
		source, needle, expected string
		found                    bool
	}{
		{`export default { emits: ['update:modelValue'] };`, "update:modelValue", "update:model-value", true},
		{`export default { emits: { save: null } };`, "save", "save", true},
		{`export default { methods: { save() { this.$emit('saved'); } } };`, "saved", "saved", true},
		{`export default { props: { saved: String } };`, "saved", "", false},
	} {
		result := javascriptparser.Parse(test.source)
		offset := uint32(strings.Index(test.source, test.needle) + 1)
		name, found := JavaScriptComponentEventAt(
			result.Tree.Root.NodeAtOffset(offset),
		)
		assert.Equal(t, test.found, found)
		assert.Equal(t, test.expected, name)
	}
}

func TestJavaScriptRegistryReferenceContexts(t *testing.T) {
	tests := []struct {
		source    string
		needle    string
		kind      AdminSymbolKind
		operation string
		found     bool
	}{
		{`Shopware.Component.getComponentRegistry().get('sw-card')`, "sw-card", AdminSymbolComponent, "get", true},
		{`Component.getComponentRegistry().has('sw-card')`, "sw-card", AdminSymbolComponent, "has", true},
		{`Shopware.Module.getModuleRegistry().get('sw-product')`, "sw-product", AdminSymbolModule, "get", true},
		{`Module.getModuleRegistry().has('sw-product')`, "sw-product", AdminSymbolModule, "has", true},
		{`other.getComponentRegistry().get('sw-card')`, "sw-card", "", "", false},
		{`Shopware.Module.getModuleRegistry().get(moduleName)`, "moduleName", "", "", false},
	}
	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			result := javascriptparser.Parse(test.source)
			offset := uint32(strings.Index(test.source, test.needle) + 1)
			reference, found := JavaScriptRegistryReferenceAt(
				result.Tree.Root.NodeAtOffset(offset),
			)
			assert.Equal(t, test.found, found)
			if !test.found {
				return
			}
			assert.Equal(t, test.kind, reference.Kind)
			assert.Equal(t, test.operation, reference.Operation)
			assert.Equal(t, test.needle, reference.Name)
		})
	}
}

func TestJavaScriptModuleRouteReferenceContexts(t *testing.T) {
	source := `
Module.register('sw-product', {
    name: 'product',
    routes: {
        detail: {
            path: 'detail/:id',
            redirect: { name: 'sw.product.detail.base' },
            meta: { parentPath: 'sw.product.parent' },
        },
    },
    navigation: [{ path: 'sw.product.index' }],
    settingsItem: { to: 'sw.settings.product.index' },
});
this.$router.push({ name: 'sw.product.detail' });
const unrelated = { name: 'sw.not.a.route', path: 'sw.also.not.a.route', to: 'sw.nope' };
`
	result := javascriptparser.Parse(source)
	cases := []struct {
		needle string
		want   bool
	}{
		{"product", false},
		{"detail/:id", false},
		{"sw.product.detail.base", true},
		{"sw.product.parent", true},
		{"sw.product.index", true},
		{"sw.settings.product.index", true},
		{"sw.product.detail", true},
		{"sw.not.a.route", false},
		{"sw.also.not.a.route", false},
		{"sw.nope", false},
	}
	for _, test := range cases {
		t.Run(test.needle, func(t *testing.T) {
			offset := uint32(strings.Index(source, "'"+test.needle+"'") + 2)
			node := result.Tree.Root.NodeAtOffset(offset)
			assert.Equal(t, test.want, IsJavaScriptModuleRouteReference(node))
		})
	}
}

func TestAdminIndexerIgnoresGeneratedAdministrationArtifacts(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	generatedPath := filepath.Join(
		root,
		"custom/plugins/Demo/src/Resources/app/administration/.tmp/vite/deps/bundle.js",
	)
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		generatedPath,
		[]byte(`Shopware.Component.register('sw-generated', {});`),
	)))

	components, err := idx.GetComponent("sw-generated")
	require.NoError(t, err)
	assert.Empty(t, components)
	assertUsageOccurrenceCount(
		t, idx, AdminSymbolComponent, "", "sw-generated", 0,
	)
}

func TestAdminComponentMemberUsagesFollowInheritanceAndLexicalShadowing(
	t *testing.T,
) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	basePath := filepath.Join(adminRoot, "component/sw-base/index.ts")
	baseTemplate := filepath.Join(
		filepath.Dir(basePath), "sw-base.html.twig",
	)
	childPath := filepath.Join(adminRoot, "component/sw-child/index.ts")
	childTemplate := filepath.Join(
		filepath.Dir(childPath), "sw-child.html.twig",
	)
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		basePath,
		[]byte(`import template from './sw-base.html.twig';
Component.register('sw-base', {
    template,
    data() {
        const count = 0;
        return { count };
    },
    methods: { save() { this.count += 1; } },
});`),
	)))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		childPath,
		[]byte(`import template from './sw-child.html.twig';
Component.extend('sw-child', 'sw-base', {
    template,
    methods: { touch() { return this.count; } },
});`),
	)))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		baseTemplate, []byte(`{{ count }}`),
	)))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		childTemplate,
		[]byte(`<div v-for="count in rows">{{ count }}</div>{{ count }}`),
	)))

	component, err := idx.GetEffectiveComponent("sw-base")
	require.NoError(t, err)
	require.NotNil(t, component)
	member, found := component.TemplateMember("count")
	require.True(t, found)
	require.True(t, member.Renameable())
	target := componentMemberTarget(member)
	sets, err := idx.GetSymbolUsages(target)
	require.NoError(t, err)
	count := 0
	declarations := 0
	shorthandDeclarations := 0
	for _, set := range sets {
		for _, occurrence := range set.Occurrences {
			count++
			if occurrence.Declaration {
				declarations++
				if occurrence.NameStyle == AdminNameShorthand {
					shorthandDeclarations++
				}
			}
		}
	}
	assert.Equal(t, 5, count)
	assert.Equal(t, 1, declarations)
	assert.Equal(t, 1, shorthandDeclarations)
}

func assertUsageOccurrenceCount(
	t *testing.T,
	idx *AdminComponentIndexer,
	kind AdminSymbolKind,
	owner,
	name string,
	expected int,
) {
	t.Helper()
	sets, err := idx.GetUsages(kind, owner, name)
	require.NoError(t, err)
	count := 0
	for _, set := range sets {
		count += len(set.Occurrences)
	}
	assert.Equal(t, expected, count, "%s %s", kind, name)
}

func adminUsageSetOccurrenceCount(sets []AdminUsageSet) int {
	count := 0
	for _, set := range sets {
		count += len(set.Occurrences)
	}
	return count
}
