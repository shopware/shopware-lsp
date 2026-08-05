package literal

import (
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/stretchr/testify/require"
)

func TestTypeOfScalarLiterals(t *testing.T) {
	t.Parallel()

	root := phpparser.Parse(`<?php
// leading trivia
	1_024;
	1.5e2;
	0xDEAD;
	"value";
true;
null;
`).Tree.Root

	numbers := phpquery.Nodes(root, phpsyntax.PhpNumber)
	require.Len(t, numbers, 3)
	integer, ok := TypeOf(numbers[0])
	require.True(t, ok)
	require.Equal(t, "1024", integer.String())
	floating, ok := TypeOf(numbers[1])
	require.True(t, ok)
	require.Equal(t, "1.5e2", floating.String())
	hexadecimal, ok := TypeOf(numbers[2])
	require.True(t, ok)
	require.Equal(t, "0xDEAD", hexadecimal.String())

	strings := phpquery.Nodes(root, phpsyntax.PhpString)
	require.Len(t, strings, 1)
	value, ok := TypeOf(strings[0])
	require.True(t, ok)
	require.Equal(t, `"value"`, value.String())

	boolean, ok := TypeOf(phpquery.Nodes(root, phpsyntax.PhpBoolean)[0])
	require.True(t, ok)
	require.Equal(t, "true", boolean.String())

	nullValue, ok := TypeOf(phpquery.Nodes(root, phpsyntax.PhpNull)[0])
	require.True(t, ok)
	require.Equal(t, "null", nullValue.String())
}

func TestTypeOfRejectsNonLiteralSyntax(t *testing.T) {
	t.Parallel()

	root := phpparser.Parse(`<?php $value;`).Tree.Root
	variable := phpquery.Nodes(root, phpsyntax.PhpVariable)[0]
	_, ok := TypeOf(variable)
	require.False(t, ok)
}
