const state = {
  projects: [],
  stages: [],
  artifacts: [],
  artifactMeta: {},
  documentTypes: {},
  currentProject: null,
  currentView: "summary",
  currentArtifact: null,
  chapters: [],
  currentChapter: 1,
  metrics: null,
  pollTimer: null,
  polling: false,
  editorDirty: false,
  chapterDirty: false,
};

const $ = (id) => document.getElementById(id);
const STATUS_LABELS = {
  done: "完成",
  in_progress: "进行中",
  pending: "待处理",
  blocked: "阻塞",
};
const AGENT_STATUS_LABELS = {
  ready: "就绪",
  running: "运行中",
  pending: "排队",
  blocked: "阻塞",
  done: "完成",
};
const PROJECT_STATUS_GROUPS = [
  { id: "running", label: "进行中" },
  { id: "not-started", label: "未开始" },
  { id: "blocked", label: "阻塞" },
  { id: "done", label: "已完成" },
];

function toast(message) {
  const el = $("toast");
  el.textContent = message;
  el.hidden = false;
  setTimeout(() => {
    el.hidden = true;
  }, 2200);
}

async function api(path, options = {}) {
  const init = {
    headers: { "Content-Type": "application/json" },
    ...options,
  };
  if (init.body && typeof init.body !== "string") {
    init.body = JSON.stringify(init.body);
  }
  const res = await fetch(path, init);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(data.error || `HTTP ${res.status}`);
  }
  return data;
}

async function loadMeta() {
  const data = await api("/api/stages");
  state.stages = data.stages;
  state.artifacts = data.artifacts;
  state.artifactMeta = data.artifact_meta || {};
  state.documentTypes = data.document_types || {};
  renderArtifactOptions();
}

async function loadProjects() {
  const data = await api("/api/projects");
  state.projects = data.projects;
  $("projectCount").textContent = String(data.projects.length);
  if (state.currentView === "project" && state.currentProject) {
    const stillExists = state.projects.some((project) => project.id === state.currentProject.id);
    if (!stillExists) {
      state.currentProject = null;
      state.currentView = "summary";
    }
  }
  renderProjects();
  if (state.currentView === "summary") {
    renderSummary();
  }
}

async function loadProject(id) {
  const previousProjectId = state.currentProject?.id;
  state.currentView = "project";
  state.currentProject = await api(`/api/projects/${encodeURIComponent(id)}`);
  if (previousProjectId !== state.currentProject.id) {
    state.currentArtifact = stageArtifact(state.currentProject.current_stage) || "输入设定.md";
  }
  state.metrics = null;
  $("emptyState").hidden = true;
  $("summaryDetail").hidden = true;
  $("projectDetail").hidden = false;
  renderProjects();
  renderDetail();
  await loadMetrics();
  await loadPrompt(state.currentProject.current_stage || "bible");
  await loadArtifact(state.currentArtifact || stageArtifact(state.currentProject.current_stage) || "输入设定.md");
  await loadChapters();
}

async function loadMetrics() {
  const project = state.currentProject;
  if (!project) return;
  state.metrics = await api(`/api/projects/${encodeURIComponent(project.id)}/metrics`);
  renderMetrics();
}

async function loadChapters() {
  const project = state.currentProject;
  if (!project) return;
  const data = await api(`/api/projects/${encodeURIComponent(project.id)}/chapters`);
  state.chapters = data.chapters || [];
  const active = state.chapters.find((chapter) => ["writing", "auditing", "revising"].includes(chapter.status))
    || state.chapters.find((chapter) => chapter.status !== "locked")
    || state.chapters[0];
  if (!state.currentChapter || !state.chapters.some((chapter) => chapter.chapter_number === state.currentChapter)) {
    state.currentChapter = active?.chapter_number || 1;
  }
  renderChapters(data);
  if (state.currentChapter) {
    await loadChapter(state.currentChapter);
  }
}

async function refreshLiveData() {
  if (state.polling) return;
  state.polling = true;
  try {
    await loadProjects();
    if (state.currentView === "summary") {
      renderSummary();
      return;
    }
    if (!state.currentProject) return;
    const id = state.currentProject.id;
    state.currentProject = await api(`/api/projects/${encodeURIComponent(id)}`);
    renderProjects();
    renderDetail({ preservePromptSelection: true });
    await loadMetrics();
    await refreshChapterListOnly();
  } finally {
    state.polling = false;
  }
}

