package semantic

import (
	"fmt"
	"slices"
	"strings"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/vmihailenco/msgpack/v5"
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
	symbolTypeExtras   *workspaceSymbolTypeExtraTable
	referenceExtras    *workspaceReferenceExtras
	symbolStrings      *workspaceSymbolStringTable
	referenceStrings   *workspaceReferenceStringTable
	referenceStringIDs []uint32
	referenceTypes     []types.Type
	referenceValues    []uint32
	referenceBloom     [2]uint64
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
	if err := symbol.valueType().EncodeMsgpack(encoder); err != nil {
		return err
	}
	if err := symbol.nativeType().EncodeMsgpack(encoder); err != nil {
		return err
	}
	if err := symbol.docType().EncodeMsgpack(encoder); err != nil {
		return err
	}
	if err := symbol.returnType().EncodeMsgpack(encoder); err != nil {
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
	document.rebuildReferenceBloom()
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
