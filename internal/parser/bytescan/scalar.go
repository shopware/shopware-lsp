package bytescan

func scalarIndexByte(source string, start int, needle byte) int {
	for position := start; position < len(source); position++ {
		if source[position] == needle {
			return position
		}
	}
	return len(source)
}

func scalarIndexAny2(source string, start int, first, second byte) int {
	for position := start; position < len(source); position++ {
		value := source[position]
		if value == first || value == second {
			return position
		}
	}
	return len(source)
}

func scalarIndexAny3(source string, start int, first, second, third byte) int {
	for position := start; position < len(source); position++ {
		value := source[position]
		if value == first || value == second || value == third {
			return position
		}
	}
	return len(source)
}

func scalarIndexAny4(source string, start int, first, second, third, fourth byte) int {
	for position := start; position < len(source); position++ {
		value := source[position]
		if value == first || value == second || value == third || value == fourth {
			return position
		}
	}
	return len(source)
}

func scalarIndexByteOrLessThan(source string, start int, needle, limit byte) int {
	for position := start; position < len(source); position++ {
		value := source[position]
		if value == needle || value < limit {
			return position
		}
	}
	return len(source)
}

func scalarIndexNonASCII(source string, start int) int {
	for position := start; position < len(source); position++ {
		if source[position] >= 0x80 {
			return position
		}
	}
	return len(source)
}
