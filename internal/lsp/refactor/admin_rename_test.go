package refactor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminRenameUpdatesComponentServiceStoreAndMixinUsages(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	mainPath := filepath.Join(adminRoot, "main.ts")
	definitionPath := filepath.Join(adminRoot, "sw-card/index.ts")
	templatePath := filepath.Join(adminRoot, "sw-card/sw-card.html.twig")
	mainSource := `
Shopware.Component.register('sw-card', definition);
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
Shopware.Filter.getByName('currency');`
	definitionSource := `export default {
    inject: ['acl'],
    methods: { check() { return this.acl.can('product.viewer'); } },
};`
	templateSource := `<sw-card v-tooltip.bottom="message"></sw-card>`
	for path, source := range map[string]string{
		mainPath: mainSource, definitionPath: definitionSource,
		templatePath: templateSource,
	} {
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(path, []byte(source))))
	}
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: mainPath, DefinitionPath: definitionPath,
		Injected: []string{"acl"},
	}))
	provider := NewAdminRenameProvider(idx)

	tests := []struct {
		name      string
		path      string
		source    string
		needle    string
		newName   string
		wantEdits int
		wantFiles int
	}{
		{"component", templatePath, templateSource, "sw-card", "acme-card", 4, 2},
		{"service", definitionPath, definitionSource, "this.acl", "accessControl", 4, 2},
		{"store", mainPath, `Shopware.Store.get('session')`, "session", "userSession", 3, 1},
		{"mixin", mainPath, `Shopware.Mixin.getByName('listing')`, "listing", "acme-listing", 2, 1},
		{"directive", templatePath, templateSource, "tooltip", "hint", 3, 2},
		{"filter", mainPath, `Shopware.Filter.getByName('currency')`, "currency", "money", 2, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			edit, renameErr := provider.Rename(
				context.Background(),
				adminRenameRequest(
					test.path, test.source, test.needle, test.newName,
				),
			)
			require.NoError(t, renameErr)
			require.NotNil(t, edit)
			assert.Len(t, edit.Changes, test.wantFiles)
			count := 0
			for _, edits := range edit.Changes {
				count += len(edits)
				for _, textEdit := range edits {
					assert.Equal(t, test.newName, textEdit.NewText)
				}
			}
			assert.Equal(t, test.wantEdits, count)
		})
	}

	componentEdit, err := provider.Rename(
		context.Background(),
		adminRenameRequest(
			templatePath, templateSource, "sw-card", "acme-card",
		),
	)
	require.NoError(t, err)
	assert.Equal(
		t, `<acme-card v-tooltip.bottom="message"></acme-card>`,
		applyAdminRenameEdits(templateSource, componentEdit.Changes[uriutil.FileURI(templatePath)]),
	)
	assert.Contains(
		t,
		applyAdminRenameEdits(mainSource, componentEdit.Changes[uriutil.FileURI(mainPath)]),
		`Component.register('acme-card'`,
	)
}

func TestAdminRenameUpdatesTypedShopwareEventBusDeclarationAndUsages(
	t *testing.T,
) {
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
    telemetry: { name: string };
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
	provider := NewAdminRenameProvider(idx)
	assertRename := func(path, source string) {
		t.Helper()
		edit, renameErr := provider.Rename(
			context.Background(),
			adminRenameRequest(
				path, source, "save-event", "persisted-event",
			),
		)
		require.NoError(t, renameErr)
		require.NotNil(t, edit)
		assert.Len(t, edit.Changes, 3)
		assert.Equal(t, 4, adminRenameEditCount(edit))
		assert.Contains(t, applyAdminRenameEdits(
			eventBusSource,
			edit.Changes[uriutil.FileURI(eventBusPath)],
		), "'persisted-event': { id: string }")
		assert.Contains(t, applyAdminRenameEdits(
			firstSource,
			edit.Changes[uriutil.FileURI(firstPath)],
		), "EventBus.on('persisted-event'")
		assert.Equal(t, 2, strings.Count(applyAdminRenameEdits(
			secondSource,
			edit.Changes[uriutil.FileURI(secondPath)],
		), "persisted-event"))
	}

	t.Run("from usage", func(t *testing.T) {
		assertRename(firstPath, firstSource)
	})
	t.Run("from typed declaration", func(t *testing.T) {
		assertRename(eventBusPath, eventBusSource)
	})
	t.Run("unquoted declaration without usages", func(t *testing.T) {
		edit, renameErr := provider.Rename(
			context.Background(),
			adminRenameRequest(
				eventBusPath, eventBusSource, "telemetry", "analytics",
			),
		)
		require.NoError(t, renameErr)
		require.NotNil(t, edit)
		assert.Equal(t, 1, adminRenameEditCount(edit))
		assert.Contains(t, applyAdminRenameEdits(
			eventBusSource,
			edit.Changes[uriutil.FileURI(eventBusPath)],
		), "analytics: { name: string }")
	})
	t.Run("rejects conflict", func(t *testing.T) {
		_, renameErr := provider.Rename(
			context.Background(),
			adminRenameRequest(
				firstPath, firstSource, "save-event", "telemetry",
			),
		)
		require.ErrorContains(t, renameErr, "already exists")
	})
	t.Run("rejects invalid name", func(t *testing.T) {
		_, renameErr := provider.Rename(
			context.Background(),
			adminRenameRequest(
				firstPath, firstSource, "save-event", "invalid event",
			),
		)
		require.ErrorContains(t, renameErr, "valid Shopware EventBus event name")
	})
}

