// Turns the binaries in bin/<platform>-<arch>/ into one npm package per platform
// (npm/dist/pdf-engine-<platform>-<arch>/) and stamps every package with one version.
//   node scripts/prepare-release.mjs 0.2.0
// Run `npm run build` first: every target must be present.
import { copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const version = process.argv[2];
if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(version ?? "")) {
  console.error("usage: node scripts/prepare-release.mjs <semver>, e.g. 0.2.0");
  process.exit(1);
}

const pkgDir = fileURLToPath(new URL("..", import.meta.url));
const root = fileURLToPath(new URL("../../..", import.meta.url));
const dist = fileURLToPath(new URL("../../dist", import.meta.url));
const mainPath = `${pkgDir}package.json`;
const main = JSON.parse(readFileSync(mainPath, "utf8"));
const targets = ["darwin-arm64", "darwin-x64", "linux-x64", "linux-arm64", "win32-x64"];

rmSync(dist, { recursive: true, force: true });
mkdirSync(`${dist}/release`, { recursive: true });

for (const target of targets) {
  const [os, cpu] = target.split("-");
  const exe = os === "win32" ? "pdf-engine.exe" : "pdf-engine";
  const src = `${pkgDir}bin/${target}/${exe}`;
  if (!existsSync(src)) throw new Error(`missing ${src}: run "npm run build" first`);

  const out = `${dist}/pdf-engine-${target}`;
  mkdirSync(`${out}/bin`, { recursive: true });
  copyFileSync(src, `${out}/bin/${exe}`); // keeps the executable bit
  copyFileSync(`${root}LICENSE`, `${out}/LICENSE`);
  writeFileSync(`${out}/README.md`, `The ${target} binary for [${main.name}](https://www.npmjs.com/package/${main.name}). Install that package instead.\n`);
  writeFileSync(`${out}/package.json`, JSON.stringify({
    name: `${main.name}-${target}`,
    version,
    description: `${target} binary for ${main.name}`,
    license: main.license,
    repository: main.repository,
    os: [os],
    cpu: [cpu],
    files: ["bin"],
    preferUnplugged: true,
  }, null, 2) + "\n");

  copyFileSync(src, `${dist}/release/pdf-engine-${target}${os === "win32" ? ".exe" : ""}`);
}

main.version = version;
main.optionalDependencies = Object.fromEntries(targets.map((t) => [`${main.name}-${t}`, version]));
writeFileSync(mainPath, JSON.stringify(main, null, 2) + "\n");
copyFileSync(`${root}LICENSE`, `${pkgDir}LICENSE`);

console.log(`prepared ${main.name}@${version} + ${targets.length} platform packages in ${dist}`);
