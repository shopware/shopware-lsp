package translation

import (
	"strings"
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPTranslationReferences(t *testing.T) {
	result := phpparser.Parse(`<?php
$translator->trans('hello.world', [], 'admin');
$translator->trans(id: 'named.key', domain: 'named');
$translator->transChoice('plural.key', 2, [], 'plural');
new TranslatableMessage('message.key', domain: 'message');
t('function.key', domain: 'function');
`)
	references := PHPReferences(result.Tree.Root)
	var values []string
	for _, reference := range references {
		values = append(values, phpquery.StringValue(reference.Node))
	}
	assert.ElementsMatch(t, []string{
		"hello.world", "admin",
		"named.key", "named",
		"plural.key", "plural",
		"message.key", "message",
		"function.key", "function",
	}, values)

	keys := make(map[string]string)
	domains := make(map[string]ReferenceRole)
	for _, reference := range references {
		switch reference.Role {
		case ReferenceKey:
			keys[reference.Key] = reference.Domain
		case ReferenceDomain:
			domains[reference.Domain] = reference.Role
		}
	}
	assert.Equal(t, "admin", keys["hello.world"])
	assert.Equal(t, "named", keys["named.key"])
	assert.Equal(t, "plural", keys["plural.key"])
	assert.Equal(t, "message", keys["message.key"])
	assert.Equal(t, "function", keys["function.key"])
	assert.Equal(t, ReferenceDomain, domains["admin"])
}

func TestPHPTranslationReferenceRejectsNestedParameterStrings(t *testing.T) {
	result := phpparser.Parse(
		`<?php $translator->trans('key', ['nested' => 'value']);`,
	)
	for _, literal := range phpquery.Nodes(
		result.Tree.Root,
		phpsyntax.PhpString,
	) {
		value := phpquery.StringValue(literal)
		_, ok := PHPReferenceAt(literal)
		if value == "key" {
			assert.True(t, ok)
		} else {
			assert.False(t, ok, value)
		}
	}
}

func TestPHPTranslationPlaceholderReference(t *testing.T) {
	result := phpparser.Parse(
		`<?php $translator->trans('hello.world', ['%na' => $name], 'admin');`,
	)
	for _, literal := range phpquery.Nodes(
		result.Tree.Root,
		phpsyntax.PhpString,
	) {
		if phpquery.StringValue(literal) != "%na" {
			continue
		}
		reference, ok := PHPPlaceholderReferenceAt(literal)
		require.True(t, ok)
		assert.Equal(t, ReferencePlaceholder, reference.Role)
		assert.Equal(t, "hello.world", reference.Key)
		assert.Equal(t, "admin", reference.Domain)
		return
	}
	t.Fatal("placeholder literal missing")
}

func TestTwigTranslationReferences(t *testing.T) {
	source := `{% trans_default_domain 'admin' %}
{{ 'default.key'|trans }}
{{ 'explicit.key'|trans({}, 'explicit') }}
{{ 'plural.key'|transchoice(2, {}, 'plural') }}`
	result := twigparser.Parse(source)
	references := TwigReferences(result.Tree.Root, []byte(source))

	keys := make(map[string]string)
	var domains []string
	for _, reference := range references {
		switch reference.Role {
		case ReferenceKey:
			keys[reference.Key] = reference.Domain
		case ReferenceDomain:
			domains = append(domains, reference.Domain)
		}
	}
	assert.Equal(t, "admin", keys["default.key"])
	assert.Equal(t, "explicit", keys["explicit.key"])
	assert.Equal(t, "plural", keys["plural.key"])
	assert.ElementsMatch(t, []string{"admin", "explicit", "plural"}, domains)
}

func TestTwigUnquotedDefaultDomainReference(t *testing.T) {
	source := `{% trans_default_domain admin %}{{ 'key'|trans }}`
	result := twigparser.Parse(source)
	for _, node := range twigquery.Nodes(
		result.Tree.Root,
		twigsyntax.HtmlText,
	) {
		if !strings.Contains(node.Text(), "admin") {
			continue
		}
		reference, ok := TwigReferenceAt(node, []byte(source))
		require.True(
			t,
			ok,
			"%s",
			twigsyntax.DebugTree(result.Tree.Root),
		)
		assert.Equal(t, ReferenceDomain, reference.Role)
		assert.Equal(t, "admin", reference.Domain)
		return
	}
	t.Fatalf("admin node missing:\n%s", twigsyntax.DebugTree(result.Tree.Root))
}

func TestTwigTranslationPlaceholderReference(t *testing.T) {
	source := `{{ 'hello.world'|trans({'%na': name}, 'admin') }}`
	result := twigparser.Parse(source)
	for _, literal := range twigquery.Nodes(
		result.Tree.Root,
		twigsyntax.TwigLiteralString,
	) {
		if twigquery.StringValue(literal) != "%na" {
			continue
		}
		reference, ok := TwigReferenceAt(literal, []byte(source))
		require.True(t, ok)
		assert.Equal(t, ReferencePlaceholder, reference.Role)
		assert.Equal(t, "hello.world", reference.Key)
		assert.Equal(t, "admin", reference.Domain)
		return
	}
	t.Fatal("placeholder literal missing")
}
