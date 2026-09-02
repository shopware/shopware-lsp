package binder

import (
	"strings"
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/stretchr/testify/require"
)

func TestEstimatedSymbolCapacityIsBounded(t *testing.T) {
	t.Parallel()
	require.Zero(t, estimatedSymbolCapacity(0))
	require.Equal(t, 1, estimatedSymbolCapacity(1))
	require.Equal(t, 1, estimatedSymbolCapacity(1024))
	require.Equal(t, 512, estimatedSymbolCapacity(^uint32(0)))
}

func TestEffectiveTypePreservesDocumentedObjectNarrowing(t *testing.T) {
	t.Parallel()
	require.Equal(
		t,
		"ConcreteCommand",
		effectiveType(
			types.Named("AbstractCommand"),
			types.Named("ConcreteCommand"),
		).String(),
	)
}

func TestEffectiveTypePreservesNativeNullability(t *testing.T) {
	t.Parallel()
	require.Equal(
		t,
		"array<string,string>|null",
		effectiveType(
			types.Nullable(types.Array(types.ArrayKey(), types.Mixed())),
			types.Array(types.String(), types.String()),
		).String(),
	)
}

func TestEffectiveTypePreservesDocumentedStaticObjectNarrowing(t *testing.T) {
	t.Parallel()
	require.Equal(
		t,
		"static",
		effectiveType(
			types.Named("MessageInterface"),
			types.Static(),
		).String(),
	)
}

func TestBinderFindsConditionalPolyfillClass(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
if (PHP_VERSION_ID < 80300) {
    final class Override {}
}
`).Tree.Root
	document := New().Bind("/polyfill.php", 1, root)
	override := findSymbol(
		t,
		document,
		semantic.ClassSymbol,
		"Override",
	)
	require.Equal(t, "Override", override.FullyQualified)
}

func TestBinderScopesAnonymousClassMethods(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function listen(object $dispatcher): object {
    $prefix = 'event';
    return new class($prefix) {
        public function __construct(private string $prefix) {}
        public function handle(object $event): string {
            return $this->prefix . $event::class;
        }
    };
}
`).Tree.Root
	document := New().Bind("/anonymous.php", 1, root)

	var anonymous semantic.Symbol
	for _, symbol := range document.Symbols {
		if symbol.Kind == semantic.ClassSymbol &&
			symbol.Flags.Has(semantic.SyntheticFlag) {
			anonymous = symbol
			break
		}
	}
	require.NotEmpty(t, anonymous.ID)
	require.Contains(t, anonymous.FullyQualified, "{anonymous@/anonymous.php:")
	handle := findSymbol(t, document, semantic.MethodSymbol, "handle")
	require.Equal(t, anonymous.ID, handle.Container)

	for _, reference := range document.References {
		if reference.Kind == semantic.VariableName &&
			reference.Name == "$event" {
			require.NotEqual(
				t,
				document.Scopes[1].ID,
				reference.Scope,
				"method parameters must resolve in the anonymous method scope",
			)
		}
	}
}

func TestBinderDeclaresTopLevelForeachLocals(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
$items = ['one'];
foreach ($items as $item) {
    echo $item;
}
`).Tree.Root
	document := New().Bind("/top-level.php", 1, root)
	require.True(t, document.Scopes[0].HasSymbol(document.Symbols, "$item"))
	for _, reference := range document.References {
		if reference.Kind == semantic.VariableName &&
			reference.Name == "$item" {
			require.Equal(t, semantic.ScopeID(0), reference.Scope)
		}
	}
}

func TestNonClassNativeTypeNameMatchesBuiltinTypeVocabulary(t *testing.T) {
	t.Parallel()
	builtins := []string{
		"unknown", "error", "never", "mixed", "void", "null",
		"bool", "boolean", "true", "false", "int", "integer",
		"positive-int", "negative-int", "non-negative-int",
		"float", "double", "number", "string", "non-empty-string",
		"numeric-string", "literal-string", "object", "resource",
		"array-key", "array", "non-empty-array", "list",
		"non-empty-list", "iterable", "callable", "closure",
		"class-string", "interface-string", "trait-string", "enum-string",
	}
	for _, name := range builtins {
		_, err := types.ParseNative(name)
		require.NoError(t, err, name)
		require.True(t, nonClassNativeTypeName(name), name)
		require.True(t, nonClassNativeTypeName(strings.ToUpper(name)), name)
		require.True(t, nonClassNativeTypeName("\\"+name), name)
	}
	for _, name := range []string{
		"App\\Product",
		"Product",
		"self",
		"static",
		"parent",
		"$this",
	} {
		require.False(t, nonClassNativeTypeName(name), name)
	}
}

func TestEstimatedDocumentSymbolCapacityCoversStructuralDeclarations(
	t *testing.T,
) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class Product {
    public const FIRST = 1, SECOND = 2;
    private string $name, $label;
    public function __construct(public string $id, int $count) {
        $callback = static fn (string $value): string => $value;
    }
}
`).Tree.Root
	document := New().Bind("/capacity.php", 1, root)
	require.Equal(t, 12, structuralSymbolCount(root))
	require.GreaterOrEqual(
		t,
		estimatedDocumentSymbolCapacity(root),
		len(document.Symbols)-1,
	)
}

