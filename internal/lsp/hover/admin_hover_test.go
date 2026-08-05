package hover

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/admin"
	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseJS(t *testing.T, code string) *jssyntax.Node {
	t.Helper()
	return javascriptparser.Parse(code).Tree.Root
}

func findNodeAtPosition(root *jssyntax.Node, line, col uint) *jssyntax.Node {
	offset := jssyntax.NewLineIndex(root.Text()).Offset(uint32(line), uint32(col))
	return root.NodeAtOffset(offset)
}

func TestAdminHoverIsInComponentCall(t *testing.T) {
	provider := &AdminHoverProvider{}

	tests := []struct {
		name     string
		code     string
		line     uint
		col      uint
		expected bool
	}{
		{
			name:     "Component.register first arg",
			code:     `Component.register('my-component', () => import('./index'));`,
			line:     0,
			col:      22,
			expected: true,
		},
		{
			name:     "Component.extend first arg",
			code:     `Component.extend('my-component', 'parent', () => import('./index'));`,
			line:     0,
			col:      22,
			expected: true,
		},
		{
			name:     "Component.extend second arg (parent)",
			code:     `Component.extend('my-component', 'parent', () => import('./index'));`,
			line:     0,
			col:      36,
			expected: true,
		},
		{
			name:     "Shopware.Component.register",
			code:     `Shopware.Component.register('my-component', () => import('./index'));`,
			line:     0,
			col:      32,
			expected: true,
		},
		{
			name:     "Shopware.Component.extend",
			code:     `Shopware.Component.extend('my-component', 'parent', () => import('./index'));`,
			line:     0,
			col:      45,
			expected: true,
		},
		{
			name:     "not in component call",
			code:     `const name = 'my-component';`,
			line:     0,
			col:      16,
			expected: false,
		},
		{
			name:     "different function call",
			code:     `someFunc('my-component');`,
			line:     0,
			col:      12,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := parseJS(t, tt.code)
			node := findNodeAtPosition(root, tt.line, tt.col)
			if node == nil {
				t.Fatalf("Could not find node at position %d:%d", tt.line, tt.col)
			}

			result := provider.isInComponentCall(node)
			assert.Equal(t, tt.expected, result, "Node kind: %s, text: %s", node.Kind(), node.Text())
		})
	}
}

func TestAdminHoverExtractComponentName(t *testing.T) {
	provider := &AdminHoverProvider{}

	tests := []struct {
		name     string
		code     string
		line     uint
		col      uint
		expected string
	}{
		{
			name:     "single quoted string",
			code:     `Component.register('my-component', () => {});`,
			line:     0,
			col:      22,
			expected: "my-component",
		},
		{
			name:     "double quoted string",
			code:     `Component.register("my-component", () => {});`,
			line:     0,
			col:      22,
			expected: "my-component",
		},
		{
			name:     "parent component name",
			code:     `Component.extend('child', 'sw-base-component', () => {});`,
			line:     0,
			col:      30,
			expected: "sw-base-component",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := parseJS(t, tt.code)
			node := findNodeAtPosition(root, tt.line, tt.col)
			if node == nil {
				t.Fatalf("Could not find node at position %d:%d", tt.line, tt.col)
			}

			result := provider.extractComponentName(node)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAdminTwigBlockHoverShowsParentAndDeprecation(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(root, "Resources/app/administration/src")
	parentTemplate := filepath.Join(adminRoot, "sw-card.html.twig")
	childTemplate := filepath.Join(adminRoot, "acme-card.html.twig")
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-card", Kind: admin.ComponentRegister,
		FilePath:     filepath.Join(adminRoot, "sw-card.js"),
		TemplatePath: parentTemplate,
		Blocks: []admin.TwigBlock{{
			Name: "sw_card_legacy", FilePath: parentTemplate, Line: 4,
			Deprecated: "Use sw_card_content instead.",
		}},
	}))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "acme-card", Kind: admin.ComponentExtend,
		TargetComponent: "sw-card", ExtendsComponent: "sw-card",
		FilePath:     filepath.Join(adminRoot, "acme-card.js"),
		TemplatePath: childTemplate,
	}))

	source := `{% block sw_card_legacy %}{% endblock %}`
	document := lsp.NewTextDocument(uriutil.FileURI(childTemplate), source, 1)
	offset := uint32(strings.Index(source, "sw_card_legacy") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	hover, err := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "Administration Twig block")
	assert.Contains(t, hover.Contents.Value, "Parent component:** `sw-card`")
	assert.Contains(t, hover.Contents.Value, "Use sw_card_content instead")
	require.NotNil(t, hover.Range)
	assert.Equal(t, 9, hover.Range.Start.Character)
	assert.Equal(t, 23, hover.Range.End.Character)
}

func TestBuildHoverContent(t *testing.T) {
	provider := &AdminHoverProvider{}

	tests := []struct {
		name       string
		components []admin.VueComponent
		contains   []string
	}{
		{
			name: "component with props",
			components: []admin.VueComponent{
				{
					Name: "sw-button",
					Props: []admin.VueComponentProp{
						{Name: "label", Type: "String", Required: true},
						{Name: "disabled", Type: "Boolean", Default: "false"},
					},
				},
			},
			contains: []string{
				"## `sw-button`",
				"### Props",
				"`label`: **String** *(required)*",
				"`disabled`: **Boolean** = `false`",
			},
		},
		{
			name: "component with emits",
			components: []admin.VueComponent{
				{
					Name:  "sw-input",
					Emits: []string{"input", "change", "blur"},
				},
			},
			contains: []string{
				"## `sw-input`",
				"### Events",
				"`input`",
				"`change`",
				"`blur`",
			},
		},
		{
			name: "component with methods",
			components: []admin.VueComponent{
				{
					Name:    "sw-modal",
					Methods: []string{"open", "close", "toggle"},
				},
			},
			contains: []string{
				"## `sw-modal`",
				"### Methods",
				"`open()`",
				"`close()`",
				"`toggle()`",
			},
		},
		{
			name: "component with computed",
			components: []admin.VueComponent{
				{
					Name:     "sw-list",
					Computed: []string{"filteredItems", "totalCount"},
				},
			},
			contains: []string{
				"## `sw-list`",
				"### Computed",
				"`filteredItems`",
				"`totalCount`",
			},
		},
		{
			name: "component that extends another",
			components: []admin.VueComponent{
				{
					Name:             "sw-custom-button",
					ExtendsComponent: "sw-button",
				},
			},
			contains: []string{
				"## `sw-custom-button`",
				"**Extends**: `sw-button`",
			},
		},
		{
			name: "component with definition path",
			components: []admin.VueComponent{
				{
					Name:           "sw-card",
					DefinitionPath: "/path/to/sw-card/index.js",
				},
			},
			contains: []string{
				"## `sw-card`",
				"*Defined in*: `/path/to/sw-card/index.js`",
			},
		},
		{
			name: "full component",
			components: []admin.VueComponent{
				{
					Name:             "sw-data-grid",
					ExtendsComponent: "sw-base-grid",
					Props: []admin.VueComponentProp{
						{Name: "columns", Type: "Array", Required: true},
						{Name: "dataSource", Type: "Array"},
					},
					Emits:          []string{"selection-change", "page-change"},
					Methods:        []string{"refresh", "selectAll"},
					Computed:       []string{"selectedItems"},
					DefinitionPath: "/path/to/sw-data-grid/index.js",
				},
			},
			contains: []string{
				"## `sw-data-grid`",
				"**Extends**: `sw-base-grid`",
				"### Props",
				"`columns`: **Array** *(required)*",
				"`dataSource`: **Array**",
				"### Events",
				"`selection-change`",
				"`page-change`",
				"### Methods",
				"`refresh()`",
				"`selectAll()`",
				"### Computed",
				"`selectedItems`",
				"*Defined in*: `/path/to/sw-data-grid/index.js`",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := provider.buildHoverContent(tt.components)

			for _, expected := range tt.contains {
				assert.Contains(t, result, expected)
			}
		})
	}
}

