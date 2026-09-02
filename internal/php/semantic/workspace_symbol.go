package semantic

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/vmihailenco/msgpack/v5"
)

type workspaceSymbol struct {
	_msgpack struct{} `msgpack:",as_array"` //nolint:unused // Encoding layout marker.

	ID       SymbolID
	Document *workspaceDocument

	primaryType types.Type

	Ranges     workspaceSymbolRanges
	properties uint16

	sideIndexes         uint32
	nameIndex           uint32
	fullyQualifiedIndex uint32
	containerIndex      uint32
	flagsAndRangeIndex  uint32
	typeSelectors       uint32
}

const (
	workspaceSymbolTypeSelectorBits = 3
	workspaceSymbolTypeSelectorMask = 1<<workspaceSymbolTypeSelectorBits - 1
	workspaceSymbolTypeFieldCount   = 4

	workspaceSymbolTypeExtraIndexShift = workspaceSymbolTypeSelectorBits * workspaceSymbolTypeFieldCount
	workspaceSymbolTypeExtraIndexMask  = 1<<(32-workspaceSymbolTypeExtraIndexShift) - 1
)

type workspaceSymbolTypeExtras struct {
	Values [3]types.Type
}

type workspaceSymbolTypeExtraTable struct {
	Values []workspaceSymbolTypeExtras
}

func (symbol *workspaceSymbol) setTypes(
	document *workspaceDocument,
	value,
	native,
	doc,
	result types.Type,
) {
	if symbol == nil {
		return
	}
	values := [...]types.Type{value, native, doc, result}
	var distinct [workspaceSymbolTypeFieldCount]types.Type
	distinctCount := 0
	selectors := uint32(0)
	for fieldIndex, current := range values {
		if current.IsUnknown() {
			continue
		}
		selector := 0
		for distinctIndex := 0; distinctIndex < distinctCount; distinctIndex++ {
			if current.Equal(distinct[distinctIndex]) {
				selector = distinctIndex + 1
				break
			}
		}
		if selector == 0 {
			distinct[distinctCount] = current
			distinctCount++
			selector = distinctCount
		}
		selectors |= uint32(selector) <<
			(fieldIndex * workspaceSymbolTypeSelectorBits)
	}
	symbol.primaryType = distinct[0]
	symbol.typeSelectors = selectors
	if distinctCount <= 1 || document == nil {
		return
	}
	if document.symbolTypeExtras == nil {
		document.symbolTypeExtras = &workspaceSymbolTypeExtraTable{}
	}
	extraIndex := len(document.symbolTypeExtras.Values)
	if uint64(extraIndex)+1 > uint64(workspaceSymbolTypeExtraIndexMask) {
		panic("semantic: workspace symbol type extra index exceeds packed range")
	}
	extra := workspaceSymbolTypeExtras{}
	copy(extra.Values[:], distinct[1:distinctCount])
	document.symbolTypeExtras.Values = append(
		document.symbolTypeExtras.Values,
		extra,
	)
	symbol.typeSelectors |= uint32(extraIndex+1) <<
		workspaceSymbolTypeExtraIndexShift
}

func (symbol *workspaceSymbol) typeAt(fieldIndex int) types.Type {
	if symbol == nil || fieldIndex < 0 ||
		fieldIndex >= workspaceSymbolTypeFieldCount {
		return types.Type{}
	}
	selector := symbol.typeSelectors >>
		(fieldIndex * workspaceSymbolTypeSelectorBits) &
		workspaceSymbolTypeSelectorMask
	if selector == 0 {
		return types.Type{}
	}
	if selector == 1 {
		return symbol.primaryType
	}
	if symbol.Document == nil || symbol.Document.symbolTypeExtras == nil {
		return types.Type{}
	}
	extraIndex := symbol.typeSelectors >>
		workspaceSymbolTypeExtraIndexShift
	if extraIndex == 0 ||
		int(extraIndex) > len(symbol.Document.symbolTypeExtras.Values) {
		return types.Type{}
	}
	valueIndex := int(selector - 2)
	if valueIndex >= len(workspaceSymbolTypeExtras{}.Values) {
		return types.Type{}
	}
	return symbol.Document.symbolTypeExtras.Values[extraIndex-1].Values[valueIndex]
}

