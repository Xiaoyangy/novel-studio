const novelState = {
  novels: [],
  currentNovel: null,
  currentChapter: null,
  currentChapterDetail: null,
  materials: [],
  currentFile: "",
  pollTimer: null,
  polling: false,
  relationshipFocus: "all",
  selectedRelationship: null,
  graphFrame: null,
  graphDrag: null,
};

const $ = (id) => document.getElementById(id);

function toast(message) {
  const el = $("toast");
  el.textContent = message;
  el.hidden = false;
  setTimeout(() => {
    el.hidden = true;
  }, 2600);
}

async function api(path) {
  const res = await fetch(path, { headers: { "Accept": "application/json" } });
  const text = await res.text();
  let data = {};
  try {
    data = text ? JSON.parse(text) : {};
  } catch {
    data = { error: text.slice(0, 180) };
  }
  if (!res.ok) {
    const hint = res.status === 404 && path === "/api/novels"
      ? "当前 8765 服务是旧版 dashboard，请重新执行 ./novel-studio service open。"
      : data.error || `HTTP ${res.status}`;
    throw new Error(hint);
  }
  return data;
}

async function loadNovels() {
  const data = await api("/api/novels");
  novelState.novels = data.novels || [];
  $("novelCount").textContent = String(novelState.novels.length);
  renderNovelList();
  if (!novelState.currentNovel && novelState.novels.length) {
    await loadNovel(novelState.novels[0].id);
  } else if (!novelState.novels.length) {
    showEmpty("暂无小说项目。运行创作后，或把产物放到 output/novel，即可在这里查看。");
  }
}

function showEmpty(message) {
  $("emptyState").textContent = message;
  $("emptyState").hidden = false;
  $("novelDetail").hidden = true;
}

async function loadNovel(id, options = {}) {
  const detail = await api(`/api/novels/${encodeURIComponent(id)}`);
  novelState.currentNovel = detail;
  novelState.materials = detail.materials || [];
  novelState.relationshipFocus = options.preserveRelationship ? novelState.relationshipFocus : "all";
  novelState.selectedRelationship = options.preserveRelationship ? novelState.selectedRelationship : null;
  $("emptyState").hidden = true;
  $("novelDetail").hidden = false;
  renderNovelList();
  renderNovelDetail();
  renderOverview();
  renderRelationshipControls();
  renderRelationshipGraph({ animate: !options.preserveRelationship });
  renderRelationshipList();
  renderMaterialControls();
  renderMaterials();

  if (novelState.currentChapter && (detail.chapters || []).some((item) => item.number === novelState.currentChapter)) {
    await loadChapter(novelState.currentChapter, { preserveFile: true });
  } else {
    $("chapterDetailSection").hidden = true;
  }

  if (!options.preserveFile && novelState.materials.length) {
    await loadFile(preferredMaterial(novelState.materials));
  } else if (options.preserveFile && novelState.currentFile) {
    await loadFile(novelState.currentFile, { silentMissing: true });
  }
}

function preferredMaterial(materials) {
  const item = materials.find((entry) => entry.path === "meta/project_progress.md")
    || materials.find((entry) => entry.path === "meta/chapter_progress.md")
    || materials.find((entry) => entry.path === "meta/progress.json")
    || materials.find((entry) => entry.group === "chapters")
    || materials[0];
  return item?.path || "";
}

async function refreshLiveData() {
  if (novelState.polling) return;
  novelState.polling = true;
  try {
    const currentId = novelState.currentNovel?.id;
    await loadNovels();
    if (currentId && novelState.novels.some((item) => item.id === currentId)) {
      await loadNovel(currentId, { preserveFile: true, preserveRelationship: true });
    }
  } finally {
    novelState.polling = false;
  }
}

function renderNovelList() {
  const list = $("novelList");
  list.innerHTML = "";
  if (!novelState.novels.length) {
    list.innerHTML = `<div class="empty">暂无 output/novel 项目。</div>`;
    return;
  }
  novelState.novels.forEach((novel) => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "project-item";
    btn.dataset.novelId = novel.id;
    if (novelState.currentNovel && novelState.currentNovel.id === novel.id) {
      btn.classList.add("active");
    }
    btn.innerHTML = `
      <span class="project-title-line">
        <span class="project-title">${escapeHtml(novel.title)}</span>
        <span class="project-status-badge status-running">${escapeHtml(phaseLabel(novel.phase))}</span>
      </span>
      <span class="project-meta">${escapeHtml(novel.source)} · ${escapeHtml(novel.flow || "-")} · ${formatNumber(novel.progress_pct, 1)}%</span>
      <span class="project-meta">${formatNumber(novel.completed_count)}/${formatNumber(novel.total_chapters)} 章 · ${escapeHtml(compactTime(novel.updated_at))}</span>
    `;
    btn.addEventListener("click", () => {
      novelState.currentChapter = null;
      novelState.currentChapterDetail = null;
      loadNovel(novel.id);
    });
    list.appendChild(btn);
  });
}

