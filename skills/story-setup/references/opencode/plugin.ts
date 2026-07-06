import type { Plugin } from "@opencode-ai/plugin"
import * as fs from "node:fs"
import * as path from "node:path"
import { execSync } from "node:child_process"

interface StoryDeployed {
  agents_version?: number
  setup_skill_version?: string
  target_cli?: string
  resolver_strategy?: string
  references_dir?: string
}

function projectRoot(): string {
  try {
    return execSync("git rev-parse --show-toplevel", {
      cwd: process.cwd(),
      encoding: "utf-8",
      stdio: ["pipe", "pipe", "pipe"],
    }).trim()
  } catch {
    return process.cwd()
  }
}

function readSentinelField(root: string, field: string): string {
  const sentinelPath = path.join(root, ".story-deployed")
  if (!fs.existsSync(sentinelPath)) return ""
  const content = fs.readFileSync(sentinelPath, "utf-8")
  for (const line of content.split("\n")) {
    const clean = line.replace(/\r$/, "")
    const match = clean.match(new RegExp(`^${field}:\\s*(.+)`))
    if (match) {
      let val = match[1].trim()
      if (
        (val.startsWith('"') && val.endsWith('"')) ||
        (val.startsWith("'") && val.endsWith("'"))
      ) {
        val = val.slice(1, -1)
      }
      return val
    }
  }
  return ""
}

function readSentinel(root: string): StoryDeployed | null {
  const sentinelPath = path.join(root, ".story-deployed")
  if (!fs.existsSync(sentinelPath)) return null
  const agentsVer = readSentinelField(root, "agents_version")
  return {
    agents_version: agentsVer ? parseInt(agentsVer, 10) : undefined,
    setup_skill_version: readSentinelField(root, "setup_skill_version") || undefined,
    target_cli: readSentinelField(root, "target_cli") || undefined,
    resolver_strategy: readSentinelField(root, "resolver_strategy") || undefined,
    references_dir: readSentinelField(root, "references_dir") || undefined,
  }
}

function sentinelExists(root: string): boolean {
  return fs.existsSync(path.join(root, ".story-deployed"))
}

function discoverActiveBook(root: string): string | null {
  const activeBookPath = path.join(root, ".active-book")
  if (fs.existsSync(activeBookPath)) {
    const active = fs.readFileSync(activeBookPath, "utf-8").split("\n")[0].trim()
    if (active) {
      const resolved = path.resolve(root, active)
      const normalizedRoot = path.resolve(root)
      if (resolved.startsWith(normalizedRoot + path.sep) && fs.existsSync(resolved)) return resolved
    }
  }

  const firstTrackDir = findFirstDir(root, "追踪", 4)
  if (firstTrackDir) return path.dirname(firstTrackDir)

  const bodyDir = findFirstBodyDir(root, 4)
  if (bodyDir) return bodyDir

  return null
}

function findFirstDir(base: string, name: string, maxDepth: number): string | null {
  if (maxDepth <= 0) return null
  try {
    for (const entry of fs.readdirSync(base, { withFileTypes: true })) {
      if (!entry.isDirectory() || entry.name.startsWith(".")) continue
      const full = path.join(base, entry.name)
      if (entry.name === name) return full
      const found = findFirstDir(full, name, maxDepth - 1)
      if (found) return found
    }
  } catch {}
  return null
}

function findFirstBodyDir(base: string, maxDepth: number): string | null {
  if (maxDepth <= 0) return null
  try {
    for (const entry of fs.readdirSync(base, { withFileTypes: true })) {
      if (!entry.isDirectory() || entry.name.startsWith(".")) continue
      const full = path.join(base, entry.name)
      if (entry.name === "正文") return path.dirname(full)
      const found = findFirstBodyDir(full, maxDepth - 1)
      if (found) return found
    }
    for (const entry of fs.readdirSync(base, { withFileTypes: true })) {
      if (entry.isFile() && entry.name === "正文.md") return base
    }
  } catch {}
  return null
}

function tryGit(root: string, args: string): string {
  try {
    return execSync(`git ${args}`, {
      cwd: root,
      encoding: "utf-8",
      stdio: ["pipe", "pipe", "pipe"],
    }).trim()
  } catch {
    return ""
  }
}

