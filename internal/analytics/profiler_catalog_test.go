package analytics

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfilerCatalogReadsLocalProfilesFiltersAndResolvesControllers(
	t *testing.T,
) {
	root := t.TempDir()
	cache := t.TempDir()
	phpIndex, err := php.NewPHPIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	twigIndex, err := twig.NewTwigIndexer(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, twigIndex.Close()) })

	controllerPath := filepath.Join(
		root,
		"src",
		"Controller",
		"ProductController.php",
	)
	componentPath := filepath.Join(
		root,
		"src",
		"Twig",
		"Components",
		"Alert.php",
	)
	componentTemplatePath := filepath.Join(
		root,
		"templates",
		"components",
		"anonymous.html.twig",
	)
	controllerSource := `<?php
namespace App\Controller;

final class ProductController
{
    #[Template('product/attribute.html.twig')]
    public function show(): array
    {
        return $this->render('product/show.html.twig');
    }
}
`
	componentSource := `<?php
namespace App\Twig\Components;

final class Alert {}
`
	componentTemplateSource := "<article>Anonymous</article>\n"
	for path, source := range map[string]string{
		controllerPath:        controllerSource,
		componentPath:         componentSource,
		componentTemplatePath: componentTemplateSource,
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	controllerFile := indexer.NewParsedFile(
		controllerPath,
		[]byte(controllerSource),
	)
	require.NoError(t, phpIndex.Index(controllerFile))
	require.NoError(t, twigIndex.Index(controllerFile))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		componentPath,
		[]byte(componentSource),
	)))
	require.NoError(t, twigIndex.Index(indexer.NewParsedFile(
		componentTemplatePath,
		[]byte(componentTemplateSource),
	)))

	profilerDirectory := filepath.Join(
		root,
		"var",
		"cache",
		"dev",
		"profiler",
	)
	require.NoError(t, os.MkdirAll(profilerDirectory, 0o755))
	indexPath := filepath.Join(profilerDirectory, "index.csv")
	require.NoError(t, os.WriteFile(
		indexPath,
		[]byte(strings.Join([]string{
			"18e6b8,127.0.0.1,GET,http://localhost/app_dev.php/products/1,1700000000,,200",
			"a1b2c3,127.0.0.1,POST,https://shop.test/products/2,1800000000,,404",
			`broken,"unterminated`,
			"abcdef,127.0.0.1,GET,relative-url,1600000000,,204",
		}, "\n")),
		0o644,
	))
	rawContent := profilerFixtureContent(
		`App\Controller\ProductController::show`,
		"product.show",
		[]string{
			"@WebProfiler/Toolbar/toolbar.html.twig",
			"product/show.html.twig",
			"layout.html.twig",
			"ignored-fourth.html.twig",
		},
		`App\Form\ProductType`,
	)
	rawContent = append(
		rawContent,
		profilerMailFixture("Time for Symfony Mailer!")...,
	)
	rawContent = append(rawContent, profilerTwigComponentFixture()...)
	writeProfilerFixture(t, profilerDirectory, "18e6b8", rawContent, false)
	writeProfilerFixture(t, profilerDirectory, "a1b2c3", rawContent, true)
	writeProfilerFixture(
		t,
		profilerDirectory,
		"abcdef",
		[]byte{0x1f, 0x8b, 0x08},
		false,
	)

	provider := NewProfilerCatalogProvider(root, phpIndex, twigIndex)
	entries, err := provider.Catalog(
		context.Background(),
		ProfilerRequestCatalogRequest{},
	)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	newest := entries[0]
	assert.Equal(t, "a1b2c3", newest.Hash)
	assert.Equal(t, "POST", newest.Method)
	assert.Equal(t, 404, newest.StatusCode)
	assert.Equal(t, int64(1800000000), newest.Timestamp)
	assert.Equal(
		t,
		"https://shop.test/_profiler/a1b2c3",
		newest.ProfilerURL,
	)
	assert.Equal(
		t,
		`App\Controller\ProductController::show`,
		newest.Controller,
	)
	assert.Equal(t, "product.show", newest.Route)
	assert.Equal(t, "product/show.html.twig", newest.EntryView)
	assert.Equal(t, []string{
		"product/attribute.html.twig",
		"product/show.html.twig",
	}, newest.StaticTemplates)
	assert.Equal(t, []string{
		"@WebProfiler/Toolbar/toolbar.html.twig",
		"product/show.html.twig",
		"layout.html.twig",
	}, newest.RenderedTemplates)
	assert.Equal(t, []string{`App\Form\ProductType`}, newest.FormTypes)
	assert.Equal(t, []ProfilerMailMessage{{
		Title: "Time for Symfony Mailer!",
		Panel: "mailer",
	}}, newest.MailMessages)
	assert.Equal(t, []ProfilerRuntimeTwigComponent{
		{
			Name:        "Anon:Card",
			Class:       "Symfony\\UX\\TwigComponent\\AnonymousComponent",
			Template:    "components/anonymous.html.twig",
			RenderCount: 5,
			FileURI:     uriutil.FileURI(componentTemplatePath),
			SourceLine:  1,
		},
		{
			Name:        "Alert",
			Class:       "App\\Twig\\Components\\Alert",
			Template:    "components/alert.html.twig",
			RenderCount: 3,
			FileURI:     uriutil.FileURI(componentPath),
			SourceLine:  4,
		},
	}, newest.TwigComponents)
	assert.Equal(t, uriutil.FileURI(controllerPath), newest.ControllerFileURI)
	assert.Equal(t, 7, newest.ControllerLine)
	assert.Equal(t, uriutil.FileURI(indexPath), newest.IndexFileURI)

	assert.Equal(
		t,
		"http://localhost/app_dev.php/_profiler/18e6b8",
		entries[1].ProfilerURL,
	)
	assert.Equal(t, "_profiler/abcdef", entries[2].ProfilerURL)
	assert.Empty(t, entries[2].Controller)

	filtered, err := provider.Catalog(
		context.Background(),
		ProfilerRequestCatalogRequest{
			URL:        "PRODUCTS/1",
			Hash:       "18E6",
			Controller: "productcontroller",
			Route:      "PRODUCT.SHOW",
			Limit:      1,
			BaseURL:    "https://debug.example.test/app_dev.php/",
		},
	)
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "18e6b8", filtered[0].Hash)
	assert.Equal(
		t,
		"https://debug.example.test/app_dev.php/_profiler/18e6b8",
		filtered[0].ProfilerURL,
	)

	relativeOverride, err := provider.Catalog(
		context.Background(),
		ProfilerRequestCatalogRequest{
			IndexPath: "var/cache/dev/profiler/index.csv",
			Hash:      "abcdef",
		},
	)
	require.NoError(t, err)
	require.Len(t, relativeOverride, 1)

	_, err = provider.Catalog(
		context.Background(),
		ProfilerRequestCatalogRequest{IndexPath: "../outside/index.csv"},
	)
	assert.ErrorContains(t, err, "outside workspace")
	_, err = provider.Catalog(
		context.Background(),
		ProfilerRequestCatalogRequest{BaseURL: "file:///tmp/profiler"},
	)
	assert.ErrorContains(t, err, "HTTP(S)")
}

