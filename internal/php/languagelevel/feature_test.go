package languagelevel

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/stretchr/testify/require"
)

func TestFeatureVersionBoundaries(t *testing.T) {
	for _, definition := range All() {
		definition := definition
		t.Run(string(definition.Feature), func(t *testing.T) {
			require.True(t, Supports(
				project.Version{Major: definition.Major, Minor: definition.Minor},
				definition.Feature,
			))
			previousMajor, previousMinor := definition.Major, definition.Minor-1
			if previousMinor < 0 {
				previousMajor--
				previousMinor = 99
			}
			require.False(t, Supports(
				project.Version{Major: previousMajor, Minor: previousMinor},
				definition.Feature,
			))
		})
	}
	require.False(t, Supports(project.Version{Major: 99}, Feature("missing")))
}

func TestDetectModernPHPFeatures(t *testing.T) {
	source := `<?php
#[Example]
readonly class Modern {
    public const string KIND = 'modern';
    public private(set) string $name {
        get => $this->name;
        set(string $value) { $this->name = $value; }
    }
    public function __construct(public readonly string $id) {}
    public function run(A&B $both, (A&B)|C $dnf): A|B {
        $value = $this?->factory(name: 'x');
        return match ($value) { null => throw new Error(), default => $value };
    }
}
enum State { case Ready; }
`
	parsed := php.Parse(source)
	require.Empty(t, parsed.Errors)
	occurrences := Detect(parsed.Tree.Root)
	counts := make(map[Feature]int)
	for _, occurrence := range occurrences {
		counts[occurrence.Feature]++
		require.Greater(t, occurrence.Range.End, occurrence.Range.Start)
		require.LessOrEqual(t, int(occurrence.Range.End), len(source))
	}

	require.Equal(t, 1, counts[Attributes])
	require.Equal(t, 1, counts[ReadonlyClasses])
	require.Equal(t, 1, counts[TypedClassConstants])
	require.Equal(t, 1, counts[AsymmetricVisibility])
	require.Equal(t, 1, counts[PropertyHooks])
	require.Equal(t, 1, counts[PropertyPromotion])
	require.Equal(t, 1, counts[ReadonlyProperties])
	require.Equal(t, 1, counts[IntersectionTypes])
	require.Equal(t, 1, counts[DNFTypes])
	require.Equal(t, 1, counts[UnionTypes])
	require.Equal(t, 1, counts[NullsafeOperator])
	require.Equal(t, 1, counts[NamedArguments])
	require.Equal(t, 1, counts[MatchExpressions])
	require.Equal(t, 1, counts[ThrowExpressions])
	require.Equal(t, 1, counts[Enums])
}

func TestDetectIgnoresOrdinaryDeclarations(t *testing.T) {
	parsed := php.Parse(`<?php
class Legacy {
    public const KIND = 'legacy';
    private string $name;
    public function __construct(string $id) {}
}`)
	require.Empty(t, parsed.Errors)
	require.Empty(t, Detect(parsed.Tree.Root))
}
