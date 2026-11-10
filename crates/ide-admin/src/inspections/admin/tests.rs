//! Tests of the Administration inspections and block override fixes.

use std::path::PathBuf;
use std::sync::Arc;

use crate::diagnostics::{
    ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE, ADMIN_BLOCK_OVERRIDE_NESTED_CODE,
    ADMIN_BLOCK_OVERRIDE_REPEATED_CODE, ADMIN_BLOCK_PARENT_CONDITIONAL_CODE,
    ADMIN_BLOCK_PARENT_REPEATED_CODE,
};
use swlsp_admin::{
    AdminComponentIndexer, ComponentRegistrationKind, TwigBlock, VueComponentEvent,
    VueComponentMember, VueComponentMemberKind, VueComponentProp, VueComponentSlot,
    VueComponentSlotMember,
};
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::Inspection;
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_shopware::ResolvedVersion;

use super::fixes::REPLACE_ADMIN_COMPONENT_TAG_FIX_ID;
use super::new_admin_inspections;
use crate::inspections::SUGGESTION_FIX_ID;
use crate::inspections::test_support::{
    apply_single, check_diagnostics, check_fix, fix_context, quick_fix_with_id,
};

/// Builds a `VueComponent` (its private fields rule out struct literals).
macro_rules! vue_component {
    ($($field:ident: $value:expr),* $(,)?) => {{
        let mut component = swlsp_admin::VueComponent::default();
        $(component.$field = $value;)*
        component
    }};
}
pub(super) use vue_component;

pub(super) struct Fixture {
    _dir: tempfile::TempDir,
    pub root: PathBuf,
    pub index: Arc<AdminComponentIndexer>,
}

impl Fixture {
    pub fn new() -> Self {
        let dir = tempfile::tempdir().expect("temporary directory");
        let root = dir.path().to_path_buf();
        let cache = root.join("cache");
        std::fs::create_dir_all(&cache).expect("cache");
        let index = AdminComponentIndexer::new(cache.to_str().unwrap(), None).expect("indexer");
        Self {
            _dir: dir,
            root,
            index: Arc::new(index),
        }
    }

    pub fn path(&self, relative: &str) -> String {
        self.root.join(relative).to_str().unwrap().to_owned()
    }

    pub fn index_file(&self, path: &str, source: &str) {
        self.index
            .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
            .expect("index");
    }

    pub fn save(&self, component: swlsp_admin::VueComponent) {
        self.index.save_component(&component).expect("save");
    }

    pub fn inspection(&self) -> Arc<dyn Inspection> {
        Arc::new(crate::inspections::test_support::Combined(
            new_admin_inspections(Some(self.index.clone()), ResolvedVersion::default()),
        ))
    }
}

/// Checks the problems marked in `fixture`, then applies every suggestion
/// fix of the single marked problem and asserts one candidate is `expected`.
fn assert_suggestion(inspection: &dyn Inspection, path: &str, fixture: &str, expected: &str) {
    let (document, problems) =
        check_diagnostics(inspection, &swlsp_uriutil::file_uri(path), fixture);
    let [problem] = problems.as_slice() else {
        panic!("expected one problem: {problems:?}");
    };
    assert!(!problem.fixes.is_empty());
    let fix = quick_fix_with_id(inspection, SUGGESTION_FIX_ID);
    let candidates: Vec<String> = problem
        .fixes
        .iter()
        .filter(|bound| bound.id.0 == SUGGESTION_FIX_ID)
        .map(|bound| apply_single(&fix, &fix_context(&document, problem, bound, None)))
        .collect();
    assert!(
        candidates.iter().any(|candidate| candidate == expected),
        "{expected:?} not in {candidates:?}"
    );
}

