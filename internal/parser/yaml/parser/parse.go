package parser

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/bytescan"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/shopware/shopware-lsp/internal/parser/yaml/lexer"
	"github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

type Error = parsekit.Error

type Result struct {
	Tree   *syntax.Tree
	Errors []Error
}

var valueRecovery = []syntax.Kind{
	syntax.TkLineBreak,
	syntax.TkComma,
	syntax.TkCloseBracket,
	syntax.TkCloseBrace,
	syntax.TkDocumentStart,
	syntax.TkDocumentEnd,
}

func Parse(source string) Result {
	tokens := lexer.LexInto(
		source,
		parsekit.AcquireTokenBuffer(len(source)/4+1),
	)
	parser := parsekit.NewOwned(tokens, parsekit.Config{
		GeneralRecoverySet: []syntax.Kind{
			syntax.TkLineBreak,
			syntax.TkDocumentStart,
			syntax.TkDocumentEnd,
		},
		ErrorKind: syntax.Error,
	})
	root := parser.Start()

	for !parser.AtEnd() {
		skipBlankLines(parser)
		for parser.At(syntax.TkDirective) {
			parser.Bump()
			consumeLineEnd(parser)
			skipBlankLines(parser)
		}
		if parser.AtEnd() {
			break
		}

		document := parser.Start()
		if parser.At(syntax.TkDocumentStart) {
			parser.Bump()
			consumeLineEnd(parser)
			skipBlankLines(parser)
		}

		if !parser.AtEnd() &&
			!parser.At(syntax.TkDocumentStart) &&
			!parser.At(syntax.TkDocumentEnd) {
			if !parseBlock(parser, currentIndent(parser)) {
				parser.AddError(parsekit.NewErrorBuilder("YAML mapping, sequence, or scalar"))
				recoverLine(parser)
			}
		}

		for !parser.AtEnd() &&
			!parser.At(syntax.TkDocumentStart) &&
			!parser.At(syntax.TkDocumentEnd) {
			skipBlankLines(parser)
			if parser.AtEnd() ||
				parser.At(syntax.TkDocumentStart) ||
				parser.At(syntax.TkDocumentEnd) {
				break
			}
			parser.AddError(parsekit.NewErrorBuilder("end of YAML document"))
			recoverLine(parser)
		}

		if parser.At(syntax.TkDocumentEnd) {
			parser.Bump()
			consumeLineEnd(parser)
		}
		parser.Complete(document, syntax.YamlDocument)
	}

	parser.Complete(root, syntax.YamlStream)
	tree, errors := parser.Finish(source)
	return Result{Tree: tree, Errors: errors}
}

func parseBlock(parser *parsekit.Parser, indent int) bool {
	switch {
	case lineStartsSequence(parser):
		parseSequence(parser, indent)
	case lineHasTopLevelColon(parser):
		parseMapping(parser, indent)
	default:
		return parseValueLine(parser)
	}
	return true
}

func parseMapping(parser *parsekit.Parser, indent int) {
	marker := parser.Start()
	for !parser.AtEnd() {
		skipBlankLines(parser)
		if currentIndent(parser) != indent ||
			lineStartsSequence(parser) ||
			!lineHasTopLevelColon(parser) {
			break
		}
		parsePairLine(parser, indent, true)
	}
	parser.Complete(marker, syntax.YamlMapping)
}

func parsePairLine(parser *parsekit.Parser, indent int, consumeLeadingIndent bool) {
	marker := parser.Start()
	if consumeLeadingIndent && parser.At(syntax.TkIndent) {
		consumeIndent(parser)
	}

	if !parseScalar(parser) {
		parser.AddError(parsekit.NewErrorBuilder("YAML mapping key"))
		parser.Recover([]syntax.Kind{syntax.TkColon, syntax.TkLineBreak})
	}
	parser.Expect(syntax.TkColon, valueRecovery)

	hasInlineValue := false
	hasCollectionDecorator := false
	if !parser.AtEnd() &&
		!parser.At(syntax.TkLineBreak) &&
		!parser.At(syntax.TkDocumentStart) &&
		!parser.At(syntax.TkDocumentEnd) {
		hasCollectionDecorator = parser.At(syntax.TkPlainScalar) &&
			isCollectionDecorator(parser.PeekToken().Text())
		hasInlineValue = parseValue(parser)
		if !hasInlineValue {
			parser.AddError(parsekit.NewErrorBuilder("YAML value"))
			parser.Recover(valueRecovery)
		}
	}

	if parser.At(syntax.TkLineBreak) {
		parser.Bump()
	} else if !parser.AtEnd() &&
		!parser.At(syntax.TkDocumentStart) &&
		!parser.At(syntax.TkDocumentEnd) &&
		!hasInlineValue {
		consumeLineEnd(parser)
	}

	if !hasInlineValue || hasCollectionDecorator {
		skipBlankLines(parser)
		nextIndent := currentIndent(parser)
		if !parser.AtEnd() &&
			!parser.At(syntax.TkDocumentStart) &&
			!parser.At(syntax.TkDocumentEnd) &&
			(nextIndent > indent || nextIndent == indent && lineStartsSequence(parser)) {
			if !parseBlock(parser, nextIndent) {
				completeNull(parser)
			}
		} else if !hasInlineValue {
			completeNull(parser)
		}
	}

	parser.Complete(marker, syntax.YamlPair)
}

