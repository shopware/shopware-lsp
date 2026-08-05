package admin

import (
	"os"
	"path/filepath"
	"testing"

	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAdminRuntimeRegistries(t *testing.T) {
	source := `
Application.addServiceProvider('feature', factory)
    .addServiceProvider('acl', factory);
Shopware.Service().register('repositoryFactory', factory);

Shopware.Store.register('profile', {
    state() {
        return { currentUser: null, isLoading: false };
    },
    getters: {
        displayName() { return this.currentUser?.name; },
    },
    actions: {
        load() {},
    },
});
Shopware.Store.register({
    id: 'seoUrl',
    state: () => ({ currentSeoUrl: null }),
});`
	filePath := "/project/src/Administration/Resources/app/administration/src/main.ts"
	root := parseJS(t, source)
	services, stores := parseAdminRuntimeRegistries(
		root,
		filePath,
		syntax.NewLineIndex(source),
	)

	require.Len(t, services, 3)
	serviceByName := make(map[string]AdminService)
	for _, service := range services {
		serviceByName[service.Name] = service
	}
	assert.Equal(t, AdminServiceProvider, serviceByName["feature"].Kind)
	assert.Equal(t, 2, serviceByName["feature"].Line)
	assert.Equal(t, 3, serviceByName["acl"].Line)
	assert.Equal(t, AdminServiceFactory, serviceByName["repositoryFactory"].Kind)

	require.Len(t, stores, 2)
	storeByName := make(map[string]AdminStore)
	for _, store := range stores {
		storeByName[store.Name] = store
	}
	assert.Equal(t, 6, storeByName["profile"].Line)
	assert.Equal(t, 18, storeByName["seoUrl"].Line)
	memberKinds := make(map[string]AdminStoreMemberKind)
	memberTypes := make(map[string]string)
	for _, member := range storeByName["profile"].Members {
		memberKinds[member.Name] = member.Kind
		memberTypes[member.Name] = member.Type
	}
	assert.Equal(t, AdminStoreState, memberKinds["currentUser"])
	assert.Equal(t, AdminStoreState, memberKinds["isLoading"])
	assert.Equal(t, AdminStoreGetter, memberKinds["displayName"])
	assert.Equal(t, AdminStoreAction, memberKinds["load"])
	assert.Equal(t, "null", memberTypes["currentUser"])
	assert.Equal(t, "boolean", memberTypes["isLoading"])
	assert.Equal(t, "() => unknown", memberTypes["load"])
	member, found := storeByName["seoUrl"].Member("currentSeoUrl")
	require.True(t, found)
	assert.Equal(t, AdminStoreState, member.Kind)
}

func TestAdminDirectiveRegistryIndex(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	adminRoot := filepath.Join(
		root, "src/Administration/Resources/app/administration/src",
	)
	filePath := filepath.Join(adminRoot, "app/directive/tooltip.directive.ts")
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		filePath,
		[]byte(`
const { Directive } = Shopware;
Directive.register('tooltip', { mounted() {} });
Shopware.Directive.register('autofocus', { mounted() {} });`),
	)))

	directives, err := idx.GetAllDirectives()
	require.NoError(t, err)
	require.Len(t, directives, 2)
	byName := make(map[string]AdminDirective)
	for _, directive := range directives {
		byName[directive.Name] = directive
	}
	assert.Equal(t, 3, byName["tooltip"].Line)
	assert.Equal(t, filePath, byName["autofocus"].FilePath)

	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		filePath, []byte(`Directive.register('popover', {});`),
	)))
	directives, err = idx.GetAllDirectives()
	require.NoError(t, err)
	require.Len(t, directives, 1)
	assert.Equal(t, "popover", directives[0].Name)
}

func TestAdminFilterRegistryIndexAndLiveOverlay(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	filePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/app/filter/currency.filter.ts",
	)
	persisted := `
const { Filter } = Shopware;
Filter.register('currency', (value: number, currency: string): string => String(value));
Shopware.Filter.register('asset', (path: string) => path);`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		filePath, []byte(persisted),
	)))

	filters, err := idx.GetAllFilters()
	require.NoError(t, err)
	require.Len(t, filters, 2)
	byName := make(map[string]AdminFilter)
	for _, filter := range filters {
		byName[filter.Name] = filter
	}
	assert.Equal(t, 3, byName["currency"].Line)
	assert.Equal(
		t, "(value: number, currency: string) => string",
		byName["currency"].Signature,
	)
	assert.Equal(t, filePath, byName["asset"].FilePath)

	live := `Shopware.Filter.register('date', (value: Date): string => value.toISOString());`
	liveRoot := parseJS(t, live)
	idx.UpdateLiveDocument(
		filePath, liveRoot, live, syntax.NewLineIndex(live),
	)
	filters, err = idx.GetAllFilters()
	require.NoError(t, err)
	require.Len(t, filters, 1)
	assert.Equal(t, "date", filters[0].Name)
	old, err := idx.GetFilter("currency")
	require.NoError(t, err)
	assert.Empty(t, old)

	idx.RemoveLiveDocument(filePath)
	filters, err = idx.GetAllFilters()
	require.NoError(t, err)
	require.Len(t, filters, 2)
}

