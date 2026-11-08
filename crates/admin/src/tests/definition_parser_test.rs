#![allow(unused_imports)]
use super::support::*;
use crate::*;

fn definition(code: &str) -> ComponentDefinition {
    parse_component_definition(&parse_js(code), code)
}

fn definition_with_line_index(code: &str) -> ComponentDefinition {
    parse_component_definition_with_line_index(&parse_js(code), &LineIndex::new(code))
}

fn sorted(mut values: Vec<String>) -> Vec<String> {
    values.sort();
    values
}

fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_owned()).collect()
}

fn line_slice(code: &str, range: &AdminSourceRange) -> String {
    let line = code.split('\n').nth(range.start_line as usize).unwrap();
    line[range.start_character as usize..range.end_character as usize].to_owned()
}

#[test]
fn parse_component_definition_props() {
    let code = r#"
export default {
    props: {
        /**
         * @description Text shown in the card header.
         * It may contain a translated label.
         * @type {String}
         * @required true
         */
        title: {
            type: String,
            required: true,
        },
        count: {
            type: Number,
            default: 0,
        },
        active: Boolean,
    },
};
"#;
    let def = definition(code);
    assert_eq!(def.props.len(), 3);
    assert_eq!(def.props[0].name, "title");
    assert_eq!(def.props[0].type_, "String");
    assert_eq!(
        def.props[0].documentation,
        "Text shown in the card header.\nIt may contain a translated label."
    );
    assert!(def.props[0].required);
    let title_start = code.find("title:").unwrap() as u32;
    let (start_line, start_character) = LineIndex::new(code).position_utf16(title_start);
    assert_eq!(def.props[0].name_range.start_line, start_line);
    assert_eq!(def.props[0].name_range.start_character, start_character);
    assert_eq!(
        def.props[0].name_range.end_character,
        start_character + "title".len() as u32
    );
    assert!(def.props[0].name_range.declaration);
    assert!(def.props[0].name_range.identifier);

    assert_eq!(def.props[1].name, "count");
    assert_eq!(def.props[1].type_, "Number");
    assert!(!def.props[1].required);
    assert_eq!(def.props[1].default, "0");

    assert_eq!(def.props[2].name, "active");
    assert_eq!(def.props[2].type_, "Boolean");
}

#[test]
fn javascript_documentation_ignores_contract_metadata() {
    let code = r#"export default {
    props: {
        /** Plain description. */
        plain: String,
        /**
         * @deprecated Use modern instead.
         * @type {String}
         */
        legacy: String,
    },
};"#;
    let def = definition(code);
    assert_eq!(def.props.len(), 2);
    assert_eq!(def.props[0].documentation, "Plain description.");
    assert!(def.props[1].documentation.is_empty());
    assert_eq!(def.props[1].deprecated, "Use modern instead.");
}

#[test]
fn javascript_documentation_on_array_style_prop() {
    let code = r#"export default {
    props: [
        /** Label displayed by the component. */
        'label',
    ],
};"#;
    let def = definition(code);
    assert_eq!(def.props.len(), 1);
    assert_eq!(def.props[0].name, "label");
    assert_eq!(
        def.props[0].documentation,
        "Label displayed by the component."
    );
}

#[test]
fn javascript_event_annotations_enrich_explicit_contract() {
    let code = r#"/**
 * @event save { Product: product } Fired after the product was saved.
 * @event closeModal (void)
 */
export default {
    emits: ['save', 'modal-close'],
};"#;
    let def = definition(code);
    assert_eq!(def.events.len(), 2);
    let save = component_definition_event(&def.events, "save").expect("save");
    assert_eq!(save.type_, "Product");
    assert_eq!(save.documentation, "Fired after the product was saved.");
    assert!(save.name_range.declaration);
    assert_ne!(save.line, 0);
    assert!(
        component_definition_event(&def.events, "close-modal").is_none(),
        "an explicit emits declaration rejects stale annotations"
    );
    assert!(component_definition_event(&def.events, "modal-close").is_some());
}

