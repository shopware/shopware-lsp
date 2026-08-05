package diagnostics

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/stretchr/testify/require"
)

func TestShopwareCriteriaAnalyzerOnlyOffersSafeLocalRewrite(t *testing.T) {
	document := lsp.NewTextDocument("file:///Criteria.php", `<?php
function load(string $id): void {
    $criteria = new Criteria();
    $criteria->addFilter(new EqualsFilter('id', $id));
}
`, 1)
	problems, err := NewShopwareCriteriaAnalyzer().Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	payload := problems[0].Payload.(map[string]any)
	require.Equal(t, true, payload["safe"])
	require.Equal(t, "$id", payload["argument"])
}

func TestShopwareCriteriaAnalyzerDoesNotOfferAmbiguousRewrite(t *testing.T) {
	document := lsp.NewTextDocument("file:///Criteria.php", `<?php
function load(string $id): void {
    $first = new Criteria();
    $second = new Criteria();
    consume(new EqualsFilter('id', $id));
}
`, 1)
	problems, err := NewShopwareCriteriaAnalyzer().Analyze(
		context.Background(),
		document,
	)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	payload := problems[0].Payload.(map[string]any)
	require.Equal(t, false, payload["safe"])
}
