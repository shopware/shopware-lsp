package phpsemantic

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestProviderImplementationAndTypeHierarchyNavigation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	basePath := filepath.Join(root, "Base.php")
	childPath := filepath.Join(root, "Child.php")
	baseSource := `<?php
namespace App;
interface Contract { public function run(): void; }
trait Shared { public function helper(): void {} }
abstract class Base implements Contract {}
`
	childSource := `<?php
namespace App;
class Child extends Base {
    use Shared;
    public function run(): void {}
}
class GrandChild extends Child {
    public function run(): void {}
}
class_alias(Child::class, \Legacy\ChildAlias::class);
`
	require.NoError(t, os.WriteFile(basePath, []byte(baseSource), 0o600))
	require.NoError(t, os.WriteFile(childPath, []byte(childSource), 0o600))
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(basePath, []byte(baseSource))))
	require.NoError(t, idx.Index(indexer.NewParsedFile(childPath, []byte(childSource))))
	provider := New(idx)

	baseDocument := lsp.NewTextDocument(uriutil.FileURI(basePath), baseSource, 1)
	contractLocations := implementationLocationsAt(
		t,
		idx,
		provider,
		baseDocument,
		byteOffset(baseSource, "Contract"),
	)
	require.Len(t, contractLocations, 4)
	require.Equal(t, uriutil.FileURI(basePath), contractLocations[0].URI)

	methodLocations := implementationLocationsAt(
		t,
		idx,
		provider,
		baseDocument,
		byteOffset(baseSource, "run"),
	)
	require.Len(t, methodLocations, 2)
	for _, location := range methodLocations {
		require.Equal(t, uriutil.FileURI(childPath), location.URI)
	}

	traitLocations := implementationLocationsAt(
		t,
		idx,
		provider,
		baseDocument,
		byteOffset(baseSource, "Shared"),
	)
	require.Len(t, traitLocations, 1)
	require.Equal(t, uriutil.FileURI(childPath), traitLocations[0].URI)

	childDocument := lsp.NewTextDocument(uriutil.FileURI(childPath), childSource, 1)
	childOffset := byteOffset(childSource, "Child extends")
	childContext := syntaxContext(childDocument, childOffset)
	ctx := idx.AddDocumentContext(
		context.Background(),
		childPath,
		childDocument.Version,
		childContext.Node,
		childContext.Root,
	)
	params := &protocol.PrepareTypeHierarchyParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: childDocument.URI},
	}
	params.Position.Line, params.Position.Character = position(
		childDocument.LineIndex,
		childOffset,
	)
	items := provider.PrepareTypeHierarchy(ctx, &lsp.TypeHierarchyPrepareRequest{
		PrepareTypeHierarchyParams: params,
		SyntaxContext:              childContext,
	})
	require.Len(t, items, 1)
	require.Equal(t, "App\\Child", items[0].Detail)
	require.Equal(t, protocol.SymbolClass, items[0].Kind)

	supertypes := provider.TypeHierarchySupertypes(context.Background(), items[0])
	require.Len(t, supertypes, 1)
	require.Equal(t, "App\\Base", supertypes[0].Detail)
	subtypes := provider.TypeHierarchySubtypes(context.Background(), items[0])
	require.Len(t, subtypes, 1)
	require.Equal(t, "App\\GrandChild", subtypes[0].Detail)

	aliasOffset := byteOffset(childSource, "ChildAlias")
	aliasContext := syntaxContext(childDocument, aliasOffset)
	aliasCtx := idx.AddDocumentContext(
		context.Background(),
		childPath,
		childDocument.Version,
		aliasContext.Node,
		aliasContext.Root,
	)
	aliasParams := &protocol.PrepareTypeHierarchyParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: childDocument.URI},
	}
	aliasParams.Position.Line, aliasParams.Position.Character = position(
		childDocument.LineIndex,
		aliasOffset,
	)
	aliasItems := provider.PrepareTypeHierarchy(aliasCtx, &lsp.TypeHierarchyPrepareRequest{
		PrepareTypeHierarchyParams: aliasParams,
		SyntaxContext:              aliasContext,
	})
	require.Len(t, aliasItems, 1)
	require.Equal(t, "Legacy\\ChildAlias", aliasItems[0].Detail)
	aliasParents := provider.TypeHierarchySupertypes(context.Background(), aliasItems[0])
	require.Len(t, aliasParents, 1)
	require.Equal(t, "App\\Child", aliasParents[0].Detail)
}

func implementationLocationsAt(
	t *testing.T,
	idx *php.PHPIndex,
	provider *Provider,
	document *lsp.TextDocument,
	offset uint32,
) []protocol.Location {
	t.Helper()
	syntax := syntaxContext(document, offset)
	ctx := idx.AddDocumentContext(
		context.Background(),
		mustDocumentPath(t, document),
		document.Version,
		syntax.Node,
		syntax.Root,
	)
	params := &protocol.ImplementationParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: document.URI},
	}
	params.Position.Line, params.Position.Character = position(document.LineIndex, offset)
	return provider.GetImplementation(ctx, &lsp.ImplementationRequest{
		ImplementationParams: params,
		SyntaxContext:        syntax,
	})
}

func mustDocumentPath(t *testing.T, document *lsp.TextDocument) string {
	t.Helper()
	path, err := uriutil.Path(document.URI)
	require.NoError(t, err)
	return path
}
