use swlsp_syntax::{SyntaxNode, rowan::NodeOrToken};

use super::*;

fn nodes(root: &SyntaxNode, kind: Kind) -> Vec<SyntaxNode> {
    root.descendants()
        .filter(|node| node.kind() == kind)
        .collect()
}

fn parse_clean(source: &str) -> SyntaxNode {
    let parse = parse(source);
    let root = parse.root();
    assert_eq!(root.text().to_string(), source);
    assert!(
        parse.errors.is_empty(),
        "unexpected errors: {:?}",
        parse.errors
    );
    assert_eq!(root.kind(), JS_PROGRAM);
    root
}

#[test]
fn text_string_matches_the_node_text() {
    use swlsp_syntax::SyntaxNodeExt as _;

    let source =
        "const value = call(first, { key: [1, 2] }) // note\n  .then((result) => result);\n";
    let root = parse(source).root();
    assert_eq!(root.text_string(), source);
    for node in root.descendants() {
        assert_eq!(node.text_string(), node.text().to_string());
    }
}

#[test]
fn administration_component() {
    let root = parse_clean(
        "import template from './sw-card.html.twig';
const { Component } = Shopware;
Component.extend('sw-child', 'sw-parent', {
    template,
    props: { title: { type: String, required: true } },
    methods: { save() { return this.repository.save(); } }
});",
    );
    assert!(!nodes(&root, JS_CALL_EXPRESSION).is_empty());
    assert!(!nodes(&root, JS_OBJECT).is_empty());
}

#[test]
fn export_default_and_dynamic_import() {
    let root = parse_clean(
        "export default Shopware.Component.wrapComponentConfig({
    template: () => import('./component.html.twig'),
    emits: ['save']
});",
    );
    assert_eq!(nodes(&root, JS_EXPORT_DEFAULT).len(), 1);
}

#[test]
fn incomplete_call_is_lossless() {
    let source = "Component.extend('child', 'parent";
    let parse = parse(source);
    assert_eq!(parse.root().text().to_string(), source);
    assert!(!parse.errors.is_empty());
}

#[test]
fn typed_definition_keeps_nested_method_blocks() {
    let root = parse_clean(
        "export default defineComponent({
    computed: {
        label(): string {
            return this.title;
        },
    },
    methods: {
        async save(value: string): Promise<void> {
            if (value) {
                await this.repository.save(value);
            } else {
                for (const item of this.items) {
                    this.queue.push(item);
                }
            }
        },
        reset(): void {
            this.queue = [];
        },
    },
});",
    );
    assert_eq!(nodes(&root, JS_METHOD).len(), 3);
    assert!(nodes(&root, JS_BLOCK).len() >= 6);
}

#[test]
fn typescript_value_assertions_inside_objects() {
    let root = parse_clean(
        "export default defineComponent({
    data() {
        return {
            rows: [] as Array<{ id: string; label: string }>,
            selected: null as Product | null,
            options: {} satisfies Record<string, boolean>,
            count: 0,
        };
    },
    computed: { total(): number { return this.count; } },
});",
    );
    assert!(nodes(&root, JS_PROPERTY).len() >= 5);
    assert_eq!(nodes(&root, JS_METHOD).len(), 2);
}

#[test]
fn inline_object_method_return_type_before_body() {
    let root = parse_clean(
        "export default defineComponent({
    data(): {
        rows: Row[];
        meta: { count: number };
    } {
        return { rows: [], meta: { count: 0 } };
    },
    computed: {
        visibleRows(): Row[] { return this.rows; },
    },
});",
    );
    assert_eq!(nodes(&root, JS_METHOD).len(), 2);
}

#[test]
fn typed_arrow_property_keeps_following_object_sections() {
    let root = parse_clean(
        "const context: CmsPageState = reactive({ currentPage: null });
const store = Shopware.Store.register({
    id: 'cmsPage',
    state: (): CmsPageState => ({ currentPage: null }),
    getters: {
        pageName: (state: CmsPageState): string => state.currentPage?.name ?? '',
    },
    actions: { reset(): void {} },
});",
    );
    assert_eq!(nodes(&root, JS_ARROW_FUNCTION).len(), 2);
    assert_eq!(nodes(&root, JS_METHOD).len(), 1);
    assert!(nodes(&root, JS_OBJECT).len() >= 5);
}

