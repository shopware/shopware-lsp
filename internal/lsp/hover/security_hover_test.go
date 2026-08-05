package hover

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/security"
)

func TestSecurityHoverDescribesVoterAttribute(t *testing.T) {
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/src/ArticleVoter.php",
		[]byte(`<?php
namespace App\Security;
use Symfony\Component\Security\Core\Authorization\Voter\Voter;
final class ArticleVoter extends Voter {
    protected function supports(string $attribute, mixed $subject): bool {
        return $attribute === 'article.edit';
    }
    protected function voteOnAttribute(string $attribute, mixed $subject, $token): bool {
        return true;
    }
}`),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/templates/other.html.twig",
		[]byte(`{{ is_granted('article.edit') }}`),
	)))

	source := `{{ is_granted('article.edit') }}`
	document := lsp.NewTextDocument(
		"file:///project/templates/article.html.twig",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "article.edit") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewSecurityHoverProvider("/project", index).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "Symfony security attribute")
	assert.Contains(t, result.Contents.Value, "App\\Security\\ArticleVoter")
	assert.Contains(t, result.Contents.Value, "1 indexed use")
}

func TestSecurityHoverDescribesConfigurationOptionAndProvider(t *testing.T) {
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	source := `security:
  providers:
    app_users:
      memory: null
  firewalls:
    main:
      provider: app_users
      json_login:
        username_path: credentials.email
`
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/config/packages/security.yaml",
		[]byte(source),
	)))
	document := lsp.NewTextDocument(
		"file:///project/config/packages/security.yaml",
		source,
		1,
	)
	provider := NewSecurityHoverProvider("/project", index)

	for needle, expected := range map[string]string{
		"firewalls":           "Application firewalls",
		"provider: app_users": "Symfony user provider",
		"username_path":       "Property path containing the username",
	} {
		offset := strings.Index(source, needle)
		require.NotEqual(t, -1, offset)
		if needle == "provider: app_users" {
			offset += strings.Index(needle, "app_users")
		}
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
		line, character := document.LineIndex.PositionUTF16(uint32(offset))
		params := &protocol.HoverParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		result, hoverErr := provider.GetHover(
			context.Background(),
			&lsp.HoverRequest{
				HoverParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document:        document,
					Language:        document.SyntaxLanguage,
					DocumentContent: document.Text,
					DocumentTree:    document.SyntaxTree,
					LineIndex:       document.LineIndex,
					Root:            document.SyntaxTree.Root,
					Node:            node,
				},
			},
		)
		require.NoError(t, hoverErr)
		require.NotNil(t, result)
		assert.Contains(t, result.Contents.Value, expected)
	}
}
