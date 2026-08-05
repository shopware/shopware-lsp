package php

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"runtime"

	fastflate "github.com/klauspost/compress/flate"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"
)

const (
	legacyWorkspaceGraphFormat    = 2
	legacyRawWorkspaceGraphFormat = 4
	persistedWorkspaceGraphFormat = 5
	maxPersistedSemanticValueSize = 128 << 20
)

var (
	semanticValueCompressorPool   = newSemanticCompressorPool()
	semanticValueDecompressorPool = newSemanticDecompressorPool()
)

const semanticDecoderBufferSize = 32 << 10
const maxPooledSemanticBuffer = 8 << 20

type semanticCompressor struct {
	writer        *fastflate.Writer
	encoderWriter *bufio.Writer
	output        bytes.Buffer
}

type semanticCompressorPool struct {
	compressors chan *semanticCompressor
}

type semanticDecompressor struct {
	input            bytes.Reader
	reader           io.ReadCloser
	resetter         fastflate.Resetter
	limited          io.LimitedReader
	buffered         *bufio.Reader
	decoder          *msgpack.Decoder
	workspaceDecoder *semantic.WorkspaceGraphDecoder
}

type semanticDecompressorPool struct {
	decompressors chan *semanticDecompressor
}

func newSemanticCompressorPool() *semanticCompressorPool {
	return &semanticCompressorPool{
		compressors: make(
			chan *semanticCompressor,
			max(1, runtime.GOMAXPROCS(0)),
		),
	}
}

func newSemanticDecompressorPool() *semanticDecompressorPool {
	return &semanticDecompressorPool{
		decompressors: make(
			chan *semanticDecompressor,
			max(1, runtime.GOMAXPROCS(0)),
		),
	}
}

func newSemanticDecompressor() *semanticDecompressor {
	decompressor := &semanticDecompressor{}
	decompressor.reader = fastflate.NewReader(&decompressor.input)
	decompressor.resetter = decompressor.reader.(fastflate.Resetter)
	decompressor.limited.R = decompressor.reader
	decompressor.buffered = bufio.NewReaderSize(
		&decompressor.limited,
		semanticDecoderBufferSize,
	)
	decompressor.decoder = msgpack.NewDecoder(decompressor.buffered)
	decompressor.workspaceDecoder = semantic.NewWorkspaceGraphDecoder()
	return decompressor
}

func (decompressor *semanticDecompressor) reset(payload []byte) error {
	decompressor.input.Reset(payload)
	if err := decompressor.resetter.Reset(
		&decompressor.input,
		nil,
	); err != nil {
		return err
	}
	decompressor.limited.R = decompressor.reader
	decompressor.limited.N = maxPersistedSemanticValueSize + 1
	decompressor.buffered.Reset(&decompressor.limited)
	decompressor.decoder.Reset(decompressor.buffered)
	return nil
}

func (decompressor *semanticDecompressor) releasePayload() {
	decompressor.input.Reset(nil)
	_ = decompressor.resetter.Reset(&decompressor.input, nil)
	decompressor.limited.R = decompressor.reader
	decompressor.limited.N = 0
	decompressor.buffered.Reset(&decompressor.limited)
	decompressor.decoder.Reset(nil)
}

func (pool *semanticDecompressorPool) get() *semanticDecompressor {
	select {
	case decompressor := <-pool.decompressors:
		return decompressor
	default:
		return newSemanticDecompressor()
	}
}

func (pool *semanticDecompressorPool) put(
	decompressor *semanticDecompressor,
) {
	if decompressor == nil {
		return
	}
	// A flate reader and bytes.Reader both retain their current input. Detach
	// the compressed database value before keeping the reusable buffers.
	decompressor.releasePayload()
	select {
	case pool.decompressors <- decompressor:
	default:
		decompressor.workspaceDecoder.Clear()
		_ = decompressor.reader.Close()
	}
}

func (pool *semanticDecompressorPool) clear() {
	for {
		select {
		case decompressor := <-pool.decompressors:
			decompressor.releasePayload()
			decompressor.workspaceDecoder.Clear()
			_ = decompressor.reader.Close()
		default:
			return
		}
	}
}

