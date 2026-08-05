package admin

import (
	"path/filepath"
	"testing"

	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseComponentRegister(t *testing.T) {
	code := `
Shopware.Component.register('sw-base-filter', () => import('src/app/component/filter/sw-base-filter/index'));
`
	filePath := "/project/src/Administration/Resources/app/administration/src/app/component/index.ts"
	components := parseComponentRegistrations(parseJS(t, code), []byte(code), filePath)

	require.Len(t, components, 1)
	assert.Equal(t, "sw-base-filter", components[0].Name)
	assert.Equal(t, "", components[0].ExtendsComponent)
	assert.Equal(t, "src/app/component/filter/sw-base-filter/index", components[0].ImportPath)
	assert.Equal(t, filePath, components[0].FilePath)
	assert.Equal(t, 2, components[0].Line)
}

func TestParseComponentAndPropDeprecations(t *testing.T) {
	code := `
/**
 * @deprecated tag:v6.8.0 - Use mt-card instead.
 */
Shopware.Component.register('sw-card', {
    props: {
        /**
         * @deprecated tag:v6.8.0 - Use dataSource instead.
         * The old value will be removed.
         */
        items: { type: Array },
        active: Boolean,
    },
});`
	filePath := "/project/src/Administration/Resources/app/administration/src/app/component/sw-card/index.ts"
	components := parseComponentRegistrations(
		parseJS(t, code), []byte(code), filePath,
	)
	require.Len(t, components, 1)
	assert.Equal(
		t, "tag:v6.8.0 - Use mt-card instead.",
		components[0].Deprecated,
	)
	require.NotNil(t, components[0].InlineDefinition)
	require.Len(t, components[0].InlineDefinition.Props, 2)
	assert.Equal(
		t,
		"tag:v6.8.0 - Use dataSource instead. The old value will be removed.",
		components[0].InlineDefinition.Props[0].Deprecated,
	)
	assert.Empty(t, components[0].InlineDefinition.Props[1].Deprecated)

	exportCode := `
/** @deprecated tag:v6.9.0 - Remove this wrapper. */
export default { props: { title: String } };`
	definition := ParseComponentDefinition(
		parseJS(t, exportCode), []byte(exportCode),
	)
	assert.Equal(
		t, "tag:v6.9.0 - Remove this wrapper.", definition.Deprecated,
	)
}

func TestParseComponentMemberDeprecations(t *testing.T) {
	code := `export default {
    props: {
        /** @deprecated Use title instead. */
        legacyTitle: String,
    },
    data() {
        return {
            /** @deprecated Use active instead. */
            legacyActive: false,
        };
    },
    computed: {
        /** @deprecated Use fullName instead. */
        legacyName() { return ''; },
    },
    methods: {
        /**
         * @deprecated tag:v6.8.0 - Use save instead.
         */
        legacySave() {},
    },
    setup() {
        const legacyOpen = () => {};
        return {
            /** @deprecated Use open instead. */
            legacyOpen,
        };
    },
};`
	definition := ParseComponentDefinitionWithLineIndex(
		parseJS(t, code), jssyntax.NewLineIndex(code),
	)
	deprecations := make(map[string]string)
	for _, member := range definition.Members {
		deprecations[member.Name] = member.Deprecated
	}
	assert.Equal(t, "Use title instead.", deprecations["legacyTitle"])
	assert.Equal(t, "Use active instead.", deprecations["legacyActive"])
	assert.Equal(t, "Use fullName instead.", deprecations["legacyName"])
	assert.Equal(
		t, "tag:v6.8.0 - Use save instead.",
		deprecations["legacySave"],
	)
	assert.Equal(t, "Use open instead.", deprecations["legacyOpen"])
}

func TestEffectiveComponentDoesNotInheritParentDeprecation(t *testing.T) {
	index, err := NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.SaveComponent(VueComponent{
		Name: "sw-parent", FilePath: "/project/parent.ts",
		Deprecated: "Use sw-modern instead.",
		Props: []VueComponentProp{{
			Name: "legacyValue", Type: "String",
			Deprecated: "Use modernValue instead.",
		}},
		Members: []VueComponentMember{{
			Name: "legacySave", Kind: ComponentMemberMethod,
			Deprecated: "Use save instead.",
		}},
		Methods: []string{"legacySave"},
	}))
	require.NoError(t, index.SaveComponent(VueComponent{
		Name: "sw-child", FilePath: "/project/child.ts",
		ExtendsComponent: "sw-parent", Kind: ComponentExtend,
		Members: []VueComponentMember{{
			Name: "legacySave", Kind: ComponentMemberMethod,
		}},
		Methods: []string{"legacySave"},
	}))

	child, err := index.GetEffectiveComponent("sw-child")
	require.NoError(t, err)
	require.NotNil(t, child)
	assert.Empty(t, child.Deprecated)
	prop, found := child.ComponentProp("legacyValue")
	require.True(t, found)
	assert.Equal(t, "Use modernValue instead.", prop.Deprecated)
	member, found := child.TemplateMember("legacySave")
	require.True(t, found)
	assert.Equal(t, "Use save instead.", member.Deprecated)
}

