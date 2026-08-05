package hover

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/environment"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentHoverShowsProcessorsAndRedactsSecrets(t *testing.T) {
	root := t.TempDir()
	idx, err := environment.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		filepath.Join(root, ".env.local"),
		[]byte("APP_SECRET=do-not-display"),
	)))
	source := "secret: '%env(resolve:APP_SECRET)%'"
	path := filepath.Join(root, "config", "services.yaml")
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "APP_SECRET") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewEnvironmentHoverProvider(root, idx).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				DocumentContent: document.Text,
				LineIndex:       document.LineIndex,
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "APP_SECRET")
	assert.Contains(t, result.Contents.Value, "resolve")
	assert.Contains(t, result.Contents.Value, ".env.local")
	assert.Contains(t, result.Contents.Value, "••••••••")
	assert.NotContains(t, result.Contents.Value, "do-not-display")
}

func TestEnvironmentDisplayValueRedactsCredentialBearingValues(t *testing.T) {
	tests := []struct {
		name, value string
	}{
		{"DATABASE_URL", "mysql://user:password@localhost/app"},
		{"MAILER_DSN", "smtp://localhost"},
		{"ADMIN_AUTH", "admin:password"},
		{"AWS_ACCESS_KEY_ID", "access-key"},
		{"SERVICE_URL", "https://token@example.com"},
	}
	for _, test := range tests {
		assert.Equal(
			t,
			"••••••••",
			environmentDisplayValue(test.name, test.value),
		)
	}
	assert.Equal(
		t,
		"https://example.com",
		environmentDisplayValue("SERVICE_URL", "https://example.com"),
	)
}

func TestEnvironmentHoverSupportsAutowireEnvProcessors(t *testing.T) {
	root := t.TempDir()
	idx, err := environment.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		filepath.Join(root, ".env"),
		[]byte("APP_ENV=dev"),
	)))
	source := "<?php #[Autowire(env: 'resolve:int:APP_ENV')] class Config {}"
	path := filepath.Join(root, "src", "Config.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "APP_ENV") + 2)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	result, err := NewEnvironmentHoverProvider(root, idx).GetHover(
		context.Background(),
		&lsp.HoverRequest{
			HoverParams: params,
			SyntaxContext: lsp.SyntaxContext{
				DocumentContent: document.Text,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node: document.SyntaxTree.Root.NodeAtOffset(
					offset,
				),
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Contents.Value, "resolve → int")
}