#[test]
fn admin_inspection_declares_and_reports_block_override_problems() {
    let fixture = Fixture::new();
    let inspection = fixture.inspection();
    let mut expected = vec![
        ADMIN_BLOCK_OVERRIDE_NESTED_CODE,
        ADMIN_BLOCK_OVERRIDE_CONDITIONAL_CODE,
        ADMIN_BLOCK_OVERRIDE_REPEATED_CODE,
        ADMIN_BLOCK_PARENT_CONDITIONAL_CODE,
        ADMIN_BLOCK_PARENT_REPEATED_CODE,
    ];
    for problem in inspection.definition().problems {
        let Some(position) = expected.iter().position(|id| *id == problem.id.0) else {
            continue;
        };
        assert_eq!(problem.default_severity, DiagnosticSeverity::ERROR);
        assert!(!problem.disabled_by_default);
        expected.remove(position);
    }
    assert!(expected.is_empty());

    let (_, problems) = check_diagnostics(
        inspection.as_ref(),
        &swlsp_uriutil::file_uri(
            &fixture.path("src/Resources/app/administration/src/component.vue"),
        ),
        r#"<template><{|admin.block-override.conditional:sw-block|} v-if="enabled" extends="conditional" /></template>"#,
    );
    assert!(problems[0].fixes.is_empty());
}

#[test]
fn admin_twig_privilege_inspection_builds_typo_replacement() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/privileges.ts"),
        "Shopware.Service('privileges').addPrivilegeMappingEntry({\n    key: 'product', roles: { viewer: { privileges: ['product:read'] } },\n});",
    );
    assert_suggestion(
        fixture.inspection().as_ref(),
        &format!("{admin_root}/view.html.twig"),
        r#"<mt-button :disabled="acl.can('{|admin.privilege.not-found:product.viwer|}')" />"#,
        r#"<mt-button :disabled="acl.can('product.viewer')" />"#,
    );
}

#[test]
fn admin_twig_block_inspection_builds_parent_block_typo_replacement() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("Resources/app/administration/src");
    let parent_template = format!("{admin_root}/sw-page.html.twig");
    let child_template = format!("{admin_root}/acme-page.html.twig");
    let block = TwigBlock {
        name: "sw_page_content".into(),
        file_path: parent_template.clone(),
        line: 4,
        ..TwigBlock::default()
    };
    fixture.save(vue_component! {
        name: "sw-page".into(),
        kind: ComponentRegistrationKind::Register,
        file_path: format!("{admin_root}/sw-page.js"),
        template_path: parent_template.clone(),
        blocks: vec![block],
    });
    fixture.save(vue_component! {
        name: "acme-page".into(),
        kind: ComponentRegistrationKind::Extend,
        target_component: "sw-page".into(),
        extends_component: "sw-page".into(),
        file_path: format!("{admin_root}/acme-page.js"),
        template_path: child_template.clone(),
    });
    assert_suggestion(
        fixture.inspection().as_ref(),
        &child_template,
        "{% block {|admin.component.block-not-found:sw_page_contnet|} %}{% endblock %}",
        "{% block sw_page_content %}{% endblock %}",
    );
}

#[test]
fn admin_prop_value_inspection_builds_typo_replacement() {
    let fixture = Fixture::new();
    fixture.save(vue_component! {
        name: "sw-label".into(),
        file_path: fixture.path("sw-label/index.js"),
        props: vec![VueComponentProp {
            name: "variant".into(),
            type_: "String".into(),
            allowed_values: vec!["primary".into(), "secondary".into()],
            allowed_values_complete: true,
            ..VueComponentProp::default()
        }],
    });
    let inspection = fixture.inspection();
    let path = fixture.path("src/Administration/Resources/app/administration/src/view.html.twig");
    for (source, expected) in [
        (
            r#"<sw-label variant="{|admin.component.invalid-prop-value:primry|}" />"#,
            r#"<sw-label variant="primary" />"#,
        ),
        (
            r#"<sw-label :variant="'{|admin.component.invalid-prop-value:primry|}'" />"#,
            r#"<sw-label :variant="'primary'" />"#,
        ),
    ] {
        assert_suggestion(inspection.as_ref(), &path, source, expected);
    }
}