function renderNovelDetail() {
  const novel = novelState.currentNovel;
  if (!novel) return;
  $("detailTitle").textContent = novel.title;
  $("detailMeta").textContent = `${novel.relative_root} · ${novel.source}`;
  $("lastUpdated").textContent = `刷新 ${compactTime(new Date().toISOString())} · 数据 ${compactTime(novel.updated_at)}`;
  $("novelProgress").style.width = `${boundedPct(novel.progress_pct)}%`;
  $("novelProgressText").textContent = `${formatNumber(novel.progress_pct, 1)}%`;

  const pipeline = novel.pipeline || {};
  const pending = novel.pending_rewrites || [];
  const cards = [
    ["章节进度", `${formatNumber(novel.completed_count)}/${formatNumber(novel.total_chapters)}`, `当前第 ${formatNumber(novel.current_chapter)} 章`, novel.progress_pct >= 100 ? "ok" : ""],
    ["Phase / Flow", `${phaseLabel(novel.phase)} / ${flowLabel(novel.flow)}`, `卷 ${formatNumber(novel.current_volume)} · 弧 ${formatNumber(novel.current_arc)}`, novel.phase === "complete" ? "ok" : ""],
    ["总字数", formatNumber(novel.total_word_count), novel.target_word_count_range || "未配置目标字数", novel.total_word_count > 0 ? "ok" : ""],
    ["返工队列", pending.length ? pending.join(", ") : "无", pending.length ? "优先处理 pending_rewrites" : "无阻塞返工", pending.length ? "warn" : "ok"],
    ["Pipeline", `${formatNumber((pipeline.completed || []).length)}/${formatNumber((pipeline.stages || []).length)}`, (pipeline.stages || []).join(" -> ") || "未记录 pipeline", ""],
    ["产物文件", formatNumber(novel.counts?.materials || novelState.materials.length), `${formatNumber(novel.counts?.reviews || 0)} 审核产物`, "ok"],
  ];
  $("metricsGrid").innerHTML = cards.map(([label, value, note, state]) => `
    <div class="metric-card ${state}">
      <span class="metric-label">${escapeHtml(label)}</span>
      <span class="metric-value">${escapeHtml(value)}</span>
      <span class="metric-note">${escapeHtml(note)}</span>
    </div>
  `).join("");

  renderChapters(novel.chapters || []);
}

function renderOverview() {
  const overview = novelState.currentNovel?.overview || {};
  const world = overview.world || {};
  const planning = overview.planning || {};
  $("overviewSummary").textContent = `${formatNumber((overview.outline_status || []).length)} 个弧线 · ${formatNumber((overview.characters || []).length)} 人物 · ${formatNumber((overview.relationships || []).length)} 条关系 · ${formatNumber((world.rules || []).length)} 条世界规则`;
  renderOutlineProgress(overview.outline_status || []);
  renderPlanning(planning);
  renderWorld(world);
  renderWorldRulePanel(world.rules || []);
  renderCharacters(overview.characters || []);
  renderOverviewFiles(overview.files || []);
}

function renderOutlineProgress(items) {
  const target = $("outlineProgressList");
  if (!items.length) {
    target.innerHTML = `<div class="empty compact">暂无结构化大纲进度。</div>`;
    return;
  }
  target.innerHTML = items.map((item) => {
    const total = Number(item.total_chapters) || 0;
    const done = Number(item.completed_chapters) || 0;
    const pct = total ? Math.round(done / total * 100) : 0;
    return `
      <div class="outline-row ${escapeHtml(item.status || "")}">
        <div>
          <strong>V${formatNumber(item.volume)}A${formatNumber(item.arc)} · ${escapeHtml(item.arc_title || item.volume_title || "-")}</strong>
          <span>${escapeHtml(item.goal || "")}</span>
          <small>第 ${formatNumber(item.start_chapter)}-${formatNumber(item.end_chapter)} 章 · ${statusLabel(item.status)}</small>
        </div>
        <div class="outline-progress">
          <em>${formatNumber(done)}/${formatNumber(total)}</em>
          <i><b style="width:${boundedPct(pct)}%"></b></i>
        </div>
      </div>
    `;
  }).join("");
}

