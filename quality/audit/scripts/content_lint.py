#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
content_lint.py - narrative content sanity checks.

These checks catch obvious factual wording mistakes and awkward surface style
that AI-taste metrics do not see, such as "四个字：抵押物待核" when the
quoted text has five Hanzi, a forced simile like "像一根没拔干净的刺",
or a dangling order word like "挂钟先停了". It also flags piled-up
state clauses like "屏幕还亮着，表停在最后一行，批注还在", abrupt
strong events like "隔壁1703忽然砸门" that lack a sound/action setup,
unsupported character evidence claims like "我听见你说话了", opaque
memo shorthand like "别回号" or "零钱暂不碰", and punctuation that flattens
panic dialogue or document-like rules into mechanical periods. It also catches
clipped outline-style phrasing such as "多半是1703蒋牧", "搬来两个月，电梯里刷短视频外放",
or summary cliches like "这通话只给了两个确认".
"""
from __future__ import annotations

import argparse
import json
import re
from collections import Counter
from pathlib import Path


COUNT_WORD_RE = re.compile(
    r"([零一二两三四五六七八九十]{1,4})个(?:小|蓝|黑|白|旧)?字[：:]\s*[“\"「『【]?([^。！？!?；;\n]+)"
)
TWO_ITEMS_AS_TWO_CHARS_RE = re.compile(r"([\u4e00-\u9fff]{2,8})和([\u4e00-\u9fff]{2,8})两个字")
FORCED_SIMILE_RE = re.compile(
    r"像一[根把柄枚颗块片团道条张][^。！？!?；;\n]{0,12}(?:刺|刀|针|钉子|石头|冰|火)"
)
STOCK_SIMILE_WORDS = [
    "像一根刺",
    "像一把刀",
    "像一根针",
    "像一块石头",
    "像潮水",
    "像被谁掐住喉咙",
]
AMBIGUOUS_ORDER_RE = re.compile(r"[^。！？!?；;\n]{0,24}先(?:(?:停|亮|黑|灭|响|断|冷|热|暗|红|白)了|闭嘴|住口|没声)[^。！？!?；;\n]*")
ORDER_FOLLOWUP_RE = re.compile(r"(?:再|又|随后|接着|然后|才|第二|另一|另一个|后面)")
ABRUPT_STRONG_EVENT_RE = re.compile(
    r"[^。！？!?；;\n]{0,28}(?:忽然|突然|猛地|猛然|一下子)[^。！？!?；;\n]{0,18}"
    r"(?:砸门|撞门|踹门|扑过来|扑倒|冲进|冲出|爆开|炸开|裂开|塌下|断电|熄灭|尖叫|惨叫|倒下|伸进来|抓住|咬住)[^。！？!?；;\n]*"
)
EVENT_SETUP_RE = re.compile(r"(?:传来|听见|听到|先是|紧接着|接着|随后|声音|动静|脚步|闷响|嗒|咚|咣|看见|只见|惊动)")
UNSUPPORTED_SPEECH_CLAIM_RE = re.compile(r"[^。！？!?；;\n]{0,18}我(?:听见|听到)你说话了?[^。！？!?；;\n]*")
OPAQUE_MEMO_SHORTHAND_RE = re.compile(r"[^。！？!?；;\n]{0,24}(?:别回号|别回名|回号|回名|暂不碰)[^。！？!?；;\n]{0,24}")
UNIT_NAME_APPOSITION_RE = re.compile(r"[^。！？!?；;\n]{0,18}(?:多半是|应该是|估计是|像是|就是)(\d{3,4})(?!的)([\u4e00-\u9fff]{2,4})[^。！？!?；;\n]*")
CLIPPED_HABIT_RE = re.compile(r"(?:搬来|住进来)[^。！？!?；;\n]{0,12}，(?:电梯里|楼道里|门口)[^。！？!?；;\n]{0,18}(?:外放|拿错|敲门|骂人)[^。！？!?；;\n]*")
CLIPPED_SUMMARY_RE = re.compile(r"[^。！？!?；;\n]{0,18}(?:这通话|这通电话只给了两个确认|两个确认：|最便宜的坑)[^。！？!?；;\n]*")
STATE_MARKERS = ["还", "在", "停在", "亮着", "没熄", "留着", "还在", "还亮着"]
STATE_PILE_SPLIT_RE = re.compile(r"[。！？!?；;\n]")
SENTENCE_SPLIT_RE = re.compile(r"[。！？!?；;\n]+")
SIMILE_RE = re.compile(r"(?:像|好像|仿佛|宛如|如同|似|倒像)")
MICRO_ACTION_RE = re.compile(
    r"[^。！？!?；;\n]{0,24}(?:(?:指腹|指尖|手指|肩膀|喉咙|掌心|脸皮|眼皮|嘴角|后背|脊背|手腕|膝盖)"
    r"[^。！？!?；;\n]{0,12}(?:收紧|绷住|发紧|出汗|一抖|发颤|发麻|僵住|顿住|停住|发冷|发白)|"
    r"[\u4e00-\u9fff]{1,8}了?一下)[^。！？!?；;\n]*"
)
DRAMATIC_NEGATION_RE = re.compile(
    r"[^。！？!?；;\n]{0,24}(?:(?:没有|没)(?:立刻|马上|急着|答|回答|开口|说话|让自己去想|让自己多想)|"
    r"没有[^。！？!?；;\n]{1,18}[，,]?只[^。！？!?；;\n]{1,24})[^。！？!?；;\n]*"
)
NOT_BUT_RE = re.compile(r"不是[^。！？!?；;\n]{1,30}[，,]?而是[^。！？!?；;\n]{1,30}")
PRECISE_MEASURE_RE = re.compile(r"(?:一指|半指|两指|三指|一寸|半寸|两寸|三寸|数寸|一尺|半尺|两尺)")
PATCH_PHRASE_RE = re.compile(r"(?:停了一拍|停了停|停了半拍|停住了|顿了一拍|顿了顿|慢了一拍|隔了一拍|卡了一下|卡了卡|僵了一拍)")
MINOR_MISTAKE_RE = re.compile(r"(?:手滑|拿错|写错|看错|听错|报错|认错|记错|漏了|掉了|碰掉|卡住|退错|走错|念错|按错|发错|删错)")
SUPPORTING_QUIP_RE = re.compile(r"([\u4e00-\u9fff]{2,4})[^。！？!?；;\n“”「」]{0,10}(?:骂|嘀咕|吐槽|抱怨|冷笑|嗤|啧|嚷)[^。！？!?；;\n“”「」]{0,12}[“「][^”」]{2,90}[”」]")
VAGUE_QUANTIFIER_RE = re.compile(r"(?:一点|几分|半(?:点|分|步|拍|秒|晌|刻|截|句|声|口|圈))")
OBJECT_RESPONSE_RE = re.compile(
    r"[^。！？!?；;\n]{0,24}(?:(?:话音|声音|话|字|手指|按钮)[^。！？!?；;\n]{0,8}(?:刚落|刚出口|刚停|刚按下|刚碰到)|"
    r"刚说完|才说完|刚写完|刚签下)?[^。！？!?；;\n]{0,24}"
    r"(?:屏幕|手机|白纸|纸面|纸条|账单|欠费单|合同|条款|门牌|电梯|门锁|灯管|灯|广播|卡面|备忘录|提示框|系统|镜子|墙面|货架|价签)"
    r"[^。！？!?；;\n]{0,18}(?:亮|暗|闪|响|震|跳|弹|显|浮|冒|多出|变成|改成|停|裂|动|滚|掉|滑)[^。！？!?；;\n]*"
)
OBJECT_RESPONSE_DELAY_RE = re.compile(
    r"(?:过了|隔了|等了|迟了|晚了|半晌|片刻|几秒|半分钟|很久)[^。！？!?；;\n]{0,36}"
    r"(?:屏幕|手机|白纸|纸面|纸条|账单|欠费单|合同|条款|门牌|电梯|门锁|灯管|灯|广播|卡面|备忘录|提示框|系统|镜子|墙面|货架|价签)"
    r"[^。！？!?；;\n]{0,18}(?:亮|暗|闪|响|震|跳|弹|显|浮|冒|多出|变成|改成|停|裂|动|滚|掉|滑)"
)
OBJECT_RESPONSE_ABSENCE_RE = re.compile(r"(?:没有回应|没回应|没有动静|没动静|什么都没发生|屏幕没亮|纸面没动|白纸没动|卡面没变|灯没闪|门牌没变|静默|安静|没人接话|无人接话)")
APHORISTIC_DIALOGUE_RE = re.compile(r"(?:命运|人生|世界|人心|真正|所谓|终究|从来|答案|选择|自由|救赎|意义|真相|没有人|所有人|总有一天|不是[^，。！？!?]{1,18}而是|你以为|其实)")
CHAPTER_HEADING_RE = re.compile(r"(?m)^#{1,3}\s*第[0-9零一二三四五六七八九十百]+章[^\n]*$")
EMPTY_PARALLEL_CHANT_RE = re.compile(r"不开[，,]不报[；;][^。！？!?“”「」]{0,16}不开[，,]不认[；;][^。！？!?“”「」]{0,16}不开[，,]不替")
DE_FA_PHRASE_RE = re.compile(r"[\u4e00-\u9fff]{1,10}(?:得发[潮虚沉乌黄紧硬白黑冷热麻颤暗亮干湿酸涩烫凉疼][\u4e00-\u9fff]?|发(?:潮|虚|沉|乌|黄|紧|硬|白|黑|冷|热|麻|颤|暗|亮|干|湿|酸|涩|烫|凉|疼))")
DUPLICATE_WIND_CONTROL_RE = re.compile(r"当风控.{0,160}破风控|破风控.{0,160}当风控", re.S)
IMPOSSIBLE_SHADOW_GEOMETRY_RE = re.compile(r"肩膀以下[^。！？!?；;\n]{0,28}腰以上空了|腰以上空了[^。！？!?；;\n]{0,28}肩膀以下")
CAT_EYE_IMPOSSIBLE_READ_RE = re.compile(r"(?:猫眼|门里|1704)[^。！？!?；;\n]{0,70}(?:白纸背面|背面翻起一角)[^。！？!?；;\n]{0,45}(?:代缴|双方确认|需双方确认)")
FORM_IMAGE_MISMATCH_RE = re.compile(r"四栏[^。！？!?；;\n]{0,30}像临时盖上去的章")
DIALOGUE_RE = re.compile(r"[“\"]([^”\"]{2,160})[”\"]")
TEMPLATED_DIALOGUE_NAME_CALL_IN_TEXT_RE = re.compile(r"[“\"「][\u4e00-\u9fff]{2,4}[。！？!?]?[”\"」][^。！？!?；;\n]{0,12}叫[他她]")
TEMPLATED_DIALOGUE_MICRO_BEAT_RE = re.compile(
    r"(?:叫[他她]|停住|停下|抬眼|抬头|看了[^。！？!?；;\n]{0,8}一眼|"
    r"把[^。！？!?；;\n]{0,8}(?:停住|放下|推过来|推过去)|模板[^。！？!?；;\n]{0,12}推|"
    r"笔[^。！？!?；;\n]{0,12}(?:停|顿))"
)
TEMPLATED_DIALOGUE_PROCEDURE_RE = re.compile(
    r"(?:口径|字段|来源|管理建议|模板|确认|范围|流程|记录|说明|权限|演示|样本|审计|日志|保全|导出)"
)
PANIC_DIALOGUE_MARKERS = [
    "我交",
    "借我",
    "开个缝",
    "十倍还你",
    "我确认",
    "救",
    "开门！",
    "开门啊",
    "别过来",
    "别贴我门",
    "谁换我的钱",
]
DOCUMENT_CONTEXT_MARKERS = [
    "条款",
    "欠费单",
    "卡面",
    "显字",
    "写着",
    "便签",
    "备忘录",
    "提示",
    "白纸",
]
DOCUMENT_LEAD_RE = re.compile(r"(?:纸面|纸上|白纸|卡面|账单|欠费单|条款|系统提示|提示框)[^。！？!?；;\n]{0,24}[：:]")

DIGITS = {
    "零": 0,
    "一": 1,
    "二": 2,
    "两": 2,
    "三": 3,
    "四": 4,
    "五": 5,
    "六": 6,
    "七": 7,
    "八": 8,
    "九": 9,
}


def parse_chinese_number(text: str) -> int | None:
    if not text:
        return None
    if text == "十":
        return 10
    if "十" in text:
        left, _, right = text.partition("十")
        tens = DIGITS.get(left, 1 if left == "" else -1)
        ones = DIGITS.get(right, 0 if right == "" else -1)
        if tens < 0 or ones < 0:
            return None
        return tens * 10 + ones
    if len(text) == 1:
        return DIGITS.get(text)
    return None


def count_hanzi(text: str) -> int:
    return len(re.findall(r"[\u4e00-\u9fff]", text))


def strip_markdown_headings(raw: str) -> str:
    return "\n".join(line for line in raw.splitlines() if not line.lstrip().startswith("#"))


def split_paragraphs(raw: str) -> list[str]:
    body = strip_markdown_headings(raw)
    return [part.strip() for part in re.split(r"\n\s*\n+", body) if part.strip()]


def split_sentences(raw: str) -> list[str]:
    return [part.strip() for part in SENTENCE_SPLIT_RE.split(strip_markdown_headings(raw)) if count_hanzi(part) > 0]


def variance(values: list[int]) -> float:
    if not values:
        return 0.0
    mean = sum(values) / len(values)
    return sum((value - mean) ** 2 for value in values) / len(values)


def paragraph_start(paragraph: str) -> str:
    text = paragraph.strip().lstrip("“\"「『（(")
    for pronoun in ("他", "她", "我"):
        if text.startswith(pronoun):
            return pronoun
    chars: list[str] = []
    for char in text:
        if not "\u4e00" <= char <= "\u9fff":
            break
        chars.append(char)
        if len(chars) == 3:
            break
    return "".join(chars) if len(chars) >= 2 else ""


def style_diagnostics(raw: str) -> dict:
    body = strip_markdown_headings(raw)
    paras = split_paragraphs(raw)
    sents = split_sentences(raw)
    hanzi_count = max(count_hanzi(body), 1)
    para_lens = [count_hanzi(para) for para in paras]
    sent_lens = [count_hanzi(sent) for sent in sents]
    starts = [paragraph_start(para) for para in paras if paragraph_start(para)]
    repeated_starts = {key: count for key, count in Counter(starts).items() if count >= 3}
    return {
        "paragraph_count": len(paras),
        "sentence_count": len(sents),
        "paragraph_length_distribution": {
            "le_30": sum(1 for value in para_lens if value <= 30),
            "31_80": sum(1 for value in para_lens if 31 <= value <= 80),
            "81_160": sum(1 for value in para_lens if 81 <= value <= 160),
            "gt_160": sum(1 for value in para_lens if value > 160),
            "median": sorted(para_lens)[len(para_lens) // 2] if para_lens else 0,
        },
        "paragraph_start_repeats": repeated_starts,
        "simile_density_per_k": round(len(SIMILE_RE.findall(body)) / hanzi_count * 1000, 2),
        "micro_action_density_per_k": round(len(MICRO_ACTION_RE.findall(body)) / hanzi_count * 1000, 2),
        "isolated_sentence_paragraphs": sum(1 for para in paras if len(split_sentences(para)) == 1),
        "sentence_length_variance": round(variance(sent_lens), 2),
    }


def line_number(raw: str, index: int) -> int:
    return raw.count("\n", 0, index) + 1


def count_mismatch_issues(raw: str) -> list[dict]:
    issues: list[dict] = []
    for match in COUNT_WORD_RE.finditer(raw):
        expected = parse_chinese_number(match.group(1))
        if expected is None:
            continue
        payload = re.split(r"[”\"」』】]", match.group(2), maxsplit=1)[0]
        actual = count_hanzi(payload)
        if actual == 0 or actual == expected:
            continue
        issues.append(
            {
                "rule": "count_mismatch",
                "severity": "error",
                "line": line_number(raw, match.start()),
                "expected": expected,
                "actual": actual,
                "target": match.group(0).strip()[:80],
                "evidence": f"{match.group(1)}个字对应后文 {actual} 个汉字",
            }
        )
    for match in TWO_ITEMS_AS_TWO_CHARS_RE.finditer(raw):
        left, right = match.group(1), match.group(2)
        actual = count_hanzi(left + right)
        if actual == 2:
            continue
        issues.append(
            {
                "rule": "two_items_as_two_chars",
                "severity": "error",
                "line": line_number(raw, match.start()),
                "expected": 2,
                "actual": actual,
                "target": match.group(0).strip()[:80],
                "evidence": f"“{left}”和“{right}”不是两个汉字，应用“两行字/两个词/两样东西”等更准确说法",
            }
        )
    return issues


def awkward_style_issues(raw: str) -> list[dict]:
    issues: list[dict] = []
    seen: set[tuple[int, str]] = set()
    for match in FORCED_SIMILE_RE.finditer(raw):
        target = match.group(0).strip()
        key = (match.start(), target)
        if key in seen:
            continue
        seen.add(key)
        issues.append(
            {
                "rule": "forced_simile",
                "severity": "warning",
                "line": line_number(raw, match.start()),
                "target": target[:80],
                "evidence": "疑似硬贴物件明喻，优先改成角色能直接感到的声音、动作、温度或后果",
            }
        )
    for phrase in STOCK_SIMILE_WORDS:
        start = 0
        while True:
            index = raw.find(phrase, start)
            if index < 0:
                break
            key = (index, phrase)
            if key not in seen:
                seen.add(key)
                issues.append(
                    {
                        "rule": "stock_simile",
                        "severity": "warning",
                        "line": line_number(raw, index),
                        "target": phrase,
                        "evidence": "库存化明喻，容易显得刻意；若不承担新信息，建议删掉或改成具体动作",
                    }
                )
            start = index + len(phrase)
    return sorted(issues, key=lambda item: (item["line"], item["target"]))


def plain_memo_line(line: str) -> bool:
    stripped = line.strip()
    if not stripped or stripped.startswith("#"):
        return False
    if re.search(r"[。！？!?；;，,：:、（）()“”「」]", stripped):
        return False
    n = count_hanzi(stripped)
    return 4 <= n <= 18


def short_terms_line(line: str) -> bool:
    stripped = line.strip()
    if not stripped or stripped.startswith("#"):
        return False
    if re.search(r"[。！？!?；;（）()“”「」]", stripped):
        return False
    n = count_hanzi(stripped)
    return 2 <= n <= 24


def structured_note_run(lines: list[str]) -> bool:
    joined = "\n".join(lines)
    hits = sum(1 for marker in ["代缴", "确认", "身份证", "名字", "零钱", "不回", "不碰"] if marker in joined)
    if hits >= 3:
        return True
    return sum(1 for line in lines if line.startswith("不")) >= 2 and len(lines) >= 3


def surface_structure_issues(raw: str) -> list[dict]:
    issues: list[dict] = []
    lines = raw.splitlines()
    run: list[tuple[int, str]] = []

    def flush_run() -> bool:
        nonlocal run
        if len(run) >= 3 and structured_note_run([line for _, line in run]):
            issues.append(
                {
                    "rule": "structured_note_triplet",
                    "severity": "warning",
                    "line": run[0][0],
                    "limit": "便签/备忘录不能写成三条工整风控手册",
                    "actual": len(run),
                    "target": " / ".join(line for _, line in run[:3]),
                    "evidence": "受惊和赶时间时的便签应有划掉、补字、写半截、犹豫或回看物件，而不是三条平行清单",
                }
            )
            run = []
            return True
        run = []
        return False

    for index, line in enumerate(lines, start=1):
        stripped = line.strip()
        if plain_memo_line(stripped):
            run.append((index, stripped))
            continue
        if flush_run():
            break
    else:
        flush_run()

    for index, line in enumerate(lines):
        stripped = line.strip()
        if not short_terms_line(stripped):
            continue
        block = [(index + 1, stripped)]
        cursor = index + 1
        while cursor < len(lines) and short_terms_line(lines[cursor].strip()):
            block.append((cursor + 1, lines[cursor].strip()))
            cursor += 1
        if len(block) < 4:
            continue
        context = "\n".join(lines[max(0, index - 3):index])
        joined = " / ".join(line for _, line in block)
        if "黑卡" in context + joined and "交易" in joined and "账单" in joined:
            issues.append(
                {
                    "rule": "card_tos_block",
                    "severity": "warning",
                    "line": block[0][0],
                    "limit": "黑卡/系统提示不能完整 ToS 式列项",
                    "actual": len(block),
                    "target": joined[:100],
                    "evidence": "卡面/系统提示若完整列出仅限、须有、额度、账单日，会像产品条款；优先写残字、糊字、空白和读不全",
                }
            )
            break

    regex_checks = [
        ("empty_parallel_chant", EMPTY_PARALLEL_CHANT_RE, "童谣不能连续空对仗；让孩子背岔、卡壳、问大人或混入数字错位"),
        ("duplicate_dialogue_point", DUPLICATE_WIND_CONTROL_RE, "相邻对白重复同一骂点，像拼接残留；删一保一或让第二句产生新信息"),
        ("impossible_body_geometry", IMPOSSIBLE_SHADOW_GEOMETRY_RE, "身体/影子空间关系必须能成像，肩膀以下和腰以上不能互相打架"),
        ("impossible_line_of_sight", CAT_EYE_IMPOSSIBLE_READ_RE, "猫眼侧向视角不能读清背面小字；让字渗到门内、贴近猫眼或只写看不清"),
    ]
    for rule, regex, evidence in regex_checks:
        match = regex.search(raw)
        if not match:
            continue
        issues.append(
            {
                "rule": rule,
                "severity": "warning",
                "line": line_number(raw, match.start()),
                "target": match.group(0).strip()[:100],
                "evidence": evidence,
            }
        )

    de_fa = list(DE_FA_PHRASE_RE.finditer(raw))
    if len(de_fa) > 4:
        issues.append(
            {
                "rule": "de_fa_adjective_repetition",
                "severity": "warning",
                "line": line_number(raw, de_fa[0].start()),
                "limit": 4,
                "actual": len(de_fa),
                "target": " / ".join(match.group(0) for match in de_fa[:4]),
                "evidence": "“X得发Y”同型形容词复现过多，检测器和编辑都会抓；只留最有质感的一两处",
            }
        )

    return sorted(issues, key=lambda item: (item["line"], item["rule"]))


def causal_integrity_issues(raw: str) -> list[dict]:
    issues: list[dict] = []

    evidence_index = raw.find("你见过物业把人昵称改成承租物")
    if evidence_index >= 0:
        change_index = raw.find("群昵称从")
        if change_index < 0 or evidence_index < change_index:
            issues.append(
                {
                    "rule": "causal_evidence_order",
                    "severity": "warning",
                    "line": line_number(raw, evidence_index),
                    "target": "你见过物业把人昵称改成承租物",
                    "evidence": "角色只能指向已经出现的证据；先写昵称变化，再让人物据此反驳",
                }
            )

    report_index = raw.find("报后四位")
    if report_index >= 0:
        static_index = raw.find("电流声", report_index)
        change_index = raw.find("群昵称从", static_index if static_index >= 0 else report_index)
        if static_index >= 0 and change_index >= 0:
            gap = raw[static_index + len("电流声"):change_index]
            if any(marker in gap for marker in ["前夫", "买票", "售票口", "物业以前"]):
                issues.append(
                    {
                        "rule": "identity_effect_delayed",
                        "severity": "warning",
                        "line": line_number(raw, static_index),
                        "target": gap.strip()[:100],
                        "evidence": "报身份后的规则后果要紧贴演示，不能被闲聊或新支线冲散",
                    }
                )

    building_index = raw.find("5栋临时承租物")
    if "阴阳公寓3栋" in raw and building_index >= 0:
        issues.append(
            {
                "rule": "building_floor_mismatch",
                "severity": "warning",
                "line": line_number(raw, building_index),
                "target": "5栋临时承租物",
                "evidence": "楼栋和楼层称呼不能混用；3栋5楼不能写成5栋承租物",
            }
        )

    phone_index = raw.find("这通电话显然不是从基站过来的")
    if phone_index >= 0:
        voice_index = raw.find("你那边是不是也起雾", phone_index)
        if voice_index >= 0:
            gap = raw[phone_index:voice_index]
            if not any(marker in gap for marker in ["多找", "少找", "旧账", "核验", "验证", "确认身份", "只有本人"]):
                issues.append(
                    {
                        "rule": "anomalous_phone_unverified",
                        "severity": "warning",
                        "line": line_number(raw, phone_index),
                        "target": gap.strip()[:100],
                        "evidence": "非基站来电要先核验身份，再相信对面声口和信息",
                    }
                )

    for match in FORM_IMAGE_MISMATCH_RE.finditer(raw):
        issues.append(
            {
                "rule": "form_image_mismatch",
                "severity": "warning",
                "line": line_number(raw, match.start()),
                "target": match.group(0).strip()[:100],
                "evidence": "票据栏位和印章形状不能错配；栏位应写成拼出来、贴歪或格线不齐",
            }
        )

    card_index = raw.find("冥府黑卡")
    if card_index >= 0:
        span = raw[card_index:card_index + 320]
        if "须有" in span and "可确认" not in span:
            issues.append(
                {
                    "rule": "card_core_rule_overblurred",
                    "severity": "warning",
                    "line": line_number(raw, card_index),
                    "target": span.strip()[:100],
                    "evidence": "黑卡可以残缺留白，但核心可玩规则不能全糊掉，应保留少量可推理信息",
                }
            )

    return sorted(issues, key=lambda item: (item["line"], item["rule"]))


def semantic_clarity_issues(raw: str) -> list[dict]:
    issues: list[dict] = []
    for match in AMBIGUOUS_ORDER_RE.finditer(raw):
        sentence = match.group(0).strip()
        if ORDER_FOLLOWUP_RE.search(sentence):
            continue
        issues.append(
            {
                "rule": "dangling_order_word",
                "severity": "warning",
                "line": line_number(raw, match.start()),
                "target": sentence[:80],
                "evidence": "顺序词“先”缺少后续参照，读者会不清楚相对于什么先发生",
            }
        )
    for match in ABRUPT_STRONG_EVENT_RE.finditer(raw):
        sentence = match.group(0).strip()
        if EVENT_SETUP_RE.search(sentence):
            continue
        issues.append(
            {
                "rule": "abrupt_strong_event",
                "severity": "warning",
                "line": line_number(raw, match.start()),
                "target": sentence[:80],
                "evidence": "强事件不能只靠“忽然/突然”等副词切入，需补声源、视线、动作链或前因后果承接",
            }
        )
    for match in UNSUPPORTED_SPEECH_CLAIM_RE.finditer(raw):
        sentence = match.group(0).strip()
        issues.append(
            {
                "rule": "unsupported_speech_claim",
                "severity": "warning",
                "line": line_number(raw, match.start()),
                "target": sentence[:80],
                "evidence": "角色声称听见对方说话，前文必须有可被其听见的外放台词；否则改成灯光、脚步、门内动静等已铺垫证据",
            }
        )
    for match in OPAQUE_MEMO_SHORTHAND_RE.finditer(raw):
        sentence = match.group(0).strip()
        issues.append(
            {
                "rule": "opaque_memo_shorthand",
                "severity": "warning",
                "line": line_number(raw, match.start()),
                "target": sentence[:80],
                "evidence": "备忘录/纸条短句不能省掉关键对象，也不能压缩成提纲口吻；需写清“身份证号/姓名/门牌号/手机号”等具体指代，或写成“零钱暂时不碰”这类自然临时判断",
            }
        )
    for match in UNIT_NAME_APPOSITION_RE.finditer(raw):
        sentence = match.group(0).strip()
        issues.append(
            {
                "rule": "unit_name_apposition",
                "severity": "warning",
                "line": line_number(raw, match.start()),
                "target": sentence[:80],
                "evidence": "房号/门牌号接人名时需用“1703的蒋牧”等自然归属表达，避免提纲式黏连",
            }
        )
    for match in CLIPPED_HABIT_RE.finditer(raw):
        sentence = match.group(0).strip()
        if re.search(r"(?:经常|常常|老是|总是|总在|常在|在电梯里|在楼道里|那人|他)", sentence):
            continue
        issues.append(
            {
                "rule": "clipped_habit_sentence",
                "severity": "warning",
                "line": line_number(raw, match.start()),
                "target": sentence[:80],
                "evidence": "人物日常习惯句不能像提纲省略主语/频率/介词；应写成“经常在电梯里……”等自然叙述",
            }
        )
    for match in CLIPPED_SUMMARY_RE.finditer(raw):
        sentence = match.group(0).strip()
        issues.append(
            {
                "rule": "clipped_summary_phrase",
                "severity": "warning",
                "line": line_number(raw, match.start()),
                "target": sentence[:80],
                "evidence": "情节归纳句不能写成摘要腔或生硬口号；应写成“这通电话只让他确认两件事：……”等自然判断",
            }
        )
    for sentence in STATE_PILE_SPLIT_RE.split(raw):
        stripped = sentence.strip()
        if not stripped:
            continue
        comma_count = stripped.count("，") + stripped.count(",")
        state_hits = sum(stripped.count(marker) for marker in STATE_MARKERS)
        repeated_hai = stripped.count("还") >= 2
        if comma_count >= 2 and state_hits >= 4 or repeated_hai and comma_count >= 1:
            issues.append(
                {
                    "rule": "state_clause_pile",
                    "severity": "warning",
                    "line": line_number(raw, raw.find(sentence)),
                    "target": stripped[:80],
                    "evidence": "同一句堆叠多个状态说明或重复“还”，建议拆成短句并让动作/视线自然承接",
                }
            )
    return issues


def punctuation_emotion_issues(raw: str) -> list[dict]:
    issues: list[dict] = []
    for match in DIALOGUE_RE.finditer(raw):
        dialogue = match.group(1).strip()
        if not any(marker in dialogue for marker in PANIC_DIALOGUE_MARKERS):
            continue
        if dialogue.count("。") >= 2 and not re.search(r"[！？!?]", dialogue):
            issues.append(
                {
                    "rule": "panic_dialogue_flat_punctuation",
                    "severity": "warning",
                    "line": line_number(raw, match.start()),
                    "target": dialogue[:80],
                    "evidence": "恐慌/求救/催促台词不能全用句号切平；按真实语气补问号、叹号、逗号或动作停顿",
                }
            )
    for line_index, line in enumerate(raw.splitlines(), start=1):
        stripped = line.strip()
        if "：" not in stripped and ":" not in stripped:
            continue
        if not any(marker in stripped for marker in DOCUMENT_CONTEXT_MARKERS):
            continue
        if not DOCUMENT_LEAD_RE.search(stripped):
            continue
        parts = re.split(r"[：:]", stripped, maxsplit=1)
        tail = parts[1]
        if tail.count("。") >= 2 and "；" not in tail:
            issues.append(
                {
                    "rule": "document_clause_flat_punctuation",
                    "severity": "warning",
                    "line": line_index,
                    "target": stripped[:80],
                    "evidence": "条款/账单/提示类文本不应被句号硬切成机器清单；同层级项目优先用冒号和分号分层",
                }
            )
    return issues


def cadence_issues(raw: str) -> list[dict]:
    body = strip_markdown_headings(raw)
    hanzi_count = count_hanzi(body)
    limit_per_3k = max(1, (hanzi_count + 2999) // 3000)
    checks = [
        ("micro_action_overuse", MICRO_ACTION_RE, 3 * limit_per_3k, "微动作节拍复读；只保留承载道具、伏笔或关系的少数几处，其余换成对话、环境反应、留白或删除"),
        ("dramatic_negation_overuse", DRAMATIC_NEGATION_RE, 2, "戏剧性否定句式复读；删掉“没有立刻/没急着”等否定声明，直接写角色做了什么"),
        ("not_but_overuse", NOT_BUT_RE, 1, "“不是A而是B”每章最多保留 1 处，其余改普通陈述或动作后果"),
        ("precise_measure_overuse", PRECISE_MEASURE_RE, 2, "一指/半寸/两寸等精确量词只留给真正需要精确的规则或伏笔，其余改模糊感知"),
        ("patch_phrase_overuse", PATCH_PHRASE_RE, 2, "补丁替代表达复读；停了一拍/停了停等修补痕迹本身也要不均匀"),
        ("minor_mistake_overuse", MINOR_MISTAKE_RE, 2, "刻意安排的小失误每章不超过 2 处；超过后会变成新模板"),
        ("vague_quantifier_overuse", VAGUE_QUANTIFIER_RE, 4, "半、一点、几分等虚量词同类每章不超过 4；半袋米这类具体物件不计"),
    ]
    issues: list[dict] = []
    for rule, regex, limit, evidence in checks:
        matches = list(regex.finditer(body))
        if len(matches) <= limit:
            continue
        first_target = matches[0].group(0).strip()
        first_index = raw.find(first_target)
        issues.append(
            {
                "rule": rule,
                "severity": "warning",
                "line": line_number(raw, first_index if first_index >= 0 else matches[0].start()),
                "limit": limit,
                "actual": len(matches),
                "target": " / ".join(match.group(0).strip()[:40] for match in matches[:3]),
                "evidence": evidence,
            }
        )

    paras = split_paragraphs(raw)
    previous = ""
    run = 0
    for para in paras:
        start = paragraph_start(para)
        if start and start == previous:
            run += 1
        else:
            previous = start
            run = 1 if start else 0
        if run >= 3:
            issues.append(
                {
                    "rule": "paragraph_start_repetition",
                    "severity": "warning",
                    "line": line_number(raw, raw.find(para)),
                    "limit": "连续段首同主语 < 3",
                    "actual": run,
                    "target": start,
                    "evidence": "连续 3 段以上同主语起手；换环境、对话、宾语前置或旁观者动作进入",
                }
            )
            break
    isolated = [para for para in paras if len(split_sentences(para)) == 1]
    if len(isolated) > 4:
        issues.append(
            {
                "rule": "isolated_sentence_overuse",
                "severity": "warning",
                "line": line_number(raw, raw.find(isolated[0])),
                "limit": 4,
                "actual": len(isolated),
                "target": " / ".join(para[:40] for para in isolated[:3]),
                "evidence": "单行孤句是强调符号，每章不超过 4；过量会形成戏剧段模板",
            }
        )
    quip_counts: Counter[str] = Counter()
    quip_examples: dict[str, list[str]] = {}
    for match in SUPPORTING_QUIP_RE.finditer(raw):
        name = match.group(1)
        quip_counts[name] += 1
        quip_examples.setdefault(name, [])
        if len(quip_examples[name]) < 3:
            quip_examples[name].append(match.group(0).strip()[:40])
    for name, count in quip_counts.items():
        if count <= 3:
            continue
        issues.append(
            {
                "rule": "supporting_quip_overuse",
                "severity": "warning",
                "line": line_number(raw, raw.find(quip_examples[name][0])),
                "limit": 3,
                "actual": count,
                "target": f"{name}: " + " / ".join(quip_examples[name]),
                "evidence": "同一配角吐槽每章不超过 3；重要节点至少留一句无人接的话，不要每句都被接住",
            }
        )
        break
    object_matches = list(OBJECT_RESPONSE_RE.finditer(body))
    if len(object_matches) > 4:
        first_target = object_matches[0].group(0).strip()
        first_index = raw.find(first_target)
        issues.append(
            {
                "rule": "object_response_overuse",
                "severity": "warning",
                "line": line_number(raw, first_index if first_index >= 0 else object_matches[0].start()),
                "limit": 4,
                "actual": len(object_matches),
                "target": " / ".join(match.group(0).strip()[:40] for match in object_matches[:3]),
                "evidence": "世界对主角言行的物理确认每章设上限；屏幕/纸面/门牌/灯光不要每句都立刻回应",
            }
        )
    if len(object_matches) >= 3 and (not OBJECT_RESPONSE_DELAY_RE.search(body) or not OBJECT_RESPONSE_ABSENCE_RE.search(body)):
        missing = []
        if not OBJECT_RESPONSE_DELAY_RE.search(body):
            missing.append("延迟")
        if not OBJECT_RESPONSE_ABSENCE_RE.search(body):
            missing.append("缺席/静默")
        issues.append(
            {
                "rule": "object_response_rhythm_flat",
                "severity": "warning",
                "line": line_number(raw, raw.find(object_matches[0].group(0).strip()) if object_matches else 0),
                "limit": "至少一次延迟、一次缺席",
                "actual": len(object_matches),
                "target": "、".join(missing),
                "evidence": "物件回应不能等距确认主角言行；重话可以落在静默上，偶尔抢拍，但必须有延迟和缺席",
            }
        )
    aphorism_total = 0
    aphorism_run = 0
    aphorism_examples: list[str] = []
    for match in DIALOGUE_RE.finditer(raw):
        quote = match.group(1)
        if not APHORISTIC_DIALOGUE_RE.search(quote):
            aphorism_run = 0
            continue
        aphorism_total += 1
        aphorism_run += 1
        if len(aphorism_examples) < 3:
            aphorism_examples.append(quote[:40])
        if aphorism_run > 3:
            issues.append(
                {
                    "rule": "dialogue_aphorism_overuse",
                    "severity": "warning",
                    "line": line_number(raw, match.start()),
                    "limit": "连续警句式应答 <= 3",
                    "actual": aphorism_run,
                    "target": " / ".join(aphorism_examples),
                    "evidence": "金句限流扩到主角；双人对手戏要检查两人语域是否可区分，连续警句式应答不超过 3 回合",
                }
            )
            break
    else:
        if aphorism_total > 4:
            issues.append(
                {
                    "rule": "dialogue_aphorism_overuse",
                    "severity": "warning",
                    "line": line_number(raw, raw.find(aphorism_examples[0]) if aphorism_examples else 0),
                    "limit": 4,
                    "actual": aphorism_total,
                    "target": " / ".join(aphorism_examples),
                    "evidence": "主角和配角都要限流金句；超过后会让声口趋同",
                }
            )
    serial_issue = serial_device_issue(raw)
    if serial_issue:
        issues.append(serial_issue)
    templated_chains: list[str] = []
    index = 0
    while index < len(paras):
        window_paras = paras[index : index + 4]
        window = "\n".join(window_paras)
        quotes = [match.group(1).strip() for match in DIALOGUE_RE.finditer(window)]
        if (
            len(quotes) >= 3
            and TEMPLATED_DIALOGUE_NAME_CALL_IN_TEXT_RE.search(window)
            and TEMPLATED_DIALOGUE_MICRO_BEAT_RE.search(window)
            and TEMPLATED_DIALOGUE_PROCEDURE_RE.search(window)
        ):
            templated_chains.append(window)
            index += 4
            continue
        index += 1
    if templated_chains:
        first_target = templated_chains[0].strip()
        first_line = first_target.splitlines()[0].strip()
        first_index = raw.find(first_line)
        issues.append(
            {
                "rule": "templated_dialogue_chain",
                "severity": "warning",
                "line": line_number(raw, first_index if first_index >= 0 else 0),
                "limit": 0,
                "actual": len(templated_chains),
                "target": " / ".join(chain.strip()[:70] for chain in templated_chains[:2]),
                "evidence": "点名、停笔/抬眼、补口径/查字段、第三人追问的三拍对白链命中即改；它会让职场戏像流程脚本",
            }
        )
    return issues


def split_chapters_for_device_scan(raw: str) -> list[str]:
    matches = list(CHAPTER_HEADING_RE.finditer(raw))
    if not matches:
        return [raw]
    chapters: list[str] = []
    for index, match in enumerate(matches):
        start = match.end()
        end = matches[index + 1].start() if index + 1 < len(matches) else len(raw)
        chapter = raw[start:end].strip()
        if chapter:
            chapters.append(chapter)
    return chapters


def classify_serial_device(text: str) -> str:
    target = text.strip()
    if not target:
        return ""
    if re.search(r"(?:屏幕|手机|提示框|系统|弹窗|监控)[^。！？!?；;\n]{0,30}(?:显示|显字|亮|跳出|弹出|多出|刷新)", target):
        return "屏幕显字"
    if re.search(r"(?:白纸|纸面|纸条|账单|欠费单|合同|条款|便签|卡面)[^。！？!?；;\n]{0,30}(?:显示|显字|多出|写着|浮出|变成|改成)", target):
        return "纸面显字"
    if re.search(r"(?:话没说完|没说完|说到一半|[—…]\s*$|[“「][^”」]{2,60}$)", target):
        return "对话截断"
    if re.search(r"(?:门牌|灯|灯管|门锁|黑卡|账单|欠费单|纸条|价签|货架)[^。！？!?；;\n]{0,30}(?:亮|暗|闪|响|震|跳|显|浮|多出|变成|改成|停|裂|动|滚|掉|滑)", target):
        return "凶兆物微动"
    return ""


def chapter_device(chapter: str, edge: str) -> str:
    paras = split_paragraphs(chapter)
    if not paras:
        return ""
    target = paras[0] if edge == "opening" else paras[-1]
    return classify_serial_device(target)


def serial_device_issue(raw: str) -> dict | None:
    chapters = split_chapters_for_device_scan(raw)
    if len(chapters) < 3:
        return None
    for edge in ["opening", "ending"]:
        previous = ""
        run = 0
        for chapter in chapters:
            device = chapter_device(chapter, edge)
            if not device:
                previous = ""
                run = 0
                continue
            if device == previous:
                run += 1
            else:
                previous = device
                run = 1
            if run > 2:
                return {
                    "rule": "serial_device_repetition",
                    "severity": "warning",
                    "line": line_number(raw, raw.find(chapter[:20])),
                    "limit": "同一开头/结尾装置连用 <= 2 章",
                    "actual": run,
                    "target": f"{edge}: {device}",
                    "evidence": "每章登记开头和结尾装置类型；同一装置连续使用超过两章会变成连载模板，例如章尾显字 3/3",
                }
    return None


def lint_text(raw: str) -> list[dict]:
    return count_mismatch_issues(raw) + causal_integrity_issues(raw)


def lint_file(path: Path) -> dict:
    raw = path.read_text(encoding="utf-8")
    return {
        "文件": str(path),
        "style_diagnostics": style_diagnostics(raw),
        "content_logic_issues": count_mismatch_issues(raw) + causal_integrity_issues(raw),
        "awkward_style_issues": awkward_style_issues(raw),
        "semantic_clarity_issues": semantic_clarity_issues(raw),
        "punctuation_emotion_issues": punctuation_emotion_issues(raw),
        "surface_structure_issues": surface_structure_issues(raw),
        "cadence_issues": cadence_issues(raw),
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("file", type=Path)
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args()

    report = lint_file(args.file)
    if args.json:
        print(json.dumps(report, ensure_ascii=False, indent=2))
    elif (
        not report["content_logic_issues"]
        and not report["awkward_style_issues"]
        and not report["semantic_clarity_issues"]
        and not report["punctuation_emotion_issues"]
        and not report["surface_structure_issues"]
        and not report["cadence_issues"]
    ):
        print_style_diagnostics(report["style_diagnostics"])
        print("内容硬检通过：未发现数词事实错误、因果链错位、别扭库存明喻、顺序词悬空、突发转场、证据声明悬空、备忘录缩写、提纲式省略、状态堆叠、标点情绪层级或表层结构感问题。")
    else:
        print_style_diagnostics(report["style_diagnostics"])
        print("内容硬检发现问题：")
        for item in report["content_logic_issues"]:
            print(f"- line {item['line']}: {item['evidence']} | {item['target']}")
        for item in report["awkward_style_issues"]:
            print(f"- line {item['line']}: {item['evidence']} | {item['target']}")
        for item in report["semantic_clarity_issues"]:
            print(f"- line {item['line']}: {item['evidence']} | {item['target']}")
        for item in report["punctuation_emotion_issues"]:
            print(f"- line {item['line']}: {item['evidence']} | {item['target']}")
        for item in report["surface_structure_issues"]:
            print(f"- line {item['line']}: {item['evidence']} | {item['target']}")
        for item in report["cadence_issues"]:
            print(f"- line {item['line']}: {item['evidence']} | {item['target']}")
    return 1 if report["content_logic_issues"] else 0


def print_style_diagnostics(stats: dict) -> None:
    dist = stats["paragraph_length_distribution"]
    print("全局体检：")
    print(
        f"- 段落 {stats['paragraph_count']}，句子 {stats['sentence_count']}；"
        f"段长分布 ≤30:{dist['le_30']} / 31-80:{dist['31_80']} / 81-160:{dist['81_160']} / >160:{dist['gt_160']}，中位 {dist['median']}"
    )
    print(
        f"- 段首重复 {stats['paragraph_start_repeats'] or '无'}；"
        f"比喻密度 {stats['simile_density_per_k']}/千字；微动作密度 {stats['micro_action_density_per_k']}/千字；"
        f"孤句段 {stats['isolated_sentence_paragraphs']}；句长方差 {stats['sentence_length_variance']}"
    )


if __name__ == "__main__":
    raise SystemExit(main())
