package admin

import (
	"path/filepath"
	"strings"
	"testing"

	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig/parser"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigDynamicComponentSelector(t *testing.T) {
	for _, test := range []struct {
		name       string
		source     string
		want       []string
		complete   bool
		candidates []string
	}{
		{
			name: "static attribute", source: `<component is="sw-card" />`,
			want: []string{"sw-card"}, complete: true,
		},
		{
			name: "bound literal", source: `<component :is="'sw-card'" />`,
			want: []string{"sw-card"}, complete: true,
		},
		{
			name:   "finite conditional",
			source: `<component :is="enabled ? 'mt-switch' : 'mt-text-field'" />`,
			want:   []string{"mt-switch", "mt-text-field"}, complete: true,
		},
		{
			name:   "nested conditional",
			source: `<component :is="first ? (second ? 'sw-a' : 'sw-b') : 'sw-c'" />`,
			want:   []string{"sw-a", "sw-b", "sw-c"}, complete: true,
		},
		{
			name:   "partially static conditional",
			source: `<component v-bind:is="enabled ? componentName : 'sw-card'" />`,
			want:   []string{"sw-card"}, complete: false,
		},
		{
			name: "runtime selector", source: `<component :is="componentName" />`,
			complete: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			parsed := twigparser.Parse(test.source)
			startTags := twigquery.Nodes(
				parsed.Tree.Root, twigsyntax.HtmlStartingTag,
			)
			require.Len(t, startTags, 1)
			selector, found := TwigDynamicComponentSelector(startTags[0])
			require.True(t, found)
			assert.Equal(t, test.want, selector.Names())
			assert.Equal(t, test.complete, selector.Complete)
			for _, candidate := range selector.Candidates {
				assert.Equal(
					t, candidate.Name,
					test.source[candidate.Range.Start:candidate.Range.End],
				)
				offset := uint32(strings.Index(test.source, candidate.Name) + 1)
				at, atFound := selector.CandidateAt(offset)
				require.True(t, atFound)
				assert.Equal(t, candidate.Name, at.Name)
			}
		})
	}
}

func TestResolveDynamicComponentSelectorFromEffectiveMemberValues(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "component/sw-host/index.js")
	templatePath := filepath.Join(
		adminRoot, "component/sw-host/sw-host.html.twig",
	)
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		definitionPath,
		[]byte(`
import template from './sw-host.html.twig';
import ImportedCard from './imported-card';
export default Shopware.Component.register('sw-host', {
    template,
    components: { ImportedCard },
    props: { runtimeComponent: { type: String, default: 'sw-fallback' } },
    computed: {
        elementType() { return this.disabled ? 'span' : 'router-link'; },
        selectedForm() {
            if (this.contact) { return 'sw-contact'; }
            return this.runtimeComponent;
        },
        inputComponent() {
            switch (this.fieldType) {
                case 'uuid': return 'sw-entity-multi-id-select';
                case 'float':
                case 'int': return 'sw-number-field';
                default: return 'sw-text-field';
            }
        },
    },
});`),
	)))
	for _, name := range []string{
		"sw-entity-multi-id-select", "sw-number-field", "sw-text-field",
		"imported-card",
	} {
		require.NoError(t, idx.SaveComponent(VueComponent{
			Name: name, FilePath: filepath.Join(adminRoot, name, "index.js"),
		}))
	}
	for _, test := range []struct {
		expression string
		names      []string
		complete   bool
		contracts  int
	}{
		{
			"inputComponent",
			[]string{
				"sw-entity-multi-id-select", "sw-number-field", "sw-text-field",
			},
			true, 3,
		},
		{"selectedForm", []string{"sw-contact", "sw-fallback"}, false, 0},
		{"elementType", []string{"span", "router-link"}, true, 0},
		{"runtimeComponent", []string{"sw-fallback"}, false, 0},
		{"ImportedCard", []string{"imported-card"}, true, 1},
	} {
		t.Run(test.expression, func(t *testing.T) {
			source := `<component :is="` + test.expression + `" />`
			parsed := twigparser.Parse(source)
			startTags := twigquery.Nodes(
				parsed.Tree.Root, twigsyntax.HtmlStartingTag,
			)
			require.Len(t, startTags, 1)
			selector, found := TwigDynamicComponentSelector(startTags[0])
			require.True(t, found)
			resolved, components, complete, resolveErr :=
				idx.ResolveDynamicComponentContracts(templatePath, selector)
			require.NoError(t, resolveErr)
			assert.Equal(t, test.names, resolved.Names())
			assert.Equal(t, test.complete, resolved.Complete)
			assert.Equal(t, test.complete && test.contracts > 0, complete)
			assert.Len(t, components, test.contracts)
		})
	}
}

