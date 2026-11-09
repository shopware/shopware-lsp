use swlsp_indexer::{Indexer, ParsedFile, PreparingIndexer};
use swlsp_parser_js::{query as jsquery, syntax as js};
use swlsp_parser_php::{query as phpquery, syntax as php};
use swlsp_parser_twig::{query as twigquery, syntax as twig};
use swlsp_syntax::SyntaxNode;

use super::*;

fn find(
    root: &SyntaxNode,
    kind: swlsp_syntax::Kind,
    value: impl Fn(&SyntaxNode) -> bool,
) -> SyntaxNode {
    root.descendants()
        .filter(|node| node.kind() == kind)
        .find(value)
        .expect("literal")
}

#[test]
fn dal_reference_contexts() {
    let js_root = swlsp_parser_js::parse("Shopware.Service('repositoryFactory').create('product')")
        .tree
        .root();
    let entity = find(&js_root, js::JS_STRING, |node| {
        jsquery::string_value(node) == "product"
    });
    assert!(is_js_entity_reference(&entity));

    for (source, kind) in [
        (
            "Shopware.EntityDefinition.get('product')",
            JsEntityReferenceKind::DefinitionGet,
        ),
        (
            "const { EntityDefinition } = Shopware; EntityDefinition.has('product')",
            JsEntityReferenceKind::DefinitionHas,
        ),
        (
            "this.EntityDefinition.get('product')",
            JsEntityReferenceKind::DefinitionGet,
        ),
    ] {
        let root = swlsp_parser_js::parse(source).tree.root();
        let literal = jsquery::nodes(&root, &[js::JS_STRING]).remove(0);
        let reference = js_entity_reference_at(&literal).expect(source);
        assert_eq!(reference.name, "product");
        assert_eq!(reference.kind, kind, "{source}");
    }

    let php_root = swlsp_parser_php::parse("<?php new EqualsFilter('manufacturerId', $id);")
        .tree
        .root();
    let php_string = find(&php_root, php::PHP_STRING, |node| {
        phpquery::string_value(node) == "manufacturerId"
    });
    assert!(is_php_field_reference(&php_string));

    let twig_root = swlsp_parser_twig::parse(
        "{% set products = services.repository.search('product', hook.request) %}",
    )
    .tree
    .root();
    let twig_entity = find(&twig_root, twig::TWIG_LITERAL_STRING, |node| {
        twigquery::string_value(node).as_deref() == Some("product")
    });
    assert!(is_twig_entity_reference(&twig_entity));
}

#[test]
fn javascript_criteria_field_reference_contexts() {
    for (source, key, kind) in [
        (
            "Criteria.equals('manufacturer.name', value)",
            "manufacturer.name",
            JsFieldReferenceKind::Field,
        ),
        (
            "Criteria.equalsAny('id', ids)",
            "id",
            JsFieldReferenceKind::Field,
        ),
        (
            "Criteria.sort('createdAt', 'DESC')",
            "createdAt",
            JsFieldReferenceKind::Field,
        ),
        (
            "Criteria.max('latest', 'createdAt')",
            "createdAt",
            JsFieldReferenceKind::Field,
        ),
        (
            "criteria.addAssociation('manufacturer.media')",
            "manufacturer.media",
            JsFieldReferenceKind::Association,
        ),
        (
            "criteria.getAssociation('prices')",
            "prices",
            JsFieldReferenceKind::Association,
        ),
        (
            "criteria.addFields('name', 'active')",
            "active",
            JsFieldReferenceKind::Field,
        ),
        ("Criteria.not('AND', [])", "AND", JsFieldReferenceKind::None),
        (
            "Criteria.max('aggregation-name', dynamicField)",
            "aggregation-name",
            JsFieldReferenceKind::None,
        ),
        (
            "other.sort('createdAt')",
            "createdAt",
            JsFieldReferenceKind::None,
        ),
    ] {
        let root = swlsp_parser_js::parse(source).tree.root();
        let literal = find(&root, js::JS_STRING, |node| {
            jsquery::string_value(node) == key
        });
        assert_eq!(js_field_reference_at(&literal), kind, "{source}");
    }
}

#[test]
fn javascript_criteria_field_reference_segment() {
    let source = "Criteria.equals('manufacturer.media.url', value)";
    let root = swlsp_parser_js::parse(source).tree.root();
    let literal = jsquery::nodes(&root, &[js::JS_STRING]).remove(0);
    for (needle, name, association) in [
        ("manufacturer", "manufacturer", true),
        ("media", "media", true),
        ("url", "url", false),
    ] {
        let offset = (source.find(needle).unwrap() + 1) as u32;
        assert_eq!(
            js_field_reference_segment(&literal, offset),
            (name.to_owned(), association)
        );
    }
}

