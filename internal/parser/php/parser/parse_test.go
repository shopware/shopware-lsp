package parser

import (
	"testing"

	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

func TestParseDeclarationsAndCalls(t *testing.T) {
	source := `<?php
namespace App\Controller;
use Symfony\Component\Routing\Attribute\Route;

#[Route(path: '/api')]
final class Demo extends Base implements One, Two
{
    private readonly Service $service;

    #[Route('/show', name: 'demo.show')]
    public function show(string $id = 'x'): array
    {
        return $this->renderStorefront('@Demo/show.html.twig', ['id' => $id]);
    }
}`
	result := Parse(source)
	if result.Tree.Root == nil {
		t.Fatal("missing root")
	}
	classes := phpquery.Classes(result.Tree.Root)
	if len(classes) != 1 {
		t.Fatalf("classes = %d, want 1\n%s", len(classes), syntax.DebugTree(result.Tree.Root))
	}
	class := classes[0]
	if phpquery.ClassName(class) != "Demo" {
		t.Fatalf("class name = %q", phpquery.ClassName(class))
	}
	if got := phpquery.ClassExtends(class); len(got) != 1 || got[0] != "Base" {
		t.Fatalf("extends = %#v", got)
	}
	if got := phpquery.ClassImplements(class); len(got) != 2 {
		t.Fatalf("implements = %#v", got)
	}
	methods := phpquery.Methods(class)
	if len(methods) != 1 || phpquery.MethodName(methods[0]) != "show" {
		t.Fatalf("methods = %#v\n%s", methods, syntax.DebugTree(result.Tree.Root))
	}
	if got := phpquery.MethodReturnType(methods[0]); got != "array" {
		t.Fatalf("return type = %q", got)
	}
	calls := phpquery.Calls(methods[0], "$this->renderStorefront")
	if len(calls) != 1 {
		t.Fatalf("calls = %d\n%s", len(calls), syntax.DebugTree(result.Tree.Root))
	}
	if got := phpquery.StringValue(phpquery.StringArgument(calls[0], 0)); got != "@Demo/show.html.twig" {
		t.Fatalf("template = %q", got)
	}
}

func TestParseIncompletePHP(t *testing.T) {
	result := Parse(`<?php class Demo { public function show( { return $this->trans('`)
	if result.Tree.Root == nil || result.Tree.Root.Text() == "" {
		t.Fatal("incomplete input must still produce a lossless CST")
	}
}

func TestParseByReferenceVariadicParameters(t *testing.T) {
	source := `<?php
function typed(mixed &...$values): void {}
function untyped(&...$values) {}
`
	result := Parse(source)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected parser errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}
	if result.Tree.Root.Text() != source {
		t.Fatal("by-reference variadic CST must remain lossless")
	}
	expectNodes(t, result.Tree.Root, syntax.PhpParameter, 2)
}

func TestParseArrayUnpacking(t *testing.T) {
	source := `<?php
$modern = [...$defaults, ...['field' => $name]];
$legacy = array(...$defaults, ...array('field' => $name));
`
	result := Parse(source)
	if len(result.Errors) != 0 {
		t.Fatalf(
			"unexpected parser errors: %v\n%s",
			result.Errors,
			syntax.DebugTree(result.Tree.Root),
		)
	}
	if result.Tree.Root.Text() != source {
		t.Fatal("PHP CST must remain lossless")
	}

	arrays := phpquery.Arrays(result.Tree.Root)
	if len(arrays) != 4 {
		t.Fatalf("arrays = %d, want 4", len(arrays))
	}
	for _, index := range []int{0, 1, 2, 3} {
		items := phpquery.ArrayItems(arrays[index])
		if len(items) == 0 {
			t.Fatalf("array %d has no items", index)
		}
	}
}

func TestParseBracedDynamicMemberAccess(t *testing.T) {
	source := `<?php
$value = (string) ($object->{$field} ?? '');
$result = $object->{$method}();
`
	result := Parse(source)
	if len(result.Errors) != 0 {
		t.Fatalf(
			"unexpected parser errors: %v\n%s",
			result.Errors,
			syntax.DebugTree(result.Tree.Root),
		)
	}
	if result.Tree.Root.Text() != source {
		t.Fatal("PHP CST must remain lossless")
	}
	if got := len(phpquery.Nodes(
		result.Tree.Root,
		syntax.PhpMemberAccess,
		syntax.PhpMemberCall,
	)); got != 2 {
		t.Fatalf("dynamic members = %d, want 2", got)
	}
}

