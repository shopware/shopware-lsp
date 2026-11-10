use super::*;
use crate::definition::php_common::test_support::*;
use std::path::Path;
use std::sync::Arc;
use swlsp_indexer::Indexer;
use swlsp_uriutil::file_uri;

fn cache(dir: &Path) -> std::path::PathBuf {
    dir.join("cache")
}

fn admin_snippet_index(root: &Path) -> (Arc<swlsp_snippet::SnippetIndexer>, String) {
    let index = Arc::new(swlsp_snippet::SnippetIndexer::new(cache(root), None).unwrap());
    let snippet_path =
        p(&root.join("src/Resources/app/administration/src/module/demo/snippet/en-GB.json"));
    index_file(
        &*index,
        &snippet_path,
        r#"{"admin":{"settings":{"title":"Settings"}}}"#,
    );
    (index, snippet_path)
}

fn index_file(indexer: &dyn Indexer, path: &str, source: &str) {
    index(indexer, path, source);
}

// -- DAL ----------------------------------------------------------------------

const PRODUCT_PATH: &str = "/project/src/Core/Content/Product/ProductDefinition.php";

const MANUFACTURER_PATH: &str = "/project/src/Core/Content/Product/ManufacturerDefinition.php";

fn dal_index(root: &Path) -> Arc<swlsp_shopware_dal::Index> {
    let index = Arc::new(swlsp_shopware_dal::Index::new(cache(root), None).unwrap());
    index_file(
        &*index,
        PRODUCT_PATH,
        "<?php
class ProductDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new IdField('id', 'id'),
            new ManyToOneAssociationField('manufacturer', 'manufacturer_id', ManufacturerDefinition::class),
        ]);
    }
}",
    );
    index_file(
        &*index,
        MANUFACTURER_PATH,
        "<?php
class ManufacturerDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product_manufacturer'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new StringField('name', 'name'),
        ]);
    }
}",
    );
    index
}

#[test]
fn admin_snippet_definition_inside_vue_bound_twig_attribute() {
    let root = tempfile::tempdir().unwrap();
    let (index, snippet_path) = admin_snippet_index(root.path());
    let source = r#"<mt-button :label="$t('admin.settings.title')" />"#;
    let document = document(
        "file:///project/src/Resources/app/administration/view.html.twig",
        source,
    );
    let request = request_at(&document, index_of(source, "admin.settings.title") + 3);
    let locations = definitions(&SnippetDefinitionProvider::new(Some(index)), &request);
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(&snippet_path));
}

#[test]
fn admin_snippet_definition_in_javascript_references() {
    let root = tempfile::tempdir().unwrap();
    let (index, snippet_path) = admin_snippet_index(root.path());
    let provider = SnippetDefinitionProvider::new(Some(index));
    for source in [
        "Shopware.Snippet.tc('admin.settings.title')",
        "Module.register('demo', {\n            description: 'admin.settings.title',\n        });",
    ] {
        let document = document(
            "file:///project/src/Resources/app/administration/index.js",
            source,
        );
        let request = request_at(&document, index_of(source, "admin.settings.title") + 3);
        let locations = definitions(&provider, &request);
        assert_eq!(locations.len(), 1, "{source}");
        assert_eq!(locations[0].uri, file_uri(&snippet_path));
    }
}

#[test]
fn dal_definition_navigates_javascript_criteria_path_segments() {
    let root = tempfile::tempdir().unwrap();
    let provider = DalDefinitionProvider::new(Some(dal_index(root.path())));
    let source = "Criteria.equals('manufacturer.name', value)";
    let document = document(
        "file:///project/src/Resources/app/administration/index.js",
        source,
    );
    for (segment, path) in [("manufacturer", PRODUCT_PATH), ("name", MANUFACTURER_PATH)] {
        let locations = definitions(
            &provider,
            &request_at(&document, index_of(source, segment) + 1),
        );
        assert_eq!(locations.len(), 1, "{segment}");
        assert_eq!(locations[0].uri, file_uri(path));
    }
}

#[test]
fn dal_definition_restricts_association_references() {
    let root = tempfile::tempdir().unwrap();
    let provider = DalDefinitionProvider::new(Some(dal_index(root.path())));
    let source = "criteria.addAssociation('manufacturer')";
    let document = document(
        "file:///project/src/Resources/app/administration/index.ts",
        source,
    );
    let locations = definitions(
        &provider,
        &request_at(&document, index_of(source, "manufacturer") + 1),
    );
    assert_eq!(locations.len(), 1);
    assert_eq!(locations[0].uri, file_uri(PRODUCT_PATH));
}

#[test]
fn dal_definition_navigates_entity_definition_lookup() {
    let root = tempfile::tempdir().unwrap();
    let provider = DalDefinitionProvider::new(Some(dal_index(root.path())));
    for source in [
        "Shopware.EntityDefinition.get('product')",
        "const { EntityDefinition } = Shopware; EntityDefinition.has('product')",
        "Shopware.Service('repositoryFactory').create('product')",
    ] {
        let document = document(
            "file:///project/src/Resources/app/administration/index.ts",
            source,
        );
        let locations = definitions(
            &provider,
            &request_at(&document, last_index_of(source, "product") + 1),
        );
        assert_eq!(locations.len(), 1, "{source}");
        assert_eq!(locations[0].uri, file_uri(PRODUCT_PATH));
    }
}
