package parser

import (
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// htmlAttributeNameRegex matches every token value allowed for html attribute
// names. Allows Vue/Alpine-style :bind, @click.stop, #slot,
// v-bind:prop.sync, _x and $y names.
// Port of html.rs:HTML_ATTRIBUTE_NAME_REGEX.
var htmlAttributeNameRegex = regexp.MustCompile(
	`^:?(?:[a-zA-Z_]|[@#_$][a-zA-Z])[a-zA-Z0-9_\-]*(?:[:.][a-zA-Z_][a-zA-Z0-9_\-]*)*$`,
)

var htmlAttributeNameSegmentRegex = regexp.MustCompile(
	`^[a-zA-Z_][a-zA-Z0-9_\-]*$`,
)

// htmlTagNameRegex matches valid html tag names. Port of
// html.rs:HTML_TAG_NAME_REGEX.
var htmlTagNameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9\-]*$`)

// htmlVoidElements: "void elements" in the HTML spec. Port of
// html.rs:HTML_VOID_ELEMENTS.
var htmlVoidElements = map[string]struct{}{
	"area": {}, "base": {}, "br": {}, "col": {}, "command": {}, "embed": {},
	"hr": {}, "img": {}, "input": {}, "keygen": {}, "link": {}, "meta": {},
	"param": {}, "source": {}, "track": {}, "wbr": {},
}

// htmlRawTextElements: "raw text elements" in the HTML spec. Port of
// html.rs:HTML_RAW_TEXT_ELEMENTS.
var htmlRawTextElements = map[string]struct{}{
	"script": {}, "style": {}, "textarea": {}, "title": {},
}

// parseAnyHtml decides whether the current position is an html element, comment,
// doctype, or plain text. Port of html.rs:parse_any_html.
func parseAnyHtml(p *parser) (completedMarker, bool) {
	if p.at(syntax.TkLessThan) {
		next := p.peekNthToken(1)
		if next != nil && next.Kind != syntax.TkWhitespace && next.Kind != syntax.TkNumber &&
			!kindInSet(next.Kind, generalRecoverySet) {
			// '<' should not be followed by EOF, a ws, a number or RECOVERY_SET
			// token, because then it is considered as arbitrary text.
			return parseHtmlElement(p), true
		}
	}
	if p.at(syntax.TkLessThanExclamationMarkMinusMinus) {
		return parseHtmlComment(p), true
	}
	if p.at(syntax.TkLessThanExclamationMark) {
		return parseHtmlDoctype(p), true
	}
	return parseHtmlText(p)
}

// kindInSet reports whether k is in set.
func kindInSet(k syntax.Kind, set []syntax.Kind) bool {
	for _, s := range set {
		if s == k {
			return true
		}
	}
	return false
}

func parseHtmlDoctype(p *parser) completedMarker {
	// debug_assert!(parser.at(T!["<!"]))
	m := p.start()
	p.bump()

	p.expect(syntax.TkDoctype, []syntax.Kind{syntax.TkWord, syntax.TkGreaterThan})
	p.expect(syntax.TkWord, []syntax.Kind{syntax.TkGreaterThan})
	p.expect(syntax.TkGreaterThan, nil)

	return p.complete(m, syntax.HtmlDoctype)
}

// htmlAtLessThanNonWord ports the nested parser_at_less_than_non_word closure in
// parse_html_text: '<' followed by ws/number/recovery/EOF is text, not a tag.
func htmlAtLessThanNonWord(p *parser) bool {
	if !p.at(syntax.TkLessThan) {
		return false
	}
	next := p.peekNthToken(1)
	if next == nil {
		return true // is_none_or -> None is true
	}
	return next.Kind == syntax.TkWhitespace || next.Kind == syntax.TkNumber ||
		kindInSet(next.Kind, generalRecoverySet)
}

func parseHtmlText(p *parser) (completedMarker, bool) {
	if p.atEnd() ||
		p.at(syntax.TkLessThanSlash) ||
		(p.atSet(generalRecoverySet) && !htmlAtLessThanNonWord(p)) {
		return completedMarker{}, false
	}

	m := p.start()

	parseMany(
		p,
		func(p *parser) bool {
			return p.at(syntax.TkLessThanSlash) ||
				(p.atSet(generalRecoverySet) && !htmlAtLessThanNonWord(p))
		},
		func(p *parser) {
			p.bump()
		},
	)

	return p.complete(m, syntax.HtmlText), true
}

// parseHtmlRawTextInner parses raw text nested inside a twig body within a
// raw-text element (script/style/textarea/title). Port of the nested
// parse_html_raw_text_inner in html.rs. Always returns Some (true).
func parseHtmlRawTextInner(p *parser) (completedMarker, bool) {
	rawTextM := p.start()

	parseMany(
		p,
		func(p *parser) bool {
			if atTwigTerminationTag(p) {
				return true // endblock in the wild may mean this tag has a missing closing tag
			}
			return false
		},
		func(p *parser) {
			// NOTE(port): guard against EOF. parseAnyTwig can consume tokens up
			// to EOF (e.g. an unknown {% tag recovers to end of input) and still
			// return false; the unguarded bump in Rust panics on such input, but
			// our totality invariant forbids panics, so we skip the bump at EOF.
			if _, ok := parseAnyTwig(p, parseHtmlRawTextInner); !ok && !p.atEnd() {
				p.bump() // just bump anything until the early exit closure stops us
			}
		},
	)
	return p.complete(rawTextM, syntax.HtmlRawText), true
}

// parseHtmlRawText parses raw-text-element content until the matching end tag or
// a wild twig terminator. Port of html.rs:parse_html_raw_text. Returns true if
// the matching ending tag was encountered.
func parseHtmlRawText(p *parser, startingTagTokentype syntax.Kind, startingTagName string) bool {
	matchingEndTagEncountered := false
	rawTextM := p.start()

	parseMany(
		p,
		func(p *parser) bool {
			if p.atFollowingContent([]followingContent{
				{kind: syntax.TkLessThanSlash},
				{kind: startingTagTokentype, matchText: true, text: startingTagName},
			}) {
				matchingEndTagEncountered = true
				return true // found matching closing tag
			}
			if atTwigTerminationTag(p) {
				return true // endblock in the wild may mean this tag has a missing closing tag
			}
			return false
		},
		func(p *parser) {
			// NOTE(port): guard against EOF. parseAnyTwig can consume tokens up
			// to EOF (e.g. an unknown {% tag recovers to end of input) and still
			// return false; the unguarded bump in Rust panics on such input, but
			// our totality invariant forbids panics, so we skip the bump at EOF.
			if _, ok := parseAnyTwig(p, parseHtmlRawTextInner); !ok && !p.atEnd() {
				p.bump() // just bump anything until the early exit closure stops us
			}
		},
	)
	p.complete(rawTextM, syntax.HtmlRawText)
	return matchingEndTagEncountered
}

func parseHtmlComment(p *parser) completedMarker {
	// debug_assert!(parser.at(T!["<!--"]))
	m := p.start()
	p.bump()

	if p.atSet([]syntax.Kind{syntax.TkLudtwigIgnoreFile, syntax.TkLudtwigIgnore}) {
		return parseLudtwigDirective(p, m, []syntax.Kind{syntax.TkMinusMinusGreaterThan})
	}
	return parsePlainHtmlComment(p, m)
}

func parsePlainHtmlComment(p *parser, outer *marker) completedMarker {
	parseMany(
		p,
		func(p *parser) bool { return p.at(syntax.TkMinusMinusGreaterThan) },
		func(p *parser) {
			p.bump()
		},
	)

	p.expect(syntax.TkMinusMinusGreaterThan, nil)
	return p.complete(outer, syntax.HtmlComment)
}

// parseHtmlElement parses an HTML element (starting tag, attributes, body,
// ending tag) with matching-end-tag/twig-terminator early exit and mismatch
// recovery. Port of html.rs:parse_html_element.
func parseHtmlElement(p *parser) completedMarker {
	// debug_assert!(parser.at(T!["<"]))
	m := p.start()

	// parse start tag
	startingTagM := p.start()
	p.bump()

	tagName := ""
	tagNameTokentype := syntax.TkWord
	if t := p.peekToken(); t != nil {
		tagName = t.Text()
		tagNameTokentype = t.Kind
	}
	tagNameLowercase := strings.ToLower(tagName)

	if tagNameTokentype == syntax.TkTwigComponentName {
		p.bump()
	} else if htmlTagNameRegex.MatchString(tagName) {
		// normal html tag name
		p.bumpAs(syntax.TkWord)
	} else {
		p.addError(newErrorBuilder("HTML Tag Name"))
		p.recover([]syntax.Kind{syntax.TkGreaterThan, syntax.TkSlashGreaterThan, syntax.TkLessThanSlash, syntax.TkWord})
	}

	// parse attributes (can include twig)
	attributesM := p.start()
	parseMany(
		p,
		func(p *parser) bool { return p.at(syntax.TkGreaterThan) || p.at(syntax.TkSlashGreaterThan) },
		func(p *parser) {
			parseHtmlAttributeOrTwig(p)
		},
	)
	p.complete(attributesM, syntax.HtmlAttributeList)

	// parse end of starting tag
	isSelfClosing := false
	if p.at(syntax.TkSlashGreaterThan) {
		p.bump()
		isSelfClosing = true
	} else {
		p.expect(syntax.TkGreaterThan, []syntax.Kind{syntax.TkLessThanSlash, syntax.TkWord, syntax.TkGreaterThan})
		isSelfClosing = false
	}

	if _, ok := htmlVoidElements[tagNameLowercase]; ok {
		isSelfClosing = true // void elements never have children or an end tag
	}

	p.complete(startingTagM, syntax.HtmlStartingTag)

	// early return in case of self-closing
	if isSelfClosing {
		return p.complete(m, syntax.HtmlTag)
	}

	// parse all the children
	bodyM := p.start()
	matchingEndTagEncountered := false

	if _, ok := htmlRawTextElements[tagNameLowercase]; ok {
		matchingEndTagEncountered = parseHtmlRawText(p, tagNameTokentype, tagName)
	} else {
		parseMany(
			p,
			func(p *parser) bool {
				if p.atFollowingContent([]followingContent{
					{kind: syntax.TkLessThanSlash},
					{kind: tagNameTokentype, matchText: true, text: tagName},
				}) {
					matchingEndTagEncountered = true
					return true // found matching closing tag
				}
				if atTwigTerminationTag(p) {
					return true // endblock in the wild may mean this tag has a missing closing tag
				}
				return false
			},
			func(p *parser) {
				parseAnyElement(p)
			},
		)
	}
	p.complete(bodyM, syntax.Body)

	// parse matching end tag or report missing (the tag itself is not self-closing!)
	endTagM := p.start()
	if matchingEndTagEncountered {
		// found matching closing tag
		p.expect(syntax.TkLessThanSlash, []syntax.Kind{tagNameTokentype, syntax.TkGreaterThan})

		if p.at(syntax.TkTwigComponentName) {
			p.bump()
		} else if p.at(tagNameTokentype) {
			p.bumpAs(syntax.TkWord)
		} else {
			p.addError(newErrorBuilder(tagName + " as ending tag name"))
			p.recover([]syntax.Kind{syntax.TkGreaterThan})
		}

		p.expect(syntax.TkGreaterThan, nil)
	} else {
		// no matching end tag found!
		p.addError(newErrorBuilder("</" + tagName + "> ending tag"))
		p.recover(nil)
	}
	p.complete(endTagM, syntax.HtmlEndingTag)

	return p.complete(m, syntax.HtmlTag)
}

// parseHtmlAttributeOrTwig parses one HTML attribute or a twig construct sitting
// in the attribute list. Port of html.rs:parse_html_attribute_or_twig.
func parseHtmlAttributeOrTwig(p *parser) (completedMarker, bool) {
	// Twig constructs are valid entries in an HTML attribute list. Check for
	// them before attempting to classify an attribute name: delimiter tokens
	// such as `{%` and `{{` are deliberately not valid HTML name prefixes.
	// Returning false for them here makes parseMany stop and prematurely closes
	// the starting tag, leaving the complete attribute list in the tag body.
	if p.atTwigBlockOpen() || p.atTwigVarOpen() || p.atTwigCommentOpen() {
		return parseAnyTwig(p, parseHtmlAttributeOrTwig)
	}

	nameTokenCount, tokenText := htmlAttributeName(p)
	if nameTokenCount == 0 {
		return completedMarker{}, false
	}

	var attributeM *marker
	if htmlAttributeNameRegex.MatchString(tokenText) {
		// normal html attribute name
		attributeM = p.start()
		if nameTokenCount > 1 {
			p.bumpNextNAs(nameTokenCount, syntax.TkWord)
		} else {
			p.bumpAs(syntax.TkWord)
		}
	} else {
		// is the attribute name a twig var expression?
		if p.atTwigVarOpen() {
			twigNameAttributeM := p.start()
			parseTwigVarStatement(p)
			attributeM = twigNameAttributeM
		} else {
			// parse any twig block / comment syntax where its children can only
			// be html attributes (this parser). This structure itself doesn't
			// count as an HTML_ATTRIBUTE node.
			return parseAnyTwig(p, parseHtmlAttributeOrTwig)
		}
	}

	if p.at(syntax.TkEqual) {
		// attribute value
		p.bump()
		parseHtmlAttributeValueString(p)
	}

	return p.complete(attributeM, syntax.HtmlAttribute), true
}

func htmlAttributeName(p *parser) (int, string) {
	first := p.peekToken()
	if first == nil {
		return 0, ""
	}
	count := 1
	var name strings.Builder
	switch first.Kind {
	case syntax.TkColon:
		word := p.peekNthToken(1)
		if word == nil || !htmlAttributeNameSegmentRegex.MatchString(word.Text()) {
			return 0, ""
		}
		count = 2
		name.WriteString(":")
		name.WriteString(word.Text())
	default:
		// Twig's context-free lexer classifies words such as "style", "block"
		// and "for" as reserved tokens. In an HTML attribute-name position they
		// are ordinary name segments and must be reclassified as TK_WORD.
		if !htmlAttributeNameRegex.MatchString(first.Text()) {
			return 0, ""
		}
		name.WriteString(first.Text())
	}
	for {
		separator := p.peekNthToken(count)
		word := p.peekNthToken(count + 1)
		if separator == nil || word == nil ||
			!htmlAttributeNameSegmentRegex.MatchString(word.Text()) ||
			(separator.Kind != syntax.TkColon && separator.Kind != syntax.TkDot) {
			break
		}
		name.WriteString(separator.Text())
		name.WriteString(word.Text())
		count += 2
	}
	return count, name.String()
}

// parseHtmlAttributeValueString parses an attribute value: quoted (with twig
// child parsers), unquoted (single word or {{ var }}), or stray-quote recovery.
// Port of html.rs:parse_html_attribute_value_string.
func parseHtmlAttributeValueString(p *parser) completedMarker {
	m := p.start()
	var quoteKind syntax.Kind
	hasQuote := false
	if p.atSet([]syntax.Kind{syntax.TkDoubleQuotes, syntax.TkSingleQuotes}) {
		startingQuoteToken := p.bump()
		quoteKind = startingQuoteToken.Kind
		hasQuote = true
	}
	// else: HTML also allows no quotes but then the value must be a single word

	innerM := p.start()
	// run the appropriate inner parser
	if hasQuote {
		switch quoteKind {
		case syntax.TkDoubleQuotes:
			htmlInnerDoubleQuoteParser(p)
		case syntax.TkSingleQuotes:
			htmlInnerSingleQuoteParser(p)
		}
	} else {
		htmlInnerNoQuoteParser(p)
	}

	if hasQuote {
		// consume any trailing trivia to be inside the inner string but only when
		// quotation exists (otherwise only the single word should be inside the
		// HTML_STRING_INNER node)
		p.explicitlyConsumeTrivia()
	}

	p.complete(innerM, syntax.HtmlStringInner)

	// expect the same closing quote if a starting quote existed
	if hasQuote {
		p.expect(quoteKind, []syntax.Kind{syntax.TkGreaterThan, syntax.TkSlashGreaterThan})
	} else {
		// check for unexpected quote which this parser still consumes to make
		// missing leading quote errors simpler
		if p.atSet([]syntax.Kind{syntax.TkDoubleQuotes, syntax.TkSingleQuotes}) {
			errorM := p.start()
			quote := p.bump()
			parserErr := newErrorBuilder("no trailing quote because there is no leading quote").atToken(quote)
			p.addError(parserErr)
			p.complete(errorM, syntax.Error)
		}
	}

	return p.complete(m, syntax.HtmlString)
}

// htmlInnerDoubleQuoteParser is the inner parser for double-quoted attribute
// values. Port of the nested inner_double_quote_parser.
func htmlInnerDoubleQuoteParser(p *parser) (completedMarker, bool) {
	parseMany(
		p,
		func(p *parser) bool {
			if p.at(syntax.TkDoubleQuotes) {
				return true
			}
			return atTwigTerminationTag(p)
		},
		func(p *parser) { htmlAttributeStringChildParser(p, htmlInnerDoubleQuoteParser) },
	)
	return completedMarker{}, false
}

// htmlInnerSingleQuoteParser is the inner parser for single-quoted attribute
// values. Port of the nested inner_single_quote_parser.
func htmlInnerSingleQuoteParser(p *parser) (completedMarker, bool) {
	parseMany(
		p,
		func(p *parser) bool {
			if p.at(syntax.TkSingleQuotes) {
				return true
			}
			return atTwigTerminationTag(p)
		},
		func(p *parser) { htmlAttributeStringChildParser(p, htmlInnerSingleQuoteParser) },
	)
	return completedMarker{}, false
}

// htmlInnerNoQuoteParser is the inner parser for unquoted attribute values:
// exactly one word or one {{ var }}. Port of the nested inner_no_quote_parser.
func htmlInnerNoQuoteParser(p *parser) (completedMarker, bool) {
	if p.at(syntax.TkWord) {
		p.bump()
	} else if p.atTwigVarOpen() {
		// a single twig var expression with missing quotes should also count as
		// an html attribute value
		parseTwigVarStatement(p)
	} else {
		p.addError(newErrorBuilder("html attribute value"))
		p.recover([]syntax.Kind{syntax.TkWord, syntax.TkGreaterThan, syntax.TkSlashGreaterThan})
	}
	return completedMarker{}, false
}

// htmlAttributeStringChildParser offers each position inside a quoted attribute
// string to twig, else bumps a raw token. A `>` or `/>` inside quotes is data,
// not a tag boundary (for example `data-action="click->live#emit"`).
func htmlAttributeStringChildParser(p *parser, innerTwigChildParser parseFunction) {
	if _, ok := parseAnyTwig(p, innerTwigChildParser); !ok {
		// The top-level recovery set treats HTML openers as synchronization
		// points. Inside a quoted attribute they are ordinary data, however. In
		// particular Vue comparison bindings such as `:disabled="count < 1"`
		// must keep the complete expression in HTML_STRING_INNER.
		if p.atSet([]syntax.Kind{
			syntax.TkLessThan,
			syntax.TkLessThanExclamationMark,
			syntax.TkLessThanExclamationMarkMinusMinus,
		}) {
			p.bump()
			return
		}
		if p.atSet(generalRecoverySet) || p.atEnd() {
			return
		}
		p.bump()
	}
}
