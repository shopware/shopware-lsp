#![allow(unused_imports)]
use super::support::*;
use crate::*;

fn admin_src(root: &std::path::Path) -> std::path::PathBuf {
    root.join("src/Administration/Resources/app/administration/src")
}

#[test]
fn parse_admin_runtime_registries_test() {
    let source = "
Application.addServiceProvider('feature', factory)
    .addServiceProvider('acl', factory);
Shopware.Service().register('repositoryFactory', factory);

Shopware.Store.register('profile', {
    state() {
        return { currentUser: null, isLoading: false };
    },
    getters: {
        displayName() { return this.currentUser?.name; },
    },
    actions: {
        load() {},
    },
});
Shopware.Store.register({
    id: 'seoUrl',
    state: () => ({ currentSeoUrl: null }),
});";
    let file_path = "/project/src/Administration/Resources/app/administration/src/main.ts";
    let root = parse_js(source);
    let (services, stores) =
        parse_admin_runtime_registries(&root, file_path, &LineIndex::new(source));

    assert_eq!(services.len(), 3);
    let by_name: HashMap<String, AdminService> =
        services.into_iter().map(|s| (s.name.clone(), s)).collect();
    assert_eq!(
        by_name["feature"].kind,
        AdminServiceRegistrationKind::Provider
    );
    assert_eq!(by_name["feature"].line, 2);
    assert_eq!(by_name["acl"].line, 3);
    assert_eq!(
        by_name["repositoryFactory"].kind,
        AdminServiceRegistrationKind::Factory
    );

    assert_eq!(stores.len(), 2);
    let stores: HashMap<String, AdminStore> =
        stores.into_iter().map(|s| (s.name.clone(), s)).collect();
    assert_eq!(stores["profile"].line, 6);
    assert_eq!(stores["seoUrl"].line, 18);
    let kinds: HashMap<&str, AdminStoreMemberKind> = stores["profile"]
        .members
        .iter()
        .map(|m| (m.name.as_str(), m.kind))
        .collect();
    let types: HashMap<&str, &str> = stores["profile"]
        .members
        .iter()
        .map(|m| (m.name.as_str(), m.type_.as_str()))
        .collect();
    assert_eq!(kinds["currentUser"], AdminStoreMemberKind::State);
    assert_eq!(kinds["isLoading"], AdminStoreMemberKind::State);
    assert_eq!(kinds["displayName"], AdminStoreMemberKind::Getter);
    assert_eq!(kinds["load"], AdminStoreMemberKind::Action);
    assert_eq!(types["currentUser"], "null");
    assert_eq!(types["isLoading"], "boolean");
    assert_eq!(types["load"], "() => unknown");
    let member = stores["seoUrl"]
        .member("currentSeoUrl")
        .expect("currentSeoUrl");
    assert_eq!(member.kind, AdminStoreMemberKind::State);
}

#[test]
fn admin_directive_registry_index() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let file_path = path_string(
        &admin_src(root.path()),
        "app/directive/tooltip.directive.ts",
    );
    index_source(
        &idx,
        &file_path,
        "
const { Directive } = Shopware;
Directive.register('tooltip', { mounted() {} });
Shopware.Directive.register('autofocus', { mounted() {} });",
    );

    let directives = idx.get_all_directives().unwrap();
    assert_eq!(directives.len(), 2);
    let by_name: HashMap<String, AdminDirective> = directives
        .into_iter()
        .map(|d| (d.name.clone(), d))
        .collect();
    assert_eq!(by_name["tooltip"].line, 3);
    assert_eq!(by_name["autofocus"].file_path, file_path);

    index_source(&idx, &file_path, "Directive.register('popover', {});");
    let directives = idx.get_all_directives().unwrap();
    assert_eq!(directives.len(), 1);
    assert_eq!(directives[0].name, "popover");
}

#[test]
fn admin_filter_registry_index_and_live_overlay() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let file_path = path_string(&admin_src(root.path()), "app/filter/currency.filter.ts");
    let persisted = "
const { Filter } = Shopware;
Filter.register('currency', (value: number, currency: string): string => String(value));
Shopware.Filter.register('asset', (path: string) => path);";
    index_source(&idx, &file_path, persisted);

    let filters = idx.get_all_filters().unwrap();
    assert_eq!(filters.len(), 2);
    let by_name: HashMap<String, AdminFilter> =
        filters.into_iter().map(|f| (f.name.clone(), f)).collect();
    assert_eq!(by_name["currency"].line, 3);
    assert_eq!(
        by_name["currency"].signature,
        "(value: number, currency: string) => string"
    );
    assert_eq!(by_name["asset"].file_path, file_path);

    let live = "Shopware.Filter.register('date', (value: Date): string => value.toISOString());";
    let live_root = parse_js(live);
    idx.update_live_document(
        &file_path,
        Some(&live_root),
        live,
        Some(&LineIndex::new(live)),
    );
    let filters = idx.get_all_filters().unwrap();
    assert_eq!(filters.len(), 1);
    assert_eq!(filters[0].name, "date");
    assert!(idx.get_filter("currency").unwrap().is_empty());

    idx.remove_live_document(&file_path);
    assert_eq!(idx.get_all_filters().unwrap().len(), 2);
}

