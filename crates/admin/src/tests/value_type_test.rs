#![allow(unused_imports)]
use super::support::*;
use crate::*;

fn definition_for(code: &str) -> ComponentDefinition {
    parse_component_definition_with_line_index(&parse_js(code), &LineIndex::new(code))
}

fn members_by_name(definition: &ComponentDefinition) -> HashMap<String, VueComponentMember> {
    definition
        .members
        .iter()
        .map(|member| (member.name.clone(), member.clone()))
        .collect()
}

#[test]
fn component_definition_retains_state_and_method_types() {
    let code = r#"
export default defineComponent({
    props: {
        fallbackProducts: { type: Array as PropType<Product[]> },
    },
		data() {
		return {
            /** @type {Product[]} */
            products: [],
            rows: [] as Array<{ id: string; label: string }>,
            count: 0,
			options: { active: true, label: 'All' },
			rowsWithRuntimeState: [] as Array<{ id: string }>,
        };
    },
    computed: {
        visibleProducts(): Product[] { return this.products; },
        fallback() { return this.fallbackProducts; },
        isEmpty() { return this.count === 0; },
    },
	methods: {
        save(product: Product): Promise<void> { return repository.save(product); },
		reset() { this.count = 0; },
		markRows() {
			this.rowsWithRuntimeState.forEach((row) => {
				row.deletable = Boolean(row.id);
			});
		},
    },
});"#;
    let definition = definition_for(code);
    let types: HashMap<String, String> = definition
        .members
        .iter()
        .map(|member| {
            (
                format!("{}:{}", member.kind.as_str(), member.name),
                member.type_.clone(),
            )
        })
        .collect();
    assert_eq!(types["data:products"], "Product[]");
    assert_eq!(types["data:rows"], "Array<{ id: string; label: string }>");
    assert_eq!(types["data:count"], "number");
    assert_eq!(types["data:options"], "{ active: boolean; label: string }");
    assert_eq!(types["computed:visibleProducts"], "Product[]");
    assert_eq!(types["computed:fallback"], "Array as PropType<Product[]>");
    assert_eq!(types["computed:isEmpty"], "boolean");
    assert_eq!(types["method:save"], "(product: Product) => Promise<void>");
    assert_eq!(types["method:reset"], "() => unknown");

    let by_name = members_by_name(&definition);
    assert_eq!(
        by_name["visibleProducts"].source_expression,
        "this.products"
    );
    assert_eq!(
        by_name["save"].source_expression,
        "repository.save(product)"
    );
    assert!(by_name["options"].open_runtime_shape);
    assert!(!by_name["rows"].open_runtime_shape);
    assert!(!by_name["products"].open_runtime_shape);
    assert_eq!(by_name["rowsWithRuntimeState"].element_members.len(), 1);
    assert_eq!(
        by_name["rowsWithRuntimeState"].element_members[0],
        VueComponentElementMember {
            name: "deletable".to_owned(),
            type_: "boolean".to_owned(),
            line: 26,
            ..Default::default()
        }
    );
}

#[test]
fn vue_type_members_extract_inline_iterable_shape() {
    let members = vue_type_members("{ id: string; label?: string; count: number }");
    assert_eq!(members.len(), 3);
    let by_name: HashMap<_, _> = members
        .iter()
        .map(|member| (member.name.as_str(), member))
        .collect();
    assert_eq!(by_name["id"].type_, "string");
    assert_eq!(by_name["label"].type_, "string");
    assert_eq!(by_name["count"].type_, "number");
}

#[test]
fn vue_callable_return_type_test() {
    assert_eq!(
        vue_callable_return_type("(id: string) => Entity<'product'> | null"),
        "Entity<'product'> | null"
    );
    assert_eq!(
        vue_callable_return_type("(): EntityCollection<'product'>"),
        "EntityCollection<'product'>"
    );
    assert!(vue_callable_return_type("Entity<'product'>").is_empty());
    assert!(vue_callable_return_type("Function").is_empty());
}

#[test]
fn vue_callable_signature_test() {
    let (parameters, return_type) = vue_callable_signature(
        "(value: string, options?: { locale: string, formats: string[] }, callback?: (result: boolean) => void) => Promise<boolean>",
    )
    .expect("signature");
    assert_eq!(
        parameters,
        vec![
            "value: string",
            "options?: { locale: string, formats: string[] }",
            "callback?: (result: boolean) => void",
        ]
    );
    assert_eq!(return_type, "Promise<boolean>");

    let (parameters, return_type) =
        vue_callable_signature("(value: string, retries?: number): boolean").expect("signature");
    assert_eq!(parameters, vec!["value: string", "retries?: number"]);
    assert_eq!(return_type, "boolean");

    assert!(vue_callable_signature("Function").is_none());
}

