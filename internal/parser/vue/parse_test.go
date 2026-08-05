package vue

import (
	"strings"
	"testing"

	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	scssquery "github.com/shopware/shopware-lsp/internal/parser/scss/query"
	scsssyntax "github.com/shopware/shopware-lsp/internal/parser/scss/syntax"
	twigast "github.com/shopware/shopware-lsp/internal/parser/twig/ast"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMixedVueSFCWithAbsoluteEmbeddedRanges(t *testing.T) {
	source := `<template>
  <sw-card :title="title"><template #actions>Save</template></sw-card>
</template>
<script setup lang="ts">
const props = defineProps({ title: { type: String, required: true } });
</script>
<style scoped>.card { color: red; }</style>`
	result := Parse(source)
	require.NotNil(t, result.Tree)
	require.Len(t, result.Sections, 3)
	assert.Equal(t, SectionTemplate, result.Sections[0].Kind)
	assert.Equal(t, SectionScript, result.Sections[1].Kind)
	assert.True(t, result.Sections[1].Setup)
	assert.Equal(t, "ts", result.Sections[1].Language)

	tags := twigquery.Nodes(result.Tree.Root, twigsyntax.HtmlStartingTag)
	require.NotEmpty(t, tags)
	var card *twigsyntax.Node
	for _, tag := range tags {
		if twigquery.HTMLTagName(tag) == "sw-card" {
			card = tag
			break
		}
	}
	require.NotNil(t, card)
	tag, ok := twigast.CastHtmlStartingTag(card)
	require.True(t, ok)
	require.NotNil(t, tag.Name())
	assert.Equal(
		t, strings.Index(source, "sw-card"), int(tag.Name().Range().Start),
	)

	calls := jsquery.Calls(result.Tree.Root, "defineProps")
	require.Len(t, calls, 1)
	assert.Equal(t, strings.Index(source, "defineProps"), int(
		jsquery.CallCallee(calls[0]).RangeTrimmedTrivia().Start,
	))
	assert.NotEmpty(t, jsquery.Nodes(result.Tree.Root, jssyntax.JsProgram))
	variables := scssquery.Nodes(
		result.Tree.Root, scsssyntax.ScssDeclaration,
	)
	require.NotEmpty(t, variables)
	assert.Equal(t, strings.Index(source, "color"), int(
		variables[0].RangeTrimmedTrivia().Start,
	))
}

func TestSectionsKeepsNestedTemplateInsideSFCBody(t *testing.T) {
	source := `<template><div><template #default>Hi</template></div></template><script>export default {}</script>`
	sections := Sections(source)
	require.Len(t, sections, 2)
	body := sections[0].BodyRange
	assert.Equal(
		t, `<div><template #default>Hi</template></div>`,
		source[body.Start:body.End],
	)
}
