package admin

import (
	"strings"
	"testing"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseScopedSlotBindings(t *testing.T) {
	assert.Equal(t, []TwigSlotBinding{
		{MemberName: "item", LocalName: "row"},
		{MemberName: "index", LocalName: "index"},
		{MemberName: "active", LocalName: "active"},
		{LocalName: "rest", WholeObject: true},
	}, parseScopedSlotBindings(
		`{ item: row, index = 0, active, ...rest }`,
	))
	assert.Equal(t, []TwigSlotBinding{{
		LocalName: "props", WholeObject: true,
	}}, parseScopedSlotBindings("props"))
	assert.ElementsMatch(t, []string{"active", "label"},
		objectBindingNames(`{ active: activeItem, label }`))
	assert.ElementsMatch(t, []string{"item", "isInlineEdit", "label"},
		objectBindingNames(`{
			item,
			isInlineEdit: (isInlineEdit(item) && item.active),
			label: getLabel(item, { translated: true }),
			...forwarded,
		}`))
	names, complete := VueObjectBindingNames(`{ label, count: value }`)
	assert.True(t, complete)
	assert.ElementsMatch(t, []string{"label", "count"}, names)
	names, complete = VueObjectBindingNames(`{ label, ...forwarded }`)
	assert.False(t, complete)
	assert.Equal(t, []string{"label"}, names)
}

func TestNestedScopedSlotsRetainOuterBindings(t *testing.T) {
	source := `<sw-parent><template #outer="props"><sw-child><template #inner="{ currentValue }"><span :title="props.title + currentValue"></span></template></sw-child></template></sw-parent>`
	root := twigparser.Parse(source).Tree.Root
	offset := uint32(strings.LastIndex(source, "props.title") + 1)
	scopes := TwigScopedSlotsAtOffset(root, offset)
	require.Len(t, scopes, 2)
	require.Len(t, scopes[0].Bindings, 1)
	assert.Equal(t, "props", scopes[0].Bindings[0].LocalName)
	require.Len(t, scopes[1].Bindings, 1)
	assert.Equal(t, "currentValue", scopes[1].Bindings[0].LocalName)

	var propsIdentifier TwigVueMember
	for _, identifier := range TwigVueExpressionRootIdentifiers(
		root, []byte(source),
	) {
		if identifier.Name == "props" {
			propsIdentifier = identifier
		}
	}
	require.Equal(t, "props", propsIdentifier.Name)
	assert.True(t, TwigVueRootIdentifierIsLocal(
		root, []byte(source), propsIdentifier,
	))
}

func TestExpressionRootIdentifierAtOffset(t *testing.T) {
	source := []byte(`item.name + ({ name: item }).name + name`)
	property := uint32(strings.Index(string(source), ".name") + len(".na"))
	_, _, found := ExpressionRootIdentifierAtOffset(source, property)
	assert.False(t, found)
	key := uint32(strings.Index(string(source), "{ name") + len("{ na"))
	_, _, found = ExpressionRootIdentifierAtOffset(source, key)
	assert.False(t, found)
	root := uint32(strings.LastIndex(string(source), "name") + 1)
	name, _, found := ExpressionRootIdentifierAtOffset(source, root)
	require.True(t, found)
	assert.Equal(t, "name", name)
}

func TestTwigScopedSlotAtOffsetIsLexicalAndInnermost(t *testing.T) {
	source := `<sw-grid>
    <template #result-item="{ item: row, index }">
        {{ row }}
        <span :title="index"></span>
        <sw-card>
            <template #header="{ title }">{{ title }}</template>
        </sw-card>
    </template>
</sw-grid>
{{ row }}`
	root := twigparser.Parse(source).Tree.Root
	rowOffset := uint32(strings.Index(source, "{{ row }}") + len("{{ r"))
	scope, found := TwigScopedSlotAtOffset(root, rowOffset)
	require.True(t, found)
	assert.Equal(t, "sw-grid", scope.ComponentName)
	assert.Equal(t, "result-item", scope.SlotName)
	assert.Equal(t, "row", scope.Bindings[0].LocalName)
	memberRange := scope.Bindings[0].MemberRange
	localRange := scope.Bindings[0].LocalRange
	assert.Equal(t, "item", source[int(memberRange.Start):int(memberRange.End)])
	assert.Equal(t, "row", source[int(localRange.Start):int(localRange.End)])

	indexOffset := uint32(strings.Index(source, `:title="index"`) + len(`:title="ind`))
	indexNode := root.NodeAtOffset(indexOffset)
	assert.True(t, IsTwigVueExpressionAt(indexNode, indexOffset))
	name, _, found := IdentifierAtOffset([]byte(source), indexOffset)
	require.True(t, found)
	assert.Equal(t, "index", name)

	nestedOffset := uint32(strings.LastIndex(source, "{{ title }}") + len("{{ ti"))
	nested, found := TwigScopedSlotAtOffset(root, nestedOffset)
	require.True(t, found)
	assert.Equal(t, "sw-card", nested.ComponentName)
	assert.Equal(t, "header", nested.SlotName)

	outsideOffset := uint32(strings.LastIndex(source, "{{ row }}") + len("{{ r"))
	_, found = TwigScopedSlotAtOffset(root, outsideOffset)
	assert.False(t, found)
}

func TestResolveTwigScopedSlotBinding(t *testing.T) {
	index, err := NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.SaveComponent(VueComponent{
		Name: "sw-grid",
		Slots: []VueComponentSlot{{
			Name: "result-item", FilePath: "/component.html.twig", Line: 8,
			Members: []VueComponentSlotMember{{
				Name: "item", Type: "Entity", FilePath: "/component.html.twig", Line: 9,
			}},
		}},
	}))
	source := `<sw-grid><template #result-item="{ item: row }">{{ row }}</template></sw-grid>`
	root := twigparser.Parse(source).Tree.Root
	offset := uint32(strings.LastIndex(source, "row") + 1)
	resolved, err := index.ResolveTwigScopedSlotBinding(
		root, root.NodeAtOffset(offset), []byte(source), offset,
	)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assert.Equal(t, "row", resolved.Identifier)
	assert.Equal(t, "item", resolved.Binding.MemberName)
	assert.True(t, resolved.MemberFound)
	assert.Equal(t, "Entity", resolved.Member.Type)
}

