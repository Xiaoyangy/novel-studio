// Run with `node --test services/dashboard/test_client.js`; no browser/npm deps.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const page = fs.readFileSync(path.join(__dirname, "static/index.html"), "utf8");
const driver = page.slice(page.indexOf("/* ---------- 抽屉驱动 ---------- */"), page.indexOf("</script>"));
const declarations = page.match(/^let currentDetail = .*;$/m)[0] + "\n" +
  page.match(/^let detailRequest = .*;$/m)[0];
const api = page.match(/^const api = .*;$/m)[0];
const costFormatter = page.slice(page.indexOf("function fmtCharacterCost("), page.indexOf("const phaseName ="));
const planningProjection = page.slice(page.indexOf("function renderCharacterPlanningProjection("), page.indexOf("function renderOverview("));
const planningProgress = page.slice(page.indexOf("function planningWorkTitle("), page.indexOf("function card("));

test("planning shows accepted chapters, actual shadow target/cycle and formal bundles independently", () => {
  const context = vm.createContext({
    esc: value => String(value || "").replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;"),
    shortTime: String, stageName: { "project-all": "整弧规划", "preplan": "弧预规划" },
    data: { chapters_completed: 0, working: { mode: "planning", chapter: 0, target_chapter: 3, cycle: 8 },
      formal_planning: { planned_chapters: 2, expected_chapters: 3 },
      runtime: { current_stage: "project-all", execution: { active: true },
        planning_progress: { planning_phase: "context_bound", last_progress_kind: "cycle_committed",
          progress_seq: 77, last_progress_at: "business-time", heartbeat_at: "heartbeat-time", status: "stalled" } } },
  });
  const html = vm.runInContext(planningProgress + "\nrenderPlanningProgress(data)", context);
  assert.match(html, /已接受 0 章｜正在推演第 3 章／周期 8｜正式计划 2\/3/);
  assert.match(html, /本周期输入已绑定（不计业务进展）/);
  assert.match(html, /最近业务提交（运行级）：周期已提交 · business-time/);
  assert.match(html, /心跳 heartbeat-time（仅存活，不计进展）/);
  assert.match(html, /持久进展停滞/);
  assert.doesNotMatch(html, /正式计划 8|已接受 8|第 0 章/);
  context.data.runtime.execution.active = false;
  assert.match(vm.runInContext("renderPlanningProgress(data)", context), /规划目标第 3 章／周期 8（未运行）/);
  assert.doesNotMatch(vm.runInContext("renderPlanningProgress(data)", context), /正在推演/);
  context.data.runtime.current_stage = "preplan";
  assert.equal(vm.runInContext("renderPlanningProgress(data)", context), "");
});

test("planning fallback does not invent a cycle or promote heartbeat to business progress", () => {
  const context = vm.createContext({ esc: String, shortTime: String, stageName: { "project-all": "整弧规划" },
    data: { working: { mode: "planning", chapter: 0, target_chapter: 2 },
      runtime: { current_stage: "project-all", execution: { active: true } }, formal_planning: {} },
  });
  let html = vm.runInContext(planningProgress + "\nrenderPlanningProgress(data)", context);
  assert.match(html, /正在推演第 2 章｜正式计划 0\/\?/);
  assert.doesNotMatch(html, /周期|最近业务提交|心跳/);
  context.data.runtime.planning_progress = { planning_phase: "context_bound", heartbeat_at: "alive-only" };
  html = vm.runInContext("renderPlanningProgress(data)", context);
  assert.match(html, /尚无角色规划提交/);
  assert.doesNotMatch(html, /最近业务提交（运行级）：[^<]*alive-only/);
  context.data.working.target_chapter = "<script>bad()</script>";
  assert.equal(vm.runInContext("planningWorkTitle(data.working, data.runtime)", context), "整弧规划");
});

