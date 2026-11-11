use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_shopware_dal as dal;
use tempfile::TempDir;

use super::php_semantic::php_index_with;
use crate::diagnostics::shopware::*;
use crate::diagnostics::test_support::check_problems;

const FRAMEWORK: (&str, &str) = (
    "/project/vendor/Dal.php",
    r"<?php
namespace Shopware\Core\Framework\DataAbstractionLayer;
class Entity {}
trait EntityIdTrait { protected string $id; public function getId(): string { return $this->id; } public function setId(string $id): void {} }
abstract class EntityDefinition {}
",
);

const ARRAY_ENTITY: (&str, &str) = (
    "/project/vendor/ArrayEntity.php",
    r"<?php
namespace Shopware\Core\Framework\Struct;
use Shopware\Core\Framework\DataAbstractionLayer\Entity;
class ArrayEntity extends Entity {}
",
);

const BASE_ENTITY: (&str, &str) = (
    "/project/src/BaseEntity.php",
    r"<?php
namespace Acme\Bundle;

use Shopware\Core\Framework\DataAbstractionLayer\Entity;

class BaseEntity extends Entity
{
    protected ?string $inherited = null;
    protected ?string $unreadable = null;
    public function getInherited(): ?string { return $this->inherited; }
    public function setInherited(?string $inherited): void {}
    public function setUnreadable(?string $unreadable): void {}
}
",
);

fn definition(entity_class: &str) -> String {
    format!(
        r"<?php
namespace Acme\Bundle;

use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;

class BundleDefinition extends EntityDefinition
{{
    public function getEntityName(): string {{ return 'acme_bundle'; }}
    {entity_class}

    protected function defineFields(): FieldCollection
    {{
        return new FieldCollection([
            (new IdField('id', 'id'))->addFlags(new PrimaryKey(), new Required()),
            new StringField('name', 'name'),
            new StringField('inherited', 'inherited'),
            new StringField('unreadable', 'unreadable'),
            new StringField('secret', 'secret'),
            new StringField('frozen', 'frozen'),
            new BoolField('active', 'active'),
            new IntField('stock', 'stock'),
            new StringField('label', 'label'),
            (new IntField('child_count', 'childCount'))->addFlags(new Computed()),
            (new StringField('display_name', 'displayName'))->addFlags(new Runtime()),
            new StringField('description', 'description'),
            new ManyToOneAssociationField('product', 'product_id', ProductDefinition::class),
            new ReferenceVersionField(ProductDefinition::class, 'product_version_id'),
        ]);
    }}
}}
"
    )
}

fn definitions(source: &str) -> (TempDir, Arc<dal::Index>) {
    let dir = tempfile::tempdir().unwrap();
    let index = dal::Index::new(dir.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/project/src/BundleDefinition.php",
            source.as_bytes().to_vec(),
        ))
        .unwrap();
    (dir, Arc::new(index))
}

const ENTITY: &str = r"<?php
namespace Acme\Bundle;

use Shopware\Core\Framework\DataAbstractionLayer\EntityIdTrait;

class {|shopware.dal.entity_property.getter_missing:{|shopware.dal.entity_property.missing:{|shopware.dal.entity_property.missing:BundleEntity|}|}|} extends BaseEntity
{
    use EntityIdTrait;

    public string $name = '';
    private string {|shopware.dal.entity_property.private:$secret|} = '';
    public readonly string {|shopware.dal.entity_property.readonly:$frozen|};
    protected bool $active = false;
    protected int {|shopware.dal.entity_property.setter_missing:$stock|} = 0;
    protected string {|shopware.dal.entity_property.getter_missing:$label|} = '';
    protected int $childCount = 0;
    protected ?string $displayName = null;

    public function isActive(): bool { return $this->active; }
    public function setActive(bool $active): void {}
    public function getStock(): int { return $this->stock; }
    public function setLabel(string $label): void {}
    public function getChildCount(): int { return $this->childCount; }
    public function getDisplayName(): ?string { return $this->displayName; }
}
";

#[test]
fn entities_need_a_property_the_dal_can_fill_for_every_field() {
    let (_php, php) = php_index_with(&[FRAMEWORK, ARRAY_ENTITY, BASE_ENTITY]);
    let (_dal, dal) = definitions(&definition(
        "public function getEntityClass(): string { return BundleEntity::class; }",
    ));
    let (document, problems) = check_problems(
        &DalDefinitionAnalyzer::new(Some(php), Some(dal)),
        "file:///project/src/BundleEntity.php",
        ENTITY,
    );
    assert!(document.parse_errors().is_empty());
    let mut messages: Vec<&str> = problems
        .iter()
        .map(|problem| problem.message.as_str())
        .collect();
    messages.sort_unstable();
    assert_eq!(
        messages,
        [
            "BundleEntity has no property $description for the field BundleDefinition defines",
            "BundleEntity has no property $product for the field BundleDefinition defines",
            "BundleEntity::$frozen is readonly; the DAL cannot fill the field BundleDefinition defines",
            "BundleEntity::$label is protected and has no getter (BundleDefinition)",
            "BundleEntity::$secret is private; the DAL cannot fill private properties (BundleDefinition)",
            "BundleEntity::$stock is protected and has no setter (BundleDefinition)",
            // Inherited, so reported on the class.
            "BundleEntity::$unreadable is protected and has no getter (BundleDefinition)",
        ]
    );
}

#[test]
fn entities_no_definition_hydrates_are_left_alone() {
    let (_php, php) = php_index_with(&[FRAMEWORK, ARRAY_ENTITY, BASE_ENTITY]);
    let plain = ENTITY
        .replace("{|shopware.dal.entity_property.getter_missing:", "")
        .replace("{|shopware.dal.entity_property.setter_missing:", "")
        .replace("{|shopware.dal.entity_property.missing:", "")
        .replace("{|shopware.dal.entity_property.private:", "")
        .replace("{|shopware.dal.entity_property.readonly:", "")
        .replace("|}", "");
    for entity_class in [
        // The default entity keeps its values in an array.
        "",
        "public function getEntityClass(): string { return OtherEntity::class; }",
    ] {
        let (_dal, dal) = definitions(&definition(entity_class));
        check_problems(
            &DalDefinitionAnalyzer::new(Some(php.clone()), Some(dal)),
            "file:///project/src/BundleEntity.php",
            &plain,
        );
    }
}
