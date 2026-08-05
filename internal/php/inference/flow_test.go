package inference

import (
	"testing"

	"github.com/stretchr/testify/require"
)

var normalizedFlowExpressionSink string

func TestNormalizeFlowExpression(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"$customer->getId()",
		normalizeFlowExpression(" \n$customer ?-> getId()\t"),
	)
	require.Equal(
		t,
		"$items['key']",
		normalizeFlowExpression("$items['key']"),
	)
}

func TestNormalizeCompactFlowExpressionDoesNotAllocate(t *testing.T) {
	allocations := testing.AllocsPerRun(100, func() {
		normalizedFlowExpressionSink = normalizeFlowExpression(
			" \n\t$customer->getId()\r\n",
		)
	})
	require.Zero(t, allocations)
}
