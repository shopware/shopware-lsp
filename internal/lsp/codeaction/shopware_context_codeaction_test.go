package codeaction

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminContextActionsExposeComponentAndMethodGenerators(t *testing.T) {
	provider := NewAdminContextProvider()
	source := `Shopware.Component.register('sw-product-list', {
    methods: {
        loadProducts(criteria, context) {
            return [];
        },
    },
});`
	componentActions := provider.GetCodeActions(
		context.Background(),
		symfonyGeneratorCodeActionRequest(
			"file:///project/Resources/app/administration/src/product.js",
			source,
			"sw-product-list",
		),
	)
	require.Len(t, componentActions, 1)
	require.NotNil(t, componentActions[0].Command)
	assert.Equal(t, extendAdminComponentAction, componentActions[0].Command.Command)

	methodActions := provider.GetCodeActions(
		context.Background(),
		symfonyGeneratorCodeActionRequest(
			"file:///project/Resources/app/administration/src/product.js",
			source,
			"loadProducts",
		),
	)
	require.Len(t, methodActions, 1)
	require.NotNil(t, methodActions[0].Command)
	assert.Equal(t, overrideAdminMethodAction, methodActions[0].Command.Command)
	assert.Equal(t, "sw-product-list", methodActions[0].Command.Arguments[0])
	assert.Equal(t, "loadProducts", methodActions[0].Command.Arguments[1])
	assert.Equal(t, "methods", methodActions[0].Command.Arguments[2])
	assert.Equal(t, "criteria,context", methodActions[0].Command.Arguments[3])
}

func TestEventListenerContextActionRecognizesEventSubclass(t *testing.T) {
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Event.php",
		[]byte(`<?php namespace Symfony\Contracts\EventDispatcher; class Event {}`),
	)))
	source := `<?php
namespace Shopware\Core\Content\Product;
use Symfony\Contracts\EventDispatcher\Event;
class ProductLoadedEvent extends Event {}
`
	actions := NewEventListenerContextProvider(phpIndex).GetCodeActions(
		context.Background(),
		symfonyGeneratorCodeActionRequest(
			"file:///project/src/ProductLoadedEvent.php",
			source,
			"ProductLoadedEvent",
		),
	)
	require.Len(t, actions, 1)
	require.NotNil(t, actions[0].Command)
	assert.Equal(t, createEventListenerAction, actions[0].Command.Command)
	assert.Equal(
		t,
		"Shopware\\Core\\Content\\Product\\ProductLoadedEvent",
		actions[0].Command.Arguments[0],
	)
}
