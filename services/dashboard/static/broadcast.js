/* Stream-safe read-only view. No prompts, raw logs or private memory are fetched. */
(function (root) {
  'use strict';
  const STAGES = {
    architect: '构建故事世界', 'outline-all': '冻结全书导航', 'zero-init': '初始化世界',
    preplan: '准备当前故事弧', 'rehearse-arc': '整弧条件预演', 'project-all': '角色独立细推',
    seal: '封存章节计划', promote: '准备正文合同', render: '正文创作与审核',
    finalize: '全书终审', deliver: '整理最终交付',
  };
  const STATES = {running: '运行中', idle: '待命', paused: '已暂停', error: '需要处理',
    stopped: '未运行', complete: '本次执行完成', completed: '本次执行完成', attention: '需要关注', unknown: '状态待确认', waiting: '等待中'};
  const OUTCOMES = {success: '行动成功', partial: '部分完成', blocked: '行动受阻',
    failure: '行动失败', failed: '行动失败', infeasible: '存在硬约束冲突'};
  const EVENTS = {proposal_submitted: '角色决定已提交', decision_submitted: '角色决定已提交',
    arbitration_submitted: '世界裁决已提交', arbitration_finalized: '世界裁决已落盘',
    cycle_committed: '世界周期已落盘', readiness_committed: '章节评估已提交',
    bundle_committed: '正式章节计划已保存', chapter_accepted: '正文已正式验收',
    chapter_completed: '章节完成记录已更新', context_bound: '本轮输入已绑定',
    generation_started: '开始当前规划', stage_started: '进入新执行阶段',
    stage_completed: '执行阶段已完成', activation: '角色收到激活事件',
    proposal: '角色决定已提交', arbitration: '世界裁决已提交', cycle: '世界周期已落盘',
    readiness: '章节评估已提交', plan: '正式章节计划已保存', accepted: '正文已正式验收',
    proposal_committed: '角色决定已提交', arbitration_committed: '世界裁决已提交', stage_failed: '执行阶段需要处理',
    error: '运行需要处理', failed: '运行需要处理'};
  const TIERS = {protagonist: '主角 · PERSISTENT AGENT', core: '核心角色 · AGENT',
    supporting: '重要配角 · AGENT', important: '重要角色 · AGENT'};
  const RAIL = [['world', '世界初始化'], ['outline', '全书导航'], ['rehearsal', '整弧预演'],
    ['decisions', '角色决策'], ['arbiter', '世界裁决'], ['planning', '章节封存'], ['writing', '正文验收']];
  const finite = value => typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
  const dict = value => value && typeof value === 'object' && !Array.isArray(value) ? value : {};
  const escapeHTML = value => String(value ?? '').replace(/[&<>"']/g, char => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[char]));
  function formatNumber(value, compact = false) {
    value = finite(value);
    if (value === null) return '—';
    if (compact && value >= 1000000) return (value / 1000000).toFixed(2).replace(/0+$/, '').replace(/\.$/, '') + 'M';
    if (compact && value >= 10000) return (value / 1000).toFixed(1).replace(/\.0$/, '') + 'K';
    return new Intl.NumberFormat('zh-CN', {maximumFractionDigits: 1}).format(value);
  }
  function formatCost(usage) {
    usage = dict(usage);
    const amount = finite(usage.cost_usd), source = usage.cost_source;
    if (amount === null || !['reported', 'estimated', 'partial'].includes(source)) return '未计价';
    const money = amount > 0 && amount < 0.0001 ? '<$0.0001' : '$' + amount.toFixed(amount < 0.01 && amount > 0 ? 4 : 2);
    return (source === 'estimated' ? '约 ' : source === 'partial' ? (finite(usage.estimated_calls) > 0 ? '估算小计 ' : '已计价小计 ') : '') + money;
  }
  function stageLabel(stage) { return STAGES[stage] || '等待阶段信息'; }
  function activityLabel(character) {
    character = dict(character);
    if (character.state === 'continuing') return '继续已授权的工作';
    if (character.state === 'sleeping' || character.state === 'dormant') return '休眠 · 等待新事件';
    if (character.state === 'revising' || character.state === 'revision') return character.submitted === true ? '修订决定已提交' : '根据冲突信息修订';
    if (character.submitted === true || character.state === 'submitted') return '本轮决定已提交';
    if (character.state === 'proposing' || character.state === 'active') return '独立观察与决策';
    if (character.state === 'error' || character.state === 'blocked') return '等待处理';
    return '等待本轮输入';
  }
  function buildView(snapshot) {
    snapshot = dict(snapshot);
    const chapter = dict(snapshot.chapter), planning = dict(snapshot.planning), execution = dict(snapshot.execution);
    const characters = (Array.isArray(snapshot.characters) ? snapshot.characters : []).filter(x => x && typeof x === 'object');
    const isRunning = snapshot.status === 'running';
    const allSubmitted = characters.length > 0 && characters.every(c => c.submitted === true || ['sleeping', 'dormant', 'continuing', 'submitted'].includes(c.state));
    let rail = 'world', hub = stageLabel(snapshot.stage), caption = '决定属于角色，结果接受世界规则检验。';
    if (snapshot.stage === 'outline-all') rail = 'outline';
    if (['preplan', 'rehearse-arc'].includes(snapshot.stage)) rail = 'rehearsal';
    if (snapshot.stage === 'project-all') {
      rail = execution.phase === 'assessing' || execution.phase === 'ready' ? 'planning' : allSubmitted ? 'arbiter' : 'decisions';
      hub = execution.phase === 'assessing' ? '章节收束评估' : execution.phase === 'ready' ? '准备正式章节计划' : allSubmitted ? '世界裁决与验证' : '角色独立决策中';
      caption = execution.phase === 'assessing' ? '以真实后果判断章节能否进入写作。' : allSubmitted ? '检查时间、资源和冲突，不改写角色的意图。' : '每个角色只依据自己的观察与记忆作出选择。';
    }
    if (['seal', 'promote'].includes(snapshot.stage)) rail = 'planning';
    if (['render', 'finalize', 'deliver'].includes(snapshot.stage)) rail = 'writing';
    if (!isRunning) { hub = snapshot.status === 'error' ? '运行需要处理' : '等待下一次执行'; caption = '保留已记录的结果，等待新的真实运行数据。'; }
    const completed = finite(chapter.completed), total = finite(chapter.total);
    return {snapshot, chapter, planning, execution, characters, isRunning, rail, hub, caption,
      acceptedPercent: completed !== null && total !== null && total > 0 ? Math.min(100, completed / total * 100) : 0};
  }
  function eventTime(value) {
    if (!value) return '时间待确认';
    const d = new Date(typeof value === 'number' ? value * 1000 : value);
    return Number.isFinite(d.getTime()) ? new Intl.DateTimeFormat('zh-CN', {hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false}).format(d) : '时间待确认';
  }
  function renderSnapshot(snapshot) {
    const v = buildView(snapshot), e = escapeHTML;
    const cards = v.characters.map((c, index) => {
      const color = ['', 'amber', 'blue'][index % 3], name = typeof c.name === 'string' && c.name ? c.name : '未命名角色';
      const active = v.isRunning && !['sleeping', 'dormant', 'idle', 'unknown'].includes(c.state);
      const chapterNo = finite(c.memory_chapter), round = finite(c.round);
      const actorUsage = dict(c.usage);
      const activity = !v.isRunning && ['active', 'proposing', 'revising', 'revision'].includes(c.state) ? (c.submitted ? '上次决定已提交' : '等待恢复执行') : activityLabel(c);
      const detail = !v.isRunning ? '当前未执行新任务，显示最后保存的状态。' : c.state === 'continuing' ? '沿用原有授权，不重复调用角色模型。' : c.submitted === true ? '决定保持独立，等待真实结果与后果。' : c.state === 'sleeping' ? '没有触发事件，不额外消耗模型调用。' : '私有观察与记忆不会在直播页面展示。';
      return `<article class="character-card ${color}${active ? ' is-active' : ''}" aria-label="${e(name)} ${e(activity)}"><div class="character-top"><div class="avatar">${e(Array.from(name)[0])}</div><div><div class="character-name">${e(name)}</div><div class="character-tier">${e(TIERS[c.tier] || '独立角色 · AGENT')}</div></div><i class="status-indicator"></i></div><div class="character-activity">${e(activity)}</div><p class="character-detail">${e(detail)}</p><div class="character-usage"><span>${e(formatNumber(actorUsage.calls))} 次调用</span><span>输入 ${e(formatNumber(actorUsage.input_tokens, true))}</span></div>${c.outcome && OUTCOMES[c.outcome] ? `<span class="character-tag">${e(OUTCOMES[c.outcome])}</span>` : ''}<div class="character-meta"><span>正式记忆 ${chapterNo === null ? '—' : e(chapterNo)} 章</span><span>${round === null ? '轮次待确认' : `提交轮次 R${e(round)}`}</span></div></article>`;
    }).join('') || '<div class="empty-state">角色注册后，将在这里逐一出现。</div>';
    const events = (Array.isArray(snapshot?.events) ? snapshot.events : []).filter(x => x && typeof x === 'object').slice(0, 16).map(event => {
      const meta = [event.stage && STAGES[event.stage] ? STAGES[event.stage] : '', finite(event.chapter) ? `第 ${event.chapter} 章` : '', finite(event.cycle) ? `周期 ${event.cycle}` : '', finite(event.round) ? `R${event.round}` : ''].filter(Boolean).join(' · ');
      return `<article class="event-card" data-event-id="${e(event.id)}"><div class="event-time">${e(eventTime(event.time))}</div><div class="event-title"><i></i>${e(EVENTS[event.kind] || '运行记录已更新')}</div><div class="event-meta">${e(meta || '持久运行记录')}</div></article>`;
    }).join('') || '<div class="empty-state">等待第一条运行记录；连接心跳不算业务进展。</div>';
    const rail = RAIL.map(([key, label], i) => `<div class="stage-step${v.rail === key && v.isRunning ? ' current' : v.rail === key && STAGES[v.snapshot.stage] ? ' halted' : ''}"${v.rail === key && v.isRunning ? ' aria-current="step"' : ''}><span>${String(i + 1).padStart(2, '0')}</span>${label}</div>`).join('');
    return {cards, events, rail};
  }

  // One request at a time. Stopping or switching books invalidates late replies.
  function createPoller({load, onData, onError = () => {}, interval = 3000,
    setTimer = setTimeout, clearTimer = clearTimeout, controllerFactory = () => new AbortController()}) {
    let running = false, generation = 0, active = null, timer = null;
    async function tick() {
      if (!running || active) return;
      const request = {controller: controllerFactory(), generation}; active = request;
      try {
        const value = await load(request.controller.signal);
        if (running && request.generation === generation) onData(value);
      } catch (error) {
        if (running && request.generation === generation && error?.name !== 'AbortError') onError(error);
      } finally {
        if (active === request) {
          active = null;
          if (running) timer = setTimer(tick, interval);
        }
      }
    }
    return {
      start() { if (!running) { running = true; tick(); } },
      stop() { running = false; generation++; if (timer !== null) clearTimer(timer); timer = null; const request = active; active = null; request?.controller.abort(); },
      refresh() { if (timer !== null) clearTimer(timer); timer = null; tick(); },
      isRunning() { return running; },
    };
  }
  root.BroadcastView = {escapeHTML, formatNumber, formatCost, stageLabel, activityLabel, buildView, renderSnapshot, createPoller};
  if (typeof module !== 'undefined' && module.exports) module.exports = root.BroadcastView;
  if (typeof document === 'undefined') return;

  const $ = id => document.getElementById(id);
  let selected = '', paused = false, connected = false, lastSnapshot = null, lastSuccess = 0, toastTimer;
  const text = (id, value) => { $(id).textContent = value; };
  const html = (id, value) => { if ($(id).innerHTML !== value) $(id).innerHTML = value; };
  async function getJSON(url, signal) {
    const controller = new AbortController(); let timedOut = false;
    const cancel = () => controller.abort();
    if (signal?.aborted) cancel(); else signal?.addEventListener('abort', cancel, {once: true});
    const timeout = setTimeout(() => { timedOut = true; cancel(); }, 8000);
    try {
      const response = await fetch(url, {signal: controller.signal, cache: 'no-store', headers: {'Accept': 'application/json'}});
      if (!response.ok) { const error = new Error('直播数据暂不可用'); error.status = response.status; throw error; }
      return await response.json();
    } catch (error) {
      if (timedOut) throw new Error('直播数据请求超时');
      throw error;
    } finally { clearTimeout(timeout); signal?.removeEventListener('abort', cancel); }
  }
  function setConnection(ok, message) {
    connected = ok;
    $('connection').classList.toggle('offline', !ok || paused);
    document.body.classList.toggle('is-disconnected', !ok || paused);
    $('connection').querySelector('span').textContent = paused ? '画面已暂停' : ok ? 'LIVE · 实时数据' : '等待重新连接';
    $('connection-warning').hidden = ok && !paused;
    text('connection-warning', paused ? '仅暂停页面刷新，不更改小说执行状态。' : message || '连接暂时中断，保留最后一次快照；状态可能已变化。');
  }
  function draw(snapshot) {
    if (!snapshot || snapshot.schema !== 'broadcast.v1') throw new Error('无法识别直播数据');
    lastSnapshot = snapshot; lastSuccess = Date.now();
    const v = buildView(snapshot), markup = renderSnapshot(snapshot), usage = dict(snapshot.usage);
    text('book-title', snapshot.title || '未命名小说');
    document.title = `${snapshot.title || 'Novel Studio'} · 创作直播间`;
    text('session-status', STATES[snapshot.status] || STATES.unknown);
    text('stage-description', `${stageLabel(snapshot.stage)} · ${v.isRunning ? '角色决策与世界后果，正在成为章节。' : '当前未运行，显示已保存的真实进展。'}`);
    text('position-label', v.isRunning ? '当前推进' : '最后工作位置');
    text('characters-heading', v.isRunning ? '角色正在行动' : '角色活动记录');
    text('chapter-number', formatNumber(v.chapter.current));
    text('chapter-total', finite(v.chapter.total) ? `全书 ${formatNumber(v.chapter.total)} 章` : '全书章数待确认');
    text('cycle-position', finite(v.planning.cycle) ? `CYCLE ${String(v.planning.cycle).padStart(2, '0')}${finite(v.planning.round) ? ` / R${v.planning.round}` : ''}` : '尚无周期记录');
    text('hub-title', v.hub); text('hub-caption', v.caption);
    text('hub-state', v.isRunning ? '执行中' : STATES[snapshot.status] || '待确认');
    $('world-hub').classList.toggle('busy', v.isRunning);
    $('world-hub').classList.toggle('needs-attention', ['error', 'attention'].includes(snapshot.status));
    html('stage-rail', markup.rail); html('character-grid', markup.cards); html('event-list', markup.events);
    text('agent-count', v.characters.length ? `${v.characters.length} 名独立角色` : '等待角色注册');
    text('accepted-number', formatNumber(v.chapter.completed));
    text('accepted-total', finite(v.chapter.total) ? `/ ${formatNumber(v.chapter.total)} 章` : '章');
    $('accepted-bar').style.width = `${v.acceptedPercent}%`;
    text('planned-number', formatNumber(v.planning.planned_chapters));
    text('words-number', formatNumber(v.chapter.words, true));
    text('committed-number', formatNumber(v.planning.completed_cycles));
    text('story-clock', finite(v.planning.story_minutes) !== null ? `T+${formatNumber(v.planning.story_minutes)}′` : '—');
    const input = finite(usage.input_tokens), output = finite(usage.output_tokens);
    text('usage-tokens', input !== null && output !== null ? formatNumber(input + output, true) : '—');
    text('input-tokens', `输入 ${formatNumber(input, true)}`); text('output-tokens', `输出 ${formatNumber(output, true)}`);
    text('usage-calls', `调用 ${formatNumber(usage.calls)}${finite(usage.unknown_calls) > 0 ? ` · ${formatNumber(usage.unknown_calls)} 次未计价` : ''}`); text('usage-cost', formatCost(usage));
    text('usage-scope', usage.cost_source === 'estimated' || finite(usage.estimated_calls) > 0 ? '含估算' : '已记录');
    $('durable-dot').style.background = v.execution.stalled ? 'var(--amber)' : 'var(--mint)';
    text('durable-label', v.execution.stalled ? '等待新的持久提交' : v.isRunning ? '只以持久提交计进展' : '当前未执行新任务');
    setConnection(true); tickClock();
  }
  const poller = createPoller({
    load: signal => getJSON(`/api/novels/${encodeURIComponent(selected)}/broadcast`, signal),
    onData: snapshot => { try { draw(snapshot); } catch (_) { setConnection(false, '直播数据格式暂不可用，保留最后一次快照。'); } },
    onError: error => { setConnection(false); if (error?.status === 404) { poller.stop(); discover(); } },
  });
  function showToast(message) { clearTimeout(toastTimer); text('toast', message); $('toast').classList.add('show'); toastTimer = setTimeout(() => $('toast').classList.remove('show'), 2200); }
  function switchBook(id) {
    poller.stop(); selected = id; lastSnapshot = null; lastSuccess = 0;
    const url = new URL(location.href); url.searchParams.set('novel', id); history.replaceState(null, '', url);
    text('book-title', '正在连接书目…'); html('character-grid', '<div class="empty-state">正在读取角色状态…</div>');
    html('event-list', '<div class="empty-state">正在读取运行记录…</div>');
    ['chapter-number', 'accepted-number', 'planned-number', 'words-number', 'committed-number', 'story-clock', 'usage-tokens'].forEach(id => text(id, '—'));
    text('hub-title', '正在读取新书目'); text('cycle-position', '等待周期数据'); text('durable-time', '尚无提交时间');
    $('accepted-bar').style.width = '0%'; html('stage-rail', '');
    setConnection(false, '正在连接所选书目的实时数据…');
    if (!paused && !document.hidden) poller.start();
  }
  async function discover() {
    try {
      const data = await getJSON('/api/broadcast');
      const novels = (Array.isArray(data.novels) ? data.novels : []).filter(n => n && typeof n.id === 'string');
      $('book-select').replaceChildren();
      if (!novels.length) {
        const option = document.createElement('option'); option.textContent = '暂无可直播的书目'; $('book-select').append(option);
        text('book-title', '等待故事启程'); text('stage-description', '启动一本小说后，运行状态会出现在这里。');
        setConnection(true); text('session-status', '暂无书目'); setTimeout(discover, 5000); return;
      }
      novels.forEach(n => { const option = document.createElement('option'); option.value = n.id; option.textContent = n.title || '未命名小说'; $('book-select').append(option); });
      const requested = new URL(location.href).searchParams.get('novel');
      const chosen = novels.find(n => n.id === requested) || novels.find(n => n.status === 'running') || novels[0];
      $('book-select').value = chosen.id; switchBook(chosen.id);
    } catch (_) { setConnection(false, '直播服务暂不可用，正在自动重连…'); setTimeout(discover, 5000); }
  }
  function tickClock() {
    text('wall-clock', new Intl.DateTimeFormat('zh-CN', {hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false}).format(new Date()));
    if (!lastSnapshot) return;
    const value = lastSnapshot.execution?.last_progress_at;
    const parsed = value ? new Date(typeof value === 'number' ? value * 1000 : value).getTime() : NaN;
    const seconds = Number.isFinite(parsed) ? Math.max(0, Math.floor((Date.now() - parsed) / 1000)) : null;
    text('durable-time', seconds === null ? '尚无提交时间' : `上次提交 ${Math.floor(seconds / 60)}分${String(seconds % 60).padStart(2, '0')}秒前`);
    text('refresh-state', paused ? '画面已暂停 · 不影响小说运行' : connected ? '只读连接 · 每 3 秒更新' : `连接中断 · 快照已保留 ${Math.max(0, Math.floor((Date.now() - lastSuccess) / 1000))} 秒`);
  }
  async function toggleFullscreen() {
    try {
      if (document.fullscreenElement) await document.exitFullscreen();
      else {
        if (!document.fullscreenEnabled || typeof document.documentElement.requestFullscreen !== 'function') throw new Error('unsupported');
        await document.documentElement.requestFullscreen();
        if (!document.fullscreenElement) showToast('内置浏览器未进入全屏；可使用浏览器全屏或 OBS 的 1920×1080 浏览器来源。');
      }
    }
    catch (_) { showToast('当前浏览器不支持页面全屏，可使用浏览器全屏或 OBS 窗口捕获。'); }
  }
  $('book-select').addEventListener('change', event => switchBook(event.target.value));
  $('pause-button').addEventListener('click', () => { paused = !paused; text('pause-button', paused ? '恢复画面' : '暂停画面'); if (paused) poller.stop(); else if (selected && !document.hidden) poller.start(); setConnection(connected); tickClock(); });
  $('clean-button').addEventListener('click', () => { document.body.classList.add('clean'); showToast('净屏模式 · 按 Esc 恢复操作区'); });
  $('fullscreen-button').addEventListener('click', toggleFullscreen);
  document.addEventListener('keydown', event => { if (/^(INPUT|SELECT|TEXTAREA)$/.test(event.target.tagName)) return; if (event.key === 'Escape') document.body.classList.remove('clean'); if (event.key.toLowerCase() === 'f') toggleFullscreen(); });
  document.addEventListener('visibilitychange', () => { if (document.hidden) poller.stop(); else if (!paused && selected) poller.start(); });
  window.addEventListener('pagehide', () => poller.stop());
  tickClock(); setInterval(tickClock, 1000); discover();
})(typeof globalThis !== 'undefined' ? globalThis : this);
