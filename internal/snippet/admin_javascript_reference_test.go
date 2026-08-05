package snippet

import (
	"testing"

	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminJavaScriptStringReferences(t *testing.T) {
	source := `
this.$t('admin.this');
this.$root.$tc('admin.root');
translator.$t('admin.injected');
getApplicationRootReference().$t('admin.factory');
$t('admin.bare');
Shopware.Snippet.t('admin.service');
Shopware.Snippet.tc('admin.service-count');
other.t('not.translation');
this.$t(dynamicKey);
` + "this.$t(`admin." + "$" + "{dynamic}`);"
	root := javascriptparser.Parse(source).Tree.Root

	arguments := AdminJavaScriptStringReferences(root)
	var keys []string
	for _, argument := range arguments {
		keys = append(keys, jsquery.StringValue(argument))
	}
	assert.Equal(t, []string{
		"admin.this",
		"admin.root",
		"admin.injected",
		"admin.factory",
		"admin.bare",
		"admin.service",
		"admin.service-count",
	}, keys)
}

func TestAdminJavaScriptStringReference(t *testing.T) {
	root := javascriptparser.Parse(
		`translator.$t('admin.title'); other.t('plain');`,
	).Tree.Root
	arguments := AdminJavaScriptStringReferences(root)
	require.Len(t, arguments, 1)
	assert.True(t, AdminJavaScriptStringReference(arguments[0]))

	calls := jsquery.Calls(root, "other.t")
	require.Len(t, calls, 1)
	plain := jsquery.StringArgument(calls[0], 0)
	require.NotNil(t, plain)
	assert.False(t, AdminJavaScriptStringReference(plain))
}

func TestAdminJavaScriptModuleSnippetReferences(t *testing.T) {
	source := `
Module.register('sw-demo', {
    title: 'sw-demo.general.title',
    description: 'sw-demo.general.description',
    routes: {
        index: {
            meta: { label: 'not-a-module-label' },
        },
    },
    navigation: [{
        label: 'sw-demo.general.navigation',
        path: 'sw.demo.index',
    }],
    settingsItem: {
        group: 'content',
    },
});
Shopware.Module.register('sw-second', {
    title: 'sw-second.general.title',
});
const unrelated = {
    title: 'ordinary.title',
    navigation: [{ label: 'ordinary.label' }],
};
`
	root := javascriptparser.Parse(source).Tree.Root
	references := AdminJavaScriptStringReferences(root)
	var keys []string
	for _, reference := range references {
		keys = append(keys, jsquery.StringValue(reference))
		assert.True(t, AdminJavaScriptStringReference(reference))
	}
	assert.Equal(t, []string{
		"sw-demo.general.title",
		"sw-demo.general.description",
		"sw-demo.general.navigation",
		"sw-second.general.title",
	}, keys)
}
