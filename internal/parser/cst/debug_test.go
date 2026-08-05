package cst

import "testing"

func TestDebugTreeFormat(t *testing.T) {
	tree := buildSample(t)
	got := DebugTree(tree.Root)
	want := `ROOT@0..31
  TWIG_BLOCK@0..31
    TWIG_STARTING_BLOCK@0..13
      TK_CURLY_PERCENT@0..2 "{%"
      TK_WHITESPACE@2..3 " "
      TK_BLOCK@3..8 "block"
      TK_WHITESPACE@8..9 " "
      TK_WORD@9..10 "a"
      TK_WHITESPACE@10..11 " "
      TK_PERCENT_CURLY@11..13 "%}"
    BODY@13..17
      HTML_TEXT@13..17
        TK_WHITESPACE@13..14 " "
        TK_WORD@14..16 "hi"
        TK_WHITESPACE@16..17 " "
    TWIG_ENDING_BLOCK@17..31
      TK_CURLY_PERCENT@17..19 "{%"
      TK_WHITESPACE@19..20 " "
      TK_ENDBLOCK@20..28 "endblock"
      TK_WHITESPACE@28..29 " "
      TK_PERCENT_CURLY@29..31 "%}"`
	if got != want {
		t.Fatalf("DebugTree mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// No trailing newline.
	if len(got) > 0 && got[len(got)-1] == '\n' {
		t.Fatal("DebugTree must not end with a newline")
	}
}

func TestDebugTreeEscaping(t *testing.T) {
	// Source containing every escapable character plus a multi-byte rune.
	src := "\n\r\t\"\\ é"
	b := NewBuilder(src)
	b.StartNode(kRoot)
	b.Token(tkUnknown, TextRange{0, uint32(len(src))})
	b.FinishNode()
	tree := b.Finish()

	got := DebugTree(tree.Root)
	// The multi-byte 'é' (2 bytes) is written verbatim; range end is len(src)=8.
	want := "ROOT@0..8\n  TK_UNKNOWN@0..8 \"\\n\\r\\t\\\"\\\\ é\""
	if got != want {
		t.Fatalf("escaping mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestDebugTreeEscapingControl pins Rust str Debug ({:?}) escaping for control
// characters and non-printable Unicode. Expected values were captured from the
// compiled ludtwig reference (debug_parse). Regression for writeEscaped emitting
// such bytes verbatim, which broke the byte-for-byte DebugTree contract.
func TestDebugTreeEscapingControl(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string // the escaped body between the surrounding quotes
	}{
		{"nul", "\x00", `\0`},
		{"bel", "\x07", `\u{7}`},
		{"backspace", "\x08", `\u{8}`},
		{"vtab", "\x0b", `\u{b}`},
		{"formfeed", "\x0c", `\u{c}`},
		{"esc", "\x1b", `\u{1b}`},
		{"del", "\x7f", `\u{7f}`},
		{"nel", "\u0085", `\u{85}`},
		{"nbsp", "\u00a0", `\u{a0}`},
		{"soft_hyphen", "\u00ad", `\u{ad}`},
		{"zwsp", "\u200b", `\u{200b}`},
		{"line_sep", "\u2028", `\u{2028}`},
		{"em_space", "\u2000", `\u{2000}`},
		{"ideographic_space", "\u3000", `\u{3000}`},
		{"bom", "\ufeff", `\u{feff}`},
		{"combining_acute", "\u0301", `\u{301}`},
		// Named escapes and printables stay as before.
		{"named_and_printable", "\n\r\t\"\\ é", `\n\r\t\"\\ é`},
		{"space_verbatim", " ", ` `},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuilder(tc.text)
			b.StartNode(kRoot)
			b.Token(tkUnknown, TextRange{0, uint32(len(tc.text))})
			b.FinishNode()
			tree := b.Finish()
			got := DebugTree(tree.Root)
			n := itoa(len(tc.text))
			want := "ROOT@0.." + n + "\n  TK_UNKNOWN@0.." + n + " \"" + tc.want + "\""
			if got != want {
				t.Fatalf("escaping mismatch:\n got: %q\nwant: %q", got, want)
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func TestDebugTreeSingleNode(t *testing.T) {
	b := NewBuilder("")
	b.StartNode(kRoot)
	b.FinishNode()
	tree := b.Finish()
	if got := DebugTree(tree.Root); got != "ROOT@0..0" {
		t.Fatalf("empty root debug = %q", got)
	}
}