// OpenCode Plugin API 提供 chat.message hook（见 @opencode-ai/plugin 类型定义），
// 可用于注入 session-start 检查与缺口检测。当前版以 partial 方式仅部署
// experimental.session.compacting 和 tool.execute.before，后续版本可扩展。

function preCompactOutput(): string {
  const root = projectRoot()
  const lines = ["=== Pre-Compact Summary ==="]
  const bookDir = discoverActiveBook(root)
  if (bookDir) {
    const ctxPath = path.join(bookDir, "追踪", "上下文.md")
    if (fs.existsSync(ctxPath)) {
      const lineCount = fs.readFileSync(ctxPath, "utf-8").split("\n").length
      const relPath = path.relative(root, ctxPath)
      lines.push(`Writing context: ${relPath} (${lineCount} lines)`)
    } else {
      lines.push("Active state: not found")
    }
  } else {
    lines.push("Active state: not found")
  }

  const changed = tryGit(root, "diff --name-only")
  const staged = tryGit(root, "diff --name-only --cached")
  const changedCount = changed ? changed.split("\n").filter(Boolean).length : 0
  const stagedCount = staged ? staged.split("\n").filter(Boolean).length : 0
  lines.push(`Git: ${changedCount} unstaged, ${stagedCount} staged`)

  lines.push("=== Pre-Compact Complete ===")
  return lines.join("\n")
}

// 相对路径按项目根解析（对齐 guard-outline-before-prose.sh 的 $ROOT/$TARGET）。
// Windows 盘符绝对路径（F:/... 或 F:\...）先把反斜杠归一，再交给平台感知的 path.isAbsolute
// 判断（win32 上 F:/... 为绝对），与 bash hook 的 [A-Za-z]:[/\\]* 分支等价（issue #184）。
function resolveTarget(root: string, target: string): string {
  const normalized = target.replace(/\\/g, "/")
  return path.isAbsolute(normalized) ? normalized : path.resolve(root, normalized)
}

