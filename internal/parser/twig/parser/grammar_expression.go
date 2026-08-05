package parser

import (
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// twigExpressionRecoverySet is the master synchronization set for twig
// expressions (port of TWIG_EXPRESSION_RECOVERY_SET, expression.rs:13-71). It
// contains every closing delimiter, every literal-start token and every
// binary/unary operator token. parser.recover(set) skips tokens until one of
// these appears, so recovery synchronizes at the next known point.
var twigExpressionRecoverySet = []syntax.Kind{
	syntax.TkSinglePipe,
	syntax.TkCloseParenthesis,
	syntax.TkCloseCurly,
	syntax.TkCloseSquare,
	syntax.TkColon,
	syntax.TkCloseCurlyCurly,
	syntax.TkMinusCloseCurlyCurly,
	syntax.TkTildeCloseCurlyCurly,
	syntax.TkPercentCurly,
	syntax.TkMinusPercentCurly,
	syntax.TkTildePercentCurly,
	syntax.TkOpenParenthesis,
	// literals
	syntax.TkNumber,
	syntax.TkDoubleQuotes,
	syntax.TkSingleQuotes,
	syntax.TkOpenSquare,
	syntax.TkNull,
	syntax.TkTrue,
	syntax.TkFalse,
	syntax.TkOpenCurly,
	// operators
	syntax.TkOr,
	syntax.TkDoublePipe,
	syntax.TkAnd,
	syntax.TkDoubleAmpersand,
	syntax.TkBinaryOr,
	syntax.TkBinaryXor,
	syntax.TkBinaryAnd,
	syntax.TkDoubleEqual,
	syntax.TkExclamationMarkEquals,
	syntax.TkLessThanEqualGreaterThan,
	syntax.TkLessThan,
	syntax.TkGreaterThan,
	syntax.TkGreaterThanEqual,
	syntax.TkLessThanEqual,
	syntax.TkNot,
	syntax.TkNotIn,
	syntax.TkIn,
	syntax.TkMatches,
	syntax.TkStartsWith,
	syntax.TkEndsWith,
	syntax.TkTripleEqual,
	syntax.TkExclamationMarkDoubleEquals,
	syntax.TkDoubleDot,
	syntax.TkTripleDot,
	syntax.TkPlus,
	syntax.TkMinus,
	syntax.TkTilde,
	syntax.TkStar,
	syntax.TkForwardSlash,
	syntax.TkDoubleForwardSlash,
	syntax.TkPercent,
	syntax.TkIs,
	syntax.TkIsNot,
	syntax.TkDoubleStar,
	syntax.TkDoubleQuestionMark,
}

// binaryBindingPower returns the (left, right) binding powers of kind as a
// binary operator, or (0, 0, false) if it is not a binary operator. Ported from
// the Operator impl (expression.rs:90-125). Left-associative when left < right;
// right-associative when right < left.
func binaryBindingPower(kind syntax.Kind) (leftBP, rightBP uint8, ok bool) {
	switch kind {
	// left associative
	case syntax.TkOr, syntax.TkDoublePipe: // '||' is not official twig but still parse it
		return 5, 6, true
	case syntax.TkAnd, syntax.TkDoubleAmpersand: // '&&' is not official twig but still parse it
		return 10, 11, true
	case syntax.TkBinaryOr:
		return 14, 15, true
	case syntax.TkBinaryXor:
		return 16, 17, true
	case syntax.TkBinaryAnd:
		return 18, 19, true
	case syntax.TkDoubleEqual,
		syntax.TkExclamationMarkEquals,
		syntax.TkLessThanEqualGreaterThan,
		syntax.TkLessThan,
		syntax.TkGreaterThan,
		syntax.TkGreaterThanEqual,
		syntax.TkLessThanEqual,
		syntax.TkIn,
		syntax.TkNotIn,
		syntax.TkMatches,
		syntax.TkStartsWith,
		syntax.TkEndsWith,
		syntax.TkTripleEqual, // not official twig but still parse `===` and `!==`
		syntax.TkExclamationMarkDoubleEquals:
		return 20, 21, true
	case syntax.TkDoubleDot:
		return 25, 26, true
	case syntax.TkPlus, syntax.TkMinus:
		return 30, 31, true
	case syntax.TkTilde:
		return 40, 41, true
	case syntax.TkStar, syntax.TkForwardSlash, syntax.TkDoubleForwardSlash, syntax.TkPercent:
		return 60, 61, true
	case syntax.TkIs, syntax.TkIsNot:
		return 100, 101, true
	// right associative
	case syntax.TkDoubleStar:
		return 121, 120, true
	case syntax.TkDoubleQuestionMark:
		return 151, 150, true
	default:
		return 0, 0, false
	}
}

// unaryBindingPower returns the right binding power of kind as a prefix unary
// operator, or (0, false) if it is not a unary operator. Ported from the
// Operator impl (expression.rs:127-134).
func unaryBindingPower(kind syntax.Kind) (rightBP uint8, ok bool) {
	switch kind {
	case syntax.TkNot:
		return 51, true
	case syntax.TkPlus, syntax.TkMinus:
		return 201, true
	case syntax.TkTripleDot:
		return 211, true
	default:
		return 0, false
	}
}

// parseTwigExpression is the entry point for twig expression parsing. Port of
// parse_twig_expression (expression.rs:73).
func parseTwigExpression(p *parser) (completedMarker, bool) {
	return parseTwigExpressionBindingPower(p, 0)
}

// parseTwigExpressionBindingPower is the Pratt parser core. Port of
// parse_twig_expression_binding_power (expression.rs:137).
func parseTwigExpressionBindingPower(p *parser, minimumBindingPower uint8) (completedMarker, bool) {
	lhs, ok := parseTwigExpressionLhs(p)
	if !ok {
		return completedMarker{}, false
	}

	// wrap lhs in expression
	m := p.precede(lhs)
	lhs = p.complete(m, syntax.TwigExpression)

	lhs = parseTwigExpressionBindingPowerRhs(p, minimumBindingPower, lhs)
	return lhs, true
}

// parseTwigExpressionBindingPowerRhs runs the binary RHS loop plus (at
// minimum_binding_power == 0) the ternary handling. Port of
// parse_twig_expression_binding_power_rhs (expression.rs:151).
func parseTwigExpressionBindingPowerRhs(p *parser, minimumBindingPower uint8, lhs completedMarker) completedMarker {
	isBinary := false
	for {
		tok := p.peekToken()
		if tok == nil {
			break
		}
		leftBP, rightBP, isOp := binaryBindingPower(tok.Kind)
		if !isOp {
			break
		}
		if leftBP < minimumBindingPower {
			break
		}

		// Eat the operator's token.
		p.bump()
		isBinary = true

		// recurse
		m := p.precede(lhs)
		_, parsedRhs := parseTwigExpressionBindingPower(p, rightBP)
		lhs = p.complete(m, syntax.TwigBinaryExpression)

		if !parsedRhs {
			break
		}
	}

	// wrap whole binary expression inside an expression
	if isBinary {
		m := p.precede(lhs)
		lhs = p.complete(m, syntax.TwigExpression)
	}

	// check for ternary operator (conditional expression) on top level
	if minimumBindingPower == 0 {
		if m, ok := parseConditionalExpression(p, lhs); ok {
			lhs = m
		}
	}

	return lhs
}

// parseConditionalExpression parses `a ? b : c`, `a ? b` and Elvis `a ?: c`.
// Port of parse_conditional_expression (expression.rs:195).
func parseConditionalExpression(p *parser, lhs completedMarker) (completedMarker, bool) {
	if !p.at(syntax.TkQuestionMark) {
		return completedMarker{}, false
	}
	m := p.precede(lhs)
	p.bump()

	// truthy expression
	if _, ok := parseTwigExpressionBindingPower(p, 0); !ok && !p.at(syntax.TkColon) {
		p.addError(newErrorBuilder("twig expression or ':'"))
		p.recover([]syntax.Kind{
			syntax.TkColon,
			syntax.TkCloseCurlyCurly,
			syntax.TkMinusCloseCurlyCurly,
			syntax.TkTildeCloseCurlyCurly,
			syntax.TkPercentCurly,
			syntax.TkMinusPercentCurly,
			syntax.TkTildePercentCurly,
		})
	}

	if p.at(syntax.TkColon) {
		p.bump()

		// falsy expression
		if _, ok := parseTwigExpressionBindingPower(p, 0); !ok {
			p.addError(newErrorBuilder("twig expression"))
			p.recover([]syntax.Kind{
				syntax.TkCloseCurlyCurly,
				syntax.TkMinusCloseCurlyCurly,
				syntax.TkTildeCloseCurlyCurly,
				syntax.TkPercentCurly,
				syntax.TkMinusPercentCurly,
				syntax.TkTildePercentCurly,
			})
		}
	}

	conditionalM := p.complete(m, syntax.TwigConditionalExpression)
	outer := p.precede(conditionalM)
	return p.complete(outer, syntax.TwigExpression), true
}

// parseTwigExpressionLhs parses the left-hand side: paren expression / arrow
// function, unary expression, or literal + postfix operators. Port of
// parse_twig_expression_lhs (expression.rs:241).
func parseTwigExpressionLhs(p *parser) (completedMarker, bool) {
	if p.at(syntax.TkOpenParenthesis) {
		m, isParenExpression := parseParenExpression(p)

		if isParenExpression {
			node := p.complete(m, syntax.TwigParenthesesExpression)
			// including postfix operators
			return parsePostfixOperators(p, node), true
		}
		return parseParenArrowFunction(p, m), true
	} else if p.atSet([]syntax.Kind{syntax.TkMinus, syntax.TkPlus, syntax.TkNot, syntax.TkTripleDot}) {
		return parseUnaryExpression(p), true
	}
	// including postfix operators
	node, ok := parseTwigLiteral(p)
	if !ok {
		return completedMarker{}, false
	}
	return parsePostfixOperators(p, node), true
}

// parseParenExpression disambiguates parenthesized expression vs arrow-function
// parameter list. Returns (marker, isParenExpression). Port of
// parse_paren_expression (expression.rs:260).
func parseParenExpression(p *parser) (*marker, bool) {
	// debug_assert!(parser.at(T!["("]));

	m := p.start()
	p.bump()

	// first check for non existent unary expression token to be an arrow function
	tok := p.peekToken()
	tokIsNonUnary := false
	if tok != nil {
		if _, isUnary := unaryBindingPower(tok.Kind); !isUnary {
			tokIsNonUnary = true
		}
	}

	if tokIsNonUnary {
		// check for a simple name literal
		if litM, ok := parseTwigName(p); ok {
			if p.at(syntax.TkComma) || p.atFollowing([]syntax.Kind{syntax.TkCloseParenthesis, syntax.TkEqualGreaterThan}) {
				return m, false // found arrow function
			}

			// check for optional function call
			if p.at(syntax.TkOpenParenthesis) {
				litM = parseTwigFunction(p, litM)
			}

			// including postfix operators
			litM = parsePostfixOperators(p, litM)

			// wrap it in expression to continue
			exprM := p.precede(litM)
			lhs := p.complete(exprM, syntax.TwigExpression)
			parseTwigExpressionBindingPowerRhs(p, 0, lhs)
		} else {
			// found a real expression, so it can't be an arrow function anymore
			parseTwigExpressionBindingPower(p, 0)
		}
	} else {
		// found a real expression, so it can't be an arrow function at this point anymore
		parseTwigExpressionBindingPower(p, 0)
	}

	p.expect(syntax.TkCloseParenthesis, twigExpressionRecoverySet)
	return m, true
}

// parseParenArrowFunction parses the remaining arguments of an arrow function
// parameter list, then the arrow function itself. Port of
// parse_paren_arrow_function (expression.rs:302).
func parseParenArrowFunction(p *parser, m *marker) completedMarker {
	// debug_assert!(parser.at(T![","]) || parser.at(T![")"]));

	// parse any remaining arguments
	parseMany(
		p,
		func(p *parser) bool { return p.at(syntax.TkCloseParenthesis) },
		func(p *parser) {
			p.expect(syntax.TkComma, twigExpressionRecoverySet)
			parseTwigName(p)
		},
	)

	p.expect(syntax.TkCloseParenthesis, twigExpressionRecoverySet)
	lastNode := p.complete(m, syntax.TwigArguments)

	return parseTwigArrowFunction(p, lastNode)
}

// parseUnaryExpression parses a prefix unary expression. Port of
// parse_unary_expression (expression.rs:321).
func parseUnaryExpression(p *parser) completedMarker {
	// debug_assert!(parser.at_set(&[T!["-"], T!["+"], T!["not"], T!["..."]]));

	m := p.start()
	// Eat the operator's token.
	opToken := p.bump()
	rightBindingPower, ok := unaryBindingPower(opToken.Kind)
	if !ok {
		panic("'-', '+', 'not' and '...' should have a binding power")
	}

	parseTwigExpressionBindingPower(p, rightBindingPower)

	return p.complete(m, syntax.TwigUnaryExpression)
}