func TestParseStaticPropertyExpressions(t *testing.T) {
	source := `<?php
class Registry {
    private static array $values = [];

    public static function add(string $value): void {
        static::$values[] = $value;
        self::$values = [];
        $copy = static::$values;
    }
}`
	result := Parse(source)
	if len(result.Errors) != 0 {
		t.Fatalf(
			"unexpected parser errors: %v\n%s",
			result.Errors,
			syntax.DebugTree(result.Tree.Root),
		)
	}
	if result.Tree.Root.Text() != source {
		t.Fatal("PHP CST must remain lossless")
	}
	if got := len(phpquery.Nodes(
		result.Tree.Root,
		syntax.PhpMemberAccess,
	)); got != 3 {
		t.Fatalf(
			"static property accesses = %d, want 3\n%s",
			got,
			syntax.DebugTree(result.Tree.Root),
		)
	}
	if got := len(phpquery.Nodes(
		result.Tree.Root,
		syntax.PhpStaticStatement,
	)); got != 0 {
		t.Fatalf("static statements = %d, want 0", got)
	}
}

func TestPropertyAttributesWithArgumentsStayAttached(t *testing.T) {
	source := `<?php
use Doctrine\ORM\Mapping as ORM;
class Product {
    #[ORM\Column(type: "string", length: 255)]
    private $name;

    #[ORM\ManyToOne(targetEntity: Category::class)]
    private ?Category $category = null;
}`
	result := Parse(source)
	if len(result.Errors) != 0 {
		t.Fatalf(
			"unexpected parser errors: %v\n%s",
			result.Errors,
			syntax.DebugTree(result.Tree.Root),
		)
	}
	class := phpquery.Classes(result.Tree.Root)[0]
	properties := phpquery.Properties(class)
	if len(properties) != 2 {
		t.Fatalf("properties = %d, want 2", len(properties))
	}
	first := phpquery.Attributes(properties[0])
	second := phpquery.Attributes(properties[1])
	if len(first) != 1 || phpquery.AttributeName(first[0]) != `ORM\Column` {
		t.Fatalf("first property attributes = %#v", first)
	}
	if len(second) != 1 ||
		phpquery.AttributeName(second[0]) != `ORM\ManyToOne` {
		t.Fatalf("second property attributes = %#v", second)
	}
}

func TestParseStructuredModernPHP(t *testing.T) {
	source := `<?php
namespace App;

use Countable;
use Traversable;

function analyze(?Service $service, Countable&Traversable $input): (Result&Countable)|null
{
    $result = $service?->load($input) ?? new Result();
    $mapper = static fn (Result $item): string => $item->name ?? 'unknown';
    $filter = function (Result $item) use (&$result): bool {
        return $item instanceof Result && $result !== null;
    };

    if ($result instanceof Result && $input !== null) {
        foreach ($result->items as $key => $item) {
            $result[$key] = $mapper($item);
        }
    } elseif ($service === null) {
        throw new RuntimeException('missing service');
    } else {
        return match ($result->status) {
            Status::Ready, Status::Pending => $result,
            default => null,
        };
    }

    try {
        return $filter($result) ? $result : null;
    } catch (RuntimeException|LogicException $error) {
        throw $error;
    } finally {
        $service?->close();
    }
}`
	result := Parse(source)
	if result.Tree.Root == nil {
		t.Fatal("missing root")
	}
	if result.Tree.Root.Text() != source {
		t.Fatal("PHP CST must remain lossless")
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected parser errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}

	expectNodes(t, result.Tree.Root, syntax.PhpFunctionDeclaration, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpAssignmentExpression, 4)
	expectNodes(t, result.Tree.Root, syntax.PhpArrowFunction, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpClosure, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpIfStatement, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpForeachStatement, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpArrayAccess, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpMatchExpression, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpTryStatement, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpCatchClause, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpFinallyClause, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpTernaryExpression, 1)
}

func TestParseClassSemanticDeclarations(t *testing.T) {
	source := `<?php
trait TracksChanges { public function changed(): bool { return true; } }
enum State: string { case Open = 'open'; case Closed = 'closed'; }
final class Entity {
    use TracksChanges { changed as private traitChanged; }
    public const KIND = 'entity';
    public function __construct(public readonly string $id) {}
}`
	result := Parse(source)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected parser errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}
	expectNodes(t, result.Tree.Root, syntax.PhpTraitUseDeclaration, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpClassConstDeclaration, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpEnumCaseDeclaration, 2)
}