func parseSequence(parser *parsekit.Parser, indent int) {
	marker := parser.Start()
	for !parser.AtEnd() {
		skipBlankLines(parser)
		if currentIndent(parser) != indent || !lineStartsSequence(parser) {
			break
		}
		parseSequenceItem(parser, indent)
	}
	parser.Complete(marker, syntax.YamlSequence)
}

func parseSequenceItem(parser *parsekit.Parser, indent int) {
	marker := parser.Start()
	if parser.At(syntax.TkIndent) {
		consumeIndent(parser)
	}
	parser.Expect(syntax.TkDash, valueRecovery)

	if parser.At(syntax.TkLineBreak) {
		parser.Bump()
		skipBlankLines(parser)
		nextIndent := currentIndent(parser)
		if !parser.AtEnd() &&
			!parser.At(syntax.TkDocumentStart) &&
			!parser.At(syntax.TkDocumentEnd) &&
			nextIndent > indent {
			if !parseBlock(parser, nextIndent) {
				completeNull(parser)
			}
		} else {
			completeNull(parser)
		}
		parser.Complete(marker, syntax.YamlSequenceItem)
		return
	}

	if lineHasTopLevelColon(parser) {
		parseCompactMapping(parser, indent+2)
		parser.Complete(marker, syntax.YamlSequenceItem)
		return
	}

	blockScalar := parser.At(syntax.TkBlockScalar)
	hasCollectionDecorator := parser.At(syntax.TkPlainScalar) &&
		isCollectionDecorator(parser.PeekToken().Text())
	if !parseValue(parser) {
		parser.AddError(parsekit.NewErrorBuilder("YAML sequence value"))
		parser.Recover(valueRecovery)
	}

	if blockScalar {
		// The block scalar token includes its header line break and content.
	} else if parser.At(syntax.TkLineBreak) {
		parser.Bump()
	} else if !parser.AtEnd() &&
		!parser.At(syntax.TkDocumentStart) &&
		!parser.At(syntax.TkDocumentEnd) {
		consumeLineEnd(parser)
	}
	if hasCollectionDecorator {
		skipBlankLines(parser)
		nextIndent := currentIndent(parser)
		if !parser.AtEnd() &&
			!parser.At(syntax.TkDocumentStart) &&
			!parser.At(syntax.TkDocumentEnd) &&
			nextIndent > indent {
			parseBlock(parser, nextIndent)
		}
	}
	parser.Complete(marker, syntax.YamlSequenceItem)
}

func parseCompactMapping(parser *parsekit.Parser, indent int) {
	marker := parser.Start()
	parsePairLine(parser, indent, false)

	for !parser.AtEnd() {
		skipBlankLines(parser)
		if currentIndent(parser) != indent ||
			lineStartsSequence(parser) ||
			!lineHasTopLevelColon(parser) {
			break
		}
		parsePairLine(parser, indent, true)
	}
	parser.Complete(marker, syntax.YamlMapping)
}

func parseValueLine(parser *parsekit.Parser) bool {
	if parser.At(syntax.TkIndent) {
		consumeIndent(parser)
	}
	blockScalar := parser.At(syntax.TkBlockScalar)
	if !parseValue(parser) {
		return false
	}
	if !blockScalar {
		consumeLineEnd(parser)
	}
	return true
}

func parseValue(parser *parsekit.Parser) bool {
	switch {
	case parser.At(syntax.TkOpenBrace):
		parseFlowMapping(parser)
	case parser.At(syntax.TkOpenBracket):
		parseFlowSequence(parser)
	default:
		return parseScalar(parser)
	}
	return true
}

