mod common;

use std::sync::Arc;

use common::*;
use swlsp_lsp::TextDocument;
use swlsp_lsp_hover::DalHoverProvider;
use swlsp_shopware_dal::Index;

fn dal_index(dir: &std::path::Path, path: &str, source: &str) -> Arc<Index> {
    let index = Arc::new(Index::new(dir, None).unwrap());
    index_file(index.as_ref(), path, source);
    index
}

#[test]
fn dal_hover_describes_javascript_criteria_path_segment() {
    let cache = tempfile::tempdir().unwrap();
    let index = dal_index(
        cache.path(),
        "/project/src/Core/Content/Product/ManufacturerDefinition.php",
        r"<?php
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
    let source = "Criteria.equals('manufacturer.name', value)";
    let document = TextDocument::new(
        "file:///project/src/Resources/app/administration/index.js",
        source,
        1,
    );
    let request = request_at(document, source.find("name").unwrap() + 1);
    let result = hover(&DalHoverProvider::new(Some(index)), &request).expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("Shopware DAL field"));
    assert!(value.contains("product_manufacturer"));
    assert!(value.contains("StringField"));
}

#[test]
fn dal_hover_describes_administration_entity_reference() {
    let cache = tempfile::tempdir().unwrap();
    let index = dal_index(
        cache.path(),
        "/project/src/Core/Content/Product/ProductDefinition.php",
        r"<?php
class ProductDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([new IdField('id', 'id')]);
    }
}",
    );
    let source = "Shopware.EntityDefinition.get('product')";
    let document = TextDocument::new(
        "file:///project/src/Resources/app/administration/index.ts",
        source,
        1,
    );
    let request = request_at(document, source.find("product").unwrap() + 1);
    let result = hover(&DalHoverProvider::new(Some(index)), &request).expect("hover");
    let value = &result.contents.value;
    assert!(value.contains("Shopware DAL entity"));
    assert!(value.contains("ProductDefinition"));
    assert!(value.contains("Indexed fields: 1"));
}