func (symbol *workspaceSymbol) valueType() types.Type {
	return symbol.typeAt(0)
}

func (symbol *workspaceSymbol) nativeType() types.Type {
	return symbol.typeAt(1)
}

func (symbol *workspaceSymbol) docType() types.Type {
	return symbol.typeAt(2)
}

func (symbol *workspaceSymbol) returnType() types.Type {
	return symbol.typeAt(3)
}

type workspaceSharedStringTable struct {
	Values []string
	Shared bool
}

type workspaceSymbolStringTable = workspaceSharedStringTable

func shareWorkspaceSymbolStrings(
	document *workspaceDocument,
	shared **workspaceSymbolStringTable,
	index *map[*byte]uint32,
) {
	if document == nil ||
		document.symbolStrings == nil ||
		len(document.symbolStrings.Values) == 0 ||
		document.symbolStrings == *shared {
		return
	}
	if *shared == nil {
		*shared = &workspaceSymbolStringTable{Shared: true}
	} else {
		(*shared).Shared = true
	}
	if *index == nil {
		*index = make(map[*byte]uint32)
	}
	local := document.symbolStrings.Values
	remap := make([]uint32, len(local)+1)
	for valueIndex, value := range local {
		key := unsafe.StringData(value)
		id, exists := (*index)[key]
		if !exists {
			if uint64(len((*shared).Values)) == math.MaxUint32 {
				panic("semantic: shared symbol string table exceeds uint32")
			}
			id = uint32(len((*shared).Values) + 1)
			(*index)[key] = id
			(*shared).Values = append((*shared).Values, value)
		}
		remap[valueIndex+1] = id
	}
	for symbolIndex := range document.Symbols {
		symbol := &document.Symbols[symbolIndex]
		symbol.nameIndex = remapWorkspaceSymbolStringIndex(
			symbol.nameIndex,
			remap,
		)
		symbol.fullyQualifiedIndex = remapWorkspaceSymbolStringIndex(
			symbol.fullyQualifiedIndex,
			remap,
		)
		symbol.containerIndex = remapWorkspaceSymbolStringIndex(
			symbol.containerIndex,
			remap,
		)
	}
	document.symbolStrings = *shared
}

func remapWorkspaceSymbolStringIndex(
	index uint32,
	remap []uint32,
) uint32 {
	if index == 0 {
		return 0
	}
	if int(index) >= len(remap) {
		panic("semantic: workspace symbol string index exceeds local table")
	}
	return remap[index]
}

func (symbol *workspaceSymbol) symbolString(index uint32) string {
	if symbol == nil ||
		index == 0 ||
		symbol.Document == nil ||
		symbol.Document.symbolStrings == nil ||
		int(index) > len(symbol.Document.symbolStrings.Values) {
		return ""
	}
	return symbol.Document.symbolStrings.Values[index-1]
}

func (symbol *workspaceSymbol) name() string {
	if symbol == nil {
		return ""
	}
	return symbol.symbolString(symbol.nameIndex)
}

func (symbol *workspaceSymbol) fullyQualified() string {
	if symbol == nil {
		return ""
	}
	return symbol.symbolString(symbol.fullyQualifiedIndex)
}

func (symbol *workspaceSymbol) container() SymbolID {
	if symbol == nil {
		return ""
	}
	return SymbolID(symbol.symbolString(symbol.containerIndex))
}

