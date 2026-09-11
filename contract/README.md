# sparks-effect-contract

Shared Go types for the API↔worker boundary: the queue `routing` message, the
`transit` graph that travels inside it, and the worker-store HTTP envelope.

This tree is the source until it is copied to the dedicated
[`sparks-effect-contract`](https://github.com/andrewcgraves/sparks-effect-contract)
repository and tagged. The API vendors it with:

```
replace github.com/andrewcgraves/sparks-effect-contract => ./contract
```

That replace is the bootstrap. Once the dedicated repo has a tagged release,
the replace drops and both consumers `go get` a version.

The routing-worker companion — import this module, delete its hand copies,
retire its `check-contract` for these types — is a follow-up. This environment
cannot write that repository. Until the worker imports a tagged module,
consumers still run `check-contract` against the API's
`internal/routing/testdata/message.golden.json` and
`internal/handler/testdata/worker-store.golden.json` (the paths the worker curls).

`geo`, `config`, and `logger` stay duplicated in each consumer. They are the
weakest members of the original inventory; shipping the three packages that
cross the wire proves the mechanism.

`SchemaVersion` still exists so the two sides can run different *module*
versions.

Stdlib only. No third-party requires.
