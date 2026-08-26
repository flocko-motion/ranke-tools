// package: main / ranke-git
// type:    logic
// job:     wires one action together: connect, prepare (crif + content_hash scan) where
// an action needs it, build claims, contribute only what's new
// limits:  orchestration only; the pieces it calls own their own concerns
package main

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/flocko-motion/ranke-go"
)

// loadSigningKey reads an ed25519 private key from a PKCS#8 PEM file — the
// contributor's own key, an application-held secret, never minted here.
func loadSigningKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("signing key %s: not valid PEM", path)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("signing key %s: %w", path, err)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("signing key %s: is %T, want ed25519.PrivateKey", path, key)
	}
	return priv, nil
}

// loadContributor fetches the contributor claim id already names and binds
// key to it, so claims built this run are attributed and signed as it. The
// id is config, not discovered here (-> DESIGN.md, crif is for entities).
func loadContributor(ctx context.Context, c *client, branch, id string, key ed25519.PrivateKey) (ranke.Contributor, error) {
	claim, err := c.getClaim(ctx, branch, id)
	if err != nil {
		return nil, fmt.Errorf("load contributor %s: %w", id, err)
	}
	self, err := claim.AsContributor(ctx, nil, key)
	if err != nil {
		return nil, fmt.Errorf("bind contributor %s: %w", id, err)
	}
	return self, nil
}

// session is what every action needs before it does its own work: a live
// client and a bound contributor to sign as.
type session struct {
	client      *client
	contributor ranke.Contributor
	signer      crypto.Signer
}

// connect loads the signing key, waits for the server, and binds the
// contributor claim o names — the setup every action shares.
func connect(ctx context.Context, o *options) (*session, error) {
	if o.server == "" {
		return nil, fmt.Errorf("--server is required")
	}
	if o.contributorID == "" || o.signingKey == "" {
		return nil, fmt.Errorf("--contributor-id and --signing-key are required")
	}
	key, err := loadSigningKey(o.signingKey)
	if err != nil {
		return nil, err
	}
	c := newClient(o.server, o.token, o.apiKey)
	if err := c.waitReady(ctx, 10*time.Second); err != nil {
		return nil, fmt.Errorf("%s: %w", o.server, err)
	}
	contributor, err := loadContributor(ctx, c, o.branch, o.contributorID, key)
	if err != nil {
		return nil, err
	}
	return &session{client: c, contributor: contributor, signer: key}, nil
}

// contributeAndReport advances the dev clock, merges claims, and prints what
// landed — the last step every action shares. u is where an externally-
// content claim's bytes are read back from before they go out on the wire.
func contributeAndReport(ctx context.Context, c *client, u ranke.Universe, branch string, claims []ranke.Claim, out io.Writer) error {
	if err := c.advanceClock(ctx, time.Now().UTC().Add(time.Minute)); err != nil {
		return err
	}
	res, err := c.contribute(ctx, u, branch, claims)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, ">> merged %d claim(s) onto %q, head %s\n", len(res.Ids), branch, res.Head)
	return nil
}

// shapeFunc builds one action's claims into u, once the contributor,
// signer, and prep are ready.
type shapeFunc func(ctx context.Context, contributor ranke.Contributor, signer crypto.Signer, p prep, u ranke.Universe) ([]ranke.Claim, error)

// run connects, prepares (crif + content_hash scan), asks shape to build
// claims, then contributes only what's new — the three phases the whole
// tool is built around (-> DESIGN.md). For snapshot/backup specifically;
// attach has no repository/project and does its own, simpler thing.
func run(cmd *cobra.Command, o *options, shape shapeFunc) error {
	ctx := cmd.Context()
	if o.repoURL == "" || o.project == "" {
		return fmt.Errorf("--repo and --project are required")
	}
	s, err := connect(ctx, o)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, ">> preparing — crif %s/%s, scanning existing content on %q\n", o.repoURL, o.project, o.branch)
	p, err := prepare(ctx, s.client, o.branch, o.repoURL, o.project)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, ">> %d known object(s) to reuse\n", len(p.knownHashes))

	u := ranke.NewMemoryUniverse()
	claims, err := shape(ctx, s.contributor, s.signer, p, u)
	if err != nil {
		return err
	}
	if len(claims) == 0 {
		fmt.Fprintln(out, ">> nothing new — everything was already archived")
		return nil
	}
	return contributeAndReport(ctx, s.client, u, o.branch, claims, out)
}

// localRepo resolves the git repo this run reads from: --clone if given.
// Cloning --repo directly isn't built yet (-> DESIGN.md).
func localRepo(o *options) (gitRepo, error) {
	if o.clone == "" {
		return gitRepo{}, fmt.Errorf("--clone is required for now — cloning --repo directly isn't built yet")
	}
	return gitRepo{dir: o.clone}, nil
}
