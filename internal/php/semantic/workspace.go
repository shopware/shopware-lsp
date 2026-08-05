package semantic

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"
)

const (
	maxWorkspaceCollectionItems = 1 << 20
	maxWorkspaceStringBytes     = 128 << 20
	workspaceStringArenaBytes   = 64 << 10
)

// workspaceDocument is the retained declaration/reference graph for a source
// file. It deliberately omits document-local analysis state and stores symbol
// metadata in optional side tables so declarations do not each pay for every
// rarely used slice in Symbol.
type workspaceDocument struct {
	Path              string
	Version           int
	WorkspaceRevision uint64
	Namespace         string
	Symbols           []workspaceSymbol
	References        []workspaceReference
	CallContracts     []CallContract

	signatures         []workspaceSignature
	signatureExtras    []workspaceSignatureExtras
	hierarchies        []workspaceHierarchy
	metadata           []workspaceMetadata
	symbolRangeExtras  *workspaceSymbolRangeExtras
	referenceExtras    *workspaceReferenceExtras
	symbolStrings      *workspaceSymbolStringTable
	referenceStrings   *workspaceReferenceStringTable
	referenceStringIDs []uint32
	referenceTypes     []types.Type
	referenceValues    []uint32
}

// WorkspaceGraph is an opaque, compact declaration/reference graph ready for
// workspace publication. Its contents are immutable after construction.
type WorkspaceGraph struct {
	document *workspaceDocument
}

// WorkspaceGraphDecoder reuses parsed immutable PHP types across a stream of
// compact workspace graphs. One decoder should be kept for a cache restore and
// cleared when that bounded phase ends.
type WorkspaceGraphDecoder struct {
	typeCache    map[string]types.Type
	stringCache  *workspaceStringInterner
	stringBuffer []byte
	stringArena  [][]byte
	arenaUsed    int

	transientStringArena [][]byte
	transientArenaChunk  int
	transientArenaUsed   int

	sharedStrings     *workspaceSharedStringTable
	sharedStringIndex map[*byte]uint32
}

func NewWorkspaceGraphDecoder() *WorkspaceGraphDecoder {
	return &WorkspaceGraphDecoder{}
}

func (decoder *WorkspaceGraphDecoder) Clear() {
	if decoder != nil {
		decoder.typeCache = nil
		decoder.stringCache = nil
		decoder.stringBuffer = nil
		decoder.stringArena = nil
		decoder.arenaUsed = 0
		decoder.transientStringArena = nil
		decoder.transientArenaChunk = 0
		decoder.transientArenaUsed = 0
		if decoder.sharedStrings != nil {
			decoder.sharedStrings.Values = slices.Clip(
				decoder.sharedStrings.Values,
			)
		}
		decoder.sharedStrings = nil
		decoder.sharedStringIndex = nil
	}
}

func (decoder *WorkspaceGraphDecoder) decodeString(
	source *msgpack.Decoder,
) (string, error) {
	if decoder == nil || decoder.stringCache == nil {
		return source.DecodeString()
	}
	length, err := source.DecodeBytesLen()
	if err != nil {
		return "", err
	}
	if length <= 0 {
		return "", nil
	}
	if length > maxWorkspaceStringBytes {
		return "", fmt.Errorf(
			"decode workspace string: length %d exceeds %d",
			length,
			maxWorkspaceStringBytes,
		)
	}
	if cap(decoder.stringBuffer) < length {
		decoder.stringBuffer = make([]byte, length)
	}
	value := decoder.stringBuffer[:length]
	if err := source.ReadFull(value); err != nil {
		return "", err
	}
	return decoder.stringCache.InternBytes(value, decoder.ownString), nil
}

func (decoder *WorkspaceGraphDecoder) ownString(value []byte) string {
	if len(value) == 0 {
		return ""
	}
	if len(decoder.stringArena) == 0 ||
		decoder.arenaUsed+len(value) >
			len(decoder.stringArena[len(decoder.stringArena)-1]) {
		chunkSize := max(workspaceStringArenaBytes, len(value))
		decoder.stringArena = append(
			decoder.stringArena,
			make([]byte, chunkSize),
		)
		decoder.arenaUsed = 0
	}
	chunk := decoder.stringArena[len(decoder.stringArena)-1]
	owned := chunk[decoder.arenaUsed : decoder.arenaUsed+len(value)]
	copy(owned, value)
	decoder.arenaUsed += len(value)
	// The arena never mutates an already returned interval. Strings retain
	// their backing chunk after the restore-scoped decoder releases its slice.
	return unsafe.String(unsafe.SliceData(owned), len(owned))
}

func (decoder *WorkspaceGraphDecoder) decodeParameterID(
	source *msgpack.Decoder,
) (string, error) {
	if decoder == nil || decoder.stringCache == nil {
		return decoder.decodeString(source)
	}
	length, err := source.DecodeBytesLen()
	if err != nil {
		return "", err
	}
	if length <= 0 {
		return "", nil
	}
	if length > maxWorkspaceStringBytes {
		return "", fmt.Errorf(
			"decode workspace parameter ID: length %d exceeds %d",
			length,
			maxWorkspaceStringBytes,
		)
	}
	if cap(decoder.stringBuffer) < length {
		decoder.stringBuffer = make([]byte, length)
	}
	value := decoder.stringBuffer[:length]
	if err := source.ReadFull(value); err != nil {
		return "", err
	}
	return decoder.ownTransientString(value), nil
}

func (decoder *WorkspaceGraphDecoder) ownTransientString(
	value []byte,
) string {
	if len(value) == 0 {
		return ""
	}
	for {
		if decoder.transientArenaChunk >=
			len(decoder.transientStringArena) {
			decoder.transientStringArena = append(
				decoder.transientStringArena,
				make([]byte, max(workspaceStringArenaBytes, len(value))),
			)
		}
		chunk := decoder.transientStringArena[decoder.transientArenaChunk]
		if decoder.transientArenaUsed+len(value) <= len(chunk) {
			owned := chunk[decoder.transientArenaUsed : decoder.transientArenaUsed+len(value)]
			copy(owned, value)
			decoder.transientArenaUsed += len(value)
			return unsafe.String(unsafe.SliceData(owned), len(owned))
		}
		decoder.transientArenaChunk++
		decoder.transientArenaUsed = 0
	}
}

func (decoder *WorkspaceGraphDecoder) resetTransientStrings() {
	if decoder == nil {
		return
	}
	decoder.transientArenaChunk = 0
	decoder.transientArenaUsed = 0
}

func (decoder *WorkspaceGraphDecoder) retainTransientString(
	value string,
) string {
	if value == "" {
		return ""
	}
	if decoder != nil && decoder.stringCache != nil {
		return decoder.stringCache.InternCopy(value, strings.Clone)
	}
	return strings.Clone(value)
}

func (decoder *WorkspaceGraphDecoder) decodeType(
	source *msgpack.Decoder,
) (types.Type, error) {
	text, err := decoder.decodeString(source)
	if err != nil {
		return types.Type{}, err
	}
	return decoder.parseTypeText(text)
}

func (decoder *WorkspaceGraphDecoder) parseTypeText(
	text string,
) (types.Type, error) {
	if decoder != nil && decoder.typeCache != nil {
		if value, exists := decoder.typeCache[text]; exists {
			return value, nil
		}
	}
	value, err := types.ParsePersisted(text)
	if err != nil {
		return types.Type{}, fmt.Errorf(
			"parse persisted PHP type %q: %w",
			text,
			err,
		)
	}
	if decoder != nil {
		if decoder.typeCache == nil {
			decoder.typeCache = make(map[string]types.Type)
		}
		decoder.typeCache[text] = value
	}
	return value, nil
}

func (decoder *WorkspaceGraphDecoder) Decode(
	source *msgpack.Decoder,
	graph *WorkspaceGraph,
) error {
	if graph == nil {
		return fmt.Errorf("decode workspace graph: nil target")
	}
	if decoder == nil {
		decoder = NewWorkspaceGraphDecoder()
	}
	return graph.decodeMsgpack(source, decoder)
}

// PackWorkspaceGraphOwned compacts a workspace-projected Document without
// cloning its nested data. The caller transfers ownership of document and
// must not mutate it after this call.
func PackWorkspaceGraphOwned(document *Document) *WorkspaceGraph {
	if document == nil {
		return nil
	}
	return &WorkspaceGraph{document: packWorkspaceDocument(document)}
}

// ProjectWorkspaceGraph builds a detached retained workspace graph directly
// from a full semantic document. File-local declarations and variable
// references are omitted. The source document remains owned by the caller and
// may be released immediately.
func ProjectWorkspaceGraph(document *Document) *WorkspaceGraph {
	graph := ProjectWorkspaceGraphBorrowed(document)
	detacher := NewWorkspaceGraphDetacher()
	detacher.DetachOwned(graph)
	detacher.Finish()
	return graph
}

