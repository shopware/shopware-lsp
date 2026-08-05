package symfony

import (
	"slices"
	"sort"
	"strings"
)

var requiredServiceTagTypes = map[string][]string{
	"assetic.asset": {
		"Assetic\\Filter\\FilterInterface",
	},
	"assetic.factory_worker": {
		"Assetic\\Factory\\Worker\\WorkerInterface",
	},
	"assetic.filter": {
		"Assetic\\Filter\\FilterInterface",
	},
	"assetic.formula_loader": {
		"Assetic\\Factory\\Loader\\FormulaLoaderInterface",
	},
	"console.command": {
		"Symfony\\Component\\Console\\Command\\Command",
	},
	"data_collector": {
		"Symfony\\Component\\HttpKernel\\DataCollector\\DataCollectorInterface",
	},
	"form.type": {
		"Symfony\\Component\\Form\\FormTypeInterface",
	},
	"form.type_extension": {
		"Symfony\\Component\\Form\\FormTypeExtensionInterface",
	},
	"form.type_guesser": {
		"Symfony\\Component\\Form\\FormTypeGuesserInterface",
	},
	"kernel.cache_warmer": {
		"Symfony\\Component\\HttpKernel\\CacheWarmer\\CacheWarmerInterface",
	},
	"kernel.event_subscriber": {
		"Symfony\\Component\\EventDispatcher\\EventSubscriberInterface",
	},
	"kernel.fragment_renderer": {
		"Symfony\\Component\\HttpKernel\\Fragment\\FragmentRendererInterface",
	},
	"routing.loader": {
		"Symfony\\Component\\Config\\Loader\\LoaderInterface",
	},
	"security.voter": {
		"Symfony\\Component\\Security\\Core\\Authorization\\Voter\\VoterInterface",
	},
	"serializer.encoder": {
		"Symfony\\Component\\Serializer\\Encoder\\EncoderInterface",
	},
	"serializer.normalizer": {
		"Symfony\\Component\\Serializer\\Normalizer\\NormalizerInterface",
	},
	"swiftmailer.default.plugin": {
		"Swift_Events_EventListener",
	},
	"templating.helper": {
		"Symfony\\Component\\Templating\\Helper\\HelperInterface",
	},
	"translation.loader": {
		"Symfony\\Component\\Translation\\Loader\\LoaderInterface",
	},
	"translation.extractor": {
		"Symfony\\Component\\Translation\\Extractor\\ExtractorInterface",
	},
	"translation.dumper": {
		"Symfony\\Component\\Translation\\Dumper\\DumperInterface",
	},
	"twig.extension": {
		"Twig\\Extension\\ExtensionInterface",
		"Twig_ExtensionInterface",
	},
	"twig.loader": {
		"Twig\\Loader\\LoaderInterface",
		"Twig_LoaderInterface",
	},
	"validator.constraint_validator": {
		"Symfony\\Component\\Validator\\ConstraintValidator",
	},
	"validator.initializer": {
		"Symfony\\Component\\Validator\\ObjectInitializerInterface",
	},
	"routing.expression_language_provider": {
		"Symfony\\Component\\ExpressionLanguage\\ExpressionFunctionProviderInterface",
	},
	"security.expression_language_provider": {
		"Symfony\\Component\\ExpressionLanguage\\ExpressionFunctionProviderInterface",
	},
}

// RequiredServiceTagTypes returns alternative base classes/interfaces accepted
// for a conventional Symfony service tag. An empty result means that the tag
// has no portable type contract.
func RequiredServiceTagTypes(tag string) []string {
	return slices.Clone(requiredServiceTagTypes[tag])
}

// ServiceTagTypeHierarchy is the small part of the PHP semantic graph needed
// to infer conventional Symfony tags from a configured service class.
type ServiceTagTypeHierarchy interface {
	IsSubtypeOf(candidate, target string) bool
}

// SuggestedServiceTags returns the conventional tags implied by the class
// hierarchy. The result is stable and contains each tag at most once.
func SuggestedServiceTags(
	className string,
	hierarchy ServiceTagTypeHierarchy,
) []string {
	className = strings.Trim(strings.TrimSpace(className), "\\")
	if className == "" || hierarchy == nil {
		return nil
	}

	result := make([]string, 0, 2)
	for tag, targets := range requiredServiceTagTypes {
		for _, target := range targets {
			if hierarchy.IsSubtypeOf(className, target) {
				result = append(result, tag)
				break
			}
		}
	}
	sort.Strings(result)
	return result
}
