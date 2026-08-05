package console

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

func TestHelpersResolveLiteralNamesFromConcreteImplementations(t *testing.T) {
	index, _ := helperPHPIndex(t)
	helpers := Helpers(index)
	require.Len(t, helpers, 2)
	assert.Equal(t, "formatter", helpers[0].Name)
	assert.Equal(t, "App\\FormatterHelper", helpers[0].Class)
	assert.Equal(t, "question", helpers[1].Name)
	assert.Equal(t, "App\\QuestionHelper", helpers[1].Class)
	assert.Equal(t, "QuestionHelper.php", filepath.Base(helpers[1].File))
	assert.Positive(t, helpers[1].Range.Len())
}

func TestHelperReferenceRequiresTypedConsoleReceiver(t *testing.T) {
	index, root := helperPHPIndex(t)
	for _, test := range []struct {
		name   string
		source string
		valid  bool
	}{
		{
			name: "helper set get",
			source: `<?php
use Symfony\Component\Console\Helper\HelperSet;
function inspect(HelperSet $helpers): void { $helpers->get('question'); }`,
			valid: true,
		},
		{
			name: "helper set has",
			source: `<?php
use Symfony\Component\Console\Helper\HelperSet;
function inspect(HelperSet $helpers): void { $helpers->has('question'); }`,
			valid: true,
		},
		{
			name: "command helper",
			source: `<?php
use Symfony\Component\Console\Command\Command;
function inspect(Command $command): void { $command->getHelper('question'); }`,
			valid: true,
		},
		{
			name: "unrelated get",
			source: `<?php
class Repository { public function get(string $name): object {} }
function inspect(Repository $repository): void { $repository->get('question'); }`,
			valid: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(root, strings.ReplaceAll(test.name, " ", "_")+".php")
			document := lsp.NewTextDocument(uriutil.FileURI(path), test.source, 1)
			offset := uint32(strings.LastIndex(test.source, "question") + 1)
			node := document.SyntaxTree.Root.NodeAtOffset(offset)
			reference, found := HelperReferenceAt(node)
			require.True(t, found)
			assert.Equal(t, "question", reference.Name)
			assert.Equal(t, "question", test.source[reference.Range.Start:reference.Range.End])
			ctx := index.AddDocumentContext(
				context.Background(),
				path,
				1,
				node,
				document.SyntaxTree.Root,
			)
			assert.Equal(t, test.valid, ValidateHelperReference(
				ctx,
				index,
				reference,
				document.Text,
			))
		})
	}
}

func TestHelperCatalogRefreshesAfterSemanticRevision(t *testing.T) {
	index, root := helperPHPIndex(t)
	catalog := NewHelperCatalog(index)
	require.Len(t, catalog.Helpers(), 2)
	require.NoError(t, index.Index(indexer.NewParsedFile(
		filepath.Join(root, "src", "DescriptorHelper.php"),
		[]byte(`<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
class DescriptorHelper extends Helper {
    public function getName(): string { return 'descriptor'; }
}`),
	)))
	helpers := catalog.Helpers()
	require.Len(t, helpers, 3)
	assert.Equal(t, "descriptor", helpers[0].Name)
}

func helperPHPIndex(t *testing.T) (*php.PHPIndex, string) {
	t.Helper()
	root := t.TempDir()
	index, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	require.NoError(t, index.Index(indexer.NewParsedFile(
		filepath.Join(root, "vendor", "Console.php"),
		[]byte(`<?php
namespace Symfony\Component\Console\Helper;
interface HelperInterface { public function getName(): string; }
abstract class Helper implements HelperInterface {}
class HelperSet {
    public function get(string $name): HelperInterface {}
    public function has(string $name): bool {}
}
namespace Symfony\Component\Console\Command;
class Command { public function getHelper(string $name): object {} }
`),
	)))
	for name, source := range map[string]string{
		"QuestionHelper.php": `<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
class QuestionHelper extends Helper {
    public function getName(): string { return 'question'; }
}`,
		"FormatterHelper.php": `<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
class FormatterHelper extends Helper {
    public function getName(): string {
        if (random_int(0, 1)) { return "formatter"; }
        return "formatter";
    }
}`,
		"AbstractHelper.php": `<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
abstract class AbstractHelper extends Helper {
    public function getName(): string { return 'ignored'; }
}`,
		"ComputedHelper.php": `<?php
namespace App;
use Symfony\Component\Console\Helper\Helper;
class ComputedHelper extends Helper {
    public function getName(): string { return self::NAME; }
}`,
	} {
		require.NoError(t, index.Index(indexer.NewParsedFile(
			filepath.Join(root, "src", name),
			[]byte(source),
		)))
	}
	return index, root
}
