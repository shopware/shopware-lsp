package snippet

import (
	"strings"
	"testing"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminTwigReferencesCoverTwigAndVueExpressions(t *testing.T) {
	source := `<div
    :label="$t('admin.bound')"
    v-tooltip="{ message: $tc( 'admin.tooltip' ) }"
    title="$t('admin.plain-attribute')"
>
    {{ $t('admin.twig') }}
    $t('admin.plain-text')
    {# {{ $t('admin.comment') }} #}
</div>`
	root := twigparser.Parse(source).Tree.Root
	references := AdminTwigReferences(root)
	require.Len(t, references, 3)
	assert.Equal(t, "admin.bound", references[0].Key)
	assert.Equal(t, "admin.tooltip", references[1].Key)
	assert.Equal(t, "admin.twig", references[2].Key)
	for _, reference := range references {
		assert.Equal(t, reference.Key, source[reference.Range.Start:reference.Range.End])
	}
}

func TestAdminTwigReferenceAtOffsetSupportsIncompleteKeys(t *testing.T) {
	for _, source := range []string{
		`<mt-button :label="$t('admin.sett" />`,
		`<mt-button :label="this.$tc(  'admin.sett" />`,
	} {
		root := twigparser.Parse(source).Tree.Root
		offset := uint32(strings.Index(source, "admin.sett") + len("admin.sett"))
		reference, found := AdminTwigReferenceAtOffset(root, offset)
		require.True(t, found)
		assert.Equal(t, "admin.sett", reference.Key)
	}
}

func TestAdminTwigReferencesRejectDynamicTemplateKeys(t *testing.T) {
	source := "<div :label=\"$t(`admin.${name}`)\" />"
	root := twigparser.Parse(source).Tree.Root
	assert.Empty(t, AdminTwigReferences(root))
}