#[test]
fn javascript_event_annotations_provide_constant_emit_fallback() {
    let code = r#"/**
 * @event media-upload-add { UploadTask[]: data }
 * @event media-upload-fail UploadTask UploadTask
 */
export default {
    methods: {
        notify(payload) { this.$emit(UploadEvents.UPLOAD_ADDED, payload); },
    },
};"#;
    let def = definition(code);
    for (name, event_type) in [
        ("media-upload-add", "UploadTask[]"),
        ("media-upload-fail", "UploadTask"),
    ] {
        let event = component_definition_event(&def.events, name).expect(name);
        assert_eq!(event.type_, event_type, "{name}");
        assert!(event.name_range.declaration, "{name}");
    }
}

#[test]
fn event_declaration_documentation() {
    let code = r#"export default {
    emits: {
        /** Fired after validation succeeds. */
        valid: null,
    },
};"#;
    let def = definition(code);
    let event = component_definition_event(&def.events, "valid").expect("valid");
    assert_eq!(event.documentation, "Fired after validation succeeds.");
}

#[test]
fn parse_component_definition_prop_allowed_values() {
    let code = r#"
export default {
    props: {
        variant: {
            type: String,
            default: '',
            validValues: ['primary', 'secondary'],
        },
        runtimeValues: {
            type: String,
            validValues: ['known', importedValue],
        },
    },
};"#;
    let def = definition(code);
    assert_eq!(def.props.len(), 2);
    let (values, complete) = vue_prop_allowed_values(&def.props[0]);
    assert!(complete);
    assert_eq!(values, strings(&["", "primary", "secondary"]));
    let (values, complete) = vue_prop_allowed_values(&def.props[1]);
    assert!(!complete);
    assert_eq!(values, strings(&["known"]));
}

#[test]
fn parse_component_definition_prop_validator_allowed_values() {
    let code = r#"
export default {
    props: {
        navigationType: {
            type: String,
            default: 'arrow',
            validator(value) {
                return ['arrow', 'button', 'all'].includes(value);
            },
        },
        arrowStyle: {
            type: String,
            validator: (candidate) => ['inside', 'outside', 'none'].includes(candidate),
        },
        dynamicValues: {
            type: String,
            validator(value) {
                return importedValues.includes(value);
            },
        },
        conditionalValues: {
            type: String,
            validator(value) {
                return ['known'].includes(value) || isExtensionValue(value);
            },
        },
        mixedValues: {
            validator(value) {
                return ['known', importedValue].includes(value);
            },
        },
        mode: {
            validator(value) {
                return ['view', 'edit', 'create'].includes(value);
            },
        },
    },
};"#;
    let def = definition(code);
    assert_eq!(def.props.len(), 6);
    let (values, complete) = vue_prop_allowed_values(&def.props[0]);
    assert!(complete);
    assert_eq!(values, strings(&["arrow", "button", "all"]));
    let (values, complete) = vue_prop_allowed_values(&def.props[1]);
    assert!(complete);
    assert_eq!(values, strings(&["inside", "outside", "none"]));
    for prop in &def.props[2..4] {
        let (values, complete) = vue_prop_allowed_values(prop);
        assert!(!complete, "{}", prop.name);
        assert!(values.is_empty(), "{}", prop.name);
    }
    let (values, complete) = vue_prop_allowed_values(&def.props[4]);
    assert!(!complete);
    assert_eq!(values, strings(&["known"]));
    assert!(def.props[4].type_.is_empty());
    assert_eq!(def.props[5].type_, "String");
    let (values, complete) = vue_prop_allowed_values(&def.props[5]);
    assert!(complete);
    assert_eq!(values, strings(&["view", "edit", "create"]));
}

