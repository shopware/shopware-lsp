package semantic

import (
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/php/types"
)

// symbolSignatureExtras stores the five infrequent function-like collections
// in two words. A single collection points directly at its backing array; only
// the uncommon multi-collection case needs an auxiliary pointer record.
type symbolSignatureExtras struct {
	data    unsafe.Pointer
	lengths uint64
}

type symbolSignatureExtraPointers struct {
	templatesData       *TemplateParameter
	throwsData          *types.Type
	assertionsData      *TypeAssertion
	literalReturnsData  *LiteralReturn
	constantReturnsData *ConstantReturn
}

type symbolSignatureExtrasFull struct {
	templates       []TemplateParameter
	throws          []types.Type
	assertions      []TypeAssertion
	literalReturns  []LiteralReturn
	constantReturns []ConstantReturn
}

const (
	symbolSignatureLengthBits = 12
	symbolSignatureLengthMask = 1<<symbolSignatureLengthBits - 1
	symbolSignatureMultiFlag  = uint64(1) << 62
	symbolSignatureFullFlag   = uint64(1) << 63
)

func newSymbolSignatureExtras(
	templates []TemplateParameter,
	throws []types.Type,
	assertions []TypeAssertion,
	literalReturns []LiteralReturn,
	constantReturns []ConstantReturn,
) symbolSignatureExtras {
	lengths := [...]int{
		len(templates),
		len(throws),
		len(assertions),
		len(literalReturns),
		len(constantReturns),
	}
	for _, length := range lengths {
		if length > symbolSignatureLengthMask {
			return symbolSignatureExtras{
				data: unsafe.Pointer(&symbolSignatureExtrasFull{
					templates: templates, throws: throws,
					assertions: assertions, literalReturns: literalReturns,
					constantReturns: constantReturns,
				}),
				lengths: symbolSignatureFullFlag,
			}
		}
	}
	collections := [...]unsafe.Pointer{
		unsafe.Pointer(workspaceSliceData(templates)),
		unsafe.Pointer(workspaceSliceData(throws)),
		unsafe.Pointer(workspaceSliceData(assertions)),
		unsafe.Pointer(workspaceSliceData(literalReturns)),
		unsafe.Pointer(workspaceSliceData(constantReturns)),
	}
	result := symbolSignatureExtras{}
	nonEmpty := 0
	for index, length := range lengths {
		result.lengths |= uint64(length) <<
			(symbolSignatureLengthBits * index)
		if length != 0 {
			nonEmpty++
			result.data = collections[index]
		}
	}
	if nonEmpty > 1 {
		result.data = unsafe.Pointer(&symbolSignatureExtraPointers{
			templatesData:       (*TemplateParameter)(collections[0]),
			throwsData:          (*types.Type)(collections[1]),
			assertionsData:      (*TypeAssertion)(collections[2]),
			literalReturnsData:  (*LiteralReturn)(collections[3]),
			constantReturnsData: (*ConstantReturn)(collections[4]),
		})
		result.lengths |= symbolSignatureMultiFlag
	}
	return result
}

func (extras symbolSignatureExtras) length(index int) uint32 {
	return uint32(
		extras.lengths >> (symbolSignatureLengthBits * index) &
			symbolSignatureLengthMask,
	)
}

func (extras symbolSignatureExtras) dataAt(index int) unsafe.Pointer {
	if extras.length(index) == 0 {
		return nil
	}
	if extras.lengths&symbolSignatureMultiFlag == 0 {
		return extras.data
	}
	pointers := (*symbolSignatureExtraPointers)(extras.data)
	switch index {
	case 0:
		return unsafe.Pointer(pointers.templatesData)
	case 1:
		return unsafe.Pointer(pointers.throwsData)
	case 2:
		return unsafe.Pointer(pointers.assertionsData)
	case 3:
		return unsafe.Pointer(pointers.literalReturnsData)
	default:
		return unsafe.Pointer(pointers.constantReturnsData)
	}
}

func (extras symbolSignatureExtras) full() *symbolSignatureExtrasFull {
	if extras.lengths&symbolSignatureFullFlag == 0 {
		return nil
	}
	return (*symbolSignatureExtrasFull)(extras.data)
}

func (s Symbol) Templates() []TemplateParameter {
	if full := s.signatureExtras.full(); full != nil {
		return full.templates
	}
	return workspaceSlice(
		(*TemplateParameter)(s.signatureExtras.dataAt(0)),
		s.signatureExtras.length(0),
	)
}

func (s Symbol) Throws() []types.Type {
	if full := s.signatureExtras.full(); full != nil {
		return full.throws
	}
	return workspaceSlice(
		(*types.Type)(s.signatureExtras.dataAt(1)),
		s.signatureExtras.length(1),
	)
}

func (s Symbol) Assertions() []TypeAssertion {
	if full := s.signatureExtras.full(); full != nil {
		return full.assertions
	}
	return workspaceSlice(
		(*TypeAssertion)(s.signatureExtras.dataAt(2)),
		s.signatureExtras.length(2),
	)
}

func (s Symbol) LiteralReturns() []LiteralReturn {
	if full := s.signatureExtras.full(); full != nil {
		return full.literalReturns
	}
	return workspaceSlice(
		(*LiteralReturn)(s.signatureExtras.dataAt(3)),
		s.signatureExtras.length(3),
	)
}

func (s Symbol) ConstantReturns() []ConstantReturn {
	if full := s.signatureExtras.full(); full != nil {
		return full.constantReturns
	}
	return workspaceSlice(
		(*ConstantReturn)(s.signatureExtras.dataAt(4)),
		s.signatureExtras.length(4),
	)
}

func (s *Symbol) SetSignatureExtras(
	templates []TemplateParameter,
	throws []types.Type,
	assertions []TypeAssertion,
	literalReturns []LiteralReturn,
	constantReturns []ConstantReturn,
) {
	if s == nil {
		return
	}
	s.signatureExtras = newSymbolSignatureExtras(
		templates,
		throws,
		assertions,
		literalReturns,
		constantReturns,
	)
}

func (s *Symbol) SetTemplates(templates []TemplateParameter) {
	if s != nil {
		s.SetSignatureExtras(
			templates, s.Throws(), s.Assertions(),
			s.LiteralReturns(), s.ConstantReturns(),
		)
	}
}

func (s *Symbol) SetThrows(throws []types.Type) {
	if s != nil {
		s.SetSignatureExtras(
			s.Templates(), throws, s.Assertions(),
			s.LiteralReturns(), s.ConstantReturns(),
		)
	}
}

func (s *Symbol) SetAssertions(assertions []TypeAssertion) {
	if s != nil {
		s.SetSignatureExtras(
			s.Templates(), s.Throws(), assertions,
			s.LiteralReturns(), s.ConstantReturns(),
		)
	}
}

func (s *Symbol) SetLiteralReturns(returns []LiteralReturn) {
	if s != nil {
		s.SetSignatureExtras(
			s.Templates(), s.Throws(), s.Assertions(),
			returns, s.ConstantReturns(),
		)
	}
}

func (s *Symbol) SetConstantReturns(returns []ConstantReturn) {
	if s != nil {
		s.SetSignatureExtras(
			s.Templates(), s.Throws(), s.Assertions(),
			s.LiteralReturns(), returns,
		)
	}
}
