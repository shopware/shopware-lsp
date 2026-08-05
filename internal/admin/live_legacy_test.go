package admin

import (
	"os"
	"path/filepath"
	"testing"

	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminIndexerComposesLiveLegacyComponentArtifacts(t *testing.T) {
	root := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "component/index.js")
	componentDir := filepath.Join(adminRoot, "component/sw-legacy-card")
	definitionPath := filepath.Join(componentDir, "index.js")
	templatePath := filepath.Join(componentDir, "sw-legacy-card.html.twig")
	require.NoError(t, os.MkdirAll(componentDir, 0o755))

	persistedTemplate := `<slot name="old-slot" :item="oldMember" />
{% block old_block %}{{ oldMember }}{% endblock %}`
	persistedDefinition := `import template from './sw-legacy-card.html.twig';
export default {
    template,
    props: {
        oldRequired: { type: String, required: true },
        oldOptional: Boolean,
    },
    emits: ['old-event'],
    data() { return { oldMember: 'old' }; },
};`
	persistedRegistration := `Shopware.Component.register(
    'sw-legacy-card',
    () => import('./sw-legacy-card'),
);`
	require.NoError(t, os.WriteFile(templatePath, []byte(persistedTemplate), 0o644))
	require.NoError(t, os.WriteFile(definitionPath, []byte(persistedDefinition), 0o644))
	require.NoError(t, os.WriteFile(registrationPath, []byte(persistedRegistration), 0o644))
	for path, source := range map[string]string{
		registrationPath: persistedRegistration,
		definitionPath:   persistedDefinition,
		templatePath:     persistedTemplate,
	} {
		require.NoError(t, index.Index(indexerpkg.NewParsedFile(
			path, []byte(source),
		)))
	}

	persisted, err := index.GetEffectiveComponent("sw-legacy-card")
	require.NoError(t, err)
	require.NotNil(t, persisted)
	_, found := persisted.ComponentProp("oldRequired")
	require.True(t, found)
	_, found = persisted.TemplateMember("oldMember")
	require.True(t, found)
	_, found = persisted.ComponentSlot("old-slot")
	require.True(t, found)
	_, found = persisted.ComponentBlock("old_block")
	require.True(t, found)
	persisted.Props[0].Name = "mutated-by-caller"
	cached, err := index.GetEffectiveComponent("sw-legacy-card")
	require.NoError(t, err)
	require.NotNil(t, cached)
	_, found = cached.ComponentProp("oldRequired")
	require.True(t, found, "cached contracts must be defensive copies")

	liveDefinition := `import template from './sw-legacy-card.html.twig';
export default {
    template,
    props: {
        liveRequired: { type: Number, required: true },
        liveOptional: String,
    },
    emits: ['live-event'],
    data() { return { liveMember: { id: 'draft' } }; },
};`
	liveDefinitionFile := indexerpkg.NewParsedFile(
		definitionPath, []byte(liveDefinition),
	)
	index.UpdateLiveDocument(
		definitionPath, liveDefinitionFile.SyntaxTree().Root,
		liveDefinition, liveDefinitionFile.LineIndex(),
	)
	live, err := index.GetEffectiveComponent("sw-legacy-card")
	require.NoError(t, err)
	require.NotNil(t, live)
	liveRequired, found := live.ComponentProp("liveRequired")
	require.True(t, found)
	assert.Equal(t, "Number", liveRequired.Type)
	assert.True(t, liveRequired.Required)
	_, found = live.ComponentProp("oldRequired")
	assert.False(t, found, "an open definition replaces its persisted API")
	_, found = live.TemplateMember("liveMember")
	require.True(t, found)
	_, found = live.TemplateMember("oldMember")
	assert.False(t, found)
	_, found = live.ComponentEvent("live-event")
	require.True(t, found)

	// Removing the export from an open definition shadows the persisted file;
	// stale props must not reappear while the buffer is incomplete.
	emptyDefinition := `export const draft = true;`
	emptyDefinitionFile := indexerpkg.NewParsedFile(
		definitionPath, []byte(emptyDefinition),
	)
	index.UpdateLiveDocument(
		definitionPath, emptyDefinitionFile.SyntaxTree().Root,
		emptyDefinition, emptyDefinitionFile.LineIndex(),
	)
	empty, err := index.GetEffectiveComponent("sw-legacy-card")
	require.NoError(t, err)
	require.NotNil(t, empty)
	assert.Empty(t, empty.Props)
	_, found = empty.ComponentProp("oldRequired")
	assert.False(t, found)
	index.UpdateLiveDocument(
		definitionPath, liveDefinitionFile.SyntaxTree().Root,
		liveDefinition, liveDefinitionFile.LineIndex(),
	)

	liveTemplate := `<slot name="live-slot" :record="liveMember" />
{% block live_block %}{{ liveMember.id }}{% endblock %}`
	liveTemplateFile := indexerpkg.NewParsedFile(
		templatePath, []byte(liveTemplate),
	)
	index.UpdateLiveDocument(
		templatePath, liveTemplateFile.SyntaxTree().Root,
		liveTemplate, liveTemplateFile.LineIndex(),
	)
	live, err = index.GetEffectiveComponent("sw-legacy-card")
	require.NoError(t, err)
	require.NotNil(t, live)
	_, found = live.ComponentSlot("live-slot")
	require.True(t, found)
	_, found = live.ComponentSlot("old-slot")
	assert.False(t, found, "an open template replaces persisted slots")
	_, found = live.ComponentBlock("live_block")
	require.True(t, found)
	_, found = live.ComponentBlock("old_block")
	assert.False(t, found, "an open template replaces persisted blocks")

	// A request-local Twig snapshot wins over the workspace overlay without
	// mutating it. This keeps direct provider tests and didChange ordering safe.
	requestTemplate := `<slot name="request-slot" :draft="liveMember" />`
	requestFile := indexerpkg.NewParsedFile(
		templatePath, []byte(requestTemplate),
	)
	requestComponent, err := index.GetComponentForDocument(
		templatePath, requestFile.SyntaxTree().Root, requestTemplate,
		requestFile.LineIndex(),
	)
	require.NoError(t, err)
	require.NotNil(t, requestComponent)
	_, found = requestComponent.ComponentSlot("request-slot")
	require.True(t, found)
	_, found = requestComponent.ComponentSlot("live-slot")
	assert.False(t, found)
	workspaceComponent, err := index.GetEffectiveComponent("sw-legacy-card")
	require.NoError(t, err)
	_, found = workspaceComponent.ComponentSlot("live-slot")
	require.True(t, found, "request-local parsing must not change the overlay")

	liveRegistration := `Shopware.Component.register(
    'sw-renamed-card',
    () => import('./sw-legacy-card'),
);`
	liveRegistrationFile := indexerpkg.NewParsedFile(
		registrationPath, []byte(liveRegistration),
	)
	index.UpdateLiveDocument(
		registrationPath, liveRegistrationFile.SyntaxTree().Root,
		liveRegistration, liveRegistrationFile.LineIndex(),
	)
	oldComponent, err := index.GetEffectiveComponent("sw-legacy-card")
	require.NoError(t, err)
	assert.Nil(t, oldComponent)
	renamed, err := index.GetEffectiveComponent("sw-renamed-card")
	require.NoError(t, err)
	require.NotNil(t, renamed)
	_, found = renamed.ComponentProp("liveRequired")
	require.True(t, found)
	_, found = renamed.ComponentSlot("live-slot")
	require.True(t, found)
	names, err := index.GetAllComponentNames()
	require.NoError(t, err)
	assert.Contains(t, names, "sw-renamed-card")
	assert.NotContains(t, names, "sw-legacy-card")

	index.RemoveLiveDocument(registrationPath)
	fallbackRegistration, err := index.GetEffectiveComponent("sw-legacy-card")
	require.NoError(t, err)
	require.NotNil(t, fallbackRegistration)
	_, found = fallbackRegistration.ComponentProp("liveRequired")
	require.True(t, found, "closing registration preserves other live buffers")

	index.RemoveLiveDocument(definitionPath)
	fallbackDefinition, err := index.GetEffectiveComponent("sw-legacy-card")
	require.NoError(t, err)
	require.NotNil(t, fallbackDefinition)
	_, found = fallbackDefinition.ComponentProp("oldRequired")
	require.True(t, found)
	_, found = fallbackDefinition.ComponentProp("liveRequired")
	assert.False(t, found)
	_, found = fallbackDefinition.ComponentSlot("live-slot")
	require.True(t, found, "closing definition preserves the live Twig buffer")

	index.RemoveLiveDocument(templatePath)
	fallbackTemplate, err := index.GetEffectiveComponent("sw-legacy-card")
	require.NoError(t, err)
	require.NotNil(t, fallbackTemplate)
	_, found = fallbackTemplate.ComponentSlot("old-slot")
	require.True(t, found)
	_, found = fallbackTemplate.ComponentSlot("live-slot")
	assert.False(t, found)
}