func parseScalar(parser *parsekit.Parser) bool {
	if !parser.AtSet([]syntax.Kind{
		syntax.TkPlainScalar,
		syntax.TkSingleQuotedScalar,
		syntax.TkDoubleQuotedScalar,
		syntax.TkBlockScalar,
	}) {
		return false
	}

	marker := parser.Start()
	token := parser.Bump()
	switch token.Kind {
	case syntax.TkSingleQuotedScalar:
		if len(token.Text()) < 2 || token.Text()[len(token.Text())-1] != '\'' {
			parser.AddError(parsekit.NewErrorBuilder("closing single quote").AtToken(token))
		}
	case syntax.TkDoubleQuotedScalar:
		if len(token.Text()) < 2 || token.Text()[len(token.Text())-1] != '"' || hasInvalidDoubleQuotedEscape(token.Text()) {
			parser.AddError(parsekit.NewErrorBuilder("valid double-quoted scalar").AtToken(token))
		}
	}
	parser.Complete(marker, syntax.YamlScalar)
	return true
}

func parseFlowMapping(parser *parsekit.Parser) {
	marker := parser.Start()
	parser.Bump()
	skipFlowSeparation(parser)

	for !parser.AtEnd() && !parser.At(syntax.TkCloseBrace) {
		position := parser.GetPos()
		pair := parser.Start()
		if !parseScalar(parser) {
			parser.AddError(parsekit.NewErrorBuilder("flow mapping key"))
			parser.Recover([]syntax.Kind{syntax.TkColon, syntax.TkComma, syntax.TkCloseBrace})
		}
		parser.Expect(syntax.TkColon, []syntax.Kind{syntax.TkComma, syntax.TkCloseBrace})
		if !parseValue(parser) {
			completeNull(parser)
		}
		parser.Complete(pair, syntax.YamlPair)
		skipFlowSeparation(parser)

		if parser.At(syntax.TkComma) {
			parser.Bump()
			skipFlowSeparation(parser)
		} else if !parser.At(syntax.TkCloseBrace) {
			parser.AddError(parsekit.NewErrorBuilder("\",\" or \"}\""))
			parser.Recover([]syntax.Kind{syntax.TkComma, syntax.TkCloseBrace})
			if parser.At(syntax.TkComma) {
				parser.Bump()
			}
		}
		if parser.GetPos() == position {
			break
		}
	}

	parser.Expect(syntax.TkCloseBrace, valueRecovery)
	parser.Complete(marker, syntax.YamlFlowMapping)
}

func parseFlowSequence(parser *parsekit.Parser) {
	marker := parser.Start()
	parser.Bump()
	skipFlowSeparation(parser)

	for !parser.AtEnd() && !parser.At(syntax.TkCloseBracket) {
		position := parser.GetPos()
		item := parser.Start()
		if !parseValue(parser) {
			parser.AddError(parsekit.NewErrorBuilder("flow sequence value"))
			parser.Recover([]syntax.Kind{syntax.TkComma, syntax.TkCloseBracket})
		}
		parser.Complete(item, syntax.YamlSequenceItem)
		skipFlowSeparation(parser)

		if parser.At(syntax.TkComma) {
			parser.Bump()
			skipFlowSeparation(parser)
		} else if !parser.At(syntax.TkCloseBracket) {
			parser.AddError(parsekit.NewErrorBuilder("\",\" or \"]\""))
			parser.Recover([]syntax.Kind{syntax.TkComma, syntax.TkCloseBracket})
			if parser.At(syntax.TkComma) {
				parser.Bump()
			}
		}
		if parser.GetPos() == position {
			break
		}
	}

	parser.Expect(syntax.TkCloseBracket, valueRecovery)
	parser.Complete(marker, syntax.YamlFlowSequence)
}

func completeNull(parser *parsekit.Parser) {
	marker := parser.Start()
	parser.Complete(marker, syntax.YamlNull)
}

func skipBlankLines(parser *parsekit.Parser) {
	for {
		if parser.At(syntax.TkLineBreak) {
			parser.Bump()
			continue
		}
		if parser.At(syntax.TkIndent) && nonTriviaAfterCurrent(parser) == syntax.TkLineBreak {
			consumeIndent(parser)
			parser.Bump()
			continue
		}
		return
	}
}

