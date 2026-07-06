#!/bin/bash
# check-prose-after-write.sh — PostToolUse(Write|Edit|MultiEdit) 正文兜底
# 正文落盘后自动跑「轻量确定性网」，把发现注入提醒——模型无关的兜底层：
# 即使主会话漏跑「确定性收尾」步骤（压缩/弱模型/分心），这些硬信号也保证被抓。
#
# 只兜「硬信号」（漏跑最伤、退化模型自己发现不了的）：截断、生成拒绝语 / AI 自指、
# 工程词漏进正文、紧邻整行复读、落盘失败/截断、字数欠账。碎句号/长段落/破折号这类
# advisory，以及复读全量 / tier2 歧义词，仍由 workflow 收尾步骤的 check-ai-patterns /
# check-degeneration 全量跑——本 hook 不部署也不依赖那两个检测器，是独立的轻量网。
#
# 覆盖范围：只在 PostToolUse 的 Write|Edit|MultiEdit 上触发。cat>/tee/cp/mv 等用 Bash
# 写正文的路径绕过本 hook（Claude/OpenCode 侧 Bash 只做 pre-guard，无 post-write 兜底）；
# 这类路径由 Codex 的 Stop 回合末 git 改动集扫描兜全。已知边界，非缺陷。
#
# 非阻塞（exit 0，advisory 提醒，不挡写作）；无发现时完全静默（不污染 context）；
# 解释器不可用时静默放行（兜底不能反过来卡流程）。
set -euo pipefail

source "$(dirname "$0")/lib/common.sh"

# 中文路径上做 bash 通配/basename/内嵌 python。Windows 中文系统的 GBK 区域会把
# UTF-8 字面量按多字节误解码、让每个比较恒假而静默失效（issue #164）。强制 C 区域走
# 字节匹配才稳定；必须在内嵌 python 之前 export（与 guard-outline-before-prose.sh 同）。
# 内嵌 python 一律以 encoding='utf-8' 显式读文件、用 stdout.buffer 写 UTF-8 字节，
# 故 LC_ALL=C 只约束 bash，不影响 python 的中文解码（正则/字数都按码点处理）。
export LC_ALL=C

HOOK_INPUT="${CLAUDE_TOOL_INPUT:-}"
if [ -z "$HOOK_INPUT" ] && [ ! -t 0 ]; then
  HOOK_INPUT="$(cat)"
fi
export HOOK_INPUT

# 探测真正可用的解释器（Windows 上裸 `python3` 会命中 Microsoft Store 占位程序、以
# exit 49 静默失败，必须实跑 -c ""）。net 与字数都靠它；探测不到就静默放行。
PYBIN=""
for c in python3 python py; do
  if "$c" -c "" >/dev/null 2>&1; then PYBIN="$c"; break; fi
done
[ -z "$PYBIN" ] && exit 0

# 抽取目标文件路径（输出走 sys.stdout.buffer 直写 UTF-8 字节，避开 cp936 文本模式把
# 中文路径编成 GBK）。
extract_target_path() {
  "$PYBIN" - <<'PY'
import json, os, sys
raw = os.environ.get("HOOK_INPUT", "")
if not raw:
    sys.exit(1)
try:
    obj = json.loads(raw)
except Exception:
    sys.exit(1)
def dig(value):
    if isinstance(value, dict):
        for k in ("file_path", "path", "filePath"):
            v = value.get(k)
            if isinstance(v, str) and v:
                return v
        for k in ("tool_input", "input", "parameters", "args"):
            found = dig(value.get(k))
            if found:
                return found
    return ""
p = dig(obj)
if not p:
    sys.exit(1)
sys.stdout.buffer.write(p.encode("utf-8"))
PY
}

TARGET="$(extract_target_path 2>/dev/null || true)"
[ -z "$TARGET" ] && exit 0

