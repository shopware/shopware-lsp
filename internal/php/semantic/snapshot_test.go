package semantic

import (
	"bytes"
	"errors"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

func TestNormalizeSymbolName(t *testing.T) {
	t.Parallel()
	require.Equal(t, "run", normalizeSymbolName("$run", true))
	require.Equal(t, "app\\service", normalizeSymbolName("\\App\\Service", false))
}

func TestPublishedSnapshotUsesLazyLowercaseCache(t *testing.T) {
	t.Parallel()

	snapshot := NewSnapshot(1, []*Document{{
		Path: "/service.php",
		Symbols: []Symbol{{
			ID:             "service",
			Kind:           ClassSymbol,
			Name:           "Service",
			FullyQualified: "App\\Service",
			Path:           "/service.php",
		}},
	}})

	require.Len(t, snapshot.ClassViews("\\APP\\SERVICE"), 1)
	normalized, found := snapshot.dynamicNames.Load("APP\\SERVICE")
	require.True(t, found)
	require.Equal(t, "app\\service", normalized)
}

func TestPublishedSnapshotOwnsLazyLowercaseCacheKeys(t *testing.T) {
	t.Parallel()

	const name = "App\\Service"
	backing := strings.Repeat("x", 1<<20) + name + strings.Repeat("y", 1<<20)
	query := backing[1<<20 : 1<<20+len(name)]
	snapshot := NewSnapshot(1, nil)

	require.Equal(t, "app\\service", snapshot.lowerName(query, false))
	var cachedKey, cachedValue string
	snapshot.dynamicNames.Range(func(key, value any) bool {
		cachedKey = key.(string)
		cachedValue = value.(string)
		return false
	})
	require.Equal(t, name, cachedKey)
	require.Equal(t, "app\\service", cachedValue)

	backingStart := uintptr(unsafe.Pointer(unsafe.StringData(backing)))
	backingEnd := backingStart + uintptr(len(backing))
	keyStart := uintptr(unsafe.Pointer(unsafe.StringData(cachedKey)))
	valueStart := uintptr(unsafe.Pointer(unsafe.StringData(cachedValue)))
	require.False(t, keyStart >= backingStart && keyStart < backingEnd)
	require.False(t, valueStart >= backingStart && valueStart < backingEnd)
}

func TestPublishedSnapshotNormalizesNamesConcurrently(t *testing.T) {
	t.Parallel()

	snapshot := NewSnapshot(1, nil)
	const workers = 16
	const iterations = 100
	results := make(chan string, workers*iterations)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for range iterations {
				results <- snapshot.lowerName("App\\Service", false)
			}
		}()
	}
	group.Wait()
	close(results)
	for result := range results {
		require.Equal(t, "app\\service", result)
	}
}

var benchmarkNormalizedName string
var benchmarkMemberID SymbolID
var benchmarkSymbolRange cst.TextRange

func BenchmarkSnapshotNormalizedNameLookup(b *testing.B) {
	const name = "App\\Service"
	b.Run("lazy_runtime_cache", func(b *testing.B) {
		snapshot := NewSnapshot(1, nil)
		require.Equal(b, "app\\service", snapshot.lowerName(name, false))
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			benchmarkNormalizedName = snapshot.lowerName(name, false)
		}
	})
}

func BenchmarkSnapshotMemberIndex(b *testing.B) {
	benchmarkSnapshotMemberIndex(b, 64)
}

func BenchmarkSnapshotSmallMemberIndex(b *testing.B) {
	benchmarkSnapshotMemberIndex(b, 8)
}

func BenchmarkStoreSingleDocumentReplacement(b *testing.B) {
	const documentCount = 10_000
	documents := make([]*Document, documentCount)
	for index := range documents {
		documents[index] = &Document{
			Path: "/project/" + strconv.Itoa(index) + ".php",
		}
	}
	store := NewStore()
	store.ReplaceMany(documents...)
	replacement := &Document{Path: documents[documentCount/2].Path}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		store.Replace(replacement)
	}
}

func BenchmarkWorkspaceSymbolRangeAccess(b *testing.B) {
	snapshot := NewSnapshot(1, []*Document{{
		Path: "/service.php",
		Symbols: []Symbol{{
			ID:             "service",
			Kind:           ClassSymbol,
			Name:           "Service",
			FullyQualified: "App\\Service",
			Path:           "/service.php",
			Range:          cst.TextRange{Start: 100, End: 200},
			SelectionRange: cst.TextRange{Start: 110, End: 117},
			BodyRange:      cst.TextRange{Start: 120, End: 190},
		}},
	}})
	view := snapshot.ClassViews("App\\Service")[0]

	b.ReportAllocs()
	for range b.N {
		benchmarkSymbolRange = view.Range()
	}
}

func benchmarkSnapshotMemberIndex(b *testing.B, memberCount int) {
	symbols := make([]Symbol, 1, memberCount+1)
	symbols[0] = Symbol{
		ID:             "class",
		Kind:           ClassSymbol,
		Name:           "Service",
		FullyQualified: "App\\Service",
		Path:           "/service.php",
	}
	for index := range memberCount {
		name := "member" + strconv.Itoa(index)
		symbols = append(symbols, Symbol{
			ID:        SymbolID(name),
			Kind:      MethodSymbol,
			Name:      name,
			Container: "class",
			Path:      "/service.php",
		})
	}
	snapshot := NewSnapshot(1, []*Document{{
		Path:    "/service.php",
		Symbols: symbols,
	}})
	visit := func(view SymbolView) bool {
		benchmarkMemberID = view.ID()
		return true
	}

	for _, name := range []string{
		"member0",
		"member" + strconv.Itoa(memberCount/2),
		"member" + strconv.Itoa(memberCount-1),
		"missing",
	} {
		b.Run("lookup_"+name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				snapshot.VisitMemberViews("class", name, visit)
			}
		})
	}
	b.Run("all_members", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			views := snapshot.MemberViewsOf("class")
			if len(views) != 0 {
				benchmarkMemberID = views[0].ID()
			}
		}
	})
}