func TestAdminHoverContentExposesDeprecations(t *testing.T) {
	provider := &AdminHoverProvider{}
	prop := admin.VueComponentProp{
		Name: "oldValue", Type: "String",
		Deprecated: "tag:v6.8.0 - Use modernValue instead.",
	}
	content := provider.buildHoverContent([]admin.VueComponent{{
		Name: "sw-legacy", Deprecated: "Use mt-modern instead.",
		Props: []admin.VueComponentProp{prop},
	}})
	assert.Contains(t, content, "**Deprecated:** Use mt-modern instead.")
	assert.Contains(t, content, "`oldValue`: **String** *(deprecated)*")

	propContent := adminPropMarkdown("sw-legacy", prop)
	assert.Contains(t, propContent, "**prop** `oldValue`: `String`")
	assert.Contains(t, propContent, "**Deprecated:** tag:v6.8.0")
}

func TestAdminComponentMemberHoverExposesDeprecation(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	templatePath := filepath.Join(
		root, "Resources/app/administration/src/sw-card.html.twig",
	)
	definitionPath := filepath.Join(
		root, "Resources/app/administration/src/sw-card.ts",
	)
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: definitionPath,
		DefinitionPath: definitionPath, TemplatePath: templatePath,
		Methods: []string{"legacySave"},
		Members: []admin.VueComponentMember{{
			Name: "legacySave", Kind: admin.ComponentMemberMethod,
			Type: "() => void", FilePath: definitionPath, Line: 12,
			Deprecated: "Use save instead.",
		}},
	}))
	provider := NewAdminHoverProvider(root, index)

	thisHover, err := provider.thisMemberHover(
		uriutil.FileURI(definitionPath), "legacySave",
	)
	require.NoError(t, err)
	require.NotNil(t, thisHover)
	assert.Contains(t, thisHover.Contents.Value, "**Deprecated:** Use save instead.")

	source := `{{ legacySave() }}`
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	offset := uint32(strings.Index(source, "legacySave") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	hover, err := provider.GetHover(context.Background(), &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document: document, DocumentContent: document.Text,
			DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
			Root:  document.SyntaxTree.Root,
			Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
			Token: document.SyntaxTree.Root.TokenAtOffset(offset),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "**Deprecated:** Use save instead.")
}

func TestAdminTemplateRuntimeIdentifierHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	templatePath := filepath.Join(
		root, "Resources/app/administration/src/sw-card.html.twig",
	)
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-card", TemplatePath: templatePath,
		FilePath: filepath.Join(filepath.Dir(templatePath), "index.ts"),
		Blocks: []admin.TwigBlock{{
			Name: "sw_card_row",
			ScopeMembers: []admin.TwigBlockScopeMember{{
				Name: "item", Type: "Product",
				FilePath: templatePath, Line: 4,
			}},
		}},
	}))
	provider := NewAdminHoverProvider(root, index)
	for _, test := range []struct {
		name, source, needle, expected string
	}{
		{
			name: "Vue instance member", source: `{{ $t('key') }}`,
			needle: "$t", expected: "Administration Vue component instance",
		},
		{
			name: "JavaScript global", source: `{{ Object.keys(values) }}`,
			needle: "Object", expected: "JavaScript template runtime",
		},
		{
			name:   "Twig block scope",
			source: `{% block sw_card_row %}{{ item.name }}{% endblock %}`,
			needle: "item", expected: "Twig block scope",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(templatePath), test.source, 1,
			)
			offset := uint32(strings.Index(test.source, test.needle) + 1)
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.HoverParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			hover, hoverErr := provider.GetHover(
				context.Background(),
				&lsp.HoverRequest{
					HoverParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree,
						LineIndex:    document.LineIndex,
						Root:         document.SyntaxTree.Root,
						Node: document.SyntaxTree.Root.NodeAtOffset(
							offset,
						),
						Token: document.SyntaxTree.Root.TokenAtOffset(
							offset,
						),
					},
				},
			)
			require.NoError(t, hoverErr)
			require.NotNil(t, hover)
			assert.Contains(t, hover.Contents.Value, test.expected)
		})
	}
}

func TestAdminTemplatePropHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	templatePath := filepath.Join(
		root,
		"src/Resources/app/administration/src/component/sw-card/sw-card.html.twig",
	)
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name:         "sw-card",
		FilePath:     filepath.Join(filepath.Dir(templatePath), "index.js"),
		TemplatePath: templatePath,
		Props: []admin.VueComponentProp{{
			Name: "title", Type: "String", Required: true, Default: "'Card'",
			Documentation: "Heading shown above the card content.",
			AllowedValues: []string{"Card", "Panel"}, AllowedValuesComplete: true,
		}},
	}))
	source := "{{ title }}"
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	offset := uint32(strings.Index(source, "title") + 1)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	result, err := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "**prop** `title`: `String`")
	assert.Contains(t, result.Contents.Value, "Heading shown above the card content.")
	assert.Contains(t, result.Contents.Value, "Required")
	assert.Contains(t, result.Contents.Value, "Default")
	assert.Contains(t, result.Contents.Value, "Allowed values: `Card`, `Panel`")

	attributeSource := `<div :title="title"></div>`
	attributeDocument := lsp.NewTextDocument(
		uriutil.FileURI(templatePath), attributeSource, 2,
	)
	attributeOffset := uint32(strings.LastIndex(attributeSource, "title") + 1)
	line, character := attributeDocument.LineIndex.PositionUTF16(attributeOffset)
	attributeParams := &protocol.HoverParams{}
	attributeParams.TextDocument.URI = attributeDocument.URI
	attributeParams.Position.Line = int(line)
	attributeParams.Position.Character = int(character)
	attributeHover, err := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: attributeParams,
			SyntaxContext: lsp.SyntaxContext{
				Document: attributeDocument, DocumentContent: attributeDocument.Text,
				DocumentTree: attributeDocument.SyntaxTree,
				LineIndex:    attributeDocument.LineIndex,
				Root:         attributeDocument.SyntaxTree.Root,
				Node: attributeDocument.SyntaxTree.Root.NodeAtOffset(
					attributeOffset,
				),
				Token: attributeDocument.SyntaxTree.Root.TokenAtOffset(
					attributeOffset,
				),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, attributeHover)
	assert.Contains(t, attributeHover.Contents.Value, "**prop** `title`: `String`")
}

func TestAdminScopedSlotBindingHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	declarationPath := filepath.Join(adminRoot, "meteor/MtButton.d.ts")
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "mt-button", FilePath: declarationPath,
		Slots: []admin.VueComponentSlot{{
			Name: "iconFront", FilePath: declarationPath, Line: 10,
			PayloadType: "{ size: number }",
			Members: []admin.VueComponentSlotMember{{
				Name: "size", Type: "number", FilePath: declarationPath, Line: 12,
			}},
		}},
	}))
	source := `<mt-button><template #iconFront="{ size: iconSize }"><span :title="iconSize"></span>{{ iconSize }}</template></mt-button>`
	provider := NewAdminHoverProvider(root, index)
	for _, test := range []struct {
		name   string
		needle string
	}{
		{name: "binding alias", needle: "size: icon"},
		{name: "vue attribute expression", needle: `:title="icon`},
		{name: "twig interpolation", needle: "{{ icon"},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(adminRoot, "view.html.twig")),
				source,
				1,
			)
			offset := uint32(strings.Index(source, test.needle) + len(test.needle))
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.HoverParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			hover, hoverErr := provider.GetHover(
				context.Background(),
				&lsp.HoverRequest{
					HoverParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root: document.SyntaxTree.Root,
						Node: document.SyntaxTree.Root.NodeAtOffset(
							offset - 1,
						),
						Token: document.SyntaxTree.Root.TokenAtOffset(
							offset - 1,
						),
					},
				},
			)
			require.NoError(t, hoverErr)
			require.NotNil(t, hover)
			assert.Contains(t, hover.Contents.Value, "**slot prop** `iconSize`: `number`")
			assert.Contains(t, hover.Contents.Value, "mt-button.iconFront")
			assert.Contains(t, hover.Contents.Value, "Contract member: `size`")
			assert.Contains(t, hover.Contents.Value, "MtButton.d.ts:12")
		})
	}
}

func TestAdminScopedSlotHoverUsesDynamicComponentPayloadIntersection(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-card-a", FilePath: filepath.Join(adminRoot, "a/index.ts"),
			Slots: []admin.VueComponentSlot{{
				Name: "row", Members: []admin.VueComponentSlotMember{{
					Name: "item", Type: "Product",
					FilePath: filepath.Join(adminRoot, "a/card.twig"), Line: 4,
				}},
			}},
		},
		{
			Name: "sw-card-b", FilePath: filepath.Join(adminRoot, "b/index.ts"),
			Slots: []admin.VueComponentSlot{{
				Name: "row", Members: []admin.VueComponentSlotMember{{
					Name: "item", Type: "Category",
					FilePath: filepath.Join(adminRoot, "b/card.twig"), Line: 8,
				}},
			}},
		},
	} {
		require.NoError(t, index.SaveComponent(component))
	}
	provider := NewAdminHoverProvider(root, index)
	for _, test := range []struct {
		name, source, needle string
	}{
		{
			name:   "destructured local",
			source: `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="{ item: row }">{{ row }}</template></component>`,
			needle: "{{ row",
		},
		{
			name:   "whole object member",
			source: `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #row="props">{{ props.item }}</template></component>`,
			needle: "item",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")),
				test.source, 1,
			)
			offset := uint32(strings.Index(test.source, test.needle) + len(test.needle))
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.HoverParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			hover, hoverErr := provider.GetHover(
				context.Background(),
				&lsp.HoverRequest{
					HoverParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root:  document.SyntaxTree.Root,
						Node:  document.SyntaxTree.Root.NodeAtOffset(offset - 1),
						Token: document.SyntaxTree.Root.TokenAtOffset(offset - 1),
					},
				},
			)
			require.NoError(t, hoverErr)
			require.NotNil(t, hover)
			assert.Contains(t, hover.Contents.Value, "Product | Category")
			assert.Contains(t, hover.Contents.Value, "`sw-card-a`, `sw-card-b`")
			assert.Contains(t, hover.Contents.Value, "a/card.twig:4")
			assert.Contains(t, hover.Contents.Value, "b/card.twig:8")
		})
	}
}

func TestAdminDynamicSlotFamilyHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(root, "src/Resources/app/administration/src")
	templatePath := filepath.Join(adminRoot, "grid/sw-grid.html.twig")
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-grid", TemplatePath: templatePath,
		Slots: []admin.VueComponentSlot{{
			NamePrefix: "column-", FilePath: templatePath, Line: 27,
			Members: []admin.VueComponentSlotMember{{
				Name: "item", Type: "Entity", FilePath: templatePath, Line: 28,
			}},
		}},
	}))
	source := `<sw-grid><template #column-name="{ item: row }">{{ row }}</template></sw-grid>`
	provider := NewAdminHoverProvider(root, index)
	for _, test := range []struct {
		name, needle string
		contains     []string
	}{
		{
			name: "family slot", needle: "column-name",
			contains: []string{
				"**slot** `column-name`", "Dynamic slot family: `column-*`",
				"`item`: `Entity`", "sw-grid.html.twig:27",
			},
		},
		{
			name: "family local", needle: "{{ row",
			contains: []string{
				"**slot prop** `row`: `Entity`", "Dynamic slot family: `column-*`",
				"Contract member: `item`", "sw-grid.html.twig:28",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")),
				source, 1,
			)
			offset := uint32(strings.Index(source, test.needle) + len(test.needle))
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.HoverParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			hover, hoverErr := provider.GetHover(
				context.Background(),
				&lsp.HoverRequest{
					HoverParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root: document.SyntaxTree.Root,
						Node: document.SyntaxTree.Root.NodeAtOffset(
							offset - 1,
						),
						Token: document.SyntaxTree.Root.TokenAtOffset(
							offset - 1,
						),
					},
				},
			)
			require.NoError(t, hoverErr)
			require.NotNil(t, hover)
			for _, expected := range test.contains {
				assert.Contains(t, hover.Contents.Value, expected)
			}
		})
	}
}

func TestAdminSlotHoverResolvesDynamicComponentOwner(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	for _, component := range []admin.VueComponent{
		{
			Name: "sw-card-a", FilePath: filepath.Join(adminRoot, "a/index.ts"),
			Slots: []admin.VueComponentSlot{{
				Name: "header", FilePath: filepath.Join(adminRoot, "a/card.html.twig"), Line: 4,
			}},
		},
		{
			Name: "sw-card-b", FilePath: filepath.Join(adminRoot, "b/index.ts"),
			Slots: []admin.VueComponentSlot{{
				Name: "header", FilePath: filepath.Join(adminRoot, "b/card.html.twig"), Line: 8,
			}},
		},
	} {
		require.NoError(t, index.SaveComponent(component))
	}
	source := `<component :is="active ? 'sw-card-a' : 'sw-card-b'"><template #header /></component>`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.html.twig")), source, 1,
	)
	offset := uint32(strings.Index(source, "header") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	hover, err := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "`sw-card-a`")
	assert.Contains(t, hover.Contents.Value, "`sw-card-b`")
}

func TestAdminThisMemberHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	definitionPath := filepath.Join(
		root,
		"src/Resources/app/administration/src/component/sw-card/index.js",
	)
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: definitionPath, DefinitionPath: definitionPath,
		Members: []admin.VueComponentMember{{
			Name: "cardClasses", Kind: admin.ComponentMemberComputed,
			FilePath: definitionPath, Line: 18,
		}},
		Computed: []string{"cardClasses"},
	}))
	source := `export default { methods: { render() { return this.cardClasses; } } };`
	document := lsp.NewTextDocument(uriutil.FileURI(definitionPath), source, 1)
	offset := uint32(strings.Index(source, "cardClasses") + 1)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	result, err := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "**computed** `cardClasses`")
	assert.Contains(t, result.Contents.Value, "sw-card")
}

func TestAdminRuntimeRegistryHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	registrationPath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/main.ts",
	)
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`
Shopware.Application.addServiceProvider('acl', factory);
Shopware.Store.register('profile', {
    actions: { load() {} },
});
Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product',
    roles: { viewer: { privileges: ['product:read'] } },
});
`),
	)))
	provider := NewAdminHoverProvider(root, index)
	documentPath := filepath.Join(filepath.Dir(registrationPath), "consumer.ts")

	tests := []struct {
		name     string
		source   string
		needle   string
		contains []string
	}{
		{
			"service", `Shopware.Service('acl')`, "acl",
			[]string{"Administration service", "`acl`", "main.ts:2"},
		},
		{
			"store member", `Shopware.Store.get('profile').load()`, "load",
			[]string{
				"**action** `load`: `() => unknown`", "store `profile`",
				"main.ts:4",
			},
		},
		{
			"store unregister", `Shopware.Store.unregister('profile')`, "profile",
			[]string{"Administration store", "`profile`", "main.ts:3"},
		},
		{
			"privilege role", `acl.can('product.viewer')`, "product.viewer",
			[]string{"Administration privilege role", "`product.viewer`", "main.ts:8"},
		},
		{
			"permission", `const item = { privilege: 'product:read' }`, "product:read",
			[]string{"Administration permission", "`product:read`", "product.viewer", "main.ts:8"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(documentPath), test.source, 1,
			)
			offset := uint32(strings.LastIndex(test.source, test.needle) + 1)
			params := &protocol.HoverParams{}
			params.TextDocument.URI = document.URI
			result, err := provider.GetHover(
				context.Background(),
				&lsp.HoverRequest{
					HoverParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root:  document.SyntaxTree.Root,
						Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
						Token: document.SyntaxTree.Root.TokenAtOffset(offset),
					},
				},
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			for _, value := range test.contains {
				assert.Contains(t, result.Contents.Value, value)
			}
		})
	}
}

func TestAdminApplicationContainerHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	globalPath := filepath.Join(adminRoot, "global.types.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		globalPath,
		[]byte(`export interface SubContainer<T extends string> { $list(): string[]; }
declare global {
    interface FactoryContainer extends SubContainer<'factory'> {
        locale: LocaleFactory;
    }
    interface ServiceContainer extends SubContainer<'service'> {
        acl: AclService;
    }
}`),
	)))
	servicePath := filepath.Join(adminRoot, "services.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		servicePath,
		[]byte(`Shopware.Application.addServiceProvider('acl', factory);`),
	)))
	provider := NewAdminHoverProvider(root, index)
	documentPath := filepath.Join(adminRoot, "consumer.ts")
	hover := func(source, needle string) *protocol.Hover {
		t.Helper()
		document := lsp.NewTextDocument(
			uriutil.FileURI(documentPath), source, 1,
		)
		offset := strings.LastIndex(source, needle)
		require.NotEqual(t, -1, offset)
		params := &protocol.HoverParams{}
		params.TextDocument.URI = document.URI
		result, hoverErr := provider.GetHover(
			context.Background(),
			&lsp.HoverRequest{
				HoverParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root:  document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(uint32(offset + 1)),
					Token: document.SyntaxTree.Root.TokenAtOffset(uint32(offset + 1)),
				},
			},
		)
		require.NoError(t, hoverErr)
		return result
	}

	container := hover(`Application.getContainer('factory')`, "factory")
	require.NotNil(t, container)
	assert.Contains(t, container.Contents.Value, "Application container")
	assert.Contains(t, container.Contents.Value, "FactoryContainer")

	factory := hover(`Application.getContainer('factory').locale`, "locale")
	require.NotNil(t, factory)
	assert.Contains(t, factory.Contents.Value, "factory` container member")
	assert.Contains(t, factory.Contents.Value, "LocaleFactory")
	assert.Contains(t, factory.Contents.Value, "global.types.ts:4")

	service := hover(`function run() {
    const services = Application.getContainer('service');
    return services.acl;
}`, "acl")
	require.NotNil(t, service)
	assert.Contains(t, service.Contents.Value, "Administration service")
	assert.Contains(t, service.Contents.Value, "Container type: `AclService`")
}

func TestAdminShopwareContextHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	contextPath := filepath.Join(adminRoot, "app/composables/use-context.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		contextPath,
		[]byte(`export interface ContextState {
    app: { environment: null | string };
    api: { languageId: null | string };
}`),
	)))
	documentPath := filepath.Join(adminRoot, "module/example/index.ts")
	source := `const languageId = Shopware.Context.api.languageId;`
	document := lsp.NewTextDocument(uriutil.FileURI(documentPath), source, 1)
	offset := uint32(strings.LastIndex(source, "languageId") + 1)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	hover, hoverErr := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.NoError(t, hoverErr)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "Shopware.Context.api.languageId")
	assert.Contains(t, hover.Contents.Value, "null | string")
	assert.Contains(t, hover.Contents.Value, "use-context.ts:3")
}

func TestAdminShopwareUtilsHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	utilPath := filepath.Join(adminRoot, "core/service/util.service.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		utilPath,
		[]byte(`export const format = { date };
export default { format };
function date(value: string): string { return value; }`),
	)))
	documentPath := filepath.Join(adminRoot, "module/example/index.ts")
	source := `const { date: formatDate } = Shopware.Utils.format;
formatDate('2026-01-01');`
	document := lsp.NewTextDocument(uriutil.FileURI(documentPath), source, 1)
	offset := uint32(strings.LastIndex(source, "formatDate") + 1)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	hover, hoverErr := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.NoError(t, hoverErr)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "Shopware.Utils.format.date")
	assert.Contains(t, hover.Contents.Value, "(value: string) => string")
	assert.Contains(t, hover.Contents.Value, "util.service.ts:1")
}

func TestAdminShopwareEventBusEventHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	for path, source := range map[string]string{
		filepath.Join(adminRoot, "core/service/util.service.ts"): `
import EventBus from './utils/eventBus.utils';
export default { EventBus };
`,
		filepath.Join(adminRoot, "core/service/utils/eventBus.utils.ts"): `
interface Events extends Record<string | symbol, unknown> {
    'language-change': { languageId: string };
}
const emitter = mitt<Events>();
export default emitter;
`,
	} {
		require.NoError(t, index.Index(indexerpkg.NewParsedFile(
			path, []byte(source),
		)))
	}
	documentPath := filepath.Join(adminRoot, "module/example/index.ts")
	source := `const bus = Shopware.Utils.EventBus;
bus.emit('language-change', { languageId });`
	document := lsp.NewTextDocument(uriutil.FileURI(documentPath), source, 1)
	offset := uint32(strings.LastIndex(source, "language-change") + 1)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	hover, hoverErr := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.NoError(t, hoverErr)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "Shopware EventBus event")
	assert.Contains(t, hover.Contents.Value, "language-change")
	assert.Contains(t, hover.Contents.Value, "{ languageId: string }")
	assert.Contains(t, hover.Contents.Value, "eventBus.utils.ts:3")
}

