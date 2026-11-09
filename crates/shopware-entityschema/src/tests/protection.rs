//! Tests for protection.

use super::inheritance::{php_ok, s};
use crate::*;

#[test]
fn entity_protections_import_render_and_rewrite() {
    let source = r"<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\Context;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityProtection\EntityProtectionCollection;
use Shopware\Core\Framework\DataAbstractionLayer\EntityProtection\ReadProtection;
use Shopware\Core\Framework\DataAbstractionLayer\EntityProtection\WriteProtection;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineProtections(): EntityProtectionCollection {
        return new EntityProtectionCollection([
            new ReadProtection(Context::SYSTEM_SCOPE),
            new WriteProtection('system', Context::CRUD_API_SCOPE),
        ]);
    }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}";
    let mut spec = import_definition(source, None).unwrap();
    assert!(spec.read_protected);
    assert_eq!(s(&["system"]), spec.read_protection_scopes);
    assert!(spec.write_protected);
    assert_eq!(s(&["system", "crud"]), spec.write_protection_scopes);
    assert!(spec.protection_method_raw.is_empty());

    let rendered = render_definition(&spec).unwrap();
    assert!(php_ok(&rendered), "{rendered}");
    assert!(rendered.contains("new ReadProtection('system')"));
    assert!(rendered.contains("new WriteProtection('system', 'crud')"));

    spec.read_protected = false;
    spec.read_protection_scopes.clear();
    let rewritten = rewrite_definition(source, &spec).unwrap();
    assert!(!rewritten.contains("new ReadProtection"));
    assert!(rewritten.contains("new WriteProtection('system', 'crud')"));
    assert_eq!(1, rewritten.matches("function defineProtections").count());
}

#[test]
fn custom_entity_protection_method_is_preserved_and_cannot_be_overwritten() {
    let source = r"<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityProtection\EntityProtectionCollection;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineProtections(): EntityProtectionCollection { return $this->protectionFactory(); }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}";
    let mut spec = import_definition(source, None).unwrap();
    assert!(
        spec.protection_method_raw
            .contains("$this->protectionFactory()")
    );

    let rewritten = rewrite_definition(source, &spec).unwrap();
    assert!(rewritten.contains("$this->protectionFactory()"));

    spec.protection_method_raw.clear();
    spec.write_protected = true;
    let error = rewrite_definition(source, &spec).unwrap_err();
    assert!(
        error
            .to_string()
            .contains("cannot overwrite customized defineProtections method"),
        "{error}"
    );
}
