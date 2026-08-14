package entityschema

import (
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestCommonMetadataFlagsRoundTripAsTypedSchema(t *testing.T) {
	source := `<?php declare(strict_types=1);
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
}`

	spec, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	field := fieldByProperty(t, spec.Fields, "type")
	require.NotNil(t, field.Metadata)
	require.False(t, *field.Metadata.AllowHTML)
	require.True(t, field.Metadata.AllowEmptyString)
	require.True(t, field.Metadata.AsArray)
	require.True(t, field.Metadata.Immutable)
	require.Equal(t, "6.7.0.0", field.Metadata.Since)
	require.Equal(t, &Deprecation{DeprecatedSince: "v6.7.0.0", WillBeRemovedIn: "v6.8.0.0", ReplacedBy: "replacement"}, field.Metadata.Deprecated)
	require.True(t, field.Metadata.APICriteriaAware)
	require.True(t, field.Metadata.IgnoreInUnusedMediaSearch)
	require.Equal(t, []string{"product", "custom"}, field.Metadata.RuleAreas)
	require.Equal(t, []string{"self::TYPE_A", "'literal'", "3", "true"}, field.Metadata.Choice.Values)
	require.True(t, *field.Metadata.Choice.Strict)
	require.Empty(t, field.PreservedFlags)

	rendered, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(rendered).Errors, rendered)
	require.Contains(t, rendered, "new AllowHtml(false)")
	require.Contains(t, rendered, "new AsArray()")
	require.Contains(t, rendered, "new IgnoreInUnusedMediaSearch()")
	require.Contains(t, rendered, "new Deprecated('v6.7.0.0', 'v6.8.0.0', 'replacement')")
	require.Contains(t, rendered, "new RuleAreas('product', 'custom')")
	require.Contains(t, rendered, "new Choice([self::TYPE_A, 'literal', 3, true], strict: true)")

	roundTripped, err := ImportDefinition(rendered, nil)
	require.NoError(t, err)
	require.Equal(t, field.Metadata, fieldByProperty(t, roundTripped.Fields, "type").Metadata)
}
