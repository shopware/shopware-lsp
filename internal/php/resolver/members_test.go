package resolver

import (
	"strconv"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

func TestMemberResolutionAcrossInheritanceAndTemplates(t *testing.T) {
	t.Parallel()
	baseID := semantic.SymbolID("base")
	childID := semantic.SymbolID("child")
	documents := []*semantic.Document{{
		Path: "/types.php",
		Symbols: []semantic.Symbol{
			{
				ID:             baseID,
				Kind:           semantic.ClassSymbol,
				Name:           "Collection",
				FullyQualified: "Collection",
				Path:           "/types.php",
				Templates:      []semantic.TemplateParameter{{Name: "T"}},
			},
			{
				ID:             "first",
				Kind:           semantic.MethodSymbol,
				Name:           "first",
				FullyQualified: "Collection::first",
				Container:      baseID,
				ReturnType:     types.Template("T"),
				Assertions: []semantic.TypeAssertion{{
					Target:   "$value",
					Type:     types.Template("T"),
					WhenTrue: true,
				}},
				Path: "/types.php",
			},
			{
				ID:             "current",
				Kind:           semantic.PropertySymbol,
				Name:           "current",
				FullyQualified: "Collection::$current",
				Container:      baseID,
				Type:           types.Template("T"),
				Path:           "/types.php",
			},
			{
				ID:             childID,
				Kind:           semantic.ClassSymbol,
				Name:           "Products",
				FullyQualified: "Products",
				Extends:        []string{"Collection"},
				Path:           "/types.php",
			},
		},
	}}
	snapshot := semantic.NewSnapshot(1, documents)
	resolved := MemberResolver{Snapshot: snapshot}.Methods(
		types.Named("Collection", types.Named("Product")),
		"first",
	)
	require.Len(t, resolved, 1)
	require.Equal(t, "Product", resolved[0].Type.String())
	require.Len(t, resolved[0].Symbol.Assertions, 1)
	require.Equal(
		t,
		"Product",
		resolved[0].Symbol.Assertions[0].Type.String(),
	)

	ids := (MemberResolver{Snapshot: snapshot}).MethodIDs(
		types.Union(types.Named("Products"), types.Named("Collection")),
		"first",
	)
	require.Equal(t, []semantic.SymbolID{"first"}, ids)

	propertyTypes := (MemberResolver{Snapshot: snapshot}).PropertyTypes(
		types.Named("Collection", types.Named("Product")),
		"current",
	)
	require.Len(t, propertyTypes, 1)
	require.Equal(t, "Product", propertyTypes[0].String())

	unionPropertyTypes := (MemberResolver{Snapshot: snapshot}).PropertyTypes(
		types.Union(
			types.Named("Collection", types.Named("Product")),
			types.Named("Collection", types.Named("Category")),
		),
		"current",
	)
	require.Len(t, unionPropertyTypes, 1)
}

func TestMemberResolutionSupportsTraitAliases(t *testing.T) {
	t.Parallel()
	traitID := semantic.SymbolID("trait")
	classID := semantic.SymbolID("consumer")
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{
		{
			Path: "/trait.php",
			Symbols: []semantic.Symbol{
				{
					ID:             traitID,
					Kind:           semantic.TraitSymbol,
					Name:           "Reusable",
					FullyQualified: "Reusable",
					Path:           "/trait.php",
				},
				{
					ID:             "value",
					Kind:           semantic.MethodSymbol,
					Name:           "value",
					FullyQualified: "Reusable::value",
					Container:      traitID,
					Visibility:     semantic.Public,
					Parameters: []semantic.Parameter{{
						Name: "$input",
						Type: types.String(),
					}},
					ReturnType: types.Int(),
					Path:       "/trait.php",
				},
			},
		},
		{
			Path: "/consumer.php",
			Symbols: []semantic.Symbol{{
				ID:             classID,
				Kind:           semantic.ClassSymbol,
				Name:           "Consumer",
				FullyQualified: "Consumer",
				Traits:         []string{"Reusable"},
				TraitAliases: []semantic.TraitAlias{{
					Trait:         "Reusable",
					Method:        "value",
					Alias:         "aliasedValue",
					Visibility:    semantic.Private,
					HasVisibility: true,
				}},
				Path: "/consumer.php",
			}},
		},
	})
	resolver := MemberResolver{Snapshot: snapshot}
	methods := resolver.Methods(types.Named("Consumer"), "aliasedValue")
	require.Len(t, methods, 1)
	require.Equal(t, "aliasedValue", methods[0].Symbol.Name)
	require.Equal(t, classID, methods[0].Symbol.Container)
	require.Equal(t, semantic.Private, methods[0].Symbol.Visibility)
	require.Len(t, methods[0].Symbol.Parameters, 1)
	require.Equal(t, "int", methods[0].Type.String())
	require.Equal(
		t,
		[]semantic.SymbolID{"value"},
		resolver.MethodIDs(types.Named("Consumer"), "aliasedValue"),
	)

	var names []string
	for _, member := range resolver.All(types.Named("Consumer")) {
		if member.Symbol.Kind == semantic.MethodSymbol {
			names = append(names, member.Symbol.Name)
		}
	}
	require.ElementsMatch(t, []string{"aliasedValue", "value"}, names)
}

