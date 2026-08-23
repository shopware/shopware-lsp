package parser

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// TestParseRawTextEofTotality asserts that raw-text elements followed by an
// unknown twig-open that recovers to EOF do not panic (totality invariant).
// Regression for the unguarded bump in parseHtmlRawText / parseHtmlRawTextInner
// after parseAnyTwig consumed tokens up to EOF and returned false.
func TestParseRawTextEofTotality(t *testing.T) {
	inputs := []string{
		"<script<{%",
		"<style </ {% ",
		"<textarea for {{~",
		"<style ] {%- ",
		"<script>{%",
		"<title x {%",
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Parse(%q) panicked: %v", in, r)
				}
			}()
			res := Parse(in)
			if res.Tree == nil || res.Tree.Root == nil {
				t.Fatalf("Parse(%q) returned nil tree", in)
			}
			if got := res.Tree.Root.Text(); got != in {
				t.Fatalf("lossless broken for %q: got %q", in, got)
			}
		}()
	}
}

func TestParseTwigControlFlowInHTMLAttributes(t *testing.T) {
	input := `<element {% if enabled %}foo="yes"{% endif %} {{ attributes }}/>`
	result := Parse(input)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}
	if result.Tree.Root.Text() != input {
		t.Fatalf("lossless parse = %q, want %q", result.Tree.Root.Text(), input)
	}

	foundIf := false
	foundVariable := false
	for element := range result.Tree.Root.Descendants() {
		node, ok := element.(*cst.Node)
		if !ok || node.Parent() == nil || node.Parent().Kind() != syntax.HtmlAttributeList {
			continue
		}
		switch node.Kind() {
		case syntax.TwigIf:
			foundIf = true
		case syntax.TwigVar:
			foundVariable = true
		}
	}
	if !foundIf || !foundVariable {
		t.Fatalf("attribute Twig nodes missing:\n%s", syntax.DebugTree(result.Tree.Root))
	}
}