func TestEstimatedDocumentScopeCapacityCoversStructuralScopes(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
namespace App;
class Product {
    public function map(): void {
        $callback = static fn (string $value): string => $value;
    }
}
`).Tree.Root
	document := New().Bind("/capacity.php", 1, root)
	_, _, _, scopeCapacity := estimatedDocumentCapacities(root)

	require.Equal(t, len(document.Scopes), scopeCapacity)
	require.Equal(t, len(document.Scopes), cap(document.Scopes))
}

func TestStructuralSymbolCountUsesExactForeachLocals(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		foreach  string
		expected int
	}{
		{name: "value", foreach: "foreach ($items as $value) {}", expected: 3},
		{name: "key_value", foreach: "foreach ($items as $key => $value) {}", expected: 4},
		{name: "destructured", foreach: "foreach ($items as [$first, $second]) {}", expected: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := phpparser.Parse(
				"<?php function walk($items) { " + test.foreach + " }",
			).Tree.Root
			document := New().Bind("/foreach.php", 1, root)
			require.Equal(t, test.expected, structuralSymbolCount(root))
			require.Len(t, document.Symbols, test.expected)
		})
	}
}

func TestEstimatedTypeFactCapacityIsBounded(t *testing.T) {
	t.Parallel()
	require.Zero(t, estimatedTypeFactCapacity(39))
	require.Equal(t, 1, estimatedTypeFactCapacity(40))
	require.Equal(t, 4096, estimatedTypeFactCapacity(^uint32(0)))
	require.Zero(t, estimatedNonLiteralTypeFactCapacity(39, 0))
	require.Equal(t, 3, estimatedNonLiteralTypeFactCapacity(120, 0))
	require.Equal(t, 2, estimatedNonLiteralTypeFactCapacity(120, 3))
	require.Equal(
		t,
		1366,
		estimatedNonLiteralTypeFactCapacity(^uint32(0), 4096),
	)
}

func TestEstimatedReferenceCapacityIsBounded(t *testing.T) {
	t.Parallel()
	require.Zero(t, estimatedReferenceCapacity(0))
	require.Equal(t, 144, estimatedReferenceCapacity(192))
	require.Equal(t, 4096, estimatedReferenceCapacity(int(^uint32(0))))
	require.Equal(t, 154, estimatedLinkedReferenceCapacity(192, 10))
	require.Equal(
		t,
		4096,
		estimatedLinkedReferenceCapacity(int(^uint32(0)), 10),
	)
	require.Equal(t, 139, estimatedDocumentReferenceCapacity(192, 10))
	require.Equal(
		t,
		4096,
		estimatedDocumentReferenceCapacity(int(^uint32(0)), 10),
	)
}

func TestEstimatedScopeCapacityIsBounded(t *testing.T) {
	t.Parallel()
	require.Equal(t, 1, estimatedScopeCapacity(0))
	require.Equal(t, 11, estimatedScopeCapacity(10))
	require.Equal(t, 4096, estimatedScopeCapacity(int(^uint32(0))))
}

func TestBindRichDeclarationsAndReferences(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App\Domain;

use Vendor\Contracts\BaseEntity;
use Vendor\Service as ImportedService;
use function Vendor\build;

#[Entity]
final class Product extends BaseEntity implements \JsonSerializable
{
    use TracksChanges;

    public const KIND = 'product';
    private readonly ImportedService $service;

    public function __construct(
        public readonly string $id,
        ImportedService $service = null,
    ) {
        $this->service = $service;
    }

    public function load(): ?Result
    {
        $local = new Result();
        return build($local);
    }

    public function absolute(): \Vendor\Result
    {
        return new \Vendor\Result();
    }
}

function helper(Product $product): Product
{
    return $product;
}`
	root := phpparser.Parse(source).Tree.Root
	document := New().Bind("/project/Product.php", 7, root)

	require.Equal(t, "App\\Domain", document.Namespace)
	require.NotEmpty(t, document.Scopes)
	class := findSymbol(t, document, semantic.ClassSymbol, "Product")
	require.Equal(t, []string{"Vendor\\Contracts\\BaseEntity"}, class.Extends())
	require.Equal(t, []string{"JsonSerializable"}, class.Implements())
	require.Equal(t, []string{"App\\Domain\\TracksChanges"}, class.Traits())
	require.True(t, class.Flags.Has(semantic.FinalFlag))

	constructor := findSymbol(t, document, semantic.MethodSymbol, "__construct")
	require.Len(t, constructor.Parameters, 2)
	require.Equal(t, "string", constructor.Parameters[0].Type.String())
	require.Equal(t, "Vendor\\Service", constructor.Parameters[1].Type.String())
	require.True(t, constructor.Parameters[0].Flags.Has(semantic.ReadonlyFlag))

	load := findSymbol(t, document, semantic.MethodSymbol, "load")
	require.Equal(t, "App\\Domain\\Result|null", load.ReturnType.String())
	require.Equal(
		t,
		"Vendor\\Result",
		findSymbol(t, document, semantic.MethodSymbol, "absolute").
			ReturnType.String(),
	)
	require.Equal(t, "void", constructor.ReturnType.String())
	findSymbol(t, document, semantic.PropertySymbol, "service")
	promoted := findSymbol(t, document, semantic.PropertySymbol, "id")
	require.True(t, promoted.Flags.Has(semantic.PromotedFlag))
	require.Equal(
		t,
		`"product"`,
		findSymbol(t, document, semantic.ClassConstantSymbol, "KIND").
			Type.String(),
	)
	findSymbol(t, document, semantic.FunctionSymbol, "helper")
	findSymbol(t, document, semantic.LocalSymbol, "$local")
	require.NotEmpty(t, document.References)
}

