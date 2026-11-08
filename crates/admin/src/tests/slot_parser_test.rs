#![allow(unused_imports)]
use super::support::*;
use crate::*;

fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_owned()).collect()
}

fn line_slice<'a>(content: &'a str, range: &AdminSourceRange) -> &'a str {
    let line = content.split('\n').nth(range.start_line as usize).unwrap();
    &line[range.start_character as usize..range.end_character as usize]
}

#[test]
fn parse_slots_from_content_test() {
    let tests: &[(&str, &str, &[&str], &[u32])] = &[
        ("no slots", "<div>Hello World</div>", &[], &[]),
        (
            "default slot only",
            "<div><slot></slot></div>",
            &["default"],
            &[1],
        ),
        (
            "named slot",
            r#"<div><slot name="header"></slot></div>"#,
            &["header"],
            &[1],
        ),
        (
            "named slot with single quotes",
            "<div><slot name='footer'></slot></div>",
            &["footer"],
            &[1],
        ),
        (
            "multiple slots",
            "<div>
	<slot name=\"header\"></slot>
	<slot></slot>
	<slot name=\"footer\"></slot>
</div>",
            &["header", "default", "footer"],
            &[2, 3, 4],
        ),
        (
            "duplicate slots deduplicated",
            "<div>
	<slot name=\"header\"></slot>
	<slot name=\"header\"></slot>
</div>",
            &["header"],
            &[2],
        ),
        (
            "self-closing slot",
            r#"<div><slot name="icon" /></div>"#,
            &["icon"],
            &[1],
        ),
        (
            "slot with other attributes",
            r#"<div><slot name="content" :data="someData"></slot></div>"#,
            &["content"],
            &[1],
        ),
        (
            "slot in twig template",
            "{% block sw_card %}
	<div class=\"sw-card\">
		<slot name=\"header\">
			{{ $tc('sw-card.defaultHeader') }}
		</slot>
		<div class=\"sw-card__content\">
			<slot></slot>
		</div>
		<slot name=\"footer\"></slot>
	</div>
{% endblock %}",
            &["header", "default", "footer"],
            &[3, 7, 9],
        ),
    ];
    for (name, content, expected_names, expected_lines) in tests {
        let result = parse_slots_from_content(content);
        assert_eq!(result.len(), expected_names.len(), "{name}");
        for (index, slot) in result.iter().enumerate() {
            assert_eq!(
                slot.name, expected_names[index],
                "{name}: slot name at {index}"
            );
            if index < expected_lines.len() {
                assert_eq!(
                    slot.line, expected_lines[index],
                    "{name}: line of {}",
                    slot.name
                );
            }
        }
    }
}

#[test]
fn parse_slots_retains_exact_declaration_ranges() {
    let content = "<slot />
<slot name=\"header\" />
<slot :name=\"'footer'\" />
<slot :name=\"`column-${column.property}`\" />";
    let result = parse_template_content(content);
    assert_eq!(result.slots.len(), 4);
    for (index, expected) in ["slot", "header", "footer", "`column-${column.property}`"]
        .iter()
        .enumerate()
    {
        let range = &result.slots[index].name_range;
        assert!(range.declaration, "{expected}");
        assert!(!range.identifier, "{expected}");
        assert_eq!(range.start_line, range.end_line, "{expected}");
        assert_eq!(line_slice(content, range), *expected);
    }
    assert_eq!(result.slots[0].name, "default");
    assert_eq!(result.slots[3].display_name(), "column-*");
}

#[test]
fn parse_scoped_slot_payload_members() {
    let content = "<div>
    <slot
        name=\"result-item\"
        :item=\"item\"
        v-bind=\"{ index, active: isActive }\"
    ></slot>
    <slot :name=\"forwardedName\" v-bind=\"forwardedData\"></slot>
</div>";
    let result = parse_template_content(content);
    assert_eq!(result.slots.len(), 1);
    assert_eq!(result.slots[0].name, "result-item");
    let members: HashMap<String, VueComponentSlotMember> = result.slots[0]
        .members
        .iter()
        .map(|member| (member.name.clone(), member.clone()))
        .collect();
    let mut names: Vec<String> = members.keys().cloned().collect();
    names.sort();
    assert_eq!(names, strings(&["active", "index", "item"]));
    assert_eq!(members["item"].line, 4);
    assert_eq!(members["index"].line, 5);
    assert_eq!(members["active"].line, 5);
    for name in ["item", "index", "active"] {
        let member = &members[name];
        assert!(member.name_range.declaration, "{name}");
        assert!(!member.name_range.identifier, "{name}");
        assert_eq!(line_slice(content, &member.name_range), name);
    }
}