func TestMemberIDVisitorsMatchSliceAPIsAndStopEarly(t *testing.T) {
	t.Parallel()
	classID := semantic.SymbolID("service")
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/service.php",
		Symbols: []semantic.Symbol{
			{
				ID:             classID,
				Kind:           semantic.ClassSymbol,
				Name:           "Service",
				FullyQualified: "Service",
				Path:           "/service.php",
			},
			{
				ID:        "run",
				Kind:      semantic.MethodSymbol,
				Name:      "run",
				Container: classID,
				Path:      "/service.php",
			},
			{
				ID:        "name",
				Kind:      semantic.PropertySymbol,
				Name:      "name",
				Container: classID,
				Path:      "/service.php",
			},
			{
				ID:        "kind",
				Kind:      semantic.ClassConstantSymbol,
				Name:      "KIND",
				Container: classID,
				Path:      "/service.php",
			},
			{
				ID:        "active",
				Kind:      semantic.EnumCaseSymbol,
				Name:      "ACTIVE",
				Container: classID,
				Path:      "/service.php",
			},
		},
	}})
	resolver := MemberResolver{Snapshot: snapshot}
	receiver := types.Named("Service")
	collect := func(
		visit func(func(semantic.SymbolID) bool) bool,
	) []semantic.SymbolID {
		var result []semantic.SymbolID
		require.True(t, visit(func(id semantic.SymbolID) bool {
			result = append(result, id)
			return true
		}))
		return result
	}

	require.Equal(
		t,
		resolver.MethodIDs(receiver, "run"),
		collect(func(visit func(semantic.SymbolID) bool) bool {
			return resolver.VisitMethodIDs(receiver, "run", visit)
		}),
	)
	require.Equal(
		t,
		resolver.AllMethodIDs(receiver),
		collect(func(visit func(semantic.SymbolID) bool) bool {
			return resolver.VisitAllMethodIDs(receiver, visit)
		}),
	)
	require.Equal(
		t,
		resolver.PropertyIDs(receiver, "name"),
		collect(func(visit func(semantic.SymbolID) bool) bool {
			return resolver.VisitPropertyIDs(receiver, "name", visit)
		}),
	)
	for _, name := range []string{"KIND", "ACTIVE"} {
		require.Equal(
			t,
			resolver.ConstantIDs(receiver, name),
			collect(func(visit func(semantic.SymbolID) bool) bool {
				return resolver.VisitConstantIDs(receiver, name, visit)
			}),
		)
	}

	visited := 0
	require.False(t, resolver.VisitMethodIDs(
		receiver,
		"run",
		func(semantic.SymbolID) bool {
			visited++
			return false
		},
	))
	require.Equal(t, 1, visited)
}

