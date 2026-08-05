package doctrine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php/parser"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/require"
)

func TestTypeDeclarationsPreferLiteralGetNameReturn(t *testing.T) {
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, ".cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })

	basePath := filepath.Join(root, "vendor/Doctrine/Type.php")
	typesPath := filepath.Join(root, "src/Types.php")
	base := []byte(`<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}
`)
	custom := []byte(`<?php
namespace App\Doctrine;
final class MoneyType extends \Doctrine\DBAL\Types\Type {
    public function getName(): string {
        return 'currency_amount';
    }
}
final class ConstantType extends \Doctrine\DBAL\Types\Type {
    private const NAME = 'constant_amount';
    public function getName(): string {
        return self::NAME;
    }
}
final class PostalAddressType extends \Doctrine\DBAL\Types\Type {}
`)
	for path, source := range map[string][]byte{
		basePath:  base,
		typesPath: custom,
	} {
		require.NoError(t, phpIndex.Index(
			indexer.NewParsedFile(path, source),
		))
	}

	declarations := TypeDeclarations(phpIndex)
	require.Len(t, declarations, 3)
	byClass := make(map[string]TypeDeclaration)
	for _, declaration := range declarations {
		byClass[declaration.Class] = declaration
	}
	require.Equal(
		t,
		"currency_amount",
		byClass["App\\Doctrine\\MoneyType"].Name,
	)
	require.Equal(
		t,
		"postal_address",
		byClass["App\\Doctrine\\PostalAddressType"].Name,
	)
	require.Equal(
		t,
		"constant_amount",
		byClass["App\\Doctrine\\ConstantType"].Name,
	)
}

func TestTypeDeclarationsForMappingManager(t *testing.T) {
	declarations := []TypeDeclaration{
		{Name: "dbal", Family: DBALTypeFamily},
		{Name: "mongo", Family: MongoDBTypeFamily},
		{Name: "couch", Family: CouchDBTypeFamily},
	}
	names := func(values []TypeDeclaration) []string {
		result := make([]string, 0, len(values))
		for _, value := range values {
			result = append(result, value.Name)
		}
		return result
	}
	require.Equal(
		t,
		[]string{"dbal"},
		names(TypeDeclarationsForMapping("Product.orm.yaml", declarations)),
	)
	require.Equal(
		t,
		[]string{"mongo"},
		names(TypeDeclarationsForMapping("Product.mongodb.xml", declarations)),
	)
	require.Equal(
		t,
		[]string{"couch"},
		names(TypeDeclarationsForMapping("Product.couchdb.yml", declarations)),
	)
	require.Equal(
		t,
		[]string{"mongo", "couch"},
		names(TypeDeclarationsForMapping("Product.odm.xml", declarations)),
	)
	require.Equal(
		t,
		[]string{"dbal", "mongo", "couch"},
		names(TypeDeclarationsForMapping("Product.yaml", declarations)),
	)
}

