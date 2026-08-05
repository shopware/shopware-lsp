package admin

import (
	"strings"
	"testing"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigRegistryReferencesFindACLCallsInVueExpressions(t *testing.T) {
	source := `<sw-card
    :disabled="!acl.can('product.viewer')"
	    v-tooltip='{ disabled: acl . can ( "order.editor" ) }'
    title="acl.can('ignored.attribute')"
>
    {{ acl.can('customer.viewer') }}
    acl.can('ignored.text')
    {# acl.can('ignored.comment') #}
</sw-card>`
	root := twigparser.Parse(source).Tree.Root
	references := TwigRegistryReferences(root)

	require.Len(t, references, 3)
	assert.Equal(t, []string{
		"product.viewer", "order.editor", "customer.viewer",
	}, []string{references[0].Name, references[1].Name, references[2].Name})
	for _, reference := range references {
		assert.Equal(t, AdminSymbolPrivilege, reference.Kind)
		assert.Equal(
			t,
			reference.Name,
			source[reference.Range.Start:reference.Range.End],
		)
	}
}

func TestTwigRegistryReferenceAtOffsetSupportsIncompletePrivilege(t *testing.T) {
	source := `<mt-button :disabled="acl.can('product.vie"></mt-button>`
	root := twigparser.Parse(source).Tree.Root
	offset := uint32(strings.Index(source, "product.vie") + len("product.vie"))

	reference, found := TwigRegistryReferenceAtOffset(root, offset)
	require.True(t, found)
	assert.Equal(t, "product.vie", reference.Name)
	assert.Equal(t, uint32(strings.Index(source, "product.vie")), reference.Range.Start)
	assert.Equal(t, offset, reference.Range.End)
}

func TestTwigRegistryReferenceAtOffsetRejectsOrdinaryAttribute(t *testing.T) {
	source := `<div title="acl.can('product.viewer')"></div>`
	root := twigparser.Parse(source).Tree.Root
	offset := uint32(strings.Index(source, "product.viewer") + 2)

	_, found := TwigRegistryReferenceAtOffset(root, offset)
	assert.False(t, found)
}

func TestTwigRegistryReferencesFindAdministrationRoutes(t *testing.T) {
	source := `<div>
    <router-link :to="{ name: 'sw.product.detail', params: { id: product.id } }" />
    <mt-button @click="$router.push({ name: 'sw.product.create' })" />
    <mt-link :link-href="$router.resolve({ name: 'sw.product.index' }).href" />
    <span :title="{ name: 'ignored.property' }" />
</div>`
	root := twigparser.Parse(source).Tree.Root
	references := TwigRegistryReferences(root)

	require.Len(t, references, 3)
	assert.Equal(t, []string{
		"sw.product.detail", "sw.product.create", "sw.product.index",
	}, []string{references[0].Name, references[1].Name, references[2].Name})
	for _, reference := range references {
		assert.Equal(t, AdminSymbolModuleRoute, reference.Kind)
		assert.Equal(
			t,
			reference.Name,
			source[reference.Range.Start:reference.Range.End],
		)
	}
}

func TestTwigRegistryReferenceAtOffsetSupportsIncompleteRoute(t *testing.T) {
	source := `<router-link :to="{ name: 'sw.product.det }" />`
	root := twigparser.Parse(source).Tree.Root
	offset := uint32(strings.Index(source, "sw.product.det") + len("sw.product.det"))

	reference, found := TwigRegistryReferenceAtOffset(root, offset)
	require.True(t, found)
	assert.Equal(t, AdminSymbolModuleRoute, reference.Kind)
	assert.Equal(t, "sw.product.det", reference.Name)
}
