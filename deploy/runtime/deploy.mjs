#!/usr/bin/env node
// Builds and runs SOS-Allert in a Runtime Cloud sandbox, with a private
// preview URL for the API and one for the web app.
//
//   node deploy.mjs            create (or reuse) the sandbox and start both
//   node deploy.mjs --delete   delete the sandbox instead
//
// The API reads backend/config.json (MySQL + Redis) and backend/ca.pem from
// the working tree. Both are gitignored and uploaded to the sandbox, never
// committed. Reaching those services needs paid credit: a sandbox without
// credit can only reach ports 80 and 443 outbound.
import { Runtime, Sandbox } from "withruntime";
import {
  GO_PATH,
  LABELS,
  REMOTE_ROOT,
  collect,
  ensureGo,
  ensureNode,
  expectOk,
  run,
  stageProject,
  waitForHttp,
} from "./lib.mjs";

const remove = process.argv.includes("--delete");
const backend = `${REMOTE_ROOT}/backend`;
const frontend = `${REMOTE_ROOT}/frontend`;
const API_PORT = 3000;
const WEB_PORT = 4173;

if (remove) {
  const result = await new Runtime().sandboxes.deleteAll({ labels: LABELS });
  console.log(`deleted ${result.deleted.length} sandbox(es)`);
  for (const failure of result.failed) console.error(failure.id, failure.error);
  process.exit(result.failed.length ? 1 : 0);
}

let stage;
let sbx;

try {
  stage = await stageProject();

  console.log("Creating (or reusing) the Runtime sandbox...");
  sbx = await Sandbox.create({
    name: "sos-allert",
    getOrCreate: true,
    labels: { ...LABELS, role: "app" },
    vcpu: 2,
    memoryMiB: 4096,
    diskMiB: 8192,
    timeoutSeconds: 3600,
    onLeaseEnd: "pause",
  });
  await sbx.extend(3600);
  console.log(`sandbox ${sbx.id} is ${sbx.state}`);

  console.log("Uploading the project...");
  await sbx.files.upload(stage, REMOTE_ROOT);

  await ensureNode(sbx);
  await ensureGo(sbx);

  expectOk(
    await run(sbx, "build API", `${GO_PATH}cd ${backend} && go build -o bin/web ./cmd/web`),
    "build API",
  );
  expectOk(
    await run(sbx, "build web app", `cd ${frontend} && npm ci --no-audit --no-fund && npm run build`),
    "build web app",
  );

  // Stop a previous run of either server before starting the new build.
  await sbx.exec("pkill -f '/bin/web' 2>/dev/null; pkill -f 'vite preview' 2>/dev/null; true");

  console.log("\nStarting the API...");
  const api = await sbx.spawn([`${backend}/bin/web`], { cwd: backend });
  if (!(await waitForHttp(sbx, API_PORT, "/"))) {
    const output = await collect(api);
    throw new Error(`the API did not answer on port ${API_PORT}\n${output}`);
  }

  console.log("Starting the web app...");
  const web = await sbx.spawn(
    ["npm", "run", "preview", "--", "--host", "0.0.0.0", "--port", String(WEB_PORT)],
    { cwd: frontend },
  );
  if (!(await waitForHttp(sbx, WEB_PORT, "/"))) {
    const output = await collect(web);
    throw new Error(`the web app did not answer on port ${WEB_PORT}\n${output}`);
  }

  // Private previews work without credit. Public ones need paid credit.
  const apiPreview = await sbx.previews.create(API_PORT);
  const webPreview = await sbx.previews.create(WEB_PORT);

  console.log("\nSOS-Allert is running on Runtime.");
  console.log(`  sandbox  ${sbx.id}`);
  console.log(`  API      ${apiPreview.urlWithToken ?? apiPreview.url}`);
  console.log(`  web app  ${webPreview.urlWithToken ?? webPreview.url}`);
  console.log("\nThe links are private: open urlWithToken, or send");
  console.log("x-runtime-preview-token with the URL. Re-run to rebuild.");
  console.log(`Stop:   npx withruntime sandboxes stop ${sbx.id}`);
  console.log("Delete: node deploy.mjs --delete");
  console.log(`\nSandbox ${sbx.id} kept.`);
} catch (error) {
  console.error(`\nFailed: ${error.message}`);
  if (error.requestId) console.error(`requestId: ${error.requestId}`);
  process.exitCode = 1;
}
