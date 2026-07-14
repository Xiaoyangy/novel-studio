#!/usr/bin/env python3
from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from content_lint import (  # noqa: E402
    DIALOGUE_MICRO_PERIOD_EXEMPTIONS,
    cadence_issues,
    dialogue_micro_period_chain_stats,
    lint_text,
)


def authorial_summary_issues(raw: str) -> list[dict]:
    return [
        issue
        for issue in lint_text(raw)
        if issue["rule"] == "authorial_abstract_summary"
    ]


class AuthorialAbstractSummaryTests(unittest.TestCase):
    def test_scene_closing_abstract_conclusions_are_errors(self) -> None:
        samples = [
            "林澈把车钥匙放回桌上。\n\n今天真正要解的，从来不是先把一辆车变成自己的。",
            "最后一盏灯亮起来，摊前终于有人停下。\n\n这才让他确定……全都没有白费。",
            "他关掉付款页。\n\n此刻他真正要面对的，根本不是买不买车。",
            "他把扳手放回箱里。\n\n眼前的不是麻烦，是结果。",
            "灯终于亮了。\n\n这总算让他知道，刚才的返工没白费。",
        ]

        for raw in samples:
            with self.subTest(raw=raw):
                issues = authorial_summary_issues(raw)
                self.assertEqual(len(issues), 1)
                self.assertEqual(issues[0]["severity"], "error")

    def test_concrete_fact_contrasts_are_not_authorial_summaries(self) -> None:
        raw = "孩子不是乱蹦，是跨过去。\n\n最难装的不是牌，是旧桌子。"

        self.assertEqual(authorial_summary_issues(raw), [])

    def test_character_dialogue_is_not_treated_as_authorial_summary(self) -> None:
        raw = (
            "他抓住车门说：“今天真正要解的，从来不是先把一辆车变成自己的。”\n\n"
            "【真正要解决的不是余额，是今晚的任务。】"
        )

        self.assertEqual(authorial_summary_issues(raw), [])


class DialogueMicroPeriodChainTests(unittest.TestCase):
    def test_three_distinct_character_turns_are_reported_by_lint_and_cadence(self) -> None:
        raw = (
            "甲说：“断电。先把线断了。”\n\n"
            "乙说：“都挪。推车会慢。”\n\n"
            "丙说：“这里。护套没盖住。”"
        )

        stats = dialogue_micro_period_chain_stats(raw)
        self.assertTrue(stats["triggered"])
        self.assertEqual(stats["turn_count"], 3)
        self.assertEqual(
            [issue["rule"] for issue in lint_text(raw)].count("dialogue_micro_period_chain"),
            1,
        )
        self.assertEqual(
            [issue["rule"] for issue in cadence_issues(raw)].count("dialogue_micro_period_chain"),
            1,
        )

    def test_multiple_fragments_in_one_or_two_turns_do_not_reach_threshold(self) -> None:
        raw = (
            "甲说：“断电。都挪。这里。先把线断了。”\n\n"
            "乙说：“先停。明天再说。”"
        )

        stats = dialogue_micro_period_chain_stats(raw)
        self.assertEqual(stats["turn_count"], 2)
        self.assertFalse(stats["triggered"])
        self.assertNotIn("dialogue_micro_period_chain", [issue["rule"] for issue in lint_text(raw)])

    def test_multiple_quote_spans_in_one_paragraph_count_as_one_turn(self) -> None:
        raw = (
            "甲说：“断电。先把线断了。”乙又说："
            "“这里。护套没盖住。”丙接道：“都挪。推车会慢。”"
        )

        stats = dialogue_micro_period_chain_stats(raw)
        self.assertEqual(stats["turn_count"], 1)
        self.assertFalse(stats["triggered"])

    def test_short_answers_other_punctuation_and_system_quotes_are_excluded(self) -> None:
        raw = (
            "甲说：“知道了。你先走。”\n\n"
            "乙问：“断哪？先别动。”\n\n"
            "丙喊：“快跑！门要塌了。”\n\n"
            "丁说：“等等……我去看看。”\n\n"
            "【系统：“断电。先检查。”】"
        )

        stats = dialogue_micro_period_chain_stats(raw)
        self.assertEqual(stats["turn_count"], 0)
        self.assertFalse(stats["triggered"])

    def test_go_short_answer_allowlist_and_ascii_quotes_are_excluded(self) -> None:
        expected = {
            "好", "好的", "好吧", "行", "行吧", "可以",
            "知道", "知道了", "明白", "明白了",
            "是", "是的", "是啊", "对", "对的", "对啊", "没错",
            "不是", "不是的", "不用", "不用了", "没事", "没事了",
            "谢谢", "谢了", "抱歉", "对不起",
            "嗯", "嗯嗯", "嗯哼", "哦", "噢", "啊", "哎", "唉", "喂",
        }
        self.assertEqual(DIALOGUE_MICRO_PERIOD_EXEMPTIONS, expected)
        for answer in expected:
            with self.subTest(answer=answer):
                raw = (f"“{answer}。后面这句话正常说完。”\n") * 3
                self.assertEqual(dialogue_micro_period_chain_stats(raw)["turn_count"], 0)

        ascii_quotes = '"断电。先把线断了。"\n' * 3
        self.assertEqual(dialogue_micro_period_chain_stats(ascii_quotes)["turn_count"], 0)


if __name__ == "__main__":
    unittest.main()
