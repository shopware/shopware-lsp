package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSlotsFromContent(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		expectedNames []string
		expectedLines []int // 1-based line numbers
	}{
		{
			name:          "no slots",
			content:       "<div>Hello World</div>",
			expectedNames: nil,
			expectedLines: nil,
		},
		{
			name:          "default slot only",
			content:       "<div><slot></slot></div>",
			expectedNames: []string{"default"},
			expectedLines: []int{1},
		},
		{
			name:          "named slot",
			content:       `<div><slot name="header"></slot></div>`,
			expectedNames: []string{"header"},
			expectedLines: []int{1},
		},
		{
			name:          "named slot with single quotes",
			content:       `<div><slot name='footer'></slot></div>`,
			expectedNames: []string{"footer"},
			expectedLines: []int{1},
		},
		{
			name: "multiple slots",
			content: `<div>
	<slot name="header"></slot>
	<slot></slot>
	<slot name="footer"></slot>
</div>`,
			expectedNames: []string{"header", "default", "footer"},
			expectedLines: []int{2, 3, 4},
		},
		{
			name: "duplicate slots deduplicated",
			content: `<div>
	<slot name="header"></slot>
	<slot name="header"></slot>
</div>`,
			expectedNames: []string{"header"},
			expectedLines: []int{2}, // Only first occurrence
		},
		{
			name:          "self-closing slot",
			content:       `<div><slot name="icon" /></div>`,
			expectedNames: []string{"icon"},
			expectedLines: []int{1},
		},
		{
			name:          "slot with other attributes",
			content:       `<div><slot name="content" :data="someData"></slot></div>`,
			expectedNames: []string{"content"},
			expectedLines: []int{1},
		},
		{
			name: "slot in twig template",
			content: `{% block sw_card %}
	<div class="sw-card">
		<slot name="header">
			{{ $tc('sw-card.defaultHeader') }}
		</slot>
		<div class="sw-card__content">
			<slot></slot>
		</div>
		<slot name="footer"></slot>
	</div>
{% endblock %}`,
			expectedNames: []string{"header", "default", "footer"},
			expectedLines: []int{3, 7, 9},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseSlotsFromContent(tt.content)

			// Check slot count
			if tt.expectedNames == nil {
				assert.Nil(t, result)
				return
			}

			assert.Len(t, result, len(tt.expectedNames))

			// Check each slot
			for i, slot := range result {
				assert.Equal(t, tt.expectedNames[i], slot.Name, "slot name mismatch at index %d", i)
				if tt.expectedLines != nil && i < len(tt.expectedLines) {
					assert.Equal(t, tt.expectedLines[i], slot.Line, "slot line mismatch for %s", slot.Name)
				}
			}
		})
	}
}

func TestParseSlotsRetainsExactDeclarationRanges(t *testing.T) {
	content := `<slot />
<slot name="header" />
<slot :name="'footer'" />
<slot :name="` + "`column-${column.property}`" + `" />`
	result := parseTemplateContent(content)
	require.Len(t, result.Slots, 4)

	for index, expected := range []string{
		"slot", "header", "footer", "`column-${column.property}`",
	} {
		rangeValue := result.Slots[index].NameRange
		assert.True(t, rangeValue.Declaration, expected)
		assert.False(t, rangeValue.Identifier, expected)
		require.Equal(t, rangeValue.StartLine, rangeValue.EndLine, expected)
		line := strings.Split(content, "\n")[rangeValue.StartLine]
		assert.Equal(
			t, expected,
			line[rangeValue.StartCharacter:rangeValue.EndCharacter],
		)
	}
	assert.Equal(t, "default", result.Slots[0].Name)
	assert.Equal(t, "column-*", result.Slots[3].DisplayName())
}