#[test]
fn admin_component_contract_inspection_builds_name_replacements() {
    let fixture = Fixture::new();
    let prop = |name: &str| VueComponentProp {
        name: name.into(),
        type_: "Boolean".into(),
        ..VueComponentProp::default()
    };
    let event = |name: &str| VueComponentEvent {
        name: name.into(),
        ..VueComponentEvent::default()
    };
    fixture.save(vue_component! {
        name: "sw-field".into(),
        file_path: fixture.path("sw-field.ts"),
        props: vec![prop("isLoading"), prop("checked")],
        events: vec![event("itemClick"), event("update:checked")],
        slots: vec![VueComponentSlot {
            name: "header".into(),
            ..VueComponentSlot::default()
        }],
    });
    let inspection = fixture.inspection();
    let path = fixture.path("Resources/app/administration/src/view.html.twig");
    for (source, expected) in [
        (
            r#"<sw-field :{|admin.component.unknown-prop:is-laoding|}.sync="ready" />"#,
            r#"<sw-field :is-loading.sync="ready" />"#,
        ),
        (
            r#"<sw-field @{|admin.component.unknown-event:item-clik|}.stop="select" />"#,
            r#"<sw-field @item-click.stop="select" />"#,
        ),
        (
            r#"<sw-field v-model:{|admin.component.unknown-model:cheked|}.trim="checked" />"#,
            r#"<sw-field v-model:checked.trim="checked" />"#,
        ),
        (
            "<sw-field><template #{|admin.component.unknown-slot:heder|}>Title</template></sw-field>",
            "<sw-field><template #header>Title</template></sw-field>",
        ),
    ] {
        assert_suggestion(inspection.as_ref(), &path, source, expected);
    }
}

#[test]
fn admin_directive_inspection_builds_typo_replacement() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/app/directive/tooltip.directive.ts"),
        "Shopware.Directive.register('tooltip', {});",
    );
    assert_suggestion(
        fixture.inspection().as_ref(),
        &format!("{admin_root}/view.html.twig"),
        r#"<div v-{|admin.directive.not-found:tooltpi|}.bottom="message"></div>"#,
        r#"<div v-tooltip.bottom="message"></div>"#,
    );
}

#[test]
fn admin_filter_inspection_builds_typo_replacement() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/app/filter/currency.ts"),
        "Shopware.Filter.register('currency', value => value);",
    );
    assert_suggestion(
        fixture.inspection().as_ref(),
        &format!("{admin_root}/consumer.ts"),
        "Shopware.Filter.getByName('{|admin.filter.not-found:currncy|}');",
        "Shopware.Filter.getByName('currency');",
    );
}

#[test]
fn admin_cms_inspection_builds_typo_replacement() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/cms.ts"),
        "cmsService.registerCmsElement({ name: 'hero' });",
    );
    assert_suggestion(
        fixture.inspection().as_ref(),
        &format!("{admin_root}/consumer.ts"),
        "cmsService.getCmsElementConfigByName('{|admin.cms-element.not-found:herp|}');",
        "cmsService.getCmsElementConfigByName('hero');",
    );
}

#[test]
fn admin_component_inspection_builds_tag_typo_replacement() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/component/sw-button/index.js"),
        "Shopware.Component.register('sw-button', {});",
    );
    let inspection = fixture.inspection();
    let uri = swlsp_uriutil::file_uri(&format!("{admin_root}/view.html.twig"));
    let before = "<{|admin.component.not-found:sw-butotn|}>Save</sw-butotn>";
    check_diagnostics(inspection.as_ref(), &uri, before);
    check_fix(
        inspection.as_ref(),
        &uri,
        before,
        REPLACE_ADMIN_COMPONENT_TAG_FIX_ID,
        "<sw-button>Save</sw-button>",
    );
}

#[test]
fn admin_twig_slot_member_inspection_builds_typo_replacement() {
    let fixture = Fixture::new();
    let member = |name: &str, type_: &str| VueComponentSlotMember {
        name: name.into(),
        type_: type_.into(),
        ..VueComponentSlotMember::default()
    };
    fixture.save(vue_component! {
        name: "sw-inherit-wrapper".into(),
        file_path: fixture.path("wrapper.js"),
        slots: vec![VueComponentSlot {
            name: "content".into(),
            members_complete: true,
            members: vec![member("currentValue", "string"), member("isInherited", "boolean")],
            ..VueComponentSlot::default()
        }],
    });
    assert_suggestion(
        fixture.inspection().as_ref(),
        &fixture.path("src/Administration/Resources/app/administration/src/view.html.twig"),
        r#"<sw-inherit-wrapper><template #content="props">{{ props.{|admin.component.unknown-slot-prop:curentValue|} }}</template></sw-inherit-wrapper>"#,
        r#"<sw-inherit-wrapper><template #content="props">{{ props.currentValue }}</template></sw-inherit-wrapper>"#,
    );
}