func TestDocumentOverlayUsesCompactInlinePathIndexes(t *testing.T) {
	t.Parallel()

	path := "/service.php"
	original := Symbol{
		ID:             "old",
		Kind:           ClassSymbol,
		Name:           "OldService",
		FullyQualified: "App\\OldService",
		Path:           path,
	}
	replacement := Symbol{
		ID:             "new",
		Kind:           ClassSymbol,
		Name:           "NewService",
		FullyQualified: "App\\NewService",
		Path:           path,
	}
	method := Symbol{
		ID:             "new::run",
		Kind:           MethodSymbol,
		Name:           "run",
		FullyQualified: "App\\NewService::run",
		Container:      replacement.ID,
		Path:           path,
	}
	otherPath := "/other.php"
	other := Symbol{
		ID:             "other",
		Kind:           ClassSymbol,
		Name:           "Other",
		FullyQualified: "App\\Other",
		Path:           otherPath,
	}

	overlay := NewSnapshot(1, []*Document{{
		Path:    path,
		Symbols: []Symbol{original},
	}, {
		Path:    otherPath,
		Symbols: []Symbol{other},
	}}).WithDeclarations(&Document{
		Path:    path,
		Symbols: []Symbol{replacement, method},
	})

	require.Equal(t, path, overlay.overlayPath)
	require.Equal(
		t,
		[]SymbolID{replacement.ID, method.ID},
		symbolIDs(overlay.expandedData),
	)
	require.Empty(t, overlay.symbols.slots)
	require.Zero(t, overlay.symbols.Len())
	require.Nil(t, overlay.pathRefs)
	require.Nil(t, overlay.reverseReferences)
	require.Nil(t, overlay.overlayReferences)
	require.True(t, overlay.HasDocument(path))
	require.Equal(
		t,
		[]SymbolID{replacement.ID, method.ID},
		symbolIDs(overlay.SymbolsIn(path)),
	)
	_, found := overlay.Symbol(original.ID)
	require.False(t, found)
	require.Equal(
		t,
		[]SymbolID{other.ID},
		symbolIDs(overlay.SymbolsIn(otherPath)),
	)
}

func TestPublishedSnapshotUsesDocumentSymbolsForPathLookup(t *testing.T) {
	t.Parallel()

	path := "/service.php"
	snapshot := NewSnapshot(1, []*Document{{
		Path: path,
		Symbols: []Symbol{
			{ID: "class", Kind: ClassSymbol, Path: path},
			{ID: "method", Kind: MethodSymbol, Container: "class", Path: path},
			{ID: "property", Kind: PropertySymbol, Container: "class", Path: path},
		},
	}})

	require.Len(t, snapshot.pathRefs[path].Symbols, 3)
	require.Equal(t, 3, cap(snapshot.pathRefs[path].Symbols))
	require.Equal(
		t,
		[]SymbolID{"class", "method", "property"},
		symbolIDs(snapshot.SymbolsIn(path)),
	)
}

func TestSnapshotMemberViewsReserveIndexedMembers(t *testing.T) {
	t.Parallel()

	snapshot := NewSnapshot(1, []*Document{{
		Path: "/members.php",
		Symbols: []Symbol{
			{ID: "class", Kind: ClassSymbol, Path: "/members.php"},
			{
				ID:        "method-a",
				Kind:      MethodSymbol,
				Name:      "run",
				Container: "class",
				Path:      "/members.php",
			},
			{
				ID:        "method-b",
				Kind:      MethodSymbol,
				Name:      "run",
				Container: "class",
				Path:      "/members.php",
			},
			{
				ID:        "property",
				Kind:      PropertySymbol,
				Name:      "value",
				Container: "class",
				Path:      "/members.php",
			},
		},
	}})

	views := snapshot.MemberViewsOf("class")
	require.Len(t, views, 3)
	require.Equal(t, len(views), cap(views))
}

func symbolIDs(symbols []Symbol) []SymbolID {
	result := make([]SymbolID, len(symbols))
	for index := range symbols {
		result[index] = symbols[index].ID
	}
	return result
}

func TestStorePublishesImmutableSnapshots(t *testing.T) {
	t.Parallel()
	store := NewStore()
	base := Symbol{
		ID:             NewSymbolID(ClassSymbol, "App\\Base", "/base.php", 0),
		Kind:           ClassSymbol,
		Name:           "Base",
		FullyQualified: "App\\Base",
		Path:           "/base.php",
	}
	child := Symbol{
		ID:             NewSymbolID(ClassSymbol, "App\\Child", "/child.php", 0),
		Kind:           ClassSymbol,
		Name:           "Child",
		FullyQualified: "App\\Child",
		Path:           "/child.php",
		Extends:        []string{"App\\Base"},
	}
	first := store.Replace(&Document{Path: base.Path, Symbols: []Symbol{base}})
	second := store.Replace(&Document{Path: child.Path, Symbols: []Symbol{child}})

	require.False(t, first.IsSubtypeOf("App\\Child", "App\\Base"))
	require.True(t, second.IsSubtypeOf("App\\Child", "App\\Base"))
	require.Equal(t, uint64(2), second.Revision)
}

func TestStorePathIndexUsesCopyOnWritePublication(t *testing.T) {
	t.Parallel()

	store := NewStore()
	document := func(path, id string) *Document {
		return &Document{
			Path: path,
			Symbols: []Symbol{{
				ID:             SymbolID(id),
				Kind:           ClassSymbol,
				Name:           id,
				FullyQualified: "App\\" + id,
				Path:           path,
			}},
		}
	}
	first := store.ReplaceMany(
		document("/first.php", "First"),
		document("/second.php", "Second"),
	)
	second := store.Replace(document("/third.php", "Third"))
	third := store.Remove("/first.php")

	require.True(t, first.HasDocument("/first.php"))
	require.True(t, first.HasDocument("/second.php"))
	require.False(t, first.HasDocument("/third.php"))
	require.True(t, second.HasDocument("/first.php"))
	require.True(t, second.HasDocument("/second.php"))
	require.True(t, second.HasDocument("/third.php"))
	require.False(t, third.HasDocument("/first.php"))
	require.True(t, third.HasDocument("/second.php"))
	require.True(t, third.HasDocument("/third.php"))
	require.Len(t, first.Classes("App\\First"), 1)
	require.Len(t, second.Classes("App\\First"), 1)
	require.Empty(t, third.Classes("App\\First"))
}

func TestStoreDefensivelyCopiesPublicReplacements(t *testing.T) {
	t.Parallel()
	store := NewStore()
	document := &Document{
		Path: "/service.php",
		Symbols: []Symbol{{
			ID:             "service",
			Kind:           ClassSymbol,
			Name:           "Service",
			FullyQualified: "App\\Service",
			Path:           "/service.php",
		}},
	}
	snapshot := store.Replace(document)
	document.Symbols[0].Name = "Mutated"
	document.Symbols[0].FullyQualified = "App\\Mutated"

	require.Len(t, snapshot.Classes("App\\Service"), 1)
	require.Empty(t, snapshot.Classes("App\\Mutated"))
}

func TestStoreDocumentsDoNotExposeRetainedNestedSymbolData(t *testing.T) {
	t.Parallel()
	store := NewStore()
	store.Replace(&Document{
		Path: "/service.php",
		Symbols: []Symbol{{
			ID:             "service",
			Kind:           ClassSymbol,
			Name:           "Service",
			FullyQualified: "App\\Service",
			Path:           "/service.php",
			Extends:        []string{"App\\Base"},
			Parameters: []Parameter{{
				Name:          "$value",
				AssistantTags: []string{"Autowire"},
			}},
		}},
	})

	first, ok := store.Document("/service.php")
	require.True(t, ok)
	first.Symbols[0].Extends[0] = "App\\Mutated"
	first.Symbols[0].Parameters[0].AssistantTags[0] = "Mutated"

	second, ok := store.Document("/service.php")
	require.True(t, ok)
	require.Equal(t, []string{"App\\Base"}, second.Symbols[0].Extends)
	require.Equal(
		t,
		[]string{"Autowire"},
		second.Symbols[0].Parameters[0].AssistantTags,
	)
}

