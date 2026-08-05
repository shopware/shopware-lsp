package resolver

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

func TestResolveSignatureAcceptsMatchingDocumentedAlias(t *testing.T) {
	t.Parallel()
	alias := types.Named("App\\ImportedArrayAlias")
	symbol := semantic.Symbol{
		Parameters: []semantic.Parameter{{
			Name:       "$value",
			Type:       types.Array(types.ArrayKey(), types.Mixed()),
			NativeType: types.Array(types.ArrayKey(), types.Mixed()),
			DocType:    alias,
		}},
		ReturnType: types.Void(),
	}

	require.True(t, ResolveSignature(
		types.Relations{},
		symbol,
		[]Argument{{Type: alias}},
	).Compatible)
	require.False(t, ResolveSignature(
		types.Relations{},
		symbol,
		[]Argument{{Type: types.Named("App\\DifferentAlias")}},
	).Compatible)
}

func TestResolveSignatureAcceptsNativeTypeWhenPHPDocIsTooNarrow(t *testing.T) {
	t.Parallel()
	node := types.Named("Twig\\Node\\Node")
	symbol := semantic.Symbol{
		Parameters: []semantic.Parameter{{
			Name:       "$nodes",
			Type:       types.Array(types.ArrayKey(), node),
			NativeType: types.Iterable(types.ArrayKey(), types.Mixed()),
			DocType:    types.Array(types.ArrayKey(), node),
		}},
		ReturnType: types.Void(),
	}

	require.True(t, ResolveSignature(
		types.Relations{},
		symbol,
		[]Argument{{Type: types.Iterable(types.Int(), node)}},
	).Compatible)
}

func TestResolveSignatureSurfacesDynamicContractReturnType(t *testing.T) {
	t.Parallel()
	symbol := semantic.Symbol{
		Parameters: []semantic.Parameter{{
			Name: "$id",
			Type: types.String(),
		}},
		ReturnType: types.Object(),
	}
	contract := semantic.CallReturnContract{
		Kind:     semantic.CallReturnArgumentMap,
		Argument: 0,
		Map: []semantic.CallMapEntry{{
			Key: semantic.CallValue{
				Kind:       semantic.CallValueString,
				Value:      "product.repository",
				Expression: "'product.repository'",
			},
			Result: semantic.CallValue{
				Kind:       semantic.CallValueClassConstant,
				Expression: "\\App\\Repository\\ProductRepository::class",
			},
		}},
	}

	resolved := ResolveSignatureWithContracts(
		types.Relations{},
		symbol,
		[]Argument{{
			Type:       types.LiteralString("product.repository"),
			Expression: "'product.repository'",
		}},
		[]semantic.CallReturnContract{contract},
	)
	require.True(t, resolved.Compatible)
	require.True(t, resolved.ContractApplied)
	require.Equal(t, "App\\Repository\\ProductRepository", resolved.ReturnType.String())

	incompatible := symbol
	incompatible.Parameters[0].Type = types.Int()
	resolved = ResolveSignatureWithContracts(
		types.Relations{},
		incompatible,
		[]Argument{{Type: types.LiteralString("product.repository")}},
		[]semantic.CallReturnContract{contract},
	)
	require.False(t, resolved.Compatible)
}

func TestResolveSignatureAllowsExtraPositionalArgumentsForUserCode(t *testing.T) {
	symbol := semantic.Symbol{
		Parameters: []semantic.Parameter{{
			Name: "$value",
			Type: types.String(),
		}},
	}
	arguments := []Argument{
		{Type: types.String()},
		{Type: types.Bool()},
	}
	require.True(t, ResolveSignature(types.Relations{}, symbol, arguments).Compatible)

	symbol.Flags |= semantic.GeneratedStubFlag
	require.False(t, ResolveSignature(types.Relations{}, symbol, arguments).Compatible)
}

func TestResolveSignatureRejectsUnknownNamedArguments(t *testing.T) {
	symbol := semantic.Symbol{
		Parameters: []semantic.Parameter{{
			Name: "$value",
			Type: types.String(),
		}},
	}
	require.False(t, ResolveSignature(
		types.Relations{},
		symbol,
		[]Argument{{Name: "$extra", Type: types.String()}},
	).Compatible)
}

