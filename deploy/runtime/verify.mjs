#!/usr/bin/env node
// Runs the project's real job in a Runtime Cloud sandbox: build, vet, the
// hermetic unit tests and the frontend production build.
//
//   node verify.mjs           run the job and delete the sandbox
//   node verify.mjs --keep    keep the sandbox for inspection
//   node verify.mjs --full    also run test/ against the live Redis (needs
//                             credit: Redis listens on a non-443 port)
import { Sandbox } from "withruntime";
import {
  GO_PATH,
  LABELS,
  REMOTE_ROOT,
  ensureGo,
  ensureNode,
  expectOk,
  run,
  stageProject,
} from "./lib.mjs";

const keep = process.argv.includes("--keep");
const full = process.argv.includes("--full");

const backend = `${REMOTE_ROOT}/backend`;
const frontend = `${REMOTE_ROOT}/frontend`;

let stage;
let sbx;
const checks = [];

try {
  stage = await stageProject();

  console.log("Creating a Runtime sandbox (2 vCPU, 4 GiB)...");
  sbx = await Sandbox.create({
    name: `sos-allert-verify-${Date.now()}`,
    labels: { ...LABELS, role: "verify" },
    vcpu: 2,
    memoryMiB: 4096,
    diskMiB: 8192,
    timeoutSeconds: 3600,
    onLeaseEnd: "delete",
  });
  console.log(`sandbox ${sbx.id} is ${sbx.state}`);

  console.log("Uploading the project...");
  await sbx.files.upload(stage, REMOTE_ROOT);

  await ensureNode(sbx);
  await ensureGo(sbx);

  const build = await run(sbx, "go build", `${GO_PATH}cd ${backend} && go build ./...`);
  checks.push(["go build ./...", build.exitCode === 0]);

  const vet = await run(sbx, "go vet", `${GO_PATH}cd ${backend} && go vet ./...`);
  checks.push(["go vet ./...", vet.exitCode === 0]);

  const test = await run(
    sbx,
    "go test (usecase)",
    `${GO_PATH}cd ${backend} && go test ./internal/usecase/ -count=1`,
  );
  checks.push([
    "go test ./internal/usecase/",
    test.exitCode === 0 && /\bok\b/.test(test.stdout),
  ]);

  if (full) {
    const integration = await run(
      sbx,
      "go test (live Redis)",
      `${GO_PATH}cd ${backend} && go test ./test/ -count=1`,
    );
    checks.push(["go test ./test/", integration.exitCode === 0]);
  }

  const install = await run(
    sbx,
    "npm ci",
    `cd ${frontend} && npm ci --no-audit --no-fund`,
  );
  checks.push(["npm ci", install.exitCode === 0]);

  const buildFrontend = await run(
    sbx,
    "npm run build",
    `cd ${frontend} && npm run build`,
  );
  checks.push(["npm run build", buildFrontend.exitCode === 0]);

  const index = await sbx.files.exists(`${frontend}/dist/index.html`);
  checks.push(["frontend dist/index.html", index]);

  console.log("\nResults");
  for (const [label, ok] of checks) console.log(`  ${ok ? "PASS" : "FAIL"}  ${label}`);

  if (checks.some(([, ok]) => !ok)) {
    throw new Error("one or more checks failed");
  }
  console.log("\nThe project's job passed on Runtime.");
} catch (error) {
  console.error(`\nFailed: ${error.message}`);
  if (error.requestId) console.error(`requestId: ${error.requestId}`);
  process.exitCode = 1;
} finally {
  if (sbx && !keep) {
    await sbx.delete().catch((error) => console.error(`cleanup failed: ${error.message}`));
    console.log(`deleted sandbox ${sbx.id}`);
  } else if (sbx) {
    console.log(`kept sandbox ${sbx.id}: npx withruntime sandboxes rm ${sbx.id}`);
  }
}
