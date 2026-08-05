package inspections

import (
	"fmt"
	"html"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
)

type missingServiceArgument struct {
	name    string
	service string
}

func missingServiceArguments(value any) []missingServiceArgument {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]missingServiceArgument, 0, len(values))
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name, _ := item["name"].(string)
		service, _ := item["suggestedService"].(string)
		if service == "" {
			service = "?"
		}
		result = append(result, missingServiceArgument{name: name, service: service})
	}
	return result
}

func serviceArgumentRewrite(
	document *lsp.TextDocument,
	node *cst.Node,
	format string,
	arguments []missingServiceArgument,
) (cst.TextRange, string, bool) {
	switch format {
	case "xml":
		return xmlServiceArgumentRewrite(document.Text, node, arguments)
	case "yaml":
		return yamlServiceArgumentRewrite(document.Text, node, arguments)
	default:
		return cst.TextRange{}, "", false
	}
}

func xmlServiceArgumentRewrite(
	content []byte,
	node *cst.Node,
	arguments []missingServiceArgument,
) (cst.TextRange, string, bool) {
	service := xmlquery.ElementAt(node)
	if service == nil || xmlquery.ElementName(service) != "service" {
		return cst.TextRange{}, "", false
	}
	text := service.Text()
	serviceIndent := indentationAt(content, service.Range().Start)
	childIndent := serviceIndent + "    "
	lines := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		lines = append(lines, fmt.Sprintf(
			`%s<argument type="service" id="%s"/>`,
			childIndent,
			html.EscapeString(argument.service),
		))
	}
	if index := strings.LastIndex(text, "/>"); index >= 0 &&
		strings.TrimSpace(text[index:]) == "/>" {
		start := service.Range().Start + uint32(index)
		return cst.TextRange{Start: start, End: start + 2},
			">\n" + strings.Join(lines, "\n") + "\n" + serviceIndent + "</service>",
			true
	}
	index := strings.LastIndex(text, "</service")
	if index < 0 {
		return cst.TextRange{}, "", false
	}
	closingOffset := service.Range().Start + uint32(index)
	lineStart := lineStartOffset(content, closingOffset)
	if strings.TrimSpace(string(content[lineStart:closingOffset])) == "" {
		return cst.TextRange{Start: lineStart, End: lineStart},
			strings.Join(lines, "\n") + "\n",
			true
	}
	return cst.TextRange{Start: closingOffset, End: closingOffset},
		"\n" + strings.Join(lines, "\n") + "\n" + serviceIndent,
		true
}

func yamlServiceArgumentRewrite(
	content []byte,
	node *cst.Node,
	arguments []missingServiceArgument,
) (cst.TextRange, string, bool) {
	servicePair := yamlquery.AncestorPair(node)
	if servicePair == nil {
		return cst.TextRange{}, "", false
	}
	config := yamlquery.PairValue(servicePair)
	if !yamlquery.IsMapping(config) {
		return cst.TextRange{}, "", false
	}
	argumentsNode := yamlquery.Property(config, "arguments")
	serviceIndent := indentationAt(content, yamlquery.PairKey(servicePair).Range().Start)
	propertyIndent := serviceIndent + "  "
	itemIndent := propertyIndent + "  "

	if argumentsNode == nil {
		if config.Kind() == yamlsyntax.YamlFlowMapping {
			offset, ok := yamlFlowClosingOffset(config, '}')
			if !ok {
				return cst.TextRange{}, "", false
			}
			values := serviceArgumentSequenceValues(arguments)
			prefix := ""
			if len(yamlquery.Pairs(config)) != 0 {
				prefix = ", "
			}
			return cst.TextRange{Start: offset, End: offset},
				prefix + "arguments: [" + strings.Join(values, ", ") + "]",
				true
		}
		lines := []string{propertyIndent + "arguments:"}
		for _, argument := range arguments {
			lines = append(lines, fmt.Sprintf(
				"%s- '%s'",
				itemIndent,
				yamlSingleQuoted(yamlServiceReference(argument.service)),
			))
		}
		offset := config.Range().End
		return cst.TextRange{Start: offset, End: offset}, "\n" + strings.Join(lines, "\n"), true
	}

	switch argumentsNode.Kind() {
	case yamlsyntax.YamlFlowSequence:
		text := argumentsNode.Text()
		index := strings.LastIndexByte(text, ']')
		if index < 0 {
			return cst.TextRange{}, "", false
		}
		prefix := ""
		if len(yamlquery.Items(argumentsNode)) != 0 {
			prefix = ", "
		}
		offset := argumentsNode.Range().Start + uint32(index)
		return cst.TextRange{Start: offset, End: offset},
			prefix + strings.Join(serviceArgumentSequenceValues(arguments), ", "),
			true
	case yamlsyntax.YamlSequence:
		var lines []string
		for _, argument := range arguments {
			lines = append(lines, fmt.Sprintf(
				"%s- '%s'",
				itemIndent,
				yamlSingleQuoted(yamlServiceReference(argument.service)),
			))
		}
		offset := argumentsNode.Range().End
		return cst.TextRange{Start: offset, End: offset}, "\n" + strings.Join(lines, "\n"), true
	case yamlsyntax.YamlFlowMapping:
		offset, ok := yamlFlowClosingOffset(argumentsNode, '}')
		if !ok {
			return cst.TextRange{}, "", false
		}
		values := serviceArgumentMappingValues(arguments)
		prefix := ""
		if len(yamlquery.Pairs(argumentsNode)) != 0 {
			prefix = ", "
		}
		return cst.TextRange{Start: offset, End: offset}, prefix + strings.Join(values, ", "), true
	case yamlsyntax.YamlMapping:
		values := serviceArgumentMappingValues(arguments)
		for index := range values {
			values[index] = itemIndent + values[index]
		}
		offset := argumentsNode.Range().End
		return cst.TextRange{Start: offset, End: offset}, "\n" + strings.Join(values, "\n"), true
	default:
		return cst.TextRange{}, "", false
	}
}

func serviceArgumentSequenceValues(arguments []missingServiceArgument) []string {
	values := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		values = append(values, fmt.Sprintf(
			"'%s'",
			yamlSingleQuoted(yamlServiceReference(argument.service)),
		))
	}
	return values
}

func serviceArgumentMappingValues(arguments []missingServiceArgument) []string {
	values := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		values = append(values, fmt.Sprintf(
			"%s: '@%s'",
			argument.name,
			yamlSingleQuoted(strings.TrimPrefix(argument.service, "@")),
		))
	}
	return values
}

func indentationAt(content []byte, offset uint32) string {
	start := lineStartOffset(content, offset)
	end := start
	for int(end) < len(content) {
		switch content[end] {
		case ' ', '\t':
			end++
		default:
			return string(content[start:end])
		}
	}
	return string(content[start:end])
}

func lineStartOffset(content []byte, offset uint32) uint32 {
	if int(offset) > len(content) {
		offset = uint32(len(content))
	}
	for offset > 0 && content[offset-1] != '\n' {
		offset--
	}
	return offset
}

func yamlSingleQuoted(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func yamlServiceReference(value string) string {
	if strings.HasPrefix(value, "@") {
		return value
	}
	return "@" + value
}

func yamlFlowClosingOffset(node *cst.Node, delimiter byte) (uint32, bool) {
	if node == nil {
		return 0, false
	}
	text := node.Text()
	index := strings.LastIndexByte(text, delimiter)
	if index < 0 {
		return 0, false
	}
	for index > 0 && (text[index-1] == ' ' || text[index-1] == '\t') {
		index--
	}
	return node.Range().Start + uint32(index), true
}
