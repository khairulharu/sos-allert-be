// Shared helpers for running SOS-Allert on Runtime Cloud.
import { cp, mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
export const REMOTE_ROOT = "/workspace/sos-allert";
export const LABELS = { app: "sos-allert" };

// Never ship VCS data, installed dependencies, build output or this harness.
const EXCLUDE = new Set([".git", "node_modules", "dist", ".idea", "deploy"]);

/** Copies backend/ and frontend/ into a temporary directory without the
 * folders a sandbox reinstalls or rebuilds itself. */
export async function stageProject() {
  const stage = await mkdtemp(join(tmpdir(), "sos-allert-stage-"));
  for (const part of ["backend", "frontend"]) {
    await cp(join(REPO_ROOT, part), join(stage, part), {
      recursive: true,
      filter: (src) => !EXCLUDE.has(src.split(/[\\/]/).pop()),
    });
  }
  return stage;
}

/** Runs a command in the sandbox, printing its output and exit code. */
export async function run(sbx, label, command, options = {}) {
  const started = Date.now();
  console.log(`\n$ ${label}`);
  const result = await sbx.exec(command, { timeoutMs: 15 * 60_000, ...options });
  const elapsed = ((Date.now() - started) / 1000).toFixed(1);
  if (result.stdout) process.stdout.write(result.stdout);
  if (result.stderr) process.stderr.write(result.stderr);
  console.log(`[${label}] exit ${result.exitCode} in ${elapsed}s`);
  return result;
}

export function expectOk(result, label) {
  if (result.exitCode !== 0) {
    throw new Error(`${label} failed with exit code ${result.exitCode}`);
  }
}

/** The default image has Node; install Ubuntu's if it does not. */
export async function ensureNode(sbx) {
  const probe = await sbx.exec("node --version && npm --version");
  if (probe.exitCode === 0) {
    console.log(`node ${probe.stdout.trim().split("\n").join(" / ")}`);
    return;
  }
  await run(
    sbx,
    "install nodejs",
    "sudo apt-get update -qq && sudo apt-get install -y nodejs npm",
  );
  const again = await sbx.exec("node --version && npm --version");
  if (again.exitCode !== 0) throw new Error("node and npm are not available in the sandbox");
}

/** The Go module needs 1.25. Use the sandbox's Go when it is new enough,
 * otherwise download the latest stable 1.25 release from go.dev. */
export async function ensureGo(sbx) {
  const probe = await sbx.exec("go version 2>/dev/null || true");
  const found = /go(\d+)\.(\d+)/.exec(probe.stdout);
  if (found && (Number(found[1]) > 1 || (Number(found[1]) === 1 && Number(found[2]) >= 25))) {
    console.log(probe.stdout.trim());
    return;
  }

  const machine = await sbx.exec("uname -m");
  const arch = machine.stdout.trim() === "aarch64" ? "arm64" : "amd64";
  const release = await latestGo125(arch);
  console.log(`installing ${release.version} for linux/${arch}`);
  const result = await run(
    sbx,
    `install ${release.version}`,
    [
      `curl -fsSL '${release.url}' -o /tmp/go.tgz`,
      `echo '${release.sha256}  /tmp/go.tgz' | sha256sum -c -`,
      "sudo rm -rf /usr/local/go",
      "sudo tar -C /usr/local -xzf /tmp/go.tgz",
      "/usr/local/go/bin/go version",
    ].join(" && "),
  );
  expectOk(result, `install ${release.version}`);
}

async function latestGo125(arch) {
  const response = await fetch("https://go.dev/dl/?mode=json&include=all");
  if (!response.ok) throw new Error(`go.dev answered ${response.status}`);
  const releases = await response.json();
  const release = releases.find((item) => item.stable && /^go1\.25\.\d+$/.test(item.version));
  if (!release) throw new Error("no stable Go 1.25 release found on go.dev");
  const file = release.files.find(
    (item) => item.os === "linux" && item.arch === arch && item.kind === "archive",
  );
  if (!file) throw new Error(`no Go 1.25 archive for linux/${arch}`);
  return {
    version: release.version,
    url: `https://go.dev/dl/${file.filename}`,
    sha256: file.sha256,
  };
}

/** Go's tools are in /usr/local/go/bin after ensureGo installs them. */
export const GO_PATH = "export PATH=/usr/local/go/bin:$PATH; ";

/** Reads a background process's output into a string. */
export async function collect(proc) {
  let text = "";
  for await (const event of proc.output()) {
    if (event.type === "stdout" || event.type === "stderr") text += event.data;
  }
  return text;
}

/** Polls an HTTP port inside the sandbox until it answers. */
export async function waitForHttp(sbx, port, path = "/", attempts = 45) {
  for (let i = 0; i < attempts; i++) {
    const probe = await sbx.exec(
      `curl -s -o /dev/null -w '%{http_code}' 'http://127.0.0.1:${port}${path}' || true`,
    );
    if (/^[1-5]\d\d$/.test(probe.stdout.trim())) return true;
    await new Promise((resolveWait) => setTimeout(resolveWait, 2000));
  }
  return false;
}
