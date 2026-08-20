package vue

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/bytescan"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	scssparser "github.com/shopware/shopware-lsp/internal/parser/scss"
	twigparser "github.com/shopware/shopware-lsp/internal/parser/twig"
	vuesyntax "github.com/shopware/shopware-lsp/internal/parser/vue/syntax"
)

type SectionKind string

const (
	SectionTemplate SectionKind = "template"
	SectionScript   SectionKind = "script"
	SectionStyle    SectionKind = "style"
	SectionCustom   SectionKind = "custom"
)

// Section is one top-level Vue SFC block. All ranges are absolute byte ranges
// in the original file, allowing embedded language trees to retain exact LSP
// positions without copied-source offset maps.
type Section struct {
	Kind       SectionKind
	Name       string
	OpenRange  cst.TextRange
	BodyRange  cst.TextRange
	CloseRange cst.TextRange
	Setup      bool
	Language   string
}

type Result struct {
	Tree     *cst.Tree
	Errors   []parsekit.Error
	Sections []Section
}

func Parse(source string) Result {
	sections := Sections(source)
	builder := cst.NewBuilder(source)
	builder.StartNode(vuesyntax.VueDocument)
	position := uint32(0)
	var errors []parsekit.Error
	for _, section := range sections {
		if position < section.OpenRange.Start {
			builder.Token(vuesyntax.TkText, cst.TextRange{
				Start: position, End: section.OpenRange.Start,
			})
		}
		builder.StartNode(sectionNodeKind(section.Kind))
		builder.Token(vuesyntax.TkSectionOpen, section.OpenRange)
		body := source[section.BodyRange.Start:section.BodyRange.End]
		switch section.Kind {
		case SectionTemplate:
			parsed := twigparser.Parse(body)
			replayTree(builder, parsed.Tree, section.BodyRange.Start)
			errors = appendShiftedErrors(errors, parsed.Errors, section.BodyRange.Start)
		case SectionScript:
			parsed := javascriptparser.Parse(body)
			replayTree(builder, parsed.Tree, section.BodyRange.Start)
			errors = appendShiftedErrors(errors, parsed.Errors, section.BodyRange.Start)
		case SectionStyle:
			parsed := scssparser.Parse(body)
			replayTree(builder, parsed.Tree, section.BodyRange.Start)
			errors = appendShiftedErrors(errors, parsed.Errors, section.BodyRange.Start)
		default:
			if section.BodyRange.Len() > 0 {
				builder.Token(vuesyntax.TkText, section.BodyRange)
			}
		}
		if section.CloseRange.Len() > 0 {
			builder.Token(vuesyntax.TkSectionClose, section.CloseRange)
			position = section.CloseRange.End
		} else {
			position = section.BodyRange.End
		}
		builder.FinishNode()
	}
	if position < uint32(len(source)) {
		builder.Token(vuesyntax.TkText, cst.TextRange{
			Start: position, End: uint32(len(source)),
		})
	}
	builder.FinishNode()
	return Result{Tree: builder.Finish(), Errors: errors, Sections: sections}
}

func Sections(source string) []Section {
	var result []Section
	for cursor := 0; cursor < len(source); {
		open := strings.IndexByte(source[cursor:], '<')
		if open < 0 {
			break
		}
		open += cursor
		name, openEnd, closing, selfClosing, ok := scanTag(source, open)
		if !ok || closing || name == "" {
			cursor = open + 1
			continue
		}
		kind := sectionKind(name)
		if kind == SectionCustom && isOrdinaryDocumentTag(name) {
			cursor = openEnd
			continue
		}
		section := Section{
			Kind: kind, Name: name,
			OpenRange: cst.TextRange{Start: uint32(open), End: uint32(openEnd)},
			BodyRange: cst.TextRange{Start: uint32(openEnd), End: uint32(openEnd)},
		}
		openText := source[open:openEnd]
		section.Setup = hasBooleanAttribute(openText, "setup")
		section.Language = attributeValue(openText, "lang")
		if selfClosing {
			result = append(result, section)
			cursor = openEnd
			continue
		}
		closeStart, closeEnd := matchingSectionClose(source, openEnd, name)
		if closeStart < 0 {
			section.BodyRange.End = uint32(len(source))
			result = append(result, section)
			break
		}
		section.BodyRange.End = uint32(closeStart)
		section.CloseRange = cst.TextRange{
			Start: uint32(closeStart), End: uint32(closeEnd),
		}
		result = append(result, section)
		cursor = closeEnd
	}
	return result
}

func sectionKind(name string) SectionKind {
	switch strings.ToLower(name) {
	case "template":
		return SectionTemplate
	case "script":
		return SectionScript
	case "style":
		return SectionStyle
	default:
		return SectionCustom
	}
}

func sectionNodeKind(kind SectionKind) cst.Kind {
	switch kind {
	case SectionTemplate:
		return vuesyntax.VueTemplateSection
	case SectionScript:
		return vuesyntax.VueScriptSection
	case SectionStyle:
		return vuesyntax.VueStyleSection
	default:
		return vuesyntax.VueCustomSection
	}
}

func replayTree(builder *cst.Builder, tree *cst.Tree, base uint32) {
	if tree == nil || tree.Root == nil {
		return
	}
	replayElement(builder, tree.Root, base)
}

