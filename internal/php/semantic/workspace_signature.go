package semantic

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"
)

type workspaceSignature struct {
	Parameters []workspaceParameter
	Extras     *workspaceSignatureExtras
}

type workspaceParameter struct {
	Name       string
	NativeType types.Type
	DocType    types.Type
	Extras     *workspaceParameterExtras
	Ranges     workspaceParameterRanges
	properties uint16
}

const (
	workspaceParameterNativeType uint8 = iota
	workspaceParameterDocType
	workspaceParameterExplicitType
)

const (
	workspaceParameterFlagsMask   = uint16(1<<13 - 1)
	workspaceParameterSourceShift = 13
	workspaceParameterSourceMask  = 0x03
	workspaceParameterOptional    = uint16(1 << 15)
)

type workspaceParameterExtras struct {
	Metadata *workspaceParameterMetadata
	ID       SymbolID
	Type     types.Type
	Ranges   *workspaceParameterFullRanges
}

type workspaceParameterMetadata struct {
	AssistantTags []string
	Attributes    []Attribute
	DefaultValue  *AttributeValue
}

type workspaceParameterRanges struct {
	StartLow  uint16
	StartHigh uint16
	Deltas    [5]uint16
}

func (ranges workspaceParameterRanges) start() uint32 {
	return uint32(ranges.StartLow) | uint32(ranges.StartHigh)<<16
}

type workspaceParameterFullRanges struct {
	Range          cst.TextRange
	SelectionRange cst.TextRange
	DefaultRange   cst.TextRange
}

const workspaceParameterMissingDefaultRange = workspaceCompactRangeMissing

func compactWorkspaceParameterRanges(
	rng,
	selectionRange,
	defaultRange cst.TextRange,
) (workspaceParameterRanges, bool) {
	rangeLength, ok := compactWorkspaceParameterDelta(rng.Start, rng.End)
	if !ok {
		return workspaceParameterRanges{}, false
	}
	selectionStart, ok := compactWorkspaceParameterDelta(
		rng.Start,
		selectionRange.Start,
	)
	if !ok {
		return workspaceParameterRanges{}, false
	}
	selectionLength, ok := compactWorkspaceParameterDelta(
		selectionRange.Start,
		selectionRange.End,
	)
	if !ok {
		return workspaceParameterRanges{}, false
	}
	defaultStart := uint16(workspaceParameterMissingDefaultRange)
	defaultLength := uint16(0)
	if defaultRange != (cst.TextRange{}) {
		defaultStart, ok = compactWorkspaceParameterDelta(
			rng.Start,
			defaultRange.Start,
		)
		if !ok {
			return workspaceParameterRanges{}, false
		}
		defaultLength, ok = compactWorkspaceParameterDelta(
			defaultRange.Start,
			defaultRange.End,
		)
		if !ok {
			return workspaceParameterRanges{}, false
		}
	}
	return workspaceParameterRanges{
		StartLow:  uint16(rng.Start),
		StartHigh: uint16(rng.Start >> 16),
		Deltas: [5]uint16{
			rangeLength,
			selectionStart,
			selectionLength,
			defaultStart,
			defaultLength,
		},
	}, true
}

func compactWorkspaceParameterDelta(start, end uint32) (uint16, bool) {
	if end < start || end-start >= workspaceParameterMissingDefaultRange {
		return 0, false
	}
	return uint16(end - start), true
}

func (parameter *workspaceParameter) setRanges(
	rng,
	selectionRange,
	defaultRange cst.TextRange,
) {
	if parameter == nil {
		return
	}
	ranges, compact := compactWorkspaceParameterRanges(
		rng,
		selectionRange,
		defaultRange,
	)
	parameter.Ranges = ranges
	if compact {
		if parameter.Extras != nil {
			parameter.Extras.Ranges = nil
		}
		return
	}
	if parameter.Extras == nil {
		parameter.Extras = &workspaceParameterExtras{}
	}
	parameter.Extras.Ranges = &workspaceParameterFullRanges{
		Range:          rng,
		SelectionRange: selectionRange,
		DefaultRange:   defaultRange,
	}
}