func TestResolveRouterViewDynamicComponentContractsFromModuleRoutes(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	templatePath := filepath.Join(
		adminRoot, "module/sw-account/sw-account.html.twig",
	)
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "module/sw-account/index.js"),
		[]byte(`
import template from './sw-account.html.twig';
Shopware.Component.register('sw-account', { template });
Shopware.Module.register('sw-account', {
    routes: {
        index: {
            component: 'sw-account',
            children: {
                history: { component: 'sw-account-history' },
                general: {
                    component: 'sw-account-general',
                    children: {
                        details: { component: 'sw-account-details' },
                    },
                },
            },
        },
    },
});`),
	)))
	for _, component := range []VueComponent{
		{
			Name:  "sw-account-general",
			Props: []VueComponentProp{{Name: "account", Type: "Account"}},
		},
		{
			Name:  "sw-account-history",
			Props: []VueComponentProp{{Name: "account", Type: "Account"}},
		},
		{Name: "sw-account-details"},
	} {
		component.FilePath = filepath.Join(
			adminRoot, "component", component.Name, "index.js",
		)
		require.NoError(t, idx.SaveComponent(component))
	}

	for _, test := range []struct {
		name       string
		source     string
		expression string
	}{
		{
			name: "destructured default name",
			source: `<router-view v-slot="{ Component }">` +
				`<component :is="Component" /></router-view>`,
			expression: "Component",
		},
		{
			name: "destructured alias",
			source: `<router-view v-slot="{ Component: view }">` +
				`<component :is="view" /></router-view>`,
			expression: "view",
		},
		{
			name: "whole route slot object",
			source: `<router-view v-slot="route">` +
				`<component :is="route.Component" /></router-view>`,
			expression: "route.Component",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			parsed := twigparser.Parse(test.source)
			var startTag *twigsyntax.Node
			var selector VueDynamicComponentSelector
			for _, candidate := range twigquery.Nodes(
				parsed.Tree.Root, twigsyntax.HtmlStartingTag,
			) {
				if current, found := TwigDynamicComponentSelector(candidate); found {
					startTag = candidate
					selector = current
					break
				}
			}
			require.NotNil(t, startTag)
			assert.Equal(t, test.expression, selector.Expression)

			withoutContext, _, complete, resolveErr :=
				idx.ResolveDynamicComponentContracts(templatePath, selector)
			require.NoError(t, resolveErr)
			assert.False(t, complete)
			assert.Empty(t, withoutContext.Names())

			resolved, components, complete, resolveErr :=
				idx.ResolveDynamicComponentContracts(
					templatePath, selector, startTag,
				)
			require.NoError(t, resolveErr)
			require.True(t, complete)
			assert.Equal(t, []string{
				"sw-account-general", "sw-account-history",
			}, resolved.Names())
			assert.Len(t, components, 2)
		})
	}

	parsed := twigparser.Parse(`<component :is="Component" />`)
	startTag := twigquery.Nodes(
		parsed.Tree.Root, twigsyntax.HtmlStartingTag,
	)[0]
	selector, found := TwigDynamicComponentSelector(startTag)
	require.True(t, found)
	resolved, components, complete, resolveErr :=
		idx.ResolveDynamicComponentContracts(templatePath, selector, startTag)
	require.NoError(t, resolveErr)
	assert.False(t, complete)
	assert.Empty(t, resolved.Names())
	assert.Empty(t, components)
}

