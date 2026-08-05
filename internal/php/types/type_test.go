package types

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

var (
	benchmarkType              Type
	benchmarkShapeField        ShapeField
	benchmarkCallableParameter CallableParameter
)

func TestBuiltinTypeParsingDoesNotAllocate(t *testing.T) {
	allocations := testing.AllocsPerRun(1000, func() {
		value, err := ParseNative("int")
		if err != nil {
			panic(err)
		}
		benchmarkType = value
	})
	require.Zero(t, allocations)
}

func BenchmarkSmallUnion(b *testing.B) {
	product := Named("Shopware\\Core\\Content\\Product")
	category := Named("Shopware\\Core\\Content\\Category")
	b.ReportAllocs()
	for b.Loop() {
		benchmarkType = Union(product, category, Null())
	}
}

func BenchmarkLiteralUnion(b *testing.B) {
	first := LiteralString("storefront")
	second := LiteralString("administration")
	b.ReportAllocs()
	for b.Loop() {
		benchmarkType = Union(first, second, Null())
	}
}

func BenchmarkArrayShapeConstruction(b *testing.B) {
	fields := []ShapeField{
		{Name: "active", Type: Bool()},
		{Name: "categories", Type: List(Named("Shopware\\Core\\Content\\Category"))},
		{Name: "count", Type: Int()},
		{Name: "id", Type: String()},
		{Name: "manufacturer", Type: Nullable(Named("Shopware\\Core\\Content\\Manufacturer"))},
		{Name: "name", Type: String(), Optional: true},
		{Name: "price", Type: Array(String(), Float())},
		{Name: "product", Type: Named("Shopware\\Core\\Content\\Product")},
		{Name: "status", Type: LiteralString("available")},
	}
	b.ReportAllocs()
	for b.Loop() {
		owned := append([]ShapeField(nil), fields...)
		benchmarkType = ArrayShapeOwned(owned, false)
	}
}

func TestTypeNodesStayCompact(t *testing.T) {
	t.Parallel()

	if size := unsafe.Sizeof(typeNode{}); size != 32 {
		t.Fatalf("type node size = %d bytes, want 32", size)
	}
	if size := unsafe.Sizeof(typeList{}); size > 16 {
		t.Fatalf("type list size = %d bytes, want at most 16", size)
	}
}

func TestRenderedTypeWriterMatchesCanonicalText(t *testing.T) {
	t.Parallel()

	values := []Type{
		LiteralString("line\n\"quoted\""),
		Named("Collection", LiteralString("ä")),
		Self(Template("T")),
		Static(Named("Entity")),
		Array(LiteralString("key"), List(Named("Product"))),
		Conditional(
			Template("T"),
			Named("Entity"),
			LiteralString("yes"),
			LiteralString(strings.Repeat("x", 80)),
		),
	}
	for _, value := range values {
		var builder strings.Builder
		builder.Grow(renderedLengthHint(value))
		writeRenderedType(&builder, value)
		require.Equal(t, value.String(), builder.String())
	}
}

func TestParseCanonicalTypes(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"?string":                                            "null|string",
		"string|null|string":                                 "null|string",
		"(Countable&Traversable)|null":                       "(Countable&Traversable)|null",
		"App\\Entity\\ProductEntity[]":                       "array<array-key,App\\Entity\\ProductEntity>",
		"array<string, ProductEntity>":                       "array<string,ProductEntity>",
		"iterable<int, ProductEntity>":                       "iterable<int,ProductEntity>",
		"non-empty-list<ProductEntity>":                      "non-empty-list<ProductEntity>",
		"class-string<ProductEntity>":                        "class-string<ProductEntity>",
		"Repository<covariant Collection<covariant Entity>>": "Repository<Collection<Entity>>",
		"Consumer<contravariant Entity>":                     "Consumer<Entity>",
		"Collection<*>":                                      "Collection<mixed>",
		"self<RuleError>":                                    "self<RuleError>",
		"static<T&Identified>":                               "static<Identified&T>",
		"callable(string, int=): bool":                       "callable(string,int=):bool",
		"\\Closure(string): int":                             "callable(string):int",
		"array{id:int,name?:string,...}":                     "array{id:int,name?:string,...}",
		"object{name:string,active?:bool}":                   "object{active?:bool,name:string}",
		"true|false":                                         "false|true",
		"bool|true|false":                                    "bool",
		"array<array-key,list<string|null>>":                 "array<array-key,list<null|string>>",
		"scalar":                                             "bool|float|int|string",
		"(T is 'array' ? ArrayNode<T> : Node<T>)":            `(T is "array" ? ArrayNode<T> : Node<T>)`,
		"($mapper is callable ? string : int)":               "($mapper is callable ? string : int)",
	}
	for source, expected := range tests {
		source := source
		expected := expected
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			actual, err := Parse(source)
			require.NoError(t, err)
			require.Equal(t, expected, actual.String())
		})
	}
}