func (parameter *workspaceParameter) ranges() (
	rng,
	selectionRange,
	defaultRange cst.TextRange,
) {
	if parameter == nil {
		return
	}
	if parameter.Extras != nil && parameter.Extras.Ranges != nil {
		full := parameter.Extras.Ranges
		return full.Range, full.SelectionRange, full.DefaultRange
	}
	start := parameter.Ranges.start()
	rng = cst.TextRange{
		Start: start,
		End:   start + uint32(parameter.Ranges.Deltas[0]),
	}
	selectionRange = cst.TextRange{
		Start: start + uint32(parameter.Ranges.Deltas[1]),
	}
	selectionRange.End =
		selectionRange.Start + uint32(parameter.Ranges.Deltas[2])
	if parameter.Ranges.Deltas[3] !=
		workspaceParameterMissingDefaultRange {
		defaultRange.Start =
			start + uint32(parameter.Ranges.Deltas[3])
		defaultRange.End =
			defaultRange.Start + uint32(parameter.Ranges.Deltas[4])
	}
	return
}

func (parameter *workspaceParameter) setFlags(value Flags) {
	if parameter == nil {
		return
	}
	if uint32(value)&^uint32(workspaceParameterFlagsMask) != 0 {
		panic("semantic: workspace parameter flags exceed packed range")
	}
	parameter.properties = parameter.properties&^workspaceParameterFlagsMask |
		uint16(value)
}

func (parameter *workspaceParameter) flags() Flags {
	if parameter == nil {
		return 0
	}
	return Flags(parameter.properties & workspaceParameterFlagsMask)
}

func (parameter *workspaceParameter) setEffectiveSource(value uint8) {
	if parameter == nil {
		return
	}
	if value > workspaceParameterExplicitType {
		panic("semantic: workspace parameter type source exceeds packed range")
	}
	mask := uint16(workspaceParameterSourceMask << workspaceParameterSourceShift)
	parameter.properties = parameter.properties&^mask |
		uint16(value)<<workspaceParameterSourceShift
}

func (parameter *workspaceParameter) effectiveSource() uint8 {
	if parameter == nil {
		return workspaceParameterNativeType
	}
	return uint8(
		parameter.properties >> workspaceParameterSourceShift &
			workspaceParameterSourceMask,
	)
}

func (parameter *workspaceParameter) setOptional(value bool) {
	if parameter == nil {
		return
	}
	if value {
		parameter.properties |= workspaceParameterOptional
	} else {
		parameter.properties &^= workspaceParameterOptional
	}
}

func (parameter *workspaceParameter) optional() bool {
	return parameter != nil &&
		parameter.properties&workspaceParameterOptional != 0
}

func packWorkspaceParameters(parameters []Parameter) []workspaceParameter {
	return packWorkspaceParametersForSymbol(parameters, nil)
}

func packWorkspaceParametersForSymbol(
	parameters []Parameter,
	owner *workspaceSymbol,
) []workspaceParameter {
	if parameters == nil {
		return nil
	}
	result := make([]workspaceParameter, len(parameters))
	for index := range parameters {
		result[index] = packWorkspaceParameter(
			&parameters[index],
			owner,
		)
	}
	return result
}

func packWorkspaceParameter(
	source *Parameter,
	owner *workspaceSymbol,
) workspaceParameter {
	if source == nil {
		return workspaceParameter{}
	}
	result := workspaceParameter{
		Name:       source.Name,
		NativeType: source.NativeType,
		DocType:    source.DocType,
	}
	result.setFlags(source.Flags)
	result.setOptional(source.Optional)
	switch {
	case source.Type.Equal(source.NativeType):
	case source.Type.Equal(source.DocType):
		result.setEffectiveSource(workspaceParameterDocType)
	default:
		result.setEffectiveSource(workspaceParameterExplicitType)
		result.Extras = &workspaceParameterExtras{
			Type: source.Type,
		}
	}
	if source.AssistantTags != nil {
		if result.Extras == nil {
			result.Extras = &workspaceParameterExtras{}
		}
		result.Extras.Metadata = &workspaceParameterMetadata{
			AssistantTags: slices.Clone(source.AssistantTags),
		}
	}
	if source.Attributes != nil {
		if result.Extras == nil {
			result.Extras = &workspaceParameterExtras{}
		}
		if result.Extras.Metadata == nil {
			result.Extras.Metadata = &workspaceParameterMetadata{}
		}
		result.Extras.Metadata.Attributes = cloneAttributes(source.Attributes)
	}
	if source.DefaultValue != nil {
		if result.Extras == nil {
			result.Extras = &workspaceParameterExtras{}
		}
		if result.Extras.Metadata == nil {
			result.Extras.Metadata = &workspaceParameterMetadata{}
		}
		value := cloneAttributeValue(*source.DefaultValue)
		result.Extras.Metadata.DefaultValue = &value
	}
	result.setID(owner, source.ID)
	result.setRanges(
		source.Range,
		source.SelectionRange,
		source.DefaultRange,
	)
	return result
}

