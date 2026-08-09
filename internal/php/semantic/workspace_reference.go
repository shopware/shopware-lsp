package semantic

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/vmihailenco/msgpack/v5"
)

type workspaceReference struct {
	RangeStart       uint32
	nameAndMetadata  uint32
	resolvedAndCount uint32
	valueAndRange    uint32
	receiverAndFlags uint32
}

const (
	workspaceReferenceValueBits         = 16
	workspaceReferenceValueMask         = 1<<workspaceReferenceValueBits - 1
	workspaceReferenceRangeShift        = workspaceReferenceValueBits
	workspaceReferenceFullRangeSentinel = math.MaxUint16

	workspaceReferenceIndexBits = 21
	workspaceReferenceIndexMask = 1<<workspaceReferenceIndexBits - 1

	workspaceReferenceCountShift = workspaceReferenceIndexBits
	workspaceReferenceKindShift  = workspaceReferenceCountShift + 8
	workspaceReferenceKindMask   = 0x07

	workspaceReferenceTargetShift = workspaceReferenceIndexBits
	workspaceReferenceFlagsShift  = workspaceReferenceTargetShift + 4
	workspaceReferenceTargetMask  = 0x0f
	workspaceReferenceFlagsMask   = 0x0f
)

type workspaceReferenceFull struct {
	Range      cst.TextRange
	ValueStart uint32
}

type workspaceReferenceExtras struct {
	Values []workspaceReferenceFull
}

type workspaceReferenceStringTable = workspaceSharedStringTable

func shareWorkspaceReferenceStrings(
	document *workspaceDocument,
	shared **workspaceReferenceStringTable,
	index *map[*byte]uint32,
) {
	if document == nil ||
		document.referenceStrings == nil ||
		len(document.referenceStrings.Values) == 0 ||
		document.referenceStringIDs != nil {
		return
	}
	if *shared == nil {
		*shared = &workspaceReferenceStringTable{Shared: true}
	} else {
		(*shared).Shared = true
	}
	if *index == nil {
		*index = make(map[*byte]uint32)
	}
	local := document.referenceStrings.Values
	ids := make([]uint32, len(local))
	for valueIndex, value := range local {
		key := unsafe.StringData(value)
		id, exists := (*index)[key]
		if !exists {
			if uint64(len((*shared).Values)) == math.MaxUint32 {
				panic("semantic: shared reference string table exceeds uint32")
			}
			id = uint32(len((*shared).Values) + 1)
			(*index)[key] = id
			(*shared).Values = append((*shared).Values, value)
		}
		ids[valueIndex] = id
	}
	document.referenceStrings = *shared
	document.referenceStringIDs = ids
}

func compactWorkspaceReferenceLocation(
	rng cst.TextRange,
	valueStart uint32,
) (uint16, bool) {
	if valueStart > workspaceReferenceValueMask {
		return 0, false
	}
	length, ok := compactWorkspaceSymbolDelta(rng.Start, rng.End)
	if !ok {
		return 0, false
	}
	return length, true
}

func newWorkspaceReference(
	rng cst.TextRange,
	nameIndex,
	resolvedIndex,
	valueStart,
	receiverIndex uint32,
	qualifiedCount,
	candidateCount uint8,
	kind NameKind,
	targetKind SymbolKind,
	flags uint8,
	fullIndex int,
) workspaceReference {
	if nameIndex > workspaceReferenceIndexMask ||
		resolvedIndex > workspaceReferenceIndexMask ||
		receiverIndex > workspaceReferenceIndexMask {
		panic("semantic: workspace reference index exceeds packed range")
	}
	if uint8(kind) > workspaceReferenceKindMask ||
		uint8(targetKind) > workspaceReferenceTargetMask ||
		flags > workspaceReferenceFlagsMask {
		panic("semantic: workspace reference metadata exceeds packed range")
	}
	var valueAndRange uint32
	if fullIndex < 0 {
		rangeLength, ok := compactWorkspaceReferenceLocation(rng, valueStart)
		if !ok {
			panic("semantic: workspace reference location requires full fallback")
		}
		valueAndRange = valueStart |
			uint32(rangeLength)<<workspaceReferenceRangeShift
	} else {
		index := uint32(fullIndex) + 1
		if index > workspaceReferenceValueMask {
			panic("semantic: workspace reference fallback index exceeds packed range")
		}
		valueAndRange = index |
			uint32(workspaceReferenceFullRangeSentinel)<<
				workspaceReferenceRangeShift
	}
	return workspaceReference{
		RangeStart: rng.Start,
		nameAndMetadata: nameIndex |
			uint32(qualifiedCount)<<workspaceReferenceCountShift |
			uint32(kind)<<workspaceReferenceKindShift,
		resolvedAndCount: resolvedIndex |
			uint32(candidateCount)<<workspaceReferenceCountShift,
		valueAndRange: valueAndRange,
		receiverAndFlags: receiverIndex |
			uint32(targetKind)<<workspaceReferenceTargetShift |
			uint32(flags)<<workspaceReferenceFlagsShift,
	}
}

