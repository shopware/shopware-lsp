package twig

import (
	"bytes"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

type GuardKind uint8

const (
	GuardFunction GuardKind = iota + 1
	GuardFilter
	GuardTest
)

func (kind GuardKind) String() string {
	switch kind {
	case GuardFunction:
		return "function"
	case GuardFilter:
		return "filter"
	case GuardTest:
		return "test"
	default:
		return ""
	}
}

type GuardCompletionContext struct {
	Kind     GuardKind
	Callable bool
	Prefix   string
	Range    cst.TextRange
}

type GuardReference struct {
	Kind  GuardKind
	Name  string
	Range cst.TextRange
}

// GuardCompletionAt recognizes the two completion positions supported by
// Twig's guard tag:
//
//	{% guard <type> %}
//	{% guard function <callable> %}
//
// The native Twig parser deliberately recovers unknown extension tags as raw
// text, so this query works from the lossless source while reusing
// TwigTagUsages to exclude comments and raw/verbatim bodies.
func GuardCompletionAt(
	content []byte,
	offset uint32,
) (GuardCompletionContext, bool) {
	_, tailStart, tail, found := activeGuardTag(content, offset)
	if !found {
		return GuardCompletionContext{}, false
	}
	tokens, valid := guardTokens(tail, tailStart)
	if !valid {
		return GuardCompletionContext{}, false
	}
	trailingSpace := len(tail) != 0 && isTwigTagSpace(tail[len(tail)-1])
	switch len(tokens) {
	case 0:
		return GuardCompletionContext{
			Range: cst.TextRange{Start: offset, End: offset},
		}, true
	case 1:
		kind := parseGuardKind(tokens[0].value)
		if kind != 0 && trailingSpace {
			return GuardCompletionContext{
				Kind:     kind,
				Callable: true,
				Range:    cst.TextRange{Start: offset, End: offset},
			}, true
		}
		return GuardCompletionContext{
			Prefix: tokens[0].value,
			Range:  tokens[0].rng,
		}, true
	case 2:
		kind := parseGuardKind(tokens[0].value)
		if kind == 0 || trailingSpace {
			return GuardCompletionContext{}, false
		}
		return GuardCompletionContext{
			Kind:     kind,
			Callable: true,
			Prefix:   tokens[1].value,
			Range:    tokens[1].rng,
		}, true
	default:
		return GuardCompletionContext{}, false
	}
}

func GuardReferenceAt(
	content []byte,
	offset uint32,
) (GuardReference, bool) {
	for _, usage := range TwigTagUsages(content) {
		if !strings.EqualFold(usage.Name, "guard") {
			continue
		}
		closeOffset := bytes.Index(content[usage.Range.End:], []byte("%}"))
		if closeOffset < 0 {
			continue
		}
		tailStart := int(usage.Range.End)
		tailEnd := tailStart + closeOffset
		for tailEnd > tailStart && isTwigTagSpace(content[tailEnd-1]) {
			tailEnd--
		}
		if tailEnd > tailStart &&
			(content[tailEnd-1] == '-' || content[tailEnd-1] == '~') {
			tailEnd--
		}
		tokens, valid := guardTokens(
			content[tailStart:tailEnd],
			tailStart,
		)
		if !valid || len(tokens) != 2 {
			continue
		}
		kind := parseGuardKind(tokens[0].value)
		if kind == 0 ||
			offset < tokens[1].rng.Start ||
			offset > tokens[1].rng.End {
			continue
		}
		return GuardReference{
			Kind:  kind,
			Name:  tokens[1].value,
			Range: tokens[1].rng,
		}, true
	}
	return GuardReference{}, false
}

type guardToken struct {
	value string
	rng   cst.TextRange
}

func activeGuardTag(
	content []byte,
	offset uint32,
) (TwigTagUsage, int, []byte, bool) {
	if uint64(offset) > uint64(len(content)) {
		return TwigTagUsage{}, 0, nil, false
	}
	before := content[:offset]
	open := bytes.LastIndex(before, []byte("{%"))
	if open < 0 {
		return TwigTagUsage{}, 0, nil, false
	}
	if closeOffset := bytes.LastIndex(before, []byte("%}")); closeOffset > open {
		return TwigTagUsage{}, 0, nil, false
	}
	probe := append(bytes.Clone(before), '%', '}')
	for _, usage := range TwigTagUsages(probe) {
		if int(usage.Range.Start) < open ||
			!strings.EqualFold(usage.Name, "guard") ||
			usage.Range.End > offset {
			continue
		}
		tailStart := int(usage.Range.End)
		return usage, tailStart, before[tailStart:], true
	}
	return TwigTagUsage{}, 0, nil, false
}

func guardTokens(content []byte, base int) ([]guardToken, bool) {
	var tokens []guardToken
	for cursor := 0; cursor < len(content); {
		for cursor < len(content) && isTwigTagSpace(content[cursor]) {
			cursor++
		}
		if cursor == len(content) {
			break
		}
		start := cursor
		for cursor < len(content) && isTwigTagNameByte(content[cursor]) {
			cursor++
		}
		if cursor == start {
			return nil, false
		}
		tokens = append(tokens, guardToken{
			value: string(content[start:cursor]),
			rng: cst.TextRange{
				Start: uint32(base + start),
				End:   uint32(base + cursor),
			},
		})
	}
	return tokens, true
}

func parseGuardKind(value string) GuardKind {
	switch strings.ToLower(value) {
	case "function":
		return GuardFunction
	case "filter":
		return GuardFilter
	case "test":
		return GuardTest
	default:
		return 0
	}
}
