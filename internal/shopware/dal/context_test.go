package dal

import (
	"strings"
	"testing"

	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/stretchr/testify/require"
)

func TestDALReferenceContexts(t *testing.T) {
	jsRoot := javascriptparser.Parse(
		`Shopware.Service('repositoryFactory').create('product')`,
	).Tree.Root
	var jsEntity *jssyntax.Node
	for _, candidate := range jsquery.Nodes(jsRoot, jssyntax.JsString) {
		if jsquery.StringValue(candidate) == "product" {
			jsEntity = candidate
			break
		}
	}
	require.NotNil(t, jsEntity)
	require.True(t, IsJSEntityReference(jsEntity))

	for _, test := range []struct {
		source string
		kind   JSEntityReferenceKind
	}{
		{`Shopware.EntityDefinition.get('product')`, JSEntityReferenceDefinitionGet},
		{`const { EntityDefinition } = Shopware; EntityDefinition.has('product')`, JSEntityReferenceDefinitionHas},
		{`this.EntityDefinition.get('product')`, JSEntityReferenceDefinitionGet},
	} {
		root := javascriptparser.Parse(test.source).Tree.Root
		literal := jsquery.Nodes(root, jssyntax.JsString)[0]
		reference, found := JSEntityReferenceAt(literal)
		require.True(t, found, test.source)
		require.Equal(t, "product", reference.Name)
		require.Equal(t, test.kind, reference.Kind)
	}

	phpRoot := phpparser.Parse(
		`<?php new EqualsFilter('manufacturerId', $id);`,
	).Tree.Root
	var phpString *phpsyntax.Node
	for _, candidate := range phpquery.Nodes(phpRoot, phpsyntax.PhpString) {
		if phpquery.StringValue(candidate) == "manufacturerId" {
			phpString = candidate
			break
		}
	}
	require.NotNil(t, phpString)
	require.True(t, IsPHPFieldReference(phpString))

	twigRoot := twigparser.Parse(
		`{% set products = services.repository.search('product', hook.request) %}`,
	).Tree.Root
	var twigEntity *twigsyntax.Node
	for _, candidate := range twigquery.Nodes(
		twigRoot,
		twigsyntax.TwigLiteralString,
	) {
		if twigquery.StringValue(candidate) == "product" {
			twigEntity = candidate
			break
		}
	}
	require.NotNil(t, twigEntity)
	require.True(t, IsTwigEntityReference(twigEntity))
}

func TestJavaScriptCriteriaFieldReferenceContexts(t *testing.T) {
	tests := []struct {
		source string
		key    string
		kind   JSFieldReferenceKind
	}{
		{`Criteria.equals('manufacturer.name', value)`, "manufacturer.name", JSFieldReferenceField},
		{`Criteria.equalsAny('id', ids)`, "id", JSFieldReferenceField},
		{`Criteria.sort('createdAt', 'DESC')`, "createdAt", JSFieldReferenceField},
		{`Criteria.max('latest', 'createdAt')`, "createdAt", JSFieldReferenceField},
		{`criteria.addAssociation('manufacturer.media')`, "manufacturer.media", JSFieldReferenceAssociation},
		{`criteria.getAssociation('prices')`, "prices", JSFieldReferenceAssociation},
		{`criteria.addFields('name', 'active')`, "active", JSFieldReferenceField},
		{`Criteria.not('AND', [])`, "AND", JSFieldReferenceNone},
		{`Criteria.max('aggregation-name', dynamicField)`, "aggregation-name", JSFieldReferenceNone},
		{`other.sort('createdAt')`, "createdAt", JSFieldReferenceNone},
	}
	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			root := javascriptparser.Parse(test.source).Tree.Root
			var literal *jssyntax.Node
			for _, candidate := range jsquery.Nodes(root, jssyntax.JsString) {
				if jsquery.StringValue(candidate) == test.key {
					literal = candidate
					break
				}
			}
			require.NotNil(t, literal)
			require.Equal(t, test.kind, JSFieldReferenceAt(literal))
		})
	}
}

func TestJavaScriptCriteriaFieldReferenceSegment(t *testing.T) {
	source := `Criteria.equals('manufacturer.media.url', value)`
	root := javascriptparser.Parse(source).Tree.Root
	literal := jsquery.Nodes(root, jssyntax.JsString)[0]
	for _, test := range []struct {
		needle      string
		name        string
		association bool
	}{
		{"manufacturer", "manufacturer", true},
		{"media", "media", true},
		{"url", "url", false},
	} {
		offset := uint32(strings.Index(source, test.needle) + 1)
		name, association := JSFieldReferenceSegment(literal, offset)
		require.Equal(t, test.name, name)
		require.Equal(t, test.association, association)
	}
}