func (pool *semanticCompressorPool) get() *semanticCompressor {
	select {
	case compressor := <-pool.compressors:
		return compressor
	default:
		writer, err := fastflate.NewWriter(io.Discard, fastflate.BestSpeed)
		if err != nil {
			panic(err)
		}
		return &semanticCompressor{
			writer:        writer,
			encoderWriter: bufio.NewWriterSize(writer, 32<<10),
		}
	}
}

func (pool *semanticCompressorPool) put(compressor *semanticCompressor) {
	if compressor == nil {
		return
	}
	// A flate writer retains its destination. Reset it before pooling so a
	// compressed semantic payload cannot be kept live by the pool.
	compressor.writer.Reset(io.Discard)
	compressor.encoderWriter.Reset(io.Discard)
	if compressor.output.Cap() > maxPooledSemanticBuffer {
		compressor.output = bytes.Buffer{}
	} else {
		compressor.output.Reset()
	}
	select {
	case pool.compressors <- compressor:
	default:
		_ = compressor.writer.Close()
	}
}

func (pool *semanticCompressorPool) clear() {
	for {
		select {
		case compressor := <-pool.compressors:
			_ = compressor.writer.Close()
		default:
			return
		}
	}
}

type persistedWorkspaceGraph struct {
	Format  uint8  `msgpack:"format"`
	Payload []byte `msgpack:"payload"`
}

type persistedWorkspaceGraphCursor struct {
	data   []byte
	offset int
}

// decodePersistedWorkspaceGraphBorrowed decodes the small repository envelope
// while leaving its compressed payload backed by the current SQLite row. The
// caller must finish decoding the graph before advancing sql.Rows.
func decodePersistedWorkspaceGraphBorrowed(
	data []byte,
) (persistedWorkspaceGraph, error) {
	cursor := persistedWorkspaceGraphCursor{data: data}
	fieldCount, err := cursor.readMapLength()
	if err != nil {
		return persistedWorkspaceGraph{}, fmt.Errorf(
			"decode workspace graph envelope: %w",
			err,
		)
	}
	if fieldCount != 2 {
		return persistedWorkspaceGraph{}, fmt.Errorf(
			"decode workspace graph envelope: expected 2 fields, got %d",
			fieldCount,
		)
	}

	var result persistedWorkspaceGraph
	var haveFormat, havePayload bool
	for range fieldCount {
		key, readErr := cursor.readBytesValue(false)
		if readErr != nil {
			return persistedWorkspaceGraph{}, fmt.Errorf(
				"decode workspace graph envelope key: %w",
				readErr,
			)
		}
		switch string(key) {
		case "format":
			if haveFormat {
				return persistedWorkspaceGraph{}, fmt.Errorf(
					"decode workspace graph envelope: duplicate format",
				)
			}
			value, valueErr := cursor.readUint()
			if valueErr != nil {
				return persistedWorkspaceGraph{}, fmt.Errorf(
					"decode workspace graph envelope format: %w",
					valueErr,
				)
			}
			if value > 1<<8-1 {
				return persistedWorkspaceGraph{}, fmt.Errorf(
					"decode workspace graph envelope: format %d exceeds uint8",
					value,
				)
			}
			result.Format = uint8(value)
			haveFormat = true
		case "payload":
			if havePayload {
				return persistedWorkspaceGraph{}, fmt.Errorf(
					"decode workspace graph envelope: duplicate payload",
				)
			}
			result.Payload, readErr = cursor.readBytesValue(true)
			if readErr != nil {
				return persistedWorkspaceGraph{}, fmt.Errorf(
					"decode workspace graph envelope payload: %w",
					readErr,
				)
			}
			havePayload = true
		default:
			return persistedWorkspaceGraph{}, fmt.Errorf(
				"decode workspace graph envelope: unknown field %q",
				key,
			)
		}
	}
	if !haveFormat || !havePayload {
		return persistedWorkspaceGraph{}, fmt.Errorf(
			"decode workspace graph envelope: missing required field",
		)
	}
	if cursor.offset != len(cursor.data) {
		return persistedWorkspaceGraph{}, fmt.Errorf(
			"decode workspace graph envelope: %d trailing bytes",
			len(cursor.data)-cursor.offset,
		)
	}
	return result, nil
}

func (cursor *persistedWorkspaceGraphCursor) readByte() (byte, error) {
	if cursor.offset >= len(cursor.data) {
		return 0, io.ErrUnexpectedEOF
	}
	value := cursor.data[cursor.offset]
	cursor.offset++
	return value, nil
}

