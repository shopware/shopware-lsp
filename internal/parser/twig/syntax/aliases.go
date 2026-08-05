package syntax

import "github.com/shopware/shopware-lsp/internal/parser/cst"

// This file re-exports the language-agnostic CST machinery from the cst package
// so that twig consumers keep importing syntax only. Every alias preserves type
// identity with the cst definition, so values flow between the two packages
// without conversion.

// Kind is the syntax kind of a node or token. The twig kind constants declared
// in kind.go have this type.
type Kind = cst.Kind

// KindNone is the reserved "no kind" sentinel (e.g. reaching EOF in an error).
const KindNone = cst.KindNone

// TextRange is a half-open byte range into the source.
type TextRange = cst.TextRange

// Element is either a Node or a Token (rowan's NodeOrToken).
type Element = cst.Element

// Node is a composite CST element with children.
type Node = cst.Node

// Token is a leaf CST element holding a slice of the source text.
type Token = cst.Token

// Tree is a parsed template: the source plus its root node.
type Tree = cst.Tree

// Builder constructs an immutable Tree top-down.
type Builder = cst.Builder

// NewBuilder returns a Builder over the given source.
var NewBuilder = cst.NewBuilder

// LineIndex maps byte offsets to line/column positions and back.
type LineIndex = cst.LineIndex

// NewLineIndex builds a LineIndex for src.
var NewLineIndex = cst.NewLineIndex

// WalkEvent distinguishes entering from leaving a node during a Walk.
type WalkEvent = cst.WalkEvent

// WalkEnter is emitted when descending into an element.
const WalkEnter = cst.WalkEnter

// WalkLeave is emitted when ascending out of a node.
const WalkLeave = cst.WalkLeave

// DebugTree renders the tree in rowan's debug format.
var DebugTree = cst.DebugTree
