// Package catalog defines the compact, generated PHP runtime stub format.
package catalog

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/vmihailenco/msgpack/v5"
)

const FormatVersion uint8 = 6

// Catalog is the deterministic output of the PhpStorm stubs generator.
// Records that are identical in several PHP versions share one entry and use
// a bit mask to describe the versions in which they exist.
type Catalog struct {
	_msgpack struct{} `msgpack:",as_array"` //nolint:unused // Encoding layout marker.

	Format             uint8
	Repository         string
	Commit             string
	Versions           []Version
	Symbols            []Symbol
	Contracts          []semantic.CallContract
	ContractExtensions []string
	Bundles            []Bundle
	ExtensionSymbols   []ExtensionSymbol
}

// Bundle stores one extension's catalog payload as an independently encoded
// MessagePack blob. Decoding the catalog header therefore does not retain type
// graphs for every optional extension.
type Bundle struct {
	_msgpack struct{} `msgpack:",as_array"` //nolint:unused // Encoding layout marker.

	Extension string
	Data      []byte
}

type ExtensionSymbol struct {
	_msgpack struct{} `msgpack:",as_array"` //nolint:unused // Encoding layout marker.

	Name      string
	Extension string
}

type bundlePayload struct {
	_msgpack struct{} `msgpack:",as_array"` //nolint:unused // Encoding layout marker.

	Symbols   []Symbol
	Contracts []semantic.CallContract
}

type Version struct {
	_msgpack struct{} `msgpack:",as_array"` //nolint:unused // Encoding layout marker.

	Major uint8
	Minor uint8
}

type Symbol struct {
	_msgpack struct{} `msgpack:",as_array"` //nolint:unused // Encoding layout marker.

	VersionMask        uint16
	Extension          string
	Kind               semantic.SymbolKind
	Name               string
	FullyQualified     string
	Container          string
	Visibility         semantic.Visibility
	WriteVisibility    semantic.Visibility
	HasWriteVisibility bool
	Flags              semantic.Flags
	Type               types.Type
	NativeType         types.Type
	DocType            types.Type
	ReturnType         types.Type
	Parameters         []Parameter
	Templates          []TemplateParameter
	Extends            []string
	Implements         []string
	Traits             []string
	ExtendsTypes       []types.Type
	ImplementsTypes    []types.Type
	TraitTypes         []types.Type
	Throws             []types.Type
	Attributes         []semantic.Attribute
	DocSummary         string
}

type Parameter struct {
	_msgpack struct{} `msgpack:",as_array"` //nolint:unused // Encoding layout marker.

	Name          string
	Type          types.Type
	NativeType    types.Type
	DocType       types.Type
	AssistantTags []string
	Attributes    []semantic.Attribute
	DefaultValue  *semantic.AttributeValue
	Flags         semantic.Flags
	Optional      bool
}

type TemplateParameter struct {
	_msgpack struct{} `msgpack:",as_array"` //nolint:unused // Encoding layout marker.

	Name          string
	Bound         types.Type
	Default       types.Type
	Covariant     bool
	Contravariant bool
}

func Encode(value Catalog) ([]byte, error) {
	return msgpack.Marshal(value)
}

// PackBundles converts generator output into lazy extension payloads. It is
// deterministic and clears the expanded slices so the embedded catalog can be
// decoded without materializing all optional declarations.
func (c *Catalog) PackBundles() error {
	if c == nil || len(c.Bundles) > 0 {
		return nil
	}
	type sourceBundle struct {
		symbols   []Symbol
		contracts []semantic.CallContract
	}
	grouped := make(map[string]*sourceBundle)
	bundle := func(extension string) *sourceBundle {
		extension = normalizeExtension(extension)
		current := grouped[extension]
		if current == nil {
			current = &sourceBundle{}
			grouped[extension] = current
		}
		return current
	}
	seenNames := make(map[string]struct{})
	for _, symbol := range c.Symbols {
		extension := normalizeExtension(symbol.Extension)
		bundle(extension).symbols = append(bundle(extension).symbols, symbol)
		if symbol.Container != "" || symbol.FullyQualified == "" || extension == "" {
			continue
		}
		key := strings.ToLower(symbol.FullyQualified) + "\x00" + extension
		if _, exists := seenNames[key]; exists {
			continue
		}
		seenNames[key] = struct{}{}
		c.ExtensionSymbols = append(c.ExtensionSymbols, ExtensionSymbol{
			Name:      symbol.FullyQualified,
			Extension: extension,
		})
	}
	for index, contract := range c.Contracts {
		extension := ""
		if index < len(c.ContractExtensions) {
			extension = c.ContractExtensions[index]
		}
		bundle(extension).contracts = append(
			bundle(extension).contracts,
			contract,
		)
	}
	extensions := make([]string, 0, len(grouped))
	for extension := range grouped {
		extensions = append(extensions, extension)
	}
	sort.Strings(extensions)
	for _, extension := range extensions {
		current := grouped[extension]
		data, err := msgpack.Marshal(bundlePayload{
			Symbols:   current.symbols,
			Contracts: current.contracts,
		})
		if err != nil {
			return fmt.Errorf("encode PHP stub bundle %s: %w", extension, err)
		}
		c.Bundles = append(c.Bundles, Bundle{
			Extension: extension,
			Data:      data,
		})
	}
	sort.Slice(c.ExtensionSymbols, func(left, right int) bool {
		if c.ExtensionSymbols[left].Name != c.ExtensionSymbols[right].Name {
			return c.ExtensionSymbols[left].Name < c.ExtensionSymbols[right].Name
		}
		return c.ExtensionSymbols[left].Extension <
			c.ExtensionSymbols[right].Extension
	})
	c.Symbols = nil
	c.Contracts = nil
	c.ContractExtensions = nil
	return nil
}

