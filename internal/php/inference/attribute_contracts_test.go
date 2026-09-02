package inference

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

func attributeContractMethodSnapshot() *semantic.Snapshot {
	classID := semantic.SymbolID("service")
	terminate := semantic.Symbol{
		ID: "terminate", Kind: semantic.MethodSymbol, Name: "terminate",
		FullyQualified: "Service::terminate", Container: classID,
		Path: "/service.php",
	}
	terminate.SetAttributes([]semantic.Attribute{{
		Name: "JetBrains\\PhpStorm\\NoReturn",
	}})
	return semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/service.php",
		Symbols: []semantic.Symbol{
			{
				ID:             classID,
				Kind:           semantic.ClassSymbol,
				Name:           "Service",
				FullyQualified: "Service",
				Path:           "/service.php",
			},
			terminate,
		},
	}})
}

func TestAttributeContractsMethod(t *testing.T) {
	t.Parallel()
	fact, matched := AttributeContracts.InferCall(CallContext{
		Snapshot: attributeContractMethodSnapshot(),
		Receiver: types.Named("Service"),
		Name:     "terminate",
	})
	require.True(t, matched)
	require.Equal(t, types.Never(), fact.Type)
}

func BenchmarkAttributeContractsMethod(b *testing.B) {
	context := CallContext{
		Snapshot: attributeContractMethodSnapshot(),
		Receiver: types.Named("Service"),
		Name:     "terminate",
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, matched := AttributeContracts.InferCall(context); !matched {
			b.Fatal("NoReturn contract did not match")
		}
	}
}
