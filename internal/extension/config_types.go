package extension

import "gopkg.in/yaml.v3"

// These configuration types mirror shopware-cli/internal/extension/config.go.
// They describe extension metadata, not the LSP's .config/shopware/lsp.yaml.
type ConfigBuild struct {
	// ExtraBundles can be used to declare additional bundles to be considered for building
	ExtraBundles []ConfigExtraBundle `yaml:"extraBundles,omitempty"`
	// Override the shopware version constraint for building, can be used to specify the version of the shopware to use for building
	ShopwareVersionConstraint string `yaml:"shopwareVersionConstraint,omitempty"`
	// Configuration for zipping
	Zip ConfigBuildZip `yaml:"zip"`
}

// Configuration for zipping.
type ConfigBuildZip struct {
	// Configuration for composer
	Composer ConfigBuildZipComposer `yaml:"composer,omitempty"`
	// Configuration for assets
	Assets ConfigBuildZipAssets `yaml:"assets,omitempty"`
	// Configuration for packing
	Pack ConfigBuildZipPack `yaml:"pack,omitempty"`

	Checksum ConfigBuildZipChecksum `yaml:"checksum,omitempty"`
}

// Configuration for checksum calculation.
type ConfigBuildZipChecksum struct {
	// Following files will be excluded from the checksum calculation
	Ignore []string `yaml:"ignore,omitempty"`
}

type ConfigBuildZipComposer struct {
	// When enabled, a vendor folder will be created in the zip build
	Enabled bool `yaml:"enabled"`
	// Commands to run before the composer install
	BeforeHooks []string `yaml:"before_hooks,omitempty"`
	// Commands to run after the composer install
	AfterHooks []string `yaml:"after_hooks,omitempty"`
	// Composer packages to be excluded from the zip build
	ExcludedPackages []string `yaml:"excluded_packages,omitempty"`
}

type ConfigBuildZipAssets struct {
	// When enabled, the shopware-cli build the assets
	Enabled bool `yaml:"enabled"`
	// When enabled, compiled assets are cached and restored across builds, keyed by the source-file content hash
	EnableAssetCaching bool `yaml:"enable_asset_caching"`
	// Commands to run before the assets build
	BeforeHooks []string `yaml:"before_hooks,omitempty"`
	// Commands to run after the assets build
	AfterHooks []string `yaml:"after_hooks,omitempty"`
	// When enabled, builtin esbuild will be used for the admin assets
	EnableESBuildForAdmin bool `yaml:"enable_es_build_for_admin"`
	// When enabled, builtin esbuild will be used for the storefront assets
	EnableESBuildForStorefront bool `yaml:"enable_es_build_for_storefront"`
	// When disabled, builtin sass support will be disabled
	DisableSass bool `yaml:"disable_sass"`
	// When enabled, npm will install only production dependencies
	NpmStrict bool `yaml:"npm_strict"`
	// Additional paths to include in asset caching
	AdditionalCaches []ConfigBuildZipAssetsAdditionalCache `yaml:"additional_caches,omitempty"`
}

type ConfigBuildZipAssetsAdditionalCache struct {
	// The output path to cache, relative to extension root
	Path string `yaml:"path"`
	// Source paths to hash for the cache key, relative to extension root
	SourcePaths []string `yaml:"source_paths"`
}

type ConfigBuildZipPackExcludes struct {
	// Paths to exclude from the zip build
	Paths []string `yaml:"paths,omitempty"`
}

type ConfigBuildZipPack struct {
	// Excludes can be used to exclude files from the zip build
	Excludes ConfigBuildZipPackExcludes `yaml:"excludes,omitempty"`
	// Commands to run before the pack
	BeforeHooks []string `yaml:"before_hooks,omitempty"`
}

type ConfigExtraBundle struct {
	// Path to the bundle, relative from the extension root (src folder)
	Path string `yaml:"path"`
	// Name of the bundle, if empty the folder name of path will be used
	Name string `yaml:"name"`
}

