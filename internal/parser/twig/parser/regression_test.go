package parser

import "testing"

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