func TestNonEmptyListRelationships(t *testing.T) {
	t.Parallel()
	relations := Relations{}
	nonEmpty := NonEmptyList(String())

	require.Equal(t, "non-empty-list<string>", nonEmpty.String())
	require.True(t, relations.IsSubtype(nonEmpty, List(String())))
	require.True(t, relations.IsSubtype(nonEmpty, Array(Int(), String())))
	require.True(t, relations.IsAssignableTo(nonEmpty, Iterable(Int(), String())))
	require.False(t, relations.IsSubtype(List(String()), nonEmpty))
	require.False(t, relations.IsAssignableTo(List(String()), nonEmpty))

	restored, err := ParsePersisted(nonEmpty.PersistedString())
	require.NoError(t, err)
	require.True(t, nonEmpty.Equal(restored))
}

func TestNonEmptyArrayRelationships(t *testing.T) {
	t.Parallel()
	relations := Relations{}
	nonEmpty := MustParse("non-empty-array<string, int>")

	require.True(t, NonEmptyArray(String(), Int()).Equal(nonEmpty))
	require.True(t, relations.IsSubtype(nonEmpty, Array(String(), Int())))
	require.True(t, relations.IsAssignableTo(
		nonEmpty,
		Iterable(String(), Int()),
	))
	require.False(t, relations.IsSubtype(Array(String(), Int()), nonEmpty))
	require.True(t, relations.IsSubtype(
		ArrayShape([]ShapeField{{Name: "id", Type: Int()}}, false),
		nonEmpty,
	))

	restored, err := ParsePersisted(nonEmpty.PersistedString())
	require.NoError(t, err)
	require.True(t, nonEmpty.Equal(restored))
}

func TestSpecialClassTypesPreserveGenericArguments(t *testing.T) {
	t.Parallel()

	value := Self(Template("T"))
	require.Equal(t, "self<T>", value.String())
	require.Equal(
		t,
		"self<Product>",
		Substitute(value, map[string]Type{"T": Named("Product")}).String(),
	)
	restored, err := ParsePersisted(value.PersistedString())
	require.NoError(t, err)
	require.True(t, value.Equal(restored))
	require.Equal(t, TemplateKind, restored.Argument(0).Kind())
}

func TestUnresolvedTemplateIsUncertain(t *testing.T) {
	t.Parallel()
	require.True(t, ContainsUncertain(Template("T")))
	require.True(t, ContainsUncertain(List(Template("T"))))
}

func TestBuiltinTypeClassificationDoesNotAllocate(t *testing.T) {
	var result string
	allocations := testing.AllocsPerRun(100, func() {
		result = builtinTypeName("INTERFACE-STRING")
	})
	require.Zero(t, allocations)
	require.Equal(t, "interface-string", result)
	require.Empty(t, builtinTypeName("App\\Product"))
}

func TestConditionalTypeSubstitutionSelectsBranch(t *testing.T) {
	t.Parallel()
	value := MustParse(
		"(T is 'array' ? ArrayNode<T> : Node<T>)",
	)
	require.Equal(
		t,
		`ArrayNode<"array">`,
		Substitute(value, map[string]Type{
			"T": LiteralString("array"),
		}).String(),
	)
	require.Equal(
		t,
		`Node<"string">`,
		Substitute(value, map[string]Type{
			"T": LiteralString("string"),
		}).String(),
	)
}

func TestCompositeTypeStringIsCached(t *testing.T) {
	value := Array(String(), Named("Product"))
	require.Equal(t, "array<string,Product>", value.String())

	allocations := testing.AllocsPerRun(100, func() {
		_ = value.Key()
	})
	require.Zero(t, allocations)
}

func TestJoinerCachesOnlyFinalCompositeType(t *testing.T) {
	relations := Relations{}
	joiner := NewJoiner(relations, Never())
	joiner.Add(LiteralString("red"))
	joiner.Add(LiteralString("green"))
	joiner.Add(LiteralString("blue"))

	require.Len(t, joiner.memberValues(), 3)
	require.Nil(t, joiner.value.node)

	value := joiner.Value()
	require.Equal(t, `"blue"|"green"|"red"`, value.String())
	require.Equal(t, value.String(), value.node.cachedText())
	require.True(t, value.Equal(
		relations.Join(
			relations.Join(Never(), LiteralString("red")),
			relations.Join(
				LiteralString("green"),
				LiteralString("blue"),
			),
		),
	))
}