func TestStorePublishesManyDocumentsAsOneGeneration(t *testing.T) {
	t.Parallel()
	store := NewStore()
	first := &Document{
		Path: "/first.php",
		Symbols: []Symbol{{
			ID:             "first",
			Kind:           ClassSymbol,
			Name:           "First",
			FullyQualified: "App\\First",
			Path:           "/first.php",
		}},
	}
	second := &Document{
		Path: "/second.php",
		Symbols: []Symbol{{
			ID:             "second",
			Kind:           ClassSymbol,
			Name:           "Second",
			FullyQualified: "App\\Second",
			Path:           "/second.php",
		}},
	}

	snapshot := store.ReplaceMany(first, second)
	require.Equal(t, uint64(1), snapshot.Revision)
	require.Len(t, snapshot.Classes("App\\First"), 1)
	require.Len(t, snapshot.Classes("App\\Second"), 1)
}

func TestStoreConsumesCanonicalGraphsWithoutMutatingOlderSnapshots(
	t *testing.T,
) {
	t.Parallel()

	store := NewStore()
	project := func(name string) *WorkspaceGraph {
		path := "/service.php"
		graph := ProjectWorkspaceGraphBorrowed(&Document{
			Path: path,
			Symbols: []Symbol{{
				ID:             SymbolID(name),
				Kind:           ClassSymbol,
				Name:           name,
				FullyQualified: "App\\" + name,
				Path:           path,
			}},
		})
		NewWorkspaceGraphDetacher().DetachOwned(graph)
		return graph
	}

	firstGraph := project("First")
	first := store.ReplaceCanonicalWorkspaceGraphsOwned(firstGraph)
	require.Nil(t, firstGraph.document)
	secondGraph := project("Second")
	second := store.ReplaceCanonicalWorkspaceGraphsOwned(secondGraph)
	require.Nil(t, secondGraph.document)

	require.Len(t, first.Classes("App\\First"), 1)
	require.Empty(t, first.Classes("App\\Second"))
	require.Empty(t, second.Classes("App\\First"))
	require.Len(t, second.Classes("App\\Second"), 1)
	require.Equal(t, uint64(2), second.Revision)
}

func TestStoreReleasesPublicationInterningTables(t *testing.T) {
	t.Parallel()
	store := NewStore()
	first := &Document{
		Path: "/first.php",
		Symbols: []Symbol{{
			ID:             "first",
			Kind:           ClassSymbol,
			Name:           "First",
			FullyQualified: "App\\First",
			Path:           "/first.php",
			Type:           types.Named("App\\Shared"),
		}},
	}
	second := &Document{
		Path: "/second.php",
		Symbols: []Symbol{{
			ID:             "second",
			Kind:           ClassSymbol,
			Name:           "Second",
			FullyQualified: "App\\Second",
			Path:           "/second.php",
			Type:           types.Named("App\\Shared"),
		}},
	}

	store.ReplaceMany(first, second)

	require.Nil(t, store.strings)
	require.Nil(t, store.types)
	require.Len(t, store.Snapshot().Classes("App\\First"), 1)
	require.Len(t, store.Snapshot().Classes("App\\Second"), 1)

	store.Replace(&Document{
		Path: "/third.php",
		Symbols: []Symbol{{
			ID:             "third",
			Kind:           ClassSymbol,
			Name:           "Third",
			FullyQualified: "App\\Third",
			Path:           "/third.php",
		}},
	})
	require.Nil(t, store.strings)
	require.Nil(t, store.types)
	require.Len(t, store.Snapshot().Classes("App\\First"), 1)
	require.Len(t, store.Snapshot().Classes("App\\Third"), 1)
}

