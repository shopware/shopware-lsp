package binder

import (
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/stretchr/testify/require"
)

func TestDeclarationSetTracksExactNodeIdentitiesAcrossGrowth(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function transform($input) {
    $first = $input;
    $second = $first;
    $third = $second;
    $fourth = $third;
    $fifth = $fourth;
    return [$first, $second, $third, $fourth, $fifth];
}
`).Tree.Root
	nodes := descendantNodes(root)
	require.Greater(t, len(nodes), 16)

	set := newDeclarationSet(1)
	added := make(map[semantic.NodeID]struct{})
	for index, node := range nodes {
		if index%2 != 0 {
			continue
		}
		set.Add(node)
		added[semantic.NodeIdentity(node)] = struct{}{}
	}

	for _, node := range nodes {
		_, expected := added[semantic.NodeIdentity(node)]
		require.Equal(t, expected, set.Contains(node), semantic.NodeIdentity(node))
	}
	require.Greater(t, len(set.slots), declarationSetCapacity(1))
}

func TestDeclarationSetDeduplicatesSemanticIdentity(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse("<?php function run($value) { return $value; }").Tree.Root
	nodes := descendantNodes(root)
	require.NotEmpty(t, nodes)

	set := newDeclarationSet(len(nodes))
	set.Add(nodes[0])
	set.Add(nodes[0])

	require.Equal(t, 1, set.count)
	require.True(t, set.Contains(nodes[0]))
	require.False(t, set.Contains(nil))
}

func TestDeclarationIdentityHashIncludesKindAndFullRange(t *testing.T) {
	t.Parallel()
	base := semantic.NodeID{Kind: 7, Start: 1 << 28, End: 1<<28 + 17}
	identities := []semantic.NodeID{
		base,
		{Kind: base.Kind + 1, Start: base.Start, End: base.End},
		{Kind: base.Kind, Start: base.Start + 1, End: base.End},
		{Kind: base.Kind, Start: base.Start, End: base.End + 1},
	}
	hashes := make(map[uint64]struct{}, len(identities))
	for _, identity := range identities {
		hashes[declarationIdentityHash(identity)] = struct{}{}
	}
	require.Len(t, hashes, len(identities))
}

func descendantNodes(root *phpsyntax.Node) []*phpsyntax.Node {
	if root == nil {
		return nil
	}
	nodes := []*phpsyntax.Node{root}
	for index := 0; index < root.ChildCount(); index++ {
		child, ok := root.Child(index).(*phpsyntax.Node)
		if ok {
			nodes = append(nodes, descendantNodes(child)...)
		}
	}
	return nodes
}