func TestJoinerMatchesPairwiseJoin(t *testing.T) {
	t.Parallel()

	manyLiterals := []Type{Never()}
	for index := range maxPreciseLiteralUnionMembers + 2 {
		manyLiterals = append(
			manyLiterals,
			LiteralString(strconv.Itoa(index)),
		)
	}
	manyObjects := []Type{Never()}
	for index := range maxPreciseUnionMembers + 2 {
		manyObjects = append(
			manyObjects,
			Named("DomainType"+strconv.Itoa(index)),
		)
	}
	testCases := map[string][]Type{
		"single":           {Never()},
		"ordinary union":   {Never(), Int(), String(), Float()},
		"nested union":     {Never(), Union(Int(), String()), Float()},
		"bool normalized":  {Never(), True(), False(), Bool()},
		"unknown absorbs":  {Never(), Int(), Unknown(), String()},
		"literal widening": manyLiterals,
		"member ceiling":   manyObjects,
	}
	relations := Relations{}
	for name, values := range testCases {
		values := values
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			expected := values[0]
			for _, value := range values[1:] {
				expected = relations.Join(expected, value)
			}
			joiner := NewJoiner(relations, values[0])
			for _, value := range values[1:] {
				joiner.Add(value)
			}
			actual := joiner.Value()
			require.True(t, expected.Equal(actual))
			require.Equal(t, expected.String(), actual.String())
		})
	}
}

func TestSemanticNamesAndCompositeTextShareOneStringSlot(t *testing.T) {
	literal := LiteralString("storefront")
	require.Equal(t, "storefront", literal.Name())
	require.Equal(t, `"storefront"`, literal.String())

	union := Union(literal, Null())
	require.Empty(t, union.Name())
	require.Equal(t, `"storefront"|null`, union.String())
	require.Equal(t, union.String(), union.node.cachedText())
}

func TestArrayShapeOwnedTransfersFieldStorage(t *testing.T) {
	t.Parallel()

	fields := []ShapeField{
		{Name: "z", Type: String()},
		{Name: "a", Type: Int()},
	}
	value := ArrayShapeOwned(fields, false)

	require.Equal(t, "array{a:int,z:string}", value.String())
	require.Equal(t, "a", fields[0].Name)
	require.Equal(t, "z", fields[1].Name)
}

func TestArrayShapeKeepsCallerFieldStorageUntouched(t *testing.T) {
	t.Parallel()

	fields := []ShapeField{
		{Name: "z", Type: String()},
		{Name: "a", Type: Int()},
	}
	value := ArrayShape(fields, false)
	fields[0].Name = "changed"

	require.Equal(t, "array{a:int,z:string}", value.String())
	require.Equal(t, "changed", fields[0].Name)
	require.Equal(t, "a", fields[1].Name)
}

func TestTypeCollectionAccessorsDoNotAllocate(t *testing.T) {
	shape := ArrayShape([]ShapeField{
		{Name: "id", Type: Int()},
		{Name: "name", Type: String()},
	}, false)
	callable := Callable([]CallableParameter{
		{Name: "$id", Type: Int()},
		{Name: "$name", Type: String(), Optional: true},
	}, Bool())

	require.Equal(t, 2, shape.FieldCount())
	require.Equal(t, "id", shape.Field(0).Name)
	require.Equal(t, ShapeField{}, shape.Field(-1))
	require.Equal(t, ShapeField{}, shape.Field(2))
	require.Equal(t, 2, callable.ParameterCount())
	require.Equal(t, "$name", callable.Parameter(1).Name)
	require.Equal(t, CallableParameter{}, callable.Parameter(-1))
	require.Equal(t, CallableParameter{}, callable.Parameter(2))

	allocations := testing.AllocsPerRun(1_000, func() {
		benchmarkShapeField = shape.Field(1)
		benchmarkCallableParameter = callable.Parameter(1)
	})
	require.Zero(t, allocations)
}

func TestCommonLiteralTypesAreCanonical(t *testing.T) {
	require.Equal(t, `""`, LiteralString("").String())
	require.Equal(t, "0", LiteralInt("0").String())
	require.Equal(t, "1", LiteralInt("1").String())

	allocations := testing.AllocsPerRun(1_000, func() {
		benchmarkType = LiteralString("")
		benchmarkType = LiteralInt("0")
		benchmarkType = LiteralInt("1")
	})
	require.Zero(t, allocations)
}

func TestCompactTypeStorageKeepsArgumentsAndTextAlive(t *testing.T) {
	name := strings.Repeat("LongTypeName", 32)
	value := Named(name, Array(String(), Named("Product")))
	expected := strings.Clone(value.String())

	runtime.GC()

	require.Equal(t, expected, value.String())
	require.Equal(t, 1, value.ArgumentCount())
	require.Equal(t, "array<string,Product>", value.Argument(0).String())
}

