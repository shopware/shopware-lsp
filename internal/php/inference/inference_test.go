package inference

import (
	"strconv"
	"strings"
	"testing"
	"unsafe"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/binder"
	"github.com/shopware/shopware-lsp/internal/php/phpstormmeta"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/stubs"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

var (
	benchmarkShapeFields  []types.ShapeField
	benchmarkShapeIndices map[string]int
	benchmarkResolvedType types.Type
)

func BenchmarkResolveOrdinaryCompositeType(b *testing.B) {
	value := types.Array(
		types.String(),
		types.List(types.Named("Shopware\\Core\\Content\\Product")),
	)
	receiver := types.Named("Shopware\\Core\\Content\\Product")

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		benchmarkResolvedType = resolveSpecialType(value, receiver, receiver)
	}
}

func TestResolveOrdinaryCompositeTypeDoesNotAllocate(t *testing.T) {
	value := types.Array(
		types.String(),
		types.List(types.Named("Shopware\\Core\\Content\\Product")),
	)
	receiver := types.Named("Shopware\\Core\\Content\\Product")

	allocations := testing.AllocsPerRun(100, func() {
		benchmarkResolvedType = resolveSpecialType(value, receiver, receiver)
	})
	require.Zero(t, allocations)
	require.True(t, benchmarkResolvedType.Equal(value))
}

func TestResolveSpecialTypeRebuildsContextDependentChildren(t *testing.T) {
	t.Parallel()

	stable := types.Named("Stable")
	current := types.Named("Current")
	receiver := types.Named("Receiver")
	tests := []struct {
		value    types.Type
		expected types.Type
	}{
		{
			value:    types.Array(stable, types.List(types.Self())),
			expected: types.Array(stable, types.List(current)),
		},
		{
			value: types.Callable(
				[]types.CallableParameter{{Type: stable}},
				types.Static(),
			),
			expected: types.Callable(
				[]types.CallableParameter{{Type: stable}},
				receiver,
			),
		},
		{
			value: types.ArrayShape([]types.ShapeField{
				{Name: "stable", Type: stable},
				{Name: "current", Type: types.Self()},
			}, false),
			expected: types.ArrayShape([]types.ShapeField{
				{Name: "stable", Type: stable},
				{Name: "current", Type: current},
			}, false),
		},
	}
	for _, test := range tests {
		actual := resolveSpecialType(test.value, receiver, current)
		require.True(t, test.expected.Equal(actual))
	}
}

func BenchmarkSmallShapeFieldCollection(b *testing.B) {
	const fieldCount = 8
	b.Run("lazy-linear", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var fields []types.ShapeField
			var indices map[string]int
			for index := 0; index < fieldCount; index++ {
				fields, indices = upsertShapeField(
					fields,
					indices,
					types.ShapeField{
						Name: strconv.Itoa(index),
						Type: types.Int(),
					},
				)
			}
			benchmarkShapeFields = fields
		}
	})
	b.Run("eager-map", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var fields []types.ShapeField
			indices := make(map[string]int)
			for index := 0; index < fieldCount; index++ {
				name := strconv.Itoa(index)
				if existing, found := indices[name]; found {
					fields[existing] = types.ShapeField{
						Name: name,
						Type: types.Int(),
					}
					continue
				}
				indices[name] = len(fields)
				fields = append(fields, types.ShapeField{
					Name: name,
					Type: types.Int(),
				})
			}
			benchmarkShapeFields = fields
			benchmarkShapeIndices = indices
		}
	})
}

func BenchmarkImplicitListInference(b *testing.B) {
	root := phpparser.Parse(`<?php
function values(): array {
    return [
        1, 2, 3, 4, 5, 6, 7, 8,
        9, 10, 11, 12, 13, 14, 15, 16,
    ];
}
`).Tree.Root
	bound := binder.New().Bind("/implicit-list.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzer := New(snapshot)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = analyzer.Analyze(bound, root)
	}
}

func TestEnvironmentForkCopiesOnWrite(t *testing.T) {
	t.Parallel()
	original := newEnvironment(2)
	original.set("$value", types.Int())

	fork := cloneEnvironment(original)
	forkValue, found := fork.get("$value")
	require.True(t, found)
	require.Equal(t, types.Int(), forkValue)

	// Writes made after the fork remain isolated in both directions.
	original.set("$after", types.Bool())
	_, found = fork.get("$after")
	require.False(t, found)
	fork.set("$value", types.String())
	originalValue, found := original.get("$value")
	require.True(t, found)
	require.Equal(t, types.Int(), originalValue)
	forkValue, found = fork.get("$value")
	require.True(t, found)
	require.Equal(t, types.String(), forkValue)

	// Ordinary value copies retain the frame reference semantics used while
	// recursively evaluating one expression.
	alias := original
	alias.set("$shared", types.Float())
	shared, found := original.get("$shared")
	require.True(t, found)
	require.Equal(t, types.Float(), shared)

	for index := 0; index <= smallEnvironmentLimit; index++ {
		original.set("$item"+strconv.Itoa(index), types.Int())
	}
	require.NotNil(t, original.handle.table)
	largeFork := cloneEnvironment(original)
	original.set("$item0", types.String())
	forkItem, found := largeFork.get("$item0")
	require.True(t, found)
	require.Equal(t, types.Int(), forkItem)
}

func TestEnvironmentHandleRemainsCompact(t *testing.T) {
	t.Parallel()
	require.LessOrEqual(t, unsafe.Sizeof(environmentHandle{}), uintptr(64))
}

func TestSmallEnvironmentKeepsFirstBindingInline(t *testing.T) {
	env := newEnvironment(1)
	require.Nil(t, env.handle.bindings)
	env.set("$value", types.Int())
	require.Nil(t, env.handle.bindings)
	require.True(t, env.handle.hasOverride)

	fork := cloneEnvironment(env)
	fork.set("$value", types.String())
	originalValue, found := env.get("$value")
	require.True(t, found)
	require.Equal(t, types.Int(), originalValue)
	forkValue, found := fork.get("$value")
	require.True(t, found)
	require.Equal(t, types.String(), forkValue)

	env.set("$other", types.Bool())
	require.False(t, env.handle.hasOverride)
	require.Len(t, env.handle.bindings, 2)
	env.deletePrefix("$other")
	require.Equal(t, 1, env.len())

	allocations := testing.AllocsPerRun(1_000, func() {
		handle := environmentHandle{}
		inline := environment{handle: &handle}
		inline.set("$inline", types.Int())
	})
	require.Zero(t, allocations)
}

func TestEnvironmentForkSkipsNoopCopies(t *testing.T) {
	t.Parallel()

	original := newEnvironment(smallEnvironmentLimit + 1)
	for index := 0; index <= smallEnvironmentLimit; index++ {
		original.set("$item"+strconv.Itoa(index), types.Int())
	}

	unchanged := cloneEnvironment(original)
	unchanged.set("$item0", types.Int())
	require.True(t, unchanged.handle.shared)

	withoutMatchingPrefix := cloneEnvironment(original)
	withoutMatchingPrefix.deletePrefix("$missing->")
	require.True(t, withoutMatchingPrefix.handle.shared)

	original.set("$item0", types.String())
	for _, environment := range []environment{unchanged, withoutMatchingPrefix} {
		value, found := environment.get("$item0")
		require.True(t, found)
		require.Equal(t, types.Int(), value)
	}
}

func TestEnvironmentForkKeepsSingleOverrideInline(t *testing.T) {
	t.Parallel()

	original := newEnvironment(2)
	original.set("$value", types.Int())
	original.set("$stable", types.Bool())

	fork := cloneEnvironment(original)
	fork.set("$value", types.String())
	require.True(t, fork.handle.shared)
	require.True(t, fork.handle.hasOverride)
	require.Equal(t, 2, fork.len())

	visited := make(map[string]types.Type)
	fork.visit(func(name string, value types.Type) {
		visited[name] = value
	})
	require.Equal(t, map[string]types.Type{
		"$value":  types.String(),
		"$stable": types.Bool(),
	}, visited)

	clonedOverride := cloneEnvironment(fork)
	fork.set("$value", types.Float())
	value, found := clonedOverride.get("$value")
	require.True(t, found)
	require.Equal(t, types.String(), value)

	fork.set("$added", types.Null())
	require.False(t, fork.handle.shared)
	require.False(t, fork.handle.hasOverride)
	require.Equal(t, 3, fork.len())

	withDerivedValue := cloneEnvironment(original)
	withDerivedValue.set("$value->name", types.String())
	withDerivedValue.deletePrefix("$value->")
	_, found = withDerivedValue.get("$value->name")
	require.False(t, found)
	value, found = original.get("$value")
	require.True(t, found)
	require.Equal(t, types.Int(), value)
}

func BenchmarkEnvironmentForkMutation(b *testing.B) {
	original := newEnvironment(smallEnvironmentLimit + 1)
	for index := 0; index <= smallEnvironmentLimit; index++ {
		original.set("$item"+strconv.Itoa(index), types.Int())
	}

	b.Run("same value", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			fork := cloneEnvironment(original)
			fork.set("$item0", types.Int())
		}
	})
	b.Run("changed value", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			fork := cloneEnvironment(original)
			fork.set("$item0", types.String())
		}
	})
	b.Run("second changed value", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			fork := cloneEnvironment(original)
			fork.set("$item0", types.String())
			fork.set("$item1", types.Bool())
		}
	})
	b.Run("missing prefix", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			fork := cloneEnvironment(original)
			fork.deletePrefix("$missing->")
		}
	})
}

func TestRuntimeExceptionProvidesConcreteThrowableMethods(t *testing.T) {
	t.Parallel()
	source := `<?php
class ApplicationException extends RuntimeException {}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/exception.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.abstract", issue.Code, issue.Message)
	}
}

func TestEmptyEnvironmentCopiesShareFirstWrite(t *testing.T) {
	t.Parallel()

	original := newEnvironment(0)
	alias := original
	alias.set("$shared", types.String())

	shared, found := original.get("$shared")
	require.True(t, found)
	require.Equal(t, types.String(), shared)
}

func TestShapeFieldIndexStaysLazyAndPreservesLastDuplicate(t *testing.T) {
	t.Parallel()

	var fields []types.ShapeField
	var indices map[string]int
	for index := 0; index < shapeFieldLinearLimit; index++ {
		fields, indices = upsertShapeField(
			fields,
			indices,
			types.ShapeField{
				Name: strconv.Itoa(index),
				Type: types.Int(),
			},
		)
	}
	require.Nil(t, indices)

	fields, indices = upsertShapeField(
		fields,
		indices,
		types.ShapeField{Name: "3", Type: types.String()},
	)
	require.Nil(t, indices)
	require.Len(t, fields, shapeFieldLinearLimit)
	require.Equal(t, types.String(), fields[3].Type)

	fields, indices = upsertShapeField(
		fields,
		indices,
		types.ShapeField{Name: "16", Type: types.Bool()},
	)
	require.NotNil(t, indices)
	require.Len(t, fields, shapeFieldLinearLimit+1)

	fields, indices = upsertShapeField(
		fields,
		indices,
		types.ShapeField{Name: "16", Type: types.Float()},
	)
	require.Equal(t, 16, indices["16"])
	require.Len(t, fields, shapeFieldLinearLimit+1)
	require.Equal(t, types.Float(), fields[16].Type)
}

func TestImplicitListArrayClassification(t *testing.T) {
	t.Parallel()

	root := phpparser.Parse(`<?php
[];
[1, 2];
[1, 'key' => 2];
[...$values];
`).Tree.Root
	arrays := phpquery.Arrays(root)
	require.Len(t, arrays, 4)
	require.True(t, isImplicitListArray(arrays[0]))
	require.True(t, isImplicitListArray(arrays[1]))
	require.False(t, isImplicitListArray(arrays[2]))
	require.False(t, isImplicitListArray(arrays[3]))
}

func TestSmallImplicitArrayLiteralPreservesTuplePositions(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @return array{0: string, 1: bool, 2: bool} */
function tuple(): array {
    return ['value', false, true];
}
$value = tuple()[0];
`).Tree.Root
	bound := binder.New().Bind("/tuple.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(
		t,
		analyzed,
		root,
		"['value', false, true]",
		`array{0:"value",1:false,2:true}`,
	)
	assertTextType(t, analyzed, root, "tuple()[0]", "string")
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestLiteralKeyAssignmentsPreserveArrayShape(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/**
 * @return array{id: string, count: int, optional?: bool}
 */
function payload(): array {
    $result = [];
    $result['id'] = 'product';
    $result['count'] = 1;
    return $result;
}
`).Tree.Root
	bound := binder.New().Bind("/assigned-shape.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertNodeType(
		t,
		analyzed,
		root,
		phpsyntax.PhpVariable,
		`array{count:1,id:"product"}`,
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestNullableCallableRetainsInvocationResult(t *testing.T) {
	t.Parallel()
	result, found := callableResult(types.Nullable(
		types.Callable(nil, types.String()),
	))
	require.True(t, found)
	require.Equal(t, types.String(), result)
}

func TestByReferenceCallDeclaresOutputVariable(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function fill(string $input, array &$matches): bool {
    return true;
}
function run(string $input): mixed {
    fill($input, $matches);

    return $matches[0];
}
function runCast(string $input): mixed {
    $count = (int) fill($input, $castMatches);

    return $castMatches[0];
}
`).Tree.Root
	bound := binder.New().Bind("/by-reference.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	outputs := make(map[string]semantic.Symbol)
	for _, symbol := range analyzed.Symbols {
		if symbol.Kind == semantic.LocalSymbol &&
			(symbol.Name == "$matches" || symbol.Name == "$castMatches") {
			outputs[symbol.Name] = symbol
		}
	}
	require.Contains(t, outputs, "$matches")
	require.Contains(t, outputs, "$castMatches")
	require.Equal(t, "array", outputs["$matches"].Type.String())
	require.Equal(t, "array", outputs["$castMatches"].Type.String())
	for _, reference := range analyzed.References {
		if reference.Kind != semantic.VariableName {
			continue
		}
		if output, exists := outputs[reference.Name]; exists {
			require.Equal(t, output.ID, reference.Resolved)
		}
	}
}

