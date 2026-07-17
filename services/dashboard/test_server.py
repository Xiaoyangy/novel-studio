import hashlib
import json
import os
import tempfile
import unittest
from datetime import datetime, timedelta
from pathlib import Path
from unittest import mock

from services.dashboard import server


class DashboardDataTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.run = Path(self.tmp.name) / "测试书"
        self.nd = self.run / "output" / "novel"
        for rel in ("meta/runtime", "meta/chapter_metrics", "chapters", "reviews", "summaries", "logs"):
            (self.nd / rel).mkdir(parents=True, exist_ok=True)

    def tearDown(self):
        self.tmp.cleanup()

    def write_json(self, rel, value):
        path = self.nd / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value, ensure_ascii=False), encoding="utf-8")

    def seed_progress(self, body="第一章\n\n正文。\n"):
        (self.nd / "chapters" / "01.md").write_text(body, encoding="utf-8")
        self.write_json("meta/progress.json", {
            "novel_name": "测试书",
            "phase": "writing",
            "flow": "rewriting",
            "current_chapter": 2,
            "in_progress_chapter": 1,
            "pending_rewrites": [1],
            "completed_chapters": [1],
            "total_chapters": 12,
            "total_word_count": len(body),
            "chapter_word_counts": {"1": len(body)},
        })
        self.write_json("meta/pipeline.json", {"stages": ["write", "review", "rewrite", "deliver"]})
        self.write_json("meta/run.json", {"provider": "codex", "model": "gpt-test", "planning_tier": "long"})
        self.write_json("outline.json", [{"chapter": 1, "title": "第一章"}, {"chapter": 2, "title": "第二章"}])
        event = {
            "seq": 1,
            "time": datetime.now().astimezone().isoformat(),
            "category": "TOOL",
            "summary": "plan_details(第1章)",
            "payload": {"Agent": "writer", "Summary": "plan_details(第1章)", "Failed": False},
        }
        (self.nd / "meta" / "runtime" / "queue.jsonl").write_text(
            json.dumps(event, ensure_ascii=False) + "\n", encoding="utf-8"
        )
        return body

    def test_summary_uses_rewrite_chapter_and_pipeline_step(self):
        body = self.seed_progress()
        data = server.summarize_run(self.run)

        self.assertEqual(data["working"]["chapter"], 1)
        self.assertEqual(data["working"]["target_chapter"], 1)
        self.assertEqual(data["working"]["next_chapter"], 2)
        self.assertEqual(data["working"]["mode"], "rewrite")
        self.assertEqual(data["working"]["step"], "plan")
        self.assertEqual(data["runtime"]["status"], "running")
        self.assertEqual(data["words_total"], len(body))
        self.assertEqual(data["health"]["status"], "ok")

    def test_newer_pipeline_activity_recovers_old_failure_event(self):
        self.seed_progress()
        now = datetime.now().astimezone()
        failed_at = now - timedelta(hours=1)
        self.write_json("meta/pipeline.json", {
            "stages": ["preplan", "project-all", "seal"],
            "completed": ["preplan"],
            "updated_at": now.isoformat(),
        })
        event = {
            "seq": 2,
            "time": failed_at.isoformat(),
            "category": "DISPATCH",
            "summary": "旧的 zero-init 失败",
            "payload": {
                "Agent": "architect_long",
                "Summary": "旧的 zero-init 失败",
                "FinishedAt": failed_at.isoformat(),
                "Failed": True,
                "Level": "error",
            },
        }
        (self.nd / "meta" / "runtime" / "queue.jsonl").write_text(
            json.dumps(event, ensure_ascii=False) + "\n", encoding="utf-8"
        )

        data = server.summarize_run(self.run)

        self.assertEqual(data["runtime"]["status"], "running")
        self.assertTrue(data["runtime"]["last_event"]["failed"])
        self.assertFalse(data["runtime"]["last_event_current"])
        self.assertTrue(data["runtime"]["last_error_recovered"])
        self.assertEqual(data["runtime"]["current_stage"], "project-all")
        self.assertEqual(data["working"]["step"], "simulate")
        self.assertFalse(data["working"]["last_failed"])

    def test_live_project_all_lease_stays_running_beyond_activity_window(self):
        self.seed_progress()
        now = datetime.now().astimezone()
        acquired = now - timedelta(minutes=10)
        old_failure = now - timedelta(hours=1)
        progress = json.loads((self.nd / "meta" / "progress.json").read_text(encoding="utf-8"))
        progress.update({"phase": "planning", "flow": "planning", "pending_rewrites": []})
        self.write_json("meta/progress.json", progress)
        self.write_json("meta/pipeline.json", {
            "stages": ["preplan", "project-all", "seal"],
            "completed": ["preplan"],
            "updated_at": acquired.isoformat(),
        })
        self.write_json("meta/runtime/pipeline_execution.json", {
            "version": 1,
            "mode": "project_all",
            "target_chapter": 1,
            "owner": f"pipeline-project-all-ch000001-pid{os.getpid()}-test",
            "process_id": os.getpid(),
            "acquired_at": acquired.isoformat(),
            "expires_at": (now + timedelta(hours=1)).isoformat(),
        })
        event = {
            "seq": 2,
            "time": old_failure.isoformat(),
            "category": "DISPATCH",
            "summary": "旧失败",
            "payload": {"FinishedAt": old_failure.isoformat(), "Failed": True, "Level": "error"},
        }
        (self.nd / "meta" / "runtime" / "queue.jsonl").write_text(
            json.dumps(event, ensure_ascii=False) + "\n", encoding="utf-8"
        )
        stale_ts = acquired.timestamp()
        for rel in ("meta/progress.json", "meta/pipeline.json"):
            os.utime(self.nd / rel, (stale_ts, stale_ts))

        data = server.summarize_run(self.run)

        self.assertGreater(data["runtime"]["age_seconds"], server.ACTIVE_WINDOW_SECONDS)
        self.assertEqual(data["runtime"]["status"], "running")
        self.assertTrue(data["runtime"]["execution"]["valid"])
        self.assertTrue(data["runtime"]["execution"]["active"])
        self.assertTrue(data["runtime"]["execution"]["process_alive"])
        self.assertEqual(data["runtime"]["current_stage"], "project-all")
        self.assertEqual(data["working"]["chapter"], 0)
        self.assertEqual(data["working"]["target_chapter"], 1)
        self.assertEqual(data["working"]["step"], "simulate")
        self.assertEqual(data["working"]["last_kind"], "pipeline")

    def test_dead_pipeline_lease_does_not_keep_stale_run_active(self):
        self.seed_progress()
        now = datetime.now().astimezone()
        acquired = now - timedelta(minutes=10)
        progress = json.loads((self.nd / "meta" / "progress.json").read_text(encoding="utf-8"))
        progress.update({"phase": "planning", "flow": "planning", "pending_rewrites": []})
        self.write_json("meta/progress.json", progress)
        self.write_json("meta/pipeline.json", {
            "stages": ["preplan", "project-all", "seal"],
            "completed": ["preplan"],
            "updated_at": acquired.isoformat(),
        })
        self.write_json("meta/runtime/pipeline_execution.json", {
            "version": 1,
            "mode": "project_all",
            "target_chapter": 1,
            "owner": "pipeline-project-all-ch000001-pid999999999-test",
            "process_id": 999999999,
            "acquired_at": acquired.isoformat(),
            "expires_at": (now + timedelta(hours=1)).isoformat(),
        })
        (self.nd / "meta" / "runtime" / "queue.jsonl").unlink(missing_ok=True)
        stale_ts = acquired.timestamp()
        for rel in ("meta/progress.json", "meta/pipeline.json"):
            os.utime(self.nd / rel, (stale_ts, stale_ts))

        with mock.patch.object(server, "process_alive", return_value=False):
            data = server.summarize_run(self.run)

        self.assertEqual(data["runtime"]["status"], "idle")
        self.assertTrue(data["runtime"]["execution"]["valid"])
        self.assertFalse(data["runtime"]["execution"]["active"])
        self.assertFalse(data["runtime"]["execution"]["process_alive"])

    def test_build_rag_process_is_reported_without_advancing_chapter_zero(self):
        now = datetime.now().astimezone()
        self.write_json("meta/progress.json", {
            "novel_name": "测试书",
            "phase": "planning",
            "flow": "planning",
            "current_chapter": 0,
            "completed_chapters": [],
            "total_chapters": 420,
        })
        self.write_json("meta/pipeline.json", {"stages": ["preplan", "project-all", "seal"], "completed": []})
        stale = (now - timedelta(hours=1)).timestamp()
        for rel in ("meta/progress.json", "meta/pipeline.json"):
            os.utime(self.nd / rel, (stale, stale))
        activity = {
            "active": True,
            "kind": "rag",
            "stage": "rag-build",
            "process_id": 60141,
            "output_dir": str(self.nd),
            "started_at": (now - timedelta(minutes=8)).isoformat(),
            "started_timestamp": (now - timedelta(minutes=8)).timestamp(),
            "observed_timestamp": now.timestamp(),
        }

        with mock.patch.object(server, "active_rag_processes", return_value=[activity]):
            data = server.summarize_run(self.run)

        self.assertEqual(data["runtime"]["status"], "running")
        self.assertEqual(data["runtime"]["current_stage"], "rag-build")
        self.assertEqual(data["runtime"]["activity"]["process_id"], 60141)
        self.assertEqual(data["current_chapter"], 0)
        self.assertEqual(data["working"]["chapter"], 0)
        self.assertEqual(data["working"]["mode"], "rag")
        self.assertEqual(data["working"]["step"], "rag")
        self.assertEqual(data["working"]["last_step"], "RAG 重建")

    def test_chapter_zero_planning_lease_is_not_presented_as_active_prose(self):
        now = datetime.now().astimezone()
        self.write_json("meta/progress.json", {
            "novel_name": "测试书",
            "phase": "outline",
            "flow": "planning",
            "current_chapter": 0,
            "completed_chapters": [],
            "total_chapters": 420,
        })
        self.write_json("meta/runtime/pipeline_execution.json", {
            "version": 1,
            "mode": "foundation",
            "target_chapter": 1,
            "owner": f"pipeline-foundation-ch000001-pid{os.getpid()}-test",
            "process_id": os.getpid(),
            "acquired_at": now.isoformat(),
            "expires_at": (now + timedelta(minutes=10)).isoformat(),
        })

        data = server.summarize_run(self.run)

        self.assertEqual(data["current_chapter"], 0)
        self.assertEqual(data["working"]["mode"], "planning")
        self.assertEqual(data["working"]["chapter"], 0)
        self.assertEqual(data["working"]["target_chapter"], 1)
        self.assertEqual(data["working"]["next_chapter"], 0)
        self.assertIsNone(data["working"]["last_chapter"])
        self.assertEqual(data["runtime"]["current_stage"], "foundation")

    def test_scan_rag_processes_matches_explicit_run_dir(self):
        command = (
            f"60141 08:05 /tmp/novel-studio-ragfix --build-rag "
            f"--dir {self.run} --probe-chapter 1\n"
        )
        completed = mock.Mock(stdout=command)

        with mock.patch.object(server.subprocess, "run", return_value=completed):
            activities = server.scan_rag_processes()

        self.assertEqual(len(activities), 1)
        self.assertEqual(activities[0]["process_id"], 60141)
        self.assertEqual(activities[0]["output_dir"], str(self.nd))
        self.assertGreater(activities[0]["started_timestamp"], 0)

    def test_frozen_outline_and_formal_arc_plan_have_independent_progress(self):
        self.write_json("meta/progress.json", {
            "novel_name": "测试书",
            "phase": "planning",
            "flow": "planning",
            "current_chapter": 0,
            "current_volume": 1,
            "current_arc": 1,
            "completed_chapters": [],
            "total_chapters": 420,
        })
        self.write_json("outline.json", [{"chapter": chapter} for chapter in range(1, 421)])
        self.write_json("layered_outline.json", [{
            "index": 1,
            "arcs": [{
                "index": 1,
                "chapters": [{"chapter": chapter} for chapter in range(1, 13)],
            }],
        }])

        data = server.summarize_run(self.run)

        self.assertTrue(data["outline_frozen"])
        self.assertEqual(data["outline_percent"], 100)
        self.assertEqual(data["chapters_outlined"], 420)
        self.assertEqual(data["formal_planning"]["state"], "not_started")
        self.assertEqual(data["formal_planning"]["planned_chapters"], 0)
        self.assertEqual(data["formal_planning"]["expected_chapters"], 12)
        self.assertEqual(data["formal_plan_percent"], 0)

        generation_id = "pg2_dashboard_test"
        self.write_json(
            f"meta/planning/v2/.building/{generation_id}/generation.json",
            {
                "generation_id": generation_id,
                "status": "building",
                "first_projected_chapter": 1,
                "last_projected_chapter": 12,
                "expected_chapter_count": 12,
                "projected_chapter_count": 3,
            },
        )
        for chapter in range(1, 4):
            self.write_json(
                f"meta/planning/v2/.building/{generation_id}/chapters/{chapter:04d}.bundle.json",
                {"chapter": chapter},
            )

        building_data = server.summarize_run(self.run)

        self.assertEqual(building_data["formal_planning"]["state"], "building")
        self.assertEqual(building_data["formal_planning"]["planned_chapters"], 3)
        self.assertEqual(building_data["formal_plan_percent"], 25)

        sealed_id = "pg2_dashboard_sealed"
        sealed = self.nd / "meta" / "planning" / "v2" / "generations" / sealed_id
        self.write_json(
            f"meta/planning/v2/generations/{sealed_id}/generation.json",
            {
                "generation_id": sealed_id,
                "status": "sealed",
                "first_projected_chapter": 1,
                "last_projected_chapter": 12,
                "expected_chapter_count": 12,
                "projected_chapter_count": 12,
            },
        )
        self.write_json(f"meta/planning/v2/generations/{sealed_id}/seal_receipt.json", {"sealed": True})
        future = datetime.now().timestamp() + 2
        os.utime(sealed / "generation.json", (future, future))
        os.utime(sealed / "seal_receipt.json", (future, future))

        sealed_data = server.summarize_run(self.run)

        self.assertEqual(sealed_data["formal_planning"]["state"], "sealed")
        self.assertTrue(sealed_data["formal_planning"]["sealed"])
        self.assertEqual(sealed_data["formal_planning"]["planned_chapters"], 12)
        self.assertEqual(sealed_data["formal_plan_percent"], 100)

    def test_dispatch_finished_at_can_report_a_current_failure(self):
        self.seed_progress()
        now = datetime.now().astimezone()
        started = now - timedelta(minutes=6)
        finished = now + timedelta(seconds=1)
        event = {
            "seq": 2,
            "time": started.isoformat(),
            "category": "DISPATCH",
            "summary": "长调用最终失败",
            "payload": {
                "FinishedAt": finished.isoformat(),
                "Failed": True,
                "Level": "error",
            },
        }
        (self.nd / "meta" / "runtime" / "queue.jsonl").write_text(
            json.dumps(event, ensure_ascii=False) + "\n", encoding="utf-8"
        )

        data = server.summarize_run(self.run)

        self.assertEqual(data["runtime"]["status"], "error")
        self.assertTrue(data["runtime"]["last_event_current"])
        self.assertFalse(data["runtime"]["last_error_recovered"])

    def test_health_reports_real_word_count_drift(self):
        body = self.seed_progress()
        progress = json.loads((self.nd / "meta" / "progress.json").read_text(encoding="utf-8"))
        progress["total_word_count"] = len(body) + 9
        progress["chapter_word_counts"]["1"] = len(body) + 9
        self.write_json("meta/progress.json", progress)

        data = server.summarize_run(self.run)
        codes = {issue["code"] for issue in data["health"]["issues"]}

        self.assertEqual(data["words_total"], len(body))
        self.assertEqual(data["words_reported"], len(body) + 9)
        self.assertIn("word_total_mismatch", codes)
        self.assertIn("chapter_word_mismatch", codes)

    def test_quality_payload_tracks_freshness_and_ai_metrics(self):
        body = self.seed_progress("第一章\n\n有停顿，也有选择。\n")
        digest = hashlib.sha256(body.encode("utf-8")).hexdigest()
        self.write_json("reviews/01.json", {
            "chapter": 1,
            "body_sha256": digest,
            "verdict": "accept",
            "contract_status": "met",
            "summary": "通过",
            "dimensions": [{"dimension": "character", "score": 90, "verdict": "pass", "comment": "稳定"}],
        })
        self.write_json("reviews/01_ai_gate.json", {
            "chapter": 1,
            "body_sha256": digest,
            "rule_violations": [],
            "aigc_report": {"aigc_percent": 4.8, "risk_label": "低", "confidence": "高"},
        })
        self.write_json("reviews/01_deepseek_ai_judge.json", {
            "chapter": 1,
            "body_sha256": digest,
            "verdict": "human_like",
            "ai_probability_percent": 5,
        })
        self.write_json("meta/chapter_metrics/01.json", {
            "chapter": 1,
            "ai_voice_score": 0.12,
            "protagonist_waver": True,
        })

        data = server.quality_payload(self.run)

        self.assertEqual(data["accepted"], 1)
        self.assertEqual(data["gate_passed"], 1)
        self.assertEqual(data["stale"], 0)
        self.assertEqual(data["average_aigc_percent"], 4.8)
        self.assertEqual(data["chapters"][0]["freshness"], "fresh")

    def test_rag_summary_uses_lightweight_status_files(self):
        rag_dir = self.nd / "meta" / "rag"
        rag_dir.mkdir(parents=True, exist_ok=True)
        (rag_dir / "index_state.json").write_text(
            '{"config":{"collection":"demo","embedding_provider":"codex",'
            '"embedding_model":"qwen-test","vector_store":"qdrant"},"chunks":[]}',
            encoding="utf-8",
        )
        (rag_dir / "index_state.md").write_text(
            "# RAG 索引状态\n\n- Collection：demo\n- Chunk 数：12345\n- 更新时间：2026-07-10T10:00:00+08:00\n",
            encoding="utf-8",
        )

        data = server.rag_index_summary(self.nd)

        self.assertTrue(data["ready"])
        self.assertEqual(data["chunks"], 12345)
        self.assertEqual(data["provider"], "codex")
        self.assertEqual(data["model"], "qwen-test")
        self.assertEqual(data["store"], "qdrant")


if __name__ == "__main__":
    unittest.main()
