# ranke-git — design notes

## Use case

Git is not immutable — a force-push or a squash can erase history a project once
depended on. `ranke-git` archives git state into a Ranke-Graph so it survives that,
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

- `crif` (`prepare.go`'s `findOne`): query first, by a stable field (`url` for the
  repo, `name` for the project); reuse the id and height if found, mint fresh —
  `derivation/input`-anchored to that run's primary commit — if not. Two
  independent runs won't converge on the same entity id by content-addressing
  alone (`created_at` differs), so this stays an explicit lookup rather than
  something automatic. Built and verified live: a second run against the same
  repo/project reuses both entities, contributing neither again.
- A project's "lives in this repo" fact is a `relation/*` edge directly on the
  `entity/project` claim, pointing at the repo entity — a plain binary fact needs
  no reified `relation/*` node; reification is for genuine n:n relations between
  entities (foundation paper §Relations).

## Attachments

`ranke-git attach` cites arbitrary content onto an already-archived commit — a
build log, a test report, a scan's output, a platform-specific artifact binary.
The driving case: after release CI, `snapshot` the repo, then `attach` its logs
and its build outputs against the same commit.

Every attachment is a `source` claim, no exceptions, however different its
*content* is from a log. `ranke-git` never parses what it's carrying — the
release-process generator in `ranke-db` classifies a vulnerability *scan* as a
`derivation` (it interprets source code against known CVEs and links the
entities it matched), but that tool knows the semantic content and deliberately
builds that graph; `ranke-git` doesn't, anywhere, and attach is no exception.
A scanner's raw output, attached here, is exactly as uninterpreted as a build
log or a Windows-vs-Linux artifact pair — `source`, all of it, unconditionally.

Two axes, both settable, kept independent:

- `--type` sets the subtype, always assembled as `source/rankegit_<type>` —
  never a bare `--type` string, and never a class other than `source`. The
  `rankegit_` prefix exists because the subtype vocabulary is open
  archive-wide (`V-TYPE`): a bare word like "log" or "advisory" is exactly what
  another tool might mean something else by, and the list of kinds here (logs,
  test results, scan output, per-platform artifacts...) is open-ended by
  design, so there's no fixed enum to validate against instead. `subtypeChars`
  enforces the ADT's own character rule (`checkSubtype`, shared with
  `R-FIELDS`' field-name shape: `[a-z0-9][a-z0-9_]*`) before any network round
  trip, so a bad `--type` fails immediately, not inside the library.
- `--content-type` is the attachment's actual MIME encoding — free-form,
  independent of `--type`, since a `build_log` could be `text/plain` today and
  something else tomorrow.

`--name` is the human title either way — a log's caption or an artifact's
filename, the same field `entity/project` and `source/ref` already use for the
same purpose.

The claim cites its target via `relation/attached_to` (`RelationTo`) — a
relation, not a `derivation/input`: the attachment isn't an interpretation of
the commit's content, it's evidence associated with the same point in time,
the same shape as the release generator's `relation/mentions`. Content is
always external, same as every other git-object claim, so two identical
attachments (a log rerun byte-for-byte) share one `content_hash`. No git repo
is touched — `attach` finds its target purely by querying the branch for the
`source/commit` claim with a matching `git_sha` (`findOne`, the same helper
crif uses), then contributes one claim. `--file` or stdin supplies the bytes.

## Sending content

`WriteClaim` carries only a claim's own record — for external content that's
just `content_hash`/`content_size`, never the bytes. `client.contribute`
separately calls `WriteContent` for every externally-content claim in the
batch (deduped by hash within the batch itself, so a blob two claims share
goes out once), reading the bytes back from the same Universe the build phase
wrote them into. Missing this was a real, confirmed bug for a while: the
claim records reached the server and even satisfied re-run dedup (which only
ever reads the `content_hash` *field*, never fetches the bytes it names), so
everything *looked* correct while every blob's actual content was silently
absent server-side. Caught by fetching a blob's content back after a live
contribute and getting a 404; fixed, and now the same fetch returns the bytes.

## Dedup within one run

`bySha` memoises every commit, tree, blob, and tag by git sha, so
a commit reachable from two refs, an unchanged subtree, or a repeated blob becomes
one claim, cited more than once rather than rebuilt — this part is built and
tested (`TestRoundTripDedupesRepeatedBlobs`, `TestBackupRoundTripIsByteExact`).

## Dedup across separate runs

`content_hash` is the reuse key at every level — `source/commit` and `source/tree`
get one from their external raw-bytes content the same way `source/blob` always
has, and unlike a claim's own id, `content_hash` is time-independent (no
`created_at` in it), so it's stable across separate runs, unlike `bySha`'s
in-memory map above which starts empty every run.

`prepare.go`'s `scanContentHashes` queries every `source/commit`/`tree`/`blob`/`tag`
claim **on the destination branch**, keyed by `content_hash`, before the build
phase mints anything — a flat type filter, not a graph walk from the repo entity
as first sketched: simpler, and equivalent in effect as long as one branch holds
one repo's history, which is this tool's own convention. The trade-off is real
though — a branch shared by more than one repo would pool their content_hashes
together too (harmless dedup, but worth knowing it isn't strictly repo-scoped).
`converter.write` checks this map before minting a git-object claim; `converter`
never even calls `PutContents`/`Sign` for something already found there.

Verified live against a running instance: an unchanged re-run of the same
commit contributes nothing at all ("everything was already archived"); a
one-file change contributes exactly the changed blob, the tree that now cites
it, and the new commit — three claims, not the whole tree again.

## Not yet decided

- Cloning `--repo` directly. `--clone` (an existing local checkout) is the only
  path in today; a bare `--repo` with no `--clone` refuses rather than cloning.
- Enumerating "every branch/tag" for `backup` — today's `--git-branch`/`--git-tag`
  are named explicitly, not discovered from the repo.
- `relation/snapshot_of` (see Edges above): once crif finds an existing entity,
  nothing yet records that *this* run's commit also relates to it — only the
  founding commit's `derivation/input` edge does. Worth adding once something
  needs to walk "every snapshot of this repo," not just "the first one."
- Whether `entity/artifact` (an artifact as its own stable, referenceable thing —
  D1-anchored to a `derivation/build` citing the snapshot) is worth adding now or
  only once something actually needs to query artifacts as things across builds.
  `attach` covers "carry the bytes" today; it doesn't give an artifact its own
  identity to reference from elsewhere.
- Content-hash reuse across separate `attach` runs. Unlike `snapshot`/`backup`,
  `attach` never consults `prepare`'s `knownHashes` — attaching the same log
  twice mints two claims, not one reused. Likely fine (an attachment is tied to
  one run, not expected to repeat the way an unchanged file does), but untested
  either way.
- Encoding a submodule (gitlink, mode 160000) — currently refused outright
  (`TestSubmoduleIsRefused`).
