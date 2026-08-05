package completion

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestPHPAttributeCompletionUsesControllerMemberScope(t *testing.T) {
	provider, root := phpAttributeCompletionFixture(t)
	source := `<?php
namespace App\Controller;

class ProductController
{
    #[Rou]
    public function detail(): void {}
}`
	items := phpAttributeCompletions(
		t,
		provider,
		root,
		source,
		strings.Index(source, "Rou")+len("Rou"),
	)
	assert.ElementsMatch(
		t,
		[]string{"Cache", "IsGranted", "Route"},
		completionLabels(items),
	)
	route := completionByLabel(t, items, "Route")
	edit, ok := route.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "Route('${1}')$0", edit.NewText)
	assert.Equal(t, int(protocol.SnippetTextFormat), route.InsertTextFormat)
	require.Len(t, route.AdditionalTextEdits, 1)
	importEdit, ok := route.AdditionalTextEdits[0].(protocol.TextEdit)
	require.True(t, ok)
	assert.Contains(t, importEdit.NewText, "use "+phpRouteAttribute+";")
}

func TestPHPAttributeCompletionSupportsLoneHashAtClassScope(t *testing.T) {
	provider, root := phpAttributeCompletionFixture(t)
	source := `<?php
namespace App\Controller;

#
final class ProductController {}
`
	offset := strings.Index(source, "#") + 1
	items := phpAttributeCompletions(t, provider, root, source, offset)
	assert.ElementsMatch(
		t,
		[]string{"AsController", "IsGranted", "Route"},
		completionLabels(items),
	)
	asController := completionByLabel(t, items, "AsController")
	edit, ok := asController.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "#[AsController]", edit.NewText)
	assert.Equal(t, "#", phpAttributeCompletionRangeText(
		t,
		source,
		edit.Range,
	))
}

func TestPHPAttributeCompletionRecognizesTwigAndCommandSemantics(
	t *testing.T,
) {
	provider, root := phpAttributeCompletionFixture(t)
	twigSource := `<?php
namespace App\Twig;
use Twig\Extension\AbstractExtension;
class FormatExtension extends AbstractExtension {
    #[AsT]
    public function money(): string {}
}`
	twigItems := phpAttributeCompletions(
		t,
		provider,
		root,
		twigSource,
		strings.Index(twigSource, "AsT")+len("AsT"),
	)
	assert.ElementsMatch(t, []string{
		"AsTwigFilter",
		"AsTwigFunction",
		"AsTwigTest",
	}, completionLabels(twigItems))

	commandSource := `<?php
namespace App\Endpoint;
use Symfony\Component\Console\Input\InputInterface;
class ImportProducts {
    public function __invoke(InputInterface $input): void {}
}`
	hashOffset := strings.Index(commandSource, "class ImportProducts")
	commandSource = commandSource[:hashOffset] + "#\n" +
		commandSource[hashOffset:]
	items := phpAttributeCompletions(
		t,
		provider,
		root,
		commandSource,
		hashOffset+1,
	)
	assert.Equal(t, []string{"AsCommand"}, completionLabels(items))
}

func TestPHPAttributeCompletionUsesDoctrineAliasAndLifecycleCompanion(
	t *testing.T,
) {
	provider, root := phpAttributeCompletionFixture(t)
	source := `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;

#[ORM\Entity]
class Product
{
    #[Col]
    private string $name;

    #[Pre]
    public function updateTimestamp(): void {}
}`
	propertyItems := phpAttributeCompletions(
		t,
		provider,
		root,
		source,
		strings.Index(source, "Col")+len("Col"),
	)
	for _, expected := range []string{
		"Column",
		"GeneratedValue",
		"Id",
		"JoinColumn",
		"ManyToMany",
		"ManyToOne",
		"OneToMany",
		"OneToOne",
	} {
		assert.Contains(t, completionLabels(propertyItems), expected)
	}
	column := completionByLabel(t, propertyItems, "Column")
	columnEdit, ok := column.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, `ORM\Column`, columnEdit.NewText)
	assert.Empty(t, column.AdditionalTextEdits)

	methodItems := phpAttributeCompletions(
		t,
		provider,
		root,
		source,
		strings.Index(source, "Pre")+len("Pre"),
	)
	prePersist := completionByLabel(t, methodItems, "PrePersist")
	prePersistEdit, ok := prePersist.TextEdit.(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, `ORM\PrePersist`, prePersistEdit.NewText)
	require.Len(t, prePersist.AdditionalTextEdits, 1)
	companion, ok := prePersist.AdditionalTextEdits[0].(protocol.TextEdit)
	require.True(t, ok)
	assert.Equal(t, "#[ORM\\HasLifecycleCallbacks]\n", companion.NewText)
}