func TestParseScopedSlotPayloadMembers(t *testing.T) {
	content := `<div>
    <slot
        name="result-item"
        :item="item"
        v-bind="{ index, active: isActive }"
    ></slot>
    <slot :name="forwardedName" v-bind="forwardedData"></slot>
</div>`
	result := parseTemplateContent(content)
	require.Len(t, result.Slots, 1)
	assert.Equal(t, "result-item", result.Slots[0].Name)
	members := make(map[string]VueComponentSlotMember)
	for _, member := range result.Slots[0].Members {
		members[member.Name] = member
	}
	assert.ElementsMatch(t, []string{"item", "index", "active"},
		func() []string {
			result := make([]string, 0, len(members))
			for name := range members {
				result = append(result, name)
			}
			return result
		}())
	assert.Equal(t, 4, members["item"].Line)
	assert.Equal(t, 5, members["index"].Line)
	assert.Equal(t, 5, members["active"].Line)
	lines := strings.Split(content, "\n")
	for _, name := range []string{"item", "index", "active"} {
		member := members[name]
		assert.True(t, member.NameRange.Declaration, name)
		assert.False(t, member.NameRange.Identifier, name)
		assert.Equal(
			t, name,
			lines[member.NameRange.StartLine][member.NameRange.StartCharacter:member.NameRange.EndCharacter],
		)
	}
}

func TestParseScopedSlotPayloadMemberPaths(t *testing.T) {
	templatePath := filepath.Join(t.TempDir(), "component.html.twig")
	require.NoError(t, os.WriteFile(
		templatePath,
		[]byte(`<slot name="default" :active="active"></slot>`),
		0o644,
	))
	result, err := ParseTemplateFromFile(templatePath)
	require.NoError(t, err)
	require.Len(t, result.Slots, 1)
	require.Len(t, result.Slots[0].Members, 1)
	assert.Equal(t, templatePath, result.Slots[0].FilePath)
	assert.Equal(t, templatePath, result.Slots[0].Members[0].FilePath)
}

func TestParseDynamicSlotFamilies(t *testing.T) {
	content := "<div>\n" +
		"  <slot :name=\"`column-${column.property}`\" " +
		"v-bind=\"{ item, isInlineEdit: isInlineEdit(item) }\"></slot>\n" +
		"  <slot :name=\"`column-label-${column.property}`\" " +
		"v-bind=\"{ column, columnIndex }\"></slot>\n" +
		"  <slot :name=\"`prefix-${row.id}-suffix`\" :row=\"row\"></slot>\n" +
		"  <slot :name=\"'bound-static'\" :active=\"active\"></slot>\n" +
		"  <slot :name=\"forwardedName\" v-bind=\"forwardedData\"></slot>\n" +
		"  <slot :name=\"'not-' + 'safe'\"></slot>\n" +
		"  <slot :name=\"`multi-${row.id}-${column.id}`\"></slot>\n" +
		"</div>"
	result := parseTemplateContent(content)
	require.Len(t, result.Slots, 4)

	component := VueComponent{Slots: result.Slots}
	column, found := component.ComponentSlot("column-name")
	require.True(t, found)
	assert.True(t, column.IsDynamicName())
	assert.Equal(t, "column-*", column.DisplayName())
	assert.Equal(t, "column-", column.NamePrefix)
	assert.Empty(t, column.NameSuffix)
	assert.Equal(t, []string{"item", "isInlineEdit"},
		slotMemberNames(column.Members))

	label, found := component.ComponentSlot("column-label-name")
	require.True(t, found)
	assert.Equal(t, "column-label-*", label.DisplayName())
	assert.Equal(t, []string{"column", "columnIndex"},
		slotMemberNames(label.Members))

	suffixed, found := component.ComponentSlot("prefix-value-suffix")
	require.True(t, found)
	assert.Equal(t, "prefix-*-suffix", suffixed.DisplayName())
	assert.Equal(t, []string{"row"}, slotMemberNames(suffixed.Members))

	bound, found := component.ComponentSlot("bound-static")
	require.True(t, found)
	assert.False(t, bound.IsDynamicName())
	assert.Equal(t, []string{"active"}, slotMemberNames(bound.Members))
	_, found = component.ComponentSlot("multi-one-two")
	assert.False(t, found)
}

