use super::super::api::Since;
use super::super::declarations::{
    ADDED_METHOD_PARAMETER_MIGRATIONS, INTERFACE_ABSTRACT_CLASS_MIGRATIONS,
    PARAMETER_TYPE_MIGRATIONS, RETURN_TYPE_MIGRATIONS,
};
use swlsp_lsp::Context;

use super::*;
use crate::ProblemAnalyzer;

#[test]
fn shopware_declaration_migration_tables_cover_rector_configuration() {
    assert_eq!(INTERFACE_ABSTRACT_CLASS_MIGRATIONS.len(), 4);
    assert_eq!(ADDED_METHOD_PARAMETER_MIGRATIONS.len(), 2);
    assert_eq!(PARAMETER_TYPE_MIGRATIONS.len(), 10);
    assert_eq!(RETURN_TYPE_MIGRATIONS.len(), 5);
}

// Table coverage: each generated source must produce its rule's code. The
// sources also trigger incidental migrations that vary per row (a renamed
// owner type, for example), so these rows assert membership instead of exact
// markup.
fn assert_declaration_migration_diagnostic(
    index: &TestIndex,
    since: Since,
    source: &str,
    expected: &str,
) {
    let document = Arc::new(TextDocument::new(
        "file:///project/src/DeclarationMapping.php",
        source,
        1,
    ));
    assert!(document.parse_errors().is_empty(), "{source}");
    let problems = ShopwareMigrationAnalyzer::new(
        Some(index.index.clone()),
        version(6, since.minor, since.patch),
    )
    .analyze(&Context::background(), &document)
    .expect("analyze");
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
    let fixture = r#"<?php
use Shopware\Core\Checkout\Cart\CartPersisterInterface;
use Shopware\Core\Framework\Adapter\Twig\TemplateIterator;
use Shopware\Core\Framework\DataAbstractionLayer\Indexing\EntityIndexer;
use Shopware\Elasticsearch\Framework\AbstractElasticsearchDefinition;
use Shopware\Storefront\Framework\Captcha\AbstractCaptcha;

class {|shopware.migration.declaration.interface_to_abstract:Persister|} implements CartPersisterInterface
{
    {|shopware.migration.declaration.interface_to_abstract:private CartPersisterInterface $inner;|}
    public function __construct({|shopware.migration.declaration.interface_to_abstract:CartPersisterInterface $inner|}) {}
}
class Captcha extends AbstractCaptcha
{
    {|shopware.migration.declaration.parameter.add:public function supports(string $type): bool { return true; }|}
}
class Indexer extends EntityIndexer
{
    public function iterate({|shopware.migration.declaration.type:$offset|}): void {}
}
class Iterator extends TemplateIterator
{
    {|shopware.migration.declaration.type:public function getIterator(): array { return []; }|}
}
class ElasticsearchDefinition extends AbstractElasticsearchDefinition
{
    {|shopware.migration.declaration.type:public function buildTermQuery(): BoolQuery {}|}
}
"#;
    let uri = "file:///project/src/Declarations.php";
    let (_, problems) = check(&index, version(6, 7, 0), uri, fixture);
    let [class, property, parameter, add, offset, iterator, query] = problems.as_slice() else {
        unreachable!()
    };
    assert_eq!(payload(class).kind, "interface-class");
    assert_eq!(payload(parameter).kind, "interface-parameter");
    assert_eq!(payload(property).kind, "interface-property");
    assert_eq!(payload(add).argument_index, 1);
    assert_eq!(payload(offset).replacement, "?array");
    assert_eq!(payload(iterator).replacement, "\\Traversable");
    assert_eq!(
        payload(query).replacement,
        "\\OpenSearchDSL\\BuilderInterface"
    );

    // The BoolQuery return type migration starts after 6.6.10.
    let query_marked =
        "{|shopware.migration.declaration.type:public function buildTermQuery(): BoolQuery {}|}";
    check(
        &index,
        version(6, 6, 10),
        uri,
        &fixture.replace(
            query_marked,
            "public function buildTermQuery(): BoolQuery {}",
        ),
    );
}

#[test]
fn interface_to_abstract_class_migration_covers_free_function_parameters() {
    let index = migration_test_php_index();
    let (_, problems) = check(
        &index,
        version(6, 5, 0),
        "file:///project/src/Function.php",
        r#"<?php
use Shopware\Core\Checkout\Cart\CartPersisterInterface;

function persist({|shopware.migration.declaration.interface_to_abstract:CartPersisterInterface $persister|}): void {}
"#,
    );
    assert_eq!(payload(&problems[0]).kind, "interface-parameter");
}

#[test]
fn interface_to_abstract_migration_avoids_replacing_existing_parent() {
    let index = migration_test_php_index();
    let (_, problems) = check(
        &index,
        version(6, 5, 0),
        "file:///project/src/UnsafeParent.php",
        r#"<?php
use Shopware\Core\Checkout\Cart\CartPersisterInterface;
class {|shopware.migration.declaration.interface_to_abstract:Persister|} extends ExistingParent implements CartPersisterInterface {}
"#,
    );
    assert!(!payload(&problems[0]).safe);
}