ROOT=$(project_root)
# 盘符绝对路径归一（对齐 guard-outline-before-prose.sh / plugin.ts，issue #184）。
case "$TARGET" in
  /*) ABS="$TARGET" ;;
  [A-Za-z]:[/\\]*) ABS="${TARGET//\\//}" ;;
  *)  ABS="$ROOT/$TARGET" ;;
esac

BASE="$(basename "$ABS")"
PARENT="$(basename "$(dirname "$ABS")")"

# 只对「正文」文件兜底，绝不碰代码/细纲/设定/大纲等非正文文件：
#   - 短篇：{书}/正文.md，且同目录有 设定.md（真短篇工程信号，排除 docs/正文.md 之类）
#   - 长篇：{书}/正文/第N章*.md（父目录必须是「正文」），且 {书} 有 大纲/追踪/设定（真书结构）
# case 模式锚定首字：细纲_第N章.md（首字「细」）、卷纲_第1卷.md、check-ai-patterns.js、
# 设定.md、大纲.md 等天然都不匹配 `正文.md`/`第*章*.md`，不会被捕获。
IS_PROSE=false
case "$BASE" in
  正文.md)
    [ -f "$(dirname "$ABS")/设定.md" ] && IS_PROSE=true
    ;;
  第*章*.md)
    if [ "$PARENT" = "正文" ]; then
      BOOK="$(dirname "$(dirname "$ABS")")"
      if [ -d "$BOOK/大纲" ] || [ -d "$BOOK/追踪" ] || [ -d "$BOOK/设定" ] || [ -f "$BOOK/设定.md" ]; then
        IS_PROSE=true
      fi
    fi
    ;;
esac
[ "$IS_PROSE" = true ] || exit 0
[ -f "$ABS" ] || exit 0

OUT=""

# 落盘检测：正文极短（<200 字节）多半是没写完或落盘失败（quota/timeout 中断）。
# 用字节（wc -c）而非字数：LC_ALL=C 下无法按码点数中文，字节阈值已足够判「几乎空」。
BYTES=$(wc -c < "$ABS" 2>/dev/null | tr -d ' ' || echo 0)
case "$BYTES" in ''|*[!0-9]*) BYTES=0 ;; esac
if [ "$BYTES" -lt 200 ]; then
  OUT+="【落盘】正文仅 ${BYTES} 字节，疑似未写完/落盘失败（quota/超时中断？），请核对并补写。\n"
fi

# 内容网 + 字数：单次内嵌 python。net 抓 截断/拒绝语/AI自指/工程词tier1/紧邻复读（硬信号，
# 退化模型自己发现不了）；字数从 大纲/细纲_第N章*.md 的「字数目标」对照实际<90% 提示。
# best-effort：找不到细纲/目标静默跳过，不误报。输出走 buffer 直写 UTF-8（Windows cp936 安全）。
NET_MSG="$("$PYBIN" - "$ABS" "$BASE" "$PARENT" <<'PY' 2>/dev/null || true
import os, re, sys, glob

abs_path, base, parent = sys.argv[1:4]

# ── 轻量确定性网（与 codex story_codex_hook.py 的 prose_net_findings 同实现，保持 parity）──
TERMINAL = set('。！？…”』」）)!?.~—')
QUOTE_OPENERS = ('「', '“', '‘', '『', '"')
# 软信号（拒绝语 / AI 自指）：只在「非对话」叙述行判——AI 伴侣 / 系统流题材里 AI 角色台词
# 「作为AI，我会保护你」是合法对话，不是模型拒绝语。硬信号（工程词 / 乱码 / 占位符）任何行都判。
SOFT_PATTERNS = [
    (re.compile(r'作为(一个)?(AI|人工智能|大?语言模型|智能助手|聊天助手)(?=，|,|。|、|；|;|：|:|！|!|？|\?|\s|）|\)|」|』|"|】|我|无法|不能|没法|$)'), 'AI 自指'),
    (re.compile(r"^(Sure|Certainly|Here'?s|As an AI|I (?:cannot|can't|am unable|apologize))"), '英文 AI 腔'),
    (re.compile(r'我(无法|不能)(继续(写|创作|生成|下去|输出)?|生成(内容|文本|正文)?|创作|续写|写作|完成(这个|本)?(章|篇|创作|请求)?)'), '生成拒绝语'),
]
HARD_PATTERNS = [
    (re.compile(r'[（(](此处|以下|这里|下文|后续)?[^）)]{0,10}(省略|略去|略过)[^）)]{0,10}[）)]'), '占位符（括号省略）'),
    (re.compile(r'(TODO|占位符|placeholder|待补充|此处待填|此处待补)'), '占位符'),
    (re.compile(r'(细纲|情节点|卷纲|功能标签|目标情绪|字数目标|章首钩子|章尾钩子|任务描述)'), '工程词泄漏'),
    (re.compile('�'), '乱码（替换字符）'),
]

def is_skippable(stripped):
    if not stripped:
        return True
    if stripped[0] == '#':
        return True
    if stripped == '---':
        return True
    if re.match(r'^[-—=*·•\s]+$', stripped):
        return True
    return False

def prose_net_findings(text):
    findings = []
    content = []  # (lineno, stripped)
    for i, raw in enumerate(text.split('\n'), 1):
        s = raw.strip()
        if is_skippable(s):
            continue
        content.append((i, s))
        is_dialogue = s[0] in QUOTE_OPENERS
        hit = False
        if not is_dialogue:
            for rx, label in SOFT_PATTERNS:
                m = rx.search(s)
                if m:
                    findings.append(f'第{i}行 元信息泄漏（{label}）：「{m.group(0)[:20]}」')
                    hit = True
                    break
        if hit:
            continue
        for rx, label in HARD_PATTERNS:
            m = rx.search(s)
            if m:
                findings.append(f'第{i}行 {label}：「{m.group(0)[:20]}」')
                break
    # 紧邻复读：相邻两条内容行完全相同且可见长度 ≥ 8（通俗网文的排比/弹幕是相似非全等，不会命中）
    for (la, sa), (lb, sb) in zip(content, content[1:]):
        if sa == sb and len(sa) >= 8:
            findings.append(f'第{lb}行 紧邻复读：整行与上一行完全相同「{sa[:20]}」')
    # 截断：最后一条内容行末字不是终止/闭合标点
    if content:
        ln, last = content[-1]
        if last and last[-1] not in TERMINAL:
            findings.append(f'第{ln}行 疑似截断：结尾「…{last[-12:]}」未以标点收束')
    return findings

try:
    text = open(abs_path, encoding='utf-8').read()
except Exception:
    sys.exit(0)

out = list(prose_net_findings(text))

# ── 字数欠账（仅长篇分章正文：父目录是「正文」且文件名是第N章）──
m = re.match(r'^第0*(\d+)章', base)
if parent == '正文' and m:
    num = m.group(1)
    book = os.path.dirname(os.path.dirname(abs_path))
    target = None
    for f in glob.glob(os.path.join(book, '大纲', '细纲_第*章*.md')):
        fm = re.search(r'细纲_第0*(\d+)章', os.path.basename(f))
        if not fm or fm.group(1) != num:
            continue
        try:
            txt = open(f, encoding='utf-8').read()
        except Exception:
            continue
        tm = re.search(r'字数目标[^0-9]{0,6}(\d{3,6})', txt)
        if tm:
            target = int(tm.group(1))
        break
    if target:
        actual = len(text)
        if actual < target * 0.9:
            out.append(f'字数：第{num}章 实际 {actual} 字 < 目标 {target} 的 90%（{int(target*0.9)}）。'
                       f'对照细纲字数预算定位欠账的密点、一次性重写到配额，别挤牙膏回炉。')

if out:
    sys.stdout.buffer.write('\n'.join(out).encode('utf-8'))
PY
)"
[ -n "$NET_MSG" ] && OUT+="【退化/工程词/字数】（硬信号：截断/拒绝语/工程词→重写；命中即处理，别留给下一章）\n${NET_MSG}\n"

[ -z "$OUT" ] && exit 0

printf '%b' "=== 正文兜底检测（${BASE}）===\n轻量确定性网自动复扫（模型无关，防主会话漏跑收尾）。按类型处理后复扫到净：\n${OUT}"
exit 0
