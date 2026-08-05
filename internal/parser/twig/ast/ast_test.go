package ast

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/twig/parser"
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// parseNoErr parses src, fails the test on any parse error, and returns the root.
func parseNoErr(t *testing.T, src string) *syntax.Node {
	t.Helper()
	res := parser.Parse(src)
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected parse errors for %q: %v", src, res.Errors)
	}
	return res.Tree.Root
}

// findNode returns the first descendant node (preorder, including root) of the
// given kind, or nil.
func findNode(root *syntax.Node, kind syntax.Kind) *syntax.Node {
	for el := range root.Descendants() {
		if n, ok := el.(*syntax.Node); ok && n.Kind() == kind {
			return n
		}
	}
	return nil
}

func mustToken(t *testing.T, tok *syntax.Token, want string) {
	t.Helper()
	if tok == nil {
		t.Fatalf("expected token %q, got nil", want)
	}
	if tok.Text() != want {
		t.Fatalf("token text = %q, want %q", tok.Text(), want)
	}
}

func TestTwigBlock(t *testing.T) {
	root := parseNoErr(t, `{% block content %}hello{% endblock %}`)
	blk, ok := CastTwigBlock(findNode(root, syntax.TwigBlock))
	if !ok {
		t.Fatal("no TwigBlock")
	}
	mustToken(t, blk.Name(), "content")

	sb, ok := blk.StartingBlock()
	if !ok {
		t.Fatal("no starting block")
	}
	mustToken(t, sb.Name(), "content")
	// parent link back to the block
	if pb, ok := sb.TwigBlock(); !ok || pb.Syntax() != blk.Syntax() {
		t.Fatal("starting block parent link broken")
	}

	if _, ok := blk.Body(); !ok {
		t.Fatal("no body")
	}

	eb, ok := blk.EndingBlock()
	if !ok {
		t.Fatal("no ending block")
	}
	if pb, ok := eb.TwigBlock(); !ok || pb.Syntax() != blk.Syntax() {
		t.Fatal("ending block parent link broken")
	}
}

func TestHtmlTagAndAttributes(t *testing.T) {
	root := parseNoErr(t, `<div class="hello">world {{ 42 }}</div>`)
	tag, ok := CastHtmlTag(findNode(root, syntax.HtmlTag))
	if !ok {
		t.Fatal("no HtmlTag")
	}
	mustToken(t, tag.Name(), "div")
	if tag.IsSelfClosing() {
		t.Fatal("div should not be self-closing")
	}
	if tag.IsTwigComponent() {
		t.Fatal("div is not a twig component")
	}
	if _, ok := tag.Body(); !ok {
		t.Fatal("no body")
	}
	if _, ok := tag.EndingTag(); !ok {
		t.Fatal("no ending tag")
	}

	attrs := tag.Attributes()
	if len(attrs) != 1 {
		t.Fatalf("attributes = %d, want 1", len(attrs))
	}
	attr := attrs[0]
	mustToken(t, attr.Name(), "class")

	val, ok := attr.Value()
	if !ok {
		t.Fatal("attribute has no value")
	}
	inner, ok := val.GetInner()
	if !ok {
		t.Fatal("value has no inner")
	}
	if inner.Syntax().Text() != "hello" {
		t.Fatalf("inner = %q, want hello", inner.Syntax().Text())
	}
	mustToken(t, val.GetOpeningQuote(), `"`)
	mustToken(t, val.GetClosingQuote(), `"`)

	// grandparent link: attribute -> attribute list -> starting tag
	st, ok := attr.HtmlTag()
	if !ok {
		t.Fatal("attribute grandparent link broken")
	}
	stFromTag, _ := tag.StartingTag()
	if st.Syntax() != stFromTag.Syntax() {
		t.Fatal("attribute grandparent is not the starting tag")
	}
}

func TestSelfClosingAndEndingTag(t *testing.T) {
	root := parseNoErr(t, `<br/>`)
	tag, _ := CastHtmlTag(findNode(root, syntax.HtmlTag))
	if !tag.IsSelfClosing() {
		t.Fatal("br/ should be self-closing")
	}

	root = parseNoErr(t, `<span>x</span>`)
	et, ok := CastHtmlEndingTag(findNode(root, syntax.HtmlEndingTag))
	if !ok {
		t.Fatal("no ending tag")
	}
	mustToken(t, et.Name(), "span")
	if et.IsTwigComponent() {
		t.Fatal("span ending tag is not a twig component")
	}
	if _, ok := et.HtmlTag(); !ok {
		t.Fatal("ending tag parent link broken")
	}
}

