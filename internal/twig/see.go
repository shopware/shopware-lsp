package twig

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
)

// SeeReference is a target declared by a Twig documentation comment such as
// `{# @see App\Controller\ProductController::show #}`.
type SeeReference struct {
	Target string
	Range  cst.TextRange
}

type SeeCompletionContext struct {
	Prefix string
	Range  cst.TextRange
}

// SeeReferences returns explicit @see targets and the reference plugin's
// legacy single-target comment form.
func SeeReferences(root *twigsyntax.Node) []SeeReference {
	if root == nil {
		return nil
	}
	var result []SeeReference
	excluded := seeVerbatimRanges([]byte(root.Text()))
	for _, comment := range twigquery.Nodes(root, twigsyntax.TwigComment) {
		if seeRangeExcluded(comment.Range(), excluded) {
			continue
		}
		for _, reference := range seeReferencesInComment(comment, false) {
			if reference.Target != "" {
				result = append(result, reference)
			}
		}
	}
	return result
}

func SeeReferenceAt(
	root *twigsyntax.Node,
	offset uint32,
) (SeeReference, bool) {
	for _, reference := range SeeReferences(root) {
		if offset >= reference.Range.Start &&
			offset <= reference.Range.End {
			return reference, true
		}
	}
	return SeeReference{}, false
}

// SeeCompletionAt recognizes an explicit @see target under construction.
// Legacy bare comments remain navigation-only to avoid offering completions
// in ordinary prose comments.
func SeeCompletionAt(
	root *twigsyntax.Node,
	offset uint32,
) (SeeCompletionContext, bool) {
	if root == nil {
		return SeeCompletionContext{}, false
	}
	excluded := seeVerbatimRanges([]byte(root.Text()))
	for _, comment := range twigquery.Nodes(root, twigsyntax.TwigComment) {
		if offset < comment.Range().Start || offset > comment.Range().End {
			continue
		}
		if seeRangeExcluded(comment.Range(), excluded) {
			continue
		}
		for _, reference := range seeReferencesInComment(comment, true) {
			if offset < reference.Range.Start ||
				offset > reference.Range.End {
				continue
			}
			length := offset - reference.Range.Start
			if length > uint32(len(reference.Target)) {
				continue
			}
			return SeeCompletionContext{
				Prefix: reference.Target[:length],
				Range:  reference.Range,
			}, true
		}
	}
	return SeeCompletionContext{}, false
}

