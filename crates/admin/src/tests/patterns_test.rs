#![allow(unused_imports)]
use super::support::*;
use crate::*;

fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(TextSize::from(start), TextSize::from(end))
}

#[test]
fn camel_to_kebab_cases() {
    for (input, expected) in [
        ("", ""),
        ("label", "label"),
        ("positionIdentifier", "position-identifier"),
        ("myPropName", "my-prop-name"),
        ("ABC", "a-b-c"),
        ("already-kebab", "already-kebab"),
        ("RésuméValue", "résumé-value"),
    ] {
        assert_eq!(camel_to_kebab(input), expected, "{input}");
    }
}

#[test]
fn vue_directive_reference_for_attribute_cases() {
    for (attribute, name, found) in [
        ("v-tooltip.bottom", "tooltip", true),
        ("v-custom:argument.modifier", "custom", true),
        ("v-model.trim", "", false),
        ("v-bind:title", "", false),
        ("title", "", false),
    ] {
        let reference = vue_directive_reference_for_attribute(attribute, range(10, 40));
        assert_eq!(reference.is_some(), found, "{attribute}");
        let reference = reference.unwrap_or_default();
        assert_eq!(reference.name, name, "{attribute}");
        if found {
            assert_eq!(u32::from(reference.range.start()), 12);
            assert_eq!(u32::from(reference.range.end()), 12 + name.len() as u32);
        }
    }
}

#[test]
fn normalize_event_name_cases() {
    for (attribute, expected) in [
        ("@save", "save"),
        ("@update:model-value", "update:model-value"),
        ("v-on:itemClick.stop.prevent", "item-click"),
        (":label", ""),
        ("v-bind:label", ""),
    ] {
        assert_eq!(normalize_event_name(attribute), expected, "{attribute}");
    }
}

#[test]
fn vue_component_contract_attribute_references() {
    for (attribute, prop, event, start, end) in [
        ("label", "label", "", 10, 15),
        (":position-id.sync", "positionId", "", 11, 22),
        ("v-bind:position-id.prop", "positionId", "", 17, 28),
        ("@itemClick.stop", "", "item-click", 11, 20),
        (
            "v-on:update:model-value.once",
            "",
            "update:model-value",
            15,
            33,
        ),
    ] {
        let prop_reference = vue_prop_reference_for_attribute(attribute, range(10, 50));
        let event_reference = vue_event_reference_for_attribute(attribute, range(10, 50));
        assert_eq!(!prop.is_empty(), prop_reference.is_some(), "{attribute}");
        assert_eq!(!event.is_empty(), event_reference.is_some(), "{attribute}");
        if let Some(reference) = prop_reference {
            assert_eq!(reference.name, prop);
            assert_eq!(reference.range, range(start, end));
        }
        if let Some(reference) = event_reference {
            assert_eq!(reference.name, event);
            assert_eq!(reference.range, range(start, end));
        }
    }
}

#[test]
fn normalize_model_argument_cases() {
    for (attribute, argument, found) in [
        ("v-model", "", true),
        ("v-model.trim", "", true),
        ("v-model:model-value", "modelValue", true),
        ("v-model:current-folder-id.lazy", "currentFolderId", true),
        ("v-model:[property]", "", false),
        (":model-value", "", false),
    ] {
        let result = normalize_model_argument(attribute);
        assert_eq!(result.is_some(), found, "{attribute}");
        assert_eq!(result.unwrap_or_default(), argument, "{attribute}");
    }
}

#[test]
fn vue_component_model_contracts() {
    let component = VueComponent {
        props: vec![
            VueComponentProp {
                name: "modelValue".into(),
                type_: "String".into(),
                ..Default::default()
            },
            VueComponentProp {
                name: "checked".into(),
                type_: "Boolean".into(),
                ..Default::default()
            },
        ],
        events: vec![
            VueComponentEvent {
                name: "update:modelValue".into(),
                type_: "(value: string) => void".into(),
                ..Default::default()
            },
            VueComponentEvent {
                name: "update:checked".into(),
                type_: "(value: boolean) => void".into(),
                ..Default::default()
            },
        ],
        ..Default::default()
    };
    let default_model = component
        .component_model("v-model.trim")
        .expect("default model");
    assert_eq!(default_model.prop_name, "modelValue");
    assert_eq!(default_model.event_name, "update:model-value");

    let checked = component
        .component_model("v-model:checked")
        .expect("checked model");
    assert_eq!(checked.prop_name, "checked");
    assert_eq!(checked.event_name, "update:checked");

    let models = component.component_models();
    assert_eq!(models.len(), 2);
    assert_eq!(models[0].attribute_name, "v-model");
    assert_eq!(models[1].attribute_name, "v-model:checked");

    let legacy = VueComponent {
        model_prop: "selection".into(),
        model_event: "selection-change".into(),
        props: vec![VueComponentProp {
            name: "selection".into(),
            type_: "Array".into(),
            ..Default::default()
        }],
        events: vec![VueComponentEvent {
            name: "selection-change".into(),
            ..Default::default()
        }],
        ..Default::default()
    };
    let legacy_model = legacy.component_model("v-model").expect("legacy model");
    assert_eq!(legacy_model.prop_name, "selection");
    assert_eq!(legacy_model.event_name, "selection-change");
}

#[test]
fn normalize_prop_name_cases() {
    for (attribute, expected) in [
        ("position-identifier", "positionIdentifier"),
        (":position-identifier.sync", "positionIdentifier"),
        ("v-bind:position-identifier.prop", "positionIdentifier"),
        ("@change", ""),
        ("#default", ""),
        ("v-if", ""),
    ] {
        assert_eq!(normalize_prop_name(attribute), expected, "{attribute}");
    }
}

#[test]
fn normalize_slot_name_cases() {
    for (input, expected) in [
        ("#iconFront", "iconFront"),
        ("v-slot", "default"),
        ("v-slot:actions", "actions"),
        ("v-slot:header.stop", "header"),
        ("#[columnName]", ""),
        ("v-slot:[columnName]", ""),
        ("@click", ""),
        ("slot", ""),
    ] {
        assert_eq!(normalize_slot_name(input), expected, "{input}");
    }
}

#[test]
fn vue_named_model_and_slot_references() {
    for (attribute, model, slot, start, end) in [
        ("v-model:cheked.trim", "cheked", "", 18, 24),
        ("#heder", "", "heder", 11, 16),
        ("v-slot:heder.stop", "", "heder", 17, 22),
        ("v-model", "", "", 0, 0),
        ("v-slot", "", "", 0, 0),
    ] {
        let model_reference = vue_model_reference_for_attribute(attribute, range(10, 50));
        let slot_reference = vue_slot_reference_for_attribute(attribute, range(10, 50));
        assert_eq!(!model.is_empty(), model_reference.is_some(), "{attribute}");
        assert_eq!(!slot.is_empty(), slot_reference.is_some(), "{attribute}");
        if let Some(reference) = model_reference {
            assert_eq!(reference.name, model);
            assert_eq!(reference.range, range(start, end));
        }
        if let Some(reference) = slot_reference {
            assert_eq!(reference.name, slot);
            assert_eq!(reference.range, range(start, end));
        }
    }
}