#[test]
fn unary_await_keeps_assignment_rhs_together() {
    let root = parse_clean(
        "export default {
\tmethods: {
\t\tasync load() {
\t\t\tthis.items = await this.repository.search(this.criteria);
\t\t\tthis.selected = await this.repository.get(this.id);
\t\t},
\t},
};",
    );
    let unary = nodes(&root, JS_UNARY_EXPRESSION);
    assert_eq!(unary.len(), 2);
    assert_eq!(
        unary[0].text().to_string().trim(),
        "await this.repository.search(this.criteria)"
    );
}

#[test]
fn computed_array_and_nullish_before_following_methods() {
    let root = parse_clean(
        "export default {
\tcomputed: {
\t\tstatusOptions() {
\t\t\treturn [
\t\t\t\t{ value: 'notSet', label: this.$t('notSet') },
\t\t\t\t{ value: 'direct', label: this.$t('direct') },
\t\t\t\t{ value: 'optIn', label: this.$t('optIn') },
\t\t\t];
\t\t},
\t\tadminEsEnable() {
\t\t\treturn Context.app.adminEsEnable ?? false;
\t\t},
\t},
\tmethods: {
\t\tasync load() {
\t\t\tconst result = await this.repository.search(this.criteria);
\t\t\tthis.items = result;
\t\t},
\t},
};",
    );
    assert_eq!(nodes(&root, JS_METHOD).len(), 3);
}

#[test]
fn debug_tree_and_errors() {
    let parse = parse("a.(");
    assert_eq!(
        parse.debug_dump(),
        "JS_PROGRAM@0..3
  JS_EXPRESSION_STATEMENT@0..3
    JS_CALL_EXPRESSION@0..3
      JS_MEMBER_EXPRESSION@0..2
        JS_IDENTIFIER@0..1
          JS_IDENTIFIER_TOKEN@0..1 \"a\"
        JS_DOT@1..2 \".\"
      JS_ARGUMENT_LIST@2..3
        JS_OPEN_PAREN@2..3 \"(\"
error at 2..3: expected property name but found (
error at 2..3: expected ) but reached end of file"
    );
}

#[test]
fn recovery_is_lossless() {
    // Fuzz corpus seeds plus typical half-typed input.
    for source in [
        "",
        "Component.register(",
        "export default { props: { title: String } }",
        "() => import('./x')",
        "`template ${value}`",
        "}}}",
        "{ a: ",
        "const x = ",
        "if (a",
        "foo(1, 2",
        "obj = { [key: 1 }",
        "a ? b",
        "x as Array<",
        "data(): { a: string",
        "/* unterminated",
        "'unterminated\n",
        "class A extends B { method() { return 1 } ",
        "class A { static options = { a: 1",
        "class A { static ",
        "class A { get value(",
        "class A { = ; * } const after = 1;",
        "#!@ ä ü ö",
        "do { x++ } while (y",
        "try { a } catch (e) { b } finally { c",
        "!",
        "new",
        "{ async *gen() {}, get x() {}, ...rest }",
    ] {
        let parse = parse(source);
        assert_eq!(
            parse.root().text().to_string(),
            source,
            "source: {source:?}"
        );
        let tokens: usize = parse
            .root()
            .descendants_with_tokens()
            .filter(|element| matches!(element, NodeOrToken::Token(_)))
            .count();
        assert!(tokens > 0 || source.is_empty());
    }
}

#[test]
fn class_bodies_hold_methods_and_fields() {
    let root = parse_clean(
        "export default class ExamplePlugin extends Plugin {\n    static options = { selector: '.btn' };\n    count = 0;\n    label;\n\n    init() {\n        this.button = DomAccess.querySelector(this.el, this.options.selector);\n    }\n\n    static async load(url) { return fetch(url); }\n    get value() { return this.count; }\n    onClick = (event) => { this.run(event); };\n    static { register(); }\n}\nconst after = new ExamplePlugin();\n",
    );
    let names = |kind: Kind| -> Vec<String> {
        nodes(&root, kind)
            .iter()
            .filter(|node| {
                node.parent()
                    .and_then(|body| body.parent())
                    .is_some_and(|class| class.kind() == JS_FUNCTION)
            })
            .map(|node| query::property_name(node).unwrap_or_default())
            .collect()
    };
    assert_eq!(names(JS_METHOD), ["init", "load", "value", ""]);
    assert_eq!(names(JS_PROPERTY), ["options", "count", "label", "onClick"]);
    // A method body holds statements: the assignment's call is a call of
    // `DomAccess.querySelector` with both arguments.
    let calls: Vec<String> = nodes(&root, JS_CALL_EXPRESSION)
        .iter()
        .filter_map(query::call_name)
        .collect();
    assert_eq!(
        calls,
        [
            "DomAccess.querySelector",
            "fetch",
            "this.run",
            "register",
            "ExamplePlugin"
        ]
    );
    assert_eq!(nodes(&root, JS_VARIABLE_DECLARATION).len(), 1);
}

