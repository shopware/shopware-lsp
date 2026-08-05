package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/environment"
	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/security"
	"github.com/shopware/shopware-lsp/internal/serializer"
	"github.com/shopware/shopware-lsp/internal/stimulus"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceConstructionAndClose(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", cacheRoot)
	projectRoot := t.TempDir()

	server := lsp.NewServer(nil, "", "test")
	workspace, err := NewWorkspace(context.Background(), projectRoot, server)
	require.NoError(t, err)
	require.Equal(t, projectRoot, workspace.Root())
	require.NotNil(t, workspace.Scanner())

	cacheDir, err := projectCacheFolder(projectRoot)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(cacheDir, "indexes.db"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(cacheDir, "file_scanner.db"))
	require.NoError(t, err)

	require.NoError(t, workspace.Close())
	require.NoError(t, workspace.Close())
}

func TestWorkspaceIndexesTranslationResources(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	translationDir := filepath.Join(projectRoot, "translations")
	require.NoError(t, os.MkdirAll(translationDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(translationDir, "messages.en.yaml"),
		[]byte("checkout.complete: Order completed\n"),
		0o644,
	))

	server := lsp.NewServer(nil, projectRoot, "test")
	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		server,
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, workspace.Close()) })

	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))

	var translations *translation.Index
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*translation.Index); ok {
			translations = candidate
			break
		}
	}
	require.NotNil(t, translations)
	messages, err := translations.GetMessages("messages", "checkout.complete")
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "Order completed", messages[0].Text)
	assert.Equal(
		t,
		filepath.Join(translationDir, "messages.en.yaml"),
		messages[0].File,
	)
}

func TestWorkspaceIndexesAndRestoresTwigControllerVariables(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	controllerDir := filepath.Join(projectRoot, "src", "Controller")
	templateDir := filepath.Join(projectRoot, "templates", "product")
	require.NoError(t, os.MkdirAll(controllerDir, 0o755))
	require.NoError(t, os.MkdirAll(templateDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(controllerDir, "ProductController.php"),
		[]byte(`<?php
namespace App\Controller;
class Product {}
class ProductController {
    public function show(Product $product) {
        return $this->render('product/show.html.twig', [
            'product' => $product,
        ]);
    }
}`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(templateDir, "show.html.twig"),
		[]byte(`{{ product }}`),
		0o644,
	))

	server := lsp.NewServer(nil, projectRoot, "test")
	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		server,
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	variables, err := workspacePHPIndex(t, workspace).TwigTemplateVariables(
		"product/show.html.twig",
	)
	require.NoError(t, err)
	require.Len(t, variables, 1)
	assert.Equal(t, "product", variables[0].Name)
	assert.Equal(t, "App\\Controller\\Product", variables[0].Type)
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored, err := workspacePHPIndex(t, reopened).TwigTemplateVariables(
		"product/show.html.twig",
	)
	require.NoError(t, err)
	require.Len(t, restored, 1)
	assert.Equal(t, variables[0], restored[0])
}

func TestWorkspaceIndexesAndRestoresSymfonyEvents(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	sourceDir := filepath.Join(projectRoot, "src")
	vendorDir := filepath.Join(projectRoot, "vendor")
	require.NoError(t, os.MkdirAll(sourceDir, 0o755))
	require.NoError(t, os.MkdirAll(vendorDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(vendorDir, "EventSubscriberInterface.php"),
		[]byte(`<?php
namespace Symfony\Component\EventDispatcher;
interface EventSubscriberInterface {}`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sourceDir, "Events.php"),
		[]byte(`<?php
namespace App;
class DomainEvent {}
class Subscriber implements \Symfony\Component\EventDispatcher\EventSubscriberInterface {
    public static function getSubscribedEvents(): array {
        return [DomainEvent::class => 'onEvent'];
    }
    public function onEvent(DomainEvent $event): void {}
}`),
		0o644,
	))

	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	indexed, found, err := workspaceEventIndex(t, workspace).GetEvent(
		"App\\DomainEvent",
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, indexed.Listeners(), 1)
	assert.Equal(t, "App\\Subscriber", indexed.Listeners()[0].Class)
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored, found, err := workspaceEventIndex(t, reopened).GetEvent(
		"App\\DomainEvent",
	)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, indexed, restored)
}

