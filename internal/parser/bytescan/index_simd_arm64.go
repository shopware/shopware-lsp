//go:build goexperiment.simd && arm64

package bytescan

import (
	"simd/archsimd"
	"unsafe"
)

const arm64VectorWidth = 16

func IndexByte(source string, start int, needle byte) int {
	if len(source)-start < arm64VectorWidth {
		return scalarIndexByte(source, start, needle)
	}

	data := unsafe.Slice(unsafe.StringData(source), len(source))
	want := archsimd.BroadcastUint8x16(needle)
	position := start
	for ; position+arm64VectorWidth <= len(source); position += arm64VectorWidth {
		chunk := archsimd.LoadUint8x16(data[position:])
		if maskHasMatch(chunk.Equal(want)) {
			return scalarIndexByte(source, position, needle)
		}
	}
	return scalarIndexByte(source, position, needle)
}

func IndexAny2(source string, start int, first, second byte) int {
	if len(source)-start < arm64VectorWidth {
		return scalarIndexAny2(source, start, first, second)
	}

	data := unsafe.Slice(unsafe.StringData(source), len(source))
	firstVector := archsimd.BroadcastUint8x16(first)
	secondVector := archsimd.BroadcastUint8x16(second)
	position := start
	for ; position+arm64VectorWidth <= len(source); position += arm64VectorWidth {
		chunk := archsimd.LoadUint8x16(data[position:])
		if maskHasMatch(chunk.Equal(firstVector).Or(chunk.Equal(secondVector))) {
			return scalarIndexAny2(source, position, first, second)
		}
	}
	return scalarIndexAny2(source, position, first, second)
}

func IndexAny3(source string, start int, first, second, third byte) int {
	if len(source)-start < arm64VectorWidth {
		return scalarIndexAny3(source, start, first, second, third)
	}

	data := unsafe.Slice(unsafe.StringData(source), len(source))
	firstVector := archsimd.BroadcastUint8x16(first)
	secondVector := archsimd.BroadcastUint8x16(second)
	thirdVector := archsimd.BroadcastUint8x16(third)
	position := start
	for ; position+arm64VectorWidth <= len(source); position += arm64VectorWidth {
		chunk := archsimd.LoadUint8x16(data[position:])
		matches := chunk.Equal(firstVector).
			Or(chunk.Equal(secondVector)).
			Or(chunk.Equal(thirdVector))
		if maskHasMatch(matches) {
			return scalarIndexAny3(source, position, first, second, third)
		}
	}
	return scalarIndexAny3(source, position, first, second, third)
}

func IndexAny4(source string, start int, first, second, third, fourth byte) int {
	if len(source)-start < arm64VectorWidth {
		return scalarIndexAny4(source, start, first, second, third, fourth)
	}

	data := unsafe.Slice(unsafe.StringData(source), len(source))
	firstVector := archsimd.BroadcastUint8x16(first)
	secondVector := archsimd.BroadcastUint8x16(second)
	thirdVector := archsimd.BroadcastUint8x16(third)
	fourthVector := archsimd.BroadcastUint8x16(fourth)
	position := start
	for ; position+arm64VectorWidth <= len(source); position += arm64VectorWidth {
		chunk := archsimd.LoadUint8x16(data[position:])
		matches := chunk.Equal(firstVector).
			Or(chunk.Equal(secondVector)).
			Or(chunk.Equal(thirdVector)).
			Or(chunk.Equal(fourthVector))
		if maskHasMatch(matches) {
			return scalarIndexAny4(source, position, first, second, third, fourth)
		}
	}
	return scalarIndexAny4(source, position, first, second, third, fourth)
}

func IndexByteOrLessThan(source string, start int, needle, limit byte) int {
	if len(source)-start < arm64VectorWidth {
		return scalarIndexByteOrLessThan(source, start, needle, limit)
	}

	data := unsafe.Slice(unsafe.StringData(source), len(source))
	needleVector := archsimd.BroadcastUint8x16(needle)
	limitVector := archsimd.BroadcastUint8x16(limit)
	position := start
	for ; position+arm64VectorWidth <= len(source); position += arm64VectorWidth {
		chunk := archsimd.LoadUint8x16(data[position:])
		if maskHasMatch(chunk.Equal(needleVector).Or(chunk.Less(limitVector))) {
			return scalarIndexByteOrLessThan(source, position, needle, limit)
		}
	}
	return scalarIndexByteOrLessThan(source, position, needle, limit)
}

func IndexNonASCII(source string, start int) int {
	if len(source)-start < arm64VectorWidth {
		return scalarIndexNonASCII(source, start)
	}

	data := unsafe.Slice(unsafe.StringData(source), len(source))
	zero := archsimd.BroadcastInt8x16(0)
	position := start
	for ; position+arm64VectorWidth <= len(source); position += arm64VectorWidth {
		chunk := archsimd.LoadUint8x16(data[position:])
		if maskHasMatch(chunk.BitsToInt8().Less(zero)) {
			return scalarIndexNonASCII(source, position)
		}
	}
	return scalarIndexNonASCII(source, position)
}

func maskHasMatch(mask archsimd.Mask8x16) bool {
	return mask.ToInt8x16().ToBits().ReduceMax() != 0
}