func TestResolveSignatureAppliesReturnTypeContract(t *testing.T) {
	t.Parallel()
	booleanContract := semantic.Attribute{
		Name: "JetBrains\\PhpStorm\\Internal\\ReturnTypeContract",
		Arguments: []semantic.AttributeArgument{
			{
				Name: "true",
				Value: semantic.AttributeValue{
					Kind:  semantic.AttributeValueString,
					Value: "float",
				},
			},
			{
				Name: "false",
				Value: semantic.AttributeValue{
					Kind:  semantic.AttributeValueString,
					Value: "string",
				},
			},
		},
	}
	symbol := semantic.Symbol{
		ReturnType: types.Union(types.String(), types.Float()),
		Parameters: []semantic.Parameter{{
			Name:       "$asFloat",
			Type:       types.Bool(),
			Optional:   true,
			Attributes: []semantic.Attribute{booleanContract},
			DefaultValue: &semantic.AttributeValue{
				Kind:  semantic.AttributeValueBool,
				Value: "false",
			},
		}},
	}
	relations := types.Relations{}
	require.Equal(
		t,
		"float",
		ResolveSignature(relations, symbol, []Argument{{Type: types.True()}}).
			ReturnType.String(),
	)
	require.Equal(
		t,
		"string",
		ResolveSignature(relations, symbol, []Argument{{Type: types.False()}}).
			ReturnType.String(),
	)
	require.Equal(
		t,
		"string",
		ResolveSignature(relations, symbol, nil).ReturnType.String(),
	)

	existenceContract := semantic.Attribute{
		Name: "JetBrains\\PhpStorm\\Internal\\ReturnTypeContract",
		Arguments: []semantic.AttributeArgument{
			{
				Name: "exists",
				Value: semantic.AttributeValue{
					Kind:  semantic.AttributeValueString,
					Value: "int|null",
				},
			},
			{
				Name: "notExists",
				Value: semantic.AttributeValue{
					Kind:  semantic.AttributeValueString,
					Value: "array|null",
				},
			},
		},
	}
	symbol.Parameters = []semantic.Parameter{{
		Name:       "$vars",
		Type:       types.Mixed(),
		Flags:      semantic.VariadicFlag,
		Attributes: []semantic.Attribute{existenceContract},
	}}
	require.Equal(
		t,
		"array|null",
		ResolveSignature(relations, symbol, nil).ReturnType.String(),
	)
	require.Equal(
		t,
		"int|null",
		ResolveSignature(relations, symbol, []Argument{{Type: types.String()}}).
			ReturnType.String(),
	)
}

func TestResolveSignatureDefersBoundsForCallerTemplate(t *testing.T) {
	t.Parallel()
	symbol := semantic.Symbol{
		Templates: []semantic.TemplateParameter{{
			Name:  "Expected",
			Bound: types.Object(),
		}},
		Parameters: []semantic.Parameter{{
			Name: "$class",
			Type: types.ClassString(types.Template("Expected")),
		}},
		ReturnType: types.Void(),
	}

	require.True(t, ResolveSignature(
		types.Relations{},
		symbol,
		[]Argument{{
			Type: types.ClassString(types.Template("T")),
		}},
	).Compatible)
	require.True(t, ResolveSignature(
		types.Relations{},
		symbol,
		[]Argument{{
			Type: types.ClassString(types.Union(
				types.Template("T"),
				types.Named("Product"),
			)),
		}},
	).Compatible)
}

func TestResolveSignatureAcceptsNominalSubtypeForGenericBound(t *testing.T) {
	t.Parallel()
	collection := types.Named("Collection", types.Named("Contract"))
	symbol := semantic.Symbol{
		Templates: []semantic.TemplateParameter{{
			Name:  "T",
			Bound: collection,
		}},
		Parameters: []semantic.Parameter{{
			Name: "$values",
			Type: types.Template("T"),
		}},
		ReturnType: types.Template("T"),
	}
	actual := types.Named("ConcreteCollection")
	resolved := ResolveSignature(
		types.Relations{Hierarchy: testHierarchy{
			"ConcreteCollection": "Collection",
		}},
		symbol,
		[]Argument{{Type: actual}},
	)
	require.True(t, resolved.Compatible)
	require.Equal(t, actual, resolved.ReturnType)
}

func TestResolveSignaturePrefersStructuredTemplateUnionArm(t *testing.T) {
	t.Parallel()
	symbol := semantic.Symbol{
		Templates: []semantic.TemplateParameter{{
			Name:    "T",
			Bound:   types.Object(),
			Default: types.Object(),
		}},
		Parameters: []semantic.Parameter{{
			Name: "$objectOrClass",
			Type: types.Union(
				types.Template("T"),
				types.ClassString(types.Template("T")),
				types.String(),
			),
		}},
		ReturnType: types.Template("T"),
	}
	product := types.Named("Product")
	resolved := ResolveSignature(
		types.Relations{},
		symbol,
		[]Argument{{Type: types.ClassString(product)}},
	)
	require.True(t, resolved.Compatible)
	require.Equal(t, product, resolved.Templates["T"])
	require.Equal(t, product, resolved.ReturnType)
}