#[test]
fn parse_component_definition_prop_validator_empty_string_guard() {
    let code = r#"
export default {
    props: {
        variant: {
            type: String,
            validator(value) {
                if (!value.length) {
                    return true;
                }
                return ['primary', 'secondary'].includes(value);
            },
        },
        arrowVariant: {
            type: String,
            validator: (candidate) => {
                if (!candidate.length) {
                    return true;
                }
                return ['inside', 'outside'].includes(candidate);
            },
        },
        untypedVariant: {
            validator(value) {
                if (!value.length) {
                    return true;
                }
                return ['known'].includes(value);
            },
        },
        compoundGuard: {
            type: String,
            validator(value) {
                if (!value.length || isExtensionValue(value)) {
                    return true;
                }
                return ['known'].includes(value);
            },
        },
        guardedSideEffect: {
            type: String,
            validator(value) {
                if (!value.length) {
                    observe(value);
                    return true;
                }
                return ['known'].includes(value);
            },
        },
    },
};"#;
    let def = definition(code);
    assert_eq!(def.props.len(), 5);
    for index in [0, 1] {
        let (values, complete) = vue_prop_allowed_values(&def.props[index]);
        assert!(complete, "{}", def.props[index].name);
        assert!(values.contains(&String::new()), "{}", def.props[index].name);
    }
    let (values, _) = vue_prop_allowed_values(&def.props[0]);
    assert_eq!(
        sorted(values),
        sorted(strings(&["primary", "secondary", ""]))
    );
    let (values, _) = vue_prop_allowed_values(&def.props[1]);
    assert_eq!(sorted(values), sorted(strings(&["inside", "outside", ""])));
    for prop in &def.props[2..] {
        let (values, complete) = vue_prop_allowed_values(prop);
        assert!(!complete, "{}", prop.name);
        assert!(values.is_empty(), "{}", prop.name);
    }
}

#[test]
fn parse_component_definition_prop_validator_local_const_values() {
    let code = r#"
export default {
    props: {
        variant: {
            type: String,
            validator(value: string) {
                const variants = ['gallery', 'detail', 'listing'];
                return variants.includes(value);
            },
        },
        arrowVariant: {
            type: String,
            validator: (candidate) => {
                const variants = ['small', 'large'];
                return variants.includes(candidate);
            },
        },
        inferredVariant: {
            validator(value) {
                const variants = ['view', 'edit'];
                return variants.includes(value);
            },
        },
        guardedVariant: {
            type: String,
            validator(value) {
                if (!value.length) {
                    return true;
                }
                const variants = ['primary', 'secondary'];
                return variants.includes(value);
            },
        },
        mixedVariant: {
            validator(value) {
                const variants = ['known', importedValue];
                return variants.includes(value);
            },
        },
        letVariant: {
            type: String,
            validator(value) {
                let variants = ['known'];
                return variants.includes(value);
            },
        },
        varVariant: {
            type: String,
            validator(value) {
                var variants = ['known'];
                return variants.includes(value);
            },
        },
        importedVariant: {
            type: String,
            validator(value) {
                const variants = importedVariants;
                return variants.includes(value);
            },
        },
        reassignedVariant: {
            type: String,
            validator(value) {
                const variants = ['known'];
                variants = importedVariants;
                return variants.includes(value);
            },
        },
        mutatedVariant: {
            type: String,
            validator(value) {
                const variants = ['known'];
                variants.push('extension');
                return variants.includes(value);
            },
        },
        wrongReceiver: {
            type: String,
            validator(value) {
                const variants = ['known'];
                return importedVariants.includes(value);
            },
        },
        compoundReturn: {
            type: String,
            validator(value) {
                const variants = ['known'];
                return variants.includes(value) || isExtensionValue(value);
            },
        },
    },
};"#;
    let def = definition(code);
    assert_eq!(def.props.len(), 12);
    let (values, complete) = vue_prop_allowed_values(&def.props[0]);
    assert!(complete);
    assert_eq!(values, strings(&["gallery", "detail", "listing"]));
    let (values, complete) = vue_prop_allowed_values(&def.props[1]);
    assert!(complete);
    assert_eq!(values, strings(&["small", "large"]));
    let (values, complete) = vue_prop_allowed_values(&def.props[2]);
    assert!(complete);
    assert_eq!(values, strings(&["view", "edit"]));
    assert_eq!(def.props[2].type_, "String");
    let (values, complete) = vue_prop_allowed_values(&def.props[3]);
    assert!(complete);
    assert_eq!(
        sorted(values),
        sorted(strings(&["primary", "secondary", ""]))
    );
    let (values, complete) = vue_prop_allowed_values(&def.props[4]);
    assert!(!complete);
    assert_eq!(values, strings(&["known"]));
    assert!(def.props[4].type_.is_empty());
    for prop in &def.props[5..] {
        let (values, complete) = vue_prop_allowed_values(prop);
        assert!(!complete, "{}", prop.name);
        assert!(values.is_empty(), "{}", prop.name);
    }
}

