package color

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestAdminSCSSDocumentColors(t *testing.T) {
	source := `// #badbad
$short: #0f8;
$short-alpha: #0f8c;
$long: #112233;
$long-alpha: #11223380;
$quoted: "#abcdef";

#fff {
    color: rgb(255, 0, 128);
    border-color: rgb(100% 0% 50% / 25%);
    background: hsl(120deg, 100%, 25%);
    box-shadow: 0 0 rgba(0, 0, 0, 8%);
    outline-color: hsla(-120, 100%, 50%, .5);
    dynamic: rgb(from var(--color) r g b / 10%);
    invalid: #12;
}
`
	document := lsp.NewTextDocument("file:///workspace/component.scss", source, 1)
	colors, err := NewAdminSCSSColorProvider().GetDocumentColors(
		context.Background(),
		&lsp.DocumentColorRequest{
			DocumentColorParams: &protocol.DocumentColorParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
			},
			Document: document,
		},
	)
	require.NoError(t, err)
	require.Len(t, colors, 9)

	want := map[string]protocol.Color{
		"#0f8":                      {Red: 0, Green: 1, Blue: 136.0 / 255, Alpha: 1},
		"#0f8c":                     {Red: 0, Green: 1, Blue: 136.0 / 255, Alpha: 204.0 / 255},
		"#112233":                   {Red: 17.0 / 255, Green: 34.0 / 255, Blue: 51.0 / 255, Alpha: 1},
		"#11223380":                 {Red: 17.0 / 255, Green: 34.0 / 255, Blue: 51.0 / 255, Alpha: 128.0 / 255},
		"rgb(255, 0, 128)":          {Red: 1, Green: 0, Blue: 128.0 / 255, Alpha: 1},
		"rgb(100% 0% 50% / 25%)":    {Red: 1, Green: 0, Blue: .5, Alpha: .25},
		"hsl(120deg, 100%, 25%)":    {Red: 0, Green: .5, Blue: 0, Alpha: 1},
		"rgba(0, 0, 0, 8%)":         {Red: 0, Green: 0, Blue: 0, Alpha: .08},
		"hsla(-120, 100%, 50%, .5)": {Red: 0, Green: 0, Blue: 1, Alpha: .5},
	}
	for _, information := range colors {
		text := textAtProtocolRange(source, document, information.Range)
		expected, ok := want[text]
		require.Truef(t, ok, "unexpected color range %q", text)
		require.InDelta(t, expected.Red, information.Color.Red, 0.00001, text)
		require.InDelta(t, expected.Green, information.Color.Green, 0.00001, text)
		require.InDelta(t, expected.Blue, information.Color.Blue, 0.00001, text)
		require.InDelta(t, expected.Alpha, information.Color.Alpha, 0.00001, text)
		delete(want, text)
	}
	require.Empty(t, want)
}

func TestAdminSCSSColorRangesUseUTF16Positions(t *testing.T) {
	source := ".😀 { color: #abcdef; }"
	document := lsp.NewTextDocument("file:///workspace/unicode.scss", source, 1)
	colors, err := NewAdminSCSSColorProvider().GetDocumentColors(
		context.Background(),
		&lsp.DocumentColorRequest{Document: document},
	)
	require.NoError(t, err)
	require.Len(t, colors, 1)
	require.Equal(t, protocol.Position{Line: 0, Character: 13}, colors[0].Range.Start)
	require.Equal(t, "#abcdef", textAtProtocolRange(source, document, colors[0].Range))
}

func TestAdminSCSSColorPresentationsPreserveAlpha(t *testing.T) {
	document := lsp.NewTextDocument("file:///workspace/component.scss", ".x { color: #000; }", 1)
	replacementRange := protocol.Range{
		Start: protocol.Position{Line: 0, Character: 12},
		End:   protocol.Position{Line: 0, Character: 16},
	}
	presentations, err := NewAdminSCSSColorProvider().GetColorPresentations(
		context.Background(),
		&lsp.ColorPresentationRequest{
			ColorPresentationParams: &protocol.ColorPresentationParams{
				Color: protocol.Color{Red: 1, Green: 0, Blue: .5, Alpha: .25},
				Range: replacementRange,
			},
			Document: document,
		},
	)
	require.NoError(t, err)
	require.Len(t, presentations, 3)
	require.Equal(t, "#ff008040", presentations[0].Label)
	require.Equal(t, "rgba(255, 0, 128, 25%)", presentations[1].Label)
	require.Equal(t, "hsla(330, 100%, 50%, 25%)", presentations[2].Label)
	for _, presentation := range presentations {
		require.NotNil(t, presentation.TextEdit)
		require.Equal(t, replacementRange, presentation.TextEdit.Range)
		require.Equal(t, presentation.Label, presentation.TextEdit.NewText)

		parsed, ok := parsePresentationLabel(presentation.Label)
		require.True(t, ok, presentation.Label)
		require.InDelta(t, 1, parsed.Red, 0.002)
		require.InDelta(t, 0, parsed.Green, 0.002)
		require.InDelta(t, .5, parsed.Blue, 0.002)
		require.InDelta(t, .25, parsed.Alpha, 0.002)
	}
}

func parsePresentationLabel(label string) (protocol.Color, bool) {
	if strings.HasPrefix(label, "#") {
		return parseHexColor(strings.TrimPrefix(label, "#"))
	}
	return parseColorFunction(label)
}

func textAtProtocolRange(source string, document *lsp.TextDocument, value protocol.Range) string {
	start := document.LineIndex.OffsetUTF16(uint32(value.Start.Line), uint32(value.Start.Character))
	end := document.LineIndex.OffsetUTF16(uint32(value.End.Line), uint32(value.End.Character))
	return source[start:end]
}