func TestAdminRenameUpdatesApplicationServiceContainerMembers(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "services.ts")
	consumerPath := filepath.Join(adminRoot, "consumer.ts")
	registrationSource := `Shopware.Application.addServiceProvider('acl', factory);`
	consumerSource := `
Shopware.Application.getContainer('service').acl;
function run() {
    const services = Application.getContainer('service');
    return services.acl;
}`
	for path, source := range map[string]string{
		registrationPath: registrationSource,
		consumerPath:     consumerSource,
	} {
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(path, []byte(source))))
	}
	edit, err := NewAdminRenameProvider(idx).Rename(
		context.Background(),
		adminRenameRequest(
			consumerPath, consumerSource, "acl", "accessControl",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, edit)
	assert.Len(t, edit.Changes, 2)
	assert.Len(t, edit.Changes[uriutil.FileURI(registrationPath)], 1)
	assert.Len(t, edit.Changes[uriutil.FileURI(consumerPath)], 2)
	assert.Contains(t, applyAdminRenameEdits(
		registrationSource,
		edit.Changes[uriutil.FileURI(registrationPath)],
	), "addServiceProvider('accessControl'")
	renamedConsumer := applyAdminRenameEdits(
		consumerSource, edit.Changes[uriutil.FileURI(consumerPath)],
	)
	assert.Contains(t, renamedConsumer, ").accessControl")
	assert.Contains(t, renamedConsumer, "services.accessControl")
}

func TestAdminRenameScopesLocalDirectiveToOwningComponent(t *testing.T) {
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
	edit, err := NewAdminRenameProvider(idx).Rename(
		context.Background(),
		adminRenameRequest(
			ownerTemplate, templateSource, "hide", "conceal",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, edit)
	require.Len(t, edit.Changes, 2)
	assert.Contains(t, edit.Changes, uriutil.FileURI(definitionPath))
	assert.Contains(t, edit.Changes, uriutil.FileURI(ownerTemplate))
	assert.NotContains(t, edit.Changes, uriutil.FileURI(globalPath))
	assert.NotContains(t, edit.Changes, uriutil.FileURI(otherTemplate))
	assert.Equal(
		t, `<div v-conceal="hidden"></div>`,
		applyAdminRenameEdits(
			templateSource, edit.Changes[uriutil.FileURI(ownerTemplate)],
		),
	)
	assert.Equal(
		t, `export default { directives: { conceal: {} } };`,
		applyAdminRenameEdits(
			definitionSource, edit.Changes[uriutil.FileURI(definitionPath)],
		),
	)
}

func TestAdminRenameUpdatesCMSRegistryReferences(t *testing.T) {
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
	provider := NewAdminRenameProvider(idx)
	elementEdit, err := provider.Rename(
		context.Background(),
		adminRenameRequest(
			consumerPath, consumerSource, "hero');", "banner",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, elementEdit)
	assert.Len(t, elementEdit.Changes, 2)
	assert.Equal(t, 3, adminRenameEditCount(elementEdit))
	assert.Contains(t, applyAdminRenameEdits(
		registrationSource,
		elementEdit.Changes[uriutil.FileURI(registrationPath)],
	), `name: 'banner'`)
	assert.Contains(t, applyAdminRenameEdits(
		registrationSource,
		elementEdit.Changes[uriutil.FileURI(registrationPath)],
	), `type: 'banner'`)

	blockEdit, err := provider.Rename(
		context.Background(),
		adminRenameRequest(
			consumerPath, consumerSource, "hero-grid", "banner-grid",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, blockEdit)
	assert.Equal(t, 2, adminRenameEditCount(blockEdit))

	componentEdit, err := provider.Rename(
		context.Background(),
		adminRenameRequest(
			registrationPath, registrationSource,
			"sw-cms-el-hero", "acme-cms-el-hero",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, componentEdit)
	assert.Equal(t, 2, adminRenameEditCount(componentEdit))
	assert.Equal(t, 2, strings.Count(applyAdminRenameEdits(
		registrationSource,
		componentEdit.Changes[uriutil.FileURI(registrationPath)],
	), "acme-cms-el-hero"))

	_, err = provider.Rename(
		context.Background(),
		adminRenameRequest(
			consumerPath, consumerSource, "hero-grid", "banner grid",
		),
	)
	require.ErrorContains(t, err, "valid Shopware CMS registry name")
}

func TestAdminRenameRejectsConflictsExternalAndUnsafeNames(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	mainPath := filepath.Join(adminRoot, "main.ts")
	mainSource := `
Shopware.Component.register('sw-card', definition);
Shopware.Component.register('sw-existing', definition);
Shopware.Application.addServiceProvider('acl', factory);
export default { inject: ['acl'], methods: { run() { return this.acl; } } };`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(mainPath, []byte(mainSource))))
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: mainPath, DefinitionPath: mainPath,
		Injected: []string{"acl"},
	}))
	existingPath := filepath.Join(adminRoot, "existing.ts")
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		existingPath,
		[]byte(`Shopware.Component.register('sw-existing', definition);`),
	)))
	runtimeCMSPath := filepath.Join(adminRoot, "runtime-cms.ts")
	runtimeCMSSource := `cmsService.getCmsElementConfigByName('runtime-element');`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		runtimeCMSPath, []byte(runtimeCMSSource),
	)))
	provider := NewAdminRenameProvider(idx)

	_, err = provider.Rename(
		context.Background(),
		adminRenameRequest(mainPath, mainSource, "sw-card", "sw-existing"),
	)
	require.ErrorContains(t, err, "already exists")
	_, err = provider.Rename(
		context.Background(),
		adminRenameRequest(mainPath, mainSource, "this.acl", "access-control"),
	)
	require.ErrorContains(t, err, "JavaScript identifier")
	_, err = provider.Rename(
		context.Background(),
		adminRenameRequest(
			runtimeCMSPath, runtimeCMSSource,
			"runtime-element", "renamed-element",
		),
	)
	require.ErrorContains(t, err, "declaration source is not indexed")

	meteorPath := filepath.Join(
		root, "src/Administration/Resources/app/administration/node_modules",
		"@shopware-ag/meteor-component-library/dist/esm/MtButton.d.ts",
	)
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "mt-button", FilePath: meteorPath, DefinitionPath: meteorPath,
	}))
	meteorTemplatePath := filepath.Join(adminRoot, "meteor.html.twig")
	meteorSource := `<mt-button />`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		meteorTemplatePath, []byte(meteorSource),
	)))
	_, err = provider.Rename(
		context.Background(),
		adminRenameRequest(
			meteorTemplatePath, meteorSource, "mt-button", "acme-button",
		),
	)
	require.ErrorContains(t, err, "external Meteor component")

	privilegeSource := `acl.can('product.viewer')`
	edit, err := provider.Rename(
		context.Background(),
		adminRenameRequest(
			mainPath, privilegeSource, "product.viewer", "product.reader",
		),
	)
	require.NoError(t, err)
	assert.Nil(t, edit)
}