func TestWorkspaceIndexesAndRestoresSymfonyForms(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	vendorDir := filepath.Join(projectRoot, "vendor")
	sourceDir := filepath.Join(projectRoot, "src")
	require.NoError(t, os.MkdirAll(vendorDir, 0o755))
	require.NoError(t, os.MkdirAll(sourceDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(vendorDir, "Form.php"),
		[]byte(`<?php
namespace Symfony\Component\Form;
interface FormTypeInterface {}
interface FormBuilderInterface {}
abstract class AbstractType implements FormTypeInterface {}`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sourceDir, "ProfileType.php"),
		[]byte(`<?php
namespace App\Form;
class ProfileType extends \Symfony\Component\Form\AbstractType {
    public function getBlockPrefix(): string { return 'profile'; }
    public function configureOptions($resolver): void {
        $resolver->setDefaults([
            'data_class' => \App\Model\Profile::class,
            'translation_domain' => 'profile',
        ]);
    }
    public function buildForm(
        \Symfony\Component\Form\FormBuilderInterface $builder,
        array $options,
    ): void {
        $builder->add('displayName');
    }
}`),
		0o644,
	))

	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	indexed, found, err := workspaceFormIndex(t, workspace).GetType("profile")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "App\\Form\\ProfileType", indexed.Class)
	assert.Equal(t, "App\\Model\\Profile", indexed.DataClass)
	fields, err := workspaceFormIndex(t, workspace).EffectiveFields(
		indexed.Class,
	)
	require.NoError(t, err)
	require.Len(t, fields, 1)
	assert.Equal(t, "displayName", fields[0].Name)
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored, found, err := workspaceFormIndex(t, reopened).GetType("profile")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, indexed, restored)
}

func TestWorkspaceIndexesAndRestoresSymfonySecurity(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	configDir := filepath.Join(projectRoot, "config", "packages")
	sourceDir := filepath.Join(projectRoot, "src", "Security")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.MkdirAll(sourceDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(configDir, "security.yaml"),
		[]byte(`security:
  providers:
    app_users:
      memory: null
  firewalls:
    main:
      provider: app_users
  role_hierarchy:
    ROLE_EDITOR: [ROLE_USER]
  access_control:
    - { path: ^/admin, roles: ROLE_ADMIN }
`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sourceDir, "ArticleVoter.php"),
		[]byte(`<?php
namespace App\Security;
use Symfony\Component\Security\Core\Authorization\Voter\Voter;
final class ArticleVoter extends Voter {
    protected function supports(string $attribute, mixed $subject): bool {
        return $attribute === 'article.edit';
    }
    protected function voteOnAttribute(string $attribute, mixed $subject, $token): bool {
        return true;
    }
}`),
		0o644,
	))

	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	securityIndex := workspaceSecurityIndex(t, workspace)
	role, found, err := securityIndex.Attribute("ROLE_EDITOR")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, role.Declarations())
	attribute, found, err := securityIndex.Attribute("article.edit")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(
		t,
		"App\\Security\\ArticleVoter",
		attribute.Declarations()[0].Class,
	)
	providerSymbol, found, err := securityIndex.ConfigSymbol(
		"app_users",
		security.ConfigProvider,
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, providerSymbol.References(), 1)
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored, found, err := workspaceSecurityIndex(
		t,
		reopened,
	).Attribute("article.edit")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, attribute, restored)
	restoredProvider, found, err := workspaceSecurityIndex(
		t,
		reopened,
	).ConfigSymbol("app_users", security.ConfigProvider)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, providerSymbol, restoredProvider)
}

func TestWorkspaceIndexesAndRestoresSerializerTargets(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	sourceDir := filepath.Join(projectRoot, "src")
	require.NoError(t, os.MkdirAll(sourceDir, 0o755))
	modelSource := "<?php\nnamespace App;\nclass Model {}\n"
	handlerSource := `<?php
namespace App;
function load($serializer): void {
    $serializer->deserialize($data, Model::class, 'json');
    $serializer->deserialize($data, 'App\Model[]', 'json');
}
`
	require.NoError(t, os.WriteFile(
		filepath.Join(sourceDir, "Model.php"),
		[]byte(modelSource),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sourceDir, "Handler.php"),
		[]byte(handlerSource),
		0o644,
	))

	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	usages, err := workspaceSerializerIndex(t, workspace).Usages("App\\Model")
	require.NoError(t, err)
	require.Len(t, usages, 2)
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored, err := workspaceSerializerIndex(
		t,
		reopened,
	).Usages("App\\Model")
	require.NoError(t, err)
	assert.Equal(t, usages, restored)
}

