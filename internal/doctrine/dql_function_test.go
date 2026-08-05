package doctrine

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDQLFunctionsAreParsedPersistedAndRemoved(t *testing.T) {
	cacheDir := t.TempDir()
	path := "/project/vendor/doctrine/orm/Parser.php"
	source := `<?php
namespace Doctrine\ORM\Query;
class Parser {
    private static $stringFunctions = [
        'substring' => Functions\SubstringFunction::class,
    ];
    private static $numericFunctions = [
        'length' => Functions\LengthFunction::class,
        'min' => Functions\MinFunction::class,
    ];
    private static $datetimeFunctions = [
        'current_time' => Functions\CurrentTimeFunction::class,
    ];
}`
	root := phpparser.Parse(source).Tree.Root
	functions := DQLFunctionsInDocument(path, root)
	require.Len(t, functions, 4)
	assert.Equal(t, "substring", functions[3].Name)
	assert.Equal(
		t,
		"Doctrine\\ORM\\Query\\Functions\\SubstringFunction",
		functions[3].Class,
	)

	idx, err := NewIndex(cacheDir)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	persisted, err := idx.DQLFunctions()
	require.NoError(t, err)
	require.Len(t, persisted, 4)
	function, found, err := idx.DQLFunction("MIN")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(
		t,
		"Doctrine\\ORM\\Query\\Functions\\MinFunction",
		function.Class,
	)
	require.NoError(t, idx.Close())

	restored, err := NewIndex(cacheDir)
	require.NoError(t, err)
	restoredFunctions, err := restored.DQLFunctions()
	require.NoError(t, err)
	require.Len(t, restoredFunctions, 4)
	require.NoError(t, restored.Index(indexer.NewParsedFile(
		path,
		[]byte("<?php namespace Doctrine\\ORM\\Query; class Parser {}"),
	)))
	restoredFunctions, err = restored.DQLFunctions()
	require.NoError(t, err)
	assert.Empty(t, restoredFunctions)
	require.NoError(t, restored.Close())
}

func TestDQLFunctionCompletionAndReference(t *testing.T) {
	idx, _ := queryBuilderFixture(t)
	parserSource := `<?php
namespace Doctrine\ORM\Query;
class Parser {
    private static $numericFunctions = [
        'min' => Functions\MinFunction::class,
    ];
}`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/vendor/doctrine/orm/Parser.php",
		[]byte(parserSource),
	)))
	source := `<?php
$dql = 'SELECT MIN(p.name) FROM App\Entity\Product p';
`
	root := phpparser.Parse(source).Tree.Root
	completionOffset := uint32(strings.Index(source, "SELECT ") + len("SELECT "))
	completionNode := root.NodeAtOffset(completionOffset)
	completions := idx.QueryCompletionsAt(
		context.Background(),
		root,
		completionNode,
		completionOffset,
	)
	var functionCompletion QueryCompletion
	for _, completion := range completions {
		if completion.Label == "MIN" {
			functionCompletion = completion
			break
		}
	}
	assert.Equal(t, QueryFunctionCompletion, functionCompletion.Kind)
	assert.Equal(t, "MIN(", functionCompletion.InsertText)

	referenceOffset := uint32(strings.Index(source, "MIN(") + 1)
	referenceNode := root.NodeAtOffset(referenceOffset)
	function, rng, found := idx.QueryFunctionReferenceAt(
		context.Background(),
		root,
		referenceNode,
		referenceOffset,
	)
	require.True(t, found)
	assert.Equal(t, "min", function.Name)
	assert.Equal(
		t,
		"Doctrine\\ORM\\Query\\Functions\\MinFunction",
		function.Class,
	)
	assert.Equal(t, "MIN", source[rng.Start:rng.End])
}
