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
	"gopkg.in/yaml.v3"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "gitbackup:", err)
		os.Exit(1)
	}
}

// options are the flags every action shares: where to write, as whom, and what repo.
type options struct {
	configPath    string   // --config; a YAML alternative to typing every flag by hand
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

// fileConfig is --config's YAML shape — one field per flag this run cares
// about, so a config file is a persistent stand-in for typing them by hand.
type fileConfig struct {
	Server        string   `yaml:"server"`
	Token         string   `yaml:"token"`
	APIKey        string   `yaml:"api_key"`
	ContributorID string   `yaml:"contributor_id"`
	SigningKey    string   `yaml:"signing_key"`
	Repo          string   `yaml:"repo"`
	Clone         string   `yaml:"clone"`
	Project       string   `yaml:"project"`
	Branch        string   `yaml:"branch"`
	Paths         []string `yaml:"paths"`
}

// loadConfig fills whatever o.configPath's file sets and the command line
// didn't — a flag given on the command line always wins, checked through
// cmd.Flags().Changed rather than a zero-value guess, since "main" (branch's
// own default) is a legitimate value either side could have meant.
func (o *options) loadConfig(cmd *cobra.Command) error {
	if o.configPath == "" {
		return nil
	}
	data, err := os.ReadFile(o.configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	var c fileConfig
	if err := yaml.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("config: %s: %w", o.configPath, err)
	}
	fromFile := func(flag string, dst *string, val string) {
		if val != "" && !cmd.Flags().Changed(flag) {
			*dst = val
		}
	}
	fromFile("server", &o.server, c.Server)
	fromFile("token", &o.token, c.Token)
	fromFile("api-key", &o.apiKey, c.APIKey)
	fromFile("contributor-id", &o.contributorID, c.ContributorID)
	fromFile("signing-key", &o.signingKey, c.SigningKey)
	fromFile("repo", &o.repoURL, c.Repo)
	fromFile("clone", &o.clone, c.Clone)
	fromFile("project", &o.project, c.Project)
	fromFile("branch", &o.branch, c.Branch)
	if len(c.Paths) > 0 && !cmd.Flags().Changed("path") {
		o.paths = c.Paths
	}
	return nil
}

// rootCmd builds the gitbackup command tree: one subcommand per action.
func rootCmd() *cobra.Command {
	var o options
	root := &cobra.Command{
		Use:           "gitbackup",
		Short:         "Archive git state into a running ranke-db, byte-exact and content-deduplicated",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return o.loadConfig(cmd)
		},
	}
	f := root.PersistentFlags()
	f.StringVar(&o.configPath, "config", "", "path to a YAML config file — an alternative to the flags below; a flag given on the command line still wins (see config.example.yaml)")
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
	root.AddCommand(snapshotCmd(&o), backupCmd(&o), demoCmd())
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
