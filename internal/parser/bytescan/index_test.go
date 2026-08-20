package bytescan

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIndexScannersMatchScalarReference(t *testing.T) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789\x00\x1f\r\n\\\"<&\x7f\x80\xff"
	state := uint64(0x73787)

	for length := 0; length <= 160; length++ {
		input := make([]byte, length)
		for index := range input {
			state = state*6364136223846793005 + 1442695040888963407
			input[index] = alphabet[state%uint64(len(alphabet))]
		}
		source := string(input)

		for start := 0; start <= len(source); start++ {
			assert.Equal(t, scalarIndexByte(source, start, 'z'), IndexByte(source, start, 'z'))
			assert.Equal(t, scalarIndexAny2(source, start, '\r', '\n'), IndexAny2(source, start, '\r', '\n'))
			assert.Equal(
				t,
				scalarIndexAny3(source, start, '\'', '"', '>'),
				IndexAny3(source, start, '\'', '"', '>'),
			)
			assert.Equal(
				t,
				scalarIndexAny4(source, start, '"', '\\', '\r', '\n'),
				IndexAny4(source, start, '"', '\\', '\r', '\n'),
			)
			assert.Equal(
				t,
				scalarIndexByteOrLessThan(source, start, '\\', 0x20),
				IndexByteOrLessThan(source, start, '\\', 0x20),
			)
			assert.Equal(t, scalarIndexNonASCII(source, start), IndexNonASCII(source, start))
		}
	}
}

var benchmarkPosition int

func BenchmarkIndexAny2(b *testing.B) {
	for _, size := range []int{16, 32, 64, 256, 4096} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			source := strings.Repeat("x", size)
			b.SetBytes(int64(len(source)))
			for b.Loop() {
				benchmarkPosition = IndexAny2(source, 0, '\r', '\n')
			}
		})
	}
}

func BenchmarkIndexAny4(b *testing.B) {
	for _, size := range []int{16, 32, 64, 256, 4096} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			source := strings.Repeat("x", size)
			b.SetBytes(int64(len(source)))
			for b.Loop() {
				benchmarkPosition = IndexAny4(source, 0, '"', '\\', '\r', '\n')
			}
		})
	}
}

func BenchmarkIndexByteOrLessThan(b *testing.B) {
	for _, size := range []int{16, 32, 64, 256, 4096} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			source := strings.Repeat("x", size)
			b.SetBytes(int64(len(source)))
			for b.Loop() {
				benchmarkPosition = IndexByteOrLessThan(source, 0, '\\', 0x20)
			}
		})
	}
}

func BenchmarkIndexNonASCII(b *testing.B) {
	for _, size := range []int{16, 32, 64, 256, 4096} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			source := strings.Repeat("x", size)
			b.SetBytes(int64(len(source)))
			for b.Loop() {
				benchmarkPosition = IndexNonASCII(source, 0)
			}
		})
	}
}

func BenchmarkIndexAny2MatchPosition(b *testing.B) {
	for _, position := range []int{0, 4, 8, 16, 24, 31, 32, 64, 128} {
		b.Run(fmt.Sprint(position), func(b *testing.B) {
			source := strings.Repeat("x", position) + "\n" + strings.Repeat("x", 256-position)
			b.SetBytes(int64(position + 1))
			for b.Loop() {
				benchmarkPosition = IndexAny2(source, 0, '\r', '\n')
			}
		})
	}
}
