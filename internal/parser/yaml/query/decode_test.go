package query

import (
	"testing"

	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDecodeNativeCST(t *testing.T) {
	for _, source := range []string{
		"value: 'it''s quoted'\n",
		`value: "escaped\nline\t\u00e4"`,
		"value: |\n  First line\n  Second line\n",
		"value: |-\n  First line\n  Second line\n",
		"value: >\n  First line\n  Second line\n",
		"value: >\n  First paragraph\n\n  Second paragraph\n",
		"defaults: &defaults\n  enabled: true\nvalue:\n  <<: *defaults\n  name: example\n",
		"value: [1, true, null, 'true', {key: value}]",
		"flag: &enabled true\nvalue: *enabled\n",
		"value: !!str 123\n",
		"value: |2\n    indented\n  normal\n",
		"value: |+\n  line\n\n",
	} {
		t.Run(source, func(t *testing.T) {
			var expected, actual map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(source), &expected))
			tree := yamlparser.Parse(source).Tree
			require.NoError(t, Decode(tree.Root, &actual))
			assert.Equal(t, expected, actual)
		})
	}
}