func TestComponentContractDocumentationPersistsAcrossIndexReload(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "cache")
	index, err := NewAdminComponentIndexer(cachePath)
	require.NoError(t, err)
	require.NoError(t, index.SaveComponent(VueComponent{
		Name: "sw-documented", FilePath: "/project/sw-documented.ts",
		Props: []VueComponentProp{{
			Name:          "label",
			Type:          "String",
			Documentation: "Label displayed by the component.",
		}},
		Events: []VueComponentEvent{{
			Name:          "save",
			Type:          "[id: string]",
			Documentation: "Persists the component.",
		}},
	}))
	require.NoError(t, index.Close())

	reopened, err := NewAdminComponentIndexer(cachePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	component, err := reopened.GetEffectiveComponent("sw-documented")
	require.NoError(t, err)
	require.NotNil(t, component)
	prop, found := component.ComponentProp("label")
	require.True(t, found)
	assert.Equal(t, "Label displayed by the component.", prop.Documentation)
	event, found := component.ComponentEvent("save")
	require.True(t, found)
	assert.Equal(t, "Persists the component.", event.Documentation)
}

func TestTemplateParentComponentSupportsExtendAndOverrideBlocks(t *testing.T) {
	root := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	baseTemplate := filepath.Join(root, "a-base.html.twig")
	overrideTemplate := filepath.Join(root, "b-override.html.twig")
	childTemplate := filepath.Join(root, "c-child.html.twig")
	baseBlock := TwigBlock{
		Name: "sw_card_content", Deprecated: "Use sw_card_body instead.",
		FilePath: baseTemplate, Line: 4,
		ScopeMembers: []TwigBlockScopeMember{{
			Name: "item", FilePath: baseTemplate, Line: 2,
		}},
	}
	for _, component := range []VueComponent{
		{
			Name: "sw-card", Kind: ComponentRegister,
			FilePath:     filepath.Join(root, "a-base.js"),
			TemplatePath: baseTemplate, Blocks: []TwigBlock{baseBlock},
		},
		{
			Name: "sw-card", Kind: ComponentOverride,
			TargetComponent: "sw-card",
			FilePath:        filepath.Join(root, "b-override.js"),
			TemplatePath:    overrideTemplate,
			Blocks: []TwigBlock{{
				Name: "sw_card_content", FilePath: overrideTemplate, Line: 2,
			}},
		},
		{
			Name: "acme-card", Kind: ComponentExtend,
			TargetComponent: "sw-card", ExtendsComponent: "sw-card",
			FilePath:     filepath.Join(root, "c-child.js"),
			TemplatePath: childTemplate,
		},
	} {
		require.NoError(t, index.SaveComponent(component))
	}

	overrideParent, err := index.GetParentComponentForTemplate(overrideTemplate)
	require.NoError(t, err)
	require.NotNil(t, overrideParent)
	block, found := overrideParent.ComponentBlock("sw_card_content")
	require.True(t, found)
	assert.Equal(t, baseTemplate, block.FilePath)
	assert.Equal(t, baseBlock.Deprecated, block.Deprecated)

	effectiveBase, err := index.GetEffectiveComponent("sw-card")
	require.NoError(t, err)
	require.NotNil(t, effectiveBase)
	block, found = effectiveBase.ComponentBlock("sw_card_content")
	require.True(t, found)
	assert.Equal(t, overrideTemplate, block.FilePath)
	assert.Equal(t, baseBlock.Deprecated, block.Deprecated)
	item, found := block.ScopeMember("item")
	require.True(t, found)
	assert.Equal(t, baseTemplate, item.FilePath)

	childParent, err := index.GetParentComponentForTemplate(childTemplate)
	require.NoError(t, err)
	require.NotNil(t, childParent)
	block, found = childParent.ComponentBlock("sw_card_content")
	require.True(t, found)
	assert.Equal(t, overrideTemplate, block.FilePath)
	assert.Equal(t, baseBlock.Deprecated, block.Deprecated)
	_, found = block.ScopeMember("item")
	assert.True(t, found)
}

func TestParseComponentDefinitionMarksRuntimeOpenMembers(t *testing.T) {
	open := ParseComponentDefinition(
		parseJS(t, `export default {
    computed: { ...mapState(() => Store.get('flow'), ['flow']) },
};`),
		[]byte(`export default {
    computed: { ...mapState(() => Store.get('flow'), ['flow']) },
};`),
	)
	require.NotNil(t, open)
	assert.True(t, open.OpenRuntimeMembers)

	closed := ParseComponentDefinition(
		parseJS(t, `export default { computed: { title() { return 'title'; } } };`),
		[]byte(`export default { computed: { title() { return 'title'; } } };`),
	)
	require.NotNil(t, closed)
	assert.False(t, closed.OpenRuntimeMembers)
}

func TestParseComponentExtend(t *testing.T) {
	code := `
Shopware.Component.extend('sw-condition-time-range', 'sw-condition-base', () => import('./rule/condition-type/sw-condition-time-range/index'));
`
	filePath := "/project/src/Administration/Resources/app/administration/src/app/component/index.ts"
	components := parseComponentRegistrations(parseJS(t, code), []byte(code), filePath)

	require.Len(t, components, 1)
	assert.Equal(t, "sw-condition-time-range", components[0].Name)
	assert.Equal(t, "sw-condition-base", components[0].ExtendsComponent)
	assert.Equal(t, "./rule/condition-type/sw-condition-time-range/index", components[0].ImportPath)
}

