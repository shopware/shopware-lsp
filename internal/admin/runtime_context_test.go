package admin

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsPrivilegeReferenceRestrictsDependencyArraysToPrivilegeMappings(
	t *testing.T,
) {
	tests := []struct {
		name     string
		source   string
		expected bool
	}{
		{
			name: "privilege mapping dependency",
			source: `Shopware.Service('privileges').addPrivilegeMappingEntry({
                dependencies: ['product.viewer'],
            })`,
			expected: true,
		},
		{
			name:     "unrelated dependency metadata",
			source:   `registerPlugin({ dependencies: ['product.viewer'] })`,
			expected: false,
		},
		{
			name:     "component privilege property",
			source:   `export default { privilege: 'product.viewer' }`,
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := parseJS(t, test.source)
			offset := strings.Index(test.source, "product.viewer") + 1
			assert.Equal(
				t, test.expected,
				IsPrivilegeReference(root.NodeAtOffset(uint32(offset))),
			)
		})
	}
}