function renderPlanning(planning) {
  const next = planning.next_plan || {};
  const actions = planning.next_chapter_actions || [];
  const patterns = planning.patterns || [];
  const health = planning.health || {};
  const hook = planning.hook_analysis || {};
  const blocks = [];
  if (next.chapter || next.title) {
    blocks.push(`
      <div class="stack-item strong">
        <strong>下一章：第${formatNumber(next.chapter)}章 ${escapeHtml(next.title || "")}</strong>
        <span>${escapeHtml(next.core_event || next.hook || "等待下一章规划")}</span>
      </div>
    `);
  }
  if (health.status || health.score !== undefined) {
    blocks.push(`
      <div class="stack-item">
        <strong>推进健康度 ${escapeHtml(health.status || "-")} · ${formatNumber(health.score)}</strong>
        <span>已审阅 ${formatNumber(health.accepted_reviewed)} / 已完成 ${formatNumber(health.completed)} · 近窗 AI 风险 ${formatNumber(health.recent_ai_voice_score, 2)}</span>
      </div>
    `);
  }
  if (Object.keys(hook).length) {
    blocks.push(`
      <div class="stack-item">
        <strong>钩子结构</strong>
        <span>${Object.entries(hook.hook_type_counts || {}).map(([key, value]) => `${key} ${value}`).join(" · ") || "已有钩子统计"}</span>
      </div>
    `);
  }
  actions.slice(0, 6).forEach((action) => blocks.push(`<div class="stack-item"><span>${escapeHtml(action)}</span></div>`));
  patterns.slice(0, 4).forEach((item) => blocks.push(`
    <div class="stack-item warn">
      <strong>${escapeHtml(item.category || item.id || "风险")}</strong>
      <span>${escapeHtml(item.diagnosis || item.recommended_fix || "")}</span>
    </div>
  `));
  $("planningProgress").innerHTML = blocks.join("") || `<div class="empty compact">暂无规划进度。</div>`;
}

function renderWorld(world) {
  const rules = world.rules || [];
  const timeline = world.timeline || [];
  const resources = world.resources || [];
  const foreshadow = world.foreshadow_plan || [];
  const blocks = [];
  rules.slice(0, 8).forEach((rule) => blocks.push(`
    <div class="stack-item">
      <strong>${escapeHtml(rule.category || "规则")}</strong>
      <span>${escapeHtml(rule.rule || rule.boundary || "")}</span>
    </div>
  `));
  timeline.slice(0, 8).forEach((event) => blocks.push(`
    <div class="stack-item timeline">
      <strong>第${formatNumber(event.chapter)}章 · ${escapeHtml(event.time || "")}</strong>
      <span>${escapeHtml(event.event || event.hook || "")}</span>
    </div>
  `));
  resources.slice(0, 4).forEach((item) => blocks.push(`
    <div class="stack-item asset">
      <strong>${escapeHtml(item.name || item.id || "资源")}</strong>
      <span>${escapeHtml(item.status || "")} · ${escapeHtml(item.risk || item.evidence || "")}</span>
    </div>
  `));
  foreshadow.slice(0, 4).forEach((item) => blocks.push(`
    <div class="stack-item hook">
      <strong>${escapeHtml(item.id || "伏笔")}</strong>
      <span>${escapeHtml(item.action || item.description || "")}</span>
    </div>
  `));
  $("worldLine").innerHTML = blocks.join("") || `<div class="empty compact">暂无世界线资料。</div>`;
}

function renderWorldRulePanel(rules) {
  const target = $("worldRuleList");
  if (!target) return;
  if (!rules.length) {
    target.innerHTML = `<div class="empty compact">暂无世界规则。</div>`;
    return;
  }
  target.innerHTML = rules.map((rule) => `
    <article class="world-rule-item">
      <strong>${escapeHtml(rule.category || "世界规则")}</strong>
      <p>${escapeHtml(rule.rule || "")}</p>
      ${rule.boundary ? `<span>${escapeHtml(rule.boundary)}</span>` : ""}
    </article>
  `).join("");
}

function renderCharacters(characters) {
  const target = $("characterArcs");
  if (!characters.length) {
    target.innerHTML = `<div class="empty compact">暂无人物资料。</div>`;
    return;
  }
  target.innerHTML = characters.map((item) => `
    <article class="character-card">
      <div class="character-head">
        <strong>${escapeHtml(item.name || "-")}</strong>
        <span>${escapeHtml(item.role || "-")} · ${escapeHtml(item.tier || "-")} · 出场 ${formatNumber(item.appearance_count)} 次</span>
      </div>
      <p>${escapeHtml(item.description || "")}</p>
      <p>${escapeHtml(item.arc || "")}</p>
      <div class="tag-row">${(item.traits || []).slice(0, 8).map((tag) => `<em>${escapeHtml(tag)}</em>`).join("")}</div>
      ${(item.current_facts || []).length ? `<ul>${item.current_facts.map((fact) => `<li>${escapeHtml(fact)}</li>`).join("")}</ul>` : ""}
    </article>
  `).join("");
}