func TestAdminRenameUpdatesTemplateScopedComponentAlias(t *testing.T) {
	tests := []struct {
		name                string
		componentsProperty  string
		oldName             string
		declarationNeedle   string
		newName             string
		expectedDeclaration string
	}{
		{
			name:                "quoted alias",
			componentsProperty:  "'mt-card-original': MtCard",
			oldName:             "mt-card-original",
			declarationNeedle:   "mt-card-original",
			newName:             "plugin-card",
			expectedDeclaration: "'plugin-card': MtCard",
		},
		{
			name:                "shorthand alias",
			componentsProperty:  "MtCard",
			oldName:             "mt-card",
			declarationNeedle:   "MtCard",
			newName:             "plugin-card",
			expectedDeclaration: "'plugin-card': MtCard",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, idx.Close()) })
			adminRoot := filepath.Join(
				root, "src/Administration/Resources/app/administration/src",
			)
			definitionPath := filepath.Join(
				adminRoot, "component/sw-wrapper/index.ts",
			)
			templatePath := filepath.Join(
				adminRoot, "component/sw-wrapper/sw-wrapper.html.twig",
			)
			definitionSource := `
import MtCard from '@shopware-ag/meteor-component-library/dist/esm/MtCard';
import template from './sw-wrapper.html.twig';
export default Shopware.Component.wrapComponentConfig({
    template,
    components: { ` + test.componentsProperty + ` },
});`
			templateSource := "<" + test.oldName + "></" + test.oldName + ">"
			require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
				definitionPath, []byte(definitionSource),
			)))
			require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
				templatePath, []byte(templateSource),
			)))

			edit, err := NewAdminRenameProvider(idx).Rename(
				context.Background(),
				adminRenameRequest(
					templatePath, templateSource, test.oldName, test.newName,
				),
			)
			require.NoError(t, err)
			require.NotNil(t, edit)
			require.Len(t, edit.Changes, 2)
			assert.Equal(
				t,
				"<"+test.newName+"></"+test.newName+">",
				applyAdminRenameEdits(
					templateSource,
					edit.Changes[uriutil.FileURI(templatePath)],
				),
			)
			assert.Contains(
				t,
				applyAdminRenameEdits(
					definitionSource,
					edit.Changes[uriutil.FileURI(definitionPath)],
				),
				test.expectedDeclaration,
			)

			edit, err = NewAdminRenameProvider(idx).Rename(
				context.Background(),
				adminRenameRequest(
					definitionPath, definitionSource,
					test.declarationNeedle, test.newName,
				),
			)
			require.NoError(t, err)
			require.NotNil(t, edit)
			require.Len(t, edit.Changes, 2)
			assert.Equal(
				t,
				"<"+test.newName+"></"+test.newName+">",
				applyAdminRenameEdits(
					templateSource,
					edit.Changes[uriutil.FileURI(templatePath)],
				),
			)
		})
	}
}

func TestAdminRenameEventIncludesTemplateScopedAliases(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	targetPath := filepath.Join(adminRoot, "component/mt-card/index.ts")
	wrapperPath := filepath.Join(adminRoot, "component/sw-wrapper/index.ts")
	localTemplatePath := filepath.Join(
		adminRoot, "component/sw-wrapper/sw-wrapper.html.twig",
	)
	globalTemplatePath := filepath.Join(adminRoot, "global.html.twig")
	targetSource := `Shopware.Component.register('mt-card', { emits: ['change'] });`
	wrapperSource := `
import MtCard from '../mt-card';
import template from './sw-wrapper.html.twig';
export default Shopware.Component.wrapComponentConfig({
    template,
    components: { 'mt-card-original': MtCard },
});`
	localTemplateSource := `<mt-card-original @change="save" />`
	globalTemplateSource := `<mt-card @change="save" />`
	for path, source := range map[string]string{
		targetPath: targetSource, wrapperPath: wrapperSource,
		localTemplatePath:  localTemplateSource,
		globalTemplatePath: globalTemplateSource,
	} {
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
			path, []byte(source),
		)))
	}

	edit, err := NewAdminRenameProvider(idx).Rename(
		context.Background(),
		adminRenameRequest(
			localTemplatePath, localTemplateSource, "change", "changed",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, edit)
	require.Len(t, edit.Changes, 3)
	assert.Contains(t, applyAdminRenameEdits(
		targetSource, edit.Changes[uriutil.FileURI(targetPath)],
	), "emits: ['changed']")
	assert.Contains(t, applyAdminRenameEdits(
		localTemplateSource,
		edit.Changes[uriutil.FileURI(localTemplatePath)],
	), `@changed=`)
	assert.Contains(t, applyAdminRenameEdits(
		globalTemplateSource,
		edit.Changes[uriutil.FileURI(globalTemplatePath)],
	), `@changed=`)
}

