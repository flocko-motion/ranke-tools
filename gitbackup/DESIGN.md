# gitbackup — design notes

## Use case

Git is not immutable — a force-push or a squash can erase history a project once
depended on. `gitbackup` archives git state into a Ranke-Graph so it survives that,
in two modes that share one mechanism:

- **snapshot** — the exact source an artifact was built from: one commit's tree
  (optionally narrowed to a list of paths, for a monorepo), byte-exact, restorable
  as that one commit but not as a working clone (no `.git`, no history, no refs).
- **backup** — a reconstructible clone: every commit reachable from a list of
  branches and tags, chained to its parents, restorable as a real, walkable git
  repository with the same branches and tags back.

Snapshot is a scope-limited subset of backup, not a different shape: same claim
types, same content strategy, same dedup mechanism. Backup walks every commit
reachable from every given ref and follows parent edges; snapshot walks exactly
one commit, follows none, and may narrow to a path subset besides.

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
- **`source/tag`** — one claim per *annotated* tag object (git's fourth object
  kind), carried exactly like commit/tree/blob: raw bytes, external, byte-exact.
  A *lightweight* tag has no such object — it's just a name pointing at a commit,
  so it never gets one of these.
- **`source/ref`** — one claim per branch or tag name, as it resolved at backup
  time. Small inline content (the name itself), `fields: {name, kind}`, and one
  `derivation/points_at` edge to what it names — the commit directly for a branch
  or a lightweight tag, the `source/tag` claim for an annotated one. This is what
  lets a restore recreate the same branches and tags, not just the same objects.

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
- `source/ref` → what it names: a `derivation/points_at` edge, to a commit
  directly or, for an annotated tag, to its `source/tag` claim.
- `source/commit`/`source/tag` → `entity/repository`/`entity/project`: intended
  to be a `relation/*`-class edge (e.g. `relation/snapshot_of`), present on
  **every** run once phase 2's crif exists — a claim can't grow new edges after
  signing, so this is how a later run would re-establish "this documents that
  entity" against one only created once. **Not built yet** — see Entities below.

## Entities

`entity/repository` and `entity/project` are the stable, abstract things — "this
repo", "this project" — as opposed to the concrete captured material. Each needs a
path back to a source (`D1`), satisfied once, at creation:

- Every run today mints both fresh, `derivation/input`-anchored to that run's
  primary commit — there is no server yet to query against for an existing one
  (phase 2). Eventually: `crif` = query first (by a stable field, e.g. `url` for
  the repo, `name` for the project), reuse the id if found, mint only if not —
  two independent runs won't converge on the same entity id by content-addressing
  alone (`created_at` differs), so this has to stay an explicit lookup.
- A project's "lives in this repo" fact is a `relation/*` edge directly on the
  `entity/project` claim, pointing at the repo entity — a plain binary fact needs
  no reified `relation/*` node; reification is for genuine n:n relations between
  entities (foundation paper §Relations).

## Dedup within one run

`bySha` memoises every commit, tree, blob, and tag by git sha, so
a commit reachable from two refs, an unchanged subtree, or a repeated blob becomes
one claim, cited more than once rather than rebuilt — this part is built and
tested (`TestRoundTripDedupesRepeatedBlobs`, `TestBackupRoundTripIsByteExact`).

## Dedup across separate runs (phase 2, not built yet)

`content_hash` is the reuse key at every level — `source/commit` and `source/tree`
get one from their external raw-bytes content the same way `source/blob` always
has, and unlike a claim's own id, `content_hash` is time-independent (no
`created_at` in it), so it's stable across separate runs, unlike `bySha`'s
in-memory map above which starts empty every run. The intent: before minting
anything, pre-fetch a `content_hash → id` map by walking from the repo entity —
follow every `relation/snapshot_of` edge back to a `source/commit`, then its trees
and blobs — scoped to this repo's own history on purpose, not global, so a rerun's
lookups stay cheap and referencing stays inside claims this tool's own account can
already read. Needs a live archive to query against, so it waits on phase 2.

## Not yet decided

- The RQL query shape for the repo-entity pre-fetch walk (phase 2).
- CLI flag finalization for `snapshot`/`backup` themselves — both still say "not
  yet implemented"; only the conversion functions and `demo` are wired up so far.
- Whether `entity/artifact` (an artifact as its own stable, referenceable thing —
  D1-anchored to a `derivation/build` citing the snapshot) is worth adding now or
  only once something actually needs to query artifacts as things across builds.
- Encoding a submodule (gitlink, mode 160000) — currently refused outright
  (`TestSubmoduleIsRefused`).
