package parser

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/scss/syntax"
)

func TestParseLosslessSCSS(t *testing.T) {
	source := `@use "sass:map";

$sw-color-brand-primary: #0042a0 !default;
$variants: (primary: blue, danger: red);

@mixin button($variant) {
  .button-#{$variant}:hover {
    color: map.get($variants, $variant);
    content: feature("ACCESSIBILITY_TWEAKS");
  }
}`
	result := Parse(source)
	if result.Tree == nil || result.Tree.Root == nil {
		t.Fatal("Parse returned a nil tree")
	}
	if result.Tree.Root.Text() != source {
		t.Fatalf("tree text = %q, want %q", result.Tree.Root.Text(), source)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("valid SCSS produced errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}
}

func TestParseReportsMalformedSCSSAndKeepsTree(t *testing.T) {
	for _, source := range []string{
		"}",
		".button { color: red;",
		"color: feature('flag';",
		"content: \"unterminated;",
		"width: calc(100% - (2rem);",
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
		"$value: red;",
		".button { color: $value; }",
		`feature("FLAG")`,
		"@media (width > 10px) { .a { display: block; } }",
		".button-#{$variant} {}",
		"calc(100% - var(--gap))",
		"/* unterminated",
		"\xff\xfe{}",
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