#[test]
fn parse_component_definition_preserves_typescript_prop_types() {
    let code = r#"
export default defineComponent({
    props: {
        editLink: {
            type: Object as PropType<RouteLocationNamedRaw | null>,
            required: false,
            default: null,
        },
        values: {
            type: Array as unknown as PropType<Array<Record<string, number>>>,
            required: true,
        },
    },
});"#;
    let def = definition(code);
    assert_eq!(def.props.len(), 2);
    assert_eq!(
        def.props[0].type_,
        "Object as PropType<RouteLocationNamedRaw | null>"
    );
    assert_eq!(def.props[0].default, "null");
    assert_eq!(
        def.props[1].type_,
        "Array as unknown as PropType<Array<Record<string, number>>>"
    );
    assert!(def.props[1].required);
}

#[test]
fn parse_component_definition_props_array() {
    let code = r#"
export default {
    props: ['title', 'count', 'active'],
};
"#;
    let def = definition(code);
    assert_eq!(def.props.len(), 3);
    assert_eq!(def.props[0].name, "title");
    assert_eq!(def.props[1].name, "count");
    assert_eq!(def.props[2].name, "active");
    assert!(!def.props[0].name_range.identifier);
    assert!(def.props[0].name_range.declaration);
    assert_eq!(
        def.props[0].name_range.end_character - def.props[0].name_range.start_character,
        "title".len() as u32
    );
    assert_eq!(line_slice(code, &def.props[0].name_range), "title");
}

#[test]
fn parse_component_definition_legacy_model_contract() {
    let code = r#"
export default {
    model: { prop: 'selection', event: 'selection-change' },
    props: { selection: { type: Array, required: true } },
    emits: ['selection-change'],
};"#;
    let def = definition_with_line_index(code);
    assert_eq!(def.model_prop, "selection");
    assert_eq!(def.model_event, "selection-change");

    let code = r#"export default { model: {}, props: ['value'], emits: ['input'] };"#;
    let def = definition_with_line_index(code);
    assert_eq!(def.model_prop, "value");
    assert_eq!(def.model_event, "input");
}

#[test]
fn parse_component_definition_define_component_with_typed_methods() {
    let code = r#"
export default defineComponent({
    props: {
        title: { type: String, required: true },
    },
    data(): CardData {
        return { isLoading: false };
    },
    computed: {
        label(): string {
            return this.title;
        },
    },
    methods: {
        async save(value: string): Promise<void> {
            if (value) {
                await this.repository.save(value);
            }
        },
        reset(): void {
            this.isLoading = false;
        },
    },
});"#;
    let def = definition_with_line_index(code);
    assert_eq!(def.props.len(), 1);
    assert_eq!(def.props[0].name, "title");
    assert!(def.props[0].required);
    assert_eq!(def.data, strings(&["isLoading"]));
    assert_eq!(def.computed, strings(&["label"]));
    assert_eq!(def.methods, strings(&["save", "reset"]));
}

fn members_by_name(definition: &ComponentDefinition) -> HashMap<String, VueComponentMember> {
    definition
        .members
        .iter()
        .map(|member| (member.name.clone(), member.clone()))
        .collect()
}

