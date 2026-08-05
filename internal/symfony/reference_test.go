package symfony

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseServiceReference(t *testing.T) {
	tests := []struct {
		value    string
		name     string
		optional bool
		ok       bool
	}{
		{"@app.service", "app.service", false, true},
		{"@?app.optional", "app.optional", true, true},
		{"@!app.optional", "app.optional", true, true},
		{"@@escaped", "", false, false},
		{"@=service('dynamic')", "", false, false},
		{"plain", "", false, false},
	}
	for _, test := range tests {
		name, optional, ok := ParseServiceReference(test.value)
		assert.Equal(t, test.name, name, test.value)
		assert.Equal(t, test.optional, optional, test.value)
		assert.Equal(t, test.ok, ok, test.value)
	}
}

func TestParameterReferences(t *testing.T) {
	assert.Equal(
		t,
		[]string{"kernel.project_dir", "app.mode"},
		ParameterReferences(
			`%kernel.project_dir%/config %% escaped %env(APP_ENV)% %app.mode%`,
		),
	)
}
