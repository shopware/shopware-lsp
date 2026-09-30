package dataset

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRootPrefersPublishPathAndCamelCaseSuffix(t *testing.T) {
	definitions := orderDefinitions()
	entity, found := ResolveRoot(
		definitions,
		"sw-product-detail__product",
		"product.manufacturer",
	)
	require.True(t, found)
	assert.Equal(t, "product_manufacturer", entity)

	entity, found = ResolveRoot(definitions, "sw-sales-channel-detail__salesChannel", "")
	require.True(t, found)
	assert.Equal(t, "sales_channel", entity)

	_, found = ResolveRoot(definitions, "sw-dashboard-detail__todayOrderData", "todayOrderData")
	assert.False(t, found)
}

func TestRequiredEntitiesWalksAssociationsAndStopsOnPlainValues(t *testing.T) {
	definitions := orderDefinitions()
	entities, resolved := RequiredEntities(definitions, "order", []string{"language.name"})
	require.True(t, resolved)
	assert.Equal(t, []string{"language", "order"}, entities)

	entities, resolved = RequiredEntities(definitions, "order", []string{"orderNumber"})
	require.True(t, resolved)
	assert.Equal(t, []string{"order"}, entities)

	entities, resolved = RequiredEntities(
		definitions,
		"order",
		[]string{"deliveries.*.shippingMethod.name", "deliveries.[0].shippingMethod.name"},
	)
	require.True(t, resolved)
	assert.Equal(t, []string{"order", "order_delivery", "shipping_method"}, entities)

	entities, resolved = RequiredEntities(definitions, "order", []string{"language"})
	require.True(t, resolved)
	assert.Equal(t, []string{"language", "order"}, entities)

	entities, resolved = RequiredEntities(definitions, "order", []string{"customFields.foo"})
	require.True(t, resolved)
	assert.Empty(t, entities)

	entities, resolved = RequiredEntities(definitions, "order", []string{"missing.association"})
	assert.False(t, resolved)
	assert.Empty(t, entities)
}

func orderDefinitions() []dal.Definition {
	return []dal.Definition{
		{
			Name: "order", Class: "OrderDefinition",
			Fields: []dal.Field{
				{Name: "orderNumber", Type: "StringField"},
				{Name: "customFields", Type: "JsonField"},
				{
					Name: "language", Type: "ManyToOneAssociationField",
					Association: true, TargetClass: "LanguageDefinition",
				},
				{
					Name: "deliveries", Type: "OneToManyAssociationField",
					Association: true, TargetClass: `Shopware\Core\Checkout\Order\Aggregate\OrderDelivery\OrderDeliveryDefinition`,
				},
			},
		},
		{
			Name: "language", Class: "LanguageDefinition",
			Fields: []dal.Field{{Name: "name", Type: "StringField"}},
		},
		{
			Name: "order_delivery", Class: "OrderDeliveryDefinition",
			FullyQualifiedClass: `Shopware\Core\Checkout\Order\Aggregate\OrderDelivery\OrderDeliveryDefinition`,
			Fields: []dal.Field{{
				Name: "shippingMethod", Type: "ManyToOneAssociationField",
				Association: true, TargetClass: "ShippingMethodDefinition",
			}},
		},
		{
			Name: "shipping_method", Class: "ShippingMethodDefinition",
			Fields: []dal.Field{{Name: "name", Type: "StringField"}},
		},
		{
			Name: "product", Class: "ProductDefinition",
			Fields: []dal.Field{{
				Name: "manufacturer", Type: "ManyToOneAssociationField",
				Association: true, TargetClass: "ProductManufacturerDefinition",
			}},
		},
		{
			Name: "product_manufacturer", Class: "ProductManufacturerDefinition",
			Fields: []dal.Field{{Name: "name", Type: "StringField"}},
		},
		{Name: "sales_channel", Class: "SalesChannelDefinition"},
	}
}