func TestPHPAttributeCompletionReusesAliasesAndAvoidsShortNameConflicts(
	t *testing.T,
) {
	provider, root := phpAttributeCompletionFixture(t)
	for _, test := range []struct {
		name            string
		importLine      string
		expected        string
		additionalEdits int
	}{
		{
			name:            "existing alias",
			importLine:      "use Symfony\\Component\\Routing\\Attribute\\Route as WebRoute;",
			expected:        "WebRoute('${1}')$0",
			additionalEdits: 0,
		},
		{
			name:            "conflicting short name",
			importLine:      "use App\\Metadata\\Route;",
			expected:        "\\Symfony\\Component\\Routing\\Attribute\\Route('${1}')$0",
			additionalEdits: 0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := `<?php
namespace App\Controller;
` + test.importLine + `
class ProductController {
    #[Rou]
    public function detail(): void {}
}`
			items := phpAttributeCompletions(
				t,
				provider,
				root,
				source,
				strings.LastIndex(source, "Rou")+len("Rou"),
			)
			route := completionByLabel(t, items, "Route")
			edit, ok := route.TextEdit.(protocol.TextEdit)
			require.True(t, ok)
			assert.Equal(t, test.expected, edit.NewText)
			assert.Len(t, route.AdditionalTextEdits, test.additionalEdits)
		})
	}
}

func TestPHPAttributeCompletionOffersClassSpecificMappings(t *testing.T) {
	provider, root := phpAttributeCompletionFixture(t)
	component := `<?php
namespace App\Twig\Components;
#
class Card {}
`
	componentItems := phpAttributeCompletions(
		t,
		provider,
		root,
		component,
		strings.Index(component, "#")+1,
	)
	assert.Equal(t, []string{"AsTwigComponent"}, completionLabels(
		componentItems,
	))

	entity := `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;
#[Ent]
class Product {}
`
	entityItems := phpAttributeCompletions(
		t,
		provider,
		root,
		entity,
		strings.LastIndex(entity, "Ent")+len("Ent"),
	)
	for _, expected := range []string{
		"Embeddable",
		"Entity",
		"HasLifecycleCallbacks",
		"Index",
		"Table",
		"UniqueConstraint",
	} {
		assert.Contains(t, completionLabels(entityItems), expected)
	}
}

func TestPHPAttributeCompletionRejectsInvalidScopesAndMissingClasses(
	t *testing.T,
) {
	provider, root := phpAttributeCompletionFixture(t)
	for _, source := range []string{
		`<?php
class Ordinary {
    #[Rou]
    public function run(): void {}
}`,
		`<?php
namespace App\Controller;
class ProductController {
    #[Rou]
    private function helper(): void {}
}`,
		`<?php
namespace App\Controller;
class ProductController {
    #[Rou]
    public static function helper(): void {}
}`,
		`<?php
namespace App\Controller;
class ProductController {
    public function detail(#[Rou] string $value): void {}
}`,
		`<?php
namespace App\Controller;
class ProductController {
    #[Route('Rou')]
    public function detail(): void {}
}`,
	} {
		offset := strings.LastIndex(source, "Rou") + len("Rou")
		assert.Empty(t, phpAttributeCompletions(
			t,
			provider,
			root,
			source,
			offset,
		))
	}

	missingIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, missingIndex.Close()) })
	source := `<?php
namespace App\Controller;
class ProductController {
    #[Rou]
    public function detail(): void {}
}`
	assert.Empty(t, phpAttributeCompletions(
		t,
		NewPHPAttributeCompletionProvider(missingIndex),
		root,
		source,
		strings.Index(source, "Rou")+len("Rou"),
	))
}

