package twig

import (
	"strings"
	"testing"

	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigTestExpressions(t *testing.T) {
	source := []byte(
		`{{ first is positive }} ` +
			`{{ second is not positive(1) }} ` +
			`{{ third is same as(3) }}`,
	)
	expressions := TestExpressions(twigparser.Parse(string(source)).Tree.Root)
	require.Len(t, expressions, 3)
	assert.Equal(
		t,
		[]string{"positive", "positive", "same as"},
		[]string{
			expressions[0].Name,
			expressions[1].Name,
			expressions[2].Name,
		},
	)
	for _, expression := range expressions {
		assert.Equal(
			t,
			expression.Name,
			string(source[expression.Range.Start:expression.Range.End]),
		)
	}
}

func TestTwigTestCompletionAt(t *testing.T) {
	for _, test := range []struct {
		source string
		prefix string
		found  bool
	}{
		{`{% if value is <caret> %}`, "", true},
		{`{% if value is not <caret> %}`, "", true},
		{`{{ value is pos<caret>itive }}`, "pos", true},
		{`{% set valid = value is pos<caret> %}`, "pos", true},
		{`{% if value <caret> %}`, "", false},
		{`{# value is <caret> #}`, "", false},
		{`{% if value is positive <caret> %}`, "", false},
	} {
		t.Run(test.source, func(t *testing.T) {
			offset := strings.Index(test.source, "<caret>")
			require.NotEqual(t, -1, offset)
			source := strings.Replace(test.source, "<caret>", "", 1)
			expression, found := TestCompletionAt(
				twigparser.Parse(source).Tree.Root,
				[]byte(source),
				uint32(offset),
			)
			assert.Equal(t, test.found, found)
			assert.Equal(t, test.prefix, expression.Name)
		})
	}
}