func TestAdminIndexerLiveInlineRegistrationCanAppearAndDisappear(t *testing.T) {
	root := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	path := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/component/live.js",
	)
	source := `Shopware.Component.register('sw-live-only', {
    props: { title: { type: String, required: true } },
    data() { return { visible: true }; },
});`
	file := indexerpkg.NewParsedFile(path, []byte(source))
	index.UpdateLiveDocument(
		path, file.SyntaxTree().Root, source, cst.NewLineIndex(source),
	)
	component, err := index.GetEffectiveComponent("sw-live-only")
	require.NoError(t, err)
	require.NotNil(t, component)
	_, found := component.ComponentProp("title")
	require.True(t, found)
	_, found = component.TemplateMember("visible")
	require.True(t, found)

	emptySource := `export const unrelated = true;`
	emptyFile := indexerpkg.NewParsedFile(path, []byte(emptySource))
	index.UpdateLiveDocument(
		path, emptyFile.SyntaxTree().Root, emptySource,
		emptyFile.LineIndex(),
	)
	component, err = index.GetEffectiveComponent("sw-live-only")
	require.NoError(t, err)
	assert.Nil(t, component)
}

func TestAdminIndexerRequestLocalTwigRetainsInheritedBlockScope(t *testing.T) {
	index, err := NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	parentTemplate := "/project/Resources/app/administration/src/parent.html.twig"
	childTemplate := "/project/Resources/app/administration/src/child.html.twig"
	require.NoError(t, index.SaveComponent(VueComponent{
		Name: "sw-parent", FilePath: "/project/parent.js",
		TemplatePath: parentTemplate,
		Blocks: []TwigBlock{{
			Name: "shared", FilePath: parentTemplate,
			ScopeMembers: []TwigBlockScopeMember{{
				Name: "set", Type: "CustomFieldSet", FilePath: parentTemplate,
			}},
		}},
	}))
	require.NoError(t, index.SaveComponent(VueComponent{
		Name: "sw-child", Kind: ComponentExtend,
		ExtendsComponent: "sw-parent", TargetComponent: "sw-parent",
		FilePath: "/project/child.js", TemplatePath: childTemplate,
		Blocks: []TwigBlock{{Name: "shared", FilePath: childTemplate}},
	}))
	source := `{% block shared %}{{ set.name }}{% endblock %}`
	file := indexerpkg.NewParsedFile(childTemplate, []byte(source))
	component, err := index.GetComponentForDocument(
		childTemplate, file.SyntaxTree().Root, source, file.LineIndex(),
	)
	require.NoError(t, err)
	require.NotNil(t, component)
	block, found := component.ComponentBlock("shared")
	require.True(t, found)
	set, found := block.ScopeMember("set")
	require.True(t, found)
	assert.Equal(t, parentTemplate, set.FilePath)
}

