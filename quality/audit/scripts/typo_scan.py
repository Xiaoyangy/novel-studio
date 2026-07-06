#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
typo_scan.py
============
中文小说章节的「错别字 / 标点 / 漏叠字」启发式扫描脚本。

用途:
  - 给「小说写作提示词模板 〇.7 写后自校」做机械预筛;
  - 脚本只做信号,不作最终判定;真正的「读起来别扭」必须靠通读。

用法:
  python3 typo_scan.py <文件或目录> [更多文件...]
  python3 typo_scan.py <文件> --json   # 机器可读 JSON 输出

支持 .md / .txt;输出按类别聚合,每类给若干行内实例。

参考 〇.7 节:
  - A 表 20 组高频易混字
  - B 表 9 项其他错误
  - C 表 5 条标点规则
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from collections import defaultdict
from pathlib import Path

# ---------- 启发式规则 ----------
# 每条规则:(类别名, 正则, 解读/示例)
# 这里的「高频易混对」只是先抓出可疑片段,真正改不改要人工看上下文。
RULES: list[tuple[str, str, str]] = [
    # —— A 表:20 组高频易混字 —— (只抓其中明显的高风险组合)
    ('的/地/得-得后形容词', r'[一-鿿]{1,3}得[很挺更极非常特别格外相当][一-鿿]?', '✅ 常见正确,无需改'),
    ('的/地/得-错用嫌疑', r'[一-鿿]的[一-鿿]{1,2}[飞迅猛][快地][跑走冲飞]', '可疑:状语用「的」'),
    ('在/再-再+感官动词', r'在[一-鿿]{0,2}[看见听见发现找到说出想到]', '可疑:动词前应为「再」'),
    ('做/作-作文/作业', r'[写一篇][作文作业]', '✅ 通常正确'),
    ('坐/座-动词', r'请[坐座]|[坐座]下来|[坐座]好', '检查动词/量词'),
    ('记/计-计划/计算', r'[计划日记记忆记住标记](?!划)', '核查「计/记」'),
    ('份/分-分钟', r'[一-鿿]份钟|[一-鿿][一二三四五六七八九十]份[一-鿿]', '可疑:应为「分钟」'),
    ('及/急-及时', r'[急及]时[一-鿿]', '检查「及时/急诊」'),
    ('须/需-必须/需要', r'[必须][要须][一-鿿]', '检查「必须/需要」'),
    ('辨/辩-辩论/辨别', r'[辨辩][别论论]', '检查「辨/辩」'),
    ('蜜/密-秘密/甜蜜', r'[蜜密]蜜', '检查「秘密/甜蜜」'),
    ('即/既-即使/既然', r'[既即]使|[既即]然', '检查「即/既」'),
    ('折/拆-拆开/折开', r'[折拆]开', '检查「折/拆」'),
    ('渡/度-度过/渡过', r'[渡度]过[一-鿿]', '检查「度/渡」'),
    ('那/哪-哪怕/哪儿', r'[那哪]怕|[那哪]里', '检查「那/哪」'),
    ('象/像-好像/大象', r'[像象]是|[像象]一[一-鿿]', '检查「像/象」'),

    # —— B 表:多/漏/叠字 / 一致性 / 病句 ——
    ('叠字-可疑', r'([一-鿿])\1{2,}', '同一汉字连续出现 ≥3 次(语气词除外)'),
    ('叠「的」', r'[一-鿿]的[一-鿿]的[一-鿿]', '句中两个「的」相邻,常多字'),
    ('叠「了」', r'[一-鿿]了了[一-鿿]', '句中两个「了」相邻'),
    ('叠「的/了/着」+ 副词', r'(慢慢的|悄悄的|静静的)[一-鿿]{0,3}\\1', '叠字作状语,出现两次及以上'),
    ('缺少主语嫌疑', r'^[一-鿿]{2,6}[看见听见发现][一-鿿]', '句首无主语'),
    ('重复修饰嫌疑', r'很[一-鿿]很[一-鿿]', '同一修饰语连用两次'),
    ('动宾不搭配', r'[浮出做出发出]了?一个?笑[容颜脸]', '「笑容/笑颜」前动词错配'),

    # —— C 表:标点 ——
    ('半角逗号', r'[一-鿿],[一-鿿]', '中文行内出现半角逗号'),
    ('半角句号', r'[一-鿿]\.[一-鿿]', '中文行内出现半角句号'),
    ('半角问号', r'[一-鿿]\?[一-鿿]', '中文行内出现半角问号'),
    ('半角冒号', r'[一-鿿]:[一-鿿]', '中文行内出现半角冒号'),
    ('半角分号', r'[一-鿿];[一-鿿]', '中文行内出现半角分号'),
    ('标点堆叠-半角', r'!{2,}|\?{2,}', '连续 ! 或 ?'),
    ('标点堆叠-全角', r'！{2,}|？{2,}', '连续 ！ 或 ？'),
    ('装饰符号', r'[★☆~※◎○●◉]', '正文里出现装饰符号'),
    ('拼音替代词', r'\b(tmd|wtf|nbcs|sb|yyds|emo|xswl|kdl|tql|nbc|mdzz)\b', '拼音/字母缩写替代正常中文'),
    ('emoji', r'[\U0001F300-\U0001FAFF\U00002600-\U000027BF]', 'emoji 表情'),
]

