package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func TestTwigComponentPHPAttributeCompletionUsesMemberScope(
	t *testing.T,
) {
	source := `<?php
namespace App\Twig\Components;

use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;

#[AsTwigComponent]
class Card
{
    #[Exp]
    public string $title;

    #[Pre]
    public function mount(): void {}
}
`
	property := twigComponentPHPAttributeCompletions(
		t,
		source,
		strings.Index(source, "Exp")+2,
	)
	require.Equal(t, []string{"ExposeInTemplate"}, completionLabels(property))
	method := twigComponentPHPAttributeCompletions(
		t,
		source,
		strings.Index(source, "Pre")+2,
	)
	require.ElementsMatch(t, []string{
		"ExposeInTemplate",
		"PostMount",
		"PreMount",
	}, completionLabels(method))

	var preMount protocol.CompletionItem
	for _, item := range method {
		if item.Label == "PreMount" {
			preMount = item
			break
		}
	}
	edit, ok := preMount.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	require.Equal(t, "PreMount", edit.NewText)
	require.Len(t, preMount.AdditionalTextEdits, 1)
	importEdit, ok := preMount.AdditionalTextEdits[0].(protocol.TextEdit)
	require.True(t, ok)
	require.Contains(t, importEdit.NewText, "use "+preMountAttribute+";")
}

func TestTwigLiveComponentPHPAttributeCompletion(t *testing.T) {
	source := `<?php
namespace App\Twig\Components;

use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;

#[AsLiveComponent]
class Search
{
    #[Li]
    public string $query = '';

    #[Li]
    public function save(#[Li] string $id): void {}
}
`
	propertyOffset := strings.Index(source, "#[Li]") + len("#[Li") - 1
	property := twigComponentPHPAttributeCompletions(
		t,
		source,
		propertyOffset,
	)
	require.Contains(t, completionLabels(property), "LiveProp")
	require.NotContains(t, completionLabels(property), "LiveAction")

	methodMarker := strings.Index(
		source[propertyOffset+1:],
		"#[Li]",
	) + propertyOffset + 1
	method := twigComponentPHPAttributeCompletions(
		t,
		source,
		methodMarker+len("#[Li")-1,
	)
	for _, expected := range []string{
		"LiveAction",
		"LiveListener",
		"PostHydrate",
		"PreDehydrate",
		"PreReRender",
	} {
		require.Contains(t, completionLabels(method), expected)
	}

	parameterMarker := strings.LastIndex(source, "#[Li]")
	parameter := twigComponentPHPAttributeCompletions(
		t,
		source,
		parameterMarker+len("#[Li")-1,
	)
	require.Equal(t, []string{"LiveArg"}, completionLabels(parameter))
}

func TestTwigComponentPHPAttributeCompletionHandlesIncompleteGroup(
	t *testing.T,
) {
	source := `<?php
namespace App\Twig\Components;
use Symfony\UX\TwigComponent\Attribute\AsTwigComponent;
#[AsTwigComponent]
class Card {
    #[Po
    public function mount(): void {}
}`
	items := twigComponentPHPAttributeCompletions(
		t,
		source,
		strings.Index(source, "#[Po")+len("#[Po"),
	)
	require.Contains(t, completionLabels(items), "PostMount")
}

func TestTwigComponentPHPAttributeCompletionRejectsOtherClasses(
	t *testing.T,
) {
	source := `<?php
class Ordinary {
    #[Exp]
    public string $title;
}`
	require.Empty(t, twigComponentPHPAttributeCompletions(
		t,
		source,
		strings.Index(source, "Exp")+2,
	))
}

func twigComponentPHPAttributeCompletions(
	t *testing.T,
	source string,
	offset int,
) []protocol.CompletionItem {
	t.Helper()
	document := lsp.NewTextDocument(
		"file:///project/src/Component.php",
		source,
		1,
	)
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	nodeOffset := offset
	if nodeOffset >= len(source) {
		nodeOffset = len(source) - 1
	}
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(nodeOffset))
	return NewTwigComponentPHPCompletionProvider().GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
}