function renderOverviewFiles(files) {
  const target = $("overviewFiles");
  if (!files.length) {
    target.innerHTML = `<div class="empty compact">暂无关键资料文件。</div>`;
    return;
  }
  target.innerHTML = "";
  files.forEach((file) => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "quick-file";
    btn.innerHTML = `<strong>${escapeHtml(file.name)}</strong><span>${escapeHtml(file.group_label)} · ${formatBytes(file.size)}</span>`;
    btn.addEventListener("click", () => loadFile(file.path));
    target.appendChild(btn);
  });
}

function relationshipCharacters(overview = novelState.currentNovel?.overview || {}) {
  const characters = [...(overview.characters || [])];
  const known = new Set(characters.map((item) => item.name));
  (overview.relationships || []).forEach((rel) => {
    [rel.character_a, rel.character_b].forEach((name) => {
      if (!name || known.has(name)) return;
      const related = (overview.relationships || [])
        .filter((item) => item.character_a === name || item.character_b === name)
        .map((item) => {
          const other = item.character_a === name ? item.character_b : item.character_a;
          return `与${other}：${item.relation || ""}`;
        });
      characters.push({
        name,
        role: "关系端点",
        tier: "support",
        appearance_count: 0,
        description: "该节点来自章节关系推进记录，尚未沉淀为完整人物卡，但已经参与当前关系网络。",
        arc: related[0] || "",
        current_facts: related.slice(0, 3),
        relationships: related,
      });
      known.add(name);
    });
  });
  return characters;
}

function renderRelationshipControls() {
  const overview = novelState.currentNovel?.overview || {};
  const characters = relationshipCharacters(overview);
  const select = $("relationshipFilter");
  const current = characters.some((item) => item.name === novelState.relationshipFocus) ? novelState.relationshipFocus : "all";
  novelState.relationshipFocus = current;
  select.innerHTML = `<option value="all">全部人物</option>`;
  characters.forEach((item) => {
    const option = document.createElement("option");
    option.value = item.name;
    option.textContent = item.name;
    option.selected = item.name === current;
    select.appendChild(option);
  });
}

function relationshipDataset() {
  const overview = novelState.currentNovel?.overview || {};
  const characters = relationshipCharacters(overview);
  const relationships = overview.relationships || [];
  const focus = novelState.relationshipFocus;
  const allowed = new Set();
  if (focus === "all") {
    characters.forEach((item) => allowed.add(item.name));
  } else {
    allowed.add(focus);
    relationships.forEach((rel) => {
      if (rel.character_a === focus) allowed.add(rel.character_b);
      if (rel.character_b === focus) allowed.add(rel.character_a);
    });
  }
  const nodes = characters
    .filter((item) => allowed.has(item.name))
    .map((item) => ({
      id: item.name,
      label: item.name,
      role: item.role || "",
      tier: item.tier || "",
      appearance_count: Number(item.appearance_count) || 0,
      description: item.description || "",
      arc: item.arc || "",
      current_facts: item.current_facts || [],
      relationships: item.relationships || [],
      type: item.role === "关系端点" ? "endpoint" : "character",
    }));
  const nodeSet = new Set(nodes.map((node) => node.id));
  const links = relationships
    .filter((rel) => nodeSet.has(rel.character_a) && nodeSet.has(rel.character_b))
    .map((rel, index) => ({
      id: `${rel.character_a}->${rel.character_b}-${rel.chapter || index}`,
      source: rel.character_a,
      target: rel.character_b,
      relation: rel.relation || "",
      chapter: rel.chapter || "",
    }));
  return { nodes, links };
}

