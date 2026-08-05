package reference

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminReferencesCrossJavaScriptAndTwig(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "main.ts")
	definitionPath := filepath.Join(adminRoot, "sw-card/index.ts")
	templatePath := filepath.Join(adminRoot, "sw-card/sw-card.html.twig")
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`
Shopware.Component.register('sw-card', () => import('./sw-card'));
Shopware.Component.extend('sw-child', 'sw-card', definition);
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
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product', roles: { viewer: { privileges: ['product:read'] } },
});
acl.can('product.viewer');
Shopware.Module.register('sw-product', {
    routes: { detail: { path: 'detail', component: 'sw-detail' } },
});
Shopware.Module.getModuleRegistry().get('sw-product');
this.$router.resolve({ name: 'sw.product.detail' });
`),
	)))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		definitionPath,
		[]byte(`
export default {
    inject: ['acl'],
    methods: { check() { return this.acl.can('product.viewer'); } },
};`),
	)))
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: registrationPath,
		DefinitionPath: definitionPath, Injected: []string{"acl"},
	}))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		templatePath,
		[]byte(`<sw-card v-tooltip.bottom="message" :disabled="acl.can('product.viewer')"><router-link :to="{ name: 'sw.product.detail' }" /></sw-card>`),
	)))
	provider := NewAdminReferenceProvider(idx)

	tests := []struct {
		name        string
		path        string
		source      string
		needle      string
		includeDecl bool
		want        int
	}{
		{"component usages", templatePath, `<sw-card></sw-card>`, "sw-card", false, 3},
		{"component with declaration", templatePath, `<sw-card></sw-card>`, "sw-card", true, 4},
		{"service from this member", definitionPath, `export default { inject: ['acl'], methods: { check() { return this.acl; } } };`, "this.acl", false, 3},
		{"service excludes stale same-file declaration", registrationPath, `Shopware.Service('acl')`, "acl", true, 3},
		{"store member excludes stale same-file declaration", registrationPath, `Shopware.Store.get('session').login()`, "login", true, 1},
		{"store unregister usage", filepath.Join(adminRoot, "store-consumer.ts"), `Shopware.Store.unregister('session')`, "session", false, 2},
		{"privilege excludes stale same-file declaration", registrationPath, `acl.can('product.viewer')`, "product.viewer", true, 3},
		{"privilege from Twig", templatePath, `<sw-card :disabled="acl.can('product.viewer')"></sw-card>`, "product.viewer", false, 3},
		{"module route from Twig", templatePath, `<router-link :to="{ name: 'sw.product.detail' }" />`, "sw.product.detail", true, 3},
		{"module registry usage", registrationPath, `Module.getModuleRegistry().get('sw-product')`, "sw-product", false, 1},
		{"module registry excludes stale same-file declaration", registrationPath, `Module.getModuleRegistry().get('sw-product')`, "sw-product", true, 1},
		{"directive from Twig", templatePath, `<div v-tooltip.bottom="message"></div>`, "tooltip", false, 2},
		{"directive excludes stale same-file declaration", registrationPath, `Shopware.Directive.getByName('tooltip')`, "tooltip", true, 2},
		{"filter registry usage", filepath.Join(adminRoot, "filter-consumer.ts"), `Shopware.Filter.getByName('currency')`, "currency", false, 1},
		{"filter includes declaration", filepath.Join(adminRoot, "filter-consumer.ts"), `Shopware.Filter.getByName('currency')`, "currency", true, 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			locations, referenceErr := provider.GetReferences(
				context.Background(),
				adminReferenceRequest(
					test.path, test.source, test.needle, test.includeDecl,
				),
			)
			require.NoError(t, referenceErr)
			assert.Len(t, locations, test.want)
		})
	}
}