// 从 bash 命令里提取真正的「正文」写入目标（重定向 / tee / touch / cp|mv 目标），用于防止
// 绕过 write/edit 守卫。只认真实写入目标，避免 heredoc 正文、文档字符串或 grep 模式里仅仅
// “提到” 正文/第N章.md 就被误判为写正文（与 codex story_codex_hook.py 同实现，保持一致）。
function extractProseTargets(cmd: string): string[] {
  const out: string[] = []
  // 重定向 / tee / touch 用正则；起始/分隔符类（非 \b）保证与 codex story_codex_hook.py 一致
  // （\b 在 Python re 是 Unicode-aware、在 JS 是 ASCII-only，会让两端对 CJK 粘连的判定不同）。
  const patterns = [
    />>?\s*['"]?([^\s'"<>|;&()]*正文[^\s'"<>|;&()]*)['"]?/g,
    /(?:^|[\s;&|(){}<>])(?:tee(?:\s+-a)?|touch)\s+['"]?([^\s'"<>|;&()]*正文[^\s'"<>|;&()]*)['"]?/g,
  ]
  for (const re of patterns) {
    let m: RegExpExecArray | null
    while ((m = re.exec(cmd)) !== null) {
      if (m[1]) out.push(m[1])
    }
  }
  // cp/mv：写入目标是段内最后一个位置参数（正则分不清 正文 源/目标，且尾部 2>/dev/null/>log/|| 会破坏锚定）
  for (const raw of cmd.split(/[;&|\n]/)) {
    const seg = raw.split(/\d*[<>]/)[0]
    const words = seg.split(/\s+/).filter(Boolean)
    if (words.length >= 2 && (words[0] === "cp" || words[0] === "mv")) {
      const positionals = words.slice(1).filter((w) => !w.startsWith("-"))
      const dest = positionals[positionals.length - 1]
      if (dest && dest.includes("正文")) out.push(dest.replace(/^['"]|['"]$/g, ""))
    }
  }
  return out
}

// 按目标文件判断是否拦截写正文，逐字对齐 Claude hook guard-outline-before-prose.sh：
// 只拦「首次创建正文文件且缺对应大纲/细纲」，已存在正文（续写/改稿/去AI味）一律放行，
// 解析不到、非正文目标、拆文迁移一律放行（宁可漏拦不可误伤）。
// 返回拦截原因；返回 null 表示放行。
function proseBlockReason(root: string, abs: string): string | null {
  const base = path.basename(abs)
  const parent = path.basename(path.dirname(abs))

  // 短篇单文件正文：{书}/正文.md
  if (base === "正文.md") {
    if (fs.existsSync(abs)) return null // 已存在 → 续写/改稿放行
    const bookDir = path.dirname(abs)
    // 拆文迁移：已有 拆文库/{书名}/ 分析源时，正文先于小节大纲迁移属正常流程
    if (fs.existsSync(path.join(root, "拆文库", path.basename(bookDir)))) return null
    // 仅在确为短篇工程时拦截（有 设定.md 信号），避免误伤 docs/正文.md 等非作品文件
    if (!fs.existsSync(path.join(bookDir, "设定.md"))) return null
    if (!fs.existsSync(path.join(bookDir, "小节大纲.md"))) {
      return `⛔ 写正文被拦截：${path.relative(root, abs) || abs} 缺少同目录 小节大纲.md。先补齐「小节大纲.md」，再通过 novel-studio --pipeline 写正文。`
    }
    return null
  }

  // 长篇分章正文：{书}/正文/第N章*.md
  if (parent !== "正文") return null
  if (!/^第.*章.*\.md$/.test(base)) return null
  if (fs.existsSync(abs)) return null // 已存在 → 续写/改稿放行
  const m = base.match(/^第0*(\d+)章/)
  if (!m) return null
  const num = m[1]
  const bookDir = path.dirname(path.dirname(abs))
  // 拆文迁移：已有 拆文库/{书名}/ 分析源时放行（细纲由章节摘要反推、晚于正文迁移）
  if (fs.existsSync(path.join(root, "拆文库", path.basename(bookDir)))) return null
  // 容忍补零差异与标题后缀：按整数章号匹配 大纲/细纲_第*章*.md
  const outlineDir = path.join(bookDir, "大纲")
  let found = false
  try {
    for (const f of fs.readdirSync(outlineDir)) {
      const fm = f.match(/^细纲_第0*(\d+)章.*\.md$/)
      if (fm && fm[1] === num) {
        found = true
        break
      }
    }
  } catch {}
  if (!found) {
    return `⛔ 写正文被拦截：第 ${num} 章缺少细纲（${path.relative(root, outlineDir)}/细纲_第${num}章.md）。先补建本章细纲，再通过 novel-studio --pipeline 写正文。`
  }
  return null
}

// ── 轻量确定性网（与 templates/hooks/check-prose-after-write.sh / codex prose_net_findings
// 同实现，保持 parity）。只兜硬信号：截断/生成拒绝语·AI自指/工程词漏正文/紧邻整行复读。
// 不依赖 check-degeneration.js，是独立的轻量网。OpenCode 无 PostToolUse 之外的可注入会话事件，
// 故内容网与标题去重在 tool.execute.after 把发现追加进写工具的返回结果，让模型读到。
const NET_TERMINAL = new Set("。！？…”』」）)!?.~—".split(""))
const NET_QUOTE_OPENERS = ["「", "“", "‘", "『", '"']
const NET_SOFT_PATTERNS: Array<[RegExp, string]> = [
  [/作为(一个)?(AI|人工智能|大?语言模型|智能助手|聊天助手)(?=，|,|。|、|；|;|：|:|！|!|？|\?|\s|）|\)|」|』|"|】|我|无法|不能|没法|$)/, "AI 自指"],
  [/^(Sure|Certainly|Here'?s|As an AI|I (?:cannot|can't|am unable|apologize))/, "英文 AI 腔"],
  [/我(无法|不能)(继续(写|创作|生成|下去|输出)?|生成(内容|文本|正文)?|创作|续写|写作|完成(这个|本)?(章|篇|创作|请求)?)/, "生成拒绝语"],
]
const NET_HARD_PATTERNS: Array<[RegExp, string]> = [
  [/[（(](此处|以下|这里|下文|后续)?[^）)]{0,10}(省略|略去|略过)[^）)]{0,10}[）)]/, "占位符（括号省略）"],
  [/(TODO|占位符|placeholder|待补充|此处待填|此处待补)/, "占位符"],
  [/(细纲|情节点|卷纲|功能标签|目标情绪|字数目标|章首钩子|章尾钩子|任务描述)/, "工程词泄漏"],
  [/�/, "乱码（替换字符）"],
]

function netIsSkippable(s: string): boolean {
  if (!s) return true
  if (s[0] === "#") return true
  if (s === "---") return true
  if (/^[-—=*·•\s]+$/.test(s)) return true
  return false
}

function proseNetFindings(text: string): string[] {
  const findings: string[] = []
  const content: Array<[number, string]> = []
  const lines = text.split("\n")
  for (let i = 0; i < lines.length; i++) {
    const s = lines[i].trim()
    if (netIsSkippable(s)) continue
    const lineNo = i + 1
    content.push([lineNo, s])
    const isDialogue = NET_QUOTE_OPENERS.includes(s[0])
    let hit = false
    if (!isDialogue) {
      for (const [rx, label] of NET_SOFT_PATTERNS) {
        const m = rx.exec(s)
        if (m) {
          findings.push(`第${lineNo}行 元信息泄漏（${label}）：「${m[0].slice(0, 20)}」`)
          hit = true
          break
        }
      }
    }
    if (hit) continue
    for (const [rx, label] of NET_HARD_PATTERNS) {
      const m = rx.exec(s)
      if (m) {
        findings.push(`第${lineNo}行 ${label}：「${m[0].slice(0, 20)}」`)
        break
      }
    }
  }
  for (let i = 1; i < content.length; i++) {
    const [, sa] = content[i - 1]
    const [lb, sb] = content[i]
    if (sa === sb && sa.length >= 8) {
      findings.push(`第${lb}行 紧邻复读：整行与上一行完全相同「${sa.slice(0, 20)}」`)
    }
  }
  if (content.length) {
    const [ln, last] = content[content.length - 1]
    if (last && !NET_TERMINAL.has(last[last.length - 1])) {
      findings.push(`第${ln}行 疑似截断：结尾「…${last.slice(-12)}」未以标点收束`)
    }
  }
  return findings
}

// 正文文件判定（与 proseBlockReason 的门一致，但这里是"已写完"的事后兜底）。
function isProsePath(abs: string): boolean {
  const base = path.basename(abs)
  const parent = path.basename(path.dirname(abs))
  if (base === "正文.md") return fs.existsSync(path.join(path.dirname(abs), "设定.md"))
  if (parent === "正文" && /^第.*章.*\.md$/.test(base)) {
    const book = path.dirname(path.dirname(abs))
    return (
      fs.existsSync(path.join(book, "大纲")) ||
      fs.existsSync(path.join(book, "追踪")) ||
      fs.existsSync(path.join(book, "设定")) ||
      fs.existsSync(path.join(book, "设定.md"))
    )
  }
  return false
}

// 字数欠账（仅长篇分章正文）：从 大纲/细纲_第N章*.md 读「字数目标」，实际 < 90% 提示。
function wordcountFinding(abs: string, text: string): string | null {
  const base = path.basename(abs)
  if (path.basename(path.dirname(abs)) !== "正文") return null
  const m = base.match(/^第0*(\d+)章/)
  if (!m) return null
  const num = m[1]
  const book = path.dirname(path.dirname(abs))
  const outlineDir = path.join(book, "大纲")
  let target: number | null = null
  try {
    for (const f of fs.readdirSync(outlineDir)) {
      const fm = f.match(/^细纲_第0*(\d+)章.*\.md$/)
      if (!fm || fm[1] !== num) continue
      const tm = fs.readFileSync(path.join(outlineDir, f), "utf-8").match(/字数目标[^0-9]{0,6}(\d{3,6})/)
      if (tm) target = parseInt(tm[1], 10)
      break
    }
  } catch {}
  if (!target) return null
  const actual = [...text].length
  if (actual < target * 0.9) {
    return `字数：第${num}章 实际 ${actual} 字 < 目标 ${target} 的 90%（${Math.floor(target * 0.9)}）。对照细纲字数预算定位欠账的密点、一次性重写到配额，别挤牙膏回炉。`
  }
  return null
}

// 标题去重（同书 正文/第N章_标题 的标题部分撞名，多半误复制）。OpenCode 无会话事件可注入，
// 故在写正文时顺带查同书撞名（无 staleness 时间窗问题）；追踪 staleness 留给 Claude/Codex 的会话起点。
function dupTitleFindings(abs: string): string[] {
  const bodyDir = path.dirname(abs)
  if (path.basename(bodyDir) !== "正文") return []
  const titles = new Map<string, string[]>()
  try {
    for (const f of fs.readdirSync(bodyDir)) {
      const mt = f.replace(/\.md$/, "").match(/^第0*\d+章[_\- 　]+(.+)$/)
      if (!mt) continue
      const key = mt[1].trim()
      if (key) titles.set(key, [...(titles.get(key) || []), f])
    }
  } catch {}
  const out: string[] = []
  for (const [title, files] of titles) {
    if (files.length > 1) out.push(`[continuity] ${files.length} 章标题重复「${title}」（${files.join("、").slice(0, 60)}），建议改名。`)
  }
  return out
}

// 正文落盘后的兜底：收集 落盘/net/字数/标题去重 发现，追加进写工具返回结果（让模型读到）。
function proseAfterWriteNote(root: string, abs: string): string {
  if (!isProsePath(abs) || !fs.existsSync(abs)) return ""
  const out: string[] = []
  let bytes = 0
  try {
    bytes = fs.statSync(abs).size
  } catch {}
  if (bytes < 200) out.push(`【落盘】正文仅 ${bytes} 字节，疑似未写完/落盘失败（quota/超时中断？），请核对并补写。`)
  let text = ""
  try {
    text = fs.readFileSync(abs, "utf-8")
  } catch {
    text = ""
  }
  if (text) {
    out.push(...proseNetFindings(text))
    const wc = wordcountFinding(abs, text)
    if (wc) out.push(wc)
  }
  out.push(...dupTitleFindings(abs))
  if (!out.length) return ""
  return `\n\n=== 正文兜底检测（${path.relative(root, abs) || path.basename(abs)}）===\n轻量确定性网自动复扫（模型无关，防漏跑收尾）。按类型处理后复扫到净：\n${out.join("\n")}`
}

export default (async () => {
  return {
    "experimental.session.compacting": async (
      _input: unknown,
      output: { context: string[]; prompt?: string }
    ) => {
      const preMsg = preCompactOutput()
      if (preMsg) {
        output.context = [...output.context, preMsg]
      }
      // 不注入 post-compact 信息：OpenCode 无压缩后 hook
    },

    "tool.execute.before": async (
      input: { tool: string; args?: Record<string, unknown> },
      output: { args?: Record<string, unknown> }
    ) => {
      const root = projectRoot()
      const targets: string[] = []

      if (input.tool === "write" || input.tool === "edit") {
        const filePath = (output.args?.filePath as string) || ""
        if (filePath) targets.push(resolveTarget(root, filePath))
      } else if (input.tool === "bash") {
        const cmd = (output.args?.command as string) || ""
        for (const t of extractProseTargets(cmd)) targets.push(resolveTarget(root, t))
      } else {
        return
      }

      for (const abs of targets) {
        const reason = proseBlockReason(root, abs)
        if (reason) {
          throw new Error(`${reason}（此操作无法通过 Bash/命令行绕过。）`)
        }
      }
    },

    // 正文落盘兜底：写正文后跑轻量确定性网（截断/拒绝语/工程词/复读 + 落盘/字数/标题去重），
    // 把发现追加进写工具的返回结果让模型读到。非正文文件、无发现一律不动结果（静默放行）。
    // OpenCode 无 PostToolUse，tool.execute.after 是写后唯一可向模型回话的钩子。
    "tool.execute.after": async (
      input: { tool: string; args?: Record<string, unknown> },
      output: { output?: string }
    ) => {
      if (input.tool !== "write" && input.tool !== "edit") return
      const filePath = (input.args?.filePath as string) || ""
      if (!filePath) return
      const root = projectRoot()
      try {
        const note = proseAfterWriteNote(root, resolveTarget(root, filePath))
        if (note && typeof output.output === "string") output.output += note
      } catch {
        // 兜底不能反过来卡流程：解析失败一律放行
      }
    },
  }
}) satisfies Plugin