func TestParseComponentOverride(t *testing.T) {
	code := `
Component.override('sw-product-detail', {
    methods: { save() {} },
});
`
	filePath := "/project/src/Administration/Resources/app/administration/src/extension/index.js"
	components := parseComponentRegistrations(parseJS(t, code), []byte(code), filePath)

	require.Len(t, components, 1)
	assert.Equal(t, "sw-product-detail", components[0].Name)
	assert.Equal(t, ComponentOverride, components[0].Kind)
	assert.Equal(t, "sw-product-detail", components[0].TargetComponent)
	require.NotNil(t, components[0].InlineDefinition)
	assert.Equal(t, []string{"save"}, components[0].InlineDefinition.Methods)
}

func TestParseMultipleComponents(t *testing.T) {
	code := `
Shopware.Component.register('sw-wizard-page', () => import('src/app/component/wizard/sw-wizard-page/index'));
Shopware.Component.register('sw-wizard', () => import('src/app/component/wizard/sw-wizard/index'));
Shopware.Component.extend('sw-sidebar-collapse', 'sw-collapse', () => import('./sidebar/sw-sidebar-collapse/index'));
`
	filePath := "/project/src/Administration/Resources/app/administration/src/app/component/index.ts"
	components := parseComponentRegistrations(parseJS(t, code), []byte(code), filePath)

	require.Len(t, components, 3)

	assert.Equal(t, "sw-wizard-page", components[0].Name)
	assert.Equal(t, "", components[0].ExtendsComponent)

	assert.Equal(t, "sw-wizard", components[1].Name)
	assert.Equal(t, "", components[1].ExtendsComponent)

	assert.Equal(t, "sw-sidebar-collapse", components[2].Name)
	assert.Equal(t, "sw-collapse", components[2].ExtendsComponent)
}

func TestParseVueApplicationComponentCollections(t *testing.T) {
	code := `
import MtButton from '@shopware-ag/meteor-component-library/dist/esm/MtButton';
import { MtDropdownMenuRoot } from '@shopware-ag/meteor-component-library';

class Adapter {
async initComponents() {
const meteorComponents = {
    MtButton,
    MtDropdownMenuRoot,
} as const;
const lazyMeteorComponents = {
    MtPopover: () => import('@shopware-ag/meteor-component-library/dist/esm/MtPopover'),
};
const unrelated = { MtIgnored };

Object.entries(meteorComponents).forEach(([componentName, component]) => {
    const componentNameAsKebabCase = Shopware.Utils.string.kebabCase(componentName);
    this.app.component(componentNameAsKebabCase, component);
});
Object.entries(lazyMeteorComponents).forEach(([componentName, importMethod]) => {
    const componentNameAsKebabCase = Shopware.Utils.string.kebabCase(componentName);
    this.registerAsyncComponent(componentNameAsKebabCase, importMethod);
});
Object.entries(unrelated).forEach(([name, value]) => console.log(name, value));
}
}
`
	filePath := "/project/src/Administration/Resources/app/administration/src/app/adapter/view/vue.adapter.ts"
	components := parseComponentRegistrations(
		parseJS(t, code), []byte(code), filePath,
	)

	require.Len(t, components, 3)
	assert.Equal(t, "mt-button", components[0].Name)
	assert.Equal(
		t,
		"@shopware-ag/meteor-component-library/dist/esm/MtButton",
		components[0].ImportPath,
	)
	assert.Equal(t, "mt-dropdown-menu-root", components[1].Name)
	assert.Equal(
		t,
		"@shopware-ag/meteor-component-library",
		components[1].ImportPath,
	)
	assert.Equal(t, "mt-popover", components[2].Name)
	assert.Equal(
		t,
		"@shopware-ag/meteor-component-library/dist/esm/MtPopover",
		components[2].ImportPath,
	)
	for _, component := range components {
		assert.Equal(t, filePath, component.FilePath)
		assert.NotZero(t, component.Line)
	}
}

func TestParseVueApplicationComponentCollectionsRequiresRegistration(t *testing.T) {
	code := `
const components = { SwNotActuallyRegistered };
Object.entries(components).forEach(([name, value]) => console.log(name, value));
`
	components := parseComponentRegistrations(
		parseJS(t, code), []byte(code),
		"/project/src/Administration/Resources/app/administration/src/app/example.ts",
	)
	assert.Empty(t, components)
}

