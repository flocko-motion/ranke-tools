// package: main / ranke-git
// type:    io
// job:     byte-exact access to one git repo's objects, via the git binary itself
// limits:  reads/writes raw object payloads and structure; means nothing about claims (-> convert.go)
package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// gitRepo is a working directory git commands run against.
type gitRepo struct{ dir string }

// run executes one git subcommand in the repo's directory.
func (g gitRepo) run(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.dir
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut.String()))
	}
	return out.Bytes(), nil
}

// catFile returns one object's payload — the bytes git hashes under "<kind>
// <len>\0", not the header. Identical shape for a commit, a tree, or a blob,
// which is what lets one content strategy serve all three (-> DESIGN.md).
func (g gitRepo) catFile(kind, sha string) ([]byte, error) {
	return g.run("cat-file", kind, sha)
}

// revParse resolves a ref (tag, branch, "HEAD") to its commit sha.
func (g gitRepo) revParse(ref string) (string, error) {
	out, err := g.run("rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// commitTree returns sha's root tree sha.
func (g gitRepo) commitTree(sha string) (string, error) {
	out, err := g.run("log", "-1", "--format=%T", sha)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// commitParents returns sha's parent commit shas, in order — empty for a root commit.
func (g gitRepo) commitParents(sha string) ([]string, error) {
	out, err := g.run("log", "-1", "--format=%P", sha)
	if err != nil {
		return nil, err
	}
	if line := strings.TrimSpace(string(out)); line != "" {
		return strings.Fields(line), nil
	}
	return nil, nil
}

// catFileType reports what object a ref currently resolves to, without
// peeling it — "tag" for an annotated tag, "commit" for a lightweight tag or
// a branch. That distinction is the whole difference between the two.
func (g gitRepo) catFileType(ref string) (string, error) {
	out, err := g.run("cat-file", "-t", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// treeEntry is one line of `git ls-tree`, already split into what a tree object's
// payload encodes: a mode, a type, a child sha, and a name.
type treeEntry struct {
	mode string
	typ  string // "blob" or "tree" — a symlink is a "blob" with mode 120000, nothing special
	sha  string
	name string
}

// lsTree lists sha's direct entries only — never recurses, since each subtree is
// its own claim (-> DESIGN.md, nested not flattened).
func (g gitRepo) lsTree(sha string) ([]treeEntry, error) {
	out, err := g.run("ls-tree", sha)
	if err != nil {
		return nil, err
	}
	var entries []treeEntry
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		meta, name, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("ls-tree %s: unparseable line %q", sha, line)
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			return nil, fmt.Errorf("ls-tree %s: unparseable line %q", sha, line)
		}
		if fields[1] == "commit" {
			return nil, fmt.Errorf("ls-tree %s: %q is a submodule (gitlink), not yet supported", sha, name)
		}
		entries = append(entries, treeEntry{mode: fields[0], typ: fields[1], sha: fields[2], name: name})
	}
	return entries, nil
}

// hashObjectWrite writes payload as a git object of kind and returns the sha git
// gives it — the restore side of catFile: feed it what catFile read, and it hands
// back the same sha, which is the round trip's own proof.
func (g gitRepo) hashObjectWrite(kind string, payload []byte) (string, error) {
	cmd := exec.Command("git", "hash-object", "-w", "-t", kind, "--stdin")
	cmd.Dir = g.dir
	cmd.Stdin = bytes.NewReader(payload)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git hash-object -t %s: %w: %s", kind, err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
