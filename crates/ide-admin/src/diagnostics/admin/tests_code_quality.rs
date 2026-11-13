//! Tests of the Administration code quality analyzer.

use std::sync::Arc;

use swlsp_shopware::ResolvedVersion;

use super::test_fixture::Fixture;
use super::*;
use crate::diagnostics::test_support::check_problems;

#[allow(clippy::field_reassign_with_default)]
fn version(minor: i64) -> ResolvedVersion {
    let mut version = ResolvedVersion::default();
    version.known = true;
    version.version.major = 6;
    version.version.minor = minor;
    version
}

const URI: &str = "file:///project/src/Resources/app/administration/src/main.js";

fn problems(minor: i64, fixture: &str) -> Vec<swlsp_lsp::Problem> {
    check_problems(
        &AdminCodeQualityAnalyzer::new(None, version(minor)),
        URI,
        fixture,
    )
    .1
}

#[test]
fn imports_snippets_and_extension_manager_overrides() {
    problems(
        6,
        r#"import Criteria from {|admin.import.core-source:'@administration/core/data/criteria.data'|};
import local from './local';
import deDE from './snippet/de-DE.json';
const { Component, Module } = Shopware;
Module.register('swag-example', { {|admin.module.snippets-import:snippets|}: { 'de-DE': deDE }, routes: {} });
Shopware.Module.register('swag-other', { {|admin.module.snippets-import:'snippets'|}: {}, routes: {} });
Shopware.Module.register('swag-fine', { routes: {} });
Service.register('snippets', { snippets: {} });
Component.override({|admin.component.extension-manager-override:'sw-extension-card-base'|}, {});
Shopware.Component.override({|admin.component.extension-manager-override:'sw-extension-store-landing-page'|}, {});
Component.override('sw-product-detail', {});
Component.register('sw-extension-mine', {});
"#,
    );
    // The Administration bundle itself is no extension.
    let (_, core) = check_problems(
        &AdminCodeQualityAnalyzer::new(None, version(7)),
        "file:///project/vendor/shopware/administration/Resources/app/administration/src/main.js",
        "import a from '@administration/a'; Component.override('sw-extension-card-base', {});",
    );
    assert!(core.is_empty());
    // Outside of the Administration nothing is said.
    let (_, outside) = check_problems(
        &AdminCodeQualityAnalyzer::new(None, version(7)),
        "file:///project/src/Resources/app/storefront/src/main.js",
        "import a from '@administration/a'; Shopware.State.get('x');",
    );
    assert!(outside.is_empty());
}

#[test]
fn vuex_state_is_reported_from_shopware_6_7() {
    let fixture = r#"const { {|admin.state.deprecated:State|} } = Shopware;
{|admin.state.deprecated:State.commit('context/setApiLanguageId', id)|};
State.get('session');
{|admin.state.deprecated:Shopware.State.commit('swProduct/setLoading', ['product', true])|};
{|admin.state.deprecated:Shopware.State.commit(dynamic)|};
Shopware.{|admin.state.deprecated:State|}.getters['a/b'];
Shopware.Store.get('context');
export default {
    computed: {
        {|admin.state.deprecated:...mapState('swProduct', ['product', 'loading'])|},
        {|admin.state.deprecated:...mapGetters('swProduct', { busy: 'isLoading' })|},
        {|admin.state.deprecated:...mapState('swProduct', { name: (state) => state.product.name })|},
        ...mapState(() => Shopware.Store.get('swFlow'), ['flow']),
        ...mapState(useStore, ['other']),
    },
};
"#;
    let plain = fixture
        .replace("{|admin.state.deprecated:", "")
        .replace("|}", "");
    assert!(problems(6, &plain).is_empty());
    let found = problems(7, fixture);
    let rewrites: Vec<(String, Option<String>)> = found
        .iter()
        .map(|problem| {
            let payload = problem.payload.as_ref();
            (
                payload
                    .and_then(|payload| payload["expected"].as_str())
                    .unwrap_or("-")
                    .lines()
                    .next()
                    .unwrap_or_default()
                    .to_owned(),
                payload.and_then(|payload| payload["replacement"].as_str().map(str::to_owned)),
            )
        })
        .collect();
    assert_eq!(
        rewrites,
        [
            ("-".to_owned(), None),
            (
                "State.commit('context/setApiLanguageId', id)".to_owned(),
                Some("Shopware.Store.get('context').setApiLanguageId(id)".to_owned())
            ),
            (
                "Shopware.State.commit('swProduct/setLoading', ['product', true])".to_owned(),
                Some("Shopware.Store.get('swProduct').setLoading(['product', true])".to_owned())
            ),
            // A dynamic commit and other State members have no rewrite.
            ("-".to_owned(), None),
            ("-".to_owned(), None),
            (
                "...mapState('swProduct', ['product', 'loading'])".to_owned(),
                Some(
                    "product() {\n            return Shopware.Store.get('swProduct').product;\n        },\n        loading() {\n            return Shopware.Store.get('swProduct').loading;\n        }"
                        .to_owned()
                )
            ),
            (
                "...mapGetters('swProduct', { busy: 'isLoading' })".to_owned(),
                Some(
                    "busy() {\n            return Shopware.Store.get('swProduct').isLoading;\n        }"
                        .to_owned()
                )
            ),
            // A function mapping is reported, the Pinia forms are not.
            ("-".to_owned(), None),
        ]
    );
    // The fix changes behaviour unless the store has the action: it is not
    // marked as an equivalent rewrite.
    assert!(found.iter().all(|problem| {
        problem
            .payload
            .as_ref()
            .is_none_or(|payload| payload.get("equivalent").is_none())
    }));
}

