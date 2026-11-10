# Parser, CST, and Query Architecture

This document describes how Shopware Language Server turns bytes on disk into
syntax trees, how those trees are represented, and how features ask questions
about them. Everything here lives in [`crates/syntax`](../crates/syntax) (the
language-neutral tree and parser engine), the `crates/parser-<language>`
frontends, and the [`crates/language`](../crates/language) registry that
selects a frontend for a file.

The diagnostics side of the pipeline — inspections, problems, quick-fix
anchoring — is documented separately in
[`diagnostics-pipeline.md`](diagnostics-pipeline.md).

## Table of contents

- [Design goals](#design-goals)
- [Pipeline overview](#pipeline-overview)
- [Crate map](#crate-map)
- [The kind registry](#the-kind-registry)
- [Lexing](#lexing)
- [Event-based parsing](#event-based-parsing)
- [Error recovery](#error-recovery)
- [The sink: events to tree](#the-sink-events-to-tree)
- [The CST](#the-cst)
- [Positions, ranges, and LSP coordinates](#positions-ranges-and-lsp-coordinates)
- [Language frontends](#language-frontends)
- [Embedded and multi-language trees](#embedded-and-multi-language-trees)
- [Typed AST wrappers](#typed-ast-wrappers)
- [Querying nodes](#querying-nodes)
- [Document and index integration](#document-and-index-integration)
- [Memory and sharing](#memory-and-sharing)
- [Adding a language](#adding-a-language)
- [Debugging and testing](#debugging-and-testing)

## Design goals

The parser foundation is a hand-written stack derived from the `ludtwig` /
`rust-analyzer` design, with trees stored in [`rowan`](https://docs.rs/rowan).
There is no tree-sitter and no generated grammar. Four invariants hold for
every language frontend:

1. **Parsing is total.** Every input produces a usable tree. `parse` has no
   error return; malformed input is represented by `ERROR` nodes plus a flat
   list of `ParseError`s.
2. **The tree is lossless.** The root's text equals the source for any input.
   Whitespace, comments, and unparseable garbage all remain in the tree, which
   is what makes structural refactoring and formatting-preserving rewrites
   possible.
3. **Ranges are absolute byte offsets** into the original source
   (`TextRange`/`TextSize`). No node needs an offset map, and every position is
   directly comparable with any other node's position in the same file.
4. **`swlsp-syntax` is language-neutral.** Adding a language never requires
   editing it.

## Pipeline overview

```
 file path + source
        │
        ▼
 LanguageId::from_path / parse        crates/language
   (extension → parser fn)
        │
        ▼
 swlsp_parser_<lang>::parse(source)   crates/parser-<lang>
        │
        ├─► lexer::lex  ─────────────►  Vec<Token>   (flat, includes trivia)
        │                               crates/parser-<lang>/src/lexer.rs
        │
        ├─► Parser::new(source, tokens, Config)
        │      grammar drives Parser:
        │        start / bump / complete / precede / expect / recover
        │      producing an event stream + Vec<ParseError>
        │                               crates/syntax/src/parse.rs
        │
        └─► Parser::finish
               sink replays events, re-attaches trivia,
               resolves forward parents
                    │
                    ▼
               rowan GreenNodeBuilder → Tree   crates/syntax/src/tree.rs
        │
        ▼
 consumers
   ├─ swlsp_lsp::TextDocument   (open editor buffers)
   ├─ swlsp_indexer::ParsedFile (workspace indexing)
   └─ <lang>::query, twig::ast  (semantic questions)
```

The lexer sees the whole source including trivia. The grammar never sees
trivia — the parser cursor skips it on every peek and bump — and the sink puts
it back when building the tree. That split is why the grammar stays readable
while the tree stays lossless.

## Crate map

| Location | Responsibility |
| --- | --- |
| `crates/syntax` | Language-neutral `Kind` registry and `language!` macro, `Token`, trivia-skipping event/marker `Parser`, recovery, `ParseError`, the sink, `Tree` over rowan, `SyntaxNodeExt`, `LineIndex`, `debug_tree` |
| `crates/parser-<lang>/src/syntax.rs` | The language's kind inventory, declared with `swlsp_syntax::language!` |
| `crates/parser-<lang>/src/lexer.rs` | `lex` (and, for hot languages, `lex_into`) producing `Vec<Token>` |
| `crates/parser-<lang>/src/lib.rs` or `parser.rs` / `parser/` | The grammar; exposes `parse(source) -> Parse` and `parse_shared(Arc<str>)` |
| `crates/parser-<lang>/src/query` | Semantic queries over that language's trees |
| `crates/parser-twig/src/ast.rs` | Typed node wrappers for Twig (the only language with this layer) |
| `crates/parser-twig/src/formatter` | Twig/HTML CST-to-text formatting with a private formatting IR |
| `crates/parser-<lang>/src/bin/dump-cst-<lang>.rs` | Prints debug trees for paths read from stdin |
| `crates/language` | `LanguageId`: extension → frontend registry used by every consumer |

## The kind registry

`Kind` is a single `u16` newtype shared by every language. Each language
declares a contiguous, non-overlapping range with the `language!` macro, which
defines one `pub const` per kind (tokens first, then nodes) and submits a
`LanguageSpec` through `inventory`:

```rust
// crates/parser-json/src/syntax.rs
swlsp_syntax::language! {
    name: "json", base: 4096,
    trivia: [TK_WHITESPACE, TK_LINE_BREAK],
    tokens: {
        TK_WHITESPACE = ("JSON_WHITESPACE", "whitespace"),
        // ... (debug name, diagnostic display text) ...
    },
    nodes: {
        JSON_DOCUMENT = "JSON_DOCUMENT",
        // ...
        ERROR = "JSON_ERROR",
    },
}
```

On first use the registry materializes four flat lookup tables (names, display
texts, trivia, is-token), so `Kind::name`, `Kind::token_text`,
`Kind::is_trivia` and `Kind::is_token` are O(1) slice reads with no per-call
language dispatch. Building the tables asserts on an empty name, an overlapping
range, a range reaching the reserved sentinel, a first node outside the range,
or a trivia kind outside the range — programming errors caught on first use.

`Kind::NONE` (`0xFFFF`, 65535) is reserved as the "no kind" sentinel — it is
what a `ParseError` carries in `found` when the parser hit end of file. No
language range may include it.

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

Because kinds are globally unique, all languages share one rowan language
(`swlsp_syntax::Lang`), and a single tree can legitimately mix kinds from
several languages — which is exactly what the Vue frontend does.

## Lexing

A lexer is a plain function over the source that emits a flat token vector.
Every byte of the source is covered exactly once, in order, with no gaps:

```rust
// crates/parser-php/src/lexer.rs
pub fn lex_into<'a>(source: &'a str, tokens: &mut Vec<Token<'a>>) {
    tokens.clear();
    // Production PHP averages about one token per five source bytes.
    tokens.reserve(source.len() / 4 + 1);
    let bytes = source.as_bytes();
    let mut position = 0;
    while position < bytes.len() {
        let (kind, length) = next(bytes, position);
        let end = position + length.max(1); // never stall
        tokens.push(Token::new(kind, source, position as u32, end as u32));
        position = end;
    }
}
```

Conventions every lexer follows:

- `lex(source)` allocates; `lex_into(source, &mut buffer)` reuses a
  caller-supplied vector where a frontend provides it.
- The scanner must always make progress. PHP clamps a zero-length match to one
  byte; the YAML lexer asserts instead, because a stalled indentation scanner
  is a bug rather than an unknown character.
- Unrecognized bytes get an explicit unknown kind rather than being dropped.
  Losslessness is a lexer property first.
- Tokens never split a UTF-8 character: an unknown multi-byte character becomes
  one token.
- Hot scans use `memchr` or plain byte loops; there is no per-token
  allocation.

Lexers are mostly mode-less. Where a token's meaning depends on context — a
Twig keyword appearing inside HTML text, for instance — the grammar relabels
it at consumption time with `Parser::bump_as`, instead of the lexer carrying
parser state. The YAML lexer is the exception: block structure genuinely needs
line-start and indentation state.

### Token representation

```rust
// crates/syntax/src/parse.rs
pub struct Token<'a> {
    pub kind: Kind,
    pub start: u32,
    pub text: &'a str, // borrowed from the source being parsed
}
```

Tokens borrow the source, so lexing never copies text. `range()` and `end()`
are derived from `start` and the text length.

## Event-based parsing

`swlsp_syntax::Parser` is the language-neutral engine. It does **not** build a
tree. It walks the token cursor and appends to an event stream describing the
tree shape; the sink materializes it afterwards. This indirection is what makes
`precede` (retroactively wrapping an already-parsed node) possible without any
tree surgery.

```rust
// crates/parser-php/src/parser.rs
pub(crate) fn parse_shared(shared: Arc<str>) -> Parse {
    let source: &str = &shared;
    let tokens = lexer::lex(source);
    let mut p = Parser::new(
        source,
        tokens,
        Config {
            general_recovery_set: GENERAL_RECOVERY, // [TK_SEMICOLON, TK_CLOSE_BRACE]
            error_kind: ERROR,
        },
    );
    let root = p.start();
    while !p.at_end() {
        let position = p.get_pos();
        parse_statement(&mut p, false);
        if p.get_pos() == position {
            // Hard progress guarantee: a production that consumed nothing
            // would otherwise loop forever on unparseable input.
            p.add_error(ErrorBuilder::new("PHP statement"));
            p.bump();
        }
    }
    p.complete(root, PHP_PROGRAM);
    let (tree, errors) = p.finish_with_source(Arc::clone(&shared));
    Parse { tree, errors }
}
```

### Config

```rust
pub struct Config {
    /// Tokens that can begin a new top-level element; always a sync point.
    pub general_recovery_set: &'static [Kind],
    /// Node kind wrapping skipped garbage.
    pub error_kind: Kind,
}
```

### The cursor API

The parser skips trivia before every operation, so all of these operate on the
non-trivia token stream:

| Call | Meaning |
| --- | --- |
| `peek() -> Option<Kind>` | Kind of the current token; `None` at EOF |
| `peek_token() -> Option<&Token>` | The current token, or `None` at EOF |
| `peek_next_non_trivia_kind()` | Kind of the token after the current one |
| `peek_nth_token(n)` | **Raw** lookahead — does *not* skip trivia between tokens; used to fuse adjacent lexemes |
| `at(k)` / `at_set(set)` / `at_end()` | Predicates |
| `at_following(&[Kind])` | The next *n* non-trivia kinds match a sequence |
| `at_following_content(&[FollowingContent])` | Same, with optional exact token-text matching |
| `get_pos() -> usize` | Raw cursor index, for the progress checks shown above |

### Consumption

| Call | Meaning |
| --- | --- |
| `bump() -> Token` | Consume one token, emitting it under its lexer kind. Panics at EOF — the grammar must check `at_end` first |
| `bump_as(kind) -> Token` | Consume one token but record a different kind (mode-less relabeling) |
| `bump_next_n_as(n, kind) -> &[Token]` | Consume *n* raw tokens; the sink fuses them into one tree token spanning all of them |
| `explicitly_consume_trivia()` | Force pending trailing trivia *inside* the currently open node instead of letting it attach outside. Call just before `complete` in string-content parsers |

### Markers

Node boundaries are markers, not recursive constructors:

```rust
let node = p.start();           // reserves a placeholder event
p.bump();                       // ... children ...
p.complete(node, PHP_NAMESPACE);
```

`start` appends a placeholder event and returns a move-only `Marker`
(`#[must_use]`); `complete` rewrites that slot into a start-node event with the
chosen kind, appends a matching finish-node event, and returns a
`CompletedMarker`. The kind is therefore decided *after* the children are
parsed, which is what makes speculative parsing cheap. `abandon` discards the
innermost open marker without creating a node.

`precede` handles left-recursive constructs. Given an already-completed node it
starts a new marker and records a *forward parent* offset on the completed
node's start event, so completing the new marker retroactively wraps the old
node:

```rust
let mut lhs = parse_unary(p);          // CompletedMarker
while p.at(TK_OPERATOR) {
    let m = p.precede(lhs);            // new marker wraps lhs
    p.bump();                          // the operator
    parse_unary(p);                    // the right operand
    lhs = p.complete(m, PHP_BINARY_EXPRESSION);
}
```

Markers must be completed, and inner markers must be completed before outer
ones. In debug builds (`cfg(debug_assertions)`, which includes `cargo test`)
the parser tracks open markers and asserts the LIFO order in `complete` and
`abandon`, and that no marker is left open in `finish`.

## Error recovery

Two mechanisms cover essentially all recovery.

**`expect` / `expect_any`** consume the wanted kind if present; otherwise they
record an error and delegate to `recover_expect`.

**`recover_expect(expected, recovery_set)`** is the core loop:

1. If the parser is already at a sync point — EOF, a kind in the local
   `recovery_set`, or a kind in `Config::general_recovery_set` — return without
   consuming anything. Only the error record is produced, no `ERROR` node.
2. Otherwise open an `ERROR` marker and loop: bump the offending token; if the
   parser is now at one of `expected`, close the `ERROR` node and consume and
   return the expected token (resyncing past the garbage); if the parser is now
   at a sync point, close the `ERROR` node and return `None`.

The general recovery set is always active in addition to the local one. It
holds the tokens that plausibly begin a new top-level element, so however lost
the parser is, it can find the next parseable construct. For PHP that is just
`;` and `}`; for Twig it is every `{%`/`{{`/`{#` variant plus `<`:

```rust
// crates/parser-twig/src/parser/mod.rs
pub(crate) const GENERAL_RECOVERY_SET: &[Kind] = &[
    TK_CURLY_PERCENT, TK_CURLY_PERCENT_MINUS, TK_CURLY_PERCENT_TILDE,
    TK_OPEN_CURLY_CURLY, TK_OPEN_CURLY_CURLY_MINUS, TK_OPEN_CURLY_CURLY_TILDE,
    TK_OPEN_CURLY_HASHTAG, TK_OPEN_CURLY_HASHTAG_MINUS, TK_OPEN_CURLY_HASHTAG_TILDE,
    TK_LESS_THAN, TK_LESS_THAN_EXCLAMATION_MARK_MINUS_MINUS, TK_LESS_THAN_EXCLAMATION_MARK,
];
```

`recover(recovery_set)` is `recover_expect` with no expected kinds. Do **not**
call it inside a `parse_many`-style loop: with no expected kind it may swallow
tokens that belong to the next sibling.

### Diagnostics from the parser

```rust
pub struct ParseError {
    pub range: TextRange, // the offending token, or the last token's range at EOF
    pub found: Kind,      // Kind::NONE means "reached end of file"
    pub expected: String, // human-readable, e.g. `"}}", "-}}" or "~}}"`
}
```

`ParseError::message` renders `expected <expected> but found <found text>`, or
`... but reached end of file` when `found` is `Kind::NONE`.

`ErrorBuilder` lets the grammar describe only what it expected;
`Parser::add_error` fills in the range and found kind from the current token,
or from the last token's range at EOF. `ErrorBuilder::at_token(&token)` pins
both explicitly, which is what you want when reporting about an *already
consumed* token; `at_range` pins only the range.

## The sink: events to tree

`Parser::finish` (or `finish_with_source`, which reuses an existing
`Arc<str>`) hands the tokens and the event stream to the sink, which replays
the events into a rowan `GreenNodeBuilder` in one pass, doing three jobs the
parser could not:

- **Re-attaching trivia.** Before every token event, and before the final
  event, the sink emits all pending trivia tokens with their original lexer
  kinds into the currently open node. This is where the losslessness the
  grammar ignored comes back.
- **Resolving forward parents.** On a start-node event, the sink walks the
  forward-parent chain, clears the visited events (so revisiting them is a
  no-op), and starts the collected nodes in reverse order — outermost forward
  parent first — so the `precede` wrapping materializes correctly.
- **Fusing tokens.** A `bump_next_n_as` event becomes a single tree token
  spanning from the first token's start to the *n*-th token's end.

The sink asserts on structural impossibilities — a forward parent that is not a
start-node event, or tokens left unconsumed at the end. Those are grammar bugs,
and failing loudly beats silently producing a tree that does not reproduce the
source.

## The CST

### Tree, nodes, and tokens

```rust
// crates/syntax/src/tree.rs
pub type SyntaxNode = rowan::SyntaxNode<Lang>;
pub type SyntaxToken = rowan::SyntaxToken<Lang>;
pub type SyntaxElement = rowan::SyntaxElement<Lang>;

pub struct Tree {
    source: Arc<str>,
    green: GreenNode,
}
```

rowan splits a tree into two layers. **Green nodes** are immutable, `Send +
Sync`, position-independent, and deduplicate identical tokens (whitespace,
punctuation, keywords) within one parse. **Red nodes** (`SyntaxNode`) are
cheap, thread-local cursors created on demand with parent pointers and absolute
offsets. A `Tree` stores the exact source plus the green root; `Tree::root()`
creates a fresh red root for traversal. Store a `Tree` (or `Arc` of it) when
data must cross threads; create red nodes where you traverse.

`Tree::text(range)` and `Tree::node_text(&node)` return a zero-copy slice of
the source. Prefer them (or `token.text()`) over `node.text().to_string()` in
hot paths.

### Traversal API

rowan's own API covers the basics: `children()`, `children_with_tokens()`,
`parent()`, `ancestors()`, `descendants()`, `descendants_with_tokens()`,
`preorder_with_tokens()`, `next_sibling()`/`prev_sibling()`,
`first_token()`/`last_token()`, `text_range()`. `SyntaxNodeExt` in
`swlsp-syntax` adds the helpers consumers need most:

| Method | Notes |
| --- | --- |
| `child_of_kind(k)`, `child_node_of_kind(k)`, `child_token_of_kind(k)`, `child_token_of_kinds(ks)` | First direct child of a kind |
| `ancestor_of_kind(k)` | Nearest strict ancestor of a kind (also on tokens through `SyntaxTokenExt`) |
| `ancestor_or_self_of_kind(k)` | Self if it matches, else the nearest ancestor |
| `descendants_of_kinds(ks)` | Preorder, including self, nodes only |
| `token_at(offset)` | Token at a byte offset. On a boundary the **right** (later) token wins. `None` outside the node |
| `node_at(offset)` | Smallest node containing the offset |
| `descendant_for_range(r)` | Smallest element fully containing a range. Zero-width children are skipped; an empty range on a boundary prefers the right side |
| `range_trimmed_trivia()` | The node's range with leading trivia removed — use this for diagnostic ranges so the squiggle does not start on whitespace |

`range_trimmed_trivia` deserves emphasis: `Parser::start` is often called
before leading whitespace is consumed, so a node's raw `text_range()` can begin
at indentation. Any user-visible range (diagnostics, code lenses, folding,
rename) should use the trimmed variant.

## Positions, ranges, and LSP coordinates

`TextRange` and `TextSize` (re-exported from rowan) are half-open byte ranges
and byte offsets. Internally, everything is byte offsets. LSP speaks lines and
UTF-16 code units, so `swlsp_syntax::LineIndex` bridges the two:

| Method | Direction |
| --- | --- |
| `position(offset) -> (line, byte_col)` | offset → 0-based line, byte column |
| `position_utf16(offset) -> (line, utf16_col)` | offset → **LSP** position |
| `offset(line, byte_col)` | line/byte column → offset |
| `offset_utf16(line, utf16_col)` | **LSP** position → offset |
| `line_start(line)`, `line_end(line)` | Line bounds; the end excludes `\n` or `\r\n` |
| `line_utf16_length(line)` | Visible line length in UTF-16 code units |

`LineIndex::new` records the byte offset of each line start with `memchr` (a
line begins after each `\n`, so a bare `\r` does not start a new line —
matching common LSP tooling), and lookups binary-search it. Both directions
clamp: out-of-range lines map to EOF, a column past the end of its line maps
to the line's end, and an offset inside a multi-byte character clamps to the
character's start.

The conversion happens once per boundary, in the LSP layer:

```rust
// crates/lsp/src/diagnostics.rs
pub use swlsp_rewrite::protocol_range;  // byte TextRange → protocol::Range
pub fn text_range(line_index: &LineIndex, range: protocol::Range) -> TextRange
```

Providers and analyzers work in `TextRange` throughout.

## Language frontends

Every frontend exposes the same surface: `parse(source) -> Parse`, and
`parse_shared(Arc<str>) -> Parse` for callers that already own the source.
`swlsp_syntax::Parse` carries the tree plus the flat error list:

```rust
pub struct Parse {
    pub tree: Tree,
    pub errors: Vec<ParseError>,
}
```

| Language | Crate | Extensions | Frontend notes |
| --- | --- | --- | --- |
| PHP | `parser-php` | `.php` | Statement-driven recursive descent; the largest grammar (`parser.rs`). Recovery syncs on `;` and `}` |
| Twig + HTML | `parser-twig` | `.twig`, `.html` | One grammar covers both, split across `parser/{mod,tags,html,expression,literal,twig,shopware}.rs` (`shopware.rs` holds Shopware tags such as `sw_include`, `sw_icon`, `sw_extends`) |
| YAML | `parser-yaml` | `.yaml`, `.yml` | Block and flow collections; the lexer is indentation-aware |
| XML | `parser-xml` | `.xml`, `.xlf`, `.xliff` | Documents, elements, attributes, tolerant editor-time recovery |
| JSON | `parser-json` | `.json` | The smallest backend; a good template for a new language |
| JavaScript / TypeScript | `parser-js` | `.js`, `.ts` | Enough structure for Shopware Administration analysis: calls, members, objects, imports, `export default` |
| SCSS | `parser-scss` | `.scss` | Stylesheet structure plus Sass expressions |
| Vue | `parser-vue` | `.vue` | Single-file components; see below |
| XPath | `parser-xpath` | *(embedded only)* | Not extension-registered; used for XPath string literals inside PHP and XML |

Only the eight extension-mapped languages are in `LanguageId`. XPath is invoked
directly by the analyzers that recognize an XPath string literal.

## Embedded and multi-language trees

Because kinds are globally unique, one tree can hold several languages. The Vue
frontend is the reference implementation:

```rust
// crates/parser-vue/src/lib.rs (simplified)
for section in &sections {
    let body = &source[section.body_range];
    let base = section.body_range.start();
    let embedded = match section.kind {
        SectionKind::Template => Some(swlsp_parser_twig::parse(body)),
        SectionKind::Script => Some(swlsp_parser_js::parse(body)),
        SectionKind::Style => Some(swlsp_parser_scss::parse(body)),
        SectionKind::Custom => None,
    };
    if let Some(parsed) = embedded {
        section_children.push(NodeOrToken::Node(parsed.tree.green().clone()));
        errors.extend(parsed.errors.into_iter().map(|mut error| {
            error.range += base;
            error
        }));
    }
    // ...
}
```

Green nodes are position-independent, so the embedded green tree is inserted
as-is: once it sits under the section node, every red node computed from it
has its exact absolute range in the `.vue` file. Parse errors are shifted by
the section's base. The result is **one** tree whose ranges are all absolute
offsets into the original file, so LSP positions need no offset map and the
root text still equals the source. Vue's own kinds frame the sections; the
interior nodes carry Twig, JavaScript, and SCSS kinds. `parse_sfc` additionally
returns the section table.

The other embedding style parses on demand rather than inline. PHP string
literals that hold JSON, CSS, or XPath are recognized by
`swlsp_php::embedded_language_strings`, and the corresponding sub-parser runs
only when an analyzer needs it:

```rust
// crates/ide-symfony/src/diagnostics/symfony/embedded.rs
for literal in embedded_language_strings(
    php_index, &path, i64::from(document.version), document.source(), &root,
) {
    result.extend(match literal.language {
        EmbeddedLanguage::Json => embedded_json_diagnostics(&literal.string),
        EmbeddedLanguage::Css => embedded_css_diagnostics(&literal.string),
        EmbeddedLanguage::XPath => embedded_xpath_diagnostics(&literal.string),
    });
}
```

## Typed AST wrappers

Twig has a typed layer over the untyped tree (`crates/parser-twig/src/ast.rs`,
a port of `ludtwig-parser`'s `syntax/typed.rs`). An `ast_nodes!` macro gives
every composite kind a zero-cost wrapper implementing rowan's `AstNode`:
`X::cast(node)` checks the kind and `x.syntax()` is the escape hatch.
Hand-written accessors add the structural navigation:

```rust
impl TwigBlock {
    pub fn name(&self) -> Option<SyntaxToken> {
        self.starting_block()?.name()
    }

    pub fn starting_block(&self) -> Option<TwigStartingBlock> {
        child(&self.0)
    }
}
```

No other language has this layer — the rest go straight from kinds to `query`.

## Querying nodes

Each language has a `query` module: the single place where "what does this
tree mean" lives. Providers, indexers, and diagnostics all go through it rather
than walking children by index. JSON is the exception: its few consumers keep
small private helpers (for example `crates/snippet/src/json.rs`).

The rule these modules enforce is **no positional coupling**. Queries match on
kinds and structural relationships, never on "the third child of the second
child", so a grammar change that inserts a node does not silently break twenty
call sites.

### Shared conventions

Names are consistent across languages, which makes the modules predictable:

| Shape | Meaning | Example |
| --- | --- | --- |
| `nodes(root, kinds)` | Every node in the subtree with one of these kinds | `phpquery::nodes(&root, &[PHP_CLASS_DECLARATION])` |
| `visit(root, kinds, f)` / `iterate_nodes` | Same traversal without materializing a vector | `phpquery::visit(...)` |
| `<thing>_at(node)` | Nearest enclosing thing, including `node` itself — the cursor-oriented form | `phpquery::class_at`, `twig::query::tag_at` |
| `<thing>_name(node)` | The declared/source name as a string | `phpquery::method_name` |
| `<thing>s(parent)` | Direct children of a role | `phpquery::methods`, `xml::query::attributes` |
| `iter_<thing>s(node)` | Allocation-free iterator for hot paths | `js::query::iter_arguments` |
| `string_value(node)` | Unquoted literal contents | `scss::query::string_value` |
| `<thing>_in_<context>(node, names)` | Predicate: is this node in a specific semantic slot? | `twig::query::string_in_function(node, &["asset"])` |

`*_at` and `*_in_*` functions accept any descendant of the construct, which is
exactly what completion and hover need: they get whatever token is under the
cursor and ask "am I in an argument of `trans()`?" without knowing the
expression shape.

### PHP

`crates/parser-php/src/query` is the largest surface. Representative groups:

- **Structure** — `namespace`, `use_declarations`, `classes`, `class_at`,
  `class_name`, `class_body`, `class_extends`, `class_implements`,
  `is_interface`, `is_trait`, `is_enum`, `is_abstract`
- **Members** — `methods`, `method_at`, `method_name`, `method_return_type`,
  `functions`, `function_at`, `function_like_at`, `properties`,
  `property_variables`, `property_type`, `declaration_visibility`
- **Parameters** — `parameters`, `parameter_name`, `parameter_type`,
  `parameter_optional`, `parameter_default`, `parameter_variadic`
- **Calls** — `calls`, `call_at`, `call_name`, `call_method_name`,
  `call_receiver`, `arguments`, `argument`, `argument_index`, `argument_name`,
  `argument_expression`, `argument_value_text`, `string_in_call`
- **Attributes** — `attribute_groups`, `attributes`, `attribute_name`,
  `attribute_at`
- **Assignments** — `assigned_variable`, `assignment_value`, `variable_name`,
  `variable_key`

A real consumer, from the Shopware 6.5 message-handler migration inspection:

```rust
// crates/ide-shopware/src/diagnostics/migration/message_handler.rs
let resolver = scan::resolver(root);
for class in scan::classes(root) {
    if ctx.is_cancelled() {
        return result;
    }
    let Some(parent) = phpquery::direct_child(&class, PHP_EXTENDS_CLAUSE)
        .and_then(|extends| phpquery::direct_child(&extends, PHP_NAME))
    else {
        continue;
    };
    if !equal_fold(
        resolver.resolve(node_text(&parent).trim()).trim_matches('\\'),
        ABSTRACT_MESSAGE_HANDLER_CLASS,
    ) {
        continue;
    }
    // ... report a Problem anchored at `class`, ranged on its trimmed name
    //     (`class_name_range(&class)`)
}
```

Note the habits worth copying: checking cancellation in the loop, and narrowing
the reported range to the class *name* (trimmed of trivia) so the squiggle is
on the identifier rather than the whole class.

`variable_key` is a small but instructive API: it returns the complete variable
token including the leading `$` as a zero-copy source slice, so binder and
inference lookups do not allocate a `"$" + name` string per lookup.

### Twig

`crates/parser-twig/src/query.rs` is written for cursor-context questions:
`function_call_at`, `function_name`, `function_argument_index`,
`string_argument`, `string_in_function`, `filter_name`, `string_in_filter`,
`is_filter_position`, `tag_at`, `tag_name`, `string_in_tag`, `block_at`,
`block_name`, `hash_string_map`, `hash_key_at`,
`string_is_hash_value_for_key`, `starting_html_tag_at`, `html_attribute_at`,
`html_tag_name`, `html_attribute_name`.

Several of these encode deliberate precision about nesting. `string_in_tag`
excludes strings nested in an option hash, so in
`{% sw_icon 'home' {'pack': 'custom'} %}` the icon name matches while `pack`
and `custom` do not. `string_arguments_in_functions` only accepts *direct*
arguments, so strings inside a nested call or array are not attributed to the
outer function.

### XML, YAML

Document-shaped languages get value-access APIs:

- **XML** — `elements`, `element_at`, `parent_element`, `element_name`,
  `child_elements`, `child_element`, `attributes`, `attribute`,
  `attribute_name`, `attribute_value`, `attribute_values`, `text_content`,
  `node_value`. `text_content` deliberately excludes nested child elements,
  matching the value semantics that DI manifests, parameters, and
  system-config fields need.
- **YAML** — `root_value`, `is_value`, `is_mapping`, `is_sequence`, `is_null`,
  `pairs`, `pair_key`, `pair_value`, `property`, `property_pair`, `items`,
  `item_value`, `scalar_value`, `raw_text`, `ancestor_pair`, `pair_path`,
  `contains`. `pair_path` walks up to build the key path — the basis for
  "which config key is the cursor in?".

### JavaScript, SCSS

- **JavaScript** — call and member analysis for the Administration:
  `calls`, `call_at`, `call_callee`, `call_name`, `call_method_name`,
  `arguments`, `string_argument`, `object_argument`, `string_in_call`,
  `properties`, `property`, `property_at`, `property_name`, `property_value`,
  `export_defaults`, `export_default_expression`, `this_member`,
  `store_member`, `import_path`, `dynamic_import_path`. `call_method_name`
  returns the terminal identifier so fluent chains
  (`Application.addServiceProvider(...).addServiceProvider(...)`) stay stable.
  `this_member` and `store_member` are intentionally conservative about
  nesting: for `Shopware.Store.get('session').currentUser.id`, `currentUser`
  matches but `id` does not, because `id` belongs to the `currentUser` value.
- **SCSS** — `variable_at`, `variable_name`, `string_at`, `string_value`,
  `function_call_at`, `function_name`, `string_in_function`,
  `string_argument_in_function`.

### NodeIndex

Whole-document queries repeated across several analyses can share one
traversal. JavaScript exposes this explicitly:

```rust
let index = swlsp_parser_js::query::NodeIndex::new(&root); // one pass over the tree
let calls = index.calls(&["Shopware.Service"]);
let objects = index.nodes_of_kind(JS_OBJECT);
```

## Document and index integration

A file is parsed **once** per snapshot, and the immutable tree is shared.

### Open editor buffers

```rust
// crates/lsp/src/document.rs
pub struct TextDocument {
    pub uri: String,
    pub version: i32,
    pub language: Option<LanguageId>, // None for files without a registered parser
    source: Arc<str>,
    tree: Option<Tree>,
    parse_errors: Vec<ParseError>,
    line_index: Arc<LineIndex>,
    analysis: Mutex<HashMap<AnalysisKey, AnalysisValue>>,
}
```

`TextDocument::new` parses through the language registry (choosing the parser
by extension) and builds the line index; the tree and the line index share one
`Arc<str>` copy of the source. An unsupported extension leaves `tree()` and
`language` as `None` — every consumer must handle that.

`TextDocument` values are **immutable snapshots**. An edit publishes a new
`Arc<TextDocument>`; nothing mutates in place. That is what lets diagnostics
compare by pointer identity and lets
`memoized_analysis(owner, revision, compute)` cache one derived analysis per
(document snapshot, workspace revision, owner, value type) so independent LSP
features share expensive semantic work.

`SyntaxContext` is the per-request bundle handed to providers:

```rust
// crates/lsp/src/document.rs
pub struct SyntaxContext {
    pub document: Option<Arc<TextDocument>>,
    pub language: Option<LanguageId>,
    pub root: Option<SyntaxNode>,
    /// The token under the cursor (moved left off trivia and EOF).
    pub token: Option<SyntaxToken>,
    /// The smallest node containing the cursor.
    pub node: Option<SyntaxNode>,
}
```

`syntax_at_position` builds it with two editor-friendly adjustments beyond a
plain `token_at`:

- **At EOF** there is no token to the right of the cursor, so it retries at
  `offset - 1`. Incomplete trailing input still gets useful context.
- **On trivia** it walks left to the previous non-trivia token, so a cursor
  after `{{ product.` or `trans('` resolves to the meaningful token rather
  than the whitespace.

### Workspace indexing

```rust
// crates/indexer/src/parsed_file.rs
impl ParsedFile {
    pub fn path(&self) -> &str;
    pub fn source(&self) -> &str;
    pub fn syntax_tree(&self) -> Option<&Tree>;   // built lazily, at most once
    pub fn line_index(&self) -> &LineIndex;       // built lazily, at most once
    pub fn memoized<T: Any + Send + Sync>(&self, key: usize, compute: impl FnOnce() -> T) -> Arc<T>;
}
```

`ParsedFile` is the immutable input shared by every indexer handling one file.
Parsing and line indexing are lazy and happen at most once, so twenty indexers
touching a `.php` file cost one parse. `ParsedFile::new` takes ownership of the
file content and reuses the allocation when it is valid UTF-8 (other files are
decoded lossily).

`memoized` extends the same idea to derived analysis: concurrent indexers
asking for the same `(type, key)` share one computation. A component's address
is the safest key because it also isolates values from separate workspace
instances.

`FileScanner` eagerly builds the tree for paths that will certainly need it
(`should_preparse_path`), then clears memoized values at the
preparation/persistence boundary.

## Memory and sharing

Cold-start indexing parses tens of thousands of files, so the tree layer is
designed to keep per-file cost low without global pools:

- **One source copy.** The `Tree`, the `LineIndex`, the `TextDocument`, and
  the `ParsedFile` share one `Arc<str>`. Use `parse_shared` when you already
  hold an `Arc<str>`.
- **Token deduplication.** rowan's green builder interns identical tokens
  within one parse, so repeated whitespace, punctuation, and keywords share
  storage.
- **Lazy red trees.** Red nodes are created on demand during traversal and
  dropped with the traversal; only the green tree is retained.
- **Ordinary ownership.** Trees are freed when the last `ParsedFile` or
  `TextDocument` holding them is dropped; there is no manual release step.

## Adding a language

Adding a language touches no shared code. The JSON backend (about 350 lines
across three files) is the smallest complete reference.

1. **Reserve a kind range.** Create `crates/parser-<lang>` with
   `src/syntax.rs` invoking `swlsp_syntax::language!` with the next free
   multiple of 4096 as `base`, the trivia kinds, the token kinds, then the node
   kinds including an `ERROR` kind.
2. **Write the lexer.** `src/lexer.rs` with `lex(source) -> Vec<Token>`. Cover
   every byte, always make progress, never split a UTF-8 character, and emit
   an unknown kind rather than dropping input.
3. **Write the grammar.** Expose `pub fn parse(source: &str) -> Parse` and
   `parse_shared(Arc<str>)`. Build a `Config` with the language's general
   recovery set and `error_kind`, drive `Parser` with
   `start`/`bump`/`complete`/`precede`/`expect`, guard the top-level loop with
   a `get_pos()` progress check, and finish with `finish_with_source`.
4. **Add a dump binary.** `src/bin/dump-cst-<lang>.rs` calling
   `swlsp_syntax::dump::run(parse)`.
5. **Register the extension** in `LanguageId` (`crates/language/src/lib.rs`) —
   unless the language is embedded-only, like XPath.
6. **Add a query module.** `src/query.rs` (or `src/query/mod.rs`) following
   the naming conventions above. Consumers should never walk children by index.
7. **Test the invariants.** At minimum: losslessness (root text equals source)
   over fixtures, totality on truncated and garbage input, `debug_tree`
   snapshots for grammar shape, and a case in the `NativeParsers` benchmark
   (`crates/bench/benches/parsers.rs`).

## Debugging and testing

### Debug trees

`swlsp_syntax::debug_tree(&root)` (also `Parse::debug_dump`, which appends the
errors, and `{:?}` on a `Tree`) renders the tree in rowan's debug format — one
line per element, two-space indent per depth, no trailing newline:

```
PHP_PROGRAM@0..34
  PHP_NAMESPACE@0..25
    PHP_OPEN_TAG@0..5 "<?php"
    PHP_WHITESPACE@5..6 "\n"
    PHP_KEYWORD@6..15 "namespace"
    ...
```

Nodes render `KIND@start..end`; tokens add the quoted text using Rust's `{:?}`
escaping, and long text is truncated at a char boundary followed by `" ..."`.
The format matches `ludtwig`'s, so Twig snapshots can be diffed against it.

### The dump-cst binaries

Each parser crate ships a `dump-cst-<lang>` binary that reads file paths from
stdin and prints `=== <path>` followed by the debug tree and parse errors:

```bash
echo path/to/file.php | cargo run -q -p swlsp-parser-php --bin dump-cst-php
find src -name '*.twig' | cargo run -q --release -p swlsp-parser-twig --bin dump-cst-twig
```

Use `dump-cst-vue` for whole `.vue` files, or the Twig/JS/SCSS binaries on an
extracted section to inspect one embedded language in isolation.

### Debug assertions

Marker-discipline checks (LIFO completion, no abandoned outer markers, no open
markers at `finish`) run only with `debug_assertions`, so the release server
never panics on marker bookkeeping while `cargo test` catches grammar mistakes.

Note the asymmetry: marker-discipline assertions are debug-only, but the sink's
structural checks (unconsumed tokens, malformed forward parents) always panic.
A tree that does not reproduce its source is worse than a crash, because it
silently corrupts every rewrite built on top of it.

### Tests and benchmarks

```bash
cargo test -p swlsp-syntax                      # tree, parser engine, line index
cargo test -p swlsp-parser-twig                 # one frontend
cargo test -p 'swlsp-parser-*'                  # all frontends
cargo bench -p swlsp-bench --bench parsers      # NativeParsers benchmark
```

The `NativeParsers` benchmark (`crates/bench/benches/parsers.rs`) parses a
representative Shopware-shaped input per frontend, asserts losslessness, and
reports throughput. It runs in CI through CodSpeed; run it locally before and
after any change to `swlsp-syntax` or a hot grammar path.

## Further reading

- [`architecture.md`](architecture.md) — the system this layer sits under
- [`indexing.md`](indexing.md) — how trees reach indexers (`ParsedFile`)
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
