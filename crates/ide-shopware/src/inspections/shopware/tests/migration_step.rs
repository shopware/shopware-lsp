use std::sync::Arc;

use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_lsp::Inspection;
use swlsp_php::PhpIndex;

use super::super::new_shopware_migration_step;
use crate::inspections::REWRITE_NODE_FIX_ID;
use crate::inspections::test_support::{check_diagnostics, check_fix};

fn inspection() -> (tempfile::TempDir, Arc<dyn Inspection>) {
    let cache = tempfile::tempdir().unwrap();
    let php = PhpIndex::open(cache.path().join("php-index"), None).unwrap();
    php.index(&ParsedFile::new(
        "/project/vendor/MigrationStep.php",
        b"<?php\nnamespace Shopware\\Core\\Framework\\Migration;\nabstract class MigrationStep {\n    protected function columnExists($connection, string $table, string $column): bool {}\n}\n".to_vec(),
    ))
    .unwrap();
    (cache, new_shopware_migration_step(Some(php)))
}

const URI: &str = "file:///project/src/Migration/Migration1700000000Example.php";

fn migration(body: &str) -> String {
    format!(
        "<?php\nnamespace Swag\\Migration;\n\nuse Doctrine\\DBAL\\Connection;\nuse Shopware\\Core\\Framework\\Migration\\MigrationStep;\n\nclass Migration1700000000Example extends MigrationStep\n{{\n{body}\n}}\n"
    )
}

#[test]
fn statements_that_fail_on_a_second_run_are_reported() {
    let (_cache, inspection) = inspection();
    let source = migration(
        r#"    public function update(Connection $connection): void
    {
        $connection->executeStatement('{|shopware.migration_step.create_unguarded:CREATE TABLE|} `swag_example` (`id` BINARY(16) NOT NULL)');
        $connection->executeStatement('CREATE TABLE IF NOT EXISTS `swag_other` (`id` BINARY(16) NOT NULL)');
        $connection->executeStatement('CREATE TEMPORARY TABLE `swag_tmp` (`id` INT)');
        // The migration creates the table itself: what it adds is new too.
        $connection->executeStatement('ALTER TABLE `swag_other` ADD COLUMN `name` VARCHAR(255) NULL');

        $connection->executeStatement(<<<SQL
            ALTER TABLE `product`
                {|shopware.migration_step.add_unguarded:ADD COLUMN `swag_a` INT NULL|},
                {|shopware.migration_step.add_unguarded:ADD INDEX `idx.swag_a` (`swag_a`)|},
                MODIFY COLUMN `name` TEXT NULL;
            SQL);
        $connection->executeStatement('{|shopware.migration_step.add_unguarded:CREATE INDEX `idx.b` ON `product` (`swag_a`)|}');
        $connection->executeStatement('ALTER TABLE `order` ADD COLUMN IF NOT EXISTS `swag_b` INT NULL');
        $connection->executeStatement('UPDATE product SET swag_a = 1');
        throw new \RuntimeException('CREATE TABLE failed');
    }

    public function updateDestructive(Connection $connection): void
    {
        $connection->executeStatement('ALTER TABLE `product` {|shopware.migration_step.add_unguarded:ADD `swag_c` INT NOT NULL|}');
    }"#,
    );
    let (_, problems) = check_diagnostics(inspection.as_ref(), URI, &source);
    assert_eq!(
        problems
            .iter()
            .map(|problem| problem.message.as_str())
            .collect::<Vec<_>>()[..2],
        [
            "CREATE TABLE `swag_example` fails when the migration runs again; write CREATE TABLE IF NOT EXISTS",
            "Adding the column `swag_a` to `product` fails when the migration runs again; check columnExists() first",
        ]
    );
}

