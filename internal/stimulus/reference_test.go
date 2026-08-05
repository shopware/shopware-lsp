package stimulus

import (
	"strings"
	"testing"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigAndHTMLControllerReferences(t *testing.T) {
	source := `<div data-controller="hello users--list"></div>
{{ stimulus_controller('@symfony/ux-chartjs/chart') }}`
	root := twigparser.Parse(source).Tree.Root
	references := References("/project/templates/page.html.twig", root)
	require.Len(t, references, 3)
	assert.Equal(t, "hello", references[0].Name)
	assert.Equal(t, "users--list", references[1].Name)
	assert.Equal(t, "@symfony/ux-chartjs/chart", references[2].Name)
	assert.True(t, references[2].Twig)

	for _, needle := range []string{"hello", "users--list", "ux-chartjs"} {
		offset := uint32(strings.Index(source, needle) + 2)
		node := root.NodeAtOffset(offset)
		reference, found := ReferenceAt(root, node, offset)
		require.True(t, found, needle)
		if needle == "ux-chartjs" {
			assert.Equal(t, "@symfony/ux-chartjs/chart", reference.Name)
			assert.True(t, reference.Twig)
		} else {
			assert.Equal(
				t,
				source[reference.Range.Start:reference.Range.End],
				reference.Name,
			)
		}
	}
}

func TestControllerReferenceCompletionContexts(t *testing.T) {
	for _, source := range []string{
		`<div data-controller=""></div>`,
		`{{ stimulus_controller('') }}`,
	} {
		root := twigparser.Parse(source).Tree.Root
		offset := uint32(strings.Index(source, `""`))
		if !strings.Contains(source, `""`) {
			offset = uint32(strings.Index(source, `''`))
		}
		offset++
		node := root.NodeAtOffset(offset)
		reference, found := ReferenceAt(root, node, offset)
		require.True(t, found, source)
		assert.Empty(t, reference.Name)
		assert.Equal(t, offset, reference.Range.Start)
		assert.Equal(t, offset, reference.Range.End)
	}
}

func TestDynamicDataControllerIsIgnored(t *testing.T) {
	source := `<div data-controller="{{ controller }}"></div>`
	root := twigparser.Parse(source).Tree.Root
	assert.Empty(t, References("/project/templates/page.html.twig", root))
}
