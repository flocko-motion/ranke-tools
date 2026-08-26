// package: main / gitbackup
// type:    logic
// job:     converts one git commit to Ranke claims and back, byte-exact (-> DESIGN.md)
// limits:  local only — no ranke-db, no network; the Universe here is a client-side
// scratch content store, not persistence (-> gitobj.go for raw git access)
package main

import (
	"context"
	"crypto"
	"fmt"
	"io"
	"time"

	"github.com/flocko-motion/ranke-go"
)

// The claim types this conversion produces — all `source`, none of it interpreted
// (-> DESIGN.md, "capture, not interpretation, all the way down").
const (
	nodeCommit = "source/commit"
	nodeTree   = "source/tree"
	nodeBlob   = "source/blob"
)

// The structural edges beyond the automatic contributor edge.
const (
	edgeTree  = "derivation/tree"  // commit -> its root tree
	edgeEntry = "derivation/entry" // tree -> one entry (blob or subtree); fields: name, mode
)

// gitShaField is the lookup key every claim here carries — content_hash already
// serves this for reuse (-> DESIGN.md), but git_sha is what a human, or `git log`,
// cross-references against.
const gitShaField = "git_sha"

// made pairs a signed claim with the height a claim citing it must climb past
// (§4.1: a citing claim declares height explicitly, one more than the tallest
// thing it cites).
type made struct {
	claim  ranke.Claim
	height uint64
}

// converter walks one commit's objects bottom-up, memoising by git sha so an
// entry appearing twice (an unchanged subtree, a duplicated blob) becomes one
// claim cited twice, not two claims.
type converter struct {
	ctx         context.Context
	git         gitRepo
	u           ranke.Universe
	contributor ranke.Contributor
	signer      crypto.Signer
	at          time.Time
	bySha       map[string]made
	claims      []ranke.Claim // build order: every child before the parent that cites it
}

// gitToClaims converts one commit into claims: every blob and tree it reaches,
// nested to match git's own structure, then the commit itself. Content at every
// level is git's own raw object payload, stored in u so a later restore (or a
// dedup lookup) can read it back — u is a plain in-process scratch store here,
// not a ranke-db connection (that's phase 2).
func gitToClaims(ctx context.Context, g gitRepo, commitSha string, u ranke.Universe, contributor ranke.Contributor, signer crypto.Signer) ([]ranke.Claim, error) {
	c := &converter{
		ctx: ctx, git: g, u: u, contributor: contributor, signer: signer,
		at: time.Now().UTC(), bySha: map[string]made{},
	}

	treeSha, err := g.commitTree(commitSha)
	if err != nil {
		return nil, fmt.Errorf("commit %s: tree: %w", commitSha, err)
	}
	root, err := c.tree(treeSha)
	if err != nil {
		return nil, err
	}
	treeEdge, err := ranke.NewEdge(ranke.EdgeConfig{
		Reference: root.claim.ID(), Referenced: root.claim, Type: edgeTree,
	})
	if err != nil {
		return nil, fmt.Errorf("commit %s: root edge: %w", commitSha, err)
	}
	payload, err := g.catFile("commit", commitSha)
	if err != nil {
		return nil, fmt.Errorf("commit %s: cat-file: %w", commitSha, err)
	}
	commit, err := c.write(nodeCommit, payload, commitSha, root.height, []ranke.Edge{treeEdge})
	if err != nil {
		return nil, fmt.Errorf("commit %s: %w", commitSha, err)
	}
	c.claims = append(c.claims, commit.claim)
	return c.claims, nil
}

