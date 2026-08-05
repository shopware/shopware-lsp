package parser

import (
	"os"
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/twig/lexer"
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// TestMain enables the debug safety-net assertions (LIFO marker completion,
// marker-leak detection) for the whole parser test suite.
func TestMain(m *testing.M) {
	parsekit.DebugAsserts = true
	os.Exit(m.Run())
}

// tok is a (kind, text) pair used to build a synthetic token slice.
type tok struct {
	kind syntax.Kind
	text string
}

// buildTokens turns a list of (kind, text) pairs into a token slice with
// contiguous ranges and returns the reconstructed source string.
func buildTokens(pairs ...tok) ([]lexer.Token, string) {
	var src []byte
	for _, p := range pairs {
		src = append(src, p.text...)
	}
	source := string(src)
	sourceRef := &source
	tokens := make([]lexer.Token, 0, len(pairs))
	pos := 0
	for _, p := range pairs {
		start := pos
		pos += len(p.text)
		tokens = append(tokens, parsekit.NewToken(
			p.kind,
			sourceRef,
			syntax.TextRange{Start: uint32(start), End: uint32(pos)},
		))
	}
	return tokens, source
}

func TestRootParsesPlainTextLosslessly(t *testing.T) {
	res := Parse("hello world")
	if len(res.Errors) != 0 {
		t.Fatalf("expected no errors, got %v", res.Errors)
	}
	if got := res.Tree.Root.Text(); got != "hello world" {
		t.Fatalf("lossless violated: %q", got)
	}
	// Plain text is wrapped in an HTML_TEXT node by the real grammar.
	want := "ROOT@0..11\n" +
		"  HTML_TEXT@0..11\n" +
		"    TK_WORD@0..5 \"hello\"\n" +
		"    TK_WHITESPACE@5..6 \" \"\n" +
		"    TK_WORD@6..11 \"world\""
	if got := syntax.DebugTree(res.Tree.Root); got != want {
		t.Fatalf("tree mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestParseEmpty(t *testing.T) {
	res := Parse("")
	if len(res.Errors) != 0 {
		t.Fatalf("expected no errors, got %v", res.Errors)
	}
	if got := syntax.DebugTree(res.Tree.Root); got != "ROOT@0..0" {
		t.Fatalf("empty parse mismatch: %q", got)
	}
}

func TestQuotedHTMLAttributeMayContainGreaterThan(t *testing.T) {
	source := `<button data-action="click->live#emit"></button>`
	result := Parse(source)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if got := result.Tree.Root.Text(); got != source {
		t.Fatalf("lossless violated: %q", got)
	}
	attributes := findNodes(result.Tree.Root, syntax.HtmlAttribute)
	if len(attributes) != 1 ||
		attributes[0].Text() != ` data-action="click->live#emit"` {
		t.Fatalf("attributes = %#v", attributes)
	}
}

func TestQuotedHTMLAttributeMayContainLessThan(t *testing.T) {
	source := `<sw-button :disabled="selection.length < 1" data-label="a<!--b" />`
	result := Parse(source)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if got := result.Tree.Root.Text(); got != source {
		t.Fatalf("lossless violated: %q", got)
	}
	strings := findNodes(result.Tree.Root, syntax.HtmlStringInner)
	if len(strings) != 2 ||
		strings[0].Text() != `selection.length < 1` ||
		strings[1].Text() != `a<!--b` {
		t.Fatalf("string inners = %#v", strings)
	}
}

func TestVueHTMLAttributeNamesIncludeArgumentsAndModifiers(t *testing.T) {
	source := `<sw-card :title.sync="title" v-bind:count.number="count" @save.stop="save" v-on:close.once="close" #footer />`
	result := Parse(source)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if got := result.Tree.Root.Text(); got != source {
		t.Fatalf("lossless violated: %q", got)
	}
	attributes := findNodes(result.Tree.Root, syntax.HtmlAttribute)
	if len(attributes) != 5 {
		t.Fatalf("attributes = %#v", attributes)
	}
	want := []string{
		":title.sync",
		"v-bind:count.number",
		"@save.stop",
		"v-on:close.once",
		"#footer",
	}
	for index, attribute := range attributes {
		var name string
		for token := range attribute.ChildTokens() {
			if token.Kind() == syntax.TkWord {
				name = token.Text()
				break
			}
		}
		if name != want[index] {
			t.Fatalf("attribute %d name = %q, want %q", index, name, want[index])
		}
	}
}

func TestHTMLAttributeNamesReclassifyTwigKeywords(t *testing.T) {
	source := `<th :style="{ width: column.width }" @click="select($event)" block="name" v-bind:for="value"></th>`
	result := Parse(source)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}
	if got := result.Tree.Root.Text(); got != source {
		t.Fatalf("lossless violated: %q", got)
	}
	attributes := findNodes(result.Tree.Root, syntax.HtmlAttribute)
	if len(attributes) != 4 {
		t.Fatalf("attributes = %#v", attributes)
	}
	want := []string{":style", "@click", "block", "v-bind:for"}
	for index, attribute := range attributes {
		var name string
		for token := range attribute.ChildTokens() {
			if token.Kind() == syntax.TkWord {
				name = token.Text()
				break
			}
		}
		if name != want[index] {
			t.Fatalf("attribute %d name = %q, want %q", index, name, want[index])
		}
	}
}

// TestParserPredicates covers atTwigBlockOpen / atTwigTag / atSet on the parser.
func TestParserPredicates(t *testing.T) {
	tokens, _ := buildTokens(
		tok{syntax.TkCurlyPercentMinus, "{%-"},
		tok{syntax.TkWhitespace, " "},
		tok{syntax.TkIf, "if"},
	)
	p := newParser(tokens)
	if !p.atTwigBlockOpen() {
		t.Fatal("atTwigBlockOpen should be true at {%-")
	}
	if p.atTwigVarOpen() || p.atTwigCommentOpen() {
		t.Fatal("should not be at var/comment open")
	}
	if !p.atTwigTag(syntax.TkIf) {
		t.Fatal("atTwigTag(TkIf) should be true for {%- if")
	}
	if p.atTwigTag(syntax.TkEndif) {
		t.Fatal("atTwigTag(TkEndif) should be false")
	}
	// clean up markers-free parser: nothing to complete.
}

func TestComponentBlockAcceptsQuotedName(t *testing.T) {
	result := Parse(
		`{% component 'Alert' with {message: 'Hi'} %}` +
			`Hello{% endcomponent %}`,
	)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if nodes := findNodes(
		result.Tree.Root,
		syntax.TwigComponentStartingBlock,
	); len(nodes) != 1 {
		t.Fatalf("component starting blocks = %d, want 1", len(nodes))
	}
	if nodes := findNodes(
		result.Tree.Root,
		syntax.TwigLiteralString,
	); len(nodes) < 2 {
		t.Fatalf("string literals = %d, want at least 2", len(nodes))
	}
}

func TestFunctionCallAcceptsModernColonNamedArguments(t *testing.T) {
	for _, source := range []string{
		`{{ include('card.html.twig', with_context: false) }}`,
		`{{ include('card.html.twig', with_context = false) }}`,
	} {
		result := Parse(source)
		if len(result.Errors) != 0 {
			t.Fatalf("%s: unexpected errors: %v", source, result.Errors)
		}
		if nodes := findNodes(
			result.Tree.Root,
			syntax.TwigNamedArgument,
		); len(nodes) != 1 {
			t.Fatalf(
				"%s: named arguments = %d, want 1",
				source,
				len(nodes),
			)
		}
	}
}

func findNodes(root *syntax.Node, kind syntax.Kind) []*syntax.Node {
	var result []*syntax.Node
	for element := range root.Descendants() {
		node, ok := element.(*syntax.Node)
		if ok && node.Kind() == kind {
			result = append(result, node)
		}
	}
	return result
}