func (document *workspaceDocument) newReference(
	rng cst.TextRange,
	nameIndex,
	resolvedIndex,
	valueStart,
	receiverIndex uint32,
	qualifiedCount,
	candidateCount uint8,
	kind NameKind,
	targetKind SymbolKind,
	flags uint8,
) workspaceReference {
	fullIndex := -1
	if _, ok := compactWorkspaceReferenceLocation(rng, valueStart); !ok {
		if document.referenceExtras == nil {
			document.referenceExtras = &workspaceReferenceExtras{}
		}
		fullIndex = len(document.referenceExtras.Values)
		document.referenceExtras.Values = append(
			document.referenceExtras.Values,
			workspaceReferenceFull{
				Range:      rng,
				ValueStart: valueStart,
			},
		)
	}
	return newWorkspaceReference(
		rng,
		nameIndex,
		resolvedIndex,
		valueStart,
		receiverIndex,
		qualifiedCount,
		candidateCount,
		kind,
		targetKind,
		flags,
		fullIndex,
	)
}

func (reference *workspaceReference) hasFullLocation() bool {
	if reference == nil {
		return false
	}
	return uint16(reference.valueAndRange>>workspaceReferenceRangeShift) ==
		workspaceReferenceFullRangeSentinel
}

func (reference *workspaceReference) fullIndex() uint32 {
	if reference == nil || !reference.hasFullLocation() {
		return 0
	}
	return reference.valueAndRange & workspaceReferenceValueMask
}

func (reference *workspaceReference) fullLocation(
	document *workspaceDocument,
) (workspaceReferenceFull, bool) {
	index := reference.fullIndex()
	if index == 0 ||
		document == nil ||
		document.referenceExtras == nil ||
		int(index) > len(document.referenceExtras.Values) {
		return workspaceReferenceFull{}, false
	}
	return document.referenceExtras.Values[index-1], true
}

func (reference *workspaceReference) rangeValue(
	document *workspaceDocument,
) cst.TextRange {
	if reference == nil {
		return cst.TextRange{}
	}
	length := uint16(
		reference.valueAndRange >> workspaceReferenceRangeShift,
	)
	if length != workspaceReferenceFullRangeSentinel {
		return cst.TextRange{
			Start: reference.RangeStart,
			End:   reference.RangeStart + uint32(length),
		}
	}
	if full, ok := reference.fullLocation(document); ok {
		return full.Range
	}
	return cst.TextRange{}
}

func (reference *workspaceReference) valueStart(
	document *workspaceDocument,
) uint32 {
	if reference == nil {
		return 0
	}
	if !reference.hasFullLocation() {
		return reference.valueAndRange & workspaceReferenceValueMask
	}
	if full, ok := reference.fullLocation(document); ok {
		return full.ValueStart
	}
	return 0
}

func (reference *workspaceReference) location(
	document *workspaceDocument,
) (cst.TextRange, uint32) {
	if reference == nil {
		return cst.TextRange{}, 0
	}
	length := uint16(
		reference.valueAndRange >> workspaceReferenceRangeShift,
	)
	if length != workspaceReferenceFullRangeSentinel {
		return cst.TextRange{
				Start: reference.RangeStart,
				End:   reference.RangeStart + uint32(length),
			},
			reference.valueAndRange & workspaceReferenceValueMask
	}
	if full, ok := reference.fullLocation(document); ok {
		return full.Range, full.ValueStart
	}
	return cst.TextRange{}, 0
}

func (reference *workspaceReference) nameIndex() uint32 {
	return reference.nameAndMetadata & workspaceReferenceIndexMask
}

func (reference *workspaceReference) resolvedIndex() uint32 {
	return reference.resolvedAndCount & workspaceReferenceIndexMask
}

func (reference *workspaceReference) receiverIndex() uint32 {
	return reference.receiverAndFlags & workspaceReferenceIndexMask
}