func TestProfilerCatalogReportsMissingOrEmptyIndexes(t *testing.T) {
	root := t.TempDir()
	provider := NewProfilerCatalogProvider(root, nil, nil)
	_, err := provider.Catalog(
		context.Background(),
		ProfilerRequestCatalogRequest{},
	)
	assert.ErrorContains(t, err, "no local Symfony profiler index")

	indexPath := filepath.Join(
		root,
		"var",
		"cache",
		"test",
		"profiler",
		"index.csv",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(indexPath), 0o755))
	require.NoError(t, os.WriteFile(indexPath, []byte("invalid\n"), 0o644))
	_, err = provider.Catalog(
		context.Background(),
		ProfilerRequestCatalogRequest{},
	)
	assert.ErrorContains(t, err, "no profiler requests were found")
}

func TestProfilerParsingHelpersUseSerializedByteLengthsAndSafeURLs(
	t *testing.T,
) {
	controller := `App\Controller\ÜnicodeController::show`
	content := []byte(profilerSerializedFixture("_controller", controller))
	assert.Equal(t, controller, profilerSerializedString(content, "_controller"))
	assert.Empty(
		t,
		profilerSerializedString(
			[]byte(`"_controller";s:999:"short";`),
			"_controller",
		),
	)
	assert.Equal(
		t,
		"https://shop.test/index.php/_profiler/abcdef",
		profilerURL(
			"",
			"https://shop.test/index.php/products?preview=1",
			"abcdef",
		),
	)
	assert.Equal(
		t,
		"_profiler/abcdef",
		profilerURL("", "not a URL", "abcdef"),
	)
	mails := profilerMailMessages(bytes.Join([][]byte{
		profilerMailFixture("Order confirmed ✓"),
		profilerMailFixture("Order confirmed ✓"),
		[]byte(
			`AbstractHeader` + "\x00" +
				`name";s:7:"Subject";` +
				`UnstructuredHeader` + "\x00" +
				`value";s:999:"broken";`,
		),
	}, nil))
	assert.Equal(t, []ProfilerMailMessage{{
		Title: "Order confirmed ✓",
		Panel: "mailer",
	}}, mails)
	componentFixture := profilerTwigComponentFixture()
	assert.Len(t, profilerTwigComponents(componentFixture), 2)
}

