use std::collections::HashMap;
use std::sync::Arc;

use swlsp_lsp::{Context, DocumentSnapshot, TextDocument};
use swlsp_lsp_protocol::DELETE_FILE_OPERATION;
use swlsp_uriutil::file_uri;

use crate::inspections::shopware::*;
use crate::inspections::test_support::{
    StaticDocumentResolver, check_diagnostics, fix_context, quick_fix_with_id,
};

fn path_uri(path: &std::path::Path) -> String {
    file_uri(&path.to_string_lossy())
}

#[test]
fn services_xml_migration_builds_atomic_imported_file_conversion() {
    let root = tempfile::tempdir().unwrap();
    let config_dir = root.path().join("src/Resources/config");
    let packages_dir = config_dir.join("packages");
    std::fs::create_dir_all(&packages_dir).unwrap();
    let services_path = config_dir.join("services.xml");
    let services_source = r#"<container>
    <imports>
        <import resource="packages/listeners.xml" type="xml"/>
    </imports>
    <services>
        <service id="App\Example" class="App\Example"/>
    </services>
</container>"#;
    let listeners_path = packages_dir.join("listeners.xml");
    let listeners_source = r#"<container><services>
    <service id="App\Listener" class="App\Listener"/>
</services></container>"#;
    std::fs::write(&services_path, services_source).unwrap();
    std::fs::write(&listeners_path, listeners_source).unwrap();

    let listener_document = Arc::new(TextDocument::new(
        path_uri(&listeners_path),
        listeners_source,
        3,
    ));
    let resolver = StaticDocumentResolver(HashMap::from([(
        listener_document.uri.clone(),
        DocumentSnapshot {
            document: listener_document.clone(),
            version: Some(listener_document.version),
        },
    )]));

    let inspection = new_services_xml_migration();
    let (document, problems) = check_diagnostics(
        inspection.as_ref(),
        &path_uri(&services_path),
        &services_source.replacen(
            "<container>",
            "<{|symfony.services_xml.deprecated:container|}>",
            1,
        ),
    );
    let problem = &problems[0];
    assert_eq!(problem.fixes.len(), 1);
    assert_eq!(problem.fixes[0].id.as_str(), CONVERT_SERVICES_XML_FIX_ID);

    let fix = quick_fix_with_id(inspection.as_ref(), CONVERT_SERVICES_XML_FIX_ID);
    let plan = fix
        .as_rewrite()
        .unwrap()
        .build(
            &Context::background(),
            &fix_context(
                &document,
                problem,
                &problem.fixes[0],
                Some(Arc::new(resolver)),
            ),
        )
        .unwrap();
    assert_eq!(plan.creates.len(), 2);
    assert_eq!(plan.deletes.len(), 2);
    assert!(plan.documents.is_empty());

    let created: HashMap<_, _> = plan
        .creates
        .iter()
        .map(|file| (file.uri.clone(), file.content.clone()))
        .collect();
    let services_yaml = &created[&path_uri(&config_dir.join("services.yaml"))];
    assert!(services_yaml.contains("resource: packages/listeners.yaml"));
    assert!(services_yaml.contains("type: yaml"));
    assert!(created[&path_uri(&packages_dir.join("listeners.yaml"))].contains("App\\Listener"));

    let deleted: HashMap<_, _> = plan
        .deletes
        .iter()
        .map(|file| (file.uri.clone(), file.source.clone()))
        .collect();
    assert_eq!(deleted[&document.uri], services_source);
    assert_eq!(deleted[&listener_document.uri], listeners_source);

    let edit = plan.workspace_edit().unwrap();
    assert_eq!(edit.document_changes.len(), 6);
    assert_eq!(edit.document_changes[4].kind, DELETE_FILE_OPERATION);
    assert_eq!(edit.document_changes[5].kind, DELETE_FILE_OPERATION);

    // Preparing the code action must never mutate the project itself.
    assert!(services_path.exists());
    assert!(listeners_path.exists());
    assert!(!config_dir.join("services.yaml").exists());
}