fn index(source: &str, path: &str, idx: &Index) {
    idx.index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .expect("index");
}

#[test]
fn index_entity_definition_fields_and_associations() {
    let directory = tempfile::tempdir().unwrap();
    let idx = Index::new(directory.path(), None).unwrap();
    let source = "<?php
class ProductDefinition extends EntityDefinition
{
    public const ENTITY_NAME = 'product';

    public function getEntityName(): string
    {
        return self::ENTITY_NAME;
    }

    public function isInheritanceAware(): bool
    {
        return true;
    }

    protected function defineFields(): FieldCollection
    {
\t\treturn new FieldCollection([
\t\t\tnew IdField('id', 'id'),
\t\t\tnew VersionField(),
\t\t\tnew FkField('manufacturer_id', 'manufacturerId', ManufacturerDefinition::class),
\t\t\tnew ManyToOneAssociationField('manufacturer', 'manufacturer_id', ManufacturerDefinition::class),
\t\t\tnew TranslatedField('name'),
\t\t\t(new StringField('runtime_label', 'runtimeLabel'))->addFlags(new Runtime()),
\t\t]);
    }
}";
    index(
        source,
        "/project/src/Core/Content/Product/ProductDefinition.php",
        &idx,
    );

    let definitions = idx.definition("product").unwrap();
    assert_eq!(definitions.len(), 1);
    let product = &definitions[0];
    assert_eq!(product.class, "ProductDefinition");
    assert_eq!(product.kind, "entity");
    assert!(product.version_aware);
    assert!(product.inheritance_aware);
    assert_eq!(product.fields.len(), 6);
    assert!(product.fields[0].primary);
    assert!(product.fields[0].stored);
    assert_eq!(product.fields[1].name, "versionId");
    assert_eq!(product.fields[1].storage_name, "version_id");
    assert!(product.fields[1].primary);
    assert_eq!(product.fields[2].name, "manufacturerId");
    assert_eq!(product.fields[2].storage_name, "manufacturer_id");
    assert_eq!(product.fields[3].name, "manufacturer");
    assert!(product.fields[3].association);
    assert!(!product.fields[3].stored);
    assert!(!product.fields[4].stored);
    assert!(!product.fields[5].stored);
    let fields = idx.field_definitions("manufacturer", true).unwrap();
    assert_eq!(fields.len(), 1);
    assert_eq!(fields[0].entity, "product");
    assert_eq!(fields[0].field.type_, "ManyToOneAssociationField");

    idx.close().unwrap();
    let idx = Index::new(directory.path(), None).unwrap();
    let restarted = idx.definition("product").unwrap();
    assert_eq!(restarted.len(), 1);
    assert_eq!(restarted[0].kind, "entity");
    assert!(restarted[0].inheritance_aware);
    idx.close().unwrap();
}

#[test]
fn index_literal_entity_name() {
    let directory = tempfile::tempdir().unwrap();
    let idx = Index::new(directory.path(), None).unwrap();
    let source = "<?php
final class AcmeDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'acme'; }
    protected function defineFields(): FieldCollection { return new FieldCollection([]); }
}";
    index(source, "/project/src/AcmeDefinition.php", &idx);
    assert_eq!(idx.definition("acme").unwrap().len(), 1);
    idx.close().unwrap();
}

#[test]
fn index_explicit_version_awareness_overrides_field_inference() {
    let directory = tempfile::tempdir().unwrap();
    let idx = Index::new(directory.path(), None).unwrap();
    index(
        "<?php
class VersionDisabledDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'version_disabled';
    public function isVersionAware(): bool { return false; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new VersionField()]); }
}",
        "/project/src/VersionDisabledDefinition.php",
        &idx,
    );
    index(
        "<?php
class MappingDefinition extends MappingEntityDefinition {
    public const ENTITY_NAME = 'version_mapping';
    public function isVersionAware(): bool { return true; }
    protected function defineFields(): FieldCollection { return new FieldCollection([new ReferenceVersionField(ProductDefinition::class)]); }
}",
        "/project/src/MappingDefinition.php",
        &idx,
    );
    let disabled = idx.definition("version_disabled").unwrap();
    assert_eq!(disabled.len(), 1);
    assert!(!disabled[0].version_aware);
    let mapping = idx.definition("version_mapping").unwrap();
    assert_eq!(mapping.len(), 1);
    assert!(mapping[0].version_aware);
    idx.close().unwrap();
}

