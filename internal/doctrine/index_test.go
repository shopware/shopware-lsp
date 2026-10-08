package doctrine

import (
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDBALIndexUpdatesRegistrationsAcrossRestartAndClear(t *testing.T) {
	cache := t.TempDir()
	idx, err := NewIndex(cache)
	require.NoError(t, err)
	path := "/project/bootstrap.php"
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(`<?php
use Doctrine\DBAL\Types\Type;
Type::addType('money', MoneyType::class);`))))
	registrations, err := idx.TypeRegistrations("money")
	require.NoError(t, err)
	require.Len(t, registrations, 1)
	require.NoError(t, idx.Close())
	idx, err = NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	registrations, err = idx.TypeRegistrations("money")
	require.NoError(t, err)
	require.Len(t, registrations, 1)
	// A file that stops being a candidate must still remove its former rows.
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte("<?php class Plain {}"))))
	registrations, err = idx.TypeRegistrations("money")
	require.NoError(t, err)
	assert.Empty(t, registrations)
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(`<?php
use Doctrine\DBAL\Types\Type;
Type::addType('money', MoneyType::class);`))))
	require.NoError(t, idx.Clear())
	registrations, err = idx.TypeRegistrations("money")
	require.NoError(t, err)
	assert.Empty(t, registrations)
}

func TestDBALCandidatesExcludeORMODMAndDQL(t *testing.T) {
	for path, source := range map[string]string{
		"/project/entity.php":       `<?php use Doctrine\ORM\Mapping as ORM; #[ORM\Entity] class Product { #[ORM\Column(type: 'string')] private string $name; }`,
		"/project/document.php":     `<?php use Doctrine\ODM\MongoDB\Mapping\Annotations as ODM; #[ODM\Document] class Product {}`,
		"/project/Product.orm.xml":  `<doctrine-mapping><entity name="App\Product"/></doctrine-mapping>`,
		"/project/Product.orm.yaml": "App\\Product:\n  type: entity\n  fields:\n    name:\n      type: string\n",
		"/project/query.php":        `<?php $dql = 'SELECT p FROM App\Product p'; $manager->createQuery($dql);`,
	} {
		t.Run(path, func(t *testing.T) { assert.Zero(t, doctrineCandidates(indexer.NewParsedFile(path, []byte(source)))) })
	}
}

func BenchmarkDBALCandidateScreening(b *testing.B) {
	file := indexer.NewParsedFile("/project/Service.php", []byte(strings.Repeat("<?php class Service { public function run(): void {} }\n", 256)))
	b.SetBytes(int64(len(file.Content)))
	b.ReportAllocs()
	for b.Loop() {
		_ = doctrineCandidates(file)
	}
}
