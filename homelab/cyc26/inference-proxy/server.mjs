// Credential-holding inference proxy for the cyc26 workspace.
//
// Workspaces send requests with a per-teammate pass. The proxy checks the pass,
// drops it, attaches the real provider credential, forwards the request and
// returns only the upstream response. Real credentials live in
// $DATA_DIR/credentials.json and never leave this process. Zero dependencies.
import { createHash, randomUUID } from "node:crypto";
import fs from "node:fs";
import http from "node:http";
import https from "node:https";
import path from "node:path";

const DATA_DIR = process.env.DATA_DIR ?? "/data";
const PORT = Number(process.env.PORT ?? 8787);
const BIND = process.env.BIND ?? "0.0.0.0";
const MAX_BODY_BYTES = 64 * 1024 * 1024;
const USAGE_ROTATE_BYTES = 20 * 1024 * 1024;
const REFRESH_MARGIN_MS = 5 * 60 * 1000;

const OPENAI_TOKEN_URL = "https://auth.openai.com/api/accounts/oauth/token";
const OPENAI_RESOURCE = "https://api.openai.com/v1";
// The Anthropic OAuth (setup-token) credential is only accepted with this beta flag.
const ANTHROPIC_OAUTH_BETA = "oauth-2025-04-20";

const CREDENTIALS_PATH = path.join(DATA_DIR, "credentials.json");
const PASSES_PATH = path.join(DATA_DIR, "passes.json");
const USAGE_PATH = path.join(DATA_DIR, "usage.jsonl");

/** Route table: path prefix to upstream base URL and the auth that provider needs. */
const PROVIDERS = {
  anthropic: { base: "https://api.anthropic.com", auth: anthropicAuth },
  openai: { base: "https://api.openai.com", auth: openaiAuth },
  opencode: { base: "https://opencode.ai/zen", auth: opencodeAuth },
};

// Headers that identify the caller or the hop; never forwarded upstream.
const DROP_REQUEST_HEADERS = new Set([
  "authorization",
  "x-api-key",
  "api-key",
  "cookie",
  "host",
  "connection",
  "content-length",
  "transfer-encoding",
  "accept-encoding",
  "forwarded",
  "x-forwarded-for",
  "x-forwarded-host",
  "x-forwarded-proto",
  "x-real-ip",
  "chatgpt-account-id",
]);
const DROP_RESPONSE_HEADERS = new Set(["set-cookie", "connection", "transfer-encoding", "keep-alive"]);