func TestParsePHP8OperatorPrecedence(t *testing.T) {
	source := `<?php
if (!$scope instanceof CheckoutScope) {}
if (!$value = load()) {}
$assigned = true and false;
$ready && $conditional = load();
$postfix++;
`
	result := Parse(source)
	if len(result.Errors) != 0 {
		t.Fatalf(
			"unexpected parser errors: %v\n%s",
			result.Errors,
			syntax.DebugTree(result.Tree.Root),
		)
	}

	ifStatements := phpquery.Nodes(result.Tree.Root, syntax.PhpIfStatement)
	if len(ifStatements) != 2 {
		t.Fatalf("if statements = %d, want 2", len(ifStatements))
	}
	firstCondition := phpquery.Nodes(
		ifStatements[0],
		syntax.PhpParenthesized,
	)[0]
	firstUnary := phpquery.Nodes(firstCondition, syntax.PhpUnaryExpression)
	if len(firstUnary) != 1 ||
		len(phpquery.Nodes(firstUnary[0], syntax.PhpBinaryExpression)) != 1 {
		t.Fatalf(
			"instanceof must bind beneath !:\n%s",
			syntax.DebugTree(firstCondition),
		)
	}
	secondCondition := phpquery.Nodes(
		ifStatements[1],
		syntax.PhpParenthesized,
	)[0]
	secondUnary := phpquery.Nodes(secondCondition, syntax.PhpUnaryExpression)
	if len(secondUnary) != 1 ||
		len(phpquery.Nodes(secondUnary[0], syntax.PhpAssignmentExpression)) != 1 {
		t.Fatalf(
			"assignment must bind beneath !:\n%s",
			syntax.DebugTree(secondCondition),
		)
	}

	statements := phpquery.Nodes(result.Tree.Root, syntax.PhpExpressionStatement)
	if len(statements) != 3 {
		t.Fatalf("expression statements = %d, want 3", len(statements))
	}
	assignment := phpquery.Nodes(
		statements[0],
		syntax.PhpAssignmentExpression,
	)
	if len(assignment) != 1 ||
		len(phpquery.Nodes(assignment[0], syntax.PhpBinaryExpression)) != 0 {
		t.Fatalf(
			"word and must bind outside assignment:\n%s",
			syntax.DebugTree(statements[0]),
		)
	}
	conditional := phpquery.Nodes(
		statements[1],
		syntax.PhpBinaryExpression,
	)
	if len(conditional) != 1 ||
		len(phpquery.Nodes(
			conditional[0],
			syntax.PhpAssignmentExpression,
		)) != 1 {
		t.Fatalf(
			"assignment must bind as the right operand of &&:\n%s",
			syntax.DebugTree(statements[1]),
		)
	}
	postfix := phpquery.Nodes(statements[2], syntax.PhpUnaryExpression)
	if len(postfix) != 1 ||
		len(phpquery.Nodes(postfix[0], syntax.PhpVariable)) != 1 {
		t.Fatalf(
			"postfix increment must contain its operand:\n%s",
			syntax.DebugTree(statements[2]),
		)
	}
}

func TestParsePHP83And84Declarations(t *testing.T) {
	source := `<?php
#[Service]
function build(#[SensitiveParameter] string $name): object {
    return new class($name) {};
}

class Modern {
    public const string KIND = 'modern';

    public private(set) string $name {
        get => $this->name;
        set(string $value) { $this->name = $value; }
    }
}`
	result := Parse(source)
	if result.Tree.Root.Text() != source {
		t.Fatal("modern PHP CST must remain lossless")
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected parser errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}
	expectNodes(t, result.Tree.Root, syntax.PhpFunctionDeclaration, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpClassConstDeclaration, 1)
	requireNodeCount(t, result.Tree.Root, syntax.PhpPropertyHookList, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpPropertyHook, 2)
	requireNodeCount(t, result.Tree.Root, syntax.PhpMethodDeclaration, 0)

	function := phpquery.Functions(result.Tree.Root)[0]
	if got := phpquery.ParameterType(phpquery.Parameters(function)[0]); got != "string" {
		t.Fatalf("attributed parameter type = %q", got)
	}
	class := phpquery.Classes(result.Tree.Root)[0]
	properties := phpquery.Properties(class)
	if len(properties) != 1 || phpquery.PropertyType(properties[0]) != "string" {
		t.Fatalf("property type = %q", phpquery.PropertyType(properties[0]))
	}
}

