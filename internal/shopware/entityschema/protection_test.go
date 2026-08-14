package entityschema

import (
	"strings"
	"testing"

	php "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/stretchr/testify/require"
)

func TestEntityProtectionsImportRenderAndRewrite(t *testing.T) {
	source := `<?php declare(strict_types=1);
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
}`

	spec, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.True(t, spec.ReadProtected)
	require.Equal(t, []string{"system"}, spec.ReadProtectionScopes)
	require.True(t, spec.WriteProtected)
	require.Equal(t, []string{"system", "crud"}, spec.WriteProtectionScopes)
	require.Empty(t, spec.ProtectionMethodRaw)

	rendered, err := RenderDefinition(spec)
	require.NoError(t, err)
	require.Empty(t, php.Parse(rendered).Errors, rendered)
	require.Contains(t, rendered, "new ReadProtection('system')")
	require.Contains(t, rendered, "new WriteProtection('system', 'crud')")

	spec.ReadProtected = false
	spec.ReadProtectionScopes = nil
	rewritten, err := RewriteDefinition(source, spec)
	require.NoError(t, err)
	require.NotContains(t, rewritten, "new ReadProtection")
	require.Contains(t, rewritten, "new WriteProtection('system', 'crud')")
	require.Equal(t, 1, strings.Count(rewritten, "function defineProtections"))
}

func TestCustomEntityProtectionMethodIsPreservedAndCannotBeOverwritten(t *testing.T) {
	source := `<?php declare(strict_types=1);
namespace Acme\Example;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
use Shopware\Core\Framework\DataAbstractionLayer\EntityProtection\EntityProtectionCollection;
use Shopware\Core\Framework\DataAbstractionLayer\Field\IdField;
use Shopware\Core\Framework\DataAbstractionLayer\FieldCollection;
class ExampleDefinition extends EntityDefinition {
    public const ENTITY_NAME = 'acme_example';
    protected function defineProtections(): EntityProtectionCollection { return $this->protectionFactory(); }
    protected function defineFields(): FieldCollection { return new FieldCollection([new IdField('id', 'id')]); }
}`

	spec, err := ImportDefinition(source, nil)
	require.NoError(t, err)
	require.Contains(t, spec.ProtectionMethodRaw, "$this->protectionFactory()")

	rewritten, err := RewriteDefinition(source, spec)
	require.NoError(t, err)
	require.Contains(t, rewritten, "$this->protectionFactory()")

	spec.ProtectionMethodRaw = ""
	spec.WriteProtected = true
	_, err = RewriteDefinition(source, spec)
	require.ErrorContains(t, err, "cannot overwrite customized defineProtections method")
}
