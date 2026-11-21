//! What the generator needs to know about an entity.

use swlsp_shopware_entityschema as schema;

#[derive(Debug, Clone, Default, PartialEq)]
pub struct Entity {
    pub name: String,
    pub fields: Vec<EntityField>,
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Kind {
    String,
    LongText,
    Int,
    Float,
    Bool,
    Date,
    DateTime,
    Enum,
    ManyToOne,
    /// Created and updated dates: shown in a list, never edited.
    Timestamp,
    /// Anything a generated form has no input for.
    #[default]
    Other,
}

#[derive(Debug, Clone, Default, PartialEq)]
pub struct EntityField {
    /// The property name.
    pub name: String,
    pub kind: Kind,
    pub required: bool,
    pub translated: bool,
    /// Written by the server only: write protected, runtime or computed.
    pub read_only: bool,
    pub allow_html: bool,
    pub choices: Vec<String>,
    /// For a many-to-one association: the property of its foreign key, the
    /// entity it points at and the property that names a record of it.
    pub foreign_key: String,
    pub target_entity: String,
    pub target_label: String,
}

impl Entity {
    pub fn field(&self, name: &str) -> Option<&EntityField> {
        self.fields.iter().find(|field| field.name == name)
    }

    pub fn translated(&self) -> bool {
        self.fields.iter().any(|field| field.translated)
    }

    /// The entity as the entity designer imported it. `target_label` names
    /// the label property of an associated entity.
    pub fn from_spec(spec: &schema::EntitySpec, target_label: &dyn Fn(&str) -> String) -> Self {
        // A definition has `createdAt` and `updatedAt` unless it replaces
        // its default fields, which then stand in the specification.
        let replaced = spec
            .definition_behavior
            .as_ref()
            .filter(|behavior| behavior.override_default_fields);
        let defaults: &[schema::FieldSpec] = match replaced {
            Some(behavior) => &behavior.default_fields,
            None => &[],
        };
        let mut fields: Vec<EntityField> = spec
            .fields
            .iter()
            .chain(defaults)
            .filter(|field| !field.property_name.is_empty())
            .map(|field| {
                let kind = match field.kind.as_str() {
                    schema::FIELD_STRING => Kind::String,
                    schema::FIELD_LONG_TEXT => Kind::LongText,
                    schema::FIELD_INT => Kind::Int,
                    schema::FIELD_FLOAT => Kind::Float,
                    schema::FIELD_BOOL => Kind::Bool,
                    schema::FIELD_DATE => Kind::Date,
                    schema::FIELD_DATE_TIME => Kind::DateTime,
                    schema::FIELD_ENUM => Kind::Enum,
                    schema::FIELD_MANY_TO_ONE => Kind::ManyToOne,
                    schema::FIELD_CREATED_AT | schema::FIELD_UPDATED_AT => Kind::Timestamp,
                    _ => Kind::Other,
                };
                let behavior = field.behavior.as_ref();
                let metadata = field.metadata.as_ref();
                EntityField {
                    name: field.property_name.clone(),
                    kind,
                    required: field.required,
                    translated: field.translated,
                    read_only: field.write_protected
                        || behavior.is_some_and(|behavior| behavior.runtime || behavior.computed),
                    allow_html: metadata.and_then(|metadata| metadata.allow_html).is_some(),
                    choices: metadata
                        .and_then(|metadata| metadata.choice.as_ref())
                        .map(|choice| choice.values.clone())
                        .unwrap_or_default(),
                    foreign_key: field.foreign_key_property_name.clone(),
                    target_label: match kind {
                        Kind::ManyToOne => target_label(&field.target_entity_name),
                        _ => String::new(),
                    },
                    target_entity: field.target_entity_name.clone(),
                }
            })
            .collect();
        if replaced.is_none() {
            for name in ["createdAt", "updatedAt"] {
                if !fields.iter().any(|field| field.name == name) {
                    fields.push(EntityField {
                        name: name.to_owned(),
                        kind: Kind::Timestamp,
                        read_only: true,
                        ..EntityField::default()
                    });
                }
            }
        }
        Entity {
            name: spec.entity_name.clone(),
            fields,
        }
    }
}
