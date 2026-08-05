package twig

import (
	"bytes"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
)

// TwigTag represents a tag supplied by a Twig token parser.
type TwigTag struct {
	Name            string
	Class           string
	FilePath        string
	Line            int
	Range           cst.TextRange
	Deprecated      bool
	Deprecation     string
	DeprecatedRange cst.TextRange
}

type TwigTagUsage struct {
	Name  string
	Range cst.TextRange
}

func ParseTwigTokenParsers(
	filePath string,
	root *phpsyntax.Node,
	content []byte,
	lineIndex *phpsyntax.LineIndex,
) []TwigTag {
	if root == nil || !isTwigTokenParserCandidate(content) {
		return nil
	}
	nameResolver := php.NewNameResolver(root)
	var result []TwigTag
	for _, class := range phpquery.Classes(root) {
		classShortName := phpquery.ClassName(class)
		if !isTokenParserClass(class, nameResolver) ||
			strings.HasSuffix(classShortName, "Test") ||
			strings.HasSuffix(
				strings.TrimSuffix(
					filepath.Base(filePath),
					filepath.Ext(filePath),
				),
				"Test",
			) {
			continue
		}
		getTag := methodNamed(class, "getTag")
		if getTag == nil {
			continue
		}
		deprecation := tokenParserDeprecation(class)
		className := nameResolver.Resolve(phpquery.ClassName(class))
		for _, stringNode := range phpquery.Nodes(getTag, phpsyntax.PhpString) {
			if !stringReturnedByMethod(stringNode, getTag) {
				continue
			}
			name := phpquery.StringValue(stringNode)
			if strings.TrimSpace(name) == "" {
				continue
			}
			nameRange := phpStringValueRange(stringNode, name)
			row, _ := lineIndex.Position(nameRange.Start)
			result = append(result, TwigTag{
				Name:            name,
				Class:           className,
				FilePath:        filePath,
				Line:            int(row) + 1,
				Range:           nameRange,
				Deprecated:      deprecation.deprecated,
				Deprecation:     deprecation.message,
				DeprecatedRange: deprecation.sourceRange,
			})
		}
	}
	return result
}

func isTwigTokenParserCandidate(content []byte) bool {
	return bytes.Contains(content, []byte("TokenParser")) &&
		bytes.Contains(content, []byte("getTag"))
}

func TwigTagUsages(content []byte) []TwigTagUsage {
	var result []TwigTagUsage
	verbatimEnd := ""
	for offset := 0; offset+2 <= len(content); {
		relative := bytes.Index(content[offset:], []byte("{%"))
		if verbatimEnd == "" {
			comment := bytes.Index(content[offset:], []byte("{#"))
			if comment >= 0 && (relative < 0 || comment < relative) {
				commentStart := offset + comment + 2
				commentEnd := bytes.Index(
					content[commentStart:],
					[]byte("#}"),
				)
				if commentEnd < 0 {
					break
				}
				offset = commentStart + commentEnd + 2
				continue
			}
		}
		if relative < 0 {
			break
		}
		open := offset + relative
		cursor := open + 2
		if cursor < len(content) && content[cursor] == '-' {
			cursor++
		}
		for cursor < len(content) && isTwigTagSpace(content[cursor]) {
			cursor++
		}
		start := cursor
		for cursor < len(content) && isTwigTagNameByte(content[cursor]) {
			cursor++
		}
		if cursor > start {
			name := string(content[start:cursor])
			if verbatimEnd == "" {
				result = append(result, TwigTagUsage{
					Name: name,
					Range: cst.TextRange{
						Start: uint32(start),
						End:   uint32(cursor),
					},
				})
				switch strings.ToLower(name) {
				case "raw":
					verbatimEnd = "endraw"
				case "verbatim":
					verbatimEnd = "endverbatim"
				}
			} else if strings.EqualFold(name, verbatimEnd) {
				verbatimEnd = ""
			}
		}
		if closeIndex := bytes.Index(content[cursor:], []byte("%}")); closeIndex >= 0 {
			offset = cursor + closeIndex + 2
		} else {
			break
		}
	}
	return result
}

func isTokenParserClass(
	class *phpsyntax.Node,
	nameResolver *php.NameResolver,
) bool {
	names := append(
		append([]string(nil), phpquery.ClassExtends(class)...),
		phpquery.ClassImplements(class)...,
	)
	for _, name := range names {
		resolved := name
		if nameResolver != nil {
			resolved = nameResolver.Resolve(name)
		}
		part := lastNamePart(resolved)
		switch part {
		case "TokenParserInterface", "Twig_TokenParserInterface",
			"AbstractTokenParser", "Twig_TokenParser":
			return true
		}
		// Custom intermediate token-parser base classes are common. The
		// workspace PHP graph is published after a cold indexing batch, so use
		// the conventional base-class name here to retain transitive parsers.
		if strings.Contains(strings.ToLower(part), "tokenparser") {
			return true
		}
	}
	return false
}

