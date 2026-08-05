package security

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
)

func TestReferenceAtPHPAuthorizationCallsAndAttributes(t *testing.T) {
	source := `<?php
use Symfony\Component\Security\Http\Attribute\IsGranted;
use Sensio\Bundle\FrameworkExtraBundle\Configuration\Security;

#[IsGranted('article.edit')]
#[Security("is_granted('article.publish', post)")]
function edit($checker): void
{
    $checker->isGranted(['article.view']);
    $checker->isGrantedForUser($user, 'article.archive');
}
`
	file := indexer.NewParsedFile("/project/src/Controller.php", []byte(source))
	root := file.SyntaxTree().Root

	for value, origin := range map[string]Origin{
		"article.edit":    OriginPHPAttribute,
		"article.publish": OriginPHPExpression,
		"article.view":    OriginPHPCall,
		"article.archive": OriginPHPCall,
	} {
		offset := uint32(strings.Index(source, value) + 2)
		node := root.NodeAtOffset(offset)
		reference, found := ReferenceAt(
			context.Background(),
			file.Path,
			root,
			node,
			source,
			offset,
		)
		require.True(t, found, value)
		require.Equal(t, value, reference.Name)
		require.Equal(t, origin, reference.Origin)
		require.True(t, reference.Range.Contains(offset))
	}
}

func TestReferenceAtPHPDocSecurityExpressions(t *testing.T) {
	source := `<?php
/** @IsGranted("article.edit") */
function edit(): void {}

/** @Security("has_role('ROLE_EDITOR')") */
function legacy(): void {}
`
	file := indexer.NewParsedFile("/project/src/Controller.php", []byte(source))
	root := file.SyntaxTree().Root
	for _, value := range []string{"article.edit", "ROLE_EDITOR"} {
		offset := uint32(strings.Index(source, value) + 2)
		reference, found := ReferenceAt(
			context.Background(),
			file.Path,
			root,
			root.NodeAtOffset(offset),
			source,
			offset,
		)
		require.True(t, found, value)
		require.Equal(t, value, reference.Name)
		require.Equal(t, OriginPHPDoc, reference.Origin)
	}
}

func TestReferenceAtTwigAuthorizationArrays(t *testing.T) {
	source := `{{ is_granted(['article.view']) }}
{{ is_granted_for_user(user, ['article.edit']) }}
{{ access_decision_for_user(user, 'article.publish', post) }}
`
	file := indexer.NewParsedFile("/project/templates/article.html.twig", []byte(source))
	root := file.SyntaxTree().Root
	for _, value := range []string{
		"article.view",
		"article.edit",
		"article.publish",
	} {
		offset := uint32(strings.Index(source, value) + 2)
		reference, found := ReferenceAt(
			context.Background(),
			file.Path,
			root,
			root.NodeAtOffset(offset),
			source,
			offset,
		)
		require.True(t, found, value)
		require.Equal(t, value, reference.Name)
		require.Equal(t, OriginTwig, reference.Origin)
	}
}

func TestReferenceAtYAMLSecurityRoles(t *testing.T) {
	source := `security:
  role_hierarchy:
    ROLE_EDITOR: [ROLE_USER]
  access_control:
    - { path: ^/admin, roles: ROLE_ADMIN }
`
	file := indexer.NewParsedFile("/project/config/packages/security.yaml", []byte(source))
	root := file.SyntaxTree().Root
	for _, value := range []string{"ROLE_EDITOR", "ROLE_USER", "ROLE_ADMIN"} {
		offset := uint32(strings.Index(source, value) + 2)
		reference, found := ReferenceAt(
			context.Background(),
			file.Path,
			root,
			root.NodeAtOffset(offset),
			source,
			offset,
		)
		require.True(t, found, value)
		require.Equal(t, value, reference.Name)
	}
}
