package ast

import (
	"iter"

	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// --- TwigBlock ---

// Name returns the block name token, delegating to the starting block.
func (x TwigBlock) Name() *syntax.Token {
	sb, ok := x.StartingBlock()
	if !ok {
		return nil
	}
	return sb.Name()
}

// StartingBlock returns the starting block child.
func (x TwigBlock) StartingBlock() (TwigStartingBlock, bool) {
	return CastTwigStartingBlock(firstChildNode(x.n, syntax.TwigStartingBlock))
}

// Body returns the block body child.
func (x TwigBlock) Body() (Body, bool) {
	return CastBody(firstChildNode(x.n, syntax.Body))
}

// EndingBlock returns the ending block child.
func (x TwigBlock) EndingBlock() (TwigEndingBlock, bool) {
	return CastTwigEndingBlock(firstChildNode(x.n, syntax.TwigEndingBlock))
}

// --- TwigStartingBlock ---

// Name returns the first TK_WORD token (the block name).
func (x TwigStartingBlock) Name() *syntax.Token {
	return childToken(x.n, syntax.TkWord)
}

// TwigBlock returns the parent complete twig block.
func (x TwigStartingBlock) TwigBlock() (TwigBlock, bool) {
	return CastTwigBlock(x.n.Parent())
}

// --- TwigEndingBlock ---

// TwigBlock returns the parent complete twig block.
func (x TwigEndingBlock) TwigBlock() (TwigBlock, bool) {
	return CastTwigBlock(x.n.Parent())
}

// --- HtmlTag ---

// Name returns the tag name token, via the starting tag.
func (x HtmlTag) Name() *syntax.Token {
	st, ok := x.StartingTag()
	if !ok {
		return nil
	}
	return st.Name()
}

// IsSelfClosing reports whether the tag has no ending tag.
func (x HtmlTag) IsSelfClosing() bool {
	_, ok := x.EndingTag()
	return !ok
}

// Attributes iterates the tag's attributes via the starting tag.
func (x HtmlTag) Attributes() iter.Seq[HtmlAttribute] {
	return func(yield func(HtmlAttribute) bool) {
		startingTag := firstChildNode(x.n, syntax.HtmlStartingTag)
		if startingTag == nil {
			return
		}
		iterateHTMLAttributes(
			firstChildNode(startingTag, syntax.HtmlAttributeList),
			yield,
		)
	}
}

// IsTwigComponent reports whether the tag is a twig component, e.g.
// '<twig:my:component />'.
func (x HtmlTag) IsTwigComponent() bool {
	st, ok := x.StartingTag()
	return ok && st.IsTwigComponent()
}

// StartingTag returns the starting tag child.
func (x HtmlTag) StartingTag() (HtmlStartingTag, bool) {
	return CastHtmlStartingTag(firstChildNode(x.n, syntax.HtmlStartingTag))
}

// Body returns the tag body child.
func (x HtmlTag) Body() (Body, bool) {
	return CastBody(firstChildNode(x.n, syntax.Body))
}

// EndingTag returns the ending tag child.
func (x HtmlTag) EndingTag() (HtmlEndingTag, bool) {
	return CastHtmlEndingTag(firstChildNode(x.n, syntax.HtmlEndingTag))
}

// --- HtmlStartingTag ---

// Name returns the first TK_WORD or TK_TWIG_COMPONENT_NAME child token.
func (x HtmlStartingTag) Name() *syntax.Token {
	return firstTokenWithKind(x.n, syntax.TkWord, syntax.TkTwigComponentName)
}

// Attributes iterates the attributes held by the HTML_ATTRIBUTE_LIST child.
func (x HtmlStartingTag) Attributes() iter.Seq[HtmlAttribute] {
	return func(yield func(HtmlAttribute) bool) {
		iterateHTMLAttributes(
			firstChildNode(x.n, syntax.HtmlAttributeList),
			yield,
		)
	}
}

func iterateHTMLAttributes(
	list *syntax.Node,
	yield func(HtmlAttribute) bool,
) {
	if list == nil {
		return
	}
	cursor := list.ChildNodeCursor()
	for cursor.Next() {
		attribute, ok := CastHtmlAttribute(cursor.Node())
		if ok && !yield(attribute) {
			return
		}
	}
}

// HtmlTag returns the parent complete html tag.
func (x HtmlStartingTag) HtmlTag() (HtmlTag, bool) {
	return CastHtmlTag(x.n.Parent())
}

// IsTwigComponent reports whether the tag is a twig component, detected by the
// presence of a TK_TWIG_COMPONENT_NAME token.
func (x HtmlStartingTag) IsTwigComponent() bool {
	return childToken(x.n, syntax.TkTwigComponentName) != nil
}

// --- HtmlAttribute ---

// Name returns the attribute name token (left of the equal sign).
func (x HtmlAttribute) Name() *syntax.Token {
	return childToken(x.n, syntax.TkWord)
}

// Value returns the attribute value string, if present.
func (x HtmlAttribute) Value() (HtmlString, bool) {
	return CastHtmlString(firstChildNode(x.n, syntax.HtmlString))
}

// HtmlTag returns the parent starting html tag. The direct parent is the
// HTML_ATTRIBUTE_LIST, so the starting tag is the grandparent.
func (x HtmlAttribute) HtmlTag() (HtmlStartingTag, bool) {
	parent := x.n.Parent()
	if parent == nil {
		return HtmlStartingTag{}, false
	}
	return CastHtmlStartingTag(parent.Parent())
}

// --- HtmlEndingTag ---

// Name returns the first TK_WORD or TK_TWIG_COMPONENT_NAME child token.
func (x HtmlEndingTag) Name() *syntax.Token {
	return firstTokenWithKind(x.n, syntax.TkWord, syntax.TkTwigComponentName)
}

// HtmlTag returns the parent complete html tag.
func (x HtmlEndingTag) HtmlTag() (HtmlTag, bool) {
	return CastHtmlTag(x.n.Parent())
}

// IsTwigComponent reports whether the tag is a twig component, e.g.
// '</twig:my:component>'.
func (x HtmlEndingTag) IsTwigComponent() bool {
	return childToken(x.n, syntax.TkTwigComponentName) != nil
}

// --- TwigBinaryExpression ---

// Operator returns the first non-trivia direct child token (the operator).
func (x TwigBinaryExpression) Operator() *syntax.Token {
	return firstNonTriviaToken(x.n)
}

// LhsExpression returns the first child castable to TwigExpression (left side).
func (x TwigBinaryExpression) LhsExpression() (TwigExpression, bool) {
	return CastTwigExpression(nthChildNode(x.n, syntax.TwigExpression, 0))
}

// RhsExpression returns the second TwigExpression child (right side).
func (x TwigBinaryExpression) RhsExpression() (TwigExpression, bool) {
	return CastTwigExpression(nthChildNode(x.n, syntax.TwigExpression, 1))
}

// --- LudtwigDirectiveRuleList ---

// GetRuleNames returns the texts of all TK_WORD child tokens.
func (x LudtwigDirectiveRuleList) GetRuleNames() []string {
	if x.n == nil {
		return nil
	}
	var out []string
	cursor := x.n.ChildTokenCursor()
	for cursor.Next() {
		tok := cursor.Token()
		if tok.Kind() == syntax.TkWord {
			out = append(out, tok.Text())
		}
	}
	return out
}

// --- LudtwigDirectiveFileIgnore ---

// GetRules returns the rule names from the child rule list, or empty.
func (x LudtwigDirectiveFileIgnore) GetRules() []string {
	if rl, ok := CastLudtwigDirectiveRuleList(firstChildNode(x.n, syntax.LudtwigDirectiveRuleList)); ok {
		return rl.GetRuleNames()
	}
	return nil
}

// --- LudtwigDirectiveIgnore ---

// GetRules returns the rule names from the child rule list, or empty.
func (x LudtwigDirectiveIgnore) GetRules() []string {
	if rl, ok := CastLudtwigDirectiveRuleList(firstChildNode(x.n, syntax.LudtwigDirectiveRuleList)); ok {
		return rl.GetRuleNames()
	}
	return nil
}

// --- TwigLiteralString ---

// GetInner returns the inner string node, if present.
func (x TwigLiteralString) GetInner() (TwigLiteralStringInner, bool) {
	return CastTwigLiteralStringInner(firstChildNode(x.n, syntax.TwigLiteralStringInner))
}

// GetOpeningQuote returns the first non-trivia token before the inner node.
func (x TwigLiteralString) GetOpeningQuote() *syntax.Token {
	return openingQuoteToken(x.n)
}

// GetClosingQuote returns the first non-trivia token after the inner node.
func (x TwigLiteralString) GetClosingQuote() *syntax.Token {
	return closingQuoteToken(x.n)
}

// --- TwigLiteralStringInner ---

// GetInterpolations returns all interpolation children.
func (x TwigLiteralStringInner) GetInterpolations() []TwigLiteralStringInterpolation {
	if x.n == nil {
		return nil
	}
	var out []TwigLiteralStringInterpolation
	cursor := x.n.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if it, ok := CastTwigLiteralStringInterpolation(child); ok {
			out = append(out, it)
		}
	}
	return out
}