func TestBindDoesNotRecordBroadObjectAsClassReference(t *testing.T) {
	t.Parallel()
	document := New().Bind(
		"/project/ObjectTypes.php",
		1,
		phpparser.Parse(`<?php
namespace App;
function transform(object $input, Product $product): object {}
`).Tree.Root,
	)

	var classReferences []string
	for _, reference := range document.References {
		if reference.Kind == semantic.ClassName {
			classReferences = append(classReferences, reference.QualifiedNames()...)
		}
	}
	require.Contains(t, classReferences, "App\\Product")
	require.NotContains(t, classReferences, "App\\object")
}

func TestBindDoesNotTreatDynamicClassConstantNameAsClassReference(t *testing.T) {
	t.Parallel()
	document := New().Bind(
		"/project/DynamicClass.php",
		1,
		phpparser.Parse(`<?php
namespace App;
function className(object $value): string {
    return $value::class;
}
`).Tree.Root,
	)

	for _, reference := range document.References {
		if reference.Kind == semantic.ClassName {
			require.NotContains(t, reference.QualifiedNames(), "App\\class")
		}
	}
}

func TestBindUntypedParameterAsMixed(t *testing.T) {
	t.Parallel()
	document := New().Bind(
		"/project/Untyped.php",
		1,
		phpparser.Parse(`<?php
function consume($value): void {}
`).Tree.Root,
	)

	function := findSymbol(t, document, semantic.FunctionSymbol, "consume")
	require.Len(t, function.Parameters, 1)
	require.Equal(t, "mixed", function.Parameters[0].Type.String())
}

func TestBindRecordsEachNativeClassTypeReferenceOnce(t *testing.T) {
	t.Parallel()
	document := New().Bind(
		"/project/NativeTypes.php",
		1,
		phpparser.Parse(`<?php
function transform(
    Simple $simple,
    Left&Right $intersection,
    (GroupedOne&GroupedTwo)|null $grouped,
): Result|false {}
`).Tree.Root,
	)

	classReferences := make(map[string]int)
	for _, reference := range document.References {
		if reference.Kind != semantic.ClassName {
			continue
		}
		for _, qualified := range reference.QualifiedNames() {
			classReferences[qualified]++
		}
	}
	require.Equal(t, map[string]int{
		"GroupedOne": 1,
		"GroupedTwo": 1,
		"Left":       1,
		"Result":     1,
		"Right":      1,
		"Simple":     1,
	}, classReferences)
}

func TestBindUsesExactParameterStorage(t *testing.T) {
	t.Parallel()
	document := New().Bind(
		"/project/Parameters.php",
		1,
		phpparser.Parse(`<?php
/** @method void virtual(int $first, string $second, bool $third) */
class Parameters {
    public function actual(int $first, string $second, bool $third): void {
        $closure = function (int $first, string $second, bool $third): void {};
    }
}
`).Tree.Root,
	)

	for _, symbol := range []semantic.Symbol{
		findSymbol(t, document, semantic.MethodSymbol, "virtual"),
		findSymbol(t, document, semantic.MethodSymbol, "actual"),
		findSymbol(t, document, semantic.ClosureSymbol, "{closure}"),
	} {
		require.Len(t, symbol.Parameters, 3, symbol.Name)
		require.Equal(
			t,
			len(symbol.Parameters),
			cap(symbol.Parameters),
			symbol.Name,
		)
	}
}

func TestBindSharesCompleteNamespaceImportsAcrossScopes(t *testing.T) {
	t.Parallel()
	document := New().Bind(
		"/project/Imports.php",
		1,
		phpparser.Parse(`<?php
namespace App;

class BeforeImport {}

use Vendor\Service;
use function Vendor\run;
use const Vendor\VERSION;

function afterImport(Service $service): void {}
`).Tree.Root,
	)

	var namespaceScopes int
	for _, scope := range document.Scopes {
		if scope.Namespace != "App" {
			continue
		}
		namespaceScopes++
		require.Equal(
			t,
			"Vendor\\Service",
			scope.Imports.Classes["service"],
			"scope %d should see the namespace import table",
			scope.ID,
		)
		require.Equal(t, "Vendor\\run", scope.Imports.Functions["run"])
		require.Equal(t, "Vendor\\VERSION", scope.Imports.Constants["VERSION"])
	}
	require.GreaterOrEqual(t, namespaceScopes, 3)
}

func TestBindKeepsSiblingNamespaceImportTablesIsolated(t *testing.T) {
	t.Parallel()
	document := New().Bind(
		"/project/Imports.php",
		1,
		phpparser.Parse(`<?php
namespace App {
    use Vendor\First;
    class One {}
}
namespace App {
    class Two {}
}
`).Tree.Root,
	)

	one := findSymbol(t, document, semantic.ClassSymbol, "One")
	two := findSymbol(t, document, semantic.ClassSymbol, "Two")
	for _, scope := range document.Scopes {
		switch scope.Owner {
		case one.ID:
			require.Equal(
				t,
				"Vendor\\First",
				scope.Imports.Classes["first"],
			)
		case two.ID:
			require.Empty(t, scope.Imports.Classes)
		}
	}
}