func TestTypeRegistrationsInYAMLAndXML(t *testing.T) {
	yamlSource := `when@test:
  doctrine:
    dbal:
      types:
        money: App\Doctrine\MoneyType
        address:
          class: 'App\Doctrine\AddressType'
`
	yamlRoot := yamlparser.Parse(yamlSource).Tree.Root
	yamlTypes := TypeRegistrationsInDocument(
		"/project/config/packages/doctrine.yaml",
		yamlRoot,
	)
	require.Len(t, yamlTypes, 2)
	require.Equal(t, "money", yamlTypes[0].Name)
	require.Equal(t, "App\\Doctrine\\MoneyType", yamlTypes[0].Class)
	require.Equal(
		t,
		"money",
		yamlSource[yamlTypes[0].NameRange.Start:yamlTypes[0].NameRange.End],
	)
	require.Equal(t, "address", yamlTypes[1].Name)
	require.Equal(t, "App\\Doctrine\\AddressType", yamlTypes[1].Class)

	xmlSource := `<container xmlns:doctrine="http://symfony.com/schema/dic/doctrine">
  <doctrine:config>
    <doctrine:dbal>
      <doctrine:type name="money" class="App\Doctrine\MoneyType"/>
      <doctrine:type name="address"> App\Doctrine\AddressType </doctrine:type>
    </doctrine:dbal>
  </doctrine:config>
</container>`
	xmlRoot := xmlparser.Parse(xmlSource).Tree.Root
	xmlTypes := TypeRegistrationsInDocument(
		"/project/config/packages/doctrine.xml",
		xmlRoot,
	)
	require.Len(t, xmlTypes, 2)
	require.Equal(t, "money", xmlTypes[0].Name)
	require.Equal(t, "App\\Doctrine\\MoneyType", xmlTypes[0].Class)
	require.Equal(t, "address", xmlTypes[1].Name)
	require.Equal(t, "App\\Doctrine\\AddressType", xmlTypes[1].Class)
	require.Equal(
		t,
		"App\\Doctrine\\AddressType",
		xmlSource[xmlTypes[1].ClassRange.Start:xmlTypes[1].ClassRange.End],
	)

	yamlEmpty := `doctrine:
  dbal:
    types:
      money:
        class:
`
	yamlEmptyRoot := yamlparser.Parse(yamlEmpty).Tree.Root
	yamlOffset := uint32(len(yamlEmpty) - 1)
	yamlReference, found := TypeRegistrationReferenceAt(
		"/project/config/packages/doctrine.yaml",
		yamlEmptyRoot,
		yamlOffset,
	)
	require.True(t, found)
	require.Equal(t, TypeRegistrationClass, yamlReference.Role)
	require.Equal(t, "money", yamlReference.Name)

	xmlEmpty := `<container xmlns:doctrine="urn:doctrine"><doctrine:config><doctrine:dbal><doctrine:type name="money" class=""/></doctrine:dbal></doctrine:config></container>`
	xmlEmptyRoot := xmlparser.Parse(xmlEmpty).Tree.Root
	xmlOffset := uint32(strings.Index(xmlEmpty, `class=""`) + len(`class="`))
	xmlReference, found := TypeRegistrationReferenceAt(
		"/project/config/packages/doctrine.xml",
		xmlEmptyRoot,
		xmlOffset,
	)
	require.True(t, found)
	require.Equal(t, TypeRegistrationClass, xmlReference.Role)
	require.Equal(t, "money", xmlReference.Name)
}

func TestTypeRegistrationsInPHPConfig(t *testing.T) {
	source := `<?php
namespace App\Config;

use App\Doctrine\AddressType;
use App\Doctrine\MoneyType as ImportedMoneyType;
use App\Doctrine\UuidType;
use Symfony\Component\DependencyInjection\Loader\Configurator\ContainerConfigurator;

return static function (ContainerConfigurator $containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => [
            'types' => [
                'money' => ImportedMoneyType::class,
                'address' => 'App\Doctrine\AddressType',
                'uuid' => ['class' => UuidType::class],
            ],
        ],
    ]);
};`
	root := phpparser.Parse(source).Tree.Root
	registrations := TypeRegistrationsInDocument(
		"/project/config/packages/doctrine.php",
		root,
	)
	require.Len(t, registrations, 3)
	require.Equal(t, "money", registrations[0].Name)
	require.Equal(
		t,
		"App\\Doctrine\\MoneyType",
		registrations[0].Class,
	)
	require.Equal(
		t,
		"ImportedMoneyType",
		source[registrations[0].ClassRange.Start:registrations[0].ClassRange.End],
	)
	require.Equal(t, "address", registrations[1].Name)
	require.Equal(
		t,
		"App\\Doctrine\\AddressType",
		registrations[1].Class,
	)
	require.Equal(
		t,
		"App\\Doctrine\\AddressType",
		source[registrations[1].ClassRange.Start:registrations[1].ClassRange.End],
	)
	require.Equal(t, "uuid", registrations[2].Name)
	require.Equal(
		t,
		"App\\Doctrine\\UuidType",
		registrations[2].Class,
	)

	for _, test := range []struct {
		needle        string
		role          TypeRegistrationReferenceRole
		name          string
		class         string
		classConstant bool
	}{
		{
			needle:        "ImportedMoneyType",
			role:          TypeRegistrationClass,
			name:          "money",
			class:         "App\\Doctrine\\MoneyType",
			classConstant: true,
		},
		{
			needle: "App\\Doctrine\\AddressType",
			role:   TypeRegistrationClass,
			name:   "address",
			class:  "App\\Doctrine\\AddressType",
		},
		{
			needle: "'uuid'",
			role:   TypeRegistrationName,
			name:   "uuid",
			class:  "App\\Doctrine\\UuidType",
		},
	} {
		offset := uint32(strings.LastIndex(source, test.needle) + 1)
		reference, found := TypeRegistrationReferenceAt(
			"/project/config/packages/doctrine.php",
			root,
			offset,
		)
		require.True(t, found, test.needle)
		require.Equal(t, test.role, reference.Role, test.needle)
		require.Equal(t, test.name, reference.Name, test.needle)
		require.Equal(t, test.class, reference.Class, test.needle)
		require.Equal(
			t,
			test.classConstant,
			reference.ClassConstant,
			test.needle,
		)
	}
}