func TestAdminRenameUpdatesInheritedComponentEventsAndSlots(t *testing.T) {
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
	props: { positionIdentifier: String, existingProp: Boolean },
    emits: ['update:modelValue', 'save-complete'],
	methods: { update(value) { this.positionIdentifier; this.$emit('update:modelValue', value); } },
};`
	templateSource := `<div><slot name="header" /><slot name="footer" /></div>`
	consumerSource := `<sw-card :position-identifier="position" @update:model-value="update"><template #header /></sw-card>
<sw-child :position-identifier="position" @update:model-value="update"><template #header /></sw-child>`
	for path, source := range map[string]string{
		definitionPath: definitionSource,
		templatePath:   templateSource,
		consumerPath:   consumerSource,
	} {
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(path, []byte(source))))
	}
	events := []admin.VueComponentEvent{
		{Name: "update:modelValue", FilePath: definitionPath, Line: 2},
		{Name: "save-complete", FilePath: definitionPath, Line: 2},
	}
	slots := []admin.VueComponentSlot{
		{Name: "header", FilePath: templatePath, Line: 1},
		{Name: "footer", FilePath: templatePath, Line: 1},
	}
	props := []admin.VueComponentProp{
		{Name: "positionIdentifier", FilePath: definitionPath, Line: 2},
		{Name: "existingProp", FilePath: definitionPath, Line: 2},
	}
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-card", Kind: admin.ComponentRegister,
		FilePath: definitionPath, DefinitionPath: definitionPath,
		TemplatePath: templatePath, Props: props, Events: events, Slots: slots,
	}))
	require.NoError(t, idx.SaveComponentDefinition(
		filepath.Dir(definitionPath),
		admin.ComponentDefinition{
			FilePath: definitionPath, TemplatePath: templatePath,
			Props: props, Emits: []string{"update:modelValue", "save-complete"},
			Events: events, Slots: slots,
		},
	))
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-child", Kind: admin.ComponentExtend,
		ExtendsComponent: "sw-card", TargetComponent: "sw-card",
		FilePath: filepath.Join(adminRoot, "component/sw-child/index.ts"),
	}))
	provider := NewAdminRenameProvider(idx)

	propEdit, err := provider.Rename(
		context.Background(),
		adminRenameRequest(
			consumerPath,
			consumerSource,
			"position-identifier",
			"product-id",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, propEdit)
	require.Len(t, propEdit.Changes, 2)
	assert.Len(t, propEdit.Changes[uriutil.FileURI(definitionPath)], 2)
	assert.Len(t, propEdit.Changes[uriutil.FileURI(consumerPath)], 2)
	renamedPropDefinition := applyAdminRenameEdits(
		definitionSource,
		propEdit.Changes[uriutil.FileURI(definitionPath)],
	)
	assert.Contains(t, renamedPropDefinition, `props: { productId:`)
	assert.Contains(t, renamedPropDefinition, `this.productId`)
	renamedPropConsumer := applyAdminRenameEdits(
		consumerSource,
		propEdit.Changes[uriutil.FileURI(consumerPath)],
	)
	assert.Equal(t, 2, strings.Count(renamedPropConsumer, ":product-id"))

	eventEdit, err := provider.Rename(
		context.Background(),
		adminRenameRequest(
			consumerPath,
			consumerSource,
			"update:model-value",
			"update:currentValue",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, eventEdit)
	require.Len(t, eventEdit.Changes, 2)
	assert.Len(t, eventEdit.Changes[uriutil.FileURI(definitionPath)], 2)
	assert.Len(t, eventEdit.Changes[uriutil.FileURI(consumerPath)], 2)
	renamedDefinition := applyAdminRenameEdits(
		definitionSource,
		eventEdit.Changes[uriutil.FileURI(definitionPath)],
	)
	assert.Contains(t, renamedDefinition, `emits: ['update:currentValue'`)
	assert.Contains(t, renamedDefinition, `$emit('update:currentValue'`)
	renamedConsumer := applyAdminRenameEdits(
		consumerSource,
		eventEdit.Changes[uriutil.FileURI(consumerPath)],
	)
	assert.Equal(t, 2, strings.Count(renamedConsumer, "@update:current-value"))
	assert.NotContains(t, renamedConsumer, "@update:model-value")

	slotEdit, err := provider.Rename(
		context.Background(),
		adminRenameRequest(
			consumerPath,
			consumerSource,
			"header",
			"toolbar",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, slotEdit)
	require.Len(t, slotEdit.Changes, 2)
	assert.Len(t, slotEdit.Changes[uriutil.FileURI(templatePath)], 1)
	assert.Len(t, slotEdit.Changes[uriutil.FileURI(consumerPath)], 2)
	assert.Contains(t, applyAdminRenameEdits(
		templateSource,
		slotEdit.Changes[uriutil.FileURI(templatePath)],
	), `slot name="toolbar"`)
	assert.Equal(t, 2, strings.Count(applyAdminRenameEdits(
		consumerSource,
		slotEdit.Changes[uriutil.FileURI(consumerPath)],
	), "#toolbar"))

	_, err = provider.Rename(
		context.Background(),
		adminRenameRequest(
			consumerPath,
			consumerSource,
			"update:model-value",
			"save-complete",
		),
	)
	require.ErrorContains(t, err, "already exists")
	_, err = provider.Rename(
		context.Background(),
		adminRenameRequest(
			consumerPath,
			consumerSource,
			"position-identifier",
			"existing-prop",
		),
	)
	require.ErrorContains(t, err, "already exists")
	_, err = provider.Rename(
		context.Background(),
		adminRenameRequest(
			consumerPath, consumerSource, "header", "footer",
		),
	)
	require.ErrorContains(t, err, "already exists")
}

func TestAdminEventRenameQuotesObjectKeysAndRejectsExternalMeteor(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "component/sw-card/index.ts")
	consumerPath := filepath.Join(adminRoot, "consumer.html.twig")
	definitionSource := `export default { emits: { saved: null } };`
	consumerSource := `<sw-card @saved="onSaved" />`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		definitionPath, []byte(definitionSource),
	)))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		consumerPath, []byte(consumerSource),
	)))
	event := admin.VueComponentEvent{
		Name: "saved", FilePath: definitionPath, Line: 1,
	}
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: definitionPath,
		DefinitionPath: definitionPath, Events: []admin.VueComponentEvent{event},
	}))
	require.NoError(t, idx.SaveComponentDefinition(
		filepath.Dir(definitionPath),
		admin.ComponentDefinition{
			FilePath: definitionPath,
			Emits:    []string{"saved"},
			Events:   []admin.VueComponentEvent{event},
		},
	))
	provider := NewAdminRenameProvider(idx)
	edit, err := provider.Rename(
		context.Background(),
		adminRenameRequest(
			consumerPath, consumerSource, "saved", "save-complete",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, edit)
	assert.Equal(
		t,
		`export default { emits: { "save-complete": null } };`,
		applyAdminRenameEdits(
			definitionSource,
			edit.Changes[uriutil.FileURI(definitionPath)],
		),
	)

	meteorPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/node_modules",
		"@shopware-ag/meteor-component-library/dist/esm/MtButton.d.ts",
	)
	meteorSource := `<mt-button @click="onClick"><template #iconFront /></mt-button>`
	meteorUsagePath := filepath.Join(adminRoot, "meteor.html.twig")
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "mt-button", FilePath: meteorPath, DefinitionPath: meteorPath,
		Events: []admin.VueComponentEvent{{
			Name: "click", FilePath: meteorPath, Line: 3,
		}},
		Slots: []admin.VueComponentSlot{{
			Name: "iconFront", FilePath: meteorPath, Line: 4,
		}},
	}))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		meteorUsagePath, []byte(meteorSource),
	)))
	for _, needle := range []string{"click", "iconFront"} {
		_, renameErr := provider.Rename(
			context.Background(),
			adminRenameRequest(
				meteorUsagePath, meteorSource, needle, "renamed",
			),
		)
		require.ErrorContains(t, renameErr, "external Meteor")
	}
}