func TestBindPHPDocAssistantTagsOnParameters(t *testing.T) {
	t.Parallel()
	source := `<?php
class Assistant
{
    /**
     * @param string $route #Route #Service
     */
    public function open(string $route): void {}
}
`
	document := New().Bind(
		"/project/Assistant.php",
		1,
		phpparser.Parse(source).Tree.Root,
	)
	method := findSymbol(t, document, semantic.MethodSymbol, "open")
	require.Len(t, method.Parameters, 1)
	require.Equal(
		t,
		[]string{"Route", "Service"},
		method.Parameters[0].AssistantTags,
	)
}

func TestBindAllocatesTypeAliasesOnlyWhenDeclared(t *testing.T) {
	t.Parallel()

	withoutAliases := New().Bind(
		"/project/Plain.php",
		1,
		phpparser.Parse("<?php class Plain {}").Tree.Root,
	)
	require.Nil(t, withoutAliases.TypeAliases)

	withAliases := New().Bind(
		"/project/Aliased.php",
		1,
		phpparser.Parse(`<?php
/**
 * @phpstan-type EntityMap array<string, int>
 */
class Aliased {}
`).Tree.Root,
	)
	require.Equal(
		t,
		"array<string,int>",
		withAliases.TypeAliases["EntityMap"].String(),
	)
	alias := findSymbol(
		t,
		withAliases,
		semantic.TypeAliasSymbol,
		"EntityMap",
	)
	require.Equal(t, "array<string,int>", alias.Type.String())
	require.True(t, alias.Flags.Has(semantic.SyntheticFlag))
}

func TestBindImportedTypeAliasUsesDeclaringClassIdentity(t *testing.T) {
	t.Parallel()

	document := New().Bind(
		"/project/Consumer.php",
		1,
		phpparser.Parse(`<?php
namespace App;
use Vendor\Definition;
/**
 * @phpstan-import-type Payload from Definition
 */
class Consumer {
    /** @param Payload $payload */
    public function consume(array $payload): void {}
}
`).Tree.Root,
	)
	alias := types.PHPDocAlias("Vendor\\Definition", "Payload")
	require.True(t, document.TypeAliases["Payload"].Equal(alias))
	method := findSymbol(t, document, semantic.MethodSymbol, "consume")
	require.True(t, method.Parameters[0].DocType.Equal(alias))
}

func TestIndexedTypeAliasExpandsForCompatibility(t *testing.T) {
	t.Parallel()

	document := New().Bind(
		"/project/Definition.php",
		1,
		phpparser.Parse(`<?php
namespace Vendor;
/**
 * @phpstan-type Payload array{id: string}
 */
class Definition {}
`).Tree.Root,
	)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	actual := types.ArrayShape([]types.ShapeField{{
		Name: "id",
		Type: types.String(),
	}}, false)

	require.True(t, snapshot.Relations().IsAssignableTo(
		actual,
		types.PHPDocAlias("Vendor\\Definition", "Payload"),
	))
}

func TestLinkResolvesCrossFileAndLocalReferences(t *testing.T) {
	t.Parallel()
	binder := New()
	base := binder.Bind("/base.php", 1, phpparser.Parse(`<?php
namespace App;
class Target {}
function build(Target $target): Target { return $target; }
`).Tree.Root)
	consumer := binder.Bind("/consumer.php", 1, phpparser.Parse(`<?php
namespace App;
function consume(): Target {
    $value = new Target();
    return build($value);
}
`).Tree.Root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{base, consumer})
	linked := Link(consumer, snapshot)

	var resolved int
	for _, reference := range linked.References {
		if reference.Resolved != "" {
			resolved++
			require.Empty(
				t,
				reference.CandidateIDs(),
				"unique references keep only their resolved ID",
			)
		}
	}
	require.GreaterOrEqual(t, resolved, 3)
}

func TestBindEnumRuntimeMembers(t *testing.T) {
	t.Parallel()
	document := New().Bind(
		"/status.php",
		1,
		phpparser.Parse(`<?php
enum Status: string {
    case Active = 'active';
}
`).Tree.Root,
	)
	status := findSymbol(t, document, semantic.EnumSymbol, "Status")
	require.Contains(t, status.Implements(), "UnitEnum")
	require.Contains(t, status.Implements(), "BackedEnum")
	cases := findSymbol(t, document, semantic.MethodSymbol, "cases")
	require.True(t, cases.Flags.Has(semantic.StaticFlag))
	require.True(t, cases.Flags.Has(semantic.SyntheticFlag))
	require.Equal(t, "list<Status>", cases.ReturnType.String())
	from := findSymbol(t, document, semantic.MethodSymbol, "from")
	require.True(t, from.Flags.Has(semantic.StaticFlag))
	require.Equal(t, "Status", from.ReturnType.String())
	require.Equal(t, "string", from.Parameters[0].Type.String())
	tryFrom := findSymbol(t, document, semantic.MethodSymbol, "tryFrom")
	require.Equal(t, "Status|null", tryFrom.ReturnType.String())
	require.Equal(
		t,
		"string",
		findSymbol(t, document, semantic.PropertySymbol, "name").Type.String(),
	)
	require.Equal(
		t,
		"string",
		findSymbol(t, document, semantic.PropertySymbol, "value").Type.String(),
	)
}