#[test]
fn vue_prop_value_type_and_provable_compatibility() {
    assert_eq!(vue_prop_value_type("Number"), "number");
    assert_eq!(
        vue_prop_value_type("Array as unknown as PropType<ReadonlyArray<Row>>"),
        "ReadonlyArray<Row>"
    );
    assert_eq!(vue_prop_value_type("[String, Number]"), "string | number");
    assert_eq!(
        vue_prop_value_type("Object as () => TextEditorLinkMenuConfig"),
        "TextEditorLinkMenuConfig"
    );

    for (expected, actual) in [
        ("Number", "string"),
        ("String", "boolean"),
        ("Array as PropType<Row[]>", "string"),
        ("Object as PropType<Row>", "Array<string>"),
        ("Function", "number"),
        ("string | number", "boolean"),
    ] {
        assert!(
            vue_types_provably_incompatible(expected, actual),
            "{expected} <- {actual}"
        );
    }
    for (expected, actual) in [
        ("Number", "number"),
        ("String", "string | null"),
        ("Function", "() => Entity<'customer_address'>[]"),
        ("Array as PropType<Row[]>", "Array<Product>"),
        ("Object as PropType<Row>", "{ name: string }"),
        (
            "Object as () => TextEditorLinkMenuConfig",
            "{ title: string }",
        ),
        ("Identifier", "string"),
        ("Number", "unknown"),
        (
            "string | object",
            "{ name: string; params: { id: string } }",
        ),
        ("string | number", "number"),
    ] {
        assert!(
            !vue_types_provably_incompatible(expected, actual),
            "{expected} <- {actual}"
        );
    }
}

#[test]
fn vue_type_allows_undefined_test() {
    for value in ["undefined", "void", "string | undefined", "(void) | number"] {
        assert!(vue_type_allows_undefined(value), "{value}");
    }
    for value in ["", "unknown", "null", "string", "{ value: undefined }"] {
        assert!(!vue_type_allows_undefined(value), "{value}");
    }
}

#[test]
fn vue_model_expression_assignable_test() {
    for expression in [
        "enabled",
        "product.active",
        "items[index].name",
        "config[name][locale]",
        "order.addresses.get(order.billingAddressId).phoneNumber",
    ] {
        assert!(vue_model_expression_assignable(expression), "{expression}");
    }
    for expression in [
        "true",
        "getValue()",
        "product?.active",
        "enabled && visible",
        "{ value: enabled }",
    ] {
        assert!(!vue_model_expression_assignable(expression), "{expression}");
    }
}

#[test]
fn vue_expression_text_type_respects_nested_comparisons_and_ternaries() {
    assert_eq!(
        vue_expression_text_type("active === true ? 'active' : 'inactive'", None),
        "string"
    );
    assert_eq!(
        vue_expression_text_type("active ? [first] : []", None),
        "Array"
    );
    assert_eq!(
        vue_expression_text_type("left.value === right.value", None),
        "boolean"
    );
    assert_eq!(vue_expression_text_type("!disabled", None), "boolean");
    assert_eq!(
        vue_expression_text_type("(item) => !item.disabled", None),
        "Function"
    );
    assert!(
        vue_expression_text_type("items.find((item) => item.id === selectedId)", None).is_empty()
    );
}

#[test]
fn component_return_inference_ignores_nested_callback_returns() {
    let code = r#"
export default {
    computed: {
        selected() {
            return this.items.find((item) => {
                return item.id === this.selectedId;
            });
        },
    },
};"#;
    let definition = definition_for(code);
    assert_eq!(definition.members.len(), 1);
    assert_eq!(definition.members[0].name, "selected");
    assert!(definition.members[0].type_.is_empty());
    assert!(
        definition.members[0]
            .source_expression
            .contains("this.items.find")
    );
}

#[test]
fn component_getter_return_inference_ignores_branch_and_callback_returns() {
    let code = r#"
export default {
    computed: {
        selected: {
            get() {
                const found = this.items.find((item) => item.id === this.selectedId);
                if (found) {
                    return [found];
                }
                return [{ id: this.selectedId }];
            },
            set(value) { this.$emit('change', value); },
        },
    },
};"#;
    let definition = definition_for(code);
    assert_eq!(definition.members.len(), 1);
    assert_eq!(definition.members[0].name, "selected");
    assert_eq!(definition.members[0].type_, "Array");
}

