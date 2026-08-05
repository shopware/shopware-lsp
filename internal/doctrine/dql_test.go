package doctrine

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStandaloneDQLTracksEntitiesFieldsAndRelations(t *testing.T) {
	idx, _ := queryBuilderFixture(t)
	source := `<?php
$dql = "SELECT p, c.title FROM App\\Entity\\Product p LEFT JOIN p.category c WHERE p.name = :name AND c.title = :title";
	`
	path := "/project/src/Search.php"
	root := phpparser.Parse(source).Tree.Root

	for _, test := range []struct {
		needle string
		labels []string
	}{
		{
			needle: "p.name",
			labels: []string{"p.id", "p.name", "p.category"},
		},
		{
			needle: "c.title",
			labels: []string{"c.title"},
		},
		{
			needle: "FROM App",
			labels: []string{
				"App\\Entity\\Product",
				"App\\Entity\\Category",
			},
		},
	} {
		offset := uint32(strings.Index(source, test.needle) + len(test.needle))
		node := root.NodeAtOffset(offset)
		completions := idx.QueryCompletionsAt(
			context.Background(),
			root,
			node,
			offset,
		)
		var labels []string
		for _, completion := range completions {
			labels = append(labels, completion.Label)
		}
		for _, label := range test.labels {
			assert.Contains(t, labels, label)
		}
	}

	for _, test := range []struct {
		needle string
		entity string
		field  string
	}{
		{
			needle: "p.name",
			entity: "App\\Entity\\Product",
			field:  "name",
		},
		{
			needle: "c.title",
			entity: "App\\Entity\\Category",
			field:  "title",
		},
	} {
		offset := uint32(strings.LastIndex(source, test.needle) + len(test.needle))
		node := root.NodeAtOffset(offset)
		reference, found := idx.QueryFieldReferenceAt(
			context.Background(),
			root,
			node,
			offset,
		)
		require.True(t, found)
		assert.Equal(t, test.entity, reference.Entity)
		assert.Equal(t, test.field, reference.Field)
	}

	entityOffset := uint32(strings.Index(source, `App\\Entity\\Product`) + 4)
	entityNode := root.NodeAtOffset(entityOffset)
	entity, found := idx.QueryEntityReferenceAt(
		context.Background(),
		root,
		entityNode,
		entityOffset,
	)
	require.True(t, found)
	assert.Equal(t, "App\\Entity\\Product", entity.Entity)

	references := DQLReferencesInDocument(
		idx,
		context.Background(),
		root,
		path,
	)
	assert.Len(t, references, 5)
	assert.Contains(t, references, DQLReference{
		Role:   DQLEntityReference,
		Entity: "App\\Entity\\Product",
		File:   path,
		Range:  entity.Range,
	})
}

func TestStandaloneDQLResolvesLegacyNamespaceShortcut(t *testing.T) {
	idx, _ := queryBuilderFixture(t)
	idx.SetNamespaceAliasProvider(&testDoctrineNamespaceProvider{
		aliases: map[string][]string{
			"LegacyBundle": {"App\\Entity"},
		},
		revision: 1,
	})
	source := `<?php
$dql = "SELECT p FROM LegacyBundle:Product p WHERE p.name = :name";`
	root := phpparser.Parse(source).Tree.Root
	offset := uint32(strings.Index(source, "p.name") + len("p.name"))
	node := root.NodeAtOffset(offset)

	field, found := idx.QueryFieldReferenceAt(
		context.Background(),
		root,
		node,
		offset,
	)
	require.True(t, found)
	assert.Equal(t, "App\\Entity\\Product", field.Entity)
	assert.Equal(t, "name", field.Field)

	entityOffset := uint32(strings.Index(source, "LegacyBundle:Product") + 5)
	entity, found := idx.QueryEntityReferenceAt(
		context.Background(),
		root,
		root.NodeAtOffset(entityOffset),
		entityOffset,
	)
	require.True(t, found)
	assert.Equal(t, "App\\Entity\\Product", entity.Entity)
	assert.Equal(
		t,
		"LegacyBundle:Product",
		source[entity.Range.Start:entity.Range.End],
	)

	query, found := idx.standaloneDQLContextAt(
		context.Background(),
		root,
		phpquery.StringAt(root.NodeAtOffset(entityOffset)),
		false,
	)
	require.True(t, found)
	assert.Equal(t, []string{"name"}, query.Parameters)

	completionOffset := uint32(strings.Index(source, "LegacyBundle") + 6)
	completions := idx.QueryCompletionsAt(
		context.Background(),
		root,
		root.NodeAtOffset(completionOffset),
		completionOffset,
	)
	var labels []string
	for _, completion := range completions {
		labels = append(labels, completion.Label)
	}
	assert.Contains(t, labels, "LegacyBundle:Product")
}

