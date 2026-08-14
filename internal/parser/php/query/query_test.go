package query

import (
	"strings"
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

func TestAttributesAndNamedArguments(t *testing.T) {
	root := phpparser.Parse(`<?php
#[Route(path: '/api', name: 'api')]
class C {
    #[Route('/x', name: 'x')]
    public function x(?string $value = null): void {}
}`).Tree.Root
	class := Classes(root)[0]
	attributes := Attributes(class)
	if len(attributes) != 1 || AttributeName(attributes[0]) != "Route" {
		t.Fatalf("class attributes = %#v", attributes)
	}
	args := Arguments(attributes[0])
	if len(args) != 2 || ArgumentName(args[0]) != "path" || ArgumentName(args[1]) != "name" {
		t.Fatalf("attribute args = %#v", args)
	}
	iterator := IterateArguments(attributes[0])
	if iterator.Len() != 2 || !iterator.Next() ||
		ArgumentName(iterator.Node()) != "path" || !iterator.Next() ||
		ArgumentName(iterator.Node()) != "name" || iterator.Next() {
		t.Fatal("argument iterator did not preserve source order")
	}
	method := Methods(class)[0]
	parameters := Parameters(method)
	if len(parameters) != 1 || ParameterName(parameters[0]) != "$value" ||
		ParameterType(parameters[0]) != "?string" || !ParameterOptional(parameters[0]) ||
		ParameterDefault(parameters[0]) == nil ||
		strings.TrimSpace(ParameterDefault(parameters[0]).Text()) != "null" {
		t.Fatalf("parameter was not parsed: %#v", parameters)
	}
	parameterIterator := IterateParameters(method)
	if parameterIterator.Len() != 1 || !parameterIterator.Next() ||
		ParameterName(parameterIterator.Node()) != "$value" ||
		parameterIterator.Next() {
		t.Fatal("parameter iterator did not preserve source order")
	}
}

func TestScopedAccessClassSupportsArbitraryConstants(t *testing.T) {
	root := phpparser.Parse(`<?php
class C {
    public function target(): string { return ProductDefinition::ENTITY_NAME; }
}`).Tree.Root
	methods := Methods(Classes(root)[0])
	if len(methods) != 1 {
		t.Fatalf("methods = %d", len(methods))
	}
	accesses := Nodes(methods[0], syntax.PhpScopedAccess, syntax.PhpMemberAccess)
	if len(accesses) != 1 {
		t.Fatalf("scoped accesses = %d\n%s", len(accesses), syntax.DebugTree(root))
	}
	if got := ScopedAccessClass(accesses[0], "ENTITY_NAME"); got != "ProductDefinition" {
		t.Fatalf("scoped access class = %q", got)
	}
}

func TestArgumentNameStopsAtNestedPositionalArgument(t *testing.T) {
	root := phpparser.Parse(`<?php
configure(fields: [new Type('string')]);
`).Tree.Root
	calls := Nodes(root, syntax.PhpObjectCreation)
	if len(calls) != 1 {
		t.Fatalf("Type calls = %d", len(calls))
	}
	arguments := Arguments(calls[0])
	if len(arguments) != 1 {
		t.Fatalf("Type arguments = %d", len(arguments))
	}
	if got := ArgumentName(arguments[0]); got != "" {
		t.Fatalf("nested positional argument name = %q, want empty", got)
	}
	expression := ArgumentExpression(calls[0], 0)
	if got := ArgumentName(expression); got != "" {
		t.Fatalf("nested expression argument name = %q, want empty", got)
	}
}

func TestNameValueExcludesLeadingCommentTrivia(t *testing.T) {
	root := phpparser.Parse(`<?php
// explanation
self::run();
`).Tree.Root
	calls := Nodes(root, syntax.PhpScopedCall)
	if len(calls) != 1 {
		t.Fatalf("scoped calls = %d", len(calls))
	}
	name := DirectChild(calls[0], syntax.PhpName)
	if got := NameValue(name); got != "self" {
		t.Fatalf("name = %q, want self", got)
	}
}

func TestDeclarationVisibilityIgnoresAttributeArguments(t *testing.T) {
	root := phpparser.Parse(`<?php
class C {
    #[Route('/private')]
    private function hidden(): void {}

    #[Route('/public')]
    public function visible(): void {}
}`).Tree.Root
	methods := Methods(Classes(root)[0])
	if visibility := DeclarationVisibility(methods[0]); visibility != "private" {
		t.Fatalf("private visibility = %q", visibility)
	}
	if visibility := DeclarationVisibility(methods[1]); visibility != "public" {
		t.Fatalf("public visibility = %q", visibility)
	}
}

func TestParameterVariadic(t *testing.T) {
	result := phpparser.Parse(`<?php
function collect(string $first, mixed ...$rest): void {}
`)
	functions := Functions(result.Tree.Root)
	if len(functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(functions))
	}
	parameters := Parameters(functions[0])
	if len(parameters) != 2 {
		t.Fatalf("parameters = %d, want 2", len(parameters))
	}
	if ParameterVariadic(parameters[0]) {
		t.Fatal("ordinary parameter reported as variadic")
	}
	if !ParameterVariadic(parameters[1]) {
		t.Fatal("variadic parameter was not recognized")
	}
}

