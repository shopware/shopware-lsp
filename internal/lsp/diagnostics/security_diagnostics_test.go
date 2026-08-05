package diagnostics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/security"
)

func TestSecurityDiagnosticsReportUnknownAttributes(t *testing.T) {
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/config/packages/security.yaml",
		[]byte(`security:
  role_hierarchy:
    ROLE_EDITOR: [ROLE_USER]
`),
	)))

	source := []byte(`{{ is_granted('ROLE_EDITRO') }}
{{ is_granted('ROLE_USER') }}
{{ is_granted('PUBLIC_ACCESS') }}
`)
	diagnostics, err := NewSecurityAnalyzer(
		index,
		nil,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/templates/security.html.twig",
			source,
		),
	)
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	assert.Equal(t, missingSecurityAttributeCode, diagnostics[0].ID)
	assert.Contains(t, diagnostics[0].Message, "ROLE_EDITRO")
	assert.Contains(
		t,
		diagnostics[0].Payload.(map[string]any)["suggestions"],
		"ROLE_EDITOR",
	)
}

func TestSecurityDiagnosticsStayQuietWithoutProjectDeclarations(t *testing.T) {
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	source := []byte(`{{ is_granted('bundle.dynamic_permission') }}`)
	diagnostics, err := NewSecurityAnalyzer(
		index,
		nil,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/templates/security.html.twig",
			source,
		),
	)
	require.NoError(t, err)
	assert.Empty(t, diagnostics)
}

func TestSecurityDiagnosticsReportUnknownUserProviders(t *testing.T) {
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	source := []byte(`security:
  providers:
    app_users:
      memory: null
  firewalls:
    main:
      provider: app_usres
    api:
      provider: app_users
`)
	diagnostics, err := NewSecurityAnalyzer(
		index,
		nil,
	).Analyze(
		context.Background(),
		diagnosticsDocument(
			"file:///project/config/packages/security.yaml",
			source,
		),
	)
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	assert.Equal(t, missingSecurityProviderCode, diagnostics[0].ID)
	assert.Contains(t, diagnostics[0].Message, "app_usres")
	assert.Contains(
		t,
		diagnostics[0].Payload.(map[string]any)["suggestions"],
		"app_users",
	)
}

func TestSecurityDiagnosticsReportUnknownXMLAndPHPProviders(t *testing.T) {
	index, err := security.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })

	tests := []struct {
		uri    string
		source string
	}{
		{
			uri: "file:///project/config/packages/security.xml",
			source: `<?xml version="1.0"?>
<srv:container xmlns="http://symfony.com/schema/dic/security"
    xmlns:srv="http://symfony.com/schema/dic/services">
  <config>
    <provider name="app_users"><memory/></provider>
    <firewall name="main" provider="app_usres"/>
  </config>
</srv:container>
`,
		},
		{
			uri: "file:///project/config/packages/security.php",
			source: `<?php
use Symfony\Config\SecurityConfig;
return static function (SecurityConfig $security): void {
    $security->provider('app_users')->memory();
    $security->firewall('main')->provider('app_usres');
};
`,
		},
	}
	for _, test := range tests {
		diagnostics, diagnosticsErr := NewSecurityAnalyzer(
			index,
			nil,
		).Analyze(
			context.Background(),
			diagnosticsDocument(test.uri, []byte(test.source)),
		)
		require.NoError(t, diagnosticsErr, test.uri)
		require.Len(t, diagnostics, 1, test.uri)
		assert.Equal(
			t,
			missingSecurityProviderCode,
			diagnostics[0].ID,
			test.uri,
		)
		assert.Contains(t, diagnostics[0].Message, "app_usres", test.uri)
		assert.Contains(
			t,
			diagnostics[0].Payload.(map[string]any)["suggestions"],
			"app_users",
			test.uri,
		)
	}
}
