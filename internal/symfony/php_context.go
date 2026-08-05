package symfony

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

type PHPConfigReferenceKind uint8

// PHPAutowireCallableMethodReference describes the method and receiver
// configured by Symfony's AutowireCallable attribute. Class keeps the
// unresolved PHP name from a Foo::class expression; callers with a document
// root can resolve imports without making this low-level context package own
// workspace semantics.
type PHPAutowireCallableMethodReference struct {
	Service string
	Class   string
	Method  string
	Range   cst.TextRange
}

const (
	PHPParameterBagInterface = "Symfony\\Component\\DependencyInjection\\" +
		"ParameterBag\\ParameterBagInterface"
	PHPContainerBagInterface = "Symfony\\Component\\DependencyInjection\\" +
		"ParameterBag\\ContainerBagInterface"
	PHPDIContainerInterface = "Symfony\\Component\\DependencyInjection\\" +
		"ContainerInterface"
	PHPParametersConfigurator = "Symfony\\Component\\DependencyInjection\\" +
		"Loader\\Configurator\\ParametersConfigurator"
)

func PHPParameterBagTypes() []string {
	return []string{
		PHPParameterBagInterface,
		PHPContainerBagInterface,
	}
}

// PHPParameterAccessTypes returns the receiver types for Symfony parameter
// APIs that provide completion and navigation at their first string argument.
func PHPParameterAccessTypes(method string) []string {
	switch method {
	case "get", "has":
		return PHPParameterBagTypes()
	case "set":
		return []string{
			PHPParameterBagInterface,
			PHPParametersConfigurator,
		}
	case "getParameter", "hasParameter":
		return []string{PHPDIContainerInterface}
	default:
		return nil
	}
}

// PHPParameterReadAccessTypes excludes declaration APIs such as set(), which
// must not report a missing parameter when introducing a new name.
func PHPParameterReadAccessTypes(method string) []string {
	switch method {
	case "get", "has", "getParameter", "hasParameter":
		return PHPParameterAccessTypes(method)
	default:
		return nil
	}
}

const (
	PHPConfigReferenceNone PHPConfigReferenceKind = iota
	PHPConfigReferenceService
	PHPConfigReferenceParameter
	PHPConfigReferenceTag
	PHPConfigReferenceClass
)

// PHPConfigReferenceAt classifies string arguments used by Symfony's PHP
// configurator helpers and definition configurators.
func PHPConfigReferenceAt(node *phpsyntax.Node) PHPConfigReferenceKind {
	if phpquery.StringAt(node) == nil {
		return PHPConfigReferenceNone
	}
	index := phpquery.StringArgumentIndex(node)
	call := phpquery.CallAt(node)
	name := shortPHPCallName(phpquery.CallMethodName(call))
	switch name {
	case "service", "service_closure":
		if index == 0 {
			return PHPConfigReferenceService
		}
	case "get":
		if index == 0 {
			return PHPConfigReferenceService
		}
	case "alias":
		if index == 1 {
			return PHPConfigReferenceService
		}
	case "param":
		if index == 0 {
			return PHPConfigReferenceParameter
		}
	case "tagged_iterator", "tagged_locator":
		if index == 0 {
			return PHPConfigReferenceTag
		}
	}
	return PHPConfigReferenceNone
}

