package phpstormmeta

import (
	"testing"

	"github.com/stretchr/testify/require"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

func TestParseOverrideReturnContracts(t *testing.T) {
	root := phpparser.Parse(`<?php
namespace PHPSTORM_META {
    override(\array_pop(0), elementType(0));
    override(\DOMNode::appendChild(0), type(0));
}`).Tree.Root

	require.Equal(t, []semantic.CallContract{
		{
			Target: semantic.NewFunctionCallTarget("array_pop"),
			Return: semantic.CallReturnContract{
				Kind:     semantic.CallReturnArgumentElementType,
				Argument: 0,
			},
		},
		{
			Target: semantic.NewMethodCallTarget("DOMNode", "appendChild"),
			Return: semantic.CallReturnContract{
				Kind:     semantic.CallReturnArgumentType,
				Argument: 0,
			},
		},
	}, Parse(root))
}

func TestParseIgnoresUnsupportedAndMalformedOverrides(t *testing.T) {
	root := phpparser.Parse(`<?php
namespace PHPSTORM_META;
override(\mapped(0), map(['' => '@']));
override(\missingRule(0));
override($dynamic, type(0));
override(\invalidIndex(0), type('first'));
override(\valid(0), type(2));
`).Tree.Root

	require.Equal(t, []semantic.CallContract{
		{
			Target: semantic.NewFunctionCallTarget("mapped"),
			Return: semantic.CallReturnContract{
				Kind:     semantic.CallReturnArgumentMap,
				Argument: 0,
				Map: []semantic.CallMapEntry{{
					Key: semantic.CallValue{
						Kind:       semantic.CallValueString,
						Value:      "",
						Expression: "''",
					},
					Result: semantic.CallValue{
						Kind:       semantic.CallValueString,
						Value:      "@",
						Expression: "'@'",
					},
				}},
			},
		},
		{
			Target: semantic.NewFunctionCallTarget("valid"),
			Return: semantic.CallReturnContract{
				Kind:     semantic.CallReturnArgumentType,
				Argument: 2,
			},
		},
	}, Parse(root))
}

func TestParseExpectedValuesArgumentSetsAndExitPoints(t *testing.T) {
	root := phpparser.Parse(`<?php
namespace PHPSTORM_META;
registerArgumentsSet('modes', \MODE_ONE, \MODE_TWO, 'custom');
expectedArguments(\configure(), 1, argumentsSet('modes'), \MODE_THREE);
expectedReturnValues(\mode(), argumentsSet('modes'));
exitPoint(\dd());
exitPoint(\trigger_error(ANY_ARGUMENT, \E_USER_ERROR));
`).Tree.Root

	contracts := Parse(root)
	require.Len(t, contracts, 4)
	require.Equal(t, semantic.NewFunctionCallTarget("configure"), contracts[0].Target)
	require.Equal(t, uint16(1), contracts[0].ExpectedArguments[0].Argument)
	require.Equal(t, []string{"\\MODE_ONE", "\\MODE_TWO", "custom", "\\MODE_THREE"}, []string{
		contracts[0].ExpectedArguments[0].Values[0].Label(),
		contracts[0].ExpectedArguments[0].Values[1].Label(),
		contracts[0].ExpectedArguments[0].Values[2].Label(),
		contracts[0].ExpectedArguments[0].Values[3].Label(),
	})
	require.Equal(t, semantic.NewFunctionCallTarget("mode"), contracts[1].Target)
	require.Len(t, contracts[1].ExpectedReturnValues, 3)
	require.Equal(t, semantic.NewFunctionCallTarget("dd"), contracts[2].Target)
	require.True(t, contracts[2].ExitPoint)
	require.Equal(
		t,
		semantic.NewFunctionCallTarget("trigger_error"),
		contracts[3].Target,
	)
	require.True(t, contracts[3].ExitPoint)
	require.Equal(t, uint16(1), contracts[3].ExitArguments[0].Argument)
	require.Equal(t, "\\E_USER_ERROR", contracts[3].ExitArguments[0].Values[0].Label())
}

func TestParseRequiresPhpStormMetaNamespace(t *testing.T) {
	root := phpparser.Parse(`<?php
namespace App;
override(\array_pop(0), elementType(0));
`).Tree.Root

	require.Empty(t, Parse(root))
}
