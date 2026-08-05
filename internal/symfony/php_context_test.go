package symfony

import (
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPConfigReferenceAt(t *testing.T) {
	root := phpparser.Parse(`<?php
service('app.service');
param('app.parameter');
tagged_iterator('app.tag');
$services->alias('alias', 'target');
`).Tree.Root
	calls := phpquery.Calls(root)
	require.Len(t, calls, 4)

	expected := []PHPConfigReferenceKind{
		PHPConfigReferenceService,
		PHPConfigReferenceParameter,
		PHPConfigReferenceTag,
		PHPConfigReferenceService,
	}
	for index, call := range calls {
		stringNode := phpquery.StringArgument(call, len(phpquery.Arguments(call))-1)
		assert.Equal(t, expected[index], PHPConfigReferenceAt(stringNode))
	}
}

func TestPHPAttributeReferenceAt(t *testing.T) {
	root := phpparser.Parse(`<?php
#[Autowire(service: 'app.service')]
#[Autowire(param: 'app.parameter')]
#[Autowire('@app.positional')]
#[Autowire('%app.positional_parameter%')]
#[Autowire('@')]
#[Autowire('%')]
#[AutowireLocator(['app.locator', 'app.other'])]
#[AutowireLocator(services: ['app.named'])]
#[AutowireLocator(exclude: 'app.excluded')]
#[AutowireServiceClosure('app.closure')]
#[AutowireServiceClosure(service: 'app.named_closure')]
#[AutowireMethodOf('app.method_of')]
#[AutowireMethodOf(service: 'app.named_method_of')]
#[AutowireCallable(service: 'app.callable')]
#[TaggedIterator('app.iterator')]
#[TaggedLocator(tag: 'app.locator_tag')]
#[Autoconfigure(['app.auto_one', 'app.auto_two'])]
#[Autoconfigure(tags: ['app.auto_named'])]
#[AutoconfigureTag('app.configure_tag')]
#[AutoconfigureTag(name: 'app.named_configure_tag')]
#[AsDecorator(decorates: 'app.decorated')]
class Example {}
`).Tree.Root
	var references []PHPConfigReferenceKind
	for _, attribute := range phpquery.Attributes(phpquery.Classes(root)[0]) {
		for _, stringNode := range phpquery.Nodes(
			attribute,
			phpsyntax.PhpString,
		) {
			references = append(
				references,
				PHPAttributeReferenceAt(stringNode),
			)
		}
	}
	assert.Equal(t, []PHPConfigReferenceKind{
		PHPConfigReferenceService,
		PHPConfigReferenceParameter,
		PHPConfigReferenceService,
		PHPConfigReferenceParameter,
		PHPConfigReferenceService,
		PHPConfigReferenceParameter,
		PHPConfigReferenceService,
		PHPConfigReferenceService,
		PHPConfigReferenceService,
		PHPConfigReferenceService,
		PHPConfigReferenceService,
		PHPConfigReferenceService,
		PHPConfigReferenceService,
		PHPConfigReferenceService,
		PHPConfigReferenceService,
		PHPConfigReferenceTag,
		PHPConfigReferenceTag,
		PHPConfigReferenceTag,
		PHPConfigReferenceTag,
		PHPConfigReferenceTag,
		PHPConfigReferenceTag,
		PHPConfigReferenceTag,
		PHPConfigReferenceService,
	}, references)
}

func TestPHPAutowireCallableMethodAt(t *testing.T) {
	root := phpparser.Parse(`<?php
#[AutowireCallable(service: 'app.formatter', method: 'format')]
#[AutowireCallable(service: Formatter::class, method: '')]
#[AutowireCallable(service: 'app.formatter', label: 'not-a-method')]
class Example {}
`).Tree.Root
	strings := phpquery.Nodes(root, phpsyntax.PhpString)
	require.Len(t, strings, 5)

	serviceReference, found := PHPAutowireCallableMethodAt(strings[1])
	require.True(t, found)
	assert.Equal(t, "app.formatter", serviceReference.Service)
	assert.Empty(t, serviceReference.Class)
	assert.Equal(t, "format", serviceReference.Method)

	classReference, found := PHPAutowireCallableMethodAt(strings[2])
	require.True(t, found)
	assert.Equal(t, "Formatter", classReference.Class)
	assert.Empty(t, classReference.Service)
	assert.Empty(t, classReference.Method)

	_, found = PHPAutowireCallableMethodAt(strings[4])
	assert.False(t, found)
}

func TestPHPWhenEnvironmentAt(t *testing.T) {
	root := phpparser.Parse(`<?php
#[When('dev')]
#[When(env: 'test')]
#[Other('prod')]
class Example {}
`).Tree.Root
	strings := phpquery.Nodes(root, phpsyntax.PhpString)
	require.Len(t, strings, 3)
	assert.True(t, PHPWhenEnvironmentAt(strings[0]))
	assert.True(t, PHPWhenEnvironmentAt(strings[1]))
	assert.False(t, PHPWhenEnvironmentAt(strings[2]))
}

func TestPHPParameterAccessTypes(t *testing.T) {
	assert.ElementsMatch(
		t,
		PHPParameterBagTypes(),
		PHPParameterAccessTypes("get"),
	)
	assert.Equal(
		t,
		[]string{PHPDIContainerInterface},
		PHPParameterAccessTypes("getParameter"),
	)
	assert.ElementsMatch(
		t,
		[]string{PHPParameterBagInterface, PHPParametersConfigurator},
		PHPParameterAccessTypes("set"),
	)
	assert.Empty(t, PHPParameterReadAccessTypes("set"))
	assert.NotEmpty(t, PHPParameterReadAccessTypes("hasParameter"))
}
