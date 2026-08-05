package twig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTwigParse(t *testing.T) {
	content := []byte(`{% block foo %}{% endblock %}`)

	file, err := ParseTwig("test", content)
	assert.NoError(t, err)

	assert.Equal(t, "test", file.Path)

	block, exists := file.Blocks["foo"]
	assert.True(t, exists)
	assert.Equal(t, "foo", block.Name)
	assert.Equal(t, "foo", string(content[block.NameRange.Start:block.NameRange.End]))
	assert.Equal(t, 1, block.Line)
	assert.NotEmpty(t, block.Hash)
	assert.Equal(t, "{% block foo %}{% endblock %}", block.Text)
}

func TestTwigParseSwExtends(t *testing.T) {
	content := []byte(`{% sw_extends '@Storefront/storefront/base.html.twig' %}`)

	file, err := ParseTwig("test", content)
	assert.NoError(t, err)

	assert.Equal(t, "test", file.Path)
	assert.Equal(t, "@Storefront/storefront/base.html.twig", file.ExtendsFile)
}

func TestTwigParseScopedSwExtends(t *testing.T) {
	content := []byte(
		`{% sw_extends { template: '@Storefront/storefront/base.html.twig', scopes: ['default'] } %}`,
	)
	file, err := ParseTwig("test", content)
	assert.NoError(t, err)
	assert.Equal(
		t,
		"@Storefront/storefront/base.html.twig",
		file.ExtendsFile,
	)
}

func TestNestedBlock(t *testing.T) {
	tpl := `
{% block a %}
	{% block b %}
		{% block c %}
		{% endblock %}
	{% endblock %}
{% endblock %}
`

	file, err := ParseTwig("test", []byte(tpl))
	assert.NoError(t, err)

	assert.Equal(t, "test", file.Path)
	assert.Len(t, file.Blocks, 3)

	blockA, existsA := file.Blocks["a"]
	assert.True(t, existsA)
	assert.Equal(t, "a", blockA.Name)
	assert.Equal(t, 2, blockA.Line)
	assert.NotEmpty(t, blockA.Hash)

	blockB, existsB := file.Blocks["b"]
	assert.True(t, existsB)
	assert.Equal(t, "b", blockB.Name)
	assert.Equal(t, 3, blockB.Line)

	blockC, existsC := file.Blocks["c"]
	assert.True(t, existsC)
	assert.Equal(t, "c", blockC.Name)
	assert.Equal(t, 4, blockC.Line)
}

func TestBlocksWithHTMLContent(t *testing.T) {
	tpl := `{% block base_body %}
    <body>
        {% block base_header %}
            <header>
                {% block base_header_inner %}{% endblock %}
            </header>
        {% endblock %}

        {% block base_content %}
            <div class="content">
                content here
            </div>
        {% endblock %}
    </body>
{% endblock %}`

	file, err := ParseTwig("test", []byte(tpl))
	assert.NoError(t, err)

	blockBody, existsBody := file.Blocks["base_body"]
	assert.True(t, existsBody, "Should find base_body block with HTML content")
	assert.Equal(t, "base_body", blockBody.Name)
	assert.Equal(t, 1, blockBody.Line)
	assert.Contains(t, blockBody.Text, "<body>")
	assert.Contains(t, blockBody.Text, "{% endblock %}")

	blockHeader, existsHeader := file.Blocks["base_header"]
	assert.True(t, existsHeader, "Should find base_header block")
	assert.Equal(t, "base_header", blockHeader.Name)

	blockInner, existsInner := file.Blocks["base_header_inner"]
	assert.True(t, existsInner, "Should find base_header_inner block")
	assert.Equal(t, "base_header_inner", blockInner.Name)

	blockContent, existsContent := file.Blocks["base_content"]
	assert.True(t, existsContent, "Should find base_content block with HTML content")
	assert.Equal(t, "base_content", blockContent.Name)
	assert.Contains(t, blockContent.Text, `<div class="content">`)
}

func TestBlockWithVersionComment(t *testing.T) {
	tpl := `{% sw_extends '@Storefront/storefront/base.html.twig' %}

{# shopware-block: abc123def456@6.4.15.0 #}
{% block foo %}
    content
{% endblock %}
`

	file, err := ParseTwig("test", []byte(tpl))
	assert.NoError(t, err)

	block, exists := file.Blocks["foo"]
	assert.True(t, exists)
	assert.NotNil(t, block.VersionComment)
	assert.Equal(t, "abc123def456", block.VersionComment.Hash)
	assert.Equal(t, "6.4.15.0", block.VersionComment.Version)
	assert.Equal(t, 3, block.VersionComment.Line)
}

func TestBlockDeprecationPreservesVersionAndMigrationHint(t *testing.T) {
	tpl := `{# @deprecated tag:v6.7.0 - use page_new #}
{% block page_old %}content{% endblock %}`

	file, err := ParseTwig("test", []byte(tpl))
	assert.NoError(t, err)

	block, exists := file.Blocks["page_old"]
	assert.True(t, exists)
	assert.Equal(t, "tag:v6.7.0 - use page_new", block.Deprecation)
}
