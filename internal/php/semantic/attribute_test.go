package semantic

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
