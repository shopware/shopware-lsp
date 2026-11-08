use crate::field_modification::field_flag_kinds;
use crate::*;

fn s(value: &str) -> String {
    value.to_owned()
}

fn flag(kind: &str) -> FieldFlagSpec {
    FieldFlagSpec {
        kind: s(kind),
        ..FieldFlagSpec::default()
    }
}

fn product_lookup(class: &str) -> Option<RelationTarget> {
    Some(RelationTarget {
        definition_class: s(class),
        entity_name: s("product"),
        ..RelationTarget::default()
    })
}

fn extension_spec(modifications: Vec<FieldModificationSpec>) -> EntitySpec {
    complete_spec(EntitySpec {
        mode: s("new"),
        definition_kind: s(DEFINITION_EXTENSION),
        namespace: s("Acme\\Example"),
        class_name: s("Product"),
        entity_name: s("product"),
        extended_definition_class: s("Shopware\\Core\\Content\\Product\\ProductDefinition"),
        field_modifications: modifications,
        ..EntitySpec::default()
    })
}

#[test]
fn entity_extension_field_modifications_cover_every_typed_flag() {
    let flags = vec![
        flag(FLAG_REQUIRED),
        flag(FLAG_PRIMARY_KEY),
        FieldFlagSpec {
            api_sources: vec![s("Shopware\\Core\\Framework\\Api\\Context\\AdminApiSource")],
            ..flag(FLAG_API_AWARE)
        },
        FieldFlagSpec {
            search_ranking: 500.0,
            search_tokenize: Some(false),
            ..flag(FLAG_SEARCH_RANKING)
        },
        FieldFlagSpec {
            clone_relevant: Some(false),
            ..flag(FLAG_CASCADE_DELETE)
        },
        FieldFlagSpec {
            enforced_by_constraint: Some(true),
            ..flag(FLAG_SET_NULL_ON_DELETE)
        },
        flag(FLAG_RESTRICT_DELETE),
        FieldFlagSpec {
            runtime_dependencies: vec![s("name"), s("updatedAt")],
            ..flag(FLAG_RUNTIME)
        },
        flag(FLAG_COMPUTED),
        flag(FLAG_NO_CONSTRAINT),
        FieldFlagSpec {
            inherited_foreign_key: s("parent_id"),
            ..flag(FLAG_INHERITED)
        },
        FieldFlagSpec {
            reverse_property: s("children"),
            ..flag(FLAG_REVERSE_INHERITED)
        },
        FieldFlagSpec {
            write_scopes: vec![s("system"), s("user")],
            ..flag(FLAG_WRITE_PROTECTED)
        },
        FieldFlagSpec {
            allow_html_sanitized: Some(false),
            ..flag(FLAG_ALLOW_HTML)
        },
        flag(FLAG_ALLOW_EMPTY_STRING),
        flag(FLAG_AS_ARRAY),
        flag(FLAG_IMMUTABLE),
        FieldFlagSpec {
            since: s("6.7.0.0"),
            ..flag(FLAG_SINCE)
        },
        FieldFlagSpec {
            deprecated: Some(Deprecation {
                deprecated_since: s("6.7.0.0"),
                will_be_removed_in: s("6.8.0.0"),
                replaced_by: s("replacement"),
            }),
            ..flag(FLAG_DEPRECATED)
        },
        flag(FLAG_IGNORE_IN_OPEN_API_SCHEMA),
        flag(FLAG_IGNORE_IN_UNUSED_MEDIA_SEARCH),
        flag(FLAG_API_CRITERIA_AWARE),
        FieldFlagSpec {
            rule_areas: vec![s("product"), s("order")],
            ..flag(FLAG_RULE_AREAS)
        },
        FieldFlagSpec {
            choice: Some(ChoiceSpec {
                values: vec![s("'one'"), s("Example::TWO")],
                strict: Some(true),
            }),
            ..flag(FLAG_CHOICE)
        },
        flag(FLAG_DO_NOT_USE_CONTEXT),
        flag(FLAG_EXTENSION),
    ];
    let remove = field_flag_kinds();
    let mut spec = extension_spec(vec![
        FieldModificationSpec {
            id: s("name-flags"),
            property_name: s("name"),
            add_flags: flags,
            ..FieldModificationSpec::default()
        },
        FieldModificationSpec {
            id: s("description-flags"),
            property_name: s("description"),
            remove_flags: remove.clone(),
            ..FieldModificationSpec::default()
        },
    ]);
    assert_eq!(validate_spec(&spec), vec![]);

    let source = render_definition(&spec).unwrap();
    assert!(
        swlsp_parser_php::parse(&source).errors.is_empty(),
        "{source}"
    );
    assert!(source.contains("$collection->get('name')?->addFlags("));
    assert_eq!(
        source
            .matches("$collection->get('description')?->removeFlag(")
            .count(),
        remove.len()
    );

    let mut imported = import_extension(&source, Some(&product_lookup)).unwrap();
    assert!(imported.modify_fields_method_raw.is_empty());
    for modification in spec
        .field_modifications
        .iter_mut()
        .chain(imported.field_modifications.iter_mut())
    {
        modification.id.clear();
    }
    assert_eq!(spec.field_modifications, imported.field_modifications);
}

#[test]
fn entity_extension_custom_modify_fields_is_locked_and_preserved() {
    let source = r#"<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Content\Product\ProductDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityExtension;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
final class ProductExtension extends EntityExtension
{
    public function modifyFields(FieldCollection $collection): void
    {
        foreach ($collection as $field) {
            $this->modify($field);
        }
    }

    public function getEntityName(): string
    {
        return ProductDefinition::ENTITY_NAME;
    }
}"#;
    let mut spec = import_extension(source, Some(&product_lookup)).unwrap();
    assert!(
        spec.modify_fields_method_raw
            .contains("$this->modify($field)")
    );
    spec.fields.push(FieldSpec {
        id: s("runtime"),
        kind: s(FIELD_STRING),
        property_name: s("runtime"),
        storage_name: s("runtime"),
        behavior: Some(FieldBehavior {
            runtime: true,
            ..FieldBehavior::default()
        }),
        editable: true,
        ..FieldSpec::default()
    });
    let rewritten = rewrite_definition(source, &spec).unwrap();
    assert!(rewritten.contains("$this->modify($field)"));
}

#[test]
fn entity_extension_field_modification_validation() {
    let mut base = extension_spec(Vec::new());
    base.field_modifications = vec![FieldModificationSpec {
        id: s("invalid"),
        property_name: s("bad-name"),
        add_flags: vec![flag(FLAG_REVERSE_INHERITED)],
        remove_flags: vec![s(FLAG_REVERSE_INHERITED)],
    }];
    let codes: Vec<String> = validate_spec(&base)
        .into_iter()
        .map(|issue| issue.code)
        .collect();
    for code in [
        "entity.extension.modifyFields.property.invalid",
        "entity.extension.modifyFields.reverseInherited.invalid",
        "entity.extension.modifyFields.flag.conflict",
    ] {
        assert!(codes.contains(&s(code)), "{code}");
    }
}
