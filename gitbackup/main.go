// package: main / gitbackup
// type:    entrypoint
// job:     the gitbackup binary — archives git state into a running ranke-db as a client
// limits:  a client only, over the documented REST contract; no dependency on ranke-db
// itself, only on ranke-go (-> DESIGN.md)
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "gitbackup:", err)
		os.Exit(1)
	}
}

// options are the flags every action shares: where to write, as whom, and what repo.
type options struct {
	server        string   // the ranke-db REST base URL
	token         string   // Authorization: Bearer credential
	apiKey        string   // X-API-Key credential
	contributorID string   // this worker's contributor claim id, already on the archive
	signingKey    string   // path to the contributor's ed25519 private key (PEM)
	repoURL       string   // the repo's remote URL; required even with --clone, to name the entity
	clone         string   // an existing local clone to read instead of cloning repoURL
	project       string   // the project name — its own entity, distinct from the repo
	branch        string   // the ranke-db branch this run contributes onto
	paths         []string // optional monorepo subset; empty archives the whole tree
}

// rootCmd builds the gitbackup command tree: one subcommand per action.
func rootCmd() *cobra.Command {
	var o options
	root := &cobra.Command{
		Use:           "gitbackup",
		Short:         "Archive git state into a running ranke-db, byte-exact and content-deduplicated",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	f := root.PersistentFlags()
	f.StringVar(&o.server, "server", "", "the ranke-db REST base URL (required)")
	f.StringVar(&o.token, "token", "", "Authorization: Bearer credential")
	f.StringVar(&o.apiKey, "api-key", "", "X-API-Key credential")
	f.StringVar(&o.contributorID, "contributor-id", "", "this worker's contributor claim id (required)")
	f.StringVar(&o.signingKey, "signing-key", "", "path to the contributor's ed25519 private key, PEM (required)")
	f.StringVar(&o.repoURL, "repo", "", "the repo's remote URL — names the repo entity, required even with --clone")
	f.StringVar(&o.clone, "clone", "", "an existing local clone to read, instead of cloning --repo")
	f.StringVar(&o.project, "project", "", "the project name — its own entity, distinct from the repo (required)")
	f.StringVar(&o.branch, "branch", "main", "the ranke-db branch this run contributes onto")
	f.StringSliceVar(&o.paths, "path", nil, "restrict to this path within the repo (repeatable; monorepo subset)")
	root.AddCommand(snapshotCmd(&o), backupCmd(&o))
	return root
}

// snapshotCmd archives one commit's tree: the exact source an artifact was built from.
// No `.git` comes back — no history, no refs, just that one commit's files, byte-exact.
func snapshotCmd(o *options) *cobra.Command {
	var ref string
	c := &cobra.Command{
		Use:   "snapshot",
		Short: "Archive one commit's tree, byte-exact — no history, no refs",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("snapshot: not yet implemented (ref %q)", ref)
		},
	}
	c.Flags().StringVar(&ref, "ref", "", "the tag or commit to archive (required)")
	return c
}

// backupCmd archives every commit reachable from the repo's refs, each chained to its
// parent — a reconstructible clone, immune to a later force-push or squash.
func backupCmd(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "backup",
		Short: "Archive every reachable commit, chained to its parents — a reconstructible clone",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("backup: not yet implemented")
		},
	}
	return c
}
