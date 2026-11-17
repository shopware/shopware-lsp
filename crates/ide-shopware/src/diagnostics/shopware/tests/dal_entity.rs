use std::sync::Arc;

use serde_json::json;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_shopware_dal as dal;
use tempfile::TempDir;

use crate::diagnostics::shopware::*;
use crate::diagnostics::test_support::check_problems;

pub(crate) fn dal_entity_index() -> (TempDir, Arc<dal::Index>) {
    let dir = tempfile::tempdir().unwrap();
    let index = dal::Index::new(dir.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/project/src/Core/Content/Product/ProductDefinition.php",
            br"<?php
class ProductDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([new IdField('id', 'id')]);
    }
}"
            .to_vec(),
        ))
        .unwrap();
    (dir, Arc::new(index))
}

#[test]
fn dal_entity_analyzer_reports_static_names_no_definition_declares() {
    let (_dir, index) = dal_entity_index();
    let (_, problems) = check_problems(
        &DalEntityAnalyzer::new(Some(index)),
        "file:///project/src/Resources/app/administration/consumer.ts",
        "
Shopware.Service('repositoryFactory').create('{|shopware.dal.entity-not-found:prodcut|}');
Shopware.EntityDefinition.get('custom_entity_blog');
this.repositoryFactory.create('ce_blog');
this.repositoryFactory.create('{|shopware.dal.entity-not-found:swag_gone|}', '/swag-gone');
this.repositoryFactory.create('product');
Shopware.EntityDefinition.has('prodcut');
Shopware.EntityDefinition.get(dynamicName);",
    );
    assert_eq!(
        problems[0].payload.as_ref().unwrap()["suggestions"],
        json!(["product"])
    );
    assert_eq!(
        problems[1].message,
        "No entity definition declares 'swag_gone'; the repository cannot be created"
    );
    assert_eq!(
        problems[1].payload.as_ref().unwrap()["suggestions"],
        json!([])
    );
}

#[test]
fn dal_entity_analyzer_only_knows_what_is_missing_where_shopware_is_indexed() {
    // A plugin checked on its own: its entities are known, Shopware's are not.
    let dir = tempfile::tempdir().unwrap();
    let index = dal::Index::new(dir.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/project/src/SwagExampleDefinition.php",
            br"<?php
class SwagExampleDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'swag_example'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([new IdField('id', 'id')]);
    }
}"
            .to_vec(),
        ))
        .unwrap();
    check_problems(
        &DalEntityAnalyzer::new(Some(Arc::new(index))),
        "file:///project/src/Resources/app/administration/consumer.ts",
        "
this.repositoryFactory.create('product');
this.repositoryFactory.create('order_line_item');
this.repositoryFactory.create('swag_example');
this.repositoryFactory.create('{|shopware.dal.entity-not-found:swag_exampel|}');",
    );
}

#[test]
fn dal_entity_analyzer_handles_embedded_vue_script() {
    let (_dir, index) = dal_entity_index();
    check_problems(
        &DalEntityAnalyzer::new(Some(index)),
        "file:///project/src/Resources/app/administration/Card.vue",
        r#"<template><div title="prodcut" /></template>
<script setup lang="ts">
const definition = Shopware.EntityDefinition.get('{|shopware.dal.entity-not-found:prodcut|}');
</script>"#,
    );
}
