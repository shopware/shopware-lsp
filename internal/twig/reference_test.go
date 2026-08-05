package twig

import (
	"slices"
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	"github.com/stretchr/testify/assert"
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
{{ include(dynamic_name) }}`
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