func TestAdminShopwareEventBusReferencesIncludeTypedDeclaration(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
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
	firstPath := filepath.Join(adminRoot, "module/first/index.ts")
	firstSource := `Shopware.Utils.EventBus.on('save-event', handler);`
	secondPath := filepath.Join(adminRoot, "module/second/index.ts")
	secondSource := `const { EventBus } = Shopware.Utils;
EventBus.emit('save-event', payload);
EventBus.off('save-event', handler);`
	for path, source := range map[string]string{
		utilPath: `import EventBus from './utils/eventBus.utils';
export default { EventBus };`,
		eventBusPath: eventBusSource,
		firstPath:    firstSource,
		secondPath:   secondSource,
	} {
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
			path, []byte(source),
		)))
	}
	provider := NewAdminReferenceProvider(idx)
	locations, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(firstPath, firstSource, "save-event", false),
	)
	require.NoError(t, err)
	require.Len(t, locations, 3)
	assert.Equal(t, 1, countAdminReferenceLocations(locations, firstPath))
	assert.Equal(t, 2, countAdminReferenceLocations(locations, secondPath))

	locations, err = provider.GetReferences(
		context.Background(),
		adminReferenceRequest(firstPath, firstSource, "save-event", true),
	)
	require.NoError(t, err)
	require.Len(t, locations, 4)
	declarationOffset := uint32(strings.Index(eventBusSource, "save-event"))
	line, character := lsp.NewTextDocument(
		uriutil.FileURI(eventBusPath), eventBusSource, 1,
	).LineIndex.PositionUTF16(declarationOffset)
	var declaration *protocol.Location
	for index := range locations {
		if locations[index].URI == uriutil.FileURI(eventBusPath) {
			declaration = &locations[index]
			break
		}
	}
	require.NotNil(t, declaration)
	assert.Equal(t, int(line), declaration.Range.Start.Line)
	assert.Equal(t, int(character), declaration.Range.Start.Character)
	assert.Equal(t, int(character)+len("save-event"), declaration.Range.End.Character)
}

func TestAdminServiceReferencesIncludeApplicationContainerMembers(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "services.ts")
	consumerPath := filepath.Join(adminRoot, "consumer.ts")
	consumerSource := `
Shopware.Application.getContainer('service').acl;
function run() {
    const services = Application.getContainer('service');
    return services.acl;
}`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`Shopware.Application.addServiceProvider('acl', factory);`),
	)))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		consumerPath, []byte(consumerSource),
	)))
	provider := NewAdminReferenceProvider(idx)
	withoutDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(
			consumerPath, consumerSource, "acl", false,
		),
	)
	require.NoError(t, err)
	assert.Len(t, withoutDeclaration, 2)
	withDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(
			consumerPath, consumerSource, "acl", true,
		),
	)
	require.NoError(t, err)
	assert.Len(t, withDeclaration, 3)
}

func TestAdminReferencesKeepLocalAndGlobalDirectivesSeparate(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
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
	definitionSource := `export default { directives: { hide: {} } };`
	templateSource := `<div v-hide="hidden"></div>`
	for path, source := range map[string]string{
		globalPath:     `Shopware.Directive.register('hide', {});`,
		definitionPath: definitionSource,
		ownerTemplate:  templateSource,
		otherTemplate:  templateSource,
	} {
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(path, []byte(source))))
	}
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-owner", FilePath: definitionPath,
		DefinitionPath: definitionPath, TemplatePath: ownerTemplate,
		LocalDirectives: []admin.VueLocalDirective{{
			Name: "hide", FilePath: definitionPath, Line: 1,
		}},
	}))
	provider := NewAdminReferenceProvider(idx)
	local, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(ownerTemplate, templateSource, "hide", true),
	)
	require.NoError(t, err)
	require.Len(t, local, 2)
	assert.Equal(t, uriutil.FileURI(definitionPath), local[0].URI)
	assert.Equal(t, uriutil.FileURI(ownerTemplate), local[1].URI)

	global, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(otherTemplate, templateSource, "hide", true),
	)
	require.NoError(t, err)
	require.Len(t, global, 2)
	assert.Equal(t, uriutil.FileURI(globalPath), global[0].URI)
	assert.Equal(t, uriutil.FileURI(otherTemplate), global[1].URI)
}

func TestAdminCMSRegistryReferences(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "cms.ts")
	consumerPath := filepath.Join(adminRoot, "consumer.ts")
	registrationSource := `
Shopware.Component.register('sw-cms-el-hero', {});
cmsService.registerCmsElement({ name: 'hero', component: 'sw-cms-el-hero' });
cmsService.registerCmsBlock({ name: 'hero-grid', slots: { content: { type: 'hero' } } });`
	consumerSource := `
cmsService.getCmsElementConfigByName('hero');
cmsService.getCmsBlockConfigByName('hero-grid');`
	for path, source := range map[string]string{
		registrationPath: registrationSource,
		consumerPath:     consumerSource,
	} {
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(path, []byte(source))))
	}
	provider := NewAdminReferenceProvider(idx)
	elementLocations, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(consumerPath, consumerSource, "hero');", true),
	)
	require.NoError(t, err)
	assert.Len(t, elementLocations, 3)
	blockLocations, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(consumerPath, consumerSource, "hero-grid", true),
	)
	require.NoError(t, err)
	assert.Len(t, blockLocations, 2)
	componentLocations, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(
			registrationPath, registrationSource, "sw-cms-el-hero", true,
		),
	)
	require.NoError(t, err)
	assert.Len(t, componentLocations, 2)
}

