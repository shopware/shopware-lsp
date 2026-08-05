package codeaction

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctrineClassConstantCodeActionConvertsRepositoryEntity(t *testing.T) {
	fixture := newDoctrineClassConstantFixture(t)
	source := `<?php
namespace App\Service;
use Doctrine\Persistence\ManagerRegistry;
function load(ManagerRegistry $registry): void
{
    $registry->getRepository('Product');
}`
	request := doctrineClassConstantRequest(source, "Product")
	actions := fixture.provider.GetCodeActions(
		context.Background(),
		request,
	)

	require.Len(t, actions, 1)
	assert.Equal(t, "Doctrine: use class constant", actions[0].Title)
	edits := actions[0].Edit.Changes[request.TextDocument.URI]
	require.Len(t, edits, 2)
	assert.Equal(t, "Product::class", edits[0].NewText)
	assert.Contains(
		t,
		edits[1].NewText,
		"use App\\Entity\\Product;",
	)
}

func TestDoctrineClassConstantCodeActionSupportsObjectManagerFind(t *testing.T) {
	fixture := newDoctrineClassConstantFixture(t)
	source := `<?php
namespace App\Service;
use App\Entity\Product as CatalogProduct;
use Doctrine\Persistence\ObjectManager;
function load(ObjectManager $manager): void
{
    $manager->find('App\Entity\Product', 1);
}`
	request := doctrineClassConstantRequest(source, "App\\Entity\\Product")
	actions := fixture.provider.GetCodeActions(
		context.Background(),
		request,
	)

	require.Len(t, actions, 1)
	edits := actions[0].Edit.Changes[request.TextDocument.URI]
	require.Len(t, edits, 1)
	assert.Equal(t, "CatalogProduct::class", edits[0].NewText)
}

func TestDoctrineClassConstantCodeActionAvoidsImportConflict(t *testing.T) {
	fixture := newDoctrineClassConstantFixture(t)
	source := `<?php
namespace App\Service;
use App\DTO\Product;
use Doctrine\Persistence\ManagerRegistry;
function load(ManagerRegistry $registry): void
{
    $registry->getRepository('Product');
}`
	request := doctrineClassConstantRequest(source, "Product")
	actions := fixture.provider.GetCodeActions(
		context.Background(),
		request,
	)

	require.Len(t, actions, 1)
	edits := actions[0].Edit.Changes[request.TextDocument.URI]
	require.Len(t, edits, 1)
	assert.Equal(t, `\App\Entity\Product::class`, edits[0].NewText)
}

func TestDoctrineClassConstantCodeActionRejectsUntypedAndConstantCalls(
	t *testing.T,
) {
	fixture := newDoctrineClassConstantFixture(t)
	for _, source := range []string{
		`<?php
class Registry {
    public function getRepository(string $class): void {}
}
function load(Registry $registry): void {
    $registry->getRepository('Product');
}`,
		`<?php
use App\Entity\Product;
use Doctrine\Persistence\ManagerRegistry;
function load(ManagerRegistry $registry): void {
    $registry->getRepository(Product::class);
}`,
	} {
		request := doctrineClassConstantRequest(source, "Product")
		actions := fixture.provider.GetCodeActions(
			context.Background(),
			request,
		)
		assert.Empty(t, actions)
	}
}

func TestDoctrineClassConstantCodeActionRejectsAmbiguousShortcut(t *testing.T) {
	fixture := newDoctrineClassConstantFixture(t)
	other := indexer.NewParsedFile(
		"/project/src/Other/Entity/Product.php",
		[]byte(`<?php
namespace Other\Entity;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
class Product {}
`),
	)
	require.NoError(t, fixture.phpIndex.Index(other))
	require.NoError(t, fixture.doctrineIndex.Index(other))
	source := `<?php
use Doctrine\Persistence\ManagerRegistry;
function load(ManagerRegistry $registry): void {
    $registry->getRepository('Product');
}`
	request := doctrineClassConstantRequest(source, "Product")

	assert.Empty(t, fixture.provider.GetCodeActions(
		context.Background(),
		request,
	))
}

type doctrineClassConstantFixture struct {
	provider      *DoctrineClassConstantCodeActionProvider
	doctrineIndex *doctrine.Index
	phpIndex      *php.PHPIndex
}

func newDoctrineClassConstantFixture(
	t *testing.T,
) doctrineClassConstantFixture {
	t.Helper()
	doctrineIndex, err := doctrine.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, doctrineIndex.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	stubs := indexer.NewParsedFile(
		"/vendor/DoctrineStubs.php",
		[]byte(`<?php
namespace Doctrine\Persistence;
interface ObjectRepository {}
interface ManagerRegistry {
    public function getRepository(string $class): ObjectRepository;
}
interface ObjectManager {
    public function getRepository(string $class): ObjectRepository;
    public function find(string $class, mixed $id): ?object;
}
namespace Doctrine\Common\Persistence;
interface ManagerRegistry {
    public function getRepository(string $class);
}
interface ObjectManager {
    public function getRepository(string $class);
    public function find(string $class, $id);
}
`),
	)
	entity := indexer.NewParsedFile(
		"/project/src/Entity/Product.php",
		[]byte(`<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity]
class Product {}
`),
	)
	require.NoError(t, phpIndex.Index(stubs))
	require.NoError(t, phpIndex.Index(entity))
	require.NoError(t, doctrineIndex.Index(entity))
	return doctrineClassConstantFixture{
		provider: NewDoctrineClassConstantCodeActionProvider(
			doctrineIndex,
			phpIndex,
		),
		doctrineIndex: doctrineIndex,
		phpIndex:      phpIndex,
	}
}

func doctrineClassConstantRequest(
	source,
	needle string,
) *lsp.CodeActionRequest {
	request := commandInvokeParameterRequest(source, needle)
	offset := strings.LastIndex(source, needle)
	request.Node = request.Root.NodeAtOffset(uint32(offset))
	line, character := request.Document.LineIndex.PositionUTF16(
		uint32(offset),
	)
	request.Range.Start.Line = int(line)
	request.Range.Start.Character = int(character)
	request.Range.End.Line = int(line)
	request.Range.End.Character = int(character) + len(needle)
	return request
}
