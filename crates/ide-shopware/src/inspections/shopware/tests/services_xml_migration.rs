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