func TestAdminReferencesRespectDeclarationFlagAndStaleRemoval(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "main.ts")
	usagePath := filepath.Join(adminRoot, "consumer.ts")
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`Shopware.Component.register('sw-card', definition);`),
	)))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		usagePath,
		[]byte(`Shopware.Component.override('sw-card', definition);`),
	)))
	provider := NewAdminReferenceProvider(idx)
	request := adminReferenceRequest(
		usagePath, `Shopware.Component.override('sw-card', definition);`,
		"sw-card", false,
	)
	locations, err := provider.GetReferences(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(usagePath), locations[0].URI)

	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		usagePath, []byte(`export default {};`),
	)))
	locations, err = provider.GetReferences(context.Background(), request)
	require.NoError(t, err)
	assert.Empty(t, locations)
}

func TestAdminModelReferencesUnifyPropAndUpdateEvent(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "sw-field/index.ts")
	templatePath := filepath.Join(adminRoot, "consumer/consumer.html.twig")
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-field", FilePath: definitionPath,
		DefinitionPath: definitionPath,
		Props: []admin.VueComponentProp{
			{Name: "modelValue", Type: "Boolean", FilePath: definitionPath, Line: 3},
			{Name: "checked", Type: "Boolean", FilePath: definitionPath, Line: 4},
		},
		Events: []admin.VueComponentEvent{
			{Name: "update:modelValue", Type: "(value: boolean) => void", FilePath: definitionPath, Line: 7},
			{Name: "update:checked", Type: "(value: boolean) => void", FilePath: definitionPath, Line: 8},
		},
	}))
	source := `<sw-field v-model="enabled" v-model:checked="selected" />`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		templatePath, []byte(source),
	)))
	provider := NewAdminReferenceProvider(idx)

	for _, test := range []struct {
		needle string
		want   int
	}{
		{"v-model", 3},
		{"checked", 3},
	} {
		locations, referenceErr := provider.GetReferences(
			context.Background(),
			adminReferenceRequest(
				templatePath, source, test.needle, true,
			),
		)
		require.NoError(t, referenceErr)
		assert.Len(t, locations, test.want)
		uris := make([]string, 0, len(locations))
		for _, location := range locations {
			uris = append(uris, location.URI)
		}
		assert.ElementsMatch(t, []string{
			uriutil.FileURI(templatePath),
			uriutil.FileURI(definitionPath),
			uriutil.FileURI(definitionPath),
		}, uris)
	}
}

func TestAdminDynamicComponentReferencesIncludeEveryStaticBranch(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "sw-card/index.ts")
	templatePath := filepath.Join(adminRoot, "consumer.html.twig")
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: definitionPath,
		DefinitionPath: definitionPath, Line: 4,
	}))
	source := `<component :is="active ? 'sw-card' : 'sw-card'" />`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		templatePath, []byte(source),
	)))
	locations, err := NewAdminReferenceProvider(idx).GetReferences(
		context.Background(),
		adminReferenceRequest(templatePath, source, "sw-card", true),
	)
	require.NoError(t, err)
	require.Len(t, locations, 3)
	assert.ElementsMatch(t, []string{
		uriutil.FileURI(definitionPath),
		uriutil.FileURI(templatePath),
		uriutil.FileURI(templatePath),
	}, []string{locations[0].URI, locations[1].URI, locations[2].URI})
}

