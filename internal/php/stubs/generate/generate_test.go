package generate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/stubs/catalog"
	"github.com/stretchr/testify/require"
)

func TestBuildVersionedCatalog(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "Core"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "Core", "Core.php"),
		[]byte(`<?php
/** @since 8.1 */
function added(): string {}
/** @removed 8.2 */
function removed(): bool {}
/** @return string|false */
#[LanguageLevelTypeAware(['8.2' => 'string'], default: 'string|false')]
function aware() {}
#[Deprecated(since: '8.2')]
function laterDeprecated(): void {}
/** @deprecated use replacement() */
#[Deprecated(since: '8.2')]
function documentedDeprecated(): void {}
#[\JetBrains\PhpStorm\ArrayShape(['id' => 'int'])]
function shaped(): array {}
function expected(
    #[\JetBrains\PhpStorm\ExpectedValues(values: ['auto', 7])]
    string|int $mode
): void {}
#[PhpStormStubsElementAvailable(from: '8.1')]
interface Feature {
    public function value(
        #[PhpStormStubsElementAvailable(from: '8.2')]
        #[LanguageLevelTypeAware(['8.2' => 'int'], default: '')]
        $mode = 0
    ): string;
}
`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "Core", ".phpstorm.meta.php"),
		[]byte(`<?php
namespace PHPSTORM_META;
override(\aware(0), type(0));
override(\Feature::value(0), elementType(0));
`),
		0o644,
	))
	lock := Lock{
		Repository:  "https://example.com/phpstorm-stubs.git",
		Commit:      "0123456789012345678901234567890123456789",
		Versions:    []string{"8.0", "8.1", "8.2"},
		Directories: []string{"Core"},
	}
	generated, stats, err := Build(root, lock)
	require.NoError(t, err)
	require.Equal(t, 1, stats.Files)
	require.Zero(t, stats.ParserErrors)
	require.Len(t, generated.MaterializeContracts(), 2)
	require.Len(t, generated.Bundles, 1)
	require.Equal(t, "core", generated.Bundles[0].Extension)
	require.NotEmpty(t, generated.ExtensionSymbols)

	v80 := generated.Materialize(project.Version{Major: 8, Minor: 0}, "phpstub://8.0/core")
	require.Empty(t, find(v80, semantic.FunctionSymbol, "added"))
	require.NotEmpty(t, find(v80, semantic.FunctionSymbol, "removed"))
	require.Equal(t, "false|string", find(v80, semantic.FunctionSymbol, "aware")[0].ReturnType.String())
	require.False(t, find(v80, semantic.FunctionSymbol, "laterDeprecated")[0].Flags.Has(semantic.DeprecatedFlag))
	require.True(t, find(v80, semantic.FunctionSymbol, "documentedDeprecated")[0].Flags.Has(semantic.DeprecatedFlag))
	require.Equal(
		t,
		"array{id:int}",
		find(v80, semantic.FunctionSymbol, "shaped")[0].ReturnType.String(),
	)
	expected := find(v80, semantic.FunctionSymbol, "expected")[0]
	require.Len(t, expected.Parameters[0].Attributes, 1)
	require.Len(t, expected.Parameters[0].Attributes[0].Arguments, 1)
	require.Equal(
		t,
		"auto",
		expected.Parameters[0].Attributes[0].Arguments[0].Value.Items[0].Value.Value,
	)
	require.Empty(t, find(v80, semantic.InterfaceSymbol, "Feature"))

	v81 := generated.Materialize(project.Version{Major: 8, Minor: 1}, "phpstub://8.1/core")
	require.NotEmpty(t, find(v81, semantic.FunctionSymbol, "added"))
	require.NotEmpty(t, find(v81, semantic.FunctionSymbol, "removed"))
	methods := find(v81, semantic.MethodSymbol, "Feature::value")
	require.Len(t, methods, 1)
	require.Empty(t, methods[0].Parameters)

	v82 := generated.Materialize(project.Version{Major: 8, Minor: 2}, "phpstub://8.2/core")
	require.Empty(t, find(v82, semantic.FunctionSymbol, "removed"))
	require.Equal(t, "string", find(v82, semantic.FunctionSymbol, "aware")[0].ReturnType.String())
	require.True(t, find(v82, semantic.FunctionSymbol, "laterDeprecated")[0].Flags.Has(semantic.DeprecatedFlag))
	methods = find(v82, semantic.MethodSymbol, "Feature::value")
	require.Len(t, methods, 1)
	require.Len(t, methods[0].Parameters, 1)
	require.Equal(t, "int", methods[0].Parameters[0].Type.String())

	first, err := catalog.Encode(generated)
	require.NoError(t, err)
	repeated, _, err := Build(root, lock)
	require.NoError(t, err)
	second, err := catalog.Encode(repeated)
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestAttributeArgumentParserHandlesNestedValues(t *testing.T) {
	t.Parallel()
	arguments, ok := attributeArguments(
		`#[LanguageLevelTypeAware(['8.0' => 'array<int, string>'], default: 'array|false')]`,
		"LanguageLevelTypeAware",
	)
	require.True(t, ok)
	require.Contains(t, arguments, "array<int, string>")
}

func find(symbols []semantic.Symbol, kind semantic.SymbolKind, fqn string) []semantic.Symbol {
	var result []semantic.Symbol
	for _, symbol := range symbols {
		if symbol.Kind == kind && symbol.FullyQualified == fqn {
			result = append(result, symbol)
		}
	}
	return result
}
