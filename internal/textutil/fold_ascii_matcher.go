package textutil

type foldASCIIState struct {
	next     [128]uint16
	terminal bool
}

// FoldASCIIMatcher searches for any of a fixed set of ASCII patterns without
// normalizing or allocating a copy of the source.
type FoldASCIIMatcher struct {
	states       []foldASCIIState
	matchesEmpty bool
}

func NewFoldASCIIMatcher(patterns ...string) FoldASCIIMatcher {
	stateCapacity := 1
	for _, pattern := range patterns {
		stateCapacity += len(pattern)
	}
	states := make([]foldASCIIState, 1, stateCapacity)
	matchesEmpty := false
	for _, pattern := range patterns {
		if pattern == "" {
			matchesEmpty = true
			continue
		}
		state := uint16(0)
		for index := 0; index < len(pattern); index++ {
			value := lowerASCII(pattern[index])
			if value >= 128 {
				panic("folded ASCII matcher patterns must be ASCII")
			}
			next := states[state].next[value]
			if next == 0 {
				if len(states) >= 1<<16 {
					panic("folded ASCII matcher has too many states")
				}
				states = append(states, foldASCIIState{})
				next = uint16(len(states) - 1)
				states[state].next[value] = next
			}
			state = next
		}
		states[state].terminal = true
	}

	failures := make([]uint16, len(states))
	queue := make([]uint16, 0, len(states)-1)
	for value := range 128 {
		if state := states[0].next[value]; state != 0 {
			queue = append(queue, state)
		}
	}
	for head := 0; head < len(queue); head++ {
		state := queue[head]
		failure := failures[state]
		for value := range 128 {
			next := states[state].next[value]
			if next == 0 {
				states[state].next[value] = states[failure].next[value]
				continue
			}
			queue = append(queue, next)
			failures[next] = states[failure].next[value]
			states[next].terminal = states[next].terminal ||
				states[failures[next]].terminal
		}
	}
	return FoldASCIIMatcher{
		states:       states,
		matchesEmpty: matchesEmpty,
	}
}

func (matcher FoldASCIIMatcher) ContainsString(source string) bool {
	if matcher.matchesEmpty {
		return true
	}
	if len(matcher.states) == 0 {
		return false
	}
	var state uint16
	for index := 0; index < len(source); index++ {
		value := source[index]
		if value >= 128 {
			state = 0
			continue
		}
		state = matcher.states[state].next[lowerASCII(value)]
		if matcher.states[state].terminal {
			return true
		}
	}
	return false
}

func (matcher FoldASCIIMatcher) ContainsBytes(source []byte) bool {
	if matcher.matchesEmpty {
		return true
	}
	if len(matcher.states) == 0 {
		return false
	}
	var state uint16
	for _, value := range source {
		if value >= 128 {
			state = 0
			continue
		}
		state = matcher.states[state].next[lowerASCII(value)]
		if matcher.states[state].terminal {
			return true
		}
	}
	return false
}

func lowerASCII(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}