func TestTwigComponent(t *testing.T) {
	root := parseNoErr(t, `<twig:my:component foo="bar"></twig:my:component>`)
	tag, ok := CastHtmlTag(findNode(root, syntax.HtmlTag))
	if !ok {
		t.Fatal("no HtmlTag")
	}
	if !tag.IsTwigComponent() {
		t.Fatal("expected twig component tag")
	}
	st, _ := tag.StartingTag()
	if !st.IsTwigComponent() {
		t.Fatal("starting tag should be a twig component")
	}
	mustToken(t, st.Name(), "twig:my:component")
	if stTag, ok := st.HtmlTag(); !ok || stTag.Syntax() != tag.Syntax() {
		t.Fatal("starting tag parent link broken")
	}

	et, ok := CastHtmlEndingTag(findNode(root, syntax.HtmlEndingTag))
	if !ok {
		t.Fatal("no ending tag")
	}
	if !et.IsTwigComponent() {
		t.Fatal("ending tag should be a twig component")
	}
	mustToken(t, et.Name(), "twig:my:component")
}

func TestTwigBinaryExpression(t *testing.T) {
	root := parseNoErr(t, `{{ 1 + 2 }}`)
	be, ok := CastTwigBinaryExpression(findNode(root, syntax.TwigBinaryExpression))
	if !ok {
		t.Fatal("no binary expression")
	}
	mustToken(t, be.Operator(), "+")

	lhs, ok := be.LhsExpression()
	if !ok {
		t.Fatal("no lhs")
	}
	if lhs.Syntax().Text() != " 1" {
		t.Fatalf("lhs text = %q, want ' 1'", lhs.Syntax().Text())
	}
	rhs, ok := be.RhsExpression()
	if !ok {
		t.Fatal("no rhs")
	}
	if rhs.Syntax().Text() != " 2" {
		t.Fatalf("rhs text = %q, want ' 2'", rhs.Syntax().Text())
	}
	// lhs and rhs must be distinct nodes
	if lhs.Syntax() == rhs.Syntax() {
		t.Fatal("lhs and rhs are the same node")
	}
}

func TestTwigLiteralString(t *testing.T) {
	root := parseNoErr(t, `{{ "hello" }}`)
	ls, ok := CastTwigLiteralString(findNode(root, syntax.TwigLiteralString))
	if !ok {
		t.Fatal("no literal string")
	}
	inner, ok := ls.GetInner()
	if !ok {
		t.Fatal("no inner")
	}
	if inner.Syntax().Text() != "hello" {
		t.Fatalf("inner = %q, want hello", inner.Syntax().Text())
	}
	mustToken(t, ls.GetOpeningQuote(), `"`)
	mustToken(t, ls.GetClosingQuote(), `"`)
	// opening and closing must be different tokens
	if ls.GetOpeningQuote() == ls.GetClosingQuote() {
		t.Fatal("opening and closing quote are the same token")
	}
}

func TestTwigLiteralStringSingleQuote(t *testing.T) {
	root := parseNoErr(t, `{{ 'x' }}`)
	ls, _ := CastTwigLiteralString(findNode(root, syntax.TwigLiteralString))
	mustToken(t, ls.GetOpeningQuote(), `'`)
	mustToken(t, ls.GetClosingQuote(), `'`)
}

func TestTwigLiteralStringInterpolation(t *testing.T) {
	root := parseNoErr(t, `{{ "a #{b} c" }}`)
	inner, ok := CastTwigLiteralStringInner(findNode(root, syntax.TwigLiteralStringInner))
	if !ok {
		t.Fatal("no inner")
	}
	interps := inner.GetInterpolations()
	if len(interps) != 1 {
		t.Fatalf("interpolations = %d, want 1", len(interps))
	}
	if interps[0].Syntax().Text() != "#{b}" {
		t.Fatalf("interpolation text = %q, want #{b}", interps[0].Syntax().Text())
	}
}