func TestResolvedMethodVisitorMatchesUnionAndStopsEarly(t *testing.T) {
	t.Parallel()
	documents := make([]*semantic.Document, 0, 2)
	for _, className := range []string{"First", "Second"} {
		classID := semantic.SymbolID(strings.ToLower(className))
		documents = append(documents, &semantic.Document{
			Path: "/" + className + ".php",
			Symbols: []semantic.Symbol{
				{
					ID:             classID,
					Kind:           semantic.ClassSymbol,
					Name:           className,
					FullyQualified: className,
					Path:           "/" + className + ".php",
				},
				{
					ID:             semantic.SymbolID(className + "::run"),
					Kind:           semantic.MethodSymbol,
					Name:           "run",
					FullyQualified: className + "::run",
					Container:      classID,
					ReturnType:     types.Named(className),
					Path:           "/" + className + ".php",
				},
			},
		})
	}
	resolver := MemberResolver{Snapshot: semantic.NewSnapshot(1, documents)}
	receiver := types.Union(types.Named("First"), types.Named("Second"))
	expected := resolver.Methods(receiver, "run")
	var actual []ResolvedMember
	require.True(t, resolver.VisitMethods(
		receiver,
		"run",
		func(member ResolvedMember) bool {
			actual = append(actual, member)
			return true
		},
	))
	require.Equal(t, expected, actual)

	visited := 0
	require.False(t, resolver.VisitMethods(
		receiver,
		"run",
		func(ResolvedMember) bool {
			visited++
			return false
		},
	))
	require.Equal(t, 1, visited)
}

func BenchmarkMemberResolverPropertyTypes(b *testing.B) {
	classID := semantic.SymbolID("collection")
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/collection.php",
		Symbols: []semantic.Symbol{
			{
				ID:             classID,
				Kind:           semantic.ClassSymbol,
				Name:           "Collection",
				FullyQualified: "Collection",
				Templates:      []semantic.TemplateParameter{{Name: "T"}},
				Path:           "/collection.php",
			},
			{
				ID:             "current",
				Kind:           semantic.PropertySymbol,
				Name:           "current",
				FullyQualified: "Collection::$current",
				Container:      classID,
				Type:           types.Template("T"),
				Path:           "/collection.php",
			},
		},
	}})
	resolver := MemberResolver{Snapshot: snapshot}
	receiver := types.Named("Collection", types.Named("Product"))
	b.ReportAllocs()
	for b.Loop() {
		result := resolver.PropertyTypes(receiver, "current")
		if len(result) != 1 {
			b.Fatalf("resolved %d properties", len(result))
		}
	}
}

func benchmarkMemberResolverMethods() (MemberResolver, types.Type) {
	classID := semantic.SymbolID("service")
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/service.php",
		Symbols: []semantic.Symbol{
			{
				ID:             classID,
				Kind:           semantic.ClassSymbol,
				Name:           "Service",
				FullyQualified: "Service",
				Path:           "/service.php",
			},
			{
				ID:             "run",
				Kind:           semantic.MethodSymbol,
				Name:           "run",
				FullyQualified: "Service::run",
				Container:      classID,
				Parameters: []semantic.Parameter{{
					Name: "$value",
					Type: types.String(),
				}},
				ReturnType: types.Int(),
				Path:       "/service.php",
			},
		},
	}})
	return MemberResolver{Snapshot: snapshot}, types.Named("Service")
}

func BenchmarkMemberResolverMethods(b *testing.B) {
	resolver, receiver := benchmarkMemberResolverMethods()
	b.ReportAllocs()
	for b.Loop() {
		result := resolver.Methods(receiver, "run")
		if len(result) != 1 || len(result[0].Symbol.Parameters) != 1 {
			b.Fatalf("resolved %d methods", len(result))
		}
	}
}

func BenchmarkMemberResolverVisitMethods(b *testing.B) {
	resolver, receiver := benchmarkMemberResolverMethods()
	b.ReportAllocs()
	for b.Loop() {
		count := 0
		resolver.VisitMethods(
			receiver,
			"run",
			func(member ResolvedMember) bool {
				count += len(member.Symbol.Parameters)
				return true
			},
		)
		if count != 1 {
			b.Fatalf("visited %d method parameters", count)
		}
	}
}

func BenchmarkMemberResolverMethodIDs(b *testing.B) {
	classID := semantic.SymbolID("service")
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/service.php",
		Symbols: []semantic.Symbol{
			{
				ID:             classID,
				Kind:           semantic.ClassSymbol,
				Name:           "Service",
				FullyQualified: "Service",
				Path:           "/service.php",
			},
			{
				ID:             "run",
				Kind:           semantic.MethodSymbol,
				Name:           "run",
				FullyQualified: "Service::run",
				Container:      classID,
				Path:           "/service.php",
			},
		},
	}})
	resolver := MemberResolver{Snapshot: snapshot}
	receiver := types.Named("Service")
	b.ReportAllocs()
	for b.Loop() {
		result := resolver.MethodIDs(receiver, "run")
		if len(result) != 1 {
			b.Fatalf("resolved %d methods", len(result))
		}
	}
}