func TestWorkspaceIndexesAndRestoresDoctrineMetadata(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	entityPath := filepath.Join(projectRoot, "src", "Product.php")
	require.NoError(t, os.MkdirAll(filepath.Dir(entityPath), 0o755))
	source := `<?php
namespace App\Entity;
use Doctrine\ORM\Mapping as ORM;
#[ORM\Entity(repositoryClass: \App\Repository\ProductRepository::class)]
#[ORM\Table(name: 'product')]
class Product {
    #[ORM\Column(type: 'string')]
    private string $name;
}`
	require.NoError(t, os.WriteFile(entityPath, []byte(source), 0o644))

	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	model, found, err := workspaceDoctrineIndex(t, workspace).Model(
		"App\\Entity\\Product",
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "product", model.Table)
	fields, err := workspaceDoctrineIndex(t, workspace).Fields(model.Class)
	require.NoError(t, err)
	require.Len(t, fields, 1)
	require.Equal(t, "name", fields[0].Name)
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored, found, err := workspaceDoctrineIndex(t, reopened).Model(
		model.Class,
	)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, model, restored)
}

func TestWorkspaceIndexesAndRestoresStimulusControllers(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	controllerPath := filepath.Join(
		projectRoot,
		"assets",
		"controllers",
		"hello_controller.js",
	)
	templatePath := filepath.Join(
		projectRoot,
		"templates",
		"page.html.twig",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(controllerPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(templatePath), 0o755))
	require.NoError(t, os.WriteFile(
		controllerPath,
		[]byte(`import { Controller } from '@hotwired/stimulus';
export default class extends Controller {}`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		templatePath,
		[]byte(`<div data-controller="hello"></div>`),
		0o644,
	))

	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	controllers, err := workspaceStimulusIndex(t, workspace).Controllers()
	require.NoError(t, err)
	require.Len(t, controllers, 1)
	assert.Equal(t, "hello", controllers[0].Name)
	usages, err := workspaceStimulusIndex(t, workspace).Usages("hello")
	require.NoError(t, err)
	require.Len(t, usages, 1)
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restoredControllers, err := workspaceStimulusIndex(
		t,
		reopened,
	).Controllers()
	require.NoError(t, err)
	assert.Equal(t, controllers, restoredControllers)
	restoredUsages, err := workspaceStimulusIndex(
		t,
		reopened,
	).Usages("hello")
	require.NoError(t, err)
	assert.Equal(t, usages, restoredUsages)
}

func TestWorkspaceIndexesAndRestoresPublicAssets(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	build := filepath.Join(projectRoot, "public", "build")
	templates := filepath.Join(projectRoot, "templates")
	bundleConfig := filepath.Join(
		projectRoot,
		"src",
		"Administration",
		"Resources",
		"config",
		"routes.xml",
	)
	require.NoError(t, os.MkdirAll(build, 0o755))
	require.NoError(t, os.MkdirAll(templates, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(bundleConfig), 0o755))
	assetPath := filepath.Join(build, "app.css")
	bundleAssetPath := filepath.Join(
		projectRoot,
		"public",
		"bundles",
		"administration",
		"administration",
		"app.js",
	)
	entrypointsPath := filepath.Join(build, "entrypoints.json")
	templatePath := filepath.Join(templates, "page.html.twig")
	require.NoError(t, os.WriteFile(assetPath, []byte("body{}"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Dir(bundleAssetPath), 0o755))
	require.NoError(t, os.WriteFile(
		bundleAssetPath,
		[]byte("console.log('app')"),
		0o644,
	))
	require.NoError(t, os.WriteFile(bundleConfig, []byte("<routes/>"), 0o644))
	require.NoError(t, os.WriteFile(
		entrypointsPath,
		[]byte(`{"entrypoints":{"app":{"css":["/build/app.css"]}}}`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		templatePath,
		[]byte(`{{ asset('build/app.css') }}
{{ asset('administration/app.js', '@Administration') }}`),
		0o644,
	))

	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	names, err := workspaceAssetIndex(t, workspace).Names()
	require.NoError(t, err)
	require.Contains(t, names, "build/app.css")
	entries, err := workspaceAssetIndex(t, workspace).EntryNames()
	require.NoError(t, err)
	require.Equal(t, []string{"app"}, entries)
	usages, err := workspaceAssetIndex(t, workspace).Usages(
		"build/app.css",
		asset.AssetReference,
	)
	require.NoError(t, err)
	require.Len(t, usages, 1)
	packageNames, err := workspaceAssetIndex(t, workspace).PackageNames()
	require.NoError(t, err)
	require.Contains(t, packageNames, "@Administration")
	packageUsages, err := workspaceAssetIndex(t, workspace).Usages(
		"@Administration",
		asset.AssetPackageReference,
	)
	require.NoError(t, err)
	require.Len(t, packageUsages, 1)
	resolvedBundleAssets, err := workspaceAssetIndex(
		t,
		workspace,
	).FindAssetsForPackage(
		"administration/app.js",
		"@Administration",
	)
	require.NoError(t, err)
	require.Len(t, resolvedBundleAssets, 1)
	require.Equal(t, bundleAssetPath, resolvedBundleAssets[0].Target)
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restoredNames, err := workspaceAssetIndex(t, reopened).Names()
	require.NoError(t, err)
	assert.Equal(t, names, restoredNames)
	restoredUsages, err := workspaceAssetIndex(t, reopened).Usages(
		"build/app.css",
		asset.AssetReference,
	)
	require.NoError(t, err)
	assert.Equal(t, usages, restoredUsages)
	restoredPackageNames, err := workspaceAssetIndex(
		t,
		reopened,
	).PackageNames()
	require.NoError(t, err)
	assert.Equal(t, packageNames, restoredPackageNames)
	restoredPackageUsages, err := workspaceAssetIndex(t, reopened).Usages(
		"@Administration",
		asset.AssetPackageReference,
	)
	require.NoError(t, err)
	assert.Equal(t, packageUsages, restoredPackageUsages)
	restoredBundleAssets, err := workspaceAssetIndex(
		t,
		reopened,
	).FindAssetsForPackage(
		"administration/app.js",
		"@Administration",
	)
	require.NoError(t, err)
	assert.Equal(t, resolvedBundleAssets, restoredBundleAssets)
}

func TestWorkspaceIndexesAndRestoresTwigComponents(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	classPath := filepath.Join(
		projectRoot,
		"src/Twig/Components/Alert.php",
	)
	templatePath := filepath.Join(
		projectRoot,
		"templates/components/Alert.html.twig",
	)
	pagePath := filepath.Join(
		projectRoot,
		"templates/page.html.twig",
	)
	for _, path := range []string{classPath, templatePath, pagePath} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	}
	require.NoError(t, os.WriteFile(classPath, []byte(`<?php
namespace App\Twig\Components;
use Symfony\UX\LiveComponent\Attribute\AsLiveComponent;
use Symfony\UX\LiveComponent\Attribute\LiveProp;
#[AsLiveComponent]
final class Alert {
    #[LiveProp(writable: true)]
    public string $title = '';
}`), 0o644))
	require.NoError(t, os.WriteFile(templatePath, []byte(`
{% props variant = 'info' %}
{% block content %}{% endblock %}
<div>{{ title }}</div>
`), 0o644))
	require.NoError(t, os.WriteFile(pagePath, []byte(`
{{ component('Alert') }}
<twig:Alert><twig:block name="content">Hi</twig:block></twig:Alert>
`), 0o644))

	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	components := workspaceTwigComponentIndex(t, workspace)
	names, err := components.Names()
	require.NoError(t, err)
	require.Contains(t, names, "Alert")
	usages, err := components.Usages("Alert")
	require.NoError(t, err)
	require.Len(t, usages, 2)
	props, err := components.Props("Alert")
	require.NoError(t, err)
	require.Len(t, props, 2)
	require.True(t, props[0].Live)
	require.True(t, props[0].Writable)
	indexedComponents, err := components.Find("Alert")
	require.NoError(t, err)
	require.NotEmpty(t, indexedComponents)
	require.True(t, indexedComponents[0].Live)
	blocks, err := components.Blocks("Alert")
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	require.Equal(t, "content", blocks[0].Name)
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored := workspaceTwigComponentIndex(t, reopened)
	restoredNames, err := restored.Names()
	require.NoError(t, err)
	require.Contains(t, restoredNames, "Alert")
	restoredUsages, err := restored.Usages("Alert")
	require.NoError(t, err)
	require.Equal(t, usages, restoredUsages)
	restoredProps, err := restored.Props("Alert")
	require.NoError(t, err)
	require.Len(t, restoredProps, 2)
	require.True(t, restoredProps[0].Live)
	require.True(t, restoredProps[0].Writable)
	restoredComponents, err := restored.Find("Alert")
	require.NoError(t, err)
	require.NotEmpty(t, restoredComponents)
	require.True(t, restoredComponents[0].Live)
}

func TestWorkspaceIndexesAndRestoresViteEntrypoints(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	configPath := filepath.Join(projectRoot, "vite.config.ts")
	targetPath := filepath.Join(projectRoot, "assets", "app.ts")
	templatePath := filepath.Join(
		projectRoot,
		"templates",
		"base.html.twig",
	)
	files := map[string]string{
		configPath: `export default defineConfig({
  build: {rollupOptions: {input: {app: './assets/app.ts'}}}
});`,
		targetPath:   "export const app = {};",
		templatePath: `{{ vite_entry_script_tags('app') }}`,
	}
	for path, source := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	entries, err := workspaceAssetIndex(t, workspace).ViteEntryNames()
	require.NoError(t, err)
	require.Equal(t, []string{"app"}, entries)
	usages, err := workspaceAssetIndex(t, workspace).Usages(
		"app",
		asset.ViteEntryReference,
	)
	require.NoError(t, err)
	require.Len(t, usages, 1)
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restoredEntries, err := workspaceAssetIndex(
		t,
		reopened,
	).ViteEntryNames()
	require.NoError(t, err)
	assert.Equal(t, entries, restoredEntries)
	restoredUsages, err := workspaceAssetIndex(t, reopened).Usages(
		"app",
		asset.ViteEntryReference,
	)
	require.NoError(t, err)
	assert.Equal(t, usages, restoredUsages)
}

func TestWorkspaceIndexesAndRestoresMessengerGraph(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	files := map[string]string{
		filepath.Join(
			projectRoot,
			"vendor",
			"MessageBusInterface.php",
		): `<?php
namespace Symfony\Component\Messenger;
interface MessageBusInterface {
    public function dispatch(object $message, array $stamps = []): object;
}`,
		filepath.Join(projectRoot, "src", "Message.php"): `<?php
namespace App;
class Message {}`,
		filepath.Join(projectRoot, "src", "Handler.php"): `<?php
namespace App;
use Symfony\Component\Messenger\Attribute\AsMessageHandler;
#[AsMessageHandler]
class Handler {
    public function __invoke(Message $message): void {}
}`,
		filepath.Join(projectRoot, "src", "Publisher.php"): `<?php
namespace App;
use Symfony\Component\Messenger\MessageBusInterface;
function publish(MessageBusInterface $messageBus): void {
    $messageBus->dispatch(new Message());
}`,
	}
	for path, source := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	message, found, err := workspaceMessengerIndex(t, workspace).GetMessage(
		"App\\Message",
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, message.Handlers(), 1)
	require.Len(t, message.Dispatches(), 1)
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored, found, err := workspaceMessengerIndex(t, reopened).GetMessage(
		"App\\Message",
	)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, message, restored)
}

func TestWorkspaceIndexesAndRestoresEnvironmentGraph(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	files := map[string]string{
		filepath.Join(projectRoot, ".env"): `
DATABASE_URL=mysql://localhost/app
APP_ENV=dev
`,
		filepath.Join(projectRoot, "Dockerfile"): `
ENV APP_RUNTIME Runtime
`,
		filepath.Join(
			projectRoot,
			"config",
			"services.yaml",
		): `
parameters:
  database_url: '%env(resolve:DATABASE_URL)%'
`,
	}
	for path, source := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o644))
	}
	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	variable, found, err := workspaceEnvironmentIndex(
		t,
		workspace,
	).Variable("DATABASE_URL")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, variable.Declarations, 1)
	require.Len(t, variable.References, 1)
	names, err := workspaceEnvironmentIndex(t, workspace).Names()
	require.NoError(t, err)
	require.Contains(t, names, "APP_RUNTIME")
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored, found, err := workspaceEnvironmentIndex(
		t,
		reopened,
	).Variable("DATABASE_URL")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, variable, restored)
}

