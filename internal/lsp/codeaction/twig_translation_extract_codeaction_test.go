package codeaction

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/translation"
)

func TestTwigTranslationExtractCodeActionValidatesStaticHTMLText(t *testing.T) {
	provider := NewTwigTranslationExtractProvider(newTranslationExtractIndex(t))
	source := `<div title="Attribute text">Visible text</div>{{ dynamic }}`
	document := lsp.NewTextDocument(
		"file:///project/templates/page.html.twig",
		source,
		1,
	)

	for _, value := range []string{"Attribute text", "Visible text"} {
		rng := twigExtractRange(source, value, 0)
		actions := provider.GetCodeActions(
			context.Background(),
			&lsp.CodeActionRequest{
				CodeActionParams: &protocol.CodeActionParams{
					TextDocument: struct {
						URI string `json:"uri"`
					}{URI: document.URI},
					Range: rng,
				},
				SyntaxContext: lsp.SyntaxContext{
					Document:  document,
					Language:  document.SyntaxLanguage,
					Root:      document.SyntaxTree.Root,
					LineIndex: document.LineIndex,
				},
			},
		)
		require.Len(t, actions, 1)
		assert.Equal(t, protocol.CodeActionRefactorExtract, actions[0].Kind)
		require.NotNil(t, actions[0].Command)
		assert.Equal(t, extractTwigTranslationAction, actions[0].Command.Command)
	}

	dynamicRange := twigExtractRange(source, "dynamic", 0)
	assert.Empty(t, provider.GetCodeActions(
		context.Background(),
		&lsp.CodeActionRequest{
			CodeActionParams: &protocol.CodeActionParams{
				TextDocument: struct {
					URI string `json:"uri"`
				}{URI: document.URI},
				Range: dynamicRange,
			},
			SyntaxContext: lsp.SyntaxContext{
				Document:  document,
				Language:  document.SyntaxLanguage,
				Root:      document.SyntaxTree.Root,
				LineIndex: document.LineIndex,
			},
		},
	))
}

func TestTwigTranslationExtractionPrepareAndGenerate(t *testing.T) {
	index := newTranslationExtractIndex(t)
	provider := NewTwigTranslationExtractProvider(index)
	source := `{% trans_default_domain 'admin' %}
<p> Hello world </p>`
	request := twigTranslationExtractionRequest{
		FileURI: "file:///project/templates/page.html.twig",
		Source:  source,
		Range:   twigExtractRange(source, "Hello world", 0),
	}
	raw := marshalTwigExtractionRequest(t, request)
	preparedValue, err := provider.prepare(context.Background(), &raw)
	require.NoError(t, err)
	prepared := preparedValue.(twigTranslationExtractionPreparation)
	assert.Equal(t, "Hello world", prepared.Text)
	assert.Equal(t, "hello.world", prepared.DefaultKey)
	assert.Equal(t, "admin", prepared.DefaultDomain)
	assert.Contains(t, prepared.Domains, "admin")
	assert.Contains(t, prepared.Domains, "messages")

	request.Key = "welcome.title"
	request.Domain = "admin"
	raw = marshalTwigExtractionRequest(t, request)
	generatedValue, err := provider.generate(context.Background(), &raw)
	require.NoError(t, err)
	generated := generatedValue.(twigTranslationExtractionEdits)
	assert.Equal(t, "{{ 'welcome.title'|trans }}", generated.Replacement)
	require.Len(t, generated.Targets, 1)
	assert.Equal(t, "en", generated.Targets[0].Locale)
	assert.Contains(
		t,
		generated.Targets[0].NewText,
		"'welcome.title': 'Hello world'",
	)

	request.Key = "public.title"
	request.Domain = "messages"
	raw = marshalTwigExtractionRequest(t, request)
	generatedValue, err = provider.generate(context.Background(), &raw)
	require.NoError(t, err)
	generated = generatedValue.(twigTranslationExtractionEdits)
	assert.Equal(
		t,
		"{{ 'public.title'|trans({}, 'messages') }}",
		generated.Replacement,
	)
}

func TestTwigTranslationExtractionUsesWholeTextAtCaretAndRejectsDuplicates(
	t *testing.T,
) {
	index := newTranslationExtractIndex(t)
	provider := NewTwigTranslationExtractProvider(index)
	source := `<p>  Hello there  </p>`
	caret := strings.Index(source, "there") + 2
	lineIndex := cst.NewLineIndex(source)
	line, character := lineIndex.PositionUTF16(uint32(caret))
	request := twigTranslationExtractionRequest{
		FileURI: "file:///project/templates/page.html.twig",
		Source:  source,
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      int(line),
				Character: int(character),
			},
			End: protocol.Position{
				Line:      int(line),
				Character: int(character),
			},
		},
	}
	raw := marshalTwigExtractionRequest(t, request)
	preparedValue, err := provider.prepare(context.Background(), &raw)
	require.NoError(t, err)
	prepared := preparedValue.(twigTranslationExtractionPreparation)
	assert.Equal(t, "Hello there", prepared.Text)
	assert.Equal(
		t,
		twigExtractRange(source, "Hello there", 0),
		prepared.Range,
	)

	request.Key = "existing"
	request.Domain = "messages"
	raw = marshalTwigExtractionRequest(t, request)
	_, err = provider.generate(context.Background(), &raw)
	require.ErrorContains(t, err, "already exists")
}

func newTranslationExtractIndex(t *testing.T) *translation.Index {
	t.Helper()
	index, err := translation.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	root := t.TempDir()
	resources := map[string]string{
		"admin.en.yaml":    "existing: Existing\n",
		"messages.en.yaml": "existing: Existing\n",
	}
	for name, source := range resources {
		path := filepath.Join(root, "translations", name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		require.NoError(t, index.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	return index
}

func twigExtractRange(
	source, value string,
	occurrence int,
) protocol.Range {
	offset := -1
	remaining := source
	base := 0
	for index := 0; index <= occurrence; index++ {
		found := strings.Index(remaining, value)
		if found < 0 {
			return protocol.Range{}
		}
		offset = base + found
		base = offset + len(value)
		remaining = source[base:]
	}
	lineIndex := cst.NewLineIndex(source)
	startLine, startCharacter := lineIndex.PositionUTF16(uint32(offset))
	endLine, endCharacter := lineIndex.PositionUTF16(
		uint32(offset + len(value)),
	)
	return protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}

func marshalTwigExtractionRequest(
	t *testing.T,
	request twigTranslationExtractionRequest,
) json.RawMessage {
	t.Helper()
	value, err := json.Marshal(request)
	require.NoError(t, err)
	return value
}
