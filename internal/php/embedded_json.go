package php

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const (
	symfonyJSONResponseClass = "Symfony\\Component\\HttpFoundation\\JsonResponse"
	symfonyDomCrawlerClass   = "Symfony\\Component\\DomCrawler\\Crawler"
	symfonyCSSConverter      = "Symfony\\Component\\CssSelector\\CssSelectorConverter"
)

// EmbeddedPHPString is a decoded, static PHP string. SourceOffsets maps each
// decoded byte boundary back to the corresponding absolute byte offset in the
// PHP document, so embedded parser ranges remain exact through PHP escapes.
type EmbeddedPHPString struct {
	Value         string
	ContentRange  cst.TextRange
	SourceOffsets []uint32
}

// EmbeddedJSONLiteral names an EmbeddedPHPString recognized in a Symfony
// JsonResponse JSON entry point.
type EmbeddedJSONLiteral = EmbeddedPHPString

type EmbeddedLanguage uint8

const (
	EmbeddedLanguageJSON EmbeddedLanguage = iota + 1
	EmbeddedLanguageCSS
	EmbeddedLanguageXPath
)

type EmbeddedLanguageString struct {
	EmbeddedPHPString
	Language EmbeddedLanguage
}

// SourceRange maps a byte range in Value back into the host PHP document.
func (literal EmbeddedPHPString) SourceRange(
	embedded cst.TextRange,
) cst.TextRange {
	if len(literal.SourceOffsets) == 0 {
		return literal.ContentRange
	}
	last := uint32(len(literal.SourceOffsets) - 1)
	start := min(embedded.Start, last)
	end := min(embedded.End, last)
	if end < start {
		end = start
	}
	return cst.TextRange{
		Start: literal.SourceOffsets[start],
		End:   literal.SourceOffsets[end],
	}
}

// EmbeddedJSONLiterals returns direct, static string arguments of
// JsonResponse::fromJsonString() and JsonResponse::setJson(). Receiver types
// are resolved semantically, so aliases and subclasses are supported while
// unrelated methods with the same name are ignored.
func EmbeddedJSONLiterals(
	index *PHPIndex,
	path string,
	version int,
	source string,
	root *cst.Node,
) []EmbeddedJSONLiteral {
	var result []EmbeddedJSONLiteral
	for _, literal := range EmbeddedLanguageStrings(
		index,
		path,
		version,
		source,
		root,
	) {
		if literal.Language == EmbeddedLanguageJSON {
			result = append(result, literal.EmbeddedPHPString)
		}
	}
	return result
}

// EmbeddedLanguageStrings recognizes all static PHP argument signatures used
// by the reference plugin's JSON, CSS selector, and XPath injections. It scans
// the CST and performs semantic receiver analysis once per document.
func EmbeddedLanguageStrings(
	index *PHPIndex,
	path string,
	version int,
	source string,
	root *cst.Node,
) []EmbeddedLanguageString {
	if index == nil || root == nil || source == "" {
		return nil
	}
	type candidate struct {
		call     *phpsyntax.Node
		literal  *phpsyntax.Node
		language EmbeddedLanguage
		target   string
	}
	var candidates []candidate
	for _, call := range phpquery.Nodes(
		root,
		phpsyntax.PhpMemberCall,
		phpsyntax.PhpScopedCall,
	) {
		var (
			language EmbeddedLanguage
			target   string
		)
		switch strings.ToLower(phpquery.CallMethodName(call)) {
		case "fromjsonstring", "setjson":
			language, target = EmbeddedLanguageJSON, symfonyJSONResponseClass
		case "filter", "children":
			language, target = EmbeddedLanguageCSS, symfonyDomCrawlerClass
		case "toxpath":
			language, target = EmbeddedLanguageCSS, symfonyCSSConverter
		case "filterxpath", "evaluate":
			language, target = EmbeddedLanguageXPath, symfonyDomCrawlerClass
		default:
			continue
		}
		literal := phpquery.StringArgument(call, 0)
		if literal == nil ||
			phpquery.ArgumentExpression(call, 0) != literal {
			continue
		}
		candidates = append(candidates, candidate{
			call:     call,
			literal:  literal,
			language: language,
			target:   target,
		})
	}
	if len(candidates) == 0 {
		return nil
	}
	document := index.AnalyzeDocument(path, version, root)
	if document == nil {
		return nil
	}
	snapshot := index.SemanticSnapshot().WithDocument(document)
	if snapshot == nil {
		return nil
	}

	var result []EmbeddedLanguageString
	for _, candidate := range candidates {
		call, literal := candidate.call, candidate.literal
		receiver := phpquery.CallReceiver(call)
		receiverType := document.TypeOf(receiver).Type
		if receiverType.IsUnknown() &&
			call.Kind() == phpsyntax.PhpScopedCall &&
			receiver != nil &&
			receiver.Kind() == phpsyntax.PhpName {
			name := assistantNameContext(
				document,
				receiver.Range().Start,
			).ResolveClass(strings.TrimSpace(receiver.Text()))
			receiverType = types.Named(name)
		}
		if receiver == nil || receiverType.IsUnknown() ||
			!snapshot.Relations().IsSubtype(
				receiverType,
				types.Named(candidate.target),
			) {
			continue
		}
		decoded, offsets, contentRange, ok := decodeStaticPHPString(
			source,
			literal,
		)
		if !ok {
			continue
		}
		result = append(result, EmbeddedLanguageString{
			EmbeddedPHPString: EmbeddedPHPString{
				Value:         decoded,
				ContentRange:  contentRange,
				SourceOffsets: offsets,
			},
			Language: candidate.language,
		})
	}
	return result
}

