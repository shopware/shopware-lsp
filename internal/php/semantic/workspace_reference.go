package semantic

import (
	"fmt"
	"math"
	"unicode"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/vmihailenco/msgpack/v5"
)

type workspaceReference struct {
	RangeStart       uint32
	nameAndResolved  uint32
	valueAndReceiver uint32
	rangeAndMetadata uint32
}

const (
	workspaceReferenceIndexBits = 16
	workspaceReferenceIndexMask = 1<<workspaceReferenceIndexBits - 1

	workspaceReferenceValueMask = math.MaxUint16

	workspaceReferenceRangeBits         = 12
	workspaceReferenceRangeMask         = 1<<workspaceReferenceRangeBits - 1
	workspaceReferenceFullRangeSentinel = workspaceReferenceRangeMask

	workspaceReferenceQualifiedShift = workspaceReferenceRangeBits
	workspaceReferenceCandidateShift = workspaceReferenceQualifiedShift + 4
	workspaceReferenceCountMask      = 0x0f
	workspaceReferenceKindShift      = workspaceReferenceCandidateShift + 4
	workspaceReferenceKindMask       = 0x07
	workspaceReferenceTargetShift    = workspaceReferenceKindShift + 3
	workspaceReferenceTargetMask     = 0x0f
	workspaceReferenceFlagsShift     = workspaceReferenceTargetShift + 4
	workspaceReferenceFlagsMask      = 0x0f
)

type workspaceReferenceFull struct {
	Range          cst.TextRange
	ValueStart     uint32
	NameIndex      uint32
	ResolvedIndex  uint32
	ReceiverIndex  uint32
	QualifiedCount uint8
	CandidateCount uint8
}

type workspaceReferenceExtras struct {
	Values []workspaceReferenceFull
}

func referenceBloomHash(value string) uint64 {
	if value == "" {
		return 0
	}
	start := 0
	if value[0] == '\\' || value[0] == '$' {
		start = 1
	}
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	hash := offset
	for _, current := range value[start:] {
		hash ^= uint64(unicode.ToLower(current))
		hash *= prime
	}
	return hash
}

func (document *workspaceDocument) addReferenceBloomValue(value string) {
	hash := referenceBloomHash(value)
	if document == nil || hash == 0 {
		return
	}
	document.referenceBloom[0] |= uint64(1) << (hash & 63)
	document.referenceBloom[1] |= uint64(1) << ((hash >> 17) & 63)
}

func (document *workspaceDocument) rebuildReferenceBloom() {
	if document == nil {
		return
	}
	document.referenceBloom = [2]uint64{}
	for index := 1; index <= document.referenceStringCount(); index++ {
		document.addReferenceBloomValue(
			document.referenceString(uint32(index)),
		)
	}
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
	if rng.End < rng.Start || rng.End-rng.Start >= workspaceReferenceFullRangeSentinel {
		return 0, false
	}
	return uint16(rng.End - rng.Start), true
}