async function refreshChapterListOnly() {
  const project = state.currentProject;
  if (!project) return;
  const data = await api(`/api/projects/${encodeURIComponent(project.id)}/chapters`);
  state.chapters = data.chapters || [];
  renderChapters(data);
}

function renderProjects() {
  const list = $("projectList");
  list.innerHTML = "";
  const groupedProjects = groupProjectsByStatus(state.projects);
  const summaryBtn = document.createElement("button");
  summaryBtn.type = "button";
  summaryBtn.className = "project-item summary-item";
  if (state.currentView === "summary") {
    summaryBtn.classList.add("active");
  }
  const runningCount = groupedProjects.running.length;
  const pendingCount = groupedProjects["not-started"].length;
  const doneCount = groupedProjects.done.length;
  summaryBtn.innerHTML = `
    <span class="project-title">数据汇总</span>
    <span class="project-meta">${formatNumber(runningCount)} 进行中 · ${formatNumber(pendingCount)} 未开始 · ${formatNumber(doneCount)} 已完成</span>
  `;
  summaryBtn.addEventListener("click", showSummary);
  list.appendChild(summaryBtn);

  PROJECT_STATUS_GROUPS.forEach((group) => {
    const projects = groupedProjects[group.id] || [];
    if (!projects.length) return;
    const heading = document.createElement("div");
    heading.className = `project-group-title status-${group.id}`;
    heading.innerHTML = `
      <span>${escapeHtml(group.label)}</span>
      <strong>${formatNumber(projects.length)}</strong>
    `;
    list.appendChild(heading);

    projects.forEach((project) => {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = `project-item project-status-${group.id}`;
      if (state.currentView === "project" && state.currentProject && state.currentProject.id === project.id) {
        btn.classList.add("active");
      }
      const agentLabel = [project.agent_id || "agent", AGENT_STATUS_LABELS[project.agent_status] || project.agent_status].filter(Boolean).join(" · ");
      btn.innerHTML = `
        <span class="project-title-line">
          <span class="project-title">${escapeHtml(project.title)}</span>
          <span class="project-status-badge status-${group.id}">${escapeHtml(group.label)}</span>
        </span>
        <span class="project-meta">${escapeHtml(project.direction_label)} · ${escapeHtml(agentLabel)} · ${escapeHtml(project.current_stage_label || project.current_stage)}</span>
        <span class="project-meta">${escapeHtml(projectTimeLabel(project))}</span>
      `;
      btn.addEventListener("click", () => loadProject(project.id));
      list.appendChild(btn);
    });
  });
}

function projectListStatus(project) {
  if (project.current_stage === "done") return "done";
  if (project.current_stage_status === "blocked" || project.agent_status === "blocked") return "blocked";
  if (project.agent_status === "pending" || project.current_stage_status === "pending") return "not-started";
  return "running";
}

function groupProjectsByStatus(projects) {
  const grouped = Object.fromEntries(PROJECT_STATUS_GROUPS.map((group) => [group.id, []]));
  projects.forEach((project) => {
    grouped[projectListStatus(project)].push(project);
  });
  return grouped;
}

function projectEndLabel(project) {
  return project.ended_at ? compactTime(project.ended_at) : "进行中";
}

function projectTimeLabel(project) {
  const start = compactTime(project.started_at || project.created_at);
  return `开始 ${start} · 结束 ${projectEndLabel(project)}`;
}

function stageProgressPct(status) {
  if (status === "done") return 100;
  if (status === "in_progress") return 60;
  if (status === "blocked") return 60;
  return 0;
}

function showSummary() {
  state.currentView = "summary";
  state.currentProject = null;
  state.currentArtifact = null;
  state.chapters = [];
  state.metrics = null;
  $("emptyState").hidden = true;
  $("projectDetail").hidden = true;
  $("summaryDetail").hidden = false;
  renderProjects();
  renderSummary();
}

function projectProgressPct(project) {
  const done = Number(project.done_stages || 0);
  const total = Number(project.total_stages || state.stages.length || 0);
  return total > 0 ? (done / total) * 100 : 0;
}

function projectStateLabel(project) {
  if (project.current_stage === "done") return "已完成";
  if (project.current_stage_status === "blocked" || project.agent_status === "blocked") return "阻塞";
  if (project.agent_status === "pending") return "排队";
  if (project.agent_status === "running" || project.current_stage_status === "in_progress") return "进行中";
  return STATUS_LABELS[project.current_stage_status] || project.current_stage_status || "待处理";
}

