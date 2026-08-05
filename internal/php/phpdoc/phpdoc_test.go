package phpdoc

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

func TestParseRichPHPDoc(t *testing.T) {
	t.Parallel()
	document := Parse(`/**
 * Product repository contract.
 *
 * @template-covariant TEntity of Entity
 * @extends Repository<TEntity>
 * @implements IteratorAggregate<int, TEntity>
 * @property-read list<TEntity> $entities
 * @method static TEntity|null find(string $id #Route, bool $strict = false)
 * @param array<string, TEntity> $items items to save #Route #Service #route
 * @return list<TEntity>
 * @throws RuntimeException
 * @phpstan-type EntityMap array<string, TEntity>
 * @deprecated
 */`)

	require.Equal(t, "Product repository contract.", document.Summary)
	require.Equal(t, "array<string,TEntity>", document.Params["$items"].String())
	require.Equal(t, "list<TEntity>", document.Return.String())
	require.Len(t, document.Templates, 1)
	require.True(t, document.Templates[0].Covariant)
	require.Equal(t, "Entity", document.Templates[0].Bound.String())
	require.Equal(t, "Repository<TEntity>", document.Extends[0].String())
	require.Equal(t, "IteratorAggregate<int,TEntity>", document.Implements[0].String())
	require.Equal(t, "list<TEntity>", document.Properties[0].Type.String())
	require.True(t, document.Properties[0].ReadOnly)
	require.Equal(t, "TEntity|null", document.Methods[0].ReturnType.String())
	require.True(t, document.Methods[0].Static)
	require.Len(t, document.Methods[0].Parameters, 2)
	require.Equal(t, []string{"Route"}, document.Methods[0].Parameters[0].Tags)
	require.True(t, document.Methods[0].Parameters[1].Optional)
	require.Equal(t, []string{"Route", "Service"}, document.ParamTags["$items"])
	require.Equal(t, "array<string,TEntity>", document.Aliases["EntityMap"].String())
	require.True(t, document.Deprecated)
}

func TestUntypedMagicMethodParameterIsMixed(t *testing.T) {
	t.Parallel()
	document := Parse(`/**
 * @method mixed randomElement($array = ['a', 'b', 'c'])
 */`)

	require.Len(t, document.Methods, 1)
	require.Len(t, document.Methods[0].Parameters, 1)
	require.Equal(t, "mixed", document.Methods[0].Parameters[0].Type.String())
	require.True(t, document.Methods[0].Parameters[0].Optional)
}

func TestParseImportedTypeAliases(t *testing.T) {
	t.Parallel()
	document := Parse(`/**
 * @phpstan-import-type Payload from Source
 * @psalm-import-type Configuration from \Vendor\Definition as LocalConfig
 */`)

	require.Equal(t, TypeImport{
		Name: "Payload",
		From: "Source",
	}, document.Imports["Payload"])
	require.Equal(t, TypeImport{
		Name: "Configuration",
		From: `\Vendor\Definition`,
	}, document.Imports["LocalConfig"])
}

func TestParseConditionalAssertions(t *testing.T) {
	t.Parallel()
	document := Parse(`/**
 * @phpstan-assert-if-true TestMethod $this
 * @psalm-assert-if-false null $value
 * @phpstan-assert !null $result
 * @phpstan-assert =ExpectedType $actual
 */`)

	require.Equal(t, []Assertion{
		{
			Target:      "$this",
			Type:        types.MustParse("TestMethod"),
			WhenTrue:    true,
			Conditional: true,
		},
		{
			Target:      "$value",
			Type:        types.MustParse("null"),
			WhenTrue:    false,
			Conditional: true,
		},
		{Target: "$result", Type: types.MustParse("null"), Negated: true},
		{Target: "$actual", Type: types.MustParse("ExpectedType")},
	}, document.Assertions)
}

func TestVariadicAndReferenceParamTagsKeepTheirTypes(t *testing.T) {
	t.Parallel()
	document := Parse(`/**
 * @param Entity ...$events
 * @param array<string, mixed> &$metadata
 */`)
	require.Equal(t, "Entity", document.Params["$events"].String())
	require.Equal(
		t,
		"array<string,mixed>",
		document.Params["$metadata"].String(),
	)
}

func TestMultilineShapeTag(t *testing.T) {
	t.Parallel()
	document := Parse(`/**
 * @return array{
 *   id: string,
 *   nested?: array<int, string>
 * }
 */`)
	require.Equal(t, "array{id:string,nested?:array<int,string>}", document.Return.String())
}

func TestMultilineConditionalReturn(t *testing.T) {
	t.Parallel()
	document := Parse(`/**
 * @return (
 *    T is 'array' ? ArrayNodeDefinition<$this>
 *    : (T is 'variable' ? VariableNodeDefinition<$this>
 *    : (T is 'scalar' ? ScalarNodeDefinition<$this>
 *    : NodeDefinition<$this>))
 * )
 */`)
	require.Equal(
		t,
		`(T is "array" ? ArrayNodeDefinition<static> : `+
			`(T is "variable" ? VariableNodeDefinition<static> : `+
			`(T is "scalar" ? ScalarNodeDefinition<static> : `+
			`NodeDefinition<static>)))`,
		document.Return.String(),
	)
}

func TestStreamingLogicalLinesPreserveSummaryAndTagContinuation(t *testing.T) {
	t.Parallel()
	document := Parse(`/**
 * First summary line.
 * Second summary line.
 * @param string $value ordinary
 *   continued description
 * @return int
 */`)

	require.Equal(
		t,
		"First summary line. Second summary line.",
		document.Summary,
	)
	require.Equal(t, "string", document.Params["$value"].String())
	require.Equal(t, "int", document.Return.String())
	require.Nil(t, document.ParamTags)
}