// ConfigStore describes the extension's Store listing metadata.
type ConfigStore struct {
	// Specifies the visibility in stores.
	Availabilities *[]string `yaml:"availabilities" jsonschema:"enum=German,enum=International"`
	// Specifies the default locale.
	DefaultLocale *string `yaml:"default_locale" jsonschema:"enum=de_DE,enum=en_GB"`
	// Specifies the languages the extension is translated.
	Localizations *[]string `yaml:"localizations" jsonschema:"enum=de_DE,enum=en_GB,enum=bs_BA,enum=bg_BG,enum=cs_CZ,enum=da_DK,enum=de_CH,enum=el_GR,enum=en_US,enum=es_ES,enum=fi_FI,enum=fr_FR,enum=hi_IN,enum=hr_HR,enum=hu_HU,enum=hy,enum=id_ID,enum=it_IT,enum=ko_KR,enum=lv_LV,enum=ms_MY,enum=nl_NL,enum=pl_PL,enum=pt_BR,enum=pt_PT,enum=ro_RO,enum=ru_RU,enum=sk_SK,enum=sl_SI,enum=sr_RS,enum=sv_SE,enum=th_TH,enum=tr_TR,enum=uk_UA,enum=vi_VN,enum=zh_CN,enum=zh_TW"`
	// Specifies the type of the extension.
	Type *string `yaml:"type" jsonschema:"enum=extension,enum=theme"`
	// Specifies the Path to the icon (256x256 px) for store.
	Icon *string `yaml:"icon"`
	// Specifies whether the extension should automatically be set compatible with Shopware bugfix versions.
	AutomaticBugfixVersionCompatibility *bool `yaml:"automatic_bugfix_version_compatibility"`
	// Specifies the meta title of the extension in store.
	MetaTitle ConfigTranslated[string] `yaml:"meta_title" jsonschema:"maxLength=50"`
	// Specifies the meta description of the extension in store.
	MetaDescription ConfigTranslated[string] `yaml:"meta_description" jsonschema:"maxLength=185"`
	// Specifies the description of the extension in store.
	Description ConfigTranslated[string] `yaml:"description"`
	// Installation manual of the extension in store.
	InstallationManual ConfigTranslated[string] `yaml:"installation_manual"`
	// Specifies the tags of the extension.
	Tags ConfigTranslated[[]string] `yaml:"tags,omitempty"`
	// Specifies the links of YouTube-Videos to show or describe the extension.
	Videos ConfigTranslated[[]string] `yaml:"videos,omitempty"`
	// Specifies the highlights of the extension.
	Highlights ConfigTranslated[[]string] `yaml:"highlights,omitempty"`
	// Specifies the features of the extension.
	Features ConfigTranslated[[]string] `yaml:"features"`
	// Specifies Frequently Asked Questions for the extension.
	Faq ConfigTranslated[[]ConfigStoreFaq] `yaml:"faq"`
	// Specifies images for the extension in the store.
	Images *[]ConfigStoreImage `yaml:"images,omitempty"`
	// Specifies the directory where the images are located.
	ImageDirectory *string `yaml:"image_directory,omitempty"`
	// Specifies the demo shops of the extension in the store.
	DemoShops *[]ConfigStoreDemoShop `yaml:"demo_shops,omitempty"`
}

type ConfigStoreDemoShop struct {
	// Specifies the type of the demo shop.
	Type string `yaml:"type" jsonschema:"enum=frontend,enum=backend"`
	// Specifies the URL to the demo shop.
	Link string `yaml:"link"`
	// Specifies the language of the demo shop.
	Localization string `yaml:"localization" jsonschema:"enum=de_DE,enum=en_GB"`
	// Specifies the login name to access the demo shop.
	LoginName string `yaml:"login_name,omitempty"`
	// Specifies the login password to access the demo shop.
	LoginPassword string `yaml:"login_password,omitempty"`
}

type ConfigTranslated[T any] struct {
	German  *T `yaml:"de,omitempty"`
	English *T `yaml:"en,omitempty"`
}

type ConfigStoreFaq struct {
	Question string `yaml:"question"`
	Answer   string `yaml:"answer"`
	Position int    `yaml:"position"`
}

type ConfigStoreImage struct {
	// File path to image relative from root of the extension
	File string `yaml:"file"`
	// Specifies whether the image is active in the language.
	Activate ConfigStoreImageActivate `yaml:"activate"`
	// Specifies whether the image is a preview in the language.
	Preview ConfigStoreImagePreview `yaml:"preview"`
	// Position of the image in the store gallery. Images are sorted ascending; the lowest value is shown first.
	Position int `yaml:"position"`
	// Deprecated: use Position instead.
	Priority int `yaml:"priority,omitempty" jsonschema_extras:"deprecated=true"`
}

type ConfigStoreImageActivate struct {
	German  bool `yaml:"de"`
	English bool `yaml:"en"`
}

type ConfigStoreImagePreview struct {
	German  bool `yaml:"de"`
	English bool `yaml:"en"`
}

// ConfigValidation is used to configure the extension validation.
type ConfigValidation struct {
	// Ignore items from the validation.
	Ignore          ConfigValidationList `yaml:"ignore,omitempty"`
	StoreCompliance bool                 `yaml:"store_compliance,omitempty"`
	// PhpVersion overrides the PHP version used for linting (e.g. "8.4").
	// When set, this takes precedence over the version derived from composer.json or the static Shopware-to-PHP mapping.
	PhpVersion string `yaml:"php_version,omitempty"`
}

type ConfigValidationList []ConfigValidationIgnore

type Config struct {
	// Controls date-based compatibility behavior, formatted as YYYY-MM-DD.
	CompatibilityDate string `yaml:"compatibility_date,omitempty" jsonschema:"format=date"`
	// Store is the store configuration of the extension.
	Store ConfigStore `yaml:"store,omitempty"`
	// Build is the build configuration of the extension.
	Build ConfigBuild `yaml:"build,omitempty"`
	// Changelog is the changelog configuration of the extension.
	Changelog ConfigChangelog `yaml:"changelog,omitempty"`
	// Validation is the validation configuration of the extension.
	Validation      ConfigValidation `yaml:"validation,omitempty"`
	StorageLocation string           `yaml:"-"`
	DecodeError     string           `yaml:"-"`
}

// ConfigChangelog mirrors shopware-cli/internal/changelog.Config.
type ConfigChangelog struct {
	Enabled   bool              `yaml:"enabled"`
	Pattern   string            `yaml:"pattern,omitempty"`
	Template  string            `yaml:"template,omitempty"`
	Variables map[string]string `yaml:"variables,omitempty"`
	VCSURL    string            `yaml:"-"`
}

// ConfigValidationIgnore accepts both CLI shorthand identifiers and objects.
type ConfigValidationIgnore struct {
	Identifier string `yaml:"identifier"`
	Path       string `yaml:"path,omitempty"`
	Message    string `yaml:"message,omitempty"`
}

func (ignore *ConfigValidationIgnore) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		ignore.Identifier = value.Value
		return nil
	}
	type object ConfigValidationIgnore
	return value.Decode((*object)(ignore))
}
