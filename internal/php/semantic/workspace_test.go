package semantic

import (
	"bytes"
	"math"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

func TestWorkspaceDocumentRoundTripPreservesEveryRetainedSymbolField(t *testing.T) {
	t.Parallel()

	symbol := Symbol{
		ID:                 "method",
		Kind:               MethodSymbol,
		Name:               "find",
		FullyQualified:     "App\\Repository::find",
		Container:          "repository",
		Path:               "/repository.php",
		Range:              cst.TextRange{Start: 10, End: 80},
		SelectionRange:     cst.TextRange{Start: 20, End: 24},
		BodyRange:          cst.TextRange{Start: 30, End: 79},
		Visibility:         Protected,
		WriteVisibility:    Private,
		HasWriteVisibility: true,
		Flags:              StaticFlag | FinalFlag | SoftFinalFlag,
		Type:               types.MustParse("callable(int): Product"),
		NativeType:         types.MustParse("Product"),
		DocType:            types.MustParse("Product|null"),
		ReturnType:         types.MustParse("Product"),
		Parameters: []Parameter{{
			ID:             "parameter",
			Name:           "$id",
			Type:           types.Int(),
			NativeType:     types.Int(),
			DocType:        types.MustParse("positive-int"),
			AssistantTags:  []string{"Autowire"},
			Range:          cst.TextRange{Start: 25, End: 29},
			SelectionRange: cst.TextRange{Start: 26, End: 28},
			DefaultRange:   cst.TextRange{Start: 28, End: 29},
			Flags:          ByReferenceFlag,
			Optional:       true,
		}},
	}
	symbol.SetSignatureExtras(
		[]TemplateParameter{{
			Name:      "T",
			Bound:     types.Named("Entity"),
			Default:   types.Named("Product"),
			Covariant: true,
		}},
		[]types.Type{types.Named("RuntimeException")},
		[]TypeAssertion{{
			Target:      "$this",
			Type:        types.Named("App\\ProductRepository"),
			WhenTrue:    true,
			Conditional: true,
			Negated:     true,
		}},
		[]LiteralReturn{{
			Value: "null",
			Range: cst.TextRange{Start: 60, End: 64},
			Type:  types.Null(),
		}},
		[]ConstantReturn{{
			Receiver: "Product",
			Name:     "DEFAULT",
			Range:    cst.TextRange{Start: 65, End: 75},
		}},
	)
	symbol.SetHierarchy(
		[]string{"App\\BaseRepository"},
		[]string{"App\\RepositoryInterface"},
		[]string{"App\\RepositoryTrait"},
		[]types.Type{types.MustParse("App\\BaseRepository<Product>")},
		[]types.Type{types.MustParse("App\\RepositoryInterface<Product>")},
		[]types.Type{types.Named("App\\RepositoryTrait")},
		[]TraitAlias{{
			Trait:         "App\\RepositoryTrait",
			Method:        "find",
			Alias:         "aliasedFind",
			Visibility:    Private,
			HasVisibility: true,
		}},
	)
	symbol.SetMetadata(
		[]Attribute{{
			Name:  "Deprecated",
			Range: cst.TextRange{Start: 1, End: 9},
		}},
		[]ConstantArrayItem{{
			Key:        "key",
			KeyRange:   cst.TextRange{Start: 31, End: 34},
			Value:      "value",
			ValueRange: cst.TextRange{Start: 36, End: 41},
			Type:       types.String(),
		}},
		"Finds a product.",
	)
	document := &Document{
		Path:              symbol.Path,
		Version:           7,
		WorkspaceRevision: 11,
		Namespace:         "App",
		Symbols:           []Symbol{symbol},
		References: []Reference{referenceWithTargets(Reference{
			Name:       "Product",
			Kind:       ClassName,
			Receiver:   types.Named("App\\Repository"),
			TargetKind: ClassSymbol,
			Static:     true,
			Write:      true,
			Range:      cst.TextRange{Start: 50, End: 57},
			Scope:      2,
			Resolved:   "product",
		}, []string{"App\\Product"}, []SymbolID{"product", "legacy-product"})},
		CallContracts: []CallContract{{
			Target: NewMethodCallTarget("App\\Repository", "find"),
			Return: CallReturnContract{
				Kind:     CallReturnArgumentType,
				Argument: 0,
			},
		}},
	}

	packed := packWorkspaceDocument(document)

	expected := document.Clone()
	expected.Scopes = nil
	expected.References[0].Scope = 0
	requireMsgpackEquivalent(t, expected, packed.materialize())
	require.Len(t, packed.signatures, 1)
	require.Len(t, packed.signatureExtras, 1)
	require.Len(t, packed.hierarchies, 1)
	require.Len(t, packed.metadata, 1)
	require.Equal(
		t,
		[]string{"App\\Product"},
		packed.reference(0).QualifiedNames(),
	)
	require.Equal(
		t,
		[]SymbolID{"product", "legacy-product"},
		packed.reference(0).CandidateIDs(),
	)

	encoded, err := msgpack.Marshal(PackWorkspaceGraphOwned(document))
	require.NoError(t, err)
	var decoded WorkspaceGraph
	require.NoError(t, msgpack.Unmarshal(encoded, &decoded))
	requireMsgpackEquivalent(t, expected, decoded.Document())
}

func TestWorkspaceSymbolRangesUseSparseExactFallbacks(t *testing.T) {
	t.Parallel()

	document := &Document{
		Path: "/ranges.php",
		Symbols: []Symbol{{
			ID:        "compact",
			Kind:      ClassSymbol,
			Path:      "/ranges.php",
			Range:     cst.TextRange{Start: 10, End: 100},
			BodyRange: cst.TextRange{Start: 20, End: 99},
			Flags:     InternalFlag,
		}, {
			ID:             "large-declaration",
			Kind:           ClassSymbol,
			Path:           "/ranges.php",
			Range:          cst.TextRange{Start: 200, End: 200 + math.MaxUint16},
			SelectionRange: cst.TextRange{Start: 210, End: 220},
			BodyRange:      cst.TextRange{Start: 230, End: 240},
			Flags:          DeprecatedFlag,
		}, {
			ID:             "large-body",
			Kind:           MethodSymbol,
			Path:           "/ranges.php",
			Range:          cst.TextRange{Start: 300, End: 400},
			SelectionRange: cst.TextRange{Start: 310, End: 320},
			BodyRange:      cst.TextRange{Start: 330, End: 330 + math.MaxUint16},
			Flags:          StaticFlag | FinalFlag,
		}},
	}

	packed := packWorkspaceDocument(document)
	require.NotNil(t, packed.symbolRangeExtras)
	require.Len(t, packed.symbolRangeExtras.Values, 2)
	require.Zero(t, packed.Symbols[0].rangeIndex())
	require.Equal(t, uint32(1), packed.Symbols[1].rangeIndex())
	require.Equal(t, uint32(2), packed.Symbols[2].rangeIndex())
	requireMsgpackEquivalent(t, document, packed.materialize())

	encoded, err := msgpack.Marshal(PackWorkspaceGraphOwned(document))
	require.NoError(t, err)
	var decoded WorkspaceGraph
	require.NoError(t, msgpack.Unmarshal(encoded, &decoded))
	requireMsgpackEquivalent(t, document, decoded.Document())
	require.Len(t, decoded.document.symbolRangeExtras.Values, 2)
}

func TestWorkspaceSymbolsShareTheirDocumentPath(t *testing.T) {
	t.Parallel()

	graph := PackWorkspaceGraphOwned(&Document{
		Path: "/workspace.php",
		Symbols: []Symbol{
			{ID: "class", Kind: ClassSymbol, Path: "/workspace.php"},
			{ID: "method", Kind: MethodSymbol, Path: "/workspace.php"},
		},
	})

	document := graph.document
	require.Len(t, document.Symbols, 2)
	for index := range document.Symbols {
		require.Same(t, document, document.Symbols[index].Document)
		require.Equal(t, document.Path, document.Symbols[index].path())
	}

	encoded, err := msgpack.Marshal(graph)
	require.NoError(t, err)
	var decoded WorkspaceGraph
	require.NoError(t, msgpack.Unmarshal(encoded, &decoded))
	for index := range decoded.document.Symbols {
		require.Same(
			t,
			decoded.document,
			decoded.document.Symbols[index].Document,
		)
	}
}

func TestWorkspaceSymbolRejectsInvalidWireLayout(t *testing.T) {
	t.Parallel()

	encoded, err := msgpack.Marshal([]any{"only-one-field"})
	require.NoError(t, err)
	var symbol workspaceSymbol
	require.ErrorContains(
		t,
		msgpack.Unmarshal(encoded, &symbol),
		"expected 20 fields",
	)
}

func TestWorkspaceDecoderBoundsDeclaredCollectionsAndStrings(t *testing.T) {
	t.Parallel()

	var collection bytes.Buffer
	require.NoError(
		t,
		msgpack.NewEncoder(&collection).EncodeArrayLen(
			maxWorkspaceCollectionItems+1,
		),
	)
	_, err := decodeWorkspaceStrings(
		msgpack.NewDecoder(&collection),
		NewWorkspaceGraphDecoder(),
	)
	require.ErrorContains(t, err, "length 1048577 exceeds 1048576")

	context := NewWorkspaceGraphDecoder()
	context.stringCache = newWorkspaceStringInterner(0)
	_, err = context.decodeString(msgpack.NewDecoder(bytes.NewReader([]byte{
		0xdb, 0x08, 0x00, 0x00, 0x01,
	})))
	require.ErrorContains(t, err, "length 134217729 exceeds 134217728")
}

func TestWorkspaceGraphDecoderOwnsStableArenaStrings(t *testing.T) {
	t.Parallel()

	encoded := func(value string) *msgpack.Decoder {
		var buffer bytes.Buffer
		require.NoError(t, msgpack.NewEncoder(&buffer).EncodeString(value))
		return msgpack.NewDecoder(&buffer)
	}
	context := NewWorkspaceGraphDecoder()
	context.stringCache = newWorkspaceStringInterner(0)

	first, err := context.decodeString(encoded("App\\First"))
	require.NoError(t, err)
	second, err := context.decodeString(encoded("App\\Second"))
	require.NoError(t, err)
	require.Equal(t, "App\\First", first)
	require.Equal(t, "App\\Second", second)
	require.NotEmpty(t, context.stringArena)

	context.Clear()
	require.Nil(t, context.stringArena)
	runtime.GC()
	require.Equal(t, "App\\First", first)
	require.Equal(t, "App\\Second", second)
}

func TestWorkspaceGraphDecoderSharesTypesAcrossDocuments(t *testing.T) {
	t.Parallel()

	valueType := types.MustParse("App\\Collection<App\\Product>")
	encode := func(path string) []byte {
		graph := PackWorkspaceGraphOwned(&Document{
			Path: path,
			Symbols: []Symbol{{
				ID:   SymbolID(path),
				Kind: ClassSymbol,
				Path: path,
				Type: valueType,
			}},
		})
		encoded, err := msgpack.Marshal(graph)
		require.NoError(t, err)
		return encoded
	}

	context := NewWorkspaceGraphDecoder()
	var first WorkspaceGraph
	require.NoError(t, context.Decode(
		msgpack.NewDecoder(bytes.NewReader(encode("/first.php"))),
		&first,
	))
	var second WorkspaceGraph
	require.NoError(t, context.Decode(
		msgpack.NewDecoder(bytes.NewReader(encode("/second.php"))),
		&second,
	))
	require.True(
		t,
		first.document.Symbols[0].Type ==
			second.document.Symbols[0].Type,
	)

	context.Clear()
	require.Nil(t, context.typeCache)
	var afterClear WorkspaceGraph
	require.NoError(t, context.Decode(
		msgpack.NewDecoder(bytes.NewReader(encode("/third.php"))),
		&afterClear,
	))
	require.False(
		t,
		first.document.Symbols[0].Type ==
			afterClear.document.Symbols[0].Type,
	)
}

func TestWorkspaceSymbolsAllocateOnlyUsedSideTables(t *testing.T) {
	t.Parallel()

	metadataSymbol := Symbol{ID: "metadata", Kind: ClassConstantSymbol}
	metadataSymbol.SetDocSummary("A constant.")
	hierarchySymbol := Symbol{ID: "hierarchy", Kind: ClassSymbol}
	hierarchySymbol.SetExtends([]string{"Base"})
	document := &Document{Symbols: []Symbol{
		{ID: "plain", Kind: PropertySymbol},
		{ID: "signature", Kind: MethodSymbol, Parameters: []Parameter{{Name: "$id"}}},
		hierarchySymbol,
		metadataSymbol,
	}}

	packed := packWorkspaceDocument(document)

	require.Nil(t, packed.Symbols[0].signature())
	require.Nil(t, packed.Symbols[0].hierarchy())
	require.Nil(t, packed.Symbols[0].metadata())
	require.NotNil(t, packed.Symbols[1].signature())
	require.NotNil(t, packed.Symbols[2].hierarchy())
	require.NotNil(t, packed.Symbols[3].metadata())
	require.Len(t, packed.signatures, 1)
	require.Empty(t, packed.signatureExtras)
	require.Len(t, packed.hierarchies, 1)
	require.Len(t, packed.metadata, 1)
}

func TestWorkspaceParameterAcceptsLegacyAndCompactWireLayouts(t *testing.T) {
	t.Parallel()

	source := []Parameter{{
		ID:            "parameter",
		Name:          "$service",
		Type:          types.Named("App\\Service"),
		NativeType:    types.Named("App\\Service"),
		DocType:       types.MustParse("App\\Service|null"),
		AssistantTags: []string{"Service", "Class"},
		Attributes: []Attribute{{
			Name: "ExpectedValues",
			Arguments: []AttributeArgument{{
				Name: "values",
				Value: AttributeValue{
					Kind:       AttributeValueArray,
					Expression: "['one']",
					Items: []AttributeArrayItem{{
						Value: AttributeValue{
							Kind:       AttributeValueString,
							Value:      "one",
							Expression: "'one'",
						},
					}},
				},
			}},
		}},
		DefaultValue: &AttributeValue{
			Kind:       AttributeValueBool,
			Value:      "false",
			Expression: "false",
		},
		Range:          cst.TextRange{Start: 10, End: 30},
		SelectionRange: cst.TextRange{Start: 20, End: 28},
		DefaultRange:   cst.TextRange{Start: 29, End: 30},
		Flags:          VariadicFlag,
		Optional:       true,
	}, {
		ID:         "documented",
		Name:       "$documented",
		Type:       types.String(),
		NativeType: types.Mixed(),
		DocType:    types.String(),
	}, {
		ID:         "explicit",
		Name:       "$explicit",
		Type:       types.Float(),
		NativeType: types.Int(),
		DocType:    types.String(),
	}}

	legacyEncoded, err := msgpack.Marshal(source)
	require.NoError(t, err)
	var packed []workspaceParameter
	require.NoError(t, msgpack.Unmarshal(legacyEncoded, &packed))
	requireMsgpackEquivalent(t, source, materializeWorkspaceParameters(packed))

	packedEncoded, err := msgpack.Marshal(packWorkspaceParameters(source))
	require.NoError(t, err)
	var restored []workspaceParameter
	require.NoError(t, msgpack.Unmarshal(packedEncoded, &restored))
	requireMsgpackEquivalent(t, source, materializeWorkspaceParameters(restored))
}

func requireMsgpackEquivalent(
	t *testing.T,
	expected any,
	actual any,
) {
	t.Helper()
	expectedEncoded, err := msgpack.Marshal(expected)
	require.NoError(t, err)
	actualEncoded, err := msgpack.Marshal(actual)
	require.NoError(t, err)
	require.Equal(t, expectedEncoded, actualEncoded)
}

func TestWorkspaceParameterRejectsInvalidCompactWireLayout(t *testing.T) {
	t.Parallel()

	invalidLength, err := msgpack.Marshal([]any{uint8(1)})
	require.NoError(t, err)
	var parameter workspaceParameter
	require.ErrorContains(
		t,
		msgpack.Unmarshal(invalidLength, &parameter),
		"expected 15, 16, or 17 fields",
	)

	unsupportedVersion := make([]any, 15)
	unsupportedVersion[0] = uint8(5)
	encoded, err := msgpack.Marshal(unsupportedVersion)
	require.NoError(t, err)
	require.ErrorContains(
		t,
		msgpack.Unmarshal(encoded, &parameter),
		"unsupported layout 5",
	)
}

func TestWorkspaceSignatureRestoresMixedParameterWireVersionsByIndex(
	t *testing.T,
) {
	t.Parallel()

	wireParameter := func(version uint8, id, name string) []any {
		return []any{
			version,
			id,
			name,
			types.Int(),
			types.Int(),
			types.Type{},
			nil,
			uint32(0),
			uint32(0),
			uint32(0),
			uint32(0),
			uint32(0),
			uint32(0),
			uint32(0),
			false,
		}
	}
	wireParameters := []any{
		wireParameter(2, "v2-first", "$first"),
		wireParameter(1, "v1-second", "$second"),
		wireParameter(2, "v2-third", "$third"),
	}
	encoded, err := msgpack.Marshal([]any{
		wireParameters,
		nil,
		nil,
		nil,
		nil,
	})
	require.NoError(t, err)

	var signature workspaceSignature
	require.NoError(t, msgpack.Unmarshal(encoded, &signature))
	parameters := materializeWorkspaceParameters(signature.Parameters)
	require.Equal(
		t,
		[]SymbolID{"v2-first", "v1-second", "v2-third"},
		[]SymbolID{parameters[0].ID, parameters[1].ID, parameters[2].ID},
	)

	parameterBytes, err := msgpack.Marshal(wireParameters)
	require.NoError(t, err)
	var deferred []decodedWorkspaceParameterID
	packed, err := decodeWorkspaceParameters(
		msgpack.NewDecoder(bytes.NewReader(parameterBytes)),
		NewWorkspaceGraphDecoder(),
		&deferred,
	)
	require.NoError(t, err)
	document := &workspaceDocument{
		Symbols: []workspaceSymbol{{ID: "owner"}},
	}
	document.attachDecodedSymbolSides(
		[]decodedWorkspaceSymbolSides{{
			signature:        &workspaceSignature{Parameters: packed},
			parameterIDCount: uint32(len(deferred)),
		}},
		deferred,
	)
	parameters = materializeWorkspaceParameters(
		document.Symbols[0].signature().Parameters,
		&document.Symbols[0],
	)
	require.Equal(
		t,
		[]SymbolID{"v2-first", "v1-second", "v2-third"},
		[]SymbolID{parameters[0].ID, parameters[1].ID, parameters[2].ID},
	)
}

func TestWorkspaceReferenceDecodesLegacyLayout(t *testing.T) {
	t.Parallel()

	type legacyWorkspaceReference struct {
		_msgpack struct{} `msgpack:",as_array"` //nolint:unused // Legacy encoding layout marker.

		Name           string
		Resolved       SymbolID
		Receiver       types.Type
		Range          cst.TextRange
		QualifiedStart uint32
		QualifiedCount uint32
		CandidateStart uint32
		CandidateCount uint32
		Scope          ScopeID
		Kind           NameKind
		TargetKind     SymbolKind
		Flags          uint8
	}
	legacy := legacyWorkspaceReference{
		Name:           "Product",
		Resolved:       "product",
		Receiver:       types.Named("App\\Product"),
		Range:          cst.TextRange{Start: 10, End: 17},
		QualifiedStart: 2,
		QualifiedCount: 3,
		CandidateStart: 5,
		CandidateCount: 7,
		Scope:          42,
		Kind:           MemberName,
		TargetKind:     MethodSymbol,
		Flags:          workspaceReferenceStatic,
	}

	encoded, err := msgpack.Marshal(legacy)
	require.NoError(t, err)
	var decoded persistedWorkspaceReference
	require.NoError(t, msgpack.Unmarshal(encoded, &decoded))
	require.Equal(t, legacy.Name, decoded.Name)
	require.Equal(t, legacy.Resolved, decoded.Resolved)
	require.True(t, legacy.Receiver.Equal(decoded.Receiver))
	require.Equal(t, legacy.Range, decoded.Range)
	require.Equal(t, legacy.QualifiedStart, decoded.QualifiedStart)
	require.Equal(t, uint16(legacy.QualifiedCount), decoded.QualifiedCount)
	require.Equal(t, legacy.CandidateStart, decoded.CandidateStart)
	require.Equal(t, uint16(legacy.CandidateCount), decoded.CandidateCount)
	require.Equal(t, legacy.Kind, decoded.Kind)
	require.Equal(t, legacy.TargetKind, decoded.TargetKind)
	require.Equal(t, legacy.Flags, decoded.Flags)
}

func TestWorkspaceGraphDecodesLegacyReferenceTables(t *testing.T) {
	t.Parallel()

	type legacyWorkspaceGraph struct {
		_msgpack struct{} `msgpack:",as_array"` //nolint:unused // Legacy encoding layout marker.

		Path                string
		Version             int
		WorkspaceRevision   uint64
		Namespace           string
		Symbols             []workspaceSymbol
		References          []persistedWorkspaceReference
		ReferenceQualified  []string
		ReferenceCandidates []SymbolID
	}
	legacy := legacyWorkspaceGraph{
		Path:              "/legacy.php",
		Version:           2,
		WorkspaceRevision: 3,
		Namespace:         "App",
		References: []persistedWorkspaceReference{{
			Name:           "Product",
			Resolved:       "product",
			Receiver:       types.Named("App\\Repository"),
			Range:          cst.TextRange{Start: 10, End: 17},
			QualifiedStart: 0,
			QualifiedCount: 1,
			CandidateStart: 0,
			CandidateCount: 1,
			Kind:           ClassName,
			TargetKind:     ClassSymbol,
		}},
		ReferenceQualified:  []string{"App\\Product"},
		ReferenceCandidates: []SymbolID{"product"},
	}

	encoded, err := msgpack.Marshal(legacy)
	require.NoError(t, err)
	var decoded WorkspaceGraph
	require.NoError(t, msgpack.Unmarshal(encoded, &decoded))

	document := decoded.Document()
	require.Equal(t, legacy.Path, document.Path)
	require.Equal(t, legacy.Namespace, document.Namespace)
	require.Len(t, document.References, 1)
	reference := document.References[0]
	require.Equal(t, "Product", reference.Name)
	require.Equal(t, SymbolID("product"), reference.Resolved)
	require.True(t, reference.Receiver.Equal(types.Named("App\\Repository")))
	require.Equal(t, cst.TextRange{Start: 10, End: 17}, reference.Range)
	require.Equal(t, []string{"App\\Product"}, reference.QualifiedNames())
	require.Equal(t, []SymbolID{"product"}, reference.CandidateIDs())
	require.Equal(t, ClassName, reference.Kind)
	require.Equal(t, ClassSymbol, reference.TargetKind)
}

func TestWorkspaceReferencesDeduplicateRetainedTables(t *testing.T) {
	t.Parallel()

	reference := referenceWithTargets(Reference{
		Name:       "find",
		Resolved:   "repository::find",
		Receiver:   types.Named("App\\Repository"),
		Kind:       MemberName,
		TargetKind: MethodSymbol,
	}, []string{"App\\Repository"}, []SymbolID{"repository::find"})
	document := &Document{
		Path:       "/repository.php",
		References: []Reference{reference, reference},
	}

	packed := packWorkspaceDocument(document)

	require.Equal(t, 3, packed.referenceStringCount())
	require.Len(t, packed.referenceTypes, 1)
	require.Len(t, packed.referenceValues, 4)
	require.Equal(
		t,
		packed.References[0].nameIndex(),
		packed.References[1].nameIndex(),
	)
	require.Equal(
		t,
		packed.References[0].resolvedIndex(),
		packed.References[1].resolvedIndex(),
	)
	require.Equal(
		t,
		packed.References[0].receiverIndex(),
		packed.References[1].receiverIndex(),
	)
	require.Equal(t, document.References, packed.materializeReferences())
}

func TestWorkspaceReferenceStringsShareBatchTableWithLocalIDs(t *testing.T) {
	t.Parallel()

	firstBacking := strings.Repeat("a", 1024) + "find"
	secondBacking := strings.Repeat("b", 1024) + "find"
	firstName := firstBacking[len(firstBacking)-len("find"):]
	secondName := secondBacking[len(secondBacking)-len("find"):]
	require.False(
		t,
		unsafe.StringData(firstName) == unsafe.StringData(secondName),
	)
	first := ProjectWorkspaceGraphBorrowed(&Document{
		Path: "/first.php",
		References: []Reference{{
			Name:     firstName,
			Resolved: "repository::find",
			Kind:     MemberName,
		}},
	})
	second := ProjectWorkspaceGraphBorrowed(&Document{
		Path: "/second.php",
		References: []Reference{{
			Name:     secondName,
			Resolved: "repository::legacyFind",
			Kind:     MemberName,
		}},
	})
	detacher := NewWorkspaceGraphDetacherCapacity(2)
	detacher.DetachOwned(first)
	detacher.DetachOwned(second)
	detacher.Finish()

	require.Same(
		t,
		first.document.referenceStrings,
		second.document.referenceStrings,
	)
	require.Len(t, first.document.referenceStrings.Values, 3)
	require.Equal(t, 2, first.document.referenceStringCount())
	require.Equal(t, 2, second.document.referenceStringCount())
	require.Equal(
		t,
		"find",
		first.document.referenceString(
			first.document.References[0].nameIndex(),
		),
	)
	require.Equal(
		t,
		"repository::legacyFind",
		second.document.referenceString(
			second.document.References[0].resolvedIndex(),
		),
	)
	require.Equal(
		t,
		len(first.document.referenceStrings.Values),
		cap(first.document.referenceStrings.Values),
	)
	require.NotEqual(t, [2]uint64{}, second.document.referenceBloom)

	encoded, err := msgpack.Marshal(second)
	require.NoError(t, err)
	var decoded WorkspaceGraph
	require.NoError(t, msgpack.Unmarshal(encoded, &decoded))
	require.Equal(
		t,
		second.document.referenceBloom,
		decoded.document.referenceBloom,
	)
	requireMsgpackEquivalent(t, second.Document(), decoded.Document())
}

func TestReferenceBloomHashNormalizesReferencePrefixesAndCase(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		referenceBloomHash("\\App\\Target"),
		referenceBloomHash("app\\target"),
	)
	require.Equal(
		t,
		referenceBloomHash("$Variable"),
		referenceBloomHash("variable"),
	)
}