func workspaceParameterIDForSymbol(
	symbol *workspaceSymbol,
	name string,
) SymbolID {
	if symbol == nil || symbol.ID == "" || name == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(
		len(workspaceParameterIDPrefix) +
			len(symbol.ID) +
			1 +
			len(name),
	)
	builder.WriteString(workspaceParameterIDPrefix)
	writeLowerIdentifier(&builder, string(symbol.ID))
	builder.WriteByte(':')
	writeLowerIdentifier(&builder, name)
	return SymbolID(builder.String())
}

var workspaceParameterIDPrefix = strconv.Itoa(int(ParameterSymbol)) + ":"

func workspaceParameterIDMatchesSymbol(
	symbol *workspaceSymbol,
	name string,
	id SymbolID,
) bool {
	if symbol == nil || symbol.ID == "" || name == "" || id == "" {
		return false
	}
	value := string(id)
	if !strings.HasPrefix(value, workspaceParameterIDPrefix) {
		return false
	}
	value = value[len(workspaceParameterIDPrefix):]
	ownerID := string(symbol.ID)
	if len(value) != len(ownerID)+1+len(name) ||
		value[len(ownerID)] != ':' {
		return false
	}
	return strings.EqualFold(value[:len(ownerID)], ownerID) &&
		strings.EqualFold(value[len(ownerID)+1:], name)
}

func (parameter *workspaceParameter) id(owner *workspaceSymbol) SymbolID {
	if parameter == nil {
		return ""
	}
	if parameter.Extras != nil && parameter.Extras.ID != "" {
		return parameter.Extras.ID
	}
	return workspaceParameterIDForSymbol(owner, parameter.Name)
}

func encodeWorkspaceParameterID(
	encoder *msgpack.Encoder,
	parameter *workspaceParameter,
) error {
	if parameter == nil {
		return encoder.EncodeString("")
	}
	if parameter.Extras != nil && parameter.Extras.ID != "" {
		return encoder.EncodeString(string(parameter.Extras.ID))
	}
	return encoder.EncodeString("")
}

func (parameter *workspaceParameter) setID(
	owner *workspaceSymbol,
	id SymbolID,
) {
	if parameter == nil {
		return
	}
	if id == "" {
		parameter.clearFallbackID()
		return
	}
	if workspaceParameterIDMatchesSymbol(owner, parameter.Name, id) {
		parameter.clearFallbackID()
		return
	}
	if parameter.Extras == nil {
		parameter.Extras = &workspaceParameterExtras{}
	}
	parameter.Extras.ID = id
}

func (parameter *workspaceParameter) clearFallbackID() {
	if parameter == nil || parameter.Extras == nil {
		return
	}
	parameter.Extras.ID = ""
	if parameter.Extras.Metadata == nil &&
		parameter.Extras.Type == (types.Type{}) &&
		parameter.Extras.Ranges == nil {
		parameter.Extras = nil
	}
}

func (parameter *workspaceParameter) assistantTags() []string {
	if parameter == nil || parameter.Extras == nil ||
		parameter.Extras.Metadata == nil {
		return nil
	}
	return parameter.Extras.Metadata.AssistantTags
}

func (parameter *workspaceParameter) attributes() []Attribute {
	if parameter == nil || parameter.Extras == nil ||
		parameter.Extras.Metadata == nil {
		return nil
	}
	return parameter.Extras.Metadata.Attributes
}

func (parameter *workspaceParameter) defaultValue() *AttributeValue {
	if parameter == nil || parameter.Extras == nil ||
		parameter.Extras.Metadata == nil {
		return nil
	}
	return parameter.Extras.Metadata.DefaultValue
}

func (parameter *workspaceParameter) effectiveType() types.Type {
	if parameter == nil {
		return types.Type{}
	}
	switch parameter.effectiveSource() {
	case workspaceParameterDocType:
		return parameter.DocType
	case workspaceParameterExplicitType:
		if parameter.Extras != nil {
			return parameter.Extras.Type
		}
	}
	return parameter.NativeType
}