function renderRelationshipGraph({ animate = true } = {}) {
  const target = $("relationshipGraph");
  const { nodes, links } = relationshipDataset();
  if (novelState.graphFrame) cancelAnimationFrame(novelState.graphFrame);
  novelState.graphFrame = null;
  const personCount = nodes.filter((node) => node.type === "character" || node.type === "endpoint").length;
  $("relationshipStats").textContent = `${formatNumber(personCount)} 人物/端点 · ${formatNumber(links.length)} 关系`;
  if (!nodes.length) {
    target.innerHTML = `<div class="empty compact">暂无人物关系数据。</div>`;
    $("relationshipInspector").textContent = "暂无人物关系数据。";
    return;
  }

  const width = 560;
  const height = 390;
  const centerX = width / 2;
  const centerY = height / 2;
  nodes.forEach((node, index) => {
    const angle = -Math.PI / 2 + index * Math.PI * 2 / Math.max(nodes.length, 1);
    const radius = (nodes.length <= 4 ? 118 : 156) + (animate ? (Math.random() - 0.5) * 18 : 0);
    const jitterX = animate ? (Math.random() - 0.5) * 28 : 0;
    const jitterY = animate ? (Math.random() - 0.5) * 28 : 0;
    node.x = centerX + Math.cos(angle) * radius + jitterX;
    node.y = centerY + Math.sin(angle) * radius + jitterY;
    node.vx = 0;
    node.vy = 0;
  });

  target.innerHTML = `
    <svg viewBox="0 0 ${width} ${height}" class="relationship-svg">
      <defs>
        <filter id="nodeShadow" x="-20%" y="-20%" width="140%" height="140%">
          <feDropShadow dx="0" dy="8" stdDeviation="8" flood-color="#10202a" flood-opacity=".16"/>
        </filter>
      </defs>
      <g class="graph-links"></g>
      <g class="graph-labels"></g>
      <g class="graph-nodes"></g>
    </svg>
  `;
  const svg = target.querySelector("svg");
  const linkLayer = target.querySelector(".graph-links");
  const labelLayer = target.querySelector(".graph-labels");
  const nodeLayer = target.querySelector(".graph-nodes");

  const linkEls = links.map((link) => {
    const line = document.createElementNS("http://www.w3.org/2000/svg", "line");
    line.classList.add("graph-link");
    line.dataset.linkId = link.id;
    line.addEventListener("click", () => selectRelationship(link));
    linkLayer.appendChild(line);
    return line;
  });
  const labelEls = links.map((link) => {
    const text = document.createElementNS("http://www.w3.org/2000/svg", "text");
    text.classList.add("graph-edge-label");
    text.textContent = compactText(link.relation, 14);
    text.dataset.linkId = link.id;
    text.addEventListener("click", () => selectRelationship(link));
    labelLayer.appendChild(text);
    return text;
  });
  const nodeEls = nodes.map((node) => {
    const group = document.createElementNS("http://www.w3.org/2000/svg", "g");
    group.classList.add("graph-node", `graph-node-${node.type || "character"}`);
    group.dataset.nodeId = node.id;
    group.innerHTML = `
      <circle r="${nodeRadius(node)}"></circle>
      <text class="node-name" text-anchor="middle" y="-2">${escapeSvg(node.label.slice(0, 5))}</text>
      <text class="node-role" text-anchor="middle" y="16">${escapeSvg(compactText(node.role, 8))}</text>
    `;
    group.addEventListener("click", () => selectCharacter(node));
    nodeLayer.appendChild(group);
    return group;
  });

  function tick() {
    simulateForces(nodes, links, width, height);
    applyGraphPositions(nodes, links, nodeEls, linkEls, labelEls);
  }
  if (animate) {
    let frames = 0;
    const run = () => {
      tick();
      frames += 1;
      if (frames < 80 && !novelState.graphDrag) {
        novelState.graphFrame = requestAnimationFrame(run);
      }
    };
    run();
  } else {
    for (let i = 0; i < 24; i += 1) tick();
  }
  if (!novelState.selectedRelationship) {
    selectCharacter(nodes[0], { silentGraph: true });
  } else {
    highlightGraphSelection();
  }
}

function simulateForces(nodes, links, width, height) {
  const byId = new Map(nodes.map((node) => [node.id, node]));
  const centerX = width / 2;
  const centerY = height / 2;
  nodes.forEach((node) => {
    node.vx += (centerX - node.x) * 0.0022;
    node.vy += (centerY - node.y) * 0.0022;
  });
  for (let i = 0; i < nodes.length; i += 1) {
    for (let j = i + 1; j < nodes.length; j += 1) {
      const a = nodes[i];
      const b = nodes[j];
      const dx = b.x - a.x || 0.01;
      const dy = b.y - a.y || 0.01;
      const distanceSq = Math.max(80, dx * dx + dy * dy);
      const force = 780 / distanceSq;
      const fx = force * dx;
      const fy = force * dy;
      a.vx -= fx;
      a.vy -= fy;
      b.vx += fx;
      b.vy += fy;
    }
  }
  links.forEach((link) => {
    const source = byId.get(link.source);
    const target = byId.get(link.target);
    if (!source || !target) return;
    const dx = target.x - source.x || 0.01;
    const dy = target.y - source.y || 0.01;
    const distance = Math.sqrt(dx * dx + dy * dy);
    const desired = 138;
    const force = (distance - desired) * 0.012;
    const fx = force * dx / distance;
    const fy = force * dy / distance;
    source.vx += fx;
    source.vy += fy;
    target.vx -= fx;
    target.vy -= fy;
  });
  nodes.forEach((node) => {
    if (node.fixed) return;
    node.vx *= 0.86;
    node.vy *= 0.86;
    node.x = Math.max(48, Math.min(width - 48, node.x + node.vx));
    node.y = Math.max(46, Math.min(height - 46, node.y + node.vy));
  });
}