#[test]
fn parse_scoped_slot_payload_member_paths() {
    let dir = temp_dir();
    let template_path = path_string(dir.path(), "component.html.twig");
    write_file(
        std::path::Path::new(&template_path),
        r#"<slot name="default" :active="active"></slot>"#,
    );
    let result = parse_template_from_file(&template_path).unwrap();
    assert_eq!(result.slots.len(), 1);
    assert_eq!(result.slots[0].members.len(), 1);
    assert_eq!(result.slots[0].file_path, template_path);
    assert_eq!(result.slots[0].members[0].file_path, template_path);
}

#[test]
fn parse_dynamic_slot_families() {
    let content = concat!(
        "<div>\n",
        "  <slot :name=\"`column-${column.property}`\" ",
        "v-bind=\"{ item, isInlineEdit: isInlineEdit(item) }\"></slot>\n",
        "  <slot :name=\"`column-label-${column.property}`\" ",
        "v-bind=\"{ column, columnIndex }\"></slot>\n",
        "  <slot :name=\"`prefix-${row.id}-suffix`\" :row=\"row\"></slot>\n",
        "  <slot :name=\"'bound-static'\" :active=\"active\"></slot>\n",
        "  <slot :name=\"forwardedName\" v-bind=\"forwardedData\"></slot>\n",
        "  <slot :name=\"'not-' + 'safe'\"></slot>\n",
        "  <slot :name=\"`multi-${row.id}-${column.id}`\"></slot>\n",
        "</div>"
    );
    let result = parse_template_content(content);
    assert_eq!(result.slots.len(), 4);

    let component = VueComponent {
        slots: result.slots,
        ..Default::default()
    };
    let column = component.component_slot("column-name").expect("column");
    assert!(column.is_dynamic_name());
    assert_eq!(column.display_name(), "column-*");
    assert_eq!(column.name_prefix, "column-");
    assert!(column.name_suffix.is_empty());
    assert_eq!(
        slot_member_names(&column.members),
        strings(&["item", "isInlineEdit"])
    );

    let label = component
        .component_slot("column-label-name")
        .expect("label");
    assert_eq!(label.display_name(), "column-label-*");
    assert_eq!(
        slot_member_names(&label.members),
        strings(&["column", "columnIndex"])
    );

    let suffixed = component
        .component_slot("prefix-value-suffix")
        .expect("suffixed");
    assert_eq!(suffixed.display_name(), "prefix-*-suffix");
    assert_eq!(slot_member_names(&suffixed.members), strings(&["row"]));

    let bound = component.component_slot("bound-static").expect("bound");
    assert!(!bound.is_dynamic_name());
    assert_eq!(slot_member_names(&bound.members), strings(&["active"]));
    assert!(component.component_slot("multi-one-two").is_none());
}

#[test]
fn component_slot_prefers_exact_and_most_specific_family() {
    let component = VueComponent {
        slots: vec![
            VueComponentSlot {
                name_prefix: "column-".to_owned(),
                line: 1,
                ..Default::default()
            },
            VueComponentSlot {
                name_prefix: "column-label-".to_owned(),
                line: 2,
                ..Default::default()
            },
            VueComponentSlot {
                name: "column-label-name".to_owned(),
                line: 3,
                ..Default::default()
            },
        ],
        ..Default::default()
    };
    assert_eq!(
        component.component_slot("column-label-name").unwrap().line,
        3
    );
    assert_eq!(
        component.component_slot("column-label-price").unwrap().line,
        2
    );
    assert_eq!(component.component_slot("column-price").unwrap().line, 1);
}

#[test]
fn parse_blocks_from_content_test() {
    let tests: &[(&str, &str, &[&str], &[u32])] = &[
        ("no blocks", "<div>Hello World</div>", &[], &[]),
        (
            "single block",
            "{% block sw_card %}<div>content</div>{% endblock %}",
            &["sw_card"],
            &[1],
        ),
        (
            "multiple blocks",
            "{% block sw_page %}
<div>
	{% block sw_page_header %}
	<header></header>
	{% endblock %}
	{% block sw_page_content %}
	<main></main>
	{% endblock %}
</div>
{% endblock %}",
            &["sw_page", "sw_page_header", "sw_page_content"],
            &[1, 3, 6],
        ),
        (
            "blocks with slots",
            "{% block sw_card %}
<div class=\"sw-card\">
	<slot name=\"header\"></slot>
	{% block sw_card_content %}
	<slot></slot>
	{% endblock %}
</div>
{% endblock %}",
            &["sw_card", "sw_card_content"],
            &[1, 4],
        ),
        (
            "duplicate blocks deduplicated",
            "{% block sw_card %}
<div>first</div>
{% endblock %}
{% block sw_card %}
<div>second</div>
{% endblock %}",
            &["sw_card"],
            &[1],
        ),
    ];
    for (name, content, expected_names, expected_lines) in tests {
        let result = parse_template_content(content);
        assert_eq!(result.blocks.len(), expected_names.len(), "{name}");
        for (index, block) in result.blocks.iter().enumerate() {
            assert_eq!(
                block.name, expected_names[index],
                "{name}: block name at {index}"
            );
            if index < expected_lines.len() {
                assert_eq!(
                    block.line, expected_lines[index],
                    "{name}: line of {}",
                    block.name
                );
            }
        }
    }
}