function renderSummary() {
  const projects = state.projects || [];
  const active = projects.filter((project) => project.current_stage !== "done");
  const running = projects.filter((project) => project.agent_status === "running");
  const pending = projects.filter((project) => project.agent_status === "pending");
  const blocked = projects.filter((project) => project.current_stage_status === "blocked" || project.agent_status === "blocked");
  const done = projects.filter((project) => project.current_stage === "done");
  $("emptyState").hidden = true;
  $("summaryDetail").hidden = false;
  $("projectDetail").hidden = true;
  $("summaryMeta").textContent = `${formatNumber(projects.length)} 个项目 · ${formatNumber(active.length)} 篇未完成`;
  $("summaryUpdated").textContent = `刷新于 ${compactTime(new Date().toISOString())}`;

  $("summaryMetrics").innerHTML = [
    {
      label: "项目总数",
      value: formatNumber(projects.length),
      note: `${formatNumber(active.length)} 篇未完成`,
      state: projects.length > 0 ? "ok" : "",
    },
    {
      label: "进行中",
      value: formatNumber(running.length),
      note: `${formatNumber(pending.length)} 篇排队`,
      state: running.length > 0 ? "ok" : "",
    },
    {
      label: "阻塞",
      value: formatNumber(blocked.length),
      note: "需要人工处理",
      state: blocked.length > 0 ? "warn" : "",
    },
    {
      label: "已完成",
      value: formatNumber(done.length),
      note: `${formatNumber(projects.length ? (done.length / projects.length) * 100 : 0, 1)}% 完成率`,
      state: done.length === projects.length && projects.length > 0 ? "ok" : "",
    },
  ].map((card) => `
    <div class="metric-card ${card.state}">
      <span class="metric-label">${escapeHtml(card.label)}</span>
      <strong class="metric-value">${escapeHtml(card.value)}</strong>
      <span class="metric-note">${escapeHtml(card.note)}</span>
    </div>
  `).join("");

  const visibleProjects = active.length ? active : projects;
  $("summaryList").innerHTML = visibleProjects.length
    ? visibleProjects.map((project) => {
      const pct = projectProgressPct(project);
      const status = projectStateLabel(project);
      const agent = [
        project.agent_id || "agent",
        AGENT_STATUS_LABELS[project.agent_status] || project.agent_status,
      ].filter(Boolean).join(" · ");
      return `
        <button class="summary-row" type="button" data-project-id="${escapeHtml(project.id)}">
          <span>
            <strong>${escapeHtml(project.title)}</strong>
            <small>${escapeHtml(project.direction_label)} · ${escapeHtml(agent)}</small>
          </span>
          <span>
            <strong>${escapeHtml(status)}</strong>
            <small>${escapeHtml(project.current_stage_label || project.current_stage)} · ${formatNumber(pct, 1)}%</small>
          </span>
          <span>
            <strong>开始 ${escapeHtml(compactTime(project.started_at || project.created_at))}</strong>
            <small>结束 ${escapeHtml(projectEndLabel(project))}</small>
          </span>
        </button>
      `;
    }).join("")
    : `<div class="empty">暂无项目数据。</div>`;

  $("summaryList").querySelectorAll("[data-project-id]").forEach((row) => {
    row.addEventListener("click", () => loadProject(row.dataset.projectId));
  });
}

function stageArtifact(stageId) {
  return state.stages.find((stage) => stage.id === stageId)?.artifact;
}

