// package: main / gitbackup
// type:    logic
// job:     converts git state to Ranke claims and back, byte-exact (-> DESIGN.md)
// limits:  local only — no ranke-db, no network (-> gitobj.go for raw git access)
package main

import (
	"context"
	"crypto"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/flocko-motion/ranke-go"
)

// The claim types this conversion produces.
const (
	nodeCommit     = "source/commit"
	nodeTree       = "source/tree"
	nodeBlob       = "source/blob"
	nodeTag        = "source/tag" // an annotated tag object; a lightweight tag has none
	nodeRef        = "source/ref" // a branch or tag name, as it resolved at backup time
	nodeRepository = "entity/repository"
	nodeProject    = "entity/project"
)

// The structural and semantic edges beyond the automatic contributor edge.
const (
	edgeTree     = "derivation/tree"      // commit -> its root tree
	edgeEntry    = "derivation/entry"     // tree -> one entry; fields: name, mode
	edgeParent   = "derivation/parent"    // commit -> a git parent (backup only)
	edgePointsAt = "derivation/points_at" // ref -> a commit or tag object; tag -> its commit
	edgeInput    = "derivation/input"     // entity -> its founding commit (D1)
	edgeHostedIn = "relation/hosted_in"   // project -> repository
)

// Claim lookup keys, alongside content_hash (-> DESIGN.md).
const (
	gitShaField    = "git_sha"
	parentShaField = "parent_git_sha"
)

// made pairs a signed claim with the height a citer must climb past (§4.1).
type made struct {
	claim  ranke.Claim
	height uint64
}

// converter walks git objects bottom-up, memoising by git sha so a repeated
// blob, tree, or commit becomes one claim, cited more than once.
type converter struct {
	ctx           context.Context
	git           gitRepo
	u             ranke.Universe
	contributor   ranke.Contributor
	signer        crypto.Signer
	at            time.Time
	bySha         map[string]made
	claims        []ranke.Claim // build order: every child before its citer
	followParents bool          // backup's full history vs. snapshot's one commit
	scope         []string      // snapshot only; nil = the whole tree
	scopeSeen     map[string]bool
}

// resolveCommit resolves ref — a tag, a branch, or a raw sha — to the commit
// it names, peeling an annotated tag to what it points at.
func resolveCommit(g gitRepo, ref string) (string, error) {
	sha, err := g.revParse(ref + "^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", ref, err)
	}
	return sha, nil
}

// normalizeScope strips leading/trailing slashes, so "services/api/" and
// "/services/api" match the same paths ls-tree reports.
func normalizeScope(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = strings.Trim(p, "/")
	}
	return out
}

// scopeRelation is how one tree path relates to a requested scope.
type scopeRelation int

const (
	scopeOut    scopeRelation = iota // excluded: no claim, no edge
	scopeToward                      // an ancestor of a scoped path; must recurse to find it
	scopeIn                          // the scoped path, or already inside it: capture in full
)

// scopeMatch classifies path against scope (empty = the whole tree).
// "toward" recurses past a tree without capturing every sibling — its raw
// content still holds them all, since content is git's job (-> DESIGN.md).
func scopeMatch(scope []string, path string) scopeRelation {
	if len(scope) == 0 {
		return scopeIn
	}
	for _, s := range scope {
		if path == s || strings.HasPrefix(path, s+"/") {
			return scopeIn
		}
		if path == "" || s == path || strings.HasPrefix(s, path+"/") {
			return scopeToward
		}
	}
	return scopeOut
}

// gitToClaims converts one commit: ref resolved, no parent history, scope
// optional — snapshot's shape. u is a local scratch store, not ranke-db
// (phase 2).
func gitToClaims(
	ctx context.Context, g gitRepo, ref string, scope []string, u ranke.Universe,
	contributor ranke.Contributor, signer crypto.Signer, repoURL, project string,
) ([]ranke.Claim, error) {
	sha, err := resolveCommit(g, ref)
	if err != nil {
		return nil, err
	}
	c := &converter{
		ctx: ctx, git: g, u: u, contributor: contributor, signer: signer,
		at: time.Now().UTC(), bySha: map[string]made{},
		scope: normalizeScope(scope), scopeSeen: map[string]bool{},
	}
	commit, err := c.commit(sha)
	if err != nil {
		return nil, err
	}
	for _, s := range c.scope {
		if !c.scopeSeen[s] {
			return nil, fmt.Errorf("scope path %q not found in commit %s", s, sha)
		}
	}
	return c.finish(commit, repoURL, project)
}

// refSpec names one ref to archive: a branch or a tag, by its short name.
type refSpec struct {
	kind string // "branch" or "tag"
	name string
}

