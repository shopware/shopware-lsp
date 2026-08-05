package syntax

import "github.com/shopware/shopware-lsp/internal/parser/cst"

type Kind = cst.Kind

const KindNone = cst.KindNone

type TextRange = cst.TextRange
type Element = cst.Element
type Node = cst.Node
type Token = cst.Token
type Tree = cst.Tree
type LineIndex = cst.LineIndex

var NewLineIndex = cst.NewLineIndex
var DebugTree = cst.DebugTree
