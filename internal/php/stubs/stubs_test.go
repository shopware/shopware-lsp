package stubs

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

var benchmarkDocument *semantic.Document

func BenchmarkDocument(b *testing.B) {
	version := project.Version{Major: 8, Minor: 3}
	benchmarkDocument = Document(version)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkDocument = Document(version)
	}
}

func TestVersionedStubDocument(t *testing.T) {
	t.Parallel()
	document := Document(project.Version{Major: 8, Minor: 3})
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	require.Len(t, snapshot.Functions("json_validate"), 1)
	require.Empty(t, semantic.NewSnapshot(1, []*semantic.Document{
		Document(project.Version{Major: 8, Minor: 2}),
	}).Functions("json_validate"))

	methods := resolver.MemberResolver{Snapshot: snapshot}.Methods(
		types.Named("DateTimeImmutable"),
		"format",
	)
	require.Len(t, methods, 1)
	require.Equal(t, "string", methods[0].Type.String())
	require.Len(t, snapshot.Classes("DateTime"), 1)
	require.Len(t, snapshot.Classes("DateTimeZone"), 1)
	require.Len(t, snapshot.Classes("DateInterval"), 1)
	require.Len(t, snapshot.Classes("ArrayIterator"), 1)
	require.Len(t, snapshot.Classes("SplFileInfo"), 1)
	require.Len(t, snapshot.Classes("SplFileObject"), 1)
	require.Len(t, snapshot.Classes("ReflectionNamedType"), 1)
	require.Len(t, snapshot.Classes("ReflectionParameter"), 1)
	require.Len(t, snapshot.Classes("Pdo\\Mysql"), 1)
	require.Len(t, snapshot.Classes("DOMElement"), 1)
	require.Len(t, snapshot.Classes("DOMDocument"), 1)
	require.Len(t, snapshot.Classes("GdImage"), 1)
	require.Len(t, snapshot.Classes("Redis"), 1)
	require.Len(t, snapshot.Classes("RedisException"), 1)
	require.Len(t, snapshot.Classes("Imagick"), 1)
	require.Len(t, snapshot.Classes("ImagickPixel"), 1)
	require.Len(t, snapshot.Classes("NumberFormatter"), 1)
	require.Len(t, snapshot.Classes("XMLReader"), 1)
	require.Len(t, snapshot.Classes("ZipArchive"), 1)
	require.Len(t, snapshot.Classes("ReflectionClass"), 1)
	require.Len(t, snapshot.Classes("JsonException"), 1)
	trace := (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("Throwable"),
		"getTrace",
	)
	require.Len(t, trace, 1)
	require.Equal(
		t,
		`list<array{args?:list,class?:class-string,file?:string,function:string,line?:int,object?:object,type?:string,...}>`,
		trace[0].Type.String(),
	)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("DateTimeImmutable"),
		"__construct",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("DateTimeImmutable"),
		"add",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("DateTime"),
		"setTime",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("DateTime"),
		"createFromImmutable",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("DateTimeImmutable"),
		"createFromInterface",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("DateTimeImmutable"),
		"setTimezone",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("Closure"),
		"bind",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("ReflectionProperty"),
		"getType",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("ReflectionMethod"),
		"getDeclaringClass",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("ReflectionMethod"),
		"getAttributes",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("ReflectionEnum"),
		"getBackingType",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("ReflectionFunction"),
		"getClosureThis",
	), 1)
	getAttributes := (resolver.MemberResolver{
		Snapshot: snapshot,
	}).Methods(
		types.Named("ReflectionClass", types.Object()),
		"getAttributes",
	)
	require.Len(t, getAttributes, 1)
	resolvedAttributes := resolver.ResolveSignature(
		snapshot.Relations(),
		getAttributes[0].Symbol,
		[]resolver.Argument{{
			Type: types.ClassString(types.Named("ProductAttribute")),
		}},
	)
	require.True(t, resolvedAttributes.Compatible)
	require.Equal(
		t,
		"list<ReflectionAttribute<ProductAttribute>>",
		resolvedAttributes.ReturnType.String(),
	)
	attributeInstance := (resolver.MemberResolver{
		Snapshot: snapshot,
	}).Methods(
		types.Named("ReflectionAttribute", types.Named("ProductAttribute")),
		"newInstance",
	)
	require.Len(t, attributeInstance, 1)
	require.Equal(t, "ProductAttribute", attributeInstance[0].Type.String())
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Properties(
		types.Named("RuntimeException"),
		"message",
	), 1)
	for _, property := range []struct {
		receiver types.Type
		name     string
		want     string
	}{
		{types.Named("DOMElement"), "localName", "null|string"},
		{types.Named("DOMDocument"), "encoding", "null|string"},
		{types.Named("ZipArchive"), "filename", "string"},
		{types.Named("ReflectionObject"), "name", "string"},
	} {
		properties := (resolver.MemberResolver{
			Snapshot: snapshot,
		}).Properties(property.receiver, property.name)
		require.Len(t, properties, 1, "%s::$%s", property.receiver, property.name)
		require.Equal(t, property.want, properties[0].Type.String())
	}
	require.Equal(t, semantic.Protected, (resolver.MemberResolver{
		Snapshot: snapshot,
	}).Properties(
		types.Named("RuntimeException"),
		"message",
	)[0].Symbol.Visibility)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("Throwable"),
		"getPrevious",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).ConstantTypes(
		types.Named("DateTimeInterface"),
		"ATOM",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).Methods(
		types.Named("SplFileObject"),
		"seek",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).ConstantTypes(
		types.Named("PDO"),
		"ATTR_PERSISTENT",
	), 1)
	require.Len(t, (resolver.MemberResolver{Snapshot: snapshot}).ConstantTypes(
		types.Named("Pdo\\Mysql"),
		"ATTR_COMPRESS",
	), 1)
	arrayIteratorConstructor := (resolver.MemberResolver{
		Snapshot: snapshot,
	}).Methods(
		types.Named(
			"ArrayIterator",
			types.Template("TKey"),
			types.Template("TValue"),
		),
		"__construct",
	)
	require.Len(t, arrayIteratorConstructor, 1)
	resolvedArrayIterator := resolver.ResolveSignature(
		snapshot.Relations(),
		arrayIteratorConstructor[0].Symbol,
		[]resolver.Argument{{Type: types.List(types.String())}},
	)
	require.True(t, resolvedArrayIterator.Compatible)
	require.Equal(t, "string", resolvedArrayIterator.Templates["TValue"].String())
	require.Len(t, snapshot.Functions("json_decode")[0].Parameters, 4)
	require.Len(t, snapshot.Functions("json_encode")[0].Parameters, 3)
	require.Len(t, snapshot.Functions("array_filter")[0].Parameters, 3)
	require.Equal(t, "string", snapshot.Functions("substr")[0].ReturnType.String())
	require.Equal(t, "false|int", snapshot.Functions("strpos")[0].ReturnType.String())
	require.Equal(t, "string", snapshot.Functions("mb_substr")[0].ReturnType.String())
	require.Equal(t, "false|int", snapshot.Functions("mb_strpos")[0].ReturnType.String())
	parseString := snapshot.Functions("parse_str")
	require.Len(t, parseString, 1)
	require.True(t, parseString[0].Parameters[1].Flags.Has(
		semantic.ByReferenceFlag,
	))
	require.True(t, resolver.ResolveSignature(
		snapshot.Relations(),
		snapshot.Functions("class_exists")[0],
		[]resolver.Argument{{Type: types.String()}},
	).Compatible)
	require.True(t, resolver.ResolveSignature(
		snapshot.Relations(),
		snapshot.Functions("is_a")[0],
		[]resolver.Argument{
			{Type: types.String()},
			{Type: types.String()},
			{Type: types.Bool()},
		},
	).Compatible)
	pregMatchAll := snapshot.Functions("preg_match_all")
	require.Len(t, pregMatchAll, 1)
	require.True(t, pregMatchAll[0].Parameters[2].Flags.Has(
		semantic.ByReferenceFlag,
	))
	require.True(t, resolver.ResolveSignature(
		snapshot.Relations(),
		pregMatchAll[0],
		[]resolver.Argument{
			{Type: types.LiteralString("/pattern/")},
			{Type: types.String()},
			{Type: types.Unknown()},
		},
	).Compatible)
	require.True(t, snapshot.IsSubtypeOf("ArrayObject", "Countable"))
}