#[test]
fn a_check_before_the_statement_makes_it_safe() {
    let (_cache, inspection) = inspection();
    for body in [
        // A check in the method.
        r"    public function update(Connection $connection): void
    {
        if ($this->columnExists($connection, 'product', 'swag_a')) {
            return;
        }
        $connection->executeStatement('ALTER TABLE `product` ADD COLUMN `swag_a` INT NULL');
    }",
        // A check before the helper is called.
        r"    public function update(Connection $connection): void
    {
        if (!EntityDefinitionQueryHelper::columnExists($connection, 'product', 'swag_a')) {
            $this->add($connection);
        }
    }

    private function add(Connection $connection): void
    {
        $connection->executeStatement('ALTER TABLE `product` ADD COLUMN `swag_a` INT NULL');
    }",
        // The schema is read, or a failure is caught.
        r"    public function update(Connection $connection): void
    {
        $columns = $connection->fetchFirstColumn('SHOW COLUMNS FROM `product`');
        if (!\in_array('swag_a', $columns, true)) {
            $connection->executeStatement('ALTER TABLE `product` ADD COLUMN `swag_a` INT NULL');
        }
        $manager = $connection->createSchemaManager();
        if (!$manager->tablesExist(['swag_example'])) {
            $connection->executeStatement('CREATE TABLE `swag_example` (`id` INT)');
        }
    }

    public function updateDestructive(Connection $connection): void
    {
        try {
            $connection->executeStatement('ALTER TABLE `product` ADD INDEX `idx.a` (`swag_a`)');
        } catch (\Throwable) {
        }
    }",
    ] {
        check_diagnostics(inspection.as_ref(), URI, &migration(body));
    }
    // A check after the statement is none.
    check_diagnostics(
        inspection.as_ref(),
        URI,
        &migration(
            r"    public function update(Connection $connection): void
    {
        $connection->executeStatement('ALTER TABLE `product` {|shopware.migration_step.add_unguarded:ADD COLUMN `swag_a` INT NULL|}');
        $this->columnExists($connection, 'product', 'swag_a');
    }",
        ),
    );
}