test("arc rehearsal keeps its stage token and never renders formal chapter progress", () => {
  const stages = page.match(/^const stageName = .*;$/m)[0];
  const context = vm.createContext({ esc: String, shortTime: String,
    data: { working: { mode: "planning", target_chapter: 1, cycle: 9 },
      runtime: { current_stage: "rehearse-arc", status: "running", execution: { active: true } },
      formal_planning: { planned_chapters: 2, expected_chapters: 3 } },
  });
  vm.runInContext(stages + "\n" + planningProgress, context);
  assert.equal(vm.runInContext("planningWorkTitle(data.working, data.runtime)", context),
    "整弧条件预演（非正式章节计划）");
  assert.equal(vm.runInContext("renderPlanningProgress(data)", context), "");
  context.data.runtime.status = "error";
  context.data.runtime.execution.active = false;
  assert.equal(vm.runInContext("planningWorkTitle(data.working, data.runtime)", context),
    "整弧条件预演（非正式章节计划）");
  assert.equal(context.data.runtime.current_stage, "rehearse-arc");
  context.data.runtime.current_stage = "project-all";
  context.data.runtime.execution.active = true;
  assert.match(vm.runInContext("renderPlanningProgress(data)", context), /正式计划 2\/3/);
  assert.match(vm.runInContext("planningWorkTitle(data.working, data.runtime)", context), /正在推演第 1 章／周期 9/);
});

test("current character planning is labeled as projected and escapes source identity", () => {
  const context = vm.createContext({
    esc: value => String(value || "").replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;"),
    projection: { state: "running", generation_id: "pg2_<script>private()</script>",
      usage_summary: { usage_calls: 2, cost_source: "estimated", cost_usd: 0.03 } },
  });
  const html = vm.runInContext(costFormatter + planningProjection + "\nrenderCharacterPlanningProjection(projection)", context);
  assert.match(html, /当前规划投影/);
  assert.match(html, /尚未写入正史/);
  assert.match(html, /正式记忆章号仍来自已接受账本/);
  assert.match(html, /按证据去重/);
  assert.match(html, /约 \$0\.03/);
  assert.doesNotMatch(html, /<script>/);
  assert.equal(vm.runInContext("renderCharacterPlanningProjection(null)", context), "");
  context.projection.state = "paused";
  assert.match(vm.runInContext("renderCharacterPlanningProjection(projection)", context), /已暂存/);
});

function characterCost(usage) {
  const context = vm.createContext({ usage });
  return vm.runInContext(costFormatter + "\nfmtCharacterCost(usage)", context);
}

test("character costs distinguish reported, estimated, unpriced and sleeping usage", () => {
  assert.equal(characterCost({ usage_calls: 0, cost_usd: 0, cost_complete: true }), "$0.00（未调用）");
  assert.equal(characterCost({ usage_calls: 1, tokens_in: 80, cost_source: "reported", cost_usd: 0 }), "$0.00（已知）");
  assert.equal(characterCost({ usage_calls: 1, cost_source: "reported", cost_usd: 1.2 }), "$1.20（已知）");
  assert.equal(characterCost({ usage_calls: 2, cost_source: "estimated", cost_usd: 0.0049 }), "约 $0.0049（估算）");
  assert.match(characterCost({ tokens_in: 100 }), /^未计价/);
  assert.match(characterCost({ usage_calls: 1, tokens_in: 100, cost_source: "unknown", cost_usd: 0 }), /^未计价/);
});

test("partially priced role and arbiter aggregates never present a complete total", () => {
  const text = characterCost({ usage_calls: 3, cost_usd: 0.0149, cost_source: "unknown", cost_complete: false,
    cost_sources: { reported: 1, estimated: 1, unknown: 1 }, unpriced_calls: 1 });
  assert.match(text, /^已计价小计/);
  assert.match(text, /1 次调用未计价/);
  assert.doesNotMatch(text, /总成本|（已知）/);
  // A mixed-model round can retain a priced subtotal while source=unknown.
  assert.match(characterCost({ usage_calls: 1, cost_usd: 0.005, cost_source: "unknown" }), /^已计价小计 \$0\.0050/);
  assert.match(characterCost({ usage_calls: 2, cost_usd: 0.005,
    cost_sources: { reported: 1, estimated: 1, unknown: 0 } }), /^约 .*（估算）$/);
  assert.match(characterCost({ usage_calls: 1, cost_usd: 0.005, cost_source: "unknown", unpriced_calls: 4 }),
    /4 次调用未计价/);
});

