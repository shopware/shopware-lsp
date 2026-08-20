package cst

import (
	"fmt"
	"strings"
	"testing"
)

var benchmarkLineIndex *LineIndex
var benchmarkLine, benchmarkColumn, benchmarkOffset uint32

func BenchmarkNewLineIndex(b *testing.B) {
	benchmarkNewLineIndex(b, NewLineIndex)
}

func BenchmarkNewLineIndexDirect(b *testing.B) {
	benchmarkNewLineIndex(b, newLineIndexDirect)
}

func benchmarkNewLineIndex(b *testing.B, build func(string) *LineIndex) {
	b.Helper()
	for _, width := range []int{16, 40, 80, 256, 4096} {
		b.Run(fmt.Sprintf("width_%d", width), func(b *testing.B) {
			source := strings.Repeat(strings.Repeat("x", width)+"\n", 256)
			b.SetBytes(int64(len(source)))
			for b.Loop() {
				benchmarkLineIndex = build(source)
			}
		})
	}
}

func newLineIndexDirect(src string) *LineIndex {
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
		if src[position] == '\n' {
			starts = append(starts, uint32(position+1))
		}
	}
	return &LineIndex{source: src, lineStarts: starts}
}

func BenchmarkPositionUTF16ASCII(b *testing.B) {
	for _, width := range []int{16, 40, 80, 256, 4096} {
		b.Run(fmt.Sprintf("width_%d", width), func(b *testing.B) {
			source := strings.Repeat("x", width)
			lineIndex := NewLineIndex(source)
			b.SetBytes(int64(len(source)))
			for b.Loop() {
				benchmarkLine, benchmarkColumn = lineIndex.PositionUTF16(uint32(len(source)))
			}
		})
	}
}

func BenchmarkOffsetUTF16ASCII(b *testing.B) {
	for _, width := range []int{16, 40, 80, 256, 4096} {
		b.Run(fmt.Sprintf("width_%d", width), func(b *testing.B) {
			source := strings.Repeat("x", width)
			lineIndex := NewLineIndex(source)
			b.SetBytes(int64(len(source)))
			for b.Loop() {
				benchmarkOffset = lineIndex.OffsetUTF16(0, uint32(len(source)))
			}
		})
	}
}

func TestLineIndexBasic(t *testing.T) {
	li := NewLineIndex("abc\ndef\nghi")
	cases := []struct {
		off               uint32
		wantLine, wantCol uint32
	}{
		{0, 0, 0},  // 'a'
		{2, 0, 2},  // 'c'
		{3, 0, 3},  // '\n' at end of line 0
		{4, 1, 0},  // 'd'
		{7, 1, 3},  // '\n' at end of line 1
		{8, 2, 0},  // 'g'
		{10, 2, 2}, // 'i'
	}
	for _, c := range cases {
		l, col := li.Position(c.off)
		if l != c.wantLine || col != c.wantCol {
			t.Errorf("Position(%d) = (%d,%d) want (%d,%d)", c.off, l, col, c.wantLine, c.wantCol)
		}
	}
}

func TestLineIndexBoundsEstimatedCapacity(t *testing.T) {
	const longSingleLineBytes = 1 << 20
	li := NewLineIndex(string(make([]byte, longSingleLineBytes)))
	if cap(li.lineStarts) != 256 {
		t.Fatalf(
			"line starts cap = %d, want bounded estimate 256",
			cap(li.lineStarts),
		)
	}
}

func TestLineIndexOffsetAtEOF(t *testing.T) {
	src := "ab\ncd"
	li := NewLineIndex(src)
	// EOF offset (len) clamps to end of last line.
	l, col := li.Position(uint32(len(src)))
	if l != 1 || col != 2 {
		t.Fatalf("Position(EOF) = (%d,%d) want (1,2)", l, col)
	}
	// Past EOF clamps too.
	l, col = li.Position(999)
	if l != 1 || col != 2 {
		t.Fatalf("Position(past EOF) = (%d,%d) want (1,2)", l, col)
	}
}