func (reference *workspaceReference) qualifiedCount() uint8 {
	return uint8(reference.nameAndMetadata >> workspaceReferenceCountShift)
}

func (reference *workspaceReference) candidateCount() uint8 {
	return uint8(reference.resolvedAndCount >> workspaceReferenceCountShift)
}

func (reference *workspaceReference) kind() NameKind {
	return NameKind(
		reference.nameAndMetadata >> workspaceReferenceKindShift,
	)
}

func (reference *workspaceReference) targetKind() SymbolKind {
	return SymbolKind(
		reference.receiverAndFlags >>
			workspaceReferenceTargetShift &
			workspaceReferenceTargetMask,
	)
}

func (reference *workspaceReference) flags() uint8 {
	return uint8(
		reference.receiverAndFlags >>
			workspaceReferenceFlagsShift &
			workspaceReferenceFlagsMask,
	)
}

func (reference workspaceReference) EncodeMsgpack(
	encoder *msgpack.Encoder,
) error {
	return encodeWorkspaceReference(encoder, &reference, nil)
}

func encodeWorkspaceReference(
	encoder *msgpack.Encoder,
	reference *workspaceReference,
	document *workspaceDocument,
) error {
	if reference == nil {
		return encoder.EncodeNil()
	}
	if reference.hasFullLocation() {
		if _, ok := reference.fullLocation(document); !ok {
			return fmt.Errorf(
				"encode compact workspace reference: missing full location",
			)
		}
	}
	if err := encoder.EncodeArrayLen(12); err != nil {
		return err
	}
	if err := encoder.EncodeUint8(1); err != nil {
		return err
	}
	rng, valueStart := reference.location(document)
	if err := encoder.EncodeUint32(rng.Start); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(rng.End); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(reference.nameIndex()); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(reference.resolvedIndex()); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(valueStart); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(reference.receiverIndex()); err != nil {
		return err
	}
	if err := encoder.EncodeUint16(uint16(reference.qualifiedCount())); err != nil {
		return err
	}
	if err := encoder.EncodeUint16(uint16(reference.candidateCount())); err != nil {
		return err
	}
	if err := encoder.EncodeUint8(uint8(reference.kind())); err != nil {
		return err
	}
	if err := encoder.EncodeUint8(uint8(reference.targetKind())); err != nil {
		return err
	}
	return encoder.EncodeUint8(reference.flags())
}

func (reference *workspaceReference) DecodeMsgpack(
	decoder *msgpack.Decoder,
) error {
	decoded, err := decodeWorkspaceReferenceFields(decoder)
	if err != nil {
		return err
	}
	if _, ok := compactWorkspaceReferenceLocation(
		decoded.Range,
		decoded.ValueStart,
	); !ok {
		return fmt.Errorf(
			"decode compact workspace reference: full location requires document context",
		)
	}
	*reference = newWorkspaceReference(
		decoded.Range,
		decoded.NameIndex,
		decoded.ResolvedIndex,
		decoded.ValueStart,
		decoded.ReceiverIndex,
		decoded.QualifiedCount,
		decoded.CandidateCount,
		decoded.Kind,
		decoded.TargetKind,
		decoded.Flags,
		-1,
	)
	return nil
}

type decodedWorkspaceReference struct {
	Range          cst.TextRange
	NameIndex      uint32
	ResolvedIndex  uint32
	ValueStart     uint32
	ReceiverIndex  uint32
	QualifiedCount uint8
	CandidateCount uint8
	Kind           NameKind
	TargetKind     SymbolKind
	Flags          uint8
}

