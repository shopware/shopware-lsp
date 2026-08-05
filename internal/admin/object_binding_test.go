package admin

import (
	"strings"
	"testing"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVueObjectBindingFieldsRetainSourceIdentity(t *testing.T) {
	source := `  { category, "is-loading": pending, config: makeConfig(item, { active: true }), ...forwarded }  `
	fields, complete := VueObjectBindingFields(source, 10)
	assert.False(t, complete)
	require.Len(t, fields, 3)

	assert.Equal(t, "category", fields[0].Name)
	assert.Equal(t, "category", fields[0].Expression)
	assert.True(t, fields[0].Shorthand)
	assert.Equal(t, "category", source[fields[0].NameRange.Start-10:fields[0].NameRange.End-10])

	assert.Equal(t, "is-loading", fields[1].Name)
	assert.Equal(t, "pending", fields[1].Expression)
	assert.False(t, fields[1].Shorthand)
	assert.Equal(t, "is-loading", source[fields[1].NameRange.Start-10:fields[1].NameRange.End-10])
	assert.Equal(t, "pending", source[fields[1].ExpressionRange.Start-10:fields[1].ExpressionRange.End-10])

	assert.Equal(t, "config", fields[2].Name)
	assert.Equal(t, "makeConfig(item, { active: true })", fields[2].Expression)
}

func TestTwigComponentObjectBindingContexts(t *testing.T) {
	source := `<sw-card v-bind="{ title: headline, disabled, variant: 'secondary', mode: selected, ...attrs }" />`
	root := twigparser.Parse(source).Tree.Root

	titleOffset := uint32(strings.Index(source, "title") + 2)
	startTag, title, found := TwigComponentObjectBindingFieldAtOffset(
		root, titleOffset,
	)
	require.True(t, found)
	assert.Equal(t, "sw-card", startTag.Text()[1:8])
	assert.Equal(t, "title", title.Name)
	assert.Equal(t, "headline", title.Expression)

	headlineOffset := uint32(strings.Index(source, "headline") + 2)
	_, _, found = TwigComponentObjectBindingFieldAtOffset(root, headlineOffset)
	assert.False(t, found)

	disabledOffset := uint32(strings.Index(source, "disabled") + 2)
	_, disabled, found := TwigComponentObjectBindingFieldAtOffset(
		root, disabledOffset,
	)
	require.True(t, found)
	assert.True(t, disabled.Shorthand)

	commaOffset := uint32(strings.Index(source, "disabled") - 1)
	_, fields, found := TwigComponentObjectBindingKeyContextAtOffset(
		root, commaOffset,
	)
	require.True(t, found)
	assert.Len(t, fields, 4)

	_, _, found = TwigComponentObjectBindingKeyContextAtOffset(
		root, headlineOffset,
	)
	assert.False(t, found)

	variantValueOffset := uint32(strings.Index(source, "secondary") + 3)
	valueTag, variant, valueRange, found :=
		TwigComponentObjectBindingValueAtOffset(root, variantValueOffset)
	require.True(t, found)
	assert.Equal(t, startTag.Range(), valueTag.Range())
	assert.Equal(t, "variant", variant.Name)
	assert.Equal(t, "'secondary'", variant.Expression)
	assert.Equal(t, "secondary", source[valueRange.Start:valueRange.End])

	selectedOffset := uint32(strings.Index(source, "selected") + 2)
	_, _, _, found = TwigComponentObjectBindingValueAtOffset(
		root, selectedOffset,
	)
	assert.False(t, found)
	_, _, _, found = TwigComponentObjectBindingValueAtOffset(root, titleOffset)
	assert.False(t, found)
}
