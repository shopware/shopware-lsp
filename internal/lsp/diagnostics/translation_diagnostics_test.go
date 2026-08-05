package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTwigTranslationDiagnostics(t *testing.T) {
	translationIndex := translationDiagnosticsIndex(t)
	provider := NewTranslationAnalyzer(translationIndex, nil)
	document := lsp.NewTextDocument(
		"file:///project/template.twig",
		`{{ 'hello.wrold'|trans }}
{{ 'admin.dashboard'|trans({}, 'admin') }}
{{ 'missing.key'|trans({}, 'admn') }}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)

	byCode := make(map[lsp.DiagnosticID]lsp.Problem)
	for _, diagnostic := range result {
		byCode[diagnostic.ID] = diagnostic
	}
	keyDiagnostic := byCode[missingTranslationKeyCode]
	assert.Contains(t, keyDiagnostic.Message, "hello.wrold")
	assert.Equal(t, "hello.wrold", problemRangeText(document, keyDiagnostic.Range))
	keyData := keyDiagnostic.Payload.(map[string]any)
	assert.Contains(t, keyData["suggestions"], "hello.world")
	assert.Equal(t, "messages", keyData["domain"])
	assert.Equal(t, "hello.wrold", keyData["key"])

	domainDiagnostic := byCode[missingTranslationDomainCode]
	assert.Contains(t, domainDiagnostic.Message, "admn")
	domainData := domainDiagnostic.Payload.(map[string]any)
	assert.Contains(t, domainData["suggestions"], "admin")
}

func TestPHPTranslationDiagnosticsUseReceiverType(t *testing.T) {
	translationIndex := translationDiagnosticsIndex(t)
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/TranslatorInterface.php",
		[]byte(`<?php
namespace Symfony\Contracts\Translation;
interface TranslatorInterface {
    public function trans(string $id, array $parameters = [], ?string $domain = null): string;
}
`),
	)))
	provider := NewTranslationAnalyzer(translationIndex, phpIndex)
	document := lsp.NewTextDocument(
		"file:///project/Controller.php",
		`<?php
namespace App;
use Symfony\Contracts\Translation\TranslatorInterface;
function translate(TranslatorInterface $translator, object $logger): void {
    $translator->trans('hello.world');
    $translator->trans('hello.wrold');
    $translator->trans('missing.key', domain: 'admn');
    $logger->trans('ignored');
}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 2)
	var codes []any
	for _, diagnostic := range result {
		codes = append(codes, diagnostic.ID)
		assert.NotContains(t, diagnostic.Message, "ignored")
	}
	assert.ElementsMatch(t, []any{
		missingTranslationKeyCode,
		missingTranslationDomainCode,
	}, codes)
}

func TestTranslationDiagnosticsValidatePHPDocAssistantReferences(
	t *testing.T,
) {
	translationIndex := translationDiagnosticsIndex(t)
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/TranslationAssistant.php",
		[]byte(`<?php
/**
 * @param string $key #TranslationKey
 * @param string $domain #TranslationDomain
 */
function resolve_translation(string $key, string $domain): void {}
/** @param string $key #TranslationKey */
function resolve_default_translation(string $key): void {}
`),
	)))
	provider := NewTranslationAnalyzer(translationIndex, phpIndex)
	document := lsp.NewTextDocument(
		"file:///project/Usage.php",
		`<?php
resolve_translation(key: 'admin.dashbord', domain: 'admin');
resolve_translation(domain: 'admn', key: 'admin.dashboard');
resolve_default_translation('hello.wrold');
resolve_default_translation('hello.world');
`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, result, 3)

	byText := make(map[string]lsp.Problem, len(result))
	for _, diagnostic := range result {
		byText[problemRangeText(document, diagnostic.Range)] = diagnostic
	}
	adminKey := byText["admin.dashbord"]
	assert.Equal(t, missingTranslationKeyCode, adminKey.ID)
	assert.Equal(t, "admin", adminKey.Payload.(map[string]any)["domain"])
	assert.Contains(
		t,
		problemSuggestionStrings(adminKey),
		"admin.dashboard",
	)
	domain := byText["admn"]
	assert.Equal(t, missingTranslationDomainCode, domain.ID)
	assert.Contains(t, problemSuggestionStrings(domain), "admin")
	defaultKey := byText["hello.wrold"]
	assert.Equal(t, missingTranslationKeyCode, defaultKey.ID)
	assert.Equal(t, "messages", defaultKey.Payload.(map[string]any)["domain"])
	assert.Contains(
		t,
		problemSuggestionStrings(defaultKey),
		"hello.world",
	)
}

func TestSnippetDiagnosticsAcceptSymfonyMessageCatalogueKey(t *testing.T) {
	translationIndex := translationDiagnosticsIndex(t)
	snippetIndex, err := snippet.NewSnippetIndexer(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snippetIndex.Close()) })
	provider := NewSnippetAnalyzer(
		snippetIndex,
		translationIndex,
	)
	document := lsp.NewTextDocument(
		"file:///project/template.twig",
		`{{ 'hello.world'|trans }}`,
		1,
	)
	result, err := provider.Analyze(context.Background(), document)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func translationDiagnosticsIndex(t *testing.T) *translation.Index {
	t.Helper()
	idx, err := translation.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	root := t.TempDir()
	resources := map[string]string{
		"messages.en.yaml": "hello.world: Hello world\n",
		"admin.en.yaml":    "admin.dashboard: Dashboard\n",
	}
	for filename, source := range resources {
		path := filepath.Join(root, "translations", filename)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
		require.NoError(t, idx.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	return idx
}
