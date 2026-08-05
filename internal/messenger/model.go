package messenger

import (
	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

type OccurrenceKind uint8

const (
	HandlerOccurrence OccurrenceKind = iota
	DispatchOccurrence
)

type SourceKind uint8

const (
	AttributeSource SourceKind = iota
	SubscriberSource
	ServiceTagSource
	DispatchSource
)

type Occurrence struct {
	Kind         OccurrenceKind
	Source       SourceKind
	Message      string
	File         string
	Range        cst.TextRange
	MessageRange cst.TextRange
	HandlerRange cst.TextRange
	ClassRange   cst.TextRange
	Class        string
	Method       string
	Service      string
	Bus          string
	Transport    string
	Priority     string
}

type Message struct {
	Name        string
	Occurrences []Occurrence
}

func (message Message) Handlers() []Occurrence {
	result := make([]Occurrence, 0, len(message.Occurrences))
	for _, occurrence := range message.Occurrences {
		if occurrence.Kind == HandlerOccurrence {
			result = append(result, occurrence)
		}
	}
	return result
}

func (message Message) Dispatches() []Occurrence {
	result := make([]Occurrence, 0, len(message.Occurrences))
	for _, occurrence := range message.Occurrences {
		if occurrence.Kind == DispatchOccurrence {
			result = append(result, occurrence)
		}
	}
	return result
}
