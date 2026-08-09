package semantic

import (
	"fmt"
	"math"
	"slices"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"
)

type workspaceHierarchy struct {
	extendsData    *string
	implementsData *string
	traitsData     *string
	Types          *workspaceHierarchyTypes
	Extras         *workspaceHierarchyExtras
	lengths        uint64
}

type workspaceHierarchyExtras struct {
	Aliases []TraitAlias
}

type workspaceHierarchyTypes struct {
	extendsData    *types.Type
	implementsData *types.Type
	traitsData     *types.Type
	lengths        uint64
}

const (
	workspaceHierarchyLengthBits = 21
	workspaceHierarchyLengthMask = 1<<workspaceHierarchyLengthBits - 1
)

func newWorkspaceHierarchy(
	extends,
	implements,
	traits []string,
	extendsTypes,
	implementsTypes,
	traitTypes []types.Type,
	aliases []TraitAlias,
) workspaceHierarchy {
	hierarchy := workspaceHierarchy{
		extendsData:    workspaceSliceData(extends),
		implementsData: workspaceSliceData(implements),
		traitsData:     workspaceSliceData(traits),
		lengths: packWorkspaceHierarchyLengths(
			len(extends),
			len(implements),
			len(traits),
		),
	}
	if len(aliases) != 0 {
		hierarchy.Extras = &workspaceHierarchyExtras{
			Aliases: slices.Clone(aliases),
		}
	}
	if len(extendsTypes) != 0 ||
		len(implementsTypes) != 0 ||
		len(traitTypes) != 0 {
		hierarchy.Types = &workspaceHierarchyTypes{
			extendsData:    workspaceSliceData(extendsTypes),
			implementsData: workspaceSliceData(implementsTypes),
			traitsData:     workspaceSliceData(traitTypes),
			lengths: packWorkspaceHierarchyLengths(
				len(extendsTypes),
				len(implementsTypes),
				len(traitTypes),
			),
		}
	}
	return hierarchy
}

func packWorkspaceHierarchyLengths(first, second, third int) uint64 {
	for _, length := range [...]int{first, second, third} {
		if length < 0 || length > workspaceHierarchyLengthMask {
			panic("semantic: workspace hierarchy collection exceeds packed range")
		}
	}
	return uint64(first) |
		uint64(second)<<workspaceHierarchyLengthBits |
		uint64(third)<<(workspaceHierarchyLengthBits*2)
}

func workspaceHierarchyLength(lengths uint64, index int) uint32 {
	return uint32(
		lengths >> (workspaceHierarchyLengthBits * index) &
			workspaceHierarchyLengthMask,
	)
}

func (hierarchy *workspaceHierarchy) extends() []string {
	if hierarchy == nil {
		return nil
	}
	return workspaceSlice(
		hierarchy.extendsData,
		workspaceHierarchyLength(hierarchy.lengths, 0),
	)
}

func (hierarchy *workspaceHierarchy) implements() []string {
	if hierarchy == nil {
		return nil
	}
	return workspaceSlice(
		hierarchy.implementsData,
		workspaceHierarchyLength(hierarchy.lengths, 1),
	)
}

func (hierarchy *workspaceHierarchy) traits() []string {
	if hierarchy == nil {
		return nil
	}
	return workspaceSlice(
		hierarchy.traitsData,
		workspaceHierarchyLength(hierarchy.lengths, 2),
	)
}

func (hierarchy *workspaceHierarchy) extendsTypes() []types.Type {
	if hierarchy == nil || hierarchy.Types == nil {
		return nil
	}
	return workspaceSlice(
		hierarchy.Types.extendsData,
		workspaceHierarchyLength(hierarchy.Types.lengths, 0),
	)
}

func (hierarchy *workspaceHierarchy) implementsTypes() []types.Type {
	if hierarchy == nil || hierarchy.Types == nil {
		return nil
	}
	return workspaceSlice(
		hierarchy.Types.implementsData,
		workspaceHierarchyLength(hierarchy.Types.lengths, 1),
	)
}

func (hierarchy *workspaceHierarchy) traitTypes() []types.Type {
	if hierarchy == nil || hierarchy.Types == nil {
		return nil
	}
	return workspaceSlice(
		hierarchy.Types.traitsData,
		workspaceHierarchyLength(hierarchy.Types.lengths, 2),
	)
}

func (hierarchy *workspaceHierarchy) aliases() []TraitAlias {
	if hierarchy == nil || hierarchy.Extras == nil {
		return nil
	}
	return hierarchy.Extras.Aliases
}

type workspaceMetadata struct {
	DocSummary string
	Extras     *workspaceMetadataExtras
}

// workspaceMetadataExtras keeps the two less-common metadata collections out
// of documentation-only records. Immutable retained slices need only their
// first element and length; their capacity is irrelevant after publication.
type workspaceMetadataExtras struct {
	attributesData    *Attribute
	constantArrayData *ConstantArrayItem
	lengths           uint64
}

