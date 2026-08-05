package language

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/stretchr/testify/require"
)

func TestRegistryResolvesExtensionsAndParses(t *testing.T) {
	registry, err := NewRegistry(Definition{
		ID:         "test",
		Extensions: []string{"TEST"},
		Parse: func(source string) ParseResult {
			builder := cst.NewBuilder(source)
			builder.StartNode(1)
			builder.Token(2, cst.TextRange{Start: 0, End: uint32(len(source))})
			builder.FinishNode()
			return ParseResult{Tree: builder.Finish()}
		},
	})
	require.NoError(t, err)

	definition, ok := registry.ForPath("/project/example.TeSt")
	require.True(t, ok)
	require.Equal(t, ID("test"), definition.ID)

	id, result, ok := registry.ParsePath("/project/example.test", "source")
	require.True(t, ok)
	require.Equal(t, ID("test"), id)
	require.Equal(t, "source", result.Tree.Root.Text())
	require.Equal(t, []string{".test"}, registry.Extensions())
}

func TestRegistryRejectsDuplicateExtensions(t *testing.T) {
	_, err := NewRegistry(
		Definition{ID: "one", Extensions: []string{".same"}, Parse: func(string) ParseResult { return ParseResult{} }},
		Definition{ID: "two", Extensions: []string{"same"}, Parse: func(string) ParseResult { return ParseResult{} }},
	)
	require.ErrorContains(t, err, "belongs to both")
}
