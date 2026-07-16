#!/usr/bin/env python3
from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from aigc_value import (  # noqa: E402
    DIALOGUE_MICRO_PERIOD_EXEMPTIONS,
    analyze_text,
    dialogue_micro_period_chain_stats,
    dialogue_ratio_estimate,
    high_quality_human_anchor_stats,
    quoted_hanzi_ratio,
    score_narrative_dynamics,
    segment_aigc_proxy,
    whole_text_single_segment_risk,
    zhuque_segment_risk_floor,
)
from content_lint import (  # noqa: E402
    dialogue_micro_period_chain_stats as lint_dialogue_micro_period_chain_stats,
)


MICRO_PERIOD_BODY = (
    "沈知遥说：“断电。先把线断了。”\n\n"
    "老丁还在看线，她又指向翘边：“这里。护套没盖住线头。”\n\n"
    "马玉芬问完，她只回了一句：“都挪。推车会慢，孩子不会。”"
)


def narrative_segment_report(
    raw_probability: float,
    raw_weak: float,
    raw_entropy: float,
    *,
    narrative: float = 0,
    burstiness: float = 0,
    structure: float = 0,
    cross_paragraph: float = 0,
) -> dict:
    return {
        "stats": {
            "dialogue_ratio": 0.33,
            "concrete_density_per_k": 17,
            "action_density_per_k": 25,
            "sensory_density_per_k": 6,
            "human_anchor": {
                "eligible": True,
                "strength": "strong",
                "anchor_type": "narrative_scene",
                "blockers": [],
                "segment_cap": 35,
            },
        },
        "zhuque_dimensions": {
            "composite_percent": 0,
            "dimensions": {
                "burstiness": {"score": burstiness},
                "structure_fingerprint": {"score": structure},
                "cross_paragraph_consistency": {"score": cross_paragraph},
                "perplexity_proxy": {
                    "stats": {"ttr": 0.72, "normalized_entropy": 0.97}
                },
            },
        },
        "latest_detector_proxy": {
            "components": {
                "probability_curvature_proxy": {
                    "score": 4,
                    "stats": {"human_anchor_adjusted_from": raw_probability},
                },
                "weak_lm_uniformity": {
                    "score": 3,
                    "stats": {"human_anchor_adjusted_from": raw_weak},
                },
                "local_entropy_uniformity": {
                    "score": 2,
                    "stats": {"human_anchor_adjusted_from": raw_entropy},
                },
                "layout_humanizer_fingerprint": {"score": 0},
                "semantic_smoothing": {"stats": {"action_density_per_k": 25}},
                "narrative_dynamics": {"score": narrative},
            }
        },
    }


