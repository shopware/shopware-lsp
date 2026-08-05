package twig

import (
	"strings"
)

func compareFold(left, right string) int {
	if isASCII(left) && isASCII(right) {
		limit := min(len(left), len(right))
		for index := 0; index < limit; index++ {
			leftByte := lowerASCII(left[index])
			rightByte := lowerASCII(right[index])
			if leftByte < rightByte {
				return -1
			}
			if leftByte > rightByte {
				return 1
			}
		}
		switch {
		case len(left) < len(right):
			return -1
		case len(left) > len(right):
			return 1
		default:
			return 0
		}
	}
	return strings.Compare(strings.ToLower(left), strings.ToLower(right))
}

func isASCII(value string) bool {
	for index := range len(value) {
		if value[index] >= 0x80 {
			return false
		}
	}
	return true
}

func lowerASCII(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}
