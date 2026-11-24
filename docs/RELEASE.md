# Releasing document-manager

How code gets from a pull request to npm, and what to do when a release goes wrong.
One version number covers the Go module, the `pdf-engine` binaries and all six npm
packages.

## 1. Architecture

```text
 PR ──► ci.yml + pr-title.yml (read-only token, no secrets)
         │  squash-merge by the owner
         ▼
 main ──► ci.yml
      └─► release-please.yml ── keeps one "chore: release x.y.z" PR open
                                 (version bump + CHANGELOG, nothing published)
         │  owner merges the release PR
         ▼
 release.yml
   plan ─► ci (the full ci.yml on this commit, builds the 6 tarballs)
        ─► publish   ⏸ waits for owner approval (environment npm-production)
                       npm trusted publishing (OIDC) + provenance
        ─► github-release  tag vX.Y.Z + GitHub Release (notes, binaries, SHA256SUMS)
```

| Workflow | Runs on | Token permissions |
|---|---|---|
| [`ci.yml`](../.github/workflows/ci.yml) | PRs, pushes to `main`, manual, called by `release.yml` | `contents: read` |
| [`pr-title.yml`](../.github/workflows/pr-title.yml) | PRs (title edits too) | none |
| [`release-please.yml`](../.github/workflows/release-please.yml) | pushes to `main` | write to its own branch and PR, dispatch CI |
| [`release.yml`](../.github/workflows/release.yml) | release PR merged, or manual (prereleases) | `id-token: write` in the `publish` job only |

What CI checks:

| Job | Checks |
|---|---|
| Go lint | `go mod verify`, `go mod tidy -diff`, gofmt, `go vet`, golangci-lint v2.15.0 ([`.golangci.yml`](../.golangci.yml)), govulncheck |
| Go test | `go test -race` with coverage (the engine tests run real PDFs through PDFium) |
| npm (Node 22/24/26) | `npm ci` (lockfile must be in sync), type-check `index.js` against `index.d.ts`, wrapper tests against a freshly built binary |
| Package | cross-compiles 5 targets, builds/packs the 6 packages, checks each tarball's exact file list, executable bit and export map |
| Smoke (5 platforms) | installs the real tarballs into an empty project on native linux-x64, linux-arm64, darwin-arm64, darwin-x64 and win32-x64 runners, runs the engine, compiles a TypeScript consumer |

Pushing a `v*` tag does **not** publish anything. Only `release.yml` creates tags.

## 2. Branches and pull requests

- `main` is protected by a ruleset: changes land through PRs only, with one
  approval, squash or rebase merge, and the CI checks below passing. Only the owner
  can bypass.
- PRs from forks run CI with a read-only token and no secrets. The first run of a
  workflow from an outside contributor needs approval in the Actions tab.
