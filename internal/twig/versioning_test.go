package twig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculateBlockHash(t *testing.T) {
	content := `{% block test %}
    <p>Hello World</p>
{% endblock %}`

	hash := calculateBlockHash(content)

	assert.NotEmpty(t, hash)
	assert.Equal(t, hash, calculateBlockHash(content))

	otherContent := `{% block test %}
    <p>Different content</p>
{% endblock %}`

	otherHash := calculateBlockHash(otherContent)
	assert.NotEqual(t, hash, otherHash)
}

func TestParseVersionComment(t *testing.T) {
	tests := []struct {
		name     string
		comment  string
		line     int
		expected *TwigVersionComment
	}{
		{
			name:    "valid version comment",
			comment: "{# shopware-block: abc123def456@6.4.15.0 #}",
			line:    10,
			expected: &TwigVersionComment{
				Hash:    "abc123def456",
				Version: "6.4.15.0",
				Line:    10,
			},
		},
		{
			name:     "invalid comment",
			comment:  "{# just a regular comment #}",
			line:     5,
			expected: nil,
		},
		{
			name:    "version comment without informational version",
			comment: "{# shopware-block: abc123 #}",
			line:    8,
			expected: &TwigVersionComment{
				Hash: "abc123",
				Line: 8,
			},
		},
		{
			name:     "malformed version comment",
			comment:  "{# shopware-block: not-a-hash@6.7.0.0 #}",
			line:     9,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseVersionComment(tt.comment, tt.line)
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.Equal(t, tt.expected.Hash, result.Hash)
				assert.Equal(t, tt.expected.Version, result.Version)
				assert.Equal(t, tt.expected.Line, result.Line)
			}
		})
	}
}

func TestFormatVersionCommentOmitsUnknownVersion(t *testing.T) {
	assert.Equal(
		t,
		"{# shopware-block: abc123 #}\n",
		FormatVersionComment("abc123", ""),
	)
	assert.Equal(
		t,
		"{# shopware-block: abc123@v6.7.2.0 #}\n",
		FormatVersionComment("abc123", "v6.7.2.0"),
	)
}

func TestTwigBlockHashStructure(t *testing.T) {
	blockHash := TwigBlockHash{
		Name:         "test_block",
		RelativePath: "storefront/page/checkout/cart/index.html.twig",
		AbsolutePath: "/path/to/storefront/page/checkout/cart/index.html.twig",
		Hash:         "abc123def456",
		Text:         "{% block test_block %}content{% endblock %}",
	}

	assert.Equal(t, "test_block", blockHash.Name)
	assert.Equal(t, "storefront/page/checkout/cart/index.html.twig", blockHash.RelativePath)
	assert.Equal(t, "abc123def456", blockHash.Hash)
}

func TestHashCompatibilityWithPhpStorm(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		expectedHash string
	}{
		{
			name: "simple block",
			content: `{% block test %}
    <p>Hello World</p>
{% endblock %}`,
			expectedHash: "86eec44546f994424c37efba9b8f58be1ec94259be09302891e4a681c4b02918",
		},
		{
			name:         "inline block",
			content:      `{% block test_block %}content{% endblock %}`,
			expectedHash: "851bf4d9e13400b18923d9428b8f2d681f6f50b2d2f5c9bf0944ef09aa44a8d1",
		},
		{
			name: "block with nested html",
			content: `{% block test_block %}
    <p>Hello World</p>
{% endblock %}`,
			expectedHash: "fa4dc0f67b4ec5acf55b934657a181b9de40d3b5435e5b04f8ece858a9e8bff4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := calculateBlockHash(tt.content)
			assert.Equal(t, tt.expectedHash, hash, "Hash should match PHPStorm plugin output")
			assert.Len(t, hash, 64, "SHA-256 hash should be 64 hex characters")
		})
	}
}
