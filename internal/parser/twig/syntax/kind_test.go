package syntax

import "testing"

func TestKindOrdinals(t *testing.T) {
	// Verified against ludtwig's untyped.rs enum discriminants.
	cases := []struct {
		k    Kind
		want Kind
	}{
		{TkWhitespace, 0},
		{TkCurlyPercent, 54},
		{Body, 165},
		// Symfony's form_theme extension node follows the ported ludtwig
		// inventory and shifts only the two terminal sentinel kinds.
		{Error, 282},
		{Root, 283},
	}
	for _, c := range cases {
		if c.k != c.want {
			t.Errorf("%s = %d want %d", c.k, uint16(c.k), c.want)
		}
	}
}

func TestIsTrivia(t *testing.T) {
	if !TkWhitespace.IsTrivia() || !TkLineBreak.IsTrivia() {
		t.Fatal("whitespace/linebreak must be trivia")
	}
	// Comments are NOT trivia.
	if TwigComment.IsTrivia() || HtmlComment.IsTrivia() {
		t.Fatal("comments must not be trivia")
	}
	if TkWord.IsTrivia() || Root.IsTrivia() {
		t.Fatal("word/root must not be trivia")
	}
}

func TestIsToken(t *testing.T) {
	if !TkWhitespace.IsToken() || !TkUnknown.IsToken() {
		t.Fatal("token kinds should report IsToken")
	}
	if Body.IsToken() || TwigBlock.IsToken() || Root.IsToken() {
		t.Fatal("node kinds should not report IsToken")
	}
	// Body is the first node kind, TkUnknown the last token kind.
	if TkUnknown+1 != Body {
		t.Fatalf("TkUnknown must immediately precede Body")
	}
}

func TestKindString(t *testing.T) {
	cases := map[Kind]string{
		TkWhitespace:               "TK_WHITESPACE",
		TkWord:                     "TK_WORD",
		TkCurlyPercent:             "TK_CURLY_PERCENT",
		TwigBlock:                  "TWIG_BLOCK",
		ShopwareTwigSwExtends:      "SHOPWARE_TWIG_SW_EXTENDS",
		LudtwigDirectiveFileIgnore: "LUDTWIG_DIRECTIVE_FILE_IGNORE",
		Error:                      "ERROR",
		Root:                       "ROOT",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("%d.String() = %q want %q", uint16(k), got, want)
		}
	}
	if got := KindNone.String(); got != "NONE" {
		t.Errorf("KindNone.String() = %q", got)
	}
}

func TestTokenText(t *testing.T) {
	cases := map[Kind]string{
		TkWhitespace:    "whitespace",
		TkLineBreak:     "line break",
		TkWord:          "word",
		TkCurlyPercent:  "{%",
		TkPercentCurly:  "%}",
		TkNotIn:         "not in",
		TkStartsWith:    "starts with",
		TkBackwardSlash: "\\",
		TkDoubleQuotes:  "\"",
		TkLudtwigIgnore: "ludtwig-ignore",
		TkUnknown:       "unknown",
		Error:           "error",
	}
	for k, want := range cases {
		if got := k.TokenText(); got != want {
			t.Errorf("%s.TokenText() = %q want %q", k, got, want)
		}
	}
	// Node kinds have no Display text in Rust (unreachable!); we fall back to
	// the SCREAMING_SNAKE name.
	if got := TwigBlock.TokenText(); got != "TWIG_BLOCK" {
		t.Errorf("TwigBlock.TokenText() fallback = %q", got)
	}
}