func TestWorkspaceSymbolStringsShareBatchTableWithRemappedIndexes(
	t *testing.T,
) {
	t.Parallel()

	firstBacking := strings.Repeat("a", 1024) + "Shared"
	secondBacking := strings.Repeat("b", 1024) + "Shared"
	firstName := firstBacking[len(firstBacking)-len("Shared"):]
	secondName := secondBacking[len(secondBacking)-len("Shared"):]
	require.False(
		t,
		unsafe.StringData(firstName) == unsafe.StringData(secondName),
	)
	first := ProjectWorkspaceGraphBorrowed(&Document{
		Path: "/first.php",
		Symbols: []Symbol{{
			ID:             "first",
			Kind:           ClassSymbol,
			Name:           firstName,
			FullyQualified: "App\\Shared",
		}},
	})
	second := ProjectWorkspaceGraphBorrowed(&Document{
		Path: "/second.php",
		Symbols: []Symbol{{
			ID:             "second",
			Kind:           MethodSymbol,
			Name:           secondName,
			FullyQualified: "App\\Shared::shared",
			Container:      "owner",
		}},
		References: []Reference{{
			Name:     secondName,
			Resolved: "owner",
			Kind:     MemberName,
		}},
	})
	detacher := NewWorkspaceGraphDetacherCapacity(4)
	detacher.DetachOwned(first)
	detacher.DetachOwned(second)
	detacher.Finish()

	require.Same(t, first.document.symbolStrings, second.document.symbolStrings)
	require.Same(
		t,
		second.document.symbolStrings,
		second.document.referenceStrings,
	)
	require.True(t, first.document.symbolStrings.Shared)
	require.Len(t, first.document.symbolStrings.Values, 4)
	require.Equal(t, "Shared", first.document.Symbols[0].name())
	require.Equal(t, "App\\Shared", first.document.Symbols[0].fullyQualified())
	require.Equal(t, SymbolID("owner"), second.document.Symbols[0].container())
	require.Equal(
		t,
		"App\\Shared::shared",
		second.document.Symbols[0].fullyQualified(),
	)
	require.Equal(
		t,
		"Shared",
		second.document.referenceString(
			second.document.References[0].nameIndex(),
		),
	)
	require.Equal(
		t,
		len(first.document.symbolStrings.Values),
		cap(first.document.symbolStrings.Values),
	)

	encoded, err := msgpack.Marshal(second)
	require.NoError(t, err)
	var decoded WorkspaceGraph
	require.NoError(t, msgpack.Unmarshal(encoded, &decoded))
	requireMsgpackEquivalent(t, second.Document(), decoded.Document())

	internCalls := make(map[string]int)
	internPackedWorkspaceGraphOwned(
		first.document,
		func(value string) string {
			internCalls[value]++
			return value
		},
		func(value types.Type) types.Type {
			return value
		},
	)
	require.Zero(t, internCalls["Shared"])
	require.Zero(t, internCalls["App\\Shared"])
}

