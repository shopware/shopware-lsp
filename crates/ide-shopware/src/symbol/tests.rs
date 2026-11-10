use super::*;
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{Context, WorkspaceSymbolProvider};
use swlsp_lsp_protocol::SymbolKind;

#[test]
fn dal_workspace_symbols_expose_technical_entity_and_field_names() {
    let cache = tempfile::tempdir().unwrap();
    let dal = Arc::new(swlsp_shopware_dal::Index::new(cache.path(), None).unwrap());
    dal.index(&ParsedFile::new(
        "/project/src/Core/Content/Product/ProductDefinition.php",
        br#"<?php
class ProductDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new IdField('id', 'id'),
            new StringField('product_number', 'productNumber'),
        ]);
    }
}"#
        .to_vec(),
    ))
    .unwrap();
    let provider = DalWorkspaceSymbolProvider::new(Some(dal));
    let entity = provider
        .workspace_symbols(&Context::background(), "productdef")
        .unwrap();
    assert_eq!(entity.len(), 1);
    assert_eq!(entity[0].name, "product");
    assert_eq!(entity[0].kind, SymbolKind::CLASS);
    assert!(entity[0].container_name.contains("ProductDefinition"));

    let fields = provider
        .workspace_symbols(&Context::background(), "productNumber")
        .unwrap();
    assert_eq!(fields.len(), 1);
    assert_eq!(fields[0].name, "productNumber");
    assert_eq!(fields[0].kind, SymbolKind::FIELD);
    assert!(fields[0].container_name.contains("product"));
}
