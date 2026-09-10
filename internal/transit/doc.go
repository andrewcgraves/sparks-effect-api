// Package transit is the API's domain: scenarios, services, the Repository
// seam, and TransitGraph compilation from seed and authored models.
//
// This package does not compute isochrones. A compile job produces a
// TransitGraph; the routing worker in a separate repository plots isochrones
// over it. Seeded compile jobs run Compile (calibrated segment times) via
// CompileSeededScenario. See CONTEXT.md.
//
// CompilableFromUserService is the live CompilableService adapter. The seeded
// Service model has a second, unexported physics adapter, kept as proof the
// seam generalises and as option value for a future seeded-physics path. It
// is not what production compile jobs run.
package transit