func TestStoreRestoresOwnedDocumentsAsOneGeneration(t *testing.T) {
	t.Parallel()
	store := NewStore()

	snapshot, err := store.RestoreOwned(func(accept func(*Document)) error {
		accept(&Document{
			Path: "/first.php",
			Symbols: []Symbol{{
				ID:             "first",
				Kind:           ClassSymbol,
				Name:           "First",
				FullyQualified: "App\\First",
				Path:           "/first.php",
			}},
		})
		accept(&Document{
			Path: "/second.php",
			Symbols: []Symbol{{
				ID:             "second",
				Kind:           ClassSymbol,
				Name:           "Second",
				FullyQualified: "App\\Second",
				Path:           "/second.php",
			}},
		})
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, uint64(1), snapshot.Revision)
	require.Len(t, snapshot.Classes("App\\First"), 1)
	require.Len(t, snapshot.Classes("App\\Second"), 1)
}

func TestStoreRestoresWorkspaceGraphsWithCapacity(t *testing.T) {
	t.Parallel()
	store := NewStore()

	snapshot, err := store.RestoreWorkspaceGraphsOwnedWithCapacity(
		WorkspaceRestoreCapacity{
			Documents: 1,
			Strings:   8,
			Types:     1,
		},
		func(accept func(*WorkspaceGraph)) error {
			accept(PackWorkspaceGraphOwned(&Document{
				Path: "/service.php",
				Symbols: []Symbol{{
					ID:             "service",
					Kind:           ClassSymbol,
					Name:           "Service",
					FullyQualified: "App\\Service",
					Path:           "/service.php",
					Type:           types.Named("App\\Service"),
				}},
			}))
			return nil
		},
	)

	require.NoError(t, err)
	require.Equal(t, uint64(1), snapshot.Revision)
	require.Len(t, snapshot.Classes("App\\Service"), 1)
	require.Nil(t, store.strings)
	require.Nil(t, store.types)
}

func TestStoreDecodesWorkspaceGraphsIntoRestoreInterners(t *testing.T) {
	t.Parallel()
	store := NewStore()
	encoded, err := msgpack.Marshal(PackWorkspaceGraphOwned(&Document{
		Path: "/service.php",
		Symbols: []Symbol{{
			ID:             "service",
			Kind:           ClassSymbol,
			Name:           "Service",
			FullyQualified: "App\\Service",
			Path:           "/service.php",
			Type:           types.Named("App\\Service"),
		}},
	}))
	require.NoError(t, err)

	var restoreDecoder *WorkspaceGraphDecoder
	snapshot, err := store.RestoreWorkspaceGraphsDecodedWithCapacity(
		WorkspaceRestoreCapacity{
			Documents: 1,
			Strings:   8,
			Types:     1,
		},
		func(
			decoder *WorkspaceGraphDecoder,
			accept func(*WorkspaceGraph),
		) error {
			restoreDecoder = decoder
			require.NotNil(t, decoder.stringCache)
			require.NotNil(t, decoder.typeCache)
			var graph WorkspaceGraph
			if err := decoder.Decode(
				msgpack.NewDecoder(bytes.NewReader(encoded)),
				&graph,
			); err != nil {
				return err
			}
			accept(&graph)
			return nil
		},
	)

	require.NoError(t, err)
	require.Equal(t, uint64(1), snapshot.Revision)
	require.Len(t, snapshot.Classes("App\\Service"), 1)
	require.NotNil(t, restoreDecoder)
	require.Nil(t, restoreDecoder.stringBuffer)
	require.Nil(t, restoreDecoder.stringCache)
	require.Nil(t, restoreDecoder.typeCache)
	require.Nil(t, store.strings)
	require.Nil(t, store.types)
}

func TestStoreRestoreFailureKeepsPublishedGeneration(t *testing.T) {
	t.Parallel()
	store := NewStore()
	before := store.Replace(&Document{
		Path: "/existing.php",
		Symbols: []Symbol{{
			ID:             "existing",
			Kind:           ClassSymbol,
			Name:           "Existing",
			FullyQualified: "App\\Existing",
			Path:           "/existing.php",
		}},
	})
	expected := errors.New("decode failed")

	after, err := store.RestoreOwned(func(accept func(*Document)) error {
		accept(&Document{
			Path: "/partial.php",
			Symbols: []Symbol{{
				ID:             "partial",
				Kind:           ClassSymbol,
				Name:           "Partial",
				FullyQualified: "App\\Partial",
				Path:           "/partial.php",
			}},
		})
		return expected
	})

	require.ErrorIs(t, err, expected)
	require.Same(t, before, after)
	require.Len(t, after.Classes("App\\Existing"), 1)
	require.Empty(t, after.Classes("App\\Partial"))
}

func TestDocumentTypeFactsAndScopes(t *testing.T) {
	t.Parallel()
	root := cst.NewBuilder("x")
	root.StartNode(999)
	root.Token(998, cst.TextRange{Start: 0, End: 1})
	root.FinishNode()
	tree := root.Finish()
	id := NodeIdentity(tree.Root)
	document := &Document{
		Path: "/file.php",
		Scopes: []Scope{
			{ID: 0, Kind: FileScope, Range: cst.TextRange{Start: 0, End: 20}},
			{ID: 1, Kind: FunctionScope, Parent: 0, Range: cst.TextRange{Start: 5, End: 15}},
		},
		TypeFacts: map[NodeID]TypeFact{
			id: {Type: types.String(), Confidence: DeclaredConfidence, Source: NativeSource},
		},
	}
	require.Equal(t, types.String(), document.TypeOf(tree.Root).Type)
	scope, ok := document.ScopeAt(10)
	require.True(t, ok)
	require.Equal(t, ScopeID(1), scope.ID)
}

func TestScopeSymbolIDsPreserveDuplicatesAndInsertionOrder(t *testing.T) {
	t.Parallel()

	var scope Scope
	scope.AddSymbol("$value", "first")
	scope.AddSymbol("$value", "second")
	scope.AddSymbol("$other", "other")

	require.True(t, scope.HasSymbol("$value"))
	require.False(t, scope.HasSymbol("$missing"))
	require.Equal(
		t,
		[]SymbolID{"prefix", "first", "second"},
		scope.AppendSymbolIDs([]SymbolID{"prefix"}, "$value"),
	)
	var values []SymbolID
	for id := range scope.SymbolIDs("$value") {
		values = append(values, id)
	}
	require.Equal(t, []SymbolID{"first", "second"}, values)

	var all []SymbolID
	for id := range scope.AllSymbolIDs() {
		all = append(all, id)
	}
	require.Equal(
		t,
		[]SymbolID{"first", "second", "other"},
		all,
	)
}

func TestDocumentCloneDetachesScopeSymbols(t *testing.T) {
	t.Parallel()

	var scope Scope
	scope.AddSymbol("$value", "first")
	scope.AddSymbol("$value", "second")
	document := &Document{Scopes: []Scope{scope}}

	cloned := document.Clone()
	cloned.Scopes[0].AddSymbol("$value", "third")

	require.Equal(
		t,
		[]SymbolID{"first", "second"},
		document.Scopes[0].AppendSymbolIDs(nil, "$value"),
	)
	require.Equal(
		t,
		[]SymbolID{"first", "second", "third"},
		cloned.Scopes[0].AppendSymbolIDs(nil, "$value"),
	)
}

func TestDocumentCompactsTypeFactsWithoutReasons(t *testing.T) {
	t.Parallel()
	builder := cst.NewBuilder("value")
	builder.StartNode(999)
	builder.Token(998, cst.TextRange{Start: 0, End: 5})
	builder.FinishNode()
	node := builder.Finish().Root
	identity := NodeIdentity(node)
	document := &Document{}
	document.SetTypeFact(identity, TypeFact{
		Type:       types.String(),
		Confidence: InferredConfidence,
		Source:     AssignmentSource,
		Origin:     node.Range(),
	})

	require.Empty(t, document.TypeFacts)
	require.Len(t, document.compactTypeFacts.inferred, 1)
	require.Empty(t, document.compactTypeFacts.packed)
	require.Equal(t, 1, document.TypeFactCount())
	fact := document.TypeOf(node)
	require.Equal(t, types.String(), fact.Type)
	require.Equal(t, InferredConfidence, fact.Confidence)
	require.Equal(t, AssignmentSource, fact.Source)
	require.Equal(t, node.Range(), fact.Origin)

	cloned := document.Clone()
	document.DeleteTypeFact(identity)
	require.Zero(t, document.TypeFactCount())
	require.Equal(t, 1, cloned.TypeFactCount())
	require.Equal(t, types.String(), cloned.TypeOf(node).Type)

	cloned.SetTypeFact(identity, TypeFact{
		Type:       types.Int(),
		Confidence: InferredConfidence,
		Source:     AssignmentSource,
		Reason:     "assignment",
	})
	require.Empty(t, cloned.TypeFacts)
	require.Empty(t, cloned.compactTypeFacts.inferred)
	require.Len(t, cloned.compactTypeFacts.packed, 1)
	require.Equal(t, "assignment", cloned.TypeOf(node).Reason)
}

func TestCompactTypeFactsMoveBetweenImplicitAndAnnotatedTables(t *testing.T) {
	t.Parallel()

	identity := NodeID{Kind: 42, Start: 17, End: 117}
	document := &Document{}
	document.ReserveTypeFacts(4)

	document.SetTypeFact(identity, TypeFact{
		Type:       types.String(),
		Confidence: InferredConfidence,
		Source:     AssignmentSource,
	})
	require.Len(t, document.compactTypeFacts.inferred, 1)

	document.SetTypeFact(identity, TypeFact{
		Type:       types.LiteralString("value"),
		Confidence: InferredConfidence,
		Source:     LiteralSource,
	})
	require.Empty(t, document.compactTypeFacts.inferred)
	require.Len(t, document.compactTypeFacts.packed, 1)
	require.Equal(t, 1, document.TypeFactCount())
	fact, found := document.TypeFact(identity)
	require.True(t, found)
	require.Equal(t, LiteralSource, fact.Source)

	document.SetTypeFact(identity, TypeFact{
		Type:       types.String(),
		Confidence: InferredConfidence,
		Source:     FlowSource,
		Reason:     "instanceof",
	})
	require.Len(t, document.compactTypeFacts.packed, 1)
	require.Equal(t, 1, document.TypeFactCount())
	fact, found = document.TypeFact(identity)
	require.True(t, found)
	require.Equal(t, FlowSource, fact.Source)
	require.Equal(t, "instanceof", fact.Reason)

	document.SetTypeFact(identity, TypeFact{
		Type:       types.Bool(),
		Confidence: InferredConfidence,
		Source:     AssignmentSource,
	})
	require.Empty(t, document.compactTypeFacts.packed)
	require.Len(t, document.compactTypeFacts.inferred, 1)
	require.Equal(t, 1, document.TypeFactCount())
	fact, found = document.TypeFact(identity)
	require.True(t, found)
	require.Equal(t, types.Bool(), fact.Type)
	require.Equal(t, AssignmentSource, fact.Source)

	cloned := document.Clone()
	document.DeleteTypeFact(identity)
	require.Zero(t, document.TypeFactCount())
	require.Equal(t, 1, cloned.TypeFactCount())
}

func TestCompactTypeFactReasonsRoundTrip(t *testing.T) {
	t.Parallel()

	reasons := []string{
		"",
		"assignment",
		"foreach value",
		"instanceof",
		"null comparison",
		"flow expression",
		"conditional predicate",
		"by-reference argument",
		"logical condition",
		"truthiness",
		"is_string",
		"is_int",
		"is_integer",
		"is_long",
		"is_float",
		"is_double",
		"is_real",
		"is_bool",
		"is_array",
		"is_callable",
		"is_iterable",
		"is_object",
		"is_null",
	}
	for _, reason := range reasons {
		encoded, ok := compactTypeFactReasonFor(reason)
		require.True(t, ok, reason)
		require.Equal(t, reason, encoded.String())
	}
	_, ok := compactTypeFactReasonFor("extension-specific reason")
	require.False(t, ok)
}

func TestCompactTypeFactsPreservePackedAndOverflowIdentities(t *testing.T) {
	t.Parallel()

	packed := NodeID{Kind: 42, Start: 17, End: 117}
	sameRangeDifferentKind := NodeID{Kind: 43, Start: 17, End: 117}
	overflow := NodeID{Kind: 42, Start: 17, End: 70_000}
	inverted := NodeID{Kind: 42, Start: 20, End: 19}
	document := &Document{}
	document.ReserveTypeFacts(4)
	for index, identity := range []NodeID{
		packed,
		sameRangeDifferentKind,
		overflow,
		inverted,
	} {
		document.SetTypeFact(identity, TypeFact{
			Type:       types.LiteralInt(strconv.Itoa(index)),
			Confidence: InferredConfidence,
			Source:     FlowSource,
		})
	}

	require.Equal(t, 4, document.TypeFactCount())
	for index, identity := range []NodeID{
		packed,
		sameRangeDifferentKind,
		overflow,
		inverted,
	} {
		fact, found := document.TypeFact(identity)
		require.True(t, found)
		require.Equal(t, strconv.Itoa(index), fact.Type.String())
	}

	cloned := document.Clone()
	document.DeleteTypeFact(packed)
	document.DeleteTypeFact(overflow)
	require.Equal(t, 2, document.TypeFactCount())
	require.Equal(t, 4, cloned.TypeFactCount())
}

func TestSemanticTypesRoundTripThroughMsgpack(t *testing.T) {
	t.Parallel()
	original := Symbol{
		ID:         "method",
		Kind:       MethodSymbol,
		ReturnType: types.MustParse("array<string,Product|null>"),
		Parameters: []Parameter{{
			Name:          "$value",
			Type:          types.MustParse("list<Product>"),
			AssistantTags: []string{"Route", "Service"},
		}},
	}
	encoded, err := msgpack.Marshal(original)
	require.NoError(t, err)
	var decoded Symbol
	require.NoError(t, msgpack.Unmarshal(encoded, &decoded))
	require.True(t, original.ReturnType.Equal(decoded.ReturnType))
	require.True(
		t,
		original.Parameters[0].Type.Equal(decoded.Parameters[0].Type),
	)
	require.Equal(
		t,
		original.Parameters[0].AssistantTags,
		decoded.Parameters[0].AssistantTags,
	)
}

func TestSnapshotDocumentOverlayReplacesPersistedSymbols(t *testing.T) {
	t.Parallel()
	persisted := &Document{
		Path: "/file.php",
		Symbols: []Symbol{{
			ID:             "old",
			Kind:           ClassSymbol,
			Name:           "Old",
			FullyQualified: "App\\Old",
			Path:           "/file.php",
		}},
	}
	overlay := &Document{
		Path: "/file.php",
		Symbols: []Symbol{{
			ID:             "new",
			Kind:           ClassSymbol,
			Name:           "New",
			FullyQualified: "App\\New",
			Path:           "/file.php",
		}},
	}
	snapshot := NewSnapshot(1, []*Document{persisted}).WithDocument(overlay)
	require.Empty(t, snapshot.Classes("App\\Old"))
	require.Len(t, snapshot.Classes("App\\New"), 1)
}

func TestSnapshotGlobalSymbolsExcludeMembersAndRespectOverlays(t *testing.T) {
	t.Parallel()
	oldClass := Symbol{
		ID:             "old-class",
		Kind:           ClassSymbol,
		Name:           "Old",
		FullyQualified: "App\\Old",
		Path:           "/class.php",
	}
	oldMethod := Symbol{
		ID:        "old-method",
		Kind:      MethodSymbol,
		Name:      "run",
		Container: oldClass.ID,
		Path:      oldClass.Path,
	}
	function := Symbol{
		ID:             "function",
		Kind:           FunctionSymbol,
		Name:           "helper",
		FullyQualified: "App\\helper",
		Path:           "/function.php",
	}
	newClass := Symbol{
		ID:             "new-class",
		Kind:           ClassSymbol,
		Name:           "New",
		FullyQualified: "App\\New",
		Path:           oldClass.Path,
	}
	newProperty := Symbol{
		ID:        "new-property",
		Kind:      PropertySymbol,
		Name:      "$value",
		Container: newClass.ID,
		Path:      newClass.Path,
	}

	snapshot := NewSnapshot(1, []*Document{
		{
			Path:    oldClass.Path,
			Symbols: []Symbol{oldClass, oldMethod},
		},
		{Path: function.Path, Symbols: []Symbol{function, function}},
	}).WithDocument(&Document{
		Path:    newClass.Path,
		Symbols: []Symbol{newClass, newProperty},
	})

	globals := snapshot.GlobalSymbols()
	ids := make([]SymbolID, 0, len(globals))
	for _, symbol := range globals {
		ids = append(ids, symbol.ID)
	}
	require.ElementsMatch(t, []SymbolID{newClass.ID, function.ID}, ids)
	classViews := snapshot.GlobalClassViews()
	require.Len(t, classViews, 1)
	require.Equal(t, newClass.ID, classViews[0].ID())
}

func TestSnapshotDocumentOverlayReplacesReverseReferences(t *testing.T) {
	t.Parallel()
	target := Symbol{
		ID:             NewSymbolID(ClassSymbol, "App\\Target", "/target.php", 0),
		Kind:           ClassSymbol,
		Name:           "Target",
		FullyQualified: "App\\Target",
		Path:           "/target.php",
	}
	persisted := &Document{
		Path: "/consumer.php",
		References: []Reference{{
			Resolved: target.ID,
			Range:    cst.TextRange{Start: 2, End: 8},
		}},
	}
	snapshot := NewSnapshot(
		1,
		[]*Document{{Path: target.Path, Symbols: []Symbol{target}}, persisted},
	)
	require.Len(t, snapshot.ReferencesTo(target.ID), 1)

	overlay := snapshot.WithDocument(&Document{
		Path: "/consumer.php",
		References: []Reference{{
			Resolved: target.ID,
			Range:    cst.TextRange{Start: 20, End: 26},
		}},
	})
	references := overlay.ReferencesTo(target.ID)
	require.Len(t, references, 1)
	require.Equal(t, uint32(20), references[0].RangeStart)
}

func TestSnapshotDeclarationOverlayDoesNotPackReferences(t *testing.T) {
	t.Parallel()
	target := Symbol{
		ID:             "target",
		Kind:           ClassSymbol,
		Name:           "Target",
		FullyQualified: "App\\Target",
		Path:           "/target.php",
	}
	overlay := NewSnapshot(1, nil).WithDeclarations(&Document{
		Path:    target.Path,
		Symbols: []Symbol{target},
		References: []Reference{{
			Resolved: target.ID,
			Range:    cst.TextRange{Start: 2, End: 8},
		}},
	})

	require.Len(t, overlay.Classes(target.FullyQualified), 1)
	require.Empty(t, overlay.ReferencesTo(target.ID))
	require.True(t, overlay.HasDocument(target.Path))
}

func TestSnapshotDoesNotExposeLocalsAsClassMembers(t *testing.T) {
	t.Parallel()
	class := Symbol{
		ID:             "class",
		Kind:           ClassSymbol,
		Name:           "C",
		FullyQualified: "C",
		Path:           "/c.php",
	}
	method := Symbol{
		ID:        "method",
		Kind:      MethodSymbol,
		Name:      "run",
		Container: class.ID,
		Path:      class.Path,
	}
	parameter := Symbol{
		ID:        "parameter",
		Kind:      ParameterSymbol,
		Name:      "$input",
		Container: method.ID,
		Path:      class.Path,
	}
	snapshot := NewSnapshot(1, []*Document{{
		Path:    class.Path,
		Symbols: []Symbol{class, method, parameter},
	}})
	require.Len(t, snapshot.Members(class.ID, "run"), 1)
	require.Empty(t, snapshot.Members(method.ID, "$input"))
}

func TestSnapshotDeduplicatesRepeatedIndexEntries(t *testing.T) {
	t.Parallel()

	class := Symbol{
		ID:             "class",
		Kind:           ClassSymbol,
		Name:           "Service",
		FullyQualified: "App\\Service",
		Path:           "/service.php",
	}
	method := Symbol{
		ID:        "method",
		Kind:      MethodSymbol,
		Name:      "run",
		Container: class.ID,
		Path:      class.Path,
	}
	alternateMethod := method
	alternateMethod.ID = "alternate-method"
	document := &Document{
		Path:    class.Path,
		Symbols: []Symbol{class, method, alternateMethod},
	}
	snapshot := NewSnapshot(1, []*Document{document, document})

	require.Len(t, snapshot.ClassViews(class.FullyQualified), 1)
	require.Len(t, snapshot.MemberViews(class.ID, method.Name), 2)
	indexedMembers := snapshot.compactMembers.valuesFor(
		class.ID,
		"run",
	)
	require.Equal(
		t,
		[]SymbolID{method.ID, alternateMethod.ID},
		[]SymbolID{indexedMembers[0].ID, indexedMembers[1].ID},
	)
	require.Nil(t, snapshot.members)
	require.Nil(t, snapshot.memberAlternates)
	require.Equal(t, []SymbolID{class.ID}, snapshot.globals)
}

func TestSnapshotVisitorsMatchSliceLookups(t *testing.T) {
	t.Parallel()

	class := Symbol{
		ID:             "class",
		Kind:           ClassSymbol,
		Name:           "Service",
		FullyQualified: "App\\Service",
		Path:           "/service.php",
	}
	method := Symbol{
		ID:        "method",
		Kind:      MethodSymbol,
		Name:      "run",
		Container: class.ID,
		Path:      class.Path,
	}
	alternateMethod := method
	alternateMethod.ID = "alternate-method"
	function := Symbol{
		ID:             "function",
		Kind:           FunctionSymbol,
		Name:           "build",
		FullyQualified: "App\\build",
		Path:           class.Path,
	}
	constant := Symbol{
		ID:             "constant",
		Kind:           GlobalConstantSymbol,
		Name:           "VERSION",
		FullyQualified: "App\\VERSION",
		Path:           class.Path,
	}
	document := &Document{
		Path: class.Path,
		Symbols: []Symbol{
			class,
			method,
			alternateMethod,
			function,
			constant,
		},
	}
	snapshot := NewSnapshot(1, []*Document{document}).
		WithDeclarations(document)

	collect := func(
		visit func(func(SymbolView) bool) bool,
	) []SymbolID {
		var result []SymbolID
		require.True(t, visit(func(symbol SymbolView) bool {
			result = append(result, symbol.ID())
			return true
		}))
		return result
	}
	viewIDs := func(views []SymbolView) []SymbolID {
		result := make([]SymbolID, 0, len(views))
		for _, view := range views {
			result = append(result, view.ID())
		}
		return result
	}

	require.Equal(
		t,
		viewIDs(snapshot.ClassViews(class.FullyQualified)),
		collect(func(visit func(SymbolView) bool) bool {
			return snapshot.VisitClassViews(class.FullyQualified, visit)
		}),
	)
	require.Equal(
		t,
		viewIDs(snapshot.FunctionViews(function.FullyQualified)),
		collect(func(visit func(SymbolView) bool) bool {
			return snapshot.VisitFunctionViews(function.FullyQualified, visit)
		}),
	)
	require.Equal(
		t,
		viewIDs(snapshot.ConstantViews(constant.FullyQualified)),
		collect(func(visit func(SymbolView) bool) bool {
			return snapshot.VisitConstantViews(constant.FullyQualified, visit)
		}),
	)
	require.Equal(
		t,
		viewIDs(snapshot.MemberViews(class.ID, method.Name)),
		collect(func(visit func(SymbolView) bool) bool {
			return snapshot.VisitMemberViews(class.ID, method.Name, visit)
		}),
	)

	visited := 0
	require.False(t, snapshot.VisitMemberViews(
		class.ID,
		method.Name,
		func(SymbolView) bool {
			visited++
			return false
		},
	))
	require.Equal(t, 1, visited)
}

func TestSnapshotSingleResultVisitorsDoNotAllocate(t *testing.T) {
	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range buildInfo.Settings {
			if setting.Key == "-gcflags" &&
				strings.Contains(setting.Value, "checkptr") {
				t.Skip("checkptr instrumentation allocates around packed views")
			}
		}
	}

	class := Symbol{
		ID:             "class",
		Kind:           ClassSymbol,
		Name:           "Service",
		FullyQualified: "App\\Service",
		Path:           "/service.php",
	}
	method := Symbol{
		ID:        "method",
		Kind:      MethodSymbol,
		Name:      "run",
		Container: class.ID,
		Path:      class.Path,
	}
	snapshot := NewSnapshot(1, []*Document{{
		Path:    class.Path,
		Symbols: []Symbol{class, method},
	}})

	visited := 0
	allocations := testing.AllocsPerRun(100, func() {
		snapshot.VisitClassViews(class.FullyQualified, func(SymbolView) bool {
			visited++
			return true
		})
		snapshot.VisitMemberViews(class.ID, method.Name, func(SymbolView) bool {
			visited++
			return true
		})
	})
	require.Zero(t, allocations)
	require.Positive(t, visited)
}

