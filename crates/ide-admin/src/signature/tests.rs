use super::*;
use std::path::Path;
use std::sync::Arc;
use swlsp_admin::{AdminComponentIndexer, VueComponent, VueComponentSlot, VueComponentSlotMember};
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{
    Context, SignatureHelpProvider, SignatureHelpRequest, SyntaxContext, TextDocument,
};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNodeExt, TextSize};
use swlsp_uriutil::file_uri;

fn p(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

fn request_at(document: TextDocument, offset: u32) -> SignatureHelpRequest {
    let document = Arc::new(document);
    let root = document.root().unwrap();
    let node = root.node_at(TextSize::from(offset));
    let (line, character) = document.line_index().position_utf16(offset);
    let mut params = protocol::SignatureHelpParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    SignatureHelpRequest {
        params,
        syntax: SyntaxContext {
            language: document.language,
            document: Some(document),
            root: Some(root),
            token: None,
            node,
        },
    }
}

struct AdminFixture {
    _root: tempfile::TempDir,
    index: Arc<AdminComponentIndexer>,
    admin_root: std::path::PathBuf,
}

fn admin_fixture() -> AdminFixture {
    let root = tempfile::tempdir().unwrap();
    let index = Arc::new(AdminComponentIndexer::new(&p(&root.path().join("cache")), None).unwrap());
    let admin_root = root
        .path()
        .join("src/Administration/Resources/app/administration/src");
    AdminFixture {
        _root: root,
        index,
        admin_root,
    }
}

impl AdminFixture {
    fn index(&self, path: &Path, source: &str) {
        self.index
            .index(&ParsedFile::new(p(path), source.as_bytes().to_vec()))
            .unwrap();
    }

    fn help(&self, path: &Path, source: &str, needle: &str) -> Option<protocol::SignatureHelp> {
        AdminSignatureProvider::new(self.index.clone())
            .get_signature_help(&Context::background(), &admin_request(path, source, needle))
            .unwrap()
    }
}

fn admin_request(path: &Path, source: &str, needle: &str) -> SignatureHelpRequest {
    let document = TextDocument::new(file_uri(&p(path)), source, 1);
    let offset = (source.rfind(needle).unwrap() + needle.len() / 2) as u32;
    let mut request = request_at(document, offset);
    request.syntax.language = None;
    request
}

fn require_admin_signature(
    result: Option<protocol::SignatureHelp>,
    label: &str,
    active: i32,
) -> protocol::SignatureHelp {
    let result = result.expect("signature help");
    assert_eq!(result.signatures.len(), 1, "{result:?}");
    assert_eq!(result.signatures[0].label, label);
    assert_eq!(result.active_parameter, active);
    assert_eq!(result.signatures[0].active_parameter, active);
    assert!(result.signatures[0].documentation.is_some());
    result
}

fn doc(result: &protocol::SignatureHelp, index: usize) -> &str {
    &result.signatures[index]
        .documentation
        .as_ref()
        .unwrap()
        .value
}

#[test]
fn admin_signature_help_for_inherited_component_methods_in_javascript_and_twig() {
    let fixture = admin_fixture();
    let base_path = fixture.admin_root.join("component/sw-base/index.ts");
    let base_template = fixture
        .admin_root
        .join("component/sw-base/sw-base.html.twig");
    let child_path = fixture.admin_root.join("component/sw-child/index.ts");
    let child_template = fixture
        .admin_root
        .join("component/sw-child/sw-child.html.twig");
    let base_source = "import template from './sw-base.html.twig';
Component.register('sw-base', {
    template,
    methods: {
        save(id: string, options?: { force: boolean, tags: string[] }): Promise<void> {
            return Promise.resolve();
        },
        submit() {
            return this.save('product-id', { force: true, tags: ['a', 'b'] });
        },
    },
});";
    let child_source = "import template from './sw-child.html.twig';
Component.extend('sw-child', 'sw-base', { template });";
    let child_template_source =
        "<button @click=\"save(product.id, { force: true, tags: ['a', 'b'] })\">Save</button>";
    fixture.index(&base_path, base_source);
    fixture.index(
        &base_template,
        "{{ save('id', { force: true, tags: [] }) }}",
    );
    fixture.index(&child_path, child_source);
    fixture.index(&child_template, child_template_source);

    let label = "save(id: string, options?: { force: boolean, tags: string[] }): Promise<void>";
    let javascript = require_admin_signature(
        fixture.help(&base_path, base_source, "force: true"),
        label,
        1,
    );
    assert!(doc(&javascript, 0).contains("sw-base"));
    let twig = require_admin_signature(
        fixture.help(&child_template, child_template_source, "'b'"),
        label,
        1,
    );
    assert!(doc(&twig, 0).contains("sw-child"));
}

#[test]
fn admin_signature_help_for_typed_markup_members_and_vue_builtins() {
    let fixture = admin_fixture();
    let definition_path = fixture.admin_root.join("component/sw-card/index.ts");
    let template_path = fixture
        .admin_root
        .join("component/sw-card/sw-card.html.twig");
    let definition_source = "import template from './sw-card.html.twig';
interface Handler {
    run: (value: string, retries?: number) => boolean;
}
Component.register('sw-card', {
    template,
    props: { handler: { type: Object as PropType<Handler> } },
});";
    let template_source =
        "<button @click=\"handler.run('value', 2); $emit('saved', handler)\">Run</button>";
    fixture.index(&definition_path, definition_source);
    fixture.index(&template_path, template_source);
    let component = fixture
        .index
        .get_component_by_template_path(&p(&template_path))
        .unwrap()
        .unwrap();
    let handler = component.template_member("handler").unwrap();
    assert!(handler.type_.contains("Handler"));
    let run_offset = (template_source.find("run").unwrap() + 1) as u32;
    let document = TextDocument::new(file_uri(&p(&template_path)), template_source, 1);
    let resolved = fixture
        .index
        .resolve_twig_vue_instance_member(
            &document.root().unwrap(),
            template_source,
            run_offset,
            &p(&template_path),
        )
        .unwrap()
        .unwrap();
    assert!(resolved.member_found);
    assert_eq!(
        resolved.member.type_,
        "(value: string, retries?: number) => boolean"
    );

    require_admin_signature(
        fixture.help(&template_path, template_source, " 2"),
        "run(value: string, retries?: number): boolean",
        1,
    );
    require_admin_signature(
        fixture.help(&template_path, template_source, "handler)"),
        "$emit(event: string, ...args: unknown[]): void",
        1,
    );
}

#[test]
fn admin_signature_help_for_dynamic_scoped_slot_overloads() {
    let fixture = admin_fixture();
    for (name, dir, type_) in [
        ("sw-card-a", "a", "(value: string) => boolean"),
        ("sw-card-b", "b", "(value: number) => boolean"),
    ] {
        fixture
            .index
            .save_component(&{
                let mut component = VueComponent::default();
                component.name = name.to_string();
                component.file_path = p(&fixture.admin_root.join(dir).join("index.ts"));
                component.slots = vec![VueComponentSlot {
                    name: "row".to_string(),
                    members: vec![VueComponentSlotMember {
                        name: "selectItem".to_string(),
                        type_: type_.to_string(),
                        ..Default::default()
                    }],
                    ..Default::default()
                }];
                component
            })
            .unwrap();
    }
    let path = fixture.admin_root.join("consumer.html.twig");
    let source = "<component :is=\"active ? 'sw-card-a' : 'sw-card-b'\"><template #row=\"props\">{{ props.selectItem('value') }}</template></component>";
    let result = fixture.help(&path, source, "'value'").unwrap();
    assert_eq!(result.signatures.len(), 2);
    let mut labels: Vec<_> = result.signatures.iter().map(|s| s.label.clone()).collect();
    labels.sort();
    assert_eq!(
        labels,
        vec![
            "selectItem(value: number): boolean",
            "selectItem(value: string): boolean"
        ]
    );
    assert!(doc(&result, 0).contains("sw-card-"));
    assert!(doc(&result, 1).contains("sw-card-"));
}

#[test]
fn admin_signature_help_for_pinia_actions() {
    let fixture = admin_fixture();
    let path = fixture.admin_root.join("store/session.ts");
    let source = "Shopware.Store.register('session', {
    actions: {
        login(user: string, remember?: boolean): Promise<boolean> {
            return Promise.resolve(true);
        },
    },
});
Shopware.Store.get('session').login('admin', true);";
    fixture.index(&path, source);
    let result = require_admin_signature(
        fixture.help(&path, source, "true);"),
        "login(user: string, remember?: boolean): Promise<boolean>",
        1,
    );
    assert!(doc(&result, 0).contains("session"));
}

