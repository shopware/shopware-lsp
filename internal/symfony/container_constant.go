package symfony

import (
	"bytes"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

type ContainerConstantReference struct {
	Name  string
	Range cst.TextRange
}

func YAMLContainerConstantReferences(
	content []byte,
) []ContainerConstantReference {
	var result []ContainerConstantReference
	for lineStart := 0; lineStart < len(content); {
		lineEnd := bytes.IndexByte(content[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(content)
		} else {
			lineEnd += lineStart
		}
		line := content[lineStart:lineEnd]
		result = append(result, yamlConstantReferencesInLine(
			line,
			lineStart,
		)...)
		if lineEnd == len(content) {
			break
		}
		lineStart = lineEnd + 1
	}
	return result
}

func YAMLContainerConstantCompletionAt(
	content []byte,
	offset uint32,
) (ContainerConstantReference, bool) {
	if int(offset) > len(content) {
		return ContainerConstantReference{}, false
	}
	probe := append(bytes.Clone(content[:offset]), 'X')
	references := YAMLContainerConstantReferences(probe)
	for index := len(references) - 1; index >= 0; index-- {
		reference := references[index]
		if reference.Range.End != uint32(len(probe)) ||
			!strings.HasSuffix(reference.Name, "X") {
			continue
		}
		reference.Name = strings.TrimSuffix(reference.Name, "X")
		reference.Range.End--
		return reference, true
	}
	return ContainerConstantReference{}, false
}

func XMLContainerConstantReferences(
	root *xmlsyntax.Node,
) []ContainerConstantReference {
	var result []ContainerConstantReference
	for _, argument := range xmlquery.Elements(root, "argument") {
		if !strings.EqualFold(
			xmlquery.AttributeValue(
				xmlquery.Attribute(argument, "type"),
			),
			"constant",
		) {
			continue
		}
		for _, text := range xmlquery.Nodes(argument, xmlsyntax.XmlText) {
			name, rng := trimmedConstantText(text)
			if name == "" {
				continue
			}
			result = append(result, ContainerConstantReference{
				Name:  name,
				Range: rng,
			})
			break
		}
	}
	return result
}

func ResolveContainerConstant(
	index *php.PHPIndex,
	name string,
) []semantic.Symbol {
	if index == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if scope := strings.Index(name, "::"); scope >= 0 {
		if scope == 0 || scope+2 >= len(name) {
			return nil
		}
		return index.FindConstants(name[:scope], name[scope+2:])
	}
	return index.FindGlobalConstants(name)
}

func yamlConstantReferencesInLine(
	line []byte,
	lineStart int,
) []ContainerConstantReference {
	const marker = "!php/const"
	var result []ContainerConstantReference
	var quote byte
	escaped := false
	for cursor := 0; cursor < len(line); cursor++ {
		value := line[cursor]
		if quote != 0 {
			if quote == '"' && value == '\\' && !escaped {
				escaped = true
				continue
			}
			if value == quote && !escaped {
				if quote == '\'' && cursor+1 < len(line) &&
					line[cursor+1] == '\'' {
					cursor++
					continue
				}
				quote = 0
			}
			escaped = false
			continue
		}
		switch value {
		case '\'', '"':
			quote = value
			continue
		case '#':
			return result
		}
		if !bytes.HasPrefix(line[cursor:], []byte(marker)) {
			continue
		}
		after := cursor + len(marker)
		if after < len(line) &&
			line[after] != ':' &&
			!isContainerConstantSpace(line[after]) {
			continue
		}
		if after < len(line) && line[after] == ':' {
			after++
		}
		for after < len(line) && isContainerConstantSpace(line[after]) {
			after++
		}
		start := after
		end := after
		if after < len(line) &&
			(line[after] == '\'' || line[after] == '"') {
			delimiter := line[after]
			start++
			end = start
			for end < len(line) && line[end] != delimiter {
				end++
			}
		} else {
			for end < len(line) &&
				isContainerConstantNameByte(line[end]) {
				end++
			}
		}
		if end > start {
			result = append(result, ContainerConstantReference{
				Name: string(line[start:end]),
				Range: cst.TextRange{
					Start: uint32(lineStart + start),
					End:   uint32(lineStart + end),
				},
			})
		}
		cursor = end
	}
	return result
}

func trimmedConstantText(
	node *xmlsyntax.Node,
) (string, cst.TextRange) {
	if node == nil {
		return "", cst.TextRange{}
	}
	text := node.Text()
	left := len(text) - len(strings.TrimLeft(text, " \t\r\n"))
	right := len(strings.TrimRight(text, " \t\r\n"))
	if right <= left {
		return "", cst.TextRange{}
	}
	rng := node.Range()
	return text[left:right], cst.TextRange{
		Start: rng.Start + uint32(left),
		End:   rng.Start + uint32(right),
	}
}

func isContainerConstantSpace(value byte) bool {
	return value == ' ' || value == '\t'
}

func isContainerConstantNameByte(value byte) bool {
	return value == '\\' || value == ':' || value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value >= 0x80
}