func TestProjectStubDocumentLoadsSelectedExtensionBundles(t *testing.T) {
	t.Parallel()
	version := project.Version{Major: 8, Minor: 3}
	core := semantic.NewSnapshot(1, []*semantic.Document{
		DocumentForExtensions(version, nil),
	})
	require.Len(t, core.Functions("strlen"), 1)
	require.Len(t, core.Functions("json_encode"), 1)
	require.Empty(t, core.Functions("curl_init"))
	require.Empty(t, core.Classes("Redis"))
	require.Empty(t, core.Classes("Imagick"))

	selected := semantic.NewSnapshot(1, []*semantic.Document{
		DocumentForExtensions(version, []string{"curl", "redis"}),
	})
	require.Len(t, selected.Functions("curl_init"), 1)
	require.Len(t, selected.Classes("Redis"), 1)
	require.Empty(t, selected.Classes("Imagick"))

	extension, found := ExtensionForSymbol("DOMDocument")
	require.True(t, found)
	require.Equal(t, "dom", extension)
	extension, found = ExtensionForSymbol("curl_init")
	require.True(t, found)
	require.Equal(t, "curl", extension)

	selectedDocument := DocumentForExtensions(version, []string{"curl", "redis"})
	require.Less(t, len(selectedDocument.Symbols), len(Document(version).Symbols))
	owners := make(map[semantic.SymbolID]struct{})
	for _, symbol := range selectedDocument.Symbols {
		if symbol.IsClassLike() {
			owners[symbol.ID] = struct{}{}
		}
	}
	for _, symbol := range selectedDocument.Symbols {
		if symbol.Container == "" {
			continue
		}
		_, exists := owners[symbol.Container]
		require.True(t, exists, "orphan selected-extension member %s", symbol.FullyQualified)
	}
}