func TestVariableNameIgnoresLeadingComments(t *testing.T) {
	root := phpparser.Parse(`<?php
function run(): void {
    // The comment is trivia owned by the following variable node.
    $value = 1;
}
`).Tree.Root
	variables := Nodes(root, syntax.PhpVariable)
	if len(variables) != 1 {
		t.Fatalf("variables = %d, want 1", len(variables))
	}
	if name := VariableName(variables[0]); name != "value" {
		t.Fatalf("variable name = %q, want value", name)
	}
	if key := VariableKey(variables[0]); key != "$value" {
		t.Fatalf("variable key = %q, want $value", key)
	}
}

func TestStringInMemberCall(t *testing.T) {
	root := phpparser.Parse(`<?php $this->trans('key'); $other->trans('no');`).Tree.Root
	calls := Calls(root)
	if len(calls) != 2 {
		t.Fatalf("calls = %d", len(calls))
	}
	string := StringArgument(calls[0], 0)
	if !StringInCall(string, 0, "trans") || StringValue(string) != "key" {
		t.Fatal("member call string context not recognized")
	}
	if receiver := CallReceiver(calls[0]); receiver == nil ||
		strings.TrimSpace(receiver.Text()) != "$this" {
		t.Fatalf("member call receiver = %#v", receiver)
	}
}

func TestCallMethodNameUsesDirectCallTarget(t *testing.T) {
	root := phpparser.Parse(`<?php
$service->first()->second();
Factory::create();
plain();
$service /* receiver */ -> /* target */ commented();
`).Tree.Root
	calls := Calls(root)
	if len(calls) != 5 {
		t.Fatalf("calls = %d", len(calls))
	}
	expected := []string{"second", "first", "create", "plain", "commented"}
	for index, call := range calls {
		if actual := CallMethodName(call); actual != expected[index] {
			t.Fatalf(
				"call %d method = %q, want %q",
				index,
				actual,
				expected[index],
			)
		}
	}

	allocations := testing.AllocsPerRun(1000, func() {
		if CallMethodName(calls[0]) != "second" {
			panic("unexpected method name")
		}
	})
	if allocations != 0 {
		t.Fatalf("CallMethodName allocations = %v, want 0", allocations)
	}
}

func TestVisitWalksMatchesInOrderAndCanStop(t *testing.T) {
	t.Parallel()

	root := phpparser.Parse(`<?php first(); second(); third();`).Tree.Root
	var names []string
	complete := Visit(root, func(call *syntax.Node) bool {
		names = append(names, CallMethodName(call))
		return len(names) < 2
	}, syntax.PhpFunctionCall)

	if complete {
		t.Fatal("Visit reported a complete traversal after the callback stopped it")
	}
	if strings.Join(names, ",") != "first,second" {
		t.Fatalf("visited calls = %v, want [first second]", names)
	}
}