func (cursor *persistedWorkspaceGraphCursor) read(
	length uint64,
) ([]byte, error) {
	remaining := len(cursor.data) - cursor.offset
	if length > uint64(remaining) {
		return nil, io.ErrUnexpectedEOF
	}
	start := cursor.offset
	cursor.offset += int(length)
	return cursor.data[start:cursor.offset], nil
}

func (cursor *persistedWorkspaceGraphCursor) readFixedUint(
	length int,
) (uint64, error) {
	data, err := cursor.read(uint64(length))
	if err != nil {
		return 0, err
	}
	switch length {
	case 1:
		return uint64(data[0]), nil
	case 2:
		return uint64(binary.BigEndian.Uint16(data)), nil
	case 4:
		return uint64(binary.BigEndian.Uint32(data)), nil
	case 8:
		return binary.BigEndian.Uint64(data), nil
	default:
		return 0, fmt.Errorf("unsupported integer width %d", length)
	}
}

func (cursor *persistedWorkspaceGraphCursor) readMapLength() (
	int,
	error,
) {
	code, err := cursor.readByte()
	if err != nil {
		return 0, err
	}
	if msgpcode.IsFixedMap(code) {
		return int(code & msgpcode.FixedMapMask), nil
	}
	var value uint64
	switch code {
	case msgpcode.Map16:
		value, err = cursor.readFixedUint(2)
	case msgpcode.Map32:
		value, err = cursor.readFixedUint(4)
	default:
		return 0, fmt.Errorf("expected map, got code 0x%x", code)
	}
	if err != nil {
		return 0, err
	}
	if value > uint64(maxInt()) {
		return 0, fmt.Errorf("map length %d exceeds int", value)
	}
	return int(value), nil
}

func (cursor *persistedWorkspaceGraphCursor) readBytesValue(
	allowBinary bool,
) ([]byte, error) {
	code, err := cursor.readByte()
	if err != nil {
		return nil, err
	}
	var length uint64
	switch {
	case allowBinary && code == msgpcode.Nil:
		return nil, nil
	case msgpcode.IsFixedString(code):
		length = uint64(code & msgpcode.FixedStrMask)
	case code == msgpcode.Str8 || allowBinary && code == msgpcode.Bin8:
		length, err = cursor.readFixedUint(1)
	case code == msgpcode.Str16 || allowBinary && code == msgpcode.Bin16:
		length, err = cursor.readFixedUint(2)
	case code == msgpcode.Str32 || allowBinary && code == msgpcode.Bin32:
		length, err = cursor.readFixedUint(4)
	default:
		return nil, fmt.Errorf("expected string or binary, got code 0x%x", code)
	}
	if err != nil {
		return nil, err
	}
	return cursor.read(length)
}

