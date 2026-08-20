package cst

import (
	"unicode/utf8"

	"github.com/shopware/shopware-lsp/internal/parser/bytescan"
)

// LineIndex maps byte offsets to line/column positions and back. Line and
// column numbers are 0-based. Columns are byte offsets from the start of the
// line unless the UTF-16 variant is used (for LSP).
type LineIndex struct {
	source string
	// lineStarts[i] is the byte offset of the first byte of line i. The first
	// entry is always 0.
	lineStarts []uint32
}

// NewLineIndex builds a LineIndex for src. A line starts after each '\n'; a
// lone '\r' or "\r\n" is treated with the '\n' terminating the line (a bare
// '\r' not followed by '\n' does not start a new line, matching common LSP
// tooling that keys on '\n').
func NewLineIndex(src string) *LineIndex {
	const (
		estimatedBytesPerLine    = 128
		maxEstimatedLineCapacity = 256
	)
	estimatedCapacity := min(
		len(src)/estimatedBytesPerLine+1,
		maxEstimatedLineCapacity,
	)
	starts := make([]uint32, 1, estimatedCapacity)
	for position := 0; position < len(src); position++ {
		position = bytescan.IndexByte(src, position, '\n')
		if position < len(src) {
			starts = append(starts, uint32(position+1))
		}
	}
	return &LineIndex{source: src, lineStarts: starts}
}

// lineAt returns the index of the line containing offset via binary search.
func (li *LineIndex) lineAt(offset uint32) int {
	// Find the last lineStart <= offset.
	lo, hi := 0, len(li.lineStarts)
	for lo < hi {
		mid := (lo + hi) / 2
		if li.lineStarts[mid] <= offset {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo - 1
}

// Position returns the 0-based line and 0-based byte column for offset.
// An offset at or past EOF is clamped to the end of the source.
func (li *LineIndex) Position(offset uint32) (line, col uint32) {
	if int(offset) > len(li.source) {
		offset = uint32(len(li.source))
	}
	l := li.lineAt(offset)
	return uint32(l), offset - li.lineStarts[l]
}

// PositionUTF16 returns the 0-based line and 0-based UTF-16 code-unit column for
// offset (the column encoding used by LSP). An offset at or past EOF is clamped.
func (li *LineIndex) PositionUTF16(offset uint32) (line, col uint32) {
	if int(offset) > len(li.source) {
		offset = uint32(len(li.source))
	}
	l := li.lineAt(offset)
	start := li.lineStarts[l]
	var units uint32
	for i := start; i < offset; {
		asciiEnd := uint32(bytescan.IndexNonASCII(li.source[:offset], int(i)))
		units += asciiEnd - i
		i = asciiEnd
		if i >= offset {
			break
		}

		// Decode against the full remaining source, not the offset-truncated
		// slice: otherwise a multi-byte rune straddling `offset` would decode as
		// repeated (RuneError, size 1) and each of its bytes would be counted as a
		// unit, making the column non-monotonic and larger than the rune's true
		// UTF-16 width. A rune that straddles the bound is clamped to its start.
		r, size := utf8.DecodeRuneInString(li.source[i:])
		if r == utf8.RuneError && size <= 1 {
			// Invalid byte: count as one unit and advance one byte.
			units++
			i++
			continue
		}
		if i+uint32(size) > offset {
			// The rune straddles the requested offset: clamp to its start.
			break
		}
		if r > 0xFFFF {
			units += 2 // surrogate pair
		} else {
			units++
		}
		i += uint32(size)
	}
	return uint32(l), units
}

// LineEnd returns the byte offset immediately after the visible content of a
// line, excluding its trailing LF or CRLF. Out-of-range lines map to EOF.
func (li *LineIndex) LineEnd(line uint32) uint32 {
	if int(line) >= len(li.lineStarts) {
		return uint32(len(li.source))
	}
	var end uint32
	if int(line)+1 < len(li.lineStarts) {
		end = li.lineStarts[line+1] - 1
	} else {
		end = uint32(len(li.source))
	}
	if end > li.lineStarts[line] && li.source[end-1] == '\r' {
		end--
	}
	return end
}

// LineUTF16Length returns the visible length of a line in UTF-16 code units.
func (li *LineIndex) LineUTF16Length(line uint32) uint32 {
	_, column := li.PositionUTF16(li.LineEnd(line))
	return column
}

// Offset returns the byte offset of the given 0-based line and 0-based byte
// column. Out-of-range lines clamp to the end of source; a column past the end
// of its line clamps to the line's end.
func (li *LineIndex) Offset(line, col uint32) uint32 {
	if int(line) >= len(li.lineStarts) {
		return uint32(len(li.source))
	}
	start := li.lineStarts[line]
	// Determine the exclusive end of this line (next line start, or EOF).
	var lineEnd uint32
	if int(line)+1 < len(li.lineStarts) {
		lineEnd = li.lineStarts[line+1]
	} else {
		lineEnd = uint32(len(li.source))
	}
	off := start + col
	// Guard against uint32 overflow: a very large col can wrap past 2^32 and
	// produce an offset before the requested line. Clamp such wraps (off < start)
	// as well as ordinary past-line-end columns to the line's end.
	if off < start || off > lineEnd {
		off = lineEnd
	}
	return off
}

// OffsetUTF16 returns the byte offset for a 0-based line and 0-based UTF-16
// code-unit column — the position encoding LSP clients send. Clamping matches
// Offset: out-of-range lines map to EOF, columns past the line's end map to the
// line's end. A column landing inside a surrogate pair advances past the whole
// character.
func (li *LineIndex) OffsetUTF16(line, col uint32) uint32 {
	if int(line) >= len(li.lineStarts) {
		return uint32(len(li.source))
	}
	start := li.lineStarts[line]
	var lineEnd uint32
	if int(line)+1 < len(li.lineStarts) {
		lineEnd = li.lineStarts[line+1]
	} else {
		lineEnd = uint32(len(li.source))
	}
	i := start
	var units uint32
	for i < lineEnd && units < col {
		asciiEnd := uint32(bytescan.IndexNonASCII(li.source[:lineEnd], int(i)))
		asciiUnits := asciiEnd - i
		remaining := col - units
		if asciiUnits >= remaining {
			return i + remaining
		}
		units += asciiUnits
		i = asciiEnd
		if i >= lineEnd {
			break
		}

		r, size := utf8.DecodeRuneInString(li.source[i:])
		if r == utf8.RuneError && size <= 1 {
			// Invalid byte: count as one unit and advance one byte, mirroring
			// PositionUTF16.
			units++
			i++
			continue
		}
		if r > 0xFFFF {
			units += 2 // surrogate pair
		} else {
			units++
		}
		i += uint32(size)
	}
	return i
}
