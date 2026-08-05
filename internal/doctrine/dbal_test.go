package doctrine

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDBALTableColumnAndAliasIntelligence(t *testing.T) {
	idx, err := NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	mapping := `<doctrine-mapping>
  <entity name="App\User" table="cms_users">
    <field name="name" type="string"/>
    <field name="email" column="user_email" type="string"/>
  </entity>
</doctrine-mapping>`
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/config/User.orm.xml",
		[]byte(mapping),
	)))
	stubs := `<?php
namespace Doctrine\DBAL;
class Connection {
    public function insert(string $table, array $data): void {}
    public function update(string $table, array $data): void {}
}
namespace Doctrine\DBAL\Query;
class QueryBuilder {
    public function from(string $table, ?string $alias = null): self {}
    public function update(string $table): self {}
    public function insert(string $table): self {}
    public function delete(string $table): self {}
    public function join(string $from, string $table, string $alias): self {}
    public function leftJoin(string $from, string $table, string $alias): self {}
    public function rightJoin(string $from, string $table, string $alias): self {}
    public function innerJoin(string $from, string $table, string $alias): self {}
}`
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/doctrine-dbal.php",
		[]byte(stubs),
	)))
	source := `<?php
use Doctrine\DBAL\Connection;
use Doctrine\DBAL\Query\QueryBuilder;
function write(Connection $connection, QueryBuilder $builder): void {
    $connection->insert('cms_users', ['' => 'value']);
    $builder->from('cms_');
    $builder->leftJoin('u', 'cms_', '');
}`
	path := "/project/src/Database.php"
	parsed := indexer.NewParsedFile(path, []byte(source))
	require.NoError(t, phpIndex.Index(parsed))
	root := phpparser.Parse(source).Tree.Root

	for _, test := range []struct {
		needle string
		labels []string
	}{
		{needle: "'cms_users'", labels: []string{"cms_users"}},
		{needle: "[''", labels: []string{"name", "user_email"}},
		{needle: "from('cms_')", labels: []string{"cms_users"}},
		{
			needle: "leftJoin('u', 'cms_', '')",
			labels: []string{"cms_users"},
		},
		{
			needle: "leftJoin('u', 'cms_', '')",
			labels: []string{"c", "cms_", "u_cms_", "uCms"},
		},
	} {
		position := strings.Index(source, test.needle)
		require.NotEqual(t, -1, position)
		offset := uint32(position + len(test.needle) - 1)
		if test.needle == "['" {
			offset = uint32(position + 2)
		}
		if strings.HasPrefix(test.needle, "from(") {
			offset = uint32(position + strings.Index(test.needle, "cms_") + 4)
		}
		if strings.HasPrefix(test.needle, "leftJoin(") {
			if test.labels[0] == "cms_users" {
				offset = uint32(
					position + strings.Index(test.needle, "cms_") + 4,
				)
			} else {
				offset = uint32(
					position + strings.LastIndex(test.needle, "''") + 1,
				)
			}
		}
		node := root.NodeAtOffset(offset)
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			path,
			1,
			node,
			root,
		)
		completions := idx.DBALCompletionsAt(ctx, root, node)
		var labels []string
		for _, completion := range completions {
			labels = append(labels, completion.Label)
		}
		for _, label := range test.labels {
			assert.Contains(t, labels, label, test.needle)
		}
	}

	table, found, err := idx.ModelForTable("cms_users")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "App\\User", table.Class)
	model, field, found, err := idx.FieldForColumn("cms_users", "user_email")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "App\\User", model.Class)
	assert.Equal(t, "email", field.Name)
}