func tokenParserDeprecation(
	class *phpsyntax.Node,
) twigCallableDeprecation {
	if documentation, rng := leadingPHPDoc(class); documentation != "" {
		if message, ok := phpDocDeprecation(documentation); ok {
			return twigCallableDeprecation{
				deprecated:  true,
				message:     message,
				sourceRange: rng,
			}
		}
	}
	for _, attribute := range phpquery.Attributes(class) {
		if !strings.EqualFold(
			lastNamePart(phpquery.AttributeName(attribute)),
			"Deprecated",
		) {
			continue
		}
		return twigCallableDeprecation{
			deprecated:  true,
			sourceRange: attribute.RangeTrimmedTrivia(),
		}
	}
	parse := methodNamed(class, "parse")
	if parse == nil {
		return twigCallableDeprecation{}
	}
	for _, call := range phpquery.Calls(parse) {
		if call.Kind() != phpsyntax.PhpFunctionCall ||
			!strings.EqualFold(
				strings.TrimPrefix(phpquery.CallMethodName(call), "\\"),
				"trigger_deprecation",
			) {
			continue
		}
		return twigCallableDeprecation{
			deprecated:  true,
			message:     lastStringArgument(call),
			sourceRange: call.RangeTrimmedTrivia(),
		}
	}
	return twigCallableDeprecation{}
}

func stringReturnedByMethod(
	stringNode,
	method *phpsyntax.Node,
) bool {
	returned := false
	for current := stringNode.Parent(); current != nil && current != method; current = current.Parent() {
		switch current.Kind() {
		case phpsyntax.PhpReturnStatement:
			returned = true
		case phpsyntax.PhpMethodDeclaration, phpsyntax.PhpFunctionDeclaration,
			phpsyntax.PhpClosure, phpsyntax.PhpArrowFunction:
			return false
		}
	}
	return returned
}

func phpStringValueRange(
	node *phpsyntax.Node,
	value string,
) cst.TextRange {
	if node == nil || value == "" {
		return cst.TextRange{}
	}
	rng := node.RangeTrimmedTrivia()
	text := node.Text()
	nodeRange := node.Range()
	start := int(rng.Start - nodeRange.Start)
	end := int(rng.End - nodeRange.Start)
	if start >= 0 && end >= start && end <= len(text) {
		if index := strings.Index(text[start:end], value); index >= 0 {
			return cst.TextRange{
				Start: rng.Start + uint32(index),
				End:   rng.Start + uint32(index+len(value)),
			}
		}
	}
	return rng
}

func leadingPHPDoc(
	node *phpsyntax.Node,
) (string, cst.TextRange) {
	if node == nil {
		return "", cst.TextRange{}
	}
	rng := node.Range()
	trimmed := node.RangeTrimmedTrivia()
	if trimmed.Start <= rng.Start {
		return "", cst.TextRange{}
	}
	prefixLength := int(trimmed.Start - rng.Start)
	text := node.Text()
	if prefixLength > len(text) {
		prefixLength = len(text)
	}
	prefix := text[:prefixLength]
	start := strings.LastIndex(prefix, "/**")
	if start < 0 {
		return "", cst.TextRange{}
	}
	relativeEnd := strings.Index(prefix[start:], "*/")
	if relativeEnd < 0 {
		return "", cst.TextRange{}
	}
	end := start + relativeEnd + 2
	if strings.TrimSpace(prefix[end:]) != "" {
		return "", cst.TextRange{}
	}
	return prefix[start:end], cst.TextRange{
		Start: rng.Start + uint32(start),
		End:   rng.Start + uint32(end),
	}
}

func phpDocDeprecation(documentation string) (string, bool) {
	index := strings.Index(strings.ToLower(documentation), "@deprecated")
	if index < 0 {
		return "", false
	}
	message := documentation[index+len("@deprecated"):]
	if end := strings.IndexAny(message, "\r\n"); end >= 0 {
		message = message[:end]
	}
	message = strings.TrimSpace(strings.Trim(message, "*/"))
	return message, true
}

func isTwigTagSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func isTwigTagNameByte(value byte) bool {
	return value == '_' || value == '-' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value >= 0x80
}
