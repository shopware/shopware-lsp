package doctrine

import (
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPODMTargetDocumentAttributesAndAnnotations(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "attributes",
			source: `<?php
namespace App\Document;
use Doctrine\ODM\MongoDB\Mapping\Annotations as ODM;
#[ODM\Document]
class Article {
    #[ODM\ReferenceOne(targetDocument: User::class)]
    private $author;
    #[ODM\EmbedOne(targetDocument: Address::class)]
    private $address;
}`,
		},
		{
			name: "annotations",
			source: `<?php
namespace App\Document;
use Doctrine\ODM\MongoDB\Mapping\Annotations as ODM;
/** @ODM\Document */
class Article {
    /** @ODM\ReferenceOne(targetDocument="User") */
    private $author;
    /** @ODM\EmbedOne(targetDocument="Address") */
    private $address;
}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := "/project/src/Document/Article.php"
			parsed := indexer.NewParsedFile(path, []byte(test.source))
			models := ModelsInDocument(
				path,
				parsed.SyntaxTree().Root,
				test.source,
			)
			require.Len(t, models, 1)
			assert.Equal(t, DocumentModel, models[0].Kind)
			author := requireField(
				t,
				models[0].Fields,
				"author",
				"",
				"App\\Document\\User",
			)
			assert.Equal(t, "ReferenceOne", author.RelationType)
			assert.NotZero(t, author.RelationRange.Len())
			address := requireField(
				t,
				models[0].Fields,
				"address",
				"",
				"",
			)
			assert.Equal(
				t,
				"App\\Document\\Address",
				address.EmbeddedClass,
			)
			assert.Equal(t, "EmbedOne", address.RelationType)
			assert.NotZero(t, address.EmbeddedRange.Len())
		})
	}
}