func TestWorkspaceIndexesAndRestoresDeprecatedSymfonyServices(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	configPath := filepath.Join(projectRoot, "config", "services.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(
		configPath,
		[]byte(`services:
  app.legacy:
    class: App\Legacy
    deprecated: 'The %service_id% service is deprecated; use app.modern.'
`),
		0o644,
	))

	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	service, found, err := workspaceSymfonyServiceIndex(
		t,
		workspace,
	).GetServiceByID("app.legacy")
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, service.Deprecated)
	require.Contains(t, service.Deprecation, "%service_id%")
	require.NotZero(t, service.DeprecatedRange.Len())
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored, found, err := workspaceSymfonyServiceIndex(
		t,
		reopened,
	).GetServiceByID("app.legacy")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, service, restored)
}

func TestWorkspaceIndexesAndRestoresTwigGlobals(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	files := map[string]string{
		"config/services.yaml": `services:
  app.clock:
    class: App\Clock
`,
		"config/packages/twig.yaml": `twig:
  globals:
    clock: '@app.clock'
    site_name: 'Store'
`,
		"src/StorefrontExtension.php": `<?php
namespace App;
use Twig\Extension\AbstractExtension;
class Feature {}
class StorefrontExtension extends AbstractExtension {
    public function getGlobals(): array {
        return ['feature' => new Feature()];
    }
}`,
	}
	for relative, content := range files {
		path := filepath.Join(projectRoot, relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	globals, err := workspaceTwigGlobalsIndex(t, workspace).GetAllGlobals()
	require.NoError(t, err)
	byName := make(map[string]twig.Global)
	for _, global := range globals {
		byName[global.Name] = global
	}
	require.Equal(t, "App\\Clock", byName["clock"].Type)
	require.Equal(t, "string", byName["site_name"].Type)
	require.Equal(t, "App\\Feature", byName["feature"].Type)
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored, err := workspaceTwigGlobalsIndex(
		t,
		reopened,
	).GetAllGlobals()
	require.NoError(t, err)
	require.Equal(t, globals, restored)
}

func TestWorkspaceIndexesAndRestoresDeprecatedTwigCallables(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	extensionPath := filepath.Join(
		projectRoot,
		"src",
		"LegacyExtension.php",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(extensionPath), 0o755))
	require.NoError(t, os.WriteFile(
		extensionPath,
		[]byte(`<?php
use Twig\Extension\AbstractExtension;
use Twig\TwigFunction;
class LegacyExtension extends AbstractExtension {
    public function getFunctions(): array {
        return [new TwigFunction('legacy_function', $this->legacy(...))];
    }
    public function legacy(): string {
        trigger_deprecation(
            'app',
            '1.0',
            'The "legacy_function" Twig function is deprecated. Use modern_function instead.',
        );
        return '';
    }
}`),
		0o644,
	))

	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	indexed, err := workspaceTwigGlobalsIndex(t, workspace).
		GetTwigFunction("legacy_function")
	require.NoError(t, err)
	require.Len(t, indexed, 1)
	require.True(t, indexed[0].Deprecated)
	require.Contains(t, indexed[0].Deprecation, "modern_function")
	require.NotZero(t, indexed[0].DeprecatedRange.Len())
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored, err := workspaceTwigGlobalsIndex(t, reopened).
		GetTwigFunction("legacy_function")
	require.NoError(t, err)
	require.Equal(t, indexed, restored)
}

func TestWorkspaceIndexesAndRestoresTemplateReferences(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	files := map[string]string{
		"templates/layout/base.html.twig": `{% block body %}{% endblock %}`,
		"templates/page.html.twig":        `{% extends 'layout/base.html.twig' %}`,
		"src/PageController.php": `<?php
final class PageController {
    public function page() {
        return $this->render('layout/base.html.twig');
    }
}`,
	}
	for relative, content := range files {
		path := filepath.Join(projectRoot, relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	references, err := workspaceTwigGlobalsIndex(
		t,
		workspace,
	).GetTemplateReferences("layout/base.html.twig")
	require.NoError(t, err)
	require.Len(t, references, 2)
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored, err := workspaceTwigGlobalsIndex(
		t,
		reopened,
	).GetTemplateReferences("layout/base.html.twig")
	require.NoError(t, err)
	require.Equal(t, references, restored)
}

func TestWorkspaceIndexesAndRestoresInheritedTwigBlocks(t *testing.T) {
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	projectRoot := t.TempDir()
	files := map[string]string{
		"templates/layout/base.html.twig": `{% block body %}{% endblock %}
{% block sidebar %}{% endblock %}`,
		"templates/page.html.twig": `{% extends 'layout/base.html.twig' %}
{% block body %}{% endblock %}
{% block page_actions %}{% endblock %}`,
	}
	for relative, content := range files {
		path := filepath.Join(projectRoot, relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	workspace, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	require.NoError(t, workspace.Scanner().IndexAll(context.Background()))
	blocks, err := workspaceTwigGlobalsIndex(
		t,
		workspace,
	).GetTemplateBlocks("page.html.twig")
	require.NoError(t, err)
	require.Len(t, blocks, 4)
	requireTwigBlocks(t, blocks, "body", "body", "page_actions", "sidebar")
	for _, block := range blocks {
		require.NotZero(t, block.Range.Len())
	}
	require.NoError(t, workspace.Close())

	reopened, err := NewWorkspace(
		context.Background(),
		projectRoot,
		lsp.NewServer(nil, projectRoot, "test"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restored, err := workspaceTwigGlobalsIndex(
		t,
		reopened,
	).GetTemplateBlocks("page.html.twig")
	require.NoError(t, err)
	require.Equal(t, blocks, restored)
}

func requireTwigBlocks(
	t *testing.T,
	blocks []twig.TemplateBlock,
	names ...string,
) {
	t.Helper()
	actual := make([]string, 0, len(blocks))
	for _, block := range blocks {
		actual = append(actual, block.Name)
	}
	require.ElementsMatch(t, names, actual)
}

func workspacePHPIndex(t *testing.T, workspace *Workspace) *php.PHPIndex {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*php.PHPIndex); ok {
			return candidate
		}
	}
	t.Fatal("PHP index is not registered")
	return nil
}

func workspaceSymfonyServiceIndex(
	t *testing.T,
	workspace *Workspace,
) *symfony.ServiceIndex {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*symfony.ServiceIndex); ok {
			return candidate
		}
	}
	t.Fatal("Symfony service index is not registered")
	return nil
}

func workspaceTwigGlobalsIndex(
	t *testing.T,
	workspace *Workspace,
) *twig.TwigIndexer {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*twig.TwigIndexer); ok {
			return candidate
		}
	}
	t.Fatal("Twig index is not registered")
	return nil
}

func workspaceTwigComponentIndex(
	t *testing.T,
	workspace *Workspace,
) *twigcomponent.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*twigcomponent.Index); ok {
			return candidate
		}
	}
	t.Fatal("Twig component index is not registered")
	return nil
}