const (
	workspaceCompactRangeMissing = math.MaxUint16

	workspaceSymbolRangeIndexShift = 13
	workspaceSymbolFlagsMask       = 1<<workspaceSymbolRangeIndexShift - 1
	workspaceSymbolRangeIndexMask  = 1<<(32-workspaceSymbolRangeIndexShift) - 1

	workspaceSymbolSignatureBits = 12
	workspaceSymbolHierarchyBits = 8
	workspaceSymbolMetadataBits  = 12

	workspaceSymbolSignatureShift = 0
	workspaceSymbolHierarchyShift = workspaceSymbolSignatureBits
	workspaceSymbolMetadataShift  = workspaceSymbolHierarchyShift +
		workspaceSymbolHierarchyBits

	workspaceSymbolSignatureMask = 1<<workspaceSymbolSignatureBits - 1
	workspaceSymbolHierarchyMask = 1<<workspaceSymbolHierarchyBits - 1
	workspaceSymbolMetadataMask  = 1<<workspaceSymbolMetadataBits - 1
	// All-one signature bits mark an exact side-table fallback. The remaining
	// 20 bits then hold its zero-based index, which covers the maximum decoded
	// symbol collection while preserving the full uint32 side indexes there.
	workspaceSymbolSideFallbackIndexMask = 1<<(32-workspaceSymbolSignatureBits) - 1

	workspaceSymbolKindBits = 4
	workspaceSymbolKindMask = 1<<workspaceSymbolKindBits - 1

	workspaceSymbolVisibilityBits  = 2
	workspaceSymbolVisibilityShift = workspaceSymbolKindBits
	workspaceSymbolVisibilityMask  = 1<<workspaceSymbolVisibilityBits - 1

	workspaceSymbolWriteVisibilityShift = workspaceSymbolVisibilityShift +
		workspaceSymbolVisibilityBits
	workspaceSymbolHasWriteVisibility = uint16(1 << 8)
)

type workspaceSymbolRanges struct {
	StartLow  uint16
	StartHigh uint16
	Deltas    [5]uint16
}

func (ranges workspaceSymbolRanges) start() uint32 {
	return uint32(ranges.StartLow) | uint32(ranges.StartHigh)<<16
}

type workspaceSymbolFullRanges struct {
	Range          cst.TextRange
	SelectionRange cst.TextRange
	BodyRange      cst.TextRange
}

type workspaceSymbolRangeExtras struct {
	Values []workspaceSymbolFullRanges
	Sides  []workspaceSymbolSideIndexes
}

type workspaceSymbolSideIndexes struct {
	Signature uint32
	Hierarchy uint32
	Metadata  uint32
}

func compactWorkspaceSymbolRanges(
	rng,
	selectionRange,
	bodyRange cst.TextRange,
) (workspaceSymbolRanges, bool) {
	rangeLength, ok := compactWorkspaceSymbolDelta(rng.Start, rng.End)
	if !ok {
		return workspaceSymbolRanges{}, false
	}
	selectionStart, selectionLength, ok := compactWorkspaceSymbolSubrange(
		rng.Start,
		selectionRange,
	)
	if !ok {
		return workspaceSymbolRanges{}, false
	}
	bodyStart, bodyLength, ok := compactWorkspaceSymbolSubrange(
		rng.Start,
		bodyRange,
	)
	if !ok {
		return workspaceSymbolRanges{}, false
	}
	return workspaceSymbolRanges{
		StartLow:  uint16(rng.Start),
		StartHigh: uint16(rng.Start >> 16),
		Deltas: [5]uint16{
			rangeLength,
			selectionStart,
			selectionLength,
			bodyStart,
			bodyLength,
		},
	}, true
}

func compactWorkspaceSymbolDelta(start, end uint32) (uint16, bool) {
	if end < start || end-start >= workspaceCompactRangeMissing {
		return 0, false
	}
	return uint16(end - start), true
}

func compactWorkspaceSymbolSubrange(
	base uint32,
	rng cst.TextRange,
) (uint16, uint16, bool) {
	if rng == (cst.TextRange{}) {
		return workspaceCompactRangeMissing, 0, true
	}
	start, ok := compactWorkspaceSymbolDelta(base, rng.Start)
	if !ok {
		return 0, 0, false
	}
	length, ok := compactWorkspaceSymbolDelta(rng.Start, rng.End)
	if !ok {
		return 0, 0, false
	}
	return start, length, true
}

func (ranges workspaceSymbolRanges) materialize() workspaceSymbolFullRanges {
	start := ranges.start()
	return workspaceSymbolFullRanges{
		Range: cst.TextRange{
			Start: start,
			End:   start + uint32(ranges.Deltas[0]),
		},
		SelectionRange: materializeWorkspaceSymbolSubrange(
			start,
			ranges.Deltas[1],
			ranges.Deltas[2],
		),
		BodyRange: materializeWorkspaceSymbolSubrange(
			start,
			ranges.Deltas[3],
			ranges.Deltas[4],
		),
	}
}