func TestAdminIndexerLiveRuntimeRegistriesReplacePersistedSourceFile(t *testing.T) {
	root := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registryPath := filepath.Join(adminRoot, "app/runtime.ts")

	persistedSource := `
Shopware.Application.addServiceProvider('old-service', factory);
Shopware.Store.register('old-store', {
    actions: { oldAction() {} },
});
Shopware.Mixin.register('shared-mixin', {
    data() { return { oldMixinMember: true }; },
});
Shopware.Directive.register('old-directive', {});
Shopware.Service('cmsService').registerCmsElement({
    name: 'old-element', component: 'sw-cms-el-old',
});
Shopware.Module.register('old-module', {
    routes: { index: { path: 'index', component: 'sw-old' } },
});
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'old', roles: { viewer: { privileges: ['old:read'] } },
});`
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registryPath, []byte(persistedSource),
	)))
	require.NoError(t, index.SaveComponent(VueComponent{
		Name: "sw-mixin-consumer", FilePath: filepath.Join(adminRoot, "consumer.js"),
		Mixins: []string{"shared-mixin"},
	}))
	persistedComponent, err := index.GetEffectiveComponent("sw-mixin-consumer")
	require.NoError(t, err)
	require.NotNil(t, persistedComponent)
	_, found := persistedComponent.TemplateMember("oldMixinMember")
	require.True(t, found)

	liveSource := `
Shopware.Application.addServiceProvider('live-service', factory);
Shopware.Store.register('live-store', {
    actions: { liveAction() {} },
});
Shopware.Mixin.register('shared-mixin', {
    data() { return { liveMixinMember: true }; },
});
Shopware.Directive.register('live-directive', {});
Shopware.Service('cmsService').registerCmsElement({
    name: 'live-element', component: 'sw-cms-el-live',
});
Shopware.Module.register('live-module', {
    routes: { detail: { path: 'detail', component: 'sw-live' } },
});
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'live', roles: { editor: { privileges: ['live:update'] } },
});`
	liveFile := indexerpkg.NewParsedFile(registryPath, []byte(liveSource))
	index.UpdateLiveDocument(
		registryPath, liveFile.SyntaxTree().Root, liveSource, liveFile.LineIndex(),
	)

	oldServices, err := index.GetService("old-service")
	require.NoError(t, err)
	assert.Empty(t, oldServices)
	liveServices, err := index.GetService("live-service")
	require.NoError(t, err)
	require.Len(t, liveServices, 1)
	assert.Equal(t, registryPath, liveServices[0].FilePath)

	oldStores, err := index.GetStore("old-store")
	require.NoError(t, err)
	assert.Empty(t, oldStores)
	liveStores, err := index.GetStore("live-store")
	require.NoError(t, err)
	require.Len(t, liveStores, 1)
	_, found = liveStores[0].Member("liveAction")
	assert.True(t, found)

	oldDirectives, err := index.GetDirective("old-directive")
	require.NoError(t, err)
	assert.Empty(t, oldDirectives)
	liveDirectives, err := index.GetDirective("live-directive")
	require.NoError(t, err)
	require.Len(t, liveDirectives, 1)

	oldCMS, err := index.GetCMSRegistration(AdminCMSElement, "old-element")
	require.NoError(t, err)
	assert.Empty(t, oldCMS)
	liveCMS, err := index.GetCMSRegistration(AdminCMSElement, "live-element")
	require.NoError(t, err)
	require.Len(t, liveCMS, 1)
	assert.Equal(t, "sw-cms-el-live", liveCMS[0].Component)

	oldModules, err := index.GetModule("old-module")
	require.NoError(t, err)
	assert.Empty(t, oldModules)
	liveModule, liveRoute, err := index.GetModuleRoute("live.module.detail")
	require.NoError(t, err)
	require.NotNil(t, liveModule)
	require.NotNil(t, liveRoute)
	assert.Equal(t, "live-module", liveModule.Name)
	assert.Equal(t, "sw-live", liveRoute.Component)

	oldPrivileges, err := index.GetPrivilege("old.viewer")
	require.NoError(t, err)
	assert.Empty(t, oldPrivileges)
	livePrivileges, err := index.GetPrivilege("live:update")
	require.NoError(t, err)
	require.Len(t, livePrivileges, 1)

	liveComponent, err := index.GetEffectiveComponent("sw-mixin-consumer")
	require.NoError(t, err)
	require.NotNil(t, liveComponent)
	_, found = liveComponent.TemplateMember("liveMixinMember")
	assert.True(t, found)
	_, found = liveComponent.TemplateMember("oldMixinMember")
	assert.False(t, found)

	// A syntactically valid but empty editor buffer replaces all registrations
	// from the source file until it is closed.
	emptySource := `export const draft = true;`
	emptyFile := indexerpkg.NewParsedFile(registryPath, []byte(emptySource))
	index.UpdateLiveDocument(
		registryPath, emptyFile.SyntaxTree().Root, emptySource, emptyFile.LineIndex(),
	)
	services, err := index.GetAllServices()
	require.NoError(t, err)
	assert.Empty(t, services)
	modules, err := index.GetAllModules()
	require.NoError(t, err)
	assert.Empty(t, modules)

	index.RemoveLiveDocument(registryPath)
	fallbackServices, err := index.GetService("old-service")
	require.NoError(t, err)
	require.Len(t, fallbackServices, 1)
	fallbackComponent, err := index.GetEffectiveComponent("sw-mixin-consumer")
	require.NoError(t, err)
	require.NotNil(t, fallbackComponent)
	_, found = fallbackComponent.TemplateMember("oldMixinMember")
	assert.True(t, found)
}