func Decode(data []byte) (Catalog, error) {
	var value Catalog
	if err := msgpack.Unmarshal(data, &value); err != nil {
		return Catalog{}, fmt.Errorf("decode PHP stub catalog: %w", err)
	}
	if value.Format != FormatVersion {
		return Catalog{}, fmt.Errorf(
			"decode PHP stub catalog: unsupported format %d",
			value.Format,
		)
	}
	if len(value.Versions) == 0 || len(value.Versions) > 16 {
		return Catalog{}, fmt.Errorf(
			"decode PHP stub catalog: invalid version count %d",
			len(value.Versions),
		)
	}
	return value, nil
}

// VersionMask returns the bit for the nearest generated PHP version at or
// below the requested version. Requests older than the catalog use its oldest
// entry; newer PHP releases use the newest known entry.
func (c Catalog) VersionMask(version project.Version) uint16 {
	if len(c.Versions) == 0 {
		return 0
	}
	selected := 0
	for index, candidate := range c.Versions {
		if int(candidate.Major) > version.Major ||
			(int(candidate.Major) == version.Major && int(candidate.Minor) > version.Minor) {
			break
		}
		selected = index
	}
	return uint16(1) << selected
}

// Materialize creates an independent semantic document payload for one PHP
// version. Generated strings and immutable type graphs are shared; mutable
// slices are copied because the hand-written semantic overlay may refine them.
func (c Catalog) Materialize(version project.Version, path string) []semantic.Symbol {
	return c.MaterializeForExtensions(version, path, nil)
}

// MaterializeForExtensions loads only records belonging to the selected
// extension bundles. A nil selection preserves the complete catalog for tests
// and tools that intentionally request every runtime extension.
func (c Catalog) MaterializeForExtensions(
	version project.Version,
	path string,
	extensions []string,
) []semantic.Symbol {
	mask := c.VersionMask(version)
	if mask == 0 {
		return nil
	}
	selected := extensionSelection(extensions)
	sources := c.Symbols
	if len(c.Bundles) > 0 {
		for _, bundle := range c.Bundles {
			if selected != nil {
				if _, enabled := selected[normalizeExtension(bundle.Extension)]; !enabled {
					continue
				}
			}
			payload, err := decodeBundle(bundle)
			if err != nil {
				panic(err)
			}
			sources = append(sources, payload.Symbols...)
		}
	}
	result := make([]semantic.Symbol, 0, len(sources))
	containerNames := make([]string, 0, len(sources))
	for _, source := range sources {
		if source.VersionMask&mask == 0 {
			continue
		}
		if selected != nil && source.Extension != "" {
			if _, enabled := selected[normalizeExtension(source.Extension)]; !enabled {
				continue
			}
		}
		id := semantic.NewSymbolID(source.Kind, source.FullyQualified, path, 0)
		parameters := make([]semantic.Parameter, len(source.Parameters))
		for index, parameter := range source.Parameters {
			parameters[index] = semantic.Parameter{
				ID: semantic.NewSymbolID(
					semantic.ParameterSymbol,
					source.FullyQualified+":"+parameter.Name,
					path,
					0,
				),
				Name:          parameter.Name,
				Type:          parameter.Type,
				NativeType:    parameter.NativeType,
				DocType:       parameter.DocType,
				AssistantTags: slices.Clone(parameter.AssistantTags),
				Attributes:    cloneAttributes(parameter.Attributes),
				Flags:         parameter.Flags,
				Optional:      parameter.Optional,
			}
			if parameter.DefaultValue != nil {
				value := cloneAttributeValue(*parameter.DefaultValue)
				parameters[index].DefaultValue = &value
			}
		}
		templates := make([]semantic.TemplateParameter, len(source.Templates))
		for index, template := range source.Templates {
			templates[index] = semantic.TemplateParameter{
				Name:          template.Name,
				Bound:         template.Bound,
				Default:       template.Default,
				Covariant:     template.Covariant,
				Contravariant: template.Contravariant,
			}
		}
		attributes := cloneAttributes(source.Attributes)
		result = append(result, semantic.Symbol{
			ID:                 id,
			Kind:               source.Kind,
			Name:               source.Name,
			FullyQualified:     source.FullyQualified,
			Container:          "",
			Path:               path,
			Visibility:         source.Visibility,
			WriteVisibility:    source.WriteVisibility,
			HasWriteVisibility: source.HasWriteVisibility,
			Flags:              source.Flags | semantic.InternalFlag,
			Type:               source.Type,
			NativeType:         source.NativeType,
			DocType:            source.DocType,
			ReturnType:         source.ReturnType,
			Parameters:         parameters,
			Templates:          templates,
			Extends:            slices.Clone(source.Extends),
			Implements:         slices.Clone(source.Implements),
			Traits:             slices.Clone(source.Traits),
			ExtendsTypes:       slices.Clone(source.ExtendsTypes),
			ImplementsTypes:    slices.Clone(source.ImplementsTypes),
			TraitTypes:         slices.Clone(source.TraitTypes),
			Throws:             slices.Clone(source.Throws),
			Attributes:         attributes,
			DocSummary:         source.DocSummary,
		})
		containerNames = append(containerNames, source.Container)
	}

	owners := make(map[string]semantic.SymbolID)
	for _, symbol := range result {
		if symbol.IsClassLike() {
			owners[strings.ToLower(symbol.FullyQualified)] = symbol.ID
		}
	}
	for index := range result {
		if containerNames[index] == "" {
			continue
		}
		if owner, ok := owners[strings.ToLower(containerNames[index])]; ok {
			result[index].Container = owner
		}
	}
	return result
}

