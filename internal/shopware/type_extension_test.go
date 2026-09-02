package shopware

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/php/inference"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

func TestShopwareTypeExtension(t *testing.T) {
	t.Parallel()
	extension := NewPHPTypeExtension()
	entityCollectionID := semantic.SymbolID("entity-collection")
	paymentCollectionID := semantic.SymbolID("payment-collection")
	entityCollection := semantic.Symbol{
		ID:             entityCollectionID,
		Kind:           semantic.ClassSymbol,
		Name:           "EntityCollection",
		FullyQualified: entityCollectionType.Name(),
		Path:           "/collections.php",
	}
	entityCollection.SetTemplates([]semantic.TemplateParameter{{
		Name: "TElement",
	}})
	paymentCollection := semantic.Symbol{
		ID:             paymentCollectionID,
		Kind:           semantic.ClassSymbol,
		Name:           "PaymentMethodCollection",
		FullyQualified: "PaymentMethodCollection",
		Path:           "/collections.php",
	}
	paymentCollection.SetHierarchy(
		[]string{entityCollectionType.Name()}, nil, nil,
		[]types.Type{types.Named(
			entityCollectionType.Name(),
			types.Named("PaymentMethodEntity"),
		)},
		nil, nil, nil,
	)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path:    "/collections.php",
		Symbols: []semantic.Symbol{entityCollection, paymentCollection},
	}})

	fact, ok := extension.InferCall(inference.CallContext{
		Snapshot: snapshot,
		Name:     "getString",
		Receiver: types.Named("Shopware\\Core\\System\\SystemConfig\\SystemConfigService"),
	})
	require.True(t, ok)
	require.Equal(t, "string", fact.Type.String())

	fact, ok = extension.InferCall(inference.CallContext{
		Snapshot: snapshot,
		Name:     "first",
		Receiver: types.Named(
			"Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityCollection",
			types.Named("ProductEntity"),
		),
	})
	require.True(t, ok)
	require.Equal(t, "ProductEntity|null", fact.Type.String())

	fact, ok = extension.InferCall(inference.CallContext{
		Snapshot: snapshot,
		Name:     "get",
		Receiver: types.Named(entityCollectionType.Name()),
	})
	require.True(t, ok)
	require.Equal(t, entityType.Name()+"|null", fact.Type.String())

	fact, ok = extension.InferCall(inference.CallContext{
		Snapshot: snapshot,
		Name:     "first",
		Receiver: types.Named("PaymentMethodCollection"),
	})
	require.True(t, ok)
	require.Equal(t, "PaymentMethodEntity|null", fact.Type.String())

	fact, ok = extension.InferCall(inference.CallContext{
		Snapshot: snapshot,
		Name:     "first",
		Receiver: types.Named(
			entitySearchResultType.Name(),
			types.Named("PaymentMethodCollection"),
		),
	})
	require.True(t, ok)
	require.Equal(t, "PaymentMethodEntity|null", fact.Type.String())

	fact, ok = extension.InferCall(inference.CallContext{
		Snapshot: snapshot,
		Name:     "get",
		Receiver: types.Union(
			types.Named("PaymentMethodCollection"),
			types.Named(
				entityCollectionType.Name(),
				types.Named("ProductEntity"),
			),
		),
	})
	require.True(t, ok)
	require.Equal(
		t,
		"PaymentMethodEntity|ProductEntity|null",
		fact.Type.String(),
	)
}

func TestShopwareMethodClassificationDoesNotAllocate(t *testing.T) {
	var method string
	allocations := testing.AllocsPerRun(100, func() {
		method = canonicalShopwareMethod("GetElements")
	})
	require.Zero(t, allocations)
	require.Equal(t, "getelements", method)
}