func TestTernaryNarrowsRepeatedPredicateExpression(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Response {
    public function getContent(): string|false {}
}
function content(Response $response): string {
    $value = is_string($response->getContent())
        ? $response->getContent()
        : '';

    return $value;
}
`).Tree.Root
	bound := binder.New().Bind("/ternary-predicate.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestUnsetAfterNullCheckRemovesArrayShapeField(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Required {}
class CascadeDelete {}
/** @return array<string, array{class: string}> */
function flags(bool $keep): array {
	$flags = [];
	$flags[Required::class] = ['class' => Required::class];
	$flags['cascade'] = $keep ? ['class' => CascadeDelete::class] : null;
    if ($flags['cascade'] === null) {
        unset($flags['cascade']);
    }

    return $flags;
}
`).Tree.Root
	bound := binder.New().Bind("/unset-array-field.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestNestedArrayWritesAndUnsetUpdateElementShape(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/**
 * @param array<string, array{id: string, translations: string}> $options
 * @return array<string, array{id: string, group: array{id: string}}>
 */
function enrich(array $options): array {
    foreach ($options as $optionId => $option) {
        $options[$optionId]['group'] = ['id' => $options[$optionId]['id']];
        unset($options[$optionId]['translations']);
    }

    return $options;
}
`).Tree.Root
	bound := binder.New().Bind("/nested-array-write.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestNestedArrayAppendKeepsOuterKeyType(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class EntityDefinition {}
/**
 * @param class-string<EntityDefinition> $definition
 * @return array<class-string<EntityDefinition>, list<string>>
 */
function collect(string $definition): array {
    $result = [];
    $result[$definition][] = 'error';

    return $result;
}
`).Tree.Root
	bound := binder.New().Bind("/nested-array-append.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestTerminatingFalsyGuardRemovesNullableReturn(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function requireValue(?string $value): string {
    if (!$value) {
        throw new RuntimeException();
    }

    return $value;
}
`).Tree.Root
	bound := binder.New().Bind("/truthiness.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestNegativeIsNumericBranchExcludesFloat(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function convert(float|string|null $value): ?string {
    if (!is_numeric($value)) {
        return $value === null ? null : $value;
    }

    return (string) $value;
}
`).Tree.Root
	bound := binder.New().Bind("/numeric-guard.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestClassConstantEqualityNarrowsNullableArgument(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class ShippingMethod {
    public const TAX_TYPE_FIXED = 'fixed';
}
function consume(string $taxType): void {}
function validate(?string $taxType, ?string $taxId): void {
    if ($taxType === ShippingMethod::TAX_TYPE_FIXED && $taxId === null) {
        consume($taxType);
    }
}
`).Tree.Root
	bound := binder.New().Bind("/constant-equality.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestTerminatingFalsyGuardPreservesConcreteGenericObject(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Product {}
/**
 * @template T
 */
class Collection {
    /** @return T|null */
    public function first() {}
}
/** @extends Collection<Product> */
class Products extends Collection {}
function consume(Product $product): void {}
function run(Products $products): void {
    $product = $products->first();
    if (!$product) {
        return;
    }
    consume($product);
}
`).Tree.Root
	bound := binder.New().Bind("/generic-truthiness.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestInlineVarAnnotationsRefineAssignmentsAndForeachValues(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @param array<string, string> $row */
function consumeRow(array $row): void {}
function consumeString(string $value): void {}
function annotatedValue(): string {
    /** @var string $annotated */
    // @phpstan-ignore varTag.type
    $annotated = 42;

    return $annotated;
}

/** @param list<array<string, mixed>> $rows */
function run(array $rows): void {
    /** @var array<string, string> $row */
    foreach ($rows as $row) {
        consumeRow($row);
    }

    /** @var string $value */
    $value = $rows[0]['value'];
    consumeString($value);
}
`).Tree.Root
	bound := binder.New().Bind("/inline-var.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestPrivateReadonlyPropertyUsesConstructorAssignmentType(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
final class Scopes {
    /** @var array<string> */
    private readonly array $values;

    /** @return list<string> */
    public function values(): array {
        return $this->values;
    }

    /** @param list<string> $values */
    public function __construct(array $values) {
        $this->values = $values;
    }
}
`).Tree.Root
	bound := binder.New().Bind("/readonly-constructor-type.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestSymfonyFinderRealPathIsBenevolentlyString(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
namespace Symfony\Component\Finder {
    class SplFileInfo {
        public function getRealPath(): string|false {}
    }
}

namespace App {
    /** @return list<string> */
    function paths(iterable $files): array {
        $paths = [];
        foreach ($files as $file) {
            if ($file instanceof \Symfony\Component\Finder\SplFileInfo) {
                $paths[] = $file->getRealPath();
            }
        }
        return $paths;
    }
}
`).Tree.Root
	bound := binder.New().Bind("/finder-real-path.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestInternalFalseUnionsAreBenevolentAtUseSites(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function path(): string {
    return tempnam('/tmp', 'prefix');
}
function expiration(DateTimeImmutable $now, string $modifier): DateTimeInterface {
    return $now->modify($modifier);
}
`).Tree.Root
	bound := binder.New().Bind("/benevolent-internal-unions.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestIssetInitializationDropsImpossibleEmptyArrayBranch(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class SalesChannel {}
/**
 * @param list<string> $foreignKeys
 * @param non-empty-list<array{foreignKey: string}> $urls
 */
function consume(array $foreignKeys, array $urls, SalesChannel $channel): void {}
/** @param iterable<string, SalesChannel> $channels */
function build(iterable $channels): void {
    $configs = [];
    foreach ($channels as $id => $channel) {
        if (!isset($configs[$id])) {
            $configs[$id] = ['urls' => [], 'channel' => $channel];
        }
        $configs[$id]['urls'][] = ['foreignKey' => $id];
    }
    foreach ($configs as $config) {
        consume(array_column($config['urls'], 'foreignKey'), $config['urls'], $config['channel']);
    }
}
`).Tree.Root
	bound := binder.New().Bind("/isset-array-initialization.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestNestedIssetInitializationDoesNotCreatePartialShape(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/**
 * @param iterable<array{package: string, version: string, normalized: string|null, source: string}> $rows
 * @return array<string, array<string, array{pretty: string, normalized: string|null, sources: list<string>}>>
 */
function collect(iterable $rows): array {
    $packages = [];
    foreach ($rows as $row) {
        $package = $row['package'];
        $version = $row['version'];
        if (!isset($packages[$package][$version])) {
            $packages[$package][$version] = [
                'pretty' => $version,
                'normalized' => $row['normalized'],
                'sources' => [],
            ];
        }
        if (!in_array($row['source'], $packages[$package][$version]['sources'], true)) {
            $packages[$package][$version]['sources'][] = $row['source'];
        }
    }
    return $packages;
}
`).Tree.Root
	bound := binder.New().Bind("/nested-isset-array-initialization.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestConditionalAssertionNarrowsOppositeBranch(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Result {
    public ?Throwable $exception = null;
    /** @phpstan-assert-if-true null $this->exception */
    public function successful(): bool {}
}
function consumeThrowable(Throwable $exception): void {}
function handle(Result $result): void {
    if ($result->successful()) {
        return;
    }
    consumeThrowable($result->exception);
}
`).Tree.Root
	bound := binder.New().Bind("/conditional-opposite-assertion.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestArraySearchPreservesHaystackKeyType(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @param array<string, string> $values */
function findKey(array $values): string {
    $updates = [];
    foreach (['needle'] as $needle) {
        if ($key = array_search($needle, $values, true)) {
            $updates[$key] = true;
            continue;
        }
    }
    foreach ($updates as $key => $_) {
        return $key;
    }
    return '';
}
`).Tree.Root
	bound := binder.New().Bind("/array-search.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestInheritedMethodSelfParameterUsesDeclaringClass(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
abstract class Aggregation {
    public function add(self $aggregation): self {
        return $this;
    }
}
class NestedAggregation extends Aggregation {}
function compose(
    NestedAggregation $nested,
    Aggregation $aggregation
): Aggregation {
    return $nested->add($aggregation);
}
`).Tree.Root
	bound := binder.New().Bind("/inherited-self.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestDynamicPlainStringConstructionRemainsUncertain(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Event {}
function consume(Event $event): void {}
function create(string $class): void {
    consume(new $class());
}
`).Tree.Root
	bound := binder.New().Bind("/dynamic-class.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestInvokableObjectIsAssignableToCallable(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Middleware {
    public function __invoke(string $request): int {
        return 1;
    }
}
function middleware(): callable {
    return new Middleware();
}
`).Tree.Root
	bound := binder.New().Bind("/invokable.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	require.True(t, snapshot.Relations().IsSubtype(
		types.Named("Middleware"),
		types.Callable(nil, types.Mixed()),
	))
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestAssertPredicateNarrowsNullableReturn(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function requireString(?string $value): string {
    \assert(\is_string($value));
    return $value;
}
function requireNamedString(?string $value): string {
    \assert(assertion: \is_string($value));
    return $value;
}
`).Tree.Root
	bound := binder.New().Bind("/assert-predicate.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestAssertIsResourceNarrowsFopenResult(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @param resource $stream */
function acceptsStream($stream): void {}
function write(): void {
    $stream = fopen('php://temp', 'rwb');
    assert(is_resource($stream));
    acceptsStream($stream);
}
`).Tree.Root
	bound := binder.New().Bind("/assert-resource.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestClassExistenceAndIsANarrowStringToBoundClassString(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Kernel {}
/**
 * @return class-string<Kernel>
 */
function validatedClass(string $class): string {
    if (!class_exists($class)) {
        throw new RuntimeException();
    }
    if (!is_a($class, Kernel::class, true)) {
        throw new RuntimeException();
    }

    return $class;
}
/** @return class-string<Kernel> */
function validatedSubclass(string $class): string {
    if (!class_exists($class)) {
        throw new RuntimeException();
    }
    if (!is_subclass_of($class, Kernel::class)) {
        throw new RuntimeException();
    }
    return $class;
}
`).Tree.Root
	bound := binder.New().Bind("/class-string-flow.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestTerminatingRepeatedGetterGuardNarrowsReturn(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Customer {
    public function getId(): ?string {}
}
function requireId(?Customer $customer): string {
    if (!$customer?->getId()) {
        throw new RuntimeException();
    }

    return $customer->getId();
}
`).Tree.Root
	bound := binder.New().Bind("/getter-truthiness.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestTruthyNullsafeCallNarrowsReceiver(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Item {
    public function enabled(): bool {}
}
function consume(Item $item): void {}
function maybeConsume(?Item $item): void {
    if ($item?->enabled()) {
        consume($item);
    }
}
`).Tree.Root
	bound := binder.New().Bind("/nullsafe-receiver.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestPropertyAssignmentUpdatesBranchFlow(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Recipient {}
class Event {
    private ?Recipient $recipient = null;

    public function recipient(): Recipient {
        if (!$this->recipient instanceof Recipient) {
            $this->recipient = new Recipient();
        }

        return $this->recipient;
    }
}
`).Tree.Root
	bound := binder.New().Bind("/property-assignment.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestInstanceofNarrowedPropertySurvivesLeadingComments(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
interface TranslatorInterface {}
class SymfonyTranslator implements TranslatorInterface {
    public function setFallbackLocales(array $locales): void {}
}
class Translator {
    public function __construct(private TranslatorInterface $translator) {}
    public function reset(): void {
        if ($this->translator instanceof SymfonyTranslator) {
            // Explain why this implementation-specific reset is required.
            $this->translator->setFallbackLocales([]);
        }
    }
}
`).Tree.Root
	bound := binder.New().Bind("/property-comment-flow.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	linked := LinkMembers(analyzed, snapshot, root)
	for _, reference := range linked.References {
		if reference.Name == "setFallbackLocales" {
			require.NotEmpty(t, reference.Resolved)
		}
	}
}

func TestTerminatingNegatedInstanceofNarrowsToIntersection(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/**
 * @template TKey
 * @template TValue
 */
interface IteratorAggregate {}
/**
 * @template T
 * @implements IteratorAggregate<array-key, T>
 */
abstract class Collection implements IteratorAggregate {}
interface StorageAware {
    public function getStorageName(): string;
}
abstract class Field {
    public function getPropertyName(): string {}
}
/** @extends Collection<Field> */
class FieldCollection extends Collection {}
function storageNames(FieldCollection $fields): \Generator {
    foreach ($fields as $field) {
        if (!$field instanceof StorageAware) {
            continue;
        }

        yield $field->getStorageName() => $field->getPropertyName();
    }
}
function storageMap(Field $field): array {
    $mapped = [];
    if ($field instanceof StorageAware && $field->getStorageName() !== '') {
        $mapped[$field->getStorageName()] = $field->getPropertyName();
    }
    return $mapped;
}
`).Tree.Root
	bound := binder.New().Bind("/instanceof-intersection.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.undefined", issue.Code, issue.Message)
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
	linked := LinkMembers(analyzed, snapshot, root)
	var resolvedNames int
	for _, reference := range linked.References {
		if reference.Name == "getStorageName" ||
			reference.Name == "getPropertyName" {
			require.NotEmpty(t, reference.Resolved)
			resolvedNames++
		}
	}
	require.Equal(t, 5, resolvedNames)
}

func TestForeachIntersectionSurvivesMemberCallInsideArrayIndexArgument(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
interface StorageAware {
    public function getStorageName(): string;
}
class Field {}
class Query {
    public function setParameter(string $name, mixed $value): void {}
}
/** @param list<Field> $fields */
function write(array $fields, array $primaryKey, Query $query): void {
    foreach ($fields as $field) {
        if (!$field instanceof StorageAware) {
            continue;
        }
        $param = 'param';
		$primaryKey[$field->getStorageName()] = 'value';
        $query->setParameter($param, $primaryKey[$field->getStorageName()]);
    }
}
`).Tree.Root
	bound := binder.New().Bind("/array-index-flow.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	linked := LinkMembers(analyzed, snapshot, root)
	for _, reference := range linked.References {
		if reference.Name == "getStorageName" {
			require.NotEmpty(t, reference.Resolved)
		}
	}
}

