package parser

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/json/syntax"
)

func TestParseLosslessJSON(t *testing.T) {
	source := `{"name":"Shopware","values":[1,-2.5e3,true,false,null,{"nested":"yes"}]}`
	result := Parse(source)

	if result.Tree == nil || result.Tree.Root == nil {
		t.Fatal("Parse returned a nil tree")
	}
	if result.Tree.Root.Text() != source {
		t.Fatalf("tree text = %q, want %q", result.Tree.Root.Text(), source)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("valid JSON produced errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}
	if result.Tree.Root.Kind() != syntax.JsonDocument {
		t.Fatalf("root kind = %s", result.Tree.Root.Kind())
	}
}

func TestParseReportsMalformedJSONAndKeepsTree(t *testing.T) {
	cases := []string{
		"",
		`{"missing":}`,
		`{"comma": true "next": false}`,
		`{"trailing": true,}`,
		`[1, 2,]`,
		`{"unclosed": [1, 2}`,
		`true false`,
		`{"escape":"\x"}`,
		`{"unicode":"\u12zz"}`,
		"{\"control\":\"raw\ttab\"}",
		`{"unterminated":"value}`,
	}

	for _, source := range cases {
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
		"{",
		"[",
		`{"key":"value"}`,
		`{"escaped":"a\\b\"c\u1234"}`,
		`[true,false,null,-12.5e+3]`,
		`{"nested":{"array":[1,2,{"x":"y"}]}}`,
		`{"missing":}`,
		`{"trailing":1,}`,
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
