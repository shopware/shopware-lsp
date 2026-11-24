//! Sections 4.1 to 4.3, 4.6, 8, 9 and 12: classes and their members.

use super::{fixed, formatted, stable};

#[test]
fn class_braces_stand_on_their_own_lines() {
    stable(
        "class ClassName extends ParentClass implements \\ArrayAccess, \\Countable
{
    // constants, properties, methods
}
",
    );
    fixed(
        "class ClassName\n    extends ParentClass\n    implements \\ArrayAccess, \\Countable {\n\n    public $a;\n\n}\n",
        "class ClassName extends ParentClass implements \\ArrayAccess, \\Countable\n{\n    public $a;\n}\n",
    );
}

#[test]
fn the_parent_of_a_class_stands_on_its_line() {
    fixed(
        "class A\n    extends\n        B\n    implements\n        C\n{\n    public $d;\n}\n",
        "class A extends B implements C\n{\n    public $d;\n}\n",
    );
}

#[test]
fn a_class_without_declarations_is_abbreviated() {
    stable("class MyException extends \\RuntimeException {}\n");
    fixed(
        "class MyException extends \\RuntimeException\n{\n}\n",
        "class MyException extends \\RuntimeException {}\n",
    );
}

#[test]
fn implements_lists_over_several_lines_hold_one_interface_per_line() {
    stable(
        "class ClassName extends ParentClass implements
    \\ArrayAccess,
    \\Countable,
    \\Serializable
{
    // constants, properties, methods
}
",
    );
    fixed(
        "class ClassName implements\n  \\ArrayAccess, \\Countable {\n}\n",
        "class ClassName implements\n    \\ArrayAccess,\n    \\Countable {}\n",
    );
}

#[test]
fn each_trait_has_its_own_use_and_stands_apart_from_what_follows() {
    stable(
        "class ClassName
{
    use FirstTrait;
    use SecondTrait;
    use ThirdTrait;
}
",
    );
    stable(
        "class ClassName
{
    use FirstTrait;

    private $property;
}
",
    );
    fixed(
        "class ClassName\n{\n\n    use FirstTrait, SecondTrait;\n    private $property;\n}\n",
        "class ClassName\n{\n    use FirstTrait;\n    use SecondTrait;\n\n    private $property;\n}\n",
    );
}

#[test]
fn trait_adaptations_stand_one_per_line() {
    stable(
        "class Talker
{
    use A;
    use B {
        A::smallTalk insteadof B;
    }
    use C {
        B::bigTalk insteadof C;
        C::mediumTalk as FooBar;
    }
}
",
    );
    fixed(
        "class Talker\n{\n    use C { B::bigTalk insteadof C; C::mediumTalk as FooBar; }\n}\n",
        "class Talker\n{\n    use C {\n        B::bigTalk insteadof C;\n        C::mediumTalk as FooBar;\n    }\n}\n",
    );
}

#[test]
fn properties_and_constants_have_a_visibility_and_one_declaration_each() {
    stable(
        "class ClassName
{
    public $foo = null;
    public static int $bar = 0;
}
",
    );
    fixed(
        "class ClassName\n{\n    var $foo = null, $baz;\n    static int $bar = 0;\n    const A = 1;\n    function f();\n}\n",
        "class ClassName\n{\n    public $foo = null;\n    public $baz;\n    public static int $bar = 0;\n    public const A = 1;\n    public function f();\n}\n",
    );
}

#[test]
fn modifiers_stand_in_order_on_one_line() {
    stable(
        "abstract class ClassName
{
    protected static readonly string $foo;

    final protected int $beep;

    abstract protected function zim();

    final public static function bar()
    {
        // method body
    }
}

readonly class ValueObject
{
    // ...
}
",
    );
    fixed(
        "abstract class ClassName\n{\n    static readonly protected string $foo;\n    protected final int $beep;\n    protected abstract function zim();\n    static public final\n        function bar() {\n    }\n}\n",
        "abstract class ClassName\n{\n    protected static readonly string $foo;\n    final protected int $beep;\n    abstract protected function zim();\n    final public static function bar() {}\n}\n",
    );
}

#[test]
fn anonymous_classes_keep_their_brace_on_the_line_unless_interfaces_wrap() {
    stable("$instance = new class {};\n");
    stable(
        "// Brace on the same line
// No arguments
$instance = new class extends \\Foo implements \\HandleableInterface {
    // Class content
};

// Brace on the next line
// Constructor arguments
$instance = new class ($a) extends \\Foo implements
    \\ArrayAccess,
    \\Countable,
    \\Serializable
{
    public function __construct(public int $a) {}
    // Class content
};
",
    );
    fixed(
        "$instance = new class() extends \\Foo\n{\n    public $a;\n};\n$b = new Foo;\n",
        "$instance = new class extends \\Foo {\n    public $a;\n};\n$b = new Foo();\n",
    );
}

