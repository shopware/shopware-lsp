package parser

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

var nastySeeds = []string{
	"",
	"{",
	"{%",
	"{{",
	"{#",
	"{% block",
	"{% block a %}",
	"{{ a b c",
	"<",
	"</",
	`<div class="{{ x }}">`,
	`<a href='{{ '}}'>`,
	`{{ "a\"b'c" }}`,
	"#{",
	`{{ "#{name}" }}`,
	"<!--",
	"<!",
	"\r\n\r\n{%- if -%}\r\n",
	"{% ééé %}",
	"\U0001F600\U0001F4A9<{{ }}",
	"{{{{{{{{{{{{{{{{",
	"{%%%%%%%%%%%%%%%%",
	"<a<b<c<d<e<f<g<h",
	"{% component 'x' with { a: { b: [1, 2",
	"twig:foo:bar:baz",
	"{{(A,",
	"<script<{%",
	"<style </ {% ",
	"<style ] {%- ",
	"{{(a,}}",
}

func FuzzParse(f *testing.F) {
	for _, source := range nastySeeds {
		f.Add(source)
	}

	f.Fuzz(func(t *testing.T, source string) {
		result := Parse(source)
		if result.Tree == nil || result.Tree.Root == nil {
			t.Fatalf("Parse returned a nil tree for %q", source)
		}
		if text := result.Tree.Root.Text(); text != source {
			t.Fatalf("lossless invariant broken: got %q, want %q", text, source)
		}

		sourceLength := uint32(len(source))
		for index, parseError := range result.Errors {
			if parseError.Range.End < parseError.Range.Start || parseError.Range.End > sourceLength {
				t.Fatalf("error %d has invalid range %v for input length %d", index, parseError.Range, sourceLength)
			}
		}

		_ = syntax.DebugTree(result.Tree.Root)
	})
}