#[test]
fn component_return_inference_stays_open_when_any_branch_is_unknown() {
    let code = r#"
export default {
    methods: {
        getOptions(id) {
            if (this.options[id]) {
                return this.options[id];
            }
            return false;
        },
    },
};"#;
    let definition = definition_for(code);
    assert_eq!(definition.members.len(), 1);
    assert_eq!(definition.members[0].type_, "(id) => unknown");
    assert!(definition.members[0].source_expression.is_empty());
}

#[test]
fn component_members_retain_exhaustive_return_alternatives() {
    let code = r#"
export default {
    computed: {
        elementType() { return this.disabled ? 'span' : 'router-link'; },
        selectedForm() {
            if (this.contact) { return 'sw-contact'; }
            return this.runtimeForm;
        },
        inputComponent() {
            switch (this.fieldType) {
                case 'uuid': return 'sw-entity-multi-id-select';
                case 'float':
                case 'int': return 'sw-number-field';
                default: return 'sw-text-field';
            }
        },
        incomplete() {
            if (this.enabled) { return 'sw-card'; }
        },
    },
};"#;
    let by_name = members_by_name(&definition_for(code));
    assert_eq!(
        by_name["elementType"].return_expressions,
        vec!["this.disabled ? 'span' : 'router-link'"]
    );
    assert!(by_name["elementType"].returns_complete);
    assert_eq!(
        by_name["selectedForm"].return_expressions,
        vec!["'sw-contact'", "this.runtimeForm"]
    );
    assert!(by_name["selectedForm"].returns_complete);
    assert_eq!(
        by_name["inputComponent"].return_expressions,
        vec![
            "'sw-entity-multi-id-select'",
            "'sw-number-field'",
            "'sw-text-field'"
        ]
    );
    assert!(by_name["inputComponent"].returns_complete);
    assert_eq!(by_name["incomplete"].return_expressions, vec!["'sw-card'"]);
    assert!(!by_name["incomplete"].returns_complete);
}

#[test]
fn vue_method_returns_complete_control_flow() {
    let tests: &[(&str, &str, bool)] = &[
        ("top-level return", "return value;", true),
        ("semicolonless return", "return value", true),
        (
            "statement after return",
            "return value; consume(value);",
            false,
        ),
        ("nested return", "if (value) { return value; }", false),
        (
            "complete fallthrough switch",
            "switch (value) {
                case 'a':
                case 'b': return first;
                default: throw failure;
            }",
            true,
        ),
        (
            "switch missing default",
            "switch (value) { case 'a': return first; }",
            false,
        ),
        (
            "switch breaks",
            "switch (value) {
                case 'a': break;
                default: return fallback;
            }",
            false,
        ),
        (
            "statement after switch",
            "switch (value) { default: return fallback; }
                consume(value);",
            false,
        ),
        (
            "last switch wins",
            "switch (first) { default: break; }
                switch (second) {
                    case 'a': return first;
                    default: return fallback;
                }",
            true,
        ),
        (
            "final return supersedes switch",
            "switch (value) { default: break; }
                return fallback;",
            true,
        ),
    ];
    for (name, body, complete) in tests {
        let root = parse_js(&format!("export default {{ value() {{{body}}} }};"));
        let methods = jsquery::nodes(&root, &[jssyntax::JS_METHOD]);
        assert_eq!(methods.len(), 1, "{name}");
        assert_eq!(
            vue_method_returns_complete(&methods[0]),
            *complete,
            "{name}"
        );
    }
}

#[test]
fn computed_return_inference_stays_open_across_attribute_fallback() {
    let code = r#"
export default {
    props: { resizeWidth: { type: Boolean } },
    computed: {
		computedMatchReferenceWidth() {
			if ('matchReferenceWidth' in this.$attrs || 'match-reference-width' in this.$attrs) {
				return this.$attrs.matchReferenceWidth ?? this.$attrs['match-reference-width'];
			}
			// Fallback to deprecated prop
			return this.resizeWidth;
        },
    },
};"#;
    let definition = definition_for(code);
    let member = definition
        .members
        .iter()
        .find(|candidate| candidate.name == "computedMatchReferenceWidth")
        .cloned()
        .unwrap_or_default();
    assert!(member.type_.is_empty());
    assert!(member.source_expression.is_empty());
}