func BenchmarkMemberResolverVisitMethodIDs(b *testing.B) {
	classID := semantic.SymbolID("service")
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/service.php",
		Symbols: []semantic.Symbol{
			{
				ID:             classID,
				Kind:           semantic.ClassSymbol,
				Name:           "Service",
				FullyQualified: "Service",
				Path:           "/service.php",
			},
			{
				ID:             "run",
				Kind:           semantic.MethodSymbol,
				Name:           "run",
				FullyQualified: "Service::run",
				Container:      classID,
				Path:           "/service.php",
			},
		},
	}})
	resolver := MemberResolver{Snapshot: snapshot}
	receiver := types.Named("Service")
	b.ReportAllocs()
	for b.Loop() {
		count := 0
		resolver.VisitMethodIDs(
			receiver,
			"run",
			func(semantic.SymbolID) bool {
				count++
				return true
			},
		)
		if count != 1 {
			b.Fatalf("visited %d methods", count)
		}
	}
}

func TestAllMethodIDsReturnsOnlyEffectiveDeclarations(t *testing.T) {
	t.Parallel()
	interfaceID := semantic.SymbolID("contract")
	concreteID := semantic.SymbolID("concrete")
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/methods.php",
		Symbols: []semantic.Symbol{
			{
				ID:             interfaceID,
				Kind:           semantic.InterfaceSymbol,
				Name:           "Contract",
				FullyQualified: "Contract",
				Path:           "/methods.php",
			},
			{
				ID:             "required",
				Kind:           semantic.MethodSymbol,
				Name:           "run",
				FullyQualified: "Contract::run",
				Container:      interfaceID,
				Path:           "/methods.php",
			},
			{
				ID:             "missing",
				Kind:           semantic.ClassSymbol,
				Name:           "Missing",
				FullyQualified: "Missing",
				Implements:     []string{"Contract"},
				Path:           "/methods.php",
			},
			{
				ID:             concreteID,
				Kind:           semantic.ClassSymbol,
				Name:           "Concrete",
				FullyQualified: "Concrete",
				Implements:     []string{"Contract"},
				Path:           "/methods.php",
			},
			{
				ID:             "implemented",
				Kind:           semantic.MethodSymbol,
				Name:           "run",
				FullyQualified: "Concrete::run",
				Container:      concreteID,
				Path:           "/methods.php",
			},
		},
	}})
	resolver := MemberResolver{Snapshot: snapshot}
	require.Equal(
		t,
		[]semantic.SymbolID{"required"},
		resolver.AllMethodIDs(types.Named("Missing")),
	)
	require.Equal(
		t,
		[]semantic.SymbolID{"implemented"},
		resolver.AllMethodIDs(types.Named("Concrete")),
	)
}

func TestResolveGenericSignature(t *testing.T) {
	t.Parallel()
	symbol := semantic.Symbol{
		Kind:       semantic.FunctionSymbol,
		Name:       "identity",
		ReturnType: types.Template("T"),
		Parameters: []semantic.Parameter{{
			Name: "$value",
			Type: types.Template("T"),
		}},
	}
	resolved := ResolveSignature(
		types.Relations{},
		symbol,
		[]Argument{{Type: types.Named("Product")}},
	)
	require.True(t, resolved.Compatible)
	require.Equal(t, "Product", resolved.ReturnType.String())

	arrayIdentity := symbol
	arrayIdentity.Parameters[0].Type = types.Array(
		types.ArrayKey(),
		types.Template("T"),
	)
	resolved = ResolveSignature(
		types.Relations{},
		arrayIdentity,
		[]Argument{{Type: types.Union(
			types.Array(types.ArrayKey(), types.Named("Product")),
			types.Array(types.ArrayKey(), types.String()),
		)}},
	)
	require.True(t, resolved.Compatible)
	require.Equal(t, "Product|string", resolved.ReturnType.String())
}