func TestLineIndexCRLF(t *testing.T) {
	// CRLF: the '\n' terminates the line; '\r' is a normal column byte.
	src := "ab\r\ncd"
	li := NewLineIndex(src)
	// offset 2 -> '\r' at col 2 on line 0.
	if l, c := li.Position(2); l != 0 || c != 2 {
		t.Fatalf("Position(2) = (%d,%d) want (0,2)", l, c)
	}
	// offset 3 -> '\n' still on line 0 at col 3.
	if l, c := li.Position(3); l != 0 || c != 3 {
		t.Fatalf("Position(3) = (%d,%d) want (0,3)", l, c)
	}
	// offset 4 -> 'c' on line 1 col 0.
	if l, c := li.Position(4); l != 1 || c != 0 {
		t.Fatalf("Position(4) = (%d,%d) want (1,0)", l, c)
	}
}

func TestLineIndexMultiByteUTF8(t *testing.T) {
	// "é" is 2 bytes (U+00E9), "𝔸" is 4 bytes (U+1D538, a surrogate pair in UTF-16).
	src := "aébc𝔸d"
	li := NewLineIndex(src)
	// Byte columns.
	// bytes: a(0) é(1,2) b(3) c(4) 𝔸(5..9) d(9)
	if l, c := li.Position(3); l != 0 || c != 3 {
		t.Fatalf("Position(3) byte col = (%d,%d) want (0,3)", l, c)
	}
	// UTF-16 columns: a=1 unit, é=1 unit, so at byte offset 3 -> 2 units.
	if l, c := li.PositionUTF16(3); l != 0 || c != 2 {
		t.Fatalf("PositionUTF16(3) = (%d,%d) want (0,2)", l, c)
	}
	// At byte offset 9 (after 𝔸): a,é,b,c = 4 units, 𝔸 = 2 units -> 6.
	if l, c := li.PositionUTF16(9); l != 0 || c != 6 {
		t.Fatalf("PositionUTF16(9) = (%d,%d) want (0,6)", l, c)
	}
}

func TestLineIndexOffset(t *testing.T) {
	src := "abc\ndef\nghi"
	li := NewLineIndex(src)
	if got := li.Offset(0, 0); got != 0 {
		t.Fatalf("Offset(0,0) = %d", got)
	}
	if got := li.Offset(1, 0); got != 4 {
		t.Fatalf("Offset(1,0) = %d want 4", got)
	}
	if got := li.Offset(2, 2); got != 10 {
		t.Fatalf("Offset(2,2) = %d want 10", got)
	}
	// Column past end of line clamps to line end (before the '\n').
	if got := li.Offset(0, 99); got != 4 {
		t.Fatalf("Offset(0,99) = %d want 4 (line end incl newline)", got)
	}
	// Column past end of last line clamps to EOF.
	if got := li.Offset(2, 99); got != uint32(len(src)) {
		t.Fatalf("Offset(2,99) = %d want %d", got, len(src))
	}
	// Line past end clamps to EOF.
	if got := li.Offset(99, 0); got != uint32(len(src)) {
		t.Fatalf("Offset(99,0) = %d want %d", got, len(src))
	}
}

func TestLineIndexRoundTrip(t *testing.T) {
	src := "hello\nworld\n\nfoo"
	li := NewLineIndex(src)
	for off := uint32(0); off <= uint32(len(src)); off++ {
		l, c := li.Position(off)
		if got := li.Offset(l, c); got != off {
			t.Fatalf("round trip offset %d -> (%d,%d) -> %d", off, l, c, got)
		}
	}
}

func TestLineIndexEmpty(t *testing.T) {
	li := NewLineIndex("")
	if l, c := li.Position(0); l != 0 || c != 0 {
		t.Fatalf("empty Position(0) = (%d,%d)", l, c)
	}
	if got := li.Offset(0, 0); got != 0 {
		t.Fatalf("empty Offset(0,0) = %d", got)
	}
}

