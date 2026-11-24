// Turns the binaries in bin/<platform>-<arch>/ into the six npm packages, packs and
// verifies them, and collects the GitHub release files. CI and the release workflow
// both run it, so what CI tests is byte-for-byte what gets published.
//   node scripts/prepare-release.mjs 0.3.0
// Run `npm run build` first: every target must be present. GIT_SHA (set by CI) is
// recorded as `gitHead` in every package.json; the release workflow uses it to tell
// "already published from this commit" (safe to skip) from a version collision.
// Note: rewrites this package's package.json (version, optionalDependencies, gitHead).
//
// Output (npm/dist/):
//   pdf-engine-<target>/   platform package directories
//   tarballs/*.tgz         the six packages, verified, ready for `npm publish`
//   release/               raw binaries, THIRD_PARTY_NOTICES.md and SHA256SUMS
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { copyFileSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const version = process.argv[2];
if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(version ?? "")) {
  console.error("usage: node scripts/prepare-release.mjs <semver>, e.g. 0.3.0");
  process.exit(1);
}
const gitHead = process.env.GIT_SHA || undefined;

const pkgDir = fileURLToPath(new URL("..", import.meta.url));
const root = fileURLToPath(new URL("../../..", import.meta.url));
const dist = fileURLToPath(new URL("../../dist", import.meta.url));
const mainPath = `${pkgDir}package.json`;
const main = JSON.parse(readFileSync(mainPath, "utf8"));
// The only platforms we build, test (ci.yml "smoke") and publish.
const targets = ["darwin-arm64", "darwin-x64", "linux-x64", "linux-arm64", "win32-x64"];
const exeFor = (target) => (target.startsWith("win32") ? "pdf-engine.exe" : "pdf-engine");

rmSync(dist, { recursive: true, force: true });
mkdirSync(`${dist}/release`, { recursive: true });
mkdirSync(`${dist}/tarballs`, { recursive: true });

const packages = []; // [dir, expected files]
for (const target of targets) {
  const [os, cpu] = target.split("-");
  const exe = exeFor(target);
  const src = `${pkgDir}bin/${target}/${exe}`;
  if (!existsSync(src)) throw new Error(`missing ${src}: run "npm run build" first`);

  const out = `${dist}/pdf-engine-${target}`;
  mkdirSync(`${out}/bin`, { recursive: true });
  copyFileSync(src, `${out}/bin/${exe}`); // keeps the executable bit
  copyFileSync(`${root}LICENSE`, `${out}/LICENSE`);
  copyFileSync(`${root}THIRD_PARTY_NOTICES.md`, `${out}/THIRD_PARTY_NOTICES.md`); // PDFium & co. ship inside the binary
  writeFileSync(`${out}/README.md`, `The ${target} binary for [${main.name}](https://www.npmjs.com/package/${main.name}). Install that package instead.\n`);
  writeFileSync(`${out}/package.json`, JSON.stringify({
    name: `${main.name}-${target}`,
    version,
    description: `${target} binary for ${main.name}`,
    license: main.license,
    repository: main.repository,
    os: [os],
    cpu: [cpu],
    files: ["bin", "THIRD_PARTY_NOTICES.md"],
    preferUnplugged: true,
    ...(gitHead && { gitHead }),
  }, null, 2) + "\n");
  packages.push([out, ["LICENSE", "README.md", "THIRD_PARTY_NOTICES.md", "package.json", `bin/${exe}`]]);

  copyFileSync(src, `${dist}/release/pdf-engine-${target}${os === "win32" ? ".exe" : ""}`);
}

main.version = version;
main.optionalDependencies = Object.fromEntries(targets.map((t) => [`${main.name}-${t}`, version]));
if (gitHead) main.gitHead = gitHead;
writeFileSync(mainPath, JSON.stringify(main, null, 2) + "\n");
copyFileSync(`${root}LICENSE`, `${pkgDir}LICENSE`);
copyFileSync(`${root}THIRD_PARTY_NOTICES.md`, `${pkgDir}THIRD_PARTY_NOTICES.md`);
copyFileSync(`${root}THIRD_PARTY_NOTICES.md`, `${dist}/release/THIRD_PARTY_NOTICES.md`);
packages.push([pkgDir, ["LICENSE", "README.md", "THIRD_PARTY_NOTICES.md", "package.json", "src/index.d.ts", "src/index.js"]]);

// Pack every package and check it holds exactly the expected files: nothing missing
// (binary, types, notices) and nothing extra (tests, scripts, dotfiles, secrets).
for (const [dir, expected] of packages) {
  const [info] = JSON.parse(execFileSync("npm", ["pack", dir, "--json", "--ignore-scripts", "--pack-destination", `${dist}/tarballs`], {
    encoding: "utf8",
    shell: process.platform === "win32",
  }));
  const files = info.files.map((f) => f.path).sort();
  const want = [...expected].sort();
  if (JSON.stringify(files) !== JSON.stringify(want)) {
    throw new Error(`${info.name}: unexpected contents\n  got:  ${files.join(", ")}\n  want: ${want.join(", ")}`);
  }
  if (info.version !== version) throw new Error(`${info.name}: packed version ${info.version}, want ${version}`);
  const bin = info.files.find((f) => f.path === "bin/pdf-engine");
  if (bin && (bin.mode & 0o111) === 0) throw new Error(`${info.name}: bin/pdf-engine is not executable (mode ${bin.mode.toString(8)})`);
  console.log(`packed ${info.filename} (${(info.size / 1e6).toFixed(1)} MB, ${files.length} files)`);
}

// Every path in the export map must be in the package.
for (const [cond, path] of Object.entries(main.exports["."])) {
  if (!existsSync(`${pkgDir}${path}`)) throw new Error(`exports["."].${cond} points at missing ${path}`);
}

const sums = readdirSync(`${dist}/release`).sort().map((f) =>
  `${createHash("sha256").update(readFileSync(`${dist}/release/${f}`)).digest("hex")}  ${f}`);
writeFileSync(`${dist}/release/SHA256SUMS`, sums.join("\n") + "\n");

console.log(`prepared ${main.name}@${version} + ${targets.length} platform packages in ${dist}`);