func TestBindPHPDocGenericsAndSyntheticMembers(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
namespace App;
/**
 * @template TEntity of Entity
 * @extends Repository<TEntity>
 * @property-read list<TEntity> $entities
 * @method TEntity|null find(string $id)
 */
class ProductRepository extends Repository {
    /** @return list<TEntity> */
    public function all(): array {}

    /**
     * @template Item of Entity
     * @param Item $entity
     * @return Item
     */
    public function identity($entity) {}
}
`).Tree.Root
	document := New().Bind("/repository.php", 1, root)
	class := findSymbol(t, document, semantic.ClassSymbol, "ProductRepository")
	require.Len(t, class.Templates(), 1)
	require.Equal(t, "App\\Entity", class.Templates()[0].Bound.String())
	require.Equal(t, "App\\Repository<TEntity>", class.ExtendsTypes()[0].String())
	entities := findSymbol(t, document, semantic.PropertySymbol, "entities")
	require.Equal(t, "list<TEntity>", entities.Type.String())
	require.True(t, entities.Flags.Has(semantic.SyntheticFlag))
	find := findSymbol(t, document, semantic.MethodSymbol, "find")
	require.Equal(t, "TEntity|null", find.ReturnType.String())
	require.True(t, find.Flags.Has(semantic.SyntheticFlag))
	require.Equal(t, "list<TEntity>", findSymbol(t, document, semantic.MethodSymbol, "all").ReturnType.String())
	identity := findSymbol(t, document, semantic.MethodSymbol, "identity")
	require.Len(t, identity.Templates(), 1)
	require.Equal(t, "Item", identity.Templates()[0].Name)
	require.Equal(t, "App\\Entity", identity.Templates()[0].Bound.String())
	require.Equal(t, "Item", identity.Parameters[0].Type.String())
	require.Equal(t, "Item", identity.ReturnType.String())
}

func TestBindTypedConstantsAndAsymmetricProperties(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
namespace App;
class Modern {
    public const string KIND = 'modern';
    public private(set) string $name {
        get => $this->name;
    }
}`).Tree.Root
	document := New().Bind("/modern.php", 1, root)
	constant := findSymbol(t, document, semantic.ClassConstantSymbol, "KIND")
	require.Equal(t, "string", constant.Type.String())
	property := findSymbol(t, document, semantic.PropertySymbol, "name")
	require.Equal(t, semantic.Public, property.Visibility)
	require.True(t, property.HasWriteVisibility)
	require.Equal(t, semantic.Private, property.WriteVisibility)
	require.Equal(t, "string", property.Type.String())
}

func TestBindInfersUntypedConstantLiteralTypes(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
namespace App;
class ExitCode {
    public const SUCCESS = 0, FAILURE = -1;
    public const HEX_MASK = 0xDEAD;
    public const LABEL = 'done';
    public const ENABLED = true;
}`).Tree.Root
	document := New().Bind("/constants.php", 1, root)
	require.Equal(
		t,
		"0",
		findSymbol(t, document, semantic.ClassConstantSymbol, "SUCCESS").
			Type.String(),
	)
	require.Equal(
		t,
		"-1",
		findSymbol(t, document, semantic.ClassConstantSymbol, "FAILURE").
			Type.String(),
	)
	require.Equal(
		t,
		"0xDEAD",
		findSymbol(t, document, semantic.ClassConstantSymbol, "HEX_MASK").
			Type.String(),
	)
	require.Equal(
		t,
		`"done"`,
		findSymbol(t, document, semantic.ClassConstantSymbol, "LABEL").
			Type.String(),
	)
	require.Equal(
		t,
		"true",
		findSymbol(t, document, semantic.ClassConstantSymbol, "ENABLED").
			Type.String(),
	)
}

func TestBindPreservesTypedConstantArrayEntries(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace Symfony\Contracts\HttpClient;
interface HttpClientInterface {
    public const OPTIONS_DEFAULTS = [
        'timeout' => null,
        'headers' => [],
        'verify_peer' => true,
        'max_redirects' => 20,
        'bindto' => '0',
    ];
}`
	root := phpparser.Parse(source).Tree.Root
	document := New().Bind("/HttpClientInterface.php", 1, root)
	constant := findSymbol(
		t,
		document,
		semantic.ClassConstantSymbol,
		"OPTIONS_DEFAULTS",
	)
	require.Len(t, constant.ConstantArray(), 5)
	require.Equal(t, "timeout", constant.ConstantArray()[0].Key)
	require.Equal(t, "null", constant.ConstantArray()[0].Type.String())
	require.Equal(t, "null", constant.ConstantArray()[0].Value)
	keyRange := constant.ConstantArray()[0].KeyRange
	require.Equal(
		t,
		"'timeout'",
		source[keyRange.Start:keyRange.End],
	)
	require.Equal(t, "array", constant.ConstantArray()[1].Type.String())
	require.Equal(t, "bool", constant.ConstantArray()[2].Type.String())
	require.Equal(t, "int", constant.ConstantArray()[3].Type.String())
	require.Equal(t, "string", constant.ConstantArray()[4].Type.String())
}

