// First job of .github/workflows/release.yml: decides what a release run does, and
// refuses anything unsafe before a build, an approval request or a publish happens.
//   GITHUB_SHA=... GITHUB_REF=refs/heads/main GITHUB_ACTOR=me GITHUB_REPOSITORY_OWNER=me \
//   EVENT=workflow_dispatch INPUT_VERSION=0.4.0-beta.1 node scripts/release-plan.mjs
// Needs git (origin remote), npm and gh (GH_TOKEN). Writes outputs to $GITHUB_OUTPUT.
//
// Stable (no INPUT_VERSION): the version in .release-please-manifest.json, which
// must match npm/pdf-engine/package.json and have a CHANGELOG.md section.
// Prerelease (INPUT_VERSION=x.y.z-beta.N | x.y.z-next.N): x.y.z must be newer than the
// last stable version; published under the "beta" or "next" dist-tag, never "latest".
import { execFileSync, spawnSync } from "node:child_process";
import { appendFileSync, readFileSync } from "node:fs";

const env = process.env;
const sha = env.GITHUB_SHA;
const fail = (msg) => {
  console.log(`::error::${msg}`);
  process.exit(1);
};
const out = (outputs) => {
  for (const [k, v] of Object.entries(outputs)) {
    console.log(`${k}=${v}`);
    if (env.GITHUB_OUTPUT) appendFileSync(env.GITHUB_OUTPUT, `${k}=${v}\n`);
  }
};
const run = (cmd, args) => spawnSync(cmd, args, { encoding: "utf8" });

if (!/^[0-9a-f]{40}$/.test(sha ?? "")) fail("GITHUB_SHA is not a commit SHA");
if (env.GITHUB_REF !== "refs/heads/main") fail(`releases run from main only, not ${env.GITHUB_REF}`);
if (!env.GITHUB_ACTOR || env.GITHUB_ACTOR !== env.GITHUB_REPOSITORY_OWNER) {
  fail(`only the repository owner can release (actor: ${env.GITHUB_ACTOR})`);
}

const manifest = JSON.parse(readFileSync(".release-please-manifest.json", "utf8"))["."];
const pkg = JSON.parse(readFileSync("npm/pdf-engine/package.json", "utf8"));
if (manifest !== pkg.version) {
  fail(`.release-please-manifest.json says ${manifest} but npm/pdf-engine/package.json says ${pkg.version}`);
}

const input = (env.INPUT_VERSION ?? "").trim();
const core = (v) => v.split("-")[0].split(".").map(Number);
const newer = (a, b) => { // a > b, comparing x.y.z only
  const [x, y] = [core(a), core(b)];
  for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return x[i] > y[i];
  return false;
};

let version, distTag;
if (!input) {
  version = manifest;
  distTag = "latest";
  if (!/^\d+\.\d+\.\d+$/.test(version)) fail(`manifest version ${version} is not a stable x.y.z version`);
  // Fails (and prints why) if CHANGELOG.md has no section for this version.
  if (run("sh", ["scripts/changelog-section.sh", version]).status !== 0) fail(`CHANGELOG.md has no section for ${version}`);
} else {
  const m = /^\d+\.\d+\.\d+-(beta|next)\.\d+$/.exec(input);
  if (!m) fail(`prerelease version must look like 1.2.0-beta.1 or 2.0.0-next.1, got "${input}"`);
  if (!newer(input, manifest)) fail(`${input} must be ahead of the last stable version ${manifest}`);
  version = input;
  distTag = m[1];
}
const prerelease = distTag !== "latest";
if (prerelease === /^\d+\.\d+\.\d+$/.test(version)) fail("internal: dist-tag does not match the version"); // never prerelease on "latest"
const tag = `v${version}`;

// Where does the tag point? `^{}` is the commit of an annotated tag.
const refs = execFileSync("git", ["ls-remote", "--tags", "origin", `refs/tags/${tag}`, `refs/tags/${tag}^{}`], { encoding: "utf8" })
  .trim().split("\n").filter(Boolean).map((l) => l.split("\t"));
const tagSha = (refs.find(([, r]) => r.endsWith("^{}")) ?? refs[0])?.[0];
if (tagSha && tagSha !== sha) {
  // A push to main that touches the manifest without bumping it (not a release PR): nothing to do.
  if (env.EVENT === "push") {
    console.log(`::notice::${tag} already exists at ${tagSha}; nothing to release.`);
    out({ skip: "true", version, tag });
    process.exit(0);
  }
  fail(`${tag} already exists at ${tagSha}, not ${sha}. To finish a failed release, re-run that release run's failed jobs instead.`);
}

// npm records the commit each version was built from (gitHead, stamped by prepare-release.mjs).
const view = run("npm", ["view", `${pkg.name}@${version}`, "gitHead", "--json"]);
let mainPublished = false;
if (view.status === 0 && view.stdout.trim()) {
  const head = JSON.parse(view.stdout);
  if (head !== sha) fail(`${pkg.name}@${version} is already on npm, built from ${head || "an unknown commit"}, not ${sha}`);
  mainPublished = true;
} else if (!/E404/.test(view.stdout + view.stderr)) {
  fail(`npm view ${pkg.name}@${version} failed:\n${view.stderr || view.stdout}`);
}
const releaseExists = run("gh", ["release", "view", tag, "--json", "tagName"]).status === 0;

if (mainPublished && releaseExists && tagSha === sha) {
  console.log(`::notice::${tag} is fully released (npm + GitHub Release); nothing to do.`);
  out({ skip: "true", version, tag });
  process.exit(0);
}
out({ skip: "false", version, tag, dist_tag: distTag, prerelease, main_published: mainPublished, release_exists: releaseExists });