function renderDetail(options = {}) {
  const project = state.currentProject;
  $("detailTitle").textContent = project.title;
  $("detailMeta").textContent = `${project.direction_label} · ${projectTimeLabel(project)} · ${project.book_dir}`;

  const promptStage = $("promptStage");
  const selectedPromptStage = options.preservePromptSelection
    ? promptStage.value || project.current_stage || "bible"
    : project.current_stage || "bible";
  promptStage.innerHTML = "";
  state.stages.forEach((stage) => {
    const status = project.stages[stage.id]?.status || "pending";
    const option = document.createElement("option");
    option.value = stage.id;
    option.textContent = `${stage.label} (${STATUS_LABELS[status] || status})`;
    if (stage.id === selectedPromptStage) option.selected = true;
    promptStage.appendChild(option);
  });

  const rail = $("stageRail");
  rail.innerHTML = "";
  state.stages.forEach((stage) => {
    const status = project.stages[stage.id]?.status || "pending";
    const pct = stageProgressPct(status);
    const card = document.createElement("div");
    card.className = `stage-card ${status}`;
    card.innerHTML = `
      <div class="stage-card-head">
        <strong>${escapeHtml(stage.label)}</strong>
        <span>${escapeHtml(STATUS_LABELS[status] || status)}</span>
      </div>
      <div class="mini-progress">
        <div style="width: ${pct}%"></div>
      </div>
    `;
    rail.appendChild(card);
  });

  const events = $("eventLog");
  events.innerHTML = "";
  [...(project.events || [])].reverse().forEach((event) => {
    const row = document.createElement("div");
    row.textContent = `${event.time}  ${event.text}`;
    events.appendChild(row);
  });
}

function renderMetrics() {
  const grid = $("metricsGrid");
  const stats = $("artifactStats");
  const typeProgress = $("typeProgress");
  const progress = $("stageProgress");
  const progressText = $("stageProgressText");
  if (!grid || !stats || !progress || !progressText) return;

  const metrics = buildLiveMetrics();
  if (!metrics) {
    grid.innerHTML = "";
    stats.innerHTML = "";
    if (typeProgress) typeProgress.innerHTML = "";
    progress.style.width = "0%";
    progressText.textContent = "0%";
    $("metricsUpdated").textContent = "等待刷新";
    return;
  }

  const quality = metrics.quality_gate || {};
  const body = metrics.body_stats || {};
  const total = metrics.total_content || {};
  const artifactProgress = metrics.artifact_progress || { ready: 0, total: state.stages.length || 0, pct: 0 };
  const bodyChars = body.content_non_space_chars !== undefined
    ? body.content_non_space_chars
    : body.non_space_chars || 0;
  const bodyInTargetRange = bodyChars >= Number(metrics.target_min || 0)
    && bodyChars <= Number(metrics.target_max || Infinity);
  const current = metrics.current_stage || {};
  const qualityMissing = Array.isArray(quality.missing) ? quality.missing : [];
  const qualityNote = quality.passed
    ? `${formatNumber(quality.passed_chapters)}/${formatNumber(quality.total_chapters)} 章 · 终版复审通过`
    : qualityMissing[0] || "等待章节审核";
  const currentStatus = STATUS_LABELS[current.status] || current.status || "-";

  const cards = [
    {
      label: "流程完成",
      value: `${formatNumber(metrics.progress_pct, 1)}%`,
      note: `${metrics.stage_counts.done}/${metrics.stage_total} 手动完成`,
      state: metrics.progress_pct >= 100 ? "ok" : "",
    },
    {
      label: "文档进度",
      value: `${formatNumber(artifactProgress.pct, 1)}%`,
      note: `${formatNumber(artifactProgress.complete)}/${formatNumber(artifactProgress.total)} 达标 · ${formatNumber(artifactProgress.ready)} 有内容`,
      state: artifactProgress.complete >= artifactProgress.total && artifactProgress.total > 0 ? "ok" : "",
    },
    {
      label: "项目字符",
      value: formatNumber(total.content_non_space_chars || 0),
      note: `${formatNumber(total.content_lines || 0)} 行 · ${formatNumber(total.content_cjk_chars || 0)} 中文`,
      state: total.content_non_space_chars > 0 ? "ok" : "",
    },
    {
      label: "正文字符",
      value: formatNumber(bodyChars),
      note: `${metrics.body_source} · ${formatNumber(metrics.target_pct, 1)}% / ${formatNumber(metrics.target_min)}-${formatNumber(metrics.target_max)}`,
      state: bodyInTargetRange ? "ok" : bodyChars > Number(metrics.target_max || Infinity) ? "warn" : "",
    },
    {
      label: "质量门",
      value: quality.passed ? "通过" : "未通过",
      note: qualityNote,
      state: quality.passed ? "ok" : "warn",
    },
    {
      label: "当前阶段",
      value: current.label || "-",
      note: currentStatus,
      state: current.status === "blocked" ? "warn" : current.status === "done" ? "ok" : "",
    },
  ];

  grid.innerHTML = cards.map((card) => `
    <div class="metric-card ${card.state}">
      <span class="metric-label">${escapeHtml(card.label)}</span>
      <span class="metric-value">${escapeHtml(card.value)}</span>
      <span class="metric-note">${escapeHtml(card.note)}</span>
    </div>
  `).join("");
  progress.style.width = `${Math.max(0, Math.min(100, Number(metrics.progress_pct) || 0))}%`;
  progressText.textContent = `${formatNumber(metrics.progress_pct, 1)}%`;
  $("metricsUpdated").textContent = `刷新 ${compactTime(metrics.updated_at)}`;

  if (typeProgress) {
    typeProgress.innerHTML = (metrics.type_progress || []).map((item) => `
      <div class="type-progress-item">
        <div>
          <strong>${escapeHtml(item.type_label)}</strong>
          <span>${formatNumber(item.complete)}/${formatNumber(item.documents)} 达标 · ${formatNumber(item.content_non_space_chars)} 字符</span>
        </div>
        <div class="mini-progress">
          <span style="width:${boundedPct(item.progress_pct)}%"></span>
        </div>
        <b>${formatNumber(item.progress_pct, 1)}%</b>
      </div>
    `).join("");
  }

  stats.innerHTML = [
    `<div class="artifact-row"><span>文档</span><span>类型</span><span>进度</span><span>内容/目标</span><span>状态</span><span>输出位置</span></div>`,
    ...metrics.artifact_stats.map((item) => `
      <div class="artifact-row">
        <span>${escapeHtml(item.name)}</span>
        <span><em class="type-chip">${escapeHtml(item.type_label || "-")}</em></span>
        <span class="doc-progress-cell">
          <span>${formatNumber(item.progress_pct, 1)}%</span>
          <i><b style="width:${boundedPct(item.progress_pct)}%"></b></i>
        </span>
        <span>${formatNumber(item.content_non_space_chars)} / ${formatNumber(item.target_chars)}</span>
        <span class="${item.progress_status === "complete" ? "ready-text" : item.progress_status === "generating" ? "writing-text" : "empty-text"}">${escapeHtml(progressStatusLabel(item.progress_status))}</span>
        <span>${escapeHtml(item.output_path || compactTime(item.updated_at))}</span>
      </div>
    `),
  ].join("");
}