#[test]
fn parse_admin_cms_registrations_test() {
    let source = "
Shopware.Service('cmsService').registerCmsElement({
    name: 'hero',
    label: 'cms.hero.label',
    component: 'sw-cms-el-hero',
    configComponent: 'sw-cms-el-config-hero',
    previewComponent: 'sw-cms-el-preview-hero',
});
cmsService.registerCmsBlock({
    name: 'hero-grid',
    category: 'image',
    component: 'sw-cms-block-hero-grid',
    previewComponent: 'sw-cms-preview-hero-grid',
    slots: {
        left: { type: 'hero', default: { config: {} } },
        right: { type: 'image' },
    },
});";
    let registrations = parse_admin_cms_registrations(
        &parse_js(source),
        "/project/Resources/app/administration/src/cms.ts",
        &LineIndex::new(source),
    );
    assert_eq!(registrations.len(), 2);
    let element = &registrations[0];
    assert_eq!(element.kind, AdminCMSRegistrationKind::Element);
    assert_eq!(element.name, "hero");
    assert_eq!(element.label, "cms.hero.label");
    assert_eq!(element.component, "sw-cms-el-hero");
    assert_eq!(element.config_component, "sw-cms-el-config-hero");
    assert_eq!(element.preview_component, "sw-cms-el-preview-hero");
    assert_eq!(element.line, 3);
    assert!(element.name_range.start_character < element.name_range.end_character);

    let block = &registrations[1];
    assert_eq!(block.kind, AdminCMSRegistrationKind::Block);
    assert_eq!(block.name, "hero-grid");
    assert_eq!(block.category, "image");
    assert_eq!(block.slots.len(), 2);
    assert_eq!(block.slots[0].name, "hero");
    assert_eq!(block.slots[1].name, "image");
}

#[test]
fn admin_cms_registry_index() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let file_path = path_string(&admin_src(root.path()), "cms.ts");
    index_source(
        &idx,
        &file_path,
        "
Shopware.Service('cmsService').registerCmsElement({
    name: 'hero', component: 'sw-cms-el-hero',
});
Shopware.Service('cmsService').registerCmsBlock({
    name: 'hero-grid', slots: { content: { type: 'hero' } },
});",
    );
    let elements = idx
        .get_all_cms_registrations_by_kind(AdminCMSRegistrationKind::Element)
        .unwrap();
    assert_eq!(elements.len(), 1);
    assert_eq!(elements[0].name, "hero");
    let blocks = idx
        .get_cms_registration(AdminCMSRegistrationKind::Block, "hero-grid")
        .unwrap();
    assert_eq!(blocks.len(), 1);
    assert_eq!(blocks[0].slots[0].name, "hero");

    index_source(
        &idx,
        &file_path,
        "Shopware.Service('cmsService').registerCmsElement({ name: 'image' });",
    );
    let elements = idx
        .get_all_cms_registrations_by_kind(AdminCMSRegistrationKind::Element)
        .unwrap();
    assert_eq!(elements.len(), 1);
    assert_eq!(elements[0].name, "image");
    assert!(
        idx.get_all_cms_registrations_by_kind(AdminCMSRegistrationKind::Block)
            .unwrap()
            .is_empty()
    );
}

#[test]
fn admin_runtime_registry_index_prefers_production_definitions() {
    let root = temp_dir();
    let idx = new_indexer(root.path());
    let admin_root = admin_src(root.path());
    let production_path = path_string(&admin_root, "main.ts");
    let test_path = path_string(&admin_root, "main.spec.ts");
    index_source(
        &idx,
        &production_path,
        "
Shopware.Application.addServiceProvider('acl', factory);
Shopware.Store.register('session', { actions: { login() {} } });
",
    );
    index_source(
        &idx,
        &test_path,
        "
Shopware.Service().register('acl', mock);
Shopware.Store.register('session', { actions: { resetMock() {} } });
",
    );

    let services = idx.get_service("acl").unwrap();
    assert_eq!(services.len(), 1);
    assert_eq!(services[0].file_path, production_path);

    let stores = idx.get_store("session").unwrap();
    assert_eq!(stores.len(), 1);
    assert_eq!(stores[0].file_path, production_path);
    assert!(stores[0].member("login").is_some());
    assert!(stores[0].member("resetMock").is_none());
}