function applyGraphPositions(nodes, links, nodeEls, linkEls, labelEls) {
  const byId = new Map(nodes.map((node) => [node.id, node]));
  links.forEach((link, index) => {
    const source = byId.get(link.source);
    const target = byId.get(link.target);
    if (!source || !target) return;
    linkEls[index].setAttribute("x1", source.x);
    linkEls[index].setAttribute("y1", source.y);
    linkEls[index].setAttribute("x2", target.x);
    linkEls[index].setAttribute("y2", target.y);
    if (labelEls[index]) {
      labelEls[index].setAttribute("x", (source.x + target.x) / 2);
      labelEls[index].setAttribute("y", (source.y + target.y) / 2 - 6);
    }
  });
  nodes.forEach((node, index) => {
    nodeEls[index].setAttribute("transform", `translate(${node.x},${node.y})`);
  });
}

function nodeRadius(node) {
  if (node.type === "endpoint") return 23;
  const base = node.role.includes("主角") ? 34 : node.role.includes("重要") ? 30 : 26;
  return Math.min(42, base + Math.sqrt(Math.max(0, node.appearance_count)) * 2);
}

function selectCharacter(node, options = {}) {
  novelState.selectedRelationship = { type: "node", id: node.id };
  $("relationshipInspector").innerHTML = `
    <div class="inspector-title">
      <strong>${escapeHtml(node.label)}</strong>
      <span>${escapeHtml(node.role || "-")} · ${escapeHtml(node.tier || "-")} · 出场 ${formatNumber(node.appearance_count)} 次</span>
    </div>
    <p>${escapeHtml(node.description || "")}</p>
    <p>${escapeHtml(node.arc || "")}</p>
    ${(node.current_facts || []).length ? `<ul>${node.current_facts.map((fact) => `<li>${escapeHtml(fact)}</li>`).join("")}</ul>` : ""}
  `;
  highlightGraphSelection();
  if (!options.silentGraph) renderRelationshipList();
}

function selectRelationship(link) {
  novelState.selectedRelationship = { type: "link", id: link.id };
  $("relationshipInspector").innerHTML = `
    <div class="inspector-title">
      <strong>${escapeHtml(link.source)} / ${escapeHtml(link.target)}</strong>
      <span>第 ${formatNumber(link.chapter)} 章推进</span>
    </div>
    <p>${escapeHtml(link.relation || "")}</p>
  `;
  highlightGraphSelection();
  renderRelationshipList();
}

function highlightGraphSelection() {
  const selected = novelState.selectedRelationship;
  document.querySelectorAll(".graph-node").forEach((el) => {
    el.classList.toggle("selected", selected?.type === "node" && el.dataset.nodeId === selected.id);
  });
  document.querySelectorAll(".graph-link, .graph-edge-label").forEach((el) => {
    el.classList.toggle("selected", selected?.type === "link" && el.dataset.linkId === selected.id);
  });
}

function renderRelationshipList() {
  const target = $("relationshipList");
  const { links } = relationshipDataset();
  if (!links.length) {
    target.innerHTML = `<div class="empty compact">暂无关系记录。</div>`;
    return;
  }
  target.innerHTML = "";
  links.forEach((link) => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "relationship-row";
    btn.dataset.linkId = link.id;
    if (novelState.selectedRelationship?.type === "link" && novelState.selectedRelationship.id === link.id) {
      btn.classList.add("active");
    }
    btn.innerHTML = `
      <strong>${escapeHtml(link.source)} / ${escapeHtml(link.target)}</strong>
      <span>第${formatNumber(link.chapter)}章 · ${escapeHtml(link.relation)}</span>
    `;
    btn.addEventListener("click", () => selectRelationship(link));
    target.appendChild(btn);
  });
}

function renderChapters(chapters) {
  $("chapterSummary").textContent = `${formatNumber(chapters.filter((item) => item.chapter).length)} 章终稿 · ${formatNumber(chapters.filter((item) => item.review).length)} 章评审`;
  $("chapterGrid").innerHTML = chapters.length
    ? chapters.map((chapter) => `
      <button type="button" class="novel-chapter-card ${chapter.chapter ? "done" : ""} ${chapter.number === novelState.currentChapter ? "active" : ""}" data-chapter="${chapter.number}">
        <strong>第${String(chapter.number).padStart(2, "0")}章</strong>
        <span>${escapeHtml(chapter.status)} · ${formatNumber(chapter.word_count)}字</span>
        <small>${chapter.draft ? "草稿" : "-"} · ${chapter.review ? "评审" : "-"} · ${chapter.ai_review ? "AI审" : "-"}</small>
      </button>
    `).join("")
    : `<div class="empty">暂无章节文件。</div>`;
  $("chapterGrid").querySelectorAll("[data-chapter]").forEach((btn) => {
    btn.addEventListener("click", () => loadChapter(Number(btn.dataset.chapter)));
  });
}