function renderChapters(data = {}) {
  const list = $("chapterList");
  const select = $("chapterSelect");
  if (!list || !select) return;
  if (data.agent || data.chapter_dir) {
    const agentLabel = data.agent?.id
      ? [data.agent.id, data.agent.status, data.agent.lane ? `lane ${data.agent.lane}` : ""].filter(Boolean).join(" · ")
      : "";
    $("chapterAgent").textContent = data.agent?.id
      ? `${agentLabel} · ${data.chapter_dir || ""}`
      : data.chapter_dir || "";
  }
  list.innerHTML = "";
  select.innerHTML = "";
  state.chapters.forEach((chapter) => {
    const number = chapter.chapter_number;
    const row = document.createElement("button");
    row.type = "button";
    row.className = `chapter-item ${chapter.status}`;
    if (number === state.currentChapter) row.classList.add("active");
    row.innerHTML = `
      <strong>第${String(number).padStart(2, "0")}章</strong>
      <span>${escapeHtml(chapter.status_label || chapter.status)} · ${formatNumber(chapter.progress_pct, 1)}%</span>
    `;
    row.addEventListener("click", () => loadChapter(number));
    list.appendChild(row);

    const option = document.createElement("option");
    option.value = String(number);
    option.textContent = `第${String(number).padStart(2, "0")}章 · ${chapter.status_label || chapter.status}`;
    if (number === state.currentChapter) option.selected = true;
    select.appendChild(option);
  });
}

async function loadChapter(number) {
  const project = state.currentProject;
  if (!project) return;
  state.currentChapter = Number(number);
  const encodedId = encodeURIComponent(project.id);
  const [content, audit] = await Promise.all([
    api(`/api/projects/${encodedId}/chapters/${state.currentChapter}/content`),
    api(`/api/projects/${encodedId}/chapters/${state.currentChapter}/audit`),
  ]);
  $("chapterSelect").value = String(state.currentChapter);
  $("chapterEditor").value = content.content || "";
  $("chapterAuditEditor").value = audit.content || "";
  state.chapterDirty = false;
  renderChapters();
  await loadChapterPrompt();
}

