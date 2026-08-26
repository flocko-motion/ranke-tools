package main

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/flocko-motion/ranke-go"
)

// testIdentity mints a throwaway root contributor claim and its signing key — a
// fresh identity per test run, never an application's, and never persisted
// anywhere beyond this process.
func testIdentity(t *testing.T) (ranke.Contributor, crypto.Signer) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	encoded, err := ranke.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("encode public key: %v", err)
	}
	claim, err := ranke.NewClaim(ranke.NodeTypeContributor, nil).
		WithInlineContent(encoded).
		WithEncoding(ranke.EncodingOctetStream).
		Sign(priv)
	if err != nil {
		t.Fatalf("sign contributor claim: %v", err)
	}
	// No Universe: this contributor's pubkey is inline, so nothing needs fetching.
	self, err := claim.AsContributor(context.Background(), nil, priv)
	if err != nil {
		t.Fatalf("bind contributor: %v", err)
	}
	return self, priv
}

// initRepo creates a git repo at dir with a deterministic identity, so the test
// depends on nothing from the environment it runs in.
func initRepo(t *testing.T, dir string) gitRepo {
	t.Helper()
	g := gitRepo{dir: dir}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"},
	} {
		if _, err := g.run(args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return g
}

// writeFile writes rel under dir, creating parent directories — used to build a
// tree with real nesting, so the conversion actually exercises recursion.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// commitAll stages and commits everything currently in the working tree.
func commitAll(t *testing.T, g gitRepo, message string) string {
	t.Helper()
	if _, err := g.run("add", "-A"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := g.run("commit", "-q", "-m", message); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	sha, err := g.revParse("HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return sha
}

// filesOf lists every regular file under dir (relative paths, sorted), skipping
// .git — a plain content comparison alongside the sha comparison, so a mismatch
// says exactly which file differs rather than just "the shas don't match".
func filesOf(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = content
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

// TestRoundTripIsByteExact converts a real commit to claims and back, into a
// second repo, and checks the restored commit against the original two ways:
// the git sha itself (the strong, single-number proof — structure, modes,
// content, and the commit's own metadata all matched, or it wouldn't), and a
// plain file-by-file diff (a clearer failure message if it ever doesn't).
func TestRoundTripIsByteExact(t *testing.T) {
	src := t.TempDir()
	g := initRepo(t, src)

	writeFile(t, src, "README.md", "a repo worth archiving\n")
	writeFile(t, src, "pkg/a.go", "package pkg\n")
	writeFile(t, src, "pkg/sub/b.go", "package sub\n")
	// The same content twice, at different paths — exercises that a tree can cite
	// one blob claim from two entries without it being built twice.
	writeFile(t, src, "pkg/sub/c.go", "package sub\n")
	origSha := commitAll(t, g, "first commit")

	contributor, signer := testIdentity(t)
	u := ranke.NewMemoryUniverse()
	ctx := context.Background()

	claims, err := gitToClaims(ctx, g, origSha, u, contributor, signer)
	if err != nil {
		t.Fatalf("gitToClaims: %v", err)
	}
	// README.md, pkg/a.go (blobs) + pkg/sub/b.go, pkg/sub/c.go sharing one blob +
	// pkg/sub, pkg (trees, the root included) + the commit. Fewer claims than
	// files+dirs would suggest, precisely because of the shared blob.
	if len(claims) == 0 {
		t.Fatal("gitToClaims produced no claims")
	}

	dst := t.TempDir()
	dstRepo := gitRepo{dir: dst}
	if _, err := dstRepo.run("init", "-q"); err != nil {
		t.Fatalf("git init dst: %v", err)
	}
	restoredSha, err := claimsToGit(ctx, dstRepo, u, claims)
	if err != nil {
		t.Fatalf("claimsToGit: %v", err)
	}
	if restoredSha != origSha {
		t.Fatalf("restored commit sha = %s, want %s (git objects did not round-trip byte-exact)", restoredSha, origSha)
	}

	if _, err := dstRepo.run("checkout", "-q", "--detach", restoredSha); err != nil {
		t.Fatalf("git checkout restored commit: %v", err)
	}
	want, got := filesOf(t, src), filesOf(t, dst)
	if len(want) != len(got) {
		t.Fatalf("restored tree has %d files, want %d", len(got), len(want))
	}
	for path, content := range want {
		if string(got[path]) != string(content) {
			t.Errorf("file %s differs after restore", path)
		}
	}
}

// TestRoundTripDedupesRepeatedBlobs pins the point of building trees bottom-up
// with a by-sha memo: the two files with identical content in the fixture above
// share one blob claim, not two.
func TestRoundTripDedupesRepeatedBlobs(t *testing.T) {
	src := t.TempDir()
	g := initRepo(t, src)
	writeFile(t, src, "a.txt", "same content\n")
	writeFile(t, src, "b.txt", "same content\n")
	sha := commitAll(t, g, "duplicate blobs")

	contributor, signer := testIdentity(t)
	u := ranke.NewMemoryUniverse()
	claims, err := gitToClaims(context.Background(), g, sha, u, contributor, signer)
	if err != nil {
		t.Fatalf("gitToClaims: %v", err)
	}

	var blobIDs []string
	for _, c := range claims {
		if c.Node().Type() == nodeBlob {
			blobIDs = append(blobIDs, c.ID().String())
		}
	}
	sort.Strings(blobIDs)
	if len(blobIDs) != 1 {
		t.Fatalf("blob claims = %v, want exactly one shared by both files", blobIDs)
	}
}