#[test]
fn enumerations_follow_classes() {
    stable(
        "enum Suit: string
{
    case Hearts = 'H';
    case Diamonds = 'D';
    case Spades = 'S';
    case Clubs = 'C';

    public const Wild = self::Spades;
}
",
    );
    fixed(
        "enum Suit : String { case Hearts = 'H'; case Spades = 'S'; }\n",
        "enum Suit: string\n{\n    case Hearts = 'H';\n    case Spades = 'S';\n}\n",
    );
}

#[test]
fn attributes_stand_above_what_they_describe() {
    stable(
        "#[Foo]
#[Bar('baz')]
class Demo
{
    #[Beep]
    private Foo $foo;

    public function __construct(
        #[Load(context: 'foo', bar: true)]
        private readonly FooService $fooService,

        #[LoadProxy(context: 'bar')]
        private readonly BarService $barService,
    ) {}

    /**
     * Sets the foo.
     */
    #[Poink('narf'), Narf('poink')]
    public function setFoo(#[Beep] Foo $new): void
    {
        // ...
    }

    #[Complex(
        prop: 'val',
        other: 5,
    )]
    #[Other, Stuff]
    #[Here]
    public function complicated(
        string $a,

        #[Decl]
        string $b,

        #[Complex(
            prop: 'val',
            other: 5,
        )]
        string $c,

        int $d,
    ): string {
        // ...
    }
}
",
    );
    fixed(
        "#[Foo()] #[Bar( 'baz' )] class Demo\n{\n    #[Beep] private Foo $foo;\n\n    #[A,\n      B]\n    public function f(#[Beep]Foo $new): void {}\n}\n",
        "#[Foo]\n#[Bar('baz')]\nclass Demo\n{\n    #[Beep]\n    private Foo $foo;\n\n    #[A]\n    #[B]\n    public function f(#[Beep] Foo $new): void {}\n}\n",
    );
}

#[test]
fn a_closing_brace_has_its_line_to_itself() {
    fixed(
        "class A\n{\n    public function f()\n    {\n        return 1;\n    } // f\n} // A\n",
        "class A\n{\n    public function f()\n    {\n        return 1;\n    }\n    // f\n}\n// A\n",
    );
}

#[test]
fn a_docblock_stands_right_above_the_attributes() {
    fixed(
        "/**\n * Doc.\n */\n\n#[Foo]\n\nclass A {}\n",
        "/**\n * Doc.\n */\n#[Foo]\nclass A {}\n",
    );
}

#[test]
fn newer_syntax_is_laid_out() {
    stable(
        "namespace A {
    const X = 1, Y = 2;
    function f()
    {
        static $a = 1, $b;
        global $g;
        goto end;
        end:
        return;
    }
}

namespace B {
    interface I extends
        J,
        K
    {
        public const C = 1;
        public function m(): void;
    }

    class P
    {
        public string $name {
            get => $this->name;
            set(string $v) {
                $this->name = $v;
            }
        }
        public private(set) int $n = 0;
        public function __construct(protected readonly int $x = new Foo()) {}
    }

    enum E: int implements I
    {
        case A = 1;
    }

    $a = fn($x) => fn($y) => $x + $y;
    $c = $d?->e ?? $f[0] ?: null;
    $g = match (true) {
        default => 1,
    };
    $i = new readonly class ($a) {};
    list($a, list($b)) = $c;
    $j = $k instanceof L ? clone $m : throw new N();
    function &g(array &$a, callable ...$b): ?static
    {
        yield;
        yield 1 => 2;
        yield from h();
    }
    $u = (function () {
        return 1;
    })();
    $v = isset($a, $b) && !empty($c) || is_null($d) xor $e and $f;
    $w = -$x ** 2 % 3 <=> 4 << 1;
    $y = &$z;
    $aa = @$bb;
    $ee = (object) ['a' => 1];
    exit;
}
",
    );
}

#[test]
fn an_attribute_of_an_enum_case_stands_above_it() {
    assert_eq!(
        formatted("<?php\n\nenum E: int\n{\n    #[\\Override] case A=1;\n    case B = 2;\n}\n"),
        "<?php\n\nenum E: int\n{\n    #[\\Override]\n    case A = 1;\n    case B = 2;\n}\n"
    );
}