func TestArrayShiftPreservesShapeElementForInterfaceNarrowing(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
interface SalesChannelDefinitionInterface {
    public function processCriteria(): void;
}
abstract class EntityDefinition {
    abstract public function getEntityName(): string;
}
function process(EntityDefinition $initial): void {
    if (!$initial instanceof SalesChannelDefinitionInterface) {
        return;
    }
    $queue = [['definition' => $initial]];
    while ($queue !== []) {
        $current = array_shift($queue);
        $definition = $current['definition'];
        if ($definition instanceof SalesChannelDefinitionInterface) {
            $definition->processCriteria();
            $definition->getEntityName();
        }
    }
}
`).Tree.Root
	bound := binder.New().Bind("/array-shift-shape.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	linked := LinkMembers(analyzed, snapshot, root)
	for _, reference := range linked.References {
		if reference.Name == "getEntityName" {
			require.NotEmpty(t, reference.Resolved)
		}
	}
}

func TestArrayShiftInsideNonEmptyLoopDoesNotAddNull(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Entity {
    public function get(string $property): mixed {}
}
function resolve(Entity $entity, string $path): void {
    $parts = explode('.', $path);
    while ($parts !== []) {
        $part = array_shift($parts);
        $entity->get($part);
    }
}
`).Tree.Root
	bound := binder.New().Bind("/array-shift-non-empty.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestArrayPopFromExplodeDoesNotAddNull(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function shortName(string $class): string {
    $parts = explode('\\', $class);
    return array_pop($parts);
}
`).Tree.Root
	bound := binder.New().Bind("/array-pop-explode.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestArrayPopAndShiftRespectNonEmptyListPHPDoc(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @param non-empty-list<string> $values */
function first(array $values): string {
    return array_shift($values);
}
/** @param non-empty-list<array{name: string}> $values */
function lastName(array $values): string {
    return array_pop($values)['name'];
}
`).Tree.Root
	bound := binder.New().Bind("/non-empty-list.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestStrictEmptyArrayComparisonNarrowsArrayToNonEmpty(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Tags {
    /** @var array<string, array{id: string}> */
    private array $values = [];

    /** @return non-empty-array<string, array{id: string}> */
    public function all(): array {
        if ($this->values !== []) {
            return $this->values;
        }

        /** @var non-empty-array<string, array{id: string}> $result */
        $result = loadTags();
        return $this->values = $result;
    }
}
function loadTags(): array {}
`).Tree.Root
	bound := binder.New().Bind("/non-empty-array-comparison.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestAppendingToNonEmptyTupleKeepsNonEmptyList(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/**
 * @param list<string> $parts
 * @return non-empty-list<string>
 */
function path(array $parts): array {
    $values = ['root'];
    foreach ($parts as $part) {
        $values[] = $part;
    }
    return $values;
}
`).Tree.Root
	bound := binder.New().Bind("/non-empty-tuple-append.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestArrayShiftInPositiveCountTernaryIsNonNull(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @param array<string, string> $values */
function identifier(array $values): array|string {
    return count($values) === 1 ? array_shift($values) : $values;
}
`).Tree.Root
	bound := binder.New().Bind("/count-array-shift.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestPregReplaceCallbackKnowsWholeMatchIsString(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function acceptsString(string $value): string { return $value; }
function replace(string $content): string {
    return (string) preg_replace_callback('/x/', static function (array $match): string {
        return acceptsString(str_replace('x', 'y', $match[0]));
    }, $content);
}
`).Tree.Root
	bound := binder.New().Bind("/preg-callback.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestForeachSupportsValueOnlyTraversablePHPDoc(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Item {}
class Items {
    /** @return Traversable<Item> */
    public function getIterator(): Traversable {}
}
function acceptsItem(Item $item): void {}
function consume(Items $items): void {
    foreach ($items->getIterator() as $item) {
        acceptsItem($item);
    }
}
`).Tree.Root
	bound := binder.New().Bind("/traversable-value.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestIssetNarrowingSurvivesTerminatingBranch(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Entity {
    private ?string $id = null;
    public function id(): string {
        if (!isset($this->id)) {
            return '';
        }
        return $this->id;
    }
}
`).Tree.Root
	bound := binder.New().Bind("/isset-property.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestVarExportReturnFlagProducesString(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function exportKey(string $key): string {
    return var_export($key, true);
}
`).Tree.Root
	bound := binder.New().Bind("/var-export.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestMultipleIssetOperandsNarrowEveryShapeOffset(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function acceptsString(string $value): void {}
/** @param array{from?: string, to?: string|null} $range */
function validateRange(array $range): void {
    if (!isset($range['from'], $range['to'])) {
        return;
    }
    acceptsString($range['from']);
    acceptsString($range['to']);
}
`).Tree.Root
	bound := binder.New().Bind("/multiple-isset.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestPredicateOnNullCoalesceNarrowsUnderlyingArrayOffset(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @param array<string, bool> $permissions */
function acceptsPermissions(array $permissions): void {}
/** @param array{permissions?: array<string, bool>|string|null} $options */
function configure(array $options): void {
    if (is_array($options['permissions'] ?? null)) {
        acceptsPermissions($options['permissions']);
    }
}
`).Tree.Root
	bound := binder.New().Bind("/coalesce-predicate.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestBooleanAliasCarriesObjectRefinementIntoTrueBranch(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class WriteCommand {}
class UpdateCommand extends WriteCommand {}
function updateOnly(UpdateCommand $command): void {}
function process(WriteCommand $command, bool $changed): void {
    $needsUpdate = $command instanceof UpdateCommand && $changed;
    if ($needsUpdate) {
        updateOnly($command);
    }
}
`).Tree.Root
	bound := binder.New().Bind("/boolean-alias.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestHasAccessorConventionNarrowsRepeatedGetter(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Field {}
class Fields {
    public function has(string $name): bool { return false; }
    public function get(string $name): ?Field { return null; }
}
class EntityExistence {
    public function hasEntityName(): bool { return false; }
    public function getEntityName(): ?string { return null; }
}
function acceptsField(Field $field): void {}
function acceptsName(string $name): void {}
function inspect(Fields $fields, EntityExistence $existence, string $key): void {
    if ($fields->has($key)) {
        acceptsField($fields->get($key));
    }
    if ($existence->hasEntityName()) {
        acceptsName($existence->getEntityName());
    }
}
`).Tree.Root
	bound := binder.New().Bind("/has-accessor.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestIsSubclassOfNarrowsClassStringWithoutTurningItIntoObject(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Command {}
/** @param class-string $class */
function packageName(string $class): void {}
/** @param array{class?: class-string} $trace */
function inspect(array $trace): void {
    if (isset($trace['class']) && is_subclass_of($trace['class'], Command::class)) {
        packageName($trace['class']);
    }
}
`).Tree.Root
	bound := binder.New().Bind("/is-subclass-class-string.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestDateTimeModifyWithStructuredRelativeTimeDoesNotReturnFalse(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function retry(\DateTimeImmutable $now, int $seconds): \DateTimeImmutable {
    return $now->modify(sprintf('+%d seconds', $seconds));
}
`).Tree.Root
	bound := binder.New().Bind("/date-modify.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestArrayMapPreservesListAndCallbackResult(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @param array<string, list<string>> $queries */
function total(array $queries): int {
    return array_sum(array_map('count', $queries));
}
/** @param array<string, mixed> $properties
 * @return list<string>
 */
function names(array $properties): array {
    return array_map(
        static fn (string|int $name): string => strtolower((string) $name),
        array_keys($properties),
    );
}
`).Tree.Root
	bound := binder.New().Bind("/array-map.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestIteratorToArrayPreservesIterableKeyAndValueTypes(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Item {}
/** @param iterable<string, Item> $items
 *  @return array<string, Item>
 */
function indexed(iterable $items): array {
    return iterator_to_array($items);
}
/** @param iterable<string, Item> $items
 *  @return list<Item>
 */
function values(iterable $items): array {
    return iterator_to_array($items, false);
}
/** @param iterable<string, Item> $items
 *  @return array<string, Item>
 */
function normalized(iterable $items): array {
    if ($items instanceof Traversable) {
        return iterator_to_array($items);
    }
    return $items;
}
`).Tree.Root
	bound := binder.New().Bind("/iterator-to-array.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestNegativeDateTimeImmutableCheckNarrowsEngineInterface(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function mutableDate(DateTimeInterface $value): DateTime {
    if ($value instanceof DateTimeImmutable) {
        return DateTime::createFromImmutable($value);
    }
    return $value;
}
`).Tree.Root
	bound := binder.New().Bind("/datetime-interface.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestConditionalReturnUsesCallableParameterArgument(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Groups {
    /**
     * @template TResult
     * @param callable(array<string, string>): TResult|null $mapper
     * @return ($mapper is callable ? array<string, list<TResult>> : array<string, list<array<string, string>>>)
     */
    public static function group(?callable $mapper = null): array {}
}
/** @return array<string, list<string>> */
function names(): array {
    return Groups::group(static fn (array $row): string => $row['name']);
}
`).Tree.Root
	bound := binder.New().Bind("/conditional-parameter.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestUnresolvedTemplateReturnDoesNotProduceDiagnostic(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/**
 * @template T of object
 * @return T
 */
function create(string $class): object {
    return new $class();
}
`).Tree.Root
	bound := binder.New().Bind("/template-return.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestUnconditionalAssertionsNarrowFollowingStatements(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Assert {
    /** @phpstan-assert !null $actual */
    public static function assertNotNull(mixed $actual): void {}
    /**
     * @template ExpectedType of object
     * @param class-string<ExpectedType> $expected
     * @phpstan-assert =ExpectedType $actual
     */
    public static function assertInstanceOf(string $expected, mixed $actual): void {}
}
class Entity {}
class TestCase extends Assert {
    public function notNull(?Entity $entity): Entity {
        static::assertNotNull($entity);
        return $entity;
    }
    public function instance(object $entity): Entity {
        static::assertInstanceOf(Entity::class, $entity);
        return $entity;
    }
}
`).Tree.Root
	bound := binder.New().Bind("/unconditional-assertions.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestPHPUnitAssertionsNarrowInsideTraits(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
namespace PHPUnit\Framework;
class Assert {
    /** @phpstan-assert !null $actual */
    public static function assertNotNull(mixed $actual): void {}
    /**
     * @template ExpectedType of object
     * @param class-string<ExpectedType> $expected
     * @phpstan-assert =ExpectedType $actual
     */
    public static function assertInstanceOf(string $expected, mixed $actual): void {}
}
namespace App;
class Entity {}
trait TestHelper {
    public function notNull(?Entity $entity): Entity {
        static::assertNotNull($entity);
        return $entity;
    }
    public function instance(object $entity): Entity {
        static::assertInstanceOf(Entity::class, $entity);
        return $entity;
    }
}
`).Tree.Root
	bound := binder.New().Bind("/trait-assertions.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestNeverReturningCallTerminatesFlow(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Consent {
    public ?string $acceptedUntil = null;
}
class Handler {
    private function fail(): never {
        throw new RuntimeException();
    }
    public function acceptedUntil(Consent $consent): string {
        if ($consent->acceptedUntil === null) {
            $this->fail();
        }
        return $consent->acceptedUntil;
    }
}
`).Tree.Root
	bound := binder.New().Bind("/never-flow.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestForeachPreservesExplicitGenericAcrossConcreteCollectionSubclasses(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/**
 * @template TKey
 * @template TValue
 */
interface IteratorAggregate {}
/**
 * @template T
 * @implements IteratorAggregate<array-key, T>
 */
abstract class Collection implements IteratorAggregate {}
class Field {}
class AssociationField extends Field {
    public function getMappingDefinition(): string {}
}
/** @extends Collection<Field> */
class FieldCollection extends Collection {}
class CompiledFieldCollection extends FieldCollection {}
function mappings(CompiledFieldCollection $fields): void {
    /** @var CompiledFieldCollection<AssociationField> $associations */
    $associations = $fields;
    foreach ($associations as $association) {
        $association->getMappingDefinition();
    }
}
`).Tree.Root
	bound := binder.New().Bind("/concrete-collection.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.undefined", issue.Code, issue.Message)
	}
}