func materializeWorkspaceSymbolSubrange(
	base uint32,
	start,
	length uint16,
) cst.TextRange {
	if start == workspaceCompactRangeMissing {
		return cst.TextRange{}
	}
	rangeStart := base + uint32(start)
	return cst.TextRange{
		Start: rangeStart,
		End:   rangeStart + uint32(length),
	}
}

func (symbol *workspaceSymbol) ranges() workspaceSymbolFullRanges {
	if symbol == nil {
		return workspaceSymbolFullRanges{}
	}
	index := symbol.rangeIndex()
	if index == 0 {
		return symbol.Ranges.materialize()
	}
	if symbol.Document == nil ||
		symbol.Document.symbolRangeExtras == nil ||
		int(index) > len(symbol.Document.symbolRangeExtras.Values) {
		return workspaceSymbolFullRanges{}
	}
	return symbol.Document.symbolRangeExtras.Values[index-1]
}

func (symbol *workspaceSymbol) rangeValue() cst.TextRange {
	if symbol == nil {
		return cst.TextRange{}
	}
	index := symbol.rangeIndex()
	if index == 0 {
		start := symbol.Ranges.start()
		return cst.TextRange{
			Start: start,
			End: start +
				uint32(symbol.Ranges.Deltas[0]),
		}
	}
	return symbol.ranges().Range
}

func (symbol *workspaceSymbol) flags() Flags {
	if symbol == nil {
		return 0
	}
	return Flags(symbol.flagsAndRangeIndex & workspaceSymbolFlagsMask)
}

func (symbol *workspaceSymbol) rangeIndex() uint32 {
	if symbol == nil {
		return 0
	}
	return symbol.flagsAndRangeIndex >> workspaceSymbolRangeIndexShift
}

func (symbol *workspaceSymbol) setFlagsAndRangeIndex(
	flags Flags,
	index int,
) {
	if uint32(flags)&^uint32(workspaceSymbolFlagsMask) != 0 {
		panic("semantic: workspace symbol flags exceed packed range")
	}
	var packedIndex uint32
	if index >= 0 {
		packedIndex = uint32(index) + 1
		if packedIndex > workspaceSymbolRangeIndexMask {
			panic("semantic: workspace symbol range index exceeds packed range")
		}
	}
	symbol.flagsAndRangeIndex =
		packedIndex<<workspaceSymbolRangeIndexShift |
			uint32(flags)
}

func (symbol *workspaceSymbol) path() string {
	if symbol == nil || symbol.Document == nil {
		return ""
	}
	return symbol.Document.Path
}

func (symbol *workspaceSymbol) kind() SymbolKind {
	if symbol == nil {
		return NamespaceSymbol
	}
	return SymbolKind(symbol.properties & workspaceSymbolKindMask)
}

func (symbol *workspaceSymbol) visibility() Visibility {
	if symbol == nil {
		return Public
	}
	return Visibility(
		symbol.properties >> workspaceSymbolVisibilityShift &
			workspaceSymbolVisibilityMask,
	)
}

func (symbol *workspaceSymbol) writeVisibility() Visibility {
	if symbol == nil {
		return Public
	}
	return Visibility(
		symbol.properties >> workspaceSymbolWriteVisibilityShift &
			workspaceSymbolVisibilityMask,
	)
}

func (symbol *workspaceSymbol) hasWriteVisibility() bool {
	return symbol != nil &&
		symbol.properties&workspaceSymbolHasWriteVisibility != 0
}

func (symbol *workspaceSymbol) setProperties(
	kind SymbolKind,
	visibility,
	writeVisibility Visibility,
	hasWriteVisibility bool,
) {
	if symbol == nil {
		return
	}
	if uint8(kind) > workspaceSymbolKindMask {
		panic("semantic: workspace symbol kind exceeds packed range")
	}
	if uint8(visibility) > workspaceSymbolVisibilityMask ||
		uint8(writeVisibility) > workspaceSymbolVisibilityMask {
		panic("semantic: workspace symbol visibility exceeds packed range")
	}
	symbol.properties = uint16(kind) |
		uint16(visibility)<<workspaceSymbolVisibilityShift |
		uint16(writeVisibility)<<workspaceSymbolWriteVisibilityShift
	if hasWriteVisibility {
		symbol.properties |= workspaceSymbolHasWriteVisibility
	}
}

