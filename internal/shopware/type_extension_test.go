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
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/collections.php",
		Symbols: []semantic.Symbol{
			{
				ID:             entityCollectionID,
				Kind:           semantic.ClassSymbol,
				Name:           "EntityCollection",
				FullyQualified: entityCollectionType.Name(),
				Templates: []semantic.TemplateParameter{{
					Name: "TElement",
				}},
				Path: "/collections.php",
			},
			{
				ID:             paymentCollectionID,
				Kind:           semantic.ClassSymbol,
				Name:           "PaymentMethodCollection",
				FullyQualified: "PaymentMethodCollection",
				Extends:        []string{entityCollectionType.Name()},
				ExtendsTypes: []types.Type{types.Named(
					entityCollectionType.Name(),
					types.Named("PaymentMethodEntity"),
				)},
				Path: "/collections.php",
			},
		},
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
