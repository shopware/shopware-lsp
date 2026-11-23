//! Completion in a module description.

use swlsp_ide::completion::test_support::request_with_cursor;
use swlsp_lsp::{CompletionProvider, Context};
use swlsp_lsp_protocol::{self as protocol, CompletionItemKind};

use super::{AdminModuleCompletionProvider, place};
use crate::admin_module::tests::Plugin;

/// The place of the `|` in `source`.
fn at(source: &str) -> Option<super::Place> {
    let offset = source.find('|').unwrap();
    let source = source.replacen('|', "", 1);
    let root = swlsp_parser_yaml::parse(&source).root();
    place(&source, &root, offset)
}

fn completions(plugin: &Plugin, source: &str) -> Vec<protocol::CompletionItem> {
    AdminModuleCompletionProvider::new(plugin.generator.clone()).get_completions(
        &Context::background(),
        &request_with_cursor(&plugin.uri(), source),
    )
}

fn labels(items: &[protocol::CompletionItem]) -> Vec<&str> {
    items.iter().map(|item| item.label.as_str()).collect()
}

fn inserted<'a>(items: &'a [protocol::CompletionItem], label: &str) -> &'a str {
    let item = items.iter().find(|item| item.label == label).unwrap();
    &item.text_edit.as_ref().unwrap().new_text
}

const MODULE: &str = "modules:\n  acme-review:\n    entity: acme_review\n";

#[test]
fn the_place_follows_the_indentation() {
    let place = at(&format!("{MODULE}    icon: star\n    li|\n")).unwrap();
    assert_eq!(place.path, ["modules", "acme-review"]);
    assert!(!place.value);
    assert_eq!((place.indent, place.step), (4, 2));
    assert_eq!(
        place.existing.iter().collect::<Vec<_>>(),
        ["entity", "icon"]
    );
    assert_eq!(u32::from(place.range.len()), 2);

    // An empty line belongs to the mapping the cursor is indented into.
    let place = at(&format!("{MODULE}    listing:\n      |\n")).unwrap();
    assert_eq!(place.path, ["modules", "acme-review", "listing"]);
    let place = at(&format!("{MODULE}    listing:\n    |\n")).unwrap();
    assert_eq!(place.path, ["modules", "acme-review"]);
    assert_eq!(at("|").unwrap().path, Vec::<String>::new());

    let place = at(&format!("{MODULE}    listing:\n      auto: fa|\n")).unwrap();
    assert_eq!(place.path, ["modules", "acme-review", "listing", "auto"]);
    assert!(place.value);
    assert_eq!(u32::from(place.range.len()), 2);

    // Before the colon of a key that is complete.
    assert!(at(&format!("{MODULE}    lis|ting:\n")).unwrap().complete);
    assert_eq!(at("# a |comment\n"), None);
    assert_eq!(at(&format!("{MODULE}    icon: a b|\n")), None);
}

#[test]
fn keys_come_from_the_schema_without_those_that_are_set() {
    let plugin = Plugin::new();
    let items = completions(&plugin, &format!("{MODULE}    |\n"));
    let found = labels(&items);
    assert!(found.contains(&"listing") && found.contains(&"labelField"));
    assert!(!found.contains(&"entity"));
    assert_eq!(inserted(&items, "listing"), "listing:\n      ");
    assert_eq!(inserted(&items, "icon"), "icon: ");
    let listing = items.iter().find(|item| item.label == "listing").unwrap();
    assert_eq!(listing.detail, "The list page.");
    assert_eq!(listing.kind, Some(CompletionItemKind::PROPERTY));

    assert_eq!(labels(&completions(&plugin, "|")), ["modules", "version"]);
    // Another file of the plugin gets nothing.
    let other = swlsp_uriutil::file_uri(&plugin.path("src/Resources/config/services.yaml"));
    let request = request_with_cursor(&other, "|");
    let provider = AdminModuleCompletionProvider::new(plugin.generator.clone());
    assert_eq!(
        provider
            .get_completions(&Context::background(), &request)
            .len(),
        0
    );
}

#[test]
fn names_come_from_the_entities_of_the_plugin() {
    let plugin = Plugin::new();
    let entities = completions(&plugin, "modules:\n  acme-review:\n    entity: |\n");
    assert_eq!(labels(&entities), ["acme_review"]);
    assert_eq!(inserted(&entities, "acme_review"), "acme_review");

    let columns = completions(
        &plugin,
        &format!(
            "{MODULE}    listing:\n      columns:\n        active:\n          enable: false\n        |\n"
        ),
    );
    let found = labels(&columns);
    assert!(
        found.contains(&"title") && found.contains(&"createdAt"),
        "{found:?}"
    );
    assert!(!found.contains(&"active"));
    assert_eq!(inserted(&columns, "title"), "title:\n          ");
    let title = columns.iter().find(|item| item.label == "title").unwrap();
    assert_eq!(title.kind, Some(CompletionItemKind::FIELD));

    let label = completions(&plugin, &format!("{MODULE}    labelField: ti|\n"));
    assert!(labels(&label).contains(&"title"));
    let locales = completions(&plugin, &format!("{MODULE}    title:\n      |\n"));
    assert_eq!(labels(&locales), ["en-GB", "de-DE"]);
    assert_eq!(inserted(&locales, "de-DE"), "de-DE: ");
}