func TestBindPreservesDirectLiteralMethodReturns(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;
class Helper {
    public function getName(bool $debug): string {
        $nested = static function (): string {
            return 'nested';
        };
        if ($debug) {
            return 'debug_formatter';
        }
        return 'formatter';
    }
}`
	document := New().Bind(
		"/Helper.php",
		1,
		phpparser.Parse(source).Tree.Root,
	)
	method := findSymbol(t, document, semantic.MethodSymbol, "getName")
	require.Len(t, method.LiteralReturns(), 2)
	require.Equal(t, "debug_formatter", method.LiteralReturns()[0].Value)
	require.Equal(t, `"debug_formatter"`, method.LiteralReturns()[0].Type.String())
	require.Equal(t, "formatter", method.LiteralReturns()[1].Value)
	for _, literal := range method.LiteralReturns() {
		require.NotEqual(t, "nested", literal.Value)
		require.Equal(
			t,
			"'"+literal.Value+"'",
			source[literal.Range.Start:literal.Range.End],
		)
	}
}

func TestBindPreservesClassConstantMethodReturns(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;
use Shared\TypeNames;
class Helper {
    public function getName(bool $nested): string {
        $callback = static function (): string {
            return self::NESTED;
        };
        if ($nested) {
            return self::LOCAL;
        }
        return TypeNames::SHARED;
    }
}`
	document := New().Bind(
		"/Helper.php",
		1,
		phpparser.Parse(source).Tree.Root,
	)
	method := findSymbol(t, document, semantic.MethodSymbol, "getName")
	require.Len(t, method.ConstantReturns(), 2)
	require.Equal(t, []semantic.ConstantReturn{
		{
			Receiver: "self",
			Name:     "LOCAL",
			Range:    method.ConstantReturns()[0].Range,
		},
		{
			Receiver: "Shared\\TypeNames",
			Name:     "SHARED",
			Range:    method.ConstantReturns()[1].Range,
		},
	}, method.ConstantReturns())
	for _, returned := range method.ConstantReturns() {
		require.NotContains(t, source[returned.Range.Start:returned.Range.End], "NESTED")
	}
}

func TestBindDeprecatedPHPDocAndAttributes(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
namespace App;
class Legacy {
    /** @deprecated */
    public string $documented;

    #[\Deprecated(message: 'Use active instead')]
    public string $attributed;

    /** @deprecated */
    public function documentedMethod(): void {}

    #[\Deprecated(message: 'Use active instead')]
    public function attributedMethod(): void {}

    #[\Deprecated]
    public const LEGACY = 'legacy';

    public function __construct(
        #[\Deprecated]
        public string $promoted = '',
    ) {}
}`).Tree.Root
	document := New().Bind("/legacy.php", 1, root)
	for _, name := range []string{"documented", "attributed"} {
		require.True(
			t,
			findSymbol(t, document, semantic.PropertySymbol, name).
				Flags.Has(semantic.DeprecatedFlag),
		)
	}
	for _, name := range []string{"documentedMethod", "attributedMethod"} {
		require.True(
			t,
			findSymbol(t, document, semantic.MethodSymbol, name).
				Flags.Has(semantic.DeprecatedFlag),
		)
	}
	require.True(
		t,
		findSymbol(t, document, semantic.ClassConstantSymbol, "LEGACY").
			Flags.Has(semantic.DeprecatedFlag),
	)
	require.True(
		t,
		findSymbol(t, document, semantic.PropertySymbol, "promoted").
			Flags.Has(semantic.DeprecatedFlag),
	)
}

func TestBindPHPDocFinalSeparatelyFromNativeFinal(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
namespace App;
/** @final */
class ExtensionPoint {}
final class ClosedClass {}
`).Tree.Root
	document := New().Bind("/classes.php", 1, root)

	extensionPoint := findSymbol(
		t,
		document,
		semantic.ClassSymbol,
		"ExtensionPoint",
	)
	require.True(t, extensionPoint.Flags.Has(semantic.SoftFinalFlag))
	require.False(t, extensionPoint.Flags.Has(semantic.FinalFlag))

	closedClass := findSymbol(t, document, semantic.ClassSymbol, "ClosedClass")
	require.True(t, closedClass.Flags.Has(semantic.FinalFlag))
	require.False(t, closedClass.Flags.Has(semantic.SoftFinalFlag))
}