func TestParseAdminCMSRegistrations(t *testing.T) {
	source := `
Shopware.Service('cmsService').registerCmsElement({
    name: 'hero',
    label: 'cms.hero.label',
    component: 'sw-cms-el-hero',
    configComponent: 'sw-cms-el-config-hero',
    previewComponent: 'sw-cms-el-preview-hero',
});
cmsService.registerCmsBlock({
    name: 'hero-grid',
    category: 'image',
    component: 'sw-cms-block-hero-grid',
    previewComponent: 'sw-cms-preview-hero-grid',
    slots: {
        left: { type: 'hero', default: { config: {} } },
        right: { type: 'image' },
    },
});`
	registrations := parseAdminCMSRegistrations(
		parseJS(t, source),
		"/project/Resources/app/administration/src/cms.ts",
		syntax.NewLineIndex(source),
	)
	require.Len(t, registrations, 2)
	element := registrations[0]
	assert.Equal(t, AdminCMSElement, element.Kind)
	assert.Equal(t, "hero", element.Name)
	assert.Equal(t, "cms.hero.label", element.Label)
	assert.Equal(t, "sw-cms-el-hero", element.Component)
	assert.Equal(t, "sw-cms-el-config-hero", element.ConfigComponent)
	assert.Equal(t, "sw-cms-el-preview-hero", element.PreviewComponent)
	assert.Equal(t, 3, element.Line)
	assert.Less(t, element.NameRange.StartCharacter, element.NameRange.EndCharacter)

	block := registrations[1]
	assert.Equal(t, AdminCMSBlock, block.Kind)
	assert.Equal(t, "hero-grid", block.Name)
	assert.Equal(t, "image", block.Category)
	require.Len(t, block.Slots, 2)
	assert.Equal(t, "hero", block.Slots[0].Name)
	assert.Equal(t, "image", block.Slots[1].Name)
}

func TestAdminCMSRegistryIndex(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	filePath := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src/cms.ts",
	)
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		filePath,
		[]byte(`
Shopware.Service('cmsService').registerCmsElement({
    name: 'hero', component: 'sw-cms-el-hero',
});
Shopware.Service('cmsService').registerCmsBlock({
    name: 'hero-grid', slots: { content: { type: 'hero' } },
});`),
	)))
	elements, err := idx.GetAllCMSRegistrationsByKind(AdminCMSElement)
	require.NoError(t, err)
	require.Len(t, elements, 1)
	assert.Equal(t, "hero", elements[0].Name)
	blocks, err := idx.GetCMSRegistration(AdminCMSBlock, "hero-grid")
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Equal(t, "hero", blocks[0].Slots[0].Name)

	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		filePath,
		[]byte(`Shopware.Service('cmsService').registerCmsElement({ name: 'image' });`),
	)))
	elements, err = idx.GetAllCMSRegistrationsByKind(AdminCMSElement)
	require.NoError(t, err)
	require.Len(t, elements, 1)
	assert.Equal(t, "image", elements[0].Name)
	blocks, err = idx.GetAllCMSRegistrationsByKind(AdminCMSBlock)
	require.NoError(t, err)
	assert.Empty(t, blocks)
}

func TestAdminRuntimeRegistryIndexPrefersProductionDefinitions(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	adminRoot := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src",
	)
	productionPath := filepath.Join(adminRoot, "main.ts")
	testPath := filepath.Join(adminRoot, "main.spec.ts")
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		productionPath,
		[]byte(`
Shopware.Application.addServiceProvider('acl', factory);
Shopware.Store.register('session', { actions: { login() {} } });
`),
	)))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		testPath,
		[]byte(`
Shopware.Service().register('acl', mock);
Shopware.Store.register('session', { actions: { resetMock() {} } });
`),
	)))

	services, err := idx.GetService("acl")
	require.NoError(t, err)
	require.Len(t, services, 1)
	assert.Equal(t, productionPath, services[0].FilePath)

	stores, err := idx.GetStore("session")
	require.NoError(t, err)
	require.Len(t, stores, 1)
	assert.Equal(t, productionPath, stores[0].FilePath)
	_, found := stores[0].Member("login")
	assert.True(t, found)
	_, found = stores[0].Member("resetMock")
	assert.False(t, found)
}

