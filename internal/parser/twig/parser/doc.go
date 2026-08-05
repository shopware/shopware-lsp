// Package parser turns a template source string into a lossless syntax tree via
// [Parse]. It is a port of ludtwig-parser's parser core (parser.rs, event.rs,
// sink.rs, source.rs, parse_error.rs) and its grammar modules.
//
// [Parse] is total: for ANY input it returns a [Result] whose Tree is a usable,
// lossless [syntax.Tree] (Tree.Root.Text() always equals the source) and whose
// Errors is a flat, ordered list of [Error] diagnostics. Malformed input
// produces ERROR nodes and TK_UNKNOWN tokens rather than a panic or a lost
// byte, which makes the parser suitable for the always-broken buffers an editor
// feeds an LSP server.
//
// # Pipeline
//
// Parsing runs in three stages, mirroring ludtwig:
//
//	source --lexer.Lex--> []lexer.Token
//	       --grammar over a trivia-skipping Source--> events + errors
//	       --sink (replay events, attach trivia)--> syntax.Tree
//
// The grammar is a marker/event parser: instead of building nodes directly it
// emits a flat event stream with forward-parent links, which the sink replays
// into the final tree while attaching trivia (whitespace and line breaks) to
// the correct nodes. Expressions are parsed with a Pratt (precedence-climbing)
// parser.
//
// # Error recovery
//
// The parser never stops at the first error. It uses recovery sets — the nine
// Twig delimiters plus the HTML openers "<", "<!--" and "<!" — to resynchronize
// after an unexpected token, so a single mistake yields one diagnostic and a
// mostly-intact tree instead of a cascade.
//
// # Diagnostics
//
// Each [Error] carries the byte [syntax.TextRange] of the offending token, the
// [syntax.Kind] actually found (syntax.KindNone at end of file), and a
// human-readable description of what was expected. [Error.Message] and
// [Error.String] render the exact strings ludtwig produces, so its snapshot
// suite doubles as twig-go's conformance suite.
package parser
