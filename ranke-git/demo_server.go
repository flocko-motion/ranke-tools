// package: main / ranke-git
// type:    entrypoint
// job:     `ranke-git demo-server` — the same story as `demo`, but genuinely over the
// network: crif, reuse, attach, against a real ranke-db
// limits:  never starts a server itself — checks for one and says how to start it
// if there isn't one (-> DESIGN.md)
package main

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/flocko-motion/ranke-go"
)

// demoServerBranch, demoServerRepoURL and demoServerProject are fixed, not
// randomised per run — running this command twice is the point, showing
// entity and content_hash reuse on the second pass.
const (
	demoServerBranch   = "ranke-git-demo-server"
	demoServerRepoURL  = "https://example.com/ranke-git-demo-server.git"
	demoServerProject  = "ranke-git-demo-server"
	demoServerDefault  = "localhost:8080"
	demoServerLogTitle = "demo build log"
)

func demoServerCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "demo-server",
		Short: "Run the demo against a real ranke-db — snapshot, crif, attach, all live (start one first: server/run.sh)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDemoServer(cmd, o)
		},
	}
}

// runDemoServer checks for a server, refusing to start one itself, then
// snapshots one commit and attaches a log to it — the driving use case
// DESIGN.md describes, run for real.
func runDemoServer(cmd *cobra.Command, o *options) error {
	ctx := cmd.Context()
	addr := o.server
	if addr == "" {
		addr = demoServerDefault
	}
	out := cmd.OutOrStdout()

	fmt.Fprintf(out, ">> looking for a ranke-db at %s ...\n", addr)
	c := newClient(addr, o.token, o.apiKey)
	if err := c.waitReady(ctx, 2*time.Second); err != nil {
		return fmt.Errorf("no ranke-db found at %s — start one first, from another shell:\n\n  server/run.sh\n\nthen run this again", addr)
	}
	fmt.Fprintln(out, ">> found it")

	fmt.Fprintln(out, ">> minting a throwaway contributor and registering it")
	contributor, signer, err := bootstrapContributor(ctx, c, demoServerBranch)
	if err != nil {
		return err
	}

	src, err := demoServerRepo()
	if err != nil {
		return err
	}
	g := gitRepo{dir: src}
	sha, err := g.revParse("HEAD")
	if err != nil {
		return err
	}
	fmt.Fprintf(out, ">> built a demo repo: %s (commit %s)\n", src, sha)

	fmt.Fprintf(out, ">> preparing — crif %s/%s on %q\n", demoServerRepoURL, demoServerProject, demoServerBranch)
	p, err := prepare(ctx, c, demoServerBranch, demoServerRepoURL, demoServerProject)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, ">> %d known object(s) to reuse\n", len(p.knownHashes))

	u := ranke.NewMemoryUniverse()
	claims, err := gitToClaims(ctx, g, sha, nil, u, contributor, signer, demoServerRepoURL, demoServerProject, p)
	if err != nil {
		return err
	}
	if len(claims) == 0 {
		fmt.Fprintln(out, ">> nothing new to snapshot — already archived")
	} else if err := contributeAndReport(ctx, c, u, demoServerBranch, claims, out); err != nil {
		return err
	}

	fmt.Fprintln(out, ">> attaching a build log to that commit")
	target, err := findOne(ctx, c, demoServerBranch, nodeCommit, gitShaField, sha)
	if err != nil {
		return fmt.Errorf("demo-server: %w", err)
	}
	if target == nil {
		return fmt.Errorf("demo-server: commit %s not found right after snapshotting it", sha)
	}
	attachU := ranke.NewMemoryUniverse()
	logClaim, err := buildAttachment(ctx, attachU, contributor, signer, *target,
		"source/"+attachTypePrefix+"build_log", demoServerLogTitle, "text/plain",
		[]byte("demo-server build log\ncompiling...\nbuild succeeded\n"))
	if err != nil {
		return err
	}
	if err := contributeAndReport(ctx, c, attachU, demoServerBranch, []ranke.Claim{logClaim}, out); err != nil {
		return err
	}

	fmt.Fprintf(out, "\n>> done — look around:\n")
	fmt.Fprintf(out, "   demo repo:  %s\n", src)
	fmt.Fprintf(out, "   query:      curl -s -X POST http://%s/query -H 'Content-Type: application/json' \\\n", addr)
	fmt.Fprintf(out, "                 -d '{\"select\":{\"branch\":%q},\"output\":{\"detail\":\"claims\",\"encoding\":\"json\"}}'\n", demoServerBranch)
	fmt.Fprintln(out, "   run this again — the repo/project entities and any unchanged files should come back reused, only the new commit and its tree freshly minted")
	return nil
}

// demoServerRepo builds one commit — the driving use case is "snapshot one
// commit, attach documents to it", not a whole history.
func demoServerRepo() (string, error) {
	dir, err := os.MkdirTemp("", "ranke-git-demo-server-")
	if err != nil {
		return "", err
	}
	g := gitRepo{dir: dir}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "Demo"},
		{"config", "user.email", "demo@example.com"},
	} {
		if _, err := g.run(args...); err != nil {
			return "", err
		}
	}
	if err := demoCommit(g, []demoFile{
		{"README.md", "ranke-git demo-server\n", 0o644},
		{"main.go", "package main\n\nfunc main() {}\n", 0o644},
	}, "release commit"); err != nil {
		return "", err
	}
	return dir, nil
}

// bootstrapContributor mints a throwaway identity and contributes its root
// claim to branch before binding it, so every claim it signs afterward
// resolves. Kept separate from demoIdentity (demo.go): that one never keeps
// the unbound claim contribute needs.
func bootstrapContributor(ctx context.Context, c *client, branch string) (ranke.Contributor, crypto.Signer, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	encoded, err := ranke.EncodePublicKey(pub)
	if err != nil {
		return nil, nil, err
	}
	claim, err := ranke.NewClaim(ranke.NodeTypeContributor, nil).
		WithInlineContent(encoded).
		WithEncoding(ranke.EncodingOctetStream).
		Sign(priv)
	if err != nil {
		return nil, nil, err
	}
	if err := c.advanceClock(ctx, time.Now().UTC().Add(time.Minute)); err != nil {
		return nil, nil, err
	}
	if _, err := c.contribute(ctx, ranke.NewMemoryUniverse(), branch, []ranke.Claim{claim}); err != nil {
		return nil, nil, fmt.Errorf("register contributor: %w", err)
	}
	self, err := claim.AsContributor(ctx, nil, priv)
	if err != nil {
		return nil, nil, err
	}
	return self, priv, nil
}