func TestAdminInferredDynamicComponentAttributeReferencesUseEveryContract(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	templatePath := filepath.Join(adminRoot, "consumer.html.twig")
	cardPath := filepath.Join(adminRoot, "sw-card/index.ts")
	panelPath := filepath.Join(adminRoot, "sw-panel/index.ts")
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-card", FilePath: cardPath, DefinitionPath: cardPath,
			Props: []admin.VueComponentProp{{
				Name: "title", FilePath: cardPath, Line: 5,
			}},
		},
		{
			Name: "sw-panel", FilePath: panelPath, DefinitionPath: panelPath,
			Props: []admin.VueComponentProp{{
				Name: "title", FilePath: panelPath, Line: 7,
			}},
		},
		{
			Name: "sw-host", FilePath: filepath.Join(adminRoot, "sw-host/index.ts"),
			TemplatePath: templatePath,
			Members: []admin.VueComponentMember{{
				Name: "dynamicCard", Kind: admin.ComponentMemberComputed,
				ReturnExpressions: []string{"'sw-card'", "'sw-panel'"},
				ReturnsComplete:   true,
			}},
		},
	} {
		require.NoError(t, idx.SaveComponent(component))
	}
	source := `<component :is="dynamicCard" v-bind="{ title: first }" /><component :is="dynamicCard" v-bind="{ title: second }" />`
	locations, err := NewAdminReferenceProvider(idx).GetReferences(
		context.Background(),
		adminReferenceRequest(templatePath, source, "title", true),
	)
	require.NoError(t, err)
	require.Len(t, locations, 4)
	var templateOccurrences int
	var declarationURIs []string
	for _, location := range locations {
		if location.URI == uriutil.FileURI(templatePath) {
			templateOccurrences++
			continue
		}
		declarationURIs = append(declarationURIs, location.URI)
	}
	assert.Equal(t, 2, templateOccurrences)
	assert.ElementsMatch(t, []string{
		uriutil.FileURI(cardPath), uriutil.FileURI(panelPath),
	}, declarationURIs)
}

func TestAdminReferencesFromDeclarationIncludeIndexedInferredDynamicUsage(
	t *testing.T,
) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "component/sw-card/index.ts")
	templatePath := filepath.Join(
		adminRoot, "component/sw-host/sw-host.html.twig",
	)
	definitionSource := `export default { props: { title: String } };`
	templateSource := `<component :is="dynamicCard" :title="title" />`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		definitionPath, []byte(definitionSource),
	)))
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: definitionPath,
		DefinitionPath: definitionPath,
		Props: []admin.VueComponentProp{{
			Name: "title", FilePath: definitionPath,
		}},
	}))
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name:         "sw-host",
		FilePath:     filepath.Join(adminRoot, "component/sw-host/index.ts"),
		TemplatePath: templatePath,
		Members: []admin.VueComponentMember{{
			Name: "dynamicCard", Kind: admin.ComponentMemberComputed,
			ReturnExpressions: []string{"'sw-card'"},
			ReturnsComplete:   true,
		}},
	}))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		templatePath, []byte(templateSource),
	)))

	locations, err := NewAdminReferenceProvider(idx).GetReferences(
		context.Background(),
		adminReferenceRequest(
			definitionPath, definitionSource, "title", true,
		),
	)
	require.NoError(t, err)
	require.Len(t, locations, 2)
	assert.ElementsMatch(t, []string{
		uriutil.FileURI(definitionPath), uriutil.FileURI(templatePath),
	}, []string{locations[0].URI, locations[1].URI})
}

func TestAdminReferencesKeepLocalComponentAliasesTemplateScoped(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	firstDefinition := filepath.Join(adminRoot, "component/sw-first/index.ts")
	firstTemplate := filepath.Join(
		adminRoot, "component/sw-first/sw-first.html.twig",
	)
	secondDefinition := filepath.Join(adminRoot, "component/sw-second/index.ts")
	secondTemplate := filepath.Join(
		adminRoot, "component/sw-second/sw-second.html.twig",
	)
	for _, fixture := range []struct {
		definition string
		template   string
	}{
		{firstDefinition, firstTemplate},
		{secondDefinition, secondTemplate},
	} {
		source := `
import MtCard from '@shopware-ag/meteor-component-library/dist/esm/MtCard';
import template from './` + filepath.Base(fixture.template) + `';
export default Shopware.Component.wrapComponentConfig({
    template,
    components: { 'mt-card-original': MtCard },
});`
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
			fixture.definition, []byte(source),
		)))
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
			fixture.template,
			[]byte(`<mt-card-original @change="save"></mt-card-original>`),
		)))
	}
	meteorPath := filepath.Join(root, "meteor/MtCard.d.ts")
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "mt-card", FilePath: meteorPath, DefinitionPath: meteorPath,
		Events: []admin.VueComponentEvent{{
			Name: "change", FilePath: meteorPath, Line: 3,
		}},
	}))
	provider := NewAdminReferenceProvider(idx)
	requestSource := `<mt-card-original @change="save"></mt-card-original>`

	locations, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(
			firstTemplate, requestSource, "mt-card-original", false,
		),
	)
	require.NoError(t, err)
	require.Len(t, locations, 2)
	for _, location := range locations {
		assert.Equal(t, uriutil.FileURI(firstTemplate), location.URI)
	}

	locations, err = provider.GetReferences(
		context.Background(),
		adminReferenceRequest(
			firstTemplate, requestSource, "mt-card-original", true,
		),
	)
	require.NoError(t, err)
	require.Len(t, locations, 3)
	assert.Equal(t, uriutil.FileURI(firstDefinition), locations[0].URI)

	locations, err = provider.GetReferences(
		context.Background(),
		adminReferenceRequest(firstTemplate, requestSource, "change", false),
	)
	require.NoError(t, err)
	require.Len(t, locations, 2)
	assert.ElementsMatch(t, []string{
		uriutil.FileURI(firstTemplate), uriutil.FileURI(secondTemplate),
	}, []string{locations[0].URI, locations[1].URI})

	firstDefinitionSource := `
import MtCard from '@shopware-ag/meteor-component-library/dist/esm/MtCard';
import template from './sw-first.html.twig';
export default Shopware.Component.wrapComponentConfig({
    template,
    components: { 'mt-card-original': MtCard },
});`
	locations, err = provider.GetReferences(
		context.Background(),
		adminReferenceRequest(
			firstDefinition, firstDefinitionSource,
			"mt-card-original", false,
		),
	)
	require.NoError(t, err)
	require.Len(t, locations, 2)
	for _, location := range locations {
		assert.Equal(t, uriutil.FileURI(firstTemplate), location.URI)
	}
}

