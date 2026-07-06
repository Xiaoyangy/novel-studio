#!/usr/bin/env python3
"""Task 062：人工外部检测登记（朱雀等网页工具，人工触发、人工登记，不做自动提交）。
用法: python3 register_external_detection.py --project <dir> --chapter 3 --detector zhuque \
      --mode novel --score 12.5 --verdict human_like [--note 截图路径]"""
import argparse, json, os, time
ap=argparse.ArgumentParser()
for k in ["project","detector","mode","verdict"]: ap.add_argument("--"+k, required=True)
ap.add_argument("--chapter", type=int, required=True); ap.add_argument("--score", type=float, required=True)
ap.add_argument("--note", default="")
a=ap.parse_args()
path=os.path.join(a.project,"meta","external_detection_log.jsonl")
os.makedirs(os.path.dirname(path), exist_ok=True)
row={"chapter":a.chapter,"detector":a.detector,"mode":a.mode,"score":a.score,"verdict":a.verdict,"note":a.note,"checked_at":time.strftime("%Y-%m-%dT%H:%M:%S")}
open(path,"a").write(json.dumps(row,ensure_ascii=False)+"\n")
print("registered:", row)
