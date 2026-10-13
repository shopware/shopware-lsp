package query

import (
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"gopkg.in/yaml.v3"
)

// Decode converts the existing native CST to semantic YAML nodes and decodes
// them into target. It never invokes another lexer/parser. Incomplete editor
// input remains usable; decoding errors leave successfully decoded fields set.
func Decode(root *syntax.Node, target any) error {
	decoder := semanticDecoder{anchors: make(map[string]*yaml.Node)}
	if root != nil {
		decoder.source = root.Text()
	}
	value := decoder.value(RootValue(root), 0)
	return value.Decode(target)
}

type semanticDecoder struct {
	source  string
	anchors map[string]*yaml.Node
}

func (d *semanticDecoder) value(node *syntax.Node, depth int) *yaml.Node {
	if node == nil || depth > 128 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}
	}
	if IsMapping(node) {
		result := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, pair := range Pairs(node) {
			key := d.value(PairKey(pair), depth+1)
			value := d.decoratedValue(pair, PairValue(pair), depth+1)
			result.Content = append(result.Content, key, value)
		}
		return result
	}
	if IsSequence(node) {
		result := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, item := range Items(node) {
			result.Content = append(result.Content, d.decoratedValue(item, ItemValue(item), depth+1))
		}
		return result
	}
	token := scalarToken(node)
	if token == nil || IsNull(node) {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}
	}
	text := token.Text()
	switch token.Kind() {
	case syntax.TkSingleQuotedScalar:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: strings.ReplaceAll(ScalarValue(node), "''", "'"), Style: yaml.SingleQuotedStyle}
	case syntax.TkDoubleQuotedScalar:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: decodeDoubleQuoted(ScalarValue(node)), Style: yaml.DoubleQuotedStyle}
	case syntax.TkBlockScalar:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: decodeBlockScalar(text, d.blockIndent(token.Range().Start)), Style: yaml.LiteralStyle}
	default:
		return d.plainScalar(strings.TrimSpace(text))
	}

}

func (d *semanticDecoder) plainScalar(text string) *yaml.Node {
	result := &yaml.Node{Kind: yaml.ScalarNode}
	for strings.HasPrefix(text, "&") || strings.HasPrefix(text, "!") {
		decoration, remaining, found := strings.Cut(text, " ")
		if !found {
			break
		}
		if decoration[0] == '&' {
			result.Anchor = decoration[1:]
		} else {
			result.Tag = decoration
		}
		text = strings.TrimSpace(remaining)
	}
	if strings.HasPrefix(text, "*") {
		name := strings.TrimPrefix(text, "*")
		if anchor := d.anchors[name]; anchor != nil {
			return &yaml.Node{Kind: yaml.AliasNode, Value: name, Alias: anchor}
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}
	}
	result.Value = text
	if result.Anchor != "" {
		d.anchors[result.Anchor] = result
	}
	return result
}

func (d *semanticDecoder) decoratedValue(parent, value *syntax.Node, depth int) *yaml.Node {
	result := d.value(value, depth)
	cursor := parent.ChildNodeCursor()
	for cursor.Next() {
		child := cursor.Node()
		if child == value {
			break
		}
		// The mapping key is not a value decorator.
		if child == PairKey(parent) {
			continue
		}
		if child.Kind() != syntax.YamlScalar {
			continue
		}
		for _, decorator := range strings.Fields(ScalarValue(child)) {
			if strings.HasPrefix(decorator, "&") {
				result.Anchor = strings.TrimPrefix(decorator, "&")
				d.anchors[result.Anchor] = result
			}
			if strings.HasPrefix(decorator, "!!") {
				result.Tag = decorator
			}
		}
	}
	return result
}

// decodeDoubleQuoted handles YAML's escape alphabet without reparsing source.
func decodeDoubleQuoted(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] != '\\' || i+1 == len(text) {
			out.WriteByte(text[i])
			continue
		}
		i++
		switch text[i] {
		case '0':
			out.WriteByte(0)
		case 'a':
			out.WriteByte('\a')
		case 'b':
			out.WriteByte('\b')
		case 't', '\t':
			out.WriteByte('\t')
		case 'n':
			out.WriteByte('\n')
		case 'v':
			out.WriteByte('\v')
		case 'f':
			out.WriteByte('\f')
		case 'r':
			out.WriteByte('\r')
		case 'e':
			out.WriteByte(27)
		case ' ', '"', '/', '\\':
			out.WriteByte(text[i])
		case 'N':
			out.WriteRune('\u0085')
		case '_':
			out.WriteRune('\u00a0')
		case 'L':
			out.WriteRune('\u2028')
		case 'P':
			out.WriteRune('\u2029')
		case 'x', 'u', 'U':
			width := 2
			switch text[i] {
			case 'u':
				width = 4
			case 'U':
				width = 8
			}
			if i+width < len(text) {
				decoded, err := strconv.ParseUint(text[i+1:i+1+width], 16, 32)
				if err == nil {
					out.WriteRune(rune(decoded))
					i += width
					continue
				}
			}
			out.WriteByte(text[i])
		default:
			out.WriteByte(text[i])
		}
	}
	return out.String()
}

func (d *semanticDecoder) blockIndent(offset uint32) int {
	if int(offset) > len(d.source) {
		return 0
	}
	prefix := d.source[:offset]
	start := strings.LastIndexByte(prefix, '\n') + 1
	width := 0
	for start+width < len(prefix) && prefix[start+width] == ' ' {
		width++
	}
	return width
}

func decodeBlockScalar(text string, baseIndent int) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	header, body, found := strings.Cut(text, "\n")
	if !found {
		return ""
	}
	// The native lexer already bounds the body. Infer its content indentation
	// from nonempty lines, preserving additional indentation within the string.
	lines := strings.Split(body, "\n")
	indent := -1
	header, _, _ = strings.Cut(header, "#")
	for _, char := range header {
		if char >= '1' && char <= '9' {
			indent = baseIndent + int(char-'0')
			break
		}
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		width := len(line) - len(strings.TrimLeft(line, " "))
		if indent < 0 {
			indent = width
		}
		break
	}
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ""
		} else if indent > 0 && len(line) >= indent {
			lines[i] = line[indent:]
		}
	}
	var out strings.Builder
	for i, line := range lines {
		out.WriteString(line)
		if i+1 == len(lines) {
			continue
		}
		next := lines[i+1]
		if strings.HasPrefix(header, ">") && line != "" && next == "" && !strings.HasPrefix(line, " ") {
			// A run of N blank lines between ordinary paragraphs contributes N
			// line breaks; the break preceding that run is folded away.
			following := i + 2
			for following < len(lines) && lines[following] == "" {
				following++
			}
			if following < len(lines) && !strings.HasPrefix(lines[following], " ") {
				continue
			}
		}
		if strings.HasPrefix(header, ">") && line != "" && next != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(next, " ") {
			out.WriteByte(' ')
		} else {
			out.WriteByte('\n')
		}
	}
	value := out.String()
	if strings.Contains(header, "-") {
		return strings.TrimRight(value, "\n")
	}
	if strings.Contains(header, "+") {
		return value
	}
	if value == "" {
		return ""
	}
	return strings.TrimRight(value, "\n") + "\n"
}
