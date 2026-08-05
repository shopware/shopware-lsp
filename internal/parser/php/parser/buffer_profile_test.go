//go:build integration

package parser

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/parsekit"
	"github.com/stretchr/testify/require"
)

// TestShopwareTrunkParserBufferProfile reports the actual lexer-token and
// parser-event density of a production PHP corpus. It deliberately has no
// timing assertions and stays behind the integration build tag.
func TestShopwareTrunkParserBufferProfile(t *testing.T) {
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
	var tokenCount, tokenReservation int64
	var eventCount, eventReservation int64
	var nodeCount, markerReservation int64
	var tokenGrowthFiles, eventGrowthFiles, markerGrowthFiles int64
	var oversizedTokenFiles, oversizedEventFiles, oversizedMarkerFiles int64
	var maxTokens, maxEvents, maxNodes int
	tokenDensity := make([]float64, 0, 1<<16)
	eventDensity := make([]float64, 0, 1<<16)
	nodeDensity := make([]float64, 0, 1<<16)

	err := filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() ||
			!strings.EqualFold(filepath.Ext(path), ".php") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source := string(content)
		var stats parsekit.BufferStats
		_ = parse(source, func(observed parsekit.BufferStats) {
			stats = observed
		})
		documents++
		sourceBytes += int64(len(source))
		tokenCount += int64(stats.Tokens)
		requestedTokens := len(source)/4 + 1
		tokenReservation += int64(requestedTokens)
		if stats.Tokens > requestedTokens {
			tokenGrowthFiles++
		}
		if stats.TokenCapacity > 1<<16 {
			oversizedTokenFiles++
		}
		maxTokens = max(maxTokens, stats.Tokens)
		eventCount += int64(stats.Events)
		requestedEvents := stats.Tokens * 2
		eventReservation += int64(requestedEvents)
		nodeCount += int64(stats.Nodes)
		requestedMarkers := max(64, stats.Tokens/2)
		markerReservation += int64(requestedMarkers)
		if stats.Events > requestedEvents {
			eventGrowthFiles++
		}
		if stats.EventCapacity > 1<<16 {
			oversizedEventFiles++
		}
		if stats.MarkerCapacity > 1<<14 {
			oversizedMarkerFiles++
		}
		maxEvents = max(maxEvents, stats.Events)
		maxNodes = max(maxNodes, stats.Nodes)
		if stats.Nodes > requestedMarkers {
			markerGrowthFiles++
		}
		if stats.Tokens > 0 {
			tokenDensity = append(
				tokenDensity,
				float64(len(source))/float64(stats.Tokens),
			)
			eventDensity = append(
				eventDensity,
				float64(stats.Events)/float64(stats.Tokens),
			)
			nodeDensity = append(
				nodeDensity,
				float64(stats.Nodes)/float64(stats.Tokens),
			)
		}
		return nil
	})
	require.NoError(t, err)

	sort.Float64s(tokenDensity)
	sort.Float64s(eventDensity)
	sort.Float64s(nodeDensity)
	t.Logf(
		"parser buffer profile: documents=%d source=%s "+
			"tokens=%d/%d (%.1f%%) growth_files=%d "+
			"pool_oversize=%d max=%d "+
			"bytes_per_token[p10=%.2f p50=%.2f p90=%.2f] "+
			"events=%d/%d (%.1f%%) growth_files=%d "+
			"pool_oversize=%d max=%d "+
			"events_per_token[p50=%.2f p90=%.2f p99=%.2f p99.9=%.2f max=%.2f] "+
			"above_2.125=%d "+
			"nodes=%d/%d (%.1f%%) growth_files=%d "+
			"pool_oversize=%d max=%d "+
			"nodes_per_token[p50=%.2f p90=%.2f p99=%.2f p99.9=%.2f max=%.2f] "+
			"above_0.625=%d",
		documents,
		formatProfileBytes(uint64(sourceBytes)),
		tokenCount,
		tokenReservation,
		utilization(tokenCount, tokenReservation),
		tokenGrowthFiles,
		oversizedTokenFiles,
		maxTokens,
		percentile(tokenDensity, 0.10),
		percentile(tokenDensity, 0.50),
		percentile(tokenDensity, 0.90),
		eventCount,
		eventReservation,
		utilization(eventCount, eventReservation),
		eventGrowthFiles,
		oversizedEventFiles,
		maxEvents,
		percentile(eventDensity, 0.50),
		percentile(eventDensity, 0.90),
		percentile(eventDensity, 0.99),
		percentile(eventDensity, 0.999),
		percentile(eventDensity, 1),
		countAbove(eventDensity, 2.125),
		nodeCount,
		markerReservation,
		utilization(nodeCount, markerReservation),
		markerGrowthFiles,
		oversizedMarkerFiles,
		maxNodes,
		percentile(nodeDensity, 0.50),
		percentile(nodeDensity, 0.90),
		percentile(nodeDensity, 0.99),
		percentile(nodeDensity, 0.999),
		percentile(nodeDensity, 1),
		countAbove(nodeDensity, 0.625),
	)
}

func percentile(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values)-1) * quantile)
	return values[index]
}

func countAbove(values []float64, threshold float64) int {
	index := sort.SearchFloat64s(values, threshold)
	for index < len(values) && values[index] == threshold {
		index++
	}
	return len(values) - index
}

func utilization(length, capacity int64) float64 {
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
