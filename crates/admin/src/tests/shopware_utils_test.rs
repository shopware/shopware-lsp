#![allow(unused_imports)]
use super::support::*;
use crate::*;

fn node_at(root: &SyntaxNode, offset: usize) -> SyntaxNode {
    root.node_at(TextSize::from(offset as u32))
        .expect("node at offset")
}

fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_owned()).collect()
}

#[test]
fn javascript_shopware_utils_member_tracks_nested_receivers() {
    let source = "
Shopware.Utils.createId();
Shopware.Utils.format.date('2026-01-01');
Shopware.Utils.EventBus.;
other.Utils.format.date();
";
    let root = parse_js(source);
    type Case<'a> = (&'a str, Option<(Vec<String>, &'a str)>);
    let tests: [Case<'_>; 4] = [
        ("createId", Some((vec![], "createId"))),
        ("date('2026", Some((strings(&["format"]), "date"))),
        ("EventBus.;", Some((strings(&["EventBus"]), ""))),
        ("other.Utils", None),
    ];
    for (needle, expected) in tests {
        let mut offset = source.find(needle).expect(needle);
        if needle == "EventBus.;" {
            offset += "EventBus.".len() - 1;
        } else {
            offset += 1;
        }
        let actual = javascript_shopware_utils_member(&node_at(&root, offset));
        let expected = expected.map(|(receiver, member)| (receiver, member.to_owned()));
        assert_eq!(actual, expected, "{needle}");
    }
}

#[test]
fn javascript_shopware_utils_member_supports_lexical_aliases_and_bindings() {
    let source = "
const utils = Shopware.Utils;
const format = Shopware.Utils.format;
const { types } = Shopware.Utils;
const { cloneDeep } = Shopware.Utils.object;
const { chunk: chunkArray } = Shopware.Utils.array;

function run() {
    utils.debug.warn('module alias');
    format.date('2026-01-01');
    types.isEmpty({});
    cloneDeep({});
    chunkArray([], 2);
    if (enabled) {
        const format = Shopware.Utils.string;
        format.camelCase('inner');
    }
    format.currency(10);
}

function mutableScope() {
    let format = Shopware.Utils.format;
    format.date('mutable');
}

function shadowedParameter(format) {
    format.date('parameter');
}
";
    let root = parse_js(source);
    let tests: [(&str, Option<(&str, &str)>); 9] = [
        ("warn('module", Some(("debug", "warn"))),
        ("date('2026", Some(("format", "date"))),
        ("isEmpty", Some(("types", "isEmpty"))),
        ("cloneDeep({", Some(("object", "cloneDeep"))),
        ("chunkArray([],", Some(("array", "chunk"))),
        ("camelCase", Some(("string", "camelCase"))),
        ("currency", Some(("format", "currency"))),
        ("date('mutable", None),
        ("date('parameter", None),
    ];
    for (needle, expected) in tests {
        let offset = source.find(needle).expect(needle);
        let actual = javascript_shopware_utils_member(&node_at(&root, offset + 1));
        let expected =
            expected.map(|(receiver, member)| (vec![receiver.to_owned()], member.to_owned()));
        assert_eq!(actual, expected, "{needle}");
    }

    let renamed_offset = source.find("chunkArray([],").unwrap();
    let renamed_node = node_at(&root, renamed_offset + 1);
    let name_node = javascript_shopware_utils_member_name_node(&renamed_node).expect("name node");
    assert_eq!(name_node.text().to_string().trim(), "chunkArray");
}

#[test]
fn javascript_shopware_event_bus_event_at_supports_aliases() {
    let mut source = "
Shopware.Utils.EventBus.on('direct-event', handler);
const { EventBus } = Shopware.Utils;
EventBus.off('destructured-event', handler);
const bus = Shopware.Utils.EventBus;
bus.emit('aliased-event', payload);
bus.emit(('parenthesized-event'), payload);
export default Shopware.Component.wrapComponentConfig({
    methods: {
        save(): void {
            // eslint-disable-next-line @typescript-eslint/unbound-method
            EventBus.emit('nested-method-event', payload);
            // direct call comment
            Shopware.Utils.EventBus.emit('nested-direct-comment-event', payload);
        },
        shadowed(EventBus): void {
            EventBus.emit('shadowed-method-event', payload);
        },
    },
});
other.EventBus.emit('unrelated-event');
Shopware.Utils.EventBus.all.get('not-an-operation');
Shopware.Utils.EventBus.emit(enabled ? 'conditional-event' : 'fallback-event');
"
    .to_owned();
    source.push_str("Shopware.Utils.EventBus.emit(`dynamic-${name}`);\n");
    let root = parse_js(&source);
    for (name, operation) in [
        ("direct-event", Some("on")),
        ("destructured-event", Some("off")),
        ("aliased-event", Some("emit")),
        ("parenthesized-event", Some("emit")),
        ("nested-method-event", Some("emit")),
        ("nested-direct-comment-event", Some("emit")),
        ("shadowed-method-event", None),
        ("unrelated-event", None),
        ("not-an-operation", None),
        ("conditional-event", None),
        ("dynamic-${name}", None),
    ] {
        let offset = source.find(name).expect(name);
        let actual = javascript_shopware_event_bus_event_at(&node_at(&root, offset + 1));
        let expected = operation.map(|operation| (operation.to_owned(), name.to_owned()));
        assert_eq!(actual, expected, "{name}");
    }
}

#[test]
fn resolve_shopware_utils_follows_value_exports_and_default_imports() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    let util_path = path_string(&admin_root, "core/service/util.service.ts");
    let debug_path = path_string(&admin_root, "core/service/utils/debug.utils.ts");
    let string_path = path_string(&admin_root, "core/service/utils/string.utils.ts");
    let event_bus_path = path_string(&admin_root, "core/service/utils/eventBus.utils.ts");
    for (path, source) in [
        (
            &util_path,
            "
import { warn, error } from './utils/debug.utils';
import stringUtils from './utils/string.utils';
import EventBus from './utils/eventBus.utils';
export const debug = { warn, error };
export const string = { camelCase: stringUtils.camelCase, isUrl: stringUtils.isUrl };
export default { createId, debug, string, EventBus };
function createId(): string { return 'id'; }
",
        ),
        (
            &debug_path,
            "
export default { warn, error };
export function warn(name: string, ...message: any[]): void {}
export function error(name: string, ...message: any[]): void {}
",
        ),
        (
            &string_path,
            "
import camelCase from 'lodash-es/camelCase';
export default { camelCase, isUrl };
function isUrl(value: string): boolean { return true; }
",
        ),
        (
            &event_bus_path,
            "
interface Events extends Record<string | symbol, unknown> { save: string; }
const emitter = mitt<Events>();
export default emitter;
",
        ),
    ] {
        index_source(&idx, path, source);
    }
    let consumer_path = path_string(&admin_root, "module/example/index.ts");

    let root_shape = idx.resolve_shopware_utils("", &consumer_path).unwrap();
    assert!(root_shape.complete);
    let names = vue_member_names(&root_shape.members);
    for name in ["createId", "debug", "string", "EventBus"] {
        assert!(names.contains(&name.to_owned()), "{name}");
    }
    let create_id = twig_vue_member_named(&root_shape.members, "createId").expect("createId");
    assert_eq!(create_id.type_, "() => string");
    assert_eq!(create_id.definition_path, util_path);
    assert!(create_id.definition_range.identifier);

    let debug_shape = idx.resolve_shopware_utils("debug", &consumer_path).unwrap();
    assert!(debug_shape.complete);
    let warn = twig_vue_member_named(&debug_shape.members, "warn").expect("warn");
    assert_eq!(warn.type_, "(name: string, ...message: any[]) => void");
    assert_eq!(warn.definition_path, util_path);

    let string_shape = idx
        .resolve_shopware_utils("string", &consumer_path)
        .unwrap();
    assert!(string_shape.complete);
    let camel_case = twig_vue_member_named(&string_shape.members, "camelCase").expect("camelCase");
    assert_eq!(camel_case.type_, "Function");
    let is_url = twig_vue_member_named(&string_shape.members, "isUrl").expect("isUrl");
    assert_eq!(is_url.type_, "(value: string) => boolean");

    let event_bus_shape = idx
        .resolve_shopware_utils("EventBus", &consumer_path)
        .unwrap();
    assert!(!event_bus_shape.complete);
    let names = vue_member_names(&event_bus_shape.members);
    for name in ["all", "emit", "off", "on"] {
        assert!(names.contains(&name.to_owned()), "{name}");
    }
    let emit = twig_vue_member_named(&event_bus_shape.members, "emit").expect("emit");
    assert!(emit.type_.contains("keyof Events"));
    let events = idx
        .resolve_shopware_event_bus_events(&consumer_path)
        .unwrap();
    assert!(!events.complete);
    let save_event = twig_vue_member_named(&events.members, "save").expect("save");
    assert_eq!(save_event.type_, "string");
    assert_eq!(save_event.definition_path, event_bus_path);
    assert!(save_event.definition_range.identifier);

    let (resolved, found) = idx
        .resolve_component_expression_type(
            &VueComponent::default(),
            "Shopware.Utils.createId()",
            &consumer_path,
        )
        .unwrap();
    assert!(found);
    assert_eq!(resolved.type_, "string");
}
