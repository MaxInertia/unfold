# GoLand Plugin Guidelines

## Version bumping

Whenever you make a change to this plugin (source under `src/`, `build.gradle.kts`,
`plugin.xml`, or anything that affects the built artifact), bump the version in
`build.gradle.kts` (the `version = "..."` line) **before** rebuilding.

- Use semantic versioning (`MAJOR.MINOR.PATCH`).
- Default to a **patch** bump for fixes and small changes (e.g. `0.1.0` → `0.1.1`).
- Use a **minor** bump for new features, and a **major** bump for breaking changes.

The version is the single source of truth at `build.gradle.kts:9`; the built artifact
(`build/distributions/unfold-goland-<version>.zip`) is named from it, so bumping it keeps
each build distinguishable and lets GoLand recognize the install as an upgrade.
