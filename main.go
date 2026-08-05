package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/shopware/shopware-lsp/internal/cli"
	"github.com/shopware/shopware-lsp/internal/runtimeconfig"
)

// Version is set during build by goreleaser
var version = "dev"

//go:embed LICENSE
var licenseText string

//go:embed THIRD_PARTY_NOTICES.md
var thirdPartyNotices string

func main() {
	log.SetFlags(0)
	policy := runtimeconfig.ApplyMemoryPolicy()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	runner := cli.New(cli.Options{
		Version:           version,
		License:           licenseText,
		ThirdPartyNotices: thirdPartyNotices,
		GCPercent:         policy.GCPercent,
		GCPolicyApplied:   policy.Applied,
	})
	if err := runner.Run(
		ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr,
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
}
