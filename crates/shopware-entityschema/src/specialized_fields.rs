//! Native Shopware storage-aware Field subclasses (port of Go
//! `specialized_fields.go`).

use serde::{Deserialize, Serialize};
use swlsp_parser_php::query as phpquery;
use swlsp_php_project::{Version, parse_version_constraint};
use swlsp_syntax::SyntaxNode;

use crate::import::flags::{
    ImportedFlagSet, ImportedModifierSet, normalize_imported_php_expression, with_field_modifiers,
};
use crate::import::{Resolver, imported_int_argument, imported_string_argument};
use crate::model::*;

pub(crate) const CONSTRUCTOR_STORAGE_PROPERTY: &str = "storage-property";
pub(crate) const CONSTRUCTOR_FIXED: &str = "fixed";

#[derive(Clone, Copy, Default)]
pub(crate) struct Descriptor {
    kind: &'static str,
    mode: &'static str,
    default_storage: &'static str,
    default_property: &'static str,
    entity_type: &'static str,
    template_entity_type: &'static str,
    boolean_getter: bool,
    entity_trait: &'static str,
    manage_entity: bool,
    implicit_computed: bool,
    target_definition: &'static str,
    target_entity: &'static str,
    target_name: &'static str,
    max_length: i64,
    max_length_argument: bool,
    minimum_additional: i64,
    minimum_version: Version,
}

const fn json(template: &'static str) -> Descriptor {
    Descriptor {
        kind: FIELD_JSON,
        mode: CONSTRUCTOR_STORAGE_PROPERTY,
        default_storage: "",
        default_property: "",
        entity_type: "",
        template_entity_type: template,
        boolean_getter: false,
        entity_trait: "",
        manage_entity: false,
        implicit_computed: false,
        target_definition: "",
        target_entity: "",
        target_name: "",
        max_length: 0,
        max_length_argument: false,
        minimum_additional: 0,
        minimum_version: Version::new(0, 0, 0),
    }
}

const fn managed(kind: &'static str, entity_type: &'static str) -> Descriptor {
    let mut descriptor = json("");
    descriptor.kind = kind;
    descriptor.entity_type = entity_type;
    descriptor.manage_entity = true;
    descriptor
}

const USER_DEFINITION: &str = "Shopware\\Core\\System\\User\\UserDefinition";
const USER_ENTITY: &str = "Shopware\\Core\\System\\User\\UserEntity";