async function loadChapterPrompt() {
  const project = state.currentProject;
  if (!project || !state.currentChapter) return;
  const mode = $("chapterPromptMode").value || "write";
  const data = await api(`/api/projects/${encodeURIComponent(project.id)}/chapters/${state.currentChapter}/prompt?mode=${encodeURIComponent(mode)}`);
  $("chapterPromptBox").value = data.prompt;
}

async function saveChapterContent() {
  const project = state.currentProject;
  if (!project || !state.currentChapter) return;
  await api(`/api/projects/${encodeURIComponent(project.id)}/chapters/${state.currentChapter}/content`, {
    method: "PUT",
    body: { content: $("chapterEditor").value },
  });
  state.chapterDirty = false;
  await loadProjects();
  await loadMetrics();
  await loadChapters();
  toast(`第${String(state.currentChapter).padStart(2, "0")}章正文已保存`);
}

async function saveChapterAudit() {
  const project = state.currentProject;
  if (!project || !state.currentChapter) return;
  await api(`/api/projects/${encodeURIComponent(project.id)}/chapters/${state.currentChapter}/audit`, {
    method: "PUT",
    body: { content: $("chapterAuditEditor").value },
  });
  await loadProjects();
  await loadMetrics();
  await loadChapters();
  toast(`第${String(state.currentChapter).padStart(2, "0")}章审核已保存`);
}

async function markChapterPassed() {
  const project = state.currentProject;
  if (!project || !state.currentChapter) return;
  await api(`/api/projects/${encodeURIComponent(project.id)}/chapters/${state.currentChapter}/status`, {
    method: "POST",
    body: { status: "passed" },
  });
  await loadProjects();
  await loadMetrics();
  await loadChapters();
  const next = state.chapters.find((chapter) => chapter.status === "writing" || chapter.status === "revising");
  if (next) await loadChapter(next.chapter_number);
  toast("本章已达标，下一章已解锁");
}

function buildLiveMetrics() {
  if (!state.metrics) return null;
  const metrics = JSON.parse(JSON.stringify(state.metrics));
  if (state.editorDirty && state.currentArtifact && $("artifactEditor")) {
    const editorStats = contentStatsFromText($("artifactEditor").value || "");
    const item = metrics.artifact_stats.find((artifact) => artifact.name === state.currentArtifact);
    if (item) {
      Object.assign(item, editorStats, {
        name: state.currentArtifact,
        ready: editorStats.content_non_space_chars > 0,
        updated_at: "正在编辑",
      });
    }
  }
  if (state.chapterDirty && state.currentChapter && $("chapterEditor")) {
    const chapterStats = chapterContentStatsFromText($("chapterEditor").value || "");
    const item = metrics.artifact_stats.find((artifact) => artifact.type === "chapter" && artifact.chapter_number === state.currentChapter);
    if (item) {
      Object.assign(item, chapterStats, {
        ready: chapterStats.content_non_space_chars > 0,
        updated_at: "正在编辑",
      });
    }
  }
  return recalculateDerivedMetrics(metrics);
}