test("small positive character prices are never rounded to free", () => {
  const text = characterCost({ usage_calls: 1, cost_source: "estimated", cost_usd: 0.000001 });
  assert.equal(text, "约 <$0.0001（估算）");
  assert.doesNotMatch(text, /\$0\.00（/);
});

test("terminal failures need attention but are not counted as running", () => {
  const portfolio = page.slice(page.indexOf("function renderPortfolio("), page.indexOf("function applyView("));
  const metrics = { innerHTML: "" };
  const context = vm.createContext({ $: () => metrics, fmtNum: String, fmtMoney: String, novels: [
    { runtime: { status: "error", active: false }, health: { status: "ok" } },
    { runtime: { status: "running", active: true }, health: { status: "ok" } },
  ] });
  vm.runInContext(portfolio + "\nrenderPortfolio(novels)", context);
  assert.match(metrics.innerHTML, /正在执行<\/div><div class="v">1<\/div>/);
  assert.match(metrics.innerHTML, /待处理<\/div><div class="v">1<\/div>/);
  assert.equal(vm.runInContext("isAttention(novels[0])", context), true);
});

test("pipeline failures expose escaped details in the runtime panel", () => {
  const renderer = page.slice(page.indexOf("function renderLog("), page.indexOf("/* ---------- 抽屉驱动 ---------- */"));
  const context = vm.createContext({
    shortTime: String, ago: String, runtimeName: { error: "运行异常" },
    esc: value => String(value || "").replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;"),
    data: { runtime: { status: "error", recent_events: [{ failed: true, summary: "zero-init 失败", detail: "knowledge_ledger 缺失 <script>bad()</script>" }] } },
  });
  const rendered = vm.runInContext(renderer + "\nrenderLog(data)", context);
  assert.match(rendered, /失败详情/);
  assert.match(rendered, /knowledge_ledger 缺失 &lt;script&gt;/);
  assert.doesNotMatch(rendered, /<script>bad\(\)<\/script>/);
});

test("backend failure state is not overwritten by a later metadata timestamp", () => {
  const expression = page.match(/^  const errRecovered = (.*);$/m)[1];
  const context = vm.createContext({
    runtime: { last_error_recovered: false }, err: { time: "2026-09-05T00:00:00Z" },
    w: { last_at: "2026-09-06T00:00:00Z", last_failed: false },
  });
  assert.equal(vm.runInContext(expression, context), false);
  context.runtime.last_error_recovered = true;
  assert.equal(vm.runInContext(expression, context), true);
});

function harness() {
  const pending = [], elements = new Map(), listeners = new Map(), timers = new Map();
  let timerId = 0, refreshes = 0;
  const element = id => {
    if (!elements.has(id)) elements.set(id, {
      innerHTML: "", textContent: "", className: "",
      classList: { add() {}, remove() {}, toggle() {} }, addEventListener() {},
    });
    return elements.get(id);
  };
  const context = vm.createContext({
    AbortController,
    $: element,
    overlay: element("overlay"), drawer: element("drawer"),
    document: {
      hidden: false, querySelectorAll: () => [],
      addEventListener: (event, callback) => listeners.set(event, callback),
    },
    phaseName: {}, esc: String,
    refresh: () => refreshes++,
    setInterval: callback => { timers.set(++timerId, callback); return timerId; },
    clearInterval: id => timers.delete(id),
    fetch: (url, options) => new Promise((resolve, reject) => {
      pending.push({ url, signal: options.signal, reject,
        resolve: data => resolve({ ok: true, json: async () => data }) });
    }),
    ...Object.fromEntries(["Overview", "Log", "Setting", "Cast", "Growth", "Plan", "Offscreen", "Quality"]
      .map(name => ["render" + name, data => `${name}:${data.name}`])),
  });
  vm.runInContext(declarations + "\n" + api + "\n" + driver, context);
  return { pending, elements, listeners, timers, context, refreshes: () => refreshes,
    run: code => vm.runInContext(code, context),
    load: (dir, tab, polling = false) => {
      context.nextDir = dir; context.nextTab = tab; context.polling = polling;
      return vm.runInContext("currentDetail = nextDir; currentTab = nextTab; loadTab(polling)", context);
    },
  };
}

