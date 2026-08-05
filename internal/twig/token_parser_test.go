package twig

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTwigTokenParsers(t *testing.T) {
	source := []byte(`<?php
namespace Twig\TokenParser;
interface TokenParserInterface {}

/**
 * @deprecated Foobar deprecated message
 */
class SpacelessTokenParser implements TokenParserInterface
{
    public function getTag(): string { return 'spaceless'; }
}

class SandboxTokenParser extends AbstractTokenParser
{
    public function getTag(): string { return 'sandbox'; }
    public function parse(): void {
        trigger_deprecation(
            'twig/twig',
            '3.9',
            'The "sandbox" tag is deprecated.',
        );
    }
}

#[\Deprecated]
class AttributeDeprecatedTokenParser implements TokenParserInterface
{
    public function getTag(): string { return 'attribute_deprecated'; }
}

class ActiveTokenParser implements TokenParserInterface
{
    public function getTag(): string { return 'active'; }
}

class DerivedTokenParser extends ActiveTokenParser
{
    public function getTag(): string { return 'derived'; }
}
`)
	tree := phpparser.ParseBytes(source).Tree
	tags := ParseTwigTokenParsers(
		"/project/TokenParsers.php",
		tree.Root,
		source,
		phpsyntax.NewLineIndex(string(source)),
	)
	require.Len(t, tags, 5)
	byName := make(map[string]TwigTag)
	for _, tag := range tags {
		byName[tag.Name] = tag
		assert.Equal(
			t,
			tag.Name,
			string(source[tag.Range.Start:tag.Range.End]),
		)
	}
	assert.True(t, byName["spaceless"].Deprecated)
	assert.Equal(
		t,
		"Foobar deprecated message",
		byName["spaceless"].Deprecation,
	)
	assert.True(t, byName["sandbox"].Deprecated)
	assert.Contains(t, byName["sandbox"].Deprecation, "sandbox")
	assert.True(t, byName["attribute_deprecated"].Deprecated)
	assert.False(t, byName["active"].Deprecated)
	assert.False(t, byName["derived"].Deprecated)
	assert.Equal(
		t,
		"Twig\\TokenParser\\SpacelessTokenParser",
		byName["spaceless"].Class,
	)
	assert.NotZero(t, byName["spaceless"].DeprecatedRange.Len())
	assert.NotZero(t, byName["sandbox"].DeprecatedRange.Len())
	assert.NotZero(t, byName["attribute_deprecated"].DeprecatedRange.Len())
}

func TestPHPDeprecatedAttributeFlagsTokenParserClass(t *testing.T) {
	source := []byte(`<?php
#[\Deprecated]
class Parser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'legacy'; }
}`)
	document := phpparser.ParseBytes(source)
	tags := ParseTwigTokenParsers(
		"/project/Parser.php",
		document.Tree.Root,
		source,
		phpsyntax.NewLineIndex(string(source)),
	)
	require.Len(t, tags, 1)
	require.True(t, tags[0].Deprecated)
}

func TestTwigTagUsages(t *testing.T) {
	source := []byte(`{% spaceless %}
{% endspaceless %}
{%- attribute_deprecated -%}
{% verbatim %}{% spaceless %}{% endverbatim %}
{% raw %}{% spaceless %}{% endraw %}
{# {% spaceless %} #}
{{ "spaceless" }}
`)
	usages := TwigTagUsages(source)
	require.Len(t, usages, 5)
	assert.Equal(t, "spaceless", usages[0].Name)
	assert.Equal(t, "endspaceless", usages[1].Name)
	assert.Equal(t, "attribute_deprecated", usages[2].Name)
	assert.Equal(t, "verbatim", usages[3].Name)
	assert.Equal(t, "raw", usages[4].Name)
	for _, usage := range usages {
		assert.Equal(
			t,
			usage.Name,
			string(source[usage.Range.Start:usage.Range.End]),
		)
	}
}

func TestParseTwigTokenParsersExcludesTests(t *testing.T) {
	source := []byte(`<?php
class ParserTest implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'from_class_test'; }
}
class Parser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'from_file_test'; }
}`)
	document := phpparser.ParseBytes(source)
	assert.Empty(t, ParseTwigTokenParsers(
		"/project/ParserTest.php",
		document.Tree.Root,
		source,
		phpsyntax.NewLineIndex(string(source)),
	))
	classTestSource := []byte(`<?php
class ParserTest implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'from_class_test'; }
}`)
	assert.Empty(t, ParseTwigTokenParsers(
		"/project/Parser.php",
		phpparser.ParseBytes(classTestSource).Tree.Root,
		classTestSource,
		phpsyntax.NewLineIndex(string(classTestSource)),
	))
}

func TestTwigIndexerPersistsTokenParserTags(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join(t.TempDir(), "LegacyTokenParser.php")
	source := []byte(`<?php
/** @deprecated Use the modern tag instead. */
class LegacyTokenParser implements \Twig\TokenParser\TokenParserInterface {
    public function getTag(): string { return 'legacy'; }
}`)
	idx, err := NewTwigIndexer(cache)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, source)))
	tags, err := idx.GetTwigTag("legacy")
	require.NoError(t, err)
	require.Len(t, tags, 1)
	require.True(t, tags[0].Deprecated)
	require.Contains(t, tags[0].Deprecation, "modern")
	require.NoError(t, idx.Close())

	reopened, err := NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	restored, err := reopened.GetTwigTag("legacy")
	require.NoError(t, err)
	require.Equal(t, tags, restored)
	deprecated, message, err := reopened.TwigTagDeprecation("endlegacy")
	require.NoError(t, err)
	require.True(t, deprecated)
	require.Contains(t, message, "modern")
}
