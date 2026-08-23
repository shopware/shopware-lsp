package formatting

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestTwigProviderSelectsAdministrationAndStorefrontIndentation(t *testing.T) {
	t.Parallel()
	provider := NewTwigProvider()
	source := `{% block content %}<div>content</div>{% endblock %}`
	tests := []struct {
		name string
		uri  string
		want string
	}{
		{
			name: "administration",
			uri:  "file:///workspace/src/Resources/app/administration/src/page.html.twig",
			want: "{% block content %}\n<div>content</div>\n{% endblock %}",
		},
		{
			name: "storefront",
			uri:  "file:///workspace/src/Resources/views/storefront/page.html.twig",
			want: "{% block content %}\n  <div>content</div>\n{% endblock %}",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := lsp.NewTextDocument(test.uri, source, 1)
			formatted, handled, err := provider.FormatDocument(
				context.Background(),
				&lsp.DocumentFormattingRequest{
					DocumentFormattingParams: &protocol.DocumentFormattingParams{
						TextDocument: protocol.TextDocumentIdentifier{URI: test.uri},
						Options: protocol.FormattingOptions{
							TabSize: 2, InsertSpaces: true,
						},
					},
					Document: document,
				},
			)
			require.NoError(t, err)
			require.True(t, handled)
			require.Equal(t, test.want, formatted)
		})
	}
}

func TestTwigProviderIgnoresUnsupportedDocuments(t *testing.T) {
	t.Parallel()
	provider := NewTwigProvider()
	document := lsp.NewTextDocument("file:///workspace/file.php", "<?php", 1)
	formatted, handled, err := provider.FormatDocument(
		context.Background(),
		&lsp.DocumentFormattingRequest{
			DocumentFormattingParams: &protocol.DocumentFormattingParams{},
			Document:                 document,
		},
	)
	require.NoError(t, err)
	require.False(t, handled)
	require.Empty(t, formatted)
}
