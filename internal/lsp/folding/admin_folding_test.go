package folding

import (
	"context"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminJavaScriptFoldingCoversImportsComponentSectionsAndComments(
	t *testing.T,
) {
	source := `import template from './sw-card.html.twig';
import './sw-card.scss';

Component.register('sw-card', {
    props: {
        title: String,
    },
    methods: {
        save() {
            const options = {
                force: true,
            };
        },
    },
});
/* component
 * details */`
	ranges := adminFoldingForSource(t, "/project/sw-card/index.ts", source)
	assertAdminFoldingRange(
		t, ranges, 0, 1, protocol.FoldingRangeKindImports,
	)
	assertAdminFoldingRange(t, ranges, 3, 13, "")
	assertAdminFoldingRange(t, ranges, 4, 5, "")
	assertAdminFoldingRange(t, ranges, 7, 12, "")
	assertAdminFoldingRange(t, ranges, 8, 11, "")
	assertAdminFoldingRange(t, ranges, 9, 10, "")
	assertAdminFoldingRange(
		t, ranges, 15, 16, protocol.FoldingRangeKindComment,
	)
}

func TestAdminTwigFoldingCoversBlocksMarkupAndSelfClosingAttributes(
	t *testing.T,
) {
	source := `{# component
   docs #}
{% block sw_card %}
<sw-card
    :title="title"
>
    <div>
        Content
    </div>
</sw-card>
{% endblock %}
<sw-button
    :disabled="true"
/>`
	ranges := adminFoldingForSource(
		t, "/project/sw-card/sw-card.html.twig", source,
	)
	assertAdminFoldingRange(
		t, ranges, 0, 1, protocol.FoldingRangeKindComment,
	)
	assertAdminFoldingRange(t, ranges, 2, 9, "")
	assertAdminFoldingRange(t, ranges, 3, 8, "")
	assertAdminFoldingRange(t, ranges, 6, 7, "")
	assertAdminFoldingRange(t, ranges, 11, 13, "")
}

func TestAdminFoldingUsesTheOpenDocumentSnapshot(t *testing.T) {
	provider := NewAdminFoldingProvider()
	document := lsp.NewTextDocument(
		uriutil.FileURI("/project/live.html.twig"),
		"<sw-card>\n  live\n</sw-card>", 7,
	)
	ranges, err := provider.GetFoldingRanges(
		context.Background(),
		&lsp.FoldingRangeRequest{
			FoldingRangeParams: &protocol.FoldingRangeParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
			},
			Document: document,
		},
	)
	require.NoError(t, err)
	require.Len(t, ranges, 1)
	assert.Equal(t, protocol.FoldingRange{StartLine: 0, EndLine: 1}, ranges[0])
}

func adminFoldingForSource(
	t *testing.T,
	path,
	source string,
) []protocol.FoldingRange {
	t.Helper()
	provider := NewAdminFoldingProvider()
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	ranges, err := provider.GetFoldingRanges(
		context.Background(),
		&lsp.FoldingRangeRequest{
			FoldingRangeParams: &protocol.FoldingRangeParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
			},
			Document: document,
		},
	)
	require.NoError(t, err)
	return ranges
}

func assertAdminFoldingRange(
	t *testing.T,
	ranges []protocol.FoldingRange,
	startLine,
	endLine int,
	kind string,
) {
	t.Helper()
	for _, rangeValue := range ranges {
		if rangeValue.StartLine == startLine && rangeValue.EndLine == endLine &&
			rangeValue.Kind == kind {
			return
		}
	}
	assert.Failf(
		t,
		"missing folding range",
		"expected %d..%d kind %q in %#v",
		startLine,
		endLine,
		kind,
		ranges,
	)
}