const workspaceMetadataLengthBits = 32

func newWorkspaceMetadata(
	attributes []Attribute,
	constantArray []ConstantArrayItem,
	docSummary string,
) workspaceMetadata {
	metadata := workspaceMetadata{DocSummary: docSummary}
	if len(attributes) == 0 && len(constantArray) == 0 {
		return metadata
	}
	if uint64(len(attributes)) > math.MaxUint32 ||
		uint64(len(constantArray)) > math.MaxUint32 {
		panic("semantic: workspace metadata collection exceeds packed range")
	}
	metadata.Extras = &workspaceMetadataExtras{
		attributesData:    workspaceSliceData(attributes),
		constantArrayData: workspaceSliceData(constantArray),
		lengths: uint64(len(attributes)) |
			uint64(len(constantArray))<<workspaceMetadataLengthBits,
	}
	return metadata
}

func (metadata *workspaceMetadata) attributes() []Attribute {
	if metadata == nil || metadata.Extras == nil {
		return nil
	}
	return workspaceSlice(
		metadata.Extras.attributesData,
		uint32(metadata.Extras.lengths),
	)
}

func (metadata *workspaceMetadata) constantArray() []ConstantArrayItem {
	if metadata == nil || metadata.Extras == nil {
		return nil
	}
	return workspaceSlice(
		metadata.Extras.constantArrayData,
		uint32(metadata.Extras.lengths>>workspaceMetadataLengthBits),
	)
}

func workspaceSliceData[T any](values []T) *T {
	if len(values) == 0 {
		return nil
	}
	return &values[0]
}

func workspaceSlice[T any](data *T, length uint32) []T {
	if data == nil || length == 0 {
		return nil
	}
	return unsafe.Slice(data, length)
}

func decodeOptionalWorkspaceHierarchy(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) (*workspaceHierarchy, error) {
	code, err := decoder.PeekCode()
	if err != nil {
		return nil, err
	}
	if code == msgpcode.Nil {
		return nil, decoder.DecodeNil()
	}
	length, err := decoder.DecodeArrayLen()
	if err != nil {
		return nil, err
	}
	if length != 6 && length != 7 {
		return nil, fmt.Errorf(
			"decode workspace hierarchy: expected 6 or 7 fields, got %d",
			length,
		)
	}
	extends, err := decodeWorkspaceStrings(decoder, context)
	if err != nil {
		return nil, err
	}
	implements, err := decodeWorkspaceStrings(decoder, context)
	if err != nil {
		return nil, err
	}
	traits, err := decodeWorkspaceStrings(decoder, context)
	if err != nil {
		return nil, err
	}
	extendsTypes, err := decodeWorkspaceTypes(decoder, context)
	if err != nil {
		return nil, err
	}
	implementsTypes, err := decodeWorkspaceTypes(decoder, context)
	if err != nil {
		return nil, err
	}
	traitTypes, err := decodeWorkspaceTypes(decoder, context)
	if err != nil {
		return nil, err
	}
	var aliases []TraitAlias
	if length == 7 {
		err = decoder.Decode(&aliases)
		if err != nil {
			return nil, err
		}
	}
	hierarchy := newWorkspaceHierarchy(
		extends,
		implements,
		traits,
		extendsTypes,
		implementsTypes,
		traitTypes,
		aliases,
	)
	return &hierarchy, nil
}

func decodeOptionalWorkspaceMetadata(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) (*workspaceMetadata, error) {
	code, err := decoder.PeekCode()
	if err != nil {
		return nil, err
	}
	if code == msgpcode.Nil {
		return nil, decoder.DecodeNil()
	}
	length, err := decoder.DecodeArrayLen()
	if err != nil {
		return nil, err
	}
	if length != 3 {
		return nil, fmt.Errorf(
			"decode workspace metadata: expected 3 fields, got %d",
			length,
		)
	}
	attributes, err := decodeWorkspaceAttributes(decoder, context)
	if err != nil {
		return nil, err
	}
	constantArray, err := decodeWorkspaceConstantArray(decoder, context)
	if err != nil {
		return nil, err
	}
	docSummary, err := context.decodeString(decoder)
	if err != nil {
		return nil, err
	}
	metadata := newWorkspaceMetadata(attributes, constantArray, docSummary)
	return &metadata, nil
}