func TestResolveGenericSignatureProjectsSubclassToGenericParameter(t *testing.T) {
	t.Parallel()
	extensionID := semantic.SymbolID("extension")
	childID := semantic.SymbolID("child")
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/extensions.php",
		Symbols: []semantic.Symbol{
			{
				ID:             extensionID,
				Kind:           semantic.ClassSymbol,
				Name:           "Extension",
				FullyQualified: "Extension",
				Templates:      []semantic.TemplateParameter{{Name: "T"}},
				Path:           "/extensions.php",
			},
			{
				ID:             childID,
				Kind:           semantic.ClassSymbol,
				Name:           "ProductExtension",
				FullyQualified: "ProductExtension",
				Extends:        []string{"Extension"},
				ExtendsTypes: []types.Type{
					types.Named("Extension", types.Named("Product")),
				},
				Path: "/extensions.php",
			},
		},
	}})
	symbol := semantic.Symbol{
		Kind:       semantic.MethodSymbol,
		Name:       "publish",
		ReturnType: types.Template("T"),
		Templates:  []semantic.TemplateParameter{{Name: "T"}},
		Parameters: []semantic.Parameter{{
			Name: "$extension",
			Type: types.Named("Extension", types.Template("T")),
		}},
	}
	resolved := ResolveSignature(
		snapshot.Relations(),
		symbol,
		[]Argument{{Type: types.Named("ProductExtension")}},
	)
	require.True(t, resolved.Compatible)
	require.Equal(t, "Product", resolved.ReturnType.String())
}

func TestMemberResolutionSpecializesCompleteSignature(t *testing.T) {
	t.Parallel()
	classID := semantic.SymbolID("box")
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/box.php",
		Symbols: []semantic.Symbol{
			{
				ID:             classID,
				Kind:           semantic.ClassSymbol,
				Name:           "Box",
				FullyQualified: "Box",
				Templates:      []semantic.TemplateParameter{{Name: "T"}},
				Path:           "/box.php",
			},
			{
				ID:             "replace",
				Kind:           semantic.MethodSymbol,
				Name:           "replace",
				FullyQualified: "Box::replace",
				Container:      classID,
				ReturnType:     types.Template("T"),
				Parameters: []semantic.Parameter{{
					Name: "$value",
					Type: types.Template("T"),
				}},
				Path: "/box.php",
			},
		},
	}})
	resolved := MemberResolver{Snapshot: snapshot}.Methods(
		types.Named("Box", types.String()),
		"replace",
	)
	require.Len(t, resolved, 1)
	require.Equal(t, "string", resolved[0].Symbol.Parameters[0].Type.String())
	require.Equal(t, "string", resolved[0].Symbol.ReturnType.String())
	retained, ok := snapshot.Symbol("replace")
	require.True(t, ok)
	require.Equal(t, "T", retained.Parameters[0].Type.String())
	require.Equal(t, "T", retained.ReturnType.String())
	require.True(t, ResolveSignature(
		snapshot.Relations(),
		resolved[0].Symbol,
		[]Argument{{Type: types.String()}},
	).Compatible)
	require.False(t, ResolveSignature(
		snapshot.Relations(),
		resolved[0].Symbol,
		[]Argument{{Type: types.Int()}},
	).Compatible)
}

func TestBoundClassTemplateDoesNotFallBackToDefault(t *testing.T) {
	t.Parallel()
	classID := semantic.SymbolID("generic-box")
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/generic-box.php",
		Symbols: []semantic.Symbol{
			{
				ID:             classID,
				Kind:           semantic.ClassSymbol,
				Name:           "GenericBox",
				FullyQualified: "GenericBox",
				Templates: []semantic.TemplateParameter{{
					Name:    "T",
					Default: types.String(),
				}},
			},
			{
				ID:             "generic-box-value",
				Kind:           semantic.MethodSymbol,
				Name:           "value",
				FullyQualified: "GenericBox::value",
				Container:      classID,
				ReturnType:     types.Template("T"),
			},
		},
	}})
	methods := MemberResolver{Snapshot: snapshot}.Methods(
		types.Named("GenericBox", types.Template("T")),
		"value",
	)
	require.Len(t, methods, 1)
	require.Len(t, methods[0].Symbol.Templates, 1)
	require.True(t, methods[0].Symbol.Templates[0].Default.IsUnknown())
	require.Equal(
		t,
		"T",
		ResolveSignature(
			snapshot.Relations(),
			methods[0].Symbol,
			nil,
		).ReturnType.String(),
	)
}

