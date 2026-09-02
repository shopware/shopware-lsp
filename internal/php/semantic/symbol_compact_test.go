package semantic

import (
	"runtime"
	"testing"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

func TestSymbolCompactSideStorageFitsTargetSize(t *testing.T) {
	var symbol Symbol
	require.LessOrEqual(t, unsafe.Sizeof(symbol), uintptr(272))
}

func TestSymbolSignatureExtrasCompactStoragePaths(t *testing.T) {
	var symbol Symbol
	require.Nil(t, symbol.Templates())
	require.Nil(t, symbol.Throws())
	require.Nil(t, symbol.Assertions())
	require.Nil(t, symbol.LiteralReturns())
	require.Nil(t, symbol.ConstantReturns())

	templates := []TemplateParameter{{Name: "T"}}
	allocations := testing.AllocsPerRun(1000, func() {
		var candidate Symbol
		candidate.SetTemplates(templates)
		runtime.KeepAlive(candidate)
	})
	require.Zero(t, allocations)

	symbol.SetTemplates(templates)
	require.Zero(t, symbol.signatureExtras.lengths&symbolSignatureMultiFlag)
	require.Zero(t, symbol.signatureExtras.lengths&symbolSignatureFullFlag)
	require.Equal(t, templates, symbol.Templates())

	copied := symbol
	copied.SetThrows([]types.Type{types.Named("RuntimeException")})
	require.Zero(t, symbol.signatureExtras.lengths&symbolSignatureMultiFlag)
	require.NotZero(t, copied.signatureExtras.lengths&symbolSignatureMultiFlag)
	require.Nil(t, symbol.Throws())
	require.Equal(t, "RuntimeException", copied.Throws()[0].String())

	large := make([]TemplateParameter, symbolSignatureLengthMask+1)
	large[len(large)-1].Name = "Last"
	symbol.SetTemplates(large)
	require.NotZero(t, symbol.signatureExtras.lengths&symbolSignatureFullFlag)
	require.Len(t, symbol.Templates(), len(large))
	require.Equal(t, "Last", symbol.Templates()[len(large)-1].Name)
}

func TestSymbolHierarchyCompactStorageAndCopyOnWrite(t *testing.T) {
	var symbol Symbol
	require.Nil(t, symbol.Extends())
	require.Nil(t, symbol.Implements())
	require.Nil(t, symbol.Traits())
	require.Nil(t, symbol.ExtendsTypes())
	require.Nil(t, symbol.ImplementsTypes())
	require.Nil(t, symbol.TraitTypes())
	require.Nil(t, symbol.TraitAliases())

	extends := []string{"App\\Base"}
	allocations := testing.AllocsPerRun(1000, func() {
		var candidate Symbol
		candidate.SetExtends(extends)
		runtime.KeepAlive(candidate)
	})
	require.Zero(t, allocations)

	symbol.SetHierarchy(
		extends,
		[]string{"App\\Contract"},
		[]string{"App\\Reusable"},
		[]types.Type{types.Named("App\\Base")},
		[]types.Type{types.Named("App\\Contract")},
		[]types.Type{types.Named("App\\Reusable")},
		[]TraitAlias{{Trait: "App\\Reusable", Method: "run", Alias: "execute"}},
	)
	require.Equal(t, extends, symbol.Extends())
	require.Equal(t, []string{"App\\Contract"}, symbol.Implements())
	require.Equal(t, []string{"App\\Reusable"}, symbol.Traits())
	require.Equal(t, "App\\Base", symbol.ExtendsTypes()[0].String())
	require.Equal(t, "App\\Contract", symbol.ImplementsTypes()[0].String())
	require.Equal(t, "App\\Reusable", symbol.TraitTypes()[0].String())
	require.Equal(t, "execute", symbol.TraitAliases()[0].Alias)

	copied := symbol
	copied.SetExtends([]string{"App\\Other"})
	require.Equal(t, "App\\Base", symbol.Extends()[0])
	require.Equal(t, "App\\Other", copied.Extends()[0])
	require.Equal(t, symbol.Implements(), copied.Implements())
}