func TestSwitchTrueNarrowsConsecutiveInstanceofCases(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
abstract class Field {}
class ParentField extends Field {
    public function getStorageName(): string {}
}
class AssociationField extends Field {
    public function getStorageName(): string {}
}
function storageName(Field $field): string {
    switch (true) {
        case $field instanceof ParentField:
        case $field instanceof AssociationField:
            return $field->getStorageName();
        default:
            return '';
    }
}
`).Tree.Root
	bound := binder.New().Bind("/switch-true.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	linked := LinkMembers(analyzed, snapshot, root)

	for _, reference := range linked.References {
		if reference.Kind == semantic.MemberName &&
			reference.Name == "getStorageName" {
			require.NotEmpty(t, reference.CandidateIDs())
			require.Equal(
				t,
				"AssociationField|ParentField",
				reference.Receiver.String(),
			)
		}
	}
}

func TestExactClassIdentityNarrowsObjectReceiver(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
abstract class Field {}
class JsonField extends Field {
    public function getPropertyMapping(): array {}
}
function mapping(Field $field): array {
    if ($field::class === JsonField::class) {
        return $field->getPropertyMapping();
    }
    return [];
}
`).Tree.Root
	bound := binder.New().Bind("/class-identity.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	linked := LinkMembers(analyzed, snapshot, root)
	for _, reference := range linked.References {
		if reference.Name == "getPropertyMapping" {
			require.NotEmpty(t, reference.Resolved)
		}
	}
}