func TestComponentSlotPrefersExactAndMostSpecificFamily(t *testing.T) {
	component := VueComponent{Slots: []VueComponentSlot{
		{NamePrefix: "column-", Line: 1},
		{NamePrefix: "column-label-", Line: 2},
		{Name: "column-label-name", Line: 3},
	}}
	slot, found := component.ComponentSlot("column-label-name")
	require.True(t, found)
	assert.Equal(t, 3, slot.Line)
	slot, found = component.ComponentSlot("column-label-price")
	require.True(t, found)
	assert.Equal(t, 2, slot.Line)
	slot, found = component.ComponentSlot("column-price")
	require.True(t, found)
	assert.Equal(t, 1, slot.Line)
}

func slotMemberNames(members []VueComponentSlotMember) []string {
	result := make([]string, 0, len(members))
	for _, member := range members {
		result = append(result, member.Name)
	}
	return result
}

func TestParseBlocksFromContent(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		expectedNames []string
		expectedLines []int // 1-based line numbers
	}{
		{
			name:          "no blocks",
			content:       "<div>Hello World</div>",
			expectedNames: nil,
			expectedLines: nil,
		},
		{
			name:          "single block",
			content:       "{% block sw_card %}<div>content</div>{% endblock %}",
			expectedNames: []string{"sw_card"},
			expectedLines: []int{1},
		},
		{
			name: "multiple blocks",
			content: `{% block sw_page %}
<div>
	{% block sw_page_header %}
	<header></header>
	{% endblock %}
	{% block sw_page_content %}
	<main></main>
	{% endblock %}
</div>
{% endblock %}`,
			expectedNames: []string{"sw_page", "sw_page_header", "sw_page_content"},
			expectedLines: []int{1, 3, 6},
		},
		{
			name: "blocks with slots",
			content: `{% block sw_card %}
<div class="sw-card">
	<slot name="header"></slot>
	{% block sw_card_content %}
	<slot></slot>
	{% endblock %}
</div>
{% endblock %}`,
			expectedNames: []string{"sw_card", "sw_card_content"},
			expectedLines: []int{1, 4},
		},
		{
			name: "duplicate blocks deduplicated",
			content: `{% block sw_card %}
<div>first</div>
{% endblock %}
{% block sw_card %}
<div>second</div>
{% endblock %}`,
			expectedNames: []string{"sw_card"},
			expectedLines: []int{1}, // Only first occurrence
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseTemplateContent(tt.content)

			// Check block count
			if tt.expectedNames == nil {
				assert.Nil(t, result.Blocks)
				return
			}

			assert.Len(t, result.Blocks, len(tt.expectedNames))

			// Check each block
			for i, block := range result.Blocks {
				assert.Equal(t, tt.expectedNames[i], block.Name, "block name mismatch at index %d", i)
				if tt.expectedLines != nil && i < len(tt.expectedLines) {
					assert.Equal(t, tt.expectedLines[i], block.Line, "block line mismatch for %s", block.Name)
				}
			}
		})
	}
}

func TestParseBlockDeprecationAndExactNameRange(t *testing.T) {
	content := `{# @deprecated tag:v6.8.0 - Use sw_modern_content instead. #}
{% block sw_legacy_content %}{% endblock %}
{% block sw_active_content %}{% endblock %}`
	result := parseTemplateContent(content)
	require.Len(t, result.Blocks, 2)
	legacy := result.Blocks[0]
	assert.Equal(t, "sw_legacy_content", legacy.Name)
	assert.Equal(
		t, "tag:v6.8.0 - Use sw_modern_content instead.",
		legacy.Deprecated,
	)
	assert.Equal(t, 2, legacy.Line)
	assert.Equal(t, AdminSourceRange{
		StartLine: 1, StartCharacter: 9,
		EndLine: 1, EndCharacter: 26,
		Declaration: true, Identifier: true,
	}, legacy.NameRange)
	assert.Empty(t, result.Blocks[1].Deprecated)
}

