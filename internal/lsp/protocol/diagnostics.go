package protocol

// DiagnosticParams represents the parameters for a textDocument/diagnostic request
type DiagnosticParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	PreviousResultId string `json:"previousResultId,omitempty"`
}

// DiagnosticResult represents a full report returned by textDocument/diagnostic.
type DiagnosticResult struct {
	Kind     string       `json:"kind"`
	Items    []Diagnostic `json:"items"`
	ResultId string       `json:"resultId,omitempty"`
}

// DiagnosticSeverity represents the severity of a diagnostic
type DiagnosticSeverity int

const (
	// DiagnosticSeverityError represents an error diagnostic
	DiagnosticSeverityError DiagnosticSeverity = 1
	// DiagnosticSeverityWarning represents a warning diagnostic
	DiagnosticSeverityWarning DiagnosticSeverity = 2
	// DiagnosticSeverityInformation represents an information diagnostic
	DiagnosticSeverityInformation DiagnosticSeverity = 3
	// DiagnosticSeverityHint represents a hint diagnostic
	DiagnosticSeverityHint DiagnosticSeverity = 4
)

// DiagnosticTag represents a tag for a diagnostic
type DiagnosticTag int

const (
	// DiagnosticTagUnnecessary indicates that the code is unnecessary
	DiagnosticTagUnnecessary DiagnosticTag = 1
	// DiagnosticTagDeprecated indicates that the code is deprecated
	DiagnosticTagDeprecated DiagnosticTag = 2
)

// Diagnostic represents a diagnostic, such as a compiler error or warning
type Diagnostic struct {
	Range              Range                          `json:"range"`
	Severity           DiagnosticSeverity             `json:"severity,omitempty"`
	Code               interface{}                    `json:"code,omitempty"`
	Source             string                         `json:"source,omitempty"`
	Message            string                         `json:"message"`
	Tags               []DiagnosticTag                `json:"tags,omitempty"`
	RelatedInformation []DiagnosticRelatedInformation `json:"relatedInformation,omitempty"`
	Data               interface{}                    `json:"data,omitempty"`
}

// DiagnosticRelatedInformation represents additional information related to a diagnostic
type DiagnosticRelatedInformation struct {
	Location Location `json:"location"`
	Message  string   `json:"message"`
}

// PublishDiagnosticsParams represents the parameters for a textDocument/publishDiagnostics notification
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     int          `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// DocumentDiagnosticReport represents a diagnostic report for a document
type DocumentDiagnosticReport struct {
	Kind     string       `json:"kind"`
	ResultId string       `json:"resultId,omitempty"`
	Items    []Diagnostic `json:"items,omitempty"`
}

// WorkspaceDiagnosticReport represents a diagnostic report for the workspace
type WorkspaceDiagnosticReport struct {
	Items []WorkspaceDocumentDiagnosticReport `json:"items"`
}

// WorkspaceDocumentDiagnosticReport represents a diagnostic report for a document in the workspace
type WorkspaceDocumentDiagnosticReport struct {
	URI     string                   `json:"uri"`
	Version int                      `json:"version"`
	Report  DocumentDiagnosticReport `json:"report"`
}
