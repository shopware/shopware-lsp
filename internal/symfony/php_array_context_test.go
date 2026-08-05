package symfony

import (
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPArrayServiceReferenceAt(t *testing.T) {
	source := `<?php
return [
    'services' => [
        'app.consumer' => [
            'decorates' => 'app.inner',
            'parent' => 'app.parent',
            'class' => 'App\\Consumer',
            'arguments' => [
                '%app.parameter%',
                '@app.logger',
                'App\\Dependency',
                service('app.helper'),
            ],
            'calls' => [['setLogger', ['%app.call_parameter%']]],
            'tags' => ['app.tag', ['name' => 'app.named_tag']],
            'factory' => ['@app.factory', 'create'],
        ],
        'app.alias' => '@app.target',
    ],
];
`
	root := phpparser.Parse(source).Tree.Root
	expected := map[string]PHPConfigReferenceKind{
		"app.inner":            PHPConfigReferenceService,
		"app.parent":           PHPConfigReferenceService,
		"App\\\\Consumer":      PHPConfigReferenceClass,
		"%app.parameter%":      PHPConfigReferenceParameter,
		"@app.logger":          PHPConfigReferenceService,
		"App\\\\Dependency":    PHPConfigReferenceClass,
		"%app.call_parameter%": PHPConfigReferenceParameter,
		"app.tag":              PHPConfigReferenceTag,
		"app.named_tag":        PHPConfigReferenceTag,
		"@app.factory":         PHPConfigReferenceService,
		"@app.target":          PHPConfigReferenceService,
	}
	for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
		value := phpquery.StringValue(literal)
		if kind, exists := expected[value]; exists {
			assert.Equal(t, kind, PHPArrayServiceReferenceAt(literal), value)
			delete(expected, value)
		}
	}
	assert.Empty(t, expected)

	var helper *phpsyntax.Node
	for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
		if phpquery.StringValue(literal) == "app.helper" {
			helper = literal
			break
		}
	}
	require.NotNil(t, helper)
	assert.True(t, PHPArrayServiceContextAt(helper))
	assert.Equal(
		t,
		PHPConfigReferenceService,
		PHPConfigReferenceAt(helper),
	)
}

func TestPHPArrayServiceReferenceSupportsAppConfig(t *testing.T) {
	source := `<?php
namespace Symfony\Component\DependencyInjection\Loader\Configurator;
App::config([
    'services' => [
        'app.consumer' => [
            'arguments' => ['%app.parameter%'],
        ],
    ],
]);
`
	root := phpparser.Parse(source).Tree.Root
	var target *phpsyntax.Node
	for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
		if phpquery.StringValue(literal) == "%app.parameter%" {
			target = literal
			break
		}
	}
	require.NotNil(t, target)
	assert.Equal(
		t,
		PHPConfigReferenceParameter,
		PHPArrayServiceReferenceAt(target),
	)
}

func TestPHPArrayServiceReferenceRejectsOrdinaryArrays(t *testing.T) {
	source := `<?php
function data(): array {
    return [
        'services' => [
            'app.consumer' => [
                'arguments' => ['%not.a.parameter%'],
            ],
        ],
    ];
}
`
	root := phpparser.Parse(source).Tree.Root
	var target *phpsyntax.Node
	for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
		if phpquery.StringValue(literal) == "%not.a.parameter%" {
			target = literal
			break
		}
	}
	require.NotNil(t, target)
	assert.Equal(
		t,
		PHPConfigReferenceNone,
		PHPArrayServiceReferenceAt(target),
	)
}

func TestPHPArrayServiceMethodAt(t *testing.T) {
	source := `<?php
use App\Service\Consumer;
return [
    'services' => [
        'app.consumer' => [
            'class' => Consumer::class,
            'calls' => [['configure', []]],
            'factory' => ['@app.factory', 'create'],
        ],
    ],
];
`
	root := phpparser.Parse(source).Tree.Root
	var configure, create *phpsyntax.Node
	for _, literal := range phpquery.Nodes(root, phpsyntax.PhpString) {
		switch phpquery.StringValue(literal) {
		case "configure":
			configure = literal
		case "create":
			create = literal
		}
	}
	require.NotNil(t, configure)
	call, found := PHPArrayServiceMethodAt(root, configure)
	require.True(t, found)
	assert.Equal(t, `\App\Service\Consumer`, call.Class)
	assert.Equal(t, "app.consumer", call.Service)
	assert.False(t, call.AllowStatic)

	require.NotNil(t, create)
	factory, found := PHPArrayServiceMethodAt(root, create)
	require.True(t, found)
	assert.Equal(t, "app.factory", factory.Service)
	assert.True(t, factory.AllowStatic)
}