func TestCompactTypeStorageKeepsDetailedPayloadsAlive(t *testing.T) {
	fieldName := strings.Repeat("field", 32)
	parameterName := "$" + strings.Repeat("parameter", 16)
	shape := ObjectShape([]ShapeField{{
		Name: fieldName,
		Type: List(Named("Product")),
	}}, true)
	value := Callable([]CallableParameter{{
		Name:     parameterName,
		Type:     shape,
		Optional: true,
	}}, Named("Result"))
	expected := strings.Clone(value.String())

	runtime.GC()

	require.Equal(t, expected, value.String())
	require.True(t, Named("Result").Equal(value.Result()))
	require.Equal(t, parameterName, value.Parameters()[0].Name)
	require.True(t, value.Parameters()[0].Optional)
	require.True(t, shape.IsOpenShape())
	require.Equal(t, fieldName, shape.Fields()[0].Name)
	require.Equal(t, "list<Product>", shape.Fields()[0].Type.String())
}

func TestTypeMsgpackEncodingRemainsBackwardCompatible(t *testing.T) {
	t.Parallel()
	expected := Named("Collection", Array(String(), Named("Product")))

	encoded, err := msgpack.Marshal(expected)
	require.NoError(t, err)
	var current Type
	require.NoError(t, msgpack.Unmarshal(encoded, &current))
	require.True(t, expected.Equal(current))

	legacy, err := msgpack.Marshal([]byte(expected.String()))
	require.NoError(t, err)
	var restoredLegacy Type
	require.NoError(t, msgpack.Unmarshal(legacy, &restoredLegacy))
	require.True(t, expected.Equal(restoredLegacy))
}

func TestStructuralTypeEqualityPreservesCanonicalSemantics(t *testing.T) {
	t.Parallel()

	left := Named("Collection", Union(
		LiteralString("storefront"),
		Null(),
	))
	right := Named("Collection", Union(
		Null(),
		LiteralString("storefront"),
	))
	require.True(t, left.Equal(right))
	require.Equal(t, left.Key(), right.Key())
	require.Equal(t, `Collection<"storefront"|null>`, left.Key())
}

func TestParseRejectsMalformedTypes(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"array<string",
		"callable(string",
		"array{id int}",
		"Foo|",
	} {
		_, err := Parse(source)
		require.Error(t, err, source)
	}
}

func TestParsePHPIntegerLiteralFormats(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"0o755",
		"0O644",
		"0xDEAD",
		"0b1010",
		"0755",
		"-0xF",
		"1_000",
	} {
		parsed, err := Parse(source)
		require.NoError(t, err, source)
		require.Equal(t, LiteralInt(source), parsed)
		require.Equal(t, source, parsed.String())
	}
}

func TestInvalidNamedTypesCollapseToUnknown(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"App\\",
		"App\\\\Product",
		"App\\Broken//comment",
		"App\\Settings.Result",
		"App\\Type,description",
		"pseudo-type-with-dashes",
	} {
		require.Equal(t, Unknown(), Named(source), source)
	}
	require.Equal(t, Unknown(), Template("T,value"))
	require.Equal(t, Int(), LiteralInt("// stdin\n1"))
	require.Equal(t, Float(), LiteralFloat("1.0 // description"))
}

func TestQualifiedNameValidationDoesNotAllocate(t *testing.T) {
	allocations := testing.AllocsPerRun(1_000, func() {
		if !validQualifiedName("Shopware\\Core\\Content\\Product") {
			t.Fatal("qualified name unexpectedly rejected")
		}
	})
	require.Zero(t, allocations)
}

