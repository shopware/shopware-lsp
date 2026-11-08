use rowan::ast::AstNode;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_js::syntax as jssyntax;
use swlsp_parser_scss::query as scssquery;
use swlsp_parser_scss::syntax as scsssyntax;
use swlsp_parser_twig::ast::HtmlStartingTag;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax as twigsyntax;
use swlsp_syntax::{SyntaxNodeExt, TextSize};

use super::*;

fn at(offset: usize) -> TextSize {
    TextSize::from(offset as u32)
}

#[test]
fn parse_mixed_vue_sfc_with_absolute_embedded_ranges() {
    let source = r#"<template>
  <sw-card :title="title"><template #actions>Save</template></sw-card>
</template>
<script setup lang="ts">
const props = defineProps({ title: { type: String, required: true } });
</script>
<style scoped>.card { color: red; }</style>"#;
    let result = parse_sfc(source);
    let root = result.parse.root();
    assert_eq!(root.text().to_string(), source);
    assert_eq!(result.sections.len(), 3);
    assert_eq!(result.sections[0].kind, SectionKind::Template);
    assert_eq!(result.sections[1].kind, SectionKind::Script);
    assert!(result.sections[1].setup);
    assert_eq!(result.sections[1].language, "ts");

    let tags = twigquery::nodes(&root, &[twigsyntax::HTML_STARTING_TAG]);
    assert!(!tags.is_empty());
    let card = tags
        .into_iter()
        .find(|tag| twigquery::html_tag_name(tag).as_deref() == Some("sw-card"))
        .expect("sw-card tag");
    let tag = HtmlStartingTag::cast(card).expect("cast");
    let name = tag.name().expect("name");
    assert_eq!(
        name.text_range().start(),
        at(source.find("sw-card").unwrap())
    );

    let calls = jsquery::calls(&root, &["defineProps"]);
    assert_eq!(calls.len(), 1);
    assert_eq!(
        jsquery::call_callee(&calls[0])
            .unwrap()
            .range_trimmed_trivia()
            .start(),
        at(source.find("defineProps").unwrap())
    );
    assert!(!jsquery::nodes(&root, &[jssyntax::JS_PROGRAM]).is_empty());
    let declarations = scssquery::nodes(&root, &[scsssyntax::SCSS_DECLARATION]);
    assert!(!declarations.is_empty());
    assert_eq!(
        declarations[0].range_trimmed_trivia().start(),
        at(source.find("color").unwrap())
    );
}

#[test]
fn sections_keeps_nested_template_inside_sfc_body() {
    let source = "<template><div><template #default>Hi</template></div></template><script>export default {}</script>";
    let sections = sections(source);
    assert_eq!(sections.len(), 2);
    assert_eq!(
        &source[sections[0].body_range],
        "<div><template #default>Hi</template></div>"
    );
}

#[test]
fn unterminated_section_extends_to_end_of_file() {
    let source = "<!-- <script> --><script>const a = 1;\n<i18n lang=json>";
    let parsed = parse_sfc(source);
    assert_eq!(parsed.parse.root().text().to_string(), source);
    assert_eq!(parsed.sections.len(), 1);
    assert_eq!(parsed.sections[0].kind, SectionKind::Script);
    assert!(parsed.sections[0].close_range.is_empty());
    assert_eq!(
        u32::from(parsed.sections[0].body_range.end()),
        source.len() as u32
    );
}

#[test]
fn custom_sections_and_attributes() {
    let source = "<i18n lang=json/>\n<docs SETUP>text</docs>\n<div>x</div>";
    let parsed = parse_sfc(source);
    assert_eq!(parsed.parse.root().text().to_string(), source);
    assert_eq!(parsed.sections.len(), 2);
    assert_eq!(parsed.sections[0].kind, SectionKind::Custom);
    assert_eq!(parsed.sections[0].language, "json");
    assert!(parsed.sections[0].close_range.is_empty());
    assert!(parsed.sections[1].setup);
    assert_eq!(&source[parsed.sections[1].body_range], "text");
    let dump = parsed.parse.debug_dump();
    assert!(dump.contains("VUE_CUSTOM_SECTION"), "{dump}");
}

#[test]
fn embedded_errors_are_shifted_to_absolute_ranges() {
    let source = "<style>\n}\n</style>";
    let parsed = parse(source);
    assert!(!parsed.errors.is_empty());
    for error in &parsed.errors {
        assert!(u32::from(error.range.start()) >= "<style>".len() as u32);
    }
}
