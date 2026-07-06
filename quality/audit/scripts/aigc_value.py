#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
aigc_value.py — 本地自研 AIGC 值检测器

这是发布流程用的确定性启发式检测器，不调用外部服务。它输出 0-1 的
`自研AIGC值` 和百分比，用来给短篇生产流程做硬闸门。

用法：
  python3 aigc_value.py <文本文件>
  python3 aigc_value.py <文本文件> --json
  python3 aigc_value.py <文本文件> --target 5

重要：这是概率性文风信号，不是事实判决；但在本地交付流程里可作为
“需要回改”的工程门控。
"""
from __future__ import annotations

import argparse
import json
import math
import re
from collections import Counter
from pathlib import Path


ENGINE = "codex-local-aigc-v3"
DIMENSION_WEIGHTS = {
    "burstiness": 0.30,
    "perplexity_proxy": 0.25,
    "structure_fingerprint": 0.25,
    "cross_paragraph_consistency": 0.20,
}
FINAL_BLEND_WEIGHTS = {
    "zhuque_four_dimensions": 0.10,
    "latest_detector_proxy": 0.85,
    "legacy_heuristic": 0.05,
}
LATEST_PROXY_WEIGHTS = {
    "probability_curvature_proxy": 0.31,
    "weak_lm_uniformity": 0.22,
    "local_entropy_uniformity": 0.20,
    "stylometry_readability": 0.07,
    "semantic_smoothing": 0.05,
    "layout_humanizer_fingerprint": 0.05,
    "content_integrity": 0.10,
    "zhuque_segment_proxy": 0.00,
}

CLICHES = {
    "时间锚点": ["那一刻", "那一瞬", "就在这时", "刹那间", "一瞬间", "顷刻间"],
    "微表情": ["嘴角", "眼眸", "眼角", "眼底", "眸子", "勾起", "扬起一抹", "抿了抿"],
    "情绪命名": ["百感交集", "五味杂陈", "一种说不出", "复杂的情绪", "莫名的", "难以言喻", "心如刀绞", "痛不欲生"],
    "金句升华": ["原来", "或许", "也许就是", "所谓", "有些", "这就是", "人生就是", "真正的选择", "真正的答案", "最终的选择", "最终答案"],
    "陈词意象": ["月光如水", "时间仿佛凝固", "命运的齿轮", "如潮水般", "似乎", "仿佛", "宛如", "犹如"],
    "解释归纳": ["这让他意识到", "这让她意识到", "终于明白", "不再是", "而是", "不仅仅是", "更是", "这意味着", "换句话说"],
    "平滑转场": ["然而", "与此同时", "紧接着", "随后", "片刻后", "很快", "没过多久"],
    "工程泄漏": ["本章", "细纲", "大纲", "设定", "爽点", "情绪爆发", "关系升级", "伏笔回收", "节奏点"],
}

CONCRETE_HINTS = [
    "微信", "手机", "电梯", "地铁", "外卖", "快递", "钥匙", "门禁", "发票", "截图",
    "合同", "医院", "药", "咖啡", "便利店", "停车场", "工牌", "雨伞", "纸巾",
    "房间", "圆桌", "桌子", "餐桌", "椅子", "座钟", "钟表", "面具", "墙", "地板",
    "天花板", "白纸", "纸", "笔", "血迹", "尸体", "雨", "雨水", "泥", "门",
    "家门", "灯", "灯光", "剧院", "舞台", "射灯", "诊所", "口袋", "衣服", "戏袍",
    "黑发", "眼睛",
]

ACTION_HINTS = [
    "拿", "放", "推", "拉", "拽", "摁", "敲", "砸", "递", "接", "翻", "撕", "扔",
    "踢", "踩", "躲", "退", "停", "看", "笑", "哭", "骂", "问", "答", "咳", "抖",
]

SENSORY_HINTS = [
    "冷", "热", "烫", "疼", "痒", "酸", "涩", "苦", "甜", "腥", "臭", "响", "哑",
    "湿", "黏", "硬", "软", "亮", "暗", "刺", "闷", "吵",
]

EMOTION_WORDS = [
    "紧张", "愤怒", "悲伤", "难过", "委屈", "害怕", "恐惧", "震惊", "惊讶", "复杂",
    "痛苦", "绝望", "崩溃", "开心", "喜悦", "温柔", "释然", "怅然", "茫然",
]

ABSTRACT_HINTS = [
    "意义", "命运", "人生", "灵魂", "内心", "情绪", "感觉", "关系", "成长", "救赎",
    "羁绊", "真相", "现实", "未来", "过去", "世界", "规则", "答案",
]

TECHNICAL_HUMAN_HINTS = [
    "系统", "算法", "模型", "实验", "数据", "检测", "识别", "跟踪", "控制", "优化",
    "实时", "计算", "阈值", "方差", "协方差", "序列", "图像", "像素", "轨迹", "预测",
    "摄像机", "相机", "目标", "运动", "干扰", "环境", "信噪比", "闭环", "响应", "延迟",
    "数据库", "平台", "处理", "分析", "PTZ", "SNR", "PC", "database", "tracking",
    "model", "control", "algorithm", "camera", "image", "sequence",
]

SOUND_NOISE_RE = re.compile(r"(?:[嗒咯叩沙咔啪滴哒哗啦停]{1,10}[，、,。；;]?){6,}")
CJK_RUN_RE = re.compile(r"[\u4e00-\u9fff]{24,}")
RARE_TERM_SOUP_RE = re.compile(r"(?:魑魅魍魉|饕餮|螭吻|赑屃|狴犴|蒲牢|睚眦|狻猊|椒图|囚牛|貔貅|獬豸|鸱吻|蚣蝮|趴蝮){4,}")
GRAMMAR_CHARS = set("的一是不了在有和人这那中为上个我你他她它以要时来用们到于就对成会可也能下过说得着里把被给但并而或及其都还只又先再才没无")
RARE_SOUP_CHARS = set("魑魅魍魉饕餮螭赑屃狴犴蒲牢睚眦狻猊椒图囚牛貔貅獬豸鸱吻蚣蝮趴")


def strip_markdown_titles(text: str) -> str:
    return "\n".join(line for line in text.splitlines() if not line.lstrip().startswith("#"))


def hanzi(text: str) -> list[str]:
    return re.findall(r"[\u4e00-\u9fff]", text)


def split_sentences(text: str) -> list[str]:
    return [s.strip() for s in re.split(r"[。！？!?；;\n]", text) if re.search(r"[\u4e00-\u9fff]", s)]


def coefficient_of_variation(values: list[int]) -> float:
    if not values:
        return 0.0
    mean = sum(values) / len(values)
    if mean == 0:
        return 0.0
    var = sum((value - mean) ** 2 for value in values) / len(values)
    return (var ** 0.5) / mean


def stddev(values: list[float] | list[int]) -> float:
    if not values:
        return 0.0
    mean = sum(values) / len(values)
    var = sum((float(value) - mean) ** 2 for value in values) / len(values)
    return var ** 0.5


def clamp(value: float, low: float = 0.0, high: float = 1.0) -> float:
    return min(high, max(low, value))


def char_entropy(chars: list[str]) -> float:
    if not chars:
        return 0.0
    counts = Counter(chars)
    total = len(chars)
    entropy = 0.0
    for count in counts.values():
        p = count / total
        entropy -= p * math.log2(p)
    return entropy


def density(count: int, n_hanzi: int) -> float:
    return round(count / max(n_hanzi, 1) * 1000, 2)


def count_markers(text: str, markers: list[str]) -> int:
    return sum(text.count(item) for item in markers)


def punctuation_kind_count(text: str) -> int:
    marks = ["，", "。", "？", "！", "：", "；", "、", "“", "”", "「", "」", "——", "……"]
    return sum(1 for mark in marks if mark in text)


def semantic_noise_matches(text: str) -> list[dict]:
    rows = []
    used_spans: list[tuple[int, int]] = []
    for match in RARE_TERM_SOUP_RE.finditer(text):
        run = match.group(0)
        chars = hanzi(run)
        if not chars:
            continue
        used_spans.append(match.span())
        rows.append(
            {
                "text": run,
                "hanzi": len(chars),
                "unique_ratio": round(len(set(chars)) / len(chars), 3),
                "grammar_ratio": 0.0,
                "top_char_ratio": round(Counter(chars).most_common(1)[0][1] / len(chars), 3),
                "rare_char_ratio": 1.0,
                "noise_type": "rare_term_soup",
            }
        )
    for match in CJK_RUN_RE.finditer(text):
        if any(match.start() >= start and match.end() <= end for start, end in used_spans):
            continue
        run = match.group(0)
        chars = hanzi(run)
        if not chars:
            continue
        unique_ratio = len(set(chars)) / len(chars)
        grammar_ratio = sum(1 for char in chars if char in GRAMMAR_CHARS) / len(chars)
        top_ratio = Counter(chars).most_common(1)[0][1] / len(chars)
        rare_ratio = sum(1 for char in chars if char in RARE_SOUP_CHARS) / len(chars)
        is_repetitive_soup = unique_ratio <= 0.24 and grammar_ratio <= 0.10 and (len(chars) >= 90 or top_ratio >= 0.09)
        is_rare_term_soup = rare_ratio >= 0.42 and grammar_ratio <= 0.12 and len(chars) >= 18
        if is_repetitive_soup or is_rare_term_soup:
            rows.append(
                {
                    "text": run,
                    "hanzi": len(chars),
                    "unique_ratio": round(unique_ratio, 3),
                    "grammar_ratio": round(grammar_ratio, 3),
                    "top_char_ratio": round(top_ratio, 3),
                    "rare_char_ratio": round(rare_ratio, 3),
                    "noise_type": "rare_char_soup" if is_rare_term_soup else "repetitive_char_soup",
                }
            )
    return rows


def detector_noise_stats(text: str) -> dict:
    sound_matches = [match.group(0) for match in SOUND_NOISE_RE.finditer(text)]
    sound_chars = sum(len(hanzi(match)) for match in sound_matches)
    semantic_matches = semantic_noise_matches(text)
    semantic_chars = sum(item["hanzi"] for item in semantic_matches)
    total = max(len(hanzi(text)), 1)
    return {
        "sound_noise_runs": len(sound_matches),
        "sound_noise_hanzi": sound_chars,
        "sound_noise_ratio": round(sound_chars / total, 4),
        "semantic_noise_runs": len(semantic_matches),
        "semantic_noise_hanzi": semantic_chars,
        "semantic_noise_ratio": round(semantic_chars / total, 4),
        "semantic_noise_examples": [
            {
                "text": item["text"][:80] + ("..." if len(item["text"]) > 80 else ""),
                "hanzi": item["hanzi"],
                "unique_ratio": item["unique_ratio"],
                "grammar_ratio": item["grammar_ratio"],
                "top_char_ratio": item["top_char_ratio"],
                "rare_char_ratio": item.get("rare_char_ratio", 0),
                "noise_type": item.get("noise_type", "semantic_noise"),
            }
            for item in semantic_matches[:3]
        ],
    }


def normalize_detector_curve_text(text: str) -> str:
    # Repetitive onomatopoeia can artificially inflate local entropy/TTR curves.
    # Keep it for ordinary style stats, but neutralize it for detector-curve proxies.
    text = SOUND_NOISE_RE.sub("声响。", text)
    for item in semantic_noise_matches(text):
        text = text.replace(item["text"], "无效乱码。")
    return text


def cliche_densities(text: str, n_hanzi: int) -> tuple[dict[str, float], float]:
    per_k = {
        name: density(sum(text.count(word) for word in words), n_hanzi)
        for name, words in CLICHES.items()
    }
    return per_k, round(sum(per_k.values()), 2)


def repeated_ngrams(chars: list[str], n: int) -> list[tuple[str, int]]:
    seq = "".join(chars)
    if len(seq) < n:
        return []
    counts = Counter(seq[i:i + n] for i in range(len(seq) - n + 1))
    return sorted(((gram, count) for gram, count in counts.items() if count >= 2), key=lambda item: -item[1])


def paragraphs(text: str) -> list[str]:
    return [p.strip() for p in re.split(r"\n\s*\n+", text) if p.strip() and not p.lstrip().startswith("#")]


def duplicate_paragraph_report(text: str) -> dict:
    paras = paragraphs(text)
    counts = Counter(paras)
    exact = [(p, count) for p, count in counts.items() if count > 1 and len(p) >= 20]

    offsets = []
    pos = 0
    for para in paras:
        offsets.append(pos)
        pos += len(para) + 2

    def norm(para: str) -> str:
        return re.sub(r"[^一-龥]", "", para)[:80]

    seen = {}
    similar = []
    for index, para in enumerate(paras):
        key = norm(para)
        if len(key) < 15:
            continue
        if key in seen:
            prev = seen[key]
            if abs(offsets[index] - offsets[prev]) >= 100:
                similar.append((prev, index, para))
        else:
            seen[key] = index

    sents = [s.strip() for s in re.split(r"[。！？!?\n]", text) if len(s.strip()) > 20]
    sent_counts = Counter(sents)
    sent_dups = [(sent, count) for sent, count in sent_counts.items() if count > 1]

    return {
        "paragraph_count": len(paras),
        "exact_count": len(exact),
        "similar_count": len(similar),
        "sentence_duplicate_count": len(sent_dups),
        "examples": [item[0][:80] for item in exact[:3]] + [item[2][:80] for item in similar[:3]],
    }


def label_for(percent: float) -> str:
    if percent >= 80:
        return "极高"
    if percent >= 60:
        return "高"
    if percent >= 35:
        return "中"
    if percent >= 15:
        return "低-中"
    return "低"


def confidence_for(n_hanzi: int, reason_count: int) -> str:
    if n_hanzi < 300:
        return "低"
    if n_hanzi >= 1200 and reason_count >= 3:
        return "高"
    return "中"


def add_reason(reasons: list[dict], score: float, feature: str, reason: str) -> float:
    if score <= 0:
        return 0.0
    reasons.append({"分值": round(score, 3), "特征": feature, "原因": reason})
    return score


def risk_item(name: str, score: float, evidence: str) -> dict:
    return {"name": name, "score": round(clamp(score) * 100, 2), "evidence": evidence}


def cosine_similarity(left: list[float], right: list[float]) -> float:
    denom = (sum(v * v for v in left) ** 0.5) * (sum(v * v for v in right) ** 0.5)
    if denom == 0:
        return 0.0
    return sum(a * b for a, b in zip(left, right)) / denom


def vector_cv(values: list[float]) -> float:
    return coefficient_of_variation([int(v * 1000) for v in values if v > 0])


def paragraph_feature_rows(paras: list[str]) -> list[dict]:
    rows = []
    for para in paras:
        p_chars = hanzi(para)
        p_sents = split_sentences(para)
        p_lens = [len(hanzi(sent)) for sent in p_sents]
        p_punct_period = max(para.count("。"), 1)
        p_per_k, p_total_cliche = cliche_densities(para, len(p_chars))
        rows.append(
            {
                "hanzi": len(p_chars),
                "sentences": len(p_sents),
                "avg_sentence_len": sum(p_lens) / max(len(p_lens), 1),
                "sentence_cv": coefficient_of_variation(p_lens),
                "comma_period_ratio": para.count("，") / p_punct_period,
                "dialogue": 1.0 if ("“" in para or "\"" in para) else 0.0,
                "cliche_total_per_k": p_total_cliche,
                "cliche_per_k": p_per_k,
                "concrete_density": density(count_markers(para, CONCRETE_HINTS) + len(re.findall(r"\d", para)), len(p_chars)),
                "action_density": density(count_markers(para, ACTION_HINTS), len(p_chars)),
                "sensory_density": density(count_markers(para, SENSORY_HINTS), len(p_chars)),
                "emotion_density": density(count_markers(para, EMOTION_WORDS), len(p_chars)),
                "abstract_density": density(count_markers(para, ABSTRACT_HINTS), len(p_chars)),
            }
        )
    return rows


def paragraph_fragmentation_stats(paras: list[str]) -> dict:
    lengths = [len(hanzi(para)) for para in paras if len(hanzi(para)) > 0]
    if not lengths:
        return {
            "paragraph_count": 0,
            "avg_hanzi_per_paragraph": 0,
            "median_hanzi_per_paragraph": 0,
            "short_paragraph_ratio": 0,
            "very_short_paragraph_ratio": 0,
            "single_sentence_paragraph_ratio": 0,
            "bracket_line_ratio": 0,
        }
    sorted_lengths = sorted(lengths)
    mid = len(sorted_lengths) // 2
    if len(sorted_lengths) % 2:
        median = sorted_lengths[mid]
    else:
        median = (sorted_lengths[mid - 1] + sorted_lengths[mid]) / 2
    sentence_counts = [len(split_sentences(para)) for para in paras if len(hanzi(para)) > 0]
    bracket_lines = [para for para in paras if para.strip().startswith("【") and para.strip().endswith("】")]
    return {
        "paragraph_count": len(lengths),
        "avg_hanzi_per_paragraph": round(sum(lengths) / len(lengths), 2),
        "median_hanzi_per_paragraph": round(median, 2),
        "short_paragraph_ratio": round(sum(1 for value in lengths if value <= 12) / len(lengths), 3),
        "very_short_paragraph_ratio": round(sum(1 for value in lengths if value <= 6) / len(lengths), 3),
        "single_sentence_paragraph_ratio": round(sum(1 for value in sentence_counts if value == 1) / max(len(sentence_counts), 1), 3),
        "bracket_line_ratio": round(len(bracket_lines) / len(lengths), 3),
    }


def paragraph_similarity_stats(rows: list[dict]) -> dict:
    vectors = []
    for row in rows:
        vectors.append([
            min(row["hanzi"] / 260, 2.0),
            min(row["avg_sentence_len"] / 28, 2.0),
            min(row["sentence_cv"], 1.8),
            min(row["comma_period_ratio"] / 3, 2.0),
            row["dialogue"],
            min(row["cliche_total_per_k"] / 6, 2.0),
            min(row["concrete_density"] / 5, 2.0),
            min(row["action_density"] / 24, 2.0),
            min(row["sensory_density"] / 12, 2.0),
            min(row["emotion_density"] / 8, 2.0),
            min(row["abstract_density"] / 8, 2.0),
        ])

    adjacent = [cosine_similarity(vectors[i - 1], vectors[i]) for i in range(1, len(vectors))]
    if not adjacent:
        return {
            "adjacent_similarity_avg": 0,
            "adjacent_similarity_std": 0,
            "adjacent_similarity_high_ratio": 0,
        }
    high_ratio = sum(1 for value in adjacent if value >= 0.965) / len(adjacent)
    return {
        "adjacent_similarity_avg": round(sum(adjacent) / len(adjacent), 3),
        "adjacent_similarity_std": round(stddev(adjacent), 3),
        "adjacent_similarity_high_ratio": round(high_ratio, 3),
    }


def local_surprisal_stats(sents: list[str]) -> dict:
    sent_chars = [hanzi(sent) for sent in sents]
    sent_chars = [chars for chars in sent_chars if len(chars) >= 4]
    all_chars = [char for chars in sent_chars for char in chars]
    if len(all_chars) < 200 or len(sent_chars) < 8:
        return {
            "sentence_count": len(sent_chars),
            "unigram_surprisal_avg": 0,
            "unigram_surprisal_cv": 0,
            "bigram_surprisal_avg": 0,
            "bigram_surprisal_cv": 0,
        }

    unigram_counts = Counter(all_chars)
    bigram_counts = Counter()
    prev_counts = Counter()
    for chars in sent_chars:
        for left, right in zip(chars, chars[1:]):
            bigram_counts[(left, right)] += 1
            prev_counts[left] += 1
    vocab = max(len(unigram_counts), 1)
    total = len(all_chars)

    unigram_values = []
    bigram_values = []
    for chars in sent_chars:
        unigram_values.append(sum(-math.log2((unigram_counts[char] + 1) / (total + vocab)) for char in chars) / len(chars))
        if len(chars) >= 2:
            bigram_values.append(
                sum(-math.log2((bigram_counts[(left, right)] + 1) / (prev_counts[left] + vocab)) for left, right in zip(chars, chars[1:]))
                / (len(chars) - 1)
            )

    return {
        "sentence_count": len(sent_chars),
        "unigram_surprisal_avg": round(sum(unigram_values) / len(unigram_values), 3),
        "unigram_surprisal_cv": round(coefficient_of_variation([int(v * 1000) for v in unigram_values]), 3),
        "bigram_surprisal_avg": round(sum(bigram_values) / max(len(bigram_values), 1), 3),
        "bigram_surprisal_cv": round(coefficient_of_variation([int(v * 1000) for v in bigram_values]), 3),
    }


def window_entropy_stats(chars: list[str], window: int = 180, step: int = 90) -> dict:
    if len(chars) < window * 2:
        return {
            "window_count": 0,
            "normalized_entropy_avg": 0,
            "normalized_entropy_std": 0,
            "normalized_entropy_cv": 0,
            "ttr_avg": 0,
            "ttr_cv": 0,
        }
    entropy_values = []
    ttr_values = []
    for start in range(0, len(chars) - window + 1, step):
        chunk = chars[start:start + window]
        unique = len(set(chunk))
        max_entropy = math.log2(max(unique, 2))
        entropy_values.append(char_entropy(chunk) / max_entropy if max_entropy else 0)
        ttr_values.append(unique / window)
    return {
        "window_count": len(entropy_values),
        "normalized_entropy_avg": round(sum(entropy_values) / len(entropy_values), 3),
        "normalized_entropy_std": round(stddev(entropy_values), 3),
        "normalized_entropy_cv": round(vector_cv(entropy_values), 3),
        "ttr_avg": round(sum(ttr_values) / len(ttr_values), 3),
        "ttr_cv": round(vector_cv(ttr_values), 3),
    }


def score_burstiness(sent_lens: list[int], para_lens: list[int], short_sentence_ratio: float, fragment_stats: dict | None = None) -> dict:
    sentence_std = stddev(sent_lens)
    sentence_cv = coefficient_of_variation(sent_lens)
    paragraph_cv = coefficient_of_variation(para_lens)
    signals = []

    if len(sent_lens) >= 8:
        if sentence_std < 1.5:
            signals.append(risk_item("sentence_std_lt_1_5", 1.0, f"句长标准差 {sentence_std:.2f} < 1.5，符合朱雀突发性高风险特征"))
        elif sentence_std < 3:
            signals.append(risk_item("sentence_std_low", 0.65, f"句长标准差 {sentence_std:.2f}，句长变化偏低"))
        elif sentence_cv < 0.45:
            signals.append(risk_item("sentence_cv_low", 0.55, f"句长 CV={sentence_cv:.3f}，节奏偏均匀"))
        elif sentence_cv < 0.65:
            signals.append(risk_item("sentence_cv_mid", 0.18, f"句长 CV={sentence_cv:.3f}，起伏略弱"))
    if len(para_lens) >= 6:
        if paragraph_cv < 0.38:
            signals.append(risk_item("paragraph_cv_low", 0.55, f"段长 CV={paragraph_cv:.3f}，段落长度过整齐"))
        elif paragraph_cv < 0.55:
            signals.append(risk_item("paragraph_cv_mid", 0.30, f"段长 CV={paragraph_cv:.3f}，段落疏密偏弱"))
    if len(sent_lens) >= 20 and short_sentence_ratio < 0.08:
        signals.append(risk_item("short_sentence_ratio_low", 0.35, f"短句比例 {short_sentence_ratio}，缺少自然断气"))
    fragment_stats = fragment_stats or {}
    if (
        len(sent_lens) >= 80
        and short_sentence_ratio >= 0.40
        and fragment_stats.get("median_hanzi_per_paragraph", 999) <= 12
        and fragment_stats.get("single_sentence_paragraph_ratio", 0) >= 0.62
    ):
        signals.append(
            risk_item(
                "over_staccato_humanizer",
                0.70,
                "短句比例很高且大量单句短段，呈现刻意打碎的 humanizer/staccato 痕迹",
            )
        )

    score = max([item["score"] for item in signals], default=0)
    if signals:
        score = min(100, score + max(0, len(signals) - 1) * 8)
    return {
        "name": "突发性",
        "score": round(score, 2),
        "weight": DIMENSION_WEIGHTS["burstiness"],
        "stats": {
            "sentence_std": round(sentence_std, 3),
            "sentence_cv": round(sentence_cv, 3),
            "paragraph_cv": round(paragraph_cv, 3),
            "short_sentence_ratio": short_sentence_ratio,
        },
        "signals": signals,
    }


def score_perplexity_proxy(chars: list[str], total_cliche: float, per_k: dict[str, float], concrete_density: float, repeated_12_extra: int) -> dict:
    entropy = char_entropy(chars)
    unique = len(set(chars))
    max_entropy = math.log2(max(unique, 2))
    entropy_ratio = entropy / max_entropy if max_entropy else 0.0
    ttr = unique / max(len(chars), 1)
    top_ratio = Counter(chars).most_common(1)[0][1] / max(len(chars), 1) if chars else 0.0
    signals = []

    if len(chars) >= 300:
        if entropy_ratio < 0.72:
            signals.append(risk_item("entropy_low", 0.62, f"归一化字熵 {entropy_ratio:.3f}，词字选择过于可预测"))
        elif entropy_ratio < 0.78:
            signals.append(risk_item("entropy_mid", 0.36, f"归一化字熵 {entropy_ratio:.3f}，随机性偏低"))
        if ttr < 0.28:
            signals.append(risk_item("ttr_low", 0.48, f"用字多样度 TTR={ttr:.3f} 偏低"))
        if top_ratio > 0.055:
            signals.append(risk_item("top_char_high", 0.28, f"最高频汉字占比 {top_ratio:.3f} 偏高"))
    if total_cliche >= 8:
        signals.append(risk_item("cliche_total_high", 0.72, f"套路措辞密度 {total_cliche}/千字，词汇过于正确/常规"))
    elif total_cliche >= 4:
        signals.append(risk_item("cliche_total_mid", 0.45, f"套路措辞密度 {total_cliche}/千字偏高"))
    if per_k.get("情绪命名", 0) >= 1 or per_k.get("陈词意象", 0) >= 1:
        signals.append(risk_item("safe_emotion_imagery", 0.40, "情绪命名/陈词意象偏高，词汇选择安全可预测"))
    if concrete_density < 0.4 and len(chars) >= 800:
        signals.append(risk_item("concrete_low", 0.35, f"具体物/数字密度 {concrete_density}/千字，缺少随机生活细节"))
    if repeated_12_extra >= 3:
        signals.append(risk_item("long_ngram_repeat", min(0.65, repeated_12_extra / 12), f"12字级额外重复 {repeated_12_extra} 次"))

    score = max([item["score"] for item in signals], default=0)
    if signals:
        score = min(100, score + max(0, len(signals) - 1) * 6)
    return {
        "name": "困惑度代理",
        "score": round(score, 2),
        "weight": DIMENSION_WEIGHTS["perplexity_proxy"],
        "note": "本地无语言模型概率，使用字熵、TTR、套路密度、具体物密度和重复作为 Perplexity 代理。",
        "stats": {
            "char_entropy": round(entropy, 3),
            "normalized_entropy": round(entropy_ratio, 3),
            "ttr": round(ttr, 3),
            "top_char_ratio": round(top_ratio, 3),
            "cliche_total_per_k": total_cliche,
            "concrete_density_per_k": concrete_density,
            "repeated_12_extra": repeated_12_extra,
        },
        "signals": signals,
    }


def score_structure_fingerprint(body: str, paras: list[str], per_k: dict[str, float], fragment_stats: dict | None = None) -> dict:
    ordered_marker_re = re.compile(r"(?:首先|其次|再次|最后|总之|综上|换句话说|第一[，、点:]|第二[，、点:]|第三[，、点:])")
    transition_markers = ["然而", "与此同时", "随后", "紧接着", "片刻后", "很快", "没过多久", "于是", "因此"]
    summary_markers = ["这让他意识到", "这让她意识到", "终于明白", "不仅仅是", "更是", "这意味着"]
    marker_count = len(ordered_marker_re.findall(body))
    transition_count = sum(body.count(item) for item in transition_markers)
    summary_count = sum(body.count(item) for item in summary_markers)
    n_hanzi = len(hanzi(body))
    transition_density = density(transition_count, n_hanzi)
    summary_density = density(summary_count, n_hanzi)

    starts = []
    for para in paras:
        clean = re.sub(r"[^一-龥]", "", para)
        if len(clean) >= 4:
            starts.append(clean[:4])
    repeated_starts = sum(count - 1 for count in Counter(starts).values() if count >= 2)
    sentence_counts = [len(split_sentences(para)) for para in paras if para.strip()]
    sentence_count_cv = coefficient_of_variation(sentence_counts)

    signals = []
    if marker_count >= 2:
        signals.append(risk_item("ordered_markers", min(1.0, marker_count / 4), f"出现结构标记 {marker_count} 次（首先/其次/最后等）"))
    if transition_density >= 2:
        signals.append(risk_item("transition_density_high", 0.65, f"机械转场密度 {transition_density}/千字"))
    elif transition_density >= 1:
        signals.append(risk_item("transition_density_mid", 0.35, f"机械转场密度 {transition_density}/千字"))
    if summary_density >= 1:
        signals.append(risk_item("summary_density", 0.62, f"解释归纳标记密度 {summary_density}/千字"))
    elif per_k.get("解释归纳", 0) >= 0.8:
        signals.append(risk_item("summary_category", 0.48, f"解释归纳类密度 {per_k['解释归纳']}/千字"))
    if repeated_starts >= 3:
        signals.append(risk_item("paragraph_start_repeat", 0.42, f"段首 4 字重复额外 {repeated_starts} 次"))
    if len(sentence_counts) >= 8 and sentence_count_cv < 0.35:
        signals.append(risk_item("paragraph_sentence_shape", 0.35, f"每段句数 CV={sentence_count_cv:.3f}，段落组织同构"))
    fragment_stats = fragment_stats or {}
    if (
        fragment_stats.get("paragraph_count", 0) >= 70
        and fragment_stats.get("median_hanzi_per_paragraph", 999) <= 12
        and fragment_stats.get("single_sentence_paragraph_ratio", 0) >= 0.62
    ):
        signals.append(
            risk_item(
                "fragmented_single_sentence_paragraphs",
                0.86,
                f"单句短段比例 {fragment_stats.get('single_sentence_paragraph_ratio')}，段落中位汉字 {fragment_stats.get('median_hanzi_per_paragraph')}，像后期反检测式碎段",
            )
        )
    if fragment_stats.get("very_short_paragraph_ratio", 0) >= 0.28 and fragment_stats.get("paragraph_count", 0) >= 70:
        signals.append(
            risk_item(
                "very_short_paragraph_overuse",
                0.58,
                f"6字以内短段占比 {fragment_stats.get('very_short_paragraph_ratio')}，短促断行过密",
            )
        )
    if fragment_stats.get("bracket_line_ratio", 0) >= 0.10 and fragment_stats.get("paragraph_count", 0) >= 60:
        signals.append(
            risk_item(
                "contract_block_density",
                0.48,
                f"独立条款/账单块占比 {fragment_stats.get('bracket_line_ratio')}，文本结构过度依赖格式化规则块",
            )
        )

    score = max([item["score"] for item in signals], default=0)
    if signals:
        score = min(100, score + max(0, len(signals) - 1) * 7)
    return {
        "name": "结构指纹",
        "score": round(score, 2),
        "weight": DIMENSION_WEIGHTS["structure_fingerprint"],
        "stats": {
            "ordered_marker_count": marker_count,
            "transition_density_per_k": transition_density,
            "summary_density_per_k": summary_density,
            "repeated_paragraph_starts": repeated_starts,
            "paragraph_sentence_count_cv": round(sentence_count_cv, 3),
        },
        "signals": signals,
    }


def score_cross_paragraph_consistency(rows: list[dict]) -> dict:
    signals = []
    if len(rows) < 6:
        return {
            "name": "跨段一致性",
            "score": 0,
            "weight": DIMENSION_WEIGHTS["cross_paragraph_consistency"],
            "stats": {"paragraph_feature_count": len(rows)},
            "signals": [],
        }

    lengths = [row["hanzi"] for row in rows if row["hanzi"] > 0]
    avg_sentence_lens = [row["avg_sentence_len"] for row in rows]
    sentence_cvs = [row["sentence_cv"] for row in rows]
    comma_ratios = [row["comma_period_ratio"] for row in rows]
    cliche_densities_values = [row["cliche_total_per_k"] for row in rows]
    dialogue_values = [row["dialogue"] for row in rows]
    similarity = paragraph_similarity_stats(rows)

    length_cv = coefficient_of_variation(lengths)
    avg_sentence_cv = coefficient_of_variation([int(v * 10) for v in avg_sentence_lens if v > 0])
    sentence_cv_std = stddev(sentence_cvs)
    comma_ratio_std = stddev(comma_ratios)
    cliche_std = stddev(cliche_densities_values)
    dialogue_std = stddev(dialogue_values)

    if length_cv < 0.35:
        signals.append(risk_item("paragraph_length_uniform", 0.58, f"段长 CV={length_cv:.3f}，段落长度过于一致"))
    elif length_cv < 0.55:
        signals.append(risk_item("paragraph_length_mid", 0.30, f"段长 CV={length_cv:.3f}，段落变化偏弱"))
    if avg_sentence_cv < 0.28:
        signals.append(risk_item("avg_sentence_uniform", 0.45, f"各段平均句长 CV={avg_sentence_cv:.3f}，句式跨段一致"))
    if sentence_cv_std < 0.12:
        signals.append(risk_item("sentence_rhythm_uniform", 0.38, f"各段句长 CV 标准差 {sentence_cv_std:.3f}，节奏变化少"))
    if comma_ratio_std < 0.45:
        signals.append(risk_item("punctuation_uniform", 0.26, f"各段逗句比标准差 {comma_ratio_std:.3f}，标点习惯过稳"))
    if cliche_std < 0.35 and max(cliche_densities_values) > 0:
        signals.append(risk_item("cliche_distribution_uniform", 0.24, f"各段套路密度标准差 {cliche_std:.3f}，套路分布均匀"))
    if dialogue_std < 0.25 and sum(dialogue_values) not in {0, len(dialogue_values)}:
        signals.append(risk_item("dialogue_distribution_uniform", 0.20, f"对话分布标准差 {dialogue_std:.3f}，段落声口切换偏均匀"))
    if similarity["adjacent_similarity_high_ratio"] >= 0.65 and similarity["adjacent_similarity_std"] <= 0.08:
        signals.append(
            risk_item(
                "paragraph_vector_uniform",
                0.46,
                f"相邻段落风格向量高相似比例 {similarity['adjacent_similarity_high_ratio']:.3f}，段落功能过于同质",
            )
        )
    elif similarity["adjacent_similarity_avg"] >= 0.94 and similarity["adjacent_similarity_std"] <= 0.06:
        signals.append(
            risk_item(
                "paragraph_vector_mid",
                0.28,
                f"相邻段落风格向量均值 {similarity['adjacent_similarity_avg']:.3f}，变化偏弱",
            )
        )

    score = max([item["score"] for item in signals], default=0)
    if signals:
        score = min(100, score + max(0, len(signals) - 1) * 5)
    return {
        "name": "跨段一致性",
        "score": round(score, 2),
        "weight": DIMENSION_WEIGHTS["cross_paragraph_consistency"],
        "stats": {
            "paragraph_feature_count": len(rows),
            "paragraph_length_cv": round(length_cv, 3),
            "avg_sentence_len_cv_across_paragraphs": round(avg_sentence_cv, 3),
            "sentence_cv_std_across_paragraphs": round(sentence_cv_std, 3),
            "comma_ratio_std": round(comma_ratio_std, 3),
            "cliche_density_std": round(cliche_std, 3),
            "dialogue_distribution_std": round(dialogue_std, 3),
            **similarity,
        },
        "signals": signals,
    }


def score_weak_lm_uniformity(surprisal: dict) -> dict:
    signals = []
    if surprisal["sentence_count"] >= 20:
        high_uniform = (
            surprisal["bigram_surprisal_cv"]
            and surprisal["bigram_surprisal_cv"] < 0.030
            and surprisal["unigram_surprisal_cv"] < 0.055
        )
        strong_curve_flat = (
            surprisal["bigram_surprisal_cv"]
            and surprisal["bigram_surprisal_cv"] < 0.035
            and surprisal["unigram_surprisal_cv"] < 0.060
        )
        strong_uniform = (
            surprisal["bigram_surprisal_cv"]
            and surprisal["bigram_surprisal_cv"] < 0.055
            and (surprisal["bigram_surprisal_avg"] < 7.25 or surprisal["unigram_surprisal_cv"] < 0.045)
        )
        mid_uniform = (
            surprisal["bigram_surprisal_cv"] < 0.070
            and (surprisal["bigram_surprisal_avg"] < 7.45 or surprisal["unigram_surprisal_cv"] < 0.050)
        )
        if high_uniform:
            signals.append(
                risk_item(
                    "bigram_unigram_curve_too_flat",
                    0.96,
                    f"句级 bigram CV={surprisal['bigram_surprisal_cv']:.3f}、unigram CV={surprisal['unigram_surprisal_cv']:.3f}，弱模型概率曲线异常平滑",
                )
            )
        elif strong_curve_flat:
            signals.append(
                risk_item(
                    "bigram_curve_flat_high",
                    0.82,
                    f"句级 bigram 惊讶度 CV={surprisal['bigram_surprisal_cv']:.3f}，跨句概率曲线过于稳定",
                )
            )
        elif strong_uniform:
            signals.append(
                risk_item(
                    "bigram_surprisal_cv_low",
                    0.58,
                    f"句级 bigram 惊讶度 CV={surprisal['bigram_surprisal_cv']:.3f} 且均值偏低，弱语言模型下过于可预测",
                )
            )
        elif mid_uniform:
            signals.append(
                risk_item(
                    "bigram_surprisal_cv_mid",
                    0.35,
                    f"句级 bigram 惊讶度 CV={surprisal['bigram_surprisal_cv']:.3f}，局部概率曲线偏平且均值偏低",
                )
            )
        if surprisal["unigram_surprisal_cv"] and surprisal["unigram_surprisal_cv"] < 0.040:
            signals.append(
                risk_item(
                    "unigram_surprisal_cv_low",
                    0.30,
                    f"句级 unigram 惊讶度 CV={surprisal['unigram_surprisal_cv']:.3f}，词字分布过稳",
                )
            )
    score = max([item["score"] for item in signals], default=0)
    if signals:
        score = min(100, score + max(0, len(signals) - 1) * 5)
    return {
        "name": "弱语言模型一致性",
        "score": round(score, 2),
        "weight": LATEST_PROXY_WEIGHTS["weak_lm_uniformity"],
        "stats": surprisal,
        "signals": signals,
    }


def score_local_entropy_uniformity(entropy: dict) -> dict:
    signals = []
    if entropy["window_count"] >= 4:
        low_diversity = entropy["normalized_entropy_avg"] < 0.955 or entropy["ttr_avg"] < 0.620
        if (
            entropy["window_count"] >= 8
            and entropy["normalized_entropy_std"] < 0.010
            and entropy["normalized_entropy_cv"] < 0.012
            and entropy["ttr_cv"] < 0.070
        ):
            signals.append(
                risk_item(
                    "window_entropy_signature_flat",
                    0.90,
                    f"滑窗熵 std={entropy['normalized_entropy_std']:.3f}、CV={entropy['normalized_entropy_cv']:.3f}，局部随机性曲线异常稳定",
                )
            )
        elif (
            entropy["window_count"] >= 8
            and entropy["normalized_entropy_std"] < 0.016
            and entropy["ttr_cv"] <= 0.080
        ):
            signals.append(
                risk_item(
                    "window_entropy_signature_mid",
                    0.68,
                    f"滑窗熵 std={entropy['normalized_entropy_std']:.3f}，局部随机性波动偏低",
                )
            )
        elif entropy["normalized_entropy_std"] < 0.018 and low_diversity:
            signals.append(
                risk_item(
                    "window_entropy_std_low",
                    0.50,
                    f"滑窗归一化熵标准差 {entropy['normalized_entropy_std']:.3f} 且局部多样度偏低，随机性过稳",
                )
            )
        elif entropy["normalized_entropy_std"] < 0.028 and low_diversity:
            signals.append(
                risk_item(
                    "window_entropy_std_mid",
                    0.30,
                    f"滑窗归一化熵标准差 {entropy['normalized_entropy_std']:.3f}，局部随机性变化偏小且多样度不足",
                )
            )
        if entropy["ttr_cv"] < 0.080 and entropy["ttr_avg"] < 0.620:
            signals.append(risk_item("window_ttr_cv_low", 0.35, f"滑窗 TTR CV={entropy['ttr_cv']:.3f}，词字多样度跨段过稳"))
    score = max([item["score"] for item in signals], default=0)
    if signals:
        score = min(100, score + max(0, len(signals) - 1) * 5)
    return {
        "name": "局部熵/TTR波动",
        "score": round(score, 2),
        "weight": LATEST_PROXY_WEIGHTS["local_entropy_uniformity"],
        "stats": entropy,
        "signals": signals,
    }


def score_probability_curvature_proxy(surprisal: dict, entropy: dict) -> dict:
    signals = []
    sentence_count = surprisal.get("sentence_count", 0)
    window_count = entropy.get("window_count", 0)
    bigram_cv = surprisal.get("bigram_surprisal_cv", 0)
    unigram_cv = surprisal.get("unigram_surprisal_cv", 0)
    entropy_std = entropy.get("normalized_entropy_std", 0)
    entropy_cv = entropy.get("normalized_entropy_cv", 0)
    ttr_cv = entropy.get("ttr_cv", 0)

    if (
        sentence_count >= 80
        and window_count >= 10
        and 0 < bigram_cv < 0.032
        and unigram_cv < 0.055
        and entropy_std < 0.010
        and entropy_cv < 0.012
    ):
        signals.append(
            risk_item(
                "fast_detectgpt_curve_proxy_high",
                0.98,
                f"句级概率曲线与滑窗熵曲线同时过平：bigram CV={bigram_cv:.3f}，unigram CV={unigram_cv:.3f}，entropy std={entropy_std:.3f}",
            )
        )
    elif (
        sentence_count >= 60
        and window_count >= 8
        and 0 < bigram_cv < 0.040
        and entropy_std < 0.014
        and ttr_cv <= 0.080
    ):
        signals.append(
            risk_item(
                "detectgpt_curve_proxy_mid_high",
                0.84,
                f"弱模型惊讶度曲线和局部多样度曲线同步偏平：bigram CV={bigram_cv:.3f}，entropy std={entropy_std:.3f}，TTR CV={ttr_cv:.3f}",
            )
        )
    if sentence_count >= 80 and 0 < bigram_cv < 0.030 and entropy_std < 0.008:
        signals.append(
            risk_item(
                "sentence_classifier_consensus_flat",
                0.88,
                "大量句子的局部概率形态高度一致，符合句级分类器高一致性风险",
            )
        )

    score = max([item["score"] for item in signals], default=0)
    if signals:
        score = min(100, score + max(0, len(signals) - 1) * 6)
    return {
        "name": "概率曲率/句级分类代理",
        "score": round(score, 2),
        "weight": LATEST_PROXY_WEIGHTS["probability_curvature_proxy"],
        "stats": {
            "sentence_count": sentence_count,
            "window_count": window_count,
            "bigram_surprisal_cv": bigram_cv,
            "unigram_surprisal_cv": unigram_cv,
            "normalized_entropy_std": entropy_std,
            "normalized_entropy_cv": entropy_cv,
            "ttr_cv": ttr_cv,
        },
        "signals": signals,
    }


def score_stylometry_readability(sent_lens: list[int], punctuation: dict, dialogue_ratio: float, n_hanzi: int) -> dict:
    signals = []
    punctuation_kinds = sum(
        1
        for value in [
            punctuation.get("comma", 0),
            punctuation.get("period", 0),
            punctuation.get("dash", 0),
            punctuation.get("ellipsis", 0),
        ]
        if value > 0
    )
    if len(sent_lens) >= 20:
        mean_len = sum(sent_lens) / len(sent_lens)
        long_ratio = sum(1 for length in sent_lens if length >= mean_len * 1.8) / len(sent_lens)
        mid_ratio = sum(1 for length in sent_lens if mean_len * 0.65 <= length <= mean_len * 1.35) / len(sent_lens)
        short_ratio = sum(1 for length in sent_lens if length <= 8) / len(sent_lens)
        bins = [
            sum(1 for length in sent_lens if length <= 8) / len(sent_lens),
            sum(1 for length in sent_lens if 9 <= length <= 16) / len(sent_lens),
            sum(1 for length in sent_lens if 17 <= length <= 28) / len(sent_lens),
            sum(1 for length in sent_lens if length >= 29) / len(sent_lens),
        ]
        balanced_bins = min(bins) >= 0.18 and max(bins) <= 0.32
        sentence_cv = coefficient_of_variation(sent_lens)
        if (
            len(sent_lens) >= 80
            and balanced_bins
            and 0.55 <= sentence_cv <= 0.75
            and 0.18 <= short_ratio <= 0.32
            and 0.16 <= dialogue_ratio <= 0.35
        ):
            signals.append(
                risk_item(
                    "over_balanced_sentence_band_distribution",
                    0.45,
                    f"短/中/长句分布过于均衡，句长CV={sentence_cv:.3f}，四档占比={[round(v, 3) for v in bins]}",
                )
            )
        if mid_ratio >= 0.72 and long_ratio < 0.06:
            signals.append(risk_item("sentence_length_centered", 0.45, f"中位句长带占比 {mid_ratio:.3f}，极长/极短句不足"))
        elif mid_ratio >= 0.62:
            signals.append(risk_item("sentence_length_centered_mid", 0.25, f"中位句长带占比 {mid_ratio:.3f}，句式分布偏集中"))
    if n_hanzi >= 1200 and dialogue_ratio < 0.08 and punctuation_kinds <= 2:
        signals.append(risk_item("punctuation_palette_thin", 0.28, f"标点种类 {punctuation_kinds} 且对话比例 {dialogue_ratio}，文本表层风格偏单一"))
    score = max([item["score"] for item in signals], default=0)
    if signals:
        score = min(100, score + max(0, len(signals) - 1) * 5)
    return {
        "name": "风格计量/可读性",
        "score": round(score, 2),
        "weight": LATEST_PROXY_WEIGHTS["stylometry_readability"],
        "stats": {
            "punctuation_kinds": punctuation_kinds,
            "dialogue_ratio": dialogue_ratio,
        },
        "signals": signals,
    }


def score_semantic_smoothing(body: str, n_hanzi: int, concrete_density: float, per_k: dict[str, float]) -> dict:
    action_density = density(count_markers(body, ACTION_HINTS), n_hanzi)
    sensory_density = density(count_markers(body, SENSORY_HINTS), n_hanzi)
    emotion_density = density(count_markers(body, EMOTION_WORDS), n_hanzi)
    abstract_density = density(count_markers(body, ABSTRACT_HINTS), n_hanzi)
    scene_density = concrete_density + action_density + sensory_density
    signals = []

    if n_hanzi >= 800 and abstract_density >= 5 and scene_density < 8:
        signals.append(risk_item("abstract_high_scene_low", 0.52, f"抽象词密度 {abstract_density}/千字，场景动作感官密度 {scene_density}/千字"))
    elif n_hanzi >= 800 and abstract_density >= 3.5 and scene_density < 10:
        signals.append(risk_item("abstract_scene_mid", 0.32, f"抽象概括偏高且场景锚点偏少"))
    if emotion_density >= 2 and action_density < 8:
        signals.append(risk_item("emotion_named_action_low", 0.42, f"情绪命名密度 {emotion_density}/千字，动作密度 {action_density}/千字"))
    if per_k.get("解释归纳", 0) >= 0.8 and scene_density < 12:
        signals.append(risk_item("summary_without_scene", 0.40, "解释归纳腔偏高，但具体动作/物件承载不足"))

    score = max([item["score"] for item in signals], default=0)
    if signals:
        score = min(100, score + max(0, len(signals) - 1) * 6)
    return {
        "name": "语义平滑/概括腔",
        "score": round(score, 2),
        "weight": LATEST_PROXY_WEIGHTS["semantic_smoothing"],
        "stats": {
            "abstract_density_per_k": abstract_density,
            "action_density_per_k": action_density,
            "sensory_density_per_k": sensory_density,
            "emotion_density_per_k": emotion_density,
            "scene_density_per_k": round(scene_density, 2),
        },
        "signals": signals,
    }


def score_content_integrity(noise_stats: dict) -> dict:
    signals = []
    semantic_runs = noise_stats.get("semantic_noise_runs", 0)
    semantic_hanzi = noise_stats.get("semantic_noise_hanzi", 0)
    semantic_ratio = noise_stats.get("semantic_noise_ratio", 0)
    if semantic_runs:
        score = 1.0 if semantic_ratio >= 0.015 or semantic_hanzi >= 90 else 0.82
        signals.append(
            risk_item(
                "semantic_noise_char_soup",
                score,
                f"出现 {semantic_runs} 段无语义长串/脏码，共 {semantic_hanzi} 汉字，占比 {semantic_ratio * 100:.2f}%，疑似为绕检噪声而非剧情内容",
            )
        )
    elif noise_stats.get("sound_noise_ratio", 0) >= 0.03:
        signals.append(
            risk_item(
                "sound_noise_overuse",
                0.42,
                f"密集拟声/重复声响占比 {noise_stats.get('sound_noise_ratio', 0) * 100:.2f}%，需确认是否服务剧情而非曲线扰动",
            )
        )
    return {
        "name": "内容完整性/绕检噪声",
        "score": round(max([item["score"] for item in signals], default=0), 2),
        "weight": LATEST_PROXY_WEIGHTS["content_integrity"],
        "stats": noise_stats,
        "signals": signals,
    }


def technical_expository_anchor_stats(
    body: str,
    n_hanzi: int,
    sent_lens: list[int],
    dialogue_ratio: float,
    total_cliche: float,
    dup_report: dict,
    noise_stats: dict,
) -> dict:
    tech_hits = count_markers(body, TECHNICAL_HUMAN_HINTS)
    tech_density = density(tech_hits, n_hanzi)
    ascii_words = re.findall(r"[A-Za-z][A-Za-z0-9_-]{1,}", body)
    ascii_density = round(len(ascii_words) / max(n_hanzi, 1) * 1000, 2)
    avg_sentence_len = round(sum(sent_lens) / max(len(sent_lens), 1), 2)
    sentence_cv = coefficient_of_variation(sent_lens)
    paragraph_count = len(paragraphs(body))

    blockers = []
    if n_hanzi < 320:
        blockers.append("技术说明文人工锚点长度不足 320 汉字")
    if noise_stats.get("semantic_noise_runs", 0):
        blockers.append("存在无语义脏码/字符汤")
    if noise_stats.get("sound_noise_ratio", 0) >= 0.03:
        blockers.append("密集拟声/重复声响占比过高")
    if dup_report.get("exact_count", 0) or dup_report.get("similar_count", 0) or dup_report.get("sentence_duplicate_count", 0):
        blockers.append("存在段落或长句真重复")
    if dialogue_ratio > 0.08:
        blockers.append("存在明显对话声口，不属于技术说明文锚点")
    if total_cliche > 4:
        blockers.append("套路/解释归纳密度偏高，不适合技术说明文人工锚点")
    if tech_density < 18 and ascii_density < 45:
        blockers.append("领域术语或英文摘要密度不足")
    if avg_sentence_len < 22:
        blockers.append("说明文句长承载不足")

    credits = []
    score = 0

    def add(points: int, name: str, evidence: str) -> None:
        nonlocal score
        score += points
        credits.append({"name": name, "points": points, "evidence": evidence})

    if tech_density >= 45:
        add(26, "technical_terms_high", f"领域术语密度 {tech_density}/千字")
    elif tech_density >= 26:
        add(20, "technical_terms_mid", f"领域术语密度 {tech_density}/千字")
    elif tech_density >= 18:
        add(14, "technical_terms_present", f"领域术语密度 {tech_density}/千字")

    if ascii_density >= 90:
        add(20, "bilingual_abstract_shape", f"英文/缩写词密度 {ascii_density}/千字")
    elif ascii_density >= 45:
        add(12, "ascii_terms_present", f"英文/缩写词密度 {ascii_density}/千字")

    if avg_sentence_len >= 34 and 0.18 <= sentence_cv <= 0.62:
        add(18, "expository_long_sentence_control", f"均句长 {avg_sentence_len}，句长CV={sentence_cv:.3f}")
    elif avg_sentence_len >= 26:
        add(12, "expository_sentence_load", f"均句长 {avg_sentence_len}")

    if paragraph_count <= 4:
        add(10, "abstract_paragraph_form", f"段落数 {paragraph_count}，符合摘要/说明文形态")
    if dialogue_ratio == 0:
        add(8, "no_dialogue_expository", "无对话声口，说明文语域稳定")
    if total_cliche <= 1:
        add(8, "cliche_absent", f"套路措辞密度 {total_cliche}/千字")

    score = min(score, 100)
    eligible = not blockers and score >= 52
    return {
        "score": round(score, 2),
        "eligible": eligible,
        "strength": "strong" if eligible and score >= 72 else ("moderate" if eligible else "none"),
        "anchor_type": "technical_expository",
        "final_cap_allowed": eligible and score >= 52,
        "blockers": blockers,
        "credits": credits[:8],
        "curve_factor": 0.05 if eligible else 1.0,
        "curve_cap": 5.0 if eligible else 100.0,
        "style_factor": 0.15 if eligible else 1.0,
        "style_cap": 12.0 if eligible else 100.0,
        "segment_cap": 18.0 if eligible else 100.0,
        "metrics": {
            "anchor_type": "technical_expository",
            "technical_term_density_per_k": tech_density,
            "ascii_word_density_per_k": ascii_density,
            "avg_sentence_len": avg_sentence_len,
            "sentence_cv": round(sentence_cv, 3),
            "paragraph_count": paragraph_count,
            "dialogue_ratio": dialogue_ratio,
        },
    }


def high_quality_human_anchor_stats(
    body: str,
    n_hanzi: int,
    sent_lens: list[int],
    para_lens: list[int],
    dialogue_ratio: float,
    concrete_density: float,
    per_k: dict[str, float],
    total_cliche: float,
    dup_report: dict,
    noise_stats: dict,
    fragment_stats: dict,
) -> dict:
    action_density = density(count_markers(body, ACTION_HINTS), n_hanzi)
    sensory_density = density(count_markers(body, SENSORY_HINTS), n_hanzi)
    emotion_density = density(count_markers(body, EMOTION_WORDS), n_hanzi)
    abstract_density = density(count_markers(body, ABSTRACT_HINTS), n_hanzi)
    scene_density = round(concrete_density + action_density + sensory_density, 2)
    sentence_cv = coefficient_of_variation(sent_lens)
    paragraph_cv = coefficient_of_variation(para_lens)
    short_sentence_ratio_12 = round(sum(1 for length in sent_lens if length <= 12) / max(len(sent_lens), 1), 3)
    quote_marks = sum(body.count(mark) for mark in ["“", "”", "「", "」", "『", "』"])
    quote_density = density(quote_marks, n_hanzi)
    punct_kinds = punctuation_kind_count(body)
    technical_anchor = technical_expository_anchor_stats(
        body,
        n_hanzi,
        sent_lens,
        dialogue_ratio,
        total_cliche,
        dup_report,
        noise_stats,
    )
    if technical_anchor.get("eligible"):
        return technical_anchor

    blockers = []
    if n_hanzi < 800:
        blockers.append("文本长度不足 800 汉字，人工锚点置信度低")
    if noise_stats.get("semantic_noise_runs", 0):
        blockers.append("存在无语义脏码/字符汤")
    if noise_stats.get("sound_noise_ratio", 0) >= 0.03:
        blockers.append("密集拟声/重复声响占比过高")
    if dup_report.get("exact_count", 0) or dup_report.get("similar_count", 0) or dup_report.get("sentence_duplicate_count", 0):
        blockers.append("存在段落或长句真重复")
    if per_k.get("工程泄漏", 0) > 0:
        blockers.append("存在写作工程词泄漏")
    if total_cliche >= 10 and scene_density < 18:
        blockers.append("套路密度高且场景锚点不足")
    if (
        fragment_stats.get("paragraph_count", 0) >= 70
        and fragment_stats.get("single_sentence_paragraph_ratio", 0) >= 0.70
        and fragment_stats.get("short_paragraph_ratio", 0) >= 0.62
        and fragment_stats.get("bracket_line_ratio", 0) >= 0.08
    ):
        blockers.append("短段与条款块同时密集，疑似人味化后处理")

    credits = []
    score = 0

    def add(points: int, name: str, evidence: str) -> None:
        nonlocal score
        score += points
        credits.append({"name": name, "points": points, "evidence": evidence})

    if sentence_cv >= 0.72:
        add(18, "sentence_cv_high", f"句长 CV={sentence_cv:.3f}，长短句自然拉开")
    elif sentence_cv >= 0.60:
        add(14, "sentence_cv_mid_high", f"句长 CV={sentence_cv:.3f}，句内节奏有起伏")
    elif sentence_cv >= 0.52:
        add(8, "sentence_cv_usable", f"句长 CV={sentence_cv:.3f}，未呈均质平滑")

    if paragraph_cv >= 0.70:
        add(16, "paragraph_cv_high", f"段长 CV={paragraph_cv:.3f}，段落疏密明显")
    elif paragraph_cv >= 0.55:
        add(12, "paragraph_cv_mid_high", f"段长 CV={paragraph_cv:.3f}，段落功能有差异")
    elif paragraph_cv >= 0.45:
        add(6, "paragraph_cv_usable", f"段长 CV={paragraph_cv:.3f}，没有过度整齐")

    if 0.10 <= short_sentence_ratio_12 <= 0.50:
        add(12, "short_sentence_mix", f"12字以内短句比例 {short_sentence_ratio_12}，有自然断气")
    elif 0.06 <= short_sentence_ratio_12 <= 0.58:
        add(6, "short_sentence_present", f"12字以内短句比例 {short_sentence_ratio_12}")

    if dialogue_ratio >= 0.25 or quote_density >= 45:
        add(14, "dialogue_voice_high", f"对话比例 {dialogue_ratio}，引号密度 {quote_density}/千字")
    elif dialogue_ratio >= 0.12 or quote_density >= 18:
        add(10, "dialogue_voice_present", f"对话比例 {dialogue_ratio}，引号密度 {quote_density}/千字")

    if scene_density >= 28:
        add(20, "scene_density_high", f"物件/动作/感官密度 {scene_density}/千字")
    elif scene_density >= 20:
        add(15, "scene_density_mid_high", f"物件/动作/感官密度 {scene_density}/千字")
    elif scene_density >= 14:
        add(9, "scene_density_present", f"物件/动作/感官密度 {scene_density}/千字")

    if action_density >= 10 and sensory_density >= 5:
        add(12, "action_sensory_chain", f"动作密度 {action_density}/千字，感官密度 {sensory_density}/千字")
    elif action_density >= 8:
        add(6, "action_chain", f"动作密度 {action_density}/千字")

    if abstract_density <= 4.5 and scene_density >= 18:
        add(8, "abstract_under_scene", f"抽象词密度 {abstract_density}/千字，低于场景锚点")
    if concrete_density >= 6:
        add(8, "concrete_objects", f"具体物/数字密度 {concrete_density}/千字")
    elif concrete_density >= 2:
        add(4, "concrete_objects_present", f"具体物/数字密度 {concrete_density}/千字")
    if punct_kinds >= 8:
        add(8, "punctuation_palette", f"标点种类 {punct_kinds}，对话/停顿层次较多")
    if total_cliche <= 4:
        add(8, "cliche_low", f"套路措辞密度 {total_cliche}/千字")
    elif total_cliche <= 7:
        add(4, "cliche_controlled", f"套路措辞密度 {total_cliche}/千字")

    score = min(score, 100)
    eligible = not blockers and score >= 52
    strength = "none"
    if eligible and score >= 72:
        strength = "strong"
    elif eligible:
        strength = "moderate"

    if strength == "strong":
        if score >= 90 and scene_density >= 28 and total_cliche <= 4:
            curve_factor = 0.34
            curve_cap = 36.0
            style_factor = 0.55
            style_cap = 50.0
            segment_cap = 88.0
        elif score >= 82 and scene_density >= 22:
            curve_factor = 0.42
            curve_cap = 45.0
            style_factor = 0.60
            style_cap = 55.0
            segment_cap = 78.0
        else:
            curve_factor = 0.55
            curve_cap = 58.0
            style_factor = 0.70
            style_cap = 65.0
            segment_cap = 70.0
    elif strength == "moderate":
        curve_factor = 0.70
        curve_cap = 70.0
        style_factor = 0.75
        style_cap = 72.0
        segment_cap = 70.0
    else:
        curve_factor = 1.0
        curve_cap = 100.0
        style_factor = 1.0
        style_cap = 100.0
        segment_cap = 100.0

    return {
        "score": round(score, 2),
        "eligible": eligible,
        "strength": strength,
        "anchor_type": "narrative_scene",
        "final_cap_allowed": eligible and strength == "strong" and score >= 88,
        "blockers": blockers,
        "credits": credits[:8],
        "curve_factor": curve_factor,
        "curve_cap": curve_cap,
        "style_factor": style_factor,
        "style_cap": style_cap,
        "segment_cap": segment_cap,
        "metrics": {
            "sentence_cv": round(sentence_cv, 3),
            "paragraph_cv": round(paragraph_cv, 3),
            "short_sentence_ratio_12": short_sentence_ratio_12,
            "dialogue_ratio": dialogue_ratio,
            "quote_density_per_k": quote_density,
            "punctuation_kinds": punct_kinds,
            "concrete_density_per_k": concrete_density,
            "action_density_per_k": action_density,
            "sensory_density_per_k": sensory_density,
            "emotion_density_per_k": emotion_density,
            "abstract_density_per_k": abstract_density,
            "scene_density_per_k": scene_density,
        },
    }


def apply_human_anchor_calibration(components: dict, human_anchor: dict) -> dict:
    if not human_anchor.get("eligible"):
        return components
    factor = float(human_anchor.get("curve_factor", 1.0))
    cap = float(human_anchor.get("curve_cap", 100.0))
    style_factor = float(human_anchor.get("style_factor", 1.0))
    style_cap = float(human_anchor.get("style_cap", 100.0))
    target_keys = {
        "probability_curvature_proxy",
        "weak_lm_uniformity",
        "local_entropy_uniformity",
        "zhuque_segment_proxy",
        "stylometry_readability",
    }
    for key in target_keys:
        item = components.get(key)
        if not item:
            continue
        original = float(item.get("score", 0))
        if key == "stylometry_readability":
            adjusted = round(min(original * style_factor, style_cap), 2)
        else:
            adjusted = round(min(original * factor, cap), 2)
        if adjusted < original:
            item["score"] = adjusted
            item.setdefault("stats", {})["human_anchor_adjusted_from"] = round(original, 2)
            item.setdefault("signals", []).append(
                {
                    "name": "high_quality_human_anchor_calibration",
                    "score": 0,
                    "evidence": f"高质人工样本锚点 {human_anchor.get('score')}，曲线类误判从 {original:.2f}% 降至 {adjusted:.2f}%",
                }
            )
    return components


def score_layout_humanizer_fingerprint(fragment_stats: dict) -> dict:
    signals = []
    paragraph_count = fragment_stats.get("paragraph_count", 0)
    median_len = fragment_stats.get("median_hanzi_per_paragraph", 0)
    avg_len = fragment_stats.get("avg_hanzi_per_paragraph", 0)
    short_ratio = fragment_stats.get("short_paragraph_ratio", 0)
    very_short_ratio = fragment_stats.get("very_short_paragraph_ratio", 0)
    single_ratio = fragment_stats.get("single_sentence_paragraph_ratio", 0)
    bracket_ratio = fragment_stats.get("bracket_line_ratio", 0)

    if paragraph_count >= 70 and single_ratio >= 0.62 and short_ratio >= 0.55:
        signals.append(
            risk_item(
                "single_sentence_short_paragraph_grid",
                0.78,
                f"单句段 {single_ratio} 且 12字以内短段 {short_ratio}，呈现句段级分类器容易识别的网格化断行",
            )
        )
    if paragraph_count >= 60 and median_len <= 10 and avg_len <= 24:
        signals.append(
            risk_item(
                "microparagraph_median_low",
                0.60,
                f"段落中位汉字 {median_len}、平均汉字 {avg_len}，整章信息点被拆得过碎",
            )
        )
    if paragraph_count >= 60 and bracket_ratio >= 0.10:
        signals.append(
            risk_item(
                "formatted_clause_blocks",
                0.48,
                f"独立条款/账单块占比 {bracket_ratio}，格式化块密度偏高",
            )
        )
    if paragraph_count >= 70 and bracket_ratio >= 0.08 and short_ratio >= 0.50 and very_short_ratio >= 0.24:
        signals.append(
            risk_item(
                "fragmented_rule_card_pattern",
                0.72,
                "短段、极短段和规则卡片同时密集，像先生成再用 humanizer 打碎的后处理文本",
            )
        )

    score = max([item["score"] for item in signals], default=0)
    if signals:
        score = min(100, score + max(0, len(signals) - 1) * 6)
    return {
        "name": "布局/碎段人味化指纹",
        "score": round(score, 2),
        "weight": LATEST_PROXY_WEIGHTS["layout_humanizer_fingerprint"],
        "stats": fragment_stats,
        "signals": signals,
    }


def empty_zhuque_segment_proxy() -> dict:
    return {
        "enabled": False,
        "segments": [],
        "suspected_ai_ratio_percent": 0,
        "human_ratio_percent": 0,
        "ai_feature_ratio_percent": 0,
        "max_segment_percent": 0,
        "max_segment_index": 0,
        "risk_floor_percent": 0,
    }


def zhuque_like_segment_bounds(total: int) -> list[tuple[int, int]]:
    if total <= 0:
        return []
    if total < 800:
        return [(0, total)]
    # 2026-07-02 reports for current 01/02/03 all kept 3k-ish chapters as one
    # Zhuque evidence span, so the local proxy must not split and dilute them.
    if 1800 <= total <= 3600:
        return [(0, total)]
    bounds = []
    start = 0
    window = 640
    while start < total:
        end = min(total, start + window)
        if total - end < 360 and bounds:
            prev_start, _ = bounds[-1]
            bounds[-1] = (prev_start, total)
            break
        bounds.append((start, end))
        start = end
    return bounds


def raw_component_score(component: dict | None) -> float:
    if not component:
        return 0.0
    score = float(component.get("score", 0) or 0)
    adjusted_from = float(component.get("stats", {}).get("human_anchor_adjusted_from", 0) or 0)
    return max(score, adjusted_from)


def segment_aigc_proxy(segment_report: dict, char_count: int, proportion: float) -> tuple[float, list[str]]:
    latest = segment_report.get("latest_detector_proxy", {}).get("components", {})
    zhuque = segment_report.get("zhuque_dimensions", {})
    weak = latest.get("weak_lm_uniformity", {}).get("score", 0)
    probability_curve = latest.get("probability_curvature_proxy", {}).get("score", 0)
    entropy = latest.get("local_entropy_uniformity", {}).get("score", 0)
    raw_weak = raw_component_score(latest.get("weak_lm_uniformity"))
    raw_probability_curve = raw_component_score(latest.get("probability_curvature_proxy"))
    raw_entropy = raw_component_score(latest.get("local_entropy_uniformity"))
    layout = latest.get("layout_humanizer_fingerprint", {}).get("score", 0)
    stats = segment_report.get("stats", {})
    concrete = stats.get("concrete_density_per_k", 0)
    dialogue_ratio = stats.get("dialogue_ratio", 0)
    action_density = stats.get("action_density_per_k", 0)
    sensory_density = stats.get("sensory_density_per_k", 0)
    semantic_stats = latest.get("semantic_smoothing", {}).get("stats", {})
    action = semantic_stats.get("action_density_per_k", 0)
    perplexity_stats = zhuque.get("dimensions", {}).get("perplexity_proxy", {}).get("stats", {})
    ttr = perplexity_stats.get("ttr", 1)
    normalized_entropy = perplexity_stats.get("normalized_entropy", 1)

    score = max(probability_curve, entropy, layout)
    evidence = []
    if weak >= 90 and concrete < 45 and char_count <= 700:
        score = max(score, 62)
        evidence.append(f"短片段弱模型曲线过稳且具体密度 {concrete}/千字偏低")
    elif weak >= 90 and concrete >= 45 and action >= 18 and char_count <= 700:
        score = max(score, 42)
        evidence.append(f"弱模型曲线过稳，但具体物/动作密度较高，按人工锚点降权")
    if weak >= 80 and proportion >= 0.50 and concrete < 45 and ttr <= 0.42:
        score = max(score, 86)
        evidence.append(f"长尾片段占比 {proportion:.3f}，弱模型曲线过稳，TTR={ttr} 且具体密度偏低")
    elif weak >= 80 and concrete < 45:
        score = max(score, weak * 0.65)
        evidence.append(f"弱模型曲线过稳且具体密度 {concrete}/千字偏低")
    if normalized_entropy < 0.93 and weak >= 70:
        score = max(score, 72)
        evidence.append(f"片段归一化熵 {normalized_entropy} 偏低，且弱模型曲线过稳")
    human_anchor = stats.get("human_anchor", {})
    anchor_type = human_anchor.get("anchor_type", "")
    narrative_like = (
        anchor_type == "narrative_scene"
        or dialogue_ratio >= 0.10
        or action_density + sensory_density >= 15
    )
    if char_count >= 1800 and proportion >= 0.95 and narrative_like and anchor_type != "technical_expository":
        raw_curve = max(raw_probability_curve, raw_entropy, raw_weak)
        if raw_curve >= 80:
            external_like_score = min(86.0, max(76.0, 62.0 + raw_curve * 0.20))
            if external_like_score > score:
                score = external_like_score
                evidence.append(
                    f"整章单段疑似朱雀形态：曲线原始高值 {raw_curve:.2f}%，本地不再用小说人工锚点压低整段风险"
                )
    blockers = human_anchor.get("blockers", [])
    length_only_blocker = blockers and all(str(item).startswith("文本长度不足") for item in blockers)
    segment_anchor_eligible = human_anchor.get("eligible") or (
        length_only_blocker
        and char_count >= 420
        and float(human_anchor.get("score", 0)) >= 52
    )
    if segment_anchor_eligible:
        if human_anchor.get("eligible"):
            cap = float(human_anchor.get("segment_cap", 100))
        else:
            cap = 34.0 if float(human_anchor.get("score", 0)) >= 72 else 48.0
        if score > cap:
            score = cap
            metrics = human_anchor.get("metrics", {})
            evidence.append(
                "片段具备高质人工锚点，按正样本校准降权："
                f"场景密度 {metrics.get('scene_density_per_k', 0)}/千字，"
                f"句长CV {metrics.get('sentence_cv', 0)}，段长CV {metrics.get('paragraph_cv', 0)}"
            )
    return round(clamp(score / 100) * 100, 2), evidence


def zhuque_segment_proxy(raw: str) -> dict:
    body = strip_markdown_titles(raw)
    visible = "".join(ch for ch in body if not ch.isspace())
    bounds = zhuque_like_segment_bounds(len(visible))
    if not bounds:
        return empty_zhuque_segment_proxy()

    segments = []
    suspected_chars = 0
    ai_chars = 0
    max_score = 0
    max_index = 0
    for index, (start, end) in enumerate(bounds, 1):
        chunk = visible[start:end]
        if not chunk:
            continue
        report = analyze_text(chunk, include_segments=False)
        proportion = len(chunk) / max(len(visible), 1)
        score, evidence = segment_aigc_proxy(report, len(chunk), proportion)
        if score >= 99:
            ai_chars += len(chunk)
        elif score >= 50:
            suspected_chars += len(chunk)
        if score > max_score:
            max_score = score
            max_index = index
        segments.append(
            {
                "index": index,
                "start": start,
                "end": end,
                "char_count": len(chunk),
                "proportion": round(proportion, 4),
                "aigc_percent": score,
                "category": "AI特征" if score >= 99 else ("疑似AI" if score >= 50 else "人工特征"),
                "evidence": evidence,
                "local_aigc_percent": report.get("aigc_percent", 0),
                "weak_lm_score": report.get("latest_detector_proxy", {}).get("components", {}).get("weak_lm_uniformity", {}).get("score", 0),
                "concrete_density_per_k": report.get("stats", {}).get("concrete_density_per_k", 0),
            }
        )

    suspected_ratio = round(suspected_chars / max(len(visible), 1) * 100, 2)
    ai_ratio = round(ai_chars / max(len(visible), 1) * 100, 2)
    human_ratio = round(100 - suspected_ratio - ai_ratio, 2)
    risk_floor = 0
    if len(segments) == 1 and suspected_ratio >= 99 and max_score >= 50:
        risk_floor = max_score
    elif max_score >= 80 and suspected_ratio >= 60:
        risk_floor = round(suspected_ratio * 0.90, 2)
    elif max_score >= 60 and suspected_ratio >= 35:
        risk_floor = round(suspected_ratio * 0.70, 2)
    return {
        "enabled": True,
        "segments": segments,
        "suspected_ai_ratio_percent": suspected_ratio,
        "human_ratio_percent": human_ratio,
        "ai_feature_ratio_percent": ai_ratio,
        "max_segment_percent": max_score,
        "max_segment_index": max_index,
        "risk_floor_percent": risk_floor,
        "note": "按朱雀报告校准的本地分片代理：输出疑似AI占比和最高风险片段，用于防止全文平均稀释长尾高风险。",
    }


def score_zhuque_segment_proxy(segment_proxy: dict) -> dict:
    signals = []
    suspected_ratio = segment_proxy.get("suspected_ai_ratio_percent", 0)
    max_score = segment_proxy.get("max_segment_percent", 0)
    max_index = segment_proxy.get("max_segment_index", 0)
    if suspected_ratio >= 60 and max_score >= 80:
        signals.append(
            risk_item(
                "zhuque_like_suspected_span_ratio_high",
                suspected_ratio / 100,
                f"疑似AI片段占比 {suspected_ratio}% 且最高片段 {max_index} 为 {max_score}%",
            )
        )
    elif suspected_ratio >= 35 or max_score >= 70:
        signals.append(
            risk_item(
                "zhuque_like_suspected_span_ratio_mid",
                max(suspected_ratio, max_score) / 100,
                f"疑似AI片段占比 {suspected_ratio}%；最高片段 {max_index} 为 {max_score}%",
            )
        )
    return {
        "name": "朱雀式分片代理",
        "score": round(max([item["score"] for item in signals], default=0), 2),
        "weight": LATEST_PROXY_WEIGHTS["zhuque_segment_proxy"],
        "stats": {
            "suspected_ai_ratio_percent": suspected_ratio,
            "human_ratio_percent": segment_proxy.get("human_ratio_percent", 0),
            "ai_feature_ratio_percent": segment_proxy.get("ai_feature_ratio_percent", 0),
            "max_segment_percent": max_score,
            "max_segment_index": max_index,
            "risk_floor_percent": segment_proxy.get("risk_floor_percent", 0),
            "segments": segment_proxy.get("segments", []),
        },
        "signals": signals,
    }


def latest_detector_proxy(
    body: str,
    chars: list[str],
    sents: list[str],
    sent_lens: list[int],
    per_k: dict[str, float],
    concrete_density: float,
    punctuation: dict,
    dialogue_ratio: float,
    fragment_stats: dict,
    segment_proxy: dict | None = None,
    human_anchor: dict | None = None,
) -> dict:
    curve_body = normalize_detector_curve_text(body)
    curve_sents = split_sentences(curve_body)
    curve_chars = hanzi(curve_body)
    noise_stats = detector_noise_stats(body)
    surprisal = local_surprisal_stats(curve_sents)
    entropy = window_entropy_stats(curve_chars)
    segment_proxy = segment_proxy or empty_zhuque_segment_proxy()
    components = {
        "probability_curvature_proxy": score_probability_curvature_proxy(surprisal, entropy),
        "weak_lm_uniformity": score_weak_lm_uniformity(surprisal),
        "local_entropy_uniformity": score_local_entropy_uniformity(entropy),
        "stylometry_readability": score_stylometry_readability(sent_lens, punctuation, dialogue_ratio, len(chars)),
        "semantic_smoothing": score_semantic_smoothing(body, len(chars), concrete_density, per_k),
        "layout_humanizer_fingerprint": score_layout_humanizer_fingerprint(fragment_stats),
        "content_integrity": score_content_integrity(noise_stats),
        "zhuque_segment_proxy": score_zhuque_segment_proxy(segment_proxy),
    }
    human_anchor = human_anchor or {}
    components = apply_human_anchor_calibration(components, human_anchor)
    composite = sum(item["score"] * item["weight"] for item in components.values())
    return {
        "composite_percent": round(composite, 2),
        "weights": LATEST_PROXY_WEIGHTS,
        "note": "近年检测器常把句级分类、弱模型概率/熵、概率曲率、风格计量、语义平滑和句段级布局指纹融合；本地用可复算代理特征近似，不调用外部模型。拟声词/重复声响和无语义脏码会先中和后再计算曲线，且脏码长串会进入内容完整性风险。若文本同时命中高质量人工样本锚点，会对曲线类特征做误判降权，但不覆盖内容完整性、真重复和空泛概括风险。",
        "detector_noise": noise_stats,
        "human_anchor": human_anchor,
        "components": components,
    }


def zhuque_dimensions(body: str, chars: list[str], sent_lens: list[int], paras: list[str], para_lens: list[int], short_sentence_ratio: float, total_cliche: float, per_k: dict[str, float], concrete_density: float, repeated_12_extra: int, fragment_stats: dict | None = None) -> dict:
    rows = paragraph_feature_rows(paras)
    dimensions = {
        "burstiness": score_burstiness(sent_lens, para_lens, short_sentence_ratio, fragment_stats),
        "perplexity_proxy": score_perplexity_proxy(chars, total_cliche, per_k, concrete_density, repeated_12_extra),
        "structure_fingerprint": score_structure_fingerprint(body, paras, per_k, fragment_stats),
        "cross_paragraph_consistency": score_cross_paragraph_consistency(rows),
    }
    composite = sum(item["score"] * item["weight"] for item in dimensions.values())
    return {
        "composite_percent": round(composite, 2),
        "weights": DIMENSION_WEIGHTS,
        "dimensions": dimensions,
    }


def top_aigc_signals(zhuque: dict, latest_proxy: dict, limit: int = 8) -> list[dict]:
    rows = []
    for source_name, group in [
        ("朱雀四维", zhuque.get("dimensions", {})),
        ("近年检测器代理", latest_proxy.get("components", {})),
    ]:
        for component in group.values():
            for signal in component.get("signals", []):
                rows.append(
                    {
                        "source": source_name,
                        "dimension": component.get("name", ""),
                        "name": signal.get("name", ""),
                        "score": signal.get("score", 0),
                        "evidence": signal.get("evidence", ""),
                    }
                )
    return sorted(rows, key=lambda item: item["score"], reverse=True)[:limit]


def analyze_text(text: str, include_segments: bool = True) -> dict:
    raw = text or ""
    body = strip_markdown_titles(raw)
    chars = hanzi(body)
    n_hanzi = len(chars)
    sents = split_sentences(body)
    sent_lens = [len(hanzi(sent)) for sent in sents]
    cv = coefficient_of_variation(sent_lens)
    avg_sent_len = sum(sent_lens) / max(len(sent_lens), 1)
    short_sentence_ratio = round(sum(1 for length in sent_lens if length <= 8) / max(len(sent_lens), 1), 3)

    paras = paragraphs(raw)
    fragment_stats = paragraph_fragmentation_stats(paras)
    para_lens = [len(hanzi(para)) for para in paras if len(hanzi(para)) > 0]
    para_cv = coefficient_of_variation(para_lens)
    dialogue_lines = sum(1 for line in body.splitlines() if "“" in line or "\"" in line)
    dialogue_ratio = round(dialogue_lines / max(len([line for line in body.splitlines() if line.strip()]), 1), 3)

    per_k, total_cliche = cliche_densities(body, n_hanzi)
    rep8 = repeated_ngrams(chars, 8)
    rep12 = repeated_ngrams(chars, 12)
    dup_report = duplicate_paragraph_report(raw)
    noise_stats = detector_noise_stats(body)

    punctuation = {
        "comma": body.count("，"),
        "period": body.count("。"),
        "dash": body.count("——"),
        "ellipsis": body.count("…") + body.count("......"),
    }
    comma_period_ratio = round(punctuation["comma"] / max(punctuation["period"], 1), 2)
    concrete_count = sum(body.count(item) for item in CONCRETE_HINTS) + len(re.findall(r"\d", body))
    concrete_density = density(concrete_count, n_hanzi)
    action_density = density(count_markers(body, ACTION_HINTS), n_hanzi)
    sensory_density = density(count_markers(body, SENSORY_HINTS), n_hanzi)
    emotion_density = density(count_markers(body, EMOTION_WORDS), n_hanzi)
    abstract_density = density(count_markers(body, ABSTRACT_HINTS), n_hanzi)
    human_anchor = high_quality_human_anchor_stats(
        body,
        n_hanzi,
        sent_lens,
        para_lens,
        dialogue_ratio,
        concrete_density,
        per_k,
        total_cliche,
        dup_report,
        noise_stats,
        fragment_stats,
    )

    reasons = []
    human_signals = []
    score = 0.0

    if cv and cv < 0.42:
        score += add_reason(reasons, 0.22, "句长突发度", f"CV={cv:.3f}，句长过于均匀")
    elif cv and cv < 0.55:
        score += add_reason(reasons, 0.14, "句长突发度", f"CV={cv:.3f}，节奏偏平")
    elif cv and cv < 0.70:
        score += add_reason(reasons, 0.06, "句长突发度", f"CV={cv:.3f}，节奏起伏仍偏弱")

    cliche_score = min(0.24, total_cliche / 12 * 0.24)
    score += add_reason(reasons, cliche_score, "套路密度", f"套路措辞密度 {total_cliche}/千字")

    high_categories = [name for name, value in per_k.items() if value >= 1.0]
    if len(high_categories) >= 4:
        score += add_reason(reasons, 0.12, "多类套路并发", "高密度类别：" + "、".join(high_categories[:6]))
    elif len(high_categories) >= 2:
        score += add_reason(reasons, 0.06, "多类套路并发", "偏高类别：" + "、".join(high_categories[:4]))

    if per_k.get("解释归纳", 0) >= 0.8:
        score += add_reason(reasons, 0.10, "解释归纳腔", f"解释归纳密度 {per_k['解释归纳']}/千字")
    if per_k.get("平滑转场", 0) >= 1.2:
        score += add_reason(reasons, 0.07, "平滑转场", f"平滑转场密度 {per_k['平滑转场']}/千字")
    if per_k.get("工程泄漏", 0) > 0:
        score += add_reason(reasons, 0.30, "工程词泄漏", "正文出现写作工程词或提示词语汇")

    rep12_extra = sum(count - 1 for _, count in rep12)
    rep8_extra = sum(count - 1 for _, count in rep8)
    if rep12_extra >= 8:
        score += add_reason(reasons, 0.14, "长片段重复", f"12字级额外重复 {rep12_extra} 次")
    elif rep12_extra >= 3:
        score += add_reason(reasons, 0.07, "长片段重复", f"12字级额外重复 {rep12_extra} 次")
    elif rep8_extra >= 20:
        score += add_reason(reasons, 0.05, "短片段重复", f"8字级额外重复 {rep8_extra} 次")

    if dup_report["exact_count"] or dup_report["similar_count"]:
        score += add_reason(
            reasons,
            0.35,
            "段落级复述",
            f"完全重复 {dup_report['exact_count']} 组，高相似 {dup_report['similar_count']} 组",
        )
    if dup_report["sentence_duplicate_count"]:
        score += add_reason(reasons, 0.12, "重复长句", f"重复长句 {dup_report['sentence_duplicate_count']} 组")

    if comma_period_ratio >= 3.2:
        score += add_reason(reasons, 0.08, "标点节奏", f"逗句比 {comma_period_ratio}，长句拖拽感强")
    if n_hanzi >= 300:
        dash_ellipsis_density = density(punctuation["dash"] + punctuation["ellipsis"], n_hanzi)
        if dash_ellipsis_density >= 3:
            score += add_reason(reasons, 0.06, "语气符号", f"破折号/省略号密度 {dash_ellipsis_density}/千字")

    if para_cv and para_cv < 0.45 and len(para_lens) >= 8:
        score += add_reason(reasons, 0.08, "段落均匀度", f"段长 CV={para_cv:.3f}，段落过于整齐")
    if concrete_density < 0.4 and n_hanzi >= 800:
        score += add_reason(reasons, 0.06, "具体物密度", f"数字/生活物件密度 {concrete_density}/千字，细节偏可替换")
    if short_sentence_ratio < 0.08 and len(sents) >= 20:
        score += add_reason(reasons, 0.04, "短句比例", f"短句比例 {short_sentence_ratio}，缺少自然断气")
    if dialogue_ratio < 0.08 and n_hanzi >= 800:
        score += add_reason(reasons, 0.03, "对话比例", f"对话行比例 {dialogue_ratio}，叙述铺陈偏多")

    positive_score = score
    if dialogue_ratio >= 0.25:
        score -= 0.05
        human_signals.append(f"对话行比例 {dialogue_ratio}，有一定人物声口")
    if short_sentence_ratio >= 0.20:
        score -= 0.04
        human_signals.append(f"短句比例 {short_sentence_ratio}，节奏有断裂")
    if concrete_density >= 1.5:
        score -= 0.04
        human_signals.append(f"具体物/数字密度 {concrete_density}/千字")
    if para_cv >= 0.85:
        score -= 0.03
        human_signals.append(f"段长 CV={para_cv:.3f}，段落疏密有变化")

    if positive_score > 0:
        score = max(score, positive_score * 0.35)
    legacy_score = min(1.0, max(0.0, score))
    legacy_percent = round(legacy_score * 100, 2)
    zhuque = zhuque_dimensions(body, chars, sent_lens, paras, para_lens, short_sentence_ratio, total_cliche, per_k, concrete_density, rep12_extra, fragment_stats)
    segment_proxy = zhuque_segment_proxy(raw) if include_segments else empty_zhuque_segment_proxy()
    latest_proxy = latest_detector_proxy(body, chars, sents, sent_lens, per_k, concrete_density, punctuation, dialogue_ratio, fragment_stats, segment_proxy, human_anchor)
    blended_percent = round(
        (zhuque["composite_percent"] * FINAL_BLEND_WEIGHTS["zhuque_four_dimensions"])
        + (latest_proxy["composite_percent"] * FINAL_BLEND_WEIGHTS["latest_detector_proxy"])
        + (legacy_percent * FINAL_BLEND_WEIGHTS["legacy_heuristic"]),
        2,
    )
    content_floor = 0.0
    if noise_stats.get("semantic_noise_runs", 0):
        content_floor = 100.0 if noise_stats.get("semantic_noise_ratio", 0) >= 0.015 or noise_stats.get("semantic_noise_hanzi", 0) >= 90 else 82.0
    human_anchor_final_cap = None
    anchor_metrics = human_anchor.get("metrics", {}) if isinstance(human_anchor, dict) else {}
    rep12_tolerated_by_anchor = rep12_extra == 0 or (
        rep12_extra <= 7000
        and anchor_metrics.get("scene_density_per_k", 0) >= 28
        and anchor_metrics.get("dialogue_ratio", 0) >= 0.25
        and anchor_metrics.get("quote_density_per_k", 0) >= 8
    )
    if (
        content_floor == 0.0
        and human_anchor.get("eligible")
        and human_anchor.get("strength") == "strong"
        and human_anchor.get("final_cap_allowed")
        and float(human_anchor.get("score", 0)) >= 88
        and not human_anchor.get("blockers")
        and total_cliche <= 7
        and rep12_tolerated_by_anchor
        and not dup_report.get("exact_count", 0)
        and not dup_report.get("similar_count", 0)
        and not dup_report.get("sentence_duplicate_count", 0)
    ):
        human_anchor_final_cap = 4.8
        blended_percent = round(min(blended_percent, human_anchor_final_cap), 2)
    percent = round(max(blended_percent, segment_proxy.get("risk_floor_percent", 0), content_floor), 2)
    score = percent / 100
    return {
        "engine": ENGINE,
        "aigc_value": round(score, 4),
        "aigc_percent": percent,
        "ai_ratio_percent": percent,
        "blended_aigc_percent": blended_percent,
        "segment_risk_floor_percent": segment_proxy.get("risk_floor_percent", 0),
        "content_integrity_floor_percent": content_floor,
        "zhuque_dimensions": zhuque,
        "latest_detector_proxy": latest_proxy,
        "zhuque_segment_proxy": segment_proxy,
        "legacy_heuristic_percent": legacy_percent,
        "final_blend_weights": FINAL_BLEND_WEIGHTS,
        "human_anchor_final_cap_percent": human_anchor_final_cap,
        "risk_label": label_for(percent),
        "confidence": confidence_for(n_hanzi, len(reasons)),
        "stats": {
            "hanzi": n_hanzi,
            "sentences": len(sents),
            "avg_sentence_len": round(avg_sent_len, 1),
            "sentence_cv": round(cv, 3),
            "short_sentence_ratio": short_sentence_ratio,
            "paragraph_count": len(paras),
            "paragraph_cv": round(para_cv, 3),
            "paragraph_fragmentation": fragment_stats,
            "detector_noise": noise_stats,
            "dialogue_ratio": dialogue_ratio,
            "cliche_total_per_k": total_cliche,
            "cliche_per_k": per_k,
            "concrete_density_per_k": concrete_density,
            "action_density_per_k": action_density,
            "sensory_density_per_k": sensory_density,
            "emotion_density_per_k": emotion_density,
            "abstract_density_per_k": abstract_density,
            "human_anchor": human_anchor,
            "comma_period_ratio": comma_period_ratio,
            "repeated_8_extra": rep8_extra,
            "repeated_12_extra": rep12_extra,
            "paragraph_duplicates": dup_report,
        },
        "top_aigc_signals": top_aigc_signals(zhuque, latest_proxy),
        "reasons": reasons[:10],
        "human_signals": human_signals[:6],
    }


def main() -> None:
    parser = argparse.ArgumentParser(description="本地自研 AIGC 值检测器")
    parser.add_argument("path")
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--target", type=float, default=5.0)
    args = parser.parse_args()

    text = Path(args.path).read_text(encoding="utf-8")
    report = analyze_text(text)
    target_percent = args.target * 100 if 0 < args.target <= 1 else args.target
    report["target"] = target_percent
    report["passed"] = report["aigc_percent"] <= target_percent

    if args.json:
        print(json.dumps(report, ensure_ascii=False, indent=2))
        return

    print(f"自研AIGC值: {report['aigc_value']:.4f} ({report['aigc_percent']:.2f}%)")
    print(f"风险等级: {report['risk_label']}  置信度: {report['confidence']}  引擎: {report['engine']}")
    print(f"门控: {'通过' if report['passed'] else '不通过'} (目标 ≤ {target_percent:g}%)")
    print("关键统计:")
    stats = report["stats"]
    print(f"  汉字 {stats['hanzi']} | 句长CV {stats['sentence_cv']} | 段长CV {stats['paragraph_cv']} | 套路密度 {stats['cliche_total_per_k']}/千字")
    print(f"  短句比例 {stats['short_sentence_ratio']} | 对话比例 {stats['dialogue_ratio']} | 具体物密度 {stats['concrete_density_per_k']}/千字")
    anchor = stats.get("human_anchor") or {}
    if anchor:
        print(
            f"  人工锚点 {anchor.get('score', 0)} | {anchor.get('strength', 'none')} | "
            f"{'启用校准' if anchor.get('eligible') else '未启用校准'}"
        )
    frag = stats.get("paragraph_fragmentation") or {}
    if frag:
        print(f"  段落碎片: 单句段 {frag['single_sentence_paragraph_ratio']} | 短段≤12字 {frag['short_paragraph_ratio']} | 条款块 {frag['bracket_line_ratio']}")
    noise = stats.get("detector_noise") or {}
    if noise.get("sound_noise_runs"):
        print(
            f"  曲线去噪: 拟声噪声 {noise['sound_noise_runs']} 段 | "
            f"汉字 {noise['sound_noise_hanzi']} | 占比 {noise['sound_noise_ratio'] * 100:.2f}%"
        )
    if noise.get("semantic_noise_runs"):
        print(
            f"  内容噪声: 无语义脏码 {noise['semantic_noise_runs']} 段 | "
            f"汉字 {noise['semantic_noise_hanzi']} | 占比 {noise['semantic_noise_ratio'] * 100:.2f}%"
        )
    print("朱雀四维代理分:")
    for key, item in report["zhuque_dimensions"]["dimensions"].items():
        print(f"  {item['name']}: {item['score']:.2f}% (weight {item['weight']:.2f})")
    print("近年检测器代理层:")
    for key, item in report["latest_detector_proxy"]["components"].items():
        print(f"  {item['name']}: {item['score']:.2f}% (weight {item['weight']:.2f})")
    print(
        f"  四维综合: {report['zhuque_dimensions']['composite_percent']:.2f}% | "
        f"最新代理综合: {report['latest_detector_proxy']['composite_percent']:.2f}% | "
        f"既有启发式: {report['legacy_heuristic_percent']:.2f}% | "
        f"分片下限: {report.get('segment_risk_floor_percent', 0):.2f}%"
    )
    seg = report.get("zhuque_segment_proxy") or {}
    if seg.get("enabled"):
        print("朱雀式分片代理:")
        print(
            f"  疑似AI占比 {seg['suspected_ai_ratio_percent']:.2f}% | "
            f"人工占比 {seg['human_ratio_percent']:.2f}% | "
            f"AI特征占比 {seg['ai_feature_ratio_percent']:.2f}% | "
            f"最高片段 {seg['max_segment_index']}={seg['max_segment_percent']:.2f}%"
        )
        for item in seg.get("segments", [])[:6]:
            print(
                f"  片段{item['index']}: {item['category']} {item['aigc_percent']:.2f}% "
                f"占比 {item['proportion'] * 100:.2f}% 字符 {item['char_count']}"
            )
    if report["top_aigc_signals"]:
        print("主要AIGC风险:")
        for item in report["top_aigc_signals"][:6]:
            print(f"  {item['score']:.2f}% [{item['dimension']}/{item['name']}] {item['evidence']}")
    if report["reasons"]:
        print("强信号:")
        for item in report["reasons"]:
            print(f"  +{item['分值']:.3f} [{item['特征']}] {item['原因']}")
    if report["human_signals"]:
        print("反向人味信号:")
        for item in report["human_signals"]:
            print(f"  - {item}")


if __name__ == "__main__":
    main()
