package semantic

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAttributeNamedMatchesQualifiedAndShortNames(t *testing.T) {
	t.Parallel()
	attributes := []Attribute{{Name: `\JetBrains\PhpStorm\NoReturn\`}}

	for _, name := range []string{
		"NoReturn",
		"noreturn",
		`JetBrains\PhpStorm\NoReturn`,
		`\JETBRAINS\PHPSTORM\NORETURN\`,
	} {
		_, found := AttributeNamed(attributes, name)
		require.True(t, found, name)
	}
	_, found := AttributeNamed(attributes, `Other\NoReturn`)
	require.False(t, found)
}

func TestSymbolViewAttributesPreserveSnapshotLayers(t *testing.T) {
	t.Parallel()
	const symbolID SymbolID = "target"
	base := NewSnapshot(1, []*Document{{
		Path: "/base.php",
		Symbols: []Symbol{{
			ID:         symbolID,
			Kind:       FunctionSymbol,
			Name:       "target",
			Attributes: []Attribute{{Name: "BaseAttribute"}},
			Path:       "/base.php",
		}},
	}})
	view, found := base.SymbolView(symbolID)
	require.True(t, found)
	require.Equal(t, "BaseAttribute", view.Attributes()[0].Name)

	overlay := base.WithDocument(&Document{
		Path: "/open.php",
		Symbols: []Symbol{{
			ID:         "open-target",
			Kind:       FunctionSymbol,
			Name:       "openTarget",
			Attributes: []Attribute{{Name: "OpenAttribute"}},
			Path:       "/open.php",
		}},
	})
	view, found = overlay.SymbolView("open-target")
	require.True(t, found)
	require.Equal(t, "OpenAttribute", view.Attributes()[0].Name)
}

func TestDeprecationOfNamedAttributeArguments(t *testing.T) {
	t.Parallel()
	details, found := DeprecationOf([]Attribute{{
		Name: "JetBrains\\PhpStorm\\Deprecated",
		Arguments: []AttributeArgument{
			{
				Name:  "replacement",
				Value: AttributeValue{Kind: AttributeValueString, Value: "next()"},
			},
			{
				Name:  "since",
				Value: AttributeValue{Kind: AttributeValueString, Value: "2.0"},
			},
			{
				Name:  "reason",
				Value: AttributeValue{Kind: AttributeValueString, Value: "Legacy API"},
			},
		},
	}})
	require.True(t, found)
	require.Equal(t, Deprecation{
		Reason:      "Legacy API",
		Replacement: "next()",
		Since:       "2.0",
	}, details)
}
