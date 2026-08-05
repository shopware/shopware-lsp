package twig

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

// TypesTagDeclaration is one variable/type pair from Twig's static-analysis
// tag, for example `{% types { product: 'App\\Product' } %}`.
type TypesTagDeclaration struct {
	Name      string
	Type      string
	Optional  bool
	NameRange cst.TextRange
	TypeRange cst.TextRange
}

type TypesTagClassReference struct {
	Name  string
	Raw   string
	Range cst.TextRange
}

type TypesTagCompletionContext struct {
	Prefix string
	Range  cst.TextRange
}

// TypesTagDeclarations parses complete and editor-incomplete `{% types %}`
// declarations from the lossless source. Unknown extension tags are recovered
// as raw text by the native Twig grammar, so this query intentionally shares
// the tag scanner used by custom token-parser intelligence.
func TypesTagDeclarations(content []byte) []TypesTagDeclaration {
	var result []TypesTagDeclaration
	for _, region := range typesTagRegions(content) {
		result = append(
			result,
			parseTypesTagDeclarations(content, region.start, region.end)...,
		)
	}
	return result
}

// TypesTagCompletionAt recognizes the PHP class-name component under offset.
// The replacement range is limited to that component, preserving surrounding
// unions, intersections, generics, and array suffixes.
func TypesTagCompletionAt(
	content []byte,
	offset uint32,
) (TypesTagCompletionContext, bool) {
	if uint64(offset) > uint64(len(content)) {
		return TypesTagCompletionContext{}, false
	}
	for _, declaration := range TypesTagDeclarations(content) {
		if offset < declaration.TypeRange.Start ||
			offset > declaration.TypeRange.End {
			continue
		}
		rng := typesTagClassComponentRange(
			content,
			declaration.TypeRange,
			offset,
		)
		if offset < rng.Start || offset > rng.End {
			return TypesTagCompletionContext{}, false
		}
		return TypesTagCompletionContext{
			Prefix: normalizeTwigTypeClassName(
				string(content[rng.Start:offset]),
			),
			Range: rng,
		}, true
	}
	return TypesTagCompletionContext{}, false
}

func TypesTagClassReferences(
	content []byte,
) []TypesTagClassReference {
	var result []TypesTagClassReference
	for _, declaration := range TypesTagDeclarations(content) {
		result = append(
			result,
			typesTagClassReferencesInDeclaration(content, declaration)...,
		)
	}
	return result
}

func TypesTagClassReferenceAt(
	content []byte,
	offset uint32,
) (TypesTagClassReference, bool) {
	for _, reference := range TypesTagClassReferences(content) {
		if offset >= reference.Range.Start &&
			offset <= reference.Range.End {
			return reference, true
		}
	}
	return TypesTagClassReference{}, false
}

type typesTagRegion struct {
	start int
	end   int
}

func typesTagRegions(content []byte) []typesTagRegion {
	var result []typesTagRegion
	for _, usage := range TwigTagUsages(content) {
		if !strings.EqualFold(usage.Name, "types") {
			continue
		}
		start := int(usage.Range.End)
		end := len(content)
		if closeOffset := bytes.Index(
			content[start:],
			[]byte("%}"),
		); closeOffset >= 0 {
			end = start + closeOffset
		}
		if end > start &&
			(content[end-1] == '-' || content[end-1] == '~') {
			end--
		}
		result = append(result, typesTagRegion{start: start, end: end})
	}
	return result
}

func parseTypesTagDeclarations(
	content []byte,
	start,
	end int,
) []TypesTagDeclaration {
	cursor := start
	for cursor < end && isTwigTagSpace(content[cursor]) {
		cursor++
	}
	if cursor >= end || content[cursor] != '{' {
		return nil
	}
	cursor++
	var result []TypesTagDeclaration
	for cursor < end {
		skipTypesTagSeparators(content, &cursor, end)
		if cursor >= end || content[cursor] == '}' {
			break
		}
		name, nameRange, found := parseTypesTagKey(content, &cursor, end)
		if !found {
			cursor++
			continue
		}
		for cursor < end && isTwigTagSpace(content[cursor]) {
			cursor++
		}
		optional := false
		if cursor < end && content[cursor] == '?' {
			optional = true
			cursor++
			for cursor < end && isTwigTagSpace(content[cursor]) {
				cursor++
			}
		}
		if cursor >= end || content[cursor] != ':' {
			continue
		}
		cursor++
		for cursor < end && isTwigTagSpace(content[cursor]) {
			cursor++
		}
		if cursor >= end ||
			(content[cursor] != '\'' && content[cursor] != '"') {
			continue
		}
		quote := content[cursor]
		cursor++
		typeStart := cursor
		for cursor < end {
			if content[cursor] == quote &&
				!typesTagEscapedQuote(content, typeStart, cursor) {
				break
			}
			cursor++
		}
		typeEnd := cursor
		if cursor < end && content[cursor] == quote {
			cursor++
		}
		rawType := string(content[typeStart:typeEnd])
		result = append(result, TypesTagDeclaration{
			Name:      name,
			Type:      normalizeTwigTypeString(rawType),
			Optional:  optional,
			NameRange: nameRange,
			TypeRange: cst.TextRange{
				Start: uint32(typeStart),
				End:   uint32(typeEnd),
			},
		})
	}
	return result
}

