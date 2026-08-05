package inspections

import (
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

func protocolTextRange(index *cst.LineIndex, rng protocol.Range) cst.TextRange {
	return cst.TextRange{
		Start: index.OffsetUTF16(uint32(rng.Start.Line), uint32(rng.Start.Character)),
		End:   index.OffsetUTF16(uint32(rng.End.Line), uint32(rng.End.Character)),
	}
}