func decodeWorkspaceReferenceFields(
	decoder *msgpack.Decoder,
) (decodedWorkspaceReference, error) {
	var reference decodedWorkspaceReference
	length, err := decoder.DecodeArrayLen()
	if err != nil {
		return reference, err
	}
	if length != 12 {
		return reference, fmt.Errorf(
			"decode compact workspace reference: expected 12 fields, got %d",
			length,
		)
	}
	version, err := decoder.DecodeUint8()
	if err != nil {
		return reference, err
	}
	if version != 1 {
		return reference, fmt.Errorf(
			"decode compact workspace reference: unsupported layout %d",
			version,
		)
	}
	if reference.Range.Start, err = decoder.DecodeUint32(); err != nil {
		return reference, err
	}
	if reference.Range.End, err = decoder.DecodeUint32(); err != nil {
		return reference, err
	}
	if reference.NameIndex, err = decoder.DecodeUint32(); err != nil {
		return reference, err
	}
	if reference.ResolvedIndex, err = decoder.DecodeUint32(); err != nil {
		return reference, err
	}
	if reference.ValueStart, err = decoder.DecodeUint32(); err != nil {
		return reference, err
	}
	if reference.ReceiverIndex, err = decoder.DecodeUint32(); err != nil {
		return reference, err
	}
	qualifiedCount, err := decoder.DecodeUint32()
	if err != nil {
		return reference, err
	}
	if qualifiedCount > math.MaxUint8 {
		return reference, fmt.Errorf(
			"decode compact workspace reference: qualified count %d exceeds %d",
			qualifiedCount,
			math.MaxUint8,
		)
	}
	candidateCount, err := decoder.DecodeUint32()
	if err != nil {
		return reference, err
	}
	if candidateCount > math.MaxUint8 {
		return reference, fmt.Errorf(
			"decode compact workspace reference: candidate count %d exceeds %d",
			candidateCount,
			math.MaxUint8,
		)
	}
	kind, err := decoder.DecodeUint8()
	if err != nil {
		return reference, err
	}
	targetKind, err := decoder.DecodeUint8()
	if err != nil {
		return reference, err
	}
	flags, err := decoder.DecodeUint8()
	if err != nil {
		return reference, err
	}
	if targetKind > uint8(workspaceReferenceTargetMask) {
		return reference, fmt.Errorf(
			"decode compact workspace reference: target kind %d exceeds %d",
			targetKind,
			workspaceReferenceTargetMask,
		)
	}
	if flags > workspaceReferenceFlagsMask {
		return reference, fmt.Errorf(
			"decode compact workspace reference: flags %d exceeds %d",
			flags,
			workspaceReferenceFlagsMask,
		)
	}
	if reference.NameIndex > workspaceReferenceIndexMask ||
		reference.ResolvedIndex > workspaceReferenceIndexMask ||
		reference.ReceiverIndex > workspaceReferenceIndexMask {
		return reference, fmt.Errorf(
			"decode compact workspace reference: index exceeds %d",
			workspaceReferenceIndexMask,
		)
	}
	if kind > workspaceReferenceKindMask {
		return reference, fmt.Errorf(
			"decode compact workspace reference: kind %d exceeds %d",
			kind,
			workspaceReferenceKindMask,
		)
	}
	reference.QualifiedCount = uint8(qualifiedCount)
	reference.CandidateCount = uint8(candidateCount)
	reference.Kind = NameKind(kind)
	reference.TargetKind = SymbolKind(targetKind)
	reference.Flags = flags
	return reference, nil
}

// persistedWorkspaceReference is the cache-compatible reference layout. The
// retained representation above is deliberately independent of this wire
// shape so it can use compact per-document indexes.
type persistedWorkspaceReference struct {
	Name     string
	Resolved SymbolID
	Receiver types.Type
	Range    cst.TextRange

	QualifiedStart uint32
	CandidateStart uint32
	QualifiedCount uint16
	CandidateCount uint16

	Kind       NameKind
	TargetKind SymbolKind
	Flags      uint8
}

// EncodeMsgpack writes the compact, versioned reference layout. The file-local
// ScopeID is intentionally omitted because workspace resolution never consults
// it. Range coordinates are scalar fields to avoid reflection per reference.
func (reference persistedWorkspaceReference) EncodeMsgpack(
	encoder *msgpack.Encoder,
) error {
	if err := encoder.EncodeArrayLen(13); err != nil {
		return err
	}
	if err := encoder.EncodeUint8(1); err != nil {
		return err
	}
	if err := encoder.EncodeString(reference.Name); err != nil {
		return err
	}
	if err := encoder.EncodeString(string(reference.Resolved)); err != nil {
		return err
	}
	if err := encoder.Encode(reference.Receiver); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(reference.Range.Start); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(reference.Range.End); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(reference.QualifiedStart); err != nil {
		return err
	}
	if err := encoder.EncodeUint16(reference.QualifiedCount); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(reference.CandidateStart); err != nil {
		return err
	}
	if err := encoder.EncodeUint16(reference.CandidateCount); err != nil {
		return err
	}
	if err := encoder.EncodeUint8(uint8(reference.Kind)); err != nil {
		return err
	}
	if err := encoder.EncodeUint8(uint8(reference.TargetKind)); err != nil {
		return err
	}
	return encoder.EncodeUint8(reference.Flags)
}