func TestRelations(t *testing.T) {
	t.Parallel()
	relations := Relations{Hierarchy: testHierarchy{
		"Product": {"Entity": true},
	}}

	require.True(t, relations.IsSubtype(LiteralInt("1"), Int()))
	require.True(t, relations.IsSubtype(True(), Bool()))
	require.True(t, relations.IsSubtype(Named("Product"), Named("Entity")))
	require.True(t, relations.IsSubtype(
		Named("SHOPWARE\\Core\\Product"),
		Named("shopware\\core\\product"),
	))
	require.True(t, relations.IsSubtype(List(String()), Iterable(Int(), String())))
	iterableRelations := Relations{Hierarchy: testHierarchy{
		"Generator": {"Traversable": true},
	}}
	require.True(t, iterableRelations.IsSubtype(
		Named("Generator"),
		Iterable(Mixed(), Mixed()),
	))
	require.True(t, iterableRelations.IsSubtype(
		Named("Generator"),
		Iterable(ArrayKey(), Mixed()),
	))
	require.False(t, iterableRelations.IsSubtype(
		Named("Generator"),
		Iterable(Int(), String()),
	))
	require.False(t, iterableRelations.IsSubtype(
		Named("Generator"),
		Iterable(String(), Mixed()),
	))
	emptyArray := Array(ArrayKey(), Never())
	require.True(t, relations.IsSubtype(emptyArray, List(String())))
	require.True(t, relations.IsSubtype(emptyArray, Array(String(), Mixed())))
	require.True(t, relations.IsSubtype(emptyArray, Iterable(Int(), Object())))
	require.True(t, relations.IsSubtype(
		ArrayShape([]ShapeField{
			{Name: "0", Type: LiteralString("first")},
			{Name: "1", Type: LiteralString("second")},
		}, false),
		List(String()),
	))
	require.True(t, relations.IsSubtype(
		ArrayShape([]ShapeField{
			{Name: "0", Type: String()},
			{Name: "1", Type: String(), Optional: true},
		}, false),
		List(String()),
	))
	require.False(t, relations.IsSubtype(
		ArrayShape([]ShapeField{
			{Name: "0", Type: String(), Optional: true},
			{Name: "1", Type: String()},
		}, false),
		List(String()),
	))
	require.False(t, relations.IsSubtype(
		ArrayShape([]ShapeField{
			{Name: "0", Type: String()},
			{Name: "2", Type: String()},
		}, false),
		List(String()),
	))
	require.True(t, relations.IsSubtype(
		ArrayShape([]ShapeField{
			{Name: "email", Type: String()},
		}, false),
		ArrayShape([]ShapeField{
			{Name: `"email"`, Type: String()},
		}, false),
	))
	require.True(t, relations.IsSubtype(
		Array(ArrayKey(), Never()),
		ArrayShape([]ShapeField{
			{Name: "optional", Type: String(), Optional: true},
		}, false),
	))
	require.True(t, relations.IsAssignableTo(Int(), Float()))
	require.True(t, relations.IsAssignableTo(
		List(ArrayShape([]ShapeField{
			{Name: "factor", Type: LiteralInt("1")},
			{Name: "key", Type: String()},
		}, false)),
		List(Array(String(), Union(Float(), String()))),
	))
	require.True(t, relations.IsAssignableTo(
		Int(),
		Union(Float(), Null()),
	))
	require.True(t, relations.IsAssignableTo(
		Union(LiteralInt("0"), Float()),
		Float(),
	))
	require.True(t, relations.IsAssignableTo(
		Array(ArrayKey(), Never()),
		Conditional(
			Unknown(),
			Callable(nil, Mixed()),
			Array(String(), List(String())),
			Array(String(), List(Array(String(), String()))),
		),
	))
	require.True(t, relations.IsSubtype(
		ArrayKey(),
		Union(Int(), String()),
	))
	require.True(t, relations.IsSubtype(
		Bool(),
		Union(True(), False()),
	))
	require.True(t, relations.IsSubtype(
		Callable(nil, Void()),
		Callable(
			[]CallableParameter{{Type: Named("Connection")}},
			Void(),
		),
	))
	require.False(t, relations.IsSubtype(
		Callable(
			[]CallableParameter{{Type: Named("Connection")}},
			Void(),
		),
		Callable(nil, Void()),
	))
	require.True(t, relations.IsAssignableTo(
		Callable(
			[]CallableParameter{{Type: Named("Symfony\\ItemInterface")}},
			String(),
		),
		Union(
			Named("CallbackInterface", String()),
			Callable(
				[]CallableParameter{
					{Type: Named("Psr\\CacheItemInterface")},
					{Type: Bool()},
				},
				String(),
			),
			Callable(
				[]CallableParameter{
					{Type: Named("Symfony\\ItemInterface")},
					{Type: Bool()},
				},
				String(),
			),
		),
	))
	require.True(t, relations.IsAssignableTo(
		Array(ArrayKey(), String()),
		Union(
			List(Union(String(), Int(), Float(), Null())),
			Array(String(), String()),
		),
	))
	require.True(t, relations.IsAssignableTo(
		Array(ArrayKey(), Union(Null(), String())),
		Union(
			List(Union(String(), Int(), Float(), Null())),
			Array(String(), String()),
		),
	))
	require.False(t, relations.IsAssignableTo(
		Array(ArrayKey(), Int()),
		Union(List(String()), Array(String(), String())),
	))
	require.False(t, relations.IsAssignableTo(
		Array(ArrayKey(), Object()),
		Union(List(String()), Array(String(), String())),
	))
	require.False(t, relations.IsSubtype(Mixed(), String()))
	require.False(t, relations.IsSubtype(Unknown(), Mixed()))
	require.Equal(t, "int|string", relations.Join(Int(), String()).String())
	require.Equal(t, "string", relations.Narrow(Union(String(), Null()), String()).String())
	require.Equal(t, "string", relations.Without(Union(String(), Null()), Null()).String())
}