func TestPHPTypeRegistrationReferenceAtIncompleteValues(t *testing.T) {
	for _, test := range []struct {
		name          string
		value         string
		classConstant bool
	}{
		{name: "empty string", value: "''"},
		{name: "missing value", value: "", classConstant: true},
		{
			name:  "expanded empty string",
			value: "['class' => '']",
		},
		{
			name:          "expanded missing value",
			value:         "['class' => ]",
			classConstant: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := `<?php
return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => ['types' => [
            'money' => ` + test.value + `
        ]],
    ]);
};`
			root := phpparser.Parse(source).Tree.Root
			offset := uint32(
				strings.Index(source, "'money' =>") +
					len("'money' => ") +
					len(test.value),
			)
			if test.value == "''" {
				offset--
			}
			if strings.HasSuffix(test.value, "']") {
				offset = uint32(
					strings.Index(source, "'class' => ''") +
						len("'class' => '"),
				)
			}
			if test.value == "['class' => ]" {
				offset = uint32(
					strings.Index(source, "'class' =>") +
						len("'class' => "),
				)
			}
			reference, found := TypeRegistrationReferenceAt(
				"/project/config/packages/doctrine.php",
				root,
				offset,
			)
			require.True(t, found)
			require.Equal(t, TypeRegistrationClass, reference.Role)
			require.Equal(t, "money", reference.Name)
			require.Equal(
				t,
				test.classConstant,
				reference.ClassConstant,
			)
		})
	}
}

func TestRuntimePHPTypeRegistrations(t *testing.T) {
	source := `<?php
namespace App\Bootstrap;

use App\Doctrine\EmojiType;
use App\Doctrine\MoneyType;
use Doctrine\DBAL\Types\Type as DBALType;

DBALType::addType('money', MoneyType::class);
\Doctrine\DBAL\Types\Type::overrideType(
    'address',
    'App\Doctrine\AddressType',
);
DBALType::getTypeRegistry()->register(
    'emoji',
    new EmojiType([':)' => '😊']),
);`
	root := phpparser.Parse(source).Tree.Root
	registrations := TypeRegistrationsInDocument(
		"/project/src/DoctrineBootstrap.php",
		root,
	)
	require.Len(t, registrations, 3)
	require.Equal(t, "money", registrations[0].Name)
	require.Equal(
		t,
		"App\\Doctrine\\MoneyType",
		registrations[0].Class,
	)
	require.Equal(t, "address", registrations[1].Name)
	require.Equal(
		t,
		"App\\Doctrine\\AddressType",
		registrations[1].Class,
	)
	require.Equal(t, "emoji", registrations[2].Name)
	require.Equal(
		t,
		"App\\Doctrine\\EmojiType",
		registrations[2].Class,
	)
	require.Equal(
		t,
		"EmojiType",
		source[registrations[2].ClassRange.Start:registrations[2].ClassRange.End],
	)

	for _, test := range []struct {
		needle         string
		name           string
		class          string
		classConstant  bool
		objectCreation bool
	}{
		{
			needle:        "MoneyType::class",
			name:          "money",
			class:         "App\\Doctrine\\MoneyType",
			classConstant: true,
		},
		{
			needle: "App\\Doctrine\\AddressType",
			name:   "address",
			class:  "App\\Doctrine\\AddressType",
		},
		{
			needle:         "EmojiType(",
			name:           "emoji",
			class:          "App\\Doctrine\\EmojiType",
			objectCreation: true,
		},
	} {
		offset := uint32(strings.LastIndex(source, test.needle) + 1)
		reference, found := TypeRegistrationReferenceAt(
			"/project/src/DoctrineBootstrap.php",
			root,
			offset,
		)
		require.True(t, found, test.needle)
		require.Equal(
			t,
			TypeRegistrationClass,
			reference.Role,
			test.needle,
		)
		require.Equal(t, test.name, reference.Name, test.needle)
		require.Equal(t, test.class, reference.Class, test.needle)
		require.Equal(
			t,
			test.classConstant,
			reference.ClassConstant,
			test.needle,
		)
		require.Equal(
			t,
			test.objectCreation,
			reference.ObjectCreation,
			test.needle,
		)
	}
}