func TestAdminRenameVueForLocalRespectsShadowing(t *testing.T) {
	idx, err := admin.NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	provider := NewAdminRenameProvider(idx)
	path := filepath.Join(
		t.TempDir(), "Resources/app/administration/view.html.twig",
	)
	source := `<div v-for="(item, index) in items">
    {{ item.name }}
    <span v-for="item in item.children">{{ item.name }}</span>
    {{ item.id }}
</div>`
	edit, err := provider.Rename(
		context.Background(),
		adminRenameRequest(path, source, "item.id", "row"),
	)
	require.NoError(t, err)
	require.NotNil(t, edit)
	renameEdits := edit.Changes[uriutil.FileURI(path)]
	require.Len(t, renameEdits, 4)
	assert.Equal(t, `<div v-for="(row, index) in items">
    {{ row.name }}
    <span v-for="item in row.children">{{ item.name }}</span>
    {{ row.id }}
</div>`, applyAdminRenameEdits(source, renameEdits))

	_, err = provider.Rename(
		context.Background(),
		adminRenameRequest(path, source, "item.id", "index"),
	)
	require.ErrorContains(t, err, "already exists in this v-for scope")

	eventSource := `<button @click="save($event)" />`
	_, err = provider.Rename(
		context.Background(),
		adminRenameRequest(path, eventSource, "$event", "event"),
	)
	require.ErrorContains(t, err, "implicit $event")
}

func TestAdminRenameRejectsDynamicSlotFamilyConsumer(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(root, "src/Administration/Resources/app/administration/src")
	templatePath := filepath.Join(adminRoot, "component/sw-grid/grid.html.twig")
	consumerPath := filepath.Join(adminRoot, "consumer.html.twig")
	consumerSource := `<sw-grid><template #column-name /></sw-grid>`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		consumerPath, []byte(consumerSource),
	)))
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-grid", FilePath: filepath.Join(adminRoot, "component/sw-grid/index.ts"),
		TemplatePath: templatePath,
		Slots: []admin.VueComponentSlot{{
			NamePrefix: "column-", FilePath: templatePath, Line: 9,
		}},
	}))
	_, err = NewAdminRenameProvider(idx).Rename(
		context.Background(),
		adminRenameRequest(
			consumerPath, consumerSource, "column-name", "column-title",
		),
	)
	require.ErrorContains(t, err, "dynamic slot family")
}