class WholeTextCurveConsensusTests(unittest.TestCase):
    def test_three_raw_high_curves_with_narrative_risk_create_hard_gate(self) -> None:
        score, evidence, hard_gate = segment_aigc_proxy(
            narrative_segment_report(100, 96, 90, narrative=55), 2935, 1.0
        )

        self.assertGreaterEqual(score, 76)
        self.assertTrue(hard_gate)
        self.assertIn("独立叙事或结构风险", "\n".join(evidence))

    def test_borderline_interiority_signal_stays_advisory(self) -> None:
        score, evidence, hard_gate = segment_aigc_proxy(
            narrative_segment_report(100, 96, 90, narrative=46), 2935, 1.0
        )

        self.assertLess(score, 50)
        self.assertFalse(hard_gate)
        self.assertIn("缺少独立叙事或结构风险", "\n".join(evidence))

    def test_three_raw_high_curves_without_independent_support_stay_soft(self) -> None:
        score, evidence, hard_gate = segment_aigc_proxy(
            narrative_segment_report(100, 96, 90), 2935, 1.0
        )

        self.assertLess(score, 50)
        self.assertFalse(hard_gate)
        self.assertIn("缺少独立叙事或结构风险", "\n".join(evidence))

    def test_one_raw_high_curve_does_not_create_floor(self) -> None:
        score, evidence, hard_gate = segment_aigc_proxy(
            narrative_segment_report(98, 82, 90), 2935, 1.0
        )

        self.assertLess(score, 50)
        self.assertFalse(hard_gate)
        self.assertNotIn("三条原始曲线同时高危", "\n".join(evidence))

    def test_strong_structure_can_supply_independent_support(self) -> None:
        score, _, hard_gate = segment_aigc_proxy(
            narrative_segment_report(100, 96, 90, structure=65), 2935, 1.0
        )

        self.assertGreaterEqual(score, 76)
        self.assertTrue(hard_gate)

    def test_shared_mid_paragraph_variance_is_not_independent_support(self) -> None:
        score, evidence, hard_gate = segment_aigc_proxy(
            narrative_segment_report(100, 96, 90, burstiness=38, cross_paragraph=30),
            2935,
            1.0,
        )

        self.assertLess(score, 50)
        self.assertFalse(hard_gate)
        self.assertIn("缺少独立叙事或结构风险", "\n".join(evidence))

    def test_non_hard_single_segment_cannot_create_risk_floor(self) -> None:
        segments = [
            {
                "proportion": 1.0,
                "aigc_percent": 95.0,
                "whole_text_hard_gate": False,
            }
        ]
        self.assertEqual(zhuque_segment_risk_floor(segments, 95.0, 100.0), 0.0)
        self.assertEqual(
            whole_text_single_segment_risk(
                {
                    "enabled": True,
                    "segments": segments,
                    "risk_floor_percent": 95.0,
                    "max_segment_percent": 95.0,
                }
            ),
            0.0,
        )

        segments[0]["whole_text_hard_gate"] = True
        self.assertEqual(zhuque_segment_risk_floor(segments, 95.0, 100.0), 95.0)


class DialogueMicroPeriodChainTests(unittest.TestCase):
    def test_signal_uses_go_name_score_threshold_and_stats(self) -> None:
        stats = dialogue_micro_period_chain_stats(MICRO_PERIOD_BODY)
        self.assertEqual(stats["turn_count"], 3)
        self.assertTrue(stats["triggered"])

        dimension = score_narrative_dynamics(MICRO_PERIOD_BODY, 100)
        signal = next(
            item
            for item in dimension["signals"]
            if item["name"] == "dialogue_micro_period_chain"
        )
        self.assertEqual(signal["score"], 64)
        self.assertEqual(
            dimension["stats"]["dialogue_micro_period_chain_turns"], 3
        )
        self.assertEqual(
            dimension["stats"]["dialogue_micro_period_chain_examples"],
            stats["examples"],
        )

        report = analyze_text(MICRO_PERIOD_BODY, include_segments=False)
        integrated = report["latest_detector_proxy"]["components"][
            "narrative_dynamics"
        ]
        self.assertEqual(
            integrated["stats"]["dialogue_micro_period_chain_turns"], 3
        )
        self.assertEqual(
            report["stats"]["dialogue_micro_period_chain"]["turn_count"], 3
        )

    def test_one_paragraph_counts_at_most_one_turn(self) -> None:
        one_paragraph = (
            "甲说：“断电。先把线断了。”乙又说："
            "“这里。护套没盖住。”丙接道：“都挪。推车会慢。”"
        )
        stats = dialogue_micro_period_chain_stats(one_paragraph)
        self.assertEqual(stats["turn_count"], 1)
        self.assertFalse(stats["triggered"])
        self.assertFalse(
            any(
                item["name"] == "dialogue_micro_period_chain"
                for item in score_narrative_dynamics(one_paragraph, 100)["signals"]
            )
        )

    def test_short_answers_prosodic_punctuation_and_system_text_are_excluded(self) -> None:
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
                body = (f"“{answer}。后面这句话正常说完。”\n\n") * 3
                self.assertEqual(dialogue_micro_period_chain_stats(body)["turn_count"], 0)

        body = (
            "“断哪根？这根新线？”\n\n"
            "“老丁，晃眼！锅都看不清了。”\n\n"
            "“我……再想想。”\n\n"
            "【“断电。先把线断了。”“这里。护套没盖住线头。”"
            "“都挪。推车会慢。”】"
        )
        self.assertEqual(dialogue_micro_period_chain_stats(body)["turn_count"], 0)

    def test_python_lint_and_detector_stats_remain_in_parity(self) -> None:
        cases = [
            MICRO_PERIOD_BODY,
            "“断电。先把线断了。”“这里。再查一遍。”",
            "【“断电。先把线断了。”】\n\n“好的。我先走。”",
        ]
        for body in cases:
            with self.subTest(body=body):
                self.assertEqual(
                    dialogue_micro_period_chain_stats(body),
                    lint_dialogue_micro_period_chain_stats(body),
                )

    def test_human_anchor_records_and_blocks_three_hit_turns(self) -> None:
        stats = dialogue_micro_period_chain_stats(MICRO_PERIOD_BODY)
        anchor = high_quality_human_anchor_stats(
            MICRO_PERIOD_BODY + "；！？……：、",
            1800,
            [8, 24, 11, 19, 7, 28, 13, 17],
            [40, 120, 50, 180, 35, 90],
            0.22,
            20,
            {},
            0,
            {},
            {},
            {},
            stats,
        )
        self.assertFalse(anchor["eligible"])
        self.assertIn(
            "至少三个对白话轮用二至四字句号短句切开后续表达，不能作为自然声口锚点",
            anchor["blockers"],
        )
        self.assertEqual(
            anchor["metrics"]["dialogue_micro_period_chain_turns"], 3
        )
        self.assertEqual(
            anchor["metrics"]["dialogue_micro_period_chain_examples"],
            stats["examples"],
        )