#[test]
fn index_keeps_custom_base_candidate_out_of_ordinary_definitions() {
    let directory = tempfile::tempdir().unwrap();
    let idx = Index::new(directory.path(), None).unwrap();
    index(
        "<?php
namespace Acme;
class CatalogModel extends AbstractRecord {
    public const ENTITY_NAME = 'acme_catalog';
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}",
        "/project/src/CatalogModel.php",
        &idx,
    );
    assert!(idx.definitions().unwrap().is_empty());
    let candidates = idx.unresolved_definitions().unwrap();
    assert_eq!(candidates.len(), 1);
    assert_eq!(candidates[0].kind, DEFINITION_KIND_UNRESOLVED);
    assert_eq!(candidates[0].fully_qualified_class, "Acme\\CatalogModel");
    idx.close().unwrap();
}

#[test]
fn index_entity_hierarchy_fields() {
    let directory = tempfile::tempdir().unwrap();
    let idx = Index::new(directory.path(), None).unwrap();
    index(
        "<?php
namespace Acme\\Content\\Node;
class NodeDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_node';
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        new ParentFkField(self::class),
        new ParentAssociationField(self::class, 'id'),
        new ChildrenAssociationField(self::class, 'queries'),
    ]); }
}",
        "/project/src/NodeDefinition.php",
        &idx,
    );
    let definitions = idx.definition("acme_node").unwrap();
    assert_eq!(definitions.len(), 1);
    let fields = &definitions[0].fields;
    assert_eq!(fields.len(), 4);
    assert_eq!(fields[1].name, "parentId");
    assert_eq!(fields[1].storage_name, "parent_id");
    assert_eq!(
        fields[1].target_class,
        "Acme\\Content\\Node\\NodeDefinition"
    );
    assert_eq!(fields[2].name, "parent");
    assert!(fields[2].association);
    assert_eq!(fields[3].name, "queries");
    assert!(fields[3].association);
    idx.close().unwrap();
}

#[test]
fn reindexing_a_file_that_stops_defining_entities_clears_it() {
    let directory = tempfile::tempdir().unwrap();
    let idx = Index::new(directory.path(), None).unwrap();
    let path = "/project/src/AcmeDefinition.php";
    index(
        "<?php
final class AcmeDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'acme'; }
    protected function defineFields(): FieldCollection { return new FieldCollection([]); }
}",
        path,
        &idx,
    );
    assert_eq!(idx.definition("acme").unwrap().len(), 1);
    index("<?php final class AcmeDefinition {}", path, &idx);
    assert!(idx.definition("acme").unwrap().is_empty());
    index(
        "<?php class AcmeDefinition extends EntityDefinition { const ENTITY_NAME = 'acme'; function defineFields() {} }",
        path,
        &idx,
    );
    idx.removed_files(&[path.to_owned()]).unwrap();
    assert!(idx.definition("acme").unwrap().is_empty());
    idx.close().unwrap();
}

#[test]
fn prepared_indexing_matches_direct_indexing() {
    let directory = tempfile::tempdir().unwrap();
    let path = "/project/src/AcmeDefinition.php";
    let source = "<?php
final class AcmeDefinition extends EntityDefinition {
    public function getEntityName(): string { return 'acme'; }
    protected function defineFields(): FieldCollection {
        return new FieldCollection([new IdField('id', 'id'), new StringField('name', 'name')]);
    }
}";
    let direct = Index::new(directory.path().join("direct"), None).unwrap();
    index(source, path, &direct);
    let prepared = Index::new(directory.path().join("prepared"), None).unwrap();
    let file = ParsedFile::new(path, source.as_bytes().to_vec());
    let data = prepared.prepare(&file).unwrap();
    prepared.index_prepared(&file, data.as_ref()).unwrap();

    let definitions = direct.definitions().unwrap();
    assert_eq!(definitions.len(), 1);
    assert_eq!(prepared.definitions().unwrap(), definitions);

    let cleared = ParsedFile::new(path, b"<?php final class AcmeDefinition {}".to_vec());
    let data = prepared.prepare(&cleared).unwrap();
    prepared.index_prepared(&cleared, data.as_ref()).unwrap();
    assert!(prepared.definition("acme").unwrap().is_empty());
    direct.close().unwrap();
    prepared.close().unwrap();
}