func TestWorkspaceSymbolStringsDecodeDirectlyIntoSharedTable(t *testing.T) {
	t.Parallel()

	encode := func(document *Document) []byte {
		graph := ProjectWorkspaceGraph(document)
		encoded, err := msgpack.Marshal(graph)
		require.NoError(t, err)
		return encoded
	}
	firstEncoded := encode(&Document{
		Path: "/first.php",
		Symbols: []Symbol{{
			ID:             "first",
			Kind:           ClassSymbol,
			Name:           "Shared",
			FullyQualified: "App\\Shared",
		}},
	})
	secondEncoded := encode(&Document{
		Path: "/second.php",
		Symbols: []Symbol{{
			ID:             "second",
			Kind:           MethodSymbol,
			Name:           "Shared",
			FullyQualified: "App\\Shared::shared",
			Container:      "owner",
		}},
		References: []Reference{{
			Name:     "Shared",
			Resolved: "owner",
			Kind:     MemberName,
		}},
	})

	decoder := NewWorkspaceGraphDecoder()
	decoder.stringCache = newWorkspaceStringInterner(8)
	var first WorkspaceGraph
	require.NoError(
		t,
		decoder.Decode(
			msgpack.NewDecoder(bytes.NewReader(firstEncoded)),
			&first,
		),
	)
	var second WorkspaceGraph
	require.NoError(
		t,
		decoder.Decode(
			msgpack.NewDecoder(bytes.NewReader(secondEncoded)),
			&second,
		),
	)

	require.Same(t, first.document.symbolStrings, second.document.symbolStrings)
	require.Same(
		t,
		second.document.symbolStrings,
		second.document.referenceStrings,
	)
	require.True(t, first.document.symbolStrings.Shared)
	require.Len(t, first.document.symbolStrings.Values, 4)
	require.Equal(t, "Shared", first.document.Symbols[0].name())
	require.Equal(
		t,
		"App\\Shared::shared",
		second.document.Symbols[0].fullyQualified(),
	)
	decoder.Clear()
	require.Equal(t, "Shared", first.document.Symbols[0].name())
	require.Equal(t, SymbolID("owner"), second.document.Symbols[0].container())
}

