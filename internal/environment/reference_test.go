package environment

import (
	"strings"
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReferencesParseProcessorChainsAndRanges(t *testing.T) {
	source := `
database: '%env(resolve:int:default:port:DATABASE_PORT)%'
host: "%env(DATABASE_HOST)%"
`
	references := References(source)
	require.Len(t, references, 2)
	assert.Equal(t, "DATABASE_PORT", references[0].Name)
	assert.Equal(
		t,
		[]string{"resolve", "int", "default", "port"},
		references[0].Processors,
	)
	assert.Equal(
		t,
		"DATABASE_PORT",
		source[references[0].NameRange.Start:references[0].NameRange.End],
	)
	assert.Equal(t, "DATABASE_HOST", references[1].Name)

	offset := uint32(strings.Index(source, "DATABASE_PORT") + 3)
	reference, found := ReferenceAt(source, offset)
	require.True(t, found)
	assert.Equal(t, references[0], reference)
}

func TestCompletionReferenceSupportsIncompleteExpressions(t *testing.T) {
	tests := []struct {
		source     string
		name       string
		processors []string
	}{
		{
			source: "value: '%env(DATAB",
			name:   "DATAB",
		},
		{
			source:     "value: '%env(resolve:int:DATAB",
			name:       "DATAB",
			processors: []string{"resolve", "int"},
		},
		{
			source:     "value: '%env(resolve:int:DATABASE_URL)%'",
			name:       "DATABASE_URL",
			processors: []string{"resolve", "int"},
		},
	}
	for _, test := range tests {
		offset := uint32(strings.Index(test.source, test.name) + len(test.name))
		reference, found := CompletionReferenceAt(test.source, offset)
		require.True(t, found, test.source)
		assert.Equal(t, test.name, reference.Name)
		assert.Equal(t, test.processors, reference.Processors)
	}
}

func TestReferenceRejectsMalformedEnvironmentNames(t *testing.T) {
	assert.Empty(t, References("%env(string:not-valid)%"))
	_, found := CompletionReferenceAt("value: %env(NO-PE", 17)
	assert.False(t, found)
}

func TestReferencesRecoverAfterAnIncompleteEarlierExpression(t *testing.T) {
	references := References(
		"# unfinished %env(BROKEN\nvalue: '%env(APP_ENV)%'",
	)
	require.Len(t, references, 1)
	assert.Equal(t, "APP_ENV", references[0].Name)
}

func TestPHPAutowireEnvAttributeReferences(t *testing.T) {
	source := `<?php
#[Autowire(env: 'resolve:int:DATABASE_URL')]
#[Autowire(service: 'not-an-environment-variable')]
class Config {}
`
	root := phpparser.Parse(source).Tree.Root
	references := PHPReferences(root)
	require.Len(t, references, 1)
	reference := references[0]
	assert.Equal(t, "DATABASE_URL", reference.Name)
	assert.Equal(t, []string{"resolve", "int"}, reference.Processors)
	assert.Equal(
		t,
		"DATABASE_URL",
		source[reference.NameRange.Start:reference.NameRange.End],
	)

	offset := uint32(strings.Index(source, "DATABASE_URL") + 2)
	node := root.NodeAtOffset(offset)
	current, found := PHPReferenceAt(node, offset)
	require.True(t, found)
	assert.Equal(t, reference, current)

	service := phpquery.Nodes(root, phpsyntax.PhpString)[1]
	_, found = PHPReferenceAt(
		service,
		service.Range().Start+1,
	)
	assert.False(t, found)
}

func TestPHPAutowireEnvAttributeCompletionSupportsEmptyValue(t *testing.T) {
	source := "<?php #[Autowire(env: '')] class Config {}"
	root := phpparser.Parse(source).Tree.Root
	literal := phpquery.Nodes(root, phpsyntax.PhpString)[0]
	offset := phpquery.StringContentRange(literal).Start
	reference, found := PHPCompletionReferenceAt(literal, offset)
	require.True(t, found)
	assert.Empty(t, reference.Name)
	assert.Equal(t, offset, reference.NameRange.Start)
	assert.Equal(t, offset, reference.NameRange.End)
}

func TestPHPEnvFunctionReferences(t *testing.T) {
	source := `<?php
env('bool:SOME_ENV_VAR');
env (
    'resolve:DATABASE_URL',
);
$object->env('NOT_A_REFERENCE');
other('ALSO_NOT_A_REFERENCE');
`
	root := phpparser.Parse(source).Tree.Root
	references := PHPReferences(root)
	require.Len(t, references, 2)
	assert.Equal(t, "SOME_ENV_VAR", references[0].Name)
	assert.Equal(t, []string{"bool"}, references[0].Processors)
	assert.Equal(t, "DATABASE_URL", references[1].Name)
	assert.Equal(t, []string{"resolve"}, references[1].Processors)

	offset := uint32(strings.Index(source, "SOME_ENV_VAR") + 2)
	current, found := PHPReferenceAt(root.NodeAtOffset(offset), offset)
	require.True(t, found)
	assert.Equal(t, references[0], current)
}

func TestPHPEnvFunctionCompletionSupportsEmptyValue(t *testing.T) {
	source := "<?php env('')"
	root := phpparser.Parse(source).Tree.Root
	literal := phpquery.Nodes(root, phpsyntax.PhpString)[0]
	offset := phpquery.StringContentRange(literal).Start
	reference, found := PHPCompletionReferenceAt(literal, offset)
	require.True(t, found)
	assert.Empty(t, reference.Name)
}
