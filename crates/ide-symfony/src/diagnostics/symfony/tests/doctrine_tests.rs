use std::sync::Arc;

use super::{index_file, open_php, suggestions, tempdir};
use crate::diagnostics::symfony::DoctrineAnalyzer;
use crate::diagnostics::test_support::check_problems;

#[test]
fn doctrine_diagnostics_validate_shopware_dal_tables_and_columns() {
    let doctrine_dir = tempdir();
    let doctrine = Arc::new(swlsp_doctrine::Index::new(doctrine_dir.path(), None).unwrap());
    let (_dir, php) = open_php();
    let dal_dir = tempdir();
    let dal = Arc::new(swlsp_shopware_dal::Index::new(dal_dir.path(), None).unwrap());
    index_file(
        dal.as_ref(),
        "/project/src/ScheduledTaskDefinition.php",
        r"<?php
class ScheduledTaskDefinition extends EntityDefinition
{
    public const ENTITY_NAME = 'scheduled_task';
    protected function defineFields(): FieldCollection
    {
        return new FieldCollection([
            new IdField('id', 'id'),
            new StringField('scheduled_task_class', 'scheduledTaskClass'),
        ]);
    }
}",
    );
    index_file(
        &php,
        "/project/vendor/doctrine-dbal.php",
        r"<?php
namespace Doctrine\DBAL;
class Connection { public function insert(string $table, array $data): void {} }
",
    );
    let (_, result) = check_problems(
        &DoctrineAnalyzer::new(Some(doctrine), Some(php.clone()), Some(dal)),
        "file:///project/src/Database.php",
        r"<?php
use Doctrine\DBAL\Connection;
function write(Connection $connection): void {
    $connection->insert('scheduled_task', [
        'scheduled_task_class' => 'Task',
        '{|symfony.doctrine.column.missing:scheduled_task_clas|}' => 'Task',
    ]);
    $connection->insert('migration_audit_log', ['payload' => 'value']);
}",
    );
    assert!(result[0].message.contains("scheduled_task_clas"));
    assert!(suggestions(&result[0]).contains(&"scheduled_task_class".to_owned()));
}

#[test]
fn doctrine_diagnostics_validate_type_registration_classes() {
    let doctrine_dir = tempdir();
    let doctrine = Arc::new(swlsp_doctrine::Index::new(doctrine_dir.path(), None).unwrap());
    let (_dir, php) = open_php();
    index_file(
        &php,
        "/project/vendor/Type.php",
        r"<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}",
    );
    index_file(
        &php,
        "/project/src/Types.php",
        r"<?php
namespace App\Doctrine;
class MoneyType extends \Doctrine\DBAL\Types\Type {}
class NotAType {}",
    );
    let (_, result) = check_problems(
        &DoctrineAnalyzer::new(Some(doctrine), Some(php.clone()), None),
        "file:///project/config/packages/doctrine.php",
        r"<?php
use Doctrine\DBAL\Types\Type;

Type::addType('valid', \App\Doctrine\MoneyType::class);
Type::addType('missing', {|symfony.doctrine.type_class.missing:\App\Doctrine\MonyType|}::class);
Type::overrideType('invalid', {|symfony.doctrine.type_class.invalid:\App\Doctrine\NotAType|}::class);",
    );
    assert!(suggestions(&result[0]).contains(&"App\\Doctrine\\MoneyType".to_owned()));
}
