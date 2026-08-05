package parser

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

func TestParseLosslessYAML(t *testing.T) {
	source := `---
services:
  App\Service\Example:
    class: "App\\Service\\Example"
    tags:
      - name: kernel.event_subscriber
      - { name: doctrine.listener, event: postPersist }
    arguments: ['@logger', "%kernel.debug%"]
parameters:
  message: |
    first line
    second line
...
`
	result := Parse(source)

	if result.Tree == nil || result.Tree.Root == nil {
		t.Fatal("Parse returned a nil tree")
	}
	if result.Tree.Root.Text() != source {
		t.Fatalf("tree text = %q, want %q", result.Tree.Root.Text(), source)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("valid YAML produced errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}
}

func TestParseIndentlessSequenceValue(t *testing.T) {
	source := "steps:\n- name: Checkout\n  uses: actions/checkout@v5\n"
	result := Parse(source)
	if len(result.Errors) != 0 {
		t.Fatalf("indentless sequence produced errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}
}

func TestParseAnchoredCollections(t *testing.T) {
	source := "defaults: &defaults\n  enabled: true\nitems:\n  - !item\n    name: first\n"
	result := Parse(source)
	if len(result.Errors) != 0 {
		t.Fatalf("anchored collections produced errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}
}

func TestParseReportsMalformedYAMLAndKeepsTree(t *testing.T) {
	for _, source := range []string{
		"key: value\nother scalar\n",
		"key: [one, two\n",
		"key: {name value}\n",
		"key: \"unterminated\n",
		"key:\n\tchild: value\n",
		"- one\n  invalid: value\n",
	} {
		t.Run(source, func(t *testing.T) {
			result := Parse(source)
			if result.Tree.Root.Text() != source {
				t.Fatalf("tree is not lossless: got %q", result.Tree.Root.Text())
			}
			if len(result.Errors) == 0 {
				t.Fatalf("malformed input produced no errors:\n%s", syntax.DebugTree(result.Tree.Root))
			}
			for _, parseError := range result.Errors {
				if parseError.Range.Start > parseError.Range.End || parseError.Range.End > uint32(len(source)) {
					t.Fatalf("invalid error range %v for %q", parseError.Range, source)
				}
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	for _, source := range []string{
		"",
		"key: value\n",
		"items:\n  - one\n  - two\n",
		"- name: feature\n  enabled: true\n",
		"flow: [{name: one}, two]\n",
		"block: |-\n  first\n  second\n",
		"---\nfoo: bar\n...\n",
		"key: [unterminated\n",
		"\xff\xfe:\n",
	} {
		f.Add(source)
	}

	f.Fuzz(func(t *testing.T, source string) {
		result := Parse(source)
		if result.Tree == nil || result.Tree.Root == nil {
			t.Fatal("Parse returned a nil tree")
		}
		if result.Tree.Root.Text() != source {
			t.Fatalf("lossless invariant broken: got %q, want %q", result.Tree.Root.Text(), source)
		}
		for _, parseError := range result.Errors {
			if parseError.Range.Start > parseError.Range.End || parseError.Range.End > uint32(len(source)) {
				t.Fatalf("invalid error range %v", parseError.Range)
			}
		}
		_ = syntax.DebugTree(result.Tree.Root)
	})
}