// ProjectWorkspaceGraphBorrowed builds the compact workspace projection while
// borrowing immutable strings and type nodes from document. All mutable nested
// slices are cloned. The caller must keep document alive until the graph is
// encoded and must call WorkspaceGraphDetacher.DetachOwned before retaining or
// publishing it.
func ProjectWorkspaceGraphBorrowed(document *Document) *WorkspaceGraph {
	if document == nil {
		return nil
	}
	symbolCount, signatureCount, signatureExtraCount, hierarchyCount,
		metadataCount, symbolRangeExtraCount :=
		countWorkspaceSymbols(document.Symbols, true)
	packer := newWorkspaceDocumentPacker(
		document.Path,
		document.Version,
		document.WorkspaceRevision,
		document.Namespace,
		symbolCount,
		signatureCount,
		signatureExtraCount,
		hierarchyCount,
		metadataCount,
		symbolRangeExtraCount,
	)
	for index := range document.Symbols {
		source := &document.Symbols[index]
		if !isWorkspaceSymbol(source.Kind) {
			continue
		}
		borrowedSource := *source
		borrowedSource.Parameters = nil
		borrowed := cloneSymbol(borrowedSource)
		borrowed.Parameters = source.Parameters
		packer.addSymbol(&borrowed)
	}
	packer.document.packProjectedReferences(document.References)
	packer.document.CallContracts = cloneCallContracts(document.CallContracts)
	return &WorkspaceGraph{document: packer.document}
}

// WorkspaceGraphDetacher canonicalizes borrowed workspace projections. Reuse
// one detacher for a scanner batch so equal values are copied once across all
// committed files. A detacher is not safe for concurrent use.
type WorkspaceGraphDetacher struct {
	strings      *workspaceStringInterner
	types        map[string]types.Type
	typeDetacher *types.Detacher
	stringArena  [][]byte
	arenaUsed    int

	sharedStrings         *workspaceSharedStringTable
	sharedStringIndex     map[*byte]uint32
	shareSymbolStrings    bool
	shareReferenceStrings bool
}

func NewWorkspaceGraphDetacher() *WorkspaceGraphDetacher {
	return NewWorkspaceGraphDetacherCapacity(0)
}

// NewWorkspaceGraphDetacherCapacity constructs a batch detacher with room for
// the expected number of distinct strings. The value is an allocation hint;
// underestimates grow normally and overestimates do not affect semantics.
func NewWorkspaceGraphDetacherCapacity(
	expectedStrings int,
) *WorkspaceGraphDetacher {
	detacher := &WorkspaceGraphDetacher{
		strings:               newWorkspaceStringInterner(expectedStrings),
		types:                 make(map[string]types.Type),
		shareSymbolStrings:    expectedStrings > 0,
		shareReferenceStrings: expectedStrings > 0,
	}
	detacher.typeDetacher = types.NewDetacher(detacher.internString)
	return detacher
}

// DetachOwned replaces every borrowed value in graph with storage owned by the
// detacher. The graph and its source document must not be mutated concurrently.
func (detacher *WorkspaceGraphDetacher) DetachOwned(graph *WorkspaceGraph) {
	if detacher == nil || graph == nil || graph.document == nil {
		return
	}
	internPackedWorkspaceGraphOwned(
		graph.document,
		detacher.internString,
		detacher.internType,
	)
	if detacher.shareSymbolStrings {
		shareWorkspaceSymbolStrings(
			graph.document,
			&detacher.sharedStrings,
			&detacher.sharedStringIndex,
		)
	}
	if detacher.shareReferenceStrings {
		shareWorkspaceReferenceStrings(
			graph.document,
			&detacher.sharedStrings,
			&detacher.sharedStringIndex,
		)
	}
}

// Finish releases construction-only indexes and trims the shared immutable
// symbol/reference string table after a batch has published all documents.
func (detacher *WorkspaceGraphDetacher) Finish() {
	if detacher == nil {
		return
	}
	if detacher.sharedStrings != nil {
		detacher.sharedStrings.Values = slices.Clip(
			detacher.sharedStrings.Values,
		)
	}
	detacher.sharedStringIndex = nil
}

func (detacher *WorkspaceGraphDetacher) internString(value string) string {
	if value == "" {
		return ""
	}
	return detacher.strings.InternCopy(value, detacher.ownString)
}

func (detacher *WorkspaceGraphDetacher) ownString(value string) string {
	if len(value) == 0 {
		return ""
	}
	if len(detacher.stringArena) == 0 ||
		detacher.arenaUsed+len(value) >
			len(detacher.stringArena[len(detacher.stringArena)-1]) {
		chunkSize := max(workspaceStringArenaBytes, len(value))
		detacher.stringArena = append(
			detacher.stringArena,
			make([]byte, chunkSize),
		)
		detacher.arenaUsed = 0
	}
	chunk := detacher.stringArena[len(detacher.stringArena)-1]
	owned := chunk[detacher.arenaUsed : detacher.arenaUsed+len(value)]
	copy(owned, value)
	detacher.arenaUsed += len(value)
	// The arena never mutates an already returned interval. Strings retain
	// their backing chunk after this batch-scoped detacher is released.
	return unsafe.String(unsafe.SliceData(owned), len(owned))
}

func (detacher *WorkspaceGraphDetacher) internType(value types.Type) types.Type {
	if value == (types.Type{}) {
		return value
	}
	key := value.Key()
	if existing, ok := detacher.types[key]; ok {
		return existing
	}
	owned := detacher.typeDetacher.Type(value)
	detacher.types[key] = owned
	return owned
}

// Path returns the source path represented by this graph.
func (graph *WorkspaceGraph) Path() string {
	if graph == nil || graph.document == nil {
		return ""
	}
	return graph.document.Path
}

// Document materializes a defensive public document view of the graph.
func (graph *WorkspaceGraph) Document() *Document {
	if graph == nil || graph.document == nil {
		return nil
	}
	document := graph.document.materialize()
	document.Symbols = cloneSymbols(document.Symbols)
	document.References = cloneReferences(document.References)
	return document
}

// VisitSymbolViews visits the compact declarations retained by this graph
// without materializing full Symbol values or allocating a Snapshot.
func (graph *WorkspaceGraph) VisitSymbolViews(
	visit func(SymbolView) bool,
) bool {
	if graph == nil || graph.document == nil || visit == nil {
		return true
	}
	for index := range graph.document.Symbols {
		if !visit(workspaceView(&graph.document.Symbols[index])) {
			return false
		}
	}
	return true
}

// EncodeMsgpack writes the compact graph without expanding public Symbols.
func (graph *WorkspaceGraph) EncodeMsgpack(encoder *msgpack.Encoder) error {
	if graph == nil || graph.document == nil {
		return encoder.EncodeNil()
	}
	document := graph.document
	if err := encoder.EncodeArrayLen(10); err != nil {
		return err
	}
	if err := encoder.EncodeString(document.Path); err != nil {
		return err
	}
	if err := encoder.EncodeInt(int64(document.Version)); err != nil {
		return err
	}
	if err := encoder.EncodeUint64(document.WorkspaceRevision); err != nil {
		return err
	}
	if err := encoder.EncodeString(document.Namespace); err != nil {
		return err
	}
	if err := encodeWorkspaceSymbols(encoder, document.Symbols); err != nil {
		return err
	}
	if err := encodeWorkspaceReferences(encoder, document); err != nil {
		return err
	}
	if err := encodeWorkspaceReferenceStrings(encoder, document); err != nil {
		return err
	}
	if err := encodeWorkspaceTypes(encoder, document.referenceTypes); err != nil {
		return err
	}
	if err := encoder.Encode(document.referenceValues); err != nil {
		return err
	}
	return encoder.Encode(document.CallContracts)
}

func encodeWorkspaceSymbols(
	encoder *msgpack.Encoder,
	symbols []workspaceSymbol,
) error {
	if symbols == nil {
		return encoder.EncodeNil()
	}
	if err := encoder.EncodeArrayLen(len(symbols)); err != nil {
		return err
	}
	for index := range symbols {
		if err := encodeWorkspaceSymbol(encoder, &symbols[index]); err != nil {
			return err
		}
	}
	return nil
}

func encodeWorkspaceSymbol(
	encoder *msgpack.Encoder,
	symbol *workspaceSymbol,
) error {
	if symbol == nil {
		return encoder.EncodeNil()
	}
	if err := encoder.EncodeArrayLen(20); err != nil {
		return err
	}
	if err := encoder.EncodeString(string(symbol.ID)); err != nil {
		return err
	}
	if err := encoder.EncodeString(symbol.name()); err != nil {
		return err
	}
	if err := encoder.EncodeString(symbol.fullyQualified()); err != nil {
		return err
	}
	if err := encoder.EncodeString(string(symbol.container())); err != nil {
		return err
	}
	if err := encoder.EncodeString(symbol.path()); err != nil {
		return err
	}
	if err := symbol.Type.EncodeMsgpack(encoder); err != nil {
		return err
	}
	if err := symbol.NativeType.EncodeMsgpack(encoder); err != nil {
		return err
	}
	if err := symbol.DocType.EncodeMsgpack(encoder); err != nil {
		return err
	}
	if err := symbol.ReturnType.EncodeMsgpack(encoder); err != nil {
		return err
	}
	ranges := symbol.ranges()
	if err := encodeTextRange(encoder, ranges.Range); err != nil {
		return err
	}
	if err := encodeTextRange(encoder, ranges.SelectionRange); err != nil {
		return err
	}
	if err := encodeTextRange(encoder, ranges.BodyRange); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(uint32(symbol.flags())); err != nil {
		return err
	}
	if err := encoder.EncodeUint8(uint8(symbol.Kind)); err != nil {
		return err
	}
	if err := encoder.EncodeUint8(uint8(symbol.Visibility)); err != nil {
		return err
	}
	if err := encoder.EncodeUint8(uint8(symbol.WriteVisibility)); err != nil {
		return err
	}
	if err := encoder.EncodeBool(symbol.HasWriteVisibility); err != nil {
		return err
	}
	if err := encodeWorkspaceSignature(
		encoder,
		symbol.signature(),
	); err != nil {
		return err
	}
	if err := encodeWorkspaceHierarchy(encoder, symbol.hierarchy()); err != nil {
		return err
	}
	return encodeWorkspaceMetadata(encoder, symbol.metadata())
}