func TestWorkspaceReferencePacksIndexesAndMetadataLosslessly(t *testing.T) {
	t.Parallel()

	source := newWorkspaceReference(
		cst.TextRange{Start: 100, End: 200},
		workspaceReferenceIndexMask,
		workspaceReferenceIndexMask-1,
		workspaceReferenceValueMask,
		workspaceReferenceIndexMask-2,
		255,
		254,
		VariableName,
		TemplateSymbol,
		workspaceReferenceFlagsMask,
		-1,
	)

	encoded, err := msgpack.Marshal(source)
	require.NoError(t, err)
	var decoded workspaceReference
	require.NoError(t, msgpack.Unmarshal(encoded, &decoded))

	require.Equal(t, source.rangeValue(nil), decoded.rangeValue(nil))
	require.Equal(t, source.nameIndex(), decoded.nameIndex())
	require.Equal(t, source.resolvedIndex(), decoded.resolvedIndex())
	require.Equal(t, source.valueStart(nil), decoded.valueStart(nil))
	require.Equal(t, source.receiverIndex(), decoded.receiverIndex())
	require.Equal(t, source.qualifiedCount(), decoded.qualifiedCount())
	require.Equal(t, source.candidateCount(), decoded.candidateCount())
	require.Equal(t, source.kind(), decoded.kind())
	require.Equal(t, source.targetKind(), decoded.targetKind())
	require.Equal(t, source.flags(), decoded.flags())

	require.Panics(t, func() {
		newWorkspaceReference(
			cst.TextRange{},
			workspaceReferenceIndexMask+1,
			0,
			0,
			0,
			0,
			0,
			ClassName,
			ClassSymbol,
			0,
			-1,
		)
	})
}

