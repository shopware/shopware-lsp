package formatter

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFormatting adopts shopware-cli's formatter corpus. Each fixture contains
// input, Administration output, and optionally Storefront output separated by
// five hyphens. Every expected output must also be idempotent.
func TestFormatting(t *testing.T) {
	files, err := os.ReadDir("testdata")
	require.NoError(t, err)

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		t.Run(file.Name(), func(t *testing.T) {
			data, readErr := os.ReadFile(filepath.Join("testdata", file.Name()))
			require.NoError(t, readErr)
			parts := strings.SplitN(string(data), "-----", 3)
			require.GreaterOrEqual(t, len(parts), 2)
			for index := range parts {
				parts[index] = strings.Trim(parts[index], "\n")
			}

			assertFormatting(t, parts[0], parts[1], Options{
				InsertSpaces: true, TabSize: 4,
				TwigBlockIndentChildren: false,
			})
			if len(parts) == 3 {
				assertFormatting(t, parts[0], parts[2], Options{
					InsertSpaces: true, TabSize: 4,
					TwigBlockIndentChildren: true,
				})
			}
		})
	}
}

func TestFormattingPreservesTildeWhitespaceControl(t *testing.T) {
	t.Parallel()
	input := `{%~ if enabled ~%}{{~ value ~}}{%~ endif ~%}`
	formatted, err := Format(input, Options{
		InsertSpaces: true, TabSize: 2, TwigBlockIndentChildren: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "{%~ if enabled ~%}\n  {{~ value ~}}\n{%~ endif ~%}", formatted)
}

func TestFormattingUsesTabsFromLSPOptions(t *testing.T) {
	t.Parallel()
	formatted, err := Format(`<div><span>text</span></div>`, Options{
		InsertSpaces: false, TabSize: 8, TwigBlockIndentChildren: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "<div>\n\t<span>text</span>\n</div>", formatted)
}

func TestFormattingOptionsAreIsolatedAcrossConcurrentCalls(t *testing.T) {
	input := `{% block content %}<div><span>text</span></div>{% endblock %}`
	options := []Options{
		{
			InsertSpaces: true, TabSize: 2,
			TwigBlockIndentChildren: true,
		},
		{
			InsertSpaces: false, TabSize: 8,
			TwigBlockIndentChildren: false,
		},
	}
	expected := make([]string, len(options))
	for index, option := range options {
		formatted, err := Format(input, option)
		require.NoError(t, err)
		expected[index] = formatted
	}
	require.NotEqual(t, expected[0], expected[1])

	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		index := worker % len(options)
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 50 {
				formatted, err := Format(input, options[index])
				if !assert.NoError(t, err) {
					return
				}
				if !assert.Equal(t, expected[index], formatted) {
					return
				}
			}
		}()
	}
	wait.Wait()
}

func TestFormattingPreservesTwigDocumentationComments(t *testing.T) {
	t.Parallel()
	input := `{##- Main page content. -##}
{% block content %}{% endblock %}`
	formatted, err := Format(input, Options{
		InsertSpaces: true, TabSize: 4, TwigBlockIndentChildren: true,
	})
	require.NoError(t, err)
	assert.Equal(
		t,
		"{##- Main page content. -##}\n{% block content %}{% endblock %}",
		formatted,
	)
}

func TestFormattingPreservesInlineDocumentationCommentLines(t *testing.T) {
	t.Parallel()
	input := `{% types {
    ## User displayed by the page.
    user: 'App\\User',
} %}
{% set
    ## Number of unread messages.
    unread_count = messages|length
%}
{% for
    ## Product identifier.
    product_id,
    ## Product in the current iteration.
    product
    in products
%}{% endfor %}
{% macro input(
    ## HTML field name.
    name,
) %}{% endmacro %}`
	formatted, err := Format(input, Options{
		InsertSpaces: true, TabSize: 4, TwigBlockIndentChildren: true,
	})
	require.NoError(t, err)
	for _, documentation := range []string{
		"## User displayed by the page.\n",
		"## Number of unread messages.\n",
		"## Product identifier.\n",
		"## Product in the current iteration.\n",
		"## HTML field name.\n",
	} {
		assert.Contains(t, formatted, documentation)
	}
	second, err := Format(formatted, Options{
		InsertSpaces: true, TabSize: 4, TwigBlockIndentChildren: true,
	})
	require.NoError(t, err)
	assert.Equal(t, formatted, second)
}

func assertFormatting(t *testing.T, input, expected string, options Options) {
	t.Helper()
	formatted, err := Format(input, options)
	require.NoError(t, err)
	assert.Equal(t, expected, formatted)

	second, err := Format(formatted, options)
	require.NoError(t, err)
	assert.Equal(t, expected, second, "formatter must be idempotent")
}