func TestStandaloneDQLCompletesEmptyQueryAndRejectsDynamicString(t *testing.T) {
	idx, _ := queryBuilderFixture(t)
	source := `<?php
$dql = "";
$dynamic = "product";
$dql = "SELECT p FROM App\\Entity\\Product p WHERE p.name = $dynamic";
`
	root := phpparser.Parse(source).Tree.Root
	offset := uint32(strings.Index(source, `""`) + 1)
	node := root.NodeAtOffset(offset)
	completions := idx.QueryCompletionsAt(
		context.Background(),
		root,
		node,
		offset,
	)
	var labels []string
	for _, completion := range completions {
		labels = append(labels, completion.Label)
	}
	assert.ElementsMatch(t, []string{"SELECT", "UPDATE", "DELETE"}, labels)

	references := DQLReferencesInDocument(
		idx,
		context.Background(),
		root,
		"/project/src/Search.php",
	)
	assert.Empty(t, references)
}

func TestStandaloneDQLSkipsEscapedStringValuesAndDynamicExpressions(
	t *testing.T,
) {
	idx, _ := queryBuilderFixture(t)
	source := `<?php
$dql = 'SELECT p FROM App\Entity\Product p WHERE p.name = \'featured\' AND p.id = :id';
$dql = 'SELECT p FROM App\Entity\Product p' . $suffix;
$entityManager->createQuery($prefix . 'SELECT p FROM App\Entity\Product p');
`
	root := phpparser.Parse(source).Tree.Root
	references := DQLReferencesInDocument(
		idx,
		context.Background(),
		root,
		"/project/src/Search.php",
	)
	var fields []string
	var entities int
	for _, reference := range references {
		if reference.Role == DQLEntityReference {
			entities++
		} else {
			fields = append(fields, reference.Field)
		}
	}
	assert.Equal(t, 1, entities)
	assert.ElementsMatch(t, []string{"name", "id"}, fields)
}

func TestStandaloneDQLRecognizesTypedDoctrineCalls(t *testing.T) {
	idx, phpIndex := queryBuilderFixture(t)
	stubs := `<?php
namespace Doctrine\ORM;
interface EntityManagerInterface {
    public function createQuery(string $dql);
}
class Query {
    public function setDQL(string $dql);
}
`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/doctrine/orm.php",
		[]byte(stubs),
	)))
	source := `<?php
namespace App;
use Doctrine\ORM\EntityManagerInterface;
use Doctrine\ORM\Query;
function search(EntityManagerInterface $entityManager, Query $query): void {
    $entityManager->createQuery("SELECT p FROM App\\Entity\\Product p WHERE p.");
    $query->setDQL("SELECT c FROM App\\Entity\\Category c WHERE c.");
}
`
	path := "/project/src/Search.php"
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	root := parsed.SyntaxTree().Root

	for _, test := range []struct {
		needle string
		label  string
	}{
		{needle: "p.\"", label: "p.name"},
		{needle: "c.\"", label: "c.title"},
	} {
		offset := uint32(strings.Index(source, test.needle) + 2)
		node := root.NodeAtOffset(offset)
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			root,
		)
		completions := idx.QueryCompletionsAt(ctx, root, node, offset)
		var labels []string
		for _, completion := range completions {
			labels = append(labels, completion.Label)
		}
		assert.Contains(t, labels, test.label)
	}
}

func TestDQLUsagesPersistAndRemoveStaleReferences(t *testing.T) {
	cacheDir := t.TempDir()
	path := "/project/src/Search.php"
	source := `<?php
$dql = 'SELECT p FROM App\Entity\Product p WHERE p.name = :name OR p.name = :fallback';
`
	idx, err := NewIndex(cacheDir)
	require.NoError(t, err)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		path,
		[]byte(source),
	)))

	entityReference := DQLReference{
		Role:   DQLEntityReference,
		Entity: "App\\Entity\\Product",
	}
	fieldReference := DQLReference{
		Role:   DQLFieldReference,
		Entity: "App\\Entity\\Product",
		Field:  "name",
	}
	entityUsages, err := idx.DQLUsages(entityReference)
	require.NoError(t, err)
	require.Len(t, entityUsages, 1)
	fieldUsages, err := idx.DQLUsages(fieldReference)
	require.NoError(t, err)
	require.Len(t, fieldUsages, 2)
	require.NoError(t, idx.Close())

	restored, err := NewIndex(cacheDir)
	require.NoError(t, err)
	fieldUsages, err = restored.DQLUsages(fieldReference)
	require.NoError(t, err)
	require.Len(t, fieldUsages, 2)

	require.NoError(t, restored.Index(indexer.NewParsedFile(
		path,
		[]byte(`<?php echo 'no query';`),
	)))
	fieldUsages, err = restored.DQLUsages(fieldReference)
	require.NoError(t, err)
	assert.Empty(t, fieldUsages)
	require.NoError(t, restored.Close())
}