func TestAdminComponentMixinAndModuleRegistryHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "main.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`
Shopware.Component.register('sw-card', { props: { title: String } });
Shopware.Mixin.register('listing', { methods: { load() {} } });
Shopware.Module.register('sw-product', {
    type: 'core',
    title: 'sw-product.general.mainMenuItemGeneral',
    routes: { index: { path: 'index', component: 'sw-card' } },
});`),
	)))
	provider := NewAdminHoverProvider(root, index)
	for _, test := range []struct {
		name, source, needle string
		contains             []string
	}{
		{
			"component registry", `Shopware.Component.getComponentRegistry().get('sw-card')`,
			"sw-card", []string{"`sw-card`", "### Props", "`title`"},
		},
		{
			"mixin", `Shopware.Mixin.getByName('listing')`,
			"listing", []string{"Administration mixin", "`listing`", "Indexed members: 1"},
		},
		{
			"module registry", `Module.getModuleRegistry().get('sw-product')`,
			"sw-product", []string{"Administration module", "`sw-product`", "Type: `core`", "Indexed routes: 1"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")),
				test.source,
				1,
			)
			offset := uint32(strings.Index(test.source, test.needle) + 1)
			params := &protocol.HoverParams{}
			params.TextDocument.URI = document.URI
			hover, hoverErr := provider.GetHover(
				context.Background(),
				&lsp.HoverRequest{
					HoverParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root:  document.SyntaxTree.Root,
						Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
						Token: document.SyntaxTree.Root.TokenAtOffset(offset),
					},
				},
			)
			require.NoError(t, hoverErr)
			require.NotNil(t, hover)
			for _, expected := range test.contains {
				assert.Contains(t, hover.Contents.Value, expected)
			}
		})
	}
}

func TestAdminDirectiveHoverFromJavaScriptAndTwig(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "app/directive/tooltip.directive.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`Shopware.Directive.register('tooltip', {});`),
	)))
	provider := NewAdminHoverProvider(root, index)
	for _, test := range []struct {
		source, needle, extension string
	}{
		{`Shopware.Directive.getByName('tooltip')`, "tooltip", ".ts"},
		{`<div v-tooltip.bottom="message"></div>`, "tooltip", ".html.twig"},
	} {
		document := lsp.NewTextDocument(
			uriutil.FileURI(filepath.Join(adminRoot, "consumer"+test.extension)),
			test.source,
			1,
		)
		offset := uint32(strings.Index(test.source, test.needle) + 1)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.HoverParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		hover, hoverErr := provider.GetHover(
			context.Background(),
			&lsp.HoverRequest{
				HoverParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root:  document.SyntaxTree.Root,
					Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
					Token: document.SyntaxTree.Root.TokenAtOffset(offset),
				},
			},
		)
		require.NoError(t, hoverErr)
		require.NotNil(t, hover)
		assert.Contains(t, hover.Contents.Value, "Administration Vue directive")
		assert.Contains(t, hover.Contents.Value, "`v-tooltip`")
		assert.Contains(t, hover.Contents.Value, "tooltip.directive.ts:1")
	}
}

func TestAdminFilterHoverShowsCallableContract(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "app/filter/currency.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`Shopware.Filter.register('currency', (value: number): string => String(value));`),
	)))
	source := `Shopware.Filter.getByName('currency')`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")), source, 1,
	)
	offset := uint32(strings.Index(source, "currency") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	hover, hoverErr := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.NoError(t, hoverErr)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "Administration filter")
	assert.Contains(t, hover.Contents.Value, "(value: number) => string")
}

func TestAdminCMSRegistryHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "cms/hero.ts")
	componentPath := filepath.Join(adminRoot, "component/sw-cms-el-hero/index.ts")
	registrationSource := `Shopware.Service('cmsService').registerCmsElement({
    name: 'hero', label: 'cms.hero', component: 'sw-cms-el-hero',
    configComponent: 'sw-cms-el-config-hero',
    previewComponent: 'sw-cms-el-preview-hero',
});`
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(registrationSource),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		componentPath,
		[]byte(`Shopware.Component.register('sw-cms-el-hero', { props: ['config'] });`),
	)))
	source := `cmsService.getCmsElementConfigByName('hero')`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "consumer.ts")), source, 1,
	)
	offset := uint32(strings.Index(source, "hero") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	hover, hoverErr := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree,
				LineIndex:    document.LineIndex,
				Root:         document.SyntaxTree.Root,
				Node:         document.SyntaxTree.Root.NodeAtOffset(offset),
				Token:        document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.NoError(t, hoverErr)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "Shopware CMS element")
	assert.Contains(t, hover.Contents.Value, "sw-cms-el-hero")
	assert.Contains(t, hover.Contents.Value, "sw-cms-el-config-hero")
	assert.Contains(t, hover.Contents.Value, "sw-cms-el-preview-hero")

	componentDocument := lsp.NewTextDocument(
		uriutil.FileURI(registrationPath), registrationSource, 1,
	)
	componentOffset := uint32(strings.Index(registrationSource, "sw-cms-el-hero") + 1)
	componentLine, componentCharacter := componentDocument.LineIndex.PositionUTF16(
		componentOffset,
	)
	componentParams := &protocol.HoverParams{}
	componentParams.TextDocument.URI = componentDocument.URI
	componentParams.Position.Line = int(componentLine)
	componentParams.Position.Character = int(componentCharacter)
	componentHover, hoverErr := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: componentParams,
			SyntaxContext: lsp.SyntaxContext{
				Document: componentDocument, DocumentContent: componentDocument.Text,
				DocumentTree: componentDocument.SyntaxTree,
				LineIndex:    componentDocument.LineIndex,
				Root:         componentDocument.SyntaxTree.Root,
				Node: componentDocument.SyntaxTree.Root.NodeAtOffset(
					componentOffset,
				),
				Token: componentDocument.SyntaxTree.Root.TokenAtOffset(
					componentOffset,
				),
			},
		},
	)
	require.NoError(t, hoverErr)
	require.NotNil(t, componentHover)
	assert.Contains(t, componentHover.Contents.Value, "sw-cms-el-hero")
	assert.Contains(t, componentHover.Contents.Value, "config")
}

func TestAdminDirectiveHoverIdentifiesTemplateLocalDeclaration(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	localPath := filepath.Join(adminRoot, "component/sw-owner/index.ts")
	templatePath := filepath.Join(
		adminRoot, "component/sw-owner/sw-owner.html.twig",
	)
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-owner", FilePath: localPath, DefinitionPath: localPath,
		TemplatePath: templatePath,
		LocalDirectives: []admin.VueLocalDirective{{
			Name: "hide", FilePath: localPath, Line: 7,
		}},
	}))
	source := `<div v-hide="hidden"></div>`
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
	offset := uint32(strings.Index(source, "hide") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	hover, hoverErr := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root:  document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.NoError(t, hoverErr)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "Component-local")
	assert.Contains(t, hover.Contents.Value, "sw-owner/index.ts:7")
}

func TestAdminMarkupEventAndSlotHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Resources/app/administration/src",
	)
	definitionPath := filepath.Join(adminRoot, "component/sw-card/index.js")
	templatePath := filepath.Join(
		adminRoot, "component/sw-card/sw-card.html.twig",
	)
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: definitionPath,
		DefinitionPath: definitionPath, TemplatePath: templatePath,
		Props: []admin.VueComponentProp{{
			Name: "modelValue", Type: "String",
			FilePath: definitionPath, Line: 5,
		}},
		Events: []admin.VueComponentEvent{{
			Name: "update:modelValue", Type: "(value: string) => void",
			Documentation: "Updates the public model value.",
			FilePath:      definitionPath, Line: 9,
		}},
		Slots: []admin.VueComponentSlot{{
			Name: "header", FilePath: templatePath, Line: 4,
		}},
	}))
	provider := NewAdminHoverProvider(root, index)
	for _, test := range []struct {
		name, source, needle string
		contains             []string
	}{
		{
			"event", `<sw-card @update:model-value.stop="onUpdate" />`,
			"update:model-value",
			[]string{
				"**event** `update:model-value`", "value: string",
				"Updates the public model value.", "index.js:9",
			},
		},
		{
			"model", `<sw-card v-model.trim="title" />`,
			"v-model",
			[]string{
				"**model binding** `v-model.trim`: `string`",
				"modelValue", "update:model-value", "index.js:5", "index.js:9",
			},
		},
		{
			"slot", `<sw-card><template #header></template></sw-card>`,
			"header",
			[]string{"**slot** `header`", "sw-card", "sw-card.html.twig:4"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(templatePath), test.source, 1,
			)
			offset := uint32(strings.Index(test.source, test.needle) + 1)
			params := &protocol.HoverParams{}
			params.TextDocument.URI = document.URI
			hover, hoverErr := provider.GetHover(
				context.Background(),
				&lsp.HoverRequest{
					HoverParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
						Root:  document.SyntaxTree.Root,
						Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
						Token: document.SyntaxTree.Root.TokenAtOffset(offset),
					},
				},
			)
			require.NoError(t, hoverErr)
			require.NotNil(t, hover)
			for _, expected := range test.contains {
				assert.Contains(t, hover.Contents.Value, expected)
			}
		})
	}
}

func TestAdminVueForAndEventLexicalHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "mt-switch",
		Events: []admin.VueComponentEvent{
			{
				Name: "update:modelValue", Type: "(value: boolean) => any",
				FilePath: filepath.Join(root, "meteor/MtSwitch.d.ts"), Line: 22,
			},
			{
				Name: "media-upload-add", Type: "UploadTask[]",
				FilePath: filepath.Join(root, "sw-upload-listener/index.js"), Line: 37,
			},
		},
	}))
	provider := NewAdminHoverProvider(root, index)
	for _, test := range []struct {
		name, source, marker string
		contains             []string
	}{
		{
			name:     "v-for local",
			source:   `<div v-for="item in products">{{ item.name }}</div>`,
			marker:   "item.name",
			contains: []string{"**v-for local** `item`", "Iterates `products`"},
		},
		{
			name:   "typed event payload",
			source: `<mt-switch @update:model-value="save($event)" />`,
			marker: "$event",
			contains: []string{
				"**event payload** `$event`: `boolean`",
				"mt-switch.update:model-value", "MtSwitch.d.ts:22",
			},
		},
		{
			name:   "direct annotation payload",
			source: `<mt-switch @media-upload-add="consume($event)" />`,
			marker: "$event",
			contains: []string{
				"**event payload** `$event`: `UploadTask[]`",
				"mt-switch.media-upload-add", "index.js:37",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(filepath.Join(
					root, "Resources/app/administration/view.html.twig",
				)),
				test.source,
				1,
			)
			offset := uint32(strings.Index(test.source, test.marker) + 1)
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.HoverParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			hover, hoverErr := provider.GetHover(
				context.Background(),
				&lsp.HoverRequest{
					HoverParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree,
						LineIndex:    document.LineIndex,
						Root:         document.SyntaxTree.Root,
						Node:         document.SyntaxTree.Root.NodeAtOffset(offset),
						Token:        document.SyntaxTree.Root.TokenAtOffset(offset),
					},
				},
			)
			require.NoError(t, hoverErr)
			require.NotNil(t, hover)
			for _, expected := range test.contains {
				assert.Contains(t, hover.Contents.Value, expected)
			}
		})
	}
}

func TestAdminVueExpressionMemberHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	templatePath := filepath.Join(
		root, "Resources/app/administration/src/view.html.twig",
	)
	definitionPath := filepath.Join(
		root, "Resources/app/administration/src/index.ts",
	)
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		definitionPath,
		[]byte(`
interface Manufacturer { name: string; }
interface Product { name: string; manufacturer: Manufacturer; getManufacturer(): Manufacturer; }
`),
	)))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-view", TemplatePath: templatePath,
		FilePath: definitionPath, DefinitionPath: definitionPath,
		Props: []admin.VueComponentProp{
			{
				Name: "products", Type: "Array as PropType<Product[]>",
				FilePath: definitionPath,
			},
			{
				Name: "productsById", Type: "Record<string, Product>",
				FilePath: definitionPath,
			},
			{
				Name: "selectedProduct", Type: "Product",
				FilePath: definitionPath,
			},
		},
	}))
	declarationPath := filepath.Join(root, "meteor/SwInheritWrapper.d.ts")
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-inherit-wrapper", FilePath: declarationPath,
		Slots: []admin.VueComponentSlot{{
			Name: "content", FilePath: declarationPath,
			Members: []admin.VueComponentSlotMember{{
				Name: "currentValue", Type: "string",
				FilePath: declarationPath, Line: 31,
			}},
		}},
	}))
	provider := NewAdminHoverProvider(root, index)
	typedSource := `<div v-for="product in products">{{ product.name }}</div>`
	typedDocument := lsp.NewTextDocument(
		uriutil.FileURI(templatePath), typedSource, 1,
	)
	typedOffset := uint32(strings.Index(typedSource, "product.name") + 1)
	typedBinding, typedErr := index.ResolveTwigVueBinding(
		typedDocument.SyntaxTree.Root, typedDocument.Text, typedOffset,
		adminHoverTemplatePath(typedDocument.URI),
	)
	require.NoError(t, typedErr)
	require.NotNil(t, typedBinding)
	assert.Equal(t, "Product", typedBinding.Type)

	for _, test := range []struct {
		name, source, marker string
		contains             []string
	}{
		{
			name:   "v-for observed member",
			source: `<div v-for="product in products">{{ product.name }}</div>`,
			marker: "name",
			contains: []string{
				"**property** `product.name`", "Binding type: `Product`",
				"Iterates `products`",
			},
		},
		{
			name:   "v-for nested typed member",
			source: `<div v-for="product in products">{{ product.manufacturer.name }}</div>`,
			marker: "name }}",
			contains: []string{
				"**property** `product.manufacturer.name`: `string`",
				"Receiver type: `Manufacturer`",
			},
		},
		{
			name:   "mapped primitive member",
			source: `<div v-for="productName in products?.map((product) => product.name) ?? []">{{ productName.length }}</div>`,
			marker: "length",
			contains: []string{
				"**property** `productName.length`: `number`",
				"Binding type: `string`",
			},
		},
		{
			name:   "mapped projection member",
			source: `<div v-for="card in products.map((product) => ({ label: product.name }))">{{ card.label }}</div>`,
			marker: "label }}",
			contains: []string{
				"**property** `card.label`: `string`",
				"Binding type: `{ label: string }`",
			},
		},
		{
			name:   "record value member",
			source: `<div v-for="(product, productId, index) in productsById">{{ product.name }}</div>`,
			marker: "name }}",
			contains: []string{
				"**property** `product.name`: `string`",
				"Binding type: `Product`",
			},
		},
		{
			name:   "record key member",
			source: `<div v-for="(product, productId, index) in productsById">{{ productId.length }}</div>`,
			marker: "length",
			contains: []string{
				"**property** `productId.length`: `number`",
				"Binding type: `string`",
			},
		},
		{
			name:   "record numeric index member",
			source: `<div v-for="(product, productId, index) in productsById">{{ index.toFixed() }}</div>`,
			marker: "toFixed",
			contains: []string{
				"**property** `index.toFixed()`: `(digits?: number) => string`",
				"Binding type: `number`",
			},
		},
		{
			name:   "Object.values record member",
			source: `<div v-for="product in Object.values(productsById)">{{ product.name }}</div>`,
			marker: "name }}",
			contains: []string{
				"**property** `product.name`: `string`",
				"Binding type: `Product`",
			},
		},
		{
			name:   "Object.keys result member",
			source: `<div v-for="productId in Object.keys(productsById)">{{ productId.length }}</div>`,
			marker: "length",
			contains: []string{
				"**property** `productId.length`: `number`",
				"Binding type: `string`",
			},
		},
		{
			name:   "static literal iterable member",
			source: `<div v-for="label in ['primary', 'fallback']">{{ label.length }}</div>`,
			marker: "length",
			contains: []string{
				"**property** `label.length`: `number`",
				"Binding type: `string`",
			},
		},
		{
			name:   "component nested typed member",
			source: `<div :title="selectedProduct.manufacturer.name"></div>`,
			marker: "name\"",
			contains: []string{
				"**property** `selectedProduct.manufacturer.name`: `string`",
				"Receiver type: `Manufacturer`", "**prop** `selectedProduct`",
				"component `sw-view`",
			},
		},
		{
			name:   "component indexed array member",
			source: `<div :title="products[0].manufacturer.name"></div>`,
			marker: "name\"",
			contains: []string{
				"**property** `products[0].manufacturer.name`: `string`",
				"Receiver type: `Manufacturer`", "component `sw-view`",
			},
		},
		{
			name:   "component v-for indexed array member",
			source: `<div v-for="(product, index) in products">{{ products[index].manufacturer.name }}</div>`,
			marker: "name }}",
			contains: []string{
				"**property** `products[index].manufacturer.name`: `string`",
				"Receiver type: `Manufacturer`", "component `sw-view`",
			},
		},
		{
			name:   "component indexed Record member",
			source: `<div :title="productsById[selectedProduct.name].manufacturer.name"></div>`,
			marker: "name\"",
			contains: []string{
				"**property** `productsById[selectedProduct.name].manufacturer.name`: `string`",
				"Receiver type: `Manufacturer`", "component `sw-view`",
			},
		},
		{
			name:   "component called typed member",
			source: `<div :title="selectedProduct.getManufacturer().name"></div>`,
			marker: "name\"",
			contains: []string{
				"**property** `selectedProduct.getManufacturer().name`: `string`",
				"Receiver type: `Manufacturer`", "component `sw-view`",
			},
		},
		{
			name:   "whole slot object member",
			source: `<sw-inherit-wrapper><template #content="props"><span :title="props.currentValue"></span></template></sw-inherit-wrapper>`,
			marker: "currentValue",
			contains: []string{
				"**slot prop** `props.currentValue`: `string`",
				"sw-inherit-wrapper.content",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				uriutil.FileURI(templatePath), test.source, 1,
			)
			offset := uint32(strings.Index(test.source, test.marker) + 1)
			line, character := document.LineIndex.PositionUTF16(offset)
			params := &protocol.HoverParams{}
			params.TextDocument.URI = document.URI
			params.Position.Line = int(line)
			params.Position.Character = int(character)
			hover, hoverErr := provider.GetHover(
				context.Background(),
				&lsp.HoverRequest{
					HoverParams: params,
					SyntaxContext: lsp.SyntaxContext{
						Document: document, DocumentContent: document.Text,
						DocumentTree: document.SyntaxTree,
						LineIndex:    document.LineIndex,
						Root:         document.SyntaxTree.Root,
						Node: document.SyntaxTree.Root.NodeAtOffset(
							offset,
						),
						Token: document.SyntaxTree.Root.TokenAtOffset(
							offset,
						),
					},
				},
			)
			require.NoError(t, hoverErr)
			require.NotNil(t, hover)
			for _, expected := range test.contains {
				assert.Contains(t, hover.Contents.Value, expected)
			}
		})
	}
}

