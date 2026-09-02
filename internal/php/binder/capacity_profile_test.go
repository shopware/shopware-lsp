//go:build integration

package binder

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/inference"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/stretchr/testify/require"
)

// TestShopwareTrunkBinderCapacityProfile reports how closely the binder's
// initial slice estimates match a production PHP corpus. It deliberately has
// no timing assertions and stays behind the integration build tag.
func TestShopwareTrunkBinderCapacityProfile(t *testing.T) {
	root := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		root = filepath.Join(home, "Developer", "sw-trunk")
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("real-world checkout not found at %s", root)
	}

	var documents, sourceBytes int64
	var symbolCount, symbolCapacity int64
	var referenceCount, referenceCapacity int64
	var initialSymbolCapacity, initialReferenceCapacity int64
	var symbolGrowths, referenceGrowths int64
	var typeFactCount int64
	var initialTypeFactCapacity, typeFactGrowths int64
	var literalNodeCount int64
	var scopeCount, scopeSymbolCount int64
	var emptyScopes, oneSymbolScopes, twoSymbolScopes int64
	var threeToFourSymbolScopes, fiveToEightSymbolScopes, largeScopes int64
	var typeFactProfile semantic.TypeFactProfileStats
	var sideProfile symbolSideProfile
	symbolCandidates := []symbolCapacityCandidate{
		{label: "90%", numerator: 9, denominator: 10},
		{
			label:       "90% when >=512",
			numerator:   9,
			denominator: 10,
			scaleAbove:  512,
		},
		{
			label:       "90% when >=1024",
			numerator:   9,
			denominator: 10,
			scaleAbove:  1024,
		},
		{
			label:       "90% when >=2048",
			numerator:   9,
			denominator: 10,
			scaleAbove:  2048,
		},
		{label: "80%", numerator: 4, denominator: 5},
		{label: "75%", numerator: 3, denominator: 4},
	}
	referenceCandidates := []referenceCapacityCandidate{
		{label: "90%", numerator: 9, denominator: 10},
		{
			label:       "90% keep cap",
			numerator:   9,
			denominator: 10,
			keepAbove:   maxEstimatedReferences,
		},
		{
			label:       "90% keep >=2048",
			numerator:   9,
			denominator: 10,
			keepAbove:   2048,
		},
		{
			label:       "90% keep >=1024",
			numerator:   9,
			denominator: 10,
			keepAbove:   1024,
		},
		{
			label:       "90% keep >=512",
			numerator:   9,
			denominator: 10,
			keepAbove:   512,
		},
		{label: "80%", numerator: 4, denominator: 5},
		{label: "75%", numerator: 3, denominator: 4},
	}
	typeFactCandidates := []typeFactCapacityCandidate{
		{label: "48 B/fact", bytesPerFact: 48},
		{label: "64 B/fact", bytesPerFact: 64},
		{label: "80 B/fact", bytesPerFact: 80},
		{label: "96 B/fact", bytesPerFact: 96},
	}
	semanticBinder := New()
	err := filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".php") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		root := phpparser.ParseBytes(content).Tree.Root
		initialSymbols, initialReferences, literalNodes, _ :=
			estimatedDocumentCapacities(root)
		document := semanticBinder.Bind(path, 1, root)
		snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
		document = inference.New(snapshot).AnalyzeOwned(document, root)
		document = inference.LinkMembersOwned(document, snapshot, root)
		sideProfile.add(document.Symbols)
		for _, scope := range document.Scopes {
			symbols := int64(0)
			for range scope.AllSymbolIDs(document.Symbols) {
				symbols++
			}
			scopeCount++
			scopeSymbolCount += symbols
			switch {
			case symbols == 0:
				emptyScopes++
			case symbols == 1:
				oneSymbolScopes++
			case symbols == 2:
				twoSymbolScopes++
			case symbols <= 4:
				threeToFourSymbolScopes++
			case symbols <= 8:
				fiveToEightSymbolScopes++
			default:
				largeScopes++
			}
		}
		typeFacts := document.TypeFactCount()
		currentTypeFactProfile := semantic.ProfileTypeFacts(document)
		typeFactProfile.Total += currentTypeFactProfile.Total
		typeFactProfile.Detailed += currentTypeFactProfile.Detailed
		typeFactProfile.Common += currentTypeFactProfile.Common
		typeFactProfile.Declared += currentTypeFactProfile.Declared
		typeFactProfile.InferredAssignment +=
			currentTypeFactProfile.InferredAssignment
		typeFactProfile.InferredLiteral += currentTypeFactProfile.InferredLiteral
		typeFactProfile.InferredSignature +=
			currentTypeFactProfile.InferredSignature
		typeFactProfile.InferredFlow += currentTypeFactProfile.InferredFlow
		typeFactProfile.Other += currentTypeFactProfile.Other
		initialTypeFacts := estimatedNonLiteralTypeFactCapacity(
			root.Range().Len(),
			literalNodes,
		)
		documents++
		sourceBytes += int64(len(content))
		initialSymbolCapacity += int64(initialSymbols)
		initialReferenceCapacity += int64(initialReferences)
		symbolCount += int64(len(document.Symbols))
		symbolCapacity += int64(cap(document.Symbols))
		for index := range symbolCandidates {
			candidate := &symbolCandidates[index]
			capacity := scaledStructuralSymbolCapacity(
				root,
				candidate.numerator,
				candidate.denominator,
				candidate.scaleAbove,
			)
			candidate.capacity += int64(capacity)
			if capacity < len(document.Symbols) {
				candidate.growths++
				candidate.shortfall += int64(
					len(document.Symbols) - capacity,
				)
			}
		}
		referenceCount += int64(len(document.References))
		referenceCapacity += int64(cap(document.References))
		for index := range referenceCandidates {
			candidate := &referenceCandidates[index]
			capacity := scaledReferenceCapacity(
				root,
				candidate.numerator,
				candidate.denominator,
				candidate.keepAbove,
			)
			candidate.capacity += int64(capacity)
			if capacity < len(document.References) {
				candidate.growths++
				candidate.shortfall += int64(
					len(document.References) - capacity,
				)
			}
		}
		if cap(document.Symbols) > initialSymbols {
			symbolGrowths++
		}
		if cap(document.References) > initialReferences {
			referenceGrowths++
		}
		typeFactCount += int64(typeFacts)
		initialTypeFactCapacity += int64(initialTypeFacts)
		literalNodeCount += int64(literalNodes)
		if initialTypeFacts < typeFacts {
			typeFactGrowths++
		}
		for index := range typeFactCandidates {
			candidate := &typeFactCandidates[index]
			capacity := estimatedTypeFactCapacityForBytes(
				root.Range().Len(),
				candidate.bytesPerFact,
			)
			candidate.capacity += int64(capacity)
			if capacity < typeFacts {
				candidate.growths++
				candidate.shortfall += int64(typeFacts - capacity)
			}
		}
		return nil
	})
	require.NoError(t, err)

	t.Logf(
		"binder capacity profile: documents=%d source=%s "+
			"symbols=%d/%d initial=%d growths=%d (%.1f%%, %s reserved) "+
			"references=%d/%d initial=%d growths=%d (%.1f%%, %s reserved) "+
			"type_facts=%d/%d growths=%d (%.1f%%)",
		documents,
		formatProfileBytes(uint64(sourceBytes)),
		symbolCount,
		symbolCapacity,
		initialSymbolCapacity,
		symbolGrowths,
		profileUtilization(symbolCount, symbolCapacity),
		formatProfileBytes(
			uint64(symbolCapacity)*uint64(unsafe.Sizeof(semantic.Symbol{})),
		),
		referenceCount,
		referenceCapacity,
		initialReferenceCapacity,
		referenceGrowths,
		profileUtilization(referenceCount, referenceCapacity),
		formatProfileBytes(
			uint64(referenceCapacity)*uint64(unsafe.Sizeof(semantic.Reference{})),
		),
		typeFactCount,
		initialTypeFactCapacity,
		typeFactGrowths,
		profileUtilization(typeFactCount, initialTypeFactCapacity),
	)
	for _, candidate := range symbolCandidates {
		t.Logf(
			"symbol capacity candidate %s: initial=%d growths=%d "+
				"shortfall=%d (%.1f%% utilization, %s initially reserved)",
			candidate.label,
			candidate.capacity,
			candidate.growths,
			candidate.shortfall,
			profileUtilization(symbolCount, candidate.capacity),
			formatProfileBytes(
				uint64(candidate.capacity)*
					uint64(unsafe.Sizeof(semantic.Symbol{})),
			),
		)
	}
	for _, candidate := range referenceCandidates {
		t.Logf(
			"reference capacity candidate %s: initial=%d growths=%d "+
				"shortfall=%d (%.1f%% utilization, %s initially reserved)",
			candidate.label,
			candidate.capacity,
			candidate.growths,
			candidate.shortfall,
			profileUtilization(referenceCount, candidate.capacity),
			formatProfileBytes(
				uint64(candidate.capacity)*
					uint64(unsafe.Sizeof(semantic.Reference{})),
			),
		)
	}
	for _, candidate := range typeFactCandidates {
		t.Logf(
			"type-fact capacity candidate %s: initial=%d growths=%d "+
				"shortfall=%d (%.1f%% utilization)",
			candidate.label,
			candidate.capacity,
			candidate.growths,
			candidate.shortfall,
			profileUtilization(typeFactCount, candidate.capacity),
		)
	}
	t.Logf(
		"type-fact profile: total=%d detailed=%d common=%d (%.1f%%) "+
			"declared=%d assignment=%d literal=%d/%d syntax "+
			"signature=%d flow=%d other=%d",
		typeFactProfile.Total,
		typeFactProfile.Detailed,
		typeFactProfile.Common,
		profileUtilization(
			int64(typeFactProfile.Common),
			int64(typeFactProfile.Total),
		),
		typeFactProfile.Declared,
		typeFactProfile.InferredAssignment,
		typeFactProfile.InferredLiteral,
		literalNodeCount,
		typeFactProfile.InferredSignature,
		typeFactProfile.InferredFlow,
		typeFactProfile.Other,
	)
	t.Logf(
		"scope symbol profile: scopes=%d symbols=%d empty=%d one=%d two=%d "+
			"three_to_four=%d five_to_eight=%d over_eight=%d",
		scopeCount,
		scopeSymbolCount,
		emptyScopes,
		oneSymbolScopes,
		twoSymbolScopes,
		threeToFourSymbolScopes,
		fiveToEightSymbolScopes,
		largeScopes,
	)
	t.Logf(
		"symbol side profile: signatures=%d parameters=%d extras=%d "+
			"extras_multi=%d extras_over_4095=%d max_templates=%d "+
			"max_throws=%d max_assertions=%d max_literal_returns=%d "+
			"max_constant_returns=%d hierarchies=%d hierarchy_multi=%d "+
			"hierarchy_types=%d hierarchy_aliases=%d max_extends=%d "+
			"max_implements=%d max_traits=%d max_extends_types=%d "+
			"max_implements_types=%d max_trait_types=%d max_aliases=%d",
		sideProfile.signatures,
		sideProfile.parameters,
		sideProfile.signatureExtras,
		sideProfile.signatureExtrasMulti,
		sideProfile.signatureExtrasOver4095,
		sideProfile.maxTemplates,
		sideProfile.maxThrows,
		sideProfile.maxAssertions,
		sideProfile.maxLiteralReturns,
		sideProfile.maxConstantReturns,
		sideProfile.hierarchies,
		sideProfile.hierarchyMulti,
		sideProfile.hierarchyTypes,
		sideProfile.hierarchyAliases,
		sideProfile.maxExtends,
		sideProfile.maxImplements,
		sideProfile.maxTraits,
		sideProfile.maxExtendsTypes,
		sideProfile.maxImplementsTypes,
		sideProfile.maxTraitTypes,
		sideProfile.maxAliases,
	)
}

