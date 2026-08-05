package cst

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DebugTree renders the tree in rowan's debug format, byte-for-byte compatible
// with ludtwig's debug_tree(): one line per element, 2-space indent per depth,
// no trailing newline.
//
//	node:  KIND@start..end
//	token: KIND@start..end "text"
//
// The token text is escaped exactly like Rust's {:?} for &str (see
// writeEscaped): the named escapes \0 \t \r \n \\ \", printable characters
// verbatim, and every other character as \u{hex} with lowercase hex digits.
func DebugTree(root *Node) string {
	var b strings.Builder
	writeElement(&b, root, 0)
	// rowan appends a trailing newline via the pretty formatter which ludtwig's
	// debug_tree() then trims; we simply never emit it.
	return b.String()
}

func writeElement(b *strings.Builder, e Element, depth int) {
	for i := 0; i < depth; i++ {
		b.WriteString("  ")
	}
	b.WriteString(e.Kind().String())
	b.WriteByte('@')
	b.WriteString(e.Range().String())

	switch el := e.(type) {
	case *Token:
		b.WriteString(" \"")
		writeEscaped(b, truncateText(el.Text()))
		b.WriteByte('"')
	case *Node:
		for _, c := range el.childValues() {
			b.WriteByte('\n')
			writeElement(b, c.element(), depth+1)
		}
	}
}

// truncateText mirrors rowan's SyntaxToken Debug: text of 25 bytes or more is
// truncated to the first char boundary in the byte range 21..25 followed by
// " ..."; shorter text is returned unchanged. The result is then {:?}-escaped
// by the caller.
func truncateText(s string) string {
	if len(s) < 25 {
		return s
	}
	for idx := 21; idx < 25; idx++ {
		if isCharBoundary(s, idx) {
			return s[:idx] + " ..."
		}
	}
	return s // unreachable in practice
}

// isCharBoundary reports whether idx lies on a UTF-8 rune boundary of s,
// matching Rust's str::is_char_boundary.
func isCharBoundary(s string, idx int) bool {
	if idx == 0 || idx == len(s) {
		return true
	}
	if idx < 0 || idx > len(s) {
		return false
	}
	// A byte is a boundary iff it is not a UTF-8 continuation byte (0b10xxxxxx).
	return s[idx]&0xC0 != 0x80
}

// writeEscaped writes s with Rust's `impl Debug for str` ({:?}) escaping, so
// DebugTree output matches ludtwig/rowan's debug_tree byte-for-byte.
//
// Rust's str Debug maps each char via char::escape_debug_ext (with
// escape_grapheme_extended=true, escape_double_quote=true):
//   - '\0' '\t' '\r' '\n' '\\' '"'    -> the two-char named escapes
//   - Grapheme_Extend chars           -> \u{hex}   (always escaped)
//   - printable chars                 -> verbatim
//   - everything else                 -> \u{hex}
//
// The \u{...} form uses lowercase hex with no leading zeros. "Printable" follows
// Rust's core::unicode::printable::is_printable, approximated here with Go's
// unicode tables (Graphic, minus the space separators Zs other than U+0020).
//
// NOTE(port): Go 1.26 and Rust 1.96 ship different Unicode versions, so a
// handful of exotic combining marks in ranges whose Grapheme_Extend/category
// changed between Unicode versions may classify differently. Those characters
// never appear in ludtwig's extracted snapshots (all ASCII / common Latin), and
// every C0/C1 control, DEL, NBSP, soft hyphen, BOM, zero-width space, and
// line/paragraph separator — the characters that realistically show up in
// templates — matches Rust exactly (verified differentially against the
// compiled reference).
func writeEscaped(b *strings.Builder, s string) {
	for _, r := range s {
		switch r {
		case '\x00':
			b.WriteString("\\0")
		case '\t':
			b.WriteString("\\t")
		case '\r':
			b.WriteString("\\r")
		case '\n':
			b.WriteString("\\n")
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		default:
			if r == utf8.RuneError {
				// A decode error means an invalid UTF-8 byte. Rust &str can never
				// contain one, but our input is a Go string; escape defensively.
				b.WriteString("\\u{fffd}")
			} else if isGraphemeExtended(r) || !isPrintableRust(r) {
				b.WriteString("\\u{")
				b.WriteString(strconv.FormatInt(int64(r), 16))
				b.WriteString("}")
			} else {
				b.WriteRune(r)
			}
		}
	}
}

// isGraphemeExtended approximates Rust's char::is_grapheme_extended
// (Unicode property Grapheme_Extend=Yes): nonspacing/enclosing marks plus the
// Other_Grapheme_Extend set.
func isGraphemeExtended(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) ||
		unicode.Is(unicode.Other_Grapheme_Extend, r)
}

// isPrintableRust approximates Rust's core::unicode::printable::is_printable:
// a character prints verbatim in {:?} unless it is a control/format/private-use/
// unassigned/separator character. Rust treats only U+0020 as a printable space;
// every other space separator (Zs) and all line/paragraph separators escape.
func isPrintableRust(r rune) bool {
	if r == ' ' {
		return true
	}
	if !unicode.IsGraphic(r) {
		return false
	}
	if unicode.Is(unicode.Zs, r) {
		return false
	}
	return true
}
