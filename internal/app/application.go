package app

import (
	"context"
	"io"

	"github.com/shopware/shopware-lsp/internal/lsp"
)

// Application is the process-level composition root. Workspace resources are
// intentionally created only after the LSP initialize request supplies a root.
type Application struct {
	server *lsp.Server
}

type Options struct {
	AllowUnsupportedProject bool
}

func New(version string) *Application {
	return NewWithOptions(version, Options{})
}

func NewWithOptions(version string, options Options) *Application {
	server := lsp.NewServer(nil, "", version)
	server.ConfigureProjectDetection(true, options.AllowUnsupportedProject)
	server.SetWorkspaceFactory(func(ctx context.Context, root string, server *lsp.Server) (lsp.WorkspaceRuntime, error) {
		return NewWorkspace(ctx, root, server)
	})
	return &Application{server: server}
}

func (a *Application) Run(in io.Reader, out io.Writer) error {
	return a.server.Start(in, out)
}

func (a *Application) Close() error {
	return a.server.CloseAll()
}
