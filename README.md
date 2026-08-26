# ranke-tools

A home for standalone tools that write to a running `ranke-db` instance as ordinary
clients — each its own contributor, over the documented REST API, depending only on
`github.com/flocko-motion/ranke-go` to build and sign its claims. Never a sibling path,
never a `replace`, never anything from `ranke-db`'s own module: every tool here is an
external consumer of the public contract, on purpose — that's what makes a breaking
change in `ranke-go` or the REST API show up here as a real, independently-noticed
failure, not something that quietly stays in lockstep because it was developed in the
same repo.

One Go module, one subdirectory per tool.

## Tools

- [`gitbackup`](./gitbackup/) — archives an exact git tree (or a repo's full history) into
  a Ranke-Graph archive, byte-exact and content-deduplicated.

## Running a dev server

`server/run.sh` runs the `ranke-db` release binary `server/.rankedb-version` pins,
against `server/config.json` — an ephemeral, in-memory instance with a throwaway
signing key, the same shape as `ranke-db`'s own `make dev`. Nothing persists between
runs, and it refuses to start a second instance while one is already running.

```sh
server/run.sh          # start (installs the pinned binary first, if needed)
server/stop.sh         # stop it from another shell
make upgrade            # move the pin to ranke-db's latest release and install it
```

The pin only moves via `make upgrade`, never implicitly on a plain `run.sh` — a
deliberate step, analogous to `ranke-db`'s own `make upgrade`, rather than the server
silently changing under you between runs. Run it regularly: if ranke-tools breaks
against the new pin, that's exactly what its tests are for.