// tree converts one git tree object: its entries first (recursively), then the
// tree claim itself, citing each entry by a derivation/entry edge carrying the
// name and mode git's own tree encoding assigns it — structure available to
// Ranke's graph independent of what the raw content holds.
func (c *converter) tree(sha string) (made, error) {
	if m, ok := c.bySha[sha]; ok {
		return m, nil
	}
	entries, err := c.git.lsTree(sha)
	if err != nil {
		return made{}, fmt.Errorf("tree %s: %w", sha, err)
	}

	edges := make([]ranke.Edge, 0, len(entries))
	var height uint64
	for _, e := range entries {
		var child made
		switch e.typ {
		case "blob":
			child, err = c.blob(e.sha)
		case "tree":
			child, err = c.tree(e.sha)
		default:
			err = fmt.Errorf("entry %q: unsupported git object type %q", e.name, e.typ)
		}
		if err != nil {
			return made{}, fmt.Errorf("tree %s: %w", sha, err)
		}
		if child.height > height {
			height = child.height
		}
		edge, err := ranke.NewEdge(ranke.EdgeConfig{
			Reference: child.claim.ID(), Referenced: child.claim, Type: edgeEntry,
			Fields: map[string]string{"name": e.name, "mode": e.mode},
		})
		if err != nil {
			return made{}, fmt.Errorf("tree %s: entry %q: %w", sha, e.name, err)
		}
		edges = append(edges, edge)
	}

	payload, err := c.git.catFile("tree", sha)
	if err != nil {
		return made{}, fmt.Errorf("tree %s: cat-file: %w", sha, err)
	}
	m, err := c.write(nodeTree, payload, sha, height, edges)
	if err != nil {
		return made{}, fmt.Errorf("tree %s: %w", sha, err)
	}
	c.claims = append(c.claims, m.claim)
	c.bySha[sha] = m
	return m, nil
}

// blob converts one git blob. Content is the file's bytes exactly, which is also
// what makes an unchanged file share one content_hash no matter how many trees,
// or how many commits, cite it.
func (c *converter) blob(sha string) (made, error) {
	if m, ok := c.bySha[sha]; ok {
		return m, nil
	}
	payload, err := c.git.catFile("blob", sha)
	if err != nil {
		return made{}, fmt.Errorf("blob %s: cat-file: %w", sha, err)
	}
	m, err := c.write(nodeBlob, payload, sha, 0, nil)
	if err != nil {
		return made{}, fmt.Errorf("blob %s: %w", sha, err)
	}
	c.claims = append(c.claims, m.claim)
	c.bySha[sha] = m
	return m, nil
}

// write builds and signs one claim: payload as external content (so identical
// bytes anywhere in the walk share one content_hash), its git sha as a lookup
// field, and edges already resolved. height is one past the tallest thing cited.
func (c *converter) write(typ string, payload []byte, gitSha string, childHeight uint64, edges []ranke.Edge) (made, error) {
	id, err := ranke.HashContent(payload)
	if err != nil {
		return made{}, fmt.Errorf("hash content: %w", err)
	}
	if err := c.u.PutContents(c.ctx, []ranke.ContentBlob{{Hash: id, Content: payload}}); err != nil {
		return made{}, fmt.Errorf("store content: %w", err)
	}
	claim, err := ranke.NewClaim(typ, c.contributor).
		WithExternalContent(id, uint64(len(payload))).
		WithEncoding(ranke.EncodingOctetStream).
		WithField(gitShaField, gitSha).
		WithCreatedAt(c.at).
		WithHeight(childHeight + 1).
		WithEdges(edges...).
		Sign(c.signer)
	if err != nil {
		return made{}, err
	}
	return made{claim: claim, height: childHeight + 1}, nil
}

// claimsToGit restores claims — in the dependency order gitToClaims returns them,
// every child before the parent that cites it — into dest, replaying each one's
// raw content through git's own object writer. Nothing here reads an edge: the
// content alone is what git needs back (-> DESIGN.md, "content is git's job").
func claimsToGit(ctx context.Context, dest gitRepo, u ranke.Universe, claims []ranke.Claim) (string, error) {
	var commitSha string
	for _, claim := range claims {
		var kind string
		switch claim.Node().Type() {
		case nodeBlob:
			kind = "blob"
		case nodeTree:
			kind = "tree"
		case nodeCommit:
			kind = "commit"
		default:
			return "", fmt.Errorf("restore: claim %s has unexpected type %q", claim.ID(), claim.Node().Type())
		}
		r, err := claim.GetContent(ctx, u)
		if err != nil {
			return "", fmt.Errorf("restore: claim %s: content: %w", claim.ID(), err)
		}
		payload, err := io.ReadAll(r)
		if err != nil {
			return "", fmt.Errorf("restore: claim %s: read content: %w", claim.ID(), err)
		}
		sha, err := dest.hashObjectWrite(kind, payload)
		if err != nil {
			return "", fmt.Errorf("restore: claim %s: write %s: %w", claim.ID(), kind, err)
		}
		if kind == "commit" {
			commitSha = sha
		}
	}
	if commitSha == "" {
		return "", fmt.Errorf("restore: no commit claim in the set")
	}
	return commitSha, nil
}
