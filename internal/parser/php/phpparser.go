package php

import (
	"github.com/shopware/shopware-lsp/internal/parser/php/parser"
	"github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

type Result = parser.Result

func Parse(source string) Result {
	return parser.Parse(source)
}

func ParseBytes(source []byte) Result {
	return parser.Parse(string(source))
}

func Root(source string) *syntax.Node {
	return Parse(source).Tree.Root
}