// --- HtmlString ---

// GetInner returns the inner string node, if present.
func (x HtmlString) GetInner() (HtmlStringInner, bool) {
	return CastHtmlStringInner(firstChildNode(x.n, syntax.HtmlStringInner))
}

// GetOpeningQuote returns the first non-trivia token before the inner node.
func (x HtmlString) GetOpeningQuote() *syntax.Token {
	return openingQuoteToken(x.n)
}

// GetClosingQuote returns the first non-trivia token after the inner node.
func (x HtmlString) GetClosingQuote() *syntax.Token {
	return closingQuoteToken(x.n)
}

// --- TwigExtends ---

// GetExtendsKeyword returns the TK_EXTENDS token.
func (x TwigExtends) GetExtendsKeyword() *syntax.Token {
	return childToken(x.n, syntax.TkExtends)
}

// --- TwigVar ---

// GetExpression returns the expression child, if present.
func (x TwigVar) GetExpression() (TwigExpression, bool) {
	return CastTwigExpression(firstChildNode(x.n, syntax.TwigExpression))
}

// --- TwigLiteralName ---

// GetName returns the TK_WORD token (the name).
func (x TwigLiteralName) GetName() *syntax.Token {
	return childToken(x.n, syntax.TkWord)
}

// --- TwigFilter ---

// Operand returns the operand on the left side of the pipe `|`.
func (x TwigFilter) Operand() (TwigOperand, bool) {
	return CastTwigOperand(nthChildNode(x.n, syntax.TwigOperand, 0))
}

// Filter returns the operand on the right side of the pipe `|`. It contains a
// TwigLiteralName or TwigFunctionCall.
func (x TwigFilter) Filter() (TwigOperand, bool) {
	return CastTwigOperand(nthChildNode(x.n, syntax.TwigOperand, 1))
}

// --- TwigFunctionCall ---

// NameOperand returns the operand holding the function name (a TwigLiteralName).
func (x TwigFunctionCall) NameOperand() (TwigOperand, bool) {
	return CastTwigOperand(firstChildNode(x.n, syntax.TwigOperand))
}

// Arguments returns the function call arguments, if present.
func (x TwigFunctionCall) Arguments() (TwigArguments, bool) {
	return CastTwigArguments(firstChildNode(x.n, syntax.TwigArguments))
}
