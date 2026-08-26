package main

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// writeFile writes rel under dir at mode, creating parent directories — used to
// build a tree with real nesting and real permission bits, so the conversion
// exercises recursion and git's mode field (not just content).
func writeFile(t *testing.T, dir, rel string, content []byte, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// writeSymlink creates a symlink at rel pointing at target — git's other
// trackable "file" kind, mode 120000, whose blob content is the target path.
func writeSymlink(t *testing.T, dir, rel, target string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("symlink %s -> %s: %v", rel, target, err)
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

// fileEntry is one working-tree entry as filesOf records it: a symlink's
// target, or a regular file's executable bit and content — everything git's
// own tree/blob modes distinguish, so a restore comparison catches more than
// content drift.
type fileEntry struct {
	symlink    string // target, "" for a regular file
	executable bool
	content    []byte
}

// filesOf walks dir (relative paths, sorted), skipping .git — a comparison
// beyond the git sha, so a mismatch says exactly which file and which
// property (content, mode, or link target) differs.
func filesOf(t *testing.T, dir string) map[string]fileEntry {
	t.Helper()
	out := map[string]fileEntry{}
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
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			out[rel] = fileEntry{symlink: target}
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = fileEntry{executable: info.Mode()&0o100 != 0, content: content}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

const (
	testRepoURL = "https://example.com/acme/widgets.git"
	testProject = "widgets"
)

// TestRoundTripIsByteExact converts a real commit to claims and back, into a
// second repo, and checks the restored commit against the original two ways:
// the git sha itself (the strong, single-number proof — structure, modes,
// content, and the commit's own metadata all matched, or it wouldn't), and a
// plain file-by-file diff (a clearer failure message if it ever doesn't). It
// also checks the one thing git's own objects can't carry back on their own:
// origin, reconstructed from the repository entity.
func TestRoundTripIsByteExact(t *testing.T) {
	src := t.TempDir()
	g := initRepo(t, src)

	writeFile(t, src, "README.md", []byte("a repo worth archiving\n"), 0o644)
	writeFile(t, src, "pkg/a.go", []byte("package pkg\n"), 0o644)
	writeFile(t, src, "pkg/sub/b.go", []byte("package sub\n"), 0o644)
	// The same content twice, at different paths — exercises that a tree can cite
	// one blob claim from two entries without it being built twice.
	writeFile(t, src, "pkg/sub/c.go", []byte("package sub\n"), 0o644)
	// An executable file — git's other trackable regular-file mode.
	writeFile(t, src, "bin/run.sh", []byte("#!/bin/sh\necho hi\n"), 0o755)
	// A zero-byte file — content is a valid, empty external blob.
	writeFile(t, src, "empty.txt", nil, 0o644)
	// Non-UTF-8 bytes — content must round-trip as bytes, never as text.
	writeFile(t, src, "data.bin", []byte{0x00, 0x01, 0xff, 0xfe, 0x89, 'P', 'N', 'G'}, 0o644)
	// A space and non-ASCII characters in a name — exercises ls-tree's own
	// name encoding, not just its mode/type/sha columns.
	writeFile(t, src, "name with space.txt", []byte("spaces in names\n"), 0o644)
	writeFile(t, src, "unicode/héllo-wörld-🎉.txt", []byte("unicode filename\n"), 0o644)
	// A dotfile — git tracks it like any other name.
	writeFile(t, src, ".env.example", []byte("KEY=value\n"), 0o644)
	// Several levels deep, with intermediate directories holding nothing but
	// another directory — exercises a tree whose only entry is another tree.
	writeFile(t, src, "deep/a/b/c/leaf.txt", []byte("deeply nested\n"), 0o644)
	// A symlink — git's third trackable kind, mode 120000.
	writeSymlink(t, src, "link-to-readme", "README.md")
	origSha := commitAll(t, g, "first commit")

	contributor, signer := testIdentity(t)
	u := ranke.NewMemoryUniverse()
	ctx := context.Background()

	claims, err := gitToClaims(ctx, g, origSha, u, contributor, signer, testRepoURL, testProject)
	if err != nil {
		t.Fatalf("gitToClaims: %v", err)
	}
	assertEntities(t, claims)

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
		t.Fatalf("restored tree has %d entries, want %d", len(got), len(want))
	}
	for path, entry := range want {
		g, ok := got[path]
		if !ok {
			t.Errorf("%s missing after restore", path)
			continue
		}
		switch {
		case entry.symlink != "":
			if g.symlink != entry.symlink {
				t.Errorf("%s: symlink target = %q, want %q", path, g.symlink, entry.symlink)
			}
		case entry.executable != g.executable:
			t.Errorf("%s: executable = %v, want %v", path, g.executable, entry.executable)
		case string(g.content) != string(entry.content):
			t.Errorf("%s: content differs after restore", path)
		}
	}

	origin, err := dstRepo.run("remote", "get-url", "origin")
	if err != nil {
		t.Fatalf("restore did not configure origin: %v", err)
	}
	if got := strings.TrimSpace(string(origin)); got != testRepoURL {
		t.Fatalf("origin = %q, want %q", got, testRepoURL)
	}
}

// assertEntities checks the repository and project entities gitToClaims built
// alongside the git objects: present, carrying the right fields, D1-anchored
// to the commit, and the project pointing at the repository.
func assertEntities(t *testing.T, claims []ranke.Claim) {
	t.Helper()
	repo := findByType(t, claims, nodeRepository)
	if url, err := repo.Node().GetField("url"); err != nil || url != testRepoURL {
		t.Fatalf("repository url = %q, %v, want %q", url, err, testRepoURL)
	}
	if len(inputEdges(repo)) != 1 {
		t.Fatalf("repository has %d derivation/input edge(s), want exactly one (D1)", len(inputEdges(repo)))
	}

	proj := findByType(t, claims, nodeProject)
	if name, err := proj.Node().GetField("name"); err != nil || name != testProject {
		t.Fatalf("project name = %q, %v, want %q", name, err, testProject)
	}
	if len(inputEdges(proj)) != 1 {
		t.Fatalf("project has %d derivation/input edge(s), want exactly one (D1)", len(inputEdges(proj)))
	}
	hosted := proj.Edges(ranke.EdgeFilterType{Type: edgeHostedIn})
	if len(hosted) != 1 || !hosted[0].Reference().Equal(repo.ID()) {
		t.Fatalf("project's %s edge does not point at the repository entity", edgeHostedIn)
	}
}

// findByType returns the one claim of typ in claims, failing if there is not
// exactly one.
func findByType(t *testing.T, claims []ranke.Claim, typ string) ranke.Claim {
	t.Helper()
	var found ranke.Claim
	for _, c := range claims {
		if c.Node().Type() != typ {
			continue
		}
		if found != nil {
			t.Fatalf("more than one %s claim", typ)
		}
		found = c
	}
	if found == nil {
		t.Fatalf("no %s claim among %d", typ, len(claims))
	}
	return found
}

// inputEdges returns claim's derivation/input edges, the D1 evidence trail.
func inputEdges(claim ranke.Claim) []ranke.Edge {
	return claim.Edges(ranke.EdgeFilterType{Type: edgeInput})
}

// TestRoundTripDedupesRepeatedBlobs pins the point of building trees bottom-up
// with a by-sha memo: the two files with identical content in the fixture above
// share one blob claim, not two.
func TestRoundTripDedupesRepeatedBlobs(t *testing.T) {
	src := t.TempDir()
	g := initRepo(t, src)
	writeFile(t, src, "a.txt", []byte("same content\n"), 0o644)
	writeFile(t, src, "b.txt", []byte("same content\n"), 0o644)
	sha := commitAll(t, g, "duplicate blobs")

	contributor, signer := testIdentity(t)
	u := ranke.NewMemoryUniverse()
	claims, err := gitToClaims(context.Background(), g, sha, u, contributor, signer, testRepoURL, testProject)
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

// TestSubmoduleIsRefused pins that a gitlink (mode 160000, a submodule
// reference) fails loudly rather than being silently mistaken for a blob or
// a tree — the one git object kind this conversion does not yet support
// (-> gitobj.go, lsTree).
func TestSubmoduleIsRefused(t *testing.T) {
	src := t.TempDir()
	g := initRepo(t, src)
	writeFile(t, src, "README.md", []byte("has a submodule\n"), 0o644)
	commitAll(t, g, "before the gitlink")

	// Staged directly via update-index, not commitAll's `git add -A`: a gitlink has
	// no working-tree counterpart, so `add -A` would just unstage it again.
	fakeCommit := strings.Repeat("a", 40)
	if _, err := g.run("update-index", "--add", "--cacheinfo", "160000,"+fakeCommit+",vendor/lib"); err != nil {
		t.Fatalf("git update-index (gitlink): %v", err)
	}
	if _, err := g.run("commit", "-q", "-m", "add a submodule reference"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	sha, err := g.revParse("HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}

	contributor, signer := testIdentity(t)
	u := ranke.NewMemoryUniverse()
	_, err = gitToClaims(context.Background(), g, sha, u, contributor, signer, testRepoURL, testProject)
	if err == nil {
		t.Fatal("gitToClaims accepted a gitlink, want a refusal")
	}
	if !strings.Contains(err.Error(), "submodule") {
		t.Fatalf("gitToClaims error = %q, want it to name the submodule", err)
	}
}