func TestResolveSignatureHonorsBoundsDefaultsAndVariadics(t *testing.T) {
	t.Parallel()
	symbol := semantic.Symbol{
		Kind:       semantic.FunctionSymbol,
		Name:       "collect",
		ReturnType: types.Template("T"),
		Templates: []semantic.TemplateParameter{{
			Name:    "T",
			Bound:   types.Named("Entity"),
			Default: types.Named("DefaultEntity"),
		}},
		Parameters: []semantic.Parameter{{
			Name:  "$values",
			Type:  types.Template("T"),
			Flags: semantic.VariadicFlag,
		}},
	}
	relations := types.Relations{Hierarchy: testHierarchy{
		"Product":       "Entity",
		"DefaultEntity": "Entity",
	}}
	resolved := ResolveSignature(
		relations,
		symbol,
		[]Argument{{Type: types.Named("Product")}, {Type: types.Named("Product")}},
	)
	require.True(t, resolved.Compatible)
	require.Equal(t, "Product", resolved.ReturnType.String())

	invalid := ResolveSignature(
		relations,
		symbol,
		[]Argument{{Type: types.String()}},
	)
	require.False(t, invalid.Compatible)

	withDefault := symbol
	withDefault.Parameters[0].Optional = true
	resolved = ResolveSignature(relations, withDefault, nil)
	require.True(t, resolved.Compatible)
	require.Equal(t, "DefaultEntity", resolved.ReturnType.String())
}

func TestResolveSignatureTreatsMixedArgumentAsNotProvablyInvalid(t *testing.T) {
	t.Parallel()
	resolved := ResolveSignature(
		types.Relations{},
		semantic.Symbol{
			Parameters: []semantic.Parameter{{
				Name: "$value",
				Type: types.String(),
			}},
			ReturnType: types.String(),
		},
		[]Argument{{Type: types.Mixed()}},
	)
	require.True(t, resolved.Compatible)
}

func TestResolveSignatureAcceptsAlreadyBoundClassTemplate(t *testing.T) {
	t.Parallel()
	template := types.Template("TElement")
	resolved := ResolveSignature(
		types.Relations{},
		semantic.Symbol{
			Kind: semantic.MethodSymbol,
			Name: "add",
			Templates: []semantic.TemplateParameter{{
				Name:  "TElement",
				Bound: types.Named("Entity"),
			}},
			ReturnType: types.Void(),
			Parameters: []semantic.Parameter{{
				Name: "$entity",
				Type: template,
			}},
		},
		[]Argument{{Type: template}},
	)
	require.True(t, resolved.Compatible)
	require.Equal(t, template, resolved.Templates["TElement"])
}

func TestResolveSignatureInfersTemplatesFromCallableAndList(t *testing.T) {
	t.Parallel()
	callable := semantic.Symbol{
		Templates: []semantic.TemplateParameter{{
			Name:  "T",
			Bound: types.Mixed(),
		}},
		Parameters: []semantic.Parameter{{
			Name: "$callback",
			Type: types.Callable(
				[]types.CallableParameter{{Type: types.Named("Entity")}},
				types.Template("T"),
			),
		}},
		ReturnType: types.Array(types.String(), types.Template("T")),
	}
	resolved := ResolveSignature(
		types.Relations{},
		callable,
		[]Argument{{Type: types.Callable(
			[]types.CallableParameter{{Type: types.Named("Entity")}},
			types.String(),
		)}},
	)
	require.True(t, resolved.Compatible)
	require.Equal(t, "array<string,string>", resolved.ReturnType.String())

	arrayFilter := semantic.Symbol{
		Templates: []semantic.TemplateParameter{
			{Name: "TKey", Bound: types.ArrayKey()},
			{Name: "TValue", Bound: types.Mixed()},
		},
		Parameters: []semantic.Parameter{{
			Name: "$array",
			Type: types.Array(
				types.Template("TKey"),
				types.Template("TValue"),
			),
		}},
		ReturnType: types.Array(
			types.Template("TKey"),
			types.Template("TValue"),
		),
	}
	resolved = ResolveSignature(
		types.Relations{},
		arrayFilter,
		[]Argument{{Type: types.List(types.String())}},
	)
	require.True(t, resolved.Compatible)
	require.Equal(t, "array<int,string>", resolved.ReturnType.String())

	fmap := semantic.Symbol{
		Templates: []semantic.TemplateParameter{{
			Name:  "T",
			Bound: types.Mixed(),
		}},
		Parameters: []semantic.Parameter{{
			Name: "$callback",
			Type: types.Callable(
				[]types.CallableParameter{{Type: types.Named("Entity")}},
				types.Union(
					types.Template("T"),
					types.False(),
					types.Null(),
				),
			),
		}},
		ReturnType: types.Array(types.String(), types.Template("T")),
	}
	resolved = ResolveSignature(
		types.Relations{},
		fmap,
		[]Argument{{Type: types.Callable(
			[]types.CallableParameter{{Type: types.Named("Entity")}},
			types.Nullable(types.String()),
		)}},
	)
	require.True(t, resolved.Compatible)
	require.Equal(t, "array<string,string>", resolved.ReturnType.String())

	uncertainArray := semantic.Symbol{
		Templates: []semantic.TemplateParameter{{
			Name:  "T",
			Bound: types.String(),
		}},
		Parameters: []semantic.Parameter{{
			Name: "$values",
			Type: types.Array(types.ArrayKey(), types.Template("T")),
		}},
		ReturnType: types.Void(),
	}
	resolved = ResolveSignature(
		types.Relations{},
		uncertainArray,
		[]Argument{{Type: types.Array(types.Int(), types.Mixed())}},
	)
	require.True(t, resolved.Compatible)
	require.NotContains(t, resolved.Templates, "T")
}