// EncodeMsgpack writes the compact versioned parameter layout. DecodeMsgpack
// also accepts the public Parameter map emitted by older workspace caches.
func (parameter workspaceParameter) EncodeMsgpack(
	encoder *msgpack.Encoder,
) error {
	return encodeWorkspaceParameter(encoder, &parameter)
}

func encodeWorkspaceParameter(
	encoder *msgpack.Encoder,
	parameter *workspaceParameter,
) error {
	if parameter == nil {
		return encoder.EncodeNil()
	}
	if err := encoder.EncodeArrayLen(17); err != nil {
		return err
	}
	if err := encoder.EncodeUint8(4); err != nil {
		return err
	}
	if err := encodeWorkspaceParameterID(
		encoder,
		parameter,
	); err != nil {
		return err
	}
	if err := encoder.EncodeString(parameter.Name); err != nil {
		return err
	}
	effectiveType := parameter.effectiveType()
	if err := effectiveType.EncodeMsgpack(encoder); err != nil {
		return err
	}
	if err := parameter.NativeType.EncodeMsgpack(encoder); err != nil {
		return err
	}
	if err := parameter.DocType.EncodeMsgpack(encoder); err != nil {
		return err
	}
	if err := encodeWorkspaceStrings(
		encoder,
		parameter.assistantTags(),
	); err != nil {
		return err
	}
	if err := encoder.Encode(parameter.attributes()); err != nil {
		return err
	}
	if err := encoder.Encode(parameter.defaultValue()); err != nil {
		return err
	}
	rng, selectionRange, defaultRange := parameter.ranges()
	if err := encoder.EncodeUint32(rng.Start); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(rng.End); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(selectionRange.Start); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(selectionRange.End); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(defaultRange.Start); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(defaultRange.End); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(uint32(parameter.flags())); err != nil {
		return err
	}
	return encoder.EncodeBool(parameter.optional())
}

// DecodeMsgpack accepts both the compact array and the public Parameter map
// emitted by older workspace caches.
func (parameter *workspaceParameter) DecodeMsgpack(
	decoder *msgpack.Decoder,
) error {
	return parameter.decodeMsgpack(decoder, NewWorkspaceGraphDecoder())
}

func (parameter *workspaceParameter) decodeMsgpack(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) error {
	return parameter.decodeMsgpackID(
		decoder,
		context,
		nil,
		nil,
	)
}

func (parameter *workspaceParameter) decodeMsgpackID(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
	id *SymbolID,
	deferred *bool,
) error {
	if deferred != nil {
		*deferred = false
	}
	code, err := decoder.PeekCode()
	if err != nil {
		return err
	}
	if workspaceParameterLegacyMap(code) {
		return parameter.decodeLegacyMsgpack(decoder, id, deferred)
	}
	length, err := decoder.DecodeArrayLen()
	if err != nil {
		return err
	}
	if err := validateWorkspaceParameterLength(length); err != nil {
		return err
	}
	version, err := decoder.DecodeUint8()
	if err != nil {
		return err
	}
	if err := validateWorkspaceParameterLayout(length, version); err != nil {
		return err
	}
	*parameter = workspaceParameter{}
	decodedID, err := context.decodeParameterID(decoder)
	if err != nil {
		return err
	}
	switch {
	case id == nil:
		parameter.setID(nil, SymbolID(decodedID))
	case version == 1:
		*id = SymbolID(decodedID)
		if deferred != nil {
			*deferred = true
		}
	default:
		parameter.setID(nil, SymbolID(decodedID))
	}
	if parameter.Name, err = context.decodeString(decoder); err != nil {
		return err
	}
	effectiveType, err := context.decodeType(decoder)
	if err != nil {
		return err
	}
	if parameter.NativeType, err = context.decodeType(decoder); err != nil {
		return err
	}
	if parameter.DocType, err = context.decodeType(decoder); err != nil {
		return err
	}
	assistantTags, err := decodeWorkspaceStrings(decoder, context)
	if err != nil {
		return err
	}
	attributes, defaultValue, err := decodeWorkspaceParameterMetadata(
		decoder,
		context,
		version,
	)
	if err != nil {
		return err
	}
	rng, selectionRange, defaultRange, err := decodeWorkspaceParameterRanges(decoder)
	if err != nil {
		return err
	}
	flags, err := decoder.DecodeUint32()
	if err != nil {
		return err
	}
	if flags&^uint32(workspaceParameterFlagsMask) != 0 {
		return fmt.Errorf(
			"decode workspace parameter: flags %d exceed packed range",
			flags,
		)
	}
	parameter.setFlags(Flags(flags))
	optional, err := decoder.DecodeBool()
	if err != nil {
		return err
	}
	parameter.setOptional(optional)
	parameter.setEffectiveType(effectiveType)
	parameter.setMetadata(assistantTags, attributes, defaultValue)
	parameter.setRanges(rng, selectionRange, defaultRange)
	return nil
}

