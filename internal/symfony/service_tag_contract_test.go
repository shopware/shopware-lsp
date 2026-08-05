package symfony

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type serviceTagHierarchy map[string]map[string]bool

func (h serviceTagHierarchy) IsSubtypeOf(candidate, target string) bool {
	return h[candidate][target]
}

func TestSuggestedServiceTagsUsesAllMatchingConventionalContracts(
	t *testing.T,
) {
	hierarchy := serviceTagHierarchy{
		"App\\Subscriber": {
			"Symfony\\Component\\EventDispatcher\\" +
				"EventSubscriberInterface": true,
			"Twig\\Extension\\ExtensionInterface": true,
		},
	}

	assert.Equal(t, []string{
		"kernel.event_subscriber",
		"twig.extension",
	}, SuggestedServiceTags("App\\Subscriber", hierarchy))
}

func TestSuggestedServiceTagsSupportsAlternativeContracts(t *testing.T) {
	hierarchy := serviceTagHierarchy{
		"App\\LegacyExtension": {
			"Twig_ExtensionInterface": true,
		},
	}

	assert.Equal(
		t,
		[]string{"twig.extension"},
		SuggestedServiceTags(
			"\\App\\LegacyExtension",
			hierarchy,
		),
	)
	assert.Empty(t, SuggestedServiceTags("", hierarchy))
	assert.Empty(t, SuggestedServiceTags("App\\LegacyExtension", nil))
}