func (symbol *workspaceSymbol) signature() *workspaceSignature {
	if symbol == nil || symbol.Document == nil {
		return nil
	}
	index := symbol.sideIndex(workspaceSymbolSignatureShift)
	if index == 0 || int(index) > len(symbol.Document.signatures) {
		return nil
	}
	return &symbol.Document.signatures[index-1]
}

func (symbol *workspaceSymbol) hierarchy() *workspaceHierarchy {
	if symbol == nil || symbol.Document == nil {
		return nil
	}
	index := symbol.sideIndex(workspaceSymbolHierarchyShift)
	if index == 0 || int(index) > len(symbol.Document.hierarchies) {
		return nil
	}
	return &symbol.Document.hierarchies[index-1]
}

func (symbol *workspaceSymbol) metadata() *workspaceMetadata {
	if symbol == nil || symbol.Document == nil {
		return nil
	}
	index := symbol.sideIndex(workspaceSymbolMetadataShift)
	if index == 0 || int(index) > len(symbol.Document.metadata) {
		return nil
	}
	return &symbol.Document.metadata[index-1]
}

func (symbol *workspaceSymbol) sideIndex(shift int) uint32 {
	if symbol == nil {
		return 0
	}
	if symbol.hasSideFallback() {
		index := symbol.sideIndexes >> workspaceSymbolSignatureBits
		if symbol.Document == nil ||
			symbol.Document.symbolRangeExtras == nil ||
			int(index) >= len(symbol.Document.symbolRangeExtras.Sides) {
			return 0
		}
		sides := symbol.Document.symbolRangeExtras.Sides[index]
		switch shift {
		case workspaceSymbolSignatureShift:
			return sides.Signature
		case workspaceSymbolHierarchyShift:
			return sides.Hierarchy
		case workspaceSymbolMetadataShift:
			return sides.Metadata
		default:
			return 0
		}
	}
	switch shift {
	case workspaceSymbolSignatureShift:
		return symbol.sideIndexes >> shift & workspaceSymbolSignatureMask
	case workspaceSymbolHierarchyShift:
		return symbol.sideIndexes >> shift & workspaceSymbolHierarchyMask
	case workspaceSymbolMetadataShift:
		return symbol.sideIndexes >> shift & workspaceSymbolMetadataMask
	default:
		return 0
	}
}

func (symbol *workspaceSymbol) hasSideFallback() bool {
	return symbol != nil &&
		symbol.sideIndexes&workspaceSymbolSignatureMask ==
			workspaceSymbolSignatureMask
}

func (symbol *workspaceSymbol) setSideIndexes(
	signature,
	hierarchy,
	metadata int,
) {
	if symbol == nil {
		return
	}
	packedSignature, signatureOK := packWorkspaceSymbolSideIndex(
		signature,
		workspaceSymbolSignatureMask-1,
	)
	packedHierarchy, hierarchyOK := packWorkspaceSymbolSideIndex(
		hierarchy,
		workspaceSymbolHierarchyMask,
	)
	packedMetadata, metadataOK := packWorkspaceSymbolSideIndex(
		metadata,
		workspaceSymbolMetadataMask,
	)
	if signatureOK && hierarchyOK && metadataOK {
		symbol.sideIndexes = packedSignature<<workspaceSymbolSignatureShift |
			packedHierarchy<<workspaceSymbolHierarchyShift |
			packedMetadata<<workspaceSymbolMetadataShift
		return
	}
	if symbol.Document == nil {
		panic("semantic: workspace symbol side-table fallback requires document")
	}
	if symbol.Document.symbolRangeExtras == nil {
		symbol.Document.symbolRangeExtras = &workspaceSymbolRangeExtras{}
	}
	extras := symbol.Document.symbolRangeExtras
	index := len(extras.Sides)
	if uint64(index) > uint64(workspaceSymbolSideFallbackIndexMask) {
		panic("semantic: workspace symbol side-table fallback index exceeds packed range")
	}
	extras.Sides = append(extras.Sides, workspaceSymbolSideIndexes{
		Signature: fullWorkspaceSymbolSideIndex(signature),
		Hierarchy: fullWorkspaceSymbolSideIndex(hierarchy),
		Metadata:  fullWorkspaceSymbolSideIndex(metadata),
	})
	symbol.sideIndexes = workspaceSymbolSignatureMask |
		uint32(index)<<workspaceSymbolSignatureBits
}