// fullRef is the ref's fully-qualified name, what git's own plumbing wants.
func (r refSpec) fullRef() string {
	if r.kind == "tag" {
		return "refs/tags/" + r.name
	}
	return "refs/heads/" + r.name
}

// backupToClaims converts every commit reachable from refs, chained to their
// parents, each ref its own source/ref claim (-> DESIGN.md, snapshot vs. backup).
func backupToClaims(
	ctx context.Context, g gitRepo, refs []refSpec, u ranke.Universe,
	contributor ranke.Contributor, signer crypto.Signer, repoURL, project string,
) ([]ranke.Claim, error) {
	if len(refs) == 0 {
		return nil, fmt.Errorf("backup: no refs given")
	}
	c := &converter{
		ctx: ctx, git: g, u: u, contributor: contributor, signer: signer,
		at: time.Now().UTC(), bySha: map[string]made{}, followParents: true,
	}
	var primary made
	for i, r := range refs {
		result, err := c.ref(g, r)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			primary = result.commit
		}
	}
	return c.finish(primary, repoURL, project)
}

// finish adds the repository and project entities, anchored to commit, and
// returns everything built.
func (c *converter) finish(commit made, repoURL, project string) ([]ranke.Claim, error) {
	repo, err := c.repository(repoURL, commit)
	if err != nil {
		return nil, err
	}
	if _, err := c.project(project, commit, repo); err != nil {
		return nil, err
	}
	return c.claims, nil
}

// commit converts one commit, memoised by sha: parents first when
// followParents is set, then its tree, then itself. Snapshot mode still
// records a parent's sha as a field, uncaptured but honest (-> DESIGN.md).
func (c *converter) commit(sha string) (made, error) {
	if m, ok := c.bySha[sha]; ok {
		return m, nil
	}
	parents, err := c.git.commitParents(sha)
	if err != nil {
		return made{}, fmt.Errorf("commit %s: parents: %w", sha, err)
	}
	fields := map[string]string{gitShaField: sha}
	if len(parents) > 0 {
		fields[parentShaField] = strings.Join(parents, ",")
	}

	var edges []ranke.Edge
	var height uint64
	if c.followParents {
		for _, p := range parents {
			pm, err := c.commit(p)
			if err != nil {
				return made{}, err
			}
			edge, err := ranke.NewEdge(ranke.EdgeConfig{
				Reference: pm.claim.ID(), Referenced: pm.claim, Type: edgeParent,
			})
			if err != nil {
				return made{}, fmt.Errorf("commit %s: parent edge: %w", sha, err)
			}
			edges = append(edges, edge)
			if pm.height > height {
				height = pm.height
			}
		}
	}

	treeSha, err := c.git.commitTree(sha)
	if err != nil {
		return made{}, fmt.Errorf("commit %s: tree: %w", sha, err)
	}
	root, err := c.tree(treeSha, "")
	if err != nil {
		return made{}, err
	}
	treeEdge, err := ranke.NewEdge(ranke.EdgeConfig{
		Reference: root.claim.ID(), Referenced: root.claim, Type: edgeTree,
	})
	if err != nil {
		return made{}, fmt.Errorf("commit %s: root edge: %w", sha, err)
	}
	edges = append(edges, treeEdge)
	if root.height > height {
		height = root.height
	}

	payload, err := c.git.catFile("commit", sha)
	if err != nil {
		return made{}, fmt.Errorf("commit %s: cat-file: %w", sha, err)
	}
	m, err := c.write(nodeCommit, payload, fields, height, edges)
	if err != nil {
		return made{}, fmt.Errorf("commit %s: %w", sha, err)
	}
	c.claims = append(c.claims, m.claim)
	c.bySha[sha] = m
	return m, nil
}