// TestPositionUTF16Monotonic asserts PositionUTF16 is monotonic across every
// byte offset of a string containing a 4-byte rune, and never exceeds the
// rune's end column. Regression for decoding the offset-truncated slice, which
// counted each byte of a straddled multi-byte rune as a UTF-16 unit.
func TestPositionUTF16Monotonic(t *testing.T) {
	src := "\U0001F600x" // emoji (4 bytes, 2 UTF-16 units) + 'x'
	li := NewLineIndex(src)
	var prev uint32
	for off := uint32(0); off <= uint32(len(src)); off++ {
		_, col := li.PositionUTF16(off)
		if col < prev {
			t.Fatalf("PositionUTF16 not monotonic: offset %d col %d < prev %d", off, col, prev)
		}
		prev = col
	}
	// A mid-rune offset must clamp to the rune's start column (0), never exceed
	// the rune's end column (2).
	if _, col := li.PositionUTF16(3); col != 0 {
		t.Fatalf("PositionUTF16(3) mid-rune = %d, want 0 (clamped to rune start)", col)
	}
	if _, col := li.PositionUTF16(4); col != 2 {
		t.Fatalf("PositionUTF16(4) = %d, want 2 (after 4-byte rune)", col)
	}
}

// TestOffsetOverflow asserts a near-max column on a non-first line clamps to the
// line's end rather than wrapping to an offset before the line. Regression for
// `off := start + col` overflowing uint32.
func TestOffsetOverflow(t *testing.T) {
	li := NewLineIndex("abc\ndef")
	if got := li.Offset(1, 0xFFFFFFFD); got != 7 {
		t.Fatalf("Offset(1, 0xFFFFFFFD) = %d, want 7 (line 1 end)", got)
	}
	li3 := NewLineIndex("abc\ndef\nghi")
	if got := li3.Offset(2, 0xFFFFFFFF); got != 11 {
		t.Fatalf("Offset(2, 0xFFFFFFFF) = %d, want 11 (line 2 end)", got)
	}
}

func TestOffsetUTF16(t *testing.T) {
	// "a𝔘b" on line 0 (𝔘 = U+1D518, 4 bytes, 2 UTF-16 units), "héllo" on line 1.
	src := "a\U0001D518b\nhéllo"
	li := NewLineIndex(src)

	cases := []struct {
		line, col16 uint32
		want        uint32
	}{
		{0, 0, 0},                 // start
		{0, 1, 1},                 // after 'a'
		{0, 3, 5},                 // after the surrogate pair
		{0, 4, 6},                 // after 'b'
		{0, 2, 5},                 // col inside the pair advances past it
		{0, 99, 7},                // clamp to line end (incl. newline)
		{1, 2, 10},                // after 'h' + 'é' (2-byte rune, 1 unit)
		{99, 0, uint32(len(src))}, // line clamp
	}
	for _, c := range cases {
		if got := li.OffsetUTF16(c.line, c.col16); got != c.want {
			t.Errorf("OffsetUTF16(%d,%d) = %d, want %d", c.line, c.col16, got, c.want)
		}
	}

	// Round-trip: every byte offset that is a rune start maps back to itself.
	for off := uint32(0); off <= uint32(len(src)); off++ {
		l, c := li.PositionUTF16(off)
		back := li.OffsetUTF16(l, c)
		lb, cb := li.PositionUTF16(back)
		if lb != l || cb != c {
			t.Errorf("round-trip broken at %d: pos=(%d,%d) back=%d pos2=(%d,%d)", off, l, c, back, lb, cb)
		}
	}
}

func TestLineEndAndUTF16Length(t *testing.T) {
	const source = "plain\r\nemoji 😀\nlast"
	lineIndex := NewLineIndex(source)

	if got := lineIndex.LineEnd(0); got != uint32(len("plain")) {
		t.Fatalf("LineEnd(0) = %d, want %d", got, len("plain"))
	}
	if got := lineIndex.LineUTF16Length(0); got != 5 {
		t.Fatalf("LineUTF16Length(0) = %d, want 5", got)
	}
	if got := lineIndex.LineUTF16Length(1); got != 8 {
		t.Fatalf("LineUTF16Length(1) = %d, want 8", got)
	}
	if got := lineIndex.LineEnd(99); got != uint32(len(source)) {
		t.Fatalf("LineEnd(out of range) = %d, want EOF %d", got, len(source))
	}
}