func TestAdminVueExpressionMemberHoverUsesUnsavedTypeImport(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	componentDir := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/component/sw-draft-card",
	)
	componentPath := filepath.Join(componentDir, "index.vue")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		componentPath,
		[]byte(`<template>{{ title }}</template>
<script setup lang="ts">
const { title } = defineProps<{ title: string }>();
</script>`),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(componentDir, "index.ts"),
		[]byte(`Shopware.Component.register('sw-draft-card', () => import('./index.vue'));`),
	)))
	draftTypesPath := filepath.Join(componentDir, "draft-contracts.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		draftTypesPath,
		[]byte(`export interface DraftProfile { city: string; zip: string }
export interface DraftProps { profile: DraftProfile }`),
	)))
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(componentDir, "DraftPanel.vue"),
		[]byte(`<template><slot /></template>
<script setup lang="ts">
defineProps<{ label: string; count?: number }>();
</script>`),
	)))

	liveSource := `<template>{{ profile.city }}</template>
<script setup lang="ts">
import type { DraftProps } from './draft-contracts';
const { profile } = defineProps<DraftProps>();
</script>`
	document := lsp.NewTextDocument(uriutil.FileURI(componentPath), liveSource, 2)
	offset := uint32(strings.Index(liveSource, "profile.city") + len("profile."))
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	hover, err := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, Language: document.SyntaxLanguage,
				DocumentContent: document.Text, DocumentTree: document.SyntaxTree,
				LineIndex: document.LineIndex, Root: document.SyntaxTree.Root,
				Node:  document.SyntaxTree.Root.NodeAtOffset(offset),
				Token: document.SyntaxTree.Root.TokenAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "`profile.city`: `string`")
	assert.Contains(t, hover.Contents.Value, "Receiver type: `DraftProfile`")

	liveLocalSource := `<template><DraftPanel :label="title" /></template>
<script setup lang="ts">
import DraftPanel from './DraftPanel.vue';
const title = 'Draft';
</script>`
	localDocument := lsp.NewTextDocument(
		uriutil.FileURI(componentPath), liveLocalSource, 3,
	)
	localOffset := uint32(strings.Index(liveLocalSource, ":label") + 2)
	localLine, localCharacter := localDocument.LineIndex.PositionUTF16(localOffset)
	localParams := &protocol.HoverParams{}
	localParams.TextDocument.URI = localDocument.URI
	localParams.Position.Line = int(localLine)
	localParams.Position.Character = int(localCharacter)
	localHover, err := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: localParams,
			SyntaxContext: lsp.SyntaxContext{
				Document: localDocument, Language: localDocument.SyntaxLanguage,
				DocumentContent: localDocument.Text,
				DocumentTree:    localDocument.SyntaxTree,
				LineIndex:       localDocument.LineIndex, Root: localDocument.SyntaxTree.Root,
				Node:  localDocument.SyntaxTree.Root.NodeAtOffset(localOffset),
				Token: localDocument.SyntaxTree.Root.TokenAtOffset(localOffset),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, localHover)
	assert.Contains(t, localHover.Contents.Value, "**prop** `label`: `string`")
	assert.Contains(t, localHover.Contents.Value, "`draft-panel`")
}

func TestAdminTwigPrivilegeHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "privileges.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`Shopware.Service('privileges').addPrivilegeMappingEntry({
    key: 'product', roles: { viewer: { privileges: ['product:read'] } },
});`),
	)))
	source := `<mt-button :disabled="acl.can('product.viewer')" />`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "view.html.twig")), source, 1,
	)
	offset := uint32(strings.Index(source, "product.viewer") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	hover, err := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root: document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "Administration privilege role")
	assert.Contains(t, hover.Contents.Value, "`product.viewer`")
}

func TestAdminBuiltinPrivilegeHoverHasNoFakeSource(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	hover, err := NewAdminHoverProvider(root, index).privilegeHover(
		admin.AdminPrivilegeAdministrator,
	)
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "Built-in Administration privilege")
	assert.Contains(t, hover.Contents.Value, "`admin`")
	assert.NotContains(t, hover.Contents.Value, "Defined in")
}

func TestAdminTwigModuleRouteHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "module/sw-product/index.ts")
	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(`Shopware.Module.register('sw-product', {
    routes: {
        detail: { path: 'detail', component: 'sw-product-detail' },
    },
});`),
	)))
	source := `<router-link :to="{ name: 'sw.product.detail' }" />`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(adminRoot, "view.html.twig")), source, 1,
	)
	offset := uint32(strings.Index(source, "sw.product.detail") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	hover, err := NewAdminHoverProvider(root, index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root: document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "Administration module route")
	assert.Contains(t, hover.Contents.Value, "`sw.product.detail`")
	assert.Contains(t, hover.Contents.Value, "`sw-product-detail`")
}

func TestAdminDynamicComponentSelectorAndPropHover(t *testing.T) {
	root := t.TempDir()
	index, err := admin.NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	templatePath := filepath.Join(adminRoot, "consumer.html.twig")
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-card", FilePath: filepath.Join(adminRoot, "sw-card/index.ts"),
		Props: []admin.VueComponentProp{{Name: "title", Type: "String"}},
	}))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-panel", FilePath: filepath.Join(adminRoot, "sw-panel/index.ts"),
		Props: []admin.VueComponentProp{{Name: "title", Type: "String"}},
	}))
	require.NoError(t, index.SaveComponent(admin.VueComponent{
		Name: "sw-host", FilePath: filepath.Join(adminRoot, "sw-host/index.ts"),
		TemplatePath: templatePath,
		Members: []admin.VueComponentMember{{
			Name: "dynamicCard", Kind: admin.ComponentMemberComputed,
			ReturnExpressions: []string{"'sw-card'", "'sw-panel'"},
			ReturnsComplete:   true,
		}},
	}))
	source := `<component :is="'sw-card'" :title="title" />`
	provider := NewAdminHoverProvider(root, index)
	for _, test := range []struct {
		needle, expected string
	}{
		{"sw-card", "Vue dynamic component selector"},
		{":title", "**prop** `title`"},
	} {
		document := lsp.NewTextDocument(uriutil.FileURI(templatePath), source, 1)
		offset := uint32(strings.Index(source, test.needle) + 1)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.HoverParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		hover, hoverErr := provider.GetHover(
			context.Background(),
			&lsp.HoverRequest{
				HoverParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document: document, DocumentContent: document.Text,
					DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
					Root: document.SyntaxTree.Root,
					Node: document.SyntaxTree.Root.NodeAtOffset(offset),
				},
			},
		)
		require.NoError(t, hoverErr)
		require.NotNil(t, hover)
		assert.Contains(t, hover.Contents.Value, test.expected)
	}

	unionSource := `<component :is="active ? 'sw-card' : 'sw-panel'" :title="title" />`
	document := lsp.NewTextDocument(uriutil.FileURI(templatePath), unionSource, 1)
	offset := uint32(strings.Index(unionSource, ":title") + 1)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	hover, hoverErr := provider.GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root: document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, hoverErr)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "Component: `sw-card`")
	assert.Contains(t, hover.Contents.Value, "Component: `sw-panel`")

	inferredSource := `<component :is="dynamicCard" v-bind="{ title: heading }" />`
	document = lsp.NewTextDocument(uriutil.FileURI(templatePath), inferredSource, 1)
	offset = uint32(strings.Index(inferredSource, "title") + 1)
	line, character = document.LineIndex.PositionUTF16(offset)
	params = &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	hover, hoverErr = provider.GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root: document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, hoverErr)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "Component: `sw-card`")
	assert.Contains(t, hover.Contents.Value, "Component: `sw-panel`")

	require.NoError(t, index.Index(indexerpkg.NewParsedFile(
		filepath.Join(adminRoot, "module/sw-host/routes.js"),
		[]byte(`Shopware.Module.register('sw-host', {
    routes: {
        index: {
            component: 'sw-host',
            children: {
                card: { component: 'sw-card' },
                panel: { component: 'sw-panel' },
            },
        },
    },
});`),
	)))
	routeSource := `<router-view v-slot="{ Component: view }">` +
		`<component :is="view" :title="heading" /></router-view>`
	document = lsp.NewTextDocument(uriutil.FileURI(templatePath), routeSource, 1)
	offset = uint32(strings.Index(routeSource, ":title") + 1)
	line, character = document.LineIndex.PositionUTF16(offset)
	params = &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	hover, hoverErr = provider.GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document: document, DocumentContent: document.Text,
				DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
				Root: document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(offset),
			},
		},
	)
	require.NoError(t, hoverErr)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "Component: `sw-card`")
	assert.Contains(t, hover.Contents.Value, "Component: `sw-panel`")
}