func TestAdminRenameResolvesSafeDynamicSlotOwner(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	sharedPath := filepath.Join(adminRoot, "base/card.html.twig")
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-card-a", FilePath: filepath.Join(adminRoot, "a/index.ts"),
			TemplatePath: sharedPath,
			Slots:        []admin.VueComponentSlot{{Name: "header", FilePath: sharedPath, Line: 1}},
		},
		{
			Name: "sw-card-b", FilePath: filepath.Join(adminRoot, "b/index.ts"),
			TemplatePath: filepath.Join(adminRoot, "b/card.html.twig"),
			Slots:        []admin.VueComponentSlot{{Name: "header", FilePath: sharedPath, Line: 1}},
		},
	} {
		require.NoError(t, idx.SaveComponent(component))
	}
	declarationSource := `<slot name="header" />`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		sharedPath, []byte(declarationSource),
	)))
	consumerPath := filepath.Join(adminRoot, "consumer.html.twig")
	consumerSource := `<component :is="active ? 'sw-card-a' : 'sw-card-b'" #header />
<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #header /></component>`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		consumerPath, []byte(consumerSource),
	)))
	edit, err := NewAdminRenameProvider(idx).Rename(
		context.Background(),
		adminRenameRequest(consumerPath, consumerSource, "header", "toolbar"),
	)
	require.NoError(t, err)
	require.NotNil(t, edit)
	assert.Equal(t, `<slot name="toolbar" />`, applyAdminRenameEdits(
		declarationSource, edit.Changes[uriutil.FileURI(sharedPath)],
	))
	renamedConsumer := applyAdminRenameEdits(
		consumerSource, edit.Changes[uriutil.FileURI(consumerPath)],
	)
	assert.Equal(t, 2, strings.Count(renamedConsumer, "#toolbar"))
	assert.NotContains(t, renamedConsumer, "#header")

	firstPath := filepath.Join(adminRoot, "c/card.html.twig")
	secondPath := filepath.Join(adminRoot, "d/card.html.twig")
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-card-c", FilePath: filepath.Join(adminRoot, "c/index.ts"),
			TemplatePath: firstPath,
			Slots:        []admin.VueComponentSlot{{Name: "header", FilePath: firstPath, Line: 1}},
		},
		{
			Name: "sw-card-d", FilePath: filepath.Join(adminRoot, "d/index.ts"),
			TemplatePath: secondPath,
			Slots:        []admin.VueComponentSlot{{Name: "header", FilePath: secondPath, Line: 1}},
		},
	} {
		require.NoError(t, idx.SaveComponent(component))
	}
	ambiguousSource := `<component :is="active ? 'sw-card-c' : 'sw-card-d'"><template #header /></component>`
	_, err = NewAdminRenameProvider(idx).Rename(
		context.Background(),
		adminRenameRequest(
			consumerPath, ambiguousSource, "header", "toolbar",
		),
	)
	require.ErrorContains(t, err, "distinct declarations")
}

func TestAdminRenameFromDeclarationUpdatesIndexedInferredDynamicUsages(
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
	componentTemplatePath := filepath.Join(
		adminRoot, "component/sw-card/sw-card.html.twig",
	)
	consumerPath := filepath.Join(
		adminRoot, "component/sw-host/sw-host.html.twig",
	)
	definitionSource := `export default {
    props: { title: String },
    emits: ['saved'],
};`
	componentTemplateSource := `<slot name="header"></slot>`
	consumerSource := `<component :is="dynamicCard" v-bind="{ title }" @saved="save"><template #header /></component>`
	for path, source := range map[string]string{
		definitionPath:        definitionSource,
		componentTemplatePath: componentTemplateSource,
		consumerPath:          consumerSource,
	} {
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
			path, []byte(source),
		)))
	}
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: definitionPath,
		DefinitionPath: definitionPath,
		TemplatePath:   componentTemplatePath,
		Props: []admin.VueComponentProp{{
			Name: "title", FilePath: definitionPath,
		}},
		Events: []admin.VueComponentEvent{{
			Name: "saved", FilePath: definitionPath,
		}},
		Slots: []admin.VueComponentSlot{{
			Name: "header", FilePath: componentTemplatePath,
		}},
	}))
	require.NoError(t, idx.SaveComponentDefinition(
		filepath.Dir(definitionPath),
		admin.ComponentDefinition{
			FilePath: definitionPath, TemplatePath: componentTemplatePath,
			Props: []admin.VueComponentProp{{
				Name: "title", FilePath: definitionPath,
			}},
			Emits: []string{"saved"},
			Events: []admin.VueComponentEvent{{
				Name: "saved", FilePath: definitionPath,
			}},
			Slots: []admin.VueComponentSlot{{
				Name: "header", FilePath: componentTemplatePath,
			}},
		},
	))
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name:         "sw-host",
		FilePath:     filepath.Join(adminRoot, "component/sw-host/index.ts"),
		TemplatePath: consumerPath,
		Members: []admin.VueComponentMember{{
			Name: "dynamicCard", Kind: admin.ComponentMemberComputed,
			ReturnExpressions: []string{"'sw-card'"},
			ReturnsComplete:   true,
		}},
	}))
	provider := NewAdminRenameProvider(idx)

	propEdit, err := provider.Rename(
		context.Background(),
		adminRenameRequest(
			definitionPath, definitionSource, "title", "heading",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, propEdit)
	assert.Contains(t, applyAdminRenameEdits(
		definitionSource,
		propEdit.Changes[uriutil.FileURI(definitionPath)],
	), `props: { heading: String }`)
	assert.Contains(t, applyAdminRenameEdits(
		consumerSource,
		propEdit.Changes[uriutil.FileURI(consumerPath)],
	), `v-bind="{ heading: title }"`)

	eventEdit, err := provider.Rename(
		context.Background(),
		adminRenameRequest(
			definitionPath, definitionSource, "saved", "stored",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, eventEdit)
	assert.Contains(t, applyAdminRenameEdits(
		consumerSource,
		eventEdit.Changes[uriutil.FileURI(consumerPath)],
	), `@stored="save"`)

	slotRequest := adminRenameRequest(
		componentTemplatePath,
		componentTemplateSource,
		"header",
		"toolbar",
	)
	slotOffset := slotRequest.LineIndex.OffsetUTF16(
		uint32(slotRequest.Position.Line),
		uint32(slotRequest.Position.Character),
	)
	slotTarget, slotFound, targetErr := idx.TwigSymbolAt(
		componentTemplatePath, slotRequest.Root, slotOffset,
	)
	require.NoError(t, targetErr)
	require.True(t, slotFound)
	assert.Equal(t, componentTemplatePath, slotTarget.Owner)
	slotEdit, err := provider.Rename(
		context.Background(), slotRequest,
	)
	require.NoError(t, err)
	require.NotNil(t, slotEdit)
	assert.Contains(t, applyAdminRenameEdits(
		consumerSource,
		slotEdit.Changes[uriutil.FileURI(consumerPath)],
	), `#toolbar`)

	panelPath := filepath.Join(adminRoot, "component/sw-panel/index.ts")
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-panel", FilePath: panelPath, DefinitionPath: panelPath,
		Props: []admin.VueComponentProp{{
			Name: "title", FilePath: panelPath,
		}},
	}))
	ambiguousTemplatePath := filepath.Join(
		adminRoot, "component/sw-ambiguous-host/template.html.twig",
	)
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-ambiguous-host",
		FilePath: filepath.Join(
			adminRoot, "component/sw-ambiguous-host/index.ts",
		),
		TemplatePath: ambiguousTemplatePath,
		Members: []admin.VueComponentMember{{
			Name: "dynamicCard", Kind: admin.ComponentMemberComputed,
			ReturnExpressions: []string{"'sw-card'", "'sw-panel'"},
			ReturnsComplete:   true,
		}},
	}))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		ambiguousTemplatePath,
		[]byte(`<component :is="dynamicCard" :title="title" />`),
	)))
	_, err = provider.Rename(
		context.Background(),
		adminRenameRequest(
			definitionPath, definitionSource, "title", "heading",
		),
	)
	require.ErrorContains(t, err, "distinct declarations")
}