func packWorkspaceSymbolSideIndex(index int, mask uint32) (uint32, bool) {
	if index < 0 {
		return 0, true
	}
	value := uint64(index) + 1
	if value > uint64(mask) {
		return 0, false
	}
	return uint32(value), true
}

func fullWorkspaceSymbolSideIndex(index int) uint32 {
	if index < 0 {
		return 0
	}
	value := uint64(index) + 1
	if value > math.MaxUint32 {
		panic("semantic: workspace symbol side-table index exceeds uint32")
	}
	return uint32(value)
}

// DecodeMsgpack restores the fixed workspace-symbol wire layout without first
// allocating a distinct copy of the enclosing document path for every symbol.
// The serialized path field remains present for cache compatibility; the
// document binds its authoritative path after all symbols have been decoded.
func (symbol *workspaceSymbol) DecodeMsgpack(
	decoder *msgpack.Decoder,
) error {
	return symbol.decodeMsgpack(decoder, NewWorkspaceGraphDecoder())
}

func (symbol *workspaceSymbol) decodeMsgpack(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) error {
	var sides decodedWorkspaceSymbolSides
	var parameterIDs []decodedWorkspaceParameterID
	document := &workspaceDocument{}
	symbolStrings := newWorkspaceSymbolStringBuilder(document, 1)
	if err := symbol.decodeFields(
		decoder,
		context,
		document,
		&sides,
		&parameterIDs,
		&symbolStrings,
	); err != nil {
		return err
	}
	document.Symbols = []workspaceSymbol{*symbol}
	document.attachDecodedSymbolSides(
		[]decodedWorkspaceSymbolSides{sides},
		parameterIDs,
	)
	*symbol = document.Symbols[0]
	return nil
}

type decodedWorkspaceSymbolSides struct {
	signature        *workspaceSignature
	parameterIDStart uint32
	parameterIDCount uint32
	hierarchy        *workspaceHierarchy
	metadata         *workspaceMetadata
	ranges           *workspaceSymbolFullRanges
}

// decodedWorkspaceParameterID records the parameter position explicitly so a
// cache containing a mixture of the legacy full-ID layout and the compact
// derived-ID layout still restores the right parameter. Current caches do not
// retain entries for derived IDs.
type decodedWorkspaceParameterID struct {
	ID    SymbolID
	Index uint32
}

