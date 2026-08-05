package doctrine

import "github.com/shopware/shopware-lsp/internal/indexer"

type doctrineCandidateFlags uint16

const (
	doctrineMappingCandidate doctrineCandidateFlags = 1 << iota
	doctrineDQLCandidate
	doctrineDQLFunctionCandidate
	doctrineTypeCandidate

	doctrineExtensionMarker
	doctrineNameMarker
	doctrineDBALMarker
	doctrineTypesMarker
	doctrineTypeTagMarker
)

type doctrineCandidatePattern struct {
	text  string
	flags doctrineCandidateFlags
}

type doctrineCandidateState struct {
	next  [128]uint16
	flags doctrineCandidateFlags
}

type doctrineCandidateMatcher struct {
	states []doctrineCandidateState
	fold   bool
}

var (
	doctrinePHPExactCandidates = newDoctrineCandidateMatcher(
		false,
		doctrineCandidatePattern{"ORM\\", doctrineMappingCandidate},
		doctrineCandidatePattern{"ODM\\", doctrineMappingCandidate},
		doctrineCandidatePattern{"@Entity", doctrineMappingCandidate},
		doctrineCandidatePattern{"@Document", doctrineMappingCandidate},
		doctrineCandidatePattern{"#[Entity", doctrineMappingCandidate},
		doctrineCandidatePattern{"#[Document", doctrineMappingCandidate},
		doctrineCandidatePattern{"$dql", doctrineDQLCandidate},
		doctrineCandidatePattern{
			"stringFunctions",
			doctrineDQLFunctionCandidate,
		},
		doctrineCandidatePattern{
			"numericFunctions",
			doctrineDQLFunctionCandidate,
		},
		doctrineCandidatePattern{
			"datetimeFunctions",
			doctrineDQLFunctionCandidate,
		},
	)
	doctrinePHPFoldCandidates = newDoctrineCandidateMatcher(
		true,
		doctrineCandidatePattern{"createquery", doctrineDQLCandidate},
		doctrineCandidatePattern{"setdql", doctrineDQLCandidate},
		doctrineCandidatePattern{"addtype", doctrineTypeCandidate},
		doctrineCandidatePattern{"overridetype", doctrineTypeCandidate},
		doctrineCandidatePattern{"gettyperegistry", doctrineTypeCandidate},
		doctrineCandidatePattern{"extension", doctrineExtensionMarker},
		doctrineCandidatePattern{"doctrine", doctrineNameMarker},
		doctrineCandidatePattern{"dbal", doctrineDBALMarker},
		doctrineCandidatePattern{"types", doctrineTypesMarker},
	)
	doctrineYAMLCandidates = newDoctrineCandidateMatcher(
		true,
		doctrineCandidatePattern{"type: entity", doctrineMappingCandidate},
		doctrineCandidatePattern{"type: embeddable", doctrineMappingCandidate},
		doctrineCandidatePattern{"repositoryclass:", doctrineMappingCandidate},
		doctrineCandidatePattern{"targetentity:", doctrineMappingCandidate},
		doctrineCandidatePattern{"doctrine:", doctrineNameMarker},
		doctrineCandidatePattern{"dbal:", doctrineDBALMarker},
		doctrineCandidatePattern{"types:", doctrineTypesMarker},
	)
	doctrineXMLExactCandidates = newDoctrineCandidateMatcher(
		false,
		doctrineCandidatePattern{
			"doctrine-mapping",
			doctrineMappingCandidate,
		},
		doctrineCandidatePattern{"<entity", doctrineMappingCandidate},
		doctrineCandidatePattern{"<document", doctrineMappingCandidate},
	)
	doctrineXMLFoldCandidates = newDoctrineCandidateMatcher(
		true,
		doctrineCandidatePattern{"dbal", doctrineDBALMarker},
		doctrineCandidatePattern{"<doctrine:type", doctrineTypeTagMarker},
		doctrineCandidatePattern{"<type", doctrineTypeTagMarker},
	)
)