type symbolSideProfile struct {
	signatures              int64
	parameters              int64
	signatureExtras         int64
	signatureExtrasMulti    int64
	signatureExtrasOver4095 int64
	maxTemplates            int
	maxThrows               int
	maxAssertions           int
	maxLiteralReturns       int
	maxConstantReturns      int
	hierarchies             int64
	hierarchyMulti          int64
	hierarchyTypes          int64
	hierarchyAliases        int64
	maxExtends              int
	maxImplements           int
	maxTraits               int
	maxExtendsTypes         int
	maxImplementsTypes      int
	maxTraitTypes           int
	maxAliases              int
}

func (profile *symbolSideProfile) add(symbols []semantic.Symbol) {
	for index := range symbols {
		symbol := &symbols[index]
		if len(symbol.Parameters) != 0 {
			profile.signatures++
			profile.parameters += int64(len(symbol.Parameters))
		}
		signatureLengths := [...]int{
			len(symbol.Templates()),
			len(symbol.Throws()),
			len(symbol.Assertions()),
			len(symbol.LiteralReturns()),
			len(symbol.ConstantReturns()),
		}
		signatureGroups := nonEmptyLengthCount(signatureLengths[:])
		if signatureGroups != 0 {
			profile.signatureExtras++
		}
		if signatureGroups > 1 {
			profile.signatureExtrasMulti++
		}
		for _, length := range signatureLengths {
			if length > 4095 {
				profile.signatureExtrasOver4095++
				break
			}
		}
		profile.maxTemplates = max(profile.maxTemplates, signatureLengths[0])
		profile.maxThrows = max(profile.maxThrows, signatureLengths[1])
		profile.maxAssertions = max(profile.maxAssertions, signatureLengths[2])
		profile.maxLiteralReturns = max(
			profile.maxLiteralReturns,
			signatureLengths[3],
		)
		profile.maxConstantReturns = max(
			profile.maxConstantReturns,
			signatureLengths[4],
		)

		hierarchyLengths := [...]int{
			len(symbol.Extends()),
			len(symbol.Implements()),
			len(symbol.Traits()),
			len(symbol.ExtendsTypes()),
			len(symbol.ImplementsTypes()),
			len(symbol.TraitTypes()),
			len(symbol.TraitAliases()),
		}
		hierarchyGroups := nonEmptyLengthCount(hierarchyLengths[:])
		if hierarchyGroups != 0 {
			profile.hierarchies++
		}
		if hierarchyGroups > 1 {
			profile.hierarchyMulti++
		}
		if hierarchyLengths[3] != 0 ||
			hierarchyLengths[4] != 0 ||
			hierarchyLengths[5] != 0 {
			profile.hierarchyTypes++
		}
		if hierarchyLengths[6] != 0 {
			profile.hierarchyAliases++
		}
		profile.maxExtends = max(profile.maxExtends, hierarchyLengths[0])
		profile.maxImplements = max(profile.maxImplements, hierarchyLengths[1])
		profile.maxTraits = max(profile.maxTraits, hierarchyLengths[2])
		profile.maxExtendsTypes = max(
			profile.maxExtendsTypes,
			hierarchyLengths[3],
		)
		profile.maxImplementsTypes = max(
			profile.maxImplementsTypes,
			hierarchyLengths[4],
		)
		profile.maxTraitTypes = max(
			profile.maxTraitTypes,
			hierarchyLengths[5],
		)
		profile.maxAliases = max(profile.maxAliases, hierarchyLengths[6])
	}
}