func TestScopedSlotLocalsWorkForDefaultAndDynamicUnknownContracts(t *testing.T) {
	index, err := NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for _, test := range []struct {
		name, source, local, component, slot string
	}{
		{
			name: "default slot on component",
			source: `<router-view v-slot="{ Component }">` +
				`{{ Component }}</router-view>`,
			local: "Component", component: "router-view", slot: "default",
		},
		{
			name: "dynamic slot",
			source: `<sw-grid><template #[columnName]="{ item }">` +
				`{{ item }}</template></sw-grid>`,
			local: "item", component: "sw-grid", slot: "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := twigparser.Parse(test.source).Tree.Root
			offset := uint32(strings.LastIndex(test.source, test.local) + 1)
			scope, found := TwigScopedSlotAtOffset(root, offset)
			require.True(t, found)
			assert.Equal(t, test.component, scope.ComponentName)
			assert.Equal(t, test.slot, scope.SlotName)
			resolved, resolveErr := index.ResolveTwigScopedSlotBinding(
				root, root.NodeAtOffset(offset), []byte(test.source), offset,
			)
			require.NoError(t, resolveErr)
			require.NotNil(t, resolved)
			assert.Equal(t, test.local, resolved.Identifier)
			assert.False(t, resolved.MemberFound)
		})
	}
}