func TestJoinArrayShapeBranchesKeepsOptionalFields(t *testing.T) {
	t.Parallel()
	relations := Relations{}
	empty := Array(ArrayKey(), Never())
	withLanguage := ArrayShape([]ShapeField{
		{Name: "languageId", Type: String()},
	}, false)
	withCurrency := ArrayShape([]ShapeField{
		{Name: "currencyId", Type: String()},
	}, false)

	require.Equal(
		t,
		"array{languageId?:string}",
		relations.Join(empty, withLanguage).String(),
	)
	require.Equal(
		t,
		"array{currencyId?:string,languageId?:string}",
		relations.Join(withLanguage, withCurrency).String(),
	)
	require.Equal(
		t,
		"array{currencyId?:string,languageId:string}",
		relations.Join(
			withLanguage,
			ArrayShape([]ShapeField{
				{Name: "currencyId", Type: String()},
				{Name: "languageId", Type: String()},
			}, false),
		).String(),
	)
	require.Equal(
		t,
		"list<string>",
		relations.Join(empty, ArrayShape([]ShapeField{
			{Name: "0", Type: String()},
		}, false)).String(),
	)
}

func TestArrayShapeAssignmentCarriesScalarWidening(t *testing.T) {
	t.Parallel()
	relations := Relations{}
	require.True(t, relations.IsAssignableTo(
		ArrayShape([]ShapeField{
			{Name: "standardRate", Type: LiteralInt("20")},
			{Name: "reduced", Type: LiteralInt("7")},
		}, false),
		ArrayShape([]ShapeField{
			{Name: "standardRate", Type: Float()},
			{Name: "reduced", Type: Float(), Optional: true},
		}, false),
	))
}

func TestGenericObjectVariance(t *testing.T) {
	t.Parallel()
	relations := Relations{Hierarchy: varianceHierarchy{
		testHierarchy: testHierarchy{
			"Product": {"Entity": true},
		},
		variances: map[string][]Variance{
			"Producer": {Covariant},
			"Consumer": {Contravariant},
		},
	}}
	entity := Named("Entity")
	product := Named("Product")
	require.True(t, relations.IsSubtype(
		Named("Producer", product),
		Named("Producer", entity),
	))
	require.True(t, relations.IsSubtype(
		Named("Consumer", entity),
		Named("Consumer", product),
	))
	require.False(t, relations.IsSubtype(
		Named("Box", product),
		Named("Box", entity),
	))
	require.True(t, relations.IsSubtype(
		Named("Box", product),
		Named("Box", product),
	))
	require.True(t, relations.IsSubtype(
		Named("Box", entity),
		Named("Box", Union(product, entity)),
	))
	require.True(t, relations.IsAssignableTo(
		Named("Box"),
		Named("Box", product),
	))
	require.True(t, relations.IsAssignableTo(
		ClassString(Named("Box")),
		ClassString(Named("Box", product)),
	))
	require.True(t, relations.IsSubtype(
		Named("Box", Named("Collection", product)),
		Named("Box", Named("Collection")),
	))
}

func TestStructuralAndCallableRelations(t *testing.T) {
	t.Parallel()
	relations := Relations{Hierarchy: testHierarchy{
		"Product": {"Entity": true},
	}}
	require.True(t, relations.IsSubtype(
		Array(String(), Named("Product")),
		Array(String(), Named("Entity")),
	))
	require.True(t, relations.IsSubtype(
		ArrayShape([]ShapeField{
			{Name: "id", Type: String()},
			{Name: "product", Type: Named("Product")},
		}, false),
		ArrayShape([]ShapeField{
			{Name: "product", Type: Named("Entity")},
		}, false),
	))
	require.False(t, relations.IsSubtype(
		ArrayShape([]ShapeField{{Name: "id", Type: String(), Optional: true}}, false),
		ArrayShape([]ShapeField{{Name: "id", Type: String()}}, false),
	))
	require.True(t, relations.IsSubtype(
		Callable(
			[]CallableParameter{{Type: Object()}},
			Named("Product"),
		),
		Callable(
			[]CallableParameter{{Type: Named("Product")}},
			Named("Entity"),
		),
	))
	require.True(t, relations.IsSubtype(
		Iterable(ArrayKey(), Named("Product")),
		Union(Array(ArrayKey(), Named("Entity")), Named("Traversable")),
	))
}