function log(event, fields = {}) {
  console.log(JSON.stringify({ t: new Date().toISOString(), event, ...fields }));
}

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function writeJSONAtomic(file, value) {
  const tmp = `${file}.${process.pid}.tmp`;
  fs.writeFileSync(tmp, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
  fs.renameSync(tmp, file);
}

/** Re-reads a JSON file when its mtime changes, so edits apply without a restart. */
function hotFile(file, empty) {
  let mtime = -1;
  let value = empty;
  return () => {
    try {
      const m = fs.statSync(file).mtimeMs;
      if (m !== mtime) {
        value = readJSON(file);
        mtime = m;
      }
    } catch (err) {
      if (err.code !== "ENOENT") log("reload_error", { file: path.basename(file), error: err.name });
      value = empty;
      mtime = -1;
    }
    return value;
  };
}

const getPasses = hotFile(PASSES_PATH, { passes: [] });
const getCredentials = hotFile(CREDENTIALS_PATH, {});

export function hashPass(pass) {
  return createHash("sha256").update(pass).digest("hex");
}

/** Per-pass request counts for the current UTC day, used for the optional daily cap. */
const dayCounts = new Map();
function countRequest(name) {
  const day = new Date().toISOString().slice(0, 10);
  const key = `${day}:${name}`;
  for (const k of dayCounts.keys()) if (!k.startsWith(day)) dayCounts.delete(k);
  const n = (dayCounts.get(key) ?? 0) + 1;
  dayCounts.set(key, n);
  return n;
}

function extractPass(headers) {
  const bearer = /^Bearer\s+(.+)$/i.exec(headers.authorization ?? "");
  return (bearer?.[1] ?? headers["x-api-key"] ?? headers["api-key"] ?? "").trim();
}

function findPass(pass) {
  if (!pass) return undefined;
  const hash = hashPass(pass);
  return getPasses().passes.find((p) => p.hash === hash);
}

// ---- provider auth ----

function anthropicAuth(headers) {
  const token = getCredentials().anthropic?.token;
  if (!token) throw new Error("anthropic credential not configured");
  headers.authorization = `Bearer ${token}`;
  const betas = new Set((headers["anthropic-beta"] ?? "").split(",").map((b) => b.trim()).filter(Boolean));
  betas.add(ANTHROPIC_OAUTH_BETA);
  headers["anthropic-beta"] = [...betas].join(",");
}

function opencodeAuth(headers) {
  const key = getCredentials().opencode?.apiKey;
  if (!key) throw new Error("opencode credential not configured");
  headers.authorization = `Bearer ${key}`;
}

let refreshInFlight;

/** Single-flight refresh of the ChatGPT login; the proxy is its only refresher. */
function refreshOpenAI(reason) {
  refreshInFlight ??= (async () => {
    const creds = getCredentials();
    const o = creds.openai;
    if (!o?.refresh || !o.clientId) throw new Error("openai credential not configured");
    const res = await fetch(OPENAI_TOKEN_URL, {
      method: "POST",
      headers: { accept: "application/json", "content-type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        grant_type: "refresh_token",
        client_id: o.clientId,
        refresh_token: o.refresh,
        resource: OPENAI_RESOURCE,
      }),
    });
    if (!res.ok) {
      // Status only: the body could echo token material.
      log("openai_refresh_failed", { status: res.status, reason });
      throw new Error(`openai refresh failed (${res.status})`);
    }
    const data = await res.json();
    if (typeof data.access_token !== "string" || typeof data.refresh_token !== "string" || !(data.expires_in > 0)) {
      log("openai_refresh_failed", { status: res.status, reason, error: "malformed response" });
      throw new Error("openai refresh returned a malformed response");
    }
    // Re-read in case the file changed while the request was in flight.
    const latest = readJSON(CREDENTIALS_PATH);
    latest.openai = {
      ...latest.openai,
      access: data.access_token,
      refresh: data.refresh_token,
      expires: Date.now() + data.expires_in * 1000,
    };
    writeJSONAtomic(CREDENTIALS_PATH, latest);
    log("openai_refreshed", { reason, expiresInSec: data.expires_in });
  })().finally(() => {
    refreshInFlight = undefined;
  });
  return refreshInFlight;
}

async function openaiAuth(headers) {
  let o = getCredentials().openai;
  if (!o?.access) throw new Error("openai credential not configured");
  if (typeof o.expires === "number" && o.expires - Date.now() < REFRESH_MARGIN_MS) {
    await refreshOpenAI("expiring");
    o = getCredentials().openai;
  }
  headers.authorization = `Bearer ${o.access}`;
}

// ---- request handling ----

function readBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    req.on("data", (c) => {
      size += c.length;
      if (size > MAX_BODY_BYTES) {
        reject(Object.assign(new Error("body too large"), { status: 413 }));
        req.destroy();
        return;
      }
      chunks.push(c);
    });
    req.on("end", () => resolve(Buffer.concat(chunks)));
    req.on("error", reject);
  });
}

function upstreamRequest(url, method, headers, body) {
  return new Promise((resolve, reject) => {
    const up = https.request(url, { method, headers }, resolve);
    up.on("error", reject);
    up.end(body);
  });
}

function send(res, status, message) {
  if (res.headersSent) return res.destroy();
  res.writeHead(status, { "content-type": "application/json" });
  res.end(JSON.stringify({ error: { message } }));
}

function appendUsage(entry) {
  try {
    const line = JSON.stringify(entry);
    try {
      if (fs.statSync(USAGE_PATH).size > USAGE_ROTATE_BYTES) fs.renameSync(USAGE_PATH, `${USAGE_PATH}.1`);
    } catch {}
    fs.appendFileSync(USAGE_PATH, `${line}\n`, { mode: 0o600 });
    console.log(line);
  } catch (err) {
    log("usage_write_error", { error: err.name });
  }
}

