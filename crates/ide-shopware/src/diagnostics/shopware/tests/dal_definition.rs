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

const ENTITY: (&str, &str) = (
    "/project/src/BundleEntity.php",
    r"<?php
namespace Acme\Bundle;

use Shopware\Core\Framework\DataAbstractionLayer\Entity;
use Shopware\Core\Framework\DataAbstractionLayer\EntityIdTrait;

class BaseEntity extends Entity
{
    protected ?string $inherited = null;
    public function getInherited(): ?string { return $this->inherited; }
    public function setInherited(?string $inherited): void {}
}

class BundleEntity extends BaseEntity
{
    use EntityIdTrait;

    public string $name = '';
    private string $secret = '';
    public readonly string $frozen;
    protected bool $active = false;
    protected int $stock = 0;
    protected string $label = '';
    protected int $childCount = 0;
    protected ?string $displayName = null;

    public function isActive(): bool { return $this->active; }
    public function setActive(bool $active): void {}
    public function getStock(): int { return $this->stock; }
    public function setLabel(string $label): void {}
    public function getChildCount(): int { return $this->childCount; }
    public function getDisplayName(): ?string { return $this->displayName; }
}
",
);

const DEFINITION: &str = r"<?php
namespace Acme\Bundle;

use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;

class BundleDefinition extends EntityDefinition
{
    public function getEntityName(): string { return 'acme_bundle'; }
    public function getEntityClass(): string { return BundleEntity::class; }

    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            (new IdField('id', 'id'))->addFlags(new PrimaryKey(), new Required()),
            new StringField('name', 'name'),
            new StringField('inherited', 'inherited'),
            new StringField('secret', '{|shopware.dal.entity_property.private:secret|}'),
            new StringField('frozen', '{|shopware.dal.entity_property.readonly:frozen|}'),
            new BoolField('active', 'active'),
            new IntField('stock', '{|shopware.dal.entity_property.setter_missing:stock|}'),
            new StringField('label', '{|shopware.dal.entity_property.getter_missing:label|}'),
            (new IntField('child_count', 'childCount'))->addFlags(new Computed()),
            (new StringField('display_name', 'displayName'))->addFlags(new Runtime()),
            new StringField('description', '{|shopware.dal.entity_property.missing:description|}'),
            new ManyToOneAssociationField('{|shopware.dal.entity_property.missing:product|}', 'product_id', ProductDefinition::class),
            new ReferenceVersionField(ProductDefinition::class, 'product_version_id'),
        ]);
    }
}
";

#[test]
fn definition_fields_need_a_property_the_dal_can_fill() {
    let (_dir, index) = php_index_with(&[FRAMEWORK, ARRAY_ENTITY, ENTITY]);
    let (document, problems) = check_problems(
        &DalDefinitionAnalyzer::new(Some(index)),
        "file:///project/src/BundleDefinition.php",
        DEFINITION,
    );
    assert!(document.parse_errors().is_empty());
    let messages: Vec<&str> = problems
        .iter()
        .map(|problem| problem.message.as_str())
        .collect();
    assert_eq!(
        messages,
        [
            "BundleEntity::$secret is private; the DAL cannot fill private properties",
            "BundleEntity::$frozen is readonly; the DAL cannot fill it",
            "BundleEntity::$stock is protected and has no setter",
            "BundleEntity::$label is protected and has no getter",
            "Entity BundleEntity has no property $description for this field",
            "Entity BundleEntity has no property $product for this field",
        ]
    );
}

#[test]
fn definitions_without_a_property_entity_are_left_alone() {
    let (_dir, index) = php_index_with(&[FRAMEWORK, ARRAY_ENTITY, ENTITY]);
    let analyzer = DalDefinitionAnalyzer::new(Some(index));
    for entity_class in [
        // The default entity keeps its values in an array.
        "",
        "public function getEntityClass(): string { return \\Shopware\\Core\\Framework\\Struct\\ArrayEntity::class; }",
        // Not indexed: nothing is known about it.
        "public function getEntityClass(): string { return MissingEntity::class; }",
    ] {
        check_problems(
            &analyzer,
            "file:///project/src/OtherDefinition.php",
            &format!(
                r"<?php
namespace Acme\Bundle;

use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;

class OtherDefinition extends EntityDefinition
{{
    public function getEntityName(): string {{ return 'acme_other'; }}
    {entity_class}

    protected function defineFields(): FieldCollection
    {{
        return new FieldCollection([new StringField('unknown', 'unknown')]);
    }}
}}
"
            ),
        );
    }
}