func TestWorkspaceReferenceLocationUsesSparseExactFallback(t *testing.T) {
	t.Parallel()

	document := &Document{
		Path: "/large-reference.php",
		References: []Reference{{
			Name:  "Product",
			Kind:  ClassName,
			Range: cst.TextRange{Start: 100, End: 100 + math.MaxUint16},
		}},
	}

	graph := PackWorkspaceGraphOwned(document)
	require.NotNil(t, graph.document.referenceExtras)
	require.Len(t, graph.document.referenceExtras.Values, 1)
	require.True(t, graph.document.References[0].hasFullLocation())
	expected := document.Clone()
	expected.Symbols = []Symbol{}
	expected.Scopes = nil
	requireMsgpackEquivalent(t, expected, graph.Document())

	encoded, err := msgpack.Marshal(graph)
	require.NoError(t, err)
	var decoded WorkspaceGraph
	require.NoError(t, msgpack.Unmarshal(encoded, &decoded))
	requireMsgpackEquivalent(t, expected, decoded.Document())
	require.Len(t, decoded.document.referenceExtras.Values, 1)

	valueDocument := &workspaceDocument{}
	valueReference := valueDocument.newReference(
		cst.TextRange{Start: 10, End: 20},
		0,
		0,
		1<<16,
		0,
		0,
		0,
		ClassName,
		ClassSymbol,
		0,
	)
	rng, valueStart := valueReference.location(valueDocument)
	require.Equal(t, cst.TextRange{Start: 10, End: 20}, rng)
	require.Equal(t, uint32(1<<16), valueStart)
	require.True(t, valueReference.hasFullLocation())
}

var (
	benchmarkWorkspaceReferenceRange      cst.TextRange
	benchmarkWorkspaceReferenceValueStart uint32
	benchmarkWorkspaceReferenceString     string
	benchmarkWorkspaceSymbolString        string
)

