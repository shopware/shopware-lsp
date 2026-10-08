package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/completion"
	"github.com/shopware/shopware-lsp/internal/lsp/definition"
	"github.com/shopware/shopware-lsp/internal/lsp/hover"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceDoctrineDBALUsesShopwareSchema(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	root := t.TempDir()
	definitionPath := filepath.Join(root, "ProductDefinition.php")
	files := map[string]string{
		definitionPath: `<?php
class ProductDefinition extends EntityDefinition {
 public const ENTITY_NAME = 'product';
 protected function defineFields(): FieldCollection {
  return new FieldCollection([
   new StringField('product_name', 'name'),
   new ManyToOneAssociationField('category', 'category_id', CategoryDefinition::class),
  ]);
 }
}`,
		filepath.Join(root, "dbal.php"): `<?php
namespace Doctrine\DBAL;
class Connection { public function insert(string $table, array $data): void {} }
`,
		filepath.Join(root, "orm.php"): `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
#[ORM\Table(name: 'orm_only')]
class ORMEntity { #[ORM\Column] private string $ormField; }
`,
	}
	for path, source := range files {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	workspace, err := NewWorkspace(context.Background(), root, lsp.NewServer(nil, root, "test"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, workspace.Close()) })
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	phpIndex := workspacePHPIndex(t, workspace)
	dbalIndex := workspaceDoctrineIndex(t, workspace)
	source := `<?php
use Doctrine\DBAL\Connection;
function write(Connection $connection): void {
 $connection->insert('product', ['product_name' => 'value']);
}`
	document := lsp.NewTextDocument(uriutil.FileURI(filepath.Join(root, "usage.php")), source, 1)
	for _, needle := range []string{"product'", "product_name'"} {
		t.Run(needle, func(t *testing.T) {
			offset := uint32(strings.Index(source, needle) + 1)
			node := document.SyntaxTree.Root.NodeAtOffset(offset)
			ctx := phpIndex.AddDocumentContext(context.Background(), filepath.Join(root, "usage.php"), 1, node, document.SyntaxTree.Root)
			syntax := lsp.SyntaxContext{Document: document, Language: document.SyntaxLanguage, Root: document.SyntaxTree.Root, Node: node, LineIndex: document.LineIndex, DocumentContent: document.Text, DocumentTree: document.SyntaxTree}
			line, column := document.LineIndex.PositionUTF16(offset)
			completionParams := &protocol.CompletionParams{}
			completionParams.TextDocument.URI = document.URI
			completionParams.Position = protocol.Position{Line: int(line), Character: int(column)}
			definitionParams := &protocol.DefinitionParams{}
			definitionParams.TextDocument.URI = document.URI
			definitionParams.Position = completionParams.Position
			hoverParams := &protocol.HoverParams{}
			hoverParams.TextDocument.URI = document.URI
			hoverParams.Position = completionParams.Position
			items := completion.NewDoctrineCompletionProvider(dbalIndex, phpIndex).GetCompletions(ctx, &lsp.CompletionRequest{CompletionParams: completionParams, SyntaxContext: syntax})
			var labels []string
			for _, item := range items {
				labels = append(labels, item.Label)
			}
			if needle == "product'" {
				assert.Contains(t, labels, "product")
				assert.NotContains(t, labels, "orm_only")
			} else {
				assert.Contains(t, labels, "product_name")
				assert.NotContains(t, labels, "category_id")
			}
			locations := definition.NewDoctrineDefinitionProvider(dbalIndex, phpIndex).GetDefinition(ctx, &lsp.DefinitionRequest{DefinitionParams: definitionParams, SyntaxContext: syntax})
			require.Len(t, locations, 1)
			assert.Equal(t, uriutil.FileURI(definitionPath), locations[0].URI)
			result, err := hover.NewDoctrineHoverProvider(dbalIndex, phpIndex).GetHover(ctx, &lsp.HoverRequest{HoverParams: hoverParams, SyntaxContext: syntax})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Contains(t, result.Contents.Value, "ProductDefinition")
		})
	}
}