func decodeWorkspaceAttributes(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) ([]Attribute, error) {
	length, err := decodeWorkspaceCollectionLen(decoder, "attributes")
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, nil
	}
	result := make([]Attribute, length)
	for index := range result {
		fieldCount, err := decoder.DecodeMapLen()
		if err != nil {
			return nil, err
		}
		item := &result[index]
		for range max(0, fieldCount) {
			field, err := context.decodeString(decoder)
			if err != nil {
				return nil, err
			}
			switch field {
			case "Name":
				item.Name, err = context.decodeString(decoder)
			case "Arguments":
				item.Arguments, err = decodeWorkspaceAttributeArguments(
					decoder,
					context,
				)
			case "Range":
				item.Range, err =
					decodeWorkspaceTextRange(decoder, context)
			default:
				err = decoder.Skip()
			}
			if err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func decodeWorkspaceAttributeArguments(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) ([]AttributeArgument, error) {
	length, err := decodeWorkspaceCollectionLen(
		decoder,
		"attribute arguments",
	)
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, nil
	}
	result := make([]AttributeArgument, length)
	for index := range result {
		fieldCount, decodeErr := decoder.DecodeMapLen()
		if decodeErr != nil {
			return nil, decodeErr
		}
		item := &result[index]
		for range max(0, fieldCount) {
			field, fieldErr := context.decodeString(decoder)
			if fieldErr != nil {
				return nil, fieldErr
			}
			switch field {
			case "Name":
				item.Name, fieldErr = context.decodeString(decoder)
			case "Value":
				item.Value, fieldErr = decodeWorkspaceAttributeValue(
					decoder,
					context,
					0,
				)
			case "Range":
				item.Range, fieldErr = decodeWorkspaceTextRange(
					decoder,
					context,
				)
			default:
				fieldErr = decoder.Skip()
			}
			if fieldErr != nil {
				return nil, fieldErr
			}
		}
	}
	return result, nil
}

const maxWorkspaceAttributeValueDepth = 32

func decodeWorkspaceAttributeValue(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
	depth int,
) (AttributeValue, error) {
	if depth >= maxWorkspaceAttributeValueDepth {
		return AttributeValue{}, fmt.Errorf(
			"decode workspace attribute value: nesting exceeds %d",
			maxWorkspaceAttributeValueDepth,
		)
	}
	fieldCount, err := decoder.DecodeMapLen()
	if err != nil {
		return AttributeValue{}, err
	}
	var result AttributeValue
	for range max(0, fieldCount) {
		field, fieldErr := context.decodeString(decoder)
		if fieldErr != nil {
			return AttributeValue{}, fieldErr
		}
		switch field {
		case "Kind":
			var kind uint8
			kind, fieldErr = decoder.DecodeUint8()
			result.Kind = AttributeValueKind(kind)
		case "Value":
			result.Value, fieldErr = context.decodeString(decoder)
		case "Expression":
			result.Expression, fieldErr = context.decodeString(decoder)
		case "Items":
			result.Items, fieldErr = decodeWorkspaceAttributeArrayItems(
				decoder,
				context,
				depth+1,
			)
		default:
			fieldErr = decoder.Skip()
		}
		if fieldErr != nil {
			return AttributeValue{}, fieldErr
		}
	}
	return result, nil
}

func decodeWorkspaceAttributeArrayItems(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
	depth int,
) ([]AttributeArrayItem, error) {
	length, err := decodeWorkspaceCollectionLen(
		decoder,
		"attribute array items",
	)
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, nil
	}
	result := make([]AttributeArrayItem, length)
	for index := range result {
		fieldCount, decodeErr := decoder.DecodeMapLen()
		if decodeErr != nil {
			return nil, decodeErr
		}
		item := &result[index]
		for range max(0, fieldCount) {
			field, fieldErr := context.decodeString(decoder)
			if fieldErr != nil {
				return nil, fieldErr
			}
			switch field {
			case "Key":
				item.Key, fieldErr = decodeWorkspaceAttributeValue(
					decoder,
					context,
					depth,
				)
			case "HasKey":
				item.HasKey, fieldErr = decoder.DecodeBool()
			case "Value":
				item.Value, fieldErr = decodeWorkspaceAttributeValue(
					decoder,
					context,
					depth,
				)
			default:
				fieldErr = decoder.Skip()
			}
			if fieldErr != nil {
				return nil, fieldErr
			}
		}
	}
	return result, nil
}

func decodeWorkspaceConstantArray(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) ([]ConstantArrayItem, error) {
	length, err := decodeWorkspaceCollectionLen(
		decoder,
		"constant array items",
	)
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, nil
	}
	result := make([]ConstantArrayItem, length)
	for index := range result {
		fieldCount, err := decoder.DecodeMapLen()
		if err != nil {
			return nil, err
		}
		item := &result[index]
		for range max(0, fieldCount) {
			field, err := context.decodeString(decoder)
			if err != nil {
				return nil, err
			}
			switch field {
			case "Key":
				item.Key, err = context.decodeString(decoder)
			case "KeyRange":
				item.KeyRange, err =
					decodeWorkspaceTextRange(decoder, context)
			case "Value":
				item.Value, err = context.decodeString(decoder)
			case "ValueRange":
				item.ValueRange, err =
					decodeWorkspaceTextRange(decoder, context)
			case "Type":
				item.Type, err = context.decodeType(decoder)
			default:
				err = decoder.Skip()
			}
			if err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

// workspaceReference retains the fields needed for workspace-wide resolution.
// Strings, receiver types, qualified names, and fallback candidates are stored
// once in per-document tables; the hot record is only indexes and scalar data.
