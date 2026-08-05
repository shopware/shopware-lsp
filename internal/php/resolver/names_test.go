package resolver

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

var (
	benchmarkNameContext NameContext
	benchmarkName        string
	benchmarkNames       []string
	benchmarkNameCount   int
	benchmarkImports     []Import
)

func TestImportedNameLookupDoesNotAllocate(t *testing.T) {
	context := NewNameContext("App")
	context.AddImport(Import{
		Kind:   ClassImport,
		Alias:  "ProductRepository",
		Target: "Shopware\\ProductRepository",
	})
	context.AddImport(Import{
		Kind:   FunctionImport,
		Alias:  "BuildProduct",
		Target: "Shopware\\BuildProduct",
	})

	allocations := testing.AllocsPerRun(100, func() {
		benchmarkName = context.ResolveClass("ProductRepository")
		benchmarkNameCount = 0
		context.VisitFunctionNames("BuildProduct", func(string) bool {
			benchmarkNameCount++
			return true
		})
	})
	require.Zero(t, allocations)
	require.Equal(t, "Shopware\\ProductRepository", benchmarkName)
	require.Equal(t, 1, benchmarkNameCount)
}

func TestNameResolution(t *testing.T) {
	t.Parallel()
	context := NewNameContext("App\\Domain")
	require.Nil(t, context.Imports.Classes)
	require.Nil(t, context.Imports.Functions)
	require.Nil(t, context.Imports.Constants)
	for _, imported := range ParseUseDeclaration(
		`use Vendor\Package\{Thing as Imported, function build, const VERSION};`,
	) {
		context.AddImport(imported)
	}
	context.AddImport(Import{Kind: ClassImport, Alias: "Base", Target: "Vendor\\Base"})

	require.Equal(t, "Vendor\\Package\\Thing", context.ResolveClass("Imported"))
	require.Equal(t, "Vendor\\Base\\Child", context.ResolveClass("Base\\Child"))
	require.Equal(t, "App\\Domain\\Local", context.ResolveClass("Local"))
	require.Equal(t, "Absolute\\Name", context.ResolveClass("\\Absolute\\Name"))
	require.Equal(t, []string{"Vendor\\Package\\build"}, context.ResolveFunction("build"))
	require.Equal(t, []string{"App\\Domain\\fallback", "fallback"}, context.ResolveFunction("fallback"))
	require.Equal(t, []string{"Vendor\\Package\\VERSION"}, context.ResolveConstant("VERSION"))

	collect := func(visit func(func(string) bool) bool) []string {
		var result []string
		require.True(t, visit(func(name string) bool {
			result = append(result, name)
			return true
		}))
		return result
	}
	require.Equal(
		t,
		context.ResolveFunction("fallback"),
		collect(func(visit func(string) bool) bool {
			return context.VisitFunctionNames("fallback", visit)
		}),
	)
	require.Equal(
		t,
		context.ResolveConstant("VERSION"),
		collect(func(visit func(string) bool) bool {
			return context.VisitConstantNames("VERSION", visit)
		}),
	)
	visited := 0
	require.False(t, context.VisitFunctionNames(
		"fallback",
		func(string) bool {
			visited++
			return false
		},
	))
	require.Equal(t, 1, visited)
}

func TestNameContextResolvesAbsoluteNativeType(t *testing.T) {
	t.Parallel()
	context := NewNameContext("App\\Feature")
	value, err := types.ParseNative(`\Throwable`)
	require.NoError(t, err)
	require.Equal(t, "Throwable", context.ResolveType(value).String())
}

func BenchmarkNewNameContext(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		benchmarkNameContext = NewNameContext("App\\Domain")
		if benchmarkNameContext.Namespace == "" {
			b.Fatal("missing namespace")
		}
	}
}

func BenchmarkResolveClassWithoutImports(b *testing.B) {
	context := NewNameContext("App\\Domain")
	b.ReportAllocs()
	for b.Loop() {
		benchmarkName = context.ResolveClass("LocalService")
	}
}

func BenchmarkResolveFunctionWithoutImports(b *testing.B) {
	context := NewNameContext("App\\Domain")
	b.ReportAllocs()
	for b.Loop() {
		benchmarkNames = context.ResolveFunction("local_function")
	}
}

func BenchmarkVisitFunctionWithoutImports(b *testing.B) {
	context := NewNameContext("App\\Domain")
	b.ReportAllocs()
	for b.Loop() {
		benchmarkNameCount = 0
		context.VisitFunctionNames(
			"local_function",
			func(string) bool {
				benchmarkNameCount++
				return true
			},
		)
		if benchmarkNameCount != 2 {
			b.Fatalf("visited %d names", benchmarkNameCount)
		}
	}
}

func TestResolveNestedTypeNames(t *testing.T) {
	t.Parallel()
	context := NewNameContext("App")
	context.AddImport(Import{Kind: ClassImport, Alias: "Product", Target: "Shopware\\Product"})
	value := types.MustParse("array<string,list<Product|null>>")
	require.Equal(
		t,
		"array<string,list<Shopware\\Product|null>>",
		context.ResolveType(value).String(),
	)
}

func TestNameContextPreservesPHPDocTemplates(t *testing.T) {
	t.Parallel()
	context := NewNameContext("App")
	value := types.MustParse("array<T,Entity>")
	resolved := context.ResolvePHPDocType(value, []string{"T"})
	require.Equal(t, "array<T,App\\Entity>", resolved.String())
	require.Equal(t, types.TemplateKind, resolved.Arguments()[0].Kind())
}

func TestNameContextResolvesGenericSelfArguments(t *testing.T) {
	t.Parallel()
	context := NewNameContext("App")
	value := types.MustParse("self<T&RuleError>")
	resolved := context.ResolvePHPDocType(value, []string{"T"})
	require.Equal(t, "self<App\\RuleError&T>", resolved.String())
	require.Equal(t, types.TemplateKind, resolved.Argument(0).Argument(1).Kind())
}

func TestParseUseDeclarations(t *testing.T) {
	t.Parallel()
	require.Equal(t, []Import{
		{Kind: FunctionImport, Alias: "run", Target: "Vendor\\run"},
		{Kind: FunctionImport, Alias: "local", Target: "Other\\build"},
	}, ParseUseDeclaration(`use function Vendor\run, Other\build as local;`))
	require.Equal(t, []Import{
		{Kind: ClassImport, Alias: "Alias", Target: "Vendor\\Package"},
	}, ParseUseDeclaration("use Vendor\\Package\n\tas  Alias;"))
}

func TestAddUseDeclarationDoesNotMaterializeImportSlice(t *testing.T) {
	context := NameContext{
		Imports: semantic.ImportTable{
			Classes:   make(map[string]string, 2),
			Functions: make(map[string]string, 2),
			Constants: make(map[string]string, 2),
		},
	}
	allocations := testing.AllocsPerRun(1000, func() {
		context.AddUseDeclaration(
			"use function vendor\\run, other\\build as local;",
		)
	})
	require.Zero(t, allocations)
}

func BenchmarkParseUseDeclaration(b *testing.B) {
	const source = `use function Vendor\run, Other\build as local;`
	b.ReportAllocs()
	for b.Loop() {
		benchmarkImports = ParseUseDeclaration(source)
	}
}