func BenchmarkWorkspaceReferenceLocationAccess(b *testing.B) {
	newDocument := func(rng cst.TextRange, valueStart uint32) *workspaceDocument {
		document := &workspaceDocument{}
		document.References = []workspaceReference{document.newReference(
			rng,
			0,
			0,
			valueStart,
			0,
			0,
			0,
			ClassName,
			ClassSymbol,
			0,
		)}
		return document
	}
	compact := newDocument(cst.TextRange{Start: 100, End: 120}, 12)
	full := newDocument(
		cst.TextRange{Start: 100, End: 100 + math.MaxUint16},
		1<<16,
	)

	for name, document := range map[string]*workspaceDocument{
		"compact": compact,
		"full":    full,
	} {
		b.Run(name, func(b *testing.B) {
			reference := &document.References[0]
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkWorkspaceReferenceRange,
					benchmarkWorkspaceReferenceValueStart =
					reference.location(document)
			}
		})
	}
}

func BenchmarkWorkspaceReferenceStringAccess(b *testing.B) {
	source := &Document{References: []Reference{{
		Name:     "find",
		Resolved: "repository::find",
		Kind:     MemberName,
	}}}
	local := packWorkspaceDocument(source)
	shared := ProjectWorkspaceGraphBorrowed(source)
	detacher := NewWorkspaceGraphDetacherCapacity(1)
	detacher.DetachOwned(shared)
	detacher.Finish()

	for name, document := range map[string]*workspaceDocument{
		"local":  local,
		"shared": shared.document,
	} {
		b.Run(name, func(b *testing.B) {
			index := document.References[0].nameIndex()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkWorkspaceReferenceString =
					document.referenceString(index)
			}
		})
	}
}

func BenchmarkWorkspaceSymbolStringAccess(b *testing.B) {
	graph := ProjectWorkspaceGraphBorrowed(&Document{
		Path: "/service.php",
		Symbols: []Symbol{{
			ID:             "service",
			Kind:           ClassSymbol,
			Name:           "Service",
			FullyQualified: "App\\Service",
		}},
	})
	detacher := NewWorkspaceGraphDetacherCapacity(3)
	detacher.DetachOwned(graph)
	detacher.Finish()
	symbol := &graph.document.Symbols[0]

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkWorkspaceSymbolString = symbol.name()
	}
}

func TestWorkspaceReferenceCapacityHintsStayBelowOccurrenceCounts(
	t *testing.T,
) {
	t.Parallel()

	require.Zero(t, workspaceReferenceStringCapacity(0, 0))
	require.Equal(t, 4, workspaceReferenceStringCapacity(4, 6))
	require.Equal(t, 1, workspaceReferenceTypeCapacity(1))
	require.Equal(t, 25, workspaceReferenceTypeCapacity(100))
}

func TestWorkspaceReferencePackerMaterializesExactTables(t *testing.T) {
	t.Parallel()

	document := &workspaceDocument{}
	packer := &workspaceReferencePacker{
		document: document,
		stringIndex: map[string]uint32{
			"second": 2,
			"first":  1,
		},
		typeIndex: map[types.Type]uint32{
			types.Named("App\\Second"): 2,
			types.Named("App\\First"):  1,
		},
	}
	packer.finishTables()

	require.Equal(
		t,
		[]string{"first", "second"},
		document.referenceStrings.Values,
	)
	require.Equal(
		t,
		len(document.referenceStrings.Values),
		cap(document.referenceStrings.Values),
	)
	require.True(t, document.referenceTypes[0].Equal(types.Named("App\\First")))
	require.True(t, document.referenceTypes[1].Equal(types.Named("App\\Second")))
	require.Equal(t, len(document.referenceTypes), cap(document.referenceTypes))
}

func TestProjectWorkspaceGraphMatchesTwoStageProjection(t *testing.T) {
	t.Parallel()

	backing := strings.Repeat("x", 4096) + "App\\Product"
	className := backing[len(backing)-len("App\\Product"):]
	product := Symbol{
		ID:             "product",
		Kind:           ClassSymbol,
		Name:           className,
		FullyQualified: className,
		Path:           "/project/Product.php",
		Type:           types.Named(className),
	}
	product.SetExtends([]string{"App\\BaseProduct"})
	product.SetDocSummary("A product.")
	document := &Document{
		Path:      "/project/Product.php",
		Namespace: "App",
		Symbols: []Symbol{
			product,
			{
				ID:        "local",
				Kind:      LocalSymbol,
				Name:      "$product",
				Path:      "/project/Product.php",
				Container: "product",
			},
		},
		References: []Reference{
			referenceWithTargets(Reference{
				Name:       className,
				Kind:       ClassName,
				Receiver:   types.Named(className),
				TargetKind: ClassSymbol,
				Range:      cst.TextRange{Start: 10, End: 17},
				Resolved:   "product",
			}, []string{className, "Product"}, []SymbolID{"product"}),
			{
				Name:  "$product",
				Kind:  VariableName,
				Range: cst.TextRange{Start: 20, End: 28},
			},
		},
	}

	expected := PackWorkspaceGraphOwned(document.WorkspaceGraph()).Document()
	actual := ProjectWorkspaceGraph(document).Document()

	require.Equal(t, expected, actual)
	require.Len(t, actual.Symbols, 1)
	require.Len(t, actual.References, 1)
	require.False(
		t,
		unsafe.StringData(className) == unsafe.StringData(actual.Symbols[0].Name),
	)
}

func TestBorrowedWorkspaceProjectionOwnsSlicesAndBatchDetachesValues(
	t *testing.T,
) {
	t.Parallel()

	firstBacking := strings.Repeat("a", 4096) + "App\\Shared"
	secondBacking := strings.Repeat("b", 4096) + "App\\Shared"
	firstName := firstBacking[len(firstBacking)-len("App\\Shared"):]
	secondName := secondBacking[len(secondBacking)-len("App\\Shared"):]
	firstSymbol := Symbol{
		ID:             "first",
		Kind:           ClassSymbol,
		Name:           firstName,
		FullyQualified: firstName,
		Path:           "/project/First.php",
		Type:           types.Named(firstName),
		Parameters: []Parameter{{
			Name:          "$value",
			AssistantTags: []string{firstName},
		}},
	}
	firstSymbol.SetExtends([]string{firstName})
	first := &Document{
		Path:    "/project/First.php",
		Symbols: []Symbol{firstSymbol},
	}
	second := &Document{
		Path: "/project/Second.php",
		Symbols: []Symbol{{
			ID:             "second",
			Kind:           ClassSymbol,
			Name:           secondName,
			FullyQualified: secondName,
			Path:           "/project/Second.php",
			Type:           types.Named(secondName),
		}},
	}

	firstGraph := ProjectWorkspaceGraphBorrowed(first)
	secondGraph := ProjectWorkspaceGraphBorrowed(second)
	first.Symbols[0].Extends()[0] = "App\\Mutated"
	first.Symbols[0].Parameters[0].AssistantTags[0] = "Mutated"

	borrowed := firstGraph.Document()
	require.Equal(t, []string{"App\\Shared"}, borrowed.Symbols[0].Extends())
	require.Equal(
		t,
		[]string{"App\\Shared"},
		borrowed.Symbols[0].Parameters[0].AssistantTags,
	)
	require.True(
		t,
		unsafe.StringData(firstName) ==
			unsafe.StringData(borrowed.Symbols[0].Name),
	)

	detacher := NewWorkspaceGraphDetacher()
	detacher.DetachOwned(firstGraph)
	detacher.DetachOwned(secondGraph)
	require.Len(t, detacher.stringArena, 1)
	detachedFirst := firstGraph.Document()
	detachedSecond := secondGraph.Document()

	require.False(
		t,
		unsafe.StringData(firstName) ==
			unsafe.StringData(detachedFirst.Symbols[0].Name),
	)
	require.False(
		t,
		unsafe.StringData(secondName) ==
			unsafe.StringData(detachedSecond.Symbols[0].Name),
	)
	require.True(
		t,
		unsafe.StringData(detachedFirst.Symbols[0].Name) ==
			unsafe.StringData(detachedSecond.Symbols[0].Name),
	)
	require.True(
		t,
		unsafe.StringData(detachedFirst.Symbols[0].Type.Name()) ==
			unsafe.StringData(detachedSecond.Symbols[0].Type.Name()),
	)
	detacher = nil
	runtime.GC()
	require.Equal(t, "App\\Shared", detachedFirst.Symbols[0].Name)
	require.Equal(t, "App\\Shared", detachedSecond.Symbols[0].Name)
}

