// Issue, revoke and inspect proxy passes. Run inside the proxy container:
//   docker exec cyc26-proxy node passctl.mjs add alice [--cap 500]
// The proxy hot-reloads passes.json, so changes apply to the next request.
import { randomBytes } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { hashPass } from "./server.mjs";

const DATA_DIR = process.env.DATA_DIR ?? "/data";
const PASSES_PATH = path.join(DATA_DIR, "passes.json");
const USAGE_PATH = path.join(DATA_DIR, "usage.jsonl");

function load() {
  try {
    return JSON.parse(fs.readFileSync(PASSES_PATH, "utf8"));
  } catch (err) {
    if (err.code === "ENOENT") return { passes: [] };
    throw err;
  }
}

function save(doc) {
  const tmp = `${PASSES_PATH}.${process.pid}.tmp`;
  fs.writeFileSync(tmp, `${JSON.stringify(doc, null, 2)}\n`, { mode: 0o600 });
  fs.renameSync(tmp, PASSES_PATH);
}

function usage(name, day) {
  const rows = new Map();
  for (const file of [`${USAGE_PATH}.1`, USAGE_PATH]) {
    if (!fs.existsSync(file)) continue;
    for (const line of fs.readFileSync(file, "utf8").split("\n")) {
      if (!line) continue;
      let e;
      try {
        e = JSON.parse(line);
      } catch {
        continue;
      }
      if (name && e.pass !== name) continue;
      if (day && !e.t.startsWith(day)) continue;
      const key = `${e.t.slice(0, 10)}  ${e.pass}  ${e.provider}`;
      const r = rows.get(key) ?? { requests: 0, errors: 0, input: 0, output: 0 };
      r.requests++;
      if (e.status >= 400) r.errors++;
      r.input += e.tokens?.input ?? 0;
      r.output += e.tokens?.output ?? 0;
      rows.set(key, r);
    }
  }
  for (const [key, r] of [...rows].sort()) {
    console.log(`${key}  requests=${r.requests} errors=${r.errors} in_tokens~${r.input} out_tokens~${r.output}`);
  }
  if (rows.size === 0) console.log("no usage recorded");
}

const [cmd, name, ...rest] = process.argv.slice(2);
const doc = load();

switch (cmd) {
  case "add": {
    if (!name || !/^[a-z0-9][a-z0-9_-]{0,31}$/.test(name)) {
      console.error("usage: add <name> [--cap <requests per UTC day>]   (name: lowercase letters, digits, - _)");
      process.exit(2);
    }
    if (doc.passes.some((p) => p.name === name)) {
      console.error(`a pass named ${name} already exists; revoke it first to reissue`);
      process.exit(1);
    }
    const capIdx = rest.indexOf("--cap");
    const cap = capIdx >= 0 ? Number(rest[capIdx + 1]) : undefined;
    if (capIdx >= 0 && !(cap > 0)) {
      console.error("--cap needs a positive number");
      process.exit(2);
    }
    const pass = `cyc26_${randomBytes(24).toString("base64url")}`;
    doc.passes.push({ name, hash: hashPass(pass), created: new Date().toISOString(), ...(cap ? { dailyRequestCap: cap } : {}) });
    save(doc);
    console.error(`Issued pass for ${name}. It is shown once and cannot be recovered:`);
    console.log(pass);
    break;
  }
  case "revoke": {
    const before = doc.passes.length;
    doc.passes = doc.passes.filter((p) => p.name !== name);
    if (doc.passes.length === before) {
      console.error(`no pass named ${name}`);
      process.exit(1);
    }
    save(doc);
    console.log(`revoked ${name}; it stops working on the next request`);
    break;
  }
  case "list":
    for (const p of doc.passes) console.log(`${p.name}  created=${p.created}${p.dailyRequestCap ? `  cap=${p.dailyRequestCap}/day` : ""}`);
    if (doc.passes.length === 0) console.log("no passes");
    break;
  case "usage": {
    const args = [name, ...rest].filter(Boolean);
    const dayIdx = args.indexOf("--day");
    usage(args[0] && !args[0].startsWith("--") ? args[0] : undefined, dayIdx >= 0 ? args[dayIdx + 1] : undefined);
    break;
  }
  default:
    console.error("commands: add <name> [--cap N] | revoke <name> | list | usage [name] [--day YYYY-MM-DD]");
    process.exit(2);
}