func TestResolveTwigScopedSlotUsesDynamicComponentContractIntersection(t *testing.T) {
	index, err := NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for _, component := range []VueComponent{
		{
			Name: "sw-card-a", FilePath: "/a/index.ts",
			Slots: []VueComponentSlot{{
				Name: "row", MembersComplete: true,
				Members: []VueComponentSlotMember{
					{Name: "item", Type: "Product", FilePath: "/a/card.twig", Line: 2},
					{Name: "onlyA", Type: "string", FilePath: "/a/card.twig", Line: 3},
				},
			}},
		},
		{
			Name: "sw-card-b", FilePath: "/b/index.ts",
			Slots: []VueComponentSlot{{
				Name: "row", MembersComplete: true,
				Members: []VueComponentSlotMember{{
					Name: "item", Type: "Category", FilePath: "/b/card.twig", Line: 5,
				}},
			}},
		},
	} {
		require.NoError(t, index.SaveComponent(component))
	}
	source := `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="{ item, onlyA }">{{ item }}</template></component>`
	root := twigparser.Parse(source).Tree.Root
	offset := uint32(strings.LastIndex(source, "item") + 1)
	scope, found := TwigScopedSlotAtOffset(root, offset)
	require.True(t, found)
	assert.Empty(t, scope.ComponentName)
	assert.NotZero(t, scope.StartingTagRange.Len())

	resolved, err := index.ResolveTwigScopedSlot(root, offset)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assert.True(t, resolved.ContractsComplete)
	assert.ElementsMatch(t, []string{"sw-card-a", "sw-card-b"}, resolved.ComponentNames())
	item, found := resolved.Slot.Member("item")
	require.True(t, found)
	assert.Equal(t, "Product | Category", item.Type)
	_, found = resolved.Slot.Member("onlyA")
	assert.False(t, found)
	assert.True(t, resolved.Slot.MembersComplete)

	binding, err := index.ResolveTwigScopedSlotBinding(
		root, root.NodeAtOffset(offset), []byte(source), offset,
	)
	require.NoError(t, err)
	require.NotNil(t, binding)
	assert.True(t, binding.MemberFound)
	assert.Len(t, binding.Members, 2)
}

func TestResolveTwigScopedSlotUsesInferredDynamicComponentOwner(t *testing.T) {
	index, err := NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	templatePath := "/administration/consumer.html.twig"
	for _, component := range []VueComponent{
		{
			Name: "sw-card-a", FilePath: "/a/index.ts",
			Slots: []VueComponentSlot{{
				Name: "row", Members: []VueComponentSlotMember{{Name: "item", Type: "Entity"}},
			}},
		},
		{
			Name: "sw-card-b", FilePath: "/b/index.ts",
			Slots: []VueComponentSlot{{
				Name: "row", Members: []VueComponentSlotMember{{Name: "item", Type: "Entity"}},
			}},
		},
		{
			Name: "sw-host", FilePath: "/host/index.ts", TemplatePath: templatePath,
			Members: []VueComponentMember{{
				Name: "dynamicCard", Kind: ComponentMemberComputed,
				ReturnExpressions: []string{"'sw-card-a'", "'sw-card-b'"},
				ReturnsComplete:   true,
			}},
		},
	} {
		require.NoError(t, index.SaveComponent(component))
	}
	source := `<component :is="dynamicCard"><template #row="{ item }">{{ item }}</template></component>`
	root := twigparser.Parse(source).Tree.Root
	offset := uint32(strings.LastIndex(source, "item") + 1)
	withoutPath, err := index.ResolveTwigScopedSlot(root, offset)
	require.NoError(t, err)
	require.NotNil(t, withoutPath)
	assert.False(t, withoutPath.ContractsComplete)

	resolved, err := index.ResolveTwigScopedSlot(root, offset, templatePath)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assert.True(t, resolved.ContractsComplete)
	_, found := resolved.Slot.Member("item")
	assert.True(t, found)
}

func TestTwigScopedSlotBindingReferencesRespectShadowing(t *testing.T) {
	index, err := NewAdminComponentIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.SaveComponent(VueComponent{
		Name: "sw-grid", FilePath: "/grid.ts",
		Slots: []VueComponentSlot{{
			Name: "row", Members: []VueComponentSlotMember{{Name: "item"}},
		}},
	}))
	source := `<sw-grid><template #row="{ item: row }">{{ row }}<div v-for="row in rows">{{ row }}</div>{{ row.name }}</template></sw-grid>`
	root := twigparser.Parse(source).Tree.Root
	offset := uint32(strings.LastIndex(source, "row.name") + 1)
	resolved, err := index.ResolveTwigScopedSlotBinding(
		root, root.NodeAtOffset(offset), []byte(source), offset,
	)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	ranges := TwigScopedSlotBindingReferences(root, []byte(source), *resolved)
	require.Len(t, ranges, 3)
	for _, rangeValue := range ranges {
		assert.Equal(t, "row", source[int(rangeValue.Start):int(rangeValue.End)])
	}
}