func TestBindConstantAttributeArgumentsOnSymbolsAndParameters(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
namespace App;
use JetBrains\PhpStorm\ExpectedValues;

final class Demo {
    #[\JetBrains\PhpStorm\ArrayShape([
        'id' => 'positive-int',
        'name' => 'string',
    ])]
    #[\JetBrains\PhpStorm\Deprecated(
        reason: 'Legacy API',
        replacement: 'readNew()',
        since: '2.0',
    )]
    public function read(
        #[ExpectedValues(values: [Mode::ACTIVE, 'all', 7], flags: [FLAG_A | FLAG_B])]
        string $mode,
    ): array {}
}
`).Tree.Root
	document := New().Bind("/attributes.php", 1, root)
	method := findSymbol(t, document, semantic.MethodSymbol, "read")
	require.Len(t, method.Attributes(), 2)
	require.Equal(t, types.ArrayShapeKind, method.ReturnType.Kind())
	require.Equal(t, "int", method.ReturnType.Field(0).Type.String())
	require.Equal(t, "string", method.ReturnType.Field(1).Type.String())

	shape := method.Attributes()[0]
	require.Equal(t, "JetBrains\\PhpStorm\\ArrayShape", shape.Name)
	require.Len(t, shape.Arguments, 1)
	require.Equal(t, semantic.AttributeValueArray, shape.Arguments[0].Value.Kind)
	require.Len(t, shape.Arguments[0].Value.Items, 2)
	require.True(t, shape.Arguments[0].Value.Items[0].HasKey)
	require.Equal(t, "id", shape.Arguments[0].Value.Items[0].Key.Value)
	require.Equal(
		t,
		"positive-int",
		shape.Arguments[0].Value.Items[0].Value.Value,
	)

	deprecated := method.Attributes()[1]
	require.Equal(t, "JetBrains\\PhpStorm\\Deprecated", deprecated.Name)
	require.Equal(t, []string{"reason", "replacement", "since"}, []string{
		deprecated.Arguments[0].Name,
		deprecated.Arguments[1].Name,
		deprecated.Arguments[2].Name,
	})
	require.Equal(t, "Legacy API", deprecated.Arguments[0].Value.Value)

	require.Len(t, method.Parameters, 1)
	require.Len(t, method.Parameters[0].Attributes, 1)
	expected := method.Parameters[0].Attributes[0]
	require.Equal(t, "JetBrains\\PhpStorm\\ExpectedValues", expected.Name)
	require.Len(t, expected.Arguments, 2)
	require.Equal(t, "values", expected.Arguments[0].Name)
	require.Len(t, expected.Arguments[0].Value.Items, 3)
	require.Equal(
		t,
		"App\\Mode::ACTIVE",
		expected.Arguments[0].Value.Items[0].Value.Value,
	)
	require.Equal(
		t,
		semantic.AttributeValueExpression,
		expected.Arguments[1].Value.Items[0].Value.Kind,
	)
	require.Equal(
		t,
		"FLAG_A | FLAG_B",
		expected.Arguments[1].Value.Items[0].Value.Expression,
	)

	parameterSymbol := findSymbol(
		t,
		document,
		semantic.ParameterSymbol,
		"$mode",
	)
	require.Equal(t, expected, parameterSymbol.Attributes()[0])
}

func TestBindShapeAndNoReturnAttributeTypes(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
namespace App;
use JetBrains\PhpStorm\{ArrayShape, NoReturn, ObjectShape};

#[NoReturn]
function stop(): void {}

#[NoReturn(1)]
function stopConditionally(int $mode): void {}

#[ObjectShape(['id' => 'positive-int', 'owner' => User::class])]
function record(): object {}

function accept(
    #[ArrayShape(['id' => 'positive-int', 'label?' => 'string'])]
    array $input,
): void {}
`).Tree.Root
	document := New().Bind("/attribute-types.php", 1, root)

	stop := findSymbol(t, document, semantic.FunctionSymbol, "stop")
	require.Equal(t, types.NeverKind, stop.ReturnType.Kind())
	conditional := findSymbol(
		t,
		document,
		semantic.FunctionSymbol,
		"stopConditionally",
	)
	require.Equal(t, types.VoidKind, conditional.ReturnType.Kind())

	record := findSymbol(t, document, semantic.FunctionSymbol, "record")
	require.Equal(t, types.ObjectShapeKind, record.ReturnType.Kind())
	// Invalid non-string shape values degrade only that field to omission,
	// preserving the useful remainder of the declaration.
	require.Equal(t, 1, record.ReturnType.FieldCount())
	require.Equal(t, "int", record.ReturnType.Field(0).Type.String())

	accept := findSymbol(t, document, semantic.FunctionSymbol, "accept")
	require.Len(t, accept.Parameters, 1)
	require.Equal(t, types.ArrayShapeKind, accept.Parameters[0].Type.Kind())
	require.True(t, accept.Parameters[0].Type.Field(1).Optional)
	require.Equal(
		t,
		accept.Parameters[0].Type,
		findSymbol(t, document, semantic.ParameterSymbol, "$input").Type,
	)
}

func TestBindInlinePromotedPropertyVarType(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
class StoreClient {
    public function __construct(
        /** @var array<string, string> */
        protected readonly array $endpoints,
    ) {}
}
`).Tree.Root
	document := New().Bind("/promoted-property-doc.php", 1, root)

	constructor := findSymbol(t, document, semantic.MethodSymbol, "__construct")
	require.Len(t, constructor.Parameters, 1)
	require.Equal(t, "array<string,string>", constructor.Parameters[0].Type.String())
	require.Equal(t, "array<string,string>", constructor.Parameters[0].DocType.String())

	property := findSymbol(t, document, semantic.PropertySymbol, "endpoints")
	require.Equal(t, "array<string,string>", property.Type.String())
	require.Equal(t, "array<string,string>", property.DocType.String())
}

func TestBindShopwarePlannedMethodParameters(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
namespace App;

use Shopware\Core\Framework\Deprecation\BCChange\NewOptionalParameter;
use Shopware\Core\Framework\Deprecation\BCChange\NewRequiredParameter;

class Collection {}
class Service {
    #[NewOptionalParameter(
        version: 'v6.8.0',
        parameterName: 'collection',
        parameterType: '?' . Collection::class,
        defaultValue: null,
    )]
    #[NewRequiredParameter(
        version: 'v6.8.0',
        parameterName: 'enabled',
        parameterType: 'bool',
    )]
    public function load(string $name): void {}
}`).Tree.Root
	document := New().Bind("/planned-parameters.php", 1, root)
	method := findSymbol(t, document, semantic.MethodSymbol, "load")
	require.Len(t, method.Parameters, 3)
	require.Equal(t, "$name", method.Parameters[0].Name)
	require.Equal(t, "string", method.Parameters[0].Type.String())
	require.False(t, method.Parameters[0].Optional)
	require.Equal(t, "$collection", method.Parameters[1].Name)
	require.Equal(t, "App\\Collection|null", method.Parameters[1].Type.String())
	require.True(t, method.Parameters[1].Flags.Has(semantic.SyntheticFlag))
	require.True(t, method.Parameters[1].Optional)
	require.Equal(t, "$enabled", method.Parameters[2].Name)
	require.Equal(t, "bool", method.Parameters[2].Type.String())
	require.True(t, method.Parameters[2].Flags.Has(semantic.SyntheticFlag))
	require.False(t, method.Parameters[2].Optional)
}

