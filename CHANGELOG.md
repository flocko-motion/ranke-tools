# Changelog

What each release changed for someone who depends on this repository: the tools
it builds, how they are released, and what the build requires. A change earns an
entry when it alters what the repo requires, provides, or removes; rewording
does not.

## Unreleased

**The release cycle is ranke-graph's shared script, cached rather than
vendored.** `scripts/release.sh` is gone. `make release` fetches
`release-cycle.sh` from ranke-graph into `bin/`, which is gitignored, and runs it
from there, so the git mechanics of a release are written once and this
repository cannot drift from them. `make upgrade` refreshes the cached copy.

**`make release` refuses a dirty tree or a missing bump word before it builds.**
Both checks are instant where the quality gate is not, so a release that was
going to fail on either no longer costs a build first.

**The build fetches ranke-graph from the rankegraph organisation.**
`RANKE_GRAPH_REPO` pointed at `flocko-motion/ranke-graph`.

**The repository states its licence.** `LICENSE` is Apache 2.0, matching
ranke-go, ranke-ts and ranke-db.