func TestSymbolNameIndexKeepsDistinctAlternates(t *testing.T) {
	t.Parallel()

	var index symbolNameIndex
	require.True(t, index.add("app\\service", "first"))
	require.False(t, index.add("app\\service", "first"))
	require.True(t, index.add("app\\service", "second"))
	require.False(t, index.add("app\\service", "second"))
	require.Equal(
		t,
		[]SymbolID{"first", "second"},
		index.ids("app\\service"),
	)
}

func TestSnapshotIndexesOnlyWinningDuplicateDeclaration(t *testing.T) {
	t.Parallel()

	original := Symbol{
		ID:             "service",
		Kind:           ClassSymbol,
		Name:           "OldService",
		FullyQualified: "App\\OldService",
		Path:           "/old.php",
	}
	replacement := original
	replacement.Name = "NewService"
	replacement.FullyQualified = "App\\NewService"
	replacement.Path = "/new.php"

	snapshot := NewSnapshot(1, []*Document{{
		Path:    original.Path,
		Symbols: []Symbol{original},
	}, {
		Path:    replacement.Path,
		Symbols: []Symbol{replacement},
	}})

	require.Empty(t, snapshot.ClassViews(original.FullyQualified))
	classes := snapshot.ClassViews(replacement.FullyQualified)
	require.Len(t, classes, 1)
	require.Equal(t, replacement.Path, classes[0].Path())
}

