package twigmigration

import (
	"errors"
	"testing"

	"github.com/shopware/shopware-lsp/internal/parser/twig/parser"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/stretchr/testify/require"
)

func TestCompileAdministrationTwigMigrations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		rule     string
		before   string
		expected string
	}{
		{"alert variant", "alert", `<sw-alert variant="error">Failure</sw-alert>`, `<mt-banner variant="critical">Failure</mt-banner>`},
		{"button route and variant", "button", `<sw-button variant="ghost-danger" :router-link="detailRoute">Open</sw-button>`, `<mt-button variant="critical" @click="$router.push(detailRoute)" ghost>Open</mt-button>`},
		{"card", "card", `<sw-card>Content</sw-card>`, `<mt-card>Content</mt-card>`},
		{"checkbox", "checkbox-field", `<sw-checkbox-field v-model:value="enabled"><template #label>Enabled</template></sw-checkbox-field>`, `<mt-checkbox v-model:checked="enabled" label="Enabled"></mt-checkbox>`},
		{"colorpicker", "colorpicker", `<sw-colorpicker :value="color"><template #label>{{ $t('color') }}</template></sw-colorpicker>`, `<mt-colorpicker :model-value="color" :label="$t('color')"></mt-colorpicker>`},
		{"datepicker", "datepicker", `<sw-datepicker v-model:value="date" />`, `<mt-datepicker v-model="date" />`},
		{"email", "email-field", `<sw-email-field value="a@example.com" size="medium" />`, `<mt-email-field model-value="a@example.com" size="default" />`},
		{"icon", "icon", `<sw-icon name="regular-times-s" small />`, `<mt-icon name="regular-times-s" size="16px" />`},
		{"loader", "loader", `<sw-loader />`, `<mt-loader />`},
		{"number", "number-field", `<sw-number-field :value="amount" @update:value="changed" />`, `<mt-number-field :model-value="amount" @update:model-value="changed" />`},
		{"password slots", "password-field", `<sw-password-field><template #label>Password</template><template #hint>{{ hint }}</template></sw-password-field>`, `<mt-password-field label="Password" :hint="hint"></mt-password-field>`},
		{"progress", "progress-bar", `<sw-progress-bar value="5" />`, `<mt-progress-bar model-value="5" />`},
		{"select prop", "select-field", `<sw-select-field :value="selected" :options="options" />`, `<mt-select :model-value="selected" :options="options" />`},
		{"select option children", "select-field", `<sw-select-field><option value="one">One</option><option :value="other">{{ otherLabel }}</option></sw-select-field>`, `<mt-select :options="[{ label: 'One', value: 'one' }, { label: otherLabel, value: other }]"></mt-select>`},
		{"skeleton", "skeleton-bar", `<sw-skeleton-bar />`, `<mt-skeleton-bar />`},
		{"switch uses Meteor model API", "switch-field", `<sw-switch-field value="true" @update:value="changed" />`, `<mt-switch model-value="true" @update:model-value="changed" />`},
		{"text uses one-way model value", "text-field", `<sw-text-field :value="computedName" :required="true" />`, `<mt-text-field :model-value="computedName" required />`},
		{"textarea event", "textarea-field", `<sw-textarea-field v-model:value="description" @update:value="changed" />`, `<mt-textarea v-model="description" @update:model-value="changed" />`},
		{"url", "url-field", `<sw-url-field :value="url"><template #label>URL</template></sw-url-field>`, `<mt-url-field :model-value="url" label="URL"></mt-url-field>`},
		{"popover preserves structural condition", "popover", `<sw-popover v-if="open" :resizeWidth="true">Content</sw-popover>`, `<mt-floating-ui v-if="open" :match-reference-width="true" :is-opened="true">Content</mt-floating-ui>`},
	}

	covered := make(map[string]struct{}, len(tests))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rule, found := RuleByID(test.rule)
			require.True(t, found)
			updated := compileSource(t, test.before, rule)
			require.Equal(t, test.expected, updated)
			require.Empty(t, parser.Parse(updated).Errors)
		})
		covered[test.rule] = struct{}{}
	}
	for _, rule := range Rules() {
		_, found := covered[rule.ID]
		require.Truef(t, found, "missing fixture for %s", rule.ID)
	}
}

func TestCompileRejectsLossyUpstreamFixes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		rule   string
		source string
	}{
		{"card", `<sw-card aiBadge>Content</sw-card>`},
		{"card", `<sw-card :contentPadding="hasPadding">Content</sw-card>`},
		{"checkbox-field", `<sw-checkbox-field><template #hint>Do not lose me</template></sw-checkbox-field>`},
		{"email-field", `<sw-email-field @base-field-mounted="mounted" />`},
		{"select-field", `<sw-select-field :aside="aside" />`},
		{"button", `<sw-button :variant="variant" />`},
		{"popover", `<sw-popover :zIndex="level" />`},
	}
	for _, test := range tests {
		rule, found := RuleByID(test.rule)
		require.True(t, found)
		result := parser.Parse(test.source)
		require.Empty(t, result.Errors)
		tags := twigquery.Nodes(result.Tree.Root, twigsyntax.HtmlTag)
		require.NotEmpty(t, tags)
		_, err := Compile(test.source, tags[0], rule)
		require.ErrorIs(t, err, ErrUnsafe)
	}
}

func TestRulesExcludeUnavailableExternalLinkMigration(t *testing.T) {
	t.Parallel()

	_, found := RuleByID("external-link")
	require.False(t, found)
	_, found = RuleForTag("sw-external-link")
	require.False(t, found)
}

func compileSource(t *testing.T, source string, rule Rule) string {
	t.Helper()
	result := parser.Parse(source)
	require.Empty(t, result.Errors)
	for _, tag := range twigquery.Nodes(result.Tree.Root, twigsyntax.HtmlTag) {
		if start := twigquery.Nodes(tag, twigsyntax.HtmlStartingTag); len(start) != 0 &&
			twigquery.HTMLTagName(start[0]) == rule.SourceTag {
			edits, err := Compile(source, tag, rule)
			require.NoError(t, err)
			updated, err := rewrite.Apply(source, edits)
			require.NoError(t, err)
			return updated
		}
	}
	require.FailNow(t, "source tag not found", rule.SourceTag)
	return ""
}

func TestErrUnsafeIsStable(t *testing.T) {
	require.True(t, errors.Is(unsafe("reason"), ErrUnsafe))
}
