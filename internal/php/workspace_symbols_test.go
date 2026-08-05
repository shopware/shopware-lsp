package php

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/require"
)

func TestPHPWorkspaceSymbolsUsePreparedSemanticGraph(t *testing.T) {
	idx, err := NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	defer func() { require.NoError(t, idx.Close()) }()

	path := filepath.Join(t.TempDir(), "SystemConfigService.php")
	file := indexer.NewParsedFile(path, []byte(`<?php
namespace App\Config;

final class SystemConfigService
{
    public const DOMAIN = 'core';
    private string $value;

    public function getValue(): string
    {
        return $this->value;
    }
}
`))
	prepared, err := idx.Prepare(file)
	require.NoError(t, err)
	symbols, err := idx.WorkspaceSymbols(file, prepared)
	require.NoError(t, err)

	byName := make(map[string]indexer.WorkspaceSymbol)
	for _, symbol := range symbols {
		byName[symbol.Name] = symbol
	}
	require.Equal(t, indexer.WorkspaceSymbolClass, byName["SystemConfigService"].Kind)
	require.Equal(t, indexer.WorkspaceSymbolPriorityPHPType, byName["SystemConfigService"].Priority)
	require.Contains(t, byName["SystemConfigService"].Aliases, `App\Config\SystemConfigService`)
	require.Equal(t, indexer.WorkspaceSymbolMethod, byName["getValue"].Kind)
	require.Equal(t, indexer.WorkspaceSymbolPriorityPHPMember, byName["getValue"].Priority)
	require.Equal(t, `App\Config\SystemConfigService`, byName["getValue"].ContainerName)
	require.Equal(t, 8, byName["getValue"].Range.Start.Line)
}