func TestAdminRenameScopedSlotLocalsUnderDynamicComponent(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
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
	} {
		require.NoError(t, idx.SaveComponent(component))
	}
	path := filepath.Join(adminRoot, "consumer.html.twig")
	provider := NewAdminRenameProvider(idx)
	source := `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="{ item: row }">{{ row.name }}<div v-for="row in rows">{{ row.name }}</div>{{ row.id }}</template></component>`
	edit, err := provider.Rename(
		context.Background(), adminRenameRequest(path, source, "row.id", "entry"),
	)
	require.NoError(t, err)
	require.NotNil(t, edit)
	assert.Equal(t,
		`<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="{ item: entry }">{{ entry.name }}<div v-for="row in rows">{{ row.name }}</div>{{ entry.id }}</template></component>`,
		applyAdminRenameEdits(source, edit.Changes[uriutil.FileURI(path)]),
	)

	shorthand := `<sw-card-a><template #row="{ item }">{{ item.name }}</template></sw-card-a>`
	edit, err = provider.Rename(
		context.Background(),
		adminRenameRequest(path, shorthand, "item.name", "row"),
	)
	require.NoError(t, err)
	require.NotNil(t, edit)
	assert.Equal(t,
		`<sw-card-a><template #row="{ item: row }">{{ row.name }}</template></sw-card-a>`,
		applyAdminRenameEdits(shorthand, edit.Changes[uriutil.FileURI(path)]),
	)

	alias := `<sw-card-a><template #row="{ item: row }">{{ row }}</template></sw-card-a>`
	_, err = provider.Rename(
		context.Background(), adminRenameRequest(path, alias, "item", "entry"),
	)
	require.ErrorContains(t, err, "contract member")

	wholeObject := `<sw-card-a><template #row="props">{{ props.item }}</template></sw-card-a>`
	_, err = provider.Rename(
		context.Background(),
		adminRenameRequest(path, wholeObject, "item", "entry"),
	)
	require.ErrorContains(t, err, "contract member")
}

func TestAdminRenameRejectsCompoundModelAttribute(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "sw-field/index.ts")
	templatePath := filepath.Join(adminRoot, "consumer.html.twig")
	require.NoError(t, idx.SaveComponent(admin.VueComponent{
		Name: "sw-field", FilePath: definitionPath,
		DefinitionPath: definitionPath,
		Props: []admin.VueComponentProp{{
			Name: "modelValue", FilePath: definitionPath, Line: 2,
		}},
		Events: []admin.VueComponentEvent{{
			Name: "update:modelValue", FilePath: definitionPath, Line: 3,
		}},
	}))
	source := `<sw-field v-model="value" />`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		templatePath, []byte(source),
	)))
	_, err = NewAdminRenameProvider(idx).Rename(
		context.Background(),
		adminRenameRequest(templatePath, source, "v-model", "value"),
	)
	require.ErrorContains(t, err, "compound v-model")
}

func TestAdminRenameUpdatesDynamicComponentLiteral(t *testing.T) {
	root := t.TempDir()
	idx, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "main.ts")
	templatePath := filepath.Join(adminRoot, "consumer.html.twig")
	registrationSource := `Shopware.Component.register('sw-card', {});`
	templateSource := `<component :is="active ? 'sw-card' : 'sw-card'" />`
	for path, source := range map[string]string{
		registrationPath: registrationSource,
		templatePath:     templateSource,
	} {
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
			path, []byte(source),
		)))
	}
	edit, err := NewAdminRenameProvider(idx).Rename(
		context.Background(),
		adminRenameRequest(templatePath, templateSource, "sw-card", "sw-panel"),
	)
	require.NoError(t, err)
	require.NotNil(t, edit)
	assert.Equal(
		t,
		`Shopware.Component.register('sw-panel', {});`,
		applyAdminRenameEdits(
			registrationSource, edit.Changes[uriutil.FileURI(registrationPath)],
		),
	)
	assert.Equal(
		t,
		`<component :is="active ? 'sw-panel' : 'sw-panel'" />`,
		applyAdminRenameEdits(
			templateSource, edit.Changes[uriutil.FileURI(templatePath)],
		),
	)
}