func TestParseAdminSetupStoreFactory(t *testing.T) {
	source := `
interface ContextState { app: object; api: object; }
const currentUser = ref<User | null>(null);
const userPending = computed(() => !currentUser.value);
function login() {}
const logout = () => {};
const state: ContextState = reactive({ app: {}, api: {} });

export default function useSession() {
    return {
        ...state,
        currentUser,
        userPending,
        login,
        logout,
    };
}`
	factory := parseAdminStoreFactory(
		parseJS(t, source),
		"/project/Resources/app/administration/src/use-session.ts",
		syntax.NewLineIndex(source),
	)
	bindings := setupBindings(
		parseJS(t, source), syntax.NewLineIndex(source),
	)
	stateBinding, hasStateBinding := bindings["state"]
	require.True(t, hasStateBinding)
	require.NotNil(t, stateBinding.object)
	require.NotNil(t, factory)
	members := make(map[string]AdminStoreMember)
	for _, member := range factory.Members {
		members[member.Name] = member
	}
	for _, name := range []string{
		"app", "api", "currentUser", "userPending", "login", "logout",
	} {
		assert.Contains(t, members, name)
	}
	assert.NotContains(t, members, "state")
	assert.Equal(t, AdminStoreState, members["currentUser"].Kind)
	assert.Equal(t, AdminStoreGetter, members["userPending"].Kind)
	assert.Equal(t, AdminStoreAction, members["login"].Kind)
	assert.Equal(t, AdminStoreAction, members["logout"].Kind)
	assert.Equal(t, 3, members["currentUser"].Line)
	assert.Equal(t, 5, members["login"].Line)
	assert.Equal(t, "User | null", members["currentUser"].Type)
	assert.Equal(t, "() => unknown", members["login"].Type)
}

func TestAdminImportedSetupStoreResolvesIndependently(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	adminRoot := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src",
	)
	storePath := filepath.Join(adminRoot, "app/store/session.store.ts")
	factoryPath := filepath.Join(adminRoot, "app/composables/use-session.ts")
	require.NoError(t, os.MkdirAll(filepath.Dir(storePath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(factoryPath), 0o755))
	require.NoError(t, os.WriteFile(factoryPath, []byte(""), 0o644))

	storeSource := `
import useSession from '../composables/use-session';
export default Shopware.Store.register('session', useSession);`
	factorySource := `
const currentUser = ref(null);
function setCurrentUser() {}
export default function useSession() {
    return { currentUser, setCurrentUser };
}`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		storePath,
		[]byte(storeSource),
	)))
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		factoryPath,
		[]byte(factorySource),
	)))

	stores, err := idx.GetStore("session")
	require.NoError(t, err)
	require.Len(t, stores, 1)
	assert.Equal(t, "useSession", stores[0].FactoryName)
	assert.Equal(t, factoryPath, stores[0].FactoryPath)
	currentUser, found := stores[0].Member("currentUser")
	require.True(t, found)
	assert.Equal(t, factoryPath, currentUser.FilePath)
	setCurrentUser, found := stores[0].Member("setCurrentUser")
	require.True(t, found)
	assert.Equal(t, AdminStoreAction, setCurrentUser.Kind)

	updatedFactory := `
const languageId = ref('');
export default function useSession() {
    return { languageId };
}`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		factoryPath,
		[]byte(updatedFactory),
	)))
	stores, err = idx.GetStore("session")
	require.NoError(t, err)
	require.Len(t, stores, 1)
	_, found = stores[0].Member("currentUser")
	assert.False(t, found)
	_, found = stores[0].Member("languageId")
	assert.True(t, found)
}

func TestAdminServiceResolvesImportedImplementation(t *testing.T) {
	root := t.TempDir()
	idx, err := NewAdminComponentIndexer(filepath.Join(root, "cache"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })

	adminRoot := filepath.Join(
		root,
		"src/Administration/Resources/app/administration/src",
	)
	registrationPath := filepath.Join(adminRoot, "app/main.ts")
	implementationPath := filepath.Join(adminRoot, "app/service/acl.service.ts")
	require.NoError(t, os.MkdirAll(filepath.Dir(implementationPath), 0o755))
	require.NoError(t, os.WriteFile(implementationPath, []byte(""), 0o644))
	source := `
import AclService from './service/acl.service';
Shopware.Application.addServiceProvider('acl', () => {
    return new AclService();
});`
	require.NoError(t, idx.Index(indexerpkg.NewParsedFile(
		registrationPath,
		[]byte(source),
	)))

	services, err := idx.GetService("acl")
	require.NoError(t, err)
	require.Len(t, services, 1)
	assert.Equal(t, "AclService", services[0].ImplementationName)
	assert.Equal(t, implementationPath, services[0].ImplementationPath)
}

func TestAdminFluentServicesKeepOwnImplementation(t *testing.T) {
	source := `
import FirstService from './first.service';
import SecondService from './second.service';
Application.addServiceProvider('first', () => new FirstService())
    .addServiceProvider('second', () => new SecondService());`
	services, _ := parseAdminRuntimeRegistries(
		parseJS(t, source),
		"/project/Resources/app/administration/src/main.ts",
		syntax.NewLineIndex(source),
	)
	require.Len(t, services, 2)
	byName := make(map[string]AdminService)
	for _, service := range services {
		byName[service.Name] = service
	}
	assert.Equal(t, "FirstService", byName["first"].ImplementationName)
	assert.Equal(t, "SecondService", byName["second"].ImplementationName)
}
