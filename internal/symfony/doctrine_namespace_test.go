package symfony

import (
	"os"
	"path/filepath"
	"testing"

	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseXMLDoctrineNamespaceAliasesTree(t *testing.T) {
	source := `<?xml version="1.0"?>
<container>
  <services>
    <service id="doctrine.orm.default_entity_manager">
      <service>
        <call method="setEntityNamespaces">
          <argument type="collection">
            <argument key="MyNiceBundle">My\NiceBundle\Entity</argument>
          </argument>
        </call>
      </service>
    </service>
    <service id="doctrine_mongodb.odm.default_configuration">
      <call method="setDocumentNamespaces">
        <argument type="collection">
          <argument key="AcmeFrontendBundle">Acme\FrontendBundle\Document</argument>
        </argument>
      </call>
    </service>
    <service id="app.unrelated">
      <call method="setEntityNamespaces">
        <argument key="IgnoredBundle">Ignored\Entity</argument>
      </call>
    </service>
  </services>
</container>`
	tree := xmlparser.Parse(source).Tree

	assert.Equal(t, map[string][]string{
		"MyNiceBundle":       {"My\\NiceBundle\\Entity"},
		"AcmeFrontendBundle": {"Acme\\FrontendBundle\\Document"},
	}, ParseXMLDoctrineNamespaceAliasesTree(tree.Root))
}

func TestServiceIndexExposesCompiledDoctrineNamespaceAliases(t *testing.T) {
	projectRoot := t.TempDir()
	containerDir := filepath.Join(projectRoot, "var", "cache", "dev_test")
	require.NoError(t, os.MkdirAll(containerDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(
			containerDir,
			"Shopware_Core_KernelDevDebugContainer.xml",
		),
		[]byte(`<container><services>
<service id="doctrine.orm.default_entity_manager">
  <call method="setEntityNamespaces">
    <argument><argument key="LegacyBundle">Legacy\Entity</argument></argument>
  </call>
</service>
</services></container>`),
		0o644,
	))
	index, err := NewServiceIndex(projectRoot, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	aliases, revision := index.GetDoctrineNamespaceAliasesState()
	assert.Equal(t, map[string][]string{
		"LegacyBundle": {"Legacy\\Entity"},
	}, aliases)
	assert.NotZero(t, revision)
}
