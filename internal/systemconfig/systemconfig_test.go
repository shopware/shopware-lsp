package systemconfig

import (
	"os"
	"testing"

	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsSystemConfigXML(t *testing.T) {
	tests := []struct {
		name     string
		content  []byte
		expected bool
	}{
		{
			name:     "valid system config XML",
			content:  []byte(`<?xml version="1.0" encoding="UTF-8" ?><config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/master/src/Core/System/SystemConfig/Schema/config.xsd"></config>`),
			expected: true,
		},
		{
			name:     "invalid system config XML",
			content:  []byte(`<?xml version="1.0" encoding="UTF-8" ?><config></config>`),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsSystemConfigXML(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSystemConfigElementQuery(t *testing.T) {
	tests := []struct {
		name     string
		content  []byte
		expected int
	}{
		{
			name: "input-field and component",
			content: []byte(`<?xml version="1.0" encoding="UTF-8" ?>
<config>
  <card>
    <input-field type="text">
      <n>fieldName</n>
      <label>Field Label</label>
    </input-field>
    <component name="custom-component">
      <n>componentName</n>
      <label>Component Label</label>
    </component>
  </card>
</config>`),
			expected: 2,
		},
		{
			name: "only input-field",
			content: []byte(`<?xml version="1.0" encoding="UTF-8" ?>
<config>
  <card>
    <input-field type="text">
      <n>fieldName</n>
      <label>Field Label</label>
    </input-field>
  </card>
</config>`),
			expected: 1,
		},
		{
			name: "no fields",
			content: []byte(`<?xml version="1.0" encoding="UTF-8" ?>
<config>
  <card>
    <title>Test Card</title>
  </card>
</config>`),
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree := xmlparser.Parse(string(tt.content)).Tree
			nodes := xmlquery.Elements(tree.Root, "input-field", "component")
			assert.Equal(t, tt.expected, len(nodes))
		})
	}
}

func TestGetSystemConfigFieldName(t *testing.T) {
	content := []byte(`<input-field type="text">
  <n>fieldName</n>
  <label>Field Label</label>
</input-field>`)

	node := xmlparser.Parse(string(content)).Tree.Root

	name := GetSystemConfigFieldName(node, content)
	assert.Equal(t, "fieldName", name)
}

func TestGetSystemConfigFieldLabel(t *testing.T) {
	tests := []struct {
		name     string
		content  []byte
		expected string
	}{
		{
			name: "simple label",
			content: []byte(`<input-field type="text">
  <n>fieldName</n>
  <label>Field Label</label>
</input-field>`),
			expected: "Field Label",
		},
		{
			name: "label with lang attribute",
			content: []byte(`<input-field type="text">
  <n>fieldName</n>
  <label lang="de-DE">Deutsches Label</label>
  <label>English Label</label>
</input-field>`),
			expected: "English Label",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := xmlparser.Parse(string(tt.content)).Tree.Root

			label := GetSystemConfigFieldLabel(node, tt.content)
			assert.Equal(t, tt.expected, label)
		})
	}
}

func TestGetSystemConfigFieldType(t *testing.T) {
	content := []byte(`<input-field type="text">
  <n>fieldName</n>
  <label>Field Label</label>
</input-field>`)

	node := xmlparser.Parse(string(content)).Tree.Root

	fieldType := GetSystemConfigFieldType(node, content)
	assert.Equal(t, "text", fieldType)
}

func TestGetSystemConfigComponent(t *testing.T) {
	content := []byte(`<component name="custom-component">
  <n>componentName</n>
  <label>Component Label</label>
</component>`)

	node := xmlparser.Parse(string(content)).Tree.Root

	component := GetSystemConfigComponent(node, content)
	assert.Equal(t, "custom-component", component)
}

func TestParseSystemConfigField(t *testing.T) {
	tests := []struct {
		name     string
		content  []byte
		expected SystemConfigField
	}{
		{
			name: "input field",
			content: []byte(`<input-field type="text">
  <n>fieldName</n>
  <label>Field Label</label>
</input-field>`),
			expected: SystemConfigField{
				Name:     "fieldName",
				Label:    "Field Label",
				Type:     "text",
				FilePath: "test-file.xml",
				Line:     1, // Line number starts from 1
			},
		},
		{
			name: "component",
			content: []byte(`<component name="custom-component">
  <n>componentName</n>
  <label>Component Label</label>
</component>`),
			expected: SystemConfigField{
				Name:      "componentName",
				Label:     "Component Label",
				Component: "custom-component",
				FilePath:  "test-file.xml",
				Line:      1, // Line number starts from 1
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := xmlparser.Parse(string(tt.content)).Tree.Root

			field := ParseSystemConfigField(node, tt.content, "test-file.xml")
			assert.Equal(t, tt.expected, field)
		})
	}
}

func TestFindAllSystemConfigFields(t *testing.T) {
	// Use t.TempDir() for temporary files
	tempDir := t.TempDir()
	testFilePath := tempDir + "/test-config.xml"

	content := []byte(`<?xml version="1.0" encoding="UTF-8" ?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/master/src/Core/System/SystemConfig/Schema/config.xsd">
  <card>
    <input-field type="text">
      <n>textField</n>
      <label>Text Field</label>
    </input-field>
    <input-field type="bool">
      <n>boolField</n>
      <label>Bool Field</label>
    </input-field>
    <component name="custom-component">
      <n>customComponent</n>
      <label>Custom Component</label>
    </component>
  </card>
</config>`)

	err := os.WriteFile(testFilePath, content, 0644)
	require.NoError(t, err)

	fileContent, err := os.ReadFile(testFilePath)
	require.NoError(t, err)

	tree := xmlparser.Parse(string(fileContent)).Tree
	fields := FindAllSystemConfigFields(tree.Root, fileContent, testFilePath)

	// Verify the results
	assert.Equal(t, 3, len(fields))

	// Check the first field (textField)
	assert.Equal(t, "textField", fields[0].Name)
	assert.Equal(t, "Text Field", fields[0].Label)
	assert.Equal(t, "text", fields[0].Type)
	assert.Equal(t, "", fields[0].Component)

	// Check the second field (boolField)
	assert.Equal(t, "boolField", fields[1].Name)
	assert.Equal(t, "Bool Field", fields[1].Label)
	assert.Equal(t, "bool", fields[1].Type)
	assert.Equal(t, "", fields[1].Component)

	// Check the third field (customComponent)
	assert.Equal(t, "customComponent", fields[2].Name)
	assert.Equal(t, "Custom Component", fields[2].Label)
	assert.Equal(t, "", fields[2].Type)
	assert.Equal(t, "custom-component", fields[2].Component)
}

func TestRealSystemConfigFile(t *testing.T) {
	// Test with the actual example file
	content, err := os.ReadFile("testdata/common.xml")
	require.NoError(t, err)

	// Verify it's a system config file
	assert.True(t, IsSystemConfigXML(content))

	tree := xmlparser.Parse(string(content)).Tree
	fields := FindAllSystemConfigFields(tree.Root, content, "testdata/common.xml")

	// Verify we found fields
	assert.Greater(t, len(fields), 0)

	// Check some specific fields
	var cashOnDeliveryField *SystemConfigField
	var senderAddressFirstNameField *SystemConfigField

	for i := range fields {
		switch fields[i].Name {
		case "cashOnDeliveryPaymentMethodIds":
			cashOnDeliveryField = &fields[i]
		case "senderAddressFirstName":
			senderAddressFirstNameField = &fields[i]
		}
	}

	// Verify the cash on delivery field
	require.NotNil(t, cashOnDeliveryField)
	assert.Equal(t, "cashOnDeliveryPaymentMethodIds", cashOnDeliveryField.Name)
	assert.Equal(t, "Cash on delivery payment methods:", cashOnDeliveryField.Label)
	assert.Equal(t, "pw-shipping-entity-multi-select-by-id-field", cashOnDeliveryField.Component)

	// Verify the sender address first name field
	require.NotNil(t, senderAddressFirstNameField)
	assert.Equal(t, "senderAddressFirstName", senderAddressFirstNameField.Name)
	assert.Equal(t, "First name", senderAddressFirstNameField.Label)
	assert.Equal(t, "text", senderAddressFirstNameField.Type)
}
