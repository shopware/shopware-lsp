package lexer

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

func TestKeywordRecognitionIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	keywords := strings.Fields(
		"abstract and array as break callable case catch class clone const " +
			"continue declare default do echo else elseif empty enddeclare " +
			"endfor endforeach endif endswitch endwhile enum eval exit extends " +
			"false final finally fn for foreach from function global goto if " +
			"implements include include_once instanceof insteadof interface " +
			"isset list match namespace new null or parent print private " +
			"protected public readonly require require_once return self static " +
			"switch throw trait true try unset use var while xor yield",
	)
	for _, keyword := range keywords {
		if !isKeyword(keyword) || !isKeyword(strings.ToUpper(keyword)) {
			t.Fatalf("keyword %q was not recognized case-insensitively", keyword)
		}
	}
	for _, name := range []string{"Product", "App_Service", "yielded", "über"} {
		if isKeyword(name) {
			t.Fatalf("identifier %q was recognized as a keyword", name)
		}
	}
}

func TestLexPHPIsLossless(t *testing.T) {
	source := "<?php\n#[Route(path: '/x')]\nfinal class C { public function f(?Foo $x = null): string { return $this->trans('key'); } }\n"
	tokens := Lex(source)
	var joined string
	for _, token := range tokens {
		joined += token.Text()
	}
	if joined != source {
		t.Fatalf("tokens are not lossless:\nwant %q\ngot  %q", source, joined)
	}

	found := map[syntax.Kind]bool{}
	for _, token := range tokens {
		found[token.Kind] = true
	}
	for _, kind := range []syntax.Kind{
		syntax.TkOpenTag,
		syntax.TkAttributeOpen,
		syntax.TkVariable,
		syntax.TkObjectOperator,
		syntax.TkString,
	} {
		if !found[kind] {
			t.Fatalf("missing token kind %s", kind)
		}
	}
}

func TestCompoundOperatorRecognition(t *testing.T) {
	t.Parallel()

	operators := []string{
		"??=", "===", "!==", "<=>", "**=", "<<=", ">>=",
		"??", "==", "!=", "<=", ">=", "++", "--", "**", "<<", ">>",
		"&&", "||", "+=", "-=", "*=", "/=", ".=", "%=", "&=", "|=", "^=",
	}
	for _, operator := range operators {
		kind, length := next(operator, 0)
		if kind != syntax.TkOperator || length != len(operator) {
			t.Errorf(
				"operator %q: got kind %s and length %d, want %s and %d",
				operator,
				kind,
				length,
				syntax.TkOperator,
				len(operator),
			)
		}
	}
}

func TestLexIntoReusesOwnedDestination(t *testing.T) {
	t.Parallel()

	destination := make([]Token, 0, 128)
	data := unsafe.SliceData(destination)
	tokens := LexInto("<?php $value = 1;", destination)

	if unsafe.SliceData(tokens) != data {
		t.Fatal("LexInto did not reuse the provided destination")
	}
	if len(tokens) == 0 {
		t.Fatal("LexInto returned no tokens")
	}
}

func TestLexHeredocAndNowdocAsStrings(t *testing.T) {
	t.Parallel()
	source := "<?php\n$a = <<<TEXT\nhello $name\nTEXT;\n" +
		"$b = <<<'RAW'\nliteral $name\nRAW;\n"
	tokens := Lex(source)
	var joined string
	var stringsFound int
	for _, token := range tokens {
		joined += token.Text()
		if token.Kind == syntax.TkString {
			stringsFound++
		}
	}
	if joined != source {
		t.Fatalf("tokens are not lossless:\nwant %q\ngot  %q", source, joined)
	}
	if stringsFound != 2 {
		t.Fatalf("expected two heredoc strings, got %d", stringsFound)
	}
}

func TestLexIndentedHeredocBeforeCallClosingTokens(t *testing.T) {
	t.Parallel()
	source := `<?php
$statement = $connection->prepare(<<<'SQL'
    SELECT *
    FROM product
    SQL);
$after = true;
`
	tokens := Lex(source)
	var joined string
	var heredoc string
	for _, token := range tokens {
		joined += token.Text()
		if token.Kind == syntax.TkString &&
			strings.HasPrefix(token.Text(), "<<<") {
			heredoc = token.Text()
		}
	}
	if joined != source {
		t.Fatalf("tokens are not lossless:\nwant %q\ngot  %q", source, joined)
	}
	if !strings.HasSuffix(heredoc, "    SQL") {
		t.Fatalf("heredoc token did not stop at its indented label: %q", heredoc)
	}
}