func TestAdminRenameUpdatesInheritedComponentMemberUsages(t *testing.T) {
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
	provider := NewAdminRenameProvider(idx)

	edit, err := provider.Rename(
		context.Background(),
		adminRenameRequest(childTemplate, childTemplateSource, "count", "total"),
	)
	require.NoError(t, err)
	require.NotNil(t, edit)
	require.Len(t, edit.Changes, 4)
	assert.Contains(t, applyAdminRenameEdits(
		baseSource, edit.Changes[uriutil.FileURI(basePath)],
	), `return { total: count };`)
	assert.Contains(t, applyAdminRenameEdits(
		baseSource, edit.Changes[uriutil.FileURI(basePath)],
	), `this.total += 1`)
	assert.Contains(t, applyAdminRenameEdits(
		childSource, edit.Changes[uriutil.FileURI(childPath)],
	), `this.total`)
	assert.Equal(t, `{{ total }}`, applyAdminRenameEdits(
		baseTemplateSource, edit.Changes[uriutil.FileURI(baseTemplate)],
	))
	assert.Equal(
		t,
		`<div v-for="count in rows">{{ count }}</div>{{ total }}`,
		applyAdminRenameEdits(
			childTemplateSource,
			edit.Changes[uriutil.FileURI(childTemplate)],
		),
	)

	fromDeclaration, err := provider.Rename(
		context.Background(),
		adminRenameRequest(basePath, baseSource, "count", "total"),
	)
	require.NoError(t, err)
	require.NotNil(t, fromDeclaration)
	assert.Equal(t, edit.Changes, fromDeclaration.Changes)

	unsavedTemplateSource := childTemplateSource + ` {{ count }}`
	fromUnsavedTemplate, err := provider.Rename(
		context.Background(),
		adminRenameRequest(
			childTemplate, unsavedTemplateSource, "count", "total",
		),
	)
	require.NoError(t, err)
	require.NotNil(t, fromUnsavedTemplate)
	assert.Len(
		t,
		fromUnsavedTemplate.Changes[uriutil.FileURI(childTemplate)],
		2,
	)
	assert.Equal(
		t,
		`<div v-for="count in rows">{{ count }}</div>{{ total }} {{ total }}`,
		applyAdminRenameEdits(
			unsavedTemplateSource,
			fromUnsavedTemplate.Changes[uriutil.FileURI(childTemplate)],
		),
	)
}

func TestAdminRenameRejectsInheritedComponentMemberConflict(t *testing.T) {
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
	baseSource := `import template from './sw-base.html.twig';
Component.register('sw-base', {
    template,
    data() { return { count: 0 }; },
});`
	childSource := `Component.extend('sw-child', 'sw-base', {
    computed: { total() { return this.count; } },
});`
	for path, source := range map[string]string{
		basePath:     baseSource,
		baseTemplate: `{{ count }}`,
		childPath:    childSource,
	} {
		require.NoError(t, idx.Index(indexerpkg.NewParsedFile(path, []byte(source))))
	}

	_, err = NewAdminRenameProvider(idx).Rename(
		context.Background(),
		adminRenameRequest(baseTemplate, `{{ count }}`, "count", "total"),
	)
	require.ErrorContains(t, err, "already exists")
}

func TestAdminRenameReplacementExpandsObjectBindingShorthand(t *testing.T) {
	shorthand := admin.AdminSourceRange{NameStyle: admin.AdminNameShorthand}
	assert.Equal(t, "heading: title", adminRenameReplacement(
		admin.AdminSymbolTarget{
			Kind: admin.AdminSymbolComponentProp, Name: "title",
		},
		"heading", shorthand,
	))
	assert.Equal(t, "title: heading", adminRenameReplacement(
		admin.AdminSymbolTarget{
			Kind: admin.AdminSymbolComponentMember, Name: "title",
		},
		"heading", shorthand,
	))
	shorthand.Declaration = true
	assert.Equal(t, "heading: title", adminRenameReplacement(
		admin.AdminSymbolTarget{
			Kind: admin.AdminSymbolComponentMember, Name: "title",
		},
		"heading", shorthand,
	))
}

func adminRenameRequest(
	path,
	source,
	needle,
	newName string,
) *lsp.RenameRequest {
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.LastIndex(source, needle) + len(needle)/2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.RenameParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.NewName = newName
	return &lsp.RenameRequest{
		RenameParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document: document, DocumentContent: document.Text,
			DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
			Root:  document.SyntaxTree.Root,
			Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
			Token: document.SyntaxTree.Root.TokenAtOffset(offset),
		},
	}
}

func applyAdminRenameEdits(source string, edits []protocol.TextEdit) string {
	lineIndex := cst.NewLineIndex(source)
	result := source
	for _, edit := range edits {
		start := lineIndex.OffsetUTF16(
			uint32(edit.Range.Start.Line), uint32(edit.Range.Start.Character),
		)
		end := lineIndex.OffsetUTF16(
			uint32(edit.Range.End.Line), uint32(edit.Range.End.Character),
		)
		result = result[:start] + edit.NewText + result[end:]
	}
	return result
}

func adminRenameEditCount(edit *protocol.WorkspaceEdit) int {
	if edit == nil {
		return 0
	}
	count := 0
	for _, edits := range edit.Changes {
		count += len(edits)
	}
	return count
}
