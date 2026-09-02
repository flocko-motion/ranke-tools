# ranke-tools — Agent Instructions

## Changelog

`CHANGELOG.md` records what each release changed for someone who depends on
this repository. A change earns an entry when it alters what the repo requires,
provides, or removes; rewording does not. Write the entry under `## Unreleased`
in the same change, and summarise: one entry per change that matters to a
reader, not a log of every edit it took to get there.

`make release <bump>` stamps that section with the version it cuts, leaves a
fresh `## Unreleased` behind, and commits it on the branch being released, so
a version heading is never written by hand. A release whose `## Unreleased`
section is empty is refused, since it would record nothing. The stamping lives
in ranke-graph's shared `release-cycle.sh`, which this repo caches under
`bin/`, and where `CHANGELOG.md` is missing the first release writes it.