func TestBindLocalFormsClosuresAndDeclarationReferences(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
namespace App;
#[Attribute]
class Service {}
class Consumer {
    public function run(Service $service): Service {
        $items = [$service];
        foreach ($items as $key => $item) {
            $format = function () use ($item): Service {
                return new Service();
            };
        }
        try {
            return $service;
        } catch (\RuntimeException $error) {
            throw $error;
        }
    }
}`).Tree.Root
	document := New().Bind("/locals.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	linked := Link(document, snapshot)

	findSymbol(t, linked, semantic.ClosureSymbol, "{closure}")
	for _, name := range []string{"$items", "$key", "$item", "$format", "$error"} {
		findSymbol(t, linked, semantic.LocalSymbol, name)
	}
	var serviceReferences, resolvedVariables int
	for _, reference := range linked.References {
		qualified := reference.QualifiedNames()
		if reference.Kind == semantic.ClassName &&
			len(qualified) > 0 &&
			qualified[0] == "App\\Service" {
			serviceReferences++
			require.NotEmpty(t, reference.Resolved)
		}
		if reference.Kind == semantic.VariableName && reference.Resolved != "" {
			resolvedVariables++
		}
	}
	require.GreaterOrEqual(t, serviceReferences, 3)
	require.GreaterOrEqual(t, resolvedVariables, 5)
}

func TestBindTraitMethodAliases(t *testing.T) {
	t.Parallel()
	document := New().Bind(
		"/trait-alias.php",
		1,
		phpparser.Parse(`<?php
namespace App;
use Shared\Reusable;
class Consumer {
    use Reusable {
        Reusable::value as private aliasedValue;
        Reusable::hidden as public;
    }
}`).Tree.Root,
	)
	class := findSymbol(t, document, semantic.ClassSymbol, "Consumer")
	require.Equal(t, []semantic.TraitAlias{
		{
			Trait:         "Shared\\Reusable",
			Method:        "value",
			Alias:         "aliasedValue",
			Visibility:    semantic.Private,
			HasVisibility: true,
		},
		{
			Trait:         "Shared\\Reusable",
			Method:        "hidden",
			Alias:         "hidden",
			Visibility:    semantic.Public,
			HasVisibility: true,
		},
	}, class.TraitAliases())
}

func TestArrayElementAssignmentDeclaresLocal(t *testing.T) {
	t.Parallel()
	root := phpparser.Parse(`<?php
function collect(string $key): array {
    $items[$key] = 'first';
    $items[$key] = 'second';
    return $items;
}`).Tree.Root
	document := New().Bind("/array-assignment.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	linked := Link(document, snapshot)

	findSymbol(t, linked, semantic.LocalSymbol, "$items")
	var resolved int
	for _, reference := range linked.References {
		if reference.Kind == semantic.VariableName &&
			reference.Name == "$items" && reference.Resolved != "" {
			resolved++
		}
	}
	require.Equal(t, 2, resolved)
}

func TestBindClassAliasDeclarationsAndTargets(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;
use Vendor\Original;
class_alias(Original::class, \Legacy\AliasName::class);
class_alias('Vendor\Other', 'Legacy\OtherAlias');
class_alias($dynamic, 'Legacy\DynamicAlias');
`
	parsed := phpparser.Parse(source)
	require.Empty(t, parsed.Errors)
	document := New().Bind("/class-alias.php", 1, parsed.Tree.Root)

	aliases := make(map[string]semantic.Symbol)
	for _, symbol := range document.Symbols {
		if symbol.Flags.Has(semantic.ClassAliasFlag) {
			aliases[symbol.FullyQualified] = symbol
		}
	}
	require.Len(t, aliases, 2)
	require.Equal(t, []string{"Vendor\\Original"}, aliases["Legacy\\AliasName"].Extends())
	require.Equal(t, []string{"Vendor\\Other"}, aliases["Legacy\\OtherAlias"].Extends())
	require.True(t, aliases["Legacy\\AliasName"].Flags.Has(semantic.SyntheticFlag))
	require.Equal(
		t,
		"\\Legacy\\AliasName",
		source[aliases["Legacy\\AliasName"].SelectionRange.Start:aliases["Legacy\\AliasName"].SelectionRange.End],
	)

	var literalTarget bool
	for _, reference := range document.References {
		if reference.Kind == semantic.ClassName &&
			reference.Name == "Vendor\\Other" &&
			reference.QualifiedNameCount() == 1 &&
			reference.QualifiedNameAt(0) == "Vendor\\Other" {
			literalTarget = true
		}
	}
	require.True(t, literalTarget)
}

func findSymbol(
	t *testing.T,
	document *semantic.Document,
	kind semantic.SymbolKind,
	name string,
) semantic.Symbol {
	t.Helper()
	for _, symbol := range document.Symbols {
		if symbol.Kind == kind && symbol.Name == name {
			return symbol
		}
	}
	t.Fatalf("missing %v symbol %q", kind, name)
	return semantic.Symbol{}
}
