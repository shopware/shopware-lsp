use crate::completion::DalCompletionProvider;
use std::sync::Arc;
use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::{CompletionProvider, CompletionRequest, SyntaxContext, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextSize};

fn ctx() -> swlsp_lsp::Context {
    swlsp_lsp::Context::background()
}

fn document(uri: &str, source: &str) -> Arc<TextDocument> {
    Arc::new(TextDocument::new(uri, source, 1))
}

fn node_at(document: &TextDocument, offset: usize) -> Option<SyntaxNode> {
    document.root()?.node_at(TextSize::from(offset as u32))
}

fn index_file(indexer: &dyn Indexer, path: &str, source: &str) {
    indexer
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
}

fn labels(items: &[protocol::CompletionItem]) -> Vec<String> {
    items.iter().map(|item| item.label.clone()).collect()
}

fn tempdir() -> tempfile::TempDir {
    tempfile::tempdir().unwrap()
}

fn dal_index(cache: &tempfile::TempDir) -> Arc<swlsp_shopware_dal::Index> {
    let index = Arc::new(swlsp_shopware_dal::Index::new(cache.path(), None).unwrap());
    index_file(
        index.as_ref(),
        "/project/src/Core/Content/Product/ProductDefinition.php",
        r"<?php
class ProductDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'product'; }
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new IdField('id', 'id'),
            new BoolField('active', 'active'),
            new DateTimeField('created_at', 'createdAt'),
            new ManyToOneAssociationField('manufacturer', 'manufacturer_id', ManufacturerDefinition::class),
        ]);
    }
}",
    );
    index
}

fn positioned(document: &Arc<swlsp_lsp::TextDocument>, offset: usize) -> CompletionRequest {
    let (line, character) = document.line_index().position_utf16(offset as u32);
    let mut params = protocol::CompletionParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = protocol::Position { line, character };
    CompletionRequest {
        params,
        syntax: SyntaxContext {
            language: document.language,
            root: document.root(),
            token: None,
            node: node_at(document, offset),
            document: Some(document.clone()),
        },
    }
}

#[test]
fn dal_completion_provides_javascript_criteria_fields() {
    let cache = tempdir();
    let provider = DalCompletionProvider::new(dal_index(&cache));
    let cases: [(&str, &str, &[&str], &[&str]); 4] = [
        (
            "filter field",
            "Criteria.equals('act', true)",
            &["active", "id", "manufacturer"],
            &[],
        ),
        (
            "association",
            "criteria.addAssociation('man')",
            &["manufacturer"],
            &["active", "id"],
        ),
        (
            "aggregation field",
            "Criteria.max('latest', 'created')",
            &["createdAt"],
            &[],
        ),
        (
            "entity definition",
            "Shopware.EntityDefinition.get('pro')",
            &["product"],
            &["active", "manufacturer"],
        ),
    ];
    for (name, source, present, absent) in cases {
        let offset = if name == "filter field" || name == "association" {
            source.find('\'').unwrap() + 2
        } else {
            source.rfind('\'').unwrap() - 1
        };
        let document = document(
            "file:///project/src/Resources/app/administration/index.js",
            source,
        );
        let labels = labels(&provider.get_completions(&ctx(), &positioned(&document, offset)));
        for expected in present {
            assert!(labels.contains(&expected.to_string()), "{name}: {expected}");
        }
        for unexpected in absent {
            assert!(
                !labels.contains(&unexpected.to_string()),
                "{name}: {unexpected}"
            );
        }
    }
}

#[test]
fn dal_completion_provides_entities_in_vue_script() {
    let cache = tempdir();
    let source = r#"<template><div /></template>
<script setup lang="ts">
const definition = Shopware.EntityDefinition.get('pro');
</script>"#;
    let document = document(
        "file:///project/src/Resources/app/administration/Card.vue",
        source,
    );
    let offset = source.find("'pro'").unwrap() + 2;
    let items = DalCompletionProvider::new(dal_index(&cache))
        .get_completions(&ctx(), &positioned(&document, offset));
    assert!(labels(&items).contains(&"product".to_string()));
}
