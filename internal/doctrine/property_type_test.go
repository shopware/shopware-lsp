package doctrine

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPMappingPreservesNullableUnionIntersectionAndDNFTypes(
	t *testing.T,
) {
	source := `<?php
namespace App;
use Doctrine\ORM\Mapping as ORM;
interface Serializable {}
interface Countable {}
class Egg {}
#[ORM\Entity]
class TypedEntity {
    #[ORM\Column]
    private int|string $count;
    #[ORM\Column]
    private ?Egg $optionalEgg;
    #[ORM\Column]
    private Serializable&Countable $tagged;
    #[ORM\Column]
    private (Serializable&Countable)|Egg $dnf;
}`
	path := "/project/src/TypedEntity.php"
	models := ModelsInDocument(
		path,
		indexer.NewParsedFile(path, []byte(source)).SyntaxTree().Root,
		source,
	)
	require.Len(t, models, 1)
	types := make(map[string]string)
	for _, field := range models[0].Fields {
		types[field.Name] = field.PHPType
	}
	assert.Equal(t, "int|string", types["count"])
	assert.Equal(t, "App\\Egg|null", types["optionalEgg"])
	assert.Equal(
		t,
		"App\\Serializable&App\\Countable",
		types["tagged"],
	)
	assert.Equal(
		t,
		"(App\\Serializable&App\\Countable)|App\\Egg",
		types["dnf"],
	)
}
