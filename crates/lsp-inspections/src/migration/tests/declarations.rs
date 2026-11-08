use super::super::declarations::DECLARATION_MIGRATION_FIX_ID;
use super::*;

#[test]
fn interface_to_abstract_class_migration_quick_fixes() {
    let index = migration_inspection_php_index();
    for (name, source, expected) in [
        (
            "class inheritance",
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartPersisterInterface;\nclass Persister implements CartPersisterInterface {}\n",
            "class Persister extends AbstractCartPersister",
        ),
        (
            "parameter type",
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartPersisterInterface;\nclass Service\n{\n    public function __construct(CartPersisterInterface $persister) {}\n}\n",
            "__construct(AbstractCartPersister $persister)",
        ),
        (
            "property type",
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartPersisterInterface;\nclass Service\n{\n    private CartPersisterInterface $persister;\n}\n",
            "private AbstractCartPersister $persister;",
        ),
    ] {
        let document = document("file:///project/src/InterfaceMigration.php", source);
        let updated = apply_only_migration_fix(
            &migration(&index, 5),
            &document,
            DECLARATION_MIGRATION_FIX_ID,
        );
        assert!(updated.contains(expected), "{name}: {updated}");
        assert!(
            updated.contains("use Shopware\\Core\\Checkout\\Cart\\AbstractCartPersister;"),
            "{name}"
        );
    }
}

#[test]
fn shopware_method_declaration_migration_quick_fixes() {
    let index = migration_inspection_php_index();
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
    for (name, minor, source, expected) in [
        (
            "required parameter",
            5,
            "<?php\nuse Shopware\\Storefront\\Framework\\Captcha\\AbstractCaptcha;\nclass Captcha extends AbstractCaptcha\n{\n    public function supports(string $type): bool { return true; }\n}\n",
            "supports(string $type, array $captchaConfig)",
        ),
        (
            "parameter type",
            5,
            "<?php\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\Indexing\\EntityIndexer;\nclass Indexer extends EntityIndexer\n{\n    public function iterate($offset): void {}\n}\n",
            "iterate(?array $offset)",
        ),
        (
            "return type",
            5,
            "<?php\nuse Shopware\\Core\\Framework\\Adapter\\Twig\\TemplateIterator;\nclass Iterator extends TemplateIterator\n{\n    public function getIterator(): array { return []; }\n}\n",
            "getIterator(): Traversable",
        ),
        (
            "Shopware 6.7 return type",
            7,
            "<?php\nuse Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition;\nclass Definition extends AbstractElasticsearchDefinition\n{\n    public function buildTermQuery(): BoolQuery {}\n}\n",
            "buildTermQuery(): BuilderInterface",
        ),
    ] {
        let document = document("file:///project/src/DeclarationMigration.php", source);
        let updated = apply_only_migration_fix(
            &migration(&index, minor),
            &document,
            DECLARATION_MIGRATION_FIX_ID,
        );
        assert!(updated.contains(expected), "{name}: {updated}");
    }
}

#[test]
fn interface_to_abstract_class_migration_quick_fixes_free_function_parameter() {
    let index = migration_inspection_php_index();
    let document = document(
        "file:///project/src/Function.php",
        "<?php\nnamespace App;\n\nuse Shopware\\Core\\Checkout\\Cart\\CartPersisterInterface;\n\nfunction persist(CartPersisterInterface $persister): void {}\n",
    );
    let updated = apply_only_migration_fix(
        &migration(&index, 5),
        &document,
        DECLARATION_MIGRATION_FIX_ID,
    );
    assert!(updated.contains("use Shopware\\Core\\Checkout\\Cart\\AbstractCartPersister;"));
    assert!(updated.contains("function persist(AbstractCartPersister $persister): void"));
}