func TestConditionalMethodAssertionNarrowsReceiver(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
abstract class Test {
    /** @phpstan-assert-if-true TestMethod $this */
    public function isTestMethod(): bool { return false; }
}
class TestMethod extends Test {
    public function className(): string { return ''; }
}
function prepare(Test $test): string {
    if (!$test->isTestMethod()) {
        return '';
    }
    return $test->className();
}
`).Tree.Root
	bound := binder.New().Bind("/conditional-assertion.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	linked := LinkMembers(analyzed, snapshot, root)
	for _, reference := range linked.References {
		if reference.Name == "className" {
			require.NotEmpty(t, reference.Resolved)
			require.Equal(t, "TestMethod", reference.Receiver.String())
		}
	}
}

func TestConditionalMethodAssertionNarrowsRepeatedReceiverMethod(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class ClassReflection {}
interface Scope {
    /** @phpstan-assert-if-true !null $this->getClassReflection() */
    public function isInClass(): bool;
    public function getClassReflection(): ?ClassReflection;
}
function acceptsClass(ClassReflection $class): void {}
function inspect(Scope $scope): void {
    if (!$scope->isInClass()) {
        return;
    }
    acceptsClass($scope->getClassReflection());
}
`).Tree.Root
	bound := binder.New().Bind("/method-result-assertion.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(
		t,
		analyzed,
		root,
		"$scope->getClassReflection()",
		"ClassReflection",
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestArrayDestructuringUsesTupleElementTypes(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @return array{0: list<string>, 1: string} */
function splitIds(string $ids): array { return [[], '']; }
/** @param list<string> $ids */
function acceptsIds(array $ids): void {}
function migrate(string $source): void {
    [$ids, $cacheKey] = splitIds($source);
    acceptsIds($ids);
    strlen($cacheKey);
}
`).Tree.Root
	bound := binder.New().Bind("/array-destructuring.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestMatchClassArmNarrowsSelectorObject(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
abstract class Aggregation {}
class FilterAggregation extends Aggregation {}
function restrict(FilterAggregation $aggregation): Aggregation { return $aggregation; }
function sanitize(Aggregation $aggregation): Aggregation {
    return match ($aggregation::class) {
        FilterAggregation::class => restrict($aggregation),
        default => $aggregation,
    };
}
`).Tree.Root
	bound := binder.New().Bind("/match-class-arm.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestNullCoalescingArrayAssignmentPreservesExistingRowShape(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/**
 * @param list<array{salesChannelId?: string|null, routeName: string, foreignKey: string, isModified?: bool}> $rows
 */
function firstRoute(array $rows): string {
    $grouped = [];
    foreach ($rows as $row) {
        $id = $row['salesChannelId'] ?? null;
        $row['isModified'] ??= true;
        $grouped[$id][] = $row;
    }
    foreach ($grouped as $group) {
        return $group[0]['routeName'];
    }
    return '';
}
`).Tree.Root
	bound := binder.New().Bind("/coalescing-array-assignment.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestArrayFilterArrowInstanceofNarrowsElementType(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
abstract class Field {}
class AssociationField extends Field {
    public function getReferenceDefinition(): string {}
}
/** @param list<Field> $fields */
function reference(array $fields): string {
    $associations = array_filter(
        $fields,
        static fn (Field $field) => $field instanceof AssociationField,
    );
    $last = $associations[count($associations) - 1];
    return $last->getReferenceDefinition();
}
`).Tree.Root
	bound := binder.New().Bind("/array-filter-arrow.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	linked := LinkMembers(analyzed, snapshot, root)
	for _, reference := range linked.References {
		if reference.Name == "getReferenceDefinition" {
			require.NotEmpty(t, reference.Resolved)
		}
	}
}

func TestCollectionFilterArrowNarrowsGenericElementType(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
namespace Shopware\Core\Framework\Struct;
/** @template T */
class Collection {
    /**
     * @param callable(T): bool $predicate
     * @return static
     */
    public function filter(callable $predicate): static {}
}
namespace App;
use Shopware\Core\Framework\Struct\Collection;
abstract class Command {}
class Login extends Command {}
class Register extends Command {}
/**
 * @template T of Command = Command
 * @extends Collection<T>
 */
class CommandCollection extends Collection {
    /** @return self<Login|Register> */
    public function tokenCommands(): self {
        return $this->filter(
            static fn (Command $command): bool =>
                $command instanceof Login || $command instanceof Register,
        );
    }
}
`).Tree.Root
	bound := binder.New().Bind("/collection-filter.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestInheritedMethodTemplateDoesNotCollideWithChildTemplate(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @template TElement */
class Collection {
    /**
     * @template T
     * @param callable(TElement): T $callback
     * @return array<array-key, T>
     */
    public function map(callable $callback): array {}
}

abstract class Command {
    public static function name(): string {}
}
/**
 * @template T of Command = Command
 * @extends Collection<T>
 */
class Commands extends Collection {
    /** @return array<string> */
    public function names(): array {
        return $this->map(static fn (Command $command): string => $command::name());
    }
}
`).Tree.Root
	bound := binder.New().Bind("/method-template-shadow.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestMemberCallOnUnionGenericCollectionsReturnsUnionElements(t *testing.T) {
	t.Parallel()
	source := `<?php
class A {}
class B extends A {}
/**
 * @template T
 * @template TKey of array-key = array-key
 */
class Items {
	/**
	 * @param TKey $key
	 * @return T|null
	 */
    public function get(mixed $key): mixed {}
}
/**
 * @template T
 * @extends Items<T, string>
 */
class GenericItems extends Items {}
/** @extends GenericItems<A> */
class AItems extends GenericItems {}
/** @param GenericItems<B> $b
 * @param list<string> $ids
 * @return array<string, A|B>
 */
function find(bool $flag, AItems $a, Items $b, array $ids): array {
    if ($flag) {
        $items = $a;
    } else {
        $items = $b;
    }
    $result = [];
    foreach ($ids as $id) {
        if ($items->get($id) === null) {
            continue;
        }
        $result[$id] = $items->get($id);
    }
    return $result;
}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/union-collections.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertNodeType(t, analyzed, root, phpsyntax.PhpMemberCall, "A|B")
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestDeepGenericBuilderReturnsToParent(t *testing.T) {
	t.Parallel()
	source := `<?php
interface NodeParentInterface {}
/** @template-covariant TParent of NodeParentInterface|null = null */
abstract class NodeDefinition implements NodeParentInterface {
    /** @return TParent */
    public function end(): ?NodeParentInterface {}
}
/**
 * @template TParent of NodeParentInterface|null = null
 * @extends NodeDefinition<TParent>
 */
class ScalarNodeDefinition extends NodeDefinition {}
/**
 * @template TParent of NodeParentInterface|null = null
 * @extends NodeDefinition<TParent>
 */
class ArrayNodeDefinition extends NodeDefinition {
    /** @return NodeBuilder<static> */
    public function children(): NodeBuilder {}
}
/** @template TParent of NodeParentInterface|null = null */
class NodeBuilder implements NodeParentInterface {
    /** @return ArrayNodeDefinition<$this> */
    public function arrayNode(string $name): ArrayNodeDefinition {}
    /** @return ScalarNodeDefinition<$this> */
    public function scalarNode(string $name): ScalarNodeDefinition {}
    /** @return TParent */
    public function end(): ?NodeParentInterface {}
}
function build(NodeBuilder $root): void {
	$root
` + strings.Repeat("        ->arrayNode('nested')->children()\n", 40) +
		strings.Repeat("        ->end()->end()\n", 40) + `
        ->scalarNode('sibling');
}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/deep-builder.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	linked := LinkMembers(analyzed, snapshot, root)
	var siblingResolved bool
	siblingReceiver := types.Unknown()
	for _, reference := range linked.References {
		if reference.Name == "scalarNode" &&
			reference.Range.Start == uint32(strings.LastIndex(source, "scalarNode")) {
			siblingResolved = reference.Resolved != "" ||
				len(reference.CandidateIDs()) != 0
			siblingReceiver = reference.Receiver
		}
	}
	require.True(t, siblingResolved, siblingReceiver.String())
}

func TestLogicalRightAssignmentPreservesCallArguments(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Context {}
class Media {
    public function getHash(): ?string {}
}
class Service {
    private function find(string $hash, Context $context): ?string {}

    public function upload(Media $media, Context $context): string {
        if ($media->getHash() && $existing = $this->find($media->getHash(), $context)) {
            return $existing;
        }

        return '';
    }
}
`).Tree.Root
	bound := binder.New().Bind("/logical-assignment.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
		require.NotEqual(t, "php.undefinedVariable", issue.Code, issue.Message)
	}
}

func TestLongLogicalChainDoesNotReanalyzePrefixes(t *testing.T) {
	t.Parallel()
	source := `<?php
function allTruthy(?string $value): bool {
    return ` + strings.Repeat("$value && ", 24) + `$value;
}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/logical-chain.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	require.Empty(t, analyzed.Issues)
}

func TestGenericConstructorUsesClassTemplateDefault(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/**
 * @template T of string|array<string, string> = string
 */
class Criteria {
    /** @param array<T>|null $ids */
    public function __construct(?array $ids = null) {}
}
class Defaults {
    public const string CURRENCY = 'currency';
}
function criteria(): Criteria {
    return new Criteria([Defaults::CURRENCY]);
}
function compoundCriteria(): Criteria {
    return new Criteria([['id' => 'first'], 'second']);
}
`).Tree.Root
	bound := binder.New().Bind("/generic-constructor.php", 1, root)
	constructor := symbolNamed(t, bound, "__construct")
	require.Equal(
		t,
		"array<array-key,string>|null",
		types.Substitute(
			constructor.Parameters[0].Type,
			map[string]types.Type{"T": types.String()},
		).String(),
	)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	linked := binder.Link(bound, snapshot)
	linkedConstructor := symbolNamed(t, linked, "__construct")
	require.Equal(
		t,
		"array<array-key,string>|null",
		types.Substitute(
			linkedConstructor.Parameters[0].Type,
			map[string]types.Type{"T": types.String()},
		).String(),
	)
	analyzed := New(snapshot).Analyze(linked, root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestClassTemplateRefinesNativeMethodReturn(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class EntityCollection {}
class ProductCollection extends EntityCollection {}
/**
 * @template TCollection of EntityCollection
 */
class SearchResult {
    /** @return TCollection */
    public function getEntities(): EntityCollection {}
}
/** @return SearchResult<ProductCollection> */
function search(): SearchResult {}
function products(): ProductCollection {
    return search()->getEntities();
}
`).Tree.Root
	bound := binder.New().Bind("/generic-method-return.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
	assertTextType(
		t,
		analyzed,
		root,
		"search()->getEntities()",
		"ProductCollection",
	)
}

func TestDynamicClassStringObjectCreationReturnsObjectType(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Product {}
/** @return class-string<Product> */
function productClass(): string {}
function createProduct(): Product {
    $class = productClass();
    return new $class();
}
`).Tree.Root
	bound := binder.New().Bind("/dynamic-object.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
	assertTextType(t, analyzed, root, "new $class()", "Product")
}

func TestFirstClassCallableUsesDeclaredSignature(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function predicate(mixed $value): bool {
    return true;
}
function consume(callable $callback): void {}
function run(): void {
    consume(predicate(...));
}
`).Tree.Root
	bound := binder.New().Bind("/first-class-callable.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
	assertTextType(
		t,
		analyzed,
		root,
		"predicate(...)",
		"callable(mixed $value):bool",
	)
}

func TestFirstClassCallableDetectionDoesNotAllocate(t *testing.T) {
	root := phpparser.Parse(`<?php
predicate(...);
predicate($value);
predicate(...$values);
`).Tree.Root
	calls := phpquery.Calls(root)
	require.Len(t, calls, 3)
	require.True(t, isFirstClassCallable(calls[0]))
	require.False(t, isFirstClassCallable(calls[1]))
	require.False(t, isFirstClassCallable(calls[2]))

	allocations := testing.AllocsPerRun(1000, func() {
		if !isFirstClassCallable(calls[0]) {
			panic("first-class callable was not detected")
		}
	})
	require.Zero(t, allocations)
}

func TestArrayFilterFirstClassPredicateNarrowsElements(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @param mixed[] $values
 *  @return list<string>
 */
function stringsOnly(array $values): array {
    return array_values(array_filter($values, is_string(...)));
}
`).Tree.Root
	bound := binder.New().Bind("/array-filter-predicate.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestArrayFilterNullPredicatePreservesShape(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @return array{request: string, response?: string} */
function response(string $request, ?string $response): array {
    return array_filter([
        'request' => $request,
        'response' => $response,
    ], static fn ($value) => $value !== null);
}
`).Tree.Root
	bound := binder.New().Bind("/array-filter-shape.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
	assertTextType(
		t,
		analyzed,
		root,
		"array_filter([\n        'request' => $request,\n        'response' => $response,\n    ], static fn ($value) => $value !== null)",
		"array{request:string,response?:string}",
	)
}

func TestEnvironmentArenaPreservesForkIsolationAcrossBlocks(t *testing.T) {
	t.Parallel()

	var arena environmentArena
	original := newEnvironmentIn(&arena, 1)
	original.set("$value", types.Int())

	forks := make([]environment, 20)
	handles := make(map[*environmentHandle]struct{}, len(forks)+1)
	handles[original.handle] = struct{}{}
	for index := range forks {
		forks[index] = cloneEnvironmentIn(&arena, original)
		handles[forks[index].handle] = struct{}{}
	}
	require.Len(t, handles, len(forks)+1)

	for index := range forks {
		forks[index].set("$value", types.LiteralInt(strconv.Itoa(index)))
	}
	originalValue, found := original.get("$value")
	require.True(t, found)
	require.Equal(t, types.Int(), originalValue)
	for index := range forks {
		value, ok := forks[index].get("$value")
		require.True(t, ok)
		require.Equal(t, strconv.Itoa(index), value.String())
	}
}

func TestCallArgumentArenaKeepsRecursiveSlicesStable(t *testing.T) {
	t.Parallel()

	var arena callArgumentArena
	require.Nil(t, arena.allocate(0))

	var retained [][]CallArgument
	for allocation := 0; allocation < 40; allocation++ {
		count := allocation%5 + 1
		arguments := arena.allocate(count)
		require.Len(t, arguments, count)
		require.Equal(t, len(arguments), cap(arguments))
		for index := range arguments {
			arguments[index] = CallArgument{
				Name: "$" + strconv.Itoa(allocation) + "_" + strconv.Itoa(index),
				Type: types.Int(),
			}
		}
		retained = append(retained, arguments)
	}
	for allocation, arguments := range retained {
		for index, argument := range arguments {
			require.Equal(
				t,
				"$"+strconv.Itoa(allocation)+"_"+strconv.Itoa(index),
				argument.Name,
			)
			require.Equal(t, types.Int(), argument.Type)
		}
	}

	large := arena.allocate(callArgumentBlockSize + 1)
	require.Len(t, large, callArgumentBlockSize+1)
	require.Equal(t, len(large), cap(large))
}

func TestDynamicArrayAssignmentPreservesStringKeyType(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @param list<string> $values
 *  @return list<string>
 */
function normalize(array $values): array {
    $normalized = [];
    foreach ($values as $value) {
        $normalized[$value] = true;
    }
    return array_keys($normalized);
}
/** @param list<string> $values
 *  @return list<string>
 */
function collect(array $values): array {
    $result = [];
    foreach ($values as $value) {
        $result[] = $value;
    }
    return $result;
}
`).Tree.Root
	bound := binder.New().Bind("/array-key-assignment.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
	assertTextType(
		t,
		analyzed,
		root,
		"array_keys($normalized)",
		"list<string>",
	)
}

func TestJSONEncodeThrowFlagRemovesFalseReturn(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function encode(mixed $value): string {
    return json_encode($value, \JSON_THROW_ON_ERROR);
}
`).Tree.Root
	bound := binder.New().Bind("/json-encode.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
	assertTextType(
		t,
		analyzed,
		root,
		`json_encode($value, \JSON_THROW_ON_ERROR)`,
		"string",
	)
}

func TestStringPositionGuardFlowsIntoSubstringOffset(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function suffix(string $value): string {
    $position = strpos($value, ':');
    if ($position === false) {
        return '';
    }

    return substr($value, $position + 1);
}
`).Tree.Root
	bound := binder.New().Bind("/string-functions.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	assertTextType(
		t,
		analyzed,
		root,
		"substr($value, $position + 1)",
		"string",
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestStringReplacementReturnFollowsSubject(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function normalized(string $value): string {
    return str_replace('_', '-', $value);
}
function normalizedMany(array $values): array {
    return str_ireplace('_', '-', $values);
}
function replacedRange(string $value): string {
    return substr_replace($value, '-', 1, 1);
}
function normalizedNullable(?string $value): string {
    return str_replace('_', '-', $value);
}
/** @param array-key $value */
function normalizedKey($value): string {
    return str_replace('_', '-', $value);
}
function normalizedUnknown(mixed $input): mixed {
    return str_replace('_', '-', $input);
}
/** @param list<string> $values */
function lastValue(array $values): ?string {
    return array_last($values);
}
function regexNormalized(string $value): string {
    $result = preg_replace('/_/', '-', $value);
    if ($result === null) {
        return '';
    }
    return $result;
}
`).Tree.Root
	bound := binder.New().Bind("/string-replacement.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 5}),
	})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)
	assertTextType(t, analyzed, root, "str_replace('_', '-', $value)", "string")
	assertTextType(
		t,
		analyzed,
		root,
		"str_ireplace('_', '-', $values)",
		"array<mixed,string>",
	)
	assertTextType(t, analyzed, root, "substr_replace($value, '-', 1, 1)", "string")
	assertTextType(t, analyzed, root, "str_replace('_', '-', $value)", "string")
	assertTextType(t, analyzed, root, "array_last($values)", "null|string")
	assertTextType(t, analyzed, root, "str_replace('_', '-', $input)", "mixed")
	assertTextType(t, analyzed, root, "preg_replace('/_/', '-', $value)", "null|string")
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestArrayAssignmentPreservesAliasesAndUnionShapes(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @phpstan-type Module array{name: string, source?: string|null} */
class App {
    /** @return Module */
    public function module(): array {}
}
/** @return array{name: string, source: string} */
function module(App $app): array {
    $module = $app->module();
    $module['source'] = 'url';
    return $module;
}
/** @return array{height: int, id: string, width: int} */
function size(bool $large): array {
    $size = $large
        ? ['height' => 800, 'width' => 800]
        : ['height' => 400, 'width' => 400];
    $size['id'] = 'id';
    return $size;
}
`).Tree.Root
	bound := binder.New().Bind("/assigned-alias-shape.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestArraySpreadPreservesStringKeyShape(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @return array{id: string, email: string} */
function base(): array {}
/** @return array{id: string, email: string, active: bool} */
function row(): array {
    return [...base(), 'active' => true];
}
`).Tree.Root
	bound := binder.New().Bind("/spread-shape.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(
		t,
		analyzed,
		root,
		"[...base(), 'active' => true]",
		"array{active:true,email:string,id:string}",
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestConditionalLiteralKeyAssignmentsProduceOptionalShapeFields(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/**
 * @return array{languageId?: string, currencyId?: string}
 */
function parameters(bool $language, bool $currency): array {
    $result = [];
    if ($language) {
        $result['languageId'] = 'language';
    }
    if ($currency) {
        $result['currencyId'] = 'currency';
    }
    return $result;
}
`).Tree.Root
	bound := binder.New().Bind("/optional-shape.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestBuiltinDependentReturnTypes(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @param list<string> $values */
function guardedLast(array $values): string {
    if (!$values) {
        throw new RuntimeException();
    }
    return array_last($values);
}
/** @param list<string> $values */
function guardedShift(array $values): string {
    if ($values !== []) {
        return array_shift($values);
    }
    return '';
}
function compares(string $left, string $right): bool {
    return version_compare($left, $right, '>');
}
function loads(): object {
    $loader = require __DIR__ . '/autoload.php';
    return $loader;
}
function printed(mixed $value): string {
    return print_r($value, true);
}
function expires(DateTimeImmutable $now): DateTimeImmutable {
    return $now->modify('+120 minutes');
}
function expiresDynamic(DateTimeImmutable $now, int $seconds): DateTimeImmutable {
    return $now->modify('+' . $seconds . ' seconds');
}
/** @param list<string> $values */
function reduced(array $values): int {
    return array_reduce($values, static fn (int $count, string $value): int => $count + 1, 0);
}
/** @param array<string, string> $values */
function flipped(array $values, string $id): string {
    return array_flip($values)[$id];
}
`).Tree.Root
	bound := binder.New().Bind("/dependent-builtins.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 5}),
	})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	assertTextType(t, analyzed, root, "array_last($values)", "string")
	assertTextType(t, analyzed, root, "array_shift($values)", "string")
	assertTextType(t, analyzed, root, "version_compare($left, $right, '>')", "bool")
	assertTextType(t, analyzed, root, "require __DIR__ . '/autoload.php'", "mixed")
	assertTextType(t, analyzed, root, "print_r($value, true)", "string")
	assertTextType(t, analyzed, root, "$now->modify('+120 minutes')", "DateTimeImmutable")
	assertTextType(
		t,
		analyzed,
		root,
		"$now->modify('+' . $seconds . ' seconds')",
		"DateTimeImmutable",
	)
	assertTextType(t, analyzed, root, "array_flip($values)", "array<string,string>")
	assertTextType(
		t,
		analyzed,
		root,
		"array_reduce($values, static fn (int $count, string $value): int => $count + 1, 0)",
		"int",
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestBuiltinConfigurationAndURLReturnTypes(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function acceptsString(string $value): void {}
function memoryLimit(): void {
    acceptsString(ini_get('memory_limit'));
}
function unknownIniOption(string $option): string|false {
    return ini_get($option);
}
function missingIniOption(): string|false {
    return ini_get('shopware_lsp.missing');
}
function parseQuery(string $dsn): void {
    $query = parse_url($dsn, PHP_URL_QUERY);
    if ($query === false || $query === null) {
        return;
    }
    $result = [];
    parse_str($query, $result);
}
function queryComponent(string $dsn): string|false|null {
    return parse_url($dsn, PHP_URL_QUERY);
}
function portComponent(string $dsn): int|false|null {
    return parse_url($dsn, PHP_URL_PORT);
}
function dynamicComponent(string $dsn, int $component): array|int|string|false|null {
    return parse_url($dsn, $component);
}
`).Tree.Root
	bound := binder.New().Bind("/builtin-config-url.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 5}),
	})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	assertTextType(t, analyzed, root, "ini_get('memory_limit')", "string")
	assertTextType(t, analyzed, root, "ini_get($option)", "false|string")
	assertTextType(
		t,
		analyzed,
		root,
		"ini_get('shopware_lsp.missing')",
		"false|string",
	)
	assertTextType(
		t,
		analyzed,
		root,
		"parse_url($dsn, PHP_URL_QUERY)",
		"false|null|string",
	)
	assertTextType(
		t,
		analyzed,
		root,
		"parse_url($dsn, PHP_URL_PORT)",
		"false|int|null",
	)
	assertTextType(
		t,
		analyzed,
		root,
		"parse_url($dsn, $component)",
		"array{fragment:string,host:string,pass:string,path:string,port:int,query:string,scheme:string,user:string}|false|int|null|string",
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestEmptyGuardNarrowsOptionalArrayField(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @param array{security_eol?: string} $support */
function securityEol(array $support): DateTime {
    if (empty($support['security_eol'])) {
        throw new RuntimeException();
    }
    return new DateTime($support['security_eol']);
}
`).Tree.Root
	bound := binder.New().Bind("/empty-guard.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 5}),
	})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestBuiltinStringCollectionReturnTypes(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @return list<string> */
function splitIds(string $ids): array {
    return explode(',', $ids);
}
function fileExtension(string $path): string {
    return pathinfo($path, PATHINFO_EXTENSION);
}
function allPathParts(string $path): array|string {
    return pathinfo($path, PATHINFO_ALL);
}
`).Tree.Root
	bound := binder.New().Bind("/builtin-string-collections.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	assertTextType(t, analyzed, root, "explode(',', $ids)", "list<string>")
	assertTextType(
		t,
		analyzed,
		root,
		"pathinfo($path, PATHINFO_EXTENSION)",
		"string",
	)
	assertTextType(
		t,
		analyzed,
		root,
		"pathinfo($path, PATHINFO_ALL)",
		"array{basename:string,dirname:string,extension:string,filename:string}|string",
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestShorthandTernaryExcludesFalsyConditionType(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function pieces(string $value): array {
    return preg_split('/\s+/', $value, -1, PREG_SPLIT_NO_EMPTY) ?: [$value];
}
function lastPiece(string $value): ?string {
    return array_last(preg_split('/\s+/', $value) ?: [$value]);
}
`).Tree.Root
	bound := binder.New().Bind("/shorthand-ternary.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 5}),
	})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	assertTextType(
		t,
		analyzed,
		root,
		"preg_split('/\\s+/', $value, -1, PREG_SPLIT_NO_EMPTY) ?: [$value]",
		"list<string>",
	)
	assertTextType(
		t,
		analyzed,
		root,
		"array_last(preg_split('/\\s+/', $value) ?: [$value])",
		"null|string",
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestOverrideBodyUsesSpecializedInheritedParameter(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Command {}
class ConcreteCommand extends Command {
    public string $iso = 'EUR';
}
/** @template TCommand of Command */
abstract class Handler {
    /** @param TCommand $command */
    abstract public function handle(Command $command): void;
}
/** @extends Handler<ConcreteCommand> */
class ConcreteHandler extends Handler {
    public function handle(Command $command): void {
        strlen($command->iso);
    }
}
`).Tree.Root
	bound := binder.New().Bind("/specialized-override.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	assertTextType(t, analyzed, root, "$command->iso", "string")
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestGeneratedStubMismatchFallsBackWithoutDiagnostic(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function consume(): string {
    return generated_builtin(1);
}
`).Tree.Root
	bound := binder.New().Bind("/generated-stub.php", 1, root)
	stub := &semantic.Document{
		Path: "phpstub://test/generated",
		Symbols: []semantic.Symbol{{
			ID:             semantic.NewSymbolID(semantic.FunctionSymbol, "generated_builtin", "phpstub://test/generated", 0),
			Kind:           semantic.FunctionSymbol,
			Name:           "generated_builtin",
			FullyQualified: "generated_builtin",
			Path:           "phpstub://test/generated",
			Flags: semantic.InternalFlag |
				semantic.GeneratedStubFlag,
			ReturnType: types.String(),
			Parameters: []semantic.Parameter{{
				Name: "$value",
				Type: types.String(),
			}},
		}},
	}
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound, stub})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	assertTextType(t, analyzed, root, "generated_builtin(1)", "string")
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestUnpackedListUsesElementTypeForVariadicParameter(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Constraint {}
class Required extends Constraint {}
class Field {
    /** @return static */
    public function addFlags(Constraint ...$constraints): static {}
}
function add(string $name, Constraint ...$constraints): void {}
function configure(): Field {
    $constraints = [new Required()];
    add('name', ...$constraints);
    return (new Field())->addFlags(...$constraints);
}
`).Tree.Root
	bound := binder.New().Bind("/unpacked-variadic.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
	assertTextType(
		t,
		analyzed,
		root,
		"(new Field())->addFlags(...$constraints)",
		"Field",
	)
}

func TestArrayFilterWithoutCallbackNarrowsFalsyElements(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Flag {}
class Required extends Flag {}
class ApiAware extends Flag {}
class Field {
    public function addFlags(Flag ...$flags): void {}
}
function configure(Field $field, ?Required $required, ?ApiAware $apiAware): void {
    $flags = array_filter([$required, $apiAware]);
    $field->addFlags(...$flags);
}
`).Tree.Root
	bound := binder.New().Bind("/array-filter-falsy.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	assertTextType(
		t,
		analyzed,
		root,
		"array_filter([$required, $apiAware])",
		"array{0?:Required,1?:ApiAware}",
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestPredicateNarrowsRepeatedMemberExpressions(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Value {}
function consumeString(string $value): void {}
function consumeValue(?Value $value): void {}
class Consumer {
    public Value|string|null $left;
    public Value|string|null $right;

    public function run(): void {
        if (is_string($this->left)) {
            consumeString($this->left);
        }
        if (is_string($this->left) || is_string($this->right)) {
            return;
        }
        consumeValue($this->left);
        consumeValue($this->right);
    }
}
`).Tree.Root
	bound := binder.New().Bind("/member-predicate.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestMatchTrueNarrowsEachArm(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
abstract class Filter {}
class EqualsFilter extends Filter {
    public function getField(): string {}
}
class RangeFilter extends Filter {
    public function getParameters(): array {}
}
function serializeFilter(Filter $filter): mixed {
    return match (true) {
        $filter instanceof EqualsFilter => $filter->getField(),
        $filter instanceof RangeFilter => $filter->getParameters(),
        default => null,
    };
}
`).Tree.Root
	bound := binder.New().Bind("/match-true.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.undefined", issue.Code, issue.Message)
	}
}