func (cursor *persistedWorkspaceGraphCursor) readUint() (uint64, error) {
	code, err := cursor.readByte()
	if err != nil {
		return 0, err
	}
	if code <= msgpcode.PosFixedNumHigh {
		return uint64(code), nil
	}
	switch code {
	case msgpcode.Uint8:
		return cursor.readFixedUint(1)
	case msgpcode.Uint16:
		return cursor.readFixedUint(2)
	case msgpcode.Uint32:
		return cursor.readFixedUint(4)
	case msgpcode.Uint64:
		return cursor.readFixedUint(8)
	default:
		return 0, fmt.Errorf("expected unsigned integer, got code 0x%x", code)
	}
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

func encodeWorkspaceGraph(
	graph *semantic.WorkspaceGraph,
) (persistedWorkspaceGraph, error) {
	if graph == nil || graph.Path() == "" {
		return persistedWorkspaceGraph{}, fmt.Errorf(
			"workspace graph is required",
		)
	}
	payload, err := encodeCompressedSemanticValue(graph, "workspace graph")
	if err != nil {
		return persistedWorkspaceGraph{}, err
	}
	return persistedWorkspaceGraph{
		Format:  persistedWorkspaceGraphFormat,
		Payload: payload,
	}, nil
}

func (graph persistedWorkspaceGraph) decode() (
	*semantic.WorkspaceGraph,
	error,
) {
	return graph.decodeWith(nil)
}

func (graph persistedWorkspaceGraph) decodeWith(
	workspaceDecoder *semantic.WorkspaceGraphDecoder,
) (
	*semantic.WorkspaceGraph,
	error,
) {
	var compression semanticCompression
	switch graph.Format {
	case legacyWorkspaceGraphFormat:
		compression = semanticCompressionZlib
	case legacyRawWorkspaceGraphFormat, persistedWorkspaceGraphFormat:
		compression = semanticCompressionRawDeflate
	default:
		return nil, fmt.Errorf(
			"unsupported workspace graph format %d",
			graph.Format,
		)
	}
	var decoded semantic.WorkspaceGraph
	if err := decodeCompressedSemanticValueWithWorkspaceDecoder(
		graph.Payload,
		&decoded,
		"workspace graph",
		compression,
		workspaceDecoder,
	); err != nil {
		return nil, err
	}
	return &decoded, nil
}

func encodeCompressedSemanticValue(
	value any,
	label string,
) ([]byte, error) {
	compressor := semanticValueCompressorPool.get()
	compressor.output.Reset()
	compressor.writer.Reset(&compressor.output)
	compressor.encoderWriter.Reset(compressor.writer)
	defer semanticValueCompressorPool.put(compressor)
	encoder := msgpack.NewEncoder(compressor.encoderWriter)
	if err := encoder.Encode(value); err != nil {
		_ = compressor.writer.Close()
		return nil, fmt.Errorf(
			"encode %s: %w",
			label,
			err,
		)
	}
	if err := compressor.encoderWriter.Flush(); err != nil {
		_ = compressor.writer.Close()
		return nil, fmt.Errorf(
			"flush encoded %s: %w",
			label,
			err,
		)
	}
	if err := compressor.writer.Close(); err != nil {
		return nil, fmt.Errorf(
			"finish %s compression: %w",
			label,
			err,
		)
	}
	return bytes.Clone(compressor.output.Bytes()), nil
}

type semanticCompression uint8

const (
	semanticCompressionZlib semanticCompression = iota
	semanticCompressionRawDeflate
)

func decodeCompressedSemanticValueWithWorkspaceDecoder(
	payload []byte,
	target any,
	label string,
	compression semanticCompression,
	workspaceDecoder *semantic.WorkspaceGraphDecoder,
) error {
	var reader io.ReadCloser
	switch compression {
	case semanticCompressionZlib:
		zlibReader, err := zlib.NewReader(bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("open %s: %w", label, err)
		}
		reader = zlibReader
	case semanticCompressionRawDeflate:
		decompressor := semanticValueDecompressorPool.get()
		defer semanticValueDecompressorPool.put(decompressor)
		if err := decompressor.reset(payload); err != nil {
			return fmt.Errorf("open %s: %w", label, err)
		}
		var decodeErr error
		if graph, ok := target.(*semantic.WorkspaceGraph); ok {
			graphDecoder := workspaceDecoder
			if graphDecoder == nil {
				graphDecoder = decompressor.workspaceDecoder
			}
			decodeErr = graphDecoder.Decode(
				decompressor.decoder,
				graph,
			)
		} else {
			decodeErr = decompressor.decoder.Decode(target)
		}
		if decodeErr != nil {
			return fmt.Errorf("decode %s: %w", label, decodeErr)
		}
		if decompressor.limited.N <= 0 {
			return fmt.Errorf(
				"%s exceeds %d bytes",
				label,
				maxPersistedSemanticValueSize,
			)
		}
		return nil
	default:
		return fmt.Errorf("unsupported %s compression %d", label, compression)
	}
	defer func() { _ = reader.Close() }()

	limited := &io.LimitedReader{
		R: reader,
		N: maxPersistedSemanticValueSize + 1,
	}
	msgpackDecoder := msgpack.NewDecoder(limited)
	var decodeErr error
	if graph, ok := target.(*semantic.WorkspaceGraph); ok {
		graphDecoder := workspaceDecoder
		if graphDecoder == nil {
			graphDecoder = semantic.NewWorkspaceGraphDecoder()
		}
		decodeErr = graphDecoder.Decode(
			msgpackDecoder,
			graph,
		)
	} else {
		decodeErr = msgpackDecoder.Decode(target)
	}
	if decodeErr != nil {
		return fmt.Errorf("decode %s: %w", label, decodeErr)
	}
	if limited.N <= 0 {
		return fmt.Errorf(
			"%s exceeds %d bytes",
			label,
			maxPersistedSemanticValueSize,
		)
	}
	return nil
}
