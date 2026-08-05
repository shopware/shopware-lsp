package admin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMeteorVolarDeclaration(t *testing.T) {
	filePath := "/project/Resources/app/administration/node_modules/" +
		"@shopware-ag/meteor-component-library/dist/esm/MtButton.d.ts"
	component := parseMeteorDeclaration(filePath, `
type __VLS_Props = {
    /** Braces in docs must not close the type: { "example": true }. */
    title: string;
    size?: "small" | "large" | legacySize;
    /** Updates the public model value. */
    "onUpdate:modelValue"?: ((value: string) => any) | undefined;
};
type legacySize = "legacy";
declare function __VLS_template(): {
    slots: Readonly<{
        default(): void;
        "icon-front"?(props: { size: number }): any;
    }>;
};`)
	require.NotNil(t, component)
	assert.Equal(t, "mt-button", component.Name)
	assert.Equal(t, filePath, component.DefinitionPath)
	props := make(map[string]VueComponentProp)
	for _, prop := range component.Props {
		props[prop.Name] = prop
	}
	require.Contains(t, props, "title")
	assert.True(t, props["title"].Required)
	assert.Equal(t, "string", props["title"].Type)
	require.Contains(t, props, "size")
	assert.False(t, props["size"].Required)
	assert.Equal(t, `"small" | "large" | legacySize`, props["size"].Type)
	values, complete := VuePropAllowedValues(props["size"])
	assert.True(t, complete)
	assert.Equal(t, []string{"small", "large", "legacy"}, values)
	assert.NotContains(t, props, "onUpdate:modelValue")
	assert.Contains(t, component.Emits, "update:model-value")
	event, found := component.ComponentEvent("update:model-value")
	require.True(t, found)
	assert.Equal(t, filePath, event.FilePath)
	assert.Equal(t, 7, event.Line)
	assert.Contains(t, event.Type, "value: string")
	assert.Equal(t, "Updates the public model value.", event.Documentation)
	assert.True(t, event.NameRange.Declaration)
	assert.False(t, event.NameRange.Identifier)
	assert.Greater(t, event.NameRange.EndCharacter, event.NameRange.StartCharacter)
	assert.Equal(t, []string{"default", "icon-front"}, slotNames(component.Slots))
	assert.Equal(t, filePath, component.Slots[0].FilePath)
	assert.True(t, component.Slots[0].NameRange.Declaration)
	assert.True(t, component.Slots[0].NameRange.Identifier)
	assert.Empty(t, component.Slots[0].PayloadType)
	assert.True(t, component.Slots[1].NameRange.Declaration)
	assert.False(t, component.Slots[1].NameRange.Identifier)
	require.Len(t, component.Slots[1].Members, 1)
	assert.Equal(t, "{ size: number }", component.Slots[1].PayloadType)
	assert.Equal(t, "size", component.Slots[1].Members[0].Name)
	assert.Equal(t, "number", component.Slots[1].Members[0].Type)
	assert.Equal(t, filePath, component.Slots[1].Members[0].FilePath)
	assert.Equal(t, 13, component.Slots[1].Members[0].Line)
	assert.True(t, component.Slots[1].Members[0].NameRange.Declaration)
	assert.True(t, component.Slots[1].Members[0].NameRange.Identifier)
}

func TestVuePropAllowedValuesRetainPartialLiteralUnions(t *testing.T) {
	values, complete := VuePropAllowedValues(VueComponentProp{
		Name: "variant",
		Type: `"primary" | "secondary" | LegacyVariant | undefined`,
	})
	assert.False(t, complete)
	assert.Equal(t, []string{"primary", "secondary"}, values)
	values, complete = VuePropAllowedValues(VueComponentProp{
		Name: "size", Type: "`${string}px`",
	})
	assert.False(t, complete)
	assert.Empty(t, values)
	values, complete = VuePropAllowedValues(VueComponentProp{
		Name: "tone", Type: `"info" | "critical"`,
		AllowedValues: []string{"info"},
	})
	assert.True(t, complete)
	assert.Equal(t, []string{"info", "critical"}, values)
}

func TestParseMeteorObjectStyleSlotPayload(t *testing.T) {
	filePath := "/project/Resources/app/administration/node_modules/" +
		"@shopware-ag/meteor-component-library/dist/esm/MtCard.d.ts"
	component := parseMeteorDeclaration(filePath, `
declare function __VLS_template(): {
    slots: Readonly<{
        default: null;
        header: {
            title: string;
            actions?: readonly string[];
        };
    }>;
};`)
	require.NotNil(t, component)
	require.Len(t, component.Slots, 2)
	header := component.Slots[1]
	assert.Equal(t, "header", header.Name)
	assert.Contains(t, header.PayloadType, "title: string")
	require.Len(t, header.Members, 2)
	assert.Equal(t, "title", header.Members[0].Name)
	assert.Equal(t, "string", header.Members[0].Type)
	assert.Equal(t, 6, header.Members[0].Line)
	assert.Equal(t, "actions", header.Members[1].Name)
	assert.Equal(t, "readonly string[]", header.Members[1].Type)
	assert.Equal(t, 7, header.Members[1].Line)
}

