//! DBAL type registration, type configuration and DBAL reference tests.

use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt, TextRange, TextSize};

use crate::candidate::doctrine_candidates;
use crate::{
    Index, TypeDeclaration, TypeRegistrationReferenceRole, type_declarations,
    type_registration_reference_at, type_registrations_in_document,
};

fn file(path: &str, source: &str) -> ParsedFile {
    ParsedFile::new(path, source.as_bytes().to_vec())
}

fn slice(source: &str, range: TextRange) -> &str {
    &source[usize::from(range.start())..usize::from(range.end())]
}

fn at(value: usize) -> TextSize {
    TextSize::from(value as u32)
}

fn php_root(source: &str) -> SyntaxNode {
    swlsp_parser_php::parse(source).tree.root()
}

#[test]
fn dbal_index_updates_registrations_across_restart_and_clear() {
    let cache = tempfile::tempdir().unwrap();
    let path = "/project/bootstrap.php";
    let registration =
        "<?php\nuse Doctrine\\DBAL\\Types\\Type;\nType::addType('money', MoneyType::class);";
    {
        let index = Index::new(cache.path(), None).unwrap();
        index.index(&file(path, registration)).unwrap();
        assert_eq!(index.type_registrations("money").unwrap().len(), 1);
        index.close().unwrap();
    }
    let index = Index::new(cache.path(), None).unwrap();
    assert_eq!(index.type_registrations("money").unwrap().len(), 1);
    // A file that stops being a candidate must still remove its former rows.
    index.index(&file(path, "<?php class Plain {}")).unwrap();
    assert!(index.type_registrations("money").unwrap().is_empty());
    index.index(&file(path, registration)).unwrap();
    index.clear().unwrap();
    assert!(index.type_registrations("money").unwrap().is_empty());
    index.close().unwrap();
}

#[test]
fn dbal_candidates_exclude_orm_odm_and_dql() {
    for (path, source) in [
        (
            "/project/entity.php",
            "<?php use Doctrine\\ORM\\Mapping as ORM; #[ORM\\Entity] class Product { #[ORM\\Column(type: 'string')] private string $name; }",
        ),
        (
            "/project/document.php",
            "<?php use Doctrine\\ODM\\MongoDB\\Mapping\\Annotations as ODM; #[ODM\\Document] class Product {}",
        ),
        (
            "/project/Product.orm.xml",
            "<doctrine-mapping><entity name=\"App\\Product\"/></doctrine-mapping>",
        ),
        (
            "/project/Product.orm.yaml",
            "App\\Product:\n  type: entity\n  fields:\n    name:\n      type: string\n",
        ),
        (
            "/project/query.php",
            "<?php $dql = 'SELECT p FROM App\\Product p'; $manager->createQuery($dql);",
        ),
    ] {
        assert_eq!(doctrine_candidates(&file(path, source)), 0, "{path}");
    }
}

#[test]
fn dbal_table_column_and_alias_intelligence() {
    let directory = tempfile::tempdir().unwrap();
    let index = Index::new(directory.path().join("doctrine"), None).unwrap();
    let php_index = PhpIndex::open(directory.path().join("php"), None).unwrap();
    let schema =
        Arc::new(swlsp_shopware_dal::Index::new(directory.path().join("dal"), None).unwrap());
    index.set_dal_index(schema.clone());
    schema
        .index(&file(
            "/project/src/UserDefinition.php",
            "<?php
class UserDefinition extends EntityDefinition {
 public const ENTITY_NAME = 'cms_users';
 protected function defineFields(): FieldCollection {return new FieldCollection([new StringField('name','name'),new StringField('user_email','email')]);}
}",
        ))
        .unwrap();
    let stubs = "<?php
namespace Doctrine\\DBAL;
class Connection {
    public function insert(string $table, array $data): void {}
    public function update(string $table, array $data): void {}
}
namespace Doctrine\\DBAL\\Query;
class QueryBuilder {
    public function from(string $table, ?string $alias = null): self {}
    public function update(string $table): self {}
    public function insert(string $table): self {}
    public function delete(string $table): self {}
    public function join(string $from, string $table, string $alias): self {}
    public function leftJoin(string $from, string $table, string $alias): self {}
    public function rightJoin(string $from, string $table, string $alias): self {}
    public function innerJoin(string $from, string $table, string $alias): self {}
}";
    php_index
        .index(&file("/project/vendor/doctrine-dbal.php", stubs))
        .unwrap();
    let source = "<?php
use Doctrine\\DBAL\\Connection;
use Doctrine\\DBAL\\Query\\QueryBuilder;
function write(Connection $connection, QueryBuilder $builder): void {
    $connection->insert('cms_users', ['' => 'value']);
    $builder->from('cms_');
    $builder->leftJoin('u', 'cms_', '');
}";
    let path = "/project/src/Database.php";
    php_index.index(&file(path, source)).unwrap();
    let root = php_root(source);

    let cases: [(&str, &[&str]); 5] = [
        ("'cms_users'", &["cms_users"]),
        ("[''", &["name", "user_email"]),
        ("from('cms_')", &["cms_users"]),
        ("leftJoin('u', 'cms_', '')", &["cms_users"]),
        (
            "leftJoin('u', 'cms_', '')",
            &["c", "cms_", "u_cms_", "uCms"],
        ),
    ];
    for (needle, labels) in cases {
        let position = source.find(needle).unwrap();
        let mut offset = position + needle.len() - 1;
        if needle.starts_with("from(") {
            offset = position + needle.find("cms_").unwrap() + 4;
        }
        if needle.starts_with("leftJoin(") {
            offset = if labels[0] == "cms_users" {
                position + needle.find("cms_").unwrap() + 4
            } else {
                position + needle.rfind("''").unwrap() + 1
            };
        }
        let node = root.node_at(at(offset)).unwrap();
        let context = php_index.document_context(path, 1, Some(&node), &root, source);
        let completions: Vec<String> = index
            .dbal_completions_at(Some(&context), &root, &node)
            .into_iter()
            .map(|completion| completion.label)
            .collect();
        for label in labels {
            assert!(
                completions.iter().any(|candidate| candidate == label),
                "{needle}: {label} not in {completions:?}"
            );
        }
    }

    let table = index.definition_for_table("cms_users").unwrap().unwrap();
    assert_eq!(table.class, "UserDefinition");
    let (model, field) = index
        .field_for_column("cms_users", "user_email")
        .unwrap()
        .unwrap();
    assert_eq!(model.class, "UserDefinition");
    assert_eq!(field.name, "email");
}

#[test]
fn type_declarations_prefer_literal_get_name_return() {
    let root = tempfile::tempdir().unwrap();
    let php_index = PhpIndex::open(root.path().join(".cache"), None).unwrap();
    let base_path = root.path().join("vendor/Doctrine/Type.php");
    let types_path = root.path().join("src/Types.php");
    php_index
        .index(&file(
            base_path.to_str().unwrap(),
            "<?php\nnamespace Doctrine\\DBAL\\Types;\nabstract class Type {}\n",
        ))
        .unwrap();
    php_index
        .index(&file(
            types_path.to_str().unwrap(),
            "<?php
namespace App\\Doctrine;
final class MoneyType extends \\Doctrine\\DBAL\\Types\\Type {
    public function getName(): string {
        return 'currency_amount';
    }
}
final class ConstantType extends \\Doctrine\\DBAL\\Types\\Type {
    private const NAME = 'constant_amount';
    public function getName(): string {
        return self::NAME;
    }
}
final class PostalAddressType extends \\Doctrine\\DBAL\\Types\\Type {}
",
        ))
        .unwrap();
    let declarations = type_declarations(&php_index);
    assert_eq!(declarations.len(), 3, "{declarations:#?}");
    let name_of = |class: &str| {
        declarations
            .iter()
            .find(|declaration| declaration.class == class)
            .map(|declaration| declaration.name.clone())
            .unwrap_or_default()
    };
    assert_eq!(name_of("App\\Doctrine\\MoneyType"), "currency_amount");
    assert_eq!(
        name_of("App\\Doctrine\\PostalAddressType"),
        "postal_address"
    );
    assert_eq!(name_of("App\\Doctrine\\ConstantType"), "constant_amount");
}

#[test]
fn type_registrations_in_yaml_and_xml() {
    let yaml_source = "when@test:
  doctrine:
    dbal:
      types:
        money: App\\Doctrine\\MoneyType
        address:
          class: 'App\\Doctrine\\AddressType'
";
    let yaml_root = swlsp_parser_yaml::parse(yaml_source).tree.root();
    let yaml_types =
        type_registrations_in_document("/project/config/packages/doctrine.yaml", &yaml_root);
    assert_eq!(yaml_types.len(), 2);
    assert_eq!(yaml_types[0].name, "money");
    assert_eq!(yaml_types[0].class, "App\\Doctrine\\MoneyType");
    assert_eq!(slice(yaml_source, yaml_types[0].name_range), "money");
    assert_eq!(yaml_types[1].name, "address");
    assert_eq!(yaml_types[1].class, "App\\Doctrine\\AddressType");

    let xml_source = r#"<container xmlns:doctrine="http://symfony.com/schema/dic/doctrine">
  <doctrine:config>
    <doctrine:dbal>
      <doctrine:type name="money" class="App\Doctrine\MoneyType"/>
      <doctrine:type name="address"> App\Doctrine\AddressType </doctrine:type>
    </doctrine:dbal>
  </doctrine:config>
</container>"#;
    let xml_root = swlsp_parser_xml::parse(xml_source).tree.root();
    let xml_types =
        type_registrations_in_document("/project/config/packages/doctrine.xml", &xml_root);
    assert_eq!(xml_types.len(), 2);
    assert_eq!(xml_types[0].name, "money");
    assert_eq!(xml_types[0].class, "App\\Doctrine\\MoneyType");
    assert_eq!(xml_types[1].name, "address");
    assert_eq!(xml_types[1].class, "App\\Doctrine\\AddressType");
    assert_eq!(
        slice(xml_source, xml_types[1].class_range),
        "App\\Doctrine\\AddressType"
    );

    let yaml_empty = "doctrine:\n  dbal:\n    types:\n      money:\n        class:\n";
    let yaml_empty_root = swlsp_parser_yaml::parse(yaml_empty).tree.root();
    let reference = type_registration_reference_at(
        "/project/config/packages/doctrine.yaml",
        &yaml_empty_root,
        at(yaml_empty.len() - 1),
    )
    .unwrap();
    assert_eq!(reference.role, TypeRegistrationReferenceRole::Class);
    assert_eq!(reference.name, "money");

    let xml_empty = r#"<container xmlns:doctrine="urn:doctrine"><doctrine:config><doctrine:dbal><doctrine:type name="money" class=""/></doctrine:dbal></doctrine:config></container>"#;
    let xml_empty_root = swlsp_parser_xml::parse(xml_empty).tree.root();
    let offset = xml_empty.find(r#"class="""#).unwrap() + r#"class=""#.len();
    let reference = type_registration_reference_at(
        "/project/config/packages/doctrine.xml",
        &xml_empty_root,
        at(offset),
    )
    .unwrap();
    assert_eq!(reference.role, TypeRegistrationReferenceRole::Class);
    assert_eq!(reference.name, "money");
}

#[test]
fn type_registrations_in_php_config() {
    let source = "<?php
namespace App\\Config;

use App\\Doctrine\\AddressType;
use App\\Doctrine\\MoneyType as ImportedMoneyType;
use App\\Doctrine\\UuidType;
use Symfony\\Component\\DependencyInjection\\Loader\\Configurator\\ContainerConfigurator;

return static function (ContainerConfigurator $containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => [
            'types' => [
                'money' => ImportedMoneyType::class,
                'address' => 'App\\Doctrine\\AddressType',
                'uuid' => ['class' => UuidType::class],
            ],
        ],
    ]);
};";
    let root = php_root(source);
    let path = "/project/config/packages/doctrine.php";
    let registrations = type_registrations_in_document(path, &root);
    assert_eq!(registrations.len(), 3);
    assert_eq!(registrations[0].name, "money");
    assert_eq!(registrations[0].class, "App\\Doctrine\\MoneyType");
    assert_eq!(
        slice(source, registrations[0].class_range),
        "ImportedMoneyType"
    );
    assert_eq!(registrations[1].name, "address");
    assert_eq!(registrations[1].class, "App\\Doctrine\\AddressType");
    assert_eq!(
        slice(source, registrations[1].class_range),
        "App\\Doctrine\\AddressType"
    );
    assert_eq!(registrations[2].name, "uuid");
    assert_eq!(registrations[2].class, "App\\Doctrine\\UuidType");

    for (needle, role, name, class, class_constant) in [
        (
            "ImportedMoneyType",
            TypeRegistrationReferenceRole::Class,
            "money",
            "App\\Doctrine\\MoneyType",
            true,
        ),
        (
            "App\\Doctrine\\AddressType",
            TypeRegistrationReferenceRole::Class,
            "address",
            "App\\Doctrine\\AddressType",
            false,
        ),
        (
            "'uuid'",
            TypeRegistrationReferenceRole::Name,
            "uuid",
            "App\\Doctrine\\UuidType",
            false,
        ),
    ] {
        let offset = source.rfind(needle).unwrap() + 1;
        let reference = type_registration_reference_at(path, &root, at(offset)).expect(needle);
        assert_eq!(reference.role, role, "{needle}");
        assert_eq!(reference.name, name, "{needle}");
        assert_eq!(reference.class, class, "{needle}");
        assert_eq!(reference.class_constant, class_constant, "{needle}");
    }
}

#[test]
fn php_type_registration_reference_at_incomplete_values() {
    for (name, value, class_constant) in [
        ("empty string", "''", false),
        ("missing value", "", true),
        ("expanded empty string", "['class' => '']", false),
        ("expanded missing value", "['class' => ]", true),
    ] {
        let source = format!(
            "<?php
return static function ($containerConfigurator): void {{
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => [
            'money' => {value}
        ]],
    ]);
}};"
        );
        let root = php_root(&source);
        let mut offset = source.find("'money' =>").unwrap() + "'money' => ".len() + value.len();
        if value == "''" {
            offset -= 1;
        }
        if value.ends_with("']") {
            offset = source.find("'class' => ''").unwrap() + "'class' => '".len();
        }
        if value == "['class' => ]" {
            offset = source.find("'class' =>").unwrap() + "'class' => ".len();
        }
        let reference = type_registration_reference_at(
            "/project/config/packages/doctrine.php",
            &root,
            at(offset),
        )
        .expect(name);
        assert_eq!(
            reference.role,
            TypeRegistrationReferenceRole::Class,
            "{name}"
        );
        assert_eq!(reference.name, "money", "{name}");
        assert_eq!(reference.class_constant, class_constant, "{name}");
    }
}

#[test]
fn runtime_php_type_registrations() {
    let source = "<?php
namespace App\\Bootstrap;

use App\\Doctrine\\EmojiType;
use App\\Doctrine\\MoneyType;
use Doctrine\\DBAL\\Types\\Type as DBALType;

DBALType::addType('money', MoneyType::class);
\\Doctrine\\DBAL\\Types\\Type::overrideType(
    'address',
    'App\\Doctrine\\AddressType',
);
DBALType::getTypeRegistry()->register(
    'emoji',
    new EmojiType([':)' => '😊']),
);";
    let root = php_root(source);
    let path = "/project/src/DoctrineBootstrap.php";
    let registrations = type_registrations_in_document(path, &root);
    assert_eq!(registrations.len(), 3);
    assert_eq!(registrations[0].name, "money");
    assert_eq!(registrations[0].class, "App\\Doctrine\\MoneyType");
    assert_eq!(registrations[1].name, "address");
    assert_eq!(registrations[1].class, "App\\Doctrine\\AddressType");
    assert_eq!(registrations[2].name, "emoji");
    assert_eq!(registrations[2].class, "App\\Doctrine\\EmojiType");
    assert_eq!(slice(source, registrations[2].class_range), "EmojiType");

    for (needle, name, class, class_constant, object_creation) in [
        (
            "MoneyType::class",
            "money",
            "App\\Doctrine\\MoneyType",
            true,
            false,
        ),
        (
            "App\\Doctrine\\AddressType",
            "address",
            "App\\Doctrine\\AddressType",
            false,
            false,
        ),
        (
            "EmojiType(",
            "emoji",
            "App\\Doctrine\\EmojiType",
            false,
            true,
        ),
    ] {
        let offset = source.rfind(needle).unwrap() + 1;
        let reference = type_registration_reference_at(path, &root, at(offset)).expect(needle);
        assert_eq!(
            reference.role,
            TypeRegistrationReferenceRole::Class,
            "{needle}"
        );
        assert_eq!(reference.name, name, "{needle}");
        assert_eq!(reference.class, class, "{needle}");
        assert_eq!(reference.class_constant, class_constant, "{needle}");
        assert_eq!(reference.object_creation, object_creation, "{needle}");
    }
}

#[test]
fn runtime_php_type_registration_incomplete_class_references() {
    for (name, call, object_creation, started) in [
        (
            "static registration",
            "DBALType::addType('money', )",
            false,
            false,
        ),
        (
            "registry registration",
            "DBALType::getTypeRegistry()->register('emoji', )",
            true,
            false,
        ),
        (
            "started registry object",
            "DBALType::getTypeRegistry()->register('emoji', new )",
            true,
            true,
        ),
    ] {
        let source = format!("<?php\nuse Doctrine\\DBAL\\Types\\Type as DBALType;\n{call};");
        let root = php_root(&source);
        let offset = source.rfind(')').unwrap();
        let reference = type_registration_reference_at("/project/bootstrap.php", &root, at(offset))
            .expect(name);
        assert_eq!(
            reference.role,
            TypeRegistrationReferenceRole::Class,
            "{name}"
        );
        assert_eq!(reference.object_creation, object_creation, "{name}");
        assert_eq!(reference.object_creation_started, started, "{name}");
    }
}

#[test]
fn index_merges_configured_dbal_type_aliases() {
    let root = tempfile::tempdir().unwrap();
    let cache = root.path().join(".cache");
    let doctrine_index = Index::new(&cache, None).unwrap();
    let php_index = PhpIndex::open(root.path().join(".php-cache"), None).unwrap();
    let base_path = root.path().join("vendor/Doctrine/Type.php");
    let class_path = root.path().join("src/MoneyType.php");
    let class_path = class_path.to_str().unwrap();
    php_index
        .index(&file(
            base_path.to_str().unwrap(),
            "<?php\nnamespace Doctrine\\DBAL\\Types;\nabstract class Type {}",
        ))
        .unwrap();
    php_index
        .index(&file(
            class_path,
            "<?php\nnamespace App\\Doctrine;\nfinal class MoneyType extends \\Doctrine\\DBAL\\Types\\Type {}",
        ))
        .unwrap();
    let config_path = root.path().join("config/packages/doctrine.php");
    let config_path = config_path.to_str().unwrap().to_owned();
    doctrine_index
        .index(&file(
            &config_path,
            "<?php
use App\\Doctrine\\MoneyType;

return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => [
            'types' => [
                'exact_money' => MoneyType::class,
            ],
        ],
    ]);
};",
        ))
        .unwrap();

    let exact: TypeDeclaration = doctrine_index
        .type_declarations(&php_index)
        .into_iter()
        .find(|declaration| declaration.name == "exact_money")
        .unwrap_or_default();
    assert_eq!(exact.class, "App\\Doctrine\\MoneyType");
    assert_eq!(exact.file, class_path);

    doctrine_index.close().unwrap();
    let reopened = Index::new(&cache, None).unwrap();
    assert!(reopened.type_declarations(&php_index).contains(&exact));
    reopened.removed_files(&[config_path]).unwrap();
    assert!(
        reopened
            .type_declarations(&php_index)
            .iter()
            .all(|declaration| declaration.name != "exact_money")
    );
    reopened.close().unwrap();
}