func TestAdminReferencesForInheritedComponentEventsAndSlots(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
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
	definitionSource := `export default {
	props: { title: String },
    emits: ['update:modelValue'],
	methods: { update(value) { this.title; this.$emit('update:modelValue', value); } },
};`
	eventDeclarationRange := admin.AdminSourceRange{
		StartLine: 2, StartCharacter: 13,
		EndLine: 2, EndCharacter: 30,
		Declaration: true,
	}
	slotDeclarationRange := admin.AdminSourceRange{
		StartLine: 0, StartCharacter: 17,
		EndLine: 0, EndCharacter: 23,
		Declaration: true,
	}
	templateSource := `<div><slot name="header" /></div>`
	consumerSource := `<sw-card :title="title" @update:model-value="update"><template #header /></sw-card>
<sw-child :title="title" @update:model-value="update"><template #header /></sw-child>`
	for path, source := range map[string]string{
		definitionPath: definitionSource,
		templatePath:   templateSource,
		consumerPath:   consumerSource,
	} {
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(path, []byte(source))))
	}
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-card", Kind: admin.ComponentRegister,
		FilePath: definitionPath, DefinitionPath: definitionPath,
		TemplatePath: templatePath,
		Props: []admin.VueComponentProp{{
			Name: "title", FilePath: definitionPath, Line: 2,
		}},
		Events: []admin.VueComponentEvent{{
			Name: "update:modelValue", FilePath: definitionPath, Line: 2,
			NameRange: eventDeclarationRange,
		}},
		Slots: []admin.VueComponentSlot{{
			Name: "header", FilePath: templatePath, Line: 1,
			NameRange: slotDeclarationRange,
		}},
	}))
	require.NoError(t, idx.SaveComponentDefinition(
		filepath.Dir(definitionPath),
		admin.ComponentDefinition{
			FilePath: definitionPath,
			Props: []admin.VueComponentProp{{
				Name: "title", FilePath: definitionPath, Line: 2,
			}},
			Emits: []string{"update:modelValue"},
			Events: []admin.VueComponentEvent{{
				Name: "update:modelValue", FilePath: definitionPath, Line: 2,
				NameRange: eventDeclarationRange,
			}},
			Slots: []admin.VueComponentSlot{{
				Name: "header", FilePath: templatePath, Line: 1,
				NameRange: slotDeclarationRange,
			}},
			TemplatePath: templatePath,
		},
	))
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-child", Kind: admin.ComponentExtend,
		ExtendsComponent: "sw-card", TargetComponent: "sw-card",
		FilePath: filepath.Join(adminRoot, "component/sw-child/index.ts"),
	}))
	provider := NewAdminReferenceProvider(idx)

	for _, test := range []struct {
		name, path, source, needle string
		includeDeclaration         bool
		want                       int
	}{
		{"event from markup", consumerPath, consumerSource, "update:model-value", false, 3},
		{"event with declaration", definitionPath, definitionSource, "update:modelValue", true, 4},
		{"prop from markup", consumerPath, consumerSource, ":title", false, 3},
		{"prop with declaration", definitionPath, definitionSource, "title:", true, 4},
		{"slot from markup", consumerPath, consumerSource, "header", false, 2},
		{"slot from declaration", templatePath, templateSource, "header", true, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			locations, referenceErr := provider.GetReferences(
				context.Background(),
				adminReferenceRequest(
					test.path,
					test.source,
					test.needle,
					test.includeDeclaration,
				),
			)
			require.NoError(t, referenceErr)
			assert.Len(t, locations, test.want)
			switch test.name {
			case "event with declaration":
				assert.Contains(t, locations, protocol.Location{
					URI: uriutil.FileURI(definitionPath),
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 13},
						End:   protocol.Position{Line: 2, Character: 30},
					},
				})
			case "slot from declaration":
				assert.Contains(t, locations, protocol.Location{
					URI: uriutil.FileURI(templatePath),
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 17},
						End:   protocol.Position{Line: 0, Character: 23},
					},
				})
			}
		})
	}
}

