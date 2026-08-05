package reference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctrineReferencesConnectDQLWithEntityAndProperty(t *testing.T) {
	root := t.TempDir()
	entityPath := filepath.Join(root, "src", "Entity", "Product.php")
	firstPath := filepath.Join(root, "src", "FirstSearch.php")
	secondPath := filepath.Join(root, "src", "SecondSearch.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(entityPath), 0o755))
	entitySource := `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
class Product {
    #[ORM\Column(type: 'string')]
    private string $name;
}
`
	firstSource := `<?php
$dql = 'SELECT p FROM App\Entity\Product p WHERE p.name = :name';
`
	secondSource := `<?php
$dql = 'SELECT p FROM App\Entity\Product p WHERE p.name = :other';
`
	sources := map[string]string{
		entityPath: entitySource,
		firstPath:  firstSource,
		secondPath: secondSource,
	}
	for path, source := range sources {
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	for _, path := range []string{entityPath, firstPath, secondPath} {
		parsed := indexer.NewParsedFile(path, []byte(sources[path]))
		require.NoError(t, phpIndex.Index(parsed))
		require.NoError(t, doctrineIndex.Index(parsed))
	}
	provider := NewDoctrineReferenceProvider(doctrineIndex, phpIndex)

	for _, test := range []struct {
		path      string
		source    string
		needle    string
		locations int
		uris      []string
	}{
		{
			path:      firstPath,
			source:    firstSource,
			needle:    "App\\Entity\\Product",
			locations: 3,
			uris: []string{
				uriutil.FileURI(entityPath),
				uriutil.FileURI(firstPath),
				uriutil.FileURI(secondPath),
			},
		},
		{
			path:      firstPath,
			source:    firstSource,
			needle:    "p.name",
			locations: 3,
			uris: []string{
				uriutil.FileURI(entityPath),
				uriutil.FileURI(firstPath),
				uriutil.FileURI(secondPath),
			},
		},
		{
			path:      entityPath,
			source:    entitySource,
			needle:    "class Product",
			locations: 2,
			uris: []string{
				uriutil.FileURI(firstPath),
				uriutil.FileURI(secondPath),
			},
		},
	} {
		document := lsp.NewTextDocument(
			uriutil.FileURI(test.path),
			test.source,
			1,
		)
		offset := strings.Index(test.source, test.needle)
		require.NotEqual(t, -1, offset)
		if test.needle == "class Product" {
			offset += len("class ") + 2
		} else {
			offset += len(test.needle) - 1
		}
		node := document.SyntaxTree.Root.NodeAtOffset(uint32(offset))
		line, character := document.LineIndex.PositionUTF16(uint32(offset))
		params := &protocol.ReferenceParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		params.Context.IncludeDeclaration = true
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			test.path,
			1,
			node,
			document.SyntaxTree.Root,
		)
		locations, referenceErr := provider.GetReferences(
			ctx,
			&lsp.ReferenceRequest{
				ReferenceParams: params,
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
		require.NoError(t, referenceErr)
		require.Len(t, locations, test.locations)
		var uris []string
		for _, location := range locations {
			uris = append(uris, location.URI)
		}
		assert.ElementsMatch(t, test.uris, uris)
	}
}

func TestDoctrineReferencesConnectConfiguredTypeAcrossMappings(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(root, "vendor", "Doctrine", "Type.php")
	typePath := filepath.Join(root, "src", "Doctrine", "MoneyType.php")
	entityPath := filepath.Join(root, "src", "Entity", "Product.php")
	mappingPath := filepath.Join(
		root,
		"config",
		"doctrine",
		"External.orm.yaml",
	)
	configPath := filepath.Join(
		root,
		"config",
		"packages",
		"doctrine.php",
	)
	sources := map[string]string{
		basePath: `<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}
`,
		typePath: `<?php
namespace App\Doctrine;
class MoneyType extends \Doctrine\DBAL\Types\Type {}
`,
		entityPath: `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
class Product {
    #[ORM\Column(type: 'configured_currency')]
    private string $amount;
}
`,
		mappingPath: `App\Entity\External:
  type: entity
  fields:
    amount:
      type: configured_currency
`,
		configPath: `<?php
use App\Doctrine\MoneyType;
use Doctrine\DBAL\Types\Type;

Type::addType('configured_currency', MoneyType::class);
`,
	}
	for path, source := range sources {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}

	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	for path, source := range sources {
		parsed := indexer.NewParsedFile(path, []byte(source))
		if filepath.Ext(path) == ".php" {
			require.NoError(t, phpIndex.Index(parsed))
		}
		require.NoError(t, doctrineIndex.Index(parsed))
	}

	for _, targetPath := range []string{configPath, mappingPath} {
		document := lsp.NewTextDocument(
			uriutil.FileURI(targetPath),
			sources[targetPath],
			2,
		)
		offset := uint32(
			strings.Index(sources[targetPath], "configured_currency") + 2,
		)
		line, character := document.LineIndex.PositionUTF16(offset)
		params := &protocol.ReferenceParams{}
		params.TextDocument.URI = document.URI
		params.Position.Line = int(line)
		params.Position.Character = int(character)
		params.Context.IncludeDeclaration = true
		locations, referenceErr := NewDoctrineReferenceProvider(
			doctrineIndex,
			phpIndex,
		).GetReferences(
			context.Background(),
			&lsp.ReferenceRequest{
				ReferenceParams: params,
				SyntaxContext: lsp.SyntaxContext{
					Document:        document,
					Language:        document.SyntaxLanguage,
					DocumentContent: document.Text,
					DocumentTree:    document.SyntaxTree,
					LineIndex:       document.LineIndex,
					Root:            document.SyntaxTree.Root,
					Node: document.SyntaxTree.Root.NodeAtOffset(
						offset,
					),
				},
			},
		)
		require.NoError(t, referenceErr)
		require.Len(t, locations, 4)
		var uris []string
		for _, location := range locations {
			uris = append(uris, location.URI)
		}
		require.ElementsMatch(t, []string{
			uriutil.FileURI(typePath),
			uriutil.FileURI(configPath),
			uriutil.FileURI(entityPath),
			uriutil.FileURI(mappingPath),
		}, uris)
	}
}

func TestDoctrineReferencesIncludeTableConstraintFields(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "Product.php")
	source := `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
#[ORM\Index(fields: ['name'])]
class Product {
    #[ORM\Column]
    private string $name;
}`
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, doctrineIndex.Index(parsed))

	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.LastIndex(source, "$name") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		path,
		1,
		node,
		document.SyntaxTree.Root,
	)
	locations, err := NewDoctrineReferenceProvider(
		doctrineIndex,
		phpIndex,
	).GetReferences(
		ctx,
		&lsp.ReferenceRequest{
			ReferenceParams: params,
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
	require.NoError(t, err)
	require.Len(t, locations, 1)
	start, _ := document.LineIndex.PositionUTF16(
		uint32(strings.Index(source, "'name']") + 1),
	)
	require.Equal(t, int(start), locations[0].Range.Start.Line)
}

func TestDoctrineReferencesResolvePHPDiscriminatorClass(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "Models.php")
	source := `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
#[ORM\DiscriminatorMap(['child' => ChildModel::class])]
class BaseModel {}
#[ORM\Entity]
class ChildModel extends BaseModel {}
`
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	require.NoError(t, doctrineIndex.Index(parsed))

	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	offset := uint32(strings.Index(source, "ChildModel::class") + 2)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = true
	locations, err := NewDoctrineReferenceProvider(
		doctrineIndex,
		phpIndex,
	).GetReferences(
		context.Background(),
		&lsp.ReferenceRequest{
			ReferenceParams: params,
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
	require.NoError(t, err)
	require.Len(t, locations, 1)
	require.Equal(t, uriutil.FileURI(path), locations[0].URI)
}