#[test]
fn parse_component_definition_setup_return_members() {
    let code = "
export default Shopware.Component.wrapComponentConfig({
    setup() {
        const count = ref<number>(0);
        const rawTitle: Ref<string> = ref('Fallback');
        const products = computed((): Product[] => Shopware.Store.get('product').items);
        const summary = computed(() => ({ total: count.value }));
        const save = async (id: string): Promise<void> => persist(id);
        function reset(force: boolean): boolean { return force; }
\t\tconst options = { handler: () => true };
        const hidden = ref(false);
        onMounted(() => {
            const nested = ref('not public');
            return nested;
        });
        return {
            count,
            title: rawTitle,
            products,
            summary,
            save,
            reset,
\t\t\toptions,
        };
    },
});";
    let def = definition_with_line_index(code);
    let members = members_by_name(&def);
    assert_eq!(members.len(), 7);
    assert_eq!(members["count"].kind, VueComponentMemberKind::Data);
    assert_eq!(members["count"].type_, "number");
    assert_eq!(members["count"].binding_name, "count");
    assert!(members["count"].shorthand);
    assert!(
        members["count"].name_range.end_character > members["count"].name_range.start_character
    );
    assert_eq!(members["title"].kind, VueComponentMemberKind::Data);
    assert_eq!(members["title"].type_, "string");
    assert_eq!(members["title"].binding_name, "rawTitle");
    assert!(!members["title"].shorthand);
    assert!(
        members["title"].name_range.end_character > members["title"].name_range.start_character
    );
    assert_eq!(members["products"].kind, VueComponentMemberKind::Computed);
    assert_eq!(members["products"].type_, "Product[]");
    assert_eq!(members["summary"].kind, VueComponentMemberKind::Computed);
    assert_eq!(members["summary"].type_, "Object");
    assert_eq!(members["save"].kind, VueComponentMemberKind::Method);
    assert_eq!(members["save"].type_, "(id: string) => Promise<void>");
    assert_eq!(members["reset"].kind, VueComponentMemberKind::Method);
    assert_eq!(members["reset"].type_, "(force: boolean) => boolean");
    assert_eq!(members["options"].kind, VueComponentMemberKind::Data);
    assert_eq!(members["options"].type_, "Object");
    assert!(!members.contains_key("hidden"));
    assert!(!members.contains_key("nested"));
}

#[test]
fn parse_component_definition_setup_merges_static_return_branches() {
    let code = "export default defineComponent({
\tsetup() {
\t\tconst template = computed(() => 'ready');
\t\tif (disabled) return { template: null };
\t\treturn { template };
\t},
});";
    let def = definition_with_line_index(code);
    assert_eq!(def.members.len(), 1);
    assert_eq!(def.members[0].name, "template");
    assert_eq!(def.members[0].kind, VueComponentMemberKind::Computed);
    assert_eq!(def.members[0].type_, "string");
}

#[test]
fn parse_component_definition_expression_setup() {
    let code = "export default defineComponent({
\tsetup: () => ({ ready: true, label: 'Ready' }),
});";
    let def = definition_with_line_index(code);
    assert_eq!(def.members.len(), 2);
    assert_eq!(def.members[0].name, "ready");
    assert_eq!(def.members[0].type_, "boolean");
    assert_eq!(def.members[1].name, "label");
    assert_eq!(def.members[1].type_, "string");
}

#[test]
fn parse_component_definition_ignores_dynamic_setup_return_objects() {
    let code = "export default defineComponent({
\tsetup: (props, context) => createExtendableSetup(
\t\t{ props, context, name: 'sw-card' },
\t\t() => ({ internalState: ref(false) }),
\t),
});";
    assert!(definition_with_line_index(code).members.is_empty());

    let code = "export default defineComponent({
\t\tsetup() { return createState({ internalState: true }); },
\t});";
    assert!(definition_with_line_index(code).members.is_empty());
}

#[test]
fn parse_component_definition_retains_direct_instance_assignments() {
    let code = "export default {
\tdata() { return { mediaItems: [], selectedMedia: null }; },
\tmethods: {
\t\tasync load() {
\t\t\tthis.mediaItems = await this.mediaRepository.search(this.criteria);
\t\t\tif (this.selectedId) {
\t\t\t\tthis.selectedMedia = await this.mediaRepository.get(this.selectedId);
\t\t\t}
\t\t\tthis.mediaItems = [];
\t\t\tthis.selectedMedia.name = 'ignored';
\t\t\tthis.page += 1;
\t\t\tthis.ready === true;
\t\t},
\t},
};";
    let def = definition_with_line_index(code);
    assert_eq!(def.assignments.len(), 3);
    assert_eq!(
        def.assignments[0],
        VueComponentAssignment {
            target: "mediaItems".to_owned(),
            expression: "await this.mediaRepository.search(this.criteria)".to_owned(),
            line: 5,
            ..Default::default()
        }
    );
    assert_eq!(def.assignments[1].target, "selectedMedia");
    assert_eq!(
        def.assignments[1].expression,
        "await this.mediaRepository.get(this.selectedId)"
    );
    assert_eq!(def.assignments[2].expression, "[]");
}

