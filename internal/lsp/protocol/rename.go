package protocol

type RenameParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position Position `json:"position"`
	NewName  string   `json:"newName"`
}
