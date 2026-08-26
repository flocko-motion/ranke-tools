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