#[test]
fn services_xml_migration_refuses_existing_yaml_target() {
    let root = tempfile::tempdir().unwrap();
    let config_dir = root.path().join("Resources/config");
    std::fs::create_dir_all(&config_dir).unwrap();
    let services_path = config_dir.join("services.xml");
    std::fs::write(&services_path, "<container/>").unwrap();
    std::fs::write(config_dir.join("services.yaml"), "services:\n").unwrap();

    let inspection = new_services_xml_migration();
    let (document, problems) = check_diagnostics(
        inspection.as_ref(),
        &path_uri(&services_path),
        "<{|symfony.services_xml.deprecated:container|}/>",
    );
    assert_eq!(problems[0].fixes.len(), 1);
    let fix = quick_fix_with_id(inspection.as_ref(), CONVERT_SERVICES_XML_FIX_ID);
    let error = fix
        .as_rewrite()
        .unwrap()
        .build(
            &Context::background(),
            &fix_context(&document, &problems[0], &problems[0].fixes[0], None),
        )
        .unwrap_err();
    assert!(
        error.to_string().contains("services.yaml exists already"),
        "{error}"
    );
}

#[test]
fn services_xml_migration_does_not_offer_lossy_conversion() {
    let root = tempfile::tempdir().unwrap();
    let (_, problems) = check_diagnostics(
        new_services_xml_migration().as_ref(),
        &path_uri(&root.path().join("Resources/config/services.xml")),
        r#"<{|symfony.services_xml.deprecated:container|}><services><stack id="app"/></services></container>"#,
    );
    assert!(problems[0].fixes.is_empty());
}

fn snapshot(path: &std::path::Path, source: &str) -> (String, DocumentSnapshot) {
    let document = Arc::new(TextDocument::new(path_uri(path), source, 3));
    (
        document.uri.clone(),
        DocumentSnapshot {
            version: Some(document.version),
            document,
        },
    )
}

/// XML files a `services.php` imports are not loaded by convention, so
/// nothing but the import shows that they are still in use.
#[test]
fn services_php_imports_of_xml_files_are_converted_and_repointed() {
    let root = tempfile::tempdir().unwrap();
    let config_dir = root.path().join("src/Resources/config");
    let services_dir = config_dir.join("services");
    std::fs::create_dir_all(&services_dir).unwrap();
    let extra_path = services_dir.join("extra.xml");
    let extra_source = r#"<container>
    <imports><import resource="nested.xml"/></imports>
    <services><service id="App\Extra" class="App\Extra"/></services>
</container>"#;
    let nested_path = services_dir.join("nested.xml");
    let nested_source = r#"<container><services><service id="App\Nested" class="App\Nested"/></services></container>"#;
    std::fs::write(&extra_path, extra_source).unwrap();
    std::fs::write(&nested_path, nested_source).unwrap();
    let resolver = StaticDocumentResolver(HashMap::from([
        snapshot(&extra_path, extra_source),
        snapshot(&nested_path, nested_source),
    ]));

    let php_path = config_dir.join("services.php");
    let inspection = new_services_xml_migration();
    let (document, problems) = check_diagnostics(
        inspection.as_ref(),
        &path_uri(&php_path),
        r"<?php
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $container): void {
    $container->import('{|symfony.services_xml.deprecated:services/extra.xml|}');
    $container->import('services/already.yaml');
    $container->import('services/*.xml');
    $container->import('@AcmeBundle/Resources/config/services.xml');
    $container->import($dynamic . '.xml');
};
",
    );
    let [problem] = problems.as_slice() else {
        panic!("expected one problem: {problems:?}");
    };
    assert_eq!(
        problem.message,
        "Symfony XML service configuration is deprecated; convert services/extra.xml to YAML"
    );
    assert_eq!(
        problem.fixes[0].id.as_str(),
        CONVERT_IMPORTED_SERVICES_XML_FIX_ID
    );

    let fix = quick_fix_with_id(inspection.as_ref(), CONVERT_IMPORTED_SERVICES_XML_FIX_ID);
    let plan = fix
        .as_rewrite()
        .unwrap()
        .build(
            &Context::background(),
            &fix_context(
                &document,
                problem,
                &problem.fixes[0],
                Some(Arc::new(resolver)),
            ),
        )
        .unwrap();
    let created: HashMap<_, _> = plan
        .creates
        .iter()
        .map(|file| (file.uri.clone(), file.content.clone()))
        .collect();
    assert_eq!(created.len(), 2);
    assert!(created[&path_uri(&services_dir.join("extra.yaml"))].contains("resource: nested.yaml"));
    assert!(created[&path_uri(&services_dir.join("nested.yaml"))].contains("App\\Nested"));
    let mut deleted: Vec<&str> = plan.deletes.iter().map(|file| file.uri.as_str()).collect();
    deleted.sort_unstable();
    assert_eq!(deleted, [path_uri(&extra_path), path_uri(&nested_path)]);
    let [php] = plan.documents.as_slice() else {
        panic!("expected one edited document");
    };
    assert_eq!(php.uri, document.uri);
    let updated = php.apply().unwrap();
    assert!(
        updated.contains("$container->import('services/extra.yaml');"),
        "{updated}"
    );
    assert!(updated.contains("import('services/*.xml')"));

    // Preparing the code action must never mutate the project itself.
    assert!(extra_path.exists());
    assert!(!services_dir.join("extra.yaml").exists());
}

