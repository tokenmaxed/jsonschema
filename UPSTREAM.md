# Upstream relationship

This repository is a maintained fork of
[`github.com/santhosh-tekuri/jsonschema`](https://github.com/santhosh-tekuri/jsonschema).
It starts from upstream `boon` commit
[`ec6106e`](https://github.com/santhosh-tekuri/jsonschema/commit/ec6106e5f0a342f025769a4da8e2d56c2dc10321).

The root module path is deliberately
`github.com/tokenmaxed/jsonschema/v6`, so consumers receive the fork without
a `replace` directive. The independently versioned `cmd/jv` module remains
on its upstream module path and is not part of the downstream release line.

## Update procedure

1. Fetch `https://github.com/santhosh-tekuri/jsonschema.git` as the
   `upstream` remote.
2. Create a fresh branch from the selected upstream commit.
3. Reapply the module-path commit, followed by each downstream fix in the order
   listed below.
4. Run the upstream tests with its minimum Go version, race detection, vet,
   lint, and the JSON Schema Test Suite.
5. Compare the complete fork diff before selecting a new immutable revision.

Keep the downstream commit stack rebased rather than merging upstream history
into it, so every divergence remains reviewable.

## Downstream changes

| ID | Change | Upstream status |
| --- | --- | --- |
| B0 | Rename the root module and its imports | Fork baseline |
| F1 | Build meta-resource JSON Pointers in one pass | [Issue #266](https://github.com/santhosh-tekuri/jsonschema/issues/266), [PR #267](https://github.com/santhosh-tekuri/jsonschema/pull/267) |
