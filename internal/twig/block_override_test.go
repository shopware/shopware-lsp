package twig

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedundantBlockOverrideRequiresExactResolvedParent(t *testing.T) {
	block := TwigBlock{Hash: "same", Text: "{% block content %}same{% endblock %}"}
	parent := TwigBlockHash{
		Hash:         block.Hash,
		Text:         block.Text,
		AbsolutePath: "/project/parent.html.twig",
	}
	resolution := UpstreamResolution{
		Candidates:     []TwigBlockHash{parent},
		ChainPaths:     []string{parent.AbsolutePath},
		ParentResolved: true,
	}

	resolved, redundant := RedundantBlockOverride(block, resolution)
	require.True(t, redundant)
	assert.Equal(t, parent, resolved)

	resolution.ParentResolved = false
	_, redundant = RedundantBlockOverride(block, resolution)
	assert.False(t, redundant)

	resolution.ParentResolved = true
	resolution.ChainPaths = []string{"/project/different.html.twig"}
	_, redundant = RedundantBlockOverride(block, resolution)
	assert.False(t, redundant)

	resolution.ChainPaths = []string{parent.AbsolutePath}
	resolution.Candidates[0].Text = "{% block content %}different{% endblock %}"
	_, redundant = RedundantBlockOverride(block, resolution)
	assert.False(t, redundant)
}

func TestParseBlockBodyPreservesExactWhitespaceAndRange(t *testing.T) {
	block := "{% block content %}\n    <div>same</div>\n{% endblock %}"
	body, rng, ok := ParseBlockBody(block)
	require.True(t, ok)
	assert.Equal(t, "\n    <div>same</div>", body)
	assert.Equal(t, body, block[rng.Start:rng.End])
	assert.Equal(t, cst.TextRange{Start: 19, End: 39}, rng)
	assert.False(t, IsParentDelegation(body))
	assert.True(t, IsParentDelegation("\n  {{ parent() }}\n"))
}