func replayElement(builder *cst.Builder, element cst.Element, base uint32) {
	switch typed := element.(type) {
	case *cst.Node:
		builder.StartNodeHint(typed.Kind(), typed.ChildCount())
		for child := range typed.ChildElements() {
			replayElement(builder, child, base)
		}
		builder.FinishNode()
	case *cst.Token:
		rangeValue := typed.Range()
		builder.Token(typed.Kind(), cst.TextRange{
			Start: base + rangeValue.Start,
			End:   base + rangeValue.End,
		})
	}
}

func appendShiftedErrors(
	target []parsekit.Error,
	errors []parsekit.Error,
	base uint32,
) []parsekit.Error {
	for _, parseErr := range errors {
		parseErr.Range.Start += base
		parseErr.Range.End += base
		target = append(target, parseErr)
	}
	return target
}

func scanTag(source string, start int) (
	name string,
	end int,
	closing bool,
	selfClosing bool,
	ok bool,
) {
	if start < 0 || start >= len(source) || source[start] != '<' {
		return "", start, false, false, false
	}
	if strings.HasPrefix(source[start:], "<!--") {
		close := strings.Index(source[start+4:], "-->")
		if close < 0 {
			return "", len(source), false, false, false
		}
		return "", start + 4 + close + 3, false, false, false
	}
	index := start + 1
	for index < len(source) && isSpace(source[index]) {
		index++
	}
	if index < len(source) && source[index] == '/' {
		closing = true
		index++
	}
	nameStart := index
	for index < len(source) && isTagNameByte(source[index]) {
		index++
	}
	if index == nameStart {
		return "", start + 1, closing, false, false
	}
	name = strings.ToLower(source[nameStart:index])
	quote := byte(0)
	for index < len(source) {
		if quote != 0 {
			index = bytescan.IndexByte(source, index, quote)
			if index >= len(source) {
				break
			}
			quote = 0
			index++
			continue
		}
		index = bytescan.IndexAny3(source, index, '\'', '"', '>')
		if index >= len(source) {
			break
		}
		value := source[index]
		if value == '\'' || value == '"' {
			quote = value
			index++
			continue
		}
		if value == '>' {
			trimmed := strings.TrimSpace(source[start+1 : index])
			selfClosing = strings.HasSuffix(trimmed, "/")
			return name, index + 1, closing, selfClosing, true
		}
		index++
	}
	return name, len(source), closing, false, true
}

func matchingSectionClose(source string, start int, name string) (int, int) {
	depth := 1
	for cursor := start; cursor < len(source); {
		next := strings.IndexByte(source[cursor:], '<')
		if next < 0 {
			return -1, -1
		}
		next += cursor
		if strings.HasPrefix(source[next:], "<!--") {
			close := strings.Index(source[next+4:], "-->")
			if close < 0 {
				return -1, -1
			}
			cursor = next + 4 + close + 3
			continue
		}
		tagName, tagEnd, closing, selfClosing, ok := scanTag(source, next)
		if !ok {
			cursor = next + 1
			continue
		}
		if tagName == name {
			if closing {
				depth--
				if depth == 0 {
					return next, tagEnd
				}
			} else if !selfClosing && name == "template" {
				depth++
			}
		}
		cursor = max(tagEnd, next+1)
	}
	return -1, -1
}

func hasBooleanAttribute(tag, name string) bool {
	return attributeValue(tag, name) != "" || hasBareAttribute(tag, name)
}

func hasBareAttribute(tag, name string) bool {
	for _, field := range strings.Fields(strings.Trim(tag, "<>/ \t\r\n")) {
		if strings.EqualFold(strings.TrimSpace(field), name) {
			return true
		}
	}
	return false
}

func attributeValue(tag, name string) string {
	lower := strings.ToLower(tag)
	for cursor := 0; cursor < len(lower); {
		index := strings.Index(lower[cursor:], name)
		if index < 0 {
			return ""
		}
		index += cursor
		beforeOK := index == 0 || !isTagNameByte(lower[index-1])
		after := index + len(name)
		afterOK := after >= len(lower) || !isTagNameByte(lower[after])
		if !beforeOK || !afterOK {
			cursor = after
			continue
		}
		for after < len(tag) && isSpace(tag[after]) {
			after++
		}
		if after >= len(tag) || tag[after] != '=' {
			cursor = after
			continue
		}
		after++
		for after < len(tag) && isSpace(tag[after]) {
			after++
		}
		if after >= len(tag) {
			return ""
		}
		quote := tag[after]
		if quote == '\'' || quote == '"' {
			end := strings.IndexByte(tag[after+1:], quote)
			if end < 0 {
				return ""
			}
			return tag[after+1 : after+1+end]
		}
		end := after
		for end < len(tag) && !isSpace(tag[end]) && tag[end] != '>' {
			end++
		}
		return strings.TrimSuffix(tag[after:end], "/")
	}
	return ""
}

func isOrdinaryDocumentTag(name string) bool {
	switch name {
	case "div", "span", "main", "section", "article", "header", "footer",
		"button", "form", "input", "label", "ul", "ol", "li", "p", "a":
		return true
	default:
		return false
	}
}

func isSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func isTagNameByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' || value == '-' || value == '_' || value == ':'
}
