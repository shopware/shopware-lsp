package textutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

var compactWhitespaceSink string

func TestCompactWhitespace(t *testing.T) {
	t.Parallel()

	require.Equal(t, "Foo\\Bar", CompactWhitespace(" \tFoo\\\r\nBar "))
	require.Equal(t, "Größe", CompactWhitespace("Grö ße"))
}

func TestCompactWhitespaceDoesNotAllocateForCompactInput(t *testing.T) {
	allocations := testing.AllocsPerRun(1000, func() {
		compactWhitespaceSink = CompactWhitespace("Shopware\\Core\\Product")
	})
	require.Zero(t, allocations)
}
