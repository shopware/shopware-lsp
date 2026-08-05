package security

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

func TestIndexCollectsVoterAndConfigurationAttributes(t *testing.T) {
	index, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	phpSource := `<?php
namespace App\Security;

use Symfony\Component\Security\Core\Authorization\Voter\Voter;

final class ArticleVoter extends Voter
{
    private const EDIT = 'article.edit';
    private const DELETE = 'article.delete';
    private const UNUSED = 'article.unused';
    private const BULK = ['article.publish'];
    private array $owned = ['article.owned'];

    protected function supports(string $attribute, mixed $subject): bool
    {
        return in_array($attribute, [self::EDIT, self::DELETE], true);
    }

    protected function voteOnAttribute(string $attribute, mixed $subject, $token): bool
    {
        if ($attribute === 'article.view') {}
        if (in_array($attribute, self::BULK, true)) {}
        if (in_array($attribute, $this->owned, true)) {}
        switch ($attribute) {
            case 'article.archive':
                break;
        }
        return true;
    }
}
`
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/src/ArticleVoter.php",
		[]byte(phpSource),
	)))

	yamlSource := `security:
  role_hierarchy:
    ROLE_EDITOR: [ROLE_USER, ROLE_REVIEWER]
  access_control:
    - { path: ^/admin, roles: [ROLE_ADMIN, PUBLIC_ACCESS] }
`
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/config/packages/security.yaml",
		[]byte(yamlSource),
	)))

	names, err := index.Names()
	require.NoError(t, err)
	for _, expected := range []string{
		"article.edit",
		"article.delete",
		"article.view",
		"article.publish",
		"article.owned",
		"article.archive",
		"ROLE_EDITOR",
		"ROLE_USER",
		"ROLE_REVIEWER",
		"ROLE_ADMIN",
		"PUBLIC_ACCESS",
		"IS_AUTHENTICATED_FULLY",
	} {
		require.True(t, slices.Contains(names, expected), "%q not in %v", expected, names)
	}
	require.False(t, slices.Contains(names, "article.unused"), names)

	attribute, found, err := index.Attribute("ARTICLE.EDIT")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, attribute.Declarations())
	require.Equal(t, OriginVoter, attribute.Declarations()[0].Origin)
	require.Equal(t, "App\\Security\\ArticleVoter", attribute.Declarations()[0].Class)
}

func TestIndexCollectsAuthorizationUsesAndRestoresCache(t *testing.T) {
	cacheDir := t.TempDir()
	index, err := NewIndex(cacheDir)
	require.NoError(t, err)

	phpSource := `<?php
use Symfony\Component\Security\Http\Attribute\IsGranted;
use Sensio\Bundle\FrameworkExtraBundle\Configuration\Security;

#[IsGranted('article.edit')]
#[Security("is_granted('article.publish', post)")]
function edit($checker): void
{
    $checker->isGranted(['article.view', 'article.edit']);
    $checker->isGranted('IS_AUTHENTICATED_FULLY');
}

/** @IsGranted("article.archive") */
function archive(): void {}

/** @Security("has_role('ROLE_EDITOR')") */
function legacy(): void {}
`
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/src/Controller.php",
		[]byte(phpSource),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/templates/article.html.twig",
		[]byte(`{{ is_granted(['article.edit', 'article.view']) }}`),
	)))

	attribute, found, err := index.Attribute("article.edit")
	require.NoError(t, err)
	require.True(t, found)
	require.GreaterOrEqual(t, len(attribute.References()), 3)
	builtIn, found, err := index.Attribute("IS_AUTHENTICATED_FULLY")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, builtIn.Declarations(), 1)
	require.Equal(t, OriginBuiltIn, builtIn.Declarations()[0].Origin)
	require.Len(t, builtIn.References(), 1)
	require.NoError(t, index.Close())

	restored, err := NewIndex(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	attribute, found, err = restored.Attribute("ROLE_EDITOR")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, attribute.References(), 1)
	require.Equal(t, OriginPHPDoc, attribute.References()[0].Origin)
}

