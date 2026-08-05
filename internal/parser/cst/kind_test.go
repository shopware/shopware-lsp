package cst

import (
	"strings"
	"testing"
)

// mustPanic runs fn and returns the recovered panic value, failing if fn did
// not panic.
func mustPanic(t *testing.T, fn func()) (val any) {
	t.Helper()
	defer func() { val = recover() }()
	fn()
	t.Fatal("expected panic, got none")
	return nil
}

// TestRegisterOverlapPanics asserts a language whose range overlaps an already
// registered one panics with a message naming both languages.
func TestRegisterOverlapPanics(t *testing.T) {
	// The "cst-test" language (from kind_testkinds_test.go) already owns
	// [0, testKindCount). A language starting inside that range must be rejected.
	got := mustPanic(t, func() {
		RegisterLanguage(LanguageSpec{
			Name:      "overlapper",
			Base:      0,
			KindNames: []string{"X"},
			FirstNode: 1,
		})
	})
	msg, _ := got.(string)
	if !strings.Contains(msg, "overlaps") || !strings.Contains(msg, "overlapper") {
		t.Fatalf("overlap panic message = %q", msg)
	}
}

// TestRegisterValidationPanics asserts the other validation rules each panic.
func TestRegisterValidationPanics(t *testing.T) {
	cases := []struct {
		name string
		spec LanguageSpec
		want string
	}{
		{
			name: "empty name",
			spec: LanguageSpec{Name: "", Base: 40000, KindNames: []string{"A"}},
			want: "empty language name",
		},
		{
			name: "no kinds",
			spec: LanguageSpec{Name: "empty", Base: 40000},
			want: "no kinds",
		},
		{
			name: "firstnode out of range",
			spec: LanguageSpec{Name: "badfn", Base: 40000, KindNames: []string{"A", "B"}, FirstNode: 39999},
			want: "FirstNode",
		},
		{
			name: "trivia out of range",
			spec: LanguageSpec{Name: "badtrivia", Base: 40000, KindNames: []string{"A"}, FirstNode: 40001, TriviaKinds: []Kind{40005}},
			want: "trivia kind",
		},
		{
			name: "reaches KindNone",
			spec: LanguageSpec{Name: "toobig", Base: 0xFFFE, KindNames: []string{"A", "B"}, FirstNode: 0xFFFE},
			want: "KindNone",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustPanic(t, func() { RegisterLanguage(tc.spec) })
			msg, _ := got.(string)
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("panic message = %q, want substring %q", msg, tc.want)
			}
		})
	}
}

// TestUnregisteredKindFallbacks pins the behavior of kinds no language claims.
func TestUnregisteredKindFallbacks(t *testing.T) {
	// A kind in a gap far above every registered language.
	const k Kind = 50000
	if got := k.String(); got != "KIND(50000)" {
		t.Errorf("unregistered String() = %q want KIND(50000)", got)
	}
	if got := k.TokenText(); got != "KIND(50000)" {
		t.Errorf("unregistered TokenText() = %q want KIND(50000)", got)
	}
	if k.IsTrivia() {
		t.Error("unregistered IsTrivia() should be false")
	}
	if k.IsToken() {
		t.Error("unregistered IsToken() should be false")
	}
	// KindNone is reserved and renders as NONE, not KIND(65535).
	if got := KindNone.String(); got != "NONE" {
		t.Errorf("KindNone.String() = %q want NONE", got)
	}
	if KindNone.IsToken() || KindNone.IsTrivia() {
		t.Error("KindNone must not be a token or trivia")
	}
}

// TestSecondLanguageIsolatedRange registers a second language in a disjoint,
// high range and checks its kinds resolve independently of the test language at
// Base 0.
func TestSecondLanguageIsolatedRange(t *testing.T) {
	const base Kind = 10000
	const (
		miniWS Kind = base + iota
		miniWord
		miniRoot
	)
	RegisterLanguage(LanguageSpec{
		Name:        "mini",
		Base:        base,
		KindNames:   []string{"MINI_WS", "MINI_WORD", "MINI_ROOT"},
		TokenTexts:  []string{"ws", "word"},
		FirstNode:   miniRoot,
		TriviaKinds: []Kind{miniWS},
	})

	if got := miniWord.String(); got != "MINI_WORD" {
		t.Errorf("miniWord.String() = %q", got)
	}
	if got := miniWord.TokenText(); got != "word" {
		t.Errorf("miniWord.TokenText() = %q", got)
	}
	// A node kind has no token text -> falls back to its name.
	if got := miniRoot.TokenText(); got != "MINI_ROOT" {
		t.Errorf("miniRoot.TokenText() = %q", got)
	}
	if !miniWS.IsTrivia() || miniWord.IsTrivia() {
		t.Error("mini trivia classification wrong")
	}
	if !miniWord.IsToken() || miniRoot.IsToken() {
		t.Error("mini token classification wrong")
	}
	// The kinds of the two languages do not interfere.
	if tkWord.IsTrivia() {
		t.Error("test-language word must not be trivia")
	}
}
