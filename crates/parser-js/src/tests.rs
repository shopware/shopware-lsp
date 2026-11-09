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
