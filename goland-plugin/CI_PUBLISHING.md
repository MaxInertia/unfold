# CI publishing — auto-release to the JetBrains Marketplace on merge

How to set up a GitHub Action that signs and publishes the plugin whenever a
merge to `main` touches `goland-plugin/` **and** the version in
`build.gradle.kts` is one the Marketplace hasn't seen yet. Merges that change
plugin code without bumping the version build nothing and publish nothing —
the version check is the release trigger, so "cut a release" stays an explicit
act (bump + change-notes in the PR), while docs-only merges are cheap no-ops.

This automates the manual flow in `scaffold-notes.md` (`signPlugin` /
`publishPlugin` with env vars exported locally).

## 1. Add the repository secrets

Four secrets, same material the local flow uses. From the repo root:

```bash
gh secret set CERTIFICATE_CHAIN     < ~/projects/unfold/chain.crt
gh secret set PRIVATE_KEY           < ~/projects/unfold/private.pem
gh secret set PRIVATE_KEY_PASSWORD  # paste interactively
gh secret set PUBLISH_TOKEN         # paste interactively — plugins.jetbrains.com → My Tokens
```

Note the first two hold the **contents** of the cert/key. The Gradle config
(`build.gradle.kts` → `signing {}`) expects file *paths*
(`CERTIFICATE_CHAIN_FILE` / `PRIVATE_KEY_FILE`), so the workflow writes the
secrets to files under `$RUNNER_TEMP` before invoking Gradle.

## 2. The workflow

Create `.github/workflows/publish-plugin.yml` (paths are repo-root relative,
so this lives in the repo's top-level `.github/`, not under `goland-plugin/`):

```yaml
name: Publish plugin

on:
  push:
    branches: [main]
    paths:
      - "goland-plugin/**"

# Never let two publishes race; queue instead of cancelling so a quick
# follow-up merge can't kill an in-flight upload.
concurrency:
  group: publish-plugin
  cancel-in-progress: false

jobs:
  publish:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: goland-plugin
    steps:
      - uses: actions/checkout@v4

      # Publish only when the local version isn't already on the Marketplace.
      # (Plugin id 32477 = "Inline Call Unfold".)
      - name: Check whether this version needs publishing
        id: gate
        run: |
          local_version=$(sed -n 's/^version = "\(.*\)"$/\1/p' build.gradle.kts)
          published=$(curl -fsSL "https://plugins.jetbrains.com/api/plugins/32477/updates?size=10" \
            | jq -r '.[].version')
          echo "local=$local_version published=[$(echo $published | tr '\n' ' ')]"
          if echo "$published" | grep -qx "$local_version"; then
            echo "publish=false" >> "$GITHUB_OUTPUT"
            echo "Version $local_version already on the Marketplace — skipping."
          else
            echo "publish=true" >> "$GITHUB_OUTPUT"
          fi

      - uses: actions/setup-java@v4
        if: steps.gate.outputs.publish == 'true'
        with:
          distribution: temurin
          java-version: 21

      - uses: gradle/actions/setup-gradle@v4
        if: steps.gate.outputs.publish == 'true'

      - name: Write signing material
        if: steps.gate.outputs.publish == 'true'
        run: |
          printf '%s' "$CERTIFICATE_CHAIN" > "$RUNNER_TEMP/chain.crt"
          printf '%s' "$PRIVATE_KEY" > "$RUNNER_TEMP/private.pem"
        env:
          CERTIFICATE_CHAIN: ${{ secrets.CERTIFICATE_CHAIN }}
          PRIVATE_KEY: ${{ secrets.PRIVATE_KEY }}

      - name: Sign and publish
        if: steps.gate.outputs.publish == 'true'
        run: ./gradlew signPlugin publishPlugin
        env:
          CERTIFICATE_CHAIN_FILE: ${{ runner.temp }}/chain.crt
          PRIVATE_KEY_FILE: ${{ runner.temp }}/private.pem
          PRIVATE_KEY_PASSWORD: ${{ secrets.PRIVATE_KEY_PASSWORD }}
          PUBLISH_TOKEN: ${{ secrets.PUBLISH_TOKEN }}
```

## Release flow once this is in place

1. In the feature PR (or a follow-up), bump `version` in `build.gradle.kts`
   and add the matching `<change-notes>` entry in `plugin.xml` — the existing
   convention from `CLAUDE.md`, one bump per PR.
2. Merge to `main`. The action sees the new version, signs, and uploads.
3. Uploaded versions still pass through JetBrains moderation before going
   live on the listing (usually fast after the first release's approval).

## Gotchas

- **The version gate is the on/off switch.** Forgetting the bump means no
  release; re-merging the same version is safely skipped, not an error.
- **Marketplace API list size:** the gate fetches the last 10 updates. If a
  version older than that were ever re-used the gate would miss it — don't
  re-use version numbers (the Marketplace rejects duplicates anyway).
- **Build compatibility:** CI compiles against the GoLand version pinned in
  `build.gradle.kts` (`goland("2025.3.1.1")`); the first run downloads it
  (~1 GB, cached by `setup-gradle` afterwards).
- **Rotating the token/cert:** update the repo secrets; nothing in the
  workflow file changes.