#[test]
fn parse_admin_setup_store_factory() {
    let source = "
interface ContextState { app: object; api: object; }
const currentUser = ref<User | null>(null);
const userPending = computed(() => !currentUser.value);
function login() {}
const logout = () => {};
const state: ContextState = reactive({ app: {}, api: {} });

export default function useSession() {
    return {
        ...state,
        currentUser,
        userPending,
        login,
        logout,
    };
}";
    let factory = parse_admin_store_factory(
        &parse_js(source),
        "/project/Resources/app/administration/src/use-session.ts",
        &LineIndex::new(source),
    );
    let bindings = setup_bindings(&parse_js(source), &LineIndex::new(source));
    let state_binding = bindings.get("state").expect("state binding");
    assert!(state_binding.object.is_some());
    let factory = factory.expect("factory");
    let members: HashMap<String, AdminStoreMember> = factory
        .members
        .into_iter()
        .map(|m| (m.name.clone(), m))
        .collect();
    for name in [
        "app",
        "api",
        "currentUser",
        "userPending",
        "login",
        "logout",
    ] {
        assert!(members.contains_key(name), "{name}");
    }
    assert!(!members.contains_key("state"));
    assert_eq!(members["currentUser"].kind, AdminStoreMemberKind::State);
    assert_eq!(members["userPending"].kind, AdminStoreMemberKind::Getter);
    assert_eq!(members["login"].kind, AdminStoreMemberKind::Action);
    assert_eq!(members["logout"].kind, AdminStoreMemberKind::Action);
    assert_eq!(members["currentUser"].line, 3);
    assert_eq!(members["login"].line, 5);
    assert_eq!(members["currentUser"].type_, "User | null");
    assert_eq!(members["login"].type_, "() => unknown");
}

#[test]
fn admin_imported_setup_store_resolves_independently() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let admin_root = admin_src(root.path());
    let store_path = path_string(&admin_root, "app/store/session.store.ts");
    let factory_path = path_string(&admin_root, "app/composables/use-session.ts");
    std::fs::create_dir_all(gopath::dir(&store_path)).unwrap();
    write_file(std::path::Path::new(&factory_path), "");

    let store_source = "
import useSession from '../composables/use-session';
export default Shopware.Store.register('session', useSession);";
    let factory_source = "
const currentUser = ref(null);
function setCurrentUser() {}
export default function useSession() {
    return { currentUser, setCurrentUser };
}";
    index_source(&idx, &store_path, store_source);
    index_source(&idx, &factory_path, factory_source);

    let stores = idx.get_store("session").unwrap();
    assert_eq!(stores.len(), 1);
    assert_eq!(stores[0].factory_name, "useSession");
    assert_eq!(stores[0].factory_path, factory_path);
    let current_user = stores[0].member("currentUser").expect("currentUser");
    assert_eq!(current_user.file_path, factory_path);
    let set_current_user = stores[0].member("setCurrentUser").expect("setCurrentUser");
    assert_eq!(set_current_user.kind, AdminStoreMemberKind::Action);

    let updated_factory = "
const languageId = ref('');
export default function useSession() {
    return { languageId };
}";
    index_source(&idx, &factory_path, updated_factory);
    let stores = idx.get_store("session").unwrap();
    assert_eq!(stores.len(), 1);
    assert!(stores[0].member("currentUser").is_none());
    assert!(stores[0].member("languageId").is_some());
}

#[test]
fn admin_service_resolves_imported_implementation() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let admin_root = admin_src(root.path());
    let registration_path = path_string(&admin_root, "app/main.ts");
    let implementation_path = path_string(&admin_root, "app/service/acl.service.ts");
    write_file(std::path::Path::new(&implementation_path), "");
    let source = "
import AclService from './service/acl.service';
Shopware.Application.addServiceProvider('acl', () => {
    return new AclService();
});";
    index_source(&idx, &registration_path, source);

    let services = idx.get_service("acl").unwrap();
    assert_eq!(services.len(), 1);
    assert_eq!(services[0].implementation_name, "AclService");
    assert_eq!(services[0].implementation_path, implementation_path);
}

#[test]
fn admin_fluent_services_keep_own_implementation() {
    let source = "
import FirstService from './first.service';
import SecondService from './second.service';
Application.addServiceProvider('first', () => new FirstService())
    .addServiceProvider('second', () => new SecondService());";
    let (services, _) = parse_admin_runtime_registries(
        &parse_js(source),
        "/project/Resources/app/administration/src/main.ts",
        &LineIndex::new(source),
    );
    assert_eq!(services.len(), 2);
    let by_name: HashMap<String, AdminService> =
        services.into_iter().map(|s| (s.name.clone(), s)).collect();
    assert_eq!(by_name["first"].implementation_name, "FirstService");
    assert_eq!(by_name["second"].implementation_name, "SecondService");
}