func encodeTextRange(
	encoder *msgpack.Encoder,
	value cst.TextRange,
) error {
	if err := encoder.EncodeMapLen(2); err != nil {
		return err
	}
	if err := encoder.EncodeString("Start"); err != nil {
		return err
	}
	if err := encoder.EncodeUint32(value.Start); err != nil {
		return err
	}
	if err := encoder.EncodeString("End"); err != nil {
		return err
	}
	return encoder.EncodeUint32(value.End)
}

func encodeWorkspaceSignature(
	encoder *msgpack.Encoder,
	signature *workspaceSignature,
) error {
	if signature == nil {
		return encoder.EncodeNil()
	}
	if err := encoder.EncodeArrayLen(6); err != nil {
		return err
	}
	if err := encodeWorkspaceParameters(
		encoder,
		signature.Parameters,
	); err != nil {
		return err
	}
	if err := encoder.Encode(signature.templates()); err != nil {
		return err
	}
	if err := encodeWorkspaceTypes(encoder, signature.throws()); err != nil {
		return err
	}
	if err := encoder.Encode(signature.literalReturns()); err != nil {
		return err
	}
	if err := encoder.Encode(signature.constantReturns()); err != nil {
		return err
	}
	return encoder.Encode(signature.assertions())
}

func encodeWorkspaceHierarchy(
	encoder *msgpack.Encoder,
	hierarchy *workspaceHierarchy,
) error {
	if hierarchy == nil {
		return encoder.EncodeNil()
	}
	if err := encoder.EncodeArrayLen(7); err != nil {
		return err
	}
	if err := encoder.Encode(hierarchy.extends()); err != nil {
		return err
	}
	if err := encoder.Encode(hierarchy.implements()); err != nil {
		return err
	}
	if err := encoder.Encode(hierarchy.traits()); err != nil {
		return err
	}
	if err := encodeWorkspaceTypes(
		encoder,
		hierarchy.extendsTypes(),
	); err != nil {
		return err
	}
	if err := encodeWorkspaceTypes(
		encoder,
		hierarchy.implementsTypes(),
	); err != nil {
		return err
	}
	if err := encodeWorkspaceTypes(encoder, hierarchy.traitTypes()); err != nil {
		return err
	}
	return encoder.Encode(hierarchy.aliases())
}

func encodeWorkspaceMetadata(
	encoder *msgpack.Encoder,
	metadata *workspaceMetadata,
) error {
	if metadata == nil {
		return encoder.EncodeNil()
	}
	if err := encoder.EncodeArrayLen(3); err != nil {
		return err
	}
	if err := encoder.Encode(metadata.attributes()); err != nil {
		return err
	}
	if err := encoder.Encode(metadata.constantArray()); err != nil {
		return err
	}
	return encoder.EncodeString(metadata.DocSummary)
}

func encodeWorkspaceReferences(
	encoder *msgpack.Encoder,
	document *workspaceDocument,
) error {
	var references []workspaceReference
	if document != nil {
		references = document.References
	}
	if references == nil {
		return encoder.EncodeNil()
	}
	if err := encoder.EncodeArrayLen(len(references)); err != nil {
		return err
	}
	for index := range references {
		if err := encodeWorkspaceReference(
			encoder,
			&references[index],
			document,
		); err != nil {
			return err
		}
	}
	return nil
}

func encodeWorkspaceReferenceStrings(
	encoder *msgpack.Encoder,
	document *workspaceDocument,
) error {
	if document == nil || document.referenceStrings == nil {
		return encoder.EncodeNil()
	}
	count := document.referenceStringCount()
	if err := encoder.EncodeArrayLen(count); err != nil {
		return err
	}
	for index := 1; index <= count; index++ {
		if err := encoder.EncodeString(
			document.referenceString(uint32(index)),
		); err != nil {
			return err
		}
	}
	return nil
}

func decodeWorkspaceReferences(
	decoder *msgpack.Decoder,
	document *workspaceDocument,
) error {
	length, err := decodeWorkspaceCollectionLen(decoder, "references")
	if err != nil {
		return err
	}
	if length < 0 {
		return nil
	}
	document.References = make([]workspaceReference, length)
	for index := range document.References {
		reference, err := decodeWorkspaceReferenceFields(decoder)
		if err != nil {
			return err
		}
		if _, compact := compactWorkspaceReferenceLocation(
			reference.Range,
			reference.ValueStart,
		); !compact &&
			document.referenceExtras != nil &&
			len(document.referenceExtras.Values) >= workspaceReferenceValueMask {
			return fmt.Errorf(
				"decode compact workspace reference: full location count exceeds %d",
				workspaceReferenceValueMask,
			)
		}
		document.References[index] = document.newReference(
			reference.Range,
			reference.NameIndex,
			reference.ResolvedIndex,
			reference.ValueStart,
			reference.ReceiverIndex,
			reference.QualifiedCount,
			reference.CandidateCount,
			reference.Kind,
			reference.TargetKind,
			reference.Flags,
		)
	}
	return nil
}

func decodeWorkspaceUint32s(
	decoder *msgpack.Decoder,
) ([]uint32, error) {
	length, err := decodeWorkspaceCollectionLen(decoder, "reference values")
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, nil
	}
	values := make([]uint32, length)
	for index := range values {
		if values[index], err = decoder.DecodeUint32(); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func encodeWorkspaceTypes(
	encoder *msgpack.Encoder,
	values []types.Type,
) error {
	if values == nil {
		return encoder.EncodeNil()
	}
	if err := encoder.EncodeArrayLen(len(values)); err != nil {
		return err
	}
	for _, value := range values {
		if err := value.EncodeMsgpack(encoder); err != nil {
			return err
		}
	}
	return nil
}

func decodeWorkspaceTypes(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) ([]types.Type, error) {
	length, err := decodeWorkspaceCollectionLen(decoder, "types")
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, nil
	}
	values := make([]types.Type, length)
	for index := range values {
		if values[index], err = context.decodeType(decoder); err != nil {
			return nil, err
		}
	}
	return values, nil
}

// DecodeMsgpack restores the compact graph directly.
func (graph *WorkspaceGraph) DecodeMsgpack(decoder *msgpack.Decoder) error {
	context := NewWorkspaceGraphDecoder()
	defer context.Clear()
	return context.Decode(decoder, graph)
}

func (graph *WorkspaceGraph) decodeMsgpack(
	decoder *msgpack.Decoder,
	context *WorkspaceGraphDecoder,
) error {
	length, err := decoder.DecodeArrayLen()
	if err != nil {
		return err
	}
	if length != 8 && length != 9 && length != 10 {
		return fmt.Errorf(
			"decode workspace graph: expected 8, 9, or 10 fields, got %d",
			length,
		)
	}
	document := &workspaceDocument{}
	if document.Path, err = context.decodeString(decoder); err != nil {
		return err
	}
	version, err := decoder.DecodeInt64()
	if err != nil {
		return err
	}
	document.Version = int(version)
	if document.WorkspaceRevision, err = decoder.DecodeUint64(); err != nil {
		return err
	}
	if document.Namespace, err = context.decodeString(decoder); err != nil {
		return err
	}
	if err = decodeWorkspaceSymbols(
		decoder,
		context,
		document,
	); err != nil {
		return err
	}
	if length >= 9 {
		if err = decodeWorkspaceReferences(decoder, document); err != nil {
			return err
		}
		var referenceStrings []string
		if referenceStrings, err =
			decodeWorkspaceStrings(decoder, context); err != nil {
			return err
		}
		if referenceStrings != nil {
			document.referenceStrings = &workspaceReferenceStringTable{
				Values: referenceStrings,
			}
		}
		if document.referenceTypes, err =
			decodeWorkspaceTypes(decoder, context); err != nil {
			return err
		}
		if document.referenceValues, err =
			decodeWorkspaceUint32s(decoder); err != nil {
			return err
		}
		if err := document.validateReferenceSpans(); err != nil {
			return err
		}
	} else {
		var references []persistedWorkspaceReference
		if err := decoder.Decode(&references); err != nil {
			return err
		}
		var qualified []string
		if err := decoder.Decode(&qualified); err != nil {
			return err
		}
		var candidates []SymbolID
		if err := decoder.Decode(&candidates); err != nil {
			return err
		}
		if err := document.packPersistedReferences(
			references,
			qualified,
			candidates,
		); err != nil {
			return err
		}
	}
	if length == 10 {
		var contracts []CallContract
		if err := decoder.Decode(&contracts); err != nil {
			return fmt.Errorf("decode workspace call contracts: %w", err)
		}
		document.CallContracts = cloneCallContracts(contracts)
	}
	if context.stringCache != nil {
		shareWorkspaceSymbolStrings(
			document,
			&context.sharedStrings,
			&context.sharedStringIndex,
		)
		shareWorkspaceReferenceStrings(
			document,
			&context.sharedStrings,
			&context.sharedStringIndex,
		)
	}
	graph.document = document
	return nil
}