func TestWorkspaceSymbolCoreIsLessThanHalfPublicSymbolSize(t *testing.T) {
	t.Parallel()

	require.Equal(t, uintptr(104), unsafe.Sizeof(workspaceSymbol{}))
	require.Less(
		t,
		unsafe.Sizeof(workspaceSymbol{}),
		unsafe.Sizeof(Symbol{})/2,
	)
	require.Less(
		t,
		unsafe.Sizeof(compactReferenceLocation{}),
		unsafe.Sizeof(ReferenceLocation{}),
	)
	require.Equal(t, uintptr(20), unsafe.Sizeof(workspaceReference{}))
	require.Equal(t, uintptr(12), unsafe.Sizeof(workspaceReferenceFull{}))
	require.Equal(t, uintptr(16), unsafe.Sizeof(workspaceSymbolRanges{}))
	require.Equal(t, uintptr(24), unsafe.Sizeof(workspaceSymbolFullRanges{}))
	require.Equal(t, uintptr(32), unsafe.Sizeof(workspaceSignature{}))
	require.Equal(t, uintptr(56), unsafe.Sizeof(workspaceSignatureExtras{}))
	require.Equal(t, uintptr(64), unsafe.Sizeof(workspaceParameter{}))
	require.Equal(t, uintptr(40), unsafe.Sizeof(workspaceParameterExtras{}))
	require.Equal(t, uintptr(56), unsafe.Sizeof(workspaceParameterMetadata{}))
	require.Equal(t, uintptr(16), unsafe.Sizeof(workspaceParameterRanges{}))
	require.Equal(t, uintptr(24), unsafe.Sizeof(workspaceParameterFullRanges{}))
	require.Equal(t, uintptr(48), unsafe.Sizeof(workspaceHierarchy{}))
	require.Equal(t, uintptr(32), unsafe.Sizeof(workspaceHierarchyTypes{}))
	require.Equal(t, uintptr(24), unsafe.Sizeof(workspaceMetadata{}))
	require.Equal(t, uintptr(24), unsafe.Sizeof(workspaceMetadataExtras{}))
	require.Less(
		t,
		unsafe.Sizeof(workspaceReference{}),
		unsafe.Sizeof(Reference{})*3/4,
	)
}

func TestWorkspaceSymbolPacksSideTableIndexesLosslessly(t *testing.T) {
	t.Parallel()

	var symbol workspaceSymbol
	symbol.setSideIndexes(10, 20, 30)

	require.Equal(
		t,
		uint64(11),
		symbol.sideIndex(workspaceSymbolSignatureShift),
	)
	require.Equal(
		t,
		uint64(21),
		symbol.sideIndex(workspaceSymbolHierarchyShift),
	)
	require.Equal(
		t,
		uint64(31),
		symbol.sideIndex(workspaceSymbolMetadataShift),
	)

	symbol.setSideIndexes(-1, -1, -1)
	require.Zero(t, symbol.sideIndexes)
	require.Panics(t, func() {
		symbol.setSideIndexes(
			int(workspaceSymbolSideIndexMask),
			-1,
			-1,
		)
	})
}

func TestWorkspaceParameterRangesPackAndFallBackLosslessly(t *testing.T) {
	t.Parallel()

	compactSource := Parameter{
		Range:          cst.TextRange{Start: 100, End: 140},
		SelectionRange: cst.TextRange{Start: 110, End: 115},
	}
	compact := packWorkspaceParameter(&compactSource, nil)
	require.Nil(t, compact.Extras)
	require.Equal(t, compactSource.Range, compact.materialize().Range)
	require.Equal(
		t,
		compactSource.SelectionRange,
		compact.materialize().SelectionRange,
	)
	require.Zero(t, compact.materialize().DefaultRange)

	fallbackSource := Parameter{
		Range:          cst.TextRange{Start: 10, End: 100_000},
		SelectionRange: cst.TextRange{Start: 20, End: 30},
		DefaultRange:   cst.TextRange{Start: 40, End: 90_000},
	}
	fallback := packWorkspaceParameter(&fallbackSource, nil)
	require.NotNil(t, fallback.Extras)
	require.NotNil(t, fallback.Extras.Ranges)
	require.Equal(t, fallbackSource.Range, fallback.materialize().Range)
	require.Equal(
		t,
		fallbackSource.SelectionRange,
		fallback.materialize().SelectionRange,
	)
	require.Equal(
		t,
		fallbackSource.DefaultRange,
		fallback.materialize().DefaultRange,
	)
}

func TestWorkspaceParameterIDsDeriveFromOwningSymbolWithExactFallback(
	t *testing.T,
) {
	t.Parallel()

	ownerID := NewSymbolID(
		MethodSymbol,
		"App\\Service::run",
		"/service.php",
		10,
	)
	derivedID := NewSymbolID(
		ParameterSymbol,
		string(ownerID)+":$id",
		"/service.php",
		20,
	)
	document := &Document{
		Path: "/service.php",
		Symbols: []Symbol{{
			ID:             ownerID,
			Kind:           MethodSymbol,
			Name:           "run",
			FullyQualified: "App\\Service::run",
			Path:           "/service.php",
			Parameters: []Parameter{{
				ID:         derivedID,
				Name:       "$id",
				Type:       types.Int(),
				NativeType: types.Int(),
			}, {
				ID:         "legacy-parameter-id",
				Name:       "$legacy",
				Type:       types.String(),
				NativeType: types.String(),
			}},
		}},
	}

	graph := PackWorkspaceGraphOwned(document)
	parameters := graph.document.Symbols[0].signature().Parameters
	require.Len(t, parameters, 2)
	require.Nil(t, parameters[0].Extras)
	require.Equal(t, derivedID, parameters[0].id(&graph.document.Symbols[0]))
	require.Equal(t, SymbolID("legacy-parameter-id"), parameters[1].Extras.ID)
	requireMsgpackEquivalent(t, document, graph.Document())

	encoded, err := msgpack.Marshal(graph)
	require.NoError(t, err)
	var decoded WorkspaceGraph
	require.NoError(t, msgpack.Unmarshal(encoded, &decoded))
	decodedParameters := decoded.document.Symbols[0].signature().Parameters
	require.Nil(t, decodedParameters[0].Extras)
	require.Equal(
		t,
		SymbolID("legacy-parameter-id"),
		decodedParameters[1].Extras.ID,
	)
	requireMsgpackEquivalent(t, document, decoded.Document())

	restoreContext := NewWorkspaceGraphDecoder()
	restoreContext.stringCache = newWorkspaceStringInterner(16)
	var restoredFirst WorkspaceGraph
	require.NoError(
		t,
		restoreContext.Decode(
			msgpack.NewDecoder(bytes.NewReader(encoded)),
			&restoredFirst,
		),
	)
	_, derivedRetained := restoreContext.stringCache.LookupBytes(
		[]byte(derivedID),
	)
	require.False(t, derivedRetained)
	_, fallbackRetained := restoreContext.stringCache.LookupBytes(
		[]byte("legacy-parameter-id"),
	)
	require.True(t, fallbackRetained)

	var restoredSecond WorkspaceGraph
	require.NoError(
		t,
		restoreContext.Decode(
			msgpack.NewDecoder(bytes.NewReader(encoded)),
			&restoredSecond,
		),
	)
	restoreContext.Clear()
	requireMsgpackEquivalent(t, document, restoredFirst.Document())
	requireMsgpackEquivalent(t, document, restoredSecond.Document())
}