/// Only the convention-loaded `services.php` is checked, and an imported
/// file that cannot be converted leaves the import alone.
#[test]
fn services_php_imports_are_scoped_and_fail_without_edits() {
    let inspection = new_services_xml_migration();
    check_diagnostics(
        inspection.as_ref(),
        "file:///project/config/routes.php",
        "<?php\nreturn static function ($routes): void {\n    $routes->import('routes/extra.xml');\n};\n",
    );

    let root = tempfile::tempdir().unwrap();
    let config_dir = root.path().join("src/Resources/config");
    std::fs::create_dir_all(&config_dir).unwrap();
    let broken_path = config_dir.join("broken.xml");
    // A method call that would pass null cannot be written in YAML.
    let broken_source = r#"<container><services><service id="a" class="A"><call method="set"><argument type="service" id="b" on-invalid="null"/></call></service></services></container>"#;
    std::fs::write(&broken_path, broken_source).unwrap();
    let resolver = StaticDocumentResolver(HashMap::from([snapshot(&broken_path, broken_source)]));
    let (document, problems) = check_diagnostics(
        inspection.as_ref(),
        &path_uri(&config_dir.join("services.php")),
        "<?php\nreturn static function ($container): void {\n    $container->import('{|symfony.services_xml.deprecated:broken.xml|}');\n};\n",
    );
    let fix = quick_fix_with_id(inspection.as_ref(), CONVERT_IMPORTED_SERVICES_XML_FIX_ID);
    let error = fix
        .as_rewrite()
        .unwrap()
        .build(
            &Context::background(),
            &fix_context(
                &document,
                &problems[0],
                &problems[0].fixes[0],
                Some(Arc::new(resolver)),
            ),
        )
        .unwrap_err();
    assert!(error.to_string().contains("on-invalid=\"null\""), "{error}");
}

