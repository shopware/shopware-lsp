package twigfmt

import (
	"os"
	"path/filepath"
	"strings"
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

func assertFormatting(t *testing.T, input, expected string, options Options) {
	t.Helper()
	formatted, err := Format(input, options)
	require.NoError(t, err)
	assert.Equal(t, expected, formatted)

	second, err := Format(formatted, options)
	require.NoError(t, err)
	assert.Equal(t, expected, second, "formatter must be idempotent")
}