func (symbol *workspaceSymbol) decodeFields(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
	document *workspaceDocument,
	sides *decodedWorkspaceSymbolSides,
	parameterIDs *[]decodedWorkspaceParameterID,
	symbolStrings *workspaceSymbolStringBuilder,
) error {
	length, err := decoder.DecodeArrayLen()
	if err != nil {
		return err
	}
	if length != 20 {
		return fmt.Errorf(
			"decode workspace symbol: expected 20 fields, got %d",
			length,
		)
	}

	*symbol = workspaceSymbol{}
	id, err := context.decodeString(decoder)
	if err != nil {
		return err
	}
	symbol.ID = SymbolID(id)
	name, err := context.decodeString(decoder)
	if err != nil {
		return err
	}
	symbol.nameIndex = symbolStrings.indexFor(name)
	fullyQualified, err := context.decodeString(decoder)
	if err != nil {
		return err
	}
	symbol.fullyQualifiedIndex = symbolStrings.indexFor(fullyQualified)
	container, err := context.decodeString(decoder)
	if err != nil {
		return err
	}
	symbol.containerIndex = symbolStrings.indexFor(container)
	if _, err = context.decodeString(decoder); err != nil {
		return err
	}
	valueType, err := context.decodeType(decoder)
	if err != nil {
		return err
	}
	nativeType, err := context.decodeType(decoder)
	if err != nil {
		return err
	}
	docType, err := context.decodeType(decoder)
	if err != nil {
		return err
	}
	returnType, err := context.decodeType(decoder)
	if err != nil {
		return err
	}
	symbol.setTypes(document, valueType, nativeType, docType, returnType)
	var ranges workspaceSymbolFullRanges
	if ranges.Range, err =
		decodeWorkspaceTextRange(decoder, context); err != nil {
		return err
	}
	if ranges.SelectionRange, err =
		decodeWorkspaceTextRange(decoder, context); err != nil {
		return err
	}
	if ranges.BodyRange, err =
		decodeWorkspaceTextRange(decoder, context); err != nil {
		return err
	}
	if compact, ok := compactWorkspaceSymbolRanges(
		ranges.Range,
		ranges.SelectionRange,
		ranges.BodyRange,
	); ok {
		symbol.Ranges = compact
	} else {
		sides.ranges = &ranges
	}
	flags, err := decoder.DecodeUint32()
	if err != nil {
		return err
	}
	if flags&^uint32(workspaceSymbolFlagsMask) != 0 {
		return fmt.Errorf(
			"decode workspace symbol: flags %d exceed packed range",
			flags,
		)
	}
	symbol.flagsAndRangeIndex = flags
	kind, err := decoder.DecodeUint8()
	if err != nil {
		return err
	}
	if kind > workspaceSymbolKindMask {
		return fmt.Errorf(
			"decode workspace symbol: kind %d exceeds packed range",
			kind,
		)
	}
	visibility, err := decoder.DecodeUint8()
	if err != nil {
		return err
	}
	if visibility > workspaceSymbolVisibilityMask {
		return fmt.Errorf(
			"decode workspace symbol: visibility %d exceeds packed range",
			visibility,
		)
	}
	writeVisibility, err := decoder.DecodeUint8()
	if err != nil {
		return err
	}
	if writeVisibility > workspaceSymbolVisibilityMask {
		return fmt.Errorf(
			"decode workspace symbol: write visibility %d exceeds packed range",
			writeVisibility,
		)
	}
	hasWriteVisibility, err := decoder.DecodeBool()
	if err != nil {
		return err
	}
	symbol.setProperties(
		SymbolKind(kind),
		Visibility(visibility),
		Visibility(writeVisibility),
		hasWriteVisibility,
	)
	parameterIDStart := len(*parameterIDs)
	if sides.signature, err =
		decodeOptionalWorkspaceSignature(
			decoder,
			context,
			parameterIDs,
		); err != nil {
		return err
	}
	sides.parameterIDStart = uint32(parameterIDStart)
	sides.parameterIDCount = uint32(len(*parameterIDs) - parameterIDStart)
	if sides.hierarchy, err =
		decodeOptionalWorkspaceHierarchy(decoder, context); err != nil {
		return err
	}
	sides.metadata, err = decodeOptionalWorkspaceMetadata(decoder, context)
	return err
}

func decodeWorkspaceSymbols(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
	document *workspaceDocument,
) error {
	if context != nil && context.stringCache != nil {
		defer context.resetTransientStrings()
	}
	length, err := decodeWorkspaceCollectionLen(decoder, "symbols")
	if err != nil {
		return err
	}
	if length < 0 {
		return nil
	}
	document.Symbols = make([]workspaceSymbol, length)
	sides := make([]decodedWorkspaceSymbolSides, length)
	var parameterIDs []decodedWorkspaceParameterID
	symbolStrings := context.newWorkspaceSymbolStringBuilder(
		document,
		length,
	)
	for index := range document.Symbols {
		if err := document.Symbols[index].decodeFields(
			decoder,
			context,
			document,
			&sides[index],
			&parameterIDs,
			&symbolStrings,
		); err != nil {
			return err
		}
	}
	document.attachDecodedSymbolSides(sides, parameterIDs)
	if context != nil && context.stringCache != nil {
		for symbolIndex := range document.Symbols {
			signature := document.Symbols[symbolIndex].signature()
			if signature == nil {
				continue
			}
			for parameterIndex := range signature.Parameters {
				extras := signature.Parameters[parameterIndex].Extras
				if extras == nil || extras.ID == "" {
					continue
				}
				extras.ID = SymbolID(
					context.retainTransientString(string(extras.ID)),
				)
			}
		}
	}
	return nil
}
