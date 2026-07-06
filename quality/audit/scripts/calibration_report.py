#!/usr/bin/env python3
"""Task 060/062：aigc 阈值校准报告 + 外部检测器相关性小节。

用法:
  python3 calibration_report.py --project data/runs/鬼城/output/novel \
      --human-manifest quality/calibration/human/manifest.json \
      --out docs/aigc-calibration-report.md

三组语料（human/llm/mixed）跑 `novel-studio` 同款 aigc 引擎（经 `go run ./quality/audit/scripts/aigc_score_stdin.go`
或直接读已有 ai_gate JSON），输出分布/ROC/给定 FPR≤5% 的可达阈值，并读取
meta/external_detection_log.jsonl 输出"本地 blended 分 vs 外部分"相关性。
口径：报告只提议（evolution_report proposed 语义）——**不自动改任何阻断阈值**。
"""
import argparse, glob, json, os, statistics as st

def load_scores(dirs, key="blended_aigc_percent"):
    out=[]
    for d in dirs:
        for f in glob.glob(os.path.join(d, "**", "*_ai_gate.json"), recursive=True):
            try: out.append(json.load(open(f)).get(key, 0.0))
            except (OSError, json.JSONDecodeError): pass
    return out

def roc_point(pos, neg, thr):
    tp=sum(1 for x in pos if x>=thr); fp=sum(1 for x in neg if x>=thr)
    return (tp/max(len(pos),1), fp/max(len(neg),1))

def main():
    ap=argparse.ArgumentParser()
    ap.add_argument("--llm-gate-dirs", nargs="+", default=[], help="LLM 初稿 ai_gate JSON 目录")
    ap.add_argument("--human-gate-dirs", nargs="+", default=[], help="人类基线 ai_gate JSON 目录（离线跑引擎产出）")
    ap.add_argument("--external-log", default="", help="meta/external_detection_log.jsonl")
    ap.add_argument("--out", required=True)
    a=ap.parse_args()
    llm, human = load_scores(a.llm_gate_dirs), load_scores(a.human_gate_dirs)
    lines=["# aigc 阈值校准报告（proposed 语义——不自动改阈值）","",
           f"- llm 组样本: {len(llm)}；human 组样本: {len(human)}",""]
    if llm: lines.append(f"- llm blended 分布: mean={st.mean(llm):.1f} p50={st.median(llm):.1f} min={min(llm):.1f} max={max(llm):.1f}")
    if human: lines.append(f"- human blended 分布: mean={st.mean(human):.1f} p50={st.median(human):.1f}")
    if llm and human:
        best=None
        for thr in range(5,95,5):
            tpr,fpr=roc_point(llm,human,thr)
            lines.append(f"- 阈值 {thr}: TPR={tpr:.2f} FPR={fpr:.2f}")
            if fpr<=0.05 and (best is None or tpr>best[1]): best=(thr,tpr)
        if best: lines.append(f"\n**建议（proposed）**：FPR≤5% 下可达阈值 {best[0]}（TPR={best[1]:.2f}）；对照现行 35%/5%/40，由用户拍板后走 evolution_report 采纳。")
    else:
        lines.append("\n> 样本不足：先按 docs/external-detector-protocol.md 抽检并补齐两组语料清单再跑。")
    if a.external_log and os.path.exists(a.external_log):
        rows=[json.loads(l) for l in open(a.external_log) if l.strip()]
        lines += ["", f"## 外部检测器对照（{len(rows)} 条登记）",
                  "| chapter | detector | mode | external | verdict |","|---|---|---|---|---|"]
        for r in rows[-20:]:
            lines.append(f"| {r.get('chapter')} | {r.get('detector')} | {r.get('mode')} | {r.get('score')} | {r.get('verdict')} |")
        lines.append("\n> 相关性差 = 本地代理失真 → 触发词表/权重复核提议（见 09 任务书 Task 062）。")
    open(a.out,"w").write("\n".join(lines)+"\n")
    print("report:", a.out)

if __name__=="__main__": main()