#[test]
fn admin_template_member_inspection_builds_typo_replacement() {
    let fixture = Fixture::new();
    let template_path =
        fixture.path("src/Administration/Resources/app/administration/src/sw-card.html.twig");
    fixture.save(vue_component! {
        name: "sw-card".into(),
        template_path: template_path.clone(),
        file_path: fixture.path("src/Administration/Resources/app/administration/src/index.ts"),
        members: vec![VueComponentMember {
            name: "getSlots".into(),
            kind: VueComponentMemberKind::Method,
            ..VueComponentMember::default()
        }],
    });
    assert_suggestion(
        fixture.inspection().as_ref(),
        &template_path,
        "{{ {|admin.component.unknown-template-member:getSlos|}() }}",
        "{{ getSlots() }}",
    );
}

#[test]
fn admin_module_registry_inspection_builds_typo_replacement() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("src/Administration/Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/main.ts"),
        "Shopware.Module.register('sw-product', { routes: {} });",
    );
    assert_suggestion(
        fixture.inspection().as_ref(),
        &format!("{admin_root}/consumer.js"),
        "Shopware.Module.getModuleRegistry().get('{|admin.module.not-found:sw-prduct|}');",
        "Shopware.Module.getModuleRegistry().get('sw-product');",
    );
}

#[test]
fn admin_application_container_inspection_builds_typo_replacement() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/global.types.ts"),
        "
export interface SubContainer<T extends string> { $list(): string[]; }
declare global {
    interface FactoryContainer extends SubContainer<'factory'> {
        locale: LocaleFactory;
    }
}",
    );
    assert_suggestion(
        fixture.inspection().as_ref(),
        &format!("{admin_root}/consumer.ts"),
        "Application.getContainer('factory').{|admin.application-container.unknown-member:loacle|};",
        "Application.getContainer('factory').locale;",
    );
}

#[test]
fn admin_shopware_context_inspection_builds_typo_replacement() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/app/composables/use-context.ts"),
        "export interface ContextState {
    app: { environment: null | string };
    api: { languageId: null | string };
}",
    );
    assert_suggestion(
        fixture.inspection().as_ref(),
        &format!("{admin_root}/consumer.ts"),
        "Shopware.Context.api.{|admin.shopware-context.unknown-member:langaugeId|};",
        "Shopware.Context.api.languageId;",
    );
}

#[test]
fn admin_shopware_utils_inspection_builds_typo_replacement() {
    let fixture = Fixture::new();
    let admin_root = fixture.path("Resources/app/administration/src");
    fixture.index_file(
        &format!("{admin_root}/core/service/util.service.ts"),
        "export default { createId };\nfunction createId(): string { return 'id'; }",
    );
    assert_suggestion(
        fixture.inspection().as_ref(),
        &format!("{admin_root}/consumer.ts"),
        "const utils = Shopware.Utils; utils.{|admin.shopware-utils.unknown-member:creatId|}();",
        "const utils = Shopware.Utils; utils.createId();",
    );
}

/// Every code the Administration analyzer can report must be declared, or a
/// file triggering it fails the Administration inspection that runs it.
#[test]
fn admin_inspection_declares_event_bus_problems() {
    let fixture = Fixture::new();
    let definition = fixture.inspection().definition();
    let declared: Vec<&str> = definition
        .problems
        .iter()
        .map(|problem| problem.id.as_str())
        .collect();
    for id in [
        "admin.event-bus.missing-payload",
        "admin.event-bus.missing-handler",
        "admin.event-bus.payload-type",
        "admin.event-bus.handler-type",
    ] {
        assert!(declared.contains(&id), "{id} is not declared");
    }
}