function recalculateDerivedMetrics(metrics) {
  const artifacts = metrics.artifact_stats || [];
  artifacts.forEach((item) => {
    const chars = Number(item.content_non_space_chars || 0);
    const target = Number(item.target_chars || 0);
    item.ready = chars > 0;
    item.progress_pct = target > 0 ? Math.min(100, Math.round((chars / target) * 1000) / 10) : 0;
    if (chars === 0) {
      item.progress_status = "empty";
    } else if (item.progress_pct >= 100) {
      item.progress_status = "complete";
    } else {
      item.progress_status = "generating";
    }
  });
  const byName = Object.fromEntries(artifacts.map((item) => [item.name, item]));
  const finalBody = byName["正文.md"] || {};
  const draftBody = byName["正文草稿.md"] || {};
  const chapterDocs = artifacts.filter((item) => item.type === "chapter");
  const chapterBody = {
    content_non_space_chars: chapterDocs.reduce((sum, item) => sum + Number(item.content_non_space_chars || 0), 0),
    content_cjk_chars: chapterDocs.reduce((sum, item) => sum + Number(item.content_cjk_chars || 0), 0),
    content_lines: chapterDocs.reduce((sum, item) => sum + Number(item.content_lines || 0), 0),
  };
  if (Number(finalBody.content_non_space_chars || 0) > 0) {
    metrics.body_source = "正文.md";
    metrics.body_stats = finalBody;
  } else if (chapterBody.content_non_space_chars > 0) {
    metrics.body_source = "分章合计";
    metrics.body_stats = chapterBody;
  } else {
    metrics.body_source = "正文草稿.md";
    metrics.body_stats = draftBody;
  }

  const bodyChars = Number(metrics.body_stats?.content_non_space_chars || 0);
  const targetMin = Number(metrics.target_min || 0);
  metrics.target_pct = targetMin > 0 ? Math.min(100, Math.round((bodyChars / targetMin) * 1000) / 10) : 0;

  metrics.stage_artifacts = (metrics.stage_artifacts || []).map((item) => {
    const stats = byName[item.artifact] || {};
    return {
      ...item,
      type: stats.type || item.type,
      type_label: stats.type_label || item.type_label,
      ready: Number(stats.content_non_space_chars || 0) > 0,
      target_chars: Number(stats.target_chars || item.target_chars || 0),
      progress_pct: Number(stats.progress_pct || 0),
      progress_status: stats.progress_status || "empty",
      content_non_space_chars: Number(stats.content_non_space_chars || 0),
      content_cjk_chars: Number(stats.content_cjk_chars || 0),
      content_lines: Number(stats.content_lines || 0),
    };
  });
  const ready = metrics.stage_artifacts.filter((item) => item.ready).length;
  const complete = metrics.stage_artifacts.filter((item) => item.progress_status === "complete").length;
  const total = metrics.stage_artifacts.length || state.stages.length || 0;
  metrics.artifact_progress = {
    ready,
    complete,
    total,
    pct: total > 0 ? Math.round((metrics.stage_artifacts.reduce((sum, item) => sum + Number(item.progress_pct || 0), 0) / total) * 10) / 10 : 0,
  };
  metrics.total_content = {
    content_non_space_chars: artifacts.reduce((sum, item) => sum + Number(item.content_non_space_chars || 0), 0),
    content_cjk_chars: artifacts.reduce((sum, item) => sum + Number(item.content_cjk_chars || 0), 0),
    content_lines: artifacts.reduce((sum, item) => sum + Number(item.content_lines || 0), 0),
  };
  metrics.type_progress = buildDocumentTypeProgress(artifacts);
  return metrics;
}

function buildDocumentTypeProgress(artifacts) {
  const groups = {};
  artifacts.forEach((item) => {
    const type = item.type || "planning";
    const group = groups[type] || {
      type,
      type_label: item.type_label || type,
      type_order: Number(item.type_order || 99),
      documents: 0,
      ready: 0,
      complete: 0,
      content_non_space_chars: 0,
      content_cjk_chars: 0,
      progress_sum: 0,
    };
    group.documents += 1;
    group.ready += item.ready ? 1 : 0;
    group.complete += item.progress_status === "complete" ? 1 : 0;
    group.content_non_space_chars += Number(item.content_non_space_chars || 0);
    group.content_cjk_chars += Number(item.content_cjk_chars || 0);
    group.progress_sum += Number(item.progress_pct || 0);
    groups[type] = group;
  });
  return Object.values(groups)
    .map((group) => ({
      ...group,
      progress_pct: group.documents > 0 ? Math.round((group.progress_sum / group.documents) * 10) / 10 : 0,
    }))
    .sort((a, b) => a.type_order - b.type_order);
}

function progressStatusLabel(status) {
  return {
    empty: "未生成",
    generating: "生成中",
    complete: "达标",
  }[status] || status || "-";
}

function boundedPct(value) {
  const number = Number(value) || 0;
  return Math.max(0, Math.min(100, number));
}

function contentStatsFromText(text) {
  const lines = text.split(/\r\n|\r|\n/);
  const content = lines.length && lines[0].trimStart().startsWith("#")
    ? lines.slice(1).join("\n").trim()
    : text.trim();
  const contentLines = content ? content.split(/\r\n|\r|\n/).length : 0;
  return {
    chars: text.length,
    non_space_chars: text.replace(/\s+/g, "").length,
    content_chars: content.length,
    content_non_space_chars: content.replace(/\s+/g, "").length,
    cjk_chars: (text.match(/[\u4e00-\u9fff]/g) || []).length,
    content_cjk_chars: (content.match(/[\u4e00-\u9fff]/g) || []).length,
    lines: text ? lines.length : 0,
    content_lines: contentLines,
  };
}