// DecodeMsgpack accepts both the compact 13-field layout and the legacy
// 12-field layout that included a ScopeID, a reflected range, and 32-bit
// counts.
func (reference *persistedWorkspaceReference) DecodeMsgpack(
	decoder *msgpack.Decoder,
) error {
	length, err := decoder.DecodeArrayLen()
	if err != nil {
		return err
	}
	if length != 13 && length != 12 {
		return fmt.Errorf(
			"decode workspace reference: expected 12 or 13 fields, got %d",
			length,
		)
	}
	if length == 13 {
		version, err := decoder.DecodeUint8()
		if err != nil {
			return err
		}
		if version != 1 {
			return fmt.Errorf(
				"decode workspace reference: unsupported layout %d",
				version,
			)
		}
	}
	if reference.Name, err = decoder.DecodeString(); err != nil {
		return err
	}
	resolved, err := decoder.DecodeString()
	if err != nil {
		return err
	}
	reference.Resolved = SymbolID(resolved)
	if err := decoder.Decode(&reference.Receiver); err != nil {
		return err
	}
	if length == 13 {
		if reference.Range.Start, err = decoder.DecodeUint32(); err != nil {
			return err
		}
		if reference.Range.End, err = decoder.DecodeUint32(); err != nil {
			return err
		}
	} else {
		if err := decoder.Decode(&reference.Range); err != nil {
			return err
		}
	}
	if reference.QualifiedStart, err = decoder.DecodeUint32(); err != nil {
		return err
	}
	qualifiedCount, err := decoder.DecodeUint32()
	if err != nil {
		return err
	}
	if qualifiedCount > math.MaxUint16 {
		return fmt.Errorf(
			"decode workspace reference: qualified count %d exceeds %d",
			qualifiedCount,
			math.MaxUint16,
		)
	}
	reference.QualifiedCount = uint16(qualifiedCount)
	if reference.CandidateStart, err = decoder.DecodeUint32(); err != nil {
		return err
	}
	candidateCount, err := decoder.DecodeUint32()
	if err != nil {
		return err
	}
	if candidateCount > math.MaxUint16 {
		return fmt.Errorf(
			"decode workspace reference: candidate count %d exceeds %d",
			candidateCount,
			math.MaxUint16,
		)
	}
	reference.CandidateCount = uint16(candidateCount)
	if length == 12 {
		if _, err := decoder.DecodeUint32(); err != nil {
			return err
		}
	}
	kind, err := decoder.DecodeUint8()
	if err != nil {
		return err
	}
	reference.Kind = NameKind(kind)
	targetKind, err := decoder.DecodeUint8()
	if err != nil {
		return err
	}
	reference.TargetKind = SymbolKind(targetKind)
	if reference.Flags, err = decoder.DecodeUint8(); err != nil {
		return err
	}
	return nil
}

func (document *workspaceDocument) packPersistedReferences(
	references []persistedWorkspaceReference,
	qualified []string,
	candidates []SymbolID,
) error {
	if len(references) == 0 {
		return nil
	}
	valueCount := 0
	for index := range references {
		reference := &references[index]
		if !validWorkspaceSpan(
			reference.QualifiedStart,
			uint32(reference.QualifiedCount),
			len(qualified),
		) {
			return fmt.Errorf(
				"decode workspace graph: reference %d has invalid qualified span",
				index,
			)
		}
		if !validWorkspaceSpan(
			reference.CandidateStart,
			uint32(reference.CandidateCount),
			len(candidates),
		) {
			return fmt.Errorf(
				"decode workspace graph: reference %d has invalid candidate span",
				index,
			)
		}
		valueCount += int(reference.QualifiedCount) +
			int(reference.CandidateCount)
	}
	packer := newWorkspaceReferencePacker(
		document,
		len(references),
		valueCount,
	)
	for index := range references {
		source := &references[index]
		if source.QualifiedCount > math.MaxUint8 ||
			source.CandidateCount > math.MaxUint8 {
			return fmt.Errorf(
				"decode workspace graph: reference %d count exceeds %d",
				index,
				math.MaxUint8,
			)
		}
		valueStart := len(document.referenceValues)
		qualifiedStart := int(source.QualifiedStart)
		qualifiedEnd := qualifiedStart + int(source.QualifiedCount)
		for _, value := range qualified[qualifiedStart:qualifiedEnd] {
			packer.appendStringValue(value)
		}
		candidateStart := int(source.CandidateStart)
		candidateEnd := candidateStart + int(source.CandidateCount)
		for _, value := range candidates[candidateStart:candidateEnd] {
			packer.appendStringValue(string(value))
		}
		if _, compact := compactWorkspaceReferenceLocation(
			source.Range,
			uint32(valueStart),
		); !compact &&
			document.referenceExtras != nil &&
			len(document.referenceExtras.Values) >= workspaceReferenceValueMask {
			return fmt.Errorf(
				"decode workspace graph: full reference location count exceeds %d",
				workspaceReferenceValueMask,
			)
		}
		document.References[index] = document.newReference(
			source.Range,
			packer.stringIndexFor(source.Name),
			packer.stringIndexFor(string(source.Resolved)),
			uint32(valueStart),
			packer.typeIndexFor(source.Receiver),
			uint8(source.QualifiedCount),
			uint8(source.CandidateCount),
			source.Kind,
			source.TargetKind,
			source.Flags,
		)
	}
	packer.finishTables()
	return document.validateReferenceSpans()
}

