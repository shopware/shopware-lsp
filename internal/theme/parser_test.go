package theme

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseThemeConfig(t *testing.T) {
	bytes, err := os.ReadFile("testdata/theme.json")
	assert.NoError(t, err)

	filePath := "testdata/theme.json"
	fields, err := ParseThemeConfig(bytes, filePath)
	assert.NoError(t, err)

	// Verify we got fields
	assert.NotEmpty(t, fields)

	// A map to make field searching easier for tests
	fieldsMap := make(map[string]ThemeConfigField)
	for _, field := range fields {
		fieldsMap[field.Key] = field
	}

	// Check that important fields exist
	assert.Contains(t, fieldsMap, "sw-color-brand-primary")
	assert.Contains(t, fieldsMap, "sw-color-success")
	assert.Contains(t, fieldsMap, "sw-logo-desktop")

	// Check a specific field
	primaryColorField := fieldsMap["sw-color-brand-primary"]
	assert.Equal(t, "Primary colour", primaryColorField.Label["en-GB"])
	assert.Equal(t, "color", primaryColorField.Type)
	assert.Equal(t, "#0042a0", primaryColorField.Value)
	assert.True(t, primaryColorField.Editable)
	assert.Equal(t, "themeColors", primaryColorField.Block)
	assert.Equal(t, 100, primaryColorField.Order)

	// Check the Path and Line fields
	assert.Equal(t, filePath, primaryColorField.Path)
	assert.Greater(t, primaryColorField.Line, 0) // Line should be greater than 0

	// Verify we have the expected number of fields
	expectedFieldCount := 20 // Based on the theme.json file
	assert.Len(t, fields, expectedFieldCount)
}