#[test]
fn parse_block_deprecation_and_exact_name_range() {
    let content = "{# @deprecated tag:v6.8.0 - Use sw_modern_content instead. #}
{% block sw_legacy_content %}{% endblock %}
{% block sw_active_content %}{% endblock %}";
    let result = parse_template_content(content);
    assert_eq!(result.blocks.len(), 2);
    let legacy = &result.blocks[0];
    assert_eq!(legacy.name, "sw_legacy_content");
    assert_eq!(
        legacy.deprecated,
        "tag:v6.8.0 - Use sw_modern_content instead."
    );
    assert_eq!(legacy.line, 2);
    assert_eq!(
        legacy.name_range,
        AdminSourceRange {
            start_line: 1,
            start_character: 9,
            end_line: 1,
            end_character: 26,
            declaration: true,
            identifier: true,
            ..Default::default()
        }
    );
    assert!(result.blocks[1].deprecated.is_empty());
}

#[test]
fn parse_block_lexical_scope_contracts() {
    let content = "<sw-grid>
    <template #row=\"props\">
        <div v-for=\"(item, index) in props.items\">
            {% block sw_grid_row %}{{ item.name }}{% endblock %}
        </div>
        {% block sw_grid_footer %}{{ props.total }}{% endblock %}
    </template>
</sw-grid>";
    let result = parse_template_content(content);
    assert_eq!(result.blocks.len(), 2);
    let row = &result.blocks[0];
    assert_eq!(row.name, "sw_grid_row");
    assert_eq!(row.scope_members.len(), 3);
    let mut names: Vec<String> = row
        .scope_members
        .iter()
        .map(|member| member.name.clone())
        .collect();
    names.sort();
    assert_eq!(names, strings(&["index", "item", "props"]));
    assert!(row.scope_members[1].name_range.declaration);
    assert!(row.scope_members[1].line > 0);
    let footer = &result.blocks[1];
    assert_eq!(footer.scope_members.len(), 1);
    assert_eq!(footer.scope_members[0].name, "props");
}

#[test]
fn parse_block_scope_across_nested_twig_blocks() {
    let content = "{% block outer %}
<sw-seo-url>
    <template #seo-additional=\"props\">
        {% block inner %}
        <div :title=\"props.currentSalesChannelId\"></div>
        {% endblock %}
    </template>
</sw-seo-url>
{% endblock %}";
    let result = parse_template_content(content);
    assert_eq!(result.blocks.len(), 2);
    let inner = result
        .blocks
        .iter()
        .find(|block| block.name == "inner")
        .expect("inner");
    let member = inner.scope_member("props").expect("props");
    assert_eq!(member.name, "props");
}

#[test]
fn resolve_template_path_test() {
    for (name, definition_path, template_import, expected) in [
        (
            "relative path same dir",
            "/project/src/component/sw-card/index.js",
            "./sw-card.html.twig",
            "/project/src/component/sw-card/sw-card.html.twig",
        ),
        (
            "relative path parent dir",
            "/project/src/component/sw-card/index.js",
            "../template/sw-card.html.twig",
            "/project/src/component/template/sw-card.html.twig",
        ),
        (
            "filename only",
            "/project/src/component/sw-card/index.js",
            "sw-card.html.twig",
            "/project/src/component/sw-card/sw-card.html.twig",
        ),
        (
            "empty template import",
            "/project/src/component/sw-card/index.js",
            "",
            "",
        ),
    ] {
        assert_eq!(
            resolve_template_path(definition_path, template_import),
            expected,
            "{name}"
        );
    }
}

#[test]
fn parse_template_from_file_retains_owning_paths() {
    let dir = temp_dir();
    let template_path = path_string(dir.path(), "sw-card.html.twig");
    write_file(
        std::path::Path::new(&template_path),
        r#"{% block sw_card %}<slot name="header" />{% endblock %}"#,
    );
    let result = parse_template_from_file(&template_path).unwrap();
    assert_eq!(result.slots.len(), 1);
    assert_eq!(result.blocks.len(), 1);
    assert_eq!(result.slots[0].file_path, template_path);
    assert_eq!(result.blocks[0].file_path, template_path);
}