func TestSnapshotResolvesCrossFileReferencesFromSemanticNames(t *testing.T) {
	t.Parallel()
	class := Symbol{
		ID:             "class",
		Kind:           ClassSymbol,
		Name:           "Service",
		FullyQualified: "App\\Service",
		Path:           "/service.php",
	}
	method := Symbol{
		ID:        "method",
		Kind:      MethodSymbol,
		Name:      "run",
		Container: class.ID,
		Path:      class.Path,
	}
	consumer := &Document{
		Path: "/consumer.php",
		References: []Reference{
			referenceWithTargets(Reference{
				Name:  "Service",
				Kind:  ClassName,
				Range: cst.TextRange{Start: 1, End: 8},
			}, []string{"App\\Service"}, nil),
			{
				Name:       "run",
				Kind:       MemberName,
				Receiver:   types.Named("App\\Service"),
				TargetKind: MethodSymbol,
				Range:      cst.TextRange{Start: 20, End: 23},
			},
		},
	}
	snapshot := NewSnapshot(1, []*Document{
		consumer,
		{Path: class.Path, Symbols: []Symbol{class, method}},
	})
	require.Nil(t, snapshot.reverseReferences.references)
	require.Len(t, snapshot.ReferencesTo(class.ID), 1)
	require.Len(t, snapshot.ReferencesTo(method.ID), 1)
	require.NotNil(t, snapshot.reverseReferences.references)
}