func TestHtmlString(t *testing.T) {
	root := parseNoErr(t, `<a href="index"></a>`)
	hs, ok := CastHtmlString(findNode(root, syntax.HtmlString))
	if !ok {
		t.Fatal("no html string")
	}
	inner, ok := hs.GetInner()
	if !ok {
		t.Fatal("no inner")
	}
	if inner.Syntax().Text() != "index" {
		t.Fatalf("inner = %q, want index", inner.Syntax().Text())
	}
	mustToken(t, hs.GetOpeningQuote(), `"`)
	mustToken(t, hs.GetClosingQuote(), `"`)
}

func TestTwigExtends(t *testing.T) {
	root := parseNoErr(t, `{% extends "base.html" %}`)
	ext, ok := CastTwigExtends(findNode(root, syntax.TwigExtends))
	if !ok {
		t.Fatal("no extends")
	}
	mustToken(t, ext.GetExtendsKeyword(), "extends")
}

func TestTwigVar(t *testing.T) {
	root := parseNoErr(t, `{{ name }}`)
	v, ok := CastTwigVar(findNode(root, syntax.TwigVar))
	if !ok {
		t.Fatal("no var")
	}
	expr, ok := v.GetExpression()
	if !ok {
		t.Fatal("no expression")
	}
	if expr.Syntax().Text() != " name" {
		t.Fatalf("expr text = %q, want ' name'", expr.Syntax().Text())
	}
}

func TestTwigLiteralName(t *testing.T) {
	root := parseNoErr(t, `{{ name }}`)
	ln, ok := CastTwigLiteralName(findNode(root, syntax.TwigLiteralName))
	if !ok {
		t.Fatal("no literal name")
	}
	mustToken(t, ln.GetName(), "name")
}

func TestTwigFilter(t *testing.T) {
	root := parseNoErr(t, `{{ user|upper }}`)
	f, ok := CastTwigFilter(findNode(root, syntax.TwigFilter))
	if !ok {
		t.Fatal("no filter")
	}
	operand, ok := f.Operand()
	if !ok {
		t.Fatal("no operand")
	}
	if operand.Syntax().Text() != " user" {
		t.Fatalf("operand text = %q, want ' user'", operand.Syntax().Text())
	}
	filt, ok := f.Filter()
	if !ok {
		t.Fatal("no filter operand")
	}
	if filt.Syntax().Text() != "upper" {
		t.Fatalf("filter text = %q, want upper", filt.Syntax().Text())
	}
	// operand and filter must be distinct
	if operand.Syntax() == filt.Syntax() {
		t.Fatal("operand and filter are the same node")
	}
}

