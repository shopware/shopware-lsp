package parser

import (
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAdministrationComponent(t *testing.T) {
	source := `import template from './sw-card.html.twig';
const { Component } = Shopware;
Component.extend('sw-child', 'sw-parent', {
    template,
    props: { title: { type: String, required: true } },
    methods: { save() { return this.repository.save(); } }
});`
	result := Parse(source)

	require.NotNil(t, result.Tree.Root)
	assert.Equal(t, source, result.Tree.Root.Text())
	assert.Empty(t, result.Errors)
	assert.Equal(t, syntax.JsProgram, result.Tree.Root.Kind())
	assert.NotEmpty(t, nodes(result.Tree.Root, syntax.JsCallExpression))
	assert.NotEmpty(t, nodes(result.Tree.Root, syntax.JsObject))
}

func TestParseExportDefaultAndDynamicImport(t *testing.T) {
	source := `export default Shopware.Component.wrapComponentConfig({
    template: () => import('./component.html.twig'),
    emits: ['save']
});`
	result := Parse(source)
	assert.Equal(t, source, result.Tree.Root.Text())
	assert.Empty(t, result.Errors)
	assert.Len(t, nodes(result.Tree.Root, syntax.JsExportDefault), 1)
}

func TestParseIncompleteCallIsLossless(t *testing.T) {
	source := `Component.extend('child', 'parent`
	result := Parse(source)
	assert.Equal(t, source, result.Tree.Root.Text())
	assert.NotEmpty(t, result.Errors)
}

func TestParseTypedAdministrationDefinitionKeepsNestedMethodBlocks(t *testing.T) {
	source := `export default defineComponent({
    computed: {
        label(): string {
            return this.title;
        },
    },
    methods: {
        async save(value: string): Promise<void> {
            if (value) {
                await this.repository.save(value);
            } else {
                for (const item of this.items) {
                    this.queue.push(item);
                }
            }
        },
        reset(): void {
            this.queue = [];
        },
    },
});`
	result := Parse(source)

	require.NotNil(t, result.Tree.Root)
	assert.Equal(t, source, result.Tree.Root.Text())
	assert.Empty(t, result.Errors)
	assert.Len(t, nodes(result.Tree.Root, syntax.JsMethod), 3)
	assert.GreaterOrEqual(t, len(nodes(result.Tree.Root, syntax.JsBlock)), 6)
}

func TestParseTypeScriptValueAssertionsInsideObjects(t *testing.T) {
	source := `export default defineComponent({
    data() {
        return {
            rows: [] as Array<{ id: string; label: string }>,
            selected: null as Product | null,
            options: {} satisfies Record<string, boolean>,
            count: 0,
        };
    },
    computed: { total(): number { return this.count; } },
});`
	result := Parse(source)

	require.NotNil(t, result.Tree.Root)
	assert.Equal(t, source, result.Tree.Root.Text())
	assert.Empty(t, result.Errors)
	assert.GreaterOrEqual(t, len(nodes(result.Tree.Root, syntax.JsProperty)), 5)
	assert.Len(t, nodes(result.Tree.Root, syntax.JsMethod), 2)
}

func TestParseInlineObjectMethodReturnTypeBeforeBody(t *testing.T) {
	source := `export default defineComponent({
    data(): {
        rows: Row[];
        meta: { count: number };
    } {
        return { rows: [], meta: { count: 0 } };
    },
    computed: {
        visibleRows(): Row[] { return this.rows; },
    },
});`
	result := Parse(source)

	require.NotNil(t, result.Tree.Root)
	assert.Equal(t, source, result.Tree.Root.Text())
	assert.Empty(t, result.Errors)
	assert.Len(t, nodes(result.Tree.Root, syntax.JsMethod), 2)
}

func TestParseTypedArrowPropertyKeepsFollowingObjectSections(t *testing.T) {
	source := `const context: CmsPageState = reactive({ currentPage: null });
const store = Shopware.Store.register({
    id: 'cmsPage',
    state: (): CmsPageState => ({ currentPage: null }),
    getters: {
        pageName: (state: CmsPageState): string => state.currentPage?.name ?? '',
    },
    actions: { reset(): void {} },
});`
	result := Parse(source)

	require.NotNil(t, result.Tree.Root)
	assert.Equal(t, source, result.Tree.Root.Text())
	assert.Empty(t, result.Errors)
	assert.Len(t, nodes(result.Tree.Root, syntax.JsArrowFunction), 2)
	assert.Len(t, nodes(result.Tree.Root, syntax.JsMethod), 1)
	assert.GreaterOrEqual(t, len(nodes(result.Tree.Root, syntax.JsObject)), 5)
}

func TestParseUnaryAwaitKeepsAssignmentRightHandSideTogether(t *testing.T) {
	source := `export default {
	methods: {
		async load() {
			this.items = await this.repository.search(this.criteria);
			this.selected = await this.repository.get(this.id);
		},
	},
};`
	result := Parse(source)

	require.NotNil(t, result.Tree.Root)
	assert.Equal(t, source, result.Tree.Root.Text())
	assert.Empty(t, result.Errors)
	unary := nodes(result.Tree.Root, syntax.JsUnaryExpression)
	require.Len(t, unary, 2)
	assert.Equal(
		t, "await this.repository.search(this.criteria)",
		strings.TrimSpace(unary[0].Text()),
	)
}

func TestParseComputedArrayAndNullishBeforeFollowingMethods(t *testing.T) {
	source := `export default {
	computed: {
		statusOptions() {
			return [
				{ value: 'notSet', label: this.$t('notSet') },
				{ value: 'direct', label: this.$t('direct') },
				{ value: 'optIn', label: this.$t('optIn') },
			];
		},
		adminEsEnable() {
			return Context.app.adminEsEnable ?? false;
		},
	},
	methods: {
		async load() {
			const result = await this.repository.search(this.criteria);
			this.items = result;
		},
	},
};`
	result := Parse(source)

	require.NotNil(t, result.Tree.Root)
	assert.Equal(t, source, result.Tree.Root.Text())
	assert.Empty(t, result.Errors)
	assert.Len(t, nodes(result.Tree.Root, syntax.JsMethod), 3)
}

func FuzzParse(f *testing.F) {
	for _, source := range []string{
		"",
		"Component.register(",
		`export default { props: { title: String } }`,
		`() => import('./x')`,
		"`template ${value}`",
	} {
		f.Add(source)
	}

	f.Fuzz(func(t *testing.T, source string) {
		result := Parse(source)
		require.NotNil(t, result.Tree)
		require.NotNil(t, result.Tree.Root)
		assert.Equal(t, source, result.Tree.Root.Text())
	})
}

func nodes(root *syntax.Node, kind syntax.Kind) []*syntax.Node {
	var result []*syntax.Node
	for element := range root.Descendants() {
		if node, ok := element.(*syntax.Node); ok && node.Kind() == kind {
			result = append(result, node)
		}
	}
	return result
}