fn assignment_expressions(definition: &ComponentDefinition) -> HashMap<String, String> {
    definition
        .assignments
        .iter()
        .map(|assignment| (assignment.target.clone(), assignment.expression.clone()))
        .collect()
}

#[test]
fn component_assignment_resolves_visible_const_initializer() {
    let code = "export default {
\tdata() {
\t\treturn { items: [], nestedItems: [], afterItems: [], mutableItems: [] };
\t},
\tmethods: {
\t\tasync load() {
\t\t\tconst result = await this.mediaRepository.search(this.criteria);
\t\t\tthis.items = result;
\t\t\tif (this.includeFolders) {
\t\t\t\tconst result = await this.folderRepository.search(this.criteria);
\t\t\t\tthis.nestedItems = result;
\t\t\t}
\t\t\tthis.afterItems = result;
\t\t\tlet mutable = await this.productRepository.search(this.criteria);
\t\t\tthis.mutableItems = mutable;
\t\t},
\t\tother() {
\t\t\tthis.unrelatedItems = result;
\t\t},
\t},
};";
    let expressions = assignment_expressions(&definition_with_line_index(code));
    assert_eq!(
        expressions["items"],
        "await this.mediaRepository.search(this.criteria)"
    );
    assert_eq!(
        expressions["nestedItems"],
        "await this.folderRepository.search(this.criteria)"
    );
    assert_eq!(
        expressions["afterItems"],
        "await this.mediaRepository.search(this.criteria)"
    );
    assert_eq!(expressions["mutableItems"], "mutable");
    assert_eq!(expressions["unrelatedItems"], "result");
}

#[test]
fn component_assignment_resolves_const_inside_try_finally() {
    let code = "export default {
\tdata() { return { items: null }; },
\tcomputed: {
\t\tnewsletterRecipientRepository() {
\t\t\treturn this.repositoryFactory.create('newsletter_recipient');
\t\t},
\t},
\tmethods: {
\t\tasync getList() {
\t\t\tif (this.adminEsEnable) {
\t\t\t\tthis.criteria.setTerm(this.term);
\t\t\t} else {
\t\t\t\tthis.criteria = await this.addQueryScores(this.term, this.criteria);
\t\t\t}
\t\t\ttry {
\t\t\t\tconst searchResult = await this.newsletterRecipientRepository.search(this.criteria);
\t\t\t\tthis.items = searchResult;
\t\t\t\tthis.total = searchResult.total;
\t\t\t} finally {
\t\t\t\tthis.isLoading = false;
\t\t\t}
\t\t},
\t},
};";
    let def = definition_with_line_index(code);
    let items = def
        .assignments
        .iter()
        .find(|assignment| assignment.target == "items")
        .cloned()
        .unwrap_or_default();
    assert_eq!(
        items.expression,
        "await this.newsletterRecipientRepository.search(this.criteria)"
    );
}

#[test]
fn component_assignment_resolves_promise_then_parameter() {
    let code = "export default {
\tdata() { return { items: null, selected: null }; },
\tmethods: {
\t\tload() {
\t\t\tthis.repository.search(this.criteria).then((result: SearchResult) => {
\t\t\t\tthis.items = result;
\t\t\t});
\t\t\tthis.repository.get(this.id).then(entity => {
\t\t\t\tthis.selected = entity;
\t\t\t});
\t\t\tthis.repository.search(this.criteria).then(
\t\t\t\t() => {},
\t\t\t\t(error) => { this.failure = error; },
\t\t\t);
\t\t\tthis.rows.map((row) => { this.currentRow = row; });
\t\t\tthis.repository.get(this.id).then(function (entity) {
\t\t\t\tthis.functionSelected = entity;
\t\t\t});
\t\t},
\t},
};";
    let expressions = assignment_expressions(&definition_with_line_index(code));
    assert_eq!(
        expressions["items"],
        "await this.repository.search(this.criteria)"
    );
    assert_eq!(
        expressions["selected"],
        "await this.repository.get(this.id)"
    );
    assert_eq!(expressions["failure"], "error");
    assert_eq!(expressions["currentRow"], "row");
    assert_eq!(expressions["functionSelected"], "entity");
}

