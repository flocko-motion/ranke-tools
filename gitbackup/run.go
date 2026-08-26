// package: main / gitbackup
// type:    logic
// job:     wires one action together: load the contributor, prepare (crif + content_hash
// scan), build claims, contribute only what's new
// limits:  orchestration only; the pieces it calls own their own concerns
package main

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
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

// shapeFunc builds one action's claims, once the contributor, signer, and
// prep are ready.
type shapeFunc func(ctx context.Context, contributor ranke.Contributor, signer crypto.Signer, p prep) ([]ranke.Claim, error)

// run resolves the contributor, prepares (crif + content_hash scan), asks
// shape to build claims, then contributes only what's new — the three
// phases the whole tool is built around (-> DESIGN.md).
func run(cmd *cobra.Command, o *options, shape shapeFunc) error {
	ctx := cmd.Context()
	if o.server == "" {
		return fmt.Errorf("--server is required")
	}
	if o.contributorID == "" || o.signingKey == "" {
		return fmt.Errorf("--contributor-id and --signing-key are required")
	}
	if o.repoURL == "" || o.project == "" {
		return fmt.Errorf("--repo and --project are required")
	}

	key, err := loadSigningKey(o.signingKey)
	if err != nil {
		return err
	}
	c := newClient(o.server, o.token, o.apiKey)
	if err := c.waitReady(ctx, 10*time.Second); err != nil {
		return fmt.Errorf("%s: %w", o.server, err)
	}
	contributor, err := loadContributor(ctx, c, o.branch, o.contributorID, key)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, ">> preparing — crif %s/%s, scanning existing content on %q\n", o.repoURL, o.project, o.branch)
	p, err := prepare(ctx, c, o.branch, o.repoURL, o.project)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, ">> %d known object(s) to reuse\n", len(p.knownHashes))

	claims, err := shape(ctx, contributor, key, p)
	if err != nil {
		return err
	}
	if len(claims) == 0 {
		fmt.Fprintln(out, ">> nothing new — everything was already archived")
		return nil
	}

	if err := c.advanceClock(ctx, time.Now().UTC().Add(time.Minute)); err != nil {
		return err
	}
	res, err := c.contribute(ctx, o.branch, claims)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, ">> merged %d claim(s) onto %q, head %s\n", len(res.Ids), o.branch, res.Head)
	return nil
}

// localRepo resolves the git repo this run reads from: --clone if given.
// Cloning --repo directly isn't built yet (-> DESIGN.md).
func localRepo(o *options) (gitRepo, error) {
	if o.clone == "" {
		return gitRepo{}, fmt.Errorf("--clone is required for now — cloning --repo directly isn't built yet")
	}
	return gitRepo{dir: o.clone}, nil
}