func PHPConfigReferenceValue(node *phpsyntax.Node) string {
	return strings.ReplaceAll(phpquery.StringValue(node), `\\`, `\`)
}

func PHPAttributeReferenceAt(node *phpsyntax.Node) PHPConfigReferenceKind {
	literal := phpquery.StringAt(node)
	attribute := phpquery.AttributeAt(literal)
	if literal == nil || attribute == nil {
		return PHPConfigReferenceNone
	}
	name := shortPHPCallName(phpquery.AttributeName(attribute))
	index := phpquery.ArgumentIndex(attribute, literal)
	argumentName := ""
	if index >= 0 {
		argumentName = phpquery.ArgumentName(phpquery.Argument(attribute, index))
	}
	switch name {
	case "Autowire":
		switch argumentName {
		case "service":
			return PHPConfigReferenceService
		case "param":
			return PHPConfigReferenceParameter
		}
		if index == 0 {
			value := strings.TrimSpace(phpquery.StringValue(literal))
			if _, ok := NormalizeServiceReference(value); ok {
				return PHPConfigReferenceService
			}
			if len(ParameterReferences(value)) != 0 {
				return PHPConfigReferenceParameter
			}
			if strings.HasPrefix(value, "@") && !strings.HasPrefix(value, "@@") {
				return PHPConfigReferenceService
			}
			if strings.HasPrefix(value, "%") {
				return PHPConfigReferenceParameter
			}
		}
	case "AutowireLocator":
		if index == 0 ||
			argumentName == "services" ||
			argumentName == "exclude" {
			return PHPConfigReferenceService
		}
	case "AutowireServiceClosure", "AutowireMethodOf":
		if index == 0 || argumentName == "service" {
			return PHPConfigReferenceService
		}
	case "AutowireCallable":
		if argumentName == "service" {
			return PHPConfigReferenceService
		}
	case "TaggedIterator", "TaggedLocator":
		if index == 0 || argumentName == "tag" {
			return PHPConfigReferenceTag
		}
	case "Autoconfigure":
		if index == 0 || argumentName == "tags" {
			return PHPConfigReferenceTag
		}
	case "AutoconfigureTag":
		if index == 0 || argumentName == "name" {
			return PHPConfigReferenceTag
		}
	case "AsDecorator":
		if index == 0 || argumentName == "decorates" {
			return PHPConfigReferenceService
		}
	}
	return PHPConfigReferenceNone
}

// PHPWhenEnvironmentAt recognizes the deployment-environment argument of
// Symfony's #[When] attribute.
func PHPWhenEnvironmentAt(node *phpsyntax.Node) bool {
	literal := phpquery.StringAt(node)
	attribute := phpquery.AttributeAt(literal)
	if literal == nil || attribute == nil ||
		shortPHPCallName(phpquery.AttributeName(attribute)) != "When" {
		return false
	}
	index := phpquery.ArgumentIndex(attribute, literal)
	if index < 0 {
		return false
	}
	argumentName := phpquery.ArgumentName(phpquery.Argument(attribute, index))
	return index == 0 || argumentName == "env"
}

// PHPAutowireCallableMethodAt recognizes the method argument of
// #[AutowireCallable(...)] and extracts the service receiver from the same
// attribute. The receiver may be either a service ID string or Foo::class.
func PHPAutowireCallableMethodAt(
	node *phpsyntax.Node,
) (PHPAutowireCallableMethodReference, bool) {
	literal := phpquery.StringAt(node)
	attribute := phpquery.AttributeAt(literal)
	if literal == nil || attribute == nil ||
		shortPHPCallName(phpquery.AttributeName(attribute)) !=
			"AutowireCallable" {
		return PHPAutowireCallableMethodReference{}, false
	}
	index := phpquery.ArgumentIndex(attribute, literal)
	if index < 0 ||
		phpquery.ArgumentName(phpquery.Argument(attribute, index)) != "method" {
		return PHPAutowireCallableMethodReference{}, false
	}

	reference := PHPAutowireCallableMethodReference{
		Method: phpquery.StringValue(literal),
		Range:  phpquery.StringContentRange(literal),
	}
	for argumentIndex, argument := range phpquery.Arguments(attribute) {
		if phpquery.ArgumentName(argument) != "service" {
			continue
		}
		if serviceLiteral := phpquery.StringArgument(
			attribute,
			argumentIndex,
		); serviceLiteral != nil {
			reference.Service = phpquery.StringValue(serviceLiteral)
		} else {
			reference.Class = phpquery.ClassConstantName(argument)
		}
		break
	}
	if reference.Service == "" && reference.Class == "" {
		return PHPAutowireCallableMethodReference{}, false
	}
	return reference, true
}

func shortPHPCallName(name string) string {
	if index := strings.LastIndex(name, "\\"); index >= 0 {
		return name[index+1:]
	}
	return name
}