func TestLiteralInferenceDerivesWithoutRecordedFact(t *testing.T) {
	source := `<?php "stable literal";`
	root := phpparser.Parse(source).Tree.Root
	document := binder.New().Bind("/literal.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	state := functionState{
		analyzerState: &analyzerState{
			analyzer:  New(snapshot),
			document:  document,
			relations: snapshot.Relations(),
			issues:    make(map[string]struct{}),
		},
		currentClass: types.Unknown(),
	}
	literals := phpquery.Nodes(root, phpsyntax.PhpString)
	require.Len(t, literals, 1)
	environment := newEnvironment(0)

	require.Equal(t, `"stable literal"`, state.infer(literals[0], environment).String())
	_, recorded := document.TypeFact(semantic.NodeIdentity(literals[0]))
	require.False(t, recorded)
	fact := document.TypeOf(literals[0])
	require.Equal(t, `"stable literal"`, fact.Type.String())
	require.Equal(t, semantic.LiteralSource, fact.Source)
	allocations := testing.AllocsPerRun(100, func() {
		_ = state.infer(literals[0], environment)
	})
	require.LessOrEqual(t, allocations, float64(1))
}

func TestJoinEnvironmentsGrowsForDisjointBranches(t *testing.T) {
	t.Parallel()
	left := newEnvironment(5)
	right := newEnvironment(5)
	for index := 0; index < 5; index++ {
		left.set("$left"+strconv.Itoa(index), types.Int())
		right.set("$right"+strconv.Itoa(index), types.String())
	}
	left.set("$shared", types.Int())
	right.set("$shared", types.String())

	joined := joinEnvironments(types.Relations{}, left, right)
	require.Equal(t, 11, joined.len())
	require.NotNil(t, joined.handle.table)
	shared, found := joined.get("$shared")
	require.True(t, found)
	require.Equal(t, "int|string", shared.String())
}