// SeePHPClassAndMember normalizes PHP class and optional method spellings used
// by Twig @see comments. Both Class::method and the legacy Class:method form
// are accepted.
func SeePHPClassAndMember(
	target string,
) (string, string, bool) {
	target = strings.TrimSpace(strings.ReplaceAll(target, `\\`, `\`))
	target = strings.TrimSuffix(target, "[]")
	if target == "" ||
		strings.HasSuffix(strings.ToLower(target), ".twig") ||
		strings.HasPrefix(target, "@") ||
		strings.HasPrefix(target, ".") ||
		strings.Contains(target, "/") {
		return "", "", false
	}
	className, member := target, ""
	switch {
	case strings.Count(target, "::") == 1:
		parts := strings.SplitN(target, "::", 2)
		className, member = parts[0], parts[1]
	case strings.Count(target, ":") == 1:
		parts := strings.SplitN(target, ":", 2)
		className, member = parts[0], parts[1]
	case strings.Contains(target, ":"):
		return "", "", false
	}
	className = strings.TrimLeft(strings.TrimSpace(className), `\`)
	member = strings.TrimSpace(member)
	if className == "" || strings.ContainsAny(className, ".@") {
		return "", "", false
	}
	return className, member, true
}

func seeReferencesInComment(
	comment *twigsyntax.Node,
	includeEmpty bool,
) []SeeReference {
	if comment == nil {
		return nil
	}
	text := comment.Text()
	innerStart := strings.Index(text, "{#")
	if innerStart < 0 {
		return nil
	}
	innerStart += 2
	innerEnd := strings.LastIndex(text, "#}")
	if innerEnd < innerStart {
		innerEnd = len(text)
	}
	base := int(comment.Range().Start)
	var result []SeeReference
	for cursor := innerStart; cursor < innerEnd; {
		index := strings.Index(text[cursor:innerEnd], "@see")
		if index < 0 {
			break
		}
		index += cursor
		after := index + len("@see")
		if index > innerStart && isSeeWordByte(text[index-1]) ||
			after < innerEnd && isSeeWordByte(text[after]) {
			cursor = after
			continue
		}
		if after >= innerEnd || !isSeeSpace(text[after]) {
			cursor = after
			continue
		}
		for after < innerEnd && isSeeSpace(text[after]) {
			after++
		}
		end := after
		for end < innerEnd && isSeeTargetByte(text[end]) {
			end++
		}
		if end > after || includeEmpty {
			result = append(result, SeeReference{
				Target: text[after:end],
				Range: cst.TextRange{
					Start: uint32(base + after),
					End:   uint32(base + end),
				},
			})
		}
		cursor = end
		if cursor == after {
			cursor++
		}
	}
	if len(result) != 0 {
		return result
	}

	start, end := innerStart, innerEnd
	for start < end && isSeeSpace(text[start]) {
		start++
	}
	for end > start && isSeeSpace(text[end-1]) {
		end--
	}
	target := text[start:end]
	if target == "" || strings.ContainsFunc(target, unicode.IsSpace) ||
		!looksLikeLegacySeeTarget(target) {
		return nil
	}
	return []SeeReference{{
		Target: target,
		Range: cst.TextRange{
			Start: uint32(base + start),
			End:   uint32(base + end),
		},
	}}
}

func looksLikeLegacySeeTarget(target string) bool {
	if strings.HasPrefix(target, "@") ||
		strings.HasPrefix(target, `\`) ||
		strings.HasPrefix(target, ".") ||
		strings.ContainsAny(target, `\/:`) ||
		strings.HasSuffix(strings.ToLower(target), ".twig") {
		return true
	}
	first, _ := utf8.DecodeRuneInString(strings.TrimSuffix(target, "[]"))
	return unicode.IsUpper(first)
}

func isSeeSpace(value byte) bool {
	return value == ' ' || value == '\t' ||
		value == '\r' || value == '\n'
}

func isSeeWordByte(value byte) bool {
	return value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func isSeeTargetByte(value byte) bool {
	return !isSeeSpace(value) && value != '#'
}

func seeVerbatimRanges(content []byte) []cst.TextRange {
	var result []cst.TextRange
	endTag := ""
	bodyStart := 0
	for offset := 0; offset+2 <= len(content); {
		if endTag == "" && bytes.HasPrefix(content[offset:], []byte("{#")) {
			close := bytes.Index(content[offset+2:], []byte("#}"))
			if close < 0 {
				break
			}
			offset += close + 4
			continue
		}
		relative := bytes.Index(content[offset:], []byte("{%"))
		if relative < 0 {
			break
		}
		open := offset + relative
		cursor := open + 2
		if cursor < len(content) &&
			(content[cursor] == '-' || content[cursor] == '~') {
			cursor++
		}
		for cursor < len(content) && isTwigTagSpace(content[cursor]) {
			cursor++
		}
		start := cursor
		for cursor < len(content) && isTwigTagNameByte(content[cursor]) {
			cursor++
		}
		name := strings.ToLower(string(content[start:cursor]))
		close := bytes.Index(content[cursor:], []byte("%}"))
		if close < 0 {
			break
		}
		close += cursor + 2
		switch {
		case endTag == "" && (name == "raw" || name == "verbatim"):
			endTag = "end" + name
			bodyStart = close
		case endTag != "" && name == endTag:
			result = append(result, cst.TextRange{
				Start: uint32(bodyStart),
				End:   uint32(open),
			})
			endTag = ""
		}
		offset = close
	}
	if endTag != "" {
		result = append(result, cst.TextRange{
			Start: uint32(bodyStart),
			End:   uint32(len(content)),
		})
	}
	return result
}

func seeRangeExcluded(
	rng cst.TextRange,
	excluded []cst.TextRange,
) bool {
	for _, candidate := range excluded {
		if rng.Start >= candidate.Start && rng.Start < candidate.End {
			return true
		}
	}
	return false
}