#[test]
fn generic_methods_and_functions_keep_their_name_and_body() {
    let root = parse_clean(
        "export default {
    methods: {
        pick<T extends keyof Row, U = Map<string, T>>(row: Row, key: T): Row[T] { return row[key]; },
        async each<T extends (item: Item) => void>(callback: T) {},
        load() {},
    },
};
class Store { get<K extends { id: string }>(key: K): K { return key; } }
function wrap<T>(value: T): T[] { return [value]; }
",
    );
    let methods: Vec<String> = nodes(&root, JS_METHOD)
        .iter()
        .filter_map(crate::query::method_name)
        .collect();
    assert_eq!(methods, ["pick", "each", "load", "get"]);
    assert!(
        nodes(&root, JS_METHOD)
            .iter()
            .all(|method| method.children().any(|child| child.kind() == JS_BLOCK))
    );
    assert_eq!(nodes(&root, JS_FUNCTION).len(), 2);
}

#[test]
fn incomplete_type_parameters_are_lossless() {
    for source in [
        "export default { methods: { pick< } };",
        "export default { methods: { pick<T extends }, data() {} };",
        "function wrap<T",
    ] {
        let parse = parse(source);
        assert_eq!(parse.root().text().to_string(), source);
    }
}

#[test]
fn type_declarations_are_one_node_without_errors() {
    let source = r"export type Payload = {
    id: string;
    limit?: number | null;
    tags: Array<{ name: string }>;
};

type Short = string | null
const after = 1;

interface Options<T> extends Base {
    value?: T;
}

export type { Other } from './other';

declare global {
    interface ServiceContainer {
        exampleService: ExampleService;
    }
}

const type = 'x';
type.length;
";
    let parse = parse(source);
    assert_eq!(parse.root().text().to_string(), source);
    assert!(parse.errors.is_empty(), "{:?}", parse.errors);
    let root = parse.root();
    let declarations: Vec<String> = root
        .children()
        .filter(|node| node.kind() == JS_TYPE_DECLARATION)
        .map(|node| {
            node.text()
                .to_string()
                .trim()
                .lines()
                .next()
                .unwrap_or("")
                .to_owned()
        })
        .collect();
    assert_eq!(
        declarations,
        [
            "export type Payload = {",
            "type Short = string | null",
            "interface Options<T> extends Base {",
            "export type { Other } from './other';",
            "declare global {",
        ]
    );
    // The members of an object type are found like those of a value.
    let members: Vec<String> = nodes(&root, JS_PROPERTY)
        .iter()
        .filter_map(|property| property.children().next())
        .map(|name| name.text().to_string().trim().to_owned())
        .collect();
    assert_eq!(
        members,
        ["id", "limit", "tags", "name", "value", "exampleService"]
    );
    // What follows a declaration is still a statement of its own.
    assert_eq!(
        root.children()
            .filter(|node| node.kind() == JS_VARIABLE_DECLARATION)
            .count(),
        2
    );
}

#[test]
fn incomplete_type_declarations_are_lossless() {
    for source in [
        "type A =",
        "export type",
        "interface A {",
        "declare global {",
        "type A = {",
    ] {
        let parse = parse(source);
        assert_eq!(parse.root().text().to_string(), source);
    }
}

#[test]
fn method_return_types_hold_nested_type_arguments() {
    let source = r"class Service {
    async load(id: string): Promise<string[] | Record<string, Record<string, unknown>>> {
        return this.client.get(id);
    }

    pair(): [string, number] {
        return ['a', 1];
    }

    after(): void {}
}
";
    let parse = parse(source);
    assert_eq!(parse.root().text().to_string(), source);
    assert!(parse.errors.is_empty(), "{:?}", parse.errors);
    let methods = nodes(&parse.root(), JS_METHOD);
    assert_eq!(methods.len(), 3);
    assert!(
        methods
            .iter()
            .all(|method| method.children().any(|child| child.kind() == JS_BLOCK))
    );
}
