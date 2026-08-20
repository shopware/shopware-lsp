//go:build !goexperiment.simd || (!amd64 && !arm64)

package bytescan

func IndexByte(source string, start int, needle byte) int {
	return scalarIndexByte(source, start, needle)
}

func IndexAny2(source string, start int, first, second byte) int {
	return scalarIndexAny2(source, start, first, second)
}

func IndexAny3(source string, start int, first, second, third byte) int {
	return scalarIndexAny3(source, start, first, second, third)
}

func IndexAny4(source string, start int, first, second, third, fourth byte) int {
	return scalarIndexAny4(source, start, first, second, third, fourth)
}

func IndexByteOrLessThan(source string, start int, needle, limit byte) int {
	return scalarIndexByteOrLessThan(source, start, needle, limit)
}

func IndexNonASCII(source string, start int) int {
	return scalarIndexNonASCII(source, start)
}