func TestDynamicComponentReturnAlternativesSurviveCacheRestore(t *testing.T) {
	root := t.TempDir()
	cachePath := filepath.Join(root, "cache")
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "component/sw-host/index.js")
	templatePath := filepath.Join(
		adminRoot, "component/sw-host/sw-host.html.twig",
	)
	idx, err := NewAdminComponentIndexer(cachePath)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		definitionPath,
		[]byte(`
import template from './sw-host.html.twig';
export default Shopware.Component.register('sw-host', {
    template,
    computed: {
        inputComponent() {
            switch (this.kind) {
                case 'card': return 'sw-card';
                default: return 'sw-panel';
            }
        },
    },
});`),
	)))
	for _, name := range []string{"sw-card", "sw-panel"} {
		require.NoError(t, idx.SaveComponent(VueComponent{
			Name: name, FilePath: filepath.Join(adminRoot, name, "index.js"),
		}))
	}
	require.NoError(t, idx.Close())

	reopened, err := NewAdminComponentIndexer(cachePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	parsed := twigparser.Parse(`<component :is="inputComponent" />`)
	startTags := twigquery.Nodes(
		parsed.Tree.Root, twigsyntax.HtmlStartingTag,
	)
	require.Len(t, startTags, 1)
	selector, found := TwigDynamicComponentSelector(startTags[0])
	require.True(t, found)
	resolved, components, complete, resolveErr :=
		reopened.ResolveDynamicComponentContracts(templatePath, selector)
	require.NoError(t, resolveErr)
	require.True(t, complete)
	assert.Equal(t, []string{"sw-card", "sw-panel"}, resolved.Names())
	assert.Len(t, components, 2)
}

func TestResolveCMSRegistryDynamicComponentContracts(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "component/sw-host/index.js")
	templatePath := filepath.Join(
		adminRoot, "component/sw-host/sw-host.html.twig",
	)
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "module/sw-cms/elements.js"),
		[]byte(`
cmsService.registerCmsElement({
    name: 'hero',
    component: 'sw-cms-el-hero',
    configComponent: 'sw-cms-el-config-hero',
    previewComponent: 'sw-cms-el-preview-hero',
});
cmsService.registerCmsElement({
    name: 'text',
    component: 'sw-cms-el-text',
    configComponent: 'sw-cms-el-config-text',
    previewComponent: 'sw-cms-el-preview-text',
});`),
	)))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		definitionPath,
		[]byte(`
import template from './sw-host.html.twig';
export default Shopware.Component.register('sw-host', {
    template,
    props: { runtimeConfig: Object },
    computed: {
        cmsServiceState() {
            return this.cmsService.getCmsServiceState();
        },
        elementConfig() {
            return this.cmsServiceState.elementRegistry[this.element.type];
        },
        cmsElements() {
            const entries = Object.entries(this.cmsService.getCmsElementRegistry());
            return Object.fromEntries(entries);
        },
    },
});`),
	)))
	for _, name := range []string{
		"sw-cms-el-hero", "sw-cms-el-text",
		"sw-cms-el-config-hero", "sw-cms-el-config-text",
		"sw-cms-el-preview-hero", "sw-cms-el-preview-text",
	} {
		require.NoError(t, idx.SaveComponent(VueComponent{
			Name: name, FilePath: filepath.Join(adminRoot, name, "index.js"),
		}))
	}

	for _, test := range []struct {
		name       string
		expression string
		want       []string
	}{
		{
			name:       "render component through service state",
			expression: "elementConfig.component",
			want:       []string{"sw-cms-el-hero", "sw-cms-el-text"},
		},
		{
			name:       "optional config component through service state",
			expression: "elementConfig?.configComponent",
			want: []string{
				"sw-cms-el-config-hero", "sw-cms-el-config-text",
			},
		},
		{
			name:       "config component through copied registry",
			expression: "cmsElements[element.type].configComponent",
			want: []string{
				"sw-cms-el-config-hero", "sw-cms-el-config-text",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			parsed := twigparser.Parse(
				`<component :is="` + test.expression + `" />`,
			)
			startTags := twigquery.Nodes(
				parsed.Tree.Root, twigsyntax.HtmlStartingTag,
			)
			require.Len(t, startTags, 1)
			selector, found := TwigDynamicComponentSelector(startTags[0])
			require.True(t, found)
			resolved, components, complete, resolveErr :=
				idx.ResolveDynamicComponentContracts(templatePath, selector)
			require.NoError(t, resolveErr)
			require.True(t, complete)
			assert.ElementsMatch(t, test.want, resolved.Names())
			assert.Len(t, components, len(test.want))
		})
	}

	parsed := twigparser.Parse(
		`<component :is="runtimeConfig.component" />`,
	)
	selector, found := TwigDynamicComponentSelector(
		twigquery.Nodes(parsed.Tree.Root, twigsyntax.HtmlStartingTag)[0],
	)
	require.True(t, found)
	resolved, components, complete, resolveErr :=
		idx.ResolveDynamicComponentContracts(templatePath, selector)
	require.NoError(t, resolveErr)
	assert.False(t, complete)
	assert.Empty(t, resolved.Names())
	assert.Empty(t, components)
}
