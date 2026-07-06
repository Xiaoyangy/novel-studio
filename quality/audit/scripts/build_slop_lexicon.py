#!/usr/bin/env python3
"""Task 059：语料驱动 slop 词表生成（EQ-Bench 60/25/15 结构）。

用法:
  python3 build_slop_lexicon.py --human deconstruction-library --llm data/runs/*/output/novel/chapters \
      --out meta/slop_lexicon.json [--top 40]

A=人类基线语料（deconstruction-library，只读、离线统计、不复制原文）；B=自产 LLM 章节。
输出 词/bigram/trigram 的 B/A 过表达比排名 + not-x-but-y 变体族命中，
人工复核后写入词表 JSON（source: corpus；与内置词表按组合并，见 internal/aigc/lexicon.go）。
"""
import argparse, glob, json, math, os, re, sys, time
from collections import Counter

NOT_BUT = re.compile(r"(不是[^。！？!?；;\n]{1,30}[，,]?(?:而是|，是)|并非[^。！？!?；;\n]{1,24}[，,]?只是|与其说[^。！？!?；;\n]{1,24}[，,]?不如说|从来不是[^。！？!?；;\n]{1,24}[，,]?而是)")

def ngrams(text, n):
    text = re.sub(r"\s+", "", text)
    return Counter(text[i:i+n] for i in range(len(text)-n+1))

def load(globs):
    texts=[]
    for g in globs:
        for f in glob.glob(os.path.join(g,"**","*.md"), recursive=True)+glob.glob(os.path.join(g,"**","*.txt"), recursive=True):
            try: texts.append(open(f, encoding="utf-8", errors="ignore").read())
            except OSError: pass
    return texts

def overexpression(human, llm, n, top, min_llm=5):
    h, l = Counter(), Counter()
    for t in human: h += ngrams(t, n)
    for t in llm: l += ngrams(t, n)
    th, tl = max(sum(h.values()),1), max(sum(l.values()),1)
    scored=[]
    for k,c in l.items():
        if c < min_llm: continue
        ratio = (c/tl) / ((h.get(k,0)+1)/th)
        if ratio > 3: scored.append((k, round(ratio,1), c))
    return sorted(scored, key=lambda x:-x[1])[:top]

def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--human", nargs="+", required=True)
    ap.add_argument("--llm", nargs="+", required=True); ap.add_argument("--out", required=True)
    ap.add_argument("--top", type=int, default=40)
    a = ap.parse_args()
    human, llm = load(a.human), load(a.llm)
    if not human or not llm: sys.exit("语料为空：human=%d llm=%d" % (len(human), len(llm)))
    report = {
      "version": "corpus-%s" % time.strftime("%Y%m%d"), "source": "corpus",
      "built_at": time.strftime("%Y-%m-%d"),
      "weights": {"words": 0.6, "not_x_but_y": 0.25, "trigrams": 0.15},
      "candidates": {  # 人工复核后把选定项移入 groups
        "word_overexpression": overexpression(human, llm, 2, a.top) + overexpression(human, llm, 4, a.top),
        "trigram_overexpression": overexpression(human, llm, 3, a.top),
        "not_x_but_y_hits": sum(len(NOT_BUT.findall(t)) for t in llm),
      },
      "groups": {},  # ← 人工复核后填充；空 groups 不会覆盖内置
      "_review_note": "候选按 B/A 过表达比排序；逐条人工确认后移入 groups 对应组，再放到项目 meta/slop_lexicon.json 生效",
    }
    json.dump(report, open(a.out, "w"), ensure_ascii=False, indent=1)
    print("candidates written:", a.out, "| human=%d llm=%d 篇" % (len(human), len(llm)))

if __name__ == "__main__": main()
