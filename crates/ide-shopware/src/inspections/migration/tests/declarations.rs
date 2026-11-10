use super::super::declarations::DECLARATION_MIGRATION_FIX_ID;
use super::*;

#[test]
fn interface_to_abstract_class_migration_quick_fixes() {
    let index = migration_inspection_php_index();
    for (before, after) in [
        (
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartPersisterInterface;\nclass {|shopware.migration.declaration.interface_to_abstract:Persister|} implements CartPersisterInterface {}\n",
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\AbstractCartPersister;\nclass Persister extends AbstractCartPersister {}\n",
        ),
        (
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartPersisterInterface;\nclass Service\n{\n    public function __construct({|shopware.migration.declaration.interface_to_abstract:CartPersisterInterface $persister|}) {}\n}\n",
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\AbstractCartPersister;\nclass Service\n{\n    public function __construct(AbstractCartPersister $persister) {}\n}\n",
        ),
        (
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\CartPersisterInterface;\nclass Service\n{\n    {|shopware.migration.declaration.interface_to_abstract:private CartPersisterInterface $persister;|}\n}\n",
            "<?php\nuse Shopware\\Core\\Checkout\\Cart\\AbstractCartPersister;\nclass Service\n{\n    private AbstractCartPersister $persister;\n}\n",
        ),
    ] {
        check_only_migration_fix(
            &migration(&index, 5),
            "file:///project/src/InterfaceMigration.php",
            before,
            DECLARATION_MIGRATION_FIX_ID,
            after,
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
    for (minor, before, after) in [
        (
            5,
            "<?php\nuse Shopware\\Storefront\\Framework\\Captcha\\AbstractCaptcha;\nclass Captcha extends AbstractCaptcha\n{\n    {|shopware.migration.declaration.parameter.add:public function supports(string $type): bool { return true; }|}\n}\n",
            "<?php\nuse Shopware\\Storefront\\Framework\\Captcha\\AbstractCaptcha;\nclass Captcha extends AbstractCaptcha\n{\n    public function supports(string $type, array $captchaConfig): bool { return true; }\n}\n",
        ),
        (
            5,
            "<?php\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\Indexing\\EntityIndexer;\nclass Indexer extends EntityIndexer\n{\n    public function iterate({|shopware.migration.declaration.type:$offset|}): void {}\n}\n",
            "<?php\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\Indexing\\EntityIndexer;\nclass Indexer extends EntityIndexer\n{\n    public function iterate(?array $offset): void {}\n}\n",
        ),
        (
            5,
            "<?php\nuse Shopware\\Core\\Framework\\Adapter\\Twig\\TemplateIterator;\nclass Iterator extends TemplateIterator\n{\n    {|shopware.migration.declaration.type:public function getIterator(): array { return []; }|}\n}\n",
            "<?php\nuse Shopware\\Core\\Framework\\Adapter\\Twig\\TemplateIterator;\nclass Iterator extends TemplateIterator\n{\n    public function getIterator(): Traversable { return []; }\n}\n",
        ),
        (
            7,
            "<?php\nuse Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition;\nclass Definition extends AbstractElasticsearchDefinition\n{\n    {|shopware.migration.declaration.type:public function buildTermQuery(): BoolQuery {}|}\n}\n",
            "<?php\nuse Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition;\nuse OpenSearchDSL\\BuilderInterface;\nclass Definition extends AbstractElasticsearchDefinition\n{\n    public function buildTermQuery(): BuilderInterface {}\n}\n",
        ),
    ] {
        check_only_migration_fix(
            &migration(&index, minor),
            "file:///project/src/DeclarationMigration.php",
            before,
            DECLARATION_MIGRATION_FIX_ID,
            after,
        );
    }
}

#[test]
fn interface_to_abstract_class_migration_quick_fixes_free_function_parameter() {
    let index = migration_inspection_php_index();
    check_only_migration_fix(
        &migration(&index, 5),
        "file:///project/src/Function.php",
        "<?php\nnamespace App;\n\nuse Shopware\\Core\\Checkout\\Cart\\CartPersisterInterface;\n\nfunction persist({|shopware.migration.declaration.interface_to_abstract:CartPersisterInterface $persister|}): void {}\n",
        DECLARATION_MIGRATION_FIX_ID,
        "<?php\nnamespace App;\n\nuse Shopware\\Core\\Checkout\\Cart\\AbstractCartPersister;\n\nfunction persist(AbstractCartPersister $persister): void {}\n",
    );
}