func newDoctrineCandidateMatcher(
	fold bool,
	patterns ...doctrineCandidatePattern,
) doctrineCandidateMatcher {
	stateCapacity := 1
	for _, pattern := range patterns {
		stateCapacity += len(pattern.text)
	}
	states := make([]doctrineCandidateState, 1, stateCapacity)
	for _, pattern := range patterns {
		state := uint16(0)
		for index := 0; index < len(pattern.text); index++ {
			value := pattern.text[index]
			if fold {
				value = lowerDoctrineCandidateASCII(value)
			}
			if value >= 128 {
				panic("Doctrine candidate patterns must be ASCII")
			}
			next := states[state].next[value]
			if next == 0 {
				if len(states) >= 1<<16 {
					panic("Doctrine candidate matcher has too many states")
				}
				states = append(states, doctrineCandidateState{})
				next = uint16(len(states) - 1)
				states[state].next[value] = next
			}
			state = next
		}
		states[state].flags |= pattern.flags
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
			states[next].flags |= states[failures[next]].flags
		}
	}
	return doctrineCandidateMatcher{states: states, fold: fold}
}

func (matcher doctrineCandidateMatcher) match(
	content []byte,
) doctrineCandidateFlags {
	if len(matcher.states) == 0 {
		return 0
	}
	var result doctrineCandidateFlags
	state := uint16(0)
	for _, value := range content {
		if matcher.fold {
			value = lowerDoctrineCandidateASCII(value)
		}
		if value >= 128 {
			state = 0
			continue
		}
		state = matcher.states[state].next[value]
		result |= matcher.states[state].flags
	}
	return result
}

func matchDoctrineCandidatePair(
	content []byte,
	exact,
	folded doctrineCandidateMatcher,
) doctrineCandidateFlags {
	if len(exact.states) == 0 {
		return folded.match(content)
	}
	if len(folded.states) == 0 {
		return exact.match(content)
	}

	var result doctrineCandidateFlags
	var exactState uint16
	var foldedState uint16
	for _, value := range content {
		if value >= 128 {
			exactState = 0
			foldedState = 0
			continue
		}
		exactState = exact.states[exactState].next[value]
		foldedValue := lowerDoctrineCandidateASCII(value)
		foldedState = folded.states[foldedState].next[foldedValue]
		result |= exact.states[exactState].flags |
			folded.states[foldedState].flags
	}
	return result
}

func doctrineCandidates(
	file *indexer.ParsedFile,
) doctrineCandidateFlags {
	if file == nil {
		return 0
	}
	var markers doctrineCandidateFlags
	switch file.Extension() {
	case ".php":
		markers = matchDoctrineCandidatePair(
			file.Content,
			doctrinePHPExactCandidates,
			doctrinePHPFoldCandidates,
		)
		if markers&doctrineTypeCandidate == 0 &&
			markers&doctrineExtensionMarker != 0 &&
			markers&doctrineNameMarker != 0 &&
			markers&doctrineDBALMarker != 0 &&
			markers&doctrineTypesMarker != 0 {
			markers |= doctrineTypeCandidate
		}
	case ".yaml", ".yml":
		markers = doctrineYAMLCandidates.match(file.Content)
		if markers&doctrineNameMarker != 0 &&
			markers&doctrineDBALMarker != 0 &&
			markers&doctrineTypesMarker != 0 {
			markers |= doctrineTypeCandidate
		}
	case ".xml":
		markers = matchDoctrineCandidatePair(
			file.Content,
			doctrineXMLExactCandidates,
			doctrineXMLFoldCandidates,
		)
		if markers&doctrineDBALMarker != 0 &&
			markers&doctrineTypeTagMarker != 0 {
			markers |= doctrineTypeCandidate
		}
	}
	return markers & (doctrineMappingCandidate |
		doctrineDQLCandidate |
		doctrineDQLFunctionCandidate |
		doctrineTypeCandidate)
}

func lowerDoctrineCandidateASCII(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}
