package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/security"
)

func TestSecurityCompletionForTwigPHPAttributesAndPHPDoc(t *testing.T) {
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/config/packages/security.yaml",
		[]byte(`security:
  role_hierarchy:
    ROLE_EDITOR: [ROLE_USER]
  access_control:
    - { path: ^/admin, roles: ROLE_ADMIN }
`),
	)))
	provider := NewSecurityCompletionProvider(index)

	tests := []struct {
		path   string
		source string
		marker string
	}{
		{
			path:   "/project/templates/article.html.twig",
			source: `{{ is_granted(['']) }}`,
			marker: "''",
		},
		{
			path: "/project/src/Controller.php",
			source: `<?php
use Symfony\Component\Security\Http\Attribute\IsGranted;
#[IsGranted('')]
function edit(): void {}
`,
			marker: "''",
		},
		{
			path: "/project/src/LegacyController.php",
			source: `<?php
/** @IsGranted("") */
function edit(): void {}
`,
			marker: `""`,
		},
	}
	for _, test := range tests {
		document := lsp.NewTextDocument(
			"file://"+test.path,
			test.source,
			1,
		)
		offset := strings.Index(test.source, test.marker) + 1
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
		request := securityCompletionRequest(document, node, uint32(offset))
		items := provider.GetCompletions(
			context.Background(),
			request,
		)
		requireCompletion(t, items, "ROLE_EDITOR")
		requireCompletion(t, items, "PUBLIC_ACCESS")
	}
}

func TestSecurityCompletionOverlaysUnsavedVoterAttributes(t *testing.T) {
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	source := `<?php
use Symfony\Component\Security\Core\Authorization\Voter\Voter;
final class ArticleVoter extends Voter {
    protected function supports(string $attribute, mixed $subject): bool {
        return $attribute === 'article.unsaved';
    }
    protected function voteOnAttribute(string $attribute, mixed $subject, $token): bool {
        return true;
    }
}
function check($authorization): void {
    $authorization->isGranted('');
}
`
	document := lsp.NewTextDocument(
		"file:///project/src/ArticleVoter.php",
		source,
		1,
	)
	offset := strings.LastIndex(source, "''") + 1
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
	items := NewSecurityCompletionProvider(index).GetCompletions(
		context.Background(),
		securityCompletionRequest(document, node, uint32(offset)),
	)
	requireCompletion(t, items, "article.unsaved")
}

func TestSecurityConfigCompletionForKeysAndProviderNames(t *testing.T) {
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/config/packages/security.yaml",
		[]byte(`security:
  providers:
    app_users:
      memory: null
`),
	)))
	provider := NewSecurityCompletionProvider(index)

	keySource := `security:
  firewalls:
    main:
      stateless: true
`
	keyDocument := lsp.NewTextDocument(
		"file:///project/config/packages/security.yaml",
		keySource,
		1,
	)
	keyOffset := uint32(strings.Index(keySource, "stateless") + 2)
	keyItems := provider.GetCompletions(
		context.Background(),
		securityCompletionRequest(
			keyDocument,
			keyDocument.SyntaxTree.Root.NodeAtOffset(keyOffset),
			keyOffset,
		),
	)
	requireCompletion(t, keyItems, "custom_authenticators")
	requireCompletion(t, keyItems, "logout")

	valueSource := `security:
  firewalls:
    main:
      provider: ''
`
	valueDocument := lsp.NewTextDocument(
		"file:///project/config/packages/security.yaml",
		valueSource,
		1,
	)
	valueOffset := uint32(strings.Index(valueSource, "''") + 1)
	valueItems := provider.GetCompletions(
		context.Background(),
		securityCompletionRequest(
			valueDocument,
			valueDocument.SyntaxTree.Root.NodeAtOffset(valueOffset),
			valueOffset,
		),
	)
	requireCompletion(t, valueItems, "app_users")
}

func TestSecurityConfigCompletionUsesAuthenticatorSpecificSchema(t *testing.T) {
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	source := `when@test:
  security:
    firewalls:
      main:
        json_login:
          username_path: credentials.email
`
	document := lsp.NewTextDocument(
		"file:///project/config/packages/security.yaml",
		source,
		1,
	)
	offset := uint32(strings.Index(source, "username_path") + 2)
	items := NewSecurityCompletionProvider(index).GetCompletions(
		context.Background(),
		securityCompletionRequest(
			document,
			document.SyntaxTree.Root.NodeAtOffset(offset),
			offset,
		),
	)
	labels := completionLabels(items)
	require.Contains(t, labels, "password_path")
	require.Contains(t, labels, "check_path")
	require.NotContains(t, labels, "username_parameter")
}

func TestSecurityConfigCompletionInTypedPHPConfigurator(t *testing.T) {
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/config/packages/security.xml",
		[]byte(`<?xml version="1.0"?>
<srv:container xmlns="http://symfony.com/schema/dic/security"
    xmlns:srv="http://symfony.com/schema/dic/services">
  <config>
    <provider name="app_users"><memory/></provider>
  </config>
</srv:container>
`),
	)))

	source := `<?php
use Symfony\Config\SecurityConfig;
return static function (SecurityConfig $security): void {
    $security->firewall('main')->provider('');
};
`
	document := lsp.NewTextDocument(
		"file:///project/config/packages/security.php",
		source,
		1,
	)
	offset := uint32(strings.LastIndex(source, "''") + 1)
	items := NewSecurityCompletionProvider(index).GetCompletions(
		context.Background(),
		securityCompletionRequest(
			document,
			document.SyntaxTree.Root.NodeAtOffset(offset),
			offset,
		),
	)
	requireCompletion(t, items, "app_users")
}

func securityCompletionRequest(
	document *lsp.TextDocument,
	node *cst.Node,
	offset uint32,
) *lsp.CompletionRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.CompletionRequest{
		CompletionParams: params,
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