func TestIndexRemovesStaleSecurityRecords(t *testing.T) {
	index, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	path := "/project/config/packages/security.yaml"
	require.NoError(t, index.Index(indexer.NewParsedFile(
		path,
		[]byte("security:\n  role_hierarchy:\n    ROLE_ADMIN: ROLE_USER\n"),
	)))
	_, found, err := index.Attribute("ROLE_ADMIN")
	require.NoError(t, err)
	require.True(t, found)

	require.NoError(t, index.Index(indexer.NewParsedFile(
		path,
		[]byte("framework:\n  secret: test\n"),
	)))
	_, found, err = index.Attribute("ROLE_ADMIN")
	require.NoError(t, err)
	require.False(t, found)
}

func TestIndexCollectsAndRestoresSecurityConfiguration(t *testing.T) {
	cacheDir := t.TempDir()
	index, err := NewIndex(cacheDir)
	require.NoError(t, err)

	path := "/project/config/packages/security.yaml"
	source := `security:
  providers:
    app_users:
      entity:
        class: App\Entity\User
    fallback:
      chain:
        providers: [app_users]
  firewalls:
    main:
      provider: fallback
      switch_user:
        provider: app_users
`
	require.NoError(t, index.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))

	providers, err := index.ConfigNames(ConfigProvider)
	require.NoError(t, err)
	require.Equal(t, []string{"app_users", "fallback"}, providers)
	firewalls, err := index.ConfigNames(ConfigFirewall)
	require.NoError(t, err)
	require.Equal(t, []string{"main"}, firewalls)

	appUsers, found, err := index.ConfigSymbol(
		"APP_USERS",
		ConfigProvider,
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, appUsers.Declarations(), 1)
	require.Len(t, appUsers.References(), 2)
	require.NoError(t, index.Close())

	restored, err := NewIndex(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	fallback, found, err := restored.ConfigSymbol(
		"fallback",
		ConfigProvider,
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, fallback.Declarations(), 1)
	require.Len(t, fallback.References(), 1)
}

func TestIndexCollectsXMLAndPHPSecurityConfiguration(t *testing.T) {
	cacheDir := t.TempDir()
	index, err := NewIndex(cacheDir)
	require.NoError(t, err)

	xmlPath := "/project/config/packages/security.xml"
	xmlSource := `<?xml version="1.0"?>
<srv:container xmlns="http://symfony.com/schema/dic/security"
    xmlns:srv="http://symfony.com/schema/dic/services">
  <config>
    <provider name="legacy_users"><memory/></provider>
  </config>
</srv:container>
`
	phpPath := "/project/config/packages/security.php"
	phpSource := `<?php
use Symfony\Config\SecurityConfig;
return static function (SecurityConfig $security): void {
    $security->provider('all_users')->chain()
        ->providers(['legacy_users']);
    $security->firewall('main')->provider('all_users');
};
`
	require.NoError(t, index.Index(indexer.NewParsedFile(
		xmlPath,
		[]byte(xmlSource),
	)))
	require.NoError(t, index.Index(indexer.NewParsedFile(
		phpPath,
		[]byte(phpSource),
	)))

	providers, err := index.ConfigNames(ConfigProvider)
	require.NoError(t, err)
	require.Equal(t, []string{"all_users", "legacy_users"}, providers)
	firewalls, err := index.ConfigNames(ConfigFirewall)
	require.NoError(t, err)
	require.Equal(t, []string{"main"}, firewalls)
	legacy, found, err := index.ConfigSymbol(
		"legacy_users",
		ConfigProvider,
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, legacy.Declarations(), 1)
	require.Equal(t, xmlPath, legacy.Declarations()[0].File)
	require.Len(t, legacy.References(), 1)
	require.Equal(t, phpPath, legacy.References()[0].File)
	require.NoError(t, index.Close())

	restored, err := NewIndex(cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	allUsers, found, err := restored.ConfigSymbol(
		"all_users",
		ConfigProvider,
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, allUsers.Declarations(), 1)
	require.Len(t, allUsers.References(), 1)
}