func TestAdminVueLexicalReferencesRespectScopes(t *testing.T) {
	idx, err := admin.NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	provider := NewAdminReferenceProvider(idx)
	path := filepath.Join(
		t.TempDir(), "Resources/app/administration/view.html.twig",
	)
	source := `<div v-for="item in items">
    {{ item.name }}
    <span v-for="item in item.children">{{ item.name }}</span>
    {{ item.id }}
</div>`
	withoutDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(path, source, "item.id", false),
	)
	require.NoError(t, err)
	assert.Len(t, withoutDeclaration, 3)
	withDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(path, source, "item.id", true),
	)
	require.NoError(t, err)
	assert.Len(t, withDeclaration, 4)
	assert.Equal(t, 12, withDeclaration[0].Range.Start.Character)

	eventSource := `<button @click="$emit('selected', $event); save($event)" />`
	eventReferences, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(path, eventSource, "$event)", true),
	)
	require.NoError(t, err)
	assert.Len(t, eventReferences, 2)

	memberSource := `<div v-for="item in items">{{ item.name }} {{ item.name }}<span v-for="item in children">{{ item.inner }}</span></div>`
	memberReferences, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(path, memberSource, "name", true),
	)
	require.NoError(t, err)
	assert.Len(t, memberReferences, 2)
	for _, location := range memberReferences {
		assert.Equal(t, len("name"), location.Range.End.Character-location.Range.Start.Character)
	}

	nestedMemberSource := `<div v-for="item in items">{{ item.child.name }} {{ item.child.name }}</div>`
	nestedMemberReferences, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(
			path, nestedMemberSource, "name", true,
		),
	)
	require.NoError(t, err)
	assert.Len(t, nestedMemberReferences, 2)
}

func TestAdminComponentInstanceMemberReferences(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "Resources/app/administration/src/component/sw-view",
	)
	definitionPath := filepath.Join(adminRoot, "index.ts")
	templatePath := filepath.Join(adminRoot, "view.html.twig")
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		definitionPath,
		[]byte("interface Manufacturer { name: string; }\n"+
			"interface Product { manufacturer: Manufacturer; getManufacturer(): Manufacturer; }"),
	)))
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-view", FilePath: definitionPath,
		DefinitionPath: definitionPath, TemplatePath: templatePath,
		Members: []admin.VueComponentMember{
			{
				Name: "page", Kind: admin.ComponentMemberProp,
				Type: "Product", FilePath: definitionPath,
			},
			{
				Name: "pages", Kind: admin.ComponentMemberProp,
				Type: "Product[]", FilePath: definitionPath,
			},
		},
	}))
	source := `<div v-for="page in pages">{{ page.manufacturer.name }}</div>
{{ page.manufacturer.name }} {{ page.manufacturer.name }}`
	provider := NewAdminReferenceProvider(idx)
	withoutDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(templatePath, source, "name }}", false),
	)
	require.NoError(t, err)
	assert.Len(t, withoutDeclaration, 2)
	withDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(templatePath, source, "name }}", true),
	)
	require.NoError(t, err)
	assert.Len(t, withDeclaration, 3)
	requireLocationURIForAdminReferenceTest(
		t, withDeclaration, definitionPath,
	)
	rootReferences, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(templatePath, source, "page.manufacturer", false),
	)
	require.NoError(t, err)
	assert.Len(t, rootReferences, 2)
	rootWithDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(templatePath, source, "page.manufacturer", true),
	)
	require.NoError(t, err)
	assert.Len(t, rootWithDeclaration, 3)
	requireLocationURIForAdminReferenceTest(
		t, rootWithDeclaration, definitionPath,
	)
	callSource := `{{ page.getManufacturer.name }} {{ page.getManufacturer().name }} {{ page.getManufacturer().name }}`
	calledReferences, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(templatePath, callSource, "name }}", false),
	)
	require.NoError(t, err)
	assert.Len(t, calledReferences, 2)
	calledWithDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(templatePath, callSource, "name }}", true),
	)
	require.NoError(t, err)
	assert.Len(t, calledWithDeclaration, 3)
	indexedSource := `{{ pages[1].manufacturer.name }} {{ pages[0].manufacturer.name }} {{ pages[0].manufacturer.name }}`
	indexedReferences, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(templatePath, indexedSource, "name }}", false),
	)
	require.NoError(t, err)
	assert.Len(t, indexedReferences, 2)
	indexedWithDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(templatePath, indexedSource, "name }}", true),
	)
	require.NoError(t, err)
	assert.Len(t, indexedWithDeclaration, 3)
	requireLocationURIForAdminReferenceTest(
		t, indexedWithDeclaration, definitionPath,
	)
}