func cloneAttributes(source []semantic.Attribute) []semantic.Attribute {
	if source == nil {
		return nil
	}
	result := slices.Clone(source)
	for attributeIndex := range result {
		arguments := slices.Clone(source[attributeIndex].Arguments)
		result[attributeIndex].Arguments = arguments
		for argumentIndex := range arguments {
			arguments[argumentIndex].Value = cloneAttributeValue(
				source[attributeIndex].Arguments[argumentIndex].Value,
			)
		}
	}
	return result
}

func cloneAttributeValue(
	source semantic.AttributeValue,
) semantic.AttributeValue {
	result := source
	result.Items = slices.Clone(source.Items)
	for index := range result.Items {
		result.Items[index].Key = cloneAttributeValue(source.Items[index].Key)
		result.Items[index].Value = cloneAttributeValue(
			source.Items[index].Value,
		)
	}
	return result
}

// MaterializeContracts returns an independent slice of dynamic call metadata.
// Contract values are immutable and contain no mutable nested collections.
func (c Catalog) MaterializeContracts() []semantic.CallContract {
	if len(c.Bundles) > 0 {
		return c.MaterializeContractsForExtensions(nil)
	}
	return slices.Clone(c.Contracts)
}

func (c Catalog) MaterializeContractsForExtensions(
	extensions []string,
) []semantic.CallContract {
	selected := extensionSelection(extensions)
	if len(c.Bundles) > 0 {
		var result []semantic.CallContract
		for _, bundle := range c.Bundles {
			if selected != nil {
				if _, enabled := selected[normalizeExtension(bundle.Extension)]; !enabled {
					continue
				}
			}
			payload, err := decodeBundle(bundle)
			if err != nil {
				panic(err)
			}
			result = append(result, payload.Contracts...)
		}
		return result
	}
	if selected == nil || len(c.ContractExtensions) != len(c.Contracts) {
		return slices.Clone(c.Contracts)
	}
	result := make([]semantic.CallContract, 0, len(c.Contracts))
	for index, contract := range c.Contracts {
		extension := normalizeExtension(c.ContractExtensions[index])
		if extension != "" {
			if _, enabled := selected[extension]; !enabled {
				continue
			}
		}
		result = append(result, contract)
	}
	return result
}

func decodeBundle(bundle Bundle) (bundlePayload, error) {
	var payload bundlePayload
	if err := msgpack.Unmarshal(bundle.Data, &payload); err != nil {
		return bundlePayload{}, fmt.Errorf(
			"decode PHP stub bundle %s: %w",
			bundle.Extension,
			err,
		)
	}
	return payload, nil
}

func extensionSelection(extensions []string) map[string]struct{} {
	if extensions == nil {
		return nil
	}
	result := make(map[string]struct{}, len(extensions))
	for _, extension := range extensions {
		if extension = normalizeExtension(extension); extension != "" {
			result[extension] = struct{}{}
		}
	}
	return result
}

func normalizeExtension(extension string) string {
	extension = strings.ToLower(strings.TrimSpace(extension))
	extension = strings.TrimPrefix(extension, "ext-")
	return strings.ReplaceAll(extension, "-", "_")
}
