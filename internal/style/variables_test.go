package style

import (
	"testing"

	scssparser "github.com/shopware/shopware-lsp/internal/parser/scss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeVariablesClassifiesBindingsReferencesAndIgnoredLabels(t *testing.T) {
	source := `$global-value: $seed !default;
.block {
  $local: red;
  $forced: blue !global;
  color: $local;
}
@mixin button($variant, $color: $global-value) {
  color: $color;
  border-color: $missing;
}
@function tone($input) { @return $input; }
@each $key, $value in $items { color: $value; }
@for $i from 1 through $count { z-index: $i; }
@include button($color: $global-value);
math.$pi;
@use "theme" with ($configured: $global-value);`
	parsed := scssparser.Parse(source)
	require.Empty(t, parsed.Errors)

	analysis := AnalyzeVariables("/project/main.scss", parsed.Tree.Root)
	assert.ElementsMatch(t, []string{
		"global-value", "local", "forced", "variant", "color",
		"input", "key", "value", "i",
	}, occurrenceNames(analysis.Bindings))
	assert.ElementsMatch(t, []string{
		"global-value", "forced",
	}, occurrenceNames(analysis.GlobalDeclarations))
	assert.ElementsMatch(t, []string{
		"seed", "local", "global-value", "color", "missing", "input",
		"items", "value", "count", "i", "global-value", "global-value",
	}, occurrenceNames(analysis.References))
	assert.NotContains(t, occurrenceNames(analysis.References), "pi")
	assert.NotContains(t, occurrenceNames(analysis.References), "configured")

	missing := analysis.References[4]
	assert.Equal(t, "$missing", source[missing.Range.Start:missing.Range.End])
}

func TestNormalizeVariableNameMatchesSassIdentifierRules(t *testing.T) {
	assert.Equal(t, "brand-primary", NormalizeVariableName("$brand_primary"))
	assert.Equal(t, "brand-primary", NormalizeVariableName("brand-primary"))
}

func occurrenceNames(occurrences []VariableOccurrence) []string {
	result := make([]string, 0, len(occurrences))
	for _, occurrence := range occurrences {
		result = append(result, occurrence.Name)
	}
	return result
}
