package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/security"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestSecurityDefinitionNavigatesToRoleAndVoter(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "packages", "security.yaml")
	voterPath := filepath.Join(root, "src", "ArticleVoter.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(voterPath), 0o755))
	configSource := `security:
  role_hierarchy:
    ROLE_EDITOR: [ROLE_USER]
`
	voterSource := `<?php
use Symfony\Component\Security\Core\Authorization\Voter\Voter;
final class ArticleVoter extends Voter {
    protected function supports(string $attribute, mixed $subject): bool {
        return $attribute === 'article.edit';
    }
    protected function voteOnAttribute(string $attribute, mixed $subject, $token): bool {
        return true;
    }
}
`
	require.NoError(t, os.WriteFile(configPath, []byte(configSource), 0o644))
	require.NoError(t, os.WriteFile(voterPath, []byte(voterSource), 0o644))
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		configPath,
		[]byte(configSource),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		voterPath,
		[]byte(voterSource),
	)))

	useSource := `{{ is_granted('ROLE_EDITOR') }}
{{ is_granted('article.edit') }}
`
	document := lsp.NewTextDocument(
		uriutil.FileURI(filepath.Join(root, "templates", "article.html.twig")),
		useSource,
		1,
	)
	provider := NewSecurityDefinitionProvider(index)
	for value, expectedPath := range map[string]string{
		"ROLE_EDITOR":  configPath,
		"article.edit": voterPath,
	} {
		offset := uint32(strings.Index(useSource, value) + 2)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		locations := provider.GetDefinition(
			context.Background(),
			securityDefinitionRequest(document, node, offset),
		)
		require.Len(t, locations, 1, value)
		assert.Equal(t, uriutil.FileURI(expectedPath), locations[0].URI)
	}
}

func TestSecurityDefinitionNavigatesToUserProvider(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "packages", "security.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	source := `security:
  providers:
    app_users:
      memory: null
  firewalls:
    main:
      provider: app_users
`
	require.NoError(t, os.WriteFile(configPath, []byte(source), 0o644))
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		configPath,
		[]byte(source),
	)))

	document := lsp.NewTextDocument(uriutil.FileURI(configPath), source, 1)
	offset := uint32(strings.LastIndex(source, "app_users") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	locations := NewSecurityDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(document, node, offset),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(configPath), locations[0].URI)
	assert.Equal(t, 2, locations[0].Range.Start.Line)
}

func TestSecurityDefinitionNavigatesFromPHPConfiguratorToXMLProvider(
	t *testing.T,
) {
	root := t.TempDir()
	xmlPath := filepath.Join(root, "config", "packages", "security.xml")
	require.NoError(t, os.MkdirAll(filepath.Dir(xmlPath), 0o755))
	xmlSource := `<?xml version="1.0"?>
<srv:container xmlns="http://symfony.com/schema/dic/security"
    xmlns:srv="http://symfony.com/schema/dic/services">
  <config>
    <provider name="app_users"><memory/></provider>
  </config>
</srv:container>
`
	require.NoError(t, os.WriteFile(xmlPath, []byte(xmlSource), 0o644))
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		xmlPath,
		[]byte(xmlSource),
	)))

	phpPath := filepath.Join(root, "config", "packages", "security.php")
	phpSource := `<?php
use Symfony\Config\SecurityConfig;
return static function (SecurityConfig $security): void {
    $security->firewall('main')->provider('app_users');
};
`
	document := lsp.NewTextDocument(
		uriutil.FileURI(phpPath),
		phpSource,
		1,
	)
	offset := uint32(strings.LastIndex(phpSource, "app_users") + 2)
	locations := NewSecurityDefinitionProvider(index).GetDefinition(
		context.Background(),
		securityDefinitionRequest(
			document,
			document.SyntaxTree.Root.NodeAtOffset(offset),
			offset,
		),
	)
	require.Len(t, locations, 1)
	assert.Equal(t, uriutil.FileURI(xmlPath), locations[0].URI)
	assert.Equal(t, 4, locations[0].Range.Start.Line)
}

func securityDefinitionRequest(
	document *lsp.TextDocument,
	node *cst.Node,
	offset uint32,
) *lsp.DefinitionRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.DefinitionRequest{
		DefinitionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	}
}
