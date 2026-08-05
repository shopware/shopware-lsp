package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigTypesTagCompletesPHPClassesAndPreservesTypeSyntax(
	t *testing.T,
) {
	provider := twigTypesCompletionFixture(t)
	items := twigTypesCompletionItems(
		t,
		provider,
		`{% types { user: 'App\\Us<caret>' } %}`,
	)
	user := requireCompletion(t, items, "App\\User")
	assert.Equal(t, int(protocol.ClassCompletion), user.Kind)
	edit, ok := user.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, `App\\User`, edit.NewText)

	genericItems := twigTypesCompletionItems(
		t,
		provider,
		`{% types { users: 'list<App\\Us<caret>>' } %}`,
	)
	genericUser := requireCompletion(t, genericItems, "App\\User")
	genericEdit, ok := genericUser.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, `App\\User`, genericEdit.NewText)
}

func TestTwigTypesTagFeedsVariableAndMemberCompletion(t *testing.T) {
	provider := twigTypesCompletionFixture(t)
	variableItems := twigTypesCompletionItems(
		t,
		provider,
		`{% types { user: 'App\\User' } %}
{{ us<caret> }}`,
	)
	user := requireCompletion(t, variableItems, "user")
	assert.Equal(t, "App\\User", user.Detail)

	memberItems := twigTypesCompletionItems(
		t,
		provider,
		`{% types { user?: 'App\\User' } %}
{{ user.<caret> }}`,
	)
	assert.Contains(t, completionLabels(memberItems), "displayName")
	assert.Contains(t, completionLabels(memberItems), "email")
}

func TestTwigTypesTagFeedsForLoopElementCompletion(t *testing.T) {
	provider := twigTypesCompletionFixture(t)
	items := twigTypesCompletionItems(
		t,
		provider,
		`{% types { users: 'App\\User[]' } %}
{% for user in users %}
{{ user.<caret> }}
{% endfor %}`,
	)
	assert.Contains(t, completionLabels(items), "displayName")
	assert.Contains(t, completionLabels(items), "email")

	elseItems := twigTypesCompletionItems(
		t,
		provider,
		`{% types { users: 'App\\User[]' } %}
{% for user in users %}{% else %}
{{ user.<caret> }}
{% endfor %}`,
	)
	assert.NotContains(t, completionLabels(elseItems), "displayName")
	assert.NotContains(t, completionLabels(elseItems), "email")
}

func TestTwigTypesTagFeedsIncompleteStatementCompletion(t *testing.T) {
	provider := twigTypesCompletionFixture(t)
	ifItems := twigTypesCompletionItems(
		t,
		provider,
		`{% types { user: 'App\\User' } %}
{% if<caret> %}`,
	)
	assert.Contains(t, completionLabels(ifItems), "if user.active")

	forItems := twigTypesCompletionItems(
		t,
		provider,
		`{% types { user: 'App\\User' } %}
{% fo<caret> %}`,
	)
	assert.Contains(
		t,
		completionLabels(forItems),
		"for friend in user.friends",
	)
}

func twigTypesCompletionFixture(t *testing.T) *TwigCompletionProvider {
	t.Helper()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/src/User.php",
		[]byte(`<?php
namespace App;
class User {
    public string $displayName;
    public bool $active;
    /** @var User[] */
    public array $friends;
    public function getEmail(): string { return ''; }
}
interface UserProvider {}
enum UserState { case ACTIVE; }`),
	)))
	twigIndex, err := twig.NewTwigIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })
	return NewTwigCompletionProvider(
		"/project",
		twigIndex,
		nil,
		phpIndex,
	)
}

func twigTypesCompletionItems(
	t *testing.T,
	provider *TwigCompletionProvider,
	source string,
) []protocol.CompletionItem {
	t.Helper()
	offset := strings.Index(source, "<caret>")
	require.NotEqual(t, -1, offset)
	source = strings.Replace(source, "<caret>", "", 1)
	_, request := twigCompletionAt(
		"file:///project/templates/page.html.twig",
		source,
		offset,
	)
	return provider.GetCompletions(context.Background(), request)
}
