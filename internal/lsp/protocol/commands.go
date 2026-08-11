package protocol

import "encoding/json"

// CommandRequest represents a custom command request
type CommandRequest struct {
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}

// RequestInputParams represents the parameters for a request input request
type RequestInputParams struct {
	Prompt       string `json:"prompt"`
	PlaceHolder  string `json:"placeHolder,omitempty"`
	DefaultValue string `json:"defaultValue,omitempty"`
}

// RequestInputResponse represents the response for a request input request
type RequestInputResponse struct {
	Value string `json:"value"`
}

// MultipleInputsParams represents the parameters for a multiple inputs request
type MultipleInputsParams struct {
	Items []RequestInputParams `json:"items"`
}

// MultipleInputsResponse represents the response for a multiple inputs request
type MultipleInputsResponse struct {
	Values []string `json:"values"`
}
