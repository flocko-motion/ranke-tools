// package: main / gitbackup
// type:    logic
// job:     converts one git commit to Ranke claims and back, byte-exact (-> DESIGN.md)
// limits:  local only — no ranke-db, no network (-> gitobj.go for raw git access)
package main

import (
	"context"
	"crypto"
	"fmt"
	"io"
	"time"

	"github.com/flocko-motion/ranke-go"
)

// The claim types this conversion produces.
const (
	nodeCommit     = "source/commit"
	nodeTree       = "source/tree"
	nodeBlob       = "source/blob"
	nodeRepository = "entity/repository"
	nodeProject    = "entity/project"
)

// The structural and semantic edges beyond the automatic contributor edge.
const (
	edgeTree     = "derivation/tree"    // commit -> its root tree
	edgeEntry    = "derivation/entry"   // tree -> one entry; fields: name, mode
	edgeInput    = "derivation/input"   // entity -> its founding commit (D1)
	edgeHostedIn = "relation/hosted_in" // project -> repository
)

// gitShaField is a claim's lookup key, alongside content_hash (-> DESIGN.md).
const gitShaField = "git_sha"

// made pairs a signed claim with the height a citer must climb past (§4.1).
type made struct {
	claim  ranke.Claim
	height uint64
}

// converter walks one commit's objects bottom-up, memoising by git sha so a
// repeated blob or an unchanged subtree becomes one claim, cited twice.
type converter struct {
	ctx         context.Context
	git         gitRepo
	u           ranke.Universe
	contributor ranke.Contributor
	signer      crypto.Signer
	at          time.Time
	bySha       map[string]made
	claims      []ranke.Claim // build order: every child before its citer
}

// gitToClaims converts one commit: every blob and tree it reaches, nested to
// match git's structure, the commit itself, then the repository and project
// entities it evidences — u is a local scratch store, not ranke-db (phase 2).
func gitToClaims(
	ctx context.Context, g gitRepo, commitSha string, u ranke.Universe,
	contributor ranke.Contributor, signer crypto.Signer, repoURL, project string,
) ([]ranke.Claim, error) {
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

	repo, err := c.repository(repoURL, commit)
	if err != nil {
		return nil, err
	}
	if _, err := c.project(project, commit, repo); err != nil {
		return nil, err
	}
	return c.claims, nil
}

// tree converts one git tree: its entries first (recursively), then the tree
// claim, citing each by a derivation/entry edge carrying its name and mode.
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

// blob converts one git blob. Content is the file's bytes exactly, so an
// unchanged file shares one content_hash no matter how many trees cite it.
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

// write builds one git-object claim: payload as external content, its git sha
// as a lookup field, edges already resolved.
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

// repository builds entity/repository, D1-anchored to the commit that first
// evidenced it — its url is what a restore needs back to reconfigure origin.
func (c *converter) repository(repoURL string, commit made) (made, error) {
	input, err := ranke.NewEdge(ranke.EdgeConfig{
		Reference: commit.claim.ID(), Referenced: commit.claim, Type: edgeInput,
	})
	if err != nil {
		return made{}, fmt.Errorf("repository: input edge: %w", err)
	}
	m, err := c.writeEntity(nodeRepository, []byte(repoURL), map[string]string{"url": repoURL}, commit.height, []ranke.Edge{input})
	if err != nil {
		return made{}, fmt.Errorf("repository: %w", err)
	}
	c.claims = append(c.claims, m.claim)
	return m, nil
}

// project builds entity/project: D1-anchored to the same commit, and pointing
// at the repository via relation/hosted_in — a binary fact needs no reified
// relation node (-> DESIGN.md).
func (c *converter) project(name string, commit, repo made) (made, error) {
	input, err := ranke.NewEdge(ranke.EdgeConfig{
		Reference: commit.claim.ID(), Referenced: commit.claim, Type: edgeInput,
	})
	if err != nil {
		return made{}, fmt.Errorf("project: input edge: %w", err)
	}
	hostedIn, err := ranke.NewEdge(ranke.EdgeConfig{
		Reference: repo.claim.ID(), Referenced: repo.claim, Type: edgeHostedIn,
		RelationDirection: ranke.RelationTo,
	})
	if err != nil {
		return made{}, fmt.Errorf("project: hosted_in edge: %w", err)
	}
	height := commit.height
	if repo.height > height {
		height = repo.height
	}
	m, err := c.writeEntity(nodeProject, []byte(name), map[string]string{"name": name}, height, []ranke.Edge{input, hostedIn})
	if err != nil {
		return made{}, fmt.Errorf("project: %w", err)
	}
	c.claims = append(c.claims, m.claim)
	return m, nil
}

// writeEntity builds one entity claim: small inline content, fields for a
// later crif lookup (phase 2), edges already resolved.
func (c *converter) writeEntity(typ string, content []byte, fields map[string]string, childHeight uint64, edges []ranke.Edge) (made, error) {
	b := ranke.NewClaim(typ, c.contributor).
		WithInlineContent(content).
		WithEncoding(ranke.EncodingPlain).
		WithCreatedAt(c.at).
		WithHeight(childHeight + 1).
		WithEdges(edges...)
	for k, v := range fields {
		b = b.WithField(k, v)
	}
	claim, err := b.Sign(c.signer)
	if err != nil {
		return made{}, err
	}
	return made{claim: claim, height: childHeight + 1}, nil
}

// claimsToGit restores claims into dest: git objects replayed through git's own
// writer in the order gitToClaims returns them (every child before its citer),
// then origin reconfigured from the repository entity's url — everything git
// itself can hold, restored; a plain `git init` + objects would leave origin
// unset (-> DESIGN.md, "content is git's job; edges are Ranke's job").
func claimsToGit(ctx context.Context, dest gitRepo, u ranke.Universe, claims []ranke.Claim) (string, error) {
	var commitSha, repoURL string
	for _, claim := range claims {
		typ := claim.Node().Type()
		if typ == nodeRepository {
			if url, err := claim.Node().GetField("url"); err == nil {
				repoURL = url
			}
			continue
		}
		if typ == nodeProject {
			continue
		}
		var kind string
		switch typ {
		case nodeBlob:
			kind = "blob"
		case nodeTree:
			kind = "tree"
		case nodeCommit:
			kind = "commit"
		default:
			return "", fmt.Errorf("restore: claim %s has unexpected type %q", claim.ID(), typ)
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
	if repoURL != "" {
		if _, err := dest.run("remote", "add", "origin", repoURL); err != nil {
			return "", fmt.Errorf("restore: configure origin: %w", err)
		}
	}
	return commitSha, nil
}