#[test]
fn admin_signature_help_for_shopware_utils() {
    let fixture = admin_fixture();
    fixture.index(
        &fixture.admin_root.join("core/service/util.service.ts"),
        "export const format = { date };
export default { format };
function date(value: string, options?: { locale: string }): string { return value; }",
    );
    let consumer = fixture.admin_root.join("module/example/index.ts");
    for source in [
        "Shopware.Utils.format.date('2026-01-01', { locale: 'de-DE' });",
        "const format = Shopware.Utils.format; format.date('2026-01-01', { locale: 'de-DE' });",
        "const { date: formatDate } = Shopware.Utils.format; formatDate('2026-01-01', { locale: 'de-DE' });",
    ] {
        let result = require_admin_signature(
            fixture.help(&consumer, source, "locale: 'de-DE'"),
            "date(value: string, options?: { locale: string }): string",
            1,
        );
        assert!(doc(&result, 0).contains("Shopware.Utils.format.date"));
    }
}

#[test]
fn admin_signature_help_specializes_shopware_event_bus_payloads() {
    let fixture = admin_fixture();
    fixture.index(
        &fixture.admin_root.join("core/service/util.service.ts"),
        "
import EventBus from './utils/eventBus.utils';
export default { EventBus };",
    );
    fixture.index(
        &fixture
            .admin_root
            .join("core/service/utils/eventBus.utils.ts"),
        "
interface Events extends Record<string | symbol, unknown> {
    save: string;
    done: undefined;
}
const emitter = mitt<Events>();
export default emitter;",
    );
    let consumer = fixture.admin_root.join("module/example/index.ts");
    for (source, needle, label, active) in [
        (
            "Shopware.Utils.EventBus.emit('save', 'id');",
            "'id'",
            "emit(event: \"save\", payload: string): void",
            1,
        ),
        (
            "const { EventBus } = Shopware.Utils;
EventBus.on('save', handler);",
            "handler",
            "on(event: \"save\", handler: (payload: string) => void): void",
            1,
        ),
        (
            "const bus = Shopware.Utils.EventBus;
bus.off('save', handler);",
            "handler",
            "off(event: \"save\", handler?: (payload: string) => void): void",
            1,
        ),
        (
            "Shopware.Utils.EventBus.emit('done');",
            "done",
            "emit(event: \"done\", payload?: undefined): void",
            0,
        ),
    ] {
        let result =
            require_admin_signature(fixture.help(&consumer, source, needle), label, active);
        assert!(doc(&result, 0).contains("Shopware EventBus event"));
    }
}

#[test]
fn admin_signature_help_for_filter_bindings() {
    let fixture = admin_fixture();
    fixture.index(
        &fixture.admin_root.join("app/filter/currency.ts"),
        "Shopware.Filter.register('currency', (value: number, code: string): string => String(value));",
    );
    for source in [
        "const currencyFilter = Shopware.Filter.getByName('currency'); currencyFilter(42, 'EUR');",
        "Shopware.Filter.getByName('currency')(42, 'EUR');",
    ] {
        require_admin_signature(
            fixture.help(&fixture.admin_root.join("consumer.ts"), source, "'EUR'"),
            "currency(value: number, code: string): string",
            1,
        );
    }
}
