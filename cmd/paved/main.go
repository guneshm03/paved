// Command paved is the internal-developer-platform CLI: software catalog,
// graph queries, scaffolder, scorecards and REST API.
package main

import (
	"os"

	"github.com/guneshm03/paved/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
