package cst

import "strconv"

// TextRange is a half-open byte range [Start, End) into the source.
// Offsets are byte offsets (not chars), matching rowan's TextRange.
type TextRange struct {
	Start, End uint32
}

// Len returns the length of the range in bytes.
func (r TextRange) Len() uint32 {
	return r.End - r.Start
}

// Contains reports whether off lies within [Start, End).
func (r TextRange) Contains(off uint32) bool {
	return off >= r.Start && off < r.End
}

// String renders the range as "start..end" (rowan's debug format).
func (r TextRange) String() string {
	return strconv.FormatUint(uint64(r.Start), 10) + ".." + strconv.FormatUint(uint64(r.End), 10)
}