func skipTypesTagSeparators(
	content []byte,
	cursor *int,
	end int,
) {
	for *cursor < end &&
		(isTwigTagSpace(content[*cursor]) || content[*cursor] == ',') {
		(*cursor)++
	}
}

func parseTypesTagKey(
	content []byte,
	cursor *int,
	end int,
) (string, cst.TextRange, bool) {
	if *cursor >= end {
		return "", cst.TextRange{}, false
	}
	if content[*cursor] == '\'' || content[*cursor] == '"' {
		quote := content[*cursor]
		(*cursor)++
		start := *cursor
		for *cursor < end && content[*cursor] != quote {
			(*cursor)++
		}
		finish := *cursor
		if *cursor < end {
			(*cursor)++
		}
		return string(content[start:finish]), cst.TextRange{
			Start: uint32(start),
			End:   uint32(finish),
		}, finish > start
	}
	start := *cursor
	for *cursor < end && isTypesTagKeyByte(content[*cursor]) {
		(*cursor)++
	}
	if *cursor == start {
		return "", cst.TextRange{}, false
	}
	return string(content[start:*cursor]), cst.TextRange{
		Start: uint32(start),
		End:   uint32(*cursor),
	}, true
}

func typesTagEscapedQuote(content []byte, start, quote int) bool {
	slashes := 0
	for index := quote - 1; index >= start && content[index] == '\\'; index-- {
		slashes++
	}
	return slashes%2 != 0
}

func normalizeTwigTypeString(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\\`, `\`))
	return strings.TrimPrefix(value, `\`)
}

func normalizeTwigTypeClassName(value string) string {
	return strings.TrimPrefix(normalizeTwigTypeString(value), `\`)
}

func typesTagClassComponentRange(
	content []byte,
	outer cst.TextRange,
	offset uint32,
) cst.TextRange {
	start := offset
	for start > outer.Start &&
		isTypesTagClassByte(content[start-1]) {
		start--
	}
	end := offset
	for end < outer.End && isTypesTagClassByte(content[end]) {
		end++
	}
	return cst.TextRange{Start: start, End: end}
}

func typesTagClassReferencesInDeclaration(
	content []byte,
	declaration TypesTagDeclaration,
) []TypesTagClassReference {
	if declaration.TypeRange.End > uint32(len(content)) {
		return nil
	}
	raw := content[declaration.TypeRange.Start:declaration.TypeRange.End]
	var result []TypesTagClassReference
	for cursor := 0; cursor < len(raw); {
		if !isTypesTagClassStart(raw[cursor]) {
			cursor++
			continue
		}
		start := cursor
		for cursor < len(raw) && isTypesTagClassByte(raw[cursor]) {
			cursor++
		}
		candidate := string(raw[start:cursor])
		name := normalizeTwigTypeClassName(candidate)
		if !typesTagPHPClassName(name) {
			continue
		}
		result = append(result, TypesTagClassReference{
			Name: name,
			Raw:  candidate,
			Range: cst.TextRange{
				Start: declaration.TypeRange.Start + uint32(start),
				End:   declaration.TypeRange.Start + uint32(cursor),
			},
		})
	}
	return result
}

func typesTagPHPClassName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if _, builtin := nonClassTwigTypes[strings.ToLower(value)]; builtin {
		return false
	}
	first, _ := utf8.DecodeRuneInString(
		strings.TrimPrefix(value, `\`),
	)
	return strings.Contains(value, `\`) || unicode.IsUpper(first)
}

func isTypesTagKeyByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func isTypesTagClassStart(value byte) bool {
	return value == '\\' || value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= utf8.RuneSelf
}

func isTypesTagClassByte(value byte) bool {
	return isTypesTagClassStart(value) ||
		value >= '0' && value <= '9'
}