func TestTwigFunctionCall(t *testing.T) {
	root := parseNoErr(t, `{{ path('home', {id: 1}) }}`)
	fc, ok := CastTwigFunctionCall(findNode(root, syntax.TwigFunctionCall))
	if !ok {
		t.Fatal("no function call")
	}
	name, ok := fc.NameOperand()
	if !ok {
		t.Fatal("no name operand")
	}
	// name operand contains the TwigLiteralName "path"
	ln, ok := CastTwigLiteralName(findNode(name.Syntax(), syntax.TwigLiteralName))
	if !ok {
		t.Fatal("name operand has no literal name")
	}
	mustToken(t, ln.GetName(), "path")

	args, ok := fc.Arguments()
	if !ok {
		t.Fatal("no arguments")
	}
	if args.Syntax().Text() != `('home', {id: 1})` {
		t.Fatalf("arguments text = %q", args.Syntax().Text())
	}
	// two argument expressions
	count := 0
	for child := range args.Syntax().ChildNodes() {
		if child.Kind() == syntax.TwigExpression {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("argument expressions = %d, want 2", count)
	}
}

func TestLudtwigDirectives(t *testing.T) {
	root := parseNoErr(t, `{# ludtwig-ignore rule-a rule-b #}`)
	dir, ok := CastLudtwigDirectiveIgnore(findNode(root, syntax.LudtwigDirectiveIgnore))
	if !ok {
		t.Fatal("no ignore directive")
	}
	rules := dir.GetRules()
	if len(rules) != 2 || rules[0] != "rule-a" || rules[1] != "rule-b" {
		t.Fatalf("rules = %v, want [rule-a rule-b]", rules)
	}

	rl, ok := CastLudtwigDirectiveRuleList(findNode(root, syntax.LudtwigDirectiveRuleList))
	if !ok {
		t.Fatal("no rule list")
	}
	names := rl.GetRuleNames()
	if len(names) != 2 || names[0] != "rule-a" || names[1] != "rule-b" {
		t.Fatalf("rule names = %v, want [rule-a rule-b]", names)
	}
}

func TestLudtwigDirectiveFileIgnore(t *testing.T) {
	root := parseNoErr(t, `{# ludtwig-ignore-file rule-x #}`)
	dir, ok := CastLudtwigDirectiveFileIgnore(findNode(root, syntax.LudtwigDirectiveFileIgnore))
	if !ok {
		t.Fatal("no file-ignore directive")
	}
	rules := dir.GetRules()
	if len(rules) != 1 || rules[0] != "rule-x" {
		t.Fatalf("rules = %v, want [rule-x]", rules)
	}
}

func TestLudtwigDirectiveEmptyRules(t *testing.T) {
	root := parseNoErr(t, `{# ludtwig-ignore-file #}`)
	dir, ok := CastLudtwigDirectiveFileIgnore(findNode(root, syntax.LudtwigDirectiveFileIgnore))
	if !ok {
		t.Fatal("no file-ignore directive")
	}
	if len(dir.GetRules()) != 0 {
		t.Fatalf("rules = %v, want empty", dir.GetRules())
	}
}

func TestCastRejectsWrongKind(t *testing.T) {
	root := parseNoErr(t, `{{ 1 }}`)
	varNode := findNode(root, syntax.TwigVar)
	if _, ok := CastTwigBlock(varNode); ok {
		t.Fatal("CastTwigBlock accepted a TwigVar node")
	}
	if _, ok := CastTwigVar(nil); ok {
		t.Fatal("CastTwigVar accepted nil")
	}
}

func TestTrimHelpers(t *testing.T) {
	cases := []struct {
		src            string
		leading, trail Trim
	}{
		{`{% block foo %}{% endblock %}`, TrimNone, TrimNone},
		{`{%- block foo -%}{%- endblock -%}`, TrimAll, TrimAll},
		{`{%~ block foo ~%}{%~ endblock ~%}`, TrimKeepNewlines, TrimKeepNewlines},
		{`{%~ block foo -%}{% endblock %}`, TrimKeepNewlines, TrimAll},
	}
	for _, c := range cases {
		root := parseNoErr(t, c.src)
		blk, _ := CastTwigBlock(findNode(root, syntax.TwigBlock))
		sb, _ := blk.StartingBlock()
		if got := LeadingTrim(sb.Syntax()); got != c.leading {
			t.Errorf("%q: leading = %d, want %d", c.src, got, c.leading)
		}
		if got := TrailingTrim(sb.Syntax()); got != c.trail {
			t.Errorf("%q: trailing = %d, want %d", c.src, got, c.trail)
		}
	}
}

func TestTrimVarDelimiter(t *testing.T) {
	root := parseNoErr(t, `{{- name -}}`)
	v, _ := CastTwigVar(findNode(root, syntax.TwigVar))
	if got := LeadingTrim(v.Syntax()); got != TrimAll {
		t.Errorf("var leading = %d, want TrimAll", got)
	}
	if got := TrailingTrim(v.Syntax()); got != TrimAll {
		t.Errorf("var trailing = %d, want TrimAll", got)
	}
}

func TestTrimCommentDelimiter(t *testing.T) {
	root := parseNoErr(t, `{#~ a comment ~#}`)
	c, ok := CastTwigComment(findNode(root, syntax.TwigComment))
	if !ok {
		t.Fatal("no comment")
	}
	if got := LeadingTrim(c.Syntax()); got != TrimKeepNewlines {
		t.Errorf("comment leading = %d, want TrimKeepNewlines", got)
	}
	if got := TrailingTrim(c.Syntax()); got != TrimKeepNewlines {
		t.Errorf("comment trailing = %d, want TrimKeepNewlines", got)
	}
}
