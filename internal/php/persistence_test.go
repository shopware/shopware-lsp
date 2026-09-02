package php

import (
	"bytes"
	stdflate "compress/flate"
	"compress/zlib"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

func TestSemanticCompressorPoolCanBeCleared(t *testing.T) {
	pool := newSemanticCompressorPool()
	compressor := pool.get()
	pool.put(compressor)
	require.Len(t, pool.compressors, 1)

	pool.clear()
	require.Empty(t, pool.compressors)
}

func TestSemanticDecompressorPoolCanBeReusedAndCleared(t *testing.T) {
	pool := newSemanticDecompressorPool()
	decompressor := pool.get()
	require.NoError(t, decompressor.reset([]byte{0x03, 0x00}))
	pool.put(decompressor)

	require.Zero(t, decompressor.input.Len())
	require.Len(t, pool.decompressors, 1)
	reused := pool.get()
	require.Same(t, decompressor, reused)
	pool.put(reused)

	pool.clear()
	require.Empty(t, pool.decompressors)
}

func TestPersistedWorkspaceGraphRoundTrip(t *testing.T) {
	symbol := semantic.Symbol{
		ID:             "product",
		Kind:           semantic.ClassSymbol,
		Name:           "Product",
		FullyQualified: "App\\Product",
		Path:           "/project/Product.php",
		Type:           types.Unknown(),
		NativeType:     types.Unknown(),
		DocType:        types.Unknown(),
		ReturnType:     types.Unknown(),
	}
	symbol.SetExtends([]string{"App\\Entity"})
	symbol.SetTemplates([]semantic.TemplateParameter{{
		Name:    "T",
		Bound:   types.Named("App\\Entity"),
		Default: types.Unknown(),
	}})
	document := (&semantic.Document{
		Path:      "/project/Product.php",
		Namespace: "App",
		Symbols:   []semantic.Symbol{symbol},
	}).WorkspaceGraph()
	graph := semantic.PackWorkspaceGraphOwned(document)

	persisted, err := encodeWorkspaceGraph(graph)
	require.NoError(t, err)
	require.Equal(t, uint8(persistedWorkspaceGraphFormat), persisted.Format)

	decoded, err := persisted.decode()
	require.NoError(t, err)
	decodedDocument := decoded.Document()
	require.Equal(t, document.Path, decodedDocument.Path)
	require.Equal(t, document.Namespace, decodedDocument.Namespace)
	require.Len(t, decodedDocument.Symbols, 1)
	require.Equal(t, document.Symbols[0].ID, decodedDocument.Symbols[0].ID)
	require.Equal(t, document.Symbols[0].Extends(), decodedDocument.Symbols[0].Extends())
	require.Len(t, decodedDocument.Symbols[0].Templates(), 1)
	require.True(t, document.Symbols[0].Templates()[0].Bound.Equal(
		decodedDocument.Symbols[0].Templates()[0].Bound,
	))

	// A later encoding may reuse the compressor itself while the first output
	// buffer remains owned by its payload.
	second, err := encodeWorkspaceGraph(semantic.PackWorkspaceGraphOwned(
		&semantic.Document{Path: "/project/Second.php"},
	))
	require.NoError(t, err)
	require.NotEmpty(t, second.Payload)
	decodedAgain, err := persisted.decode()
	require.NoError(t, err)
	require.Equal(t, document.Path, decodedAgain.Path())

	previousFormat := persisted
	previousFormat.Format = legacyRawWorkspaceGraphFormat
	decodedPrevious, err := previousFormat.decode()
	require.NoError(t, err)
	require.Equal(t, document.Path, decodedPrevious.Path())
}

func TestPersistedWorkspaceGraphUsesStandardRawDeflate(t *testing.T) {
	graph := semantic.PackWorkspaceGraphOwned(&semantic.Document{
		Path: "/project/Portable.php",
	})
	persisted, err := encodeWorkspaceGraph(graph)
	require.NoError(t, err)

	reader := stdflate.NewReader(bytes.NewReader(persisted.Payload))
	raw, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	var decoded semantic.WorkspaceGraph
	require.NoError(t, msgpack.Unmarshal(raw, &decoded))
	require.Equal(t, graph.Path(), decoded.Path())
}

func TestPersistedWorkspaceGraphEnvelopeBorrowsPayload(t *testing.T) {
	original := persistedWorkspaceGraph{
		Format:  persistedWorkspaceGraphFormat,
		Payload: []byte{1, 2, 3, 4},
	}
	encoded, err := msgpack.Marshal(original)
	require.NoError(t, err)

	decoded, err := decodePersistedWorkspaceGraphBorrowed(encoded)
	require.NoError(t, err)
	require.Equal(t, original, decoded)

	decoded.Payload[0] = 99
	var observed persistedWorkspaceGraph
	require.NoError(t, msgpack.Unmarshal(encoded, &observed))
	require.Equal(t, decoded.Payload, observed.Payload)
}

func TestPersistedWorkspaceGraphEnvelopeAcceptsEitherFieldOrder(
	t *testing.T,
) {
	var encoded bytes.Buffer
	encoder := msgpack.NewEncoder(&encoded)
	require.NoError(t, encoder.EncodeMapLen(2))
	require.NoError(t, encoder.EncodeString("payload"))
	require.NoError(t, encoder.EncodeBytes([]byte{1, 2}))
	require.NoError(t, encoder.EncodeString("format"))
	require.NoError(t, encoder.EncodeUint16(persistedWorkspaceGraphFormat))

	decoded, err := decodePersistedWorkspaceGraphBorrowed(encoded.Bytes())
	require.NoError(t, err)
	require.Equal(t, uint8(persistedWorkspaceGraphFormat), decoded.Format)
	require.Equal(t, []byte{1, 2}, decoded.Payload)
}

func TestPersistedWorkspaceGraphEnvelopeRejectsMalformedValues(t *testing.T) {
	testCases := map[string]any{
		"not a map": []any{1, 2},
		"wrong fields": map[string]any{
			"format": persistedWorkspaceGraphFormat,
		},
		"unknown field": map[string]any{
			"format":  persistedWorkspaceGraphFormat,
			"unknown": []byte{1},
		},
		"format overflow": map[string]any{
			"format":  uint16(256),
			"payload": []byte{1},
		},
		"invalid payload": map[string]any{
			"format":  persistedWorkspaceGraphFormat,
			"payload": 1,
		},
	}
	for name, value := range testCases {
		t.Run(name, func(t *testing.T) {
			encoded, err := msgpack.Marshal(value)
			require.NoError(t, err)
			_, err = decodePersistedWorkspaceGraphBorrowed(encoded)
			require.Error(t, err)
		})
	}

	valid, err := msgpack.Marshal(persistedWorkspaceGraph{
		Format:  persistedWorkspaceGraphFormat,
		Payload: []byte{1},
	})
	require.NoError(t, err)
	_, err = decodePersistedWorkspaceGraphBorrowed(append(valid, 0))
	require.ErrorContains(t, err, "trailing bytes")
	_, err = decodePersistedWorkspaceGraphBorrowed(valid[:len(valid)-1])
	require.Error(t, err)
}

func TestPersistedWorkspaceGraphRejectsUnknownFormat(t *testing.T) {
	_, err := (persistedWorkspaceGraph{Format: 99}).decode()
	require.ErrorContains(t, err, "unsupported workspace graph format")
}

func TestPersistedWorkspaceGraphRejectsCorruptPayload(t *testing.T) {
	_, err := (persistedWorkspaceGraph{
		Format:  persistedWorkspaceGraphFormat,
		Payload: []byte("not-deflate"),
	}).decode()
	require.Error(t, err)

	// A failed stream must not poison the reusable reader.
	valid, err := encodeWorkspaceGraph(semantic.PackWorkspaceGraphOwned(
		&semantic.Document{Path: "/project/Valid.php"},
	))
	require.NoError(t, err)
	decoded, err := valid.decode()
	require.NoError(t, err)
	require.Equal(t, "/project/Valid.php", decoded.Path())
}

func TestPersistedWorkspaceGraphDecodesLegacyZlibFormat(t *testing.T) {
	document := (&semantic.Document{
		Path:      "/project/Legacy.php",
		Namespace: "App",
	}).WorkspaceGraph()
	graph := semantic.PackWorkspaceGraphOwned(document)
	persisted := persistedWorkspaceGraph{
		Format:  legacyWorkspaceGraphFormat,
		Payload: encodeLegacyZlibValue(t, graph),
	}

	decoded, err := persisted.decode()
	require.NoError(t, err)
	require.Equal(t, graph.Path(), decoded.Path())
}

func TestPersistedWorkspaceGraphDecodesConcurrently(t *testing.T) {
	graph := semantic.PackWorkspaceGraphOwned(&semantic.Document{
		Path: "/project/Concurrent.php",
	})
	persisted, err := encodeWorkspaceGraph(graph)
	require.NoError(t, err)

	const workerCount = 32
	errors := make(chan error, workerCount)
	var wait sync.WaitGroup
	wait.Add(workerCount)
	for range workerCount {
		go func() {
			defer wait.Done()
			for range 8 {
				decoded, decodeErr := persisted.decode()
				if decodeErr != nil {
					errors <- decodeErr
					return
				}
				if decoded.Path() != graph.Path() {
					errors <- fmt.Errorf(
						"decoded path %q, expected %q",
						decoded.Path(),
						graph.Path(),
					)
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errors)
	for decodeErr := range errors {
		require.NoError(t, decodeErr)
	}
}

func encodeLegacyZlibValue(t *testing.T, value any) []byte {
	t.Helper()

	var output bytes.Buffer
	writer, err := zlib.NewWriterLevel(&output, zlib.BestSpeed)
	require.NoError(t, err)
	require.NoError(t, msgpack.NewEncoder(writer).Encode(value))
	require.NoError(t, writer.Close())
	return output.Bytes()
}
