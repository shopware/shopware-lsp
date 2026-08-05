// Package syntax defines the lossless concrete syntax tree (CST) shared by the
// whole twig-go stack: the [Kind] inventory, the [Node]/[Token] element types,
// the [Builder] used by the parser sink, tree-walking iterators, the
// [DebugTree] printer, [TextRange], and [LineIndex].
//
// The tree is a faithful port of the data model ludtwig-parser builds on top of
// rowan, with one deliberate deviation: there is no green/red split. twig-go
// builds a single immutable tree with absolute byte offsets directly in the
// sink. Rowan's structural sharing only pays off for incremental reparsing;
// Twig templates are small (a full reparse is sub-millisecond), and absolute
// offsets plus zero-copy text make consumer code (linters, formatters, LSP
// servers) both simpler and faster.
//
// # Invariants
//
// Three invariants hold for any tree produced by the parser:
//
//   - Lossless: root.Text() == source. Every byte of the input — including
//     whitespace, line breaks and unrecognized garbage — lives in exactly one
//     token, so the source can be reconstructed by concatenating the tree's
//     tokens left to right.
//   - Zero-copy: every Text() value is a sub-slice of the original source
//     string; the tree allocates no new string data for token text.
//   - Total: malformed input yields ERROR nodes and TK_UNKNOWN tokens rather
//     than a panic or a lost byte.
//
// # Kinds
//
// [Kind] enumerates every token and node kind. Token kinds sort before node
// kinds (see [Kind.IsToken]), the two trivia kinds are [TkWhitespace] and
// [TkLineBreak] (see [Kind.IsTrivia]), and [Kind.String] renders the
// SCREAMING_SNAKE names used by ludtwig's debug output (for example TK_WORD,
// TWIG_BLOCK, ROOT). [Kind.TokenText] renders the human display text used in
// diagnostics.
//
// # Navigation
//
// [Node] and [Token] implement the [Element] interface. [Node] offers the usual
// tree navigation as Go 1.23 range-over-func iterators — [Node.ChildNodes],
// [Node.Descendants], [Node.Ancestors], and [Node.Walk] (an enter/leave
// preorder traversal) — alongside kind-directed lookups ([Node.ChildOfKind],
// [Node.ChildTokenOfKind]) and offset lookup ([Node.TokenAtOffset]) for LSP
// hover and completion.
//
// # Positions
//
// [LineIndex] converts between byte offsets and line/column positions, with a
// UTF-16 column variant ([LineIndex.PositionUTF16]) for the Language Server
// Protocol.
package syntax