class StraightQuoteDialogueTests(unittest.TestCase):
    def test_straight_chinese_dialogue_matches_typographic_metrics(self) -> None:
        typographic = (
            "林澈抬头说：“先关灯。别让孩子踩到线。”\n\n"
            "“这边我来收，你去看摊主。”\n\n"
            "“嗯”"
        )
        straight = typographic.replace("“", '"').replace("”", '"')
        typographic_dimension = score_narrative_dynamics(typographic, 100)
        straight_dimension = score_narrative_dynamics(straight, 100)
        for key in (
            "dialogue_paragraph_count",
            "action_dialogue_lead_count",
            "dialogue_turn_count",
            "dialogue_micro_period_chain_turns",
        ):
            with self.subTest(key=key):
                self.assertEqual(
                    straight_dimension["stats"][key],
                    typographic_dimension["stats"][key],
                )
        n_hanzi = len([char for char in straight if "\u4e00" <= char <= "\u9fff"])
        self.assertEqual(
            dialogue_ratio_estimate(straight, n_hanzi),
            dialogue_ratio_estimate(typographic, n_hanzi),
        )
        self.assertEqual(
            quoted_hanzi_ratio(straight, n_hanzi),
            quoted_hanzi_ratio(typographic, n_hanzi),
        )

    def test_straight_english_and_embedded_labels_are_not_dialogue(self) -> None:
        body = '项目名叫"青山夜市"，配置项 title="中文对白"，英文材料写着"hello world"。'
        dimension = score_narrative_dynamics(body, 100)
        self.assertEqual(dimension["stats"]["dialogue_paragraph_count"], 0)
        self.assertEqual(dimension["stats"]["dialogue_turn_count"], 0)
        self.assertEqual(dialogue_ratio_estimate(body, 15), 0)
        self.assertEqual(quoted_hanzi_ratio(body, 15), 0)


if __name__ == "__main__":
    unittest.main()
