package twig

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompareFoldMatchesLowercaseOrdering(t *testing.T) {
	t.Parallel()

	values := []string{
		"",
		"Product",
		"product",
		"ProductDetail",
		"product_list",
		"Änderung",
		"änderung",
	}
	for _, left := range values {
		for _, right := range values {
			require.Equal(
				t,
				strings.Compare(
					strings.ToLower(left),
					strings.ToLower(right),
				),
				compareFold(left, right),
				left+" / "+right,
			)
		}
	}
}

func TestCompareFoldASCIIIsAllocationFree(t *testing.T) {
	var comparison int
	allocations := testing.AllocsPerRun(100, func() {
		comparison = compareFold("ProductDetail", "product_list")
	})
	require.Zero(t, allocations)
	require.Positive(t, comparison)
}