type workspaceSymbol struct {
	_msgpack struct{} `msgpack:",as_array"` //nolint:unused // Encoding layout marker.

	ID                  SymbolID
	Document            *workspaceDocument
	nameIndex           uint32
	fullyQualifiedIndex uint32
	containerIndex      uint32

	Type       types.Type
	NativeType types.Type
	DocType    types.Type
	ReturnType types.Type

	Ranges             workspaceSymbolRanges
	flagsAndRangeIndex uint32

	Kind               SymbolKind
	Visibility         Visibility
	WriteVisibility    Visibility
	HasWriteVisibility bool

	sideIndexes uint64
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
			if len((*shared).Values) == math.MaxUint32 {
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

	workspaceSymbolRangeIndexShift = 12
	workspaceSymbolFlagsMask       = 1<<workspaceSymbolRangeIndexShift - 1
	workspaceSymbolRangeIndexMask  = 1<<(32-workspaceSymbolRangeIndexShift) - 1

	workspaceSymbolSideIndexBits = 21
	workspaceSymbolSideIndexMask = 1<<workspaceSymbolSideIndexBits - 1

	workspaceSymbolSignatureShift = 0
	workspaceSymbolHierarchyShift = workspaceSymbolSideIndexBits
	workspaceSymbolMetadataShift  = workspaceSymbolSideIndexBits * 2
)

type workspaceSymbolRanges struct {
	Start  uint32
	Deltas [5]uint16
}

type workspaceSymbolFullRanges struct {
	Range          cst.TextRange
	SelectionRange cst.TextRange
	BodyRange      cst.TextRange
}

type workspaceSymbolRangeExtras struct {
	Values []workspaceSymbolFullRanges
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
		Start: rng.Start,
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
	return workspaceSymbolFullRanges{
		Range: cst.TextRange{
			Start: ranges.Start,
			End:   ranges.Start + uint32(ranges.Deltas[0]),
		},
		SelectionRange: materializeWorkspaceSymbolSubrange(
			ranges.Start,
			ranges.Deltas[1],
			ranges.Deltas[2],
		),
		BodyRange: materializeWorkspaceSymbolSubrange(
			ranges.Start,
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
		return cst.TextRange{
			Start: symbol.Ranges.Start,
			End: symbol.Ranges.Start +
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

func (symbol *workspaceSymbol) sideIndex(shift int) uint64 {
	if symbol == nil {
		return 0
	}
	return symbol.sideIndexes >> shift & workspaceSymbolSideIndexMask
}

func (symbol *workspaceSymbol) setSideIndexes(
	signature,
	hierarchy,
	metadata int,
) {
	if symbol == nil {
		return
	}
	symbol.sideIndexes =
		packWorkspaceSymbolSideIndex(
			signature,
		)<<workspaceSymbolSignatureShift |
			packWorkspaceSymbolSideIndex(
				hierarchy,
			)<<workspaceSymbolHierarchyShift |
			packWorkspaceSymbolSideIndex(
				metadata,
			)<<workspaceSymbolMetadataShift
}

func packWorkspaceSymbolSideIndex(index int) uint64 {
	if index < 0 {
		return 0
	}
	value := uint64(index) + 1
	if value > workspaceSymbolSideIndexMask {
		panic("semantic: workspace symbol side-table index exceeds packed range")
	}
	return value
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
	if symbol.Type, err = context.decodeType(decoder); err != nil {
		return err
	}
	if symbol.NativeType, err = context.decodeType(decoder); err != nil {
		return err
	}
	if symbol.DocType, err = context.decodeType(decoder); err != nil {
		return err
	}
	if symbol.ReturnType, err = context.decodeType(decoder); err != nil {
		return err
	}
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
	symbol.Kind = SymbolKind(kind)
	visibility, err := decoder.DecodeUint8()
	if err != nil {
		return err
	}
	symbol.Visibility = Visibility(visibility)
	writeVisibility, err := decoder.DecodeUint8()
	if err != nil {
		return err
	}
	symbol.WriteVisibility = Visibility(writeVisibility)
	if symbol.HasWriteVisibility, err = decoder.DecodeBool(); err != nil {
		return err
	}
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

type workspaceSignature struct {
	Parameters []workspaceParameter
	Extras     *workspaceSignatureExtras
}

type workspaceParameter struct {
	Name            string
	NativeType      types.Type
	DocType         types.Type
	Extras          *workspaceParameterExtras
	Ranges          workspaceParameterRanges
	Flags           Flags
	EffectiveSource uint8
	Optional        bool
}

const (
	workspaceParameterNativeType uint8 = iota
	workspaceParameterDocType
	workspaceParameterExplicitType
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
	Start  uint32
	Deltas [5]uint16
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
		Start: rng.Start,
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
	rng = cst.TextRange{
		Start: parameter.Ranges.Start,
		End:   parameter.Ranges.Start + uint32(parameter.Ranges.Deltas[0]),
	}
	selectionRange = cst.TextRange{
		Start: parameter.Ranges.Start + uint32(parameter.Ranges.Deltas[1]),
	}
	selectionRange.End =
		selectionRange.Start + uint32(parameter.Ranges.Deltas[2])
	if parameter.Ranges.Deltas[3] !=
		workspaceParameterMissingDefaultRange {
		defaultRange.Start =
			parameter.Ranges.Start + uint32(parameter.Ranges.Deltas[3])
		defaultRange.End =
			defaultRange.Start + uint32(parameter.Ranges.Deltas[4])
	}
	return
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
		Flags:      source.Flags,
		Optional:   source.Optional,
	}
	switch {
	case source.Type.Equal(source.NativeType):
	case source.Type.Equal(source.DocType):
		result.EffectiveSource = workspaceParameterDocType
	default:
		result.EffectiveSource = workspaceParameterExplicitType
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
	switch parameter.EffectiveSource {
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
	if err := encoder.EncodeUint32(uint32(parameter.Flags)); err != nil {
		return err
	}
	return encoder.EncodeBool(parameter.Optional)
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
	if msgpcode.IsFixedMap(code) ||
		code == msgpcode.Map16 ||
		code == msgpcode.Map32 {
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
	length, err := decoder.DecodeArrayLen()
	if err != nil {
		return err
	}
	if length != 15 && length != 16 && length != 17 {
		return fmt.Errorf(
			"decode workspace parameter: expected 15, 16, or 17 fields, got %d",
			length,
		)
	}
	version, err := decoder.DecodeUint8()
	if err != nil {
		return err
	}
	if version != 1 && version != 2 && version != 3 && version != 4 {
		return fmt.Errorf(
			"decode workspace parameter: unsupported layout %d",
			version,
		)
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
	var attributes []Attribute
	if version >= 3 {
		expectedLength := 16
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
		attributes, err = decodeWorkspaceAttributes(decoder, context)
		if err != nil {
			return err
		}
	} else if length != 15 {
		return fmt.Errorf(
			"decode workspace parameter: layout %d requires 15 fields",
			version,
		)
	}
	var defaultValue *AttributeValue
	if version >= 4 {
		code, peekErr := decoder.PeekCode()
		if peekErr != nil {
			return peekErr
		}
		if code == msgpcode.Nil {
			if err = decoder.DecodeNil(); err != nil {
				return err
			}
		} else {
			value, decodeErr := decodeWorkspaceAttributeValue(
				decoder,
				context,
				0,
			)
			if decodeErr != nil {
				return decodeErr
			}
			defaultValue = &value
		}
	}
	var rng cst.TextRange
	if rng.Start, err = decoder.DecodeUint32(); err != nil {
		return err
	}
	if rng.End, err = decoder.DecodeUint32(); err != nil {
		return err
	}
	var selectionRange cst.TextRange
	if selectionRange.Start, err = decoder.DecodeUint32(); err != nil {
		return err
	}
	if selectionRange.End, err = decoder.DecodeUint32(); err != nil {
		return err
	}
	var defaultRange cst.TextRange
	if defaultRange.Start, err = decoder.DecodeUint32(); err != nil {
		return err
	}
	if defaultRange.End, err = decoder.DecodeUint32(); err != nil {
		return err
	}
	flags, err := decoder.DecodeUint32()
	if err != nil {
		return err
	}
	parameter.Flags = Flags(flags)
	if parameter.Optional, err = decoder.DecodeBool(); err != nil {
		return err
	}
	parameter.setEffectiveType(effectiveType)
	if assistantTags != nil {
		if parameter.Extras == nil {
			parameter.Extras = &workspaceParameterExtras{}
		}
		if parameter.Extras.Metadata == nil {
			parameter.Extras.Metadata = &workspaceParameterMetadata{}
		}
		parameter.Extras.Metadata.AssistantTags = assistantTags
	}
	if attributes != nil {
		if parameter.Extras == nil {
			parameter.Extras = &workspaceParameterExtras{}
		}
		if parameter.Extras.Metadata == nil {
			parameter.Extras.Metadata = &workspaceParameterMetadata{}
		}
		parameter.Extras.Metadata.Attributes = attributes
	}
	if defaultValue != nil {
		if parameter.Extras == nil {
			parameter.Extras = &workspaceParameterExtras{}
		}
		if parameter.Extras.Metadata == nil {
			parameter.Extras.Metadata = &workspaceParameterMetadata{}
		}
		parameter.Extras.Metadata.DefaultValue = defaultValue
	}
	parameter.setRanges(rng, selectionRange, defaultRange)
	return nil
}

func (parameter *workspaceParameter) setEffectiveType(value types.Type) {
	parameter.EffectiveSource = workspaceParameterNativeType
	switch {
	case value.Equal(parameter.NativeType):
	case value.Equal(parameter.DocType):
		parameter.EffectiveSource = workspaceParameterDocType
	default:
		parameter.EffectiveSource = workspaceParameterExplicitType
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
		Flags:          parameter.Flags,
		Optional:       parameter.Optional,
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
			if len((*shared).Values) == math.MaxUint32 {
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
	if len(packer.stringIndex) == math.MaxUint32 {
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
	if len(packer.typeIndex) == math.MaxUint32 {
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

type workspaceDocumentPacker struct {
	document              *workspaceDocument
	symbolStrings         workspaceSymbolStringBuilder
	symbolIndex           int
	symbolRangeExtraIndex int
	signatureIndex        int
	signatureExtraIndex   int
	hierarchyIndex        int
	metadataIndex         int
}

const workspaceSymbolStringLinearLimit = 16

type workspaceSymbolStringBuilder struct {
	document       *workspaceDocument
	index          map[string]uint32
	canonicalIndex map[*byte]uint32
	capacity       int
}

func newWorkspaceSymbolStringBuilder(
	document *workspaceDocument,
	symbolCount int,
) workspaceSymbolStringBuilder {
	return workspaceSymbolStringBuilder{
		document: document,
		capacity: min(symbolCount*2, 64),
	}
}

func (decoder *WorkspaceGraphDecoder) newWorkspaceSymbolStringBuilder(
	document *workspaceDocument,
	symbolCount int,
) workspaceSymbolStringBuilder {
	if decoder == nil || decoder.stringCache == nil {
		return newWorkspaceSymbolStringBuilder(document, symbolCount)
	}
	if decoder.sharedStrings == nil {
		decoder.sharedStrings = &workspaceSharedStringTable{Shared: true}
	} else {
		decoder.sharedStrings.Shared = true
	}
	if decoder.sharedStringIndex == nil {
		decoder.sharedStringIndex = make(map[*byte]uint32)
	}
	document.symbolStrings = decoder.sharedStrings
	return workspaceSymbolStringBuilder{
		document:       document,
		canonicalIndex: decoder.sharedStringIndex,
	}
}

func (builder *workspaceSymbolStringBuilder) indexFor(value string) uint32 {
	if builder == nil || builder.document == nil || value == "" {
		return 0
	}
	if builder.canonicalIndex != nil {
		if index, exists :=
			builder.canonicalIndex[unsafe.StringData(value)]; exists {
			return index
		}
	}
	if builder.index != nil {
		if index, exists := builder.index[value]; exists {
			return index
		}
	}
	if builder.document.symbolStrings == nil {
		builder.document.symbolStrings = &workspaceSymbolStringTable{
			Values: make([]string, 0, builder.capacity),
		}
	}
	values := builder.document.symbolStrings.Values
	if builder.index == nil && builder.canonicalIndex == nil {
		for index, existing := range values {
			if existing == value {
				return uint32(index + 1)
			}
		}
	}
	if len(values) == math.MaxUint32 {
		panic("semantic: workspace symbol string table exceeds uint32")
	}
	index := uint32(len(values) + 1)
	builder.document.symbolStrings.Values = append(values, value)
	if builder.canonicalIndex != nil {
		builder.canonicalIndex[unsafe.StringData(value)] = index
	} else if builder.index != nil {
		builder.index[value] = index
	} else if len(values)+1 > workspaceSymbolStringLinearLimit {
		builder.index = make(
			map[string]uint32,
			len(builder.document.symbolStrings.Values),
		)
		for valueIndex, existing := range builder.document.symbolStrings.Values {
			builder.index[existing] = uint32(valueIndex + 1)
		}
	}
	return index
}

func countWorkspaceSymbols(
	symbols []Symbol,
	workspaceOnly bool,
) (
	symbolCount,
	signatureCount,
	signatureExtraCount,
	hierarchyCount,
	metadataCount,
	symbolRangeExtraCount int,
) {
	for index := range symbols {
		symbol := &symbols[index]
		if workspaceOnly && !isWorkspaceSymbol(symbol.Kind) {
			continue
		}
		symbolCount++
		if hasWorkspaceSignature(symbol) {
			signatureCount++
			if hasWorkspaceSignatureExtras(symbol) {
				signatureExtraCount++
			}
		}
		if hasWorkspaceHierarchy(symbol) {
			hierarchyCount++
		}
		if hasWorkspaceMetadata(symbol) {
			metadataCount++
		}
		if _, ok := compactWorkspaceSymbolRanges(
			symbol.Range,
			symbol.SelectionRange,
			symbol.BodyRange,
		); !ok {
			symbolRangeExtraCount++
		}
	}
	return
}

func newWorkspaceDocumentPacker(
	path string,
	version int,
	revision uint64,
	namespace string,
	symbolCount,
	signatureCount,
	signatureExtraCount,
	hierarchyCount,
	metadataCount,
	symbolRangeExtraCount int,
) *workspaceDocumentPacker {
	packer := &workspaceDocumentPacker{
		document: &workspaceDocument{
			Path:              path,
			Version:           version,
			WorkspaceRevision: revision,
			Namespace:         namespace,
			Symbols:           make([]workspaceSymbol, symbolCount),
			signatures:        make([]workspaceSignature, signatureCount),
			signatureExtras: make(
				[]workspaceSignatureExtras,
				signatureExtraCount,
			),
			hierarchies: make([]workspaceHierarchy, hierarchyCount),
			metadata:    make([]workspaceMetadata, metadataCount),
		},
	}
	if symbolRangeExtraCount != 0 {
		packer.document.symbolRangeExtras = &workspaceSymbolRangeExtras{
			Values: make(
				[]workspaceSymbolFullRanges,
				symbolRangeExtraCount,
			),
		}
	}
	packer.symbolStrings = newWorkspaceSymbolStringBuilder(
		packer.document,
		symbolCount,
	)
	return packer
}

func (packer *workspaceDocumentPacker) addSymbol(source *Symbol) {
	if packer == nil || packer.document == nil || source == nil {
		return
	}
	target := &packer.document.Symbols[packer.symbolIndex]
	packer.symbolIndex++
	*target = workspaceSymbol{
		ID:                  source.ID,
		Kind:                source.Kind,
		Document:            packer.document,
		nameIndex:           packer.symbolStrings.indexFor(source.Name),
		fullyQualifiedIndex: packer.symbolStrings.indexFor(source.FullyQualified),
		containerIndex: packer.symbolStrings.indexFor(
			string(source.Container),
		),
		Visibility:         source.Visibility,
		WriteVisibility:    source.WriteVisibility,
		HasWriteVisibility: source.HasWriteVisibility,
		Type:               source.Type,
		NativeType:         source.NativeType,
		DocType:            source.DocType,
		ReturnType:         source.ReturnType,
	}
	rangeIndex := -1
	if compact, ok := compactWorkspaceSymbolRanges(
		source.Range,
		source.SelectionRange,
		source.BodyRange,
	); ok {
		target.Ranges = compact
	} else {
		rangeIndex = packer.symbolRangeExtraIndex
		packer.document.symbolRangeExtras.Values[rangeIndex] =
			workspaceSymbolFullRanges{
				Range:          source.Range,
				SelectionRange: source.SelectionRange,
				BodyRange:      source.BodyRange,
			}
		packer.symbolRangeExtraIndex++
	}
	target.setFlagsAndRangeIndex(source.Flags, rangeIndex)
	signatureIndex := -1
	hierarchyIndex := -1
	metadataIndex := -1
	if hasWorkspaceSignature(source) {
		signatureIndex = packer.signatureIndex
		signature := &packer.document.signatures[signatureIndex]
		packer.signatureIndex++
		signature.Parameters = packWorkspaceParametersForSymbol(
			source.Parameters,
			target,
		)
		if hasWorkspaceSignatureExtras(source) {
			signature.Extras =
				&packer.document.signatureExtras[packer.signatureExtraIndex]
			packer.signatureExtraIndex++
			*signature.Extras = newWorkspaceSignatureExtras(
				source.Templates,
				source.Throws,
				source.LiteralReturns,
				source.ConstantReturns,
				source.Assertions,
			)
		}
	}
	if hasWorkspaceHierarchy(source) {
		hierarchyIndex = packer.hierarchyIndex
		packer.hierarchyIndex++
		packer.document.hierarchies[hierarchyIndex] = newWorkspaceHierarchy(
			source.Extends,
			source.Implements,
			source.Traits,
			source.ExtendsTypes,
			source.ImplementsTypes,
			source.TraitTypes,
			source.TraitAliases,
		)
	}
	if hasWorkspaceMetadata(source) {
		metadataIndex = packer.metadataIndex
		packer.metadataIndex++
		packer.document.metadata[metadataIndex] = newWorkspaceMetadata(
			source.Attributes,
			source.ConstantArray,
			source.DocSummary,
		)
	}
	target.setSideIndexes(signatureIndex, hierarchyIndex, metadataIndex)
}

func packWorkspaceDocument(document *Document) *workspaceDocument {
	if document == nil {
		return nil
	}
	symbolCount, signatureCount, signatureExtraCount, hierarchyCount,
		metadataCount, symbolRangeExtraCount :=
		countWorkspaceSymbols(document.Symbols, false)
	packer := newWorkspaceDocumentPacker(
		document.Path,
		document.Version,
		document.WorkspaceRevision,
		document.Namespace,
		symbolCount,
		signatureCount,
		signatureExtraCount,
		hierarchyCount,
		metadataCount,
		symbolRangeExtraCount,
	)
	for index := range document.Symbols {
		packer.addSymbol(&document.Symbols[index])
	}
	packer.document.packReferences(document.References)
	packer.document.CallContracts = cloneCallContracts(document.CallContracts)
	return packer.document
}

func (document *workspaceDocument) packReferences(references []Reference) {
	if document == nil || len(references) == 0 {
		return
	}
	qualifiedCount := 0
	candidateCount := 0
	for index := range references {
		qualifiedCount += boundedWorkspaceReferenceCount(
			references[index].QualifiedNameCount(),
		)
		candidateCount += boundedWorkspaceReferenceCount(
			len(references[index].CandidateIDs()),
		)
	}
	packer := newWorkspaceReferencePacker(
		document,
		len(references),
		qualifiedCount+candidateCount,
	)
	for index := range references {
		source := &references[index]
		target := &document.References[index]
		sourceQualifiedCount := boundedWorkspaceReferenceCount(
			source.QualifiedNameCount(),
		)
		valueStart := len(document.referenceValues)
		for qualifiedIndex := 0; qualifiedIndex < sourceQualifiedCount; qualifiedIndex++ {
			packer.appendStringValue(source.QualifiedNameAt(qualifiedIndex))
		}
		candidateIDs := source.CandidateIDs()
		sourceCandidateCount := boundedWorkspaceReferenceCount(
			len(candidateIDs),
		)
		for _, candidate := range candidateIDs[:sourceCandidateCount] {
			packer.appendStringValue(string(candidate))
		}
		*target = document.newReference(
			source.Range,
			packer.stringIndexFor(source.Name),
			packer.stringIndexFor(string(source.Resolved)),
			uint32(valueStart),
			packer.typeIndexFor(source.Receiver),
			uint8(sourceQualifiedCount),
			uint8(sourceCandidateCount),
			source.Kind,
			source.TargetKind,
			workspaceReferenceFlags(source),
		)
	}
	packer.finishTables()
}

func (document *workspaceDocument) packProjectedReferences(
	references []Reference,
) {
	if document == nil || len(references) == 0 {
		return
	}
	referenceCount := 0
	qualifiedCount := 0
	candidateCount := 0
	for index := range references {
		reference := &references[index]
		if reference.Kind == VariableName {
			continue
		}
		referenceCount++
		qualifiedCount += boundedWorkspaceReferenceCount(
			reference.QualifiedNameCount(),
		)
		candidateCount += boundedWorkspaceReferenceCount(
			len(reference.CandidateIDs()),
		)
	}
	if referenceCount == 0 {
		return
	}
	packer := newWorkspaceReferencePacker(
		document,
		referenceCount,
		qualifiedCount+candidateCount,
	)
	targetIndex := 0
	for index := range references {
		source := &references[index]
		if source.Kind == VariableName {
			continue
		}
		sourceQualifiedCount := boundedWorkspaceReferenceCount(
			source.QualifiedNameCount(),
		)
		valueStart := len(document.referenceValues)
		for qualifiedIndex := 0; qualifiedIndex < sourceQualifiedCount; qualifiedIndex++ {
			packer.appendStringValue(source.QualifiedNameAt(qualifiedIndex))
		}
		candidateIDs := source.CandidateIDs()
		sourceCandidateCount := boundedWorkspaceReferenceCount(
			len(candidateIDs),
		)
		for _, candidate := range candidateIDs[:sourceCandidateCount] {
			packer.appendStringValue(string(candidate))
		}
		target := &document.References[targetIndex]
		*target = document.newReference(
			source.Range,
			packer.stringIndexFor(source.Name),
			packer.stringIndexFor(string(source.Resolved)),
			uint32(valueStart),
			packer.typeIndexFor(source.Receiver),
			uint8(sourceQualifiedCount),
			uint8(sourceCandidateCount),
			source.Kind,
			source.TargetKind,
			workspaceReferenceFlags(source),
		)
		targetIndex++
	}
	packer.finishTables()
}

func boundedWorkspaceReferenceCount(count int) int {
	return min(count, math.MaxUint8)
}

func workspaceReferenceFlags(reference *Reference) uint8 {
	if reference == nil {
		return 0
	}
	flags := uint8(0)
	if reference.Static {
		flags |= workspaceReferenceStatic
	}
	if reference.Write {
		flags |= workspaceReferenceWrite
	}
	return flags
}

func (document *workspaceDocument) validateReferenceSpans() error {
	if document == nil {
		return nil
	}
	for index := range document.References {
		reference := &document.References[index]
		if reference.hasFullLocation() {
			if _, ok := reference.fullLocation(document); !ok {
				return fmt.Errorf(
					"decode workspace graph: reference %d has invalid full location",
					index,
				)
			}
		}
		if !document.validReferenceStringIndex(reference.nameIndex()) {
			return fmt.Errorf(
				"decode workspace graph: reference %d has invalid name index",
				index,
			)
		}
		if !document.validReferenceStringIndex(reference.resolvedIndex()) {
			return fmt.Errorf(
				"decode workspace graph: reference %d has invalid resolved index",
				index,
			)
		}
		if !document.validReferenceTypeIndex(reference.receiverIndex()) {
			return fmt.Errorf(
				"decode workspace graph: reference %d has invalid receiver index",
				index,
			)
		}
		valueStart := reference.valueStart(document)
		if !validWorkspaceSpan(
			valueStart,
			uint32(reference.qualifiedCount())+
				uint32(reference.candidateCount()),
			len(document.referenceValues),
		) {
			return fmt.Errorf(
				"decode workspace graph: reference %d has invalid value span",
				index,
			)
		}
		valueEnd := int(valueStart) +
			int(reference.qualifiedCount()) +
			int(reference.candidateCount())
		for _, stringIndex := range document.referenceValues[int(valueStart):valueEnd] {
			if !document.validReferenceStringIndex(stringIndex) {
				return fmt.Errorf(
					"decode workspace graph: reference %d has invalid value index",
					index,
				)
			}
		}
	}
	return nil
}

func (document *workspaceDocument) validReferenceStringIndex(index uint32) bool {
	return index == 0 ||
		uint64(index) <= uint64(document.referenceStringCount())
}

func (document *workspaceDocument) validReferenceTypeIndex(index uint32) bool {
	return index == 0 || uint64(index) <= uint64(len(document.referenceTypes))
}

func validWorkspaceSpan(start, count uint32, length int) bool {
	return uint64(start)+uint64(count) <= uint64(length)
}

func (document *workspaceDocument) referenceString(index uint32) string {
	if index == 0 {
		return ""
	}
	if document == nil || document.referenceStrings == nil {
		return ""
	}
	if len(document.referenceStringIDs) != 0 {
		sharedIndex := document.referenceStringIDs[index-1]
		if sharedIndex == 0 ||
			int(sharedIndex) > len(document.referenceStrings.Values) {
			return ""
		}
		return document.referenceStrings.Values[sharedIndex-1]
	}
	return document.referenceStrings.Values[index-1]
}

func (document *workspaceDocument) referenceStringCount() int {
	if document == nil || document.referenceStrings == nil {
		return 0
	}
	if document.referenceStringIDs != nil {
		return len(document.referenceStringIDs)
	}
	return len(document.referenceStrings.Values)
}

func (document *workspaceDocument) referenceType(index uint32) types.Type {
	if index == 0 {
		return types.Type{}
	}
	return document.referenceTypes[index-1]
}

func (document *workspaceDocument) referenceValue(index int) string {
	return document.referenceString(document.referenceValues[index])
}

func (document *workspaceDocument) reference(index int) Reference {
	if document == nil || index < 0 || index >= len(document.References) {
		return Reference{}
	}
	source := &document.References[index]
	rng, valueStart := source.location(document)
	qualifiedStart := int(valueStart)
	qualifiedCount := source.qualifiedCount()
	qualifiedEnd := qualifiedStart + int(qualifiedCount)
	candidateStart := qualifiedEnd
	candidateCount := source.candidateCount()
	candidateEnd := candidateStart + int(candidateCount)
	var qualified []string
	if qualifiedCount > 0 {
		qualified = make([]string, qualifiedCount)
		for valueIndex := qualifiedStart; valueIndex < qualifiedEnd; valueIndex++ {
			qualified[valueIndex-qualifiedStart] = document.referenceValue(valueIndex)
		}
	}
	var candidates []SymbolID
	if candidateCount > 0 {
		candidates = make([]SymbolID, candidateCount)
		for valueIndex := candidateStart; valueIndex < candidateEnd; valueIndex++ {
			candidates[valueIndex-candidateStart] = SymbolID(
				document.referenceValue(valueIndex),
			)
		}
	}
	reference := Reference{
		Name:       document.referenceString(source.nameIndex()),
		Kind:       source.kind(),
		Receiver:   document.referenceType(source.receiverIndex()),
		TargetKind: source.targetKind(),
		Static:     source.flags()&workspaceReferenceStatic != 0,
		Write:      source.flags()&workspaceReferenceWrite != 0,
		Range:      rng,
		Resolved: SymbolID(
			document.referenceString(source.resolvedIndex()),
		),
	}
	reference.SetQualifiedNames(qualified)
	reference.SetCandidateIDs(candidates)
	return reference
}

func (document *workspaceDocument) materializeReferences() []Reference {
	if document == nil || len(document.References) == 0 {
		return nil
	}
	result := make([]Reference, len(document.References))
	for index := range document.References {
		result[index] = document.reference(index)
	}
	return result
}

func (document *workspaceDocument) attachDecodedSymbolSides(
	sides []decodedWorkspaceSymbolSides,
	parameterIDs []decodedWorkspaceParameterID,
) {
	if document == nil {
		return
	}
	signatureCount := 0
	signatureExtraCount := 0
	hierarchyCount := 0
	metadataCount := 0
	symbolRangeExtraCount := 0
	for index := range min(len(sides), len(document.Symbols)) {
		side := &sides[index]
		if side.signature != nil {
			signatureCount++
			if side.signature.Extras != nil {
				signatureExtraCount++
			}
		}
		if side.hierarchy != nil {
			hierarchyCount++
		}
		if side.metadata != nil {
			metadataCount++
		}
		if side.ranges != nil {
			symbolRangeExtraCount++
		}
	}
	document.signatures = make([]workspaceSignature, signatureCount)
	document.signatureExtras = make(
		[]workspaceSignatureExtras,
		signatureExtraCount,
	)
	document.hierarchies = make([]workspaceHierarchy, hierarchyCount)
	document.metadata = make([]workspaceMetadata, metadataCount)
	if symbolRangeExtraCount != 0 {
		document.symbolRangeExtras = &workspaceSymbolRangeExtras{
			Values: make(
				[]workspaceSymbolFullRanges,
				symbolRangeExtraCount,
			),
		}
	} else {
		document.symbolRangeExtras = nil
	}
	signatureIndex := 0
	signatureExtraIndex := 0
	hierarchyIndex := 0
	metadataIndex := 0
	symbolRangeExtraIndex := 0
	for index := range document.Symbols {
		symbol := &document.Symbols[index]
		symbol.Document = document
		if index >= len(sides) {
			symbol.setFlagsAndRangeIndex(symbol.flags(), -1)
			symbol.setSideIndexes(-1, -1, -1)
			continue
		}
		side := &sides[index]
		symbolSignatureIndex := -1
		symbolHierarchyIndex := -1
		symbolMetadataIndex := -1
		if side.signature != nil {
			document.signatures[signatureIndex] = *side.signature
			symbolSignatureIndex = signatureIndex
			signature := &document.signatures[signatureIndex]
			if signature.Extras != nil {
				document.signatureExtras[signatureExtraIndex] =
					*signature.Extras
				signature.Extras =
					&document.signatureExtras[signatureExtraIndex]
				signatureExtraIndex++
			}
			parameterIDStart := int(side.parameterIDStart)
			parameterIDEnd := min(
				parameterIDStart+int(side.parameterIDCount),
				len(parameterIDs),
			)
			for parameterIDIndex := parameterIDStart; parameterIDIndex < parameterIDEnd; parameterIDIndex++ {
				parameterID := &parameterIDs[parameterIDIndex]
				parameterIndex := int(parameterID.Index)
				if parameterIndex >= len(signature.Parameters) {
					continue
				}
				signature.Parameters[parameterIndex].setID(
					symbol,
					parameterID.ID,
				)
			}
			signatureIndex++
		}
		if side.hierarchy != nil {
			document.hierarchies[hierarchyIndex] = *side.hierarchy
			symbolHierarchyIndex = hierarchyIndex
			hierarchyIndex++
		}
		if side.metadata != nil {
			document.metadata[metadataIndex] = *side.metadata
			symbolMetadataIndex = metadataIndex
			metadataIndex++
		}
		rangeIndex := -1
		if side.ranges != nil {
			document.symbolRangeExtras.Values[symbolRangeExtraIndex] =
				*side.ranges
			rangeIndex = symbolRangeExtraIndex
			symbolRangeExtraIndex++
		}
		symbol.setFlagsAndRangeIndex(symbol.flags(), rangeIndex)
		symbol.setSideIndexes(
			symbolSignatureIndex,
			symbolHierarchyIndex,
			symbolMetadataIndex,
		)
	}
}

func internPackedWorkspaceGraphOwned(
	document *workspaceDocument,
	internString func(string) string,
	internType func(types.Type) types.Type,
) {
	if document == nil {
		return
	}
	document.Path = internString(document.Path)
	document.Namespace = internString(document.Namespace)
	for index := range document.CallContracts {
		contract := &document.CallContracts[index]
		contract.Target.Name = internString(contract.Target.Name)
		contract.Target.Class = internString(contract.Target.Class)
		for entryIndex := range contract.Return.Map {
			entry := &contract.Return.Map[entryIndex]
			entry.Key.Value = internString(entry.Key.Value)
			entry.Key.Expression = internString(entry.Key.Expression)
			entry.Result.Value = internString(entry.Result.Value)
			entry.Result.Expression = internString(entry.Result.Expression)
		}
		for expectedIndex := range contract.ExpectedArguments {
			expected := &contract.ExpectedArguments[expectedIndex]
			for valueIndex := range expected.Values {
				value := &expected.Values[valueIndex]
				value.Value = internString(value.Value)
				value.Expression = internString(value.Expression)
			}
		}
		for valueIndex := range contract.ExpectedReturnValues {
			value := &contract.ExpectedReturnValues[valueIndex]
			value.Value = internString(value.Value)
			value.Expression = internString(value.Expression)
		}
		for conditionIndex := range contract.ExitArguments {
			condition := &contract.ExitArguments[conditionIndex]
			for valueIndex := range condition.Values {
				value := &condition.Values[valueIndex]
				value.Value = internString(value.Value)
				value.Expression = internString(value.Expression)
			}
		}
	}
	if document.symbolStrings != nil &&
		!document.symbolStrings.Shared {
		internStringsOwned(document.symbolStrings.Values, internString)
	}
	for index := range document.Symbols {
		symbol := &document.Symbols[index]
		symbol.ID = SymbolID(internString(string(symbol.ID)))
		symbol.Document = document
		symbol.Type = internType(symbol.Type)
		symbol.NativeType = internType(symbol.NativeType)
		symbol.DocType = internType(symbol.DocType)
		symbol.ReturnType = internType(symbol.ReturnType)

		signature := symbol.signature()
		if signature != nil {
			for parameterIndex := range signature.Parameters {
				parameter := &signature.Parameters[parameterIndex]
				parameter.Name = internString(parameter.Name)
				if parameter.Extras != nil {
					parameter.Extras.ID = SymbolID(
						internString(string(parameter.Extras.ID)),
					)
				}
				parameter.NativeType = internType(parameter.NativeType)
				parameter.DocType = internType(parameter.DocType)
				if parameter.EffectiveSource ==
					workspaceParameterExplicitType &&
					parameter.Extras != nil {
					parameter.Extras.Type = internType(
						parameter.Extras.Type,
					)
				}
				internStringsOwned(parameter.assistantTags(), internString)
				internWorkspaceAttributes(
					parameter.attributes(),
					internString,
				)
				internWorkspaceAttributeValue(
					parameter.defaultValue(),
					internString,
				)
			}
			templates := signature.templates()
			for templateIndex := range templates {
				template := &templates[templateIndex]
				template.Name = internString(template.Name)
				template.Bound = internType(template.Bound)
				template.Default = internType(template.Default)
			}
			internTypesOwned(signature.throws(), internType)
			literalReturns := signature.literalReturns()
			for returnIndex := range literalReturns {
				item := &literalReturns[returnIndex]
				item.Value = internString(item.Value)
				item.Type = internType(item.Type)
			}
			constantReturns := signature.constantReturns()
			for returnIndex := range constantReturns {
				item := &constantReturns[returnIndex]
				item.Receiver = internString(item.Receiver)
				item.Name = internString(item.Name)
			}
			assertions := signature.assertions()
			for assertionIndex := range assertions {
				assertion := &assertions[assertionIndex]
				assertion.Target = internString(assertion.Target)
				assertion.Type = internType(assertion.Type)
			}
		}

		hierarchy := symbol.hierarchy()
		if hierarchy != nil {
			internStringsOwned(hierarchy.extends(), internString)
			internStringsOwned(hierarchy.implements(), internString)
			internStringsOwned(hierarchy.traits(), internString)
			internTypesOwned(hierarchy.extendsTypes(), internType)
			internTypesOwned(hierarchy.implementsTypes(), internType)
			internTypesOwned(hierarchy.traitTypes(), internType)
			aliases := hierarchy.aliases()
			for aliasIndex := range aliases {
				alias := &aliases[aliasIndex]
				alias.Trait = internString(alias.Trait)
				alias.Method = internString(alias.Method)
				alias.Alias = internString(alias.Alias)
			}
		}

		metadata := symbol.metadata()
		if metadata != nil {
			attributes := metadata.attributes()
			internWorkspaceAttributes(attributes, internString)
			constantArray := metadata.constantArray()
			for itemIndex := range constantArray {
				item := &constantArray[itemIndex]
				item.Key = internString(item.Key)
				item.Value = internString(item.Value)
				item.Type = internType(item.Type)
			}
			metadata.DocSummary = internString(metadata.DocSummary)
		}
	}
	if document.referenceStrings != nil &&
		document.referenceStringIDs == nil {
		internStringsOwned(document.referenceStrings.Values, internString)
	}
	internTypesOwned(document.referenceTypes, internType)
}

func internWorkspaceAttributes(
	attributes []Attribute,
	intern func(string) string,
) {
	for attributeIndex := range attributes {
		attribute := &attributes[attributeIndex]
		attribute.Name = intern(attribute.Name)
		for argumentIndex := range attribute.Arguments {
			argument := &attribute.Arguments[argumentIndex]
			argument.Name = intern(argument.Name)
			internWorkspaceAttributeValue(&argument.Value, intern)
		}
	}
}

func internWorkspaceAttributeValue(
	value *AttributeValue,
	intern func(string) string,
) {
	if value == nil {
		return
	}
	value.Value = intern(value.Value)
	value.Expression = intern(value.Expression)
	for index := range value.Items {
		internWorkspaceAttributeValue(&value.Items[index].Key, intern)
		internWorkspaceAttributeValue(&value.Items[index].Value, intern)
	}
}

func hasWorkspaceSignature(symbol *Symbol) bool {
	return len(symbol.Parameters) != 0 ||
		hasWorkspaceSignatureExtras(symbol)
}

func hasWorkspaceSignatureExtras(symbol *Symbol) bool {
	return len(symbol.Templates) != 0 ||
		len(symbol.Throws) != 0 ||
		len(symbol.LiteralReturns) != 0 ||
		len(symbol.ConstantReturns) != 0 ||
		len(symbol.Assertions) != 0
}

func hasWorkspaceHierarchy(symbol *Symbol) bool {
	return len(symbol.Extends) != 0 ||
		len(symbol.Implements) != 0 ||
		len(symbol.Traits) != 0 ||
		len(symbol.ExtendsTypes) != 0 ||
		len(symbol.ImplementsTypes) != 0 ||
		len(symbol.TraitTypes) != 0 ||
		len(symbol.TraitAliases) != 0
}

func hasWorkspaceMetadata(symbol *Symbol) bool {
	return len(symbol.Attributes) != 0 ||
		len(symbol.ConstantArray) != 0 ||
		symbol.DocSummary != ""
}

func (document *workspaceDocument) materialize() *Document {
	if document == nil {
		return nil
	}
	symbols := make([]Symbol, len(document.Symbols))
	for index := range document.Symbols {
		symbols[index] = document.Symbols[index].materialize()
	}
	return &Document{
		Path:              document.Path,
		Version:           document.Version,
		WorkspaceRevision: document.WorkspaceRevision,
		Namespace:         document.Namespace,
		Symbols:           symbols,
		References:        document.materializeReferences(),
		CallContracts:     cloneCallContracts(document.CallContracts),
	}
}

func (symbol *workspaceSymbol) materialize() Symbol {
	if symbol == nil {
		return Symbol{}
	}
	ranges := symbol.ranges()
	result := Symbol{
		ID:                 symbol.ID,
		Kind:               symbol.Kind,
		Name:               symbol.name(),
		FullyQualified:     symbol.fullyQualified(),
		Container:          symbol.container(),
		Path:               symbol.path(),
		Range:              ranges.Range,
		SelectionRange:     ranges.SelectionRange,
		BodyRange:          ranges.BodyRange,
		Visibility:         symbol.Visibility,
		WriteVisibility:    symbol.WriteVisibility,
		HasWriteVisibility: symbol.HasWriteVisibility,
		Flags:              symbol.flags(),
		Type:               symbol.Type,
		NativeType:         symbol.NativeType,
		DocType:            symbol.DocType,
		ReturnType:         symbol.ReturnType,
	}
	signature := symbol.signature()
	if signature != nil {
		result.Parameters = materializeWorkspaceParameters(
			signature.Parameters,
			symbol,
		)
		result.Templates = signature.templates()
		result.Throws = signature.throws()
		result.LiteralReturns = signature.literalReturns()
		result.ConstantReturns = signature.constantReturns()
		result.Assertions = signature.assertions()
	}
	hierarchy := symbol.hierarchy()
	if hierarchy != nil {
		result.Extends = hierarchy.extends()
		result.Implements = hierarchy.implements()
		result.Traits = hierarchy.traits()
		result.ExtendsTypes = hierarchy.extendsTypes()
		result.ImplementsTypes = hierarchy.implementsTypes()
		result.TraitTypes = hierarchy.traitTypes()
		result.TraitAliases = slices.Clone(hierarchy.aliases())
	}
	metadata := symbol.metadata()
	if metadata != nil {
		result.Attributes = metadata.attributes()
		result.ConstantArray = metadata.constantArray()
		result.DocSummary = metadata.DocSummary
	}
	return result
}

// SymbolView is a lightweight immutable view of a declaration retained by a
// Snapshot. Materialize it only when the complete Symbol value is needed.
type SymbolView struct {
	workspace *workspaceSymbol
	expanded  *Symbol
}

func workspaceView(symbol *workspaceSymbol) SymbolView {
	return SymbolView{workspace: symbol}
}

func expandedView(symbol *Symbol) SymbolView {
	return SymbolView{expanded: symbol}
}

// Materialize returns the complete public Symbol value represented by the
// view. Its nested slices remain immutable snapshot-owned data.
func (view SymbolView) Materialize() Symbol {
	if view.expanded != nil {
		return *view.expanded
	}
	return view.workspace.materialize()
}

func (view SymbolView) ID() SymbolID {
	if view.expanded != nil {
		return view.expanded.ID
	}
	if view.workspace == nil {
		return ""
	}
	return view.workspace.ID
}

func (view SymbolView) Kind() SymbolKind {
	if view.expanded != nil {
		return view.expanded.Kind
	}
	if view.workspace == nil {
		return NamespaceSymbol
	}
	return view.workspace.Kind
}

func (view SymbolView) Name() string {
	if view.expanded != nil {
		return view.expanded.Name
	}
	if view.workspace == nil {
		return ""
	}
	return view.workspace.name()
}

func (view SymbolView) FullyQualified() string {
	if view.expanded != nil {
		return view.expanded.FullyQualified
	}
	if view.workspace == nil {
		return ""
	}
	return view.workspace.fullyQualified()
}

func (view SymbolView) Container() SymbolID {
	if view.expanded != nil {
		return view.expanded.Container
	}
	if view.workspace == nil {
		return ""
	}
	return view.workspace.container()
}

func (view SymbolView) Flags() Flags {
	if view.expanded != nil {
		return view.expanded.Flags
	}
	if view.workspace == nil {
		return 0
	}
	return view.workspace.flags()
}

func (view SymbolView) Visibility() Visibility {
	if view.expanded != nil {
		return view.expanded.Visibility
	}
	if view.workspace == nil {
		return Public
	}
	return view.workspace.Visibility
}

func (view SymbolView) Path() string {
	if view.expanded != nil {
		return view.expanded.Path
	}
	if view.workspace == nil {
		return ""
	}
	return view.workspace.path()
}

func (view SymbolView) Range() cst.TextRange {
	if view.expanded != nil {
		return view.expanded.Range
	}
	if view.workspace == nil {
		return cst.TextRange{}
	}
	return view.workspace.rangeValue()
}

func (view SymbolView) SelectionRange() cst.TextRange {
	if view.expanded != nil {
		return view.expanded.SelectionRange
	}
	if view.workspace == nil {
		return cst.TextRange{}
	}
	return view.workspace.ranges().SelectionRange
}

// HierarchyNames returns the immutable trait, parent-class, and implemented
// interface names retained by the snapshot.
func (view SymbolView) HierarchyNames() (
	traits,
	extends,
	implements []string,
) {
	return view.hierarchyNames()
}

// TraitAliases returns method adaptations declared by the viewed class.
func (view SymbolView) TraitAliases() []TraitAlias {
	if view.expanded != nil {
		return view.expanded.TraitAliases
	}
	if view.workspace == nil || view.workspace.hierarchy() == nil {
		return nil
	}
	return view.workspace.hierarchy().aliases()
}

func (view SymbolView) hierarchyNames() (
	traits,
	extends,
	implements []string,
) {
	if view.expanded != nil {
		return view.expanded.Traits,
			view.expanded.Extends,
			view.expanded.Implements
	}
	if view.workspace == nil || view.workspace.hierarchy() == nil {
		return nil, nil, nil
	}
	hierarchy := view.workspace.hierarchy()
	return hierarchy.traits(),
		hierarchy.extends(),
		hierarchy.implements()
}
