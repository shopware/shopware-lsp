package admin

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var benchmarkPatternString string

func TestCamelToKebab(t *testing.T) {
	t.Parallel()

	for input, expected := range map[string]string{
		"":                   "",
		"label":              "label",
		"positionIdentifier": "position-identifier",
		"myPropName":         "my-prop-name",
		"ABC":                "a-b-c",
		"already-kebab":      "already-kebab",
		"RésuméValue":        "résumé-value",
	} {
		assert.Equal(t, expected, CamelToKebab(input), input)
	}
}

func BenchmarkCamelToKebab(b *testing.B) {
	b.Run("already_normalized", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkPatternString = CamelToKebab("already-kebab")
		}
	})
	b.Run("camel_case", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			benchmarkPatternString = CamelToKebab("positionIdentifier")
		}
	})
}

func TestVueDirectiveReferenceForAttribute(t *testing.T) {
	for _, test := range []struct {
		attribute string
		name      string
		found     bool
	}{
		{"v-tooltip.bottom", "tooltip", true},
		{"v-custom:argument.modifier", "custom", true},
		{"v-model.trim", "", false},
		{"v-bind:title", "", false},
		{"title", "", false},
	} {
		reference, found := VueDirectiveReferenceForAttribute(
			test.attribute, cst.TextRange{Start: 10, End: 40},
		)
		assert.Equal(t, test.found, found, test.attribute)
		assert.Equal(t, test.name, reference.Name, test.attribute)
		if found {
			assert.Equal(t, uint32(12), reference.Range.Start)
			assert.Equal(t, uint32(12+len(test.name)), reference.Range.End)
		}
	}
}

func TestNormalizeEventName(t *testing.T) {
	for _, test := range []struct {
		attribute string
		expected  string
	}{
		{"@save", "save"},
		{"@update:model-value", "update:model-value"},
		{"v-on:itemClick.stop.prevent", "item-click"},
		{":label", ""},
		{"v-bind:label", ""},
	} {
		t.Run(test.attribute, func(t *testing.T) {
			assert.Equal(t, test.expected, NormalizeEventName(test.attribute))
		})
	}
}

func TestVueComponentContractAttributeReferences(t *testing.T) {
	for _, test := range []struct {
		attribute string
		prop      string
		event     string
		start     uint32
		end       uint32
	}{
		{attribute: "label", prop: "label", start: 10, end: 15},
		{attribute: ":position-id.sync", prop: "positionId", start: 11, end: 22},
		{attribute: "v-bind:position-id.prop", prop: "positionId", start: 17, end: 28},
		{attribute: "@itemClick.stop", event: "item-click", start: 11, end: 20},
		{attribute: "v-on:update:model-value.once", event: "update:model-value", start: 15, end: 33},
	} {
		t.Run(test.attribute, func(t *testing.T) {
			rangeValue := cst.TextRange{Start: 10, End: 50}
			prop, propFound := VuePropReferenceForAttribute(
				test.attribute, rangeValue,
			)
			event, eventFound := VueEventReferenceForAttribute(
				test.attribute, rangeValue,
			)
			assert.Equal(t, test.prop != "", propFound)
			assert.Equal(t, test.event != "", eventFound)
			if propFound {
				assert.Equal(t, test.prop, prop.Name)
				assert.Equal(t, cst.TextRange{Start: test.start, End: test.end}, prop.Range)
			}
			if eventFound {
				assert.Equal(t, test.event, event.Name)
				assert.Equal(t, cst.TextRange{Start: test.start, End: test.end}, event.Range)
			}
		})
	}
}

func TestNormalizeModelArgument(t *testing.T) {
	for _, test := range []struct {
		attribute string
		argument  string
		found     bool
	}{
		{"v-model", "", true},
		{"v-model.trim", "", true},
		{"v-model:model-value", "modelValue", true},
		{"v-model:current-folder-id.lazy", "currentFolderId", true},
		{"v-model:[property]", "", false},
		{":model-value", "", false},
	} {
		argument, found := NormalizeModelArgument(test.attribute)
		assert.Equal(t, test.argument, argument, test.attribute)
		assert.Equal(t, test.found, found, test.attribute)
	}
}

