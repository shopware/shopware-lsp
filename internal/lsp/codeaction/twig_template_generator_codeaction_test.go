package codeaction

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/twig"
)

func TestTwigTemplateGeneratorOffersContextualActions(t *testing.T) {
	provider := newTwigTemplateGeneratorFixture(t)

	empty := symfonyGeneratorCodeActionRequest(
		"file:///project/templates/child.html.twig",
		"<main></main>",
		"main",
	)
	actions := provider.GetCodeActions(context.Background(), empty)
	require.Len(t, actions, 1)
	assert.Equal(t, "Symfony: Add Twig extends", actions[0].Title)
	assert.Equal(t, generateTwigExtendsAction, actions[0].Command.Command)

	childSource := `{% extends 'layout.html.twig' %}
{% block sidebar %}custom{% endblock %}
`
	child := symfonyGeneratorCodeActionRequest(
		"file:///project/templates/child.html.twig",
		childSource,
		"sidebar",
	)
	actions = provider.GetCodeActions(context.Background(), child)
	require.Len(t, actions, 1)
	assert.Equal(t, "Symfony: Override Twig blocks", actions[0].Title)
	assert.Equal(t, generateTwigBlocksAction, actions[0].Command.Command)
}

func TestTwigTemplateGeneratorDefersAdministrationTemplatesToComponentOverride(
	t *testing.T,
) {
	provider := newTwigTemplateGeneratorFixture(t)
	source := `{% extends 'layout.html.twig' %}
{% block sidebar %}custom{% endblock %}
`
	request := symfonyGeneratorCodeActionRequest(
		"file:///project/src/Administration/Resources/app/administration/src/component/sw-card/sw-card.html.twig",
		source,
		"sidebar",
	)
	assert.Empty(t, provider.GetCodeActions(context.Background(), request))
}

func TestTwigTemplateGeneratorHidesExtendsWithoutAnotherTemplate(
	t *testing.T,
) {
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	currentPath := "/project/templates/child.html.twig"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		currentPath, []byte("<main></main>"),
	)))
	provider := NewTwigTemplateGeneratorProvider(index)
	request := symfonyGeneratorCodeActionRequest(
		"file://"+currentPath, "<main></main>", "main",
	)
	assert.Empty(t, provider.GetCodeActions(context.Background(), request))

	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/templates/base.html.twig",
		[]byte("{% block content %}{% endblock %}"),
	)))
	actions := provider.GetCodeActions(context.Background(), request)
	require.Len(t, actions, 1)
	assert.Equal(t, "Symfony: Add Twig extends", actions[0].Title)
}

func TestTwigExtendsGeneratorListsAndValidatesTemplates(t *testing.T) {
	provider := newTwigTemplateGeneratorFixture(t)
	raw := mustGeneratorJSON(t, twigTemplateGeneratorRequest{
		FileURI: "file:///project/templates/child.html.twig",
		Source:  "<main></main>",
	})
	value, err := provider.getTwigExtendsCandidates(
		context.Background(),
		&raw,
	)
	require.NoError(t, err)
	candidates := value.(twigTemplateCandidatesResponse)
	assert.Equal(
		t,
		[]string{"base.html.twig", "layout.html.twig"},
		candidates.Templates,
	)

	raw = mustGeneratorJSON(t, twigTemplateGeneratorRequest{
		FileURI:  "file:///project/templates/child.html.twig",
		Source:   "<main></main>",
		Template: "layout.html.twig",
	})
	value, err = provider.generateTwigExtends(
		context.Background(),
		&raw,
	)
	require.NoError(t, err)
	assert.Equal(
		t,
		"{% extends 'layout.html.twig' %}\n",
		value.(twigTemplateGenerationResponse).Content,
	)
}

func TestTwigBlockGeneratorUsesParentChainAndExcludesOverrides(
	t *testing.T,
) {
	provider := newTwigTemplateGeneratorFixture(t)
	source := `{% extends 'layout.html.twig' %}
{% block sidebar %}custom{% endblock %}
`
	raw := mustGeneratorJSON(t, twigTemplateGeneratorRequest{
		FileURI: "file:///project/templates/child.html.twig",
		Source:  source,
	})
	value, err := provider.getTwigBlockCandidates(
		context.Background(),
		&raw,
	)
	require.NoError(t, err)
	candidates := value.(twigBlockCandidatesResponse)
	assert.Equal(t, "layout.html.twig", candidates.Parent)
	assert.Equal(
		t,
		[]string{"content", "footer", "header"},
		candidates.Blocks,
	)

	raw = mustGeneratorJSON(t, twigTemplateGeneratorRequest{
		FileURI:        "file:///project/templates/child.html.twig",
		Source:         source,
		SelectedBlocks: []string{"header", "content"},
	})
	value, err = provider.generateTwigBlocks(
		context.Background(),
		&raw,
	)
	require.NoError(t, err)
	assert.Equal(
		t,
		"{% block content %}\n    $0\n{% endblock %}\n\n"+
			"{% block header %}\n    \n{% endblock %}\n",
		value.(twigTemplateGenerationResponse).Content,
	)
}

func newTwigTemplateGeneratorFixture(
	t *testing.T,
) *TwigTemplateGeneratorProvider {
	t.Helper()
	index, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	for path, source := range map[string]string{
		"/project/templates/base.html.twig": `{% block header %}{% endblock %}
{% block content %}{% endblock %}
{% block footer %}{% endblock %}`,
		"/project/templates/layout.html.twig": `{% extends 'base.html.twig' %}
{% block header %}layout{% endblock %}
{% block sidebar %}{% endblock %}`,
	} {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	return NewTwigTemplateGeneratorProvider(index)
}
