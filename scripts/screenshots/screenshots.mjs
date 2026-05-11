#!/usr/bin/env node
/**
 * Capture portal screenshots in light + dark mode.
 *
 * Prerequisites: the dev stack is running (`make dev` or equivalent) so:
 *   - Postgres is reachable at APITEST_DATABASE_URL
 *   - The api-test binary is reachable at APITEST_BASE_URL
 *
 * The script:
 *   1. Seeds the audit_events / audit_payloads / api_keys tables with
 *      deterministic mock data.
 *   2. Drives a headless Chromium via Playwright through every portal page.
 *   3. Saves PNG screenshots into docs/images/portal/<page>-<theme>.png
 *      at 2x DPR.
 *
 * Re-run on every portal UI change. The seed step is idempotent (truncate +
 * insert) and uses a fixed PRNG seed so byte-stable PNGs only diff when the
 * rendered UI actually changes.
 */

import { chromium } from "playwright";
import pg from "pg";
import { mkdir, rm } from "node:fs/promises";
import { existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(__dirname, "..", "..");

const BASE_URL = process.env.APITEST_BASE_URL || "http://localhost:8080";
const DATABASE_URL =
  process.env.APITEST_DATABASE_URL ||
  "postgres://api:api@localhost:5432/apitest?sslmode=disable";
const API_KEY = process.env.APITEST_DEV_KEY || "devkey-please-change";
const OUT_DIR = resolve(REPO_ROOT, "docs/images/portal");

const VIEWPORT = { width: 1440, height: 900 };
const DEVICE_SCALE = 2;

// PAGES: each entry produces one screenshot per theme. `path` may be a
// string or a 0-arg function (so deep-link targets can reference state
// stashed by seed()). `prep` runs after navigation to drive the UI into
// the right state for the capture (e.g. click a row to open the detail
// panel).
const PAGES = [
  { slug: "login",     path: "/portal/login",          requiresAuth: false, prep: null },
  { slug: "dashboard", path: "/portal/",               requiresAuth: true,  prep: null },
  { slug: "endpoints", path: "/portal/endpoints",      requiresAuth: true,  prep: null },

  // endpoints-detail: deep-link to a specific endpoint so the right-pane
  // detail card (method, path, group, auth required, description, curl
  // hint) is visible alongside the catalog table.
  { slug: "endpoints-detail",
    path: "/portal/endpoints/flaky",
    requiresAuth: true,
    prep: async (page) => {
      // Wait for the detail header to render (vs. the "Select an endpoint"
      // placeholder).
      await page.waitForSelector('text="Endpoint"', { timeout: 5000 });
      await page.waitForTimeout(300);
    } },

  { slug: "audit",     path: "/portal/audit",          requiresAuth: true,
    prep: async (page) => {
      // Let the auto-refresh land at least one render before snapping.
      await page.waitForSelector("table tbody tr", { timeout: 5000 });
      await page.waitForTimeout(400);
    } },

  // discovery: native OpenAPI 3.1 reference rendered from /openapi.json.
  // Capture an expanded operation so the screenshot communicates what the
  // page actually does (parameter tables, response schema, examples) and
  // not just the closed-card list.
  { slug: "discovery",
    path: "/portal/discovery",
    requiresAuth: true,
    prep: async (page) => {
      await page.waitForSelector('section button code', { timeout: 5000 });
      // Click the first operation card so its parameter/response/schema
      // detail expands.
      await page.locator('section button').first().click();
      await page.waitForTimeout(400);
    } },

  // audit-detail: click the first row so the right-pane EventDetail card
  // (timestamp, duration, request_id, auth, user, remote, bytes, plus
  // request/response headers / query / body trees) is rendered.
  // Audit.tsx tracks `selected` in component state, so there's no URL
  // deep-link — we drive the click in prep.
  { slug: "audit-detail",
    path: "/portal/audit",
    requiresAuth: true,
    prep: async (page) => {
      await page.waitForSelector("table tbody tr", { timeout: 5000 });
      // Pick the first row that targets a seeded payload event so the
      // detail card actually renders headers/body trees, not just the
      // summary fields.
      const targetRow = page.locator(`tr:has-text("${DETAIL_TARGET_PATH}")`).first();
      await targetRow.click();
      await page.waitForSelector('text="Timestamp"', { timeout: 5000 });
      await page.waitForTimeout(500);
    } },

  { slug: "keys",      path: "/portal/keys",           requiresAuth: true,  prep: null },
  { slug: "config",    path: "/portal/config",         requiresAuth: true,  prep: null },
  { slug: "about",     path: "/portal/about",          requiresAuth: true,  prep: null },
];

const THEMES = [
  { slug: "light", classes: [] },
  { slug: "dark",  classes: ["dark"] },
];

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

// Endpoints mirror pkg/endpoints/*; method+path+name+group come straight
// from the registry. duration / errorRate are screenshot-only fixtures
// shaping how each row reads (slow rows get high ms; flaky/status rows
// get realistic error mixes).
const ENDPOINTS = [
  { name: "whoami",     group: "identity", method: "GET",    path: "/v1/whoami",         duration: [3, 18] },
  { name: "headers",    group: "identity", method: "GET",    path: "/v1/headers",        duration: [4, 22] },
  { name: "fixed",      group: "data",     method: "GET",    path: "/v1/fixed/alpha",    duration: [3, 14] },
  { name: "sized",      group: "data",     method: "GET",    path: "/v1/sized",          duration: [4, 60] },
  { name: "lorem",      group: "data",     method: "GET",    path: "/v1/lorem",          duration: [6, 28] },
  { name: "echo_get",   group: "echo",     method: "GET",    path: "/v1/echo",           duration: [3, 18] },
  { name: "echo_post",  group: "echo",     method: "POST",   path: "/v1/echo",           duration: [4, 22] },
  { name: "echo_put",   group: "echo",     method: "PUT",    path: "/v1/echo",           duration: [4, 24] },
  { name: "echo_delete",group: "echo",     method: "DELETE", path: "/v1/echo",           duration: [3, 18] },
  { name: "status",     group: "failure",  method: "GET",    path: "/v1/status/503",     duration: [2, 12], errorRate: 1.0, errorStatus: 503 },
  { name: "slow",       group: "failure",  method: "GET",    path: "/v1/slow",           duration: [200, 2400] },
  { name: "flaky",      group: "failure",  method: "GET",    path: "/v1/flaky",          duration: [3, 24], errorRate: 0.4, errorStatus: 503 },
];

const USERS = [
  { subject: "ca01195f-f6c6-488b-9f18-ae1bde84aa38", email: "alice@example.com", name: "Alice Anderson", auth: "oidc" },
  { subject: "9f8b2e1c-aa94-4c12-93e1-7d0f2c5a9b88", email: "bob@example.com",   name: "Bob Becker",     auth: "oidc" },
  { subject: "apikey:ci-runner",     email: null, name: null, auth: "apikey", apiKey: "ci-runner" },
  { subject: "apikey:dev-local",     email: null, name: null, auth: "apikey", apiKey: "dev-local" },
  { subject: "bearer:plexara-gw",    email: null, name: null, auth: "bearer" },
];

// Deterministic PRNG so the same seed produces the same screenshots
// across runs.
function mulberry32(seed) {
  return function () {
    let t = (seed += 0x6d2b79f5);
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rand = mulberry32(20260510); // YYYYMMDD

function randInt(lo, hi) { return Math.floor(rand() * (hi - lo + 1)) + lo; }
function pick(arr) { return arr[Math.floor(rand() * arr.length)]; }
function uuid() { return crypto.randomUUID(); }

function makeAuditEvent(now, ageSeconds) {
  const ts = new Date(now.getTime() - ageSeconds * 1000);
  const ep = pick(ENDPOINTS);
  const user = pick(USERS);
  const errorRate = ep.errorRate ?? 0.05;
  const failed = rand() < errorRate;
  const success = !failed;
  const durationMs = randInt(ep.duration[0], ep.duration[1]);
  const status = success ? 200 : (ep.errorStatus ?? pick([502, 503, 504]));
  const bytesIn = randInt(0, 240);
  const bytesOut = success ? randInt(48, 2400) : randInt(40, 180);
  const errorCategory = success ? null : pick(["upstream", "timeout", "synthetic"]);
  const errorMessage = success ? null : pick([
    "synthetic error",
    "flaky failure (roll=0.42 < rate=0.50)",
    "context deadline exceeded",
  ]);

  return {
    id: uuid(),
    ts,
    duration_ms: durationMs,
    request_id: uuid(),
    session_id: pick(["7SG2G43XYV6JOQZKTMW37GAPM4", "BXQ9K2P5R7T8YV3JM4NW6ZA8H1", "K3L9MNB2C5XV7YQ8RT4P6WZ1JD"]),
    user_subject: user.subject,
    user_email: user.email,
    auth_type: user.auth,
    api_key_name: user.auth === "apikey" ? user.apiKey : null,
    method: ep.method,
    path: ep.path,
    route_name: ep.name,
    endpoint_group: ep.group,
    status,
    bytes_in: bytesIn,
    bytes_out: bytesOut,
    success,
    error_message: errorMessage,
    error_category: errorCategory,
    remote_addr: pick(["10.0.1.42", "10.0.1.55", "192.168.1.10"]),
    user_agent: pick(["plexara-gw/1.0", "curl/8.4.0", "Mozilla/5.0"]),
    _endpoint: ep, // stashed for payload synthesis below
  };
}

// ---------------------------------------------------------------------------
// Seed
// ---------------------------------------------------------------------------

async function seed() {
  console.log("→ connecting to Postgres");
  const client = new pg.Client({ connectionString: DATABASE_URL });
  await client.connect();

  try {
    console.log("→ truncating audit_payloads + audit_events + api_keys");
    // audit_payloads cascades on audit_events delete, but TRUNCATE is
    // explicit per table so order doesn't matter here.
    await client.query("TRUNCATE audit_payloads");
    await client.query("TRUNCATE audit_events CASCADE");
    await client.query("TRUNCATE api_keys");

    console.log("→ inserting api_keys");
    // bcrypt hash of the literal string "demo-key-not-real" (cost 10)
    // so the table has rows without any of these keys actually
    // authenticating.
    const dummyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy";
    const apiKeys = [
      { name: "ci-runner",      desc: "CI integration tests",       created: "now() - interval '21 days'", lastUsed: "now() - interval '12 minutes'" },
      { name: "alice-personal", desc: "Alice personal exploration", created: "now() - interval '3 days'",  lastUsed: "now() - interval '2 hours'" },
      { name: "demo-only",      desc: "Read-only demo key",         created: "now() - interval '60 days'", lastUsed: "NULL" },
    ];
    for (const k of apiKeys) {
      await client.query(
        `INSERT INTO api_keys (id, name, hash, description, created_by, created_at, last_used_at)
         VALUES ($1, $2, $3, $4, 'alice@example.com', ${k.created}, ${k.lastUsed})`,
        [uuid(), k.name, dummyHash, k.desc],
      );
    }

    console.log("→ inserting audit_events");
    const now = new Date();
    const events = [];
    // 100 events over the past 75 minutes, weighted toward the recent end
    // so the dashboard "last 1h" window has visible activity.
    for (let i = 0; i < 100; i++) {
      const skew = Math.pow(rand(), 2.2);
      const ageSeconds = Math.floor(skew * 75 * 60);
      events.push(makeAuditEvent(now, ageSeconds));
    }

    // audit_events has 21 columns (no `parameters` like mcp-test; bodies
    // live in audit_payloads). Build placeholder string + flat values
    // array for one bulk INSERT.
    const COL_COUNT = 21;
    const placeholders = events.map((_, i) => {
      const o = i * COL_COUNT;
      const ph = Array.from({ length: COL_COUNT }, (_, j) => `$${o + j + 1}`).join(",");
      return `(${ph})`;
    }).join(",\n");

    const values = events.flatMap((e) => [
      e.id, e.ts, e.duration_ms, e.request_id, e.session_id,
      e.user_subject, e.user_email, e.auth_type, e.api_key_name,
      e.method, e.path, e.route_name, e.endpoint_group,
      e.status, e.bytes_in, e.bytes_out,
      e.success, e.error_message, e.error_category,
      e.remote_addr, e.user_agent,
    ]);

    await client.query(
      `INSERT INTO audit_events (
        id, ts, duration_ms, request_id, session_id,
        user_subject, user_email, auth_type, api_key_name,
        method, path, route_name, endpoint_group,
        status, bytes_in, bytes_out,
        success, error_message, error_category,
        remote_addr, user_agent
      ) VALUES ${placeholders}`,
      values,
    );

    // Seed audit_payloads for the 20 most-recent events so the audit
    // detail panel has real headers / query / body trees to render. The
    // other 80 events stay summary-only — realistic for a deployment
    // with capture_payloads on but post-retention payload pruning.
    console.log("→ inserting audit_payloads (20 most-recent events)");
    const recent = [...events]
      .sort((a, b) => b.ts.getTime() - a.ts.getTime())
      .slice(0, 20);

    for (const e of recent) {
      const ep = e._endpoint;
      const reqHeaders = {
        "User-Agent": [e.user_agent],
        "X-Forwarded-For": [e.remote_addr],
        "Content-Type": ["application/json"],
        // Sensitive headers are stored redacted; mirror the pkg/auth
        // RedactHeaders contract so the detail panel shows the
        // [redacted] values an operator will see in production.
        "Authorization": ["[redacted]"],
        "Cookie": ["[redacted]"],
      };
      const reqQuery = makeRequestQuery(ep);
      const reqBody = makeRequestBody(ep);
      const respHeaders = {
        "Content-Type": [e.success ? "application/json" : "text/plain; charset=utf-8"],
        "X-Request-Id": [e.request_id],
      };
      const respBody = makeResponseBody(ep, e, reqQuery);

      await client.query(
        `INSERT INTO audit_payloads (
          event_id,
          request_headers, request_query, request_content_type,
          request_body, request_size_bytes, request_truncated, request_remote_addr,
          response_headers, response_content_type,
          response_body, response_size_bytes, response_truncated,
          replayed_from, captured_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
        [
          e.id,
          JSON.stringify(reqHeaders),
          reqQuery ? JSON.stringify(reqQuery) : null,
          reqBody ? "application/json" : null,
          reqBody,                       // BYTEA
          reqBody ? reqBody.length : 0,
          false,
          e.remote_addr,
          JSON.stringify(respHeaders),
          e.success ? "application/json" : "text/plain; charset=utf-8",
          respBody,                      // BYTEA
          respBody ? respBody.length : 0,
          false,
          null,
          e.ts,
        ],
      );
    }

    console.log(`✓ seeded ${events.length} audit events (${recent.length} with payloads) + ${apiKeys.length} api keys`);

    // Stash the path of the most-recent successful payload event so the
    // capture step can drive a deterministic row click for the
    // audit-detail screenshot. Picking by path (not id) lets us match
    // the row even though the audit table doesn't render the id.
    const successful = recent.filter((e) => e.success);
    if (successful.length === 0) {
      throw new Error("seed produced no successful payload events; reduce errorRate or increase event count");
    }
    DETAIL_TARGET_PATH = successful[0].path;
  } finally {
    await client.end();
  }
}

function makeRequestQuery(ep) {
  switch (ep.name) {
    case "sized":   return { bytes: ["1024"] };
    case "lorem":   return { words: ["50"], seed: ["demo"] };
    case "slow":    return { ms: ["1200"] };
    case "flaky":   return { fail_rate: ["0.5"], seed: ["demo"] };
    default:        return null;
  }
}

function makeRequestBody(ep) {
  if (ep.method === "POST" || ep.method === "PUT" || ep.method === "PATCH") {
    return Buffer.from(JSON.stringify({
      message: "hello",
      extras: { traceId: "abc-123" },
    }));
  }
  return null;
}

function makeResponseBody(ep, e, reqQuery) {
  if (!e.success) {
    return Buffer.from(`error: ${e.error_message ?? "synthetic error"}\n`);
  }
  switch (ep.name) {
    case "whoami":
      return Buffer.from(JSON.stringify({
        subject: e.user_email ?? e.user_subject,
        auth_type: e.auth_type,
      }));
    case "headers":
      return Buffer.from(JSON.stringify({
        "User-Agent": e.user_agent,
        "X-Forwarded-For": e.remote_addr,
      }));
    case "fixed":
      return Buffer.from(JSON.stringify({ key: "alpha", value: "static-fixture" }));
    case "sized":
      return Buffer.from("x".repeat(240) + "...");
    case "lorem":
      return Buffer.from("Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod...");
    case "slow":
      return Buffer.from(JSON.stringify({ slept_ms: Number(reqQuery?.ms?.[0] ?? e.duration_ms) }));
    case "echo_get":
    case "echo_post":
    case "echo_put":
    case "echo_patch":
    case "echo_delete":
      return Buffer.from(JSON.stringify({
        method: ep.method,
        path: ep.path,
        echoed_at: new Date().toISOString(),
      }));
    default:
      return Buffer.from(JSON.stringify({ ok: true, route: ep.name }));
  }
}

// Filled by seed() and consumed by the capture step's prep functions.
let DETAIL_TARGET_PATH = null;

// ---------------------------------------------------------------------------
// Capture
// ---------------------------------------------------------------------------

async function capture() {
  console.log(`→ launching Chromium against ${BASE_URL}`);
  if (existsSync(OUT_DIR)) await rm(OUT_DIR, { recursive: true });
  await mkdir(OUT_DIR, { recursive: true });

  const browser = await chromium.launch();
  const context = await browser.newContext({
    viewport: VIEWPORT,
    deviceScaleFactor: DEVICE_SCALE,
  });

  // Reduce motion so background animations don't shimmer between captures.
  await context.addInitScript(() => {
    const css = `*,*::before,*::after{animation-duration:0s !important;animation-delay:0s !important;transition-duration:0s !important}`;
    const s = document.createElement("style");
    s.textContent = css;
    (document.head || document.documentElement).appendChild(s);
  });

  // Establish the portal origin so localStorage / sessionStorage are usable.
  const page = await context.newPage();
  await page.goto(`${BASE_URL}/portal/login`, { waitUntil: "domcontentloaded" });
  await page.evaluate((key) => sessionStorage.setItem("api-test-api-key", key), API_KEY);

  for (const target of PAGES) {
    for (const theme of THEMES) {
      // Set theme + api key in storage before navigation. The portal's
      // theme module reads localStorage at parse time and applies the
      // .dark class before stylesheets load, avoiding any flash.
      await page.evaluate(
        ({ themeSlug, apiKey, requiresAuth }) => {
          localStorage.setItem("api-test-theme", themeSlug);
          if (requiresAuth) sessionStorage.setItem("api-test-api-key", apiKey);
          else              sessionStorage.removeItem("api-test-api-key");
        },
        { themeSlug: theme.slug, apiKey: API_KEY, requiresAuth: target.requiresAuth },
      );

      const targetPath = typeof target.path === "function" ? target.path() : target.path;
      await page.goto(`${BASE_URL}${targetPath}`, { waitUntil: "networkidle" });
      // Some portal pages trigger queries; wait a short beat for them to settle.
      await page.waitForTimeout(500);

      // Re-apply the .dark class in case React replaced it.
      await page.evaluate((dark) => {
        const html = document.documentElement;
        if (dark) html.classList.add("dark");
        else      html.classList.remove("dark");
      }, theme.slug === "dark");

      if (target.prep) await target.prep(page);
      await page.waitForTimeout(200);

      const out = resolve(OUT_DIR, `${target.slug}-${theme.slug}.png`);
      await page.screenshot({ path: out, fullPage: false });
      console.log(`  ✓ ${target.slug} (${theme.slug}) → ${out.replace(REPO_ROOT + "/", "")}`);
    }
  }

  await browser.close();
}

// ---------------------------------------------------------------------------
// Run
// ---------------------------------------------------------------------------

(async () => {
  try {
    await seed();
    await capture();
    console.log("\nDone. Screenshots in docs/images/portal/.");
  } catch (err) {
    console.error("FAIL:", err.message);
    process.exit(1);
  }
})();