#[test]
fn changes_that_break_the_previous_version_belong_in_update_destructive() {
    let (_cache, inspection) = inspection();
    let source = migration(
        r"    public function update(Connection $connection): void
    {
        $connection->executeStatement('CREATE TABLE IF NOT EXISTS `swag_new` (`id` INT)');
        $connection->executeStatement('{|shopware.migration_step.destructive_update:DROP TABLE IF EXISTS `swag_old`|}');
        $connection->executeStatement('{|shopware.migration_step.destructive_update:RENAME TABLE `swag_a` TO `swag_b`|}');
        $connection->executeStatement('ALTER TABLE `product`
            {|shopware.migration_step.destructive_update:DROP COLUMN `swag_x`|},
            {|shopware.migration_step.destructive_update:CHANGE `swag_y` `swag_z` INT NULL|},
            {|shopware.migration_step.destructive_update:RENAME COLUMN `a` TO `b`|},
            {|shopware.migration_step.destructive_update:RENAME TO `product_old`|},
            CHANGE `swag_same` `swag_same` TEXT NULL,
            DROP INDEX `idx.old`,
            DROP FOREIGN KEY `fk.old`');
        if (!$this->columnExists($connection, 'product', 'swag_required')) {
            $connection->executeStatement('ALTER TABLE `product` {|shopware.migration_step.destructive_update:ADD COLUMN `swag_required` INT NOT NULL|}, ADD `swag_default` INT NOT NULL DEFAULT 0');
        }
        // Its own table is in use by nothing yet.
        $connection->executeStatement('ALTER TABLE `swag_new` DROP COLUMN `id`');
        $connection->executeStatement('DROP TABLE `swag_new`');
        $this->cleanup($connection);
        $this->shared($connection);
    }

    public function updateDestructive(Connection $connection): void
    {
        $connection->executeStatement('DROP TABLE IF EXISTS `swag_old`');
        $connection->executeStatement('ALTER TABLE `product` DROP COLUMN `swag_x`');
        $this->later($connection);
        $this->shared($connection);
    }

    private function cleanup(Connection $connection): void
    {
        $connection->executeStatement('ALTER TABLE `product` {|shopware.migration_step.destructive_update:DROP COLUMN `swag_helper`|}');
    }

    private function later(Connection $connection): void
    {
        $connection->executeStatement('ALTER TABLE `product` DROP COLUMN `swag_later`');
    }

    private function shared(Connection $connection): void
    {
        $connection->executeStatement('{|shopware.migration_step.destructive_update:DROP TABLE `swag_shared`|}');
    }",
    );
    let (_, problems) = check_diagnostics(inspection.as_ref(), URI, &source);
    assert_eq!(
        problems[0].message,
        "Dropping the table `swag_old` in update() breaks the previous version of the code, which still runs during a deployment; do it in updateDestructive()"
    );
}

#[test]
fn only_migration_steps_are_looked_at_and_inserts_are_opt_in() {
    let (_cache, inspection) = inspection();
    // Another class running the same statements.
    check_diagnostics(
        inspection.as_ref(),
        "file:///project/src/Installer.php",
        "<?php\nnamespace Swag;\n\nclass Installer\n{\n    public function update($connection): void\n    {\n        $connection->executeStatement('CREATE TABLE `swag_example` (`id` INT)');\n        $connection->executeStatement('DROP TABLE `swag_old`');\n    }\n}\n",
    );
    // The insert rule is declared, and off unless configured.
    let definition = inspection.definition();
    let insert = definition
        .problems
        .iter()
        .find(|problem| problem.id.as_str() == "shopware.migration_step.insert_unguarded")
        .unwrap();
    assert!(insert.disabled_by_default);
    assert!(
        definition
            .problems
            .iter()
            .filter(|problem| problem.id != insert.id)
            .all(|problem| !problem.disabled_by_default)
    );
}

#[test]
fn the_create_fix_adds_if_not_exists() {
    let (_cache, inspection) = inspection();
    for (before, after) in [
        (
            "$connection->executeStatement('{|shopware.migration_step.create_unguarded:CREATE TABLE|} `swag_example` (`id` INT)');",
            "$connection->executeStatement('CREATE TABLE IF NOT EXISTS `swag_example` (`id` INT)');",
        ),
        (
            "$connection->executeStatement(<<<SQL\n            {|shopware.migration_step.create_unguarded:create   table|} swag_example (\n                id INT\n            );\n            SQL);",
            "$connection->executeStatement(<<<SQL\n            create   table IF NOT EXISTS swag_example (\n                id INT\n            );\n            SQL);",
        ),
    ] {
        let wrap = |statement: &str| {
            migration(&format!(
                "    public function update(Connection $connection): void\n    {{\n        {statement}\n    }}"
            ))
        };
        let presentation = check_fix(
            inspection.as_ref(),
            URI,
            &wrap(before),
            REWRITE_NODE_FIX_ID,
            &wrap(after),
        );
        assert!(presentation.preferred);
        assert_eq!(presentation.title, "Add IF NOT EXISTS");
    }
}

#[test]
fn inserts_that_add_a_row_again_are_found() {
    let (_cache, inspection) = inspection();
    check_diagnostics(
        inspection.as_ref(),
        URI,
        &migration(
            r"    public function update(Connection $connection): void
    {
        $connection->executeStatement('{|shopware.migration_step.insert_unguarded:INSERT INTO `swag_setting` (`id`, `name`) VALUES (1, :name)|}', ['name' => 'a']);
        $connection->{|shopware.migration_step.insert_unguarded:insert|}('swag_setting', ['id' => 2]);
        $connection->executeStatement('INSERT IGNORE INTO `swag_setting` (`id`) VALUES (3)');
        $connection->executeStatement('INSERT INTO `swag_setting` (`id`) VALUES (4) ON DUPLICATE KEY UPDATE `id` = `id`');
        $connection->executeStatement('REPLACE INTO `swag_setting` (`id`) VALUES (5)');
    }

    public function updateDestructive(Connection $connection): void
    {
        // The row is looked for first.
        $id = $connection->fetchOne('SELECT `id` FROM `swag_setting` WHERE `name` = :name', ['name' => 'b']);
        if ($id === false) {
            $connection->insert('swag_setting', ['id' => 6]);
            $connection->executeStatement('INSERT INTO `swag_setting` (`id`) VALUES (7)');
        }
    }",
        ),
    );
}
