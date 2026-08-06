package diagnostics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminI18nDeprecationAnalyzerFindsCallsAcrossAdminLanguages(t *testing.T) {
	analyzer := NewAdminI18nDeprecationAnalyzer()
	for _, test := range []struct {
		name   string
		uri    string
		source string
		count  int
	}{
		{
			name:   "JavaScript member call",
			uri:    "file:///project/src/Resources/app/administration/src/component.js",
			source: `this.$tc('translation.key');`,
			count:  1,
		},
		{
			name:   "TypeScript bare call",
			uri:    "file:///project/src/Resources/app/administration/src/component.ts",
			source: `$tc('translation.key', count);`,
			count:  1,
		},
		{
			name:   "Twig call",
			uri:    "file:///project/src/Resources/app/administration/src/component.html.twig",
			source: `{{ $tc('translation.key') }}`,
			count:  1,
		},
		{
			name: "Vue template and script calls",
			uri:  "file:///project/src/Resources/app/administration/src/component.vue",
			source: `<template>{{ $tc('template.key') }}</template>
<script>this.$tc('script.key', count);</script>`,
			count: 2,
		},
		{
			name:   "non-call references",
			uri:    "file:///project/src/Resources/app/administration/src/component.js",
			source: `const $tc = translator; this.$tc;`,
		},
		{
			name:   "current translation API",
			uri:    "file:///project/src/Resources/app/administration/src/component.js",
			source: `this.$t('translation.key');`,
		},
		{
			name:   "outside Administration",
			uri:    "file:///project/src/storefront/component.js",
			source: `this.$tc('translation.key');`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := diagnosticsDocument(test.uri, []byte(test.source))
			problems, err := analyzer.Analyze(context.Background(), document)
			require.NoError(t, err)
			require.Len(t, problems, test.count)
			for _, problem := range problems {
				require.Equal(t, deprecatedAdminI18nTCCode, problem.ID)
				require.NotNil(t, problem.Element)
				require.Equal(t, "$tc", problem.Element.Text())
				require.Equal(t, "$tc", document.Source[problem.Range.Start:problem.Range.End])
			}
		})
	}
}