function chapterContentStatsFromText(text) {
  const lines = text.split(/\r\n|\r|\n/);
  const filtered = lines.length && lines[0].trimStart().startsWith("#")
    ? [lines[0], ...lines.slice(1).filter((line) => !line.trim().startsWith(">"))].join("\n")
    : text;
  return contentStatsFromText(filtered);
}

function renderArtifactOptions() {
  const select = $("artifactSelect");
  select.innerHTML = "";
  state.artifacts.forEach((name) => {
    const option = document.createElement("option");
    option.value = name;
    option.textContent = name;
    select.appendChild(option);
  });
}

async function loadPrompt(stageId) {
  const project = state.currentProject;
  if (!project) return;
  const data = await api(`/api/projects/${encodeURIComponent(project.id)}/prompt?stage=${encodeURIComponent(stageId)}`);
  $("promptBox").value = data.prompt;
}

async function loadArtifact(name) {
  const project = state.currentProject;
  if (!project) return;
  state.currentArtifact = name;
  $("artifactSelect").value = name;
  const data = await api(`/api/projects/${encodeURIComponent(project.id)}/artifacts/${encodeURIComponent(name)}`);
  $("artifactEditor").value = data.content;
  state.editorDirty = false;
  renderEditorStats();
  renderMetrics();
}

async function saveArtifact() {
  const project = state.currentProject;
  const name = $("artifactSelect").value;
  if (!project || !name) return;
  await api(`/api/projects/${encodeURIComponent(project.id)}/artifacts/${encodeURIComponent(name)}`, {
    method: "PUT",
    body: { content: $("artifactEditor").value },
  });
  state.editorDirty = false;
  await refreshLiveData();
  renderEditorStats();
  toast(`${name} 已保存`);
}

function renderEditorStats() {
  const el = $("artifactLiveStats");
  if (!el) return;
  const stats = contentStatsFromText($("artifactEditor").value || "");
  el.textContent = `${formatNumber(stats.content_non_space_chars)} 内容字符 · ${formatNumber(stats.content_lines)} 行`;
}

function formatNumber(value, digits = 0) {
  if (value === null || value === undefined || value === "") return "-";
  const number = Number(value);
  if (!Number.isFinite(number)) return String(value);
  return number.toLocaleString("zh-CN", { maximumFractionDigits: digits });
}

function compactTime(value) {
  if (!value) return "-";
  return String(value).replace("T", " ").slice(0, 16);
}

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function bindEvents() {
  $("refreshBtn").addEventListener("click", async () => {
    await loadProjects();
    if (state.currentProject) {
      await loadProject(state.currentProject.id);
    }
    toast("已刷新");
  });

  $("promptStage").addEventListener("change", async (event) => {
    await loadPrompt(event.target.value);
    const artifact = stageArtifact(event.target.value);
    if (artifact) await loadArtifact(artifact);
  });
  $("chapterSelect").addEventListener("change", (event) => loadChapter(event.target.value));
  $("chapterPromptMode").addEventListener("change", loadChapterPrompt);
  $("chapterEditor").addEventListener("input", () => {
    state.chapterDirty = true;
    renderMetrics();
  });
  $("saveChapterBtn").addEventListener("click", saveChapterContent);
  $("saveChapterAuditBtn").addEventListener("click", saveChapterAudit);
  $("markChapterPassedBtn").addEventListener("click", markChapterPassed);
  $("copyChapterPromptBtn").addEventListener("click", async () => {
    await navigator.clipboard.writeText($("chapterPromptBox").value);
    toast("章节提示词已复制");
  });
  $("artifactSelect").addEventListener("change", (event) => loadArtifact(event.target.value));
  $("artifactEditor").addEventListener("input", () => {
    state.editorDirty = true;
    renderEditorStats();
    renderMetrics();
  });
  $("saveArtifactBtn").addEventListener("click", saveArtifact);
  $("copyPromptBtn").addEventListener("click", async () => {
    await navigator.clipboard.writeText($("promptBox").value);
    toast("提示词已复制");
  });
}

function startPolling() {
  if (state.pollTimer) clearInterval(state.pollTimer);
  state.pollTimer = setInterval(() => {
    refreshLiveData().catch((error) => console.warn(error));
  }, 3500);
}

async function boot() {
  bindEvents();
  await loadMeta();
  await loadProjects();
  startPolling();
}

boot().catch((error) => {
  console.error(error);
  toast(error.message);
});