func workspaceDoctrineIndex(t *testing.T, workspace *Workspace) *doctrine.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*doctrine.Index); ok {
			return candidate
		}
	}
	t.Fatal("Doctrine index is not registered")
	return nil
}

func workspaceAssetIndex(t *testing.T, workspace *Workspace) *asset.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*asset.Index); ok {
			return candidate
		}
	}
	t.Fatal("asset index is not registered")
	return nil
}

func workspaceStimulusIndex(
	t *testing.T,
	workspace *Workspace,
) *stimulus.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*stimulus.Index); ok {
			return candidate
		}
	}
	t.Fatal("Stimulus index is not registered")
	return nil
}

func workspaceEventIndex(t *testing.T, workspace *Workspace) *event.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*event.Index); ok {
			return candidate
		}
	}
	t.Fatal("event index is not registered")
	return nil
}

func workspaceMessengerIndex(
	t *testing.T,
	workspace *Workspace,
) *messenger.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*messenger.Index); ok {
			return candidate
		}
	}
	t.Fatal("Messenger index is not registered")
	return nil
}

func workspaceEnvironmentIndex(
	t *testing.T,
	workspace *Workspace,
) *environment.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*environment.Index); ok {
			return candidate
		}
	}
	t.Fatal("environment index is not registered")
	return nil
}

func workspaceFormIndex(t *testing.T, workspace *Workspace) *form.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*form.Index); ok {
			return candidate
		}
	}
	t.Fatal("form index is not registered")
	return nil
}

func workspaceSecurityIndex(t *testing.T, workspace *Workspace) *security.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*security.Index); ok {
			return candidate
		}
	}
	t.Fatal("security index is not registered")
	return nil
}

func workspaceSerializerIndex(t *testing.T, workspace *Workspace) *serializer.Index {
	t.Helper()
	for _, idx := range workspace.indexers {
		if candidate, ok := idx.(*serializer.Index); ok {
			return candidate
		}
	}
	t.Fatal("serializer index is not registered")
	return nil
}
