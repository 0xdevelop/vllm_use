# Web admin build

The checked-in `dist` is an intentionally minimal embed bootstrap, not a build
of the React admin UI. It keeps the Go embed and binary build valid before web
dependencies are installed.

From this directory, run `bun install` to generate the genuine `bun.lock`, then
run `bun run test`, `bun run lint`, and `bun run build`. The Vite build replaces
`dist` with assets generated from the current `src` tree. Do not hand-edit or
fabricate the resolver lock or generated production assets.