func profilerFixtureContent(
	controller string,
	route string,
	templates []string,
	formType string,
) []byte {
	var content strings.Builder
	content.WriteString(profilerSerializedFixture("_controller", controller))
	content.WriteString(profilerSerializedFixture("_route", route))
	content.WriteString(`"template_paths"a:`)
	_, _ = fmt.Fprint(&content, len(templates))
	content.WriteString(":{")
	for _, template := range templates {
		_, _ = fmt.Fprintf(
			&content,
			`s:%d:"%s";s:%d:"%s";`,
			len(template),
			template,
			len("/templates/"+template),
			"/templates/"+template,
		)
	}
	content.WriteString("}")
	content.WriteByte(0)
	content.WriteString(
		`\Symfony\Bundle\FrameworkBundle\DataCollector\FormDataCollector"` +
			`"forms";a:1:{` +
			`"type_class";a:1:{"value";s:` +
			fmt.Sprint(len(formType)) +
			`:"` + formType + `";}}`,
	)
	content.WriteByte(0)
	return []byte(content.String())
}

func profilerSerializedFixture(key, value string) string {
	return fmt.Sprintf(`"%s";s:%d:"%s";`, key, len([]byte(value)), value)
}

func profilerMailFixture(title string) []byte {
	return []byte(
		`s:50:"` + "\x00" +
			`Symfony\Component\Mime\Header\AbstractHeader` + "\x00" +
			`name";s:7:"Subject";` +
			`s:55:"` + "\x00" +
			`Symfony\Component\Mime\Header\UnstructuredHeader` + "\x00" +
			`value";s:` + fmt.Sprint(len([]byte(title))) +
			`:"` + title + `";}`,
	)
}

func profilerTwigComponentFixture() []byte {
	component := func(
		name,
		class,
		template string,
		renderCount int,
	) string {
		return "a:4:{" +
			profilerPHPSerializedString("name") +
			profilerPHPSerializedString(name) +
			profilerPHPSerializedString("class") +
			profilerPHPSerializedString(class) +
			profilerPHPSerializedString("template") +
			profilerPHPSerializedString(template) +
			`s:12:"render_count";i:` +
			fmt.Sprint(renderCount) + ";}"
	}
	return []byte(
		`a:1:{s:14:"twig_component";O:1:"C":1:{s:4:"data";` +
			`O:1:"D":1:{s:4:"data";a:5:{` +
			`i:0;a:1:{s:10:"components";a:1:{i:1;i:2;}}` +
			`i:1;a:0:{}` +
			`i:2;a:2:{` +
			`s:5:"Alert";a:1:{i:1;i:3;}` +
			`s:9:"Anon:Card";a:1:{i:1;i:4;}}` +
			`i:3;` + component(
			"Alert",
			`App\Twig\Components\Alert`,
			"components/alert.html.twig",
			3,
		) +
			`i:4;` + component(
			"Anon:Card",
			`Symfony\UX\TwigComponent\AnonymousComponent`,
			"components/anonymous.html.twig",
			5,
		) +
			`}}}}}`,
	)
}

func profilerPHPSerializedString(value string) string {
	return fmt.Sprintf(`s:%d:"%s";`, len([]byte(value)), value)
}

func writeProfilerFixture(
	t *testing.T,
	directory string,
	hash string,
	content []byte,
	compressed bool,
) {
	t.Helper()
	path := filepath.Join(directory, hash[4:6], hash[2:4], hash)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	if !compressed {
		require.NoError(t, os.WriteFile(path, content, 0o644))
		return
	}
	file, err := os.Create(path)
	require.NoError(t, err)
	writer := gzip.NewWriter(file)
	_, err = writer.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())
}