func TestParseMeteorExtractPropTypesDeclaration(t *testing.T) {
	filePath := "/project/Resources/app/administration/node_modules/" +
		"@shopware-ag/meteor-component-library/dist/esm/MtTextField.d.ts"
	component := parseMeteorDeclaration(filePath, `
declare function __VLS_template(): {
    slots: {
        'header-left'?(_: {}): any;
        default?(_: {}): any;
    };
};
declare const component: DefineComponent<ExtractPropTypes<{
    /** { "code": 500 } */
    modelValue: {
        type: PropType<string | number>;
        required: false;
        default: string;
    };
    active: {
        type: BooleanConstructor;
        required: true;
    };
}>>;
type Public = {
    "onUpdate:modelValue"?: ((...args: any[]) => any) | undefined;
};`)
	require.NotNil(t, component)
	props := make(map[string]VueComponentProp)
	for _, prop := range component.Props {
		props[prop.Name] = prop
	}
	require.Contains(t, props, "modelValue")
	assert.Equal(t, "string | number", props["modelValue"].Type)
	assert.False(t, props["modelValue"].Required)
	require.Contains(t, props, "active")
	assert.Equal(t, "Boolean", props["active"].Type)
	assert.True(t, props["active"].Required)
	assert.Equal(t, []string{"header-left", "default"}, slotNames(component.Slots))
	assert.Contains(t, component.Emits, "update:model-value")
}

func TestParseMeteorRuntimePropCompatibility(t *testing.T) {
	tests := []struct {
		fileName string
		propName string
		source   string
		wantType string
	}{
		{
			fileName: "MtLink.d.ts",
			propName: "to",
			source:   `type __VLS_Props = { to?: string; };`,
			wantType: "string | object",
		},
		{
			fileName: "MtIcon.d.ts",
			propName: "size",
			source:   `type __VLS_Props = { name: string; size?: string; };`,
			wantType: "string | number",
		},
	}
	for _, test := range tests {
		t.Run(test.fileName, func(t *testing.T) {
			filePath := filepath.Join(
				"/project/Resources/app/administration/node_modules",
				"@shopware-ag/meteor-component-library/dist/esm",
				test.fileName,
			)
			component := parseMeteorDeclaration(filePath, test.source)
			require.NotNil(t, component)
			prop, found := component.ComponentProp(test.propName)
			require.True(t, found)
			assert.Equal(t, test.wantType, prop.Type)
		})
	}
}

func TestAdminIndexerDiscoversMeteorDeclarationsOnly(t *testing.T) {
	projectRoot := t.TempDir()
	cacheRoot := t.TempDir()
	declarationPath := filepath.Join(
		projectRoot,
		"src/Administration/Resources/app/administration/node_modules",
		"@shopware-ag/meteor-component-library/dist/esm/MtButton.d.ts",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(declarationPath), 0o755))
	require.NoError(t, os.WriteFile(declarationPath, []byte(`
type __VLS_Props = { disabled?: boolean; variant?: "primary" | "secondary"; };
`), 0o644))
	unrelatedPath := filepath.Join(
		projectRoot, "node_modules/unrelated/MtIgnored.d.ts",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(unrelatedPath), 0o755))
	require.NoError(t, os.WriteFile(unrelatedPath, []byte(`
type __VLS_Props = { ignored?: boolean; };
`), 0o644))

	idx, err := NewAdminComponentIndexer(filepath.Join(cacheRoot, "admin"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	scanner, err := indexerpkg.NewFileScanner(
		projectRoot, filepath.Join(cacheRoot, "scanner.db"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scanner.Close()) })
	scanner.AddIndexer(idx)
	require.NoError(t, scanner.IndexAll(context.Background()))

	component, err := idx.GetEffectiveComponent("mt-button")
	require.NoError(t, err)
	require.NotNil(t, component)
	assert.ElementsMatch(t, []string{"disabled", "variant"}, adminPropNamesForTest(component.Props))
	ignored, err := idx.GetEffectiveComponent("mt-ignored")
	require.NoError(t, err)
	assert.Nil(t, ignored)
}

func slotNames(slots []VueComponentSlot) []string {
	result := make([]string, 0, len(slots))
	for _, slot := range slots {
		result = append(result, slot.Name)
	}
	return result
}

func adminPropNamesForTest(props []VueComponentProp) []string {
	result := make([]string, 0, len(props))
	for _, prop := range props {
		result = append(result, prop.Name)
	}
	return result
}