/// Sorted by short class name.
pub(crate) fn descriptors() -> &'static [(&'static str, Descriptor)] {
    static DESCRIPTORS: std::sync::LazyLock<Vec<(&'static str, Descriptor)>> =
        std::sync::LazyLock::new(|| {
            let mut result = vec![
                (
                    "CustomFields",
                    Descriptor {
                        default_storage: "custom_fields",
                        default_property: "customFields",
                        entity_trait: "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityCustomFieldsTrait",
                        ..json("array")
                    },
                ),
                (
                    "CalculatedPriceField",
                    json("Shopware\\Core\\Checkout\\Cart\\Price\\Struct\\CalculatedPrice"),
                ),
                (
                    "CashRoundingConfigField",
                    Descriptor {
                        minimum_version: Version::new(6, 4, 0),
                        ..json(
                            "Shopware\\Core\\Framework\\DataAbstractionLayer\\Pricing\\CashRoundingConfig",
                        )
                    },
                ),
                (
                    "PriceField",
                    json(
                        "Shopware\\Core\\Framework\\DataAbstractionLayer\\Pricing\\PriceCollection",
                    ),
                ),
                (
                    "TaxFreeConfigField",
                    Descriptor {
                        minimum_version: Version::new(6, 4, 0),
                        ..json("Shopware\\Core\\Framework\\DataAbstractionLayer\\TaxFreeConfig")
                    },
                ),
                ("VersionDataPayloadField", json("array")),
                (
                    "VariantListingConfigField",
                    json(
                        "Shopware\\Core\\Content\\Product\\DataAbstractionLayer\\VariantListingConfig",
                    ),
                ),
                ("SlotConfigField", json("array")),
                (
                    "PriceDefinitionField",
                    json("Shopware\\Core\\Checkout\\Cart\\Price\\Struct\\PriceDefinitionInterface"),
                ),
                ("FlowTemplateConfigField", json("array")),
                (
                    "CheapestPriceField",
                    json(
                        "Shopware\\Core\\Content\\Product\\DataAbstractionLayer\\CheapestPrice\\CheapestPrice|Shopware\\Core\\Content\\Product\\DataAbstractionLayer\\CheapestPrice\\CheapestPriceContainer",
                    ),
                ),
                (
                    "CartPriceField",
                    json("Shopware\\Core\\Checkout\\Cart\\Price\\Struct\\CartPrice"),
                ),
                (
                    "BreadcrumbField",
                    Descriptor {
                        default_storage: "breadcrumb",
                        default_property: "breadcrumb",
                        minimum_version: Version::new(6, 4, 17),
                        ..json("array")
                    },
                ),
                (
                    "TreeBreadcrumbField",
                    Descriptor {
                        default_storage: "breadcrumb",
                        default_property: "breadcrumb",
                        ..json("array")
                    },
                ),
                ("ConfigJsonField", json("array|bool|float|int|string")),
                (
                    "MeasurementUnitsField",
                    Descriptor {
                        kind: FIELD_OBJECT,
                        minimum_version: Version::new(6, 7, 1),
                        ..json("Shopware\\Core\\Content\\MeasurementSystem\\MeasurementUnits")
                    },
                ),
                (
                    "ManyToManyIdField",
                    Descriptor {
                        kind: FIELD_LIST,
                        minimum_additional: 1,
                        ..json("array")
                    },
                ),
                (
                    "NumberRangeField",
                    Descriptor {
                        max_length: 64,
                        max_length_argument: true,
                        ..managed(FIELD_STRING, "string")
                    },
                ),
                ("PasswordField", managed(FIELD_STRING, "string")),
                ("EmailField", managed(FIELD_STRING, "string")),
                ("TimeZoneField", managed(FIELD_STRING, "string")),
                ("RemoteAddressField", managed(FIELD_STRING, "string")),
                ("CronIntervalField", managed(FIELD_STRING, "string")),
                ("DateIntervalField", managed(FIELD_STRING, "string")),
                ("TreePathField", managed(FIELD_LONG_TEXT, "string")),
                ("TreeLevelField", managed(FIELD_INT, "int")),
                (
                    "ChildCountField",
                    Descriptor {
                        mode: CONSTRUCTOR_FIXED,
                        default_storage: "child_count",
                        default_property: "childCount",
                        ..managed(FIELD_INT, "int")
                    },
                ),
                (
                    "WasModifiedByUserField",
                    Descriptor {
                        default_storage: "was_modified_by_user",
                        default_property: "wasModifiedByUser",
                        boolean_getter: true,
                        ..managed(FIELD_BOOL, "bool")
                    },
                ),
                (
                    "LockedField",
                    Descriptor {
                        mode: CONSTRUCTOR_FIXED,
                        default_storage: "locked",
                        default_property: "locked",
                        boolean_getter: true,
                        implicit_computed: true,
                        ..managed(FIELD_BOOL, "bool")
                    },
                ),
                (
                    "CreatedByField",
                    Descriptor {
                        mode: CONSTRUCTOR_FIXED,
                        default_storage: "created_by_id",
                        default_property: "createdById",
                        minimum_version: Version::new(6, 3, 5),
                        target_definition: USER_DEFINITION,
                        target_entity: USER_ENTITY,
                        target_name: "user",
                        ..managed(FIELD_FOREIGN_KEY, "string")
                    },
                ),
                (
                    "UpdatedByField",
                    Descriptor {
                        mode: CONSTRUCTOR_FIXED,
                        default_storage: "updated_by_id",
                        default_property: "updatedById",
                        minimum_version: Version::new(6, 3, 5),
                        target_definition: USER_DEFINITION,
                        target_entity: USER_ENTITY,
                        target_name: "user",
                        ..managed(FIELD_FOREIGN_KEY, "string")
                    },
                ),
                (
                    "StateMachineStateField",
                    Descriptor {
                        minimum_additional: 1,
                        target_definition: "Shopware\\Core\\System\\StateMachine\\Aggregation\\StateMachineState\\StateMachineStateDefinition",
                        target_entity: "Shopware\\Core\\System\\StateMachine\\Aggregation\\StateMachineState\\StateMachineStateEntity",
                        target_name: "state_machine_state",
                        ..managed(FIELD_FOREIGN_KEY, "string")
                    },
                ),
            ];
            result.sort_by(|left, right| left.0.cmp(right.0));
            result
        });
    &DESCRIPTORS
}

fn descriptor(short_name: &str) -> Option<Descriptor> {
    descriptors()
        .iter()
        .find(|(name, _)| *name == short_name)
        .map(|(_, descriptor)| *descriptor)
}

pub(crate) fn import_specialized_field(
    id: &str,
    raw: &str,
    creation: &SyntaxNode,
    flags: &ImportedFlagSet,
    modifiers: &ImportedModifierSet,
    resolve: &Resolver,
) -> Option<FieldSpec> {
    let class_name = phpquery::object_class_name(creation);
    let short_name = short_class(&class_name);
    let descriptor = descriptor(short_name)?;
    let mut implementation = FieldImplementation {
        class: resolve.resolve(&class_name),
        constructor_mode: descriptor.mode.to_owned(),
        fixed_storage_name: descriptor.default_storage.to_owned(),
        fixed_property_name: descriptor.default_property.to_owned(),
        entity_type: descriptor.entity_type.to_owned(),
        entity_boolean_getter: descriptor.boolean_getter,
        entity_trait: descriptor.entity_trait.to_owned(),
        manage_entity: descriptor.manage_entity,
        implicit_computed: descriptor.implicit_computed,
        max_length_argument: descriptor.max_length_argument,
        minimum_additional_arguments: descriptor.minimum_additional,
        ..FieldImplementation::default()
    };
    let argument_count = phpquery::arguments(creation).count();
    let mut field = FieldSpec {
        id: id.to_owned(),
        kind: descriptor.kind.to_owned(),
        storage_name: descriptor.default_storage.to_owned(),
        property_name: descriptor.default_property.to_owned(),
        target_definition_class: descriptor.target_definition.to_owned(),
        target_entity_class: descriptor.target_entity.to_owned(),
        target_entity_name: descriptor.target_name.to_owned(),
        reference_field: "id".to_owned(),
        reference_storage_name: "id".to_owned(),
        max_length: descriptor.max_length,
        editable: true,
        raw: raw.to_owned(),
        ..FieldSpec::default()
    };
    flags.apply_stored(&mut field);
    if descriptor.mode == CONSTRUCTOR_STORAGE_PROPERTY {
        if argument_count > 0 {
            field.storage_name = imported_string_argument(creation, 0);
        }
        if argument_count > 1 {
            field.property_name = imported_string_argument(creation, 1);
        }
        for index in 2..argument_count {
            implementation
                .additional_arguments
                .push(normalize_imported_php_expression(
                    &phpquery::argument_value_text(creation, index),
                    resolve,
                ));
        }
    } else {
        for index in 0..argument_count {
            implementation
                .fixed_arguments
                .push(normalize_imported_php_expression(
                    &phpquery::argument_value_text(creation, index),
                    resolve,
                ));
        }
    }
    if short_name == "NumberRangeField" && argument_count > 2 {
        field.max_length = imported_int_argument(creation, 2, descriptor.max_length);
    }
    field.implementation = Some(implementation);
    let valid = !field.storage_name.is_empty() && !field.property_name.is_empty();
    valid.then(|| with_field_modifiers(field, modifiers))
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct SpecializedFieldTemplate {
    pub id: String,
    pub label: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub minimum_shopware_version: String,
    pub field: FieldSpec,
}

/// Safe creation templates for every native storage-aware Field subclass
/// known by the importer.
pub fn specialized_field_templates() -> Vec<SpecializedFieldTemplate> {
    let mut result = Vec::new();
    for (name, descriptor) in descriptors() {
        let mut entity_type = descriptor.template_entity_type;
        let mut manage_entity = descriptor.manage_entity || !entity_type.is_empty();
        if entity_type.is_empty() {
            entity_type = descriptor.entity_type;
        }
        if entity_type.is_empty() {
            manage_entity = true;
            match descriptor.kind {
                FIELD_JSON | FIELD_LIST | FIELD_OBJECT => entity_type = "array",
                _ => continue,
            }
        }
        let field = FieldSpec {
            kind: descriptor.kind.to_owned(),
            storage_name: descriptor.default_storage.to_owned(),
            property_name: descriptor.default_property.to_owned(),
            target_definition_class: descriptor.target_definition.to_owned(),
            target_entity_class: descriptor.target_entity.to_owned(),
            target_entity_name: descriptor.target_name.to_owned(),
            reference_field: "id".to_owned(),
            reference_storage_name: "id".to_owned(),
            max_length: descriptor.max_length,
            editable: true,
            implementation: Some(FieldImplementation {
                class: specialized_field_class(name),
                constructor_mode: descriptor.mode.to_owned(),
                fixed_storage_name: descriptor.default_storage.to_owned(),
                fixed_property_name: descriptor.default_property.to_owned(),
                entity_type: entity_type.to_owned(),
                entity_boolean_getter: descriptor.boolean_getter,
                entity_trait: descriptor.entity_trait.to_owned(),
                manage_entity,
                implicit_computed: descriptor.implicit_computed,
                max_length_argument: descriptor.max_length_argument,
                minimum_additional_arguments: descriptor.minimum_additional,
                ..FieldImplementation::default()
            }),
            ..FieldSpec::default()
        };
        let minimum_version = if descriptor.minimum_version.major != 0 {
            descriptor.minimum_version.to_string()
        } else {
            String::new()
        };
        result.push(SpecializedFieldTemplate {
            id: format!("specialized:{name}"),
            label: split_specialized_label(name),
            minimum_shopware_version: minimum_version,
            field,
        });
    }
    result
}

/// Whether a known native field subclass is available at the lower bound of
/// the configured Shopware version constraint. Unknown versions and
/// third-party subclasses remain available.
pub fn specialized_field_supported(class_name: &str, version_constraint: &str) -> bool {
    let Some(descriptor) = descriptor(short_class(class_name)) else {
        return true;
    };
    if descriptor.minimum_version.major == 0 {
        return true;
    }
    parse_version_constraint(version_constraint)
        .is_none_or(|version| version >= descriptor.minimum_version)
}

fn specialized_field_class(name: &str) -> String {
    match name {
        "SlotConfigField" => "Shopware\\Core\\Content\\Cms\\DataAbstractionLayer\\Field\\SlotConfigField".to_owned(),
        "FlowTemplateConfigField" => {
            "Shopware\\Core\\Content\\Flow\\DataAbstractionLayer\\Field\\FlowTemplateConfigField".to_owned()
        }
        "CheapestPriceField" => {
            "Shopware\\Core\\Content\\Product\\DataAbstractionLayer\\CheapestPrice\\CheapestPriceField".to_owned()
        }
        "MeasurementUnitsField" => {
            "Shopware\\Core\\Content\\MeasurementSystem\\Field\\MeasurementUnitsField".to_owned()
        }
        "NumberRangeField" => {
            "Shopware\\Core\\System\\NumberRange\\DataAbstractionLayer\\NumberRangeField".to_owned()
        }
        _ => format!("Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\{name}"),
    }
}

fn split_specialized_label(name: &str) -> String {
    let name = name.strip_suffix("Field").unwrap_or(name);
    let mut result = String::with_capacity(name.len() + 12);
    for (index, character) in name.chars().enumerate() {
        if index > 0 && character.is_uppercase() {
            result.push(' ');
        }
        result.push(character);
    }
    result.push_str(" (Shopware)");
    result
}