func TestWorkspaceParameterDerivedIDMatchesCanonicalSymbolID(t *testing.T) {
	t.Parallel()

	owner := &workspaceSymbol{ID: "5:App\\ÜberService::RUN"}
	for _, name := range []string{"$Service", "$größe"} {
		require.Equal(
			t,
			NewSymbolID(
				ParameterSymbol,
				string(owner.ID)+":"+name,
				owner.path(),
				0,
			),
			workspaceParameterIDForSymbol(owner, name),
		)
	}
}

var benchmarkWorkspaceParameterID SymbolID

func BenchmarkWorkspaceParameterIDAccess(b *testing.B) {
	owner := &workspaceSymbol{
		ID: NewSymbolID(
			MethodSymbol,
			"App\\Service::run",
			"/service.php",
			10,
		),
	}
	derived := workspaceParameter{Name: "$service"}
	fallback := workspaceParameter{
		Name: "$legacy",
		Extras: &workspaceParameterExtras{
			ID: "legacy-parameter-id",
		},
	}

	for name, parameter := range map[string]*workspaceParameter{
		"derived":  &derived,
		"fallback": &fallback,
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkWorkspaceParameterID = parameter.id(owner)
			}
		})
	}
}

func TestWorkspaceSignatureExtrasPackCollectionsLosslessly(t *testing.T) {
	t.Parallel()

	templates := []TemplateParameter{{Name: "T"}}
	throws := []types.Type{types.Named("RuntimeException")}
	literalReturns := []LiteralReturn{{
		Value: "active",
		Type:  types.String(),
	}}
	constantReturns := []ConstantReturn{{
		Receiver: "Status",
		Name:     "ACTIVE",
	}}
	extras := newWorkspaceSignatureExtras(
		templates,
		throws,
		literalReturns,
		constantReturns,
		nil,
	)

	require.Equal(t, templates, extras.templates())
	require.Equal(t, throws, extras.throws())
	require.Equal(t, literalReturns, extras.literalReturns())
	require.Equal(t, constantReturns, extras.constantReturns())
}

func TestWorkspaceHierarchyPacksCollectionsLosslessly(t *testing.T) {
	t.Parallel()

	extends := []string{"App\\Base"}
	implements := []string{"Stringable", "Countable"}
	traits := []string{"App\\Shared"}
	extendsTypes := []types.Type{types.Named("App\\Base")}
	implementsTypes := []types.Type{
		types.Named("Stringable"),
		types.Named("Countable"),
	}
	hierarchy := newWorkspaceHierarchy(
		extends,
		implements,
		traits,
		extendsTypes,
		implementsTypes,
		nil,
		nil,
	)

	require.Equal(t, extends, hierarchy.extends())
	require.Equal(t, implements, hierarchy.implements())
	require.Equal(t, traits, hierarchy.traits())
	require.Equal(t, extendsTypes, hierarchy.extendsTypes())
	require.Equal(t, implementsTypes, hierarchy.implementsTypes())
	require.Nil(t, hierarchy.traitTypes())
	require.NotNil(t, hierarchy.Types)

	namesOnly := newWorkspaceHierarchy(extends, nil, nil, nil, nil, nil, nil)
	require.Nil(t, namesOnly.Types)
	require.Equal(t, extends, namesOnly.extends())
}

func TestWorkspaceMetadataPacksCollectionsLosslessly(t *testing.T) {
	t.Parallel()

	attributes := []Attribute{{Name: "Route"}}
	constantArray := []ConstantArrayItem{{
		Key:   "name",
		Value: "storefront.home",
		Type:  types.String(),
	}}
	metadata := newWorkspaceMetadata(
		attributes,
		constantArray,
		"Storefront route.",
	)

	require.Equal(t, attributes, metadata.attributes())
	require.Equal(t, constantArray, metadata.constantArray())
	require.Equal(t, "Storefront route.", metadata.DocSummary)
	require.NotNil(t, metadata.Extras)

	summaryOnly := newWorkspaceMetadata(nil, nil, "A summary.")
	require.Nil(t, summaryOnly.Extras)
	require.Nil(t, summaryOnly.attributes())
	require.Nil(t, summaryOnly.constantArray())
}

func TestWorkspaceGraphRejectsInvalidReferenceSpan(t *testing.T) {
	t.Parallel()

	graph := PackWorkspaceGraphOwned(&Document{
		Path: "/invalid.php",
		References: []Reference{referenceWithTargets(Reference{
			Name: "Product",
			Kind: ClassName,
		}, []string{"App\\Product"}, nil)},
	})
	reference := &graph.document.References[0]
	*reference = newWorkspaceReference(
		reference.rangeValue(graph.document),
		reference.nameIndex(),
		reference.resolvedIndex(),
		reference.valueStart(graph.document),
		reference.receiverIndex(),
		2,
		reference.candidateCount(),
		reference.kind(),
		reference.targetKind(),
		reference.flags(),
		-1,
	)

	encoded, err := msgpack.Marshal(graph)
	require.NoError(t, err)
	var decoded WorkspaceGraph
	require.ErrorContains(
		t,
		msgpack.Unmarshal(encoded, &decoded),
		"invalid value span",
	)
}

func TestWorkspaceReferenceBoundsRetainedValueCounts(t *testing.T) {
	t.Parallel()

	qualified := make([]string, 256)
	for index := range qualified {
		qualified[index] = "App\\Product"
	}
	graph := PackWorkspaceGraphOwned(&Document{
		Path: "/bounded.php",
		References: []Reference{referenceWithTargets(Reference{
			Name: "Product",
			Kind: ClassName,
		}, qualified, nil)},
	})

	require.Equal(
		t,
		uint8(255),
		graph.document.References[0].qualifiedCount(),
	)
	require.Len(t, graph.document.reference(0).QualifiedNames(), 255)
}