func workspaceParameterLegacyMap(code byte) bool {
	return msgpcode.IsFixedMap(code) || code == msgpcode.Map16 || code == msgpcode.Map32
}

func (parameter *workspaceParameter) decodeLegacyMsgpack(
	decoder *msgpack.Decoder,
	id *SymbolID,
	deferred *bool,
) error {
	var legacy Parameter
	if err := decoder.Decode(&legacy); err != nil {
		return err
	}
	*parameter = packWorkspaceParameter(&legacy, nil)
	if id != nil {
		*id = legacy.ID
		if deferred != nil {
			*deferred = true
		}
		parameter.clearFallbackID()
	}
	return nil
}

func validateWorkspaceParameterLayout(length int, version uint8) error {
	if version < 1 || version > 4 {
		return fmt.Errorf(
			"decode workspace parameter: unsupported layout %d",
			version,
		)
	}
	expectedLength := 15
	if version >= 3 {
		expectedLength = 16
	}
	if version >= 4 {
		expectedLength = 17
	}
	if length != expectedLength {
		return fmt.Errorf(
			"decode workspace parameter: layout %d requires %d fields",
			version,
			expectedLength,
		)
	}
	return nil
}

func validateWorkspaceParameterLength(length int) error {
	if length == 15 || length == 16 || length == 17 {
		return nil
	}
	return fmt.Errorf(
		"decode workspace parameter: expected 15, 16, or 17 fields, got %d",
		length,
	)
}

func decodeWorkspaceParameterMetadata(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
	version uint8,
) ([]Attribute, *AttributeValue, error) {
	var attributes []Attribute
	var err error
	if version >= 3 {
		attributes, err = decodeWorkspaceAttributes(decoder, context)
		if err != nil {
			return nil, nil, err
		}
	}
	if version < 4 {
		return attributes, nil, nil
	}
	defaultValue, err := decodeOptionalWorkspaceAttributeValue(decoder, context)
	return attributes, defaultValue, err
}

