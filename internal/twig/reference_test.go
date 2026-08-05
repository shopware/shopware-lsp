package twig

import (
	"slices"
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigTemplateStrings(t *testing.T) {
	source := `{% extends 'base.html.twig' %}
{% use 'macros.html.twig' %}
{{ include('partial.html.twig') }}
{{ source('source.html.twig') }}
{% include ['fallback.html.twig', 'second.html.twig'] with {'label': 'not-a-template.html.twig'} %}
{% form_theme form with ['forms/base.html.twig', 'forms/custom.html.twig'] %}
{% sw_extends { template: 'scoped.html.twig', scopes: ['not-a-template.html.twig'] } %}
{{ block('title', 'blocks/page.html.twig') }}
{{ block('not-a-template.html.twig') }}
{{ include(dynamic_name) }}
{% include '@Storefront/section-' ~ section.type ~ '.html.twig' %}
{% include 'static/' ~ 'card.html.twig' %}`
	root := twigparser.Parse(source).Tree.Root
	var names []string
	for _, reference := range TwigTemplateReferences("/project/templates/page.html.twig", root) {
		names = append(names, reference.Template)
		assert.Equal(t, reference.Template, source[reference.Range.Start:reference.Range.End])
	}
	assert.ElementsMatch(t, []string{
		"base.html.twig",
		"macros.html.twig",
		"partial.html.twig",
		"source.html.twig",
		"fallback.html.twig",
		"second.html.twig",
		"forms/base.html.twig",
		"forms/custom.html.twig",
		"scoped.html.twig",
		"blocks/page.html.twig",
	}, names)
	assert.False(t, slices.Contains(names, "not-a-template.html.twig"))
	assert.False(t, slices.Contains(names, "@Storefront/section-"))
	assert.False(t, slices.Contains(names, ".html.twig"))
	assert.False(t, slices.Contains(names, "static/card.html.twig"))
}

func TestTwigTemplateTargetGroups(t *testing.T) {
	source := `{% include 'base' ~ '.html.twig' %}
{% include '@Storefront/section-' ~ section.type ~ '.html.twig' %}
{% include ['fallback.html.twig', 'second.html.twig'] %}
{% include ['known.html.twig', dynamic_name] %}
{% include 'optional.html.twig' ignore missing %}
{{ include(template: 'function.html.twig', ignore_missing: true) }}
{{ source('source.html.twig', true) }}`
	root := twigparser.Parse(source).Tree.Root
	groups := TwigTemplateTargetGroups(root)
	require.Len(t, groups, 7)

	assert.True(t, groups[0].Exact)
	assert.Equal(t, "base.html.twig", groups[0].Targets[0].Template)
	assert.False(t, groups[1].Exact)
	assert.Empty(t, groups[1].Targets)
	assert.True(t, groups[2].Exact)
	assert.Equal(t, []string{
		"fallback.html.twig",
		"second.html.twig",
	}, twigTemplateTargetNames(groups[2].Targets))
	assert.False(t, groups[3].Exact)
	assert.Equal(t, []string{
		"known.html.twig",
	}, twigTemplateTargetNames(groups[3].Targets))
	assert.True(t, groups[4].IgnoreMissing)
	assert.True(t, groups[5].IgnoreMissing)
	assert.True(t, groups[6].IgnoreMissing)

	var dynamicPrefix *twigsyntax.Node
	for _, literal := range twigquery.Nodes(
		root,
		twigsyntax.TwigLiteralString,
	) {
		if twigquery.StringValue(literal) == "@Storefront/section-" {
			dynamicPrefix = literal
			break
		}
	}
	require.NotNil(t, dynamicPrefix)
	assert.True(t, IsTwigTemplateString(dynamicPrefix),
		"dynamic prefixes must remain available to completion")
}

func twigTemplateTargetNames(targets []TwigTemplateTarget) []string {
	result := make([]string, 0, len(targets))
	for _, target := range targets {
		result = append(result, target.Template)
	}
	return result
}

func TestPHPTemplateStrings(t *testing.T) {
	source := `<?php
$this->render('page.html.twig');
$this->renderView('view.html.twig');
$this->renderStorefront('storefront.html.twig');
$this->stream('stream.html.twig');
$this->render(template: 'named.html.twig');
$this->renderBlock('ignored.html.twig', 'block');
$this->render(['nested.html.twig']);
$this->render("$dynamic.html.twig");
#[Template('attribute.html.twig')]
/** @Template(template="annotation.html.twig") */
function page() {}
`
	root := phpparser.Parse(source).Tree.Root
	var names []string
	for _, reference := range PHPTemplateReferences("/project/Controller.php", root) {
		names = append(names, reference.Template)
		assert.Equal(t, reference.Template, source[reference.Range.Start:reference.Range.End])
	}
	assert.ElementsMatch(t, []string{
		"page.html.twig",
		"view.html.twig",
		"storefront.html.twig",
		"stream.html.twig",
		"named.html.twig",
		"ignored.html.twig",
		"attribute.html.twig",
		"annotation.html.twig",
	}, names)
	assert.False(t, slices.Contains(names, "nested.html.twig"))
}
