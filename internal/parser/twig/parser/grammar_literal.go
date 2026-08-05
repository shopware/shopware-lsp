package parser

import (
	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// twigNameRegexMatch is a hand-written port of the Rust
// TWIG_NAME_REGEX = ^[a-zA-Z_\x7f-\xff][a-zA-Z0-9_\x7f-\xff]*$.
//
// NOTE(port): Rust's regex crate operates in Unicode mode over CODEPOINTS
// (regex::Regex, not regex::bytes::Regex, no (?-u) flag), so the \x7f-\xff class
// is the character range U+007F..U+00FF only — NOT raw bytes. We therefore
// iterate over runes and accept a high character only when it is in [0x7F, 0xFF];
// runes above U+00FF (CJK, emoji, Cyrillic, …) are rejected, matching Rust.
// Decode-aware iteration also maps invalid UTF-8 bytes to U+FFFD (> 0xFF), which
// is rejected — safe because Rust &str input can never contain invalid UTF-8.
//
// NOTE(port): deliberate extension beyond ludtwig — a leading '$' followed by a
// regular name character is accepted, so Vue-style names used by Shopware admin
// templates ($tc('...'), $t(...), foo.$refs) parse as ordinary TWIG_LITERAL_NAME
// nodes instead of ERROR. The lexer already tokenizes '$word' as one TK_WORD
// (same as ludtwig); only this name rule was stricter.
func twigNameRegexMatch(text string) bool {
	if len(text) == 0 {
		return false
	}
	if text[0] == '$' {
		text = text[1:]
		if len(text) == 0 {
			return false
		}
	}
	for i, r := range text {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		isUnderscore := r == '_'
		isHigh := r >= 0x7f && r <= 0xff
		if i == 0 {
			if !isLetter && !isUnderscore && !isHigh {
				return false
			}
		} else {
			if !isLetter && !isDigit && !isUnderscore && !isHigh {
				return false
			}
		}
	}
	return true
}

// parseTwigLiteral dispatches on the current token to parse a twig literal. Port
// of parse_twig_literal (literal.rs:14).
func parseTwigLiteral(p *parser) (completedMarker, bool) {
	if p.at(syntax.TkNumber) {
		return parseTwigNumber(p), true
	} else if p.atSet([]syntax.Kind{syntax.TkDoubleQuotes, syntax.TkSingleQuotes}) {
		return parseTwigString(p, true), true
	} else if p.at(syntax.TkOpenSquare) {
		return parseTwigArray(p), true
	} else if p.at(syntax.TkNull) {
		return parseTwigNull(p), true
	} else if p.atSet([]syntax.Kind{syntax.TkTrue, syntax.TkFalse}) {
		return parseTwigBoolean(p), true
	} else if p.at(syntax.TkOpenCurly) {
		return parseTwigHash(p), true
	}

	// literal name
	var tokenKind syntax.Kind
	hasTokenKind := false
	if tok := p.peekToken(); tok != nil {
		tokenKind = tok.Kind
		hasTokenKind = true
	}

	node, ok := parseTwigName(p)
	if !ok {
		return completedMarker{}, false
	}

	// check for optional function call or arrow function
	if p.at(syntax.TkOpenParenthesis) {
		node = parseTwigFunction(p, node)
	} else if p.at(syntax.TkEqualGreaterThan) {
		// wrap literal name node
		m := p.precede(node)
		lastNode := p.complete(m, syntax.TwigArguments)
		node = parseTwigArrowFunction(p, lastNode)
	} else if hasTokenKind && (tokenKind == syntax.TkSameAs || tokenKind == syntax.TkDivisibleBy) {
		// Support parentheses-optional syntax for twig tests like `is same as 0`.
		// Parse the following expression as an implicit argument,
		// producing the same TWIG_FUNCTION_CALL tree as the parenthesized form.
		node = parseTwigTestImplicitArgument(p, node)
	}

	return node, true
}

// parsePostfixOperators parses any amount of postfix operators like accessors
// (.), indexers ([]), function calls() or filters (|). Port of
// parse_postfix_operators (literal.rs:52).
func parsePostfixOperators(p *parser, node completedMarker) completedMarker {
	parseMany(
		p,
		func(p *parser) bool { return false },
		func(p *parser) {
			if p.at(syntax.TkDot) {
				node = parseTwigAccessor(p, node)
			} else if p.at(syntax.TkOpenSquare) {
				node = parseTwigIndexer(p, node)
			} else if p.at(syntax.TkSinglePipe) {
				node = parseTwigFilter(p, node)
			}
		},
	)

	return node
}

func parseTwigNumber(p *parser) completedMarker {
	// debug_assert!(parser.at(T![number]));
	m := p.start()
	p.bump()

	return p.complete(m, syntax.TwigLiteralNumber)
}

// parseTwigString parses a string literal, handling interpolation (#{expr}) only
// in double-quoted strings when interpolationAllowed. Port of parse_twig_string
// (literal.rs:81).
func parseTwigString(p *parser, interpolationAllowed bool) completedMarker {
	// debug_assert!(parser.at_set(&[T!["\""], T!["'"]]));
	m := p.start()
	startingQuoteToken := p.bump()
	quoteKind := startingQuoteToken.Kind
	if quoteKind != syntax.TkDoubleQuotes {
		// interpolation only allowed in double quoted strings
		interpolationAllowed = false
	}

	mInner := p.start()
	parseMany(
		p,
		func(p *parser) bool { return p.at(quoteKind) },
		func(p *parser) {
			if p.atFollowing([]syntax.Kind{syntax.TkBackwardSlash, quoteKind}) {
				// escaped quote should be consumed
				p.bump()
				p.bump()
			} else if p.atFollowing([]syntax.Kind{syntax.TkHashtag, syntax.TkOpenCurly}) {
				if !interpolationAllowed {
					openingToken := p.bump()
					interpolationError := newErrorBuilder(
						"no string interpolation, because it isn't allowed here",
					).atToken(openingToken)
					p.addError(interpolationError)
					return
				}

				// found twig expression in string (string interpolation)
				p.explicitlyConsumeTrivia() // keep trivia out of interpolation node (its part of the raw string)
				interpolationM := p.start()
				p.bump() // bump both starting tokens
				p.bump()
				if _, ok := parseTwigExpression(p); !ok {
					p.addError(newErrorBuilder("twig expression"))
				}
				p.expect(syntax.TkCloseCurly, twigExpressionRecoverySet)
				p.complete(interpolationM, syntax.TwigLiteralStringInterpolation)
			} else {
				// bump the token inside the string
				p.bump()
			}
		},
	)
	p.explicitlyConsumeTrivia() // consume any trailing trivia inside the string
	p.complete(mInner, syntax.TwigLiteralStringInner)

	p.expect(quoteKind, twigExpressionRecoverySet)
	return p.complete(m, syntax.TwigLiteralString)
}

func parseTwigArray(p *parser) completedMarker {
	// debug_assert!(parser.at(T!["["]));
	m := p.start()
	p.bump()

	// parse any amount of array values
	listM := p.start()
	parseMany(
		p,
		func(p *parser) bool { return p.at(syntax.TkCloseSquare) },
		func(p *parser) {
			parseTwigExpression(p)

			if p.at(syntax.TkComma) {
				// consume separator
				p.bump()
			} else if !p.at(syntax.TkCloseSquare) {
				p.addError(newErrorBuilder(","))
			}
		},
	)
	p.complete(listM, syntax.TwigLiteralArrayInner)

	p.expect(syntax.TkCloseSquare, twigExpressionRecoverySet)
	return p.complete(m, syntax.TwigLiteralArray)
}

func parseTwigNull(p *parser) completedMarker {
	// debug_assert!(parser.at(T!["null"]));
	m := p.start()
	p.bump()

	return p.complete(m, syntax.TwigLiteralNull)
}

func parseTwigBoolean(p *parser) completedMarker {
	// debug_assert!(parser.at_set(&[T!["true"], T!["false"]]));
	m := p.start()
	p.bump()

	return p.complete(m, syntax.TwigLiteralBoolean)
}

// parseTwigHash parses a hash literal. Port of parse_twig_hash (literal.rs:183).
func parseTwigHash(p *parser) completedMarker {
	// debug_assert!(parser.at(T!["{"]));
	m := p.start()
	p.bump()

	// parse any amount of hash pairs
	listM := p.start()
	parseMany(
		p,
		func(p *parser) bool { return p.at(syntax.TkCloseCurly) },
		func(p *parser) {
			parseTwigHashPair(p)

			if p.at(syntax.TkComma) {
				// consume separator
				p.bump()
			} else if !p.at(syntax.TkCloseCurly) {
				p.addError(newErrorBuilder(","))
			}
		},
	)
	p.complete(listM, syntax.TwigLiteralHashItems)

	p.expect(syntax.TkCloseCurly, twigExpressionRecoverySet)
	return p.complete(m, syntax.TwigLiteralHash)
}

// parseTwigHashPair parses one hash entry (key + optional `: value`). Port of
// parse_twig_hash_pair (literal.rs:210).
func parseTwigHashPair(p *parser) (completedMarker, bool) {
	var key completedMarker
	if p.at(syntax.TkNumber) {
		m := parseTwigNumber(p)
		preceded := p.precede(m)
		key = p.complete(preceded, syntax.TwigLiteralHashKey)
	} else if p.atSet([]syntax.Kind{syntax.TkSingleQuotes, syntax.TkDoubleQuotes}) {
		m := parseTwigString(p, false) // no interpolation in keys
		preceded := p.precede(m)
		key = p.complete(preceded, syntax.TwigLiteralHashKey)
	} else if p.at(syntax.TkOpenParenthesis) {
		m := p.start()
		p.bump()
		if _, ok := parseTwigExpression(p); !ok {
			p.addError(newErrorBuilder("twig expression"))
			p.recover(twigExpressionRecoverySet)
		}
		p.expect(syntax.TkCloseParenthesis, twigExpressionRecoverySet)
		key = p.complete(m, syntax.TwigLiteralHashKey)
	} else {
		tok := p.peekToken()
		if tok == nil {
			return completedMarker{}, false
		}
		if twigNameRegexMatch(tok.Text()) {
			m := p.start()
			p.bumpAs(syntax.TkWord)
			key = p.complete(m, syntax.TwigLiteralHashKey)
		} else {
			return completedMarker{}, false
		}
	}

	// check if key exists (has a value)
	if p.at(syntax.TkColon) {
		p.bump()
		if _, ok := parseTwigExpression(p); !ok {
			p.addError(newErrorBuilder("value as twig expression"))
			p.recover(twigExpressionRecoverySet)
		}
	}

	preceded := p.precede(key)
	return p.complete(preceded, syntax.TwigLiteralHashPair), true
}

// parseTwigFilter parses a `| filter` postfix operator. Port of parse_twig_filter
// (literal.rs:252).
func parseTwigFilter(p *parser, lastNode completedMarker) completedMarker {
	// debug_assert!(parser.at(T!["|"]));

	// wrap last_node in an operand and create outer marker
	m := p.precede(lastNode)
	lastNode = p.complete(m, syntax.TwigOperand)
	outer := p.precede(lastNode)

	// bump the operator
	p.bump()

	// parse the rhs and wrap it also in an operand
	m2 := p.start()
	if _, ok := parseTwigName(p); !ok {
		p.addError(newErrorBuilder("twig filter"))
		p.recover(twigExpressionRecoverySet)
	} else if p.at(syntax.TkOpenParenthesis) {
		// parse any amount of arguments
		argumentsM := p.start()
		p.bump()
		parseMany(
			p,
			func(p *parser) bool { return p.at(syntax.TkCloseParenthesis) },
			func(p *parser) {
				parseTwigFunctionArgument(p)
				if p.at(syntax.TkComma) {
					p.bump()
				} else if !p.at(syntax.TkCloseParenthesis) {
					p.addError(newErrorBuilder(","))
				}
			},
		)
		p.expect(syntax.TkCloseParenthesis, twigExpressionRecoverySet)
		p.complete(argumentsM, syntax.TwigArguments)
	}
	p.complete(m2, syntax.TwigOperand)

	// complete the outer marker
	return p.complete(outer, syntax.TwigFilter)
}

// parseTwigIndexer parses a `[ ... ]` indexer/slice postfix operator. Port of
// parse_twig_indexer (literal.rs:296).
func parseTwigIndexer(p *parser, lastNode completedMarker) completedMarker {
	// debug_assert!(parser.at(T!["["]));

	// wrap last_node in an operand and create outer marker
	m := p.precede(lastNode)
	lastNode = p.complete(m, syntax.TwigOperand)
	outer := p.precede(lastNode)

	// bump the opening '['
	p.bump()

	indexM := p.start()

	// try to parse the first and maybe only expression
	_, lowerOk := parseTwigExpression(p)
	missingLowerSliceBound := !lowerOk

	// look for range syntax and try to parse the second and maybe only expression
	isSlice := false
	missingUpperSliceBound := true
	if p.at(syntax.TkColon) {
		p.bump()
		isSlice = true // now it is a slice instead of a simple lookup
		_, upperOk := parseTwigExpression(p)
		missingUpperSliceBound = !upperOk
	}

	if missingLowerSliceBound && missingUpperSliceBound {
		p.addError(newErrorBuilder("twig expression"))
		p.recover(twigExpressionRecoverySet)
	}

	if isSlice {
		p.complete(indexM, syntax.TwigIndexRange)
	} else {
		p.complete(indexM, syntax.TwigIndex)
	}

	p.expect(syntax.TkCloseSquare, twigExpressionRecoverySet)

	// complete the outer marker
	return p.complete(outer, syntax.TwigIndexLookup)
}

// parseTwigAccessor parses a `.name` accessor (or `.0` index lookup) postfix
// operator. Port of parse_twig_accessor (literal.rs:342).
func parseTwigAccessor(p *parser, lastNode completedMarker) completedMarker {
	// debug_assert!(parser.at(T!["."]));

	// wrap last_node in an operand and create outer marker
	m := p.precede(lastNode)
	lastNode = p.complete(m, syntax.TwigOperand)
	outer := p.precede(lastNode)

	// bump the operator
	p.bump()

	// parse the rhs and wrap it also in an operand
	m2 := p.start()
	if p.at(syntax.TkNumber) {
		// found an array index lookup instead of name accessor!
		n := parseTwigNumber(p)
		nPrec := p.precede(n)
		p.complete(nPrec, syntax.TwigExpression)
		p.complete(m2, syntax.TwigIndex)
		node := p.complete(outer, syntax.TwigIndexLookup)
		return node
	} else if _, ok := parseTwigName(p); !ok {
		p.addError(newErrorBuilder(
			"twig variable property, key or method",
		))
		p.recover(twigExpressionRecoverySet)
	}
	p.complete(m2, syntax.TwigOperand)

	// complete the outer marker
	node := p.complete(outer, syntax.TwigAccessor)

	// check for optional function call
	if p.at(syntax.TkOpenParenthesis) {
		node = parseTwigFunction(p, node)
	}

	return node
}

// parseTwigFunction parses a `( args )` function call on the given operand. Port
// of parse_twig_function (literal.rs:382).
func parseTwigFunction(p *parser, lastNode completedMarker) completedMarker {
	// debug_assert!(parser.at(T!["("]));

	// wrap last_node in an operand and create outer marker
	m := p.precede(lastNode)
	lastNode = p.complete(m, syntax.TwigOperand)
	outer := p.precede(lastNode)

	// parse any amount of arguments
	argumentsM := p.start()
	// bump the opening '('
	p.bump()
	parseMany(
		p,
		func(p *parser) bool { return p.at(syntax.TkCloseParenthesis) },
		func(p *parser) {
			parseTwigFunctionArgument(p)
			if p.at(syntax.TkComma) {
				p.bump()
			} else if !p.at(syntax.TkCloseParenthesis) {
				p.addError(newErrorBuilder(","))
			}
		},
	)
	p.expect(syntax.TkCloseParenthesis, twigExpressionRecoverySet)
	p.complete(argumentsM, syntax.TwigArguments)

	// complete the outer marker
	return p.complete(outer, syntax.TwigFunctionCall)
}

// parseTwigTestImplicitArgument parses an implicit (no parentheses) argument for
// twig test expressions like `is same as 0`, producing the same
// TWIG_FUNCTION_CALL tree structure as the parenthesized form. Port of
// parse_twig_test_implicit_argument (literal.rs:418).
func parseTwigTestImplicitArgument(p *parser, lastNode completedMarker) completedMarker {
	// wrap last_node in an operand and create outer marker (same as parse_twig_function)
	m := p.precede(lastNode)
	operand := p.complete(m, syntax.TwigOperand)
	outer := p.precede(operand)

	// parse the single argument expression without parentheses
	argumentsM := p.start()
	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder("argument for twig test"))
		p.recover(twigExpressionRecoverySet)
	}
	p.complete(argumentsM, syntax.TwigArguments)

	return p.complete(outer, syntax.TwigFunctionCall)
}