func TestResolveSignatureMapsNamedArgumentsWithoutTemporaryParameterMaps(t *testing.T) {
	t.Parallel()
	symbol := semantic.Symbol{
		Kind:       semantic.FunctionSymbol,
		Name:       "format",
		ReturnType: types.String(),
		Parameters: []semantic.Parameter{
			{Name: "$first", Type: types.String()},
			{Name: "$second", Type: types.Int(), Optional: true},
			{Name: "$rest", Type: types.String(), Flags: semantic.VariadicFlag},
		},
	}
	relations := types.Relations{}
	resolved := ResolveSignature(relations, symbol, []Argument{
		{Name: "second", Type: types.Int()},
		{Name: "$first", Type: types.String()},
		{Name: "unknown", Type: types.String()},
	})
	require.True(t, resolved.Compatible)
	require.Nil(t, resolved.Templates)

	require.False(t, ResolveSignature(relations, symbol, []Argument{
		{Name: "first", Type: types.String()},
		{Name: "$first", Type: types.String()},
	}).Compatible)
	require.False(t, ResolveSignature(relations, symbol, []Argument{
		{Name: "first", Type: types.String()},
		{Type: types.Int()},
	}).Compatible)
	require.False(t, ResolveSignature(relations, symbol, []Argument{
		{Name: "second", Type: types.Int()},
	}).Compatible)

	withoutVariadic := symbol
	withoutVariadic.Parameters = withoutVariadic.Parameters[:2]
	require.False(t, ResolveSignature(relations, withoutVariadic, []Argument{
		{Name: "unknown", Type: types.String()},
	}).Compatible)
}

func TestResolveSignatureTracksMoreThanSixtyFourParameters(t *testing.T) {
	t.Parallel()
	parameters := make([]semantic.Parameter, 65)
	for index := range parameters {
		parameters[index] = semantic.Parameter{
			Name:     "$p" + strconv.Itoa(index),
			Type:     types.Int(),
			Optional: true,
		}
	}
	resolved := ResolveSignature(types.Relations{}, semantic.Symbol{
		Kind:       semantic.FunctionSymbol,
		Name:       "large",
		ReturnType: types.Void(),
		Parameters: parameters,
	}, []Argument{{Name: "p64", Type: types.Int()}})
	require.True(t, resolved.Compatible)
}

