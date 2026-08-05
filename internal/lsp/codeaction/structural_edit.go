package codeaction

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

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
