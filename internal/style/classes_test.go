package style

import (
	"testing"

	scssparser "github.com/shopware/shopware-lsp/internal/parser/scss"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassDeclarationsIncludeNestedBEMAndEscapedNames(t *testing.T) {
	source := `.sw-card, .utility\:visible {
    &__title, &--active {
        color: red;
    }
    @media (min-width: 40rem) {
        &__wide { display: block; }
    }
    .child { display: block; }
    .button-#{$variant} { color: blue; }
}`
	parsed := scssparser.Parse(source)
	require.NotNil(t, parsed.Tree)
	require.Empty(t, parsed.Errors)

	declarations := ClassDeclarations("component.scss", parsed.Tree.Root)
	var names []string
	for _, declaration := range declarations {
		names = append(names, declaration.Name)
		assert.Equal(t, ClassDeclaration, declaration.Kind)
		assert.NotEmpty(t, declaration.Range)
	}
	assert.ElementsMatch(t, []string{
		"sw-card",
		"utility:visible",
		"sw-card__title",
		"utility:visible__title",
		"sw-card--active",
		"utility:visible--active",
		"sw-card__wide",
		"utility:visible__wide",
		"child",
	}, names)
	assert.NotContains(t, names, "button-")
}

func TestClassUsagesKeepStaticTwigAttributeSegments(t *testing.T) {
	source := `<div class="sw-card {{ active ? 'is-active' : '' }} sw-card__title foo-{{ suffix }} {{ prefix }}-bar"></div>`
	parsed := twigparser.Parse(source)
	require.NotNil(t, parsed.Tree)
	require.Empty(t, parsed.Errors)

	usages := ClassUsages("page.html.twig", parsed.Tree.Root)
	require.Len(t, usages, 2)
	assert.Equal(t, "sw-card", usages[0].Name)
	assert.Equal(t, "sw-card", source[usages[0].Range.Start:usages[0].Range.End])
	assert.Equal(t, "sw-card__title", usages[1].Name)
	assert.Equal(t, "sw-card__title", source[usages[1].Range.Start:usages[1].Range.End])
}