func TestParseBlockLexicalScopeContracts(t *testing.T) {
	content := `<sw-grid>
    <template #row="props">
        <div v-for="(item, index) in props.items">
            {% block sw_grid_row %}{{ item.name }}{% endblock %}
        </div>
        {% block sw_grid_footer %}{{ props.total }}{% endblock %}
    </template>
</sw-grid>`
	result := parseTemplateContent(content)
	require.Len(t, result.Blocks, 2)
	row := result.Blocks[0]
	assert.Equal(t, "sw_grid_row", row.Name)
	require.Len(t, row.ScopeMembers, 3)
	assert.ElementsMatch(t, []string{"props", "item", "index"}, []string{
		row.ScopeMembers[0].Name,
		row.ScopeMembers[1].Name,
		row.ScopeMembers[2].Name,
	})
	assert.True(t, row.ScopeMembers[1].NameRange.Declaration)
	assert.Positive(t, row.ScopeMembers[1].Line)
	footer := result.Blocks[1]
	require.Len(t, footer.ScopeMembers, 1)
	assert.Equal(t, "props", footer.ScopeMembers[0].Name)
}

func TestParseBlockScopeAcrossNestedTwigBlocks(t *testing.T) {
	content := `{% block outer %}
<sw-seo-url>
    <template #seo-additional="props">
        {% block inner %}
        <div :title="props.currentSalesChannelId"></div>
        {% endblock %}
    </template>
</sw-seo-url>
{% endblock %}`
	result := parseTemplateContent(content)
	require.Len(t, result.Blocks, 2)
	inner, found := func() (TwigBlock, bool) {
		for _, block := range result.Blocks {
			if block.Name == "inner" {
				return block, true
			}
		}
		return TwigBlock{}, false
	}()
	require.True(t, found)
	member, found := inner.ScopeMember("props")
	require.True(t, found)
	assert.Equal(t, "props", member.Name)
}

func TestResolveTemplatePath(t *testing.T) {
	tests := []struct {
		name           string
		definitionPath string
		templateImport string
		expected       string
	}{
		{
			name:           "relative path same dir",
			definitionPath: "/project/src/component/sw-card/index.js",
			templateImport: "./sw-card.html.twig",
			expected:       "/project/src/component/sw-card/sw-card.html.twig",
		},
		{
			name:           "relative path parent dir",
			definitionPath: "/project/src/component/sw-card/index.js",
			templateImport: "../template/sw-card.html.twig",
			expected:       "/project/src/component/template/sw-card.html.twig",
		},
		{
			name:           "filename only",
			definitionPath: "/project/src/component/sw-card/index.js",
			templateImport: "sw-card.html.twig",
			expected:       "/project/src/component/sw-card/sw-card.html.twig",
		},
		{
			name:           "empty template import",
			definitionPath: "/project/src/component/sw-card/index.js",
			templateImport: "",
			expected:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolveTemplatePath(tt.definitionPath, tt.templateImport)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseTemplateFromFileRetainsOwningPaths(t *testing.T) {
	templatePath := filepath.Join(t.TempDir(), "sw-card.html.twig")
	require.NoError(t, os.WriteFile(
		templatePath,
		[]byte(`{% block sw_card %}<slot name="header" />{% endblock %}`),
		0o644,
	))
	result, err := ParseTemplateFromFile(templatePath)
	require.NoError(t, err)
	require.Len(t, result.Slots, 1)
	require.Len(t, result.Blocks, 1)
	assert.Equal(t, templatePath, result.Slots[0].FilePath)
	assert.Equal(t, templatePath, result.Blocks[0].FilePath)
}