#[test]
fn static_callback_call_receiver_handles_optional_chain() {
    let root = parse_js("promise?.then((value) => value);");
    let calls = jsquery::calls(&root, &[]);
    assert!(!calls.is_empty());
    let then_call = calls
        .iter()
        .find(|call| jsquery::call_method_name(call).as_deref() == Some("then"))
        .expect("then call");
    assert_eq!(
        static_callback_call_receiver(then_call, "then").as_deref(),
        Some("promise")
    );
}

#[test]
fn direct_component_const_initializer_parses_single_declarator() {
    let (name, expression) = direct_component_const_initializer(
        "const selected: Entity<'media'> | null = await this.repository.get(id);",
    )
    .expect("initializer");
    assert_eq!(name, "selected");
    assert_eq!(expression, "await this.repository.get(id)");

    assert!(direct_component_const_initializer("let selected = value;").is_none());
    assert!(direct_component_const_initializer("const first = one, second = two;").is_none());
}

#[test]
fn parse_component_definition_after_inline_data_return_type() {
    let code = r#"
export default defineComponent({
    data(): {
        rows: Row[];
        isLoading: boolean;
    } {
        return { rows: [], isLoading: false };
    },
    computed: {
        visibleRows(): Row[] { return this.rows; },
    },
});"#;
    let def = definition_with_line_index(code);
    assert_eq!(
        sorted(def.data.clone()),
        sorted(strings(&["rows", "isLoading"]))
    );
    assert_eq!(def.computed, strings(&["visibleRows"]));
    let member = def
        .members
        .iter()
        .find(|candidate| candidate.name == "visibleRows")
        .expect("visibleRows");
    assert_eq!(member.type_, "Row[]");
    let member_types: HashMap<String, String> = def
        .members
        .iter()
        .map(|candidate| (candidate.name.clone(), candidate.type_.clone()))
        .collect();
    assert_eq!(member_types["rows"], "Row[]");
    assert_eq!(member_types["isLoading"], "boolean");
}

#[test]
fn parse_component_definition_emits() {
    let code = r#"
export default {
    emits: ['filter-reset', 'update:modelValue', 'close'],
};
"#;
    let def = definition(code);
    assert_eq!(
        def.emits,
        strings(&["filter-reset", "update:modelValue", "close"])
    );
    assert_eq!(def.events.len(), 3);
    assert_eq!(def.events[0].name, "filter-reset");
    assert_eq!(def.events[0].line, 3);
    assert!(def.events[0].name_range.declaration);
    assert!(!def.events[0].name_range.identifier);
    assert_eq!(line_slice(code, &def.events[0].name_range), "filter-reset");
    assert_eq!(
        canonical_event_name(&def.events[1].name),
        "update:model-value"
    );
}

#[test]
fn parse_component_definition_source_aware_events() {
    let code = r#"export default {
    emits: {
        save(payload) { return Boolean(payload); },
        'update:modelValue': null,
    },
    methods: {
        close() { this.$emit('close'); },
        update(context) { context.emit('updated'); },
        dynamic(name) { this.$emit(name); },
    },
};"#;
    let def = definition_with_line_index(code);
    assert_eq!(
        def.emits,
        strings(&["save", "update:modelValue", "close", "updated"])
    );
    assert_eq!(def.events.len(), 4);
    assert_eq!(def.events[0].line, 3);
    assert_eq!(def.events[1].line, 4);
    assert_eq!(def.events[2].line, 7);
    assert_eq!(def.events[3].line, 8);
    for (index, (name, identifier)) in [
        ("save", true),
        ("update:modelValue", false),
        ("close", false),
        ("updated", false),
    ]
    .into_iter()
    .enumerate()
    {
        let event = &def.events[index];
        assert!(event.name_range.declaration, "{}", event.name);
        assert_eq!(event.name_range.identifier, identifier, "{}", event.name);
        assert_eq!(line_slice(code, &event.name_range), name, "{}", event.name);
    }
}