// tree converts one git tree: entries first (scope permitting), then itself,
// citing each by name and mode. path is relative to the commit root ("" at
// the top).
func (c *converter) tree(sha, path string) (made, error) {
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
		entryPath := e.name
		if path != "" {
			entryPath = path + "/" + e.name
		}
		relation := scopeMatch(c.scope, entryPath)
		if relation == scopeOut {
			continue
		}
		if relation == scopeIn {
			c.markScopeSeen(entryPath)
		}

		var child made
		switch e.typ {
		case "blob":
			child, err = c.blob(e.sha)
		case "tree":
			child, err = c.tree(e.sha, entryPath)
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
	m, err := c.write(nodeTree, payload, map[string]string{gitShaField: sha}, height, edges)
	if err != nil {
		return made{}, fmt.Errorf("tree %s: %w", sha, err)
	}
	c.claims = append(c.claims, m.claim)
	c.bySha[sha] = m
	return m, nil
}

// markScopeSeen records that entryPath satisfied one or more requested scope
// paths, so gitToClaims can refuse a scope naming something that was never
// actually reached rather than silently archiving nothing for it.
func (c *converter) markScopeSeen(entryPath string) {
	for _, s := range c.scope {
		if entryPath == s || strings.HasPrefix(entryPath, s+"/") {
			c.scopeSeen[s] = true
		}
	}
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
	m, err := c.write(nodeBlob, payload, map[string]string{gitShaField: sha}, 0, nil)
	if err != nil {
		return made{}, fmt.Errorf("blob %s: %w", sha, err)
	}
	c.claims = append(c.claims, m.claim)
	c.bySha[sha] = m
	return m, nil
}

// ref resolves and converts one branch or tag: an annotated tag gets its own
// source/tag claim, a lightweight tag or a branch points at the commit
// directly. Either way the ref itself becomes a source/ref claim.
func (c *converter) ref(g gitRepo, r refSpec) (struct{ ref, commit made }, error) {
	full := r.fullRef()
	typ, err := g.catFileType(full)
	if err != nil {
		return struct{ ref, commit made }{}, fmt.Errorf("ref %s: %w", full, err)
	}
	commitSha, err := resolveCommit(g, full)
	if err != nil {
		return struct{ ref, commit made }{}, fmt.Errorf("ref %s: %w", full, err)
	}
	commit, err := c.commit(commitSha)
	if err != nil {
		return struct{ ref, commit made }{}, err
	}

	target := commit
	if typ == "tag" {
		tagSha, err := g.revParse(full)
		if err != nil {
			return struct{ ref, commit made }{}, fmt.Errorf("ref %s: %w", full, err)
		}
		if target, err = c.tag(tagSha, commit); err != nil {
			return struct{ ref, commit made }{}, err
		}
	}

	edge, err := ranke.NewEdge(ranke.EdgeConfig{
		Reference: target.claim.ID(), Referenced: target.claim, Type: edgePointsAt,
	})
	if err != nil {
		return struct{ ref, commit made }{}, fmt.Errorf("ref %s: points_at edge: %w", full, err)
	}
	m, err := c.writeFact(nodeRef, []byte(r.name), map[string]string{"name": r.name, "kind": r.kind}, target.height, []ranke.Edge{edge})
	if err != nil {
		return struct{ ref, commit made }{}, fmt.Errorf("ref %s: %w", full, err)
	}
	c.claims = append(c.claims, m.claim)
	return struct{ ref, commit made }{ref: m, commit: commit}, nil
}

// tag converts one annotated tag object, memoised by sha — carried the same
// way as commit/tree/blob: raw bytes, external, byte-exact.
func (c *converter) tag(sha string, commit made) (made, error) {
	if m, ok := c.bySha[sha]; ok {
		return m, nil
	}
	edge, err := ranke.NewEdge(ranke.EdgeConfig{
		Reference: commit.claim.ID(), Referenced: commit.claim, Type: edgePointsAt,
	})
	if err != nil {
		return made{}, fmt.Errorf("tag %s: points_at edge: %w", sha, err)
	}
	payload, err := c.git.catFile("tag", sha)
	if err != nil {
		return made{}, fmt.Errorf("tag %s: cat-file: %w", sha, err)
	}
	m, err := c.write(nodeTag, payload, map[string]string{gitShaField: sha}, commit.height, []ranke.Edge{edge})
	if err != nil {
		return made{}, fmt.Errorf("tag %s: %w", sha, err)
	}
	c.claims = append(c.claims, m.claim)
	c.bySha[sha] = m
	return m, nil
}

// write builds one git-object claim: payload as external content, fields for
// lookup (at least git_sha), edges already resolved.
func (c *converter) write(typ string, payload []byte, fields map[string]string, childHeight uint64, edges []ranke.Edge) (made, error) {
	id, err := ranke.HashContent(payload)
	if err != nil {
		return made{}, fmt.Errorf("hash content: %w", err)
	}
	if err := c.u.PutContents(c.ctx, []ranke.ContentBlob{{Hash: id, Content: payload}}); err != nil {
		return made{}, fmt.Errorf("store content: %w", err)
	}
	b := ranke.NewClaim(typ, c.contributor).
		WithExternalContent(id, uint64(len(payload))).
		WithEncoding(ranke.EncodingOctetStream).
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

// repository builds entity/repository, D1-anchored to the commit that first
// evidenced it — its url is what a restore needs back to reconfigure origin.
func (c *converter) repository(repoURL string, commit made) (made, error) {
	input, err := ranke.NewEdge(ranke.EdgeConfig{
		Reference: commit.claim.ID(), Referenced: commit.claim, Type: edgeInput,
	})
	if err != nil {
		return made{}, fmt.Errorf("repository: input edge: %w", err)
	}
	m, err := c.writeFact(nodeRepository, []byte(repoURL), map[string]string{"url": repoURL}, commit.height, []ranke.Edge{input})
	if err != nil {
		return made{}, fmt.Errorf("repository: %w", err)
	}
	c.claims = append(c.claims, m.claim)
	return m, nil
}

// project builds entity/project: D1-anchored like repository, plus a
// relation/hosted_in edge — a binary fact needs no reified node (-> DESIGN.md).
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
	m, err := c.writeFact(nodeProject, []byte(name), map[string]string{"name": name}, height, []ranke.Edge{input, hostedIn})
	if err != nil {
		return made{}, fmt.Errorf("project: %w", err)
	}
	c.claims = append(c.claims, m.claim)
	return m, nil
}

// writeFact builds one small claim with no git object of its own — an entity
// or a ref: inline content, fields for a later crif lookup (phase 2).
func (c *converter) writeFact(typ string, content []byte, fields map[string]string, childHeight uint64, edges []ranke.Edge) (made, error) {
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

// claimsToGit restores claims into dest: objects replayed through git's own
// writer, then every ref recreated from its points_at edge, then origin from
// the repository entity's url (-> DESIGN.md). commitSha is the last commit
// written; refs is what tells branches and tags apart in a backup.
func claimsToGit(ctx context.Context, dest gitRepo, u ranke.Universe, claims []ranke.Claim) (commitSha string, refs map[string]string, err error) {
	byID := make(map[string]ranke.Claim, len(claims))
	for _, claim := range claims {
		byID[claim.ID().String()] = claim
	}

	type pendingRef struct{ kind, name, targetID string }
	var pending []pendingRef
	var repoURL string

	for _, claim := range claims {
		typ := claim.Node().Type()
		switch typ {
		case nodeRepository:
			if url, err := claim.Node().GetField("url"); err == nil {
				repoURL = url
			}
			continue
		case nodeProject:
			continue
		case nodeRef:
			name, _ := claim.Node().GetField("name")
			kind, _ := claim.Node().GetField("kind")
			points := claim.Edges(ranke.EdgeFilterType{Type: edgePointsAt})
			if len(points) != 1 {
				return "", nil, fmt.Errorf("restore: ref %q has %d points_at edge(s), want 1", name, len(points))
			}
			pending = append(pending, pendingRef{kind: kind, name: name, targetID: points[0].Reference().String()})
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
		case nodeTag:
			kind = "tag"
		default:
			return "", nil, fmt.Errorf("restore: claim %s has unexpected type %q", claim.ID(), typ)
		}
		r, err := claim.GetContent(ctx, u)
		if err != nil {
			return "", nil, fmt.Errorf("restore: claim %s: content: %w", claim.ID(), err)
		}
		payload, err := io.ReadAll(r)
		if err != nil {
			return "", nil, fmt.Errorf("restore: claim %s: read content: %w", claim.ID(), err)
		}
		sha, err := dest.hashObjectWrite(kind, payload)
		if err != nil {
			return "", nil, fmt.Errorf("restore: claim %s: write %s: %w", claim.ID(), kind, err)
		}
		if kind == "commit" {
			commitSha = sha
		}
	}
	if commitSha == "" && len(pending) == 0 {
		return "", nil, fmt.Errorf("restore: no commit claim in the set")
	}

	refs = make(map[string]string, len(pending))
	for _, p := range pending {
		target, ok := byID[p.targetID]
		if !ok {
			return "", nil, fmt.Errorf("restore: ref %q points at a claim not in the set", p.name)
		}
		sha, err := target.Node().GetField(gitShaField)
		if err != nil {
			return "", nil, fmt.Errorf("restore: ref %q: target has no %s: %w", p.name, gitShaField, err)
		}
		full := "refs/heads/" + p.name
		if p.kind == "tag" {
			full = "refs/tags/" + p.name
		}
		if _, err := dest.run("update-ref", full, sha); err != nil {
			return "", nil, fmt.Errorf("restore: create ref %s: %w", full, err)
		}
		refs[p.name] = sha
	}

	if repoURL != "" {
		if _, err := dest.run("remote", "add", "origin", repoURL); err != nil {
			return "", nil, fmt.Errorf("restore: configure origin: %w", err)
		}
	}
	return commitSha, refs, nil
}
