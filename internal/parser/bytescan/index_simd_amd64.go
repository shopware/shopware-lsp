//go:build goexperiment.simd && amd64

package bytescan

import (
	"math/bits"
	"simd/archsimd"
	"unsafe"
)

const vectorWidth = 32

func IndexByte(source string, start int, needle byte) int {
	if !archsimd.X86.AVX2() || len(source)-start < vectorWidth {
		return scalarIndexByte(source, start, needle)
	}

	data := unsafe.Slice(unsafe.StringData(source), len(source))
	want := archsimd.BroadcastUint8x32(needle)
	position := start
	for ; position+vectorWidth <= len(source); position += vectorWidth {
		chunk := archsimd.LoadUint8x32(data[position:])
		if matches := chunk.Equal(want).ToBits(); matches != 0 {
			return position + bits.TrailingZeros32(matches)
		}
	}
	return scalarIndexByte(source, position, needle)
}

func IndexAny2(source string, start int, first, second byte) int {
	if !archsimd.X86.AVX2() || len(source)-start < vectorWidth {
		return scalarIndexAny2(source, start, first, second)
	}

	data := unsafe.Slice(unsafe.StringData(source), len(source))
	firstVector := archsimd.BroadcastUint8x32(first)
	secondVector := archsimd.BroadcastUint8x32(second)
	position := start
	for ; position+vectorWidth <= len(source); position += vectorWidth {
		chunk := archsimd.LoadUint8x32(data[position:])
		matches := chunk.Equal(firstVector).Or(chunk.Equal(secondVector)).ToBits()
		if matches != 0 {
			return position + bits.TrailingZeros32(matches)
		}
	}
	return scalarIndexAny2(source, position, first, second)
}

func IndexAny3(source string, start int, first, second, third byte) int {
	if !archsimd.X86.AVX2() || len(source)-start < vectorWidth {
		return scalarIndexAny3(source, start, first, second, third)
	}

	data := unsafe.Slice(unsafe.StringData(source), len(source))
	firstVector := archsimd.BroadcastUint8x32(first)
	secondVector := archsimd.BroadcastUint8x32(second)
	thirdVector := archsimd.BroadcastUint8x32(third)
	position := start
	for ; position+vectorWidth <= len(source); position += vectorWidth {
		chunk := archsimd.LoadUint8x32(data[position:])
		matches := chunk.Equal(firstVector).
			Or(chunk.Equal(secondVector)).
			Or(chunk.Equal(thirdVector)).
			ToBits()
		if matches != 0 {
			return position + bits.TrailingZeros32(matches)
		}
	}
	return scalarIndexAny3(source, position, first, second, third)
}

func IndexAny4(source string, start int, first, second, third, fourth byte) int {
	if !archsimd.X86.AVX2() || len(source)-start < vectorWidth {
		return scalarIndexAny4(source, start, first, second, third, fourth)
	}

	data := unsafe.Slice(unsafe.StringData(source), len(source))
	firstVector := archsimd.BroadcastUint8x32(first)
	secondVector := archsimd.BroadcastUint8x32(second)
	thirdVector := archsimd.BroadcastUint8x32(third)
	fourthVector := archsimd.BroadcastUint8x32(fourth)
	position := start
	for ; position+vectorWidth <= len(source); position += vectorWidth {
		chunk := archsimd.LoadUint8x32(data[position:])
		matches := chunk.Equal(firstVector).
			Or(chunk.Equal(secondVector)).
			Or(chunk.Equal(thirdVector)).
			Or(chunk.Equal(fourthVector)).
			ToBits()
		if matches != 0 {
			return position + bits.TrailingZeros32(matches)
		}
	}
	return scalarIndexAny4(source, position, first, second, third, fourth)
}

func IndexByteOrLessThan(source string, start int, needle, limit byte) int {
	if !archsimd.X86.AVX2() || len(source)-start < vectorWidth {
		return scalarIndexByteOrLessThan(source, start, needle, limit)
	}

	data := unsafe.Slice(unsafe.StringData(source), len(source))
	needleVector := archsimd.BroadcastUint8x32(needle)
	limitVector := archsimd.BroadcastUint8x32(limit)
	position := start
	for ; position+vectorWidth <= len(source); position += vectorWidth {
		chunk := archsimd.LoadUint8x32(data[position:])
		matches := chunk.Equal(needleVector).Or(chunk.Less(limitVector)).ToBits()
		if matches != 0 {
			return position + bits.TrailingZeros32(matches)
		}
	}
	return scalarIndexByteOrLessThan(source, position, needle, limit)
}

func IndexNonASCII(source string, start int) int {
	if !archsimd.X86.AVX2() || len(source)-start < vectorWidth {
		return scalarIndexNonASCII(source, start)
	}

	data := unsafe.Slice(unsafe.StringData(source), len(source))
	zero := archsimd.BroadcastInt8x32(0)
	position := start
	for ; position+vectorWidth <= len(source); position += vectorWidth {
		chunk := archsimd.LoadUint8x32(data[position:])
		if matches := chunk.BitsToInt8().Less(zero).ToBits(); matches != 0 {
			return position + bits.TrailingZeros32(matches)
		}
	}
	return scalarIndexNonASCII(source, position)
}