func TestPHPCallableRepresentationsAreAssignable(t *testing.T) {
	t.Parallel()
	relations := Relations{}
	callable := Callable(nil, Mixed())
	for _, value := range []Type{
		String(),
		LiteralString("handler"),
		Array(ArrayKey(), Mixed()),
		List(Mixed()),
		Object(),
		ArrayShape([]ShapeField{
			{Name: "0", Type: ClassString(Named("Handler"))},
			{Name: "1", Type: LiteralString("run")},
		}, false),
	} {
		require.True(t, relations.IsAssignableTo(value, callable), value.String())
	}
	require.False(t, relations.IsAssignableTo(
		ArrayShape([]ShapeField{{Name: "name", Type: String()}}, false),
		callable,
	))
}

func TestRelationsWidenLargeLiteralUnions(t *testing.T) {
	t.Parallel()
	relations := Relations{}
	value := Never()
	for index := range 1_000 {
		value = relations.Join(value, LiteralString(strconv.Itoa(index)))
	}
	require.Equal(t, String(), value)
}

func TestRelationsWidenLiteralUnionsBeforeStructuralUnions(t *testing.T) {
	t.Parallel()

	relations := Relations{}
	literals := Never()
	for index := range maxPreciseLiteralUnionMembers + 1 {
		literals = relations.Join(
			literals,
			LiteralString(strconv.Itoa(index)),
		)
	}
	require.Equal(t, String(), literals)

	objects := Never()
	for index := range maxPreciseLiteralUnionMembers + 1 {
		objects = relations.Join(
			objects,
			Named("Example"+strconv.Itoa(index)),
		)
	}
	require.Equal(t, UnionKind, objects.Kind())
	require.Len(t, objects.Arguments(), maxPreciseLiteralUnionMembers+1)
}

type testHierarchy map[string]map[string]bool

func (h testHierarchy) IsSubtypeOf(candidate, target string) bool {
	return h[candidate][target]
}

type varianceHierarchy struct {
	testHierarchy
	variances map[string][]Variance
}

func (h varianceHierarchy) TemplateVariance(name string, index int) Variance {
	values := h.variances[name]
	if index < 0 || index >= len(values) {
		return Invariant
	}
	return values[index]
}

func TestTypesAreImmutableAtAPIBoundary(t *testing.T) {
	t.Parallel()
	original := Named("Collection", String())
	require.Equal(t, 1, original.ArgumentCount())
	require.Equal(t, String(), original.Argument(0))
	require.Equal(t, Unknown(), original.Argument(-1))
	require.Equal(t, Unknown(), original.Argument(1))
	arguments := original.Arguments()
	arguments[0] = Int()
	require.Equal(t, "Collection<string>", original.String())

	callable := Callable([]CallableParameter{{Name: "$value", Type: String()}}, Bool())
	parameters := callable.Parameters()
	parameters[0].Type = Int()
	require.Equal(t, "callable(string $value):bool", callable.String())
}

func TestDetacherPreservesCanonicalTypesAndSharing(t *testing.T) {
	t.Parallel()
	named := Named("App\\Product")
	value := Array(String(), List(named))
	detacher := NewDetacher(nil)

	detached := detacher.Type(value)
	require.True(t, value.Equal(detached))
	require.NotSame(t, value.node, detached.node)
	require.Same(
		t,
		detacher.Type(named).node,
		detacher.Type(named).node,
	)
	require.Same(t, String().node, detacher.Type(String()).node)
}

func TestSubstituteTemplates(t *testing.T) {
	t.Parallel()
	value := MustParse("array<TKey,list<TValue|null>>")
	actual := Substitute(value, map[string]Type{
		"TKey":   String(),
		"TValue": Named("Product"),
	})
	require.Equal(t, "array<string,list<Product|null>>", actual.String())
}

func TestPersistedTypePreservesNonConventionalTemplateNames(t *testing.T) {
	t.Parallel()
	original := Callable(
		[]CallableParameter{{
			Name: "$ids",
			Type: Array(ArrayKey(), Template("IDStructure")),
		}},
		ArrayShape([]ShapeField{{
			Name: "value",
			Type: Template("IDStructure"),
		}}, false),
	)

	encoded := original.PersistedString()
	require.Contains(t, encoded, persistedTemplatePrefix+"IDStructure")
	restored, err := ParsePersisted(encoded)
	require.NoError(t, err)
	require.True(t, original.Equal(restored))
	require.Equal(
		t,
		TemplateKind,
		restored.Parameters()[0].Type.Arguments()[1].Kind(),
	)
	require.Equal(
		t,
		TemplateKind,
		restored.Result().Fields()[0].Type.Kind(),
	)
}