func TestRuntimePHPTypeRegistrationIncompleteClassReferences(t *testing.T) {
	for _, test := range []struct {
		name                  string
		call                  string
		objectCreation        bool
		objectCreationStarted bool
	}{
		{
			name: "static registration",
			call: "DBALType::addType('money', )",
		},
		{
			name:           "registry registration",
			call:           "DBALType::getTypeRegistry()->register('emoji', )",
			objectCreation: true,
		},
		{
			name:                  "started registry object",
			call:                  "DBALType::getTypeRegistry()->register('emoji', new )",
			objectCreation:        true,
			objectCreationStarted: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := `<?php
use Doctrine\DBAL\Types\Type as DBALType;
` + test.call + `;`
			root := phpparser.Parse(source).Tree.Root
			offset := uint32(strings.LastIndex(source, ")"))
			reference, found := TypeRegistrationReferenceAt(
				"/project/bootstrap.php",
				root,
				offset,
			)
			require.True(t, found)
			require.Equal(t, TypeRegistrationClass, reference.Role)
			require.Equal(
				t,
				test.objectCreation,
				reference.ObjectCreation,
			)
			require.Equal(
				t,
				test.objectCreationStarted,
				reference.ObjectCreationStarted,
			)
		})
	}
}

func TestIndexMergesConfiguredDBALTypeAliases(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	doctrineIndex, err := NewIndex(cache)
	require.NoError(t, err)
	phpIndex, err := php.NewPHPIndex(filepath.Join(root, ".php-cache"))
	require.NoError(t, err)

	basePath := filepath.Join(root, "vendor/Doctrine/Type.php")
	classPath := filepath.Join(root, "src/MoneyType.php")
	for path, source := range map[string]string{
		basePath: `<?php
namespace Doctrine\DBAL\Types;
abstract class Type {}`,
		classPath: `<?php
namespace App\Doctrine;
final class MoneyType extends \Doctrine\DBAL\Types\Type {}`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	configPath := filepath.Join(root, "config/packages/doctrine.php")
	configSource := `<?php
use App\Doctrine\MoneyType;

return static function ($containerConfigurator): void {
    $containerConfigurator->extension('doctrine', [
        'dbal' => [
            'types' => [
                'exact_money' => MoneyType::class,
            ],
        ],
    ]);
};`
	require.NoError(t, doctrineIndex.Index(indexer.NewParsedFile(
		configPath,
		[]byte(configSource),
	)))

	declarations := doctrineIndex.TypeDeclarations(phpIndex)
	var exact TypeDeclaration
	for _, declaration := range declarations {
		if declaration.Name == "exact_money" {
			exact = declaration
			break
		}
	}
	require.Equal(t, "App\\Doctrine\\MoneyType", exact.Class)
	require.Equal(t, classPath, exact.File)
	require.Equal(t, DBALTypeFamily, exact.Family)

	require.NoError(t, doctrineIndex.Close())
	reopened, err := NewIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, reopened.Close())
		require.NoError(t, phpIndex.Close())
	})
	declarations = reopened.TypeDeclarations(phpIndex)
	require.Contains(t, declarations, exact)
	require.NoError(t, reopened.RemovedFiles([]string{configPath}))
	for _, declaration := range reopened.TypeDeclarations(phpIndex) {
		require.NotEqual(t, "exact_money", declaration.Name)
	}
}