func decodeOptionalWorkspaceAttributeValue(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) (*AttributeValue, error) {
	code, err := decoder.PeekCode()
	if err != nil {
		return nil, err
	}
	if code == msgpcode.Nil {
		return nil, decoder.DecodeNil()
	}
	value, err := decodeWorkspaceAttributeValue(decoder, context, 0)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func decodeWorkspaceParameterRanges(
	decoder *msgpack.Decoder,
) (cst.TextRange, cst.TextRange, cst.TextRange, error) {
	var values [6]uint32
	for index := range values {
		value, err := decoder.DecodeUint32()
		if err != nil {
			return cst.TextRange{}, cst.TextRange{}, cst.TextRange{}, err
		}
		values[index] = value
	}
	return cst.TextRange{Start: values[0], End: values[1]},
		cst.TextRange{Start: values[2], End: values[3]},
		cst.TextRange{Start: values[4], End: values[5]}, nil
}

func (parameter *workspaceParameter) setMetadata(
	assistantTags []string,
	attributes []Attribute,
	defaultValue *AttributeValue,
) {
	if assistantTags == nil && attributes == nil && defaultValue == nil {
		return
	}
	if parameter.Extras == nil {
		parameter.Extras = &workspaceParameterExtras{}
	}
	if parameter.Extras.Metadata == nil {
		parameter.Extras.Metadata = &workspaceParameterMetadata{}
	}
	parameter.Extras.Metadata.AssistantTags = assistantTags
	parameter.Extras.Metadata.Attributes = attributes
	parameter.Extras.Metadata.DefaultValue = defaultValue
}

func (parameter *workspaceParameter) setEffectiveType(value types.Type) {
	parameter.setEffectiveSource(workspaceParameterNativeType)
	switch {
	case value.Equal(parameter.NativeType):
	case value.Equal(parameter.DocType):
		parameter.setEffectiveSource(workspaceParameterDocType)
	default:
		parameter.setEffectiveSource(workspaceParameterExplicitType)
		if parameter.Extras == nil {
			parameter.Extras = &workspaceParameterExtras{}
		}
		parameter.Extras.Type = value
	}
}

func encodeWorkspaceParameters(
	encoder *msgpack.Encoder,
	parameters []workspaceParameter,
) error {
	if parameters == nil {
		return encoder.EncodeNil()
	}
	if err := encoder.EncodeArrayLen(len(parameters)); err != nil {
		return err
	}
	for index := range parameters {
		if err := encodeWorkspaceParameter(
			encoder,
			&parameters[index],
		); err != nil {
			return err
		}
	}
	return nil
}

func decodeWorkspaceParameters(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
	ids *[]decodedWorkspaceParameterID,
) ([]workspaceParameter, error) {
	length, err := decodeWorkspaceCollectionLen(decoder, "parameters")
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, nil
	}
	parameters := make([]workspaceParameter, length)
	for index := range parameters {
		var id SymbolID
		var deferred bool
		if err := parameters[index].decodeMsgpackID(
			decoder,
			context,
			&id,
			&deferred,
		); err != nil {
			return nil, err
		}
		if deferred {
			if len(*ids) == 0 && cap(*ids) == 0 {
				*ids = make(
					[]decodedWorkspaceParameterID,
					0,
					max(length, 8),
				)
			}
			*ids = append(*ids, decodedWorkspaceParameterID{
				ID:    id,
				Index: uint32(index),
			})
		}
	}
	return parameters, nil
}

func encodeWorkspaceStrings(
	encoder *msgpack.Encoder,
	values []string,
) error {
	if values == nil {
		return encoder.EncodeNil()
	}
	if err := encoder.EncodeArrayLen(len(values)); err != nil {
		return err
	}
	for _, value := range values {
		if err := encoder.EncodeString(value); err != nil {
			return err
		}
	}
	return nil
}

