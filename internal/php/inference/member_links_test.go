package inference

import (
	"strings"
	"testing"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/php/binder"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/stretchr/testify/require"
)

func TestLinkMembersUsesInferredReceiverType(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;
class Service {
    public function value(): string { return 'ok'; }
}
class Consumer {
    public function __construct(private Service $service) {}
    public function run(): string { return $this->service->value(); }
}`
	root := phpparser.Parse(source).Tree.Root
	document := binder.New().Bind("/project/member.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	document = binder.Link(document, snapshot)
	document = New(snapshot).Analyze(document, root)
	document = LinkMembers(document, snapshot, root)

	offset := uint32(strings.LastIndex(source, "value"))
	var found bool
	for _, reference := range document.References {
		if reference.Range.Contains(offset) && reference.Resolved != "" {
			symbol, ok := snapshot.Symbol(reference.Resolved)
			require.True(t, ok)
			require.Equal(t, semantic.MethodSymbol, symbol.Kind)
			require.Equal(t, "value", symbol.Name)
			require.Empty(t, reference.CandidateIDs())
			found = true
		}
	}
	require.True(t, found)
}

func TestLinkMembersInfersReceiverInsideMemberAssignment(t *testing.T) {
	t.Parallel()
	source := `<?php
class Service {
    public private(set) string $value = '';
}
function update(Service $service): void {
    $service->value = 'changed';
}`
	root := phpparser.Parse(source).Tree.Root
	document := binder.New().Bind("/project/member-write.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	document = binder.Link(document, snapshot)
	document = New(snapshot).Analyze(document, root)
	document = LinkMembers(document, snapshot, root)

	for _, reference := range document.References {
		if reference.Kind != semantic.MemberName || reference.Name != "value" {
			continue
		}
		require.True(t, reference.Write)
		require.Equal(t, "Service", reference.Receiver.String())
		require.NotEmpty(t, reference.Resolved)
		return
	}
	t.Fatal("member assignment reference was not linked")
}

func TestLinkMembersKeepsNarrowedReceiverForRepeatedCall(t *testing.T) {
	t.Parallel()
	source := `<?php
abstract class Aggregation {}
class Sorting {
    public function direction(): string {}
}
class TermsAggregation extends Aggregation {
    public function getSorting(): ?Sorting {}
}
function serializeAggregation(Aggregation $aggregation): ?string {
    if ($aggregation instanceof TermsAggregation) {
        if ($aggregation->getSorting()) {
            return $aggregation->getSorting()->direction();
        }
    }
    return null;
}`
	root := phpparser.Parse(source).Tree.Root
	document := binder.New().Bind("/project/repeated-call.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	document = binder.Link(document, snapshot)
	document = New(snapshot).Analyze(document, root)
	document = LinkMembers(document, snapshot, root)

	var getSortingReferences int
	for _, reference := range document.References {
		if reference.Kind != semantic.MemberName ||
			reference.Name != "getSorting" {
			continue
		}
		getSortingReferences++
		require.Equal(t, "TermsAggregation", reference.Receiver.String())
		require.NotEmpty(t, reference.Resolved)
	}
	require.Equal(t, 2, getSortingReferences)
}

func TestLinkMembersNarrowsReceiverInsideWhileCondition(t *testing.T) {
	t.Parallel()
	source := `<?php
class Expr {}
class Identifier {}
class MethodCall extends Expr {
    public Identifier $name;
    public Expr $var;
}
function inspect(Expr $current): void {
    while ($current instanceof MethodCall) {
        $current->name;
        $current = $current->var;
    }
}`
	root := phpparser.Parse(source).Tree.Root
	document := binder.New().Bind("/project/while-narrowing.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	document = binder.Link(document, snapshot)
	document = New(snapshot).Analyze(document, root)
	document = LinkMembers(document, snapshot, root)

	var narrowed int
	for _, reference := range document.References {
		if reference.Kind != semantic.MemberName ||
			(reference.Name != "name" && reference.Name != "var") {
			continue
		}
		narrowed++
		require.Equal(t, "MethodCall", reference.Receiver.String())
		require.NotEmpty(t, reference.Resolved)
	}
	require.Equal(t, 2, narrowed)
}

func TestAnalyzerReportsReturnAndArgumentTypeMismatches(t *testing.T) {
	t.Parallel()
	source := `<?php
namespace App;
class Service {
    public function value(string $input): int { return 'wrong'; }
    public function run(): int { return $this->value(42); }
}`
	root := phpparser.Parse(source).Tree.Root
	document := binder.New().Bind("/project/issues.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	document = binder.Link(document, snapshot)
	document = New(snapshot).Analyze(document, root)
	var codes []string
	for _, issue := range document.Issues {
		codes = append(codes, issue.Code)
	}
	require.Contains(t, codes, "php.returnType")
	require.Contains(t, codes, "php.arguments")
}

func TestLinkMembersDistinguishesStaticPropertiesAndConstants(t *testing.T) {
	t.Parallel()
	source := `<?php
class Service {
    public static string $name;
    public const string KIND = 'service';
}
function values(): array {
    return [Service::$name, Service::KIND];
}`
	root := phpparser.Parse(source).Tree.Root
	document := binder.New().Bind("/project/static.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	document = binder.Link(document, snapshot)
	document = New(snapshot).Analyze(document, root)
	document = LinkMembers(document, snapshot, root)

	var property, constant bool
	for _, reference := range document.References {
		if reference.Kind != semantic.MemberName {
			continue
		}
		switch reference.Name {
		case "$name":
			property = reference.TargetKind == semantic.PropertySymbol &&
				reference.Resolved != ""
		case "KIND":
			constant = reference.TargetKind == semantic.ClassConstantSymbol &&
				reference.Resolved != ""
		}
	}
	require.True(t, property, "%+v", document.References)
	require.True(t, constant, "%+v", document.References)
}

func TestLinkMembersTreatsDynamicStaticClassAsProperty(t *testing.T) {
	t.Parallel()
	source := `<?php
class Kernel {}
class Factory {
    /** @var class-string<Kernel> */
    public static string $kernelClass = Kernel::class;
    public static function create(): Kernel {
        return new static::$kernelClass();
    }
}`
	root := phpparser.Parse(source).Tree.Root
	document := binder.New().Bind("/project/dynamic-static-class.php", 1, root)
	snapshot := semantic.NewSnapshot(1, []*semantic.Document{document})
	document = binder.Link(document, snapshot)
	document = New(snapshot).Analyze(document, root)
	document = LinkMembers(document, snapshot, root)

	for _, issue := range document.Issues {
		require.NotEqual(t, "php.returnType", issue.Code, issue.Message)
		require.NotEqual(t, "php.arguments", issue.Code, issue.Message)
	}
	for _, reference := range document.References {
		if reference.Kind != semantic.MemberName ||
			reference.Name != "$kernelClass" {
			continue
		}
		require.Equal(t, semantic.PropertySymbol, reference.TargetKind)
		require.NotEmpty(t, reference.Resolved)
		return
	}
	t.Fatal("dynamic static class property was not linked")
}