func TestAdminWholeSlotObjectMemberReferences(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	declarationPath := filepath.Join(root, "meteor/SwInheritWrapper.d.ts")
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-inherit-wrapper", FilePath: declarationPath,
		Slots: []admin.VueComponentSlot{{
			Name: "content",
			Members: []admin.VueComponentSlotMember{{
				Name: "currentValue", FilePath: declarationPath, Line: 14,
				NameRange: admin.AdminSourceRange{
					StartLine: 13, StartCharacter: 6,
					EndLine: 13, EndCharacter: 18,
					Declaration: true, Identifier: true,
				},
			}},
		}},
	}))
	path := filepath.Join(
		root, "Resources/app/administration/view.html.twig",
	)
	source := `<sw-inherit-wrapper><template #content="props"><div v-for="props in rows">{{ props.currentValue }}</div>{{ props.currentValue }} {{ props.currentValue }}</template></sw-inherit-wrapper>`
	provider := NewAdminReferenceProvider(idx)
	withoutDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(path, source, "currentValue", false),
	)
	require.NoError(t, err)
	require.Len(t, withoutDeclaration, 2)
	withDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(path, source, "currentValue", true),
	)
	require.NoError(t, err)
	require.Len(t, withDeclaration, 3)
	requireLocationURIForAdminReferenceTest(t, withDeclaration, declarationPath)
	assert.Contains(t, withDeclaration, protocol.Location{
		URI: uriutil.FileURI(declarationPath),
		Range: protocol.Range{
			Start: protocol.Position{Line: 13, Character: 6},
			End:   protocol.Position{Line: 13, Character: 18},
		},
	})
}

func TestAdminReferencesResolveDynamicSlotFamilies(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(root, "src/Administration/Resources/app/administration/src")
	templatePath := filepath.Join(adminRoot, "component/sw-grid/grid.html.twig")
	consumerPath := filepath.Join(adminRoot, "consumer.html.twig")
	consumerSource := `<sw-grid><template #column-name /></sw-grid>
<sw-child><template #column-name /></sw-child>`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		consumerPath, []byte(consumerSource),
	)))
	family := admin.VueComponentSlot{
		NamePrefix: "column-", FilePath: templatePath, Line: 9,
	}
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-grid", FilePath: filepath.Join(adminRoot, "component/sw-grid/index.ts"),
		TemplatePath: templatePath, Slots: []admin.VueComponentSlot{family},
	}))
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-child", Kind: admin.ComponentExtend,
		ExtendsComponent: "sw-grid", TargetComponent: "sw-grid",
		FilePath: filepath.Join(adminRoot, "component/sw-child/index.ts"),
	}))
	provider := NewAdminReferenceProvider(idx)
	request := adminReferenceRequest(
		consumerPath, consumerSource, "column-name", false,
	)
	locations, err := provider.GetReferences(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, locations, 2)
	request.Context.IncludeDeclaration = true
	locations, err = provider.GetReferences(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, locations, 3)
	requireLocationURIForAdminReferenceTest(t, locations, templatePath)
}