# 引号配对(左开 U+201C/U+2018,右闭 U+201D/U+2019)
QUOTE_PAIRS = [
    ('“', '”'),   # "" 双弯引号
    ('‘', '’'),   # '' 单弯引号
    ('"', '"'),               # " " 直引号(混排时也常出现)
    ("'", "'"),               # ' ' 直单引号
    ('「', '」'),
    ('『', '』'),
    ('《', '》'),
    ('(', ')'),
    ('（', '）'),
    ('【', '】'),
]


def scan_file(path: Path) -> dict:
    """对单个文件跑全部规则,返回结构化结果。"""
    text = path.read_text(encoding='utf-8')
    lines = text.splitlines()
    findings: dict[str, list[dict]] = defaultdict(list)
    for label, pat, note in RULES:
        rgx = re.compile(pat)
        for ln, line in enumerate(lines, 1):
            for m in rgx.finditer(line):
                snippet = m.group(0)[:30]
                findings[label].append({
                    'line': ln,
                    'snippet': snippet,
                    'context': line.strip()[:80],
                    'note': note,
                })
    # 引号配对
    for o, c in QUOTE_PAIRS:
        oc, cc = text.count(o), text.count(c)
        # 仅当至少一者出现且不一致时报警
        if (oc or cc) and oc != cc:
            findings['引号不配对'].append({
                'line': 0,
                'snippet': f'开 U+{ord(o):04X} {oc} 个 / 闭 U+{ord(c):04X} {cc} 个',
                'context': '',
                'note': '左开右闭必须数量一致',
            })
    # 半角 / 全角 标点统计
    half = sum(text.count(p) for p in [',', '.', '?', '!', ';', ':'])
    full = sum(text.count(p) for p in ['，', '。', '？', '！', '；', '：'])
    findings['_标点统计'].append({
        'line': 0,
        'snippet': f'半角 {half} / 全角 {full}',
        'context': '',
        'note': '参考值',
    })
    return dict(findings)


def render_human(findings: dict, filename: str) -> str:
    out = [f'=== {filename} ===']
    # 排序:去掉 _ 统计项
    real = {k: v for k, v in findings.items() if not k.startswith('_')}
    stat = findings.get('_标点统计', [])
    for label, items in sorted(real.items(), key=lambda x: -len(x[1])):
        out.append(f'  [{label}] x{len(items)}')
        for it in items[:3]:
            if it['line']:
                out.append(f"    L{it['line']}: 「{it['snippet']}」 — {it['context']}")
            else:
                out.append(f"    {it['snippet']}")
    if stat:
        out.append(f"  [标点统计] {stat[0]['snippet']}")
    out.append(f'  类别合计 {len(real)} / 信号总数 {sum(len(v) for v in real.values())}')
    return '\n'.join(out)


def main():
    ap = argparse.ArgumentParser(description='中文小说错别字启发式扫描')
    ap.add_argument('paths', nargs='+', help='文件或目录(目录会取所有 .md/.txt)')
    ap.add_argument('--json', action='store_true', help='输出 JSON')
    args = ap.parse_args()

    targets: list[Path] = []
    for a in args.paths:
        p = Path(a)
        if p.is_dir():
            targets.extend(sorted(p.glob('*.md')))
            targets.extend(sorted(p.glob('*.txt')))
        elif p.exists():
            targets.append(p)
        else:
            print(f'[!] 跳过不存在的路径: {p}', file=sys.stderr)

    all_results = {}
    for t in targets:
        all_results[t.name] = scan_file(t)

    if args.json:
        print(json.dumps(all_results, ensure_ascii=False, indent=2))
        return

    for name, fs in all_results.items():
        print(render_human(fs, name))
        print()


if __name__ == '__main__':
    main()