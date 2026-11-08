use super::super::api::Since;
use super::super::declarations::{
    ADDED_METHOD_PARAMETER_MIGRATIONS, INTERFACE_ABSTRACT_CLASS_MIGRATIONS,
    PARAMETER_TYPE_MIGRATIONS, RETURN_TYPE_MIGRATIONS,
};
use super::*;

#[test]
fn shopware_declaration_migration_tables_cover_rector_configuration() {
    assert_eq!(INTERFACE_ABSTRACT_CLASS_MIGRATIONS.len(), 4);
    assert_eq!(ADDED_METHOD_PARAMETER_MIGRATIONS.len(), 2);
    assert_eq!(PARAMETER_TYPE_MIGRATIONS.len(), 10);
    assert_eq!(RETURN_TYPE_MIGRATIONS.len(), 5);
}

fn assert_declaration_migration_diagnostic(
    index: &TestIndex,
    since: Since,
    source: &str,
    expected: &str,
) {
    let document = document("file:///project/src/DeclarationMapping.php", source);
    assert!(document.parse_errors().is_empty(), "{source}");
    let problems = analyze(index, version(6, since.minor, since.patch), &document);
    assert!(
        problems.iter().any(|problem| problem.id.0 == expected),
        "{source} did not produce {expected}"
    );
}

#[test]
fn every_shopware_declaration_migration_mapping_produces_diagnostic() {
    let index = migration_test_php_index();
    for rule in INTERFACE_ABSTRACT_CLASS_MIGRATIONS {
        assert_declaration_migration_diagnostic(
            &index,
            rule.since,
            &format!(
                "<?php function migrate(\\{} $value): void {{}}",
                rule.interface_name
            ),
            "shopware.migration.declaration.interface_to_abstract",
        );
    }
    for rule in ADDED_METHOD_PARAMETER_MIGRATIONS {
        assert_declaration_migration_diagnostic(
            &index,
            rule.since,
            &format!(
                "<?php class Subject extends \\{} {{ public function {}(): void {{}} }}",
                rule.class, rule.method
            ),
            "shopware.migration.declaration.parameter.add",
        );
    }
    for rule in PARAMETER_TYPE_MIGRATIONS {
        assert_declaration_migration_diagnostic(
            &index,
            rule.since,
            &format!(
                "<?php class Subject extends \\{} {{ public function {}($value): void {{}} }}",
                rule.class, rule.method
            ),
            "shopware.migration.declaration.type",
        );
    }
    for rule in RETURN_TYPE_MIGRATIONS {
        assert_declaration_migration_diagnostic(
            &index,
            rule.since,
            &format!(
                "<?php class Subject extends \\{} {{ public function {}() {{}} }}",
                rule.class, rule.method
            ),
            "shopware.migration.declaration.type",
        );
    }
}

#[test]
fn shopware_declaration_migrations_are_versioned() {
    let index = migration_test_php_index();
    for (path, source) in [
        (
            "/project/vendor/AbstractCaptcha.php",
            "<?php\nnamespace Shopware\\Storefront\\Framework\\Captcha;\nabstract class AbstractCaptcha {}",
        ),
        (
            "/project/vendor/EntityIndexer.php",
            "<?php\nnamespace Shopware\\Core\\Framework\\DataAbstractionLayer\\Indexing;\nabstract class EntityIndexer {}",
        ),
        (
            "/project/vendor/TemplateIterator.php",
            "<?php\nnamespace Shopware\\Core\\Framework\\Adapter\\Twig;\nabstract class TemplateIterator {}",
        ),
        (
            "/project/vendor/AbstractElasticsearchDefinition.php",
            "<?php\nnamespace Shopware\\Elasticsearch\\Framework;\nabstract class AbstractElasticsearchDefinition {}",
        ),
    ] {
        index.add(path, source);
    }
    let document = document(
        "file:///project/src/Declarations.php",
        r#"<?php
use Shopware\Core\Checkout\Cart\CartPersisterInterface;
use Shopware\Core\Framework\Adapter\Twig\TemplateIterator;
use Shopware\Core\Framework\DataAbstractionLayer\Indexing\EntityIndexer;
use Shopware\Elasticsearch\Framework\AbstractElasticsearchDefinition;
use Shopware\Storefront\Framework\Captcha\AbstractCaptcha;

class Persister implements CartPersisterInterface
{
    private CartPersisterInterface $inner;
    public function __construct(CartPersisterInterface $inner) {}
}
class Captcha extends AbstractCaptcha
{
    public function supports(string $type): bool { return true; }
}
class Indexer extends EntityIndexer
{
    public function iterate($offset): void {}
}
class Iterator extends TemplateIterator
{
    public function getIterator(): array { return []; }
}
class ElasticsearchDefinition extends AbstractElasticsearchDefinition
{
    public function buildTermQuery(): BoolQuery {}
}
"#,
    );
    let problems = analyze(&index, version(6, 7, 0), &document);
    assert_eq!(problems.len(), 7);
    let code = "shopware.migration.declaration.interface_to_abstract";
    assert_eq!(problems[0].id.0, code);
    assert_eq!(payload(&problems[0]).kind, "interface-class");
    assert_eq!(problems[1].id.0, code);
    assert_eq!(payload(&problems[1]).kind, "interface-parameter");
    assert_eq!(problems[2].id.0, code);
    assert_eq!(payload(&problems[2]).kind, "interface-property");
    assert_eq!(
        problems[3].id.0,
        "shopware.migration.declaration.parameter.add"
    );
    assert_eq!(payload(&problems[3]).argument_index, 1);
    assert_eq!(problems[4].id.0, "shopware.migration.declaration.type");
    assert_eq!(payload(&problems[4]).replacement, "?array");
    assert_eq!(problems[5].id.0, "shopware.migration.declaration.type");
    assert_eq!(payload(&problems[5]).replacement, "\\Traversable");
    assert_eq!(problems[6].id.0, "shopware.migration.declaration.type");
    assert_eq!(
        payload(&problems[6]).replacement,
        "\\OpenSearchDSL\\BuilderInterface"
    );

    assert_eq!(analyze(&index, version(6, 6, 10), &document).len(), 6);
}

#[test]
fn interface_to_abstract_class_migration_covers_free_function_parameters() {
    let index = migration_test_php_index();
    let document = document(
        "file:///project/src/Function.php",
        r#"<?php
use Shopware\Core\Checkout\Cart\CartPersisterInterface;

function persist(CartPersisterInterface $persister): void {}
"#,
    );
    let problems = analyze(&index, version(6, 5, 0), &document);
    assert_eq!(problems.len(), 1);
    assert_eq!(
        problems[0].id.0,
        "shopware.migration.declaration.interface_to_abstract"
    );
    assert_eq!(payload(&problems[0]).kind, "interface-parameter");
}

#[test]
fn interface_to_abstract_migration_avoids_replacing_existing_parent() {
    let index = migration_test_php_index();
    let document = document(
        "file:///project/src/UnsafeParent.php",
        r#"<?php
use Shopware\Core\Checkout\Cart\CartPersisterInterface;
class Persister extends ExistingParent implements CartPersisterInterface {}
"#,
    );
    let problems = analyze(&index, version(6, 5, 0), &document);
    assert_eq!(problems.len(), 1);
    assert!(!payload(&problems[0]).safe);
}
