# The PHP formatter

`crates/php-format` formats PHP to
[PER Coding Style 2.0](https://www.php-fig.org/per/coding-style/). It serves
`textDocument/formatting` for PHP (`crates/ide-php/src/formatting`) and
`shopware-lsp format`. What it changes is listed in
[`reference.md`](reference.md#php-formatting); this page explains how it
works and how a change to it is checked.

## Why the token stream

The PHP tree is lossless but coarse: imports and trait adaptations are flat
runs of tokens, `declare(…)` is a call, and some valid syntax
(`$a = &$b`, labels) reaches the tree as an error node. A formatter that
printed the tree node by node would need every shape to be exact and would
lose what it did not know.

So the formatter never prints nodes. It takes every token that is not white
space, in order, and decides one thing per token: the **gap** before it. A
gap is nothing, a space, or a line break with an indentation. Tokens are
written back in their order, so nothing can be lost or reordered by the
layout, and whatever no rule knows about falls back to how the source set
the two tokens apart.

## Stages

| Stage | Module | What it does |
|---|---|---|
| Normalize | `normalize.rs` | The changes that are more than white space (braces, `elseif`, `new Foo()`, `[…]`, visibility, modifier order, one trait or property per statement, import order, attribute blocks). They are text edits; the result is parsed again, for a few rounds until no edit is left. |
| Tokens | `tokens.rs` | Builds the token list with the line breaks the source had before each token, and pairs the brackets. Refuses documents it does not handle. |
| Casing | `casing.rs` | Lower-cases keywords, native types and `true`/`false`/`null`, and shortens casts. Only where a word is a keyword: `App\Enum\Status`, `Foo::LIST` and `$a->print()` keep their case. |
| Layout | `layout.rs` | Walks the tree and sets the gaps PER Coding Style fixes: statements, blocks, class members, lists, conditions, attributes, the header of a file. |
| Spacing | `spacing.rs` | The gap between two tokens no layout rule decided: a table of token pairs, and the places an expression may keep a line break of the source. |
| Emit | `emit.rs` | Writes the tokens, works out indentation levels, and places comments. |
| Wrap | `wrap.rs` | Runs layout and emit, and while a line is over the width (80 characters, or `php.lineWidth`) marks the outermost place it can be split at (a list, a chain of calls, the loosest operators, a ternary, a short closure, the value after `=`) and runs them again. |
| Verify | `verify.rs` | Parses the result and compares it with the source. |

### Lists

A bracket pair is laid out as a list by `layout::list`: on one line, or one
item per line with a trailing comma where PHP allows one. A list is split
when the source has a line break between two of its items or inside one of
them, outside further brackets. That is what lets
`$app->get('/x', function () { … })` keep its closure on the line while
`foo($a,\n$b)` becomes one argument per line. The arms of a `match` are
always split.

### Indentation

An indentation is not a count of open brackets. It is relative to a line
that is already written:

- `Inside(token)`: one level deeper than the line of `token`, for what a
  bracket or a block holds;
- `Like(token)`: the level of the line of `token`, for a closing bracket;
- `Continue`: for a line that continues an expression, one level deeper
  than the line the expression starts on. The expression is found from the
  tree: the lowest node around the token that does not start with it.

This gives chains, operators and ternaries their one level, and keeps
nested ones relative to where they start.

### Comments

No layout rule mentions comments. The emitter keeps a comment on the line
it was on: after the token before it, or on a line of its own at the level
of the token after it (inside the brackets when that token closes them).
A line comment forces the line break after it. Docblocks are re-indented on
their `*`; other comments over several lines keep the distance between
their lines.

## Safety

The formatter must not change what a file does. Three things keep it so:

1. **It refuses what it does not understand.** Syntax errors, unknown
   tokens and error nodes return `Skipped::SyntaxError` or
   `Skipped::Unsupported`, and the document is left as it is. A document
   with markup outside PHP tags goes through `template.rs`, which keeps
   every line and its indentation and only sets the spaces inside a line.
2. **Normalizing is conservative.** Each edit is made only on a shape that
   cannot be misread: `new Foo` gets its parentheses only for a plain class
   name at the end of the expression, a trailing comma is added only to
   real lists (not to `include(…)` or `new (…)`).
3. **Every result is checked.** `verify::same_code` parses the formatted
   text and compares its tokens and comments with those of the source,
   levelling only what the formatter changes on purpose (trailing commas,
   the case of keywords, the indentation of heredocs). A mismatch returns
   `Skipped::Unsafe` instead of text.

The check uses the native lexer, which is more lenient than PHP. Two of its
gaps made the formatter produce invalid PHP before they were closed in the
lexer (`1e-10` was three tokens, a heredoc did not end at `EOT .`); two more kept
valid files from being formatted (`'a'.++$i` was one operator, `.5` no
number), so a
change to the formatter is also checked with PHP itself.

## Formatting a selection

`try_format_range` (`src/range.rs`) has no layout of its own. It formats the
whole document, compares source and result line by line (lines are matched
by what they hold apart from white space, so a line that only has its
spaces set is a change of its own), and applies the changes that touch the
selected lines.

The result is kept when formatting it again gives the formatted document:
it is then the same code, with the other lines as they were. A change that
needs a second one elsewhere fails that check: an opening brace without its
closing one, or one half of a reordered import. The selection then grows to
the construct around it, node by node, until the check holds; after a few
attempts the whole document is formatted. A list that is reflowed is one
change, however many lines it has.

Each attempt formats the document once more, so formatting a selection
costs two to a few times what formatting the document costs.

## Checking a change

Unit tests (`src/tests`) hold the examples of the specification, section by
section: each compliant example must come out unchanged, and a mangled
version must come out as the example.

`examples/corpus.rs` formats every PHP file below the given directories,
read-only, and counts what happened. It must report no `unsafe` and no
`unstable` (a second pass that changes the result):

```bash
cargo run --release -p swlsp-php-format --example corpus -- ~/shopware/src ~/shopware/vendor
LAYOUT=lines cargo run --release -p swlsp-php-format --example corpus -- ~/shopware/src
LAYOUT=flat  cargo run --release -p swlsp-php-format --example corpus -- ~/shopware/src
```

`LAYOUT=lines` puts every token on a line of its own before formatting and
`LAYOUT=flat` puts all of them on one, which exercises the layout on input
no one would write. `RANGE=1` formats three selections of every file and
must report none `wrong`. `LONG=1` tells for every line still over the width what
on it cannot be split. `OUT=<directory>` writes the formatted files there, for
two checks with PHP tools:

- parse source and result with `nikic/php-parser` and compare the trees:
  they must be equal but for the normalizations;
- run PHP-CS-Fixer with the `@PER-CS2.0` rule set over the results: on
  code that follows the conventions of the specification it has nothing
  left to change. What it still changes on other code is not layout: it
  adds `// no break` comments and moves trait imports to the top of a
  class.