func TestObjectCreationArgumentsDoNotUseEnclosingCall(t *testing.T) {
	root := phpparser.Parse(`<?php consume(new Product('value'));`).Tree.Root
	objects := Nodes(root, syntax.PhpObjectCreation)
	if len(objects) != 1 {
		t.Fatalf("object creations = %d", len(objects))
	}
	arguments := Arguments(objects[0])
	if len(arguments) != 1 || ArgumentValueText(objects[0], 0) != "'value'" {
		t.Fatalf("object arguments = %#v", arguments)
	}
}

func TestPositionalNestedArrayItemIsNotTreatedAsKeyed(t *testing.T) {
	root := phpparser.Parse(`<?php
return [
    ['resource' => 'legacy.php'],
];
`).Tree.Root
	arrays := Arrays(root)
	if len(arrays) != 2 {
		t.Fatalf("arrays = %d, want 2", len(arrays))
	}
	items := ArrayItems(arrays[0])
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if key := ArrayItemKey(items[0]); key != nil {
		t.Fatalf("positional nested array key = %#v", key)
	}
	value := ArrayItemValue(items[0])
	if value == nil || value.Range() != arrays[1].Range() {
		t.Fatalf("positional nested array value = %#v", value)
	}
}

func TestAssociativeArrayKey(t *testing.T) {
	root := phpparser.Parse(
		`<?php $this->redirectToRoute('product.show', ['id' => 1, 'plain']);`,
	).Tree.Root
	call := Calls(root)[0]
	array := ArrayAt(ArgumentExpression(call, 1))
	items := ArrayItems(array)
	if len(items) != 2 {
		t.Fatalf("array items = %#v", items)
	}
	key := ArrayItemKey(items[0])
	if key == nil || StringValue(key) != "id" || ArrayItemAt(key) != items[0] {
		t.Fatalf("associative key not recognized: %#v", key)
	}
	if value := ArrayItemValue(items[0]); value == nil ||
		strings.TrimSpace(value.Text()) != "1" {
		t.Fatalf("associative value not recognized: %#v", value)
	}
	if ArrayItemKey(items[1]) != nil {
		t.Fatal("list-style array item must not have a key")
	}
	if value := ArrayItemValue(items[1]); value == nil ||
		StringValue(value) != "plain" {
		t.Fatalf("list value not recognized: %#v", value)
	}
	keyRange := StringContentRange(key)
	if source := root.Text(); source[keyRange.Start:keyRange.End] != "id" {
		t.Fatalf("string content range = %q", source[keyRange.Start:keyRange.End])
	}
}

func TestAssignmentAndClassConstantQueries(t *testing.T) {
	root := phpparser.Parse(`<?php
$services = $container->services();
$services->set(Foo\Bar::class . '.inner');
`).Tree.Root
	statements := ExpressionStatements(root)
	if len(statements) != 2 || AssignedVariable(statements[0]) != "$services" {
		t.Fatalf("assignment query failed: %#v", statements)
	}
	if value := AssignmentValue(statements[0]); value == nil || strings.TrimSpace(value.Text()) != "$container->services()" {
		t.Fatalf("assignment value = %#v (%q)", value, value.Text())
	}
	calls := Calls(statements[1])
	if len(calls) == 0 {
		t.Fatal("set call not parsed")
	}
	if value := ClassConstantName(Argument(calls[0], 0)); value != `Foo\Bar` {
		t.Fatalf("class constant = %q", value)
	}
	if value := ArgumentValueText(calls[0], 0); value != `Foo\Bar::class . '.inner'` {
		t.Fatalf("argument value text = %q", value)
	}
}

func TestClassKinds(t *testing.T) {
	root := phpparser.Parse(`<?php
abstract class Base {}
trait Reusable {}
enum Status {}
interface Contract {}
`).Tree.Root
	classes := Classes(root)
	if len(classes) != 4 || !IsAbstract(classes[0]) || !IsTrait(classes[1]) ||
		!IsEnum(classes[2]) || !IsInterface(classes[3]) {
		t.Fatalf("class kinds were not preserved: %#v", classes)
	}
}