func TestPartiallyDefaultedClassTemplatesRemainInferable(t *testing.T) {
	t.Parallel()
	classID := semantic.SymbolID("pair")
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/pair.php",
		Symbols: []semantic.Symbol{
			{
				ID:             classID,
				Kind:           semantic.ClassSymbol,
				Name:           "Pair",
				FullyQualified: "Pair",
				Templates: []semantic.TemplateParameter{
					{Name: "T", Bound: types.Object()},
					{Name: "TLabel", Default: types.String()},
				},
			},
			{
				ID:             "construct",
				Kind:           semantic.MethodSymbol,
				Name:           "__construct",
				FullyQualified: "Pair::__construct",
				Container:      classID,
				Parameters: []semantic.Parameter{
					{Name: "$value", Type: types.Template("T")},
					{Name: "$label", Type: types.Template("TLabel"), Optional: true},
				},
			},
		},
	}})
	constructor := MemberResolver{Snapshot: snapshot}.Methods(
		types.Named("Pair"),
		"__construct",
	)
	require.Len(t, constructor, 1)
	require.Len(t, constructor[0].Symbol.Templates, 2)
	resolved := ResolveSignature(
		snapshot.Relations(),
		constructor[0].Symbol,
		[]Argument{{Type: types.Named("Product")}},
	)
	require.True(t, resolved.Compatible)
	require.Equal(t, "Product", resolved.Templates["T"].String())
	require.Equal(t, "string", resolved.Templates["TLabel"].String())
}

func TestMethodTemplateShadowsClassTemplate(t *testing.T) {
	t.Parallel()
	classID := semantic.SymbolID("container")
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/container.php",
		Symbols: []semantic.Symbol{
			{
				ID:             classID,
				Kind:           semantic.ClassSymbol,
				Name:           "Container",
				FullyQualified: "Container",
				Templates:      []semantic.TemplateParameter{{Name: "T"}},
			},
			{
				ID:             "convert",
				Kind:           semantic.MethodSymbol,
				Name:           "convert",
				FullyQualified: "Container::convert",
				Container:      classID,
				Templates:      []semantic.TemplateParameter{{Name: "T"}},
				ReturnType:     types.Template("T"),
				Parameters: []semantic.Parameter{{
					Name: "$value",
					Type: types.Template("T"),
				}},
			},
		},
	}})
	method := MemberResolver{Snapshot: snapshot}.Methods(
		types.Named("Container", types.Named("Product")),
		"convert",
	)
	require.Len(t, method, 1)
	resolved := ResolveSignature(
		snapshot.Relations(),
		method[0].Symbol,
		[]Argument{{Type: types.String()}},
	)
	require.True(t, resolved.Compatible)
	require.Equal(t, "string", resolved.ReturnType.String())
}

func TestMemberResolutionSpecializesMethodTemplateBound(t *testing.T) {
	t.Parallel()
	classID := semantic.SymbolID("collection")
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{{
		Path: "/collection.php",
		Symbols: []semantic.Symbol{
			{
				ID:             classID,
				Kind:           semantic.ClassSymbol,
				Name:           "Collection",
				FullyQualified: "Collection",
				Templates:      []semantic.TemplateParameter{{Name: "TElement"}},
			},
			{
				ID:             "first-where",
				Kind:           semantic.MethodSymbol,
				Name:           "firstWhere",
				FullyQualified: "Collection::firstWhere",
				Container:      classID,
				Templates: []semantic.TemplateParameter{{
					Name:  "T",
					Bound: types.Template("TElement"),
				}},
				ReturnType: types.Nullable(types.Template("T")),
				Parameters: []semantic.Parameter{{
					Name: "$predicate",
					Type: types.Callable(
						[]types.CallableParameter{{
							Type: types.Template("T"),
						}},
						types.Bool(),
					),
				}},
			},
		},
	}})
	methods := MemberResolver{Snapshot: snapshot}.Methods(
		types.Named("Collection", types.Named("Delivery")),
		"firstWhere",
	)
	require.Len(t, methods, 1)
	require.Len(t, methods[0].Symbol.Templates, 2)
	require.Equal(
		t,
		"Delivery",
		methods[0].Symbol.Templates[1].Bound.String(),
	)
	resolved := ResolveSignature(
		snapshot.Relations(),
		methods[0].Symbol,
		[]Argument{{Type: types.Callable(
			[]types.CallableParameter{{Type: types.Named("Delivery")}},
			types.Bool(),
		)}},
	)
	require.True(t, resolved.Compatible)
	require.Equal(t, "Delivery|null", resolved.ReturnType.String())
}

type testHierarchy map[string]string

func (h testHierarchy) IsSubtypeOf(candidate, target string) bool {
	for candidate != "" {
		if candidate == target {
			return true
		}
		candidate = h[candidate]
	}
	return false
}