test("polling does not duplicate an in-flight detail request", async () => {
  const h = harness();
  const first = h.load("book", "cast");
  await h.load("book", "cast", true);
  await h.load("book", "cast", true);
  assert.equal(h.pending.length, 1);
  h.pending[0].resolve({ name: "人物" });
  await first;
  assert.equal(h.elements.get("#d-body").innerHTML, "Cast:人物");
  const second = h.load("book", "cast", true);
  assert.equal(h.pending.length, 2);
  h.pending[1].resolve({ name: "更新" });
  await second;
});

test("late responses cannot render into a different tab", async () => {
  const h = harness();
  const old = h.load("book", "setting");
  const current = h.load("book", "cast");
  assert.equal(h.pending[0].signal.aborted, true);
  h.pending[1].resolve({ name: "正确人物" });
  await current;
  // A server may still complete an aborted request: identity checks must hold.
  h.pending[0].resolve({ name: "过期设定" });
  await old;
  assert.equal(h.elements.get("#d-body").innerHTML, "Cast:正确人物");
});

test("switching books discards old overview errors", async () => {
  const h = harness();
  const old = h.load("旧书", "overview");
  const current = h.load("新书", "overview");
  h.pending[1].resolve({ name: "新书", phase: "writing" });
  await current;
  h.pending[0].reject(new Error("旧书加载失败"));
  await old;
  assert.equal(h.elements.get("#d-title").textContent, "新书");
  assert.equal(h.elements.get("#d-body").innerHTML, "Overview:新书");
});

test("closing the drawer cancels its request and ignores late success", async () => {
  const h = harness();
  const request = h.load("book", "quality");
  h.run("closeDetail()");
  assert.equal(h.pending[0].signal.aborted, true);
  h.pending[0].resolve({ name: "已关闭" });
  await request;
  assert.equal(h.elements.get("#d-body").innerHTML, "");
});

test("failed current request releases polling for a retry", async () => {
  const h = harness();
  const failed = h.load("book", "cast");
  h.pending[0].reject(new Error("断开"));
  await failed;
  assert.match(h.elements.get("#d-body").innerHTML, /断开/);
  const retry = h.load("book", "cast", true);
  assert.equal(h.pending.length, 2);
  h.pending[1].resolve({ name: "恢复" });
  await retry;
  assert.equal(h.elements.get("#d-body").innerHTML, "Cast:恢复");
});

test("background tabs stop polling and refresh when visible again", async () => {
  const h = harness();
  h.run("openDetail('book')");
  h.pending[0].resolve({ name: "book", phase: "writing" });
  // Let fetch + JSON + loadTab complete before exercising the interval.
  await new Promise(resolve => setImmediate(resolve));
  h.context.document.hidden = true;
  const initialRefreshes = h.refreshes();
  for (const callback of h.timers.values()) callback();
  assert.equal(h.pending.length, 1);
  assert.equal(h.refreshes(), initialRefreshes);
  h.context.document.hidden = false;
  h.listeners.get("visibilitychange")();
  assert.equal(h.pending.length, 2);
  assert.equal(h.refreshes(), initialRefreshes + 1);
  h.pending[1].resolve({ name: "book", phase: "writing" });
});
