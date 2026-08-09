//go:build integration

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/stretchr/testify/require"
)

func realWorldDocumentLinkTargets(
	links []protocol.DocumentLink,
) []string {
	result := make([]string, 0, len(links))
	for _, link := range links {
		result = append(result, link.Target)
	}
	return result
}

func realWorldProjectRoot(t *testing.T) string {
	t.Helper()
	if configured := os.Getenv("SHOPWARE_LSP_REAL_WORLD_ROOT"); configured != "" {
		root, err := filepath.Abs(configured)
		require.NoError(t, err)
		require.DirExists(t, root)
		return root
	}
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	root := filepath.Join(home, "Developer", "sw-trunk")
	if _, err := os.Stat(root); err != nil {
		t.Skipf(
			"real-world checkout not found at %s; set SHOPWARE_LSP_REAL_WORLD_ROOT",
			root,
		)
	}
	return root
}

func openRealWorldWorkspace(
	t *testing.T,
	ctx context.Context,
	root string,
) (*Workspace, *php.PHPIndex) {
	t.Helper()
	workspace, phpIndex, _ := openRealWorldWorkspaceWithServer(
		t, ctx, root,
	)
	return workspace, phpIndex
}

func openRealWorldWorkspaceWithServer(
	t *testing.T,
	ctx context.Context,
	root string,
) (*Workspace, *php.PHPIndex, *lsp.Server) {
	t.Helper()
	server := lsp.NewServer(nil, root, "integration-test")
	workspace, err := NewWorkspace(ctx, root, server)
	require.NoError(t, err)
	for _, idx := range workspace.indexers {
		if phpIndex, ok := idx.(*php.PHPIndex); ok {
			return workspace, phpIndex, server
		}
	}
	require.NoError(t, workspace.Close())
	t.Fatal("PHP index is not registered")
	return nil, nil, nil
}

func formatBytes(value uint64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case value >= gib:
		return formatFloat(float64(value)/gib) + " GiB"
	case value >= mib:
		return formatFloat(float64(value)/mib) + " MiB"
	case value >= kib:
		return formatFloat(float64(value)/kib) + " KiB"
	default:
		return formatFloat(float64(value)) + " B"
	}
}

func requireTwigGlobal(
	t *testing.T,
	globals []twig.Global,
	name,
	typeName string,
) {
	t.Helper()
	for _, global := range globals {
		if global.Name != name {
			continue
		}
		if typeName == "" || global.Type == typeName {
			return
		}
	}
	require.Failf(
		t,
		"missing Twig global",
		"name=%s type=%s globals=%v",
		name,
		typeName,
		globals,
	)
}

func requireTwigTag(
	t *testing.T,
	tags []twig.TwigTag,
	name string,
) {
	t.Helper()
	for _, tag := range tags {
		if tag.Name == name {
			return
		}
	}
	t.Fatalf("Twig tag %q not found in %#v", name, tags)
}

func requireTwigOperator(
	t *testing.T,
	operators []twig.TwigOperator,
	name,
	pathSuffix string,
) {
	t.Helper()
	for _, operator := range operators {
		if operator.Name == name &&
			strings.HasSuffix(
				filepath.ToSlash(operator.FilePath),
				filepath.ToSlash(pathSuffix),
			) {
			return
		}
	}
	t.Fatalf(
		"Twig operator %q from %q not found in %#v",
		name,
		pathSuffix,
		operators,
	)
}

func requireTemplateReferencePath(
	t *testing.T,
	references []twig.TemplateReference,
	suffix string,
) {
	t.Helper()
	suffix = filepath.Clean(suffix)
	for _, reference := range references {
		if strings.HasSuffix(filepath.Clean(reference.FilePath), suffix) {
			return
		}
	}
	t.Fatalf("template reference path ending in %q not found", suffix)
}

func formatFloat(value float64) string {
	return fmt.Sprintf("%.1f", value)
}

func requireTwigVariable(
	t *testing.T,
	variables []php.TwigTemplateVariable,
	name string,
) {
	t.Helper()
	for _, variable := range variables {
		if variable.Name == name {
			return
		}
	}
	t.Fatalf("Twig variable %q not found in %#v", name, variables)
}

func requireFormGeneratorCandidate(
	t *testing.T,
	candidates []realWorldFormCandidate,
	name,
	suggestedType string,
) {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.Name == name {
			require.Equal(t, suggestedType, candidate.SuggestedType)
			return
		}
	}
	t.Fatalf("form generator candidate %q not found in %#v", name, candidates)
}

func requireTwigTemplateInput(
	t *testing.T,
	variables []twig.TemplateVariable,
	name string,
) {
	t.Helper()
	for _, variable := range variables {
		if variable.Name == name {
			return
		}
	}
	t.Fatalf("Twig template input %q not found in %#v", name, variables)
}

func requireTwigBlock(
	t *testing.T,
	blocks []twig.TemplateBlock,
	name string,
) {
	t.Helper()
	for _, block := range blocks {
		if block.Name == name {
			require.NotZero(t, block.Range.Len())
			require.FileExists(t, block.FilePath)
			return
		}
	}
	t.Fatalf("Twig block %q not found in %#v", name, blocks)
}

func requireRoute(t *testing.T, routes []symfony.Route, name string) {
	t.Helper()
	for _, route := range routes {
		if route.Name == name {
			return
		}
	}
	t.Fatalf("Symfony route %q not found in %#v", name, routes)
}
