package twig

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGuardCompletionAt(t *testing.T) {
	tests := []struct {
		source   string
		found    bool
		callable bool
		kind     GuardKind
		prefix   string
	}{
		{
			source: `{% guard <caret> %}`,
			found:  true,
		},
		{
			source: `{%- guard fun<caret> %}`,
			found:  true,
			prefix: "fun",
		},
		{
			source:   "{% guard\nfunction <caret>%}",
			found:    true,
			callable: true,
			kind:     GuardFunction,
		},
		{
			source:   `{% guard filter upp<caret> %}`,
			found:    true,
			callable: true,
			kind:     GuardFilter,
			prefix:   "upp",
		},
		{
			source:   `{% guard test ev<caret> %}`,
			found:    true,
			callable: true,
			kind:     GuardTest,
			prefix:   "ev",
		},
		{
			source: `{# {% guard function <caret> #}`,
		},
		{
			source: `{% raw %}{% guard function <caret>`,
		},
		{
			source: `{% guard function importmap <caret>%}`,
		},
	}
	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			offset := strings.Index(test.source, "<caret>")
			require.NotEqual(t, -1, offset)
			source := strings.Replace(test.source, "<caret>", "", 1)
			context, found := GuardCompletionAt(
				[]byte(source),
				uint32(offset),
			)
			assert.Equal(t, test.found, found)
			if !test.found {
				return
			}
			assert.Equal(t, test.callable, context.Callable)
			assert.Equal(t, test.kind, context.Kind)
			assert.Equal(t, test.prefix, context.Prefix)
		})
	}
}

func TestGuardReferenceAt(t *testing.T) {
	source := `{% guard function importmap %}
{% guard filter upper %}
{% guard test even %}
{# {% guard function hidden_comment %} #}
{% raw %}{% guard filter hidden_raw %}{% endraw %}`
	for _, test := range []struct {
		needle string
		kind   GuardKind
	}{
		{needle: "importmap", kind: GuardFunction},
		{needle: "upper", kind: GuardFilter},
		{needle: "even", kind: GuardTest},
	} {
		offset := strings.Index(source, test.needle) + 2
		reference, found := GuardReferenceAt(
			[]byte(source),
			uint32(offset),
		)
		require.True(t, found, test.needle)
		assert.Equal(t, test.kind, reference.Kind)
		assert.Equal(t, test.needle, reference.Name)
		assert.Equal(
			t,
			test.needle,
			source[reference.Range.Start:reference.Range.End],
		)
	}
	for _, needle := range []string{"hidden_comment", "hidden_raw"} {
		offset := strings.Index(source, needle) + 2
		_, found := GuardReferenceAt([]byte(source), uint32(offset))
		assert.False(t, found)
	}
}