const (
	workspaceReferenceStatic uint8 = 1 << iota
	workspaceReferenceWrite
)

type workspaceReferencePacker struct {
	document     *workspaceDocument
	stringIndex  map[string]uint32
	typeIndex    map[types.Type]uint32
	typeCapacity int
}

func newWorkspaceReferencePacker(
	document *workspaceDocument,
	referenceCount,
	valueCount int,
) *workspaceReferencePacker {
	stringCapacity := workspaceReferenceStringCapacity(
		referenceCount,
		valueCount,
	)
	document.References = make([]workspaceReference, referenceCount)
	document.referenceStrings = nil
	document.referenceStringIDs = nil
	document.referenceTypes = nil
	document.referenceValues = make([]uint32, 0, valueCount)
	return &workspaceReferencePacker{
		document:     document,
		stringIndex:  make(map[string]uint32, stringCapacity),
		typeCapacity: workspaceReferenceTypeCapacity(referenceCount),
	}
}

func workspaceReferenceStringCapacity(referenceCount, valueCount int) int {
	// A production graph averages 0.389 distinct strings per reference/value
	// slot. Reserve 3/8 so the temporary uniqueness map and retained string
	// table avoid most growth without carrying the former 0.5 hint's slack.
	total := referenceCount + valueCount
	return total/8*3 + (total%8*3+7)/8
}

func workspaceReferenceTypeCapacity(referenceCount int) int {
	// Only member references carry receiver types, and receivers repeat heavily
	// within one document. Allocate this table lazily so reference-only files
	// do not pay for an unused map bucket.
	return (referenceCount + 3) / 4
}

func (packer *workspaceReferencePacker) stringIndexFor(value string) uint32 {
	if value == "" {
		return 0
	}
	if index, exists := packer.stringIndex[value]; exists {
		return index
	}
	if uint64(len(packer.stringIndex)) == math.MaxUint32 {
		panic("semantic: workspace reference string table exceeds uint32")
	}
	index := uint32(len(packer.stringIndex) + 1)
	packer.stringIndex[value] = index
	return index
}

func (packer *workspaceReferencePacker) typeIndexFor(value types.Type) uint32 {
	if value == (types.Type{}) {
		return 0
	}
	if packer.typeIndex == nil {
		packer.typeIndex = make(
			map[types.Type]uint32,
			packer.typeCapacity,
		)
	}
	if index, exists := packer.typeIndex[value]; exists {
		return index
	}
	if uint64(len(packer.typeIndex)) == math.MaxUint32 {
		panic("semantic: workspace reference type table exceeds uint32")
	}
	index := uint32(len(packer.typeIndex) + 1)
	packer.typeIndex[value] = index
	return index
}

func (packer *workspaceReferencePacker) appendStringValue(
	value string,
) {
	packer.document.referenceValues = append(
		packer.document.referenceValues,
		packer.stringIndexFor(value),
	)
}

func (packer *workspaceReferencePacker) finishTables() {
	if packer == nil || packer.document == nil {
		return
	}
	if len(packer.stringIndex) != 0 {
		values := make([]string, len(packer.stringIndex))
		for value, index := range packer.stringIndex {
			values[index-1] = value
		}
		packer.document.referenceStrings = &workspaceReferenceStringTable{
			Values: values,
		}
	}
	if len(packer.typeIndex) != 0 {
		values := make([]types.Type, len(packer.typeIndex))
		for value, index := range packer.typeIndex {
			values[index-1] = value
		}
		packer.document.referenceTypes = values
	}
}