func TestParseDNFPropertyType(t *testing.T) {
	source := `<?php
class Typed {
    private (Serializable&Countable)|Iterator $value;
}`
	result := Parse(source)
	if result.Tree.Root.Text() != source {
		t.Fatal("DNF property CST must remain lossless")
	}
	if len(result.Errors) != 0 {
		t.Fatalf(
			"unexpected parser errors: %v\n%s",
			result.Errors,
			syntax.DebugTree(result.Tree.Root),
		)
	}
	class := phpquery.Classes(result.Tree.Root)[0]
	properties := phpquery.Properties(class)
	if len(properties) != 1 {
		t.Fatalf("properties = %d", len(properties))
	}
	if got := phpquery.PropertyType(properties[0]); got != "(Serializable&Countable)|Iterator" {
		t.Fatalf("property type = %q", got)
	}
}

func TestParseHeredocAndNowdoc(t *testing.T) {
	source := "<?php\n$a = <<<TEXT\nhello $name\nTEXT;\n" +
		"$b = <<<'RAW'\nliteral $name\nRAW;\n"
	result := Parse(source)
	if result.Tree.Root.Text() != source {
		t.Fatal("heredoc CST must remain lossless")
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected parser errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}
	expectNodes(t, result.Tree.Root, syntax.PhpString, 2)
}

func TestParseIndentedHeredocAsCallArgument(t *testing.T) {
	source := `<?php
$statement = $connection->prepare(<<<'SQL'
    SELECT *
    FROM product
    SQL);
$after = true;
`
	result := Parse(source)
	if result.Tree.Root.Text() != source {
		t.Fatal("indented heredoc CST must remain lossless")
	}
	if len(result.Errors) != 0 {
		t.Fatalf(
			"unexpected parser errors: %v\n%s",
			result.Errors,
			syntax.DebugTree(result.Tree.Root),
		)
	}
	expectNodes(t, result.Tree.Root, syntax.PhpString, 1)
}

func TestParseAlternativeControlFlowSyntax(t *testing.T) {
	source := `<?php
if ($ready):
    echo 'ready';
elseif ($waiting):
    echo 'waiting';
else:
    echo 'no';
endif;
foreach ($items as $item):
    echo $item;
endforeach;
while ($running):
    break;
endwhile;
for ($i = 0; $i < 2; $i++):
    echo $i;
endfor;
switch ($state):
    case 'open':
        break;
    default:
        break;
endswitch;
`
	result := Parse(source)
	if result.Tree.Root.Text() != source {
		t.Fatal("alternative syntax CST must remain lossless")
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected parser errors: %v\n%s", result.Errors, syntax.DebugTree(result.Tree.Root))
	}
	expectNodes(t, result.Tree.Root, syntax.PhpIfStatement, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpForeachStatement, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpWhileStatement, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpForStatement, 1)
	expectNodes(t, result.Tree.Root, syntax.PhpSwitchStatement, 1)
}

func FuzzPHPParserLossless(f *testing.F) {
	for _, source := range []string{
		"",
		"<?php",
		"<?php $value = [1, 2, 3];",
		"<?php class Broken { public function run(",
		"<?php #[A(x: [1])] readonly class C {}",
		"<?php function f((A&B)|null $x): never { throw new E(); }",
	} {
		f.Add(source)
	}
	f.Fuzz(func(t *testing.T, source string) {
		result := Parse(source)
		if result.Tree == nil || result.Tree.Root == nil {
			t.Fatal("parser returned no CST")
		}
		if result.Tree.Root.Text() != source {
			t.Fatalf("lossless invariant failed: got %q, want %q", result.Tree.Root.Text(), source)
		}
	})
}

func expectNodes(t *testing.T, root *syntax.Node, kind syntax.Kind, minimum int) {
	t.Helper()
	nodes := phpquery.Nodes(root, kind)
	if len(nodes) < minimum {
		t.Fatalf("%s nodes = %d, want at least %d\n%s", kind, len(nodes), minimum, syntax.DebugTree(root))
	}
}

func requireNodeCount(t *testing.T, root *syntax.Node, kind syntax.Kind, expected int) {
	t.Helper()
	nodes := phpquery.Nodes(root, kind)
	if len(nodes) != expected {
		t.Fatalf("%s nodes = %d, want %d\n%s", kind, len(nodes), expected, syntax.DebugTree(root))
	}
}
