#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
paragraph_dup.py — 段落级真重复检测（发布系统级别）

为什么需要这个独立脚本？
text_signals.py 用 n-gram 滑窗统计重复片段，会把"同一段内部"
的字符也算成"重复"，产生大量伪信号。发布系统（如番茄、起点）
检测的是**不同段落**之间是否出现了逐字复述，量化信号抓不到。

用法：
  python3 paragraph_dup.py <文本文件>
  python3 paragraph_dup.py <文本文件> --json
  python3 paragraph_dup.py <文本文件> --min-len 30 --min-distance 80

输出：
  - 完全重复段落（多段一字不差）
  - 高度相似段落（去标点后前 80 字相同，且间隔 >= min-distance 字符）
  - 完全重复句子（> 20 字）
"""
import re, sys, json
from collections import Counter


def paragraphs_of(raw):
    """按空行切段，去掉 markdown 标题。"""
    return [p.strip() for p in re.split(r"\n\n+", raw) if p.strip() and not p.strip().startswith("#")]


def exact_dup(paragraphs, min_len=10):
    """
    完全重复段落。min_len 默认为 10 字符——再短就有可能是常见过渡句
    (如'## 一'),没意义。
    """
    c = Counter(paragraphs)
    return [(p, n) for p, n in c.items() if n > 1 and len(p) >= min_len]


def similar_dup(paragraphs, min_len_chars=15, min_distance=100):
    """
    高度相似段落：去标点后前 min_len_chars 字相同。
    只在**文章不同位置**（间隔 >= min_distance 字符）才算真重复。
    """
    def norm(p):
        return re.sub(r"[^一-龥]", "", p)

    # 记每个段落的起始字符位置
    offsets = []
    pos = 0
    for p in paragraphs:
        offsets.append(pos)
        pos += len(p) + 2

    seen = {}
    pairs = []
    for i, p in enumerate(paragraphs):
        k = norm(p)[:max(min_len_chars, 15)]
        if len(k) < min_len_chars:
            continue
        if k in seen:
            j = seen[k]
            if abs(offsets[i] - offsets[j]) >= min_distance:
                pairs.append((j, i, p, abs(offsets[i] - offsets[j])))
        else:
            seen[k] = i
    return pairs


def sent_dup(raw, min_len=20):
    sents = [s.strip() for s in re.split(r"[。！？\n]", raw) if s.strip() and len(s.strip()) > min_len]
    c = Counter(sents)
    return [(s, n) for s, n in c.items() if n > 1]


def main():
    args = sys.argv[1:]
    json_out = "--json" in args
    args = [a for a in args if a != "--json"]

    min_len = 20
    min_distance = 100
    min_chars = 15
    for i, a in enumerate(args):
        if a == "--min-len":
            min_len = int(args[i+1])
        elif a == "--min-distance":
            min_distance = int(args[i+1])
        elif a == "--min-chars":
            min_chars = int(args[i+1])
    if not args:
        print(__doc__)
        sys.exit(1)
    path = args[0]
    raw = open(path, encoding="utf-8").read()
    paragraphs = paragraphs_of(raw)

    exact = exact_dup(paragraphs, min_len)
    similar = similar_dup(paragraphs, min_chars, min_distance)
    sents = sent_dup(raw)

    report = {
        "文件": path,
        "段落总数": len(paragraphs),
        "完全重复段落": [{"次数": n, "片段": p[:80] + ("..." if len(p) > 80 else "")} for p, n in exact],
        "高度相似段落组": [
            {"段A": j + 1, "段B": i + 1, "字符距离": d, "片段": p[:60] + "..."}
            for j, i, p, d in similar
        ],
        "完全重复句子": [{"次数": n, "片段": s[:80] + ("..." if len(s) > 80 else "")} for s, n in sents],
    }

    if json_out:
        print(json.dumps(report, ensure_ascii=False, indent=2))
        return

    print(f"=== 段落级去重检查 ===")
    print(f"文件: {path}")
    print(f"段落总数: {len(paragraphs)}")
    print()

    if not exact and not similar and not sents:
        print("✓ 没有检测到段落/句子级真重复。")
        return

    if exact:
        print(f"--- 完全重复段落 ({len(exact)} 组) ---")
        for p, n in exact:
            print(f"  ×{n}: {p[:80]}{'...' if len(p) > 80 else ''}")
        print()

    if similar:
        print(f"--- 高度相似段落 ({len(similar)} 组) ---")
        for j, i, p, d in similar:
            print(f"  段{j+1} ≈ 段{i+1} (字符距离 {d}):")
            print(f"    {p[:100]}{'...' if len(p) > 100 else ''}")
        print()

    if sents:
        print(f"--- 完全重复句子 ({len(sents)} 组) ---")
        for s, n in sents:
            print(f"  ×{n}: {s[:80]}")


if __name__ == "__main__":
    main()