func TestPersistedUnionKeepsCallableArmsGrouped(t *testing.T) {
	t.Parallel()
	template := Template("T")
	original := Union(
		Named("CallbackInterface", template),
		Callable(
			[]CallableParameter{
				{Type: Named("CacheItemInterface")},
				{Type: Bool()},
			},
			template,
		),
		Callable(
			[]CallableParameter{
				{Type: Named("ItemInterface")},
				{Type: Bool()},
			},
			template,
		),
	)
	encoded := original.PersistedString()
	require.Contains(t, encoded, "(callable(")
	restored, err := ParsePersisted(encoded)
	require.NoError(t, err)
	require.True(t, original.Equal(restored))
}

func TestParsePersistedAcceptsLegacyCanonicalType(t *testing.T) {
	t.Parallel()
	restored, err := ParsePersisted("array<string,Product>")
	require.NoError(t, err)
	require.Equal(t, "array<string,Product>", restored.String())
}

func TestParsePersistedRoundTripsNullableCallableShapeField(t *testing.T) {
	t.Parallel()
	original := ArrayShape([]ShapeField{
		{
			Name:     "clientCertSource",
			Type:     Nullable(Callable(nil, Mixed())),
			Optional: true,
		},
		{Name: "credentials", Type: Null()},
	}, false)

	restored, err := ParsePersisted(original.PersistedString())
	require.NoError(t, err)
	require.True(t, original.Equal(restored))
}

func TestInferredShapeQuotesNonIdentifierKeys(t *testing.T) {
	t.Parallel()
	original := ArrayShape([]ShapeField{{
		Name: "grpc.service_config_disable_resolution",
		Type: LiteralInt("1"),
	}}, false)

	require.Equal(
		t,
		`array{"grpc.service_config_disable_resolution":1}`,
		original.String(),
	)
	restored, err := ParsePersisted(original.PersistedString())
	require.NoError(t, err)
	require.True(t, original.Equal(restored))
}

func TestParsePersistedLoadsInferredClientOptionsShape(t *testing.T) {
	t.Parallel()
	_, err := ParsePersisted(
		`array|array{apiEndpoint:unknown,clientCertSource:callable,credentials:null,credentialsConfig:array-shape,disableRetries:false,gapicVersion:unknown,libName:null,libVersion:null,logger?:Psr\Log\LoggerInterface|false|null,transport:null,transportConfig:array<mixed,array{stubOpts?:array{grpc.service_config_disable_resolution:1}}>,universeDomain:unknown}|array{apiEndpoint:unknown,clientCertSource?:callable&null,credentials:null,credentialsConfig:array-shape,disableRetries:false,gapicVersion:unknown,libName:null,libVersion:null,logger?:Psr\Log\LoggerInterface|false|null,transport:null,transportConfig:array<mixed,array{stubOpts?:array{grpc.service_config_disable_resolution:1}}>,universeDomain:unknown}`,
	)
	require.NoError(t, err)
}

func TestParsePersistedAcceptsNonASCIIClassName(t *testing.T) {
	t.Parallel()
	original := Named("\uFFFD")
	require.False(t, original.IsUnknown())
	restored, err := ParsePersisted(original.PersistedString())
	require.NoError(t, err)
	require.True(t, original.Equal(restored))
}

func TestSubstitutePreservesBareObject(t *testing.T) {
	t.Parallel()
	require.Equal(t, Object(), Substitute(Object(), map[string]Type{
		"T": String(),
	}))
}

func TestClassStringIsAValidArrayKey(t *testing.T) {
	t.Parallel()
	relations := Relations{}
	require.True(
		t,
		relations.IsSubtype(
			ClassString(Named("Plugin")),
			ArrayKey(),
		),
	)
	require.True(
		t,
		relations.IsSubtype(
			Array(ClassString(Named("Plugin")), Named("Plugin")),
			Array(ArrayKey(), Mixed()),
		),
	)
}

func TestArrayKeyIsBenevolentlyAssignableToEitherScalarKeyType(t *testing.T) {
	t.Parallel()
	relations := Relations{}
	require.True(t, relations.IsAssignableTo(ArrayKey(), Int()))
	require.True(t, relations.IsAssignableTo(ArrayKey(), String()))
	require.False(t, relations.IsSubtype(ArrayKey(), Int()))
	require.False(t, relations.IsSubtype(ArrayKey(), String()))
	require.False(
		t,
		relations.IsAssignableTo(Union(Int(), String()), String()),
	)
}

func TestParseNativeDoesNotGuessTemplates(t *testing.T) {
	t.Parallel()
	value, err := ParseNative("T")
	require.NoError(t, err)
	require.Equal(t, ObjectKind, value.Kind())
	require.Equal(t, "T", value.Name())
}