func TestVueComponentModelContracts(t *testing.T) {
	component := VueComponent{
		Props: []VueComponentProp{
			{Name: "modelValue", Type: "String"},
			{Name: "checked", Type: "Boolean"},
		},
		Events: []VueComponentEvent{
			{Name: "update:modelValue", Type: "(value: string) => void"},
			{Name: "update:checked", Type: "(value: boolean) => void"},
		},
	}
	defaultModel, found := component.ComponentModel("v-model.trim")
	require.True(t, found)
	assert.Equal(t, "modelValue", defaultModel.PropName)
	assert.Equal(t, "update:model-value", defaultModel.EventName)

	checked, found := component.ComponentModel("v-model:checked")
	require.True(t, found)
	assert.Equal(t, "checked", checked.PropName)
	assert.Equal(t, "update:checked", checked.EventName)

	models := component.ComponentModels()
	require.Len(t, models, 2)
	assert.Equal(t, "v-model", models[0].AttributeName)
	assert.Equal(t, "v-model:checked", models[1].AttributeName)

	legacy := VueComponent{
		ModelProp: "selection", ModelEvent: "selection-change",
		Props:  []VueComponentProp{{Name: "selection", Type: "Array"}},
		Events: []VueComponentEvent{{Name: "selection-change"}},
	}
	legacyModel, found := legacy.ComponentModel("v-model")
	require.True(t, found)
	assert.Equal(t, "selection", legacyModel.PropName)
	assert.Equal(t, "selection-change", legacyModel.EventName)
}

func TestNormalizePropName(t *testing.T) {
	for _, test := range []struct {
		attribute string
		expected  string
	}{
		{"position-identifier", "positionIdentifier"},
		{":position-identifier.sync", "positionIdentifier"},
		{"v-bind:position-identifier.prop", "positionIdentifier"},
		{"@change", ""},
		{"#default", ""},
		{"v-if", ""},
	} {
		t.Run(test.attribute, func(t *testing.T) {
			assert.Equal(t, test.expected, NormalizePropName(test.attribute))
		})
	}
}

func TestNormalizeSlotName(t *testing.T) {
	for _, test := range []struct {
		input, expected string
	}{
		{"#iconFront", "iconFront"},
		{"v-slot", "default"},
		{"v-slot:actions", "actions"},
		{"v-slot:header.stop", "header"},
		{"#[columnName]", ""},
		{"v-slot:[columnName]", ""},
		{"@click", ""},
		{"slot", ""},
	} {
		assert.Equal(t, test.expected, NormalizeSlotName(test.input))
	}
}

func TestVueNamedModelAndSlotReferences(t *testing.T) {
	for _, test := range []struct {
		attribute string
		model     string
		slot      string
		start     uint32
		end       uint32
	}{
		{attribute: "v-model:cheked.trim", model: "cheked", start: 18, end: 24},
		{attribute: "#heder", slot: "heder", start: 11, end: 16},
		{attribute: "v-slot:heder.stop", slot: "heder", start: 17, end: 22},
		{attribute: "v-model", start: 0, end: 0},
		{attribute: "v-slot", start: 0, end: 0},
	} {
		t.Run(test.attribute, func(t *testing.T) {
			rangeValue := cst.TextRange{Start: 10, End: 50}
			model, modelFound := VueModelReferenceForAttribute(
				test.attribute, rangeValue,
			)
			slot, slotFound := VueSlotReferenceForAttribute(
				test.attribute, rangeValue,
			)
			assert.Equal(t, test.model != "", modelFound)
			assert.Equal(t, test.slot != "", slotFound)
			if modelFound {
				assert.Equal(t, test.model, model.Name)
				assert.Equal(t, cst.TextRange{Start: test.start, End: test.end}, model.Range)
			}
			if slotFound {
				assert.Equal(t, test.slot, slot.Name)
				assert.Equal(t, cst.TextRange{Start: test.start, End: test.end}, slot.Range)
			}
		})
	}
}