func TestSelectedExtensionsHonorDisabledDependencies(t *testing.T) {
	t.Parallel()
	selected := SelectedExtensions(
		[]string{"dom", "xsl", "pdo_sqlite", "curl"},
		[]string{"libxml", "pdo"},
	)
	require.Contains(t, selected, "curl")
	require.NotContains(t, selected, "libxml")
	require.NotContains(t, selected, "dom")
	require.NotContains(t, selected, "xsl")
	require.NotContains(t, selected, "pdo")
	require.NotContains(t, selected, "pdo_sqlite")
}

func TestGeneratedPhpStormStubCoverage(t *testing.T) {
	t.Parallel()
	version := project.Version{Major: 8, Minor: 3}
	document := Document(version)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	for _, function := range []string{
		"array_combine",
		"array_map",
		"curl_init",
		"random_int",
		"str_replace",
	} {
		require.Len(t, snapshot.Functions(function), 1, function)
	}
	require.True(t, snapshot.Functions("array_map")[0].Flags.Has(
		semantic.GeneratedStubFlag,
	))
	require.False(t, snapshot.Functions("array_filter")[0].Flags.Has(
		semantic.GeneratedStubFlag,
	))
	for _, class := range []string{
		"Phar",
		"Random\\Randomizer",
		"SoapClient",
		"mysqli",
	} {
		require.Len(t, snapshot.Classes(class), 1, class)
	}

	seen := make(map[semantic.SymbolID]struct{}, len(document.Symbols))
	classIDs := make(map[semantic.SymbolID]struct{})
	for _, symbol := range document.Symbols {
		_, duplicate := seen[symbol.ID]
		require.False(t, duplicate, "duplicate generated/overlay symbol %s", symbol.FullyQualified)
		seen[symbol.ID] = struct{}{}
		if symbol.IsClassLike() {
			classIDs[symbol.ID] = struct{}{}
		}
	}
	for _, symbol := range document.Symbols {
		if symbol.Container == "" {
			continue
		}
		_, exists := classIDs[symbol.Container]
		require.True(t, exists, "orphan generated member %s", symbol.FullyQualified)
	}
}

func TestGeneratedPhpStormStubVersionAvailability(t *testing.T) {
	t.Parallel()
	php81 := semantic.NewSnapshot(1, []*semantic.Document{
		Document(project.Version{Major: 8, Minor: 1}),
	})
	php82 := semantic.NewSnapshot(1, []*semantic.Document{
		Document(project.Version{Major: 8, Minor: 2}),
	})
	require.Empty(t, php81.Classes("Random\\Randomizer"))
	require.Len(t, php82.Classes("Random\\Randomizer"), 1)
	require.Empty(t, php82.Functions("array_last"))
	php85 := semantic.NewSnapshot(1, []*semantic.Document{
		Document(project.Version{Major: 8, Minor: 5}),
	})
	require.Len(t, php85.Functions("array_last"), 1)
}