func nonEmptyLengthCount(lengths []int) int {
	count := 0
	for _, length := range lengths {
		if length != 0 {
			count++
		}
	}
	return count
}

type symbolCapacityCandidate struct {
	label       string
	numerator   int
	denominator int
	capacity    int64
	growths     int64
	shortfall   int64
	scaleAbove  int
}

type referenceCapacityCandidate struct {
	label       string
	numerator   int
	denominator int
	capacity    int64
	growths     int64
	shortfall   int64
	keepAbove   int
}

type typeFactCapacityCandidate struct {
	label        string
	bytesPerFact int
	capacity     int64
	growths      int64
	shortfall    int64
}

func scaledStructuralSymbolCapacity(
	root *phpsyntax.Node,
	numerator,
	denominator,
	scaleAbove int,
) int {
	if root == nil {
		return 0
	}
	capacity := estimatedSymbolCapacity(root.Range().Len())
	structural, _, _, _, _ := structuralDocumentCounts(root)
	if structural > 0 {
		structural++
	}
	const maxStructuralSymbols = 4096
	structural = min(structural, maxStructuralSymbols)
	if scaleAbove > 0 && structural < scaleAbove {
		return max(capacity, structural)
	}
	structural = (structural*numerator + denominator - 1) / denominator
	return max(capacity, structural)
}

func scaledReferenceCapacity(
	root *phpsyntax.Node,
	numerator,
	denominator int,
	keepAbove int,
) int {
	if root == nil {
		return 0
	}
	_, referenceNodes, memberNodes, _, _ :=
		structuralDocumentCounts(root)
	capacity := estimatedLinkedReferenceCapacity(referenceNodes, memberNodes)
	if keepAbove > 0 && capacity >= keepAbove {
		return capacity
	}
	return (capacity*numerator + denominator - 1) / denominator
}

func profileUtilization(length, capacity int64) float64 {
	if capacity == 0 {
		return 100
	}
	return float64(length) * 100 / float64(capacity)
}

func formatProfileBytes(value uint64) string {
	const mebibyte = 1 << 20
	return strings.TrimRight(strings.TrimRight(
		strconv.FormatFloat(float64(value)/mebibyte, 'f', 1, 64),
		"0",
	), ".") + " MiB"
}