func TestSnapshotBuildsReverseReferencesOnceConcurrently(t *testing.T) {
	t.Parallel()

	class := Symbol{
		ID:             "class",
		Kind:           ClassSymbol,
		Name:           "Service",
		FullyQualified: "App\\Service",
		Path:           "/service.php",
	}
	snapshot := NewSnapshot(1, []*Document{
		{
			Path: class.Path,
			Symbols: []Symbol{
				class,
			},
		},
		{
			Path: "/consumer.php",
			References: []Reference{referenceWithTargets(Reference{
				Name:  "Service",
				Kind:  ClassName,
				Range: cst.TextRange{Start: 10, End: 17},
			}, []string{"App\\Service"}, nil)},
		},
	})

	const readers = 16
	start := make(chan struct{})
	results := make(chan []ReferenceLocation, readers)
	var wait sync.WaitGroup
	wait.Add(readers)
	for range readers {
		go func() {
			defer wait.Done()
			<-start
			results <- snapshot.ReferencesTo(class.ID)
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	for references := range results {
		require.Equal(t, []ReferenceLocation{{
			Path:       "/consumer.php",
			RangeStart: 10,
			RangeEnd:   17,
		}}, references)
	}
}

func TestSnapshotRelationsTreatClassNamesCaseInsensitively(t *testing.T) {
	t.Parallel()
	snapshot := NewSnapshot(1, []*Document{{
		Path: "/product.php",
		Symbols: []Symbol{{
			ID:             "product",
			Kind:           ClassSymbol,
			Name:           "Product",
			FullyQualified: "Shopware\\Core\\Product",
			Path:           "/product.php",
		}},
	}})
	require.True(t, snapshot.Relations().IsSubtype(
		types.Named("SHOPWARE\\CORE\\PRODUCT"),
		types.Named("shopware\\core\\product"),
	))
}

func TestOverlayResolvesReferencesFromOtherDocuments(t *testing.T) {
	t.Parallel()
	consumer := &Document{
		Path: "/consumer.php",
		References: []Reference{referenceWithTargets(Reference{
			Name:  "Service",
			Kind:  ClassName,
			Range: cst.TextRange{Start: 10, End: 17},
		}, []string{"App\\Service"}, nil)},
	}
	snapshot := NewSnapshot(1, []*Document{consumer})
	service := Symbol{
		ID:             "service",
		Kind:           ClassSymbol,
		Name:           "Service",
		FullyQualified: "App\\Service",
		Path:           "/service.php",
	}
	overlay := snapshot.WithDocument(&Document{
		Path:    "/service.php",
		Symbols: []Symbol{service},
	})
	require.Equal(t, []ReferenceLocation{{
		Path:       "/consumer.php",
		RangeStart: 10,
		RangeEnd:   17,
	}}, overlay.ReferencesTo(service.ID))
}

func TestOverlayReferencesPrefilterUnrelatedInheritedMembers(t *testing.T) {
	t.Parallel()
	parent := Symbol{
		ID:             "parent",
		Kind:           ClassSymbol,
		Name:           "ParentService",
		FullyQualified: "App\\ParentService",
		Path:           "/parent.php",
	}
	method := Symbol{
		ID:        "execute",
		Kind:      MethodSymbol,
		Name:      "execute",
		Container: parent.ID,
		Path:      parent.Path,
	}
	child := Symbol{
		ID:             "child",
		Kind:           ClassSymbol,
		Name:           "ChildService",
		FullyQualified: "App\\ChildService",
		Path:           "/child.php",
		Extends:        []string{parent.FullyQualified},
	}
	consumer := &Document{
		Path: "/consumer.php",
		References: []Reference{{
			Name:       "execute",
			Kind:       MemberName,
			Receiver:   types.Named(child.FullyQualified),
			TargetKind: MethodSymbol,
			Range:      cst.TextRange{Start: 10, End: 17},
		}, {
			Name:       "unrelated",
			Kind:       MemberName,
			Receiver:   types.Named(child.FullyQualified),
			TargetKind: MethodSymbol,
			Range:      cst.TextRange{Start: 30, End: 39},
		}},
	}
	snapshot := NewSnapshot(1, []*Document{
		{Path: parent.Path, Symbols: []Symbol{parent, method}},
		{Path: child.Path, Symbols: []Symbol{child}},
		consumer,
	}).WithDocument(&Document{Path: "/open.php"})

	target, found := snapshot.SymbolView(method.ID)
	require.True(t, found)
	packed := snapshot.base.pathRefs[consumer.Path]
	require.NotNil(t, packed)
	require.True(t, snapshot.referenceMayTargetPacked(
		packed, &packed.References[0], target,
	))
	require.False(t, snapshot.referenceMayTargetPacked(
		packed, &packed.References[1], target,
	))
	require.Equal(t, []ReferenceLocation{{
		Path:       consumer.Path,
		RangeStart: 10,
		RangeEnd:   17,
	}}, snapshot.ReferencesTo(method.ID))
}

func TestUpdatedSymbolOverlayIsImmutableAndReusesIndexes(t *testing.T) {
	t.Parallel()
	method := Symbol{
		ID:             "method",
		Kind:           FunctionSymbol,
		Name:           "value",
		FullyQualified: "value",
		Path:           "/value.php",
		ReturnType:     types.Unknown(),
	}
	snapshot := NewSnapshot(1, []*Document{{
		Path:    method.Path,
		Symbols: []Symbol{method},
	}})
	method.ReturnType = types.String()
	updated := snapshot.WithUpdatedSymbols(&Document{
		Path:    method.Path,
		Symbols: []Symbol{method},
	})
	original, ok := snapshot.Symbol(method.ID)
	require.True(t, ok)
	require.True(t, original.ReturnType.IsUnknown())
	resolved := updated.Functions("value")
	require.Len(t, resolved, 1)
	require.Equal(t, "string", resolved[0].ReturnType.String())
}

func TestSnapshotProjectsGenericSupertypes(t *testing.T) {
	t.Parallel()
	base := Symbol{
		ID:             "base",
		Kind:           ClassSymbol,
		Name:           "Producer",
		FullyQualified: "Producer",
		Templates: []TemplateParameter{{
			Name:      "T",
			Covariant: true,
		}},
	}
	entity := Symbol{
		ID:             "entity",
		Kind:           ClassSymbol,
		Name:           "Entity",
		FullyQualified: "Entity",
	}
	product := Symbol{
		ID:             "product",
		Kind:           ClassSymbol,
		Name:           "Product",
		FullyQualified: "Product",
		Extends:        []string{"Entity"},
	}
	child := Symbol{
		ID:             "child",
		Kind:           ClassSymbol,
		Name:           "ProductProducer",
		FullyQualified: "ProductProducer",
		Extends:        []string{"Producer"},
		ExtendsTypes: []types.Type{
			types.Named("Producer", types.Named("Product")),
		},
	}
	snapshot := NewSnapshot(1, []*Document{{
		Path:    "/types.php",
		Symbols: []Symbol{base, entity, product, child},
	}})
	require.True(t, snapshot.Relations().IsSubtype(
		types.Named("ProductProducer"),
		types.Named("Producer", types.Named("Entity")),
	))
	projected, ok := snapshot.AsSupertype(
		types.Named("ProductProducer"),
		"Producer",
	)
	require.True(t, ok)
	require.Equal(t, "Producer<Product>", projected.String())
}

func TestSnapshotProjectsExplicitGenericThroughConcreteCollectionSubclasses(t *testing.T) {
	t.Parallel()
	iterator := Symbol{
		ID:             "iterator",
		Kind:           InterfaceSymbol,
		Name:           "IteratorAggregate",
		FullyQualified: "IteratorAggregate",
		Templates: []TemplateParameter{
			{Name: "TKey"},
			{Name: "TValue"},
		},
	}
	collection := Symbol{
		ID:             "collection",
		Kind:           ClassSymbol,
		Name:           "Collection",
		FullyQualified: "Collection",
		Templates:      []TemplateParameter{{Name: "TElement"}},
		Implements:     []string{"IteratorAggregate"},
		ImplementsTypes: []types.Type{types.Named(
			"IteratorAggregate",
			types.ArrayKey(),
			types.Template("TElement"),
		)},
	}
	fieldCollection := Symbol{
		ID:             "field-collection",
		Kind:           ClassSymbol,
		Name:           "FieldCollection",
		FullyQualified: "FieldCollection",
		Extends:        []string{"Collection"},
		ExtendsTypes: []types.Type{
			types.Named("Collection", types.Named("Field")),
		},
	}
	compiledFieldCollection := Symbol{
		ID:             "compiled-field-collection",
		Kind:           ClassSymbol,
		Name:           "CompiledFieldCollection",
		FullyQualified: "CompiledFieldCollection",
		Extends:        []string{"FieldCollection"},
	}
	snapshot := NewSnapshot(1, []*Document{{
		Path: "/collections.php",
		Symbols: []Symbol{
			iterator,
			collection,
			fieldCollection,
			compiledFieldCollection,
		},
	}})

	projected, ok := snapshot.AsSupertype(
		types.Named("CompiledFieldCollection", types.Named("ManyToManyAssociationField")),
		"IteratorAggregate",
	)
	require.True(t, ok)
	require.Equal(
		t,
		"IteratorAggregate<array-key,ManyToManyAssociationField>",
		projected.String(),
	)
}
