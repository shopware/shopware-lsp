package catalog

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

func TestCatalogRoundTripAndMaterialization(t *testing.T) {
	t.Parallel()
	value := Catalog{
		Format:     FormatVersion,
		Repository: "https://example.com/stubs.git",
		Commit:     "0123456789012345678901234567890123456789",
		Versions: []Version{
			{Major: 8, Minor: 1},
			{Major: 8, Minor: 2},
		},
		Symbols: []Symbol{
			{
				VersionMask:    0b11,
				Kind:           semantic.InterfaceSymbol,
				Name:           "Contract",
				FullyQualified: "Runtime\\Contract",
				Implements:     []string{"Stringable"},
			},
			{
				VersionMask:    0b10,
				Kind:           semantic.MethodSymbol,
				Name:           "value",
				FullyQualified: "Runtime\\Contract::value",
				Container:      "Runtime\\Contract",
				ReturnType:     types.String(),
				NativeType:     types.String(),
				Attributes: []semantic.Attribute{{
					Name: "JetBrains\\PhpStorm\\Deprecated",
					Arguments: []semantic.AttributeArgument{{
						Name: "reason",
						Value: semantic.AttributeValue{
							Kind:       semantic.AttributeValueString,
							Value:      "Use valueNew()",
							Expression: "'Use valueNew()'",
						},
					}},
				}},
			},
		},
		Contracts: []semantic.CallContract{{
			Target: semantic.NewFunctionCallTarget("identity"),
			Return: semantic.CallReturnContract{
				Kind:     semantic.CallReturnArgumentType,
				Argument: 0,
			},
		}},
	}
	encoded, err := Encode(value)
	require.NoError(t, err)
	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, value.Repository, decoded.Repository)
	require.Equal(t, value.Commit, decoded.Commit)
	require.Equal(t, uint16(1), decoded.VersionMask(project.Version{Major: 8, Minor: 1}))
	require.Equal(t, uint16(2), decoded.VersionMask(project.Version{Major: 8, Minor: 4}))
	require.Equal(t, value.Contracts, decoded.MaterializeContracts())

	one := decoded.Materialize(project.Version{Major: 8, Minor: 1}, "phpstub://8.1/core")
	require.Len(t, one, 1)
	two := decoded.Materialize(project.Version{Major: 8, Minor: 2}, "phpstub://8.2/core")
	require.Len(t, two, 2)
	require.Equal(t, two[0].ID, two[1].Container)
	require.True(t, two[1].Flags.Has(semantic.InternalFlag))
	require.Equal(
		t,
		"Use valueNew()",
		two[1].Attributes()[0].Arguments[0].Value.Value,
	)

	two[0].Implements()[0] = "Changed"
	two[1].Attributes()[0].Arguments[0].Value.Value = "Changed"
	again := decoded.Materialize(project.Version{Major: 8, Minor: 2}, "phpstub://8.2/core")
	require.Equal(t, "Stringable", again[0].Implements()[0])
	require.Equal(
		t,
		"Use valueNew()",
		again[1].Attributes()[0].Arguments[0].Value.Value,
	)
}

func TestDecodeRejectsUnknownFormat(t *testing.T) {
	t.Parallel()
	encoded, err := Encode(Catalog{
		Format:   FormatVersion + 1,
		Versions: []Version{{Major: 8, Minor: 2}},
	})
	require.NoError(t, err)
	_, err = Decode(encoded)
	require.ErrorContains(t, err, "unsupported format")
}

func TestCatalogMaterializesSelectedExtensionBundles(t *testing.T) {
	t.Parallel()
	value := Catalog{
		Format:   FormatVersion,
		Versions: []Version{{Major: 8, Minor: 3}},
		Symbols: []Symbol{
			{
				VersionMask:    1,
				Extension:      "core",
				Kind:           semantic.FunctionSymbol,
				Name:           "strlen",
				FullyQualified: "strlen",
			},
			{
				VersionMask:    1,
				Extension:      "curl",
				Kind:           semantic.FunctionSymbol,
				Name:           "curl_init",
				FullyQualified: "curl_init",
			},
		},
		Contracts: []semantic.CallContract{
			{Target: semantic.NewFunctionCallTarget("strlen"), ExitPoint: true},
			{Target: semantic.NewFunctionCallTarget("curl_init"), ExitPoint: true},
		},
		ContractExtensions: []string{"core", "curl"},
	}
	require.NoError(t, value.PackBundles())
	require.Empty(t, value.Symbols)
	require.Empty(t, value.Contracts)
	require.Len(t, value.Bundles, 2)
	require.Len(t, value.ExtensionSymbols, 2)
	encoded, err := Encode(value)
	require.NoError(t, err)
	value, err = Decode(encoded)
	require.NoError(t, err)

	symbols := value.MaterializeForExtensions(
		project.Version{Major: 8, Minor: 3},
		"phpstub://8.3/selected",
		[]string{"core"},
	)
	require.Len(t, symbols, 1)
	require.Equal(t, "strlen", symbols[0].Name)
	contracts := value.MaterializeContractsForExtensions([]string{"core"})
	require.Len(t, contracts, 1)
	require.Equal(t, "strlen", contracts[0].Target.Name)

	require.Len(t, value.Materialize(
		project.Version{Major: 8, Minor: 3},
		"phpstub://8.3/all",
	), 2)
}