func TestGeneratedPhpStormAttributeSemantics(t *testing.T) {
	t.Parallel()
	document := Document(project.Version{Major: 8, Minor: 3})
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})

	pathinfo := snapshot.Functions("pathinfo")
	require.NotEmpty(t, pathinfo)
	require.Equal(t, types.UnionKind, pathinfo[0].ReturnType.Kind())
	var foundShape bool
	for index := 0; index < pathinfo[0].ReturnType.ArgumentCount(); index++ {
		if pathinfo[0].ReturnType.Argument(index).Kind() == types.ArrayShapeKind {
			foundShape = true
			break
		}
	}
	require.True(t, foundShape)
	require.Len(t, pathinfo[0].Parameters, 2)
	expected, found := semantic.AttributeNamed(
		pathinfo[0].Parameters[1].Attributes,
		"ExpectedValues",
	)
	require.True(t, found)
	flags, found := expected.Argument("flags", 1)
	require.True(t, found)
	require.Equal(t, semantic.AttributeValueArray, flags.Kind)
	require.NotEmpty(t, flags.Items)

	gettimeofday := snapshot.Functions("gettimeofday")
	require.NotEmpty(t, gettimeofday)
	require.Len(t, gettimeofday[0].Parameters, 1)
	trueResult := resolver.ResolveSignature(
		snapshot.Relations(),
		gettimeofday[0],
		[]resolver.Argument{{Type: types.True()}},
	)
	require.Equal(t, "float", trueResult.ReturnType.String())
	falseResult := resolver.ResolveSignature(
		snapshot.Relations(),
		gettimeofday[0],
		[]resolver.Argument{{Type: types.False()}},
	)
	require.Equal(t, types.ArrayShapeKind, falseResult.ReturnType.Kind())
	omittedResult := resolver.ResolveSignature(
		snapshot.Relations(),
		gettimeofday[0],
		nil,
	)
	require.Equal(t, types.ArrayShapeKind, omittedResult.ReturnType.Kind())
}

func TestGeneratedPhpStormStubVersionedDeprecations(t *testing.T) {
	t.Parallel()
	php82 := semantic.NewSnapshot(1, []*semantic.Document{
		Document(project.Version{Major: 8, Minor: 2}),
	})
	php85 := semantic.NewSnapshot(1, []*semantic.Document{
		Document(project.Version{Major: 8, Minor: 5}),
	})
	php86 := semantic.NewSnapshot(1, []*semantic.Document{
		Document(project.Version{Major: 8, Minor: 6}),
	})

	require.False(t, php82.Functions("curl_close")[0].Flags.Has(semantic.DeprecatedFlag))
	require.True(t, php85.Functions("curl_close")[0].Flags.Has(semantic.DeprecatedFlag))
	curlDeprecation, found := semantic.DeprecationOf(
		php85.Functions("curl_close")[0].Attributes(),
	)
	require.True(t, found)
	require.Equal(t, "Deprecated: it has no effect", curlDeprecation.Reason)
	require.Equal(t, "8.5", curlDeprecation.Since)
	require.False(t, php85.Functions("mb_eregi_replace")[0].Flags.Has(semantic.DeprecatedFlag))
	require.True(t, php86.Functions("mb_eregi_replace")[0].Flags.Has(semantic.DeprecatedFlag))
}

func TestGeneratedPhpStormStubProvenance(t *testing.T) {
	t.Parallel()
	repository, commit := generatedSource()
	require.Equal(t, "https://github.com/JetBrains/phpstorm-stubs.git", repository)
	require.Equal(t, "b872a0404e009a42cd4ff77a54505e01b2f126e3", commit)
}

func TestGeneratedPhpStormCallContracts(t *testing.T) {
	t.Parallel()
	contracts := generatedContracts()
	require.NotEmpty(t, contracts)

	var arrayPop bool
	var appendChild bool
	var extensionLoaded bool
	var ddExit bool
	var mockeryMap bool
	for _, contract := range contracts {
		switch contract.Target {
		case semantic.NewFunctionCallTarget("array_pop"):
			arrayPop = contract.Return.Kind ==
				semantic.CallReturnArgumentElementType &&
				contract.Return.Argument == 0
		case semantic.NewMethodCallTarget("DOMNode", "appendChild"):
			appendChild = contract.Return.Kind ==
				semantic.CallReturnArgumentType &&
				contract.Return.Argument == 0
		case semantic.NewFunctionCallTarget("extension_loaded"):
			for _, expected := range contract.ExpectedArguments {
				extensionLoaded = extensionLoaded ||
					expected.Argument == 0 && len(expected.Values) > 20
			}
		case semantic.NewFunctionCallTarget("dd"):
			ddExit = contract.ExitPoint
		case semantic.NewMethodCallTarget("Mockery", "mock"):
			mockeryMap = contract.Return.Kind == semantic.CallReturnArgumentMap
		}
	}
	require.True(t, arrayPop)
	require.True(t, appendChild)
	require.True(t, extensionLoaded)
	require.True(t, ddExit)
	require.True(t, mockeryMap)
}
