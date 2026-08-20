# Parser, CST, and Query Architecture

This document describes how Shopware Language Server turns bytes on disk into
syntax trees, how those trees are represented, and how features ask questions
about them. Everything here lives under [`internal/parser`](../internal/parser)
plus the [`internal/language`](../internal/language) registry that selects a
frontend for a file.

The diagnostics side of the pipeline — inspections, problems, quick-fix
anchoring — is documented separately in
[`diagnostics-pipeline.md`](diagnostics-pipeline.md).

## Table of contents

- [Design goals](#design-goals)
- [Pipeline overview](#pipeline-overview)
- [Package map](#package-map)
- [The kind registry](#the-kind-registry)
- [Lexing](#lexing)
- [Event-based parsing with parsekit](#event-based-parsing-with-parsekit)
- [Error recovery](#error-recovery)
- [The sink: events to tree](#the-sink-events-to-tree)
- [The CST](#the-cst)
- [Positions, ranges, and LSP coordinates](#positions-ranges-and-lsp-coordinates)
- [Language frontends](#language-frontends)
- [Embedded and multi-language trees](#embedded-and-multi-language-trees)
- [Typed AST wrappers](#typed-ast-wrappers)
- [Querying nodes](#querying-nodes)
- [Document and index integration](#document-and-index-integration)
- [Memory management and pooling](#memory-management-and-pooling)
- [Adding a language](#adding-a-language)
- [Debugging and testing](#debugging-and-testing)

## Design goals

The parser foundation is a pure-Go, hand-written stack derived from the
`ludtwig` / `rust-analyzer` (`rowan`) design. There is no tree-sitter, no CGO,
and no generated grammar. Four invariants hold for every language frontend:

1. **Parsing is total.** Every input produces a usable tree. There is no error
   return from `Parse`; malformed input is represented by `ERROR` nodes plus a
   flat list of diagnostics.
2. **The tree is lossless.** `result.Tree.Root.Text() == source` for any input.
   Whitespace, comments, and unparseable garbage all remain in the tree, which
   is what makes structural refactoring and formatting-preserving rewrites
   possible.
3. **Ranges are absolute byte offsets** into the original source. No node needs
   an offset map, and every position is directly comparable with any other
   node's position in the same file.
4. **`cst` and `parsekit` are language-neutral.** Adding a language never
   requires editing them.

## Pipeline overview

```
 file path + source
        │
        ▼
 language.Registry.ParsePath          internal/language
   (extension → Parser func)
        │
        ▼
 <lang>.Parse(source)                 internal/parser/<lang>
        │
        ├─► lexer.LexInto  ──────────►  []parsekit.Token   (flat, includes trivia)
        │                               internal/parser/<lang>/lexer
        │
        ├─► parsekit.NewOwned(tokens, Config)
        │      grammar drives Parser:
        │        Start / Bump / Complete / Precede / Expect / Recover
        │      producing an event stream + []parsekit.Error
        │                               internal/parser/parsekit
        │
        └─► Parser.Finish(source)
               sink replays events, re-attaches trivia,
               resolves forward parents
                    │
                    ▼
               cst.Builder → *cst.Tree  internal/parser/cst
        │
        ▼
 consumers
   ├─ lsp.TextDocument      (open editor buffers)
   ├─ indexer.ParsedFile    (workspace indexing)
   └─ <lang>/query, <lang>/ast  (semantic questions)
```

The lexer sees the whole source including trivia. The grammar never sees
trivia — `parsekit.source` skips it on every peek and bump — and the sink puts
it back when building the tree. That split is why the grammar stays readable
while the tree stays lossless.

## Package map

| Package | Responsibility |
| --- | --- |
| `internal/parser/cst` | Language-neutral lossless tree, `Kind` registry, `TextRange`, `LineIndex`, `Builder`, `DebugTree`, slab pools |
| `internal/parser/parsekit` | Language-neutral `Token`, trivia-skipping cursor, event/marker parser, recovery, `Error`, the sink |
| `internal/parser/bytescan` | Allocation-free byte scans for lexer hot paths (scalar, plus SIMD under `GOEXPERIMENT=simd`) |
| `internal/parser/<lang>/syntax` | The language's `Kind` inventory and `cst.RegisterLanguage` call |
| `internal/parser/<lang>/lexer` | `Lex` / `LexInto` producing `[]parsekit.Token` |
| `internal/parser/<lang>/parser` | The grammar; exposes `Parse(source) Result` |
| `internal/parser/<lang>/query` | Semantic queries over that language's trees |
| `internal/parser/twig/ast` | Generated typed node wrappers for Twig (the only language with this layer) |
| `internal/parser/<lang>` (root file) | Thin public entry point re-exporting `Parse` |
| `internal/language` | Extension → frontend registry used by every consumer |

## The kind registry

`cst.Kind` is a single `uint16` shared by every language. Each language
reserves a contiguous, non-overlapping range at `init` time through
`cst.RegisterLanguage`:

```go
// internal/parser/php/syntax/kind.go
const phpBase Kind = 24576

const (
    TkWhitespace Kind = phpBase + iota
    // ... token kinds ...
    PhpProgram        // first node kind
    // ... node kinds ...
    Error
    phpKindCount
)

func init() {
    cst.RegisterLanguage(cst.LanguageSpec{
        Name:        "php",
        Base:        phpBase,
        KindNames:   names,      // SCREAMING_SNAKE debug names, indexed from Base
        TokenTexts:  texts,      // diagnostic display text ("" falls back to the name)
        FirstNode:   PhpProgram, // kinds below this are tokens, at or above are nodes
        TriviaKinds: []Kind{TkWhitespace, TkLineBreak, TkLineComment, TkBlockComment},
    })
}
```

Registration materializes four flat lookup tables (`kindNames`, `tokenTexts`,
`isTrivia`, `isTokenTab`), so `Kind.String()`, `Kind.TokenText()`,
`Kind.IsTrivia()` and `Kind.IsToken()` are O(1) array reads with no per-call
language dispatch. `RegisterLanguage` panics on an empty name, an overlapping
range, a `FirstNode` outside the range, or a trivia kind outside the range —
these are programming errors caught at process start.

`cst.KindNone` (`0xFFFF`, 65535) is reserved as the "no kind" sentinel — it is
what `Peek` returns at EOF and what a `parsekit.Error` carries in `Found` when
the parser hit end of file. No language range may include it.

### Registered ranges

Bases are spaced 4096 apart, which leaves ample room for each language to grow
without renumbering another. Current occupancy:

| Language | Base | Range end (exclusive) | Kinds used | Tokens | Nodes | Trivia kinds |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| twig (incl. HTML) | 0 | 287 | 287 | 165 | 122 | whitespace, line break |
| json | 4096 | 4119 | 23 | 14 | 9 | whitespace, line break |
| yaml | 8192 | 8222 | 30 | 19 | 11 | whitespace, comment |
| scss | 12288 | 12326 | 38 | 23 | 15 | whitespace, line break, line/block comment |
| xml | 16384 | 16416 | 32 | 16 | 16 | whitespace, line break |
| javascript | 20480 | 20530 | 50 | 24 | 26 | whitespace, line break, line/block comment |
| php | 24576 | 24690 | 114 | 33 | 81 | whitespace, line break, line/block comment |
| xpath | 28672 | 28705 | 33 | 29 | 4 | whitespace, line break |
| vue | 32768 | 32776 | 8 | 3 | 5 | *(none of its own)* |

Note that "trivia" is a per-language declaration, not a universal rule. Twig
and XML keep comments as ordinary tokens because Twig comments carry
whitespace-control markers and XML comments are semantically interesting;
PHP, JavaScript and SCSS declare comments as trivia so the grammar does not
have to thread them through every production. Vue registers no trivia because
its own kinds only cover section framing; the trees it embeds carry their own
languages' trivia flags.

Because kinds are globally unique, a single tree can legitimately mix kinds
from several languages — which is exactly what the Vue frontend does.

### Language-local aliases

Each `<lang>/syntax` package re-exports the neutral `cst` types so a grammar or
query file only imports its own syntax package:

```go
// internal/parser/php/syntax/aliases.go
type Kind = cst.Kind
type Node = cst.Node
type Token = cst.Token
type Tree = cst.Tree
type TextRange = cst.TextRange
```

`phpsyntax.Node` and `cst.Node` are therefore the same type — the alias is
readability, not encapsulation.

## Lexing

A lexer is a plain function over the source that emits a flat token slice. Every
byte of the source is covered exactly once, in order, with no gaps:

```go
// internal/parser/php/lexer/lexer.go
func Lex(source string) []Token { return LexInto(source, nil) }

func LexInto(source string, tokens []Token) []Token {
    capacity := len(source)/4 + 1   // PHP averages ~1 token per 5 bytes
    if cap(tokens) < capacity {
        tokens = make([]Token, 0, capacity)
    } else {
        tokens = tokens[:0]
    }
    sourceRef := &source
    for position := 0; position < len(source); {
        kind, length := next(source, position)
        if length <= 0 {
            length = 1              // never stall
        }
        end := position + length
        tokens = append(tokens, parsekit.NewToken(
            kind, sourceRef, cst.TextRange{Start: uint32(position), End: uint32(end)},
        ))
        position = end
    }
    return tokens
}
```

Conventions every lexer follows:

- `Lex(source)` allocates; `LexInto(source, buffer)` reuses a caller-supplied
  buffer so the parser can hand it a pooled slice.
- The scanner must always make progress. PHP clamps a zero-length match to one
  byte; the YAML lexer panics instead, because a stalled indentation scanner
  is a bug rather than an unknown character.
- Unrecognized bytes get an explicit `TkUnknown`-style kind rather than being
  dropped. Losslessness is a lexer property first.
- Hot scans go through `bytescan` (`IndexByte`, `IndexNonASCII`, …) which is
  allocation-free and has SIMD implementations selected by
  `GOEXPERIMENT=simd` on amd64 and arm64.

Lexers are mostly mode-less. Where a token's meaning depends on context — a
Twig keyword appearing inside HTML text, for instance — the grammar relabels
it at consumption time with `Parser.BumpAs`, instead of the lexer carrying
parser state. The YAML lexer is the exception: block structure genuinely needs
line-start and indentation state, so its scanner tracks `lineStart`,
`currentIndent`, and `flowDepth`.

### Token representation

```go
// internal/parser/parsekit/token.go
type Token struct {
    source *string
    start  uint32
    length uint16
    Kind   cst.Kind
}
```

All ordinary tokens from one lexer run share a single `*string` header, so a
token costs two machine words while still supporting 32-bit source offsets. A
token of 65,535 bytes or more (a huge heredoc, comment, or embedded text blob)
stores its own zero-copy substring header instead; `Range()` and `Text()`
handle both cases transparently. `Text()` is always a zero-copy slice of the
original source — never a copy.

## Event-based parsing with parsekit

`parsekit.Parser` is the language-neutral engine. It does **not** build a tree.
It walks the token cursor and appends to an event stream describing the tree
shape; the sink materializes it afterwards. This indirection is what makes
`Precede` (retroactively wrapping an already-parsed node) possible without any
tree surgery.

```go
// internal/parser/php/parser/parse.go
func parse(source string, observeBuffers func(parsekit.BufferStats)) Result {
    tokens := lexer.LexInto(source, parsekit.AcquireTokenBuffer(len(source)/4+1))
    parser := parsekit.NewOwned(tokens, parsekit.Config{
        GeneralRecoverySet: []syntax.Kind{syntax.TkSemicolon, syntax.TkCloseBrace},
        ErrorKind:          syntax.Error,
    })
    root := parser.Start()
    for !parser.AtEnd() {
        position := parser.GetPos()
        parseStatement(parser, false)
        if parser.GetPos() == position {
            // Hard progress guarantee: a production that consumed nothing
            // would otherwise loop forever on unparseable input.
            parser.AddError(parsekit.NewErrorBuilder("PHP statement"))
            parser.Bump()
        }
    }
    parser.Complete(root, syntax.PhpProgram)
    tree, errors := parser.Finish(source)
    return Result{Tree: tree, Errors: errors}
}
```

### Config

```go
type Config struct {
    GeneralRecoverySet []cst.Kind // tokens that plausibly start a new top-level element
    ErrorKind          cst.Kind   // the language's ERROR node kind
    EventsPerToken     int        // event-stream preallocation; 0 → 2
}
```

`EventsPerToken` is pure capacity tuning. Twig sets `5` because its grammar
nests deeply (HTML element → attribute → Twig expression → filter chain);
everything else uses the default of 2.

### The cursor API

`parsekit.source` skips trivia before every operation, so all of these operate
on the non-trivia token stream:

| Call | Meaning |
| --- | --- |
| `Peek() (Kind, bool)` | Kind of the current token; `false` at EOF |
| `PeekToken() *Token` | The current token, or `nil` at EOF |
| `PeekNextNonTriviaKind()` | Kind of the token after the current one |
| `PeekNthToken(n)` | **Raw** lookahead — does *not* skip trivia between tokens; used to fuse adjacent lexemes |
| `At(k)` / `AtSet(set)` / `AtEnd()` | Predicates |
| `AtFollowing([]Kind)` | The next *n* non-trivia kinds match a sequence |
| `AtFollowingContent([]FollowingContent)` | Same, with optional exact token-text matching |
| `GetPos() int` | Raw cursor index, for the progress checks shown above |

### Consumption

| Call | Meaning |
| --- | --- |
| `Bump() *Token` | Consume one token, emitting it under its lexer kind. Panics at EOF — the grammar must check `AtEnd` first |
| `BumpAs(kind) Token` | Consume one token but record a different kind (mode-less relabeling) |
| `BumpNextNAs(n, kind) []Token` | Consume *n* raw tokens; the sink fuses them into one tree token spanning all of them |
| `ExplicitlyConsumeTrivia()` | Force pending trailing trivia *inside* the currently open node instead of letting it attach outside. Call just before `Complete` in string-content parsers |

### Markers

Node boundaries are markers, not recursive constructors:

```go
node := parser.Start()          // reserves a placeholder event
parser.Bump()                   // ... children ...
parser.Complete(node, syntax.PhpNamespace)
```

`Start` appends an `evtPlaceholder`; `Complete` rewrites that slot into
`evtStartNode` with the chosen kind and appends a matching `evtFinishNode`. The
kind is therefore decided *after* the children are parsed, which is what makes
speculative parsing cheap.

`Precede` handles left-recursive constructs. Given an already-completed node it
starts a new marker and records a *forward parent* offset on the completed
node's start event, so completing the new marker retroactively wraps the old
node:

```go
lhs := parseUnary(parser)              // CompletedMarker
for parser.At(syntax.TkOperator) {
    m := parser.Precede(lhs)           // new marker wraps lhs
    parser.Bump()                      // the operator
    parseUnary(parser)                 // the right operand
    lhs = parser.Complete(m, syntax.PhpBinaryExpression)
}
```

Markers must be completed, and inner markers must be completed before outer
ones. `parsekit.DebugAsserts` (off in production, set to `true` in
`parsekit`'s test harness) enables the LIFO check in `complete` and the
"markers need to be completed" check that `Finish` runs.

## Error recovery

Two mechanisms cover essentially all recovery.

**`Expect` / `ExpectAny`** consume the wanted kind if present; otherwise they
record an error and delegate to `RecoverExpect`.

**`RecoverExpect(expectedKinds, recoverySet)`** is the core loop:

1. If the parser is already at a sync point — EOF, a kind in the local
   `recoverySet`, or a kind in `Config.GeneralRecoverySet` — return without
   consuming anything. Only the error record is produced, no `ERROR` node.
2. Otherwise open an `ERROR` marker and loop: bump the offending token; if the
   parser is now at one of `expectedKinds`, close the `ERROR` node and consume
   and return the expected token (resyncing past the garbage); if the parser is
   now at a sync point, close the `ERROR` node and return `nil`.

The general recovery set is always active in addition to the local one. It
holds the tokens that plausibly begin a new top-level element, so however lost
the parser is, it can find the next parseable construct. For PHP that is just
`;` and `}`; for Twig it is every `{%`/`{{`/`{#` variant plus `<`:

```go
// internal/parser/twig/parser/parser.go
var generalRecoverySet = []syntax.Kind{
    syntax.TkCurlyPercent, syntax.TkCurlyPercentMinus, syntax.TkCurlyPercentTilde,
    syntax.TkOpenCurlyCurly, syntax.TkOpenCurlyCurlyMinus, syntax.TkOpenCurlyCurlyTilde,
    syntax.TkOpenCurlyHashtag, syntax.TkOpenCurlyHashtagMinus, syntax.TkOpenCurlyHashtagTilde,
    syntax.TkLessThan, syntax.TkLessThanExclamationMarkMinusMinus, syntax.TkLessThanExclamationMark,
}
```

`Recover(recoverySet)` is `RecoverExpect` with no expected kinds. Do **not**
call it inside a `parseMany`-style loop: with no expected kind it may swallow
tokens that belong to the next sibling.

### Diagnostics from the parser

```go
type Error struct {
    Range    cst.TextRange // the offending token, or the last token's range at EOF
    Found    cst.Kind      // KindNone means "reached end of file"
    Expected string        // human-readable, e.g. `"}}", "-}}" or "~}}"`
}
```

`Error.Message()` renders `expected <Expected> but found <Found.TokenText()>`,
or `... but reached end of file` when `Found` is `KindNone`.

`ErrorBuilder` lets the grammar describe only what it expected;
`Parser.AddError` fills in the range and found-kind from the current token, or
from the last token's range at EOF. `ErrorBuilder.AtToken(tok)` pins both
explicitly, which is what you want when reporting about an *already consumed*
token.

## The sink: events to tree

`Parser.Finish(source)` hands the token slice, the event stream, and the error
list to the sink, which runs two passes.

**Pass 1 — `countTreeShape`.** Walks the event stream to compute the exact
number of tree tokens, the total number of direct-child slots, and the direct
child count of every node. It also resolves and marks forward-parent chains. It
panics on structural impossibilities — a `FinishNode` with no `StartNode`, a
forward parent pointing outside the stream or at a non-`StartNode`, unclosed
nodes, or tokens left unconsumed at the end. Those are parser bugs, and failing
loudly beats silently producing a tree that does not reproduce the source.

Direct child counts live in a `[]uint8` parallel to the event stream: 7 bits of
count plus one `forwardStartConsumed` flag. A node with 127 or more direct
children (a large file, class body, or statement list) spills into a
`map[int]uint32` so ordinary nodes do not pay for a 32-bit counter.

**Pass 2 — `finish`.** Replays the events into a `cst.Builder` that was sized
exactly by pass 1, doing three jobs the parser could not:

- **Re-attaching trivia.** Before every token event, and before the final
  event, `consumeTrivia` emits all pending trivia tokens with their original
  lexer kinds into the currently open node. This is where the losslessness the
  grammar ignored comes back.
- **Resolving forward parents.** On an `evtStartNode`, the sink walks the
  forward-parent chain, nulls the visited events (so revisiting them is a
  no-op), and starts the collected nodes in reverse order — outermost
  forward parent first — so the `Precede` wrapping materializes correctly.
- **Fusing tokens.** `evtAddNextNTokensAs` becomes a single tree token spanning
  from the first token's start to the *n*-th token's end.

## The CST

### Element, Node, Token

```go
// internal/parser/cst/tree.go
type Element interface {
    Kind() Kind
    Range() TextRange
    Parent() *Node  // nil for the root
    Text() string   // zero-copy slice of source
    isElement()     // keeps the interface closed to *Node / *Token
}
```

`*Node` is composite, `*Token` is a leaf. Unlike `rowan` there is no
green/red split: the sink computes absolute offsets and parent pointers
directly, so the tree is a single immutable allocation graph with no lazy
layer.

`Tree` is just the pair:

```go
type Tree struct {
    Source string
    Root   *Node
}
```

### Memory layout

Both `Node` and `Token` begin with the same 16-byte header:

```go
type elementHeader struct {
    parentOrSource unsafe.Pointer
    start          uint32
    lengthAndFlags uint16   // bit 15 = "is token", bits 0..14 = length
    kind           Kind
}
```

Consequences worth knowing:

- A `Token` is exactly 16 bytes.
- Element lengths under 32,767 bytes are inline. Longer spans set the overflow
  sentinel and store the real `TextRange` in a root-owned
  `map[unsafe.Pointer]TextRange`. Only very large nodes (usually the root) pay
  for that lookup.
- Non-root nodes store their parent in `parentOrSource`; the **root points to
  itself** and is the prefix of a `rootNodeStorage` that also holds the shared
  `*string` source, the range-overflow table, and the element slabs. Holding a
  pointer to any node keeps the whole tree and its source alive.
- Nodes and tokens are served from slabs (`nodeBlocks`, `tokenBlocks`,
  `childBlocks`), not one heap allocation per element.

### Builder

`cst.Builder` mirrors `rowan`'s `GreenNodeBuilder`:
`StartNode` / `Token` / `FinishNode` / `Finish`.

- `NewBuilder(source)` — dynamic growth.
- `NewBuilderCapacity(source, nodes, tokens)` — slab-preallocated.
- `NewBuilderCapacities(source, nodes, tokens, children)` — additionally
  preallocates direct-child storage, and switches on an **exactness contract**:
  every node must be started with `StartNodeCapacity` matching the number of
  children it actually emits, or `FinishNode` panics. This is the mode the sink
  uses, because pass 1 already knows every count.
- `StartNodeHint(kind, n)` reserves likely capacity without the exactness
  contract — used by the Vue frontend, which replays trees it did not size.

A node's range is computed on `FinishNode` from its first and last child. An
empty node becomes zero-width at the builder's running offset (the end of the
last token added), matching `rowan`'s behavior for empty nodes.

### Traversal API

All of these are methods on `*cst.Node` (a `nil` receiver is safe and yields
nothing):

**Children**

| Method | Notes |
| --- | --- |
| `ChildCount()`, `Child(i)`, `FirstChild()`, `LastChild()` | Direct access |
| `ChildElements() iter.Seq[Element]` | Preferred iteration; no interface slice allocated |
| `ChildNodes()`, `ChildTokens()` | Filtered `iter.Seq` |
| `ChildNodeCursor()`, `ChildTokenCursor()` | Zero-allocation value cursors (`for c := n.ChildNodeCursor(); c.Next();`) for hot paths |
| `Children() []Element` | Materializes a slice — a compatibility shim; prefer `ChildElements` |
| `ChildOfKind(k)`, `ChildTokenOfKind(k)` | First direct child of a kind |

**Navigation**

| Method | Notes |
| --- | --- |
| `Parent()` | `nil` at the root |
| `NextSibling()`, `PrevSibling()` | Also available on `*Token` |
| `FirstToken()`, `LastToken()` | Leftmost / rightmost token in the subtree |
| `Ancestors() iter.Seq[*Node]` | Self, then every parent up to the root |
| `AncestorOfKind(k)` | Nearest strict ancestor of a kind (also on `*Token`) |
| `Descendants() iter.Seq[Element]` | Preorder, includes self, includes tokens |
| `Walk() iter.Seq2[WalkEvent, Element]` | Preorder with `WalkEnter` / `WalkLeave`; tokens get `Enter` only |

**Position lookup**

| Method | Notes |
| --- | --- |
| `TokenAtOffset(off)` | Token at a byte offset. On a boundary the **right** (later) token wins. `nil` outside the node |
| `NodeAtOffset(off)` | Smallest node containing the offset |
| `DescendantForRange(r)` | Smallest element fully containing a range. Zero-width nodes are never returned; an empty range on a boundary prefers the right side |
| `RangeTrimmedTrivia()` | The node's range with leading trivia removed — use this for diagnostic ranges so the squiggle does not start on whitespace |

`RangeTrimmedTrivia` deserves emphasis: `parser.Start()` is often called before
leading whitespace is consumed, so a node's raw `Range()` can begin at
indentation. Any user-visible range (diagnostics, code lenses, folding, rename)
should use the trimmed variant.

## Positions, ranges, and LSP coordinates

```go
type TextRange struct { Start, End uint32 }  // half-open [Start, End), byte offsets
```

Internally, everything is byte offsets. LSP speaks lines and UTF-16 code units,
so `cst.LineIndex` bridges the two:

| Method | Direction |
| --- | --- |
| `Position(offset) (line, byteCol)` | offset → 0-based line, byte column |
| `PositionUTF16(offset) (line, utf16Col)` | offset → **LSP** position |
| `Offset(line, byteCol)` | line/byte column → offset |
| `OffsetUTF16(line, utf16Col)` | **LSP** position → offset |
| `LineEnd(line)` | Offset after the visible content, excluding `\n` or `\r\n` |
| `LineUTF16Length(line)` | Visible line length in UTF-16 code units |

`NewLineIndex` records the byte offset of each line start (a line begins after
each `\n`, so a bare `\r` does not start a new line — matching common LSP
tooling), and `lineAt` binary-searches it. The UTF-16 conversions fast-path
ASCII runs via `bytescan.IndexNonASCII` and only decode runes at the first
non-ASCII byte. Both directions clamp: out-of-range lines map to EOF, a column
past the end of its line maps to the line's end, and a column landing inside a
surrogate pair advances past the whole character.

The conversion happens exactly once per boundary, in the LSP layer:

```go
// internal/lsp/diagnostics.go
func protocolRangeFromText(lineIndex *cst.LineIndex, rng cst.TextRange) protocol.Range
func textRangeFromProtocol(lineIndex *cst.LineIndex, rng protocol.Range) cst.TextRange
```

Providers and analyzers work in `cst.TextRange` throughout.

## Language frontends

Every frontend exposes the same surface: a root package with `Parse`, and a
`Result` carrying the tree plus the flat error list.

```go
// internal/parser/php/phpparser.go
type Result = parser.Result   // { Tree *syntax.Tree; Errors []Error }

func Parse(source string) Result
func ParseBytes(source []byte) Result
func Root(source string) *syntax.Node
```

| Language | Extensions | Frontend notes |
| --- | --- | --- |
| PHP | `.php` | Statement-driven recursive descent; the largest grammar (`parse.go`, ~1,900 lines). Recovery syncs on `;` and `}` |
| Twig + HTML | `.twig`, `.html` | One grammar covers both. Split across `grammar.go`, `grammar_tags.go`, `grammar_html.go`, `grammar_expression.go`, `grammar_literal.go`, `grammar_twig.go`, `grammar_shopware.go` (Shopware-specific tags such as `sw_include`, `sw_icon`, `sw_extends`) |
| YAML | `.yaml`, `.yml` | Block and flow collections; the lexer is indentation-aware |
| XML | `.xml`, `.xlf`, `.xliff` | Documents, elements, attributes, tolerant editor-time recovery |
| JSON | `.json` | Second-simplest backend; a good template for a new language |
| JavaScript / TypeScript | `.js`, `.ts` | Enough structure for Shopware Administration analysis: calls, members, objects, imports, `export default` |
| SCSS | `.scss` | Stylesheet structure plus Sass expressions |
| Vue | `.vue` | Single-file components; see below |
| XPath | *(embedded only)* | Not extension-registered; used for XPath string literals inside PHP and XML |

Only the eight extension-mapped languages are in `language.NewBuiltinRegistry`.
XPath is invoked directly by the analyzers that recognize an XPath string
literal.

## Embedded and multi-language trees

Because kinds are globally unique, one tree can hold several languages. The Vue
frontend is the reference implementation:

```go
// internal/parser/vue/parse.go
func Parse(source string) Result {
    sections := Sections(source)              // scan <template>/<script>/<style> blocks
    builder := cst.NewBuilder(source)
    builder.StartNode(vuesyntax.VueDocument)
    for _, section := range sections {
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
            // ...
        case SectionStyle:
            parsed := scssparser.Parse(body)
            // ...
        }
        // ...
    }
    // ...
}
```

`replayTree` walks the sub-tree and re-emits every node and token into the
outer builder, adding the section's base offset to each token range:

```go
func replayElement(builder *cst.Builder, element cst.Element, base uint32) {
    switch typed := element.(type) {
    case *cst.Node:
        builder.StartNodeHint(typed.Kind(), typed.ChildCount())
        for child := range typed.ChildElements() {
            replayElement(builder, child, base)
        }
        builder.FinishNode()
    case *cst.Token:
        r := typed.Range()
        builder.Token(typed.Kind(), cst.TextRange{Start: base + r.Start, End: base + r.End})
    }
}
```

Parse errors are shifted by the same base. The result is **one** tree whose
ranges are all absolute offsets into the original `.vue` file, so LSP positions
need no offset map, and `Root.Text() == source` still holds. Vue's own kinds
frame the sections; the interior nodes carry Twig, JavaScript, and SCSS kinds.

The other embedding style parses on demand rather than inline. PHP string
literals that hold JSON, CSS, or XPath are recognized by
`php.EmbeddedLanguageStrings`, and the corresponding sub-parser runs only when
an analyzer needs it:

```go
// internal/lsp/diagnostics/embedded_language_diagnostics.go
embedded := php.EmbeddedLanguageStrings(
    p.phpIndex, path, document.Version, string(document.Text), document.SyntaxTree.Root,
)
for _, literal := range embedded {
    switch literal.Language {
    case php.EmbeddedLanguageJSON:  /* embeddedJSONDiagnostics(...) */
    case php.EmbeddedLanguageCSS:   /* embeddedCSSDiagnostics(...) */
    case php.EmbeddedLanguageXPath: /* embeddedXPathDiagnostics(...) */
    }
}
```

## Typed AST wrappers

Twig has a generated typed layer over the untyped tree
(`internal/parser/twig/ast`, a port of `ludtwig-parser`'s `syntax/typed.rs`).
Each composite kind gets a zero-cost wrapper with a checked cast and a `Syntax`
escape hatch:

```go
type TwigBlock struct{ n *syntax.Node }

func CastTwigBlock(n *syntax.Node) (TwigBlock, bool) {
    if n == nil || n.Kind() != syntax.TwigBlock {
        return TwigBlock{}, false
    }
    return TwigBlock{n: n}, true
}

func (x TwigBlock) Syntax() *syntax.Node { return x.n }
```

Hand-written accessors in `accessors.go` add the structural navigation:

```go
func (x TwigBlock) StartingBlock() (TwigStartingBlock, bool) {
    return CastTwigStartingBlock(firstChildNode(x.n, syntax.TwigStartingBlock))
}

func (x TwigBlock) Name() *syntax.Token {
    sb, ok := x.StartingBlock()
    if !ok {
        return nil
    }
    return sb.Name()
}
```

`nodes.go` is generated (`// Code generated by gen.go; DO NOT EDIT.`);
`accessors.go`, `helpers.go`, and `whitespace.go` are hand-written. No other
language has this layer — the rest go straight from kinds to `query`.

## Querying nodes

Every language has a `query` package: the single place where "what does this
tree mean" lives. Providers, indexers, and diagnostics all go through it rather
than walking children by index.

The rule these packages enforce is **no positional coupling**. Queries match on
kinds and structural relationships, never on "the third child of the second
child", so a grammar change that inserts a node does not silently break twenty
call sites.

### Shared conventions

Names are consistent across languages, which makes the packages predictable:

| Shape | Meaning | Example |
| --- | --- | --- |
| `Nodes(root, kinds...)` | Every node in the subtree with one of these kinds | `phpquery.Nodes(root, phpsyntax.PhpClassDeclaration)` |
| `Visit(root, visit, kinds...)` | Same traversal without materializing a slice; return `false` to stop | `phpquery.Visit(root, fn, kinds...)` |
| `<Thing>At(node)` | Nearest enclosing `Thing`, including `node` itself — the cursor-oriented form | `phpquery.ClassAt(node)`, `twigquery.TagAt(node)` |
| `<Thing>Name(node)` | The declared/source name as a string | `phpquery.MethodName(m)` |
| `<Thing>s(parent)` | Direct children of a role | `phpquery.Methods(class)`, `xmlquery.Attributes(el)` |
| `Iterate<Thing>s(node)` | Allocation-free value iterator for hot paths | `phpquery.IterateArguments(call)` |
| `StringValue(node)` | Unquoted literal contents | `jsonquery.StringValue(v)` |
| `<Thing>In<Context>(node, names...)` | Predicate: is this node in a specific semantic slot? | `twigquery.StringInFunction(node, "asset")` |

`*At` and `*In*` functions accept any descendant of the construct, which is
exactly what completion and hover need: they get whatever token is under the
cursor and ask "am I in an argument of `trans()`?" without knowing the
expression shape.

### PHP

`internal/parser/php/query` is the largest surface. Representative groups:

- **Structure** — `Namespace`, `UseDeclarations`, `Classes`, `ClassAt`,
  `ClassName`, `ClassBody`, `ClassExtends`, `ClassImplements`, `IsInterface`,
  `IsTrait`, `IsEnum`, `IsAbstract`
- **Members** — `Methods`, `MethodAt`, `MethodName`, `MethodReturnType`,
  `Functions`, `FunctionAt`, `FunctionLikeAt`, `Properties`,
  `PropertyVariables`, `PropertyType`, `DeclarationVisibility`
- **Parameters** — `Parameters`, `IterateParameters`, `ParameterName`,
  `ParameterType`, `ParameterOptional`, `ParameterDefault`, `ParameterVariadic`
- **Calls** — `Calls(root, names...)`, `CallAt`, `CallName`, `CallMethodName`,
  `CallReceiver`, `Arguments`, `IterateArguments`, `Argument(node, i)`,
  `ArgumentIndex`, `ArgumentName`, `ArgumentExpression`, `ArgumentValueText`
- **Attributes** — `AttributeGroups`, `Attributes`, `AttributeName`,
  `AttributeAt`
- **Assignments** — `AssignedVariable`, `AssignmentValue`, `VariableName`,
  `VariableKey`

A real consumer, from the Shopware 6.5 message-handler migration inspection:

```go
// internal/lsp/diagnostics/shopware_migration_message_handler.go
resolver := php.NewNameResolver(root)
for _, class := range phpquery.Classes(root) {
    extends := phpquery.DirectChild(class, phpsyntax.PhpExtendsClause)
    parent := phpquery.DirectChild(extends, phpsyntax.PhpName)
    if parent == nil || !strings.EqualFold(
        strings.Trim(resolver.Resolve(strings.TrimSpace(parent.Text())), "\\"),
        abstractMessageHandlerClass,
    ) {
        continue
    }
    name := phpquery.DirectChild(class, phpsyntax.PhpName)
    rng := class.RangeTrimmedTrivia()
    if name != nil {
        rng = name.RangeTrimmedTrivia()
    }
    // ... report a Problem anchored at `class` with range `rng`
}
```

Note the two habits worth copying: use of `RangeTrimmedTrivia` for the reported
range, and narrowing the range to the class *name* when it exists so the
squiggle is on the identifier rather than the whole class.

`VariableKey` is a small but instructive API: it returns the complete variable
token including the leading `$` as a zero-copy source slice, so binder and
inference lookups do not allocate a `"$" + name` string per lookup.

### Twig

`internal/parser/twig/query` is written for cursor-context questions:
`FunctionCallAt`, `FunctionName`, `FunctionArgumentIndex`, `StringArgument`,
`StringInFunction`, `FilterName`, `StringInFilter`, `IsFilterPosition`,
`TagAt`, `TagName`, `StringInTag`, `BlockAt`, `BlockName`, `HashStringMap`,
`HashKeyAt`, `StringIsHashValueForKey`, `StartingHTMLTagAt`,
`HTMLAttributeAt`, `HTMLTagName`, `HTMLAttributeName`.

Several of these encode deliberate precision about nesting. `StringInTag`
excludes strings nested in an option hash, so in
`{% sw_icon 'home' {'pack': 'custom'} %}` the icon name matches while `pack`
and `custom` do not. `StringArgumentsInFunctions` only accepts *direct*
arguments, so strings inside a nested call or array are not attributed to the
outer function.

### XML, YAML, JSON

Document-shaped languages get value-access APIs:

- **XML** — `Elements(root, names...)`, `ElementAt`, `ParentElement`,
  `ElementName`, `ChildElements`, `ChildElement`, `Attributes`, `Attribute`,
  `AttributeName`, `AttributeValue`, `AttributeValues`, `TextContent`,
  `NodeValue`. `TextContent` deliberately excludes nested child elements,
  matching the value semantics that DI manifests, parameters, and
  system-config fields need.
- **YAML** — `RootValue`, `IsValue`, `IsMapping`, `IsSequence`, `IsNull`,
  `Pairs`, `PairKey`, `PairValue`, `Property`, `PropertyPair`, `Items`,
  `ItemValue`, `ScalarValue`, `RawText`, `AncestorPair`, `PairPath`,
  `Contains`. `PairPath` walks up to build the key path — the basis for
  "which config key is the cursor in?".
- **JSON** — `RootValue`, `IsValue`, `Pairs`, `PairKey`, `PairValue`,
  `Property`, `StringValue`, `BooleanValue`, `IntegerValue`, `ScalarText`.

### JavaScript, SCSS

- **JavaScript** — call and member analysis for the Administration:
  `Calls(root, names...)`, `CallAt`, `CallCallee`, `CallName`,
  `CallMethodName`, `Arguments`, `StringArgument`, `ObjectArgument`,
  `StringInCall`, `Properties`, `Property`, `PropertyAt`, `PropertyName`,
  `PropertyValue`, `ExportDefaults`, `ExportDefaultExpression`, `ThisMember`,
  `StoreMember`, `ImportPath`, `DynamicImportPath`. `CallMethodName` returns
  the terminal identifier so fluent chains
  (`Application.addServiceProvider(...).addServiceProvider(...)`) stay stable.
  `ThisMember` and `StoreMember` are intentionally conservative about nesting:
  for `Shopware.Store.get('session').currentUser.id`, `currentUser` matches
  but `id` does not, because `id` belongs to the `currentUser` value.
- **SCSS** — `VariableAt`, `VariableName`, `StringAt`, `StringValue`,
  `FunctionCallAt`, `FunctionName`, `StringInFunction`,
  `StringArgumentInFunction`.

### NodeIndex

Whole-document queries repeated across several analyses can share one
traversal. JavaScript exposes this explicitly:

```go
index := jsquery.NewNodeIndex(root)   // one pass over the tree
calls := index.Calls("Shopware.Service")
objects := index.Nodes(jssyntax.JsObject)
```

The returned slices are immutable views valid for the lifetime of the parsed
tree.

## Document and index integration

A file is parsed **once** per snapshot, and the immutable tree is shared.

### Open editor buffers

```go
// internal/lsp/document.go
type TextDocument struct {
    URI            string
    Text           []byte
    Source         string
    Version        int
    SyntaxTree     *cst.Tree
    SyntaxLanguage language.ID
    ParseErrors    []parsekit.Error
    LineIndex      *cst.LineIndex

    analysisCache *documentAnalysisCache
}
```

`NewTextDocumentWithRegistry` builds the line index and calls
`registry.ParsePath(uri, source)`. An unsupported extension leaves
`SyntaxTree` nil and `SyntaxLanguage` empty — every consumer must handle that.

`TextDocument` values are **immutable snapshots**. An edit produces a new
`*TextDocument`; nothing mutates in place. That is what lets diagnostics
compare by pointer identity (`cached.document == document`) and lets
`MemoizedAnalysis(owner, revision, compute)` cache one derived analysis per
(document snapshot, workspace revision) pair so independent LSP features share
expensive semantic work.

`SyntaxContext` is the per-request bundle handed to providers:

```go
// internal/lsp/request.go
type SyntaxContext struct {
    Document        *TextDocument
    Language        language.ID
    DocumentContent []byte
    DocumentTree    *cst.Tree
    LineIndex       *cst.LineIndex
    Root            *cst.Node
    Token           *cst.Token   // token under the cursor
    Node            *cst.Node    // smallest node containing the cursor
}
```

`syntaxAtPosition` builds it with two editor-friendly adjustments beyond a
plain `TokenAtOffset`:

- **At EOF** there is no token to the right of the cursor, so it retries at
  `offset - 1`. Incomplete trailing input still gets useful context.
- **On trivia** it walks left to the previous non-trivia token, so a cursor
  after `{{ product.` or `trans('` resolves to the meaningful token rather
  than the whitespace.

### Workspace indexing

```go
// internal/indexer/parsed_file.go
type ParsedFile struct {
    Path    string
    Content []byte
    Source  string
    // ...
}

func (f *ParsedFile) SyntaxTree() *cst.Tree   // sync.Once
func (f *ParsedFile) LineIndex() *cst.LineIndex // sync.Once
func (f *ParsedFile) Memoized(key any, compute func() any) any
```

`ParsedFile` is the immutable input shared by every indexer handling one file.
Parsing and line indexing are lazy and happen at most once, so twenty indexers
touching a `.php` file cost one parse. `Source` reuses the `Content` byte
backing store via `unsafe.String` rather than copying every indexed file.

`Memoized` extends the same idea to derived analysis: concurrent indexers
asking for the same key block on one computation. Keys must be comparable; a
component pointer is the safest choice because it also isolates values from
separate workspace instances.

`FileScanner` eagerly warms the tree for paths that will certainly need it
(`shouldPreparsePath`), then clears memoized values at the
preparation/persistence boundary and releases syntax storage once a file's
prepared values are committed.

## Memory management and pooling

Cold-start indexing parses tens of thousands of files. Three bounded pools keep
that from permanently inflating the resident set of a long-running server.

**Parser event collections** (`parsekit`) — `eventCollection` holds the event
slice, the child-count buffer, and the marker slabs. `Parser.Finish` returns it
to a pool sized `min(GOMAXPROCS, 8)`, bounded to 8 MiB total, with per-item
caps of 2^19 events and 2^18 markers. The pool's `get` picks the
least-wasteful candidate that covers the requested capacity.

**Token buffers** (`parsekit`) — `AcquireTokenBuffer(capacity)` hands out a
pooled slice; `NewOwned` makes the parser return it after `Finish`. Buffers are
cleared on release, because the backing array otherwise retains every source
header the lexer wrote. Caps: 2^18 tokens per buffer, 2^19 items aggregate.

**CST element slabs** (`cst`) — node, token, and child-reference blocks come
from `transientBlockPool`s (caps 2^16/2^17/2^18 items per block, 2^17/2^18/2^19
pooled, max 256 blocks each). A scanner-owned tree can be recycled with
`Tree.ReleaseTransientStorage()`, which clears the tree and returns its slabs.

Both packages expose `ReleaseTransientBuffers()` to drop every idle slab, and
the file scanner calls both after a batch completes so the running server does
not retain cold-index scratch memory.

Two important caveats:

- `ReleaseTransientStorage` makes the tree **unusable**. It exists only for
  scanner-owned trees at a known lifecycle boundary. Parser and editor callers
  should let trees follow ordinary garbage-collection lifetime.
- `Parser.BufferStats()` reports token/event/node/marker utilization for
  capacity tuning. Sample it after completing the root marker and before
  `Finish` releases the owned buffers.

## Adding a language

Adding a language touches no shared code. The JSON backend (~600 lines across
four files) is the smallest complete reference.

1. **Reserve a kind range.** Create `internal/parser/<lang>/syntax/kind.go`
   with a `const <lang>Base Kind = <next free multiple of 4096>`, the token
   kinds, then the node kinds, then `Error`, then a `<lang>KindCount`
   sentinel. In `init`, build the name and text tables and call
   `cst.RegisterLanguage`. Add `aliases.go` re-exporting the `cst` types.
2. **Write the lexer.** `internal/parser/<lang>/lexer/lexer.go` with
   `Lex(source) []Token` and `LexInto(source, tokens) []Token`. Cover every
   byte, always make progress, use `bytescan` for scans, and emit an unknown
   kind rather than dropping input.
3. **Write the grammar.** `internal/parser/<lang>/parser/parse.go` exposing
   `Parse(source) Result`. Build a `parsekit.Config` with the language's
   general recovery set and `ErrorKind`, drive `parsekit.Parser` with
   `Start`/`Bump`/`Complete`/`Precede`/`Expect`, guard the top-level loop with
   a `GetPos()` progress check, and finish with `parser.Finish(source)`.
4. **Add the public entry point.** `internal/parser/<lang>/<lang>parser.go`
   re-exporting `Parse` and `Result`.
5. **Register the extension** in `language.NewBuiltinRegistry` — unless the
   language is embedded-only, like XPath.
6. **Add a query package.** `internal/parser/<lang>/query/query.go` following
   the naming conventions above. Consumers should never walk children by index.
7. **Test the invariants.** At minimum: losslessness
   (`result.Tree.Root.Text() == source`) over a fixture corpus, totality on
   truncated and garbage input, `DebugTree` snapshots for grammar shape, and a
   `bytescan`-comparable benchmark in
   `internal/parser/performance_validation_test.go`.

## Debugging and testing

### DebugTree

`cst.DebugTree(root)` renders the tree in `rowan`'s debug format — one line per
element, two-space indent per depth, no trailing newline:

```
PHP_PROGRAM@0..34
  PHP_NAMESPACE@0..25
    PHP_OPEN_TAG@0..5 "<?php"
    PHP_WHITESPACE@5..6 "\n"
    PHP_KEYWORD@6..15 "namespace"
    ...
```

Nodes render `KIND@start..end`; tokens add the quoted text. The escaping is
byte-for-byte compatible with Rust's `{:?}` for `&str` (named escapes for
`\0 \t \r \n \\ \"`, `\u{hex}` with lowercase digits otherwise) and text of 25
bytes or more is truncated at a char boundary followed by `" ..."`. That
compatibility is deliberate: it lets Twig snapshots be diffed directly against
`ludtwig`'s.

### The debug_ast command

```bash
go run cmd/debug_ast/main.go path/to/file.php
go run cmd/debug_ast/main.go -lang=js example.vue
echo "this.\$tc('key')" | go run cmd/debug_ast/main.go -lang=js -
```

The language is auto-detected from the extension, or forced with `-lang`
(`php`, `js`, `twig`, `vue`, `json`, `yaml`, `scss`, `xml`). Forcing the
language is how you inspect one embedded section of a `.vue` file in isolation.

### DebugAsserts

`parsekit.DebugAsserts` is `false` in production so the shipped parser never
panics on marker bookkeeping. `parsekit`'s test harness sets it to `true`,
enabling the LIFO completion check and the "markers need to be completed"
check in `Finish`. Turn it on in tests for a new grammar.

Note the asymmetry: marker-discipline assertions are opt-in, but the sink's
structural checks (unconsumed tokens, unclosed nodes, malformed forward
parents) always panic. A tree that does not reproduce its source is worse than
a crash, because it silently corrupts every rewrite built on top of it.

### Tests and benchmarks

```bash
go test ./internal/parser/...                 # all frontends
go test -race ./internal/parser/...           # pooling is shared mutable state
go test ./internal/parser/cst -run TestTree
go test -bench=BenchmarkNativeParsers ./internal/parser
```

`internal/parser/performance_validation_test.go` benchmarks every frontend
(and its lexer separately) against a representative Shopware-shaped source,
reporting allocations and bytes/second. Use it before and after any change to
`cst`, `parsekit`, or a hot grammar path.

## Further reading

- [`architecture.md`](architecture.md) — the system this layer sits under
- [`internal/parser/README.md`](../internal/parser/README.md) — the short
  in-tree summary of the same layering
- [`indexing.md`](indexing.md) — how trees reach indexers (`ParsedFile`) and
  when their storage is recycled
- [`lsp-server.md`](lsp-server.md) — how `SyntaxContext` turns a cursor
  position into a tree node for providers
- [`diagnostics-pipeline.md`](diagnostics-pipeline.md) — how trees become
  diagnostics and quick fixes
- [`refactoring-engine.md`](refactoring-engine.md) — how CST elements become
  validated source edits
- [`php-semantic-engine.md`](php-semantic-engine.md) — the semantic layer built
  on the PHP CST
- [`maintainability.md`](maintainability.md) — why the large grammar dispatch
  files are intentionally centralized