- **PR titles must be [Conventional Commits](https://www.conventionalcommits.org/)**,
  because squash-merging turns the title into the commit Release Please reads:
  `feat:`, `fix:`, `perf:`, `refactor:`, `docs:`, `test:`, `chore:`, `ci:`, `build:`,
  `revert:`, with an optional scope (`fix(engine): ...`) and `!` for breaking changes.
- Do not hand-edit `CHANGELOG.md` in feature PRs. Fix the wording in the release PR.

## 3. Versioning

[Semantic Versioning](https://semver.org). The authoritative version is
[`.release-please-manifest.json`](../.release-please-manifest.json); Release Please
copies it into `npm/pdf-engine/package.json` and `package-lock.json`, and
`release.yml` refuses to run if they disagree. Tags are `vX.Y.Z`.

| PR title | 1.0.0 and later | Before 1.0.0 (now) |
|---|---|---|
| `fix:` / `perf:` | patch | patch |
| `feat:` | minor | minor |
| `feat!:`, `fix!:` or a `BREAKING CHANGE:` footer | major | **minor** (0.x.0) |
| `docs:`, `chore:`, `ci:`, `test:`, `refactor:`, `build:` | no release | no release |

Before 1.0.0 a breaking change bumps the minor version
(`bump-minor-pre-major` in [`release-please-config.json`](../release-please-config.json))
and is listed under ⚠ BREAKING CHANGES. To force a version (for example 1.0.0), add a
commit or PR with the footer `Release-As: 1.0.0`.

Go module users get the same tags: `go get github.com/ifaridaiman/document-manager@vX.Y.Z`.

## 4. npm trusted publishing (one-time setup on npmjs.com)

No npm token exists in GitHub. npm accepts a short-lived OIDC token from GitHub
Actions instead, and only from this repository, this workflow and this environment.
Do this for **each of the six packages**:

`@documentmanager/pdf-engine`, `@documentmanager/pdf-engine-darwin-arm64`,
`@documentmanager/pdf-engine-darwin-x64`, `@documentmanager/pdf-engine-linux-x64`,
`@documentmanager/pdf-engine-linux-arm64`, `@documentmanager/pdf-engine-win32-x64`

1. npmjs.com → the package → **Settings** → **Trusted publishing** → **GitHub Actions**.
2. Organization or user: `ifaridaiman`. Repository: `document-manager`.
   Workflow filename: `release.yml`. Environment name: `npm-production`.
3. Save. If a trusted publisher already exists without the environment, edit it to
   add `npm-production`.
4. Recommended once it works: **Publishing access** → "Require two-factor
   authentication and disallow tokens", so even a leaked token cannot publish.

Requirements the workflow already meets: npm CLI ≥ 11.5.1 (it pins 11.21.3),
`id-token: write`, a `repository.url` matching this repo (needed for provenance), and
`--access public` for the scoped packages.

## 5. GitHub environment and repository settings

Settings → Environments → **`npm-production`**:

- Required reviewers: `ifaridaiman`. "Prevent self-review" must stay **off**,
  because the owner both merges the release PR and approves it.
- Deployment branches and tags: **Selected branches**, rule `main`.
- No secrets (trusted publishing needs none).

Repository settings this pipeline relies on:

- Actions → General → Workflow permissions: **Read repository contents** by default,
  and **Allow GitHub Actions to create and approve pull requests** turned on (Release
  Please opens its PR with `GITHUB_TOKEN`).
- Actions → General → Fork pull request workflows: **Require approval for all
  external contributors**.
- Rules → Rulesets → *Protect Main Branch*: require the status checks `Go lint`,
  `Go test`, `npm (Node 22)`, `npm (Node 24)`, `npm (Node 26)`, `Package` and the
  five `Smoke (...)` checks.
- Code security: Dependabot alerts and security updates, secret scanning and push
  protection on.

Plan limits: environments with required reviewers are available on GitHub Free for
**public** repositories. If the repository ever becomes private on a Free or Pro
plan, required reviewers stop applying and publishing loses its approval step. The
`plan` job still rejects any actor other than the repository owner, so that guard
remains.

Who can start a release: `workflow_dispatch` is available to anyone with write
access, not only the owner. Three things stop anyone else: `release.yml` exits unless
the actor is the repository owner and the ref is `main`, the environment only allows
`main` and needs the owner's approval, and npm only accepts tokens from that
environment.

## 6. Preparing a release

1. Merge PRs to `main` as usual. Release Please updates the open PR titled
   **`chore: release X.Y.Z`** after every merge. The PR's CI is dispatched
   automatically.
2. Review that PR: the version, and the generated `CHANGELOG.md` section. Edit the
   changelog text in the PR if it needs better wording. To release a different
   version, see `Release-As` in section 3.
3. Wait for its checks to pass.

## 7. Approving and publishing

1. Squash-merge the release PR.
2. Actions → **release** shows a run for that commit. `plan` and `ci` run first (about
   10 minutes, including all smoke tests).
3. The `publish` job waits for review. Check the version and commit, then click
   **Review deployments → npm-production → Approve and deploy**.
4. `publish` pushes the 5 platform packages and then the main package, all with
   provenance. `github-release` then creates `vX.Y.Z` at the release commit, attaches
   the binaries, `THIRD_PARTY_NOTICES.md` and `SHA256SUMS`, and marks the release PR
   `autorelease: tagged`.

To stop a release, reject the deployment. Nothing has been published at that point.

## 8. Beta and next releases

| Channel | Version | npm dist-tag | Install |
|---|---|---|---|
| Stable | `1.1.0` | `latest` | `npm i @documentmanager/pdf-engine` |
| Beta | `1.1.0-beta.1` | `beta` | `npm i @documentmanager/pdf-engine@beta` |
| Next major | `2.0.0-next.1` | `next` | `npm i @documentmanager/pdf-engine@next` |

1. Actions → **release** → **Run workflow** on `main`, version `1.1.0-beta.1`.
2. Approve `npm-production` as above.

The version must be `x.y.z-beta.N` or `x.y.z-next.N`, and `x.y.z` must be newer than
the current stable version. The dist-tag comes from the version, so a prerelease can
never move `latest`. Prereleases get a GitHub prerelease with generated notes; they
don't touch `CHANGELOG.md` or the manifest. Increase `N` for each attempt: a published
version can never be reused.

## 9. Recovering from a failed release

Every job can be re-run. Already-published packages (matched by the `gitHead` commit
recorded in each package) and an existing GitHub Release are skipped or updated, never
duplicated.

| Failed at | What to do |
|---|---|
| `plan` | Read the error. It names what's wrong (actor, branch, version mismatch, a tag or npm version from another commit). Fix it with a PR; Release Please will open a new release PR if needed. |
| `ci` | Fix with a normal PR. The release PR stays merged, so the next `chore: release` PR includes the fix and a new version. If nothing on npm changed, you can also re-run the run once the problem (e.g. a flaky runner) is gone. |
| `publish`, some packages published | **Re-run failed jobs** on the same run. It republishes nothing that's already there and continues with the rest. Do not start a new run from a newer commit: the version is already partly claimed by this commit and `plan` will refuse. |
| `github-release` | **Re-run failed jobs.** It creates the tag and release, or uploads missing files to an existing one. |
| Run expired (artifacts are kept 14 days) | Actions → release → **Run workflow** with an empty version, on `main`, while `main` is still at the release commit. Otherwise release a new patch version. |

A version that reached npm cannot be republished or reused, even after
`npm unpublish`. If a bad version went out, release a fixed patch version and
deprecate the bad one, signed in to npm with 2FA:

```bash
npm deprecate @documentmanager/pdf-engine@0.3.0 "Broken release, use 0.3.1"
```

If the release PR ended up stuck as `autorelease: pending` (Release Please then logs
"untagged, merged release PRs outstanding" and opens no new PR), change its label to
`autorelease: tagged` once the release is complete.

## 10. Verifying a published package

```bash
npm view @documentmanager/pdf-engine dist-tags        # latest / beta / next
npm view @documentmanager/pdf-engine@0.3.0 gitHead optionalDependencies
mkdir /tmp/try && cd /tmp/try && npm init -y >/dev/null
npm install @documentmanager/pdf-engine@0.3.0
npm audit signatures                                  # registry signatures + provenance
node --input-type=module -e "import { engine } from '@documentmanager/pdf-engine'; console.log(await engine.inspect(new TextEncoder().encode('x')).catch(e => e.code)); engine.close()"
# prints INVALID_PDF: the platform binary started and answered
```

The npm page shows a **Provenance** badge linking to the exact workflow run and commit.
Release binaries can be checked with `sha256sum -c SHA256SUMS`.

## 11. Troubleshooting npm publishing

| Error | Cause and fix |
|---|---|
| `ENEEDAUTH` / `E401` / "Unable to authenticate" | The package has no trusted publisher, or its workflow filename, environment or repository doesn't match section 4 exactly (it's case-sensitive). Also check that the job has `id-token: write`. |
| `E404 Not Found - PUT` | The same cause as above, as npm reports it for scoped packages. |
| `E403` "You cannot publish over the previously published versions" | The version exists. The pre-publish check should catch this. If it got here anyway, another run published it. Re-run so the check skips it, or release a new version. |
| `E403` 2FA / OTP required | Publishing access requires 2FA for tokens. Trusted publishing is exempt; this means OIDC wasn't used. See the first row. |
| `EALLOWGIT` "Refusing to fetch github:..." | `npm publish dir/name` was read as a GitHub repository. Publish `./path` or a `.tgz`, as the workflow does. This bug is why 0.2.0 and 0.2.1 never published the main package. |
| Provenance: "repository.url does not match" | `repository.url` in `npm/pdf-engine/package.json` must stay `git+https://github.com/ifaridaiman/document-manager.git`. |
| `ETARGET` installing the main package | A platform package of that version is missing (failed partial release). Re-run the release; platform packages are always published before the main one. |
| Smoke check fails on one platform only | That binary is broken on that OS or CPU. Don't release; reproduce locally with `npm run build:local && npm test` on that platform. |

## Local equivalents

```bash
go test -race ./... && go vet ./...
cd npm/pdf-engine
npm ci && npm run typecheck && npm run build:local && npm test
npm run build && GIT_SHA=$(git rev-parse HEAD) npm run prepare-release -- 0.0.0-local.0
git checkout package.json   # prepare-release rewrites it
```
