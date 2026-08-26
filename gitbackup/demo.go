// package: main / gitbackup
// type:    entrypoint
// job:     `gitbackup demo` — a small fixture repo, converted and restored, both kept on
// disk to look at — illustrative only, not one of the tool's two real actions
// limits:  no ranke-db, same as the rest of phase one (-> convert_test.go for real coverage)
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/flocko-motion/ranke-go"
)

func demoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "demo",
		Short: "Build a small fixture repo, convert it, and restore it — keeps both so you can look",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDemo(cmd.OutOrStdout())
		},
	}
}

// runDemo mirrors what TestRoundTripIsByteExact proves, but writes to fixed,
// printed, never-cleaned-up paths — the test is the real coverage, this is
// for a human to look at.
func runDemo(out io.Writer) error {
	base, err := os.MkdirTemp("", "gitbackup-demo-")
	if err != nil {
		return err
	}
	src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")

	g, err := demoBuildRepo(src)
	if err != nil {
		return err
	}
	commitSha, err := g.revParse("HEAD")
	if err != nil {
		return err
	}

	contributor, signer, err := demoIdentity()
	if err != nil {
		return err
	}
	u := ranke.NewMemoryUniverse()
	ctx := context.Background()
	claims, err := gitToClaims(ctx, g, commitSha, u, contributor, signer,
		"https://example.com/demo/gitbackup.git", "gitbackup-demo")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	dstRepo := gitRepo{dir: dst}
	if _, err := dstRepo.run("init", "-q"); err != nil {
		return err
	}
	restoredSha, err := claimsToGit(ctx, dstRepo, u, claims)
	if err != nil {
		return err
	}
	if _, err := dstRepo.run("checkout", "-q", "--detach", restoredSha); err != nil {
		return err
	}

	fmt.Fprintf(out, "%d claims built\n\n", len(claims))
	fmt.Fprintf(out, "source repo:   %s (commit %s)\n", src, commitSha)
	fmt.Fprintf(out, "restored repo: %s (commit %s)\n", dst, restoredSha)
	if commitSha == restoredSha {
		fmt.Fprintln(out, "byte-exact: the two commit shas match")
	}
	fmt.Fprintf(out, "\nlook around:\n  cd %s && git log --stat\n  cd %s && git log --stat && git remote -v\n  diff -rq %s %s -x .git\n", src, dst, src, dst)
	return nil
}

// demoBuildRepo creates a small repo exercising an ordinary file, an
// executable one, a nested path, and a symlink — the shapes the round trip
// has to carry, not an exhaustive fixture (that's the test's job).
func demoBuildRepo(dir string) (gitRepo, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return gitRepo{}, err
	}
	g := gitRepo{dir: dir}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "Demo"},
		{"config", "user.email", "demo@example.com"},
	} {
		if _, err := g.run(args...); err != nil {
			return gitRepo{}, err
		}
	}
	files := []struct {
		rel  string
		body string
		mode os.FileMode
	}{
		{"README.md", "gitbackup demo\n", 0o644},
		{"bin/run.sh", "#!/bin/sh\necho hi\n", 0o755},
		{"pkg/sub/a.go", "package sub\n", 0o644},
	}
	for _, f := range files {
		path := filepath.Join(dir, f.rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return gitRepo{}, err
		}
		if err := os.WriteFile(path, []byte(f.body), f.mode); err != nil {
			return gitRepo{}, err
		}
	}
	if err := os.Symlink("README.md", filepath.Join(dir, "link-to-readme")); err != nil {
		return gitRepo{}, err
	}
	if _, err := g.run("add", "-A"); err != nil {
		return gitRepo{}, err
	}
	if _, err := g.run("commit", "-q", "-m", "demo commit"); err != nil {
		return gitRepo{}, err
	}
	return g, nil
}

// demoIdentity mints a throwaway root contributor — never an application's
// key, same as the test identity (-> convert_test.go, testIdentity).
func demoIdentity() (ranke.Contributor, ed25519.PrivateKey, error) {
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
	self, err := claim.AsContributor(context.Background(), nil, priv)
	if err != nil {
		return nil, nil, err
	}
	return self, priv, nil
}