func skipFlowSeparation(parser *parsekit.Parser) {
	for parser.At(syntax.TkLineBreak) || parser.At(syntax.TkIndent) {
		if parser.At(syntax.TkIndent) {
			consumeIndent(parser)
		} else {
			parser.Bump()
		}
	}
}

func consumeIndent(parser *parsekit.Parser) {
	token := parser.Bump()
	if strings.ContainsRune(token.Text(), '\t') {
		parser.AddError(parsekit.NewErrorBuilder("spaces for YAML indentation").AtToken(token))
	}
}

func consumeLineEnd(parser *parsekit.Parser) {
	if parser.At(syntax.TkLineBreak) {
		parser.Bump()
		return
	}
	if parser.AtEnd() ||
		parser.At(syntax.TkDocumentStart) ||
		parser.At(syntax.TkDocumentEnd) {
		return
	}
	parser.AddError(parsekit.NewErrorBuilder("end of line"))
	recoverLine(parser)
}

func recoverLine(parser *parsekit.Parser) {
	parser.Recover([]syntax.Kind{syntax.TkLineBreak})
	if parser.At(syntax.TkLineBreak) {
		parser.Bump()
	}
}

func currentIndent(parser *parsekit.Parser) int {
	if !parser.At(syntax.TkIndent) {
		return 0
	}
	width := 0
	for _, value := range parser.PeekToken().Text() {
		if value == '\t' {
			width += 2
		} else {
			width++
		}
	}
	return width
}

func lineStartsSequence(parser *parsekit.Parser) bool {
	depth := 0
	for offset := 0; ; offset++ {
		token := parser.PeekNthToken(offset)
		if token == nil || token.Kind == syntax.TkLineBreak {
			return false
		}
		if token.Kind.IsTrivia() || token.Kind == syntax.TkIndent {
			continue
		}
		if depth == 0 {
			return token.Kind == syntax.TkDash
		}
	}
}

func lineHasTopLevelColon(parser *parsekit.Parser) bool {
	depth := 0
	for offset := 0; ; offset++ {
		token := parser.PeekNthToken(offset)
		if token == nil || token.Kind == syntax.TkLineBreak {
			return false
		}
		if token.Kind.IsTrivia() || token.Kind == syntax.TkIndent {
			continue
		}
		switch token.Kind {
		case syntax.TkOpenBracket, syntax.TkOpenBrace:
			depth++
		case syntax.TkCloseBracket, syntax.TkCloseBrace:
			if depth > 0 {
				depth--
			}
		case syntax.TkColon:
			if depth == 0 {
				return true
			}
		}
	}
}

func nonTriviaAfterCurrent(parser *parsekit.Parser) syntax.Kind {
	for offset := 1; ; offset++ {
		token := parser.PeekNthToken(offset)
		if token == nil {
			return syntax.KindNone
		}
		if !token.Kind.IsTrivia() {
			return token.Kind
		}
	}
}

func hasInvalidDoubleQuotedEscape(text string) bool {
	if len(text) < 2 {
		return true
	}
	for position := 1; position < len(text)-1; position++ {
		position = bytescan.IndexByte(text[:len(text)-1], position, '\\')
		if position >= len(text)-1 {
			break
		}
		position++
		if position >= len(text)-1 {
			return true
		}
		switch text[position] {
		case '0', 'a', 'b', 't', 'n', 'v', 'f', 'r', 'e', ' ', '"', '/', '\\', 'N', '_', 'L', 'P':
		case 'x':
			if !hasHexDigits(text, position+1, 2) {
				return true
			}
			position += 2
		case 'u':
			if !hasHexDigits(text, position+1, 4) {
				return true
			}
			position += 4
		case 'U':
			if !hasHexDigits(text, position+1, 8) {
				return true
			}
			position += 8
		case '\r', '\n':
			// YAML permits escaped physical line breaks.
		default:
			return true
		}
	}
	return false
}

func hasHexDigits(text string, start, count int) bool {
	if start+count > len(text)-1 {
		return false
	}
	for _, digit := range text[start : start+count] {
		if !isHexDigit(digit) {
			return false
		}
	}
	return true
}

func isHexDigit(digit rune) bool {
	return digit >= '0' && digit <= '9' ||
		digit >= 'a' && digit <= 'f' ||
		digit >= 'A' && digit <= 'F'
}

func isCollectionDecorator(text string) bool {
	text = strings.TrimSpace(text)
	if len(text) < 2 || text[0] != '&' && text[0] != '!' {
		return false
	}
	return !strings.ContainsAny(text, " \t")
}