#[test]
fn routes_xml_is_converted_with_the_route_files_it_imports() {
    let root = tempfile::tempdir().unwrap();
    let config_dir = root.path().join("src/Resources/config");
    let routes_dir = config_dir.join("routes");
    std::fs::create_dir_all(&routes_dir).unwrap();
    let routes_path = config_dir.join("routes.xml");
    let routes_source = r#"<routes>
    <import resource="routes/storefront.xml"/>
    <import resource="../../Controller" type="attribute"/>
    <route id="acme.ping" path="/acme/ping" controller="Acme\Controller\Ping" methods="GET|POST"/>
</routes>"#;
    let storefront_path = routes_dir.join("storefront.xml");
    let storefront_source = r#"<routes><route id="acme.page" path="/acme/page" controller="Acme\Controller\Page"/></routes>"#;
    std::fs::write(&routes_path, routes_source).unwrap();
    std::fs::write(&storefront_path, storefront_source).unwrap();
    let resolver = StaticDocumentResolver(HashMap::from([snapshot(
        &storefront_path,
        storefront_source,
    )]));

    let inspection = new_services_xml_migration();
    let (document, problems) = check_diagnostics(
        inspection.as_ref(),
        &path_uri(&routes_path),
        &routes_source.replacen("<routes>", "<{|symfony.routes_xml.deprecated:routes|}>", 1),
    );
    let problem = &problems[0];
    assert_eq!(
        problem.message,
        "Symfony routes.xml is deprecated; migrate to routes.yaml"
    );
    let fix = quick_fix_with_id(inspection.as_ref(), CONVERT_SERVICES_XML_FIX_ID);
    let context = fix_context(
        &document,
        problem,
        &problem.fixes[0],
        Some(Arc::new(resolver)),
    );
    assert_eq!(
        fix.present(&Context::background(), &context)
            .unwrap()
            .unwrap()
            .title,
        "Symfony: Convert routes.xml to routes.yaml"
    );
    let plan = fix
        .as_rewrite()
        .unwrap()
        .build(&Context::background(), &context)
        .unwrap();
    let created: HashMap<_, _> = plan
        .creates
        .iter()
        .map(|file| (file.uri.clone(), file.content.clone()))
        .collect();
    let routes_yaml = &created[&path_uri(&config_dir.join("routes.yaml"))];
    assert!(
        routes_yaml.contains("resource: routes/storefront.yaml"),
        "{routes_yaml}"
    );
    assert!(routes_yaml.contains("type: attribute"), "{routes_yaml}");
    assert!(
        routes_yaml.contains("methods: [GET, POST]"),
        "{routes_yaml}"
    );
    assert!(created[&path_uri(&routes_dir.join("storefront.yaml"))].contains("acme.page:"));
    assert_eq!(plan.deletes.len(), 2);

    // A services file is not a routes file, whatever it is called.
    let (_, problems) = check_diagnostics(
        inspection.as_ref(),
        &path_uri(&routes_path),
        "<{|symfony.routes_xml.deprecated:routes|}><something/></routes>",
    );
    assert!(problems[0].fixes.is_empty());
}

#[test]
fn routes_php_imports_of_xml_files_are_converted_and_repointed() {
    let root = tempfile::tempdir().unwrap();
    let config_dir = root.path().join("src/Resources/config");
    std::fs::create_dir_all(&config_dir).unwrap();
    let extra_path = config_dir.join("extra.xml");
    let extra_source = r#"<routes><route id="acme.extra" path="/acme/extra" controller="Acme\Controller\Extra"/></routes>"#;
    std::fs::write(&extra_path, extra_source).unwrap();
    let resolver = StaticDocumentResolver(HashMap::from([snapshot(&extra_path, extra_source)]));

    let inspection = new_services_xml_migration();
    let (document, problems) = check_diagnostics(
        inspection.as_ref(),
        &path_uri(&config_dir.join("routes.php")),
        "<?php\nreturn static function ($routes): void {\n    $routes->import('{|symfony.routes_xml.deprecated:extra.xml|}');\n};\n",
    );
    assert_eq!(
        problems[0].message,
        "Symfony XML route configuration is deprecated; convert extra.xml to YAML"
    );
    let fix = quick_fix_with_id(inspection.as_ref(), CONVERT_IMPORTED_SERVICES_XML_FIX_ID);
    let plan = fix
        .as_rewrite()
        .unwrap()
        .build(
            &Context::background(),
            &fix_context(
                &document,
                &problems[0],
                &problems[0].fixes[0],
                Some(Arc::new(resolver)),
            ),
        )
        .unwrap();
    assert_eq!(plan.creates.len(), 1);
    assert!(plan.creates[0].content.contains("acme.extra:"));
    assert!(
        plan.documents[0]
            .apply()
            .unwrap()
            .contains("$routes->import('extra.yaml');")
    );
}