async function loadChapter(number, options = {}) {
  const novel = novelState.currentNovel;
  if (!novel || !number) return;
  const detail = await api(`/api/novels/${encodeURIComponent(novel.id)}/chapters/${number}`);
  novelState.currentChapter = number;
  novelState.currentChapterDetail = detail;
  renderChapters(novel.chapters || []);
  renderChapterDetail(detail);
  const preferred = (detail.artifacts || []).find((item) => item.artifact_role === "章节终稿")
    || (detail.artifacts || [])[0];
  if (!options.preserveFile && preferred) {
    await loadFile(preferred.path);
  }
}

function renderChapterDetail(detail) {
  $("chapterDetailSection").hidden = false;
  $("chapterDetailTitle").textContent = `第${String(detail.number).padStart(2, "0")}章 · ${detail.title || ""}`;
  $("chapterDetailMeta").textContent = `${detail.review_status || "未审阅"} · AI ${formatNumber(detail.ai?.percent, 2)}% ${detail.ai?.risk_label || ""}`;
  const reviewIssues = detail.review?.issues || [];
  $("chapterDetailBody").innerHTML = `
    <div class="chapter-brief-grid">
      ${briefBlock("目标", detail.goal)}
      ${briefBlock("冲突", detail.conflict)}
      ${briefBlock("钩子", detail.hook)}
      ${briefBlock("摘要", detail.summary)}
      ${briefBlock("评审结论", detail.review_summary)}
    </div>
    ${listSection("关键事件", detail.timeline_events, (item) => `第${formatNumber(item.chapter)}章 · ${item.time || ""}｜${item.event || ""}`)}
    ${listSection("状态变化", detail.state_changes, (item) => `${item.entity || ""} · ${item.field || ""}：${item.old_value || ""} -> ${item.new_value || ""}`)}
    ${listSection("关系推进", detail.relationship_updates, (item) => `${item.character_a || ""} / ${item.character_b || ""}：${item.relation || ""}`)}
    ${listSection("资源入账", detail.resource_changes, (item) => `${item.name || item.id || ""}：${item.status || ""} · ${item.risk || item.evidence || ""}`)}
    ${listSection("评审问题", reviewIssues, (item) => `${item.severity || ""} · ${item.description || item.evidence || ""}`)}
  `;
  renderChapterArtifacts(detail.artifacts || []);
}

function briefBlock(label, value) {
  if (!value) return "";
  return `<div class="brief-block"><strong>${escapeHtml(label)}</strong><span>${escapeHtml(value)}</span></div>`;
}

function listSection(title, items, formatter) {
  if (!items || !items.length) return "";
  return `
    <div class="chapter-list-section">
      <strong>${escapeHtml(title)}</strong>
      <ul>${items.slice(0, 10).map((item) => `<li>${escapeHtml(formatter(item))}</li>`).join("")}</ul>
    </div>
  `;
}

function renderChapterArtifacts(artifacts) {
  $("chapterArtifactSummary").textContent = `${formatNumber(artifacts.length)} 个文件`;
  const target = $("chapterArtifactList");
  if (!artifacts.length) {
    target.innerHTML = `<div class="empty compact">本章暂无可识别的生产产物。</div>`;
    return;
  }
  target.innerHTML = "";
  artifacts.forEach((item) => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = `artifact-row ${item.path === novelState.currentFile ? "active" : ""}`;
    btn.innerHTML = `
      <span><strong>${escapeHtml(item.artifact_role)}</strong><small>${escapeHtml(item.path)}</small></span>
      <em>${formatNumber(item.content_non_space_chars)} 字符</em>
      <em>${formatBytes(item.size)}</em>
      <em>${escapeHtml(compactTime(item.updated_at))}</em>
    `;
    btn.addEventListener("click", () => loadFile(item.path));
    target.appendChild(btn);
  });
}

function renderMaterialControls() {
  const select = $("groupFilter");
  const groups = new Map();
  novelState.materials.forEach((item) => groups.set(item.group, item.group_label));
  const current = select.value || "all";
  select.innerHTML = `<option value="all">全部资料</option>`;
  [...groups.entries()].forEach(([id, label]) => {
    const option = document.createElement("option");
    option.value = id;
    option.textContent = label;
    if (id === current) option.selected = true;
    select.appendChild(option);
  });
}