func TestResolveImportPath(t *testing.T) {
	tests := []struct {
		name             string
		registrationFile string
		importPath       string
		expected         string
	}{
		{
			name:             "absolute src path with index",
			registrationFile: "/project/src/Administration/Resources/app/administration/src/app/component/index.ts",
			importPath:       "src/app/component/filter/sw-base-filter/index",
			// Has extension-like suffix, so gets .js appended
			expected: "/project/src/Administration/Resources/app/administration/src/app/component/filter/sw-base-filter/index/index.js",
		},
		{
			name:             "relative path with index",
			registrationFile: "/project/src/Administration/Resources/app/administration/src/app/component/index.ts",
			importPath:       "./filter/sw-base-filter/index",
			expected:         "/project/src/Administration/Resources/app/administration/src/app/component/filter/sw-base-filter/index/index.js",
		},
		{
			name:             "parent relative path - directory import",
			registrationFile: "/project/src/Administration/Resources/app/administration/src/module/sw-settings/index.js",
			importPath:       "../sw-other/component",
			// Falls back to /index.js since file doesn't exist
			expected: "/project/src/Administration/Resources/app/administration/src/module/sw-other/component/index.js",
		},
		{
			name:             "import with .js extension",
			registrationFile: "/project/src/Administration/Resources/app/administration/src/app/component/index.ts",
			importPath:       "./filter/sw-base-filter.js",
			expected:         "/project/src/Administration/Resources/app/administration/src/app/component/filter/sw-base-filter.js",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveImportPath(tt.registrationFile, tt.importPath)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIndexerFiltering(t *testing.T) {
	code := `
Shopware.Component.register('sw-base-filter', () => import('src/app/component/filter/sw-base-filter/index'));
`
	// Non-administration path should return empty
	nonAdminPath := "/project/src/Storefront/Resources/app/storefront/src/main.js"
	components := parseComponentRegistrations(parseJS(t, code), []byte(code), nonAdminPath)

	// The parsing still works, but the indexer filters by path in Index()
	// So here we just test that parsing works regardless of path
	require.Len(t, components, 1)
}

func TestParseDestructuredComponent(t *testing.T) {
	code := `
const { Component } = Shopware;

Component.register('my-component', {
    template,
    props: {
        title: String,
    },
    methods: {
        handleClick() {}
    },
});
`
	filePath := "/project/src/Administration/Resources/app/administration/src/module/my-module/component/my-component/index.js"
	components := parseComponentRegistrations(parseJS(t, code), []byte(code), filePath)

	require.Len(t, components, 1)
	assert.Equal(t, "my-component", components[0].Name)
	assert.Equal(t, "", components[0].ExtendsComponent)
	assert.Equal(t, filePath, components[0].DefinitionPath) // Inline definition, same file

	// Check inline definition was parsed
	require.NotNil(t, components[0].InlineDefinition)
	assert.True(t, components[0].InlineDefinition.HasTemplate)
	require.Len(t, components[0].InlineDefinition.Props, 1)
	assert.Equal(t, "title", components[0].InlineDefinition.Props[0].Name)
	assert.Equal(t, "String", components[0].InlineDefinition.Props[0].Type)
	require.Len(t, components[0].InlineDefinition.Methods, 1)
	assert.Equal(t, "handleClick", components[0].InlineDefinition.Methods[0])
}

func TestParseInlineComponentDefinition(t *testing.T) {
	code := `
Shopware.Component.register('inline-component', {
    template,
    
    emits: ['change', 'submit'],
    
    props: {
        name: {
            type: String,
            required: true,
        },
        count: {
            type: Number,
            default: 0,
        },
    },
    
    computed: {
        fullName() {
            return this.name;
        },
    },
    
    methods: {
        save() {},
        cancel() {},
    },
});
`
	filePath := "/project/src/Administration/Resources/app/administration/src/component/index.js"
	components := parseComponentRegistrations(parseJS(t, code), []byte(code), filePath)

	require.Len(t, components, 1)
	assert.Equal(t, "inline-component", components[0].Name)

	def := components[0].InlineDefinition
	require.NotNil(t, def)

	assert.True(t, def.HasTemplate)

	require.Len(t, def.Emits, 2)
	assert.Equal(t, "change", def.Emits[0])
	assert.Equal(t, "submit", def.Emits[1])

	require.Len(t, def.Props, 2)
	assert.Equal(t, "name", def.Props[0].Name)
	assert.Equal(t, "String", def.Props[0].Type)
	assert.True(t, def.Props[0].Required)

	assert.Equal(t, "count", def.Props[1].Name)
	assert.Equal(t, "Number", def.Props[1].Type)
	assert.Equal(t, "0", def.Props[1].Default)

	require.Len(t, def.Computed, 1)
	assert.Equal(t, "fullName", def.Computed[0])

	require.Len(t, def.Methods, 2)
	assert.Equal(t, "save", def.Methods[0])
	assert.Equal(t, "cancel", def.Methods[1])
}

func TestParseExtendWithInlineDefinition(t *testing.T) {
	code := `
const { Component } = Shopware;

Component.extend('my-extended', 'sw-base', {
    props: {
        extra: Boolean,
    },
    methods: {
        customMethod() {},
    },
});
`
	filePath := "/project/src/Administration/Resources/app/administration/src/module/my-module/index.js"
	components := parseComponentRegistrations(parseJS(t, code), []byte(code), filePath)

	require.Len(t, components, 1)
	assert.Equal(t, "my-extended", components[0].Name)
	assert.Equal(t, "sw-base", components[0].ExtendsComponent)

	def := components[0].InlineDefinition
	require.NotNil(t, def)

	require.Len(t, def.Props, 1)
	assert.Equal(t, "extra", def.Props[0].Name)
	assert.Equal(t, "Boolean", def.Props[0].Type)

	require.Len(t, def.Methods, 1)
	assert.Equal(t, "customMethod", def.Methods[0])
}

func TestAdminComponentIndexer(t *testing.T) {
	tempDir := t.TempDir()

	indexer, err := NewAdminComponentIndexer(tempDir)
	require.NoError(t, err)
	defer func() { _ = indexer.Close() }()

	// Index a registration file
	registrationCode := `
Shopware.Component.register('sw-test-component', () => import('src/app/component/test/sw-test-component/index'));
`
	registrationPath := "/project/src/Administration/Resources/app/administration/src/app/component/index.ts"

	err = indexer.Index(indexerpkg.NewParsedFile(registrationPath, []byte(registrationCode)))
	require.NoError(t, err)

	// Index a definition file
	definitionCode := `
import template from './sw-test-component.html.twig';

export default {
    template,
    
    emits: ['change', 'submit'],
    
    props: {
        title: {
            type: String,
            required: true,
        },
        disabled: {
            type: Boolean,
            default: false,
        },
    },
    
    computed: {
        isActive() {
            return !this.disabled;
        },
    },
    
    methods: {
        handleClick() {
            this.$emit('change');
        },
        submit() {
            this.$emit('submit');
        },
    },
};
`
	definitionPath := "/project/src/Administration/Resources/app/administration/src/app/component/test/sw-test-component/index.js"

	err = indexer.Index(indexerpkg.NewParsedFile(definitionPath, []byte(definitionCode)))
	require.NoError(t, err)

	// Check component was registered
	components, err := indexer.GetComponent("sw-test-component")
	require.NoError(t, err)
	require.Len(t, components, 1)
	assert.Equal(t, "sw-test-component", components[0].Name)

	// Check definition was indexed
	def, err := indexer.GetComponentDefinition(definitionPath)
	require.NoError(t, err)
	require.NotNil(t, def)

	// TemplatePath should be resolved to absolute path
	assert.Equal(t, "/project/src/Administration/Resources/app/administration/src/app/component/test/sw-test-component/sw-test-component.html.twig", def.TemplatePath)
	assert.True(t, def.HasTemplate)

	require.Len(t, def.Emits, 2)
	assert.Equal(t, "change", def.Emits[0])
	assert.Equal(t, "submit", def.Emits[1])

	require.Len(t, def.Props, 2)
	assert.Equal(t, "title", def.Props[0].Name)
	assert.Equal(t, "String", def.Props[0].Type)
	assert.True(t, def.Props[0].Required)

	assert.Equal(t, "disabled", def.Props[1].Name)
	assert.Equal(t, "Boolean", def.Props[1].Type)
	assert.False(t, def.Props[1].Required)
	assert.Equal(t, "false", def.Props[1].Default)

	require.Len(t, def.Computed, 1)
	assert.Equal(t, "isActive", def.Computed[0])

	require.Len(t, def.Methods, 2)
	assert.Equal(t, "handleClick", def.Methods[0])
	assert.Equal(t, "submit", def.Methods[1])

	// Test GetComponentWithDefinition - but note the paths won't match in this test
	// because the registration uses 'src/app/...' which resolves differently
	allComponents, err := indexer.GetAllComponents()
	require.NoError(t, err)
	assert.Len(t, allComponents, 1)
}

func TestAdminComponentIndexerIndexesMixinsModulesAndRoutes(t *testing.T) {
	tempDir := t.TempDir()
	adminIndex, err := NewAdminComponentIndexer(tempDir)
	require.NoError(t, err)
	defer func() { _ = adminIndex.Close() }()

	filePath := "/project/src/Administration/Resources/app/administration/src/module/sw-product/index.js"
	content := `
Shopware.Mixin.register('notification', {});
Module.register('sw-product', {
    type: 'core',
    name: 'product',
    title: 'sw-product.general.title',
    description: 'sw-product.general.description',
    routes: {
		index: { path: 'index', components: { default: 'sw-product-list' } },
		detail: {
			path: 'detail/:id',
			component: 'sw-product-detail',
			children: {
				base: {
					path: 'base',
					component: 'sw-product-detail-base',
					children: {
						prices: { path: 'prices', component: 'sw-product-prices' },
					},
				},
			},
		},
		create: {
			path: 'create',
			component: 'sw-product-create',
			children: createChildren(),
		},
    },
});

function createChildren() {
	return {
		general: {
			path: 'general',
			component: 'sw-product-create-general',
		},
	};
}
`
	require.NoError(t, adminIndex.Index(
		indexerpkg.NewParsedFile(filePath, []byte(content)),
	))

	mixins, err := adminIndex.GetMixin("notification")
	require.NoError(t, err)
	require.Len(t, mixins, 1)
	assert.Equal(t, 2, mixins[0].Line)

	modules, err := adminIndex.GetModule("sw-product")
	require.NoError(t, err)
	require.Len(t, modules, 1)
	assert.Equal(t, "product", modules[0].DisplayName)
	assert.Equal(t, "core", modules[0].Type)
	require.Len(t, modules[0].Routes, 6)
	assert.Equal(t, "sw.product.index", modules[0].Routes[0].Name)
	assert.Equal(t, "sw-product-list", modules[0].Routes[0].Component)
	assert.Equal(t, "sw.product.detail.base", modules[0].Routes[2].Name)
	assert.Equal(t, "sw-product-detail-base", modules[0].Routes[2].Component)
	assert.Equal(t, "sw.product.detail.base.prices", modules[0].Routes[3].Name)
	assert.Equal(t, "sw.product.create", modules[0].Routes[4].Name)
	assert.Equal(t, "sw.product.create.general", modules[0].Routes[5].Name)
	assert.Equal(t, "sw-product-create-general", modules[0].Routes[5].Component)

	module, route, err := adminIndex.GetModuleRoute("sw.product.detail")
	require.NoError(t, err)
	require.NotNil(t, module)
	require.NotNil(t, route)
	assert.Equal(t, "sw-product", module.Name)
	assert.Equal(t, "detail/:id", route.Path)

	module, route, err = adminIndex.GetModuleRoute("sw.product.detail.base.prices")
	require.NoError(t, err)
	require.NotNil(t, module)
	require.NotNil(t, route)
	assert.Equal(t, "sw-product-prices", route.Component)

	module, route, err = adminIndex.GetModuleRoute("sw.product.create.general")
	require.NoError(t, err)
	require.NotNil(t, module)
	require.NotNil(t, route)
	assert.Equal(t, "general", route.Path)
}

func TestAdminComponentIndexerIndexesConstConfigAndMiddlewareRoutes(
	t *testing.T,
) {
	index, err := NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	filePath := "/project/Resources/app/administration/src/module/routes.js"
	source := `
const securityOptions = {
    type: 'plugin',
    name: 'settings-security',
    routes: {
        index: { path: 'index', component: 'sw-settings-security-view' },
    },
};
Shopware.Module.register('sw-settings-security', securityOptions);

function profileRouteMiddleware(next, currentRoute) {
    const childRouteName = 'sw.profile.index.mfa';
    const children = currentRoute.children || [];
    children.push({
        name: childRouteName,
        path: 'mfa',
        component: 'frosh-profile-mfa',
    });
    next(currentRoute);
}
Shopware.Module.register('frosh-profile-mfa', {
    routeMiddleware: profileRouteMiddleware,
});

Module.register('sw-theme-manager', {
    routeMiddleware(next, currentRoute) {
        const name = 'sw.sales.channel.detail.theme';
        currentRoute.children.push({
            name,
            path: '/sw/sales/channel/detail/:id/theme',
            component: 'sw-sales-channel-detail-theme',
        });
        next(currentRoute);
    },
});
`
	require.NoError(t, index.Index(
		indexerpkg.NewParsedFile(filePath, []byte(source)),
	))

	for routeName, expectedComponent := range map[string]string{
		"sw.settings.security.index":    "sw-settings-security-view",
		"sw.profile.index.mfa":          "frosh-profile-mfa",
		"sw.sales.channel.detail.theme": "sw-sales-channel-detail-theme",
	} {
		module, route, routeErr := index.GetModuleRoute(routeName)
		require.NoError(t, routeErr)
		require.NotNil(t, module, routeName)
		require.NotNil(t, route, routeName)
		assert.Equal(t, expectedComponent, route.Component, routeName)
		assert.NotZero(t, route.Line, routeName)
	}

	security, err := index.GetModule("sw-settings-security")
	require.NoError(t, err)
	require.Len(t, security, 1)
	assert.Equal(t, "plugin", security[0].Type)
	assert.Equal(t, "settings-security", security[0].DisplayName)
}

func TestWrapComponentConfig(t *testing.T) {
	tempDir := t.TempDir()

	indexer, err := NewAdminComponentIndexer(tempDir)
	require.NoError(t, err)
	defer func() { _ = indexer.Close() }()

	// Index a wrapped component config file (like Meteor component wrappers)
	wrapCode := `
import { MtCard } from '@shopware-ag/meteor-component-library';
import template from './mt-card.html.twig';

export default Shopware.Component.wrapComponentConfig({
    template,

    components: {
        'mt-card-original': MtCard,
    },

    inheritAttrs: false,

    props: {
        positionIdentifier: {
            type: String,
            required: true,
            default: null,
        },
        title: {
            type: String,
        },
    },

    computed: {
        filteredSlots() {
            return this.$slots;
        },
    },

    methods: {
        getFilteredSlots() {
            return this.$slots;
        },
    },
});
`
	// Path follows pattern: mt-card/index.ts
	wrapPath := "/project/src/Administration/Resources/app/administration/src/app/component/meteor-wrapper/mt-card/index.ts"

	err = indexer.Index(indexerpkg.NewParsedFile(wrapPath, []byte(wrapCode)))
	require.NoError(t, err)

	// Check component was registered with derived name
	components, err := indexer.GetComponent("mt-card")
	require.NoError(t, err)
	require.Len(t, components, 1)
	assert.Equal(t, "mt-card", components[0].Name)
	assert.Equal(t, wrapPath, components[0].FilePath)
	assert.Equal(t, wrapPath, components[0].DefinitionPath)
	require.Len(t, components[0].LocalComponents, 1)
	assert.Equal(t, "mt-card-original", components[0].LocalComponents[0].Name)
	assert.Equal(t, "MtCard", components[0].LocalComponents[0].Symbol)
	assert.Equal(
		t,
		"@shopware-ag/meteor-component-library",
		components[0].LocalComponents[0].ImportPath,
	)
	assert.Equal(t, wrapPath, components[0].LocalComponents[0].FilePath)
	assert.NotZero(t, components[0].LocalComponents[0].Line)
	assert.True(t, components[0].LocalComponents[0].Quoted)
	assert.False(t, components[0].LocalComponents[0].Shorthand)
	assert.Equal(
		t,
		components[0].LocalComponents[0].Line-1,
		components[0].LocalComponents[0].NameRange.StartLine,
	)
	assert.Less(
		t,
		components[0].LocalComponents[0].NameRange.StartCharacter,
		components[0].LocalComponents[0].NameRange.EndCharacter,
	)

	// Check props were parsed
	require.Len(t, components[0].Props, 2)
	assert.Equal(t, "positionIdentifier", components[0].Props[0].Name)
	assert.Equal(t, "String", components[0].Props[0].Type)
	assert.True(t, components[0].Props[0].Required)

	assert.Equal(t, "title", components[0].Props[1].Name)
	assert.Equal(t, "String", components[0].Props[1].Type)

	// Check computed was parsed
	require.Len(t, components[0].Computed, 1)
	assert.Equal(t, "filteredSlots", components[0].Computed[0])

	// Check methods were parsed
	require.Len(t, components[0].Methods, 1)
	assert.Equal(t, "getFilteredSlots", components[0].Methods[0])

	// Verify GetComponentWithDefinition works
	componentsWithDef, err := indexer.GetComponentWithDefinition("mt-card")
	require.NoError(t, err)
	require.Len(t, componentsWithDef, 1)
	assert.Equal(t, "mt-card", componentsWithDef[0].Name)
	assert.Len(t, componentsWithDef[0].Props, 2)

	// Local aliases resolve only for the template that owns the Options API
	// declaration and can reuse the target component's public contract.
	local, found, err := indexer.GetComponentForTemplateTag(
		componentsWithDef[0].TemplatePath,
		"mt-card-original",
	)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, local)
	assert.Equal(t, "mt-card-original", local.Name)
	assert.Len(t, local.Props, 2)

	global, err := indexer.GetComponent("mt-card-original")
	require.NoError(t, err)
	assert.Empty(t, global)

	local, found, err = indexer.GetComponentForTemplateTag(
		"/project/another-component.html.twig",
		"mt-card-original",
	)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, local)
}

func TestDeriveComponentNameFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{
			path:     "/path/to/mt-card/index.ts",
			expected: "mt-card",
		},
		{
			path:     "/path/to/mt-card/index.js",
			expected: "mt-card",
		},
		{
			path:     "/path/to/sw-button.js",
			expected: "sw-button",
		},
		{
			path:     "/path/to/sw-button.ts",
			expected: "sw-button",
		},
		{
			path:     "/path/to/component/my-component/index.ts",
			expected: "my-component",
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := deriveComponentNameFromPath(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDeduplicateComponents(t *testing.T) {
	tests := []struct {
		name       string
		components []VueComponent
		wantLen    int
		wantProps  int
	}{
		{
			name:       "empty list",
			components: []VueComponent{},
			wantLen:    0,
			wantProps:  0,
		},
		{
			name: "single component",
			components: []VueComponent{
				{Name: "test", Props: []VueComponentProp{{Name: "prop1"}}},
			},
			wantLen:   1,
			wantProps: 1,
		},
		{
			name: "two components same name - prefer one with more props",
			components: []VueComponent{
				{Name: "test", FilePath: "/file1.ts"},
				{Name: "test", FilePath: "/file2.ts", Props: []VueComponentProp{{Name: "prop1"}, {Name: "prop2"}}},
			},
			wantLen:   1,
			wantProps: 2,
		},
		{
			name: "merge data from both components",
			components: []VueComponent{
				{Name: "test", FilePath: "/file1.ts", ExtendsComponent: "parent"},
				{Name: "test", FilePath: "/file2.ts", Props: []VueComponentProp{{Name: "prop1"}}, DefinitionPath: "/def.ts"},
			},
			wantLen:   1,
			wantProps: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deduplicateComponents(tt.components)
			assert.Len(t, result, tt.wantLen)
			if tt.wantLen > 0 {
				assert.Len(t, result[0].Props, tt.wantProps)
			}
		})
	}
}

func TestMergeComponents(t *testing.T) {
	fallback := VueComponent{
		Name:             "test",
		ExtendsComponent: "parent",
		ImportPath:       "/import/path",
		Props:            []VueComponentProp{{Name: "fallbackProp"}},
		Emits:            []string{"fallbackEmit"},
	}

	preferred := VueComponent{
		Name:           "test",
		DefinitionPath: "/def/path",
		Props:          []VueComponentProp{{Name: "preferredProp1"}, {Name: "preferredProp2"}},
		Methods:        []string{"method1"},
	}

	result := mergeComponents(fallback, preferred)

	// Should take preferred values when available
	assert.Equal(t, "test", result.Name)
	assert.Equal(t, "/def/path", result.DefinitionPath)
	assert.Len(t, result.Props, 2)
	assert.Equal(t, "preferredProp1", result.Props[0].Name)
	assert.Len(t, result.Methods, 1)

	// Should take fallback values when preferred is empty
	assert.Equal(t, "parent", result.ExtendsComponent)
	assert.Equal(t, "/import/path", result.ImportPath)
	assert.Len(t, result.Emits, 1)
	assert.Equal(t, "fallbackEmit", result.Emits[0])
}

func TestOverlayComponentsRetainsInheritedSlotOwner(t *testing.T) {
	parent := VueComponent{
		Name: "sw-parent", TemplatePath: "/project/sw-parent.html.twig",
		Slots: []VueComponentSlot{{
			Name: "header", FilePath: "/project/sw-parent.html.twig", Line: 4,
		}},
	}
	child := VueComponent{
		Name: "sw-child", TemplatePath: "/project/sw-child.html.twig",
		Slots: []VueComponentSlot{{
			Name: "footer", FilePath: "/project/sw-child.html.twig", Line: 8,
		}},
	}
	result := overlayComponents(parent, child)
	require.Len(t, result.Slots, 2)
	assert.Equal(t, "/project/sw-parent.html.twig", result.Slots[0].FilePath)
	assert.Equal(t, "/project/sw-child.html.twig", result.Slots[1].FilePath)
}

func TestEffectiveComponentIncludesParentsOverridesAndMixins(t *testing.T) {
	root := t.TempDir()
	index, err := NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root,
		"src",
		"Resources",
		"app",
		"administration",
		"src",
	)
	childPath := filepath.Join(adminRoot, "component", "sw-child", "index.js")
	files := map[string]string{
		filepath.Join(adminRoot, "mixin", "notification.js"): `
Mixin.register('notification', {
    computed: { notificationTitle() { return ''; } },
    methods: { createNotificationSuccess() {} },
});`,
		filepath.Join(adminRoot, "component", "sw-parent", "index.js"): `
Component.register('sw-parent', {
    props: { parentLabel: { type: String, required: true } },
	emits: ['parent-event'],
    inject: ['repositoryFactory'],
    data() { return { parentLoading: false }; },
	methods: { notifyParent() { this.$emit('runtime-event'); } },
});`,
		childPath: `
Component.extend('sw-child', 'sw-parent', {
    mixins: [Mixin.getByName('notification')],
    props: { childCount: Number },
    computed: { childTitle() { return ''; } },
});`,
		filepath.Join(adminRoot, "extension", "sw-child", "index.js"): `
Component.override('sw-child', {
    props: { overrideEnabled: Boolean },
    methods: { saveOverride() {} },
});`,
	}
	for path, source := range files {
		require.NoError(t, index.Index(indexerpkg.NewParsedFile(path, []byte(source))))
	}

	component, err := index.GetEffectiveComponent("sw-child")
	require.NoError(t, err)
	require.NotNil(t, component)
	assert.ElementsMatch(t, []string{
		"parentLabel", "childCount", "overrideEnabled",
	}, propNames(component.Props))
	assert.ElementsMatch(t, []string{"repositoryFactory"}, component.Injected)
	assert.ElementsMatch(t, []string{"parentLoading"}, component.Data)
	assert.ElementsMatch(t, []string{
		"notificationTitle", "childTitle",
	}, component.Computed)
	assert.ElementsMatch(t, []string{
		"notifyParent", "createNotificationSuccess", "saveOverride",
	}, component.Methods)
	assert.ElementsMatch(t, []string{
		"parent-event", "runtime-event",
	}, component.Emits)
	parentEvent, found := component.ComponentEvent("parent-event")
	require.True(t, found)
	assert.Contains(t, parentEvent.FilePath, "sw-parent")
	assert.NotZero(t, parentEvent.Line)

	var parentProp VueComponentProp
	for _, prop := range component.Props {
		if prop.Name == "parentLabel" {
			parentProp = prop
		}
	}
	assert.Contains(t, parentProp.FilePath, "sw-parent")
	assert.NotZero(t, parentProp.Line)
	byDefinition, err := index.GetComponentsByDefinitionPath(childPath)
	require.NoError(t, err)
	require.Len(t, byDefinition, 1)
	assert.Equal(t, "sw-child", byDefinition[0].Name)
	assert.Contains(t, propNames(byDefinition[0].Props), "parentLabel")
}

func TestEffectiveComponentIncludesDefineComponentMixinAndRegistration(
	t *testing.T,
) {
	root := t.TempDir()
	index, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	mixinPath := filepath.Join(adminRoot, "app/mixin/validation.mixin.ts")
	componentPath := filepath.Join(adminRoot, "component/sw-card/index.ts")

	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		mixinPath,
		[]byte(`export default Shopware.Mixin.register('validation', defineComponent({
            inject: { validationService: { default: null } },
            props: { validation: { type: String } },
            methods: {
                validate(value: string): boolean {
                    if (value) { return true; }
                    return false;
                },
            },
        }));`),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		componentPath,
		[]byte(`Shopware.Component.register('sw-card', defineComponent({
            props: { title: { type: String, required: true } },
            mixins: [Shopware.Mixin.getByName('validation')],
        }));`),
	)))

	mixins, err := index.GetMixin("validation")
	require.NoError(t, err)
	require.Len(t, mixins, 1)
	assert.Equal(t, []string{"validation"}, propNames(mixins[0].Definition.Props))
	assert.Equal(t, []string{"validate"}, mixins[0].Definition.Methods)
	assert.Equal(t, []string{"validationService"}, mixins[0].Definition.Injected)

	component, err := index.GetEffectiveComponent("sw-card")
	require.NoError(t, err)
	require.NotNil(t, component)
	assert.ElementsMatch(
		t,
		[]string{"validation", "title"},
		propNames(component.Props),
	)
	assert.Contains(t, component.Methods, "validate")
	assert.Contains(t, component.Injected, "validationService")
}

func propNames(props []VueComponentProp) []string {
	names := make([]string, 0, len(props))
	for _, prop := range props {
		names = append(names, prop.Name)
	}
	return names
}
