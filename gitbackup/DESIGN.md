# gitbackup — design notes

## Use case

Git is not immutable — a force-push or a squash can erase history a project once
depended on. `gitbackup` archives git state into a Ranke-Graph so it survives that,
in two modes that share one mechanism:

- **snapshot** — the exact source an artifact was built from: one commit's tree,
  byte-exact, restorable as that one commit but not as a working clone (no `.git`,
  no history, no refs).
- **backup** — a reconstructible clone: every commit reachable from the given refs,
  chained to its parents, restorable as a real, walkable git repository.

Snapshot is a scope-limited subset of backup, not a different shape: same claim
types, same content strategy, same dedup mechanism. Backup walks every commit and
follows parent edges; snapshot walks exactly one commit and follows none.

## Claim shape

All of it stays in the `source` class — capture, not interpretation, all the way
down. Nothing here is a `derivation`.

- **`source/commit`** — one claim per git commit. Content is git's own raw commit
  object bytes, verbatim, stored external (`content_hash`). Fields carry the git
  commit sha for lookup (`git_sha`) and, in snapshot mode, the parent's git sha as
  a plain field even when no parent claim exists to cite (an honest record of what
  wasn't captured, not a broken reference).
- **`source/tree`** — one claim per git tree object (one per directory level,
  nested — never flattened). Content is git's raw tree object bytes, external.
  Cites each entry (blob or subtree) via an edge carrying `fields: {name, mode}` —
  the structural information a tree object encodes, available as edge fields
  independent of what the raw content holds.
- **`source/blob`** — one claim per git blob. Content is the file's bytes exactly —
  already byte-identical to a git blob's payload — external, so identical files
  anywhere in the walk share one `content_hash` regardless of path or commit.

Storing git's raw object bytes (rather than decomposing and re-deriving git's
encoding at restore time) is what makes byte-exact restore trivial and testable:
after restore, recompute the commit's git sha and compare it to the original — one
hash, not a file diff, proving structure, modes, content, message, and timestamps
all matched.

### Edges beyond the tree/blob structure

Edge classes are closed to `derivation`, `relation`, and `contribution` (`V-TYPE`)
— there is no `source/*` edge class, but a `source`-class *node* citing another
source via a `derivation`- or `relation`-class edge is normal (see: an ingestion
worker citing the dump an `.eml` was split from). This design uses:

- `source/commit` → its root `source/tree`: a `derivation`-class edge.
- `source/tree` → each entry: a `derivation`-class edge, `fields: {name, mode}`.
- `source/commit` → its git parent commit(s): a `derivation`-class edge. **Backup
  only** — this is the edge that reconstructs the repo's real commit graph inside
  the archive. Snapshot mode never follows it; the parent sha still rides as a
  plain field (see above).
- `source/commit` → `entity/repository` and `entity/project`: a `relation/*`-class
  edge (e.g. `relation/snapshot_of`), `dir: RelationTo`. Present on **every** run,
  not just the first — a claim can't grow new edges after signing, so this is how
  every later snapshot re-establishes "this documents that entity" against an
  entity that was only created once.

## Entities

`entity/repository` and `entity/project` are the stable, abstract things — "this
repo", "this project" — as opposed to the concrete captured material. Each needs a
path back to a source (`D1`), satisfied once, at creation:

- First run: mint the entity with a `derivation/input` edge to that run's
  `source/commit` — its founding evidence. `crif` = query first (by a stable
  field, e.g. `url` for the repo, `name` for the project), reuse the id if found,
  mint only if not — two independent runs won't converge on the same entity id by
  content-addressing alone (`created_at` differs), so this has to be an explicit
  lookup, not something automatic.
- A project's "lives in this repo" fact is a `relation/*` edge directly on the
  `entity/project` claim, pointing at the repo entity — a plain binary fact needs
  no reified `relation/*` node; reification is for genuine n:n relations between
  entities (foundation paper §Relations).

## Dedup / incremental runs

`content_hash` is the reuse key at every level — `source/commit` and `source/tree`
get one from their external raw-bytes content the same way `source/blob` always
has, and unlike a claim's own id, `content_hash` is time-independent (no
`created_at` in it), so it's stable across separate runs. Before minting anything,
`gitbackup` pre-fetches a `content_hash → id` map by walking from the repo entity:
follow every `relation/snapshot_of` edge back to a `source/commit`, then its trees
and blobs — scoped to this repo's own history on purpose, not global, so a rerun's
lookups stay cheap and referencing stays inside claims this tool's own account can
already read.

The walk (of git's objects, per run) then goes bottom-up — blobs, then trees, then
the commit — checking the map before minting at each step. An unchanged subtree
costs one lookup and reuses everything beneath it; nothing gets rewritten unless it
actually changed.

## Not yet decided

- Exact field names beyond `git_sha` / `url` / `name` (paths, file modes as
  strings vs. ints, encoding declarations).
- The RQL query shape for the repo-entity pre-fetch walk.
- CLI flag finalization (see `main.go` for the current skeleton).
- Whether `entity/artifact` (an artifact as its own stable, referenceable thing —
  D1-anchored to a `derivation/build` citing the snapshot) is worth adding now or
  only once something actually needs to query artifacts as things across builds.