func decodeStaticPHPString(
	source string,
	literal *phpsyntax.Node,
) (string, []uint32, cst.TextRange, bool) {
	contentRange := phpquery.StringContentRange(literal)
	if contentRange.Start == 0 ||
		contentRange.Start > contentRange.End ||
		contentRange.End >= uint32(len(source)) {
		return "", nil, cst.TextRange{}, false
	}
	quote := source[contentRange.Start-1]
	if (quote != '\'' && quote != '"') ||
		source[contentRange.End] != quote {
		return "", nil, cst.TextRange{}, false
	}
	raw := []byte(source[contentRange.Start:contentRange.End])
	output := make([]byte, 0, len(raw))
	offsets := []uint32{contentRange.Start}
	appendMapped := func(value []byte, start, end uint32) {
		output = append(output, value...)
		for index := range value {
			mapped := start
			if index == len(value)-1 {
				mapped = end
			}
			offsets = append(offsets, mapped)
		}
	}
	appendRawByte := func(position int) {
		start := contentRange.Start + uint32(position)
		appendMapped(raw[position:position+1], start, start+1)
	}

	for position := 0; position < len(raw); {
		if quote == '"' && phpStringInterpolationAt(raw, position) {
			return "", nil, cst.TextRange{}, false
		}
		if raw[position] != '\\' || position+1 >= len(raw) {
			appendRawByte(position)
			position++
			continue
		}

		escapeStart := contentRange.Start + uint32(position)
		next := raw[position+1]
		if quote == '\'' {
			if next == '\\' || next == '\'' {
				appendMapped(
					[]byte{next},
					escapeStart,
					escapeStart+2,
				)
				position += 2
				continue
			}
			appendRawByte(position)
			position++
			continue
		}

		var escaped byte
		switch next {
		case 'n':
			escaped = '\n'
		case 'r':
			escaped = '\r'
		case 't':
			escaped = '\t'
		case 'v':
			escaped = '\v'
		case 'e':
			escaped = 0x1b
		case 'f':
			escaped = '\f'
		case '\\', '$', '"':
			escaped = next
		default:
			escaped = 0
		}
		if escaped != 0 {
			appendMapped(
				[]byte{escaped},
				escapeStart,
				escapeStart+2,
			)
			position += 2
			continue
		}

		if next == 'x' || next == 'X' {
			end := position + 2
			for end < len(raw) && end < position+4 &&
				isPHPHexDigit(raw[end]) {
				end++
			}
			if end > position+2 {
				value, err := strconv.ParseUint(
					string(raw[position+2:end]),
					16,
					8,
				)
				if err == nil {
					appendMapped(
						[]byte{byte(value)},
						escapeStart,
						contentRange.Start+uint32(end),
					)
					position = end
					continue
				}
			}
		}
		if next >= '0' && next <= '7' {
			end := position + 1
			for end < len(raw) && end < position+4 &&
				raw[end] >= '0' && raw[end] <= '7' {
				end++
			}
			value, err := strconv.ParseUint(
				string(raw[position+1:end]),
				8,
				8,
			)
			if err == nil {
				appendMapped(
					[]byte{byte(value)},
					escapeStart,
					contentRange.Start+uint32(end),
				)
				position = end
				continue
			}
		}
		if next == 'u' && position+3 < len(raw) &&
			raw[position+2] == '{' {
			closeOffset := bytes.IndexByte(raw[position+3:], '}')
			if closeOffset >= 0 {
				end := position + 3 + closeOffset
				value, err := strconv.ParseUint(
					string(raw[position+3:end]),
					16,
					32,
				)
				if err == nil && utf8.ValidRune(rune(value)) {
					buffer := make([]byte, utf8.RuneLen(rune(value)))
					utf8.EncodeRune(buffer, rune(value))
					appendMapped(
						buffer,
						escapeStart,
						contentRange.Start+uint32(end+1),
					)
					position = end + 1
					continue
				}
			}
		}

		// PHP preserves the backslash for unknown double-quoted escapes.
		appendRawByte(position)
		position++
	}
	return string(output), offsets, contentRange, true
}

func phpStringInterpolationAt(source []byte, position int) bool {
	if position < 0 || position >= len(source) {
		return false
	}
	if source[position] == '{' {
		return position+1 < len(source) && source[position+1] == '$'
	}
	if source[position] != '$' || position+1 >= len(source) {
		return false
	}
	next := source[position+1]
	return next == '{' || next == '_' ||
		next >= 'a' && next <= 'z' ||
		next >= 'A' && next <= 'Z' ||
		next >= 0x80
}

func isPHPHexDigit(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}
