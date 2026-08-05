package feature

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFeatureFile(t *testing.T) {
	// Read the test file
	filePath := filepath.Join("testdata", "feature.yaml")
	content, err := os.ReadFile(filePath)
	require.NoError(t, err, "Reading test file should not fail")

	// Parse the features from the file
	features, err := ParseFeatureFile(content, filePath)
	require.NoError(t, err, "Parsing feature file should not fail")
	require.Len(t, features, 8, "Should find 8 features in the test file")

	// Verify the expected features are present
	expectedFeatures := map[string]int{
		"v6.5.0.0":                              4,
		"v6.6.0.0":                              8,
		"v6.7.0.0":                              12,
		"v6.8.0.0":                              16,
		"DISABLE_VUE_COMPAT":                    20,
		"ACCESSIBILITY_TWEAKS":                  24,
		"TELEMETRY_METRICS":                     29,
		"FLOW_EXECUTION_AFTER_BUSINESS_PROCESS": 34,
	}

	for _, feature := range features {
		expectedLine, ok := expectedFeatures[feature.Name]
		assert.True(t, ok, "Feature %s should be in the expected list", feature.Name)
		assert.Equal(t, expectedLine, feature.Line, "Feature %s should be at line %d", feature.Name, expectedLine)
		assert.Equal(t, filePath, feature.File, "Feature %s should have the correct file path", feature.Name)
	}
}
