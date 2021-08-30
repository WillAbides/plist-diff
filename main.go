package main

import (
	"fmt"

	"github.com/alecthomas/kong"
)

var version = "dev"

const description = `plist-diff watches a directory tree and reports changes to stdout every 2 seconds.

It will also compare two directory trees with each other if you give it a second directory tree.

On a mac, you can watch for changes to preferences with:

plist-diff ~/Library/Preferences

`

type cliRoot struct {
	A                 string           `kong:"arg,name='watchtree',help='directory tree (or file) to watch for changes'"`
	B                 string           `kong:"arg,optional,name='othertree',help='directory tree (or file) to compare instead of watching the first tree for changes'"`
	Timestamps        bool             `kong:"help='include timestamp data in diffs. timestamps are ignored by default'"`
	PermissionsErrors bool             `kong:"help='return an error when a file cannot be opened due to insufficient permissions. these errors are ignored by default'"`
	Version           kong.VersionFlag `kong:"help=${VersionHelp}"`
}

var kongVars = kong.Vars{
	"version":     version,
	"VersionHelp": `output the plist-diff version and exit`,
}

func main() {
	var cli cliRoot
	kctx := kong.Parse(&cli,
		kongVars,
		kong.Description(description),
	)
	kctx.FatalIfErrorf(run(kctx, cli))
}

func run(kctx *kong.Context, cli cliRoot) error {
	d := &differ{
		IgnoreTimestamps:      !cli.Timestamps,
		IgnorePermissionError: !cli.PermissionsErrors,
	}
	if cli.B == "" {
		return d.watch(cli.A, kctx.Stdout)
	}
	eq, diff, err := d.diff(cli.A, cli.B)
	if err != nil {
		return err
	}
	if eq {
		return nil
	}
	fmt.Fprintln(kctx.Stdout, diff.String())
	return nil
}