#[test]
fn emitted_events_have_to_be_declared() {
    let fixture = r#"const { Component, Mixin } = Shopware;
Component.register('swag-plain', {
    emits: ['saved', 'update:value'],
    methods: {
        run() {
            this.$emit('saved');
            this.$emit('update:value', 1);
            this.$emit({|admin.component.undeclared-emit:'closed'|});
            this.$emit(dynamic);
            this.$emit(`prefix-${name}`);
            this.$emit({|admin.component.undeclared-emit:`plain`|});
            other.$emit('ignored');
        },
    },
});
Component.register('swag-none', {
    methods: { run() { this.$emit({|admin.component.undeclared-emit:'done'|}); } },
});
Component.register('swag-object', {
    emits: { done: null },
    methods: { run() { this.$emit('done'); } },
});
// Members from elsewhere: nothing can be said without the index.
Component.register('swag-mixed', {
    mixins: [Mixin.getByName('notification')],
    methods: { run() { this.$emit('done'); } },
});
Component.extend('swag-child', 'swag-plain', {
    methods: { run() { this.$emit('done'); } },
});
Component.override('swag-plain', {
    methods: { run() { this.$emit('done'); } },
});
Component.register('swag-variable', {
    emits: EVENTS,
    methods: { run() { this.$emit('done'); } },
});
Mixin.register('swag-mixin', {
    methods: { run() { this.$emit({|admin.component.undeclared-emit:'mixed'|}); } },
});
const helper = { run() { this.$emit('not-a-component'); } };
"#;
    assert!(
        problems(
            6,
            &fixture
                .replace("{|admin.component.undeclared-emit:", "")
                .replace("|}", "")
        )
        .is_empty()
    );
    let found = problems(7, fixture);
    assert_eq!(found[0].payload.as_ref().unwrap()["event"], "closed");
}

#[test]
fn emits_follow_parents_mixins_and_templates_through_the_index() {
    let fixture = Fixture::new();
    let source = "src/Resources/app/administration/src";
    let files = [
        (
            "parent/index.js",
            "import template from './parent.html.twig';\nShopware.Component.register('swag-parent', {\n    template,\n    emits: ['parent-event'],\n});\n",
        ),
        (
            "parent/parent.html.twig",
            "<button @click=\"$emit('parent-event')\"></button>\n",
        ),
        (
            "mixin.js",
            "Shopware.Mixin.register('swag-mixin', {\n    emits: ['mixin-event'],\n});\n",
        ),
        (
            "child/index.js",
            "import template from './child.html.twig';\nShopware.Component.extend('swag-child', 'swag-parent', {\n    template,\n    mixins: [Shopware.Mixin.getByName('swag-mixin')],\n    emits: ['child-event'],\n});\n",
        ),
    ];
    for (name, text) in files {
        let path = fixture.path(&format!("{source}/{name}"));
        fixture.write(&path, text);
        fixture.index(&path, text);
    }
    let analyzer = AdminCodeQualityAnalyzer::new(Some(fixture.indexer.clone()), version(7));
    let uri = |name: &str| swlsp_uriutil::file_uri(&fixture.path(&format!("{source}/{name}")));
    check_problems(
        &analyzer,
        &uri("child/index.js"),
        "import template from './child.html.twig';\nShopware.Component.extend('swag-child', 'swag-parent', {\n    template,\n    mixins: [Shopware.Mixin.getByName('swag-mixin')],\n    emits: ['child-event'],\n    methods: {\n        run() {\n            this.$emit('child-event');\n            this.$emit('parent-event');\n            this.$emit('mixin-event');\n            this.$emit({|admin.component.undeclared-emit:'unknown-event'|});\n        },\n    },\n});\n",
    );
    let template = fixture.path(&format!("{source}/child/child.html.twig"));
    fixture.write(&template, "");
    let (_, found) = check_problems(
        &analyzer,
        &swlsp_uriutil::file_uri(&template),
        "{% block swag_child %}\n<button @click=\"$emit('child-event')\" @blur=\"$emit('parent-event'); $emit({|admin.component.undeclared-emit:'template-event'|})\">\n    {{ label }}\n</button>\n<sw-field @update:value=\"$emit('mixin-event', $event)\" title=\"$emit('text')\"></sw-field>\n{% endblock %}\n",
    );
    assert_eq!(
        found[0].message,
        "Event 'template-event' is emitted but not declared in the emits option of swag-child"
    );
    let _ = Arc::strong_count(&fixture.indexer);
}
