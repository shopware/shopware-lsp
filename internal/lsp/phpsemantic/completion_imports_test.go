package phpsemantic

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func TestPHPClassCompletionAddsAndReusesImports(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	declarations := `<?php
namespace Vendor\Package;
class Order {}
class Product {}
namespace App\Service;
class LocalThing {}
namespace Other;
class Order {}
`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		filepath.Join(root, "Declarations.php"),
		[]byte(declarations),
	)))

	t.Run("add import", func(t *testing.T) {
		source := `<?php
namespace App\Service;
use Existing\Dependency;
class Consumer { public function run(): void { Ord } }
`
		items, document := phpGlobalCompletions(t, idx, root, source, "Ord")
		order := completionForDetail(t, items, "Vendor\\Package\\Order")
		require.Equal(t, "Order", order.Label)
		edit, ok := order.TextEdit.(protocol.TextEdit)
		require.True(t, ok)
		require.Equal(t, "Order", edit.NewText)
		require.Equal(t, "Ord", completionEditSource(document, edit))
		require.Len(t, order.AdditionalTextEdits, 1)
		importEdit, ok := order.AdditionalTextEdits[0].(protocol.TextEdit)
		require.True(t, ok)
		require.Equal(t, "\nuse Vendor\\Package\\Order;", importEdit.NewText)
	})

	t.Run("reuse grouped alias", func(t *testing.T) {
		source := `<?php
namespace App\Service;
use Vendor\Package\{Order as PurchaseOrder, Product};
class Consumer { public function run(): void { Pur } }
`
		items, _ := phpGlobalCompletions(t, idx, root, source, "Pur")
		order := completionForDetail(t, items, "Vendor\\Package\\Order")
		edit, ok := order.TextEdit.(protocol.TextEdit)
		require.True(t, ok)
		require.Equal(t, "PurchaseOrder", edit.NewText)
		require.Empty(t, order.AdditionalTextEdits)
	})

	t.Run("avoid alias conflict", func(t *testing.T) {
		source := `<?php
namespace App\Service;
use Other\Order;
class Consumer { public function run(): void { Ord } }
`
		items, _ := phpGlobalCompletions(t, idx, root, source, "Ord")
		order := completionForDetail(t, items, "Vendor\\Package\\Order")
		edit, ok := order.TextEdit.(protocol.TextEdit)
		require.True(t, ok)
		require.Equal(t, "\\Vendor\\Package\\Order", edit.NewText)
		require.Empty(t, order.AdditionalTextEdits)
	})

	t.Run("current namespace", func(t *testing.T) {
		source := `<?php
namespace App\Service;
class Consumer { public function run(): void { Loc } }
`
		items, _ := phpGlobalCompletions(t, idx, root, source, "Loc")
		local := completionForDetail(t, items, "App\\Service\\LocalThing")
		edit, ok := local.TextEdit.(protocol.TextEdit)
		require.True(t, ok)
		require.Equal(t, "LocalThing", edit.NewText)
		require.Empty(t, local.AdditionalTextEdits)
	})

	t.Run("explicit qualification", func(t *testing.T) {
		source := `<?php
namespace App\Service;
class Consumer { public function run(): void { \Vendor\Pack } }
`
		items, document := phpGlobalCompletions(t, idx, root, source, `\Vendor\Pack`)
		order := completionForDetail(t, items, "Vendor\\Package\\Order")
		edit, ok := order.TextEdit.(protocol.TextEdit)
		require.True(t, ok)
		require.Equal(t, "\\Vendor\\Package\\Order", edit.NewText)
		require.Equal(t, `\Vendor\Pack`, completionEditSource(document, edit))
		require.Empty(t, order.AdditionalTextEdits)
	})
}

func TestPHPClassCompletionRanksDeterministically(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	idx, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	declarations := `<?php
namespace App\Service; class Product {}
namespace App\Domain; class Product {}
namespace Vendor\Package; class Product {}
namespace External\Package; /** @deprecated */ class Product {}
`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		filepath.Join(root, "Ranked.php"),
		[]byte(declarations),
	)))
	source := `<?php
namespace App\Service;
use Vendor\Package\Product as VendorProduct;
class Consumer { public function run(): void { Pro } }
`
	items, _ := phpGlobalCompletions(t, idx, root, source, "Pro")
	var ranked []protocol.CompletionItem
	for _, item := range items {
		if item.Label == "Product" {
			ranked = append(ranked, item)
		}
	}
	require.Len(t, ranked, 4)
	require.Contains(t, ranked[0].Detail, "App\\Service\\Product")
	require.Contains(t, ranked[1].Detail, "Vendor\\Package\\Product")
	require.Contains(t, ranked[2].Detail, "App\\Domain\\Product")
	require.Contains(t, ranked[3].Detail, "External\\Package\\Product")
	require.True(t, ranked[3].Deprecated)
	for index := 1; index < len(ranked); index++ {
		require.Less(t, ranked[index-1].SortText, ranked[index].SortText)
	}
}

func TestPHPCompletionPackageUsesComposerPSR4Ownership(t *testing.T) {
	t.Parallel()
	model := &project.Model{
		PSR4: map[string][]string{"App\\": {"src"}},
		Dependencies: []project.Package{{
			Name: "vendor/library",
			PSR4: map[string][]string{"Vendor\\Library\\": {"src"}},
		}},
	}
	require.True(t, samePHPCompletionPackage(
		model,
		"App\\Domain\\Product",
		"App\\Service\\Consumer",
	))
	require.False(t, samePHPCompletionPackage(
		model,
		"Vendor\\Library\\Product",
		"App\\Service\\Consumer",
	))
	require.True(t, samePHPCompletionPackage(
		model,
		"Vendor\\Library\\Product",
		"Vendor\\Library\\Factory",
	))
}

func phpGlobalCompletions(
	t *testing.T,
	idx *php.PHPIndex,
	root,
	source,
	needle string,
) ([]protocol.CompletionItem, *lsp.TextDocument) {
	t.Helper()
	path := filepath.Join(root, "Usage.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.LastIndex(source, needle) + len(needle))
	syntax := syntaxContext(document, offset)
	ctx := idx.AddDocumentContext(
		context.Background(),
		path,
		document.Version,
		syntax.Node,
		syntax.Root,
	)
	items := New(idx).GetCompletions(ctx, &lsp.CompletionRequest{
		CompletionParams: completionParams(document.URI, document.LineIndex, offset),
		SyntaxContext:    syntax,
	})
	return items, document
}

func completionForDetail(
	t *testing.T,
	items []protocol.CompletionItem,
	fullyQualified string,
) protocol.CompletionItem {
	t.Helper()
	for _, item := range items {
		if strings.Contains(item.Detail, fullyQualified) {
			return item
		}
	}
	t.Fatalf("missing completion for %s", fullyQualified)
	return protocol.CompletionItem{}
}

func completionEditSource(
	document *lsp.TextDocument,
	edit protocol.TextEdit,
) string {
	start := document.LineIndex.OffsetUTF16(
		uint32(edit.Range.Start.Line),
		uint32(edit.Range.Start.Character),
	)
	end := document.LineIndex.OffsetUTF16(
		uint32(edit.Range.End.Line),
		uint32(edit.Range.End.Character),
	)
	return string(document.Text[start:end])
}
