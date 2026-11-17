//! Tests of the `$super` call diagnostics.

use super::test_fixture::{Fixture, suggestions};

const ADMIN: &str = "src/Resources/app/administration/src";

fn base(fixture: &Fixture) {
    fixture.index(
        &fixture.path(&format!("{ADMIN}/component/sw-card/index.js")),
        "Component.register('sw-card', {
    computed: { cardClasses() { return {}; }, title: { get() {}, set() {} } },
    methods: { createdComponent() {}, onSave() {} },
});",
    );
}

fn check(fixture: &Fixture, relative: &str, source: &str) -> Vec<swlsp_lsp::Problem> {
    let path = fixture.path(&format!("{ADMIN}/{relative}"));
    let (plain, _) = swlsp_lsp::fixture::parse(source);
    fixture.index(&path, &plain);
    fixture.check_path(&path, source)
}

#[test]
fn override_super_call_must_name_a_base_method_or_computed() {
    let fixture = Fixture::new();
    base(&fixture);
    let problems = check(
        &fixture,
        "extension/sw-card/index.js",
        "Component.override('sw-card', {
    computed: {
        cardClasses() { return this.$super('cardClasses'); },
        title: { get() { return this.$super('title.get'); } },
        own() { return this.$super('{|admin.component.unknown-super-member:own|}'); },
    },
    methods: {
        createdComponent() { this.$super('createdComponent'); },
        onSave() { return this.$super('{|admin.component.unknown-super-member:onSaev|}', 1); },
        dynamic(name) { return this.$super(name); },
    },
});",
    );
    assert!(problems[0].message.contains("'sw-card'"));
    assert!(suggestions(&problems[1]).contains(&"onSave".to_owned()));
}

#[test]
fn extend_super_call_is_checked_against_the_parent() {
    let fixture = Fixture::new();
    base(&fixture);
    check(
        &fixture,
        "component/sw-big-card/index.js",
        "Component.extend('sw-big-card', 'sw-card', {
    methods: {
        onSave() { return this.$super('onSave'); },
        onReset() { return this.$super('{|admin.component.unknown-super-member:onReset|}'); },
    },
});",
    );
}

#[test]
fn super_call_through_another_override_is_known() {
    let fixture = Fixture::new();
    base(&fixture);
    fixture.index(
        &fixture.path(&format!("{ADMIN}/other/sw-card/index.js")),
        "Component.override('sw-card', { methods: { added() {} } });",
    );
    check(
        &fixture,
        "extension/sw-card/index.js",
        "Component.override('sw-card', {
    methods: { added() { return this.$super('added'); } },
});",
    );
}

#[test]
fn super_call_is_not_checked_without_a_complete_base() {
    let fixture = Fixture::new();
    // The base is not indexed.
    check(
        &fixture,
        "extension/sw-missing/index.js",
        "Component.override('sw-missing', {
    methods: { run() { return this.$super('run'); } },
});",
    );
    // The base takes members from a mixin that is not indexed.
    fixture.index(
        &fixture.path(&format!("{ADMIN}/component/sw-open/index.js")),
        "Component.register('sw-open', {
    mixins: [Mixin.getByName('unknown')],
    methods: { known() {} },
});",
    );
    check(
        &fixture,
        "extension/sw-open/index.js",
        "Component.override('sw-open', {
    methods: { run() { return this.$super('run'); } },
});",
    );
}

#[test]
fn super_call_tells_a_spread_option_from_a_spread_in_a_body() {
    let fixture = Fixture::new();
    // A spread inside a method body leaves the method list complete.
    fixture.index(
        &fixture.path(&format!("{ADMIN}/component/sw-list/index.js")),
        "Component.register('sw-list', {
    methods: { load(items) { return [...items]; } },
});",
    );
    check(
        &fixture,
        "extension/sw-list/index.js",
        "Component.override('sw-list', {
    methods: { reload() { return this.$super('{|admin.component.unknown-super-member:reload|}'); } },
});",
    );
    // A spread option may add any name.
    fixture.index(
        &fixture.path(&format!("{ADMIN}/component/sw-mapped/index.js")),
        "Component.register('sw-mapped', {
    computed: { ...mapPropertyErrors('order', ['email']), total() { return 1; } },
});",
    );
    check(
        &fixture,
        "extension/sw-mapped/index.js",
        "Component.override('sw-mapped', {
    computed: { emailError() { return this.$super('emailError'); } },
});",
    );
}

#[test]
fn super_call_is_not_checked_when_the_base_methods_do_not_parse() {
    let fixture = Fixture::new();
    fixture.index(
        &fixture.path(&format!("{ADMIN}/component/sw-generic/index.ts")),
        "Component.register('sw-generic', {
    methods: {
        pick<T extends keyof Row>(row: Row, key: T) { return row[key]; },
        load() {},
    },
});",
    );
    check(
        &fixture,
        "extension/sw-generic/index.js",
        "Component.override('sw-generic', {
    methods: { pick() { return this.$super('pick'); } },
});",
    );
}