func TestPHPAttributeCompletionRespectsPHPLanguageLevel(t *testing.T) {
	provider, root := phpAttributeCompletionFixture(t)
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "composer.json"),
		[]byte(`{"require":{"php":"7.4"}}`),
		0o600,
	))
	require.NoError(t, provider.phpIndex.ConfigureProject(root))
	source := `<?php
namespace App\Controller;
class ProductController extends \Symfony\Bundle\FrameworkBundle\Controller\AbstractController {
    #[Rou]
    public function detail(): void {}
}`
	items := phpAttributeCompletions(
		t,
		provider,
		root,
		source,
		strings.Index(source, "Rou")+len("Rou"),
	)
	require.Empty(t, items)
}

func phpAttributeCompletionFixture(
	t *testing.T,
) (*PHPAttributeCompletionProvider, string) {
	t.Helper()
	root := t.TempDir()
	index, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	vendor := `<?php
namespace Symfony\Component\Routing\Attribute;
class Route {}
namespace Symfony\Component\Security\Http\Attribute;
class IsGranted {}
namespace Symfony\Component\HttpKernel\Attribute;
class Cache {}
class AsController {}
namespace Symfony\Component\Console\Attribute;
class AsCommand {}
namespace Symfony\Component\Console\Input;
interface InputInterface {}
namespace Symfony\Component\Console\Output;
interface OutputInterface {}
namespace Symfony\Component\Console\Command;
class Command {}
namespace Symfony\Bundle\FrameworkBundle\Controller;
abstract class AbstractController {}
namespace Twig\Attribute;
class AsTwigFilter {}
class AsTwigFunction {}
class AsTwigTest {}
namespace Twig\Extension;
interface ExtensionInterface {}
abstract class AbstractExtension implements ExtensionInterface {}
namespace Symfony\UX\TwigComponent\Attribute;
class AsTwigComponent {}
namespace Doctrine\ORM\Mapping;
class Column {}
class Id {}
class GeneratedValue {}
class OneToMany {}
class OneToOne {}
class ManyToOne {}
class ManyToMany {}
class JoinColumn {}
class Entity {}
class Table {}
class UniqueConstraint {}
class Index {}
class Embeddable {}
class HasLifecycleCallbacks {}
class PostLoad {}
class PostPersist {}
class PostRemove {}
class PostUpdate {}
class PrePersist {}
class PreRemove {}
class PreUpdate {}
`
	require.NoError(t, index.Index(indexer.NewParsedFile(
		filepath.Join(root, "vendor", "attributes.php"),
		[]byte(vendor),
	)))
	return NewPHPAttributeCompletionProvider(index), root
}

func phpAttributeCompletions(
	t *testing.T,
	provider *PHPAttributeCompletionProvider,
	root,
	source string,
	offset int,
) []protocol.CompletionItem {
	t.Helper()
	path := filepath.Join(root, "src", "Subject.php")
	document := lsp.NewTextDocument(uriutil.FileURI(path), source, 1)
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	nodeOffset := offset
	if nodeOffset >= len(source) {
		nodeOffset = len(source) - 1
	}
	if nodeOffset > 0 {
		nodeOffset--
	}
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(nodeOffset))
	return provider.GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
}

func completionByLabel(
	t *testing.T,
	items []protocol.CompletionItem,
	label string,
) protocol.CompletionItem {
	t.Helper()
	for _, item := range items {
		if item.Label == label {
			return item
		}
	}
	t.Fatalf("completion %q not found in %v", label, completionLabels(items))
	return protocol.CompletionItem{}
}

func phpAttributeCompletionRangeText(
	t *testing.T,
	source string,
	value protocol.Range,
) string {
	t.Helper()
	document := lsp.NewTextDocument("file:///range.php", source, 1)
	start := document.LineIndex.OffsetUTF16(
		uint32(value.Start.Line),
		uint32(value.Start.Character),
	)
	end := document.LineIndex.OffsetUTF16(
		uint32(value.End.Line),
		uint32(value.End.Character),
	)
	return source[start:end]
}
