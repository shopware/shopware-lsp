#![allow(unused_imports)]
use super::support::*;
use crate::*;

#[test]
fn javascript_application_container_member_supports_direct_and_const_aliases() {
    let source = "
Shopware.Application.getContainer('factory').locale;
function run() {
    const services = Application.getContainer('service');
    services.repositoryFactory;
\tif (enabled) {
\t\tconst services = Application.getContainer('init');
\t\tservices.httpClient;
\t}
    services.loginService;
    let mutable = Application.getContainer('factory');
    mutable.module;
}
";
    let root = parse_js(source);
    for (needle, container, member, matched) in [
        ("locale", "factory", "locale", true),
        ("repositoryFactory", "service", "repositoryFactory", true),
        ("httpClient", "init", "httpClient", true),
        ("loginService", "service", "loginService", true),
        ("module", "", "", false),
    ] {
        let offset = if needle == "loginService" {
            source.rfind(needle)
        } else {
            source.find(needle)
        }
        .expect("needle");
        let node = root
            .node_at(TextSize::from(offset as u32 + 1))
            .expect("node");
        let result = javascript_application_container_member(&node);
        assert_eq!(result.is_some(), matched, "{needle}");
        let (actual_container, actual_member) = result.unwrap_or_default();
        assert_eq!(actual_container, container, "{needle}");
        assert_eq!(actual_member, member, "{needle}");
    }
}

#[test]
fn application_container_name_reference() {
    let source = "Application.getContainer('factory'); other.getContainer('service')";
    let root = parse_js(source);
    let literals = jsquery::nodes(&root, &[jssyntax::JS_STRING]);
    assert_eq!(literals.len(), 2);
    assert!(is_application_container_name_reference(&literals[0]));
    assert!(!is_application_container_name_reference(&literals[1]));
}

#[test]
fn resolve_application_container_merges_ambient_interfaces_and_runtime_services() {
    let root = temp_dir();
    let idx = new_indexer(&root.path().join("cache"));
    let admin_root = "src/Administration/Resources/app/administration/src";
    let global_path = path_string(root.path(), &format!("{admin_root}/global.types.ts"));
    let extension_path = path_string(root.path(), &format!("{admin_root}/module/extension.ts"));
    let registration_path = path_string(root.path(), &format!("{admin_root}/service.ts"));
    for (path, source) in [
        (
            &global_path,
            "
export interface SubContainer<T extends string> { $list(): string[]; }
declare global {
    interface ServiceContainer extends SubContainer<'service'> {
        acl: AclService;
    }
    interface ServiceContainer {
        localeHelper: LocaleHelper;
    }
    interface FactoryContainer extends SubContainer<'factory'> {
        locale: LocaleFactory;
    }
}",
        ),
        (
            &extension_path,
            "
import type { SubContainer } from '../global.types';
declare global {
    interface ServiceContainer extends SubContainer<'service'> {
        extensionSdkService: ExtensionSdkService;
    }
}",
        ),
        (
            &registration_path,
            "
class RuntimeService {}
Shopware.Application.addServiceProvider('runtimeService', () => new RuntimeService());",
        ),
    ] {
        index_source(&idx, path, source);
    }

    let service_shape = idx
        .resolve_application_container("service", &extension_path)
        .expect("service container");
    assert!(!service_shape.complete);
    let service_members: HashMap<String, TwigVueMember> = service_shape
        .members
        .iter()
        .map(|member| (member.name.clone(), member.clone()))
        .collect();
    for name in [
        "$list",
        "acl",
        "localeHelper",
        "extensionSdkService",
        "runtimeService",
    ] {
        assert!(service_members.contains_key(name), "{name}");
    }
    assert_eq!(service_members["acl"].type_, "AclService");
    assert_eq!(
        service_members["runtimeService"].definition_path,
        registration_path
    );

    let factory_shape = idx
        .resolve_application_container("factory", &extension_path)
        .expect("factory container");
    assert!(factory_shape.complete);
    assert!(vue_member_names(&factory_shape.members).contains(&"locale".to_owned()));
}