#[test]
fn parse_component_definition_methods() {
    let code = r#"
export default {
    methods: {
        resetFilter() {
            this.$emit('filter-reset');
        },
        handleClick() {
            console.log('clicked');
        },
    },
};
"#;
    let def = definition(code);
    assert_eq!(def.methods, strings(&["resetFilter", "handleClick"]));
}

#[test]
fn parse_component_definition_computed() {
    let code = r#"
export default {
    computed: {
        fullName() {
            return this.firstName + ' ' + this.lastName;
        },
        isActive() {
            return this.status === 'active';
        },
    },
};
"#;
    let def = definition(code);
    assert_eq!(def.computed, strings(&["fullName", "isActive"]));
}

#[test]
fn parse_component_definition_template_scope() {
    let code = r#"
export default {
    inject: ['repositoryFactory', 'acl'],
    mixins: [Mixin.getByName('notification')],
    data() {
        return {
            isLoading: false,
            selectedIds: [],
        };
    },
    computed: {
        repository() { return this.repositoryFactory.create('product'); },
    },
    methods: {
        save() {},
    },
};
"#;
    let def = definition(code);
    assert_eq!(
        sorted(def.injected.clone()),
        sorted(strings(&["repositoryFactory", "acl"]))
    );
    assert_eq!(def.mixins, strings(&["notification"]));
    assert_eq!(
        sorted(def.data.clone()),
        sorted(strings(&["isLoading", "selectedIds"]))
    );
    assert_eq!(def.members.len(), 6);
    for member in &def.members {
        assert_ne!(member.line, 0, "{}", member.name);
    }
}

#[test]
fn parse_component_definition_local_directives() {
    let code = r#"
const hide = {};
export default {
    directives: {
        hide,
        focus: {},
        'drag-target': {},
        myDirective: {},
    },
};
"#;
    let def = definition_with_line_index(code);
    assert_eq!(def.local_directives.len(), 4);
    assert_eq!(def.local_directives[0].name, "hide");
    assert!(def.local_directives[0].shorthand);
    assert_eq!(def.local_directives[1].name, "focus");
    assert!(!def.local_directives[1].shorthand);
    assert_eq!(def.local_directives[2].name, "drag-target");
    assert!(def.local_directives[2].quoted);
    assert_eq!(def.local_directives[3].name, "my-directive");
    for directive in &def.local_directives {
        assert_ne!(directive.line, 0, "{}", directive.name);
        assert!(
            directive.name_range.start_character < directive.name_range.end_character,
            "{}",
            directive.name
        );
    }
}

#[test]
fn parse_component_definition_template_import() {
    let code = r#"
import template from './sw-base-filter.html.twig';
import './sw-base-filter.scss';

export default {
    template,
    props: {},
};
"#;
    let def = definition(code);
    assert_eq!(def.template_path, "./sw-base-filter.html.twig");
    assert!(def.has_template);
}

#[test]
fn parse_component_definition_full() {
    let code = r#"
import template from './sw-base-filter.html.twig';
import './sw-base-filter.scss';

export default {
    template,

    emits: ['filter-reset'],

    props: {
        title: {
            type: String,
            required: true,
        },
        showResetButton: {
            type: Boolean,
            required: true,
        },
        active: {
            type: Boolean,
            required: true,
        },
    },

    computed: {
        isVisible() {
            return this.active && this.showResetButton;
        },
    },

    methods: {
        resetFilter() {
            this.$emit('filter-reset');
        },
    },
};
"#;
    let def = definition(code);
    assert_eq!(def.template_path, "./sw-base-filter.html.twig");
    assert!(def.has_template);
    assert_eq!(def.emits, strings(&["filter-reset"]));
    assert_eq!(
        prop_names(&def.props),
        strings(&["title", "showResetButton", "active"])
    );
    assert_eq!(def.computed, strings(&["isVisible"]));
    assert_eq!(def.methods, strings(&["resetFilter"]));
}

#[test]
fn parse_component_definition_no_export_default() {
    let code = r#"
const component = {
    props: {
        title: String,
    },
};
"#;
    let def = definition(code);
    assert!(def.props.is_empty());
    assert!(def.methods.is_empty());
}
