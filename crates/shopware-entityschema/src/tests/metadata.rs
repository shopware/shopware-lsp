//! Port of Go `metadata_test.go`.

use super::inheritance::{field_by_property, php_ok, s};
use crate::*;

#[test]
fn common_metadata_flags_round_trip_as_typed_schema() {
    let source = r"<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\AllowEmptyString;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\AllowHtml;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiCriteriaAware;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\AsArray;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Choice;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Deprecated;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Immutable;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\IgnoreInUnusedMediaSearch;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\RuleAreas;
use Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Since;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\Field\StringField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineFields(): FieldCollection { return new FieldCollection([
        new IdField('id', 'id'),
        (new StringField('type', 'type'))->addFlags(
            new AllowHtml(false),
            new AllowEmptyString(),
            new AsArray(),
            new Immutable(),
            new Since('6.7.0.0'),
            new Deprecated('v6.7.0.0', 'v6.8.0.0', 'replacement'),
            new ApiCriteriaAware(),
            new IgnoreInUnusedMediaSearch(),
            new RuleAreas(RuleAreas::PRODUCT_AREA, 'custom'),
            new Choice([self::TYPE_A, 'literal', 3, true], strict: true),
        ),
    ]); }
}";
    let spec = import_definition(source, None).unwrap();
    let field = field_by_property(&spec.fields, "type");
    let metadata = field.metadata.clone().unwrap();
    assert_eq!(Some(false), metadata.allow_html);
    assert!(metadata.allow_empty_string);
    assert!(metadata.as_array);
    assert!(metadata.immutable);
    assert_eq!("6.7.0.0", metadata.since);
    assert_eq!(
        Some(Deprecation {
            deprecated_since: "v6.7.0.0".into(),
            will_be_removed_in: "v6.8.0.0".into(),
            replaced_by: "replacement".into(),
        }),
        metadata.deprecated
    );
    assert!(metadata.api_criteria_aware);
    assert!(metadata.ignore_in_unused_media_search);
    assert_eq!(s(&["product", "custom"]), metadata.rule_areas);
    let choice = metadata.choice.clone().unwrap();
    assert_eq!(
        s(&["self::TYPE_A", "'literal'", "3", "true"]),
        choice.values
    );
    assert_eq!(Some(true), choice.strict);
    assert!(field.preserved_flags.is_empty());

    let rendered = render_definition(&spec).unwrap();
    assert!(php_ok(&rendered), "{rendered}");
    assert!(rendered.contains("new AllowHtml(false)"));
    assert!(rendered.contains("new AsArray()"));
    assert!(rendered.contains("new IgnoreInUnusedMediaSearch()"));
    assert!(rendered.contains("new Deprecated('v6.7.0.0', 'v6.8.0.0', 'replacement')"));
    assert!(rendered.contains("new RuleAreas('product', 'custom')"));
    assert!(rendered.contains("new Choice([self::TYPE_A, 'literal', 3, true], strict: true)"));

    let round_tripped = import_definition(&rendered, None).unwrap();
    assert_eq!(
        field.metadata,
        field_by_property(&round_tripped.fields, "type").metadata
    );
}