func compactWorkspaceReference(
	rng cst.TextRange,
	nameIndex,
	resolvedIndex,
	valueStart,
	receiverIndex uint32,
	qualifiedCount,
	candidateCount uint8,
) (uint16, bool) {
	if nameIndex > workspaceReferenceIndexMask ||
		resolvedIndex > workspaceReferenceIndexMask ||
		receiverIndex > workspaceReferenceIndexMask ||
		qualifiedCount > workspaceReferenceCountMask ||
		candidateCount > workspaceReferenceCountMask {
		return 0, false
	}
	return compactWorkspaceReferenceLocation(rng, valueStart)
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
	if uint8(kind) > workspaceReferenceKindMask ||
		uint8(targetKind) > workspaceReferenceTargetMask ||
		flags > workspaceReferenceFlagsMask {
		panic("semantic: workspace reference metadata exceeds packed range")
	}
	metadata := uint32(kind)<<workspaceReferenceKindShift |
		uint32(targetKind)<<workspaceReferenceTargetShift |
		uint32(flags)<<workspaceReferenceFlagsShift
	result := workspaceReference{RangeStart: rng.Start}
	if fullIndex < 0 {
		rangeLength, ok := compactWorkspaceReference(
			rng,
			nameIndex,
			resolvedIndex,
			valueStart,
			receiverIndex,
			qualifiedCount,
			candidateCount,
		)
		if !ok {
			panic("semantic: workspace reference requires full fallback")
		}
		result.nameAndResolved = nameIndex |
			resolvedIndex<<workspaceReferenceIndexBits
		result.valueAndReceiver = valueStart |
			receiverIndex<<workspaceReferenceIndexBits
		result.rangeAndMetadata = uint32(rangeLength) |
			uint32(qualifiedCount)<<workspaceReferenceQualifiedShift |
			uint32(candidateCount)<<workspaceReferenceCandidateShift |
			metadata
	} else {
		index := uint32(fullIndex) + 1
		if index > workspaceReferenceValueMask {
			panic("semantic: workspace reference fallback index exceeds packed range")
		}
		result.valueAndReceiver = index
		result.rangeAndMetadata = workspaceReferenceFullRangeSentinel | metadata
	}
	return result
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
	if _, ok := compactWorkspaceReference(
		rng,
		nameIndex,
		resolvedIndex,
		valueStart,
		receiverIndex,
		qualifiedCount,
		candidateCount,
	); !ok {
		if document.referenceExtras == nil {
			document.referenceExtras = &workspaceReferenceExtras{}
		}
		fullIndex = len(document.referenceExtras.Values)
		document.referenceExtras.Values = append(
			document.referenceExtras.Values,
			workspaceReferenceFull{
				Range:          rng,
				ValueStart:     valueStart,
				NameIndex:      nameIndex,
				ResolvedIndex:  resolvedIndex,
				ReceiverIndex:  receiverIndex,
				QualifiedCount: qualifiedCount,
				CandidateCount: candidateCount,
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
	return reference.rangeAndMetadata&workspaceReferenceRangeMask ==
		workspaceReferenceFullRangeSentinel
}

func (reference *workspaceReference) fullIndex() uint32 {
	if reference == nil || !reference.hasFullLocation() {
		return 0
	}
	return reference.valueAndReceiver & workspaceReferenceValueMask
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
	length := uint16(reference.rangeAndMetadata & workspaceReferenceRangeMask)
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
		return reference.valueAndReceiver & workspaceReferenceValueMask
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
	length := uint16(reference.rangeAndMetadata & workspaceReferenceRangeMask)
	if length != workspaceReferenceFullRangeSentinel {
		return cst.TextRange{
				Start: reference.RangeStart,
				End:   reference.RangeStart + uint32(length),
			},
			reference.valueAndReceiver & workspaceReferenceValueMask
	}
	if full, ok := reference.fullLocation(document); ok {
		return full.Range, full.ValueStart
	}
	return cst.TextRange{}, 0
}

func (reference *workspaceReference) nameIndex(document *workspaceDocument) uint32 {
	if reference == nil {
		return 0
	}
	if full, ok := reference.fullLocation(document); ok {
		return full.NameIndex
	}
	return reference.nameAndResolved & workspaceReferenceIndexMask
}

func (reference *workspaceReference) resolvedIndex(document *workspaceDocument) uint32 {
	if reference == nil {
		return 0
	}
	if full, ok := reference.fullLocation(document); ok {
		return full.ResolvedIndex
	}
	return reference.nameAndResolved >> workspaceReferenceIndexBits
}

func (reference *workspaceReference) receiverIndex(document *workspaceDocument) uint32 {
	if reference == nil {
		return 0
	}
	if full, ok := reference.fullLocation(document); ok {
		return full.ReceiverIndex
	}
	return reference.valueAndReceiver >> workspaceReferenceIndexBits
}

func (reference *workspaceReference) qualifiedCount(document *workspaceDocument) uint8 {
	if reference == nil {
		return 0
	}
	if full, ok := reference.fullLocation(document); ok {
		return full.QualifiedCount
	}
	return uint8(
		reference.rangeAndMetadata >> workspaceReferenceQualifiedShift &
			workspaceReferenceCountMask,
	)
}

func (reference *workspaceReference) candidateCount(document *workspaceDocument) uint8 {
	if reference == nil {
		return 0
	}
	if full, ok := reference.fullLocation(document); ok {
		return full.CandidateCount
	}
	return uint8(
		reference.rangeAndMetadata >> workspaceReferenceCandidateShift &
			workspaceReferenceCountMask,
	)
}

func (reference *workspaceReference) kind() NameKind {
	return NameKind(
		reference.rangeAndMetadata >> workspaceReferenceKindShift &
			workspaceReferenceKindMask,
	)
}

func (reference *workspaceReference) targetKind() SymbolKind {
	return SymbolKind(
		reference.rangeAndMetadata >>
			workspaceReferenceTargetShift &
			workspaceReferenceTargetMask,
	)
}

func (reference *workspaceReference) flags() uint8 {
	return uint8(
		reference.rangeAndMetadata >>
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
	if err := encoder.EncodeUint32(reference.nameIndex(document)); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(reference.resolvedIndex(document)); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(valueStart); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(reference.receiverIndex(document)); err != nil {
		return err
	}
	if err := encoder.EncodeUint16(uint16(reference.qualifiedCount(document))); err != nil {
		return err
	}
	if err := encoder.EncodeUint16(uint16(reference.candidateCount(document))); err != nil {
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
	if _, ok := compactWorkspaceReference(
		decoded.Range,
		decoded.NameIndex,
		decoded.ResolvedIndex,
		decoded.ValueStart,
		decoded.ReceiverIndex,
		decoded.QualifiedCount,
		decoded.CandidateCount,
	); !ok {
		return fmt.Errorf(
			"decode compact workspace reference: full data requires document context",
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
		nameIndex := packer.stringIndexFor(source.Name)
		resolvedIndex := packer.stringIndexFor(string(source.Resolved))
		receiverIndex := packer.typeIndexFor(source.Receiver)
		if _, compact := compactWorkspaceReference(
			source.Range,
			nameIndex,
			resolvedIndex,
			uint32(valueStart),
			receiverIndex,
			uint8(source.QualifiedCount),
			uint8(source.CandidateCount),
		); !compact &&
			document.referenceExtras != nil &&
			len(document.referenceExtras.Values) >= workspaceReferenceValueMask {
			return fmt.Errorf(
				"decode workspace graph: reference fallback count exceeds %d",
				workspaceReferenceValueMask,
			)
		}
		document.References[index] = document.newReference(
			source.Range,
			nameIndex,
			resolvedIndex,
			uint32(valueStart),
			receiverIndex,
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
	packer.document.rebuildReferenceBloom()
}