// parseTwigArrowFunction parses `=> body` on the given arguments node. Port of
// parse_twig_arrow_function (literal.rs:438).
func parseTwigArrowFunction(p *parser, lastNode completedMarker) completedMarker {
	// debug_assert!(parser.at(T!["=>"]));

	outer := p.precede(lastNode)

	// Consume the arrow. Callers normally guarantee we are at "=>"
	// (Rust's debug_assert, compiled out in release), so release ludtwig does a
	// bare `parser.bump()` here. The parenthesized parameter-list path
	// (parseParenArrowFunction) can, however, reach this function at a token that
	// is neither "=>" nor EOF (e.g. "}}" after "(a,"). To match release ludtwig's
	// observable behavior we blind-bump that token into the TWIG_ARROW_FUNCTION
	// node whenever a token exists. Only at EOF — where a bare bump would panic
	// and where release ludtwig itself panics — do we deviate, falling back to
	// expect so the totality invariant holds with a sensible diagnostic.
	if !p.atEnd() {
		p.bump()
	} else {
		p.expect(syntax.TkEqualGreaterThan, twigExpressionRecoverySet)
	}

	// parse closure expression
	if _, ok := parseTwigExpression(p); !ok {
		p.addError(newErrorBuilder(
			"single twig expression as the body of the closure",
		))
		p.recover(twigExpressionRecoverySet)
	}

	// complete the outer marker
	return p.complete(outer, syntax.TwigArrowFunction)
}