func TestAdminIndexerLiveStoreFactoryReplacesPersistedMembers(t *testing.T) {
	root := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	storePath := filepath.Join(adminRoot, "app/store/session.ts")
	factoryPath := filepath.Join(adminRoot, "app/composable/use-session.ts")
	require.NoError(t, os.MkdirAll(filepath.Dir(factoryPath), 0o755))
	require.NoError(t, os.WriteFile(factoryPath, nil, 0o644))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		storePath,
		[]byte(`import useSession from '../composable/use-session';
export default Shopware.Store.register('session', useSession);`),
	)))
	persistedFactory := `const oldMember = ref(null);
export default function useSession() { return { oldMember }; }`
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		factoryPath, []byte(persistedFactory),
	)))

	liveFactory := `const liveMember = ref(null);
function save() {}
export default function useSession() { return { liveMember, save }; }`
	liveFile := indexerpkg.NewParsedFile(factoryPath, []byte(liveFactory))
	index.UpdateLiveDocument(
		factoryPath, liveFile.SyntaxTree().Root, liveFactory, liveFile.LineIndex(),
	)
	stores, err := index.GetStore("session")
	require.NoError(t, err)
	require.Len(t, stores, 1)
	_, found := stores[0].Member("liveMember")
	assert.True(t, found)
	_, found = stores[0].Member("save")
	assert.True(t, found)
	_, found = stores[0].Member("oldMember")
	assert.False(t, found)

	emptyFactory := `export const draft = true;`
	emptyFile := indexerpkg.NewParsedFile(factoryPath, []byte(emptyFactory))
	index.UpdateLiveDocument(
		factoryPath, emptyFile.SyntaxTree().Root, emptyFactory, emptyFile.LineIndex(),
	)
	stores, err = index.GetStore("session")
	require.NoError(t, err)
	require.Len(t, stores, 1)
	_, found = stores[0].Member("oldMember")
	assert.False(t, found)

	index.RemoveLiveDocument(factoryPath)
	stores, err = index.GetStore("session")
	require.NoError(t, err)
	require.Len(t, stores, 1)
	_, found = stores[0].Member("oldMember")
	assert.True(t, found)
}

func TestAdminIndexerLiveVueRuntimeRegistryReplacesPersistedFile(t *testing.T) {
	root := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	path := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/component/runtime.vue",
	)
	persisted := `<script setup>
Shopware.Directive.register('vue-old', {});
</script>
<template><div /></template>`
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		path, []byte(persisted),
	)))
	old, err := index.GetDirective("vue-old")
	require.NoError(t, err)
	require.Len(t, old, 1)

	live := `<script setup>
Shopware.Directive.register('vue-live', {});
</script>
<template><div /></template>`
	file := indexerpkg.NewParsedFile(path, []byte(live))
	index.UpdateLiveDocument(
		path, file.SyntaxTree().Root, live, file.LineIndex(),
	)
	old, err = index.GetDirective("vue-old")
	require.NoError(t, err)
	assert.Empty(t, old)
	current, err := index.GetDirective("vue-live")
	require.NoError(t, err)
	require.Len(t, current, 1)

	index.RemoveLiveDocument(path)
	old, err = index.GetDirective("vue-old")
	require.NoError(t, err)
	require.Len(t, old, 1)
}
