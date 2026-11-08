#![allow(unused_imports)]
use super::support::*;
use crate::*;

fn slice(source: &str, range: TextRange, base: u32) -> &str {
    &source[(u32::from(range.start()) - base) as usize..(u32::from(range.end()) - base) as usize]
}

#[test]
fn vue_object_binding_fields_retain_source_identity() {
    let source = r#"  { category, "is-loading": pending, config: makeConfig(item, { active: true }), ...forwarded }  "#;
    let (fields, complete) = vue_object_binding_fields(source, 10);
    assert!(!complete);
    assert_eq!(fields.len(), 3);

    assert_eq!(fields[0].name, "category");
    assert_eq!(fields[0].expression, "category");
    assert!(fields[0].shorthand);
    assert_eq!(slice(source, fields[0].name_range, 10), "category");

    assert_eq!(fields[1].name, "is-loading");
    assert_eq!(fields[1].expression, "pending");
    assert!(!fields[1].shorthand);
    assert_eq!(slice(source, fields[1].name_range, 10), "is-loading");
    assert_eq!(slice(source, fields[1].expression_range, 10), "pending");

    assert_eq!(fields[2].name, "config");
    assert_eq!(fields[2].expression, "makeConfig(item, { active: true })");
}

#[test]
fn twig_component_object_binding_contexts() {
    let source = r#"<sw-card v-bind="{ title: headline, disabled, variant: 'secondary', mode: selected, ...attrs }" />"#;
    let root = parse_twig(source);
    let index_of = |needle: &str| source.find(needle).unwrap() as u32;

    let title_offset = index_of("title") + 2;
    let (start_tag, title) =
        twig_component_object_binding_field_at_offset(&root, title_offset).expect("title");
    assert_eq!(&start_tag.text().to_string()[1..8], "sw-card");
    assert_eq!(title.name, "title");
    assert_eq!(title.expression, "headline");

    let headline_offset = index_of("headline") + 2;
    assert!(twig_component_object_binding_field_at_offset(&root, headline_offset).is_none());

    let disabled_offset = index_of("disabled") + 2;
    let (_, disabled) =
        twig_component_object_binding_field_at_offset(&root, disabled_offset).expect("disabled");
    assert!(disabled.shorthand);

    let comma_offset = index_of("disabled") - 1;
    let (_, fields) = twig_component_object_binding_key_context_at_offset(&root, comma_offset)
        .expect("key context");
    assert_eq!(fields.len(), 4);

    assert!(twig_component_object_binding_key_context_at_offset(&root, headline_offset).is_none());

    let variant_value_offset = index_of("secondary") + 3;
    let (value_tag, variant, value_range) =
        twig_component_object_binding_value_at_offset(&root, variant_value_offset).expect("value");
    assert_eq!(start_tag.text_range(), value_tag.text_range());
    assert_eq!(variant.name, "variant");
    assert_eq!(variant.expression, "'secondary'");
    assert_eq!(&source[value_range], "secondary");

    let selected_offset = index_of("selected") + 2;
    assert!(twig_component_object_binding_value_at_offset(&root, selected_offset).is_none());
    assert!(twig_component_object_binding_value_at_offset(&root, title_offset).is_none());
}