/** Best-effort token counts from the tail of a JSON or SSE response; bodies are never stored. */
function tokenUsage(tail) {
  const last = (re) => {
    let m;
    let v;
    while ((m = re.exec(tail))) v = Number(m[1]);
    return v;
  };
  const input = last(/"(?:input|prompt)_tokens"\s*:\s*(\d+)/g);
  const output = last(/"(?:output|completion)_tokens"\s*:\s*(\d+)/g);
  return input === undefined && output === undefined ? undefined : { input, output };
}

async function handle(req, res) {
  const started = Date.now();
  const url = new URL(req.url ?? "/", "http://proxy.invalid");

  if (url.pathname === "/healthz") {
    res.writeHead(200, { "content-type": "text/plain" });
    return res.end("ok\n");
  }

  const match = /^\/([a-z]+)(\/.*)?$/.exec(url.pathname);
  const provider = match && Object.hasOwn(PROVIDERS, match[1]) ? match[1] : undefined;
  if (!provider) return send(res, 404, "unknown route");

  const pass = findPass(extractPass(req.headers));
  if (!pass) {
    log("rejected", { reason: "bad pass", provider });
    return send(res, 401, "invalid or revoked proxy pass");
  }
  if (pass.dailyRequestCap && countRequest(pass.name) > pass.dailyRequestCap) {
    log("rejected", { reason: "cap", pass: pass.name, provider });
    return send(res, 429, "daily request cap reached for this pass");
  }

  const route = PROVIDERS[provider];
  // Build the target by string concatenation, then verify host and path prefix, so
  // "..", "//evil.example" or encoded variants cannot leave the provider's API.
  const base = new URL(route.base);
  const target = new URL(route.base + (match[2] ?? "/") + url.search);
  if (target.host !== base.host || !target.pathname.startsWith(`${base.pathname.replace(/\/$/, "")}/`)) {
    return send(res, 400, "bad path");
  }

  let body;
  try {
    body = await readBody(req);
  } catch (err) {
    return send(res, err.status ?? 400, "could not read request body");
  }

  const baseHeaders = {};
  for (const [k, v] of Object.entries(req.headers)) {
    if (!DROP_REQUEST_HEADERS.has(k)) baseHeaders[k] = v;
  }
  baseHeaders["accept-encoding"] = "identity";
  baseHeaders["content-length"] = String(body.length);

  const entry = { t: new Date(started).toISOString(), pass: pass.name, provider, method: req.method, path: target.pathname, reqBytes: body.length };
  try {
    let upstream;
    for (let attempt = 0; attempt < 2; attempt++) {
      const headers = { ...baseHeaders };
      await route.auth(headers);
      upstream = await upstreamRequest(target, req.method, headers, body);
      if (provider === "openai" && upstream.statusCode === 401 && attempt === 0) {
        upstream.resume();
        await refreshOpenAI("upstream 401");
        continue;
      }
      break;
    }
    const outHeaders = {};
    for (const [k, v] of Object.entries(upstream.headers)) {
      if (!DROP_RESPONSE_HEADERS.has(k)) outHeaders[k] = v;
    }
    res.writeHead(upstream.statusCode ?? 502, outHeaders);
    let respBytes = 0;
    let tail = "";
    upstream.on("data", (c) => {
      respBytes += c.length;
      tail = (tail + c.toString("latin1")).slice(-16384);
    });
    upstream.pipe(res);
    upstream.on("end", () => {
      appendUsage({ ...entry, status: upstream.statusCode, ms: Date.now() - started, respBytes, tokens: tokenUsage(tail) });
    });
    upstream.on("error", () => res.destroy());
    res.on("close", () => upstream.destroy());
  } catch (err) {
    const id = randomUUID().slice(0, 8);
    log("upstream_error", { id, pass: pass.name, provider, error: String(err.message).slice(0, 200) });
    appendUsage({ ...entry, status: 502, ms: Date.now() - started });
    send(res, 502, `upstream error (ref ${id})`);
  }
}

if (process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href) {
  const server = http.createServer((req, res) => {
    handle(req, res).catch((err) => {
      log("handler_error", { error: err.name });
      send(res, 500, "internal error");
    });
  });
  // Streaming completions can idle for long stretches.
  server.requestTimeout = 0;
  server.headersTimeout = 30_000;
  server.keepAliveTimeout = 65_000;
  server.listen(PORT, BIND, () => log("listening", { bind: BIND, port: PORT }));
}
