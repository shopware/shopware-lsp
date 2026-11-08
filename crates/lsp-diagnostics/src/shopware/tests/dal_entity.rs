use std::sync::Arc;

use serde_json::json;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_shopware_dal as dal;
use tempfile::TempDir;

use super::analyze;
use crate::shopware::*;
use crate::test_support::{diagnostics_document, document};

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
fn dal_entity_analyzer_reports_only_close_static_misspellings() {
    let (_dir, index) = dal_entity_index();
    let source = "
Shopware.Service('repositoryFactory').create('prodcut');
Shopware.EntityDefinition.get('runtime_custom_entity');
Shopware.EntityDefinition.has('prodcut');
Shopware.EntityDefinition.get(dynamicName);";
    let document = diagnostics_document(
        "file:///project/src/Resources/app/administration/consumer.ts",
        source,
    );
    let problems = analyze(&DalEntityAnalyzer::new(Some(index)), &document);
    assert_eq!(problems.len(), 1);
    let problem = &problems[0];
    assert_eq!(problem.id.as_str(), "shopware.dal.entity-not-found");
    assert_eq!(&source[problem.range], "prodcut");
    assert_eq!(
        problem.payload.as_ref().unwrap()["suggestions"],
        json!(["product"])
    );
}

#[test]
fn dal_entity_analyzer_handles_embedded_vue_script() {
    let (_dir, index) = dal_entity_index();
    let source = r#"<template><div title="prodcut" /></template>
<script setup lang="ts">
const definition = Shopware.EntityDefinition.get('prodcut');
</script>"#;
    let document = document(
        "file:///project/src/Resources/app/administration/Card.vue",
        source,
    );
    let problems = analyze(&DalEntityAnalyzer::new(Some(index)), &document);
    assert_eq!(problems.len(), 1);
    assert_eq!(
        usize::from(problems[0].range.start()),
        source.rfind("prodcut").unwrap()
    );
}