func TestExpressionAndFlowInference(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;

class Repository {
    public function find(string $id): Product|null {}
}
class Product {
    public string $name;
}
class Service {
    public function run(Repository $repository, string|null $id): string {
        if ($id === null) {
            return 'missing';
        }
        $product = $repository->find($id);
        if (!$product instanceof Product) {
            return 'unknown';
        }
        $names = [$product->name, 'fallback'];
        return $names[0];
    }
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/service.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	linked := binder.Link(bound, snapshot)
	analyzed := New(snapshot, Builtins).Analyze(linked, root)

	require.Equal(t, "string", symbolNamed(t, analyzed, "run").ReturnType.String())
	assertNodeType(t, analyzed, root, phpsyntax.PhpMemberCall, "App\\Product|null")
	assertTextType(t, analyzed, root, "$product->name", "string")
	assertTextType(t, analyzed, root, "$names[0]", "string")
}

func TestGenericCallInference(t *testing.T) {
	t.Parallel()
	source := `<?php
function identity(T $value): T { return $value; }
function useIt(): string { return identity('value'); }
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/generic.php", 1, root)
	for index := range bound.Symbols {
		if bound.Symbols[index].Name == "identity" {
			bound.Symbols[index].Parameters[0].Type = types.Template("T")
			bound.Symbols[index].ReturnType = types.Template("T")
		}
	}
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(bound, root)
	assertTextType(t, analyzed, root, "identity('value')", `"value"`)
}

func TestConstructorValidationAndGenericInference(t *testing.T) {
	t.Parallel()
	source := `<?php
/**
 * @template T of object
 */
class Box {
    /** @param T $value */
    public function __construct($value) {}
    /** @return T */
    public function value(): object { return $this->value; }
}
class Product {}
function valid(): Box { return new Box(new Product()); }
function invalid(): Box { return new Box('wrong'); }
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/constructor.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	constructors := (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("Box"),
		"__construct",
	)
	require.Len(t, constructors, 1)
	require.Equal(t, "T", constructors[0].Symbol.Parameters[0].Type.String())
	require.Len(t, constructors[0].Symbol.Templates, 1)
	require.Equal(t, "object", constructors[0].Symbol.Templates[0].Bound.String())
	require.False(t, resolver.ResolveSignature(
		snapshot.Relations(),
		constructors[0].Symbol,
		[]resolver.Argument{{Type: types.LiteralString("wrong")}},
	).Compatible)
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(t, analyzed, root, "new Box(new Product())", "Box<Product>")
	var argumentIssues int
	for _, issue := range analyzed.Issues {
		if issue.Code == "php.arguments" {
			argumentIssues++
		}
	}
	require.Equal(t, 1, argumentIssues)
}

func TestTupleAndUnknownLengthArgumentUnpacking(t *testing.T) {
	t.Parallel()
	source := `<?php
class DomainException {
    public function __construct(int $status, string $code, string $message, array $parameters) {}
}
function exception(string $label, ?string $code): DomainException {
    if ($code === null) {
        $messages = ['Invalid {{ label }}', ['label' => $label]];
    } else {
        $messages = ['Invalid {{ code }}', ['code' => $code]];
    }
    return new DomainException(400, 'invalid', ...$messages);
}
abstract class Command {
    /** @param array<array-key, mixed> $payload */
    public static function create(array $payload): static {
        return new static(...$payload);
    }
}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/argument-unpack.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestDefaultedMethodTemplateFlowsThroughChainedGenericReturn(t *testing.T) {
	t.Parallel()
	source := `<?php
/**
 * @template IDStructure of string|array<string, string> = string
 */
class Criteria {}

/**
 * @template IDStructure of string|array<string, string> = string
 */
class IdSearchResult {
    /** @return list<IDStructure> */
    public function getIds(): array {}
}

class Repository {
    /**
     * @template IDStructure of string|array<string, string> = string
     * @param Criteria<IDStructure> $criteria
     * @return IdSearchResult<IDStructure>
     */
    public function searchIds(Criteria $criteria): IdSearchResult {}
}

class Consumer {
    /** @return list<string> */
    public function getIds(Repository $repository): array {
        $criteria = new Criteria();
        return $repository->searchIds($criteria)->getIds();
    }
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/defaulted-method-template.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(
		t,
		analyzed,
		root,
		"$repository->searchIds($criteria)->getIds()",
		"list<string>",
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestGenericSelfReturnPreservesReceiverArguments(t *testing.T) {
	t.Parallel()
	source := `<?php
/** @template T = string */
class Collection {
    /** @return self<T> */
    public function filtered(): self {}
    /** @return list<T> */
    public function values(): array {}
}
/** @template T = string */
class Owner {
    /** @return Collection<T> */
    public function collection(): Collection {}
    /** @return list<T> */
    public function values(): array {
        return $this->collection()->filtered()->values();
    }
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/generic-self.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(
		t,
		analyzed,
		root,
		"$this->collection()->filtered()",
		"Collection<T>",
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestGenericSelfFactoryAndFluentMethodsRefineReceiver(t *testing.T) {
	t.Parallel()
	source := `<?php
interface RuleError {}
interface IdentifierRuleError extends RuleError {}
interface LineRuleError extends RuleError {}

/** @template-covariant T of RuleError */
final class RuleErrorBuilder {
    /** @return self<RuleError> */
    public static function message(string $message): self {}

    /** @return self<T&IdentifierRuleError> */
    public function identifier(string $identifier): self {}

    /** @return self<T&LineRuleError> */
    public function line(int $line): self {}

    /** @return T */
    public function build(): RuleError {}
}

/** @return list<RuleError> */
function errors(): array {
    return [
        RuleErrorBuilder::message('failure')
            ->identifier('app.failure')
            ->line(1)
            ->build(),
    ];
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/generic-fluent-self.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(
		t,
		analyzed,
		root,
		"RuleErrorBuilder::message('failure')",
		"RuleErrorBuilder<RuleError>",
	)
	assertTextType(
		t,
		analyzed,
		root,
		"RuleErrorBuilder::message('failure')\n            ->identifier('app.failure')",
		"RuleErrorBuilder<IdentifierRuleError&RuleError>",
	)
	assertTextType(
		t,
		analyzed,
		root,
		"RuleErrorBuilder::message('failure')\n            ->identifier('app.failure')\n            ->line(1)\n            ->build()",
		"IdentifierRuleError&LineRuleError&RuleError",
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestResolveSpecialTypeBoundsAdversarialNesting(t *testing.T) {
	t.Parallel()

	value := types.String()
	for range maxSpecialTypeDepth * 2 {
		value = types.Named("Nested", value)
	}
	resolved := resolveSpecialType(
		value,
		types.Named("Receiver"),
		types.Named("Current"),
	)
	for depth := 0; depth < maxSpecialTypeDepth; depth++ {
		if resolved.IsUnknown() {
			return
		}
		require.Equal(t, types.ObjectKind, resolved.Kind())
		resolved = resolved.Argument(0)
	}
	require.True(t, resolved.IsUnknown())
}

func TestUntypedMagicMethodParameterAcceptsTypedArray(t *testing.T) {
	t.Parallel()
	source := `<?php
/**
 * @method mixed randomElement($array = ['a', 'b', 'c'])
 */
class Generator {}

class Consumer {
    /** @param list<string> $ids */
    public function getRandomId(Generator $generator, array $ids): ?string {
        return $generator->randomElement($ids);
    }
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/magic-method.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestPHPDocScalarPseudoTypeAcceptsScalarShapeValues(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;

final class Event {
    /** @return array<string, scalar|array<mixed>|null> */
    public function values(): array {
        return [
            'active' => true,
            'count' => 1,
            'name' => 'shopware',
            'ratio' => 1.5,
        ];
    }
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/scalar-pseudo-type.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
		require.NotEqual(t, "php.undefined", issue.Code, issue.Message)
	}
}

func TestFakerRandomElementPreservesInputElementType(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace Faker {
    /**
     * @method mixed randomElement($array = ['a', 'b', 'c'])
     */
    class Generator {}
}

namespace App {
    use Faker\Generator;

    class Consumer {
        /** @param list<string> $ids */
        public function getRandomId(Generator $generator, array $ids): ?string {
            return $generator->randomElement($ids);
        }
    }
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/faker.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, FakerTypes).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	assertTextType(
		t,
		analyzed,
		root,
		"$generator->randomElement($ids)",
		"null|string",
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestNewSelfUsesEnclosingClassType(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;
class DomainError {
    public static function create(): self {
        return new self();
    }
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/new-self.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(t, analyzed, root, "new self()", "App\\DomainError")
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestNewSelfPreservesAndCanReplaceEnclosingClassTemplate(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;
/**
 * @template T = string
 */
class Box {
    /** @param T $value */
    public function __construct(mixed $value) {}

    /**
     * @param T $value
     * @return self<T>
     */
    public function copy(mixed $value): self {
        return new self($value);
    }

    /**
     * @template U
     * @param U $replacement
     * @return self<U>
     */
    public function replace(mixed $replacement): self {
        return new self($replacement);
    }
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/generic-new-self.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(t, analyzed, root, "new self($value)", "App\\Box<T>")
	assertTextType(t, analyzed, root, "new self($replacement)", "App\\Box<U>")
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestNestedClosureReturnUsesClosureDeclaration(t *testing.T) {
	t.Parallel()
	source := `<?php
function consume(callable $callback): void {}
function run(): void {
    consume(function (): bool {
        return true;
    });
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/nested-closure.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestGeneratorBareReturnIsNotValidatedAsObjectReturn(t *testing.T) {
	t.Parallel()
	source := `<?php
class Generator {}
function values(bool $enabled): Generator {
    if (!$enabled) {
        return;
    }
    yield 'value';
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/generator.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestEmptyArraySatisfiesTypedArrayAndListReturns(t *testing.T) {
	t.Parallel()
	source := `<?php
/** @return list<string> */
function names(): array {
    return [];
}
/** @return array<string, mixed> */
function values(): array {
    return [];
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/empty-array.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestAssociativeArrayLiteralPreservesShape(t *testing.T) {
	t.Parallel()
	source := `<?php
/**
 * @return array{uri: string, mimeType: string, text: string}
 */
function resource(): array {
    return [
        'uri' => 'shopware://entities',
        'mimeType' => 'application/json',
        'text' => '{}',
    ];
}

/**
 * @param array<string, string> $defaults
 * @return array<string, string>
 */
function merged(array $defaults): array {
    return [...$defaults, ...['field' => 'value']];
}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/array-shape-literal.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(
		t,
		analyzed,
		root,
		`[
        'uri' => 'shopware://entities',
        'mimeType' => 'application/json',
        'text' => '{}',
    ]`,
		`array{mimeType:"application/json",text:"{}",uri:"shopware://entities"}`,
	)
	assertTextType(
		t,
		analyzed,
		root,
		"[...$defaults, ...['field' => 'value']]",
		"non-empty-array<string,string>",
	)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestIntegerKeyArraySpreadProducesList(t *testing.T) {
	t.Parallel()
	source := `<?php
/** @return list<string> */
function names(): array {}
/** @return array<int, string> */
function indexedNames(): array {}
$list = [...names(), 'tail'];
$reindexed = [...indexedNames()];
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/list-spread.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(t, analyzed, root, "[...names(), 'tail']", "non-empty-list<string>")
	assertTextType(t, analyzed, root, "[...indexedNames()]", "list<string>")
}

func TestConcreteTraitMethodSatisfiesAbstractTraitRequirement(t *testing.T) {
	t.Parallel()
	source := `<?php
interface Contract {
    public function value(): string;
}
trait RequiresValue {
    abstract public function value(): string;
}
trait ProvidesValue {
    public function value(): string {
        return 'value';
    }
}
class Service implements Contract {
    use RequiresValue;
    use ProvidesValue;
}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/traits.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.abstract", issue.Code, issue.Message)
	}
}

func TestBackedEnumRuntimeMethodsDoNotProduceOverrideDiagnostics(t *testing.T) {
	t.Parallel()
	source := `<?php
enum Status: string {
    case Active = 'active';
}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/status.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.override", issue.Code, issue.Message)
	}
}

func TestRedisExtensionSignaturesAcceptCompatibleTestDoubles(t *testing.T) {
	t.Parallel()
	source := `<?php
class RedisStub extends Redis {
    public function get(string $key): mixed {}
    public function set(string $key, mixed $value, mixed $options = null): Redis|string|bool {}
    public function del(array|string $key, string ...$other_keys): Redis|int|false {}
    public function keys(string $pattern): Redis|array|false {}
    public function sAdd(string $key, mixed $value, mixed ...$other_values): Redis|int|false {}
    public function sMembers(string $key): Redis|array|false {}
    public function multi(int $value = Redis::MULTI): Redis|bool {}
    public function exec(): array {}
}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/redis-stub.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.override", issue.Code, issue.Message)
	}
}

func TestInheritedGenericConstructorAcceptsSubclassElements(t *testing.T) {
	t.Parallel()
	source := `<?php
/** @template TElement */
abstract class Collection {
    /** @param iterable<TElement> $elements */
    public function __construct(iterable $elements = []) {}
    /** @param TElement $element */
    public function add($element): void {}
}
class Field {}
class IdField extends Field {
    /** @return static */
    public function addFlags(): self {
        return $this;
    }
}
/** @extends Collection<Field> */
class FieldCollection extends Collection {}
function fields(): FieldCollection {
    $fields = new FieldCollection([(new IdField())->addFlags()]);
    $fields->add(new IdField());
    return $fields;
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/inherited-constructor.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestUntypedMethodParameterAcceptsKnownArgument(t *testing.T) {
	t.Parallel()
	source := `<?php
class Collection {
    public function add($element): void {}
}
function append(Collection $items): void {
    $items->add('value');
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/untyped-method.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestMixedValueDoesNotProduceUnprovableReturnMismatch(t *testing.T) {
	t.Parallel()
	source := `<?php
function value(mixed $input): string {
    return $input;
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/mixed-return.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestVariadicParameterIsAListInsideFunction(t *testing.T) {
	t.Parallel()
	source := `<?php
function values(?string ...$values): array {
    return $values;
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/variadic.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertNodeType(
		t,
		analyzed,
		root,
		phpsyntax.PhpVariable,
		"list<null|string>",
	)
}

func TestObjectCreationInsideFunctionArgumentDoesNotRecurse(t *testing.T) {
	t.Parallel()
	source := `<?php
class Product {
    public function __construct(string $name) {}
}
function consume(Product $product): void {}
function run(): void {
    consume(new Product('demo'));
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/nested-constructor.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(t, analyzed, root, "new Product('demo')", "Product")
}

func TestLargeLiteralArrayWidensInsteadOfGrowingUnboundedUnion(t *testing.T) {
	t.Parallel()
	var source strings.Builder
	source.WriteString("<?php $messages = [")
	for index := range 1_000 {
		if index > 0 {
			source.WriteByte(',')
		}
		source.WriteString("'message-")
		source.WriteString(strconv.Itoa(index))
		source.WriteByte('\'')
	}
	source.WriteString("];")

	root := phpparser.Parse(source.String()).Tree.Root
	bound := binder.New().Bind("/locale.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	arrays := phpquery.Arrays(root)
	require.Len(t, arrays, 1)
	require.Equal(t, "non-empty-list<string>", analyzed.TypeOf(arrays[0]).Type.String())
}

func TestNumericLiteralInferenceExcludesLeadingComments(t *testing.T) {
	t.Parallel()
	source := `<?php
$descriptors = [
    // stdin
    1,
    // stdout
    2,
];`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/descriptors.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	numbers := phpquery.Nodes(root, phpsyntax.PhpNumber)
	require.Len(t, numbers, 2)
	require.Equal(t, "1", analyzed.TypeOf(numbers[0]).Type.String())
	require.Equal(t, "2", analyzed.TypeOf(numbers[1]).Type.String())
}

func TestUnknownExpressionsDoNotCreateRedundantTypeFacts(t *testing.T) {
	t.Parallel()
	source := `<?php
function run($service) {
    return $service->missing();
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/unknown.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	calls := phpquery.Nodes(root, phpsyntax.PhpMemberCall)
	require.Len(t, calls, 1)
	require.True(t, analyzed.TypeOf(calls[0]).Type.IsUnknown())
	require.NotContains(t, analyzed.TypeFacts, semantic.NodeIdentity(calls[0]))
}

func TestAssignmentUpdatesLocalCompletionType(t *testing.T) {
	t.Parallel()
	source := `<?php
function run(): void {
    $value = 'first';
    $value = 42;
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/locals.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, symbol := range analyzed.Symbols {
		if symbol.Kind == semantic.LocalSymbol && symbol.Name == "$value" {
			require.Equal(t, `"first"|42`, symbol.Type.String())
			return
		}
	}
	t.Fatal("missing local symbol")
}

func TestDeclarationCompatibilityDiagnostics(t *testing.T) {
	t.Parallel()
	source := `<?php
class Product {}
interface Contract {
    public function transform(object $value): object;
    public function missing(): void;
}
class Base {
    final protected function sealed(): void {}
    public function transform(Product $value): object { return $value; }
    protected string $name;
}
class Child extends Base implements Contract {
    private function sealed(): void {}
    public function transform(Product $value): string { return 'wrong'; }
    public int $name;
    #[Override]
    public function stray(): void {}
}
final class Closed {}
class Invalid extends Closed {}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/declarations.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	var messages []string
	for _, issue := range analyzed.Issues {
		messages = append(messages, issue.Message)
	}
	require.Condition(t, func() bool {
		return containsMessage(messages, "cannot override final member")
	})
	require.Condition(t, func() bool {
		return containsMessage(messages, "incompatible signature")
	})
	require.Condition(t, func() bool {
		return containsMessage(messages, "must keep the invariant type string")
	})
	require.Condition(t, func() bool {
		return containsMessage(messages, "marked #[Override]")
	})
	require.Condition(t, func() bool {
		return containsMessage(messages, "must implement abstract method")
	})
	require.Condition(t, func() bool {
		return containsMessage(messages, "cannot extend final class Closed")
	})
}

func TestDeclarationCompatibilityAcceptsVariance(t *testing.T) {
	t.Parallel()
	source := `<?php
class Entity {}
class Product extends Entity {}
interface Mapper {
    public function map(Product $value): Entity;
}
class ProductMapper implements Mapper {
    public function map(object $value): Product { return new Product(); }
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/variance.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.override", issue.Code, issue.Message)
		require.NotEqual(t, "php.abstract", issue.Code, issue.Message)
	}
}

func TestDeclarationCompatibilityHonorsSyntheticAndStagedSignatures(
	t *testing.T,
) {
	t.Parallel()
	source := `<?php
namespace App;

use Shopware\Core\Framework\Deprecation\BCChange\NewOptionalParameter;

/** @method string future(string $value) */
interface FutureInterface {}
class FutureImplementation implements FutureInterface {
    public function future(int $value): int { return $value; }
}

interface TentativeReturn {
    public function current(): string;
}
class LegacyReturn implements TentativeReturn {
    #[\ReturnTypeWillChange]
    public function current() { return 'value'; }
}

trait Assigns {
    public function assign(array $values): static { return $this; }
}
class Values {
    use Assigns;
    public function assign(array $values): static { return $this; }
}

class StagedBase {
    #[NewOptionalParameter(
        version: 'v2',
        parameterName: 'silent',
        parameterType: 'bool',
        defaultValue: true,
    )]
    public function save(string $value): void {}
}
class StagedChild extends StagedBase {
    public function save(string $value): void {}
}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/compatibility-mechanisms.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.override", issue.Code, issue.Message)
	}
}

func TestLateStaticReturnFromParentCallUsesCurrentClass(t *testing.T) {
	t.Parallel()
	source := `<?php
class Base {
    /** @return static */
    public function fluent(): self { return $this; }
}
class Child extends Base {
    public function child(): self {
        return parent::fluent();
    }
}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/late-static.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(t, analyzed, root, "parent::fluent()", "Child")
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestStrictFalseComparisonNarrowsFollowingFlow(t *testing.T) {
	t.Parallel()
	source := `<?php
function locate(): int|false {}
function position(): int {
    $position = locate();
    if ($position === false) {
        return 0;
    }
    return $position;
}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/false-comparison.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestSpaceshipOperatorReturnsInt(t *testing.T) {
	t.Parallel()
	source := `<?php
function compare(string $left, string $right): int {
    return $left <=> $right;
}
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/spaceship.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(t, analyzed, root, "$left <=> $right", "int")
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestTopLevelScriptInference(t *testing.T) {
	t.Parallel()
	source := `<?php
$name = 'shopware';
$length = strlen($name);
return $length;
`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/config.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(t, analyzed, root, "$length", "int")
	var foundLocal bool
	for _, symbol := range analyzed.Symbols {
		if symbol.Kind == semantic.LocalSymbol && symbol.Name == "$name" {
			require.Equal(t, `"shopware"`, symbol.Type.String())
			foundLocal = true
		}
	}
	require.True(t, foundLocal)
}

func TestAssignmentGuardNarrowsAssignedVariable(t *testing.T) {
	t.Parallel()
	source := `<?php
class Source {
    public function value(): ?string { return null; }
}
/** @param array<array-key, string> $values */
function accept(array $values): void {}
function run(Source $source): void {
    if (($value = $source->value()) !== null) {
        accept([$value]);
    }
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/assignment-guard.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	assertTextType(t, analyzed, root, "[$value]", "array{0:string}")
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
}

func TestSameDocumentReturnInferenceReachesFixedPoint(t *testing.T) {
	t.Parallel()
	source := `<?php
class Values {
    public function raw() { return 'value'; }
    public function forwarded() { return $this->raw(); }
    public function finalValue(): string { return $this->forwarded(); }
    public function stable(): string { return 'stable'; }
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/fixed-point.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzer := New(snapshot)
	analysisCounts := make(map[semantic.SymbolID]int)
	analyzer.observeFunction = func(id semantic.SymbolID) {
		analysisCounts[id]++
	}
	analyzed := analyzer.Analyze(binder.Link(bound, snapshot), root)

	require.Equal(t, `"value"`, symbolNamed(t, analyzed, "raw").ReturnType.String())
	require.Equal(t, `"value"`, symbolNamed(t, analyzed, "forwarded").ReturnType.String())
	assertTextType(t, analyzed, root, "$this->forwarded()", `"value"`)
	require.Equal(t, 1, analysisCounts[symbolNamed(t, analyzed, "raw").ID])
	require.Equal(t, 2, analysisCounts[symbolNamed(t, analyzed, "forwarded").ID])
	require.Equal(t, 2, analysisCounts[symbolNamed(t, analyzed, "finalValue").ID])
	require.Equal(t, 1, analysisCounts[symbolNamed(t, analyzed, "stable").ID])
}

func TestForeachValueDoesNotReplaceIterableType(t *testing.T) {
	t.Parallel()
	source := `<?php
/** @param list<string> $items */
function preserve($items) {
    foreach ($items as $item) {}
    return $items;
}`
	root := phpparser.Parse(source).Tree.Root
	bound := binder.New().Bind("/foreach.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)

	require.Equal(
		t,
		"list<string>",
		symbolNamed(t, analyzed, "preserve").ReturnType.String(),
	)
}

func containsMessage(messages []string, needle string) bool {
	for _, message := range messages {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

func assertNodeType(
	t *testing.T,
	document *semantic.Document,
	root *phpsyntax.Node,
	kind phpsyntax.Kind,
	expected string,
) {
	t.Helper()
	nodes := phpquery.Nodes(root, kind)
	require.NotEmpty(t, nodes)
	fact := document.TypeOf(nodes[len(nodes)-1])
	require.Equal(t, expected, fact.Type.String())
}

func TestNonEmptyArrayAndListOperationsPreserveCardinality(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @param non-empty-array<string> $values
 *  @return non-empty-list<string>
 */
function reindex(array $values): array {
    return array_values($values);
}
/** @param list<string> $parents
 *  @return non-empty-list<string>
 */
function appendRequired(array $parents, string $current): array {
    return [...$parents, $current];
}
/** @return array<string, non-empty-list<string>> */
function grouped(): array {
    $result = [];
    $result['language'][] = 'id';
    return $result;
}
/** @return list<string> */
function tokens(string $value): array {
    return preg_split('/_+/', $value, -1, PREG_SPLIT_NO_EMPTY) ?: [];
}
`).Tree.Root
	bound := binder.New().Bind("/non-empty-array-operations.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestArrayMapContextuallyTypesCallbackParameters(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @template T */
class Result {
    /** @return T */
    public function get(): mixed {}
}
/**
 * @param list<Result<string|array<string, string>>> $results
 * @return list<string|array<string, string>>
 */
function values(array $results): array {
    return array_map(static fn (Result $result) => $result->get(), $results);
}
`).Tree.Root
	bound := binder.New().Bind("/array-map-context.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestOptionalNonNullShapeFieldStaysNonNullAfterIssetBranch(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @param array<string, string> $values
 *  @return array<string, string>
 */
function resolve(array $values): array { return $values; }
/**
 * @param array{imports: array<string, string>, scopes?: array<string, string>} $map
 * @return array{imports: array<string, string>, scopes?: array<string, string>}
 */
function importMap(array $map): array {
    if (isset($map['scopes'])) {
        $map['scopes'] = resolve($map['scopes']);
    }
    return $map;
}
`).Tree.Root
	bound := binder.New().Bind("/isset-optional-shape.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestPHPStanIgnoreNextLineSuppressesDiagnostic(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @return list<string> */
function values(): array {
    /** @phpstan-ignore-next-line return.type */
    return array_combine(['key'], ['value']);
}
`).Tree.Root
	bound := binder.New().Bind("/phpstan-ignore.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		bound,
		stubs.Document(project.Version{Major: 8, Minor: 3}),
	})
	analyzed := New(snapshot, Builtins).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestPHPStanTargetedIgnoreSuppressesCascadingDiagnostic(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
/** @return class-string|null */
function className(): ?string {
    /** @phpstan-ignore cast.string */
    return (string) arbitraryValue();
}
function arbitraryValue(): mixed {}
`).Tree.Root
	bound := binder.New().Bind("/phpstan-targeted-ignore.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	for _, issue := range analyzed.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
	}
}

func TestPHPStanTargetedIgnoreLeavesUnrelatedDiagnosticOnSameLine(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function needsArgument(string $value): int { return 1; }
function value(): string {
    /** @phpstan-ignore return.type */ return needsArgument();
}
`).Tree.Root
	bound := binder.New().Bind("/phpstan-targeted.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot).Analyze(binder.Link(bound, snapshot), root)
	var returnTypes, arguments int
	for _, issue := range analyzed.Issues {
		switch issue.Code {
		case "php.returnType":
			returnTypes++
		case "php.arguments":
			arguments++
		}
	}
	require.Zero(t, returnTypes)
	require.Positive(t, arguments)
}

func TestPhpStormMetaFunctionReturnContracts(t *testing.T) {
	t.Parallel()
	metaRoot := phpparser.Parse(`<?php
namespace PHPSTORM_META;
override(\identity(0), type(0));
override(\first(0), elementType(0));
`).Tree.Root
	metaDocument := &semantic.Document{
		Path:          "/.phpstorm.meta.php",
		CallContracts: phpstormmeta.Parse(metaRoot),
	}
	root := phpparser.Parse(`<?php
class Product {}
function run(): void {
    $identity = identity(new Product());
    $first = first([new Product()]);
}
`).Tree.Root
	bound := binder.New().Bind("/contracts.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{metaDocument, bound})
	analyzed := New(snapshot, CallContracts).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	assertTextType(t, analyzed, root, "identity(new Product())", "Product")
	assertTextType(t, analyzed, root, "first([new Product()])", "Product")
}

func TestNoReturnAttributeConditionalExitPoint(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
use JetBrains\PhpStorm\NoReturn;

#[NoReturn(1)]
function stopOnOne(int $mode): void {}

#[NoReturn(NoReturn::ANY_ARGUMENT, true)]
function stopOnFlag(string $message, bool $exit): void {}

function runOne(): void {
    $continued = stopOnOne(2);
    $stopped = stopOnOne(1);
}
function runFlag(): void {
    $notStopped = stopOnFlag('message', false);
    $wildcard = stopOnFlag('message', true);
}
`).Tree.Root
	bound := binder.New().Bind("/no-return-attribute.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{bound})
	analyzed := New(snapshot, AttributeContracts).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	assertTextType(t, analyzed, root, "stopOnOne(1)", "never")
	assertTextType(t, analyzed, root, "stopOnOne(2)", "void")
	assertTextType(t, analyzed, root, "stopOnFlag('message', true)", "never")
	assertTextType(t, analyzed, root, "stopOnFlag('message', false)", "void")
}

func TestPhpStormMetaMethodContractAppliesToSubtype(t *testing.T) {
	t.Parallel()
	metaRoot := phpparser.Parse(`<?php
namespace PHPSTORM_META;
override(\BaseContainer::get(0), type(0));
`).Tree.Root
	metaDocument := &semantic.Document{
		Path:          "/.phpstorm.meta.php",
		CallContracts: phpstormmeta.Parse(metaRoot),
	}
	root := phpparser.Parse(`<?php
class Product {}
class BaseContainer { public function get(mixed $value): mixed {} }
class Container extends BaseContainer {}
function run(Container $container): void {
    $product = $container->get(new Product());
}
`).Tree.Root
	bound := binder.New().Bind("/method-contract.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{metaDocument, bound})
	analyzed := New(snapshot, CallContracts).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	assertTextType(
		t,
		analyzed,
		root,
		"$container->get(new Product())",
		"Product",
	)
}

func TestPhpStormMetaMapAndExitPointContracts(t *testing.T) {
	t.Parallel()
	metaRoot := phpparser.Parse(`<?php
namespace PHPSTORM_META;
override(\make(0), map(['product' => \Product::class, '' => '@']));
override(\mock(0), map(['' => '$0']));
exitPoint(\dd());
exitPoint(\trigger_error(ANY_ARGUMENT, \E_USER_ERROR));
`).Tree.Root
	metaDocument := &semantic.Document{
		Path:          "/.phpstorm.meta.php",
		CallContracts: phpstormmeta.Parse(metaRoot),
	}
	root := phpparser.Parse(`<?php
class Product {}
function run(): void {
    $mapped = make('product');
    $fallback = make('GeneratedProduct');
    $mock = mock(Product::class);
}
function warning(): void {
    trigger_error('warning', \E_USER_WARNING);
}
function failure(): void {
    trigger_error('failure', \E_USER_ERROR);
}
function dump(): void {
    dd();
}
`).Tree.Root
	bound := binder.New().Bind("/map-contract.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{metaDocument, bound})
	require.Len(t, metaDocument.CallContracts, 4)
	seenMapContract := false
	snapshot.VisitFunctionCallContracts("make", func(contract semantic.CallContract) bool {
		seenMapContract = contract.Return.Kind == semantic.CallReturnArgumentMap
		return true
	})
	require.True(t, seenMapContract)
	require.Equal(
		t,
		semantic.CallValueClassConstant,
		metaDocument.CallContracts[0].Return.Map[0].Result.Kind,
		metaDocument.CallContracts[0].Return.Map[0].Result,
	)
	value, matched := evaluateCallReturnContract(
		CallContext{
			Snapshot:  snapshot,
			Arguments: []CallArgument{{Type: types.LiteralString("product")}},
		},
		metaDocument.CallContracts[0].Return,
	)
	require.True(t, matched)
	require.Equal(t, "Product", value.String())
	analyzed := New(snapshot, CallContracts).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	assertTextType(t, analyzed, root, "make('product')", "Product")
	assertTextType(t, analyzed, root, "make('GeneratedProduct')", "GeneratedProduct")
	assertTextType(t, analyzed, root, "mock(Product::class)", "Product")
	assertTextType(
		t,
		analyzed,
		root,
		"trigger_error('failure', \\E_USER_ERROR)",
		"never",
	)
	for _, call := range phpquery.Calls(root) {
		if strings.TrimSpace(call.Text()) !=
			"trigger_error('warning', \\E_USER_WARNING)" {
			continue
		}
		require.NotEqual(t, types.NeverKind, analyzed.TypeOf(call).Type.Kind())
	}
	assertTextType(t, analyzed, root, "dd()", "never")
}

func TestPhpStormMetaFrameworkReturnContracts(t *testing.T) {
	t.Parallel()
	metaRoot := phpparser.Parse(`<?php
namespace PHPSTORM_META;
override(\Psr\Container\ContainerInterface::get(0), map([
    'product.repository' => \App\Repository\ProductRepository::class,
]));
override(\Doctrine\Persistence\ObjectManager::getRepository(0), map([
    \App\Entity\Product::class => \App\Repository\ProductRepository::class,
]));
override(\PHPUnit\Framework\TestCase::createMock(0), map(['' => '$0']));
`).Tree.Root
	metaDocument := &semantic.Document{
		Path:          "/.phpstorm.meta.php",
		CallContracts: phpstormmeta.Parse(metaRoot),
	}
	root := phpparser.Parse(`<?php
namespace Psr\Container {
    interface ContainerInterface { public function get(string $id): object; }
}
namespace Doctrine\Persistence {
    interface ObjectManager { public function getRepository(string $class): object; }
}
namespace PHPUnit\Framework {
    abstract class TestCase { protected function createMock(string $class): object {} }
}
namespace App\Entity { final class Product {} }
namespace App\Repository { final class ProductRepository {} }
namespace App\Infrastructure {
    final class Container implements \Psr\Container\ContainerInterface {
        public function get(string $id): object {}
    }
    final class EntityManager implements \Doctrine\Persistence\ObjectManager {
        public function getRepository(string $class): object {}
    }
}
namespace App\Tests {
    use App\Entity\Product;
    use App\Infrastructure\Container;
    use App\Infrastructure\EntityManager;
    final class ProductTest extends \PHPUnit\Framework\TestCase {
        public function run(Container $container, EntityManager $manager): void {
            $service = $container->get('product.repository');
            $repository = $manager->getRepository(Product::class);
            $mock = $this->createMock(Product::class);
        }
    }
}
`).Tree.Root
	bound := binder.New().Bind("/framework-contracts.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{metaDocument, bound})
	analyzed := New(snapshot, CallContracts).Analyze(
		binder.Link(bound, snapshot),
		root,
	)

	for _, expression := range []string{
		"$container->get('product.repository')",
		"$manager->getRepository(Product::class)",
	} {
		assertTextType(
			t,
			analyzed,
			root,
			expression,
			"App\\Repository\\ProductRepository",
		)
	}
	assertTextType(
		t,
		analyzed,
		root,
		"$this->createMock(Product::class)",
		"App\\Entity\\Product",
	)
}

func assertTextType(
	t *testing.T,
	document *semantic.Document,
	root *phpsyntax.Node,
	text,
	expected string,
) {
	t.Helper()
	for element := range root.Descendants() {
		node, ok := element.(*phpsyntax.Node)
		if !ok || strings.TrimSpace(node.Text()) != text {
			continue
		}
		fact := document.TypeOf(node)
		if !fact.Type.IsUnknown() {
			require.Equal(t, expected, fact.Type.String())
			return
		}
	}
	t.Fatalf("no inferred node %q", text)
}

func symbolNamed(t *testing.T, document *semantic.Document, name string) semantic.Symbol {
	t.Helper()
	for _, symbol := range document.Symbols {
		if symbol.Name == name {
			return symbol
		}
	}
	t.Fatalf("missing symbol %q", name)
	return semantic.Symbol{}
}