function renderMaterials() {
  const query = ($("materialFilter").value || "").trim().toLowerCase();
  const group = $("groupFilter").value || "all";
  const visible = novelState.materials.filter((item) => {
    const groupOk = group === "all" || item.group === group;
    const queryOk = !query || item.path.toLowerCase().includes(query) || item.group_label.toLowerCase().includes(query);
    return groupOk && queryOk;
  });
  $("materialSummary").textContent = `${formatNumber(visible.length)}/${formatNumber(novelState.materials.length)} 个文件`;
  $("materialTable").innerHTML = [
    `<div class="material-row head"><span>文件</span><span>类型</span><span>内容</span><span>更新时间</span><span>大小</span></div>`,
    ...visible.map((item) => `
      <button type="button" class="material-row ${item.path === novelState.currentFile ? "active" : ""}" data-path="${escapeHtml(item.path)}">
        <span>${escapeHtml(item.path)}</span>
        <span><em class="type-chip">${escapeHtml(item.group_label)}</em></span>
        <span>${formatNumber(item.content_non_space_chars)} 字符 · ${formatNumber(item.content_lines)} 行</span>
        <span>${escapeHtml(compactTime(item.updated_at))}</span>
        <span>${formatBytes(item.size)}</span>
      </button>
    `),
  ].join("");
  $("materialTable").querySelectorAll("[data-path]").forEach((row) => {
    row.addEventListener("click", () => loadFile(row.dataset.path));
  });
}

async function loadFile(path, options = {}) {
  const novel = novelState.currentNovel;
  if (!novel || !path) return;
  try {
    const data = await api(`/api/novels/${encodeURIComponent(novel.id)}/file?path=${encodeURIComponent(path)}`);
    novelState.currentFile = path;
    $("viewerTitle").textContent = path;
    $("viewerMeta").textContent = `${formatBytes(data.size)} · ${compactTime(data.updated_at)}${data.truncated ? " · 已截断预览" : ""}`;
    $("fileViewer").textContent = data.binary
      ? "这是二进制文件，浏览器看板只展示路径和元数据。"
      : data.content || "文件为空。";
    renderMaterials();
    if (novelState.currentChapterDetail) {
      renderChapterArtifacts(novelState.currentChapterDetail.artifacts || []);
    }
  } catch (error) {
    if (!options.silentMissing) throw error;
  }
}

function phaseLabel(value) {
  return {
    init: "初始化",
    premise: "前提",
    outline: "大纲",
    writing: "写作中",
    complete: "已完成",
  }[value] || value || "-";
}

function flowLabel(value) {
  return {
    writing: "写作",
    reviewing: "评审",
    rewriting: "重写",
    polishing: "打磨",
    steering: "干预",
  }[value] || value || "-";
}

function statusLabel(value) {
  return {
    complete: "已完成",
    current: "当前推进",
    planned: "规划中",
  }[value] || value || "-";
}

function formatNumber(value, digits = 0) {
  if (value === null || value === undefined || value === "") return "-";
  const number = Number(value);
  if (!Number.isFinite(number)) return String(value);
  return number.toLocaleString("zh-CN", { maximumFractionDigits: digits });
}

function formatBytes(value) {
  const size = Number(value) || 0;
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

function compactTime(value) {
  if (!value) return "-";
  return String(value).replace("T", " ").slice(0, 16);
}

function compactText(value, limit) {
  const text = String(value || "").replace(/\s+/g, " ").trim();
  return text.length > limit ? `${text.slice(0, limit)}...` : text;
}

function boundedPct(value) {
  const number = Number(value) || 0;
  return Math.max(0, Math.min(100, number));
}

function escapeHtml(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function escapeSvg(value) {
  return escapeHtml(value).replaceAll("'", "&apos;");
}

function bindEvents() {
  $("refreshBtn").addEventListener("click", async () => {
    await refreshLiveData();
    toast("已刷新");
  });
  $("materialFilter").addEventListener("input", renderMaterials);
  $("groupFilter").addEventListener("change", renderMaterials);
  $("relationshipFilter").addEventListener("change", (event) => {
    novelState.relationshipFocus = event.target.value;
    novelState.selectedRelationship = null;
    renderRelationshipGraph({ animate: true });
    renderRelationshipList();
  });
  $("graphResetBtn").addEventListener("click", () => {
    renderRelationshipGraph({ animate: true });
    toast("关系图已重排");
  });
}

function startPolling() {
  if (novelState.pollTimer) clearInterval(novelState.pollTimer);
  novelState.pollTimer = setInterval(() => {
    refreshLiveData().catch((error) => console.warn(error));
  }, 3500);
}

async function boot() {
  bindEvents();
  await loadNovels();
  startPolling();
}

boot().catch((error) => {
  console.error(error);
  showEmpty(error.message);
  toast(error.message);
});