func TestAdminReferencesResolveDynamicScopedSlotPayloadDeclarations(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
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
		require.NoError(t, idx.SaveComponent(component))
	}
	consumerPath := filepath.Join(adminRoot, "consumer.html.twig")
	source := `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="props">{{ props.item }} {{ props.item }}</template></component>`
	provider := NewAdminReferenceProvider(idx)
	withoutDeclarations, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(consumerPath, source, "item", false),
	)
	require.NoError(t, err)
	require.Len(t, withoutDeclarations, 2)

	withDeclarations, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(consumerPath, source, "item", true),
	)
	require.NoError(t, err)
	require.Len(t, withDeclarations, 4)
	requireLocationURIForAdminReferenceTest(t, withDeclarations, firstPath)
	requireLocationURIForAdminReferenceTest(t, withDeclarations, secondPath)

	localWithoutDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(consumerPath, source, `props"`, false),
	)
	require.NoError(t, err)
	require.Len(t, localWithoutDeclaration, 2)
	localWithDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(consumerPath, source, `props"`, true),
	)
	require.NoError(t, err)
	require.Len(t, localWithDeclaration, 3)
}

func TestAdminReferencesResolveSlotsBelowDynamicComponentOwner(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
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
		require.NoError(t, idx.SaveComponent(component))
	}
	consumerPath := filepath.Join(adminRoot, "consumer.html.twig")
	source := `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #header /></component>`
	provider := NewAdminReferenceProvider(idx)
	withoutDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(consumerPath, source, "header", false),
	)
	require.NoError(t, err)
	require.Len(t, withoutDeclaration, 1)
	assert.Equal(t, uriutil.FileURI(consumerPath), withoutDeclaration[0].URI)

	withDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(consumerPath, source, "header", true),
	)
	require.NoError(t, err)
	require.Len(t, withDeclaration, 3)
	requireLocationURIForAdminReferenceTest(t, withDeclaration, firstPath)
	requireLocationURIForAdminReferenceTest(t, withDeclaration, secondPath)
}

func TestAdminReferencesFollowInheritedComponentMembers(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	basePath := filepath.Join(adminRoot, "component/sw-base/index.ts")
	baseTemplate := filepath.Join(filepath.Dir(basePath), "sw-base.html.twig")
	childPath := filepath.Join(adminRoot, "component/sw-child/index.ts")
	childTemplate := filepath.Join(filepath.Dir(childPath), "sw-child.html.twig")
	baseSource := `import template from './sw-base.html.twig';
Component.register('sw-base', {
    template,
    methods: { save() { this.count += 1; } },
    data() {
        const count = 0;
        return { count };
    },
});`
	childSource := `import template from './sw-child.html.twig';
Component.extend('sw-child', 'sw-base', {
    template,
    methods: { touch() { return this.count; } },
});`
	baseTemplateSource := `{{ count }}`
	childTemplateSource := `<div v-for="count in rows">{{ count }}</div>{{ count }}`
	for path, source := range map[string]string{
		basePath:      baseSource,
		baseTemplate:  baseTemplateSource,
		childPath:     childSource,
		childTemplate: childTemplateSource,
	} {
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(path, []byte(source))))
	}
	provider := NewAdminReferenceProvider(idx)

	withoutDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(childTemplate, childTemplateSource, "count", false),
	)
	require.NoError(t, err)
	require.Len(t, withoutDeclaration, 4)
	assert.Equal(t, 1, countAdminReferenceLocations(
		withoutDeclaration, childTemplate,
	))

	withDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(childTemplate, childTemplateSource, "count", true),
	)
	require.NoError(t, err)
	require.Len(t, withDeclaration, 5)
	assert.Equal(t, 2, countAdminReferenceLocations(withDeclaration, basePath))

	fromDeclaration, err := provider.GetReferences(
		context.Background(),
		adminReferenceRequest(basePath, baseSource, "count", true),
	)
	require.NoError(t, err)
	require.Len(t, fromDeclaration, 5)
}

func countAdminReferenceLocations(
	locations []protocol.Location,
	path string,
) int {
	want := uriutil.FileURI(path)
	count := 0
	for _, location := range locations {
		if location.URI == want {
			count++
		}
	}
	return count
}

func requireLocationURIForAdminReferenceTest(
	t *testing.T,
	locations []protocol.Location,
	path string,
) {
	t.Helper()
	want := uriutil.FileURI(path)
	for _, location := range locations {
		if location.URI == want {
			return
		}
	}
	t.Fatalf("location %s not found in %#v", want, locations)
}

func adminReferenceRequest(
	path,
	source,
	needle string,
	includeDeclaration bool,
) *lsp.ReferenceRequest {
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.LastIndex(source, needle) + len(needle)/2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = includeDeclaration
	return &lsp.ReferenceRequest{
		ReferenceParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document: document, DocumentContent: document.Text,
			DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
			Root:  document.SyntaxTree.Root,
			Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
			Token: document.SyntaxTree.Root.TokenAtOffset(offset),
		},
	}
}