func decodeWorkspaceStrings(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) ([]string, error) {
	length, err := decodeWorkspaceCollectionLen(decoder, "strings")
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, nil
	}
	values := make([]string, length)
	for index := range values {
		if values[index], err = context.decodeString(decoder); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func decodeWorkspaceCollectionLen(
	decoder *msgpack.Decoder,
	label string,
) (int, error) {
	length, err := decoder.DecodeArrayLen()
	if err != nil {
		return 0, err
	}
	if length > maxWorkspaceCollectionItems {
		return 0, fmt.Errorf(
			"decode workspace %s: length %d exceeds %d",
			label,
			length,
			maxWorkspaceCollectionItems,
		)
	}
	return length, nil
}

func decodeWorkspaceTextRange(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) (cst.TextRange, error) {
	length, err := decoder.DecodeMapLen()
	if err != nil {
		return cst.TextRange{}, err
	}
	var result cst.TextRange
	for range max(0, length) {
		field, err := context.decodeString(decoder)
		if err != nil {
			return cst.TextRange{}, err
		}
		switch field {
		case "Start":
			result.Start, err = decoder.DecodeUint32()
		case "End":
			result.End, err = decoder.DecodeUint32()
		default:
			err = decoder.Skip()
		}
		if err != nil {
			return cst.TextRange{}, err
		}
	}
	return result, nil
}

func (parameter *workspaceParameter) materialize(
	owner ...*workspaceSymbol,
) Parameter {
	if parameter == nil {
		return Parameter{}
	}
	var symbol *workspaceSymbol
	if len(owner) != 0 {
		symbol = owner[0]
	}
	rng, selectionRange, defaultRange := parameter.ranges()
	return Parameter{
		ID:             parameter.id(symbol),
		Name:           parameter.Name,
		Type:           parameter.effectiveType(),
		NativeType:     parameter.NativeType,
		DocType:        parameter.DocType,
		AssistantTags:  parameter.assistantTags(),
		Attributes:     parameter.attributes(),
		DefaultValue:   parameter.defaultValue(),
		Range:          rng,
		SelectionRange: selectionRange,
		DefaultRange:   defaultRange,
		Flags:          parameter.flags(),
		Optional:       parameter.optional(),
	}
}

func materializeWorkspaceParameters(
	parameters []workspaceParameter,
	owner ...*workspaceSymbol,
) []Parameter {
	if parameters == nil {
		return nil
	}
	var symbol *workspaceSymbol
	if len(owner) != 0 {
		symbol = owner[0]
	}
	result := make([]Parameter, len(parameters))
	for index := range parameters {
		result[index] = parameters[index].materialize(symbol)
	}
	return result
}

type workspaceSignatureExtras struct {
	templatesData       *TemplateParameter
	throwsData          *types.Type
	literalReturnsData  *LiteralReturn
	constantReturnsData *ConstantReturn
	assertionsData      *TypeAssertion
	lengths             uint64
	constantReturnCount uint32
	assertionCount      uint32
}

const (
	workspaceSignatureExtraLengthBits = 21
	workspaceSignatureExtraLengthMask = 1<<workspaceSignatureExtraLengthBits - 1
)

func newWorkspaceSignatureExtras(
	templates []TemplateParameter,
	throws []types.Type,
	literalReturns []LiteralReturn,
	constantReturns []ConstantReturn,
	assertions []TypeAssertion,
) workspaceSignatureExtras {
	for _, length := range [...]int{
		len(templates),
		len(throws),
		len(literalReturns),
		len(constantReturns),
		len(assertions),
	} {
		if length > workspaceSignatureExtraLengthMask {
			panic("semantic: workspace signature collection exceeds packed range")
		}
	}
	return workspaceSignatureExtras{
		templatesData:       workspaceSliceData(templates),
		throwsData:          workspaceSliceData(throws),
		literalReturnsData:  workspaceSliceData(literalReturns),
		constantReturnsData: workspaceSliceData(constantReturns),
		assertionsData:      workspaceSliceData(assertions),
		lengths: uint64(len(templates)) |
			uint64(len(throws))<<workspaceSignatureExtraLengthBits |
			uint64(len(literalReturns))<<(workspaceSignatureExtraLengthBits*2),
		constantReturnCount: uint32(len(constantReturns)),
		assertionCount:      uint32(len(assertions)),
	}
}

func (extras *workspaceSignatureExtras) templates() []TemplateParameter {
	if extras == nil {
		return nil
	}
	return workspaceSlice(
		extras.templatesData,
		uint32(extras.lengths&workspaceSignatureExtraLengthMask),
	)
}

func (extras *workspaceSignatureExtras) throws() []types.Type {
	if extras == nil {
		return nil
	}
	return workspaceSlice(
		extras.throwsData,
		uint32(
			extras.lengths>>workspaceSignatureExtraLengthBits&
				workspaceSignatureExtraLengthMask,
		),
	)
}

func (extras *workspaceSignatureExtras) literalReturns() []LiteralReturn {
	if extras == nil {
		return nil
	}
	return workspaceSlice(
		extras.literalReturnsData,
		uint32(
			extras.lengths>>(workspaceSignatureExtraLengthBits*2)&
				workspaceSignatureExtraLengthMask,
		),
	)
}

func (extras *workspaceSignatureExtras) constantReturns() []ConstantReturn {
	if extras == nil {
		return nil
	}
	return workspaceSlice(
		extras.constantReturnsData,
		extras.constantReturnCount,
	)
}

func (extras *workspaceSignatureExtras) assertions() []TypeAssertion {
	if extras == nil {
		return nil
	}
	return workspaceSlice(extras.assertionsData, extras.assertionCount)
}

func (signature *workspaceSignature) templates() []TemplateParameter {
	if signature == nil || signature.Extras == nil {
		return nil
	}
	return signature.Extras.templates()
}

func (signature *workspaceSignature) throws() []types.Type {
	if signature == nil || signature.Extras == nil {
		return nil
	}
	return signature.Extras.throws()
}

func (signature *workspaceSignature) literalReturns() []LiteralReturn {
	if signature == nil || signature.Extras == nil {
		return nil
	}
	return signature.Extras.literalReturns()
}

func (signature *workspaceSignature) constantReturns() []ConstantReturn {
	if signature == nil || signature.Extras == nil {
		return nil
	}
	return signature.Extras.constantReturns()
}

func (signature *workspaceSignature) assertions() []TypeAssertion {
	if signature == nil || signature.Extras == nil {
		return nil
	}
	return signature.Extras.assertions()
}

// DecodeMsgpack accepts the five-field signature wire layout while keeping
// its four uncommon collections behind one optional retained side record.
func (signature *workspaceSignature) DecodeMsgpack(
	decoder *msgpack.Decoder,
) error {
	return signature.decodeMsgpack(decoder, NewWorkspaceGraphDecoder())
}

func (signature *workspaceSignature) decodeMsgpack(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) error {
	var ids []decodedWorkspaceParameterID
	if err := signature.decodeMsgpackIDs(
		decoder,
		context,
		&ids,
	); err != nil {
		return err
	}
	for index := range ids {
		parameterIndex := int(ids[index].Index)
		if parameterIndex >= len(signature.Parameters) {
			continue
		}
		signature.Parameters[parameterIndex].setID(nil, ids[index].ID)
	}
	return nil
}

func (signature *workspaceSignature) decodeMsgpackIDs(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
	parameterIDs *[]decodedWorkspaceParameterID,
) error {
	signature.Parameters = nil
	signature.Extras = nil
	length, err := decoder.DecodeArrayLen()
	if err != nil {
		return err
	}
	if length != 5 && length != 6 {
		return fmt.Errorf(
			"decode workspace signature: expected 5 or 6 fields, got %d",
			length,
		)
	}
	if signature.Parameters, err =
		decodeWorkspaceParameters(
			decoder,
			context,
			parameterIDs,
		); err != nil {
		return err
	}
	templates, err := decodeWorkspaceTemplates(decoder, context)
	if err != nil {
		return err
	}
	throws, err := decodeWorkspaceTypes(decoder, context)
	if err != nil {
		return err
	}
	literalReturns, err := decodeWorkspaceLiteralReturns(decoder, context)
	if err != nil {
		return err
	}
	constantReturns, err := decodeWorkspaceConstantReturns(decoder, context)
	if err != nil {
		return err
	}
	var assertions []TypeAssertion
	if length >= 6 {
		if err := decoder.Decode(&assertions); err != nil {
			return err
		}
	}
	if len(templates) != 0 ||
		len(throws) != 0 ||
		len(literalReturns) != 0 ||
		len(constantReturns) != 0 ||
		len(assertions) != 0 {
		extras := newWorkspaceSignatureExtras(
			templates,
			throws,
			literalReturns,
			constantReturns,
			assertions,
		)
		signature.Extras = &extras
	}
	return nil
}

func decodeOptionalWorkspaceSignature(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
	parameterIDs *[]decodedWorkspaceParameterID,
) (*workspaceSignature, error) {
	code, err := decoder.PeekCode()
	if err != nil {
		return nil, err
	}
	if code == msgpcode.Nil {
		return nil, decoder.DecodeNil()
	}
	signature := &workspaceSignature{}
	if err := signature.decodeMsgpackIDs(
		decoder,
		context,
		parameterIDs,
	); err != nil {
		return nil, err
	}
	return signature, nil
}

func decodeWorkspaceTemplates(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) ([]TemplateParameter, error) {
	length, err := decodeWorkspaceCollectionLen(decoder, "templates")
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, nil
	}
	result := make([]TemplateParameter, length)
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
			case "Bound":
				item.Bound, err = context.decodeType(decoder)
			case "Default":
				item.Default, err = context.decodeType(decoder)
			case "Covariant":
				item.Covariant, err = decoder.DecodeBool()
			case "Contravariant":
				item.Contravariant, err = decoder.DecodeBool()
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

func decodeWorkspaceLiteralReturns(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) ([]LiteralReturn, error) {
	length, err := decodeWorkspaceCollectionLen(decoder, "literal returns")
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, nil
	}
	result := make([]LiteralReturn, length)
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
			case "Value":
				item.Value, err = context.decodeString(decoder)
			case "Range":
				item.Range, err =
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

func decodeWorkspaceConstantReturns(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) ([]ConstantReturn, error) {
	length, err := decodeWorkspaceCollectionLen(decoder, "constant returns")
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, nil
	}
	result := make([]ConstantReturn, length)
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
			case "Receiver":
				item.Receiver, err = context.decodeString(decoder)
			case "Name":
				item.Name, err = context.decodeString(decoder)
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