#[test]
fn values_and_component_keys_follow_the_schema() {
    let plugin = Plugin::new();
    let field = format!(
        "{MODULE}    detail:\n      cards:\n        general:\n          fields:\n            title:\n"
    );
    let align = completions(
        &plugin,
        &format!("{MODULE}    listing:\n      columns:\n        title:\n          align: |\n"),
    );
    assert_eq!(labels(&align), ["left", "center", "right"]);
    assert_eq!(align[0].kind, Some(CompletionItemKind::VALUE));
    let enable = completions(&plugin, &format!("{field}              enable: |\n"));
    assert_eq!(labels(&enable), ["true", "false"]);

    let component = format!("{field}              component:\n");
    let keys = completions(&plugin, &format!("{component}                |\n"));
    assert_eq!(labels(&keys), ["type"]);
    let types = completions(&plugin, &format!("{component}                type: |\n"));
    assert_eq!(types.len(), 10);
    assert_eq!(types[0].detail, "A text field.");
    let keys = completions(
        &plugin,
        &format!("{component}                type: number\n                |\n"),
    );
    assert_eq!(labels(&keys), ["numberType", "props"]);
}

#[test]
fn the_place_in_a_flow_mapping_follows_the_braces() {
    let place = at(&format!(
        "{MODULE}    title: {{ en-GB: Reviews, de|: x }}\n"
    ))
    .unwrap();
    assert_eq!(place.path, ["modules", "acme-review", "title"]);
    assert!(place.flow && !place.value && place.complete);
    assert_eq!(place.existing.iter().collect::<Vec<_>>(), ["en-GB"]);
    assert_eq!(u32::from(place.range.len()), 2);

    // Over several lines, and not closed yet.
    let place = at(&format!(
        "{MODULE}    listing: {{\n      auto: false,\n      |"
    ))
    .unwrap();
    assert_eq!(place.path, ["modules", "acme-review", "listing"]);
    assert_eq!(place.existing.iter().collect::<Vec<_>>(), ["auto"]);

    let place = at(&format!(
        "{MODULE}    listing: {{ sorting: {{\"direction\":A|, field: x }} }}\n"
    ));
    let place = place.unwrap();
    assert_eq!(
        place.path,
        ["modules", "acme-review", "listing", "sorting", "direction"]
    );
    assert!(place.value);
    assert_eq!(u32::from(place.range.len()), 1);

    // After the mapping the line is like any other.
    let place = at(&format!("{MODULE}    title: {{ en-GB: Reviews }}\n    |\n")).unwrap();
    assert!(!place.flow);
    assert_eq!(place.path, ["modules", "acme-review"]);
    // A sequence holds nothing the schema knows.
    assert_eq!(at(&format!("{MODULE}    title: [ |]\n")), None);
    assert_eq!(at(&format!("{MODULE}    title: {{ en-GB: a b| }}\n")), None);
}

#[test]
fn a_flow_mapping_is_completed_like_a_block() {
    let plugin = Plugin::new();
    let locales = completions(
        &plugin,
        &format!("{MODULE}    title: {{ en-GB: Reviews, | }}\n"),
    );
    assert_eq!(labels(&locales), ["de-DE"]);
    assert_eq!(inserted(&locales, "de-DE"), "de-DE: ");

    // A key that holds a mapping stays on the line.
    let keys = completions(&plugin, &format!("{MODULE}    listing: {{ | }}\n"));
    assert!(labels(&keys).contains(&"columns"), "{:?}", labels(&keys));
    assert_eq!(inserted(&keys, "columns"), "columns: ");

    let columns = completions(
        &plugin,
        &format!("{MODULE}    listing: {{ columns: {{ title: {{ align: left }}, ac| }} }}\n"),
    );
    assert!(
        labels(&columns).contains(&"active"),
        "{:?}",
        labels(&columns)
    );
    assert!(!labels(&columns).contains(&"title"));
    let align = completions(
        &plugin,
        &format!("{MODULE}    listing: {{ columns: {{ title: {{ align: | }} }} }}\n"),
    );
    assert_eq!(labels(&align), ["left", "center", "right"]);
    let entity = completions(&plugin, "modules: { acme-review: { entity: | } }\n");
    assert_eq!(labels(&entity), ["acme_review"]);
}
