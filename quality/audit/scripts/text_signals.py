#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
text_signals.py — 复刻并扩充 review/SKILL.md 的量化信号

输出：
  1. 句长突发度 CV
  2. 用字多样度 TTR
  3. 套路措辞密度（按类别）
  4. 重复长片段（8/12 字级）
  5. 标点习惯
  6. 段落级真重复（独立段落间的逐字重复，发布系统级别）
  7. AI 味风险分（本地信号分 + 可选外部 AIGC 分）
  8. 自研 AIGC 值（codex-local-aigc-v4）

用法：
  python3 text_signals.py <文本文件> [--json] [--min-dup-len N] [--min-dup-distance N]
  python3 text_signals.py <文本文件> --external-aigc 0.8252

重要：本脚本不替用户做高风险定性判决，输出仅为概率性信号。
"""
import re, sys, json
from collections import Counter

try:
    from aigc_value import analyze_text as analyze_aigc_text
except Exception:
    analyze_aigc_text = None


CLICHES = {
    "时间锚点": ["那一刻","那一瞬","就在这时","刹那间","一瞬间","顷刻间"],
    "微表情":   ["嘴角","眼眸","眼角","眼底","眸子","勾起","扬起一抹","抿了抿"],
    "情绪命名": ["百感交集","五味杂陈","一种说不出","复杂的情绪","莫名的","难以言喻","心如刀绞","痛不欲生"],
    "金句升华": ["原来","或许","也许就是","所谓","有些","这就是","人生就是","真正的选择","真正的答案","最终的选择","最终答案"],
    "陈词意象": ["月光如水","时间仿佛凝固","命运的齿轮","如潮水般","似乎","仿佛","宛如","犹如"],
    "解释归纳": ["这让他意识到","这让她意识到","终于明白","不再是","而是","不仅仅是","更是","这意味着","换句话说"],
    "平滑转场": ["然而","与此同时","紧接着","随后","片刻后","很快","没过多久"],
    "工程泄漏": ["本章","细纲","大纲","设定","爽点","情绪爆发","关系升级","伏笔回收","节奏点"],
}


def load_text(path):
    raw = open(path, encoding="utf-8").read()
    # 去掉 markdown 标题行
    body = "\n".join(l for l in raw.splitlines() if not l.startswith("#"))
    return raw, body


def char_han(text):
    return re.findall(r"[一-鿿]", text)


def burstiness_cv(text):
    """句长突发度 CV（高低差）。"""
    sents = [s for s in re.split(r"[。！？\n]", text) if re.search(r"[一-鿿]", s)]
    lens = [len(re.findall(r"[一-鿿]", s)) for s in sents]
    if not lens:
        return 0.0, 0
    mean = sum(lens) / len(lens)
    var = sum((x - mean) ** 2 for x in lens) / len(lens)
    return (var ** 0.5) / mean, len(sents)


def ttr(hanzi):
    return len(set(hanzi)) / max(len(hanzi), 1)


def cliche_density(body, hanzi_count):
    per_k = {}
    for cat, words in CLICHES.items():
        per_k[cat] = round(sum(body.count(w) for w in words) / max(hanzi_count, 1) * 1000, 2)
    return per_k, round(sum(per_k.values()), 2)


def repeated_ngrams(hanzi, n, min_repeats=2):
    """找 n 字级重复片段（仅做内省，需配合 min_repeats 避免单次巧合）。"""
    seq = "".join(hanzi)
    cnt = Counter(seq[i:i+n] for i in range(len(seq) - n + 1))
    return sorted(((g, c) for g, c in cnt.items() if c >= min_repeats), key=lambda x: -x[1])


def punct_stats(text):
    return {
        "逗号":   text.count("，"),
        "句号":   text.count("。"),
        "破折号": text.count("——"),
        "省略号": text.count("…") + text.count("......"),
    }


def normalize_aigc_score(value):
    """腾讯朱雀等外部检测常输出 0-1 小数；这里统一换算成百分比。"""
    if value is None or value == "":
        return None
    try:
        score = float(str(value).strip().rstrip("%"))
    except ValueError:
        return None
    if 0 <= score <= 1:
        score *= 100
    return round(score, 2)


def risk_label(score):
    if score >= 70:
        return "高"
    if score >= 40:
        return "中"
    if score >= 20:
        return "低-中"
    return "低"


def ai_taste_risk(n_han, cv, total_cliche, per_k, rep8_all, rep12_all, punct, paragraph_report, sent_dups, external_percent=None, local_aigc_percent=None):
    score = 0
    reasons = []

    def add(points, reason):
        nonlocal score
        score += points
        reasons.append({"加分": points, "原因": reason})

    if n_han < 300:
        reasons.append({"加分": 0, "原因": "文本少于 300 汉字，AI 味判断置信度低"})

    if cv and cv < 0.45:
        add(18, f"句长突发度 CV={cv:.3f}，节奏过平")
    elif cv and cv < 0.60:
        add(10, f"句长突发度 CV={cv:.3f}，节奏起伏偏弱")

    if total_cliche >= 8:
        add(25, f"套路措辞密度 {total_cliche}/千字，强烈偏高")
    elif total_cliche >= 4:
        add(16, f"套路措辞密度 {total_cliche}/千字，偏高")
    elif total_cliche >= 2:
        add(8, f"套路措辞密度 {total_cliche}/千字，轻度偏高")

    high_categories = [name for name, value in per_k.items() if value >= 1.0]
    if len(high_categories) >= 4:
        add(12, "多类 AI 套路同时出现：" + "、".join(high_categories[:6]))
    elif len(high_categories) >= 2:
        add(6, "多类套路并发：" + "、".join(high_categories[:4]))

    if per_k.get("工程泄漏", 0) > 0:
        add(25, "正文疑似混入工程词/提示词语汇")

    rep12_extra = sum(c - 1 for _, c in rep12_all)
    rep8_extra = sum(c - 1 for _, c in rep8_all)
    if rep12_extra >= 8:
        add(14, f"12 字级重复片段额外重复 {rep12_extra} 次")
    elif rep12_extra >= 3:
        add(7, f"12 字级重复片段额外重复 {rep12_extra} 次")
    elif rep8_extra >= 20:
        add(5, f"8 字级重复片段额外重复 {rep8_extra} 次")

    exact_count = len(paragraph_report["exact_duplicates"])
    similar_count = len(paragraph_report["similar_pairs"])
    sent_count = len(sent_dups)
    if exact_count or similar_count:
        add(35, f"段落级真重复：完全 {exact_count} 组，高相似 {similar_count} 组")
    if sent_count:
        add(12, f"重复长句 {sent_count} 组")

    comma_period_ratio = punct["逗号"] / max(punct["句号"], 1)
    if comma_period_ratio >= 3.2:
        add(8, f"逗句比 {comma_period_ratio:.2f}，长句拖拽感偏强")
    if n_han >= 300:
        dash_ellipsis_per_k = (punct["破折号"] + punct["省略号"]) / max(n_han, 1) * 1000
        if dash_ellipsis_per_k >= 3:
            add(6, f"破折号/省略号密度 {dash_ellipsis_per_k:.2f}/千字，语气符号模式化")

    local_score = min(100, round(score))
    combined_score = local_score
    if local_aigc_percent is not None:
        if local_aigc_percent >= 35:
            combined_score = max(combined_score, round(local_aigc_percent))
            reasons.append({"加分": 0, "原因": f"自研 AIGC 检测 {local_aigc_percent}% 为高风险硬信号"})
        elif local_aigc_percent >= 4:
            combined_score = max(combined_score, round(local_aigc_percent))
            reasons.append({"加分": 0, "原因": f"自研 AIGC 检测 {local_aigc_percent}% 未过严格 <4% 门禁"})
    if external_percent is not None:
        if external_percent >= 60:
            combined_score = max(combined_score, round(external_percent))
            reasons.append({"加分": 0, "原因": f"外部 AIGC 检测 {external_percent}% 为高风险硬信号"})
        elif external_percent >= 20:
            combined_score = max(combined_score, round(external_percent))
            reasons.append({"加分": 0, "原因": f"外部 AIGC 检测 {external_percent}% 需要复核"})
        elif external_percent >= 4:
            combined_score = max(combined_score, round(external_percent))
            reasons.append({"加分": 0, "原因": f"外部 AIGC 检测 {external_percent}% 未过严格 <4% 门禁"})

    return {
        "本地信号分": local_score,
        "本地风险等级": risk_label(local_score),
        "综合风险分": min(100, combined_score),
        "综合风险等级": risk_label(min(100, combined_score)),
        "主要原因": reasons[:8],
    }


def paragraph_duplicates(raw, min_dup_len=40, min_dup_distance=100):
    """
    段落级真重复检测（发布系统级别）。

    关键区别于 n-gram 滑窗：本函数按段落切分，
    只在**不同段落**、**间隔 >= min_dup_distance 字符**之间找重复。
    这样能过滤掉"同一段内部滑窗产生的伪重复"。

    返回：[(para_index_a, para_index_b, snippet, distance_chars), ...]
    """
    paragraphs = [p.strip() for p in re.split(r"\n\n+", raw) if p.strip() and not p.strip().startswith("#")]

    # 找完全重复段落
    counter = Counter(paragraphs)
    exact = [(p, c) for p, c in counter.items() if c > 1 and len(p) > 20]

    # 找高度相似段落（去标点前 80 字相同，且在文章不同位置）
    def norm(p):
        return re.sub(r"[^一-龥]", "", p)[:80]

    seen_norm = {}
    similar = []
    para_offsets = []  # 段落起始字符位置
    pos = 0
    for p in paragraphs:
        para_offsets.append(pos)
        pos += len(p) + 2  # +2 for the "\n\n" separator

    for i, p in enumerate(paragraphs):
        k = norm(p)
        if not k or len(k) < 15:
            continue
        if k in seen_norm:
            j = seen_norm[k]
            if abs(para_offsets[i] - para_offsets[j]) >= min_dup_distance:
                similar.append((j, i, p, abs(para_offsets[i] - para_offsets[j])))
        else:
            seen_norm[k] = i

    return {
        "paragraph_count": len(paragraphs),
        "exact_duplicates": exact,
        "similar_pairs": similar,
    }


def sentence_duplicates(raw, min_len=20):
    """完全重复句子（> min_len 字符）。发布系统常用级别。"""
    sents = [s.strip() for s in re.split(r"[。！？\n]", raw) if s.strip() and len(s.strip()) > min_len]
    counter = Counter(sents)
    return [(s, c) for s, c in counter.items() if c > 1]


def main():
    args = sys.argv[1:]
    json_out = "--json" in args
    args = [a for a in args if a != "--json"]

    min_dup_len = 40
    min_dup_distance = 100
    external_aigc = None
    cleaned_args = []
    skip_next = False
    for i, a in enumerate(args):
        if skip_next:
            skip_next = False
            continue
        if a == "--min-dup-len":
            min_dup_len = int(args[i+1])
            skip_next = True
        elif a == "--min-dup-distance":
            min_dup_distance = int(args[i+1])
            skip_next = True
        elif a == "--external-aigc":
            external_aigc = args[i+1]
            skip_next = True
        else:
            cleaned_args.append(a)
    args = cleaned_args

    if not args:
        print("用法: python3 text_signals.py <文本文件> [--json] [--min-dup-len N] [--min-dup-distance N] [--external-aigc 0.8252]")
        sys.exit(1)
    path = args[0]
    raw, body = load_text(path)
    hanzi = char_han(body)
    n_han = len(hanzi)

    cv, n_sents = burstiness_cv(body)
    per_k, total_cliche = cliche_density(body, n_han)
    rep8_all = repeated_ngrams(hanzi, 8)
    rep12_all = repeated_ngrams(hanzi, 12)
    rep8 = rep8_all[:8]
    rep12 = rep12_all[:5]
    punct = punct_stats(body)
    pd = paragraph_duplicates(raw, min_dup_len, min_dup_distance)
    sents_dup = sentence_duplicates(raw)
    external_percent = normalize_aigc_score(external_aigc)
    local_aigc = analyze_aigc_text(raw) if analyze_aigc_text else None
    local_aigc_percent = local_aigc.get("aigc_percent") if isinstance(local_aigc, dict) else None
    risk = ai_taste_risk(n_han, cv, total_cliche, per_k, rep8_all, rep12_all, punct, pd, sents_dup, external_percent, local_aigc_percent)

    report = {
        "汉字数": n_han,
        "句子数": n_sents,
        "平均句长": round(n_han / max(n_sents, 1), 1),
        "句长突发度CV": round(cv, 3),
        "用字多样度TTR": round(ttr(hanzi), 3),
        "独立字": len(set(hanzi)),
        "套路措辞密度总(每千字)": total_cliche,
        "套路分项(每千字)": per_k,
        "重复8字片段(>=2次)": [{"片段": g, "次数": c} for g, c in rep8],
        "重复12字片段(>=2次)": [{"片段": g, "次数": c} for g, c in rep12],
        "标点": {**punct, "逗句比": round(punct["逗号"] / max(punct["句号"], 1), 2)},
        "AI味风险": risk,
        "自研AIGC检测": local_aigc,
        "外部AIGC检测": {
            "原始值": external_aigc,
            "换算百分比": external_percent,
        } if external_aigc is not None else None,
        "段落级重复": {
            "段落总数": pd["paragraph_count"],
            "完全重复段落数": len(pd["exact_duplicates"]),
            "完全重复段落": [{"次数": c, "片段": p[:60] + "..."} for p, c in pd["exact_duplicates"][:5]],
            "高度相似段落组数": len(pd["similar_pairs"]),
            "高度相似段落": [{"段A": j+1, "段B": i+1, "字符距离": d, "片段": p[:60] + "..."} for j, i, p, d in pd["similar_pairs"][:5]],
        },
        "句子级重复": {
            "完全重复句子数": len(sents_dup),
            "完全重复句子": [{"次数": c, "片段": s[:60] + "..."} for s, c in sents_dup[:5]],
        },
    }

    if json_out:
        print(json.dumps(report, ensure_ascii=False, indent=2))
        return

    # 人类可读输出
    print(f"汉字数: {n_han}  句子数: {n_sents}  平均句长: {n_han/max(n_sents,1):.1f}")
    print(f"句长突发度 CV: {cv:.3f}    用字多样度 TTR: {ttr(hanzi):.3f}  (独立字 {len(set(hanzi))})")
    print(f"套路措辞密度(每千字): 总 {total_cliche}")
    for k, v in per_k.items():
        print(f"   - {k}: {v}")
    print(f"本地AI味风险分: {risk['本地信号分']}/100 ({risk['本地风险等级']})")
    print(f"综合AI味风险分: {risk['综合风险分']}/100 ({risk['综合风险等级']})")
    if local_aigc:
        print(f"自研AIGC值: {local_aigc['aigc_value']:.4f} ({local_aigc['aigc_percent']:.2f}%)  风险 {local_aigc['risk_label']}  置信度 {local_aigc['confidence']}")
        latest_proxy = local_aigc.get("latest_detector_proxy") or {}
        if latest_proxy:
            print(f"近年检测器代理综合: {latest_proxy.get('composite_percent', 0):.2f}%")
            for item in (latest_proxy.get("components") or {}).values():
                print(f"   - {item.get('name')}: {item.get('score', 0):.2f}%")
    if external_aigc is not None:
        print(f"外部AIGC检测: 原始值 {external_aigc} -> {external_percent}%")
    if risk["主要原因"]:
        print("AI味强信号:")
        for item in risk["主要原因"]:
            print(f"   +{item['加分']}: {item['原因']}")
    print(f"重复8字片段(>=2次): {len(rep8)} 种")
    for g, c in rep8:
        print(f"   {c}× 「{g}」")
    print(f"重复12字片段(>=2次): {len(rep12)} 种")
    for g, c in rep12:
        print(f"   {c}× 「{g}」")
    print(f"标点: 逗号{punct['逗号']} 句号{punct['句号']} 破折号{punct['破折号']} 省略号{punct['省略号']}  逗句比 {punct['逗号']/max(punct['句号'],1):.2f}")
    print()
    print(f"=== 段落级重复（发布系统级别）===")
    print(f"段落总数: {pd['paragraph_count']}")
    print(f"完全重复段落: {len(pd['exact_duplicates'])}")
    for p, c in pd["exact_duplicates"]:
        print(f"   ×{c}: {p[:80]}")
    print(f"高度相似段落组数: {len(pd['similar_pairs'])}")
    for j, i, p, d in pd["similar_pairs"]:
        print(f"   段{j+1} ≈ 段{i+1} (距离 {d}): {p[:60]}")
    print()
    print(f"=== 句子级重复（发布系统级别）===")
    print(f"完全重复句子(>20字): {len(sents_dup)}")
    for s, c in sents_dup:
        print(f"   ×{c}: {s[:60]}")


if __name__ == "__main__":
    main()