// parseTwigFunctionArgument parses a single function argument, either a named
// argument (word = / word :) or a plain expression. Twig 3.12 added ":" as the
// preferred named-argument separator while retaining "=" for compatibility.
func parseTwigFunctionArgument(p *parser) (completedMarker, bool) {
	// Be specific about the following separator because a lone word can be a
	// normal variable or another function call.
	if p.atFollowing([]syntax.Kind{syntax.TkWord, syntax.TkEqual}) ||
		p.atFollowing([]syntax.Kind{syntax.TkWord, syntax.TkColon}) {
		namedArgM := p.start()
		p.bump()
		p.expectAny(
			[]syntax.Kind{syntax.TkEqual, syntax.TkColon},
			twigExpressionRecoverySet,
		)
		parseTwigExpression(p)
		return p.complete(namedArgM, syntax.TwigNamedArgument), true
	}
	return parseTwigExpression(p)
}

// parseTwigName parses a single-token name matching TWIG_NAME_REGEX (re-bumped as
// TK_WORD), plus the special `same as` / `divisible by` test tokens. Port of
// parse_twig_name (literal.rs:475).
func parseTwigName(p *parser) (completedMarker, bool) {
	// special case to allow for 'same as' and 'divisible by' twig test
	// ('is' / 'is not' operator)
	isAtSpecial := p.atSet([]syntax.Kind{syntax.TkSameAs, syntax.TkDivisibleBy})
	tok := p.peekToken()
	if tok == nil {
		return completedMarker{}, false
	}
	if !isAtSpecial && !twigNameRegexMatch(tok.Text()) {
		return completedMarker{}, false
	}

	m := p.start()
	p.bumpAs(syntax.TkWord)
	return p.complete(m, syntax.TwigLiteralName), true
}
