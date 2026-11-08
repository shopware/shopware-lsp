#![allow(unused_imports)]
use super::support::*;
use crate::*;

#[test]
fn parse_meteor_volar_declaration() {
    let file_path = "/project/Resources/app/administration/node_modules/\
@shopware-ag/meteor-component-library/dist/esm/MtButton.d.ts";
    let component = parse_meteor_declaration(
        file_path,
        r#"
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
};"#,
    )
    .expect("component");
    assert_eq!(component.name, "mt-button");
    assert_eq!(component.definition_path, file_path);
    let props: HashMap<String, VueComponentProp> = component
        .props
        .iter()
        .map(|p| (p.name.clone(), p.clone()))
        .collect();
    assert!(props["title"].required);
    assert_eq!(props["title"].type_, "string");
    assert!(!props["size"].required);
    assert_eq!(props["size"].type_, r#""small" | "large" | legacySize"#);
    let (values, complete) = vue_prop_allowed_values(&props["size"]);
    assert!(complete);
    assert_eq!(values, vec!["small", "large", "legacy"]);
    assert!(!props.contains_key("onUpdate:modelValue"));
    assert!(component.emits.contains(&"update:model-value".to_owned()));
    let event = component
        .component_event("update:model-value")
        .expect("event");
    assert_eq!(event.file_path, file_path);
    assert_eq!(event.line, 7);
    assert!(event.type_.contains("value: string"));
    assert_eq!(event.documentation, "Updates the public model value.");
    assert!(event.name_range.declaration);
    assert!(!event.name_range.identifier);
    assert!(event.name_range.end_character > event.name_range.start_character);
    assert_eq!(slot_names(&component.slots), vec!["default", "icon-front"]);
    assert_eq!(component.slots[0].file_path, file_path);
    assert!(component.slots[0].name_range.declaration);
    assert!(component.slots[0].name_range.identifier);
    assert!(component.slots[0].payload_type.is_empty());
    assert!(component.slots[1].name_range.declaration);
    assert!(!component.slots[1].name_range.identifier);
    assert_eq!(component.slots[1].members.len(), 1);
    assert_eq!(component.slots[1].payload_type, "{ size: number }");
    let member = &component.slots[1].members[0];
    assert_eq!(member.name, "size");
    assert_eq!(member.type_, "number");
    assert_eq!(member.file_path, file_path);
    assert_eq!(member.line, 13);
    assert!(member.name_range.declaration);
    assert!(member.name_range.identifier);
}

#[test]
fn vue_prop_allowed_values_retain_partial_literal_unions() {
    let (values, complete) = vue_prop_allowed_values(&VueComponentProp {
        name: "variant".into(),
        type_: r#""primary" | "secondary" | LegacyVariant | undefined"#.into(),
        ..Default::default()
    });
    assert!(!complete);
    assert_eq!(values, vec!["primary", "secondary"]);
    let (values, complete) = vue_prop_allowed_values(&VueComponentProp {
        name: "size".into(),
        type_: "`${string}px`".into(),
        ..Default::default()
    });
    assert!(!complete);
    assert!(values.is_empty());
    let (values, complete) = vue_prop_allowed_values(&VueComponentProp {
        name: "tone".into(),
        type_: r#""info" | "critical""#.into(),
        allowed_values: vec!["info".into()],
        ..Default::default()
    });
    assert!(complete);
    assert_eq!(values, vec!["info", "critical"]);
}

#[test]
fn parse_meteor_object_style_slot_payload() {
    let file_path = "/project/Resources/app/administration/node_modules/\
@shopware-ag/meteor-component-library/dist/esm/MtCard.d.ts";
    let component = parse_meteor_declaration(
        file_path,
        "
declare function __VLS_template(): {
    slots: Readonly<{
        default: null;
        header: {
            title: string;
            actions?: readonly string[];
        };
    }>;
};",
    )
    .expect("component");
    assert_eq!(component.slots.len(), 2);
    let header = &component.slots[1];
    assert_eq!(header.name, "header");
    assert!(header.payload_type.contains("title: string"));
    assert_eq!(header.members.len(), 2);
    assert_eq!(header.members[0].name, "title");
    assert_eq!(header.members[0].type_, "string");
    assert_eq!(header.members[0].line, 6);
    assert_eq!(header.members[1].name, "actions");
    assert_eq!(header.members[1].type_, "readonly string[]");
    assert_eq!(header.members[1].line, 7);
}

#[test]
fn parse_meteor_extract_prop_types_declaration() {
    let file_path = "/project/Resources/app/administration/node_modules/\
@shopware-ag/meteor-component-library/dist/esm/MtTextField.d.ts";
    let component = parse_meteor_declaration(
        file_path,
        r#"
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
};"#,
    )
    .expect("component");
    let props: HashMap<String, VueComponentProp> = component
        .props
        .iter()
        .map(|p| (p.name.clone(), p.clone()))
        .collect();
    assert_eq!(props["modelValue"].type_, "string | number");
    assert!(!props["modelValue"].required);
    assert_eq!(props["active"].type_, "Boolean");
    assert!(props["active"].required);
    assert_eq!(slot_names(&component.slots), vec!["header-left", "default"]);
    assert!(component.emits.contains(&"update:model-value".to_owned()));
}

#[test]
fn parse_meteor_runtime_prop_compatibility() {
    let tests = [
        (
            "MtLink.d.ts",
            "to",
            "type __VLS_Props = { to?: string; };",
            "string | object",
        ),
        (
            "MtIcon.d.ts",
            "size",
            "type __VLS_Props = { name: string; size?: string; };",
            "string | number",
        ),
    ];
    for (file_name, prop_name, source, want_type) in tests {
        let file_path = format!(
            "/project/Resources/app/administration/node_modules/@shopware-ag/meteor-component-library/dist/esm/{file_name}"
        );
        let component = parse_meteor_declaration(&file_path, source).expect("component");
        let prop = component.component_prop(prop_name).expect("prop");
        assert_eq!(prop.type_, want_type, "{file_name}");
    }
}

#[test]
fn admin_indexer_discovers_meteor_declarations_only() {
    use std::sync::Arc;

    let project_root = temp_dir();
    let cache_root = temp_dir();
    let declaration_path = project_root.path().join(
        "src/Administration/Resources/app/administration/node_modules/\
@shopware-ag/meteor-component-library/dist/esm/MtButton.d.ts",
    );
    write_file(
        &declaration_path,
        "
type __VLS_Props = { disabled?: boolean; variant?: \"primary\" | \"secondary\"; };
",
    );
    let unrelated_path = project_root
        .path()
        .join("node_modules/unrelated/MtIgnored.d.ts");
    write_file(
        &unrelated_path,
        "
type __VLS_Props = { ignored?: boolean; };
",
    );

    let idx = Arc::new(new_indexer(&cache_root.path().join("admin")));
    let mut scanner = swlsp_indexer::FileScanner::new(
        project_root.path().to_str().unwrap(),
        cache_root.path().join("scanner.db"),
        None,
    )
    .unwrap();
    scanner.add_indexer(idx.clone());
    scanner
        .index_all(&swlsp_indexer::CancelToken::new())
        .unwrap();

    let component = idx
        .get_effective_component("mt-button")
        .unwrap()
        .expect("mt-button");
    let mut names = admin_prop_names_for_test(&component.props);
    names.sort();
    assert_eq!(names, vec!["disabled", "variant"]);
    assert!(idx.get_effective_component("mt-ignored").unwrap().is_none());
    scanner.close().unwrap();
}
