import hashlib
import json
import os
import tempfile
import unittest
from datetime import datetime, timedelta
from concurrent.futures import ThreadPoolExecutor
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

    def test_direct_pipeline_failure_remains_visible_without_host_queue(self):
        self.write_json("meta/progress.json", {"phase": "init", "current_chapter": 0})
        failed = datetime.now().astimezone() - timedelta(hours=1)
        record = {"schema": "pipeline-timing.v1", "scope": "stage", "stage": "zero-init",
                  "status": "error", "finished_at": failed.isoformat(),
                  "error": "initial_state[林澄].knowledge_ledger 未补足"}
        log = self.nd / "meta/pipeline_timings.jsonl"
        log.write_text(json.dumps(record, ensure_ascii=False) + "\n", encoding="utf-8")
        data = server.summarize_run(self.run)
        self.assertEqual(data["runtime"]["status"], "error")
        self.assertFalse(data["runtime"]["active"])
        self.assertFalse(data["runtime"]["last_error_recovered"])
        self.assertEqual(data["runtime"]["current_stage"], "zero-init")
        self.assertIn("knowledge_ledger", data["runtime"]["last_error"]["detail"])
        # A new successful stage record is actual recovery evidence.
        record.update(status="ok", error="", finished_at=datetime.now().astimezone().isoformat())
        with log.open("a", encoding="utf-8") as f:
            f.write(json.dumps(record) + "\n")
        data = server.summarize_run(self.run)
        self.assertTrue(data["runtime"]["last_error_recovered"])
        self.assertNotEqual(data["runtime"]["status"], "error")

    def test_timing_events_ignore_unknown_schema_scope_and_unfinished_rows(self):
        records = [
            {"schema": "pipeline-timing.v9", "scope": "stage", "stage": "render", "status": "error"},
            {"schema": "pipeline-timing.v1", "scope": "chapter", "stage": "render", "status": "error"},
            {"schema": "pipeline-timing.v1", "scope": "stage", "stage": "render", "status": "started"},
            {"schema": "pipeline-timing.v1", "scope": "stage", "stage": "render", "status": "error", "finished_at": "invalid"},
        ]
        (self.nd / "meta/pipeline_timings.jsonl").write_text(
            "\n".join(json.dumps(row) for row in records) + "\n", encoding="utf-8")
        self.assertEqual(server.runtime_events(self.nd), [])

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

    def test_rehearsal_owner_identifies_nonformal_planning_and_preserves_stage_token(self):
        now = datetime.now().astimezone()
        self.write_json("meta/progress.json", {"phase": "writing", "current_chapter": 1,
                                                "total_chapters": 3, "completed_chapters": []})
        # An explicit retry's live owner takes precedence over an older
        # completed-stage list whose next pending entry is project-all.
        self.write_json("meta/pipeline.json", {"stages": ["preplan", "rehearse-arc", "project-all"],
                                               "completed": ["preplan", "rehearse-arc"]})
        self.write_json("meta/runtime/pipeline_execution.json", {
            "version": 1, "mode": "project_all", "target_chapter": 1, "process_id": os.getpid(),
            "owner": f"pipeline-rehearse-arc-ch000001-pid{os.getpid()}-1788847200000000000",
            "acquired_at": now.isoformat(), "expires_at": (now + timedelta(hours=1)).isoformat(),
        })
        data = server.summarize_run(self.run)
        self.assertEqual(data["runtime"]["status"], "running")
        self.assertEqual(data["runtime"]["execution"]["mode"], "project_all")
        self.assertEqual(data["runtime"]["current_stage"], "rehearse-arc")
        self.assertEqual(data["working"]["mode"], "planning")
        self.assertEqual(data["working"]["chapter"], 0)
        self.assertEqual(data["working"]["step"], "rehearse-arc")
        self.assertNotEqual(data["formal_planning"]["state"], "building")

    def test_only_exact_matching_rehearsal_owner_overrides_project_all_mode(self):
        now = datetime.now().astimezone()
        pid = os.getpid()
        owners = [
            f"pipeline-project-all-ch000001-pid{pid}-1788847200000000000",
            f"prefix-pipeline-rehearse-arc-ch000001-pid{pid}-1788847200000000000",
            f"pipeline-rehearse-arc-extra-ch000001-pid{pid}-1788847200000000000",
            f"pipeline-rehearse-arc-ch1-pid{pid}-1788847200000000000",
            f"pipeline-rehearse-arc-ch000002-pid{pid}-1788847200000000000",
            f"pipeline-rehearse-arc-ch000001-pid{pid + 1}-1788847200000000000",
            f"pipeline-rehearse-arc-ch000001-pid{pid}-1788847200000000000-extra",
            f"pipeline-rehearse-arc-ch000001-pid{pid}-1٢3",
        ]
        self.write_json("meta/pipeline.json", {"stages": ["rehearse-arc", "project-all"], "completed": []})
        for owner in owners:
            with self.subTest(owner=owner):
                self.write_json("meta/runtime/pipeline_execution.json", {
                    "version": 1, "mode": "project_all", "target_chapter": 1, "process_id": pid,
                    "owner": owner, "acquired_at": now.isoformat(),
                    "expires_at": (now + timedelta(hours=1)).isoformat(),
                })
                self.assertEqual(server.pipeline_execution_state(self.nd)["stage"], "project-all")
                self.assertEqual(server.runtime_state(self.nd, {})["current_stage"], "project-all")
        self.write_json("meta/runtime/pipeline_execution.json", {
            "version": 1, "mode": "foundation", "target_chapter": 1, "process_id": pid,
            "owner": f"pipeline-rehearse-arc-ch000001-pid{pid}-1788847200000000000",
            "acquired_at": now.isoformat(), "expires_at": (now + timedelta(hours=1)).isoformat(),
        })
        self.assertEqual(server.pipeline_execution_state(self.nd)["stage"], "foundation")

    def test_rehearsal_timing_failure_and_pending_stage_keep_recovery_identifier(self):
        now = datetime.now().astimezone()
        self.write_json("meta/progress.json", {"phase": "writing", "current_chapter": 1, "total_chapters": 3})
        self.write_json("meta/pipeline.json", {"stages": ["preplan", "rehearse-arc", "project-all"],
                                               "completed": ["preplan"]})
        failed = {"schema": "pipeline-timing.v1", "scope": "stage", "stage": "rehearse-arc",
                  "status": "error", "finished_at": (now - timedelta(minutes=1)).isoformat(),
                  "error": "整弧预演资料缺口 <script>bad()</script>"}
        (self.nd / "meta/pipeline_timings.jsonl").write_text(json.dumps(failed) + "\n", encoding="utf-8")
        # An expired rehearsal owner must not count as a new live retry.
        self.write_json("meta/runtime/pipeline_execution.json", {
            "version": 1, "mode": "project_all", "target_chapter": 1, "process_id": os.getpid(),
            "owner": f"pipeline-rehearse-arc-ch000001-pid{os.getpid()}-1788847200000000000",
            "acquired_at": (now - timedelta(hours=2)).isoformat(),
            "expires_at": (now - timedelta(hours=1)).isoformat(),
        })
        data = server.summarize_run(self.run)
        self.assertEqual(data["runtime"]["status"], "error")
        self.assertFalse(data["runtime"]["execution"]["active"])
        self.assertFalse(data["runtime"]["last_error_recovered"])
        self.assertEqual(data["runtime"]["current_stage"], "rehearse-arc")
        self.assertEqual(data["working"]["step"], "rehearse-arc")
        self.assertEqual(data["working"]["mode"], "planning")
        self.assertEqual(server.next_pipeline_stage({"stages": data["pipeline_stages"],
                                                    "completed": data["pipeline_completed"]}), "rehearse-arc")
        (self.nd / "meta/runtime/pipeline_execution.json").unlink()
        (self.nd / "meta/pipeline_timings.jsonl").unlink()
        pending = server.summarize_run(self.run)
        self.assertFalse(pending["runtime"]["execution"]["active"])
        self.assertEqual(pending["runtime"]["current_stage"], "rehearse-arc")
        self.assertEqual(pending["working"]["step"], "rehearse-arc")

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

    def test_character_agent_dashboard_exposes_status_not_private_context(self):
        self.write_json("meta/character_agents/registry.json", {
            "version": "character-agent-registry.v1",
            "registry_root": "sha256:registry",
            "entries": [{
                "agent_id": "ca_lin", "character": "林默", "tier": "core",
                "status": "active", "last_activated_chapter": 3, "memory_version": 2,
            }],
        })
        self.write_json("meta/character_agents/memory/ca_lin.json", {
            "last_accepted_chapter": 2,
            "facts": [{"text": "不得出现在看板的私有记忆"}],
        })
        evidence = {
            "evidence_root": "sha256:evidence", "chapter": 3,
            "activation": {"entries": [{
                "agent_id": "ca_lin", "character": "林默", "tier": "core",
                "state": "active", "reasons": ["scene_appearance"],
            }]},
            "proposals": [{
                "agent_id": "ca_lin", "round": 1, "decision": "带走证据",
                "intended_action": "从后门离开", "decision_reason": "不得暴露的私有理由",
            }],
            "arbitrations": [{
                "round": 1,
                "resolutions": [{
                    "agent_id": "ca_lin", "character": "林默", "outcome": "success",
                    "immediate_result": "安全离开",
                }],
                "conflicts": [],
            }],
            "usage": [{
                "agent_id": "ca_lin", "character": "林默", "chapter": 3,
                "round": 1, "input": 120, "output": 30, "cost_usd": 0.01,
            }],
        }
        self.write_json("meta/planning/v2/.building/pg2_test/chapters/0003.bundle.json", {
            "character_agent_evidence": evidence,
        })
        successor_digest = "sha256:" + "a" * 64
        self.write_json("meta/character_agents/successors/current.json", {
            "version": "character-agent-successor-plan.v1",
            "parent_generation_id": "pg2_test",
            "plan_digest": successor_digest,
        })
        self.write_json(f"meta/character_agents/successors/pg2_test/{'a' * 64}.json", {
            "digest": successor_digest,
            "trigger_chapter": 3,
            "hard_contract_conflicts": ["证据必须保留"],
            "architect_summary": "改走备用交付路线。",
            "revised_chapters": [{"core_event": "不得泄漏的未来软大纲"}],
        })

        payload = server.character_agent_payload(self.nd)

        self.assertEqual(len(payload["characters"]), 1)
        row = payload["characters"][0]
        self.assertEqual(row["recent_decision"], "带走证据")
        self.assertEqual(row["arbitration_result"], "安全离开")
        self.assertEqual(row["memory_chapter"], 2)
        self.assertEqual(row["cost_usd"], 0.01)
        self.assertEqual(payload["successor_generation"]["trigger_chapter"], 3)
        self.assertNotIn("revised_chapters", payload["successor_generation"])
        serialized = json.dumps(payload, ensure_ascii=False)
        self.assertNotIn("不得出现在看板的私有记忆", serialized)
        self.assertNotIn("不得暴露的私有理由", serialized)
        self.assertNotIn("不得泄漏的未来软大纲", serialized)

    def test_activation_chapter_dashboard_keeps_cycle_order_and_readiness_separate(self):
        def cycle(index):
            return {"index": index, "evidence": {
                "chapter": 1, "evidence_root": f"cycle-{index}",
                "activation": {"entries": [{"agent_id": "ca_a", "character": "甲", "tier": "core", "state": "active", "reasons": ["received_information"]}]},
                "proposals": [{"agent_id": "ca_a", "character": "甲", "round": 1, "decision": f"选择{index}", "intended_action": "核验", "decision_reason": "SECRET理由"}],
                "arbitrations": [{"round": 1, "resolutions": [{"agent_id": "ca_a", "character": "甲", "outcome": "success", "immediate_result": f"结果{index}"}]}],
                "observations": [{"private": "SECRET观察"}], "memory": {"private": "SECRET记忆"},
            }}
        self.write_json("meta/planning/v2/generations/pg2_cycles/chapters/0001.bundle.json", {
            "character_activation_evidence": {"cycles": [cycle(2), cycle(1)], "context": {"private": "SECRET未来目标"}},
        })
        usage_path = self.nd / "meta/character_agents/usage.jsonl"
        usage_path.parent.mkdir(parents=True, exist_ok=True)
        usage_path.write_text(json.dumps({"usage_id": "ready-1", "generation_id": "pg2_cycles", "role": "chapter_readiness", "agent_id": "chapter_readiness", "chapter": 1, "cycle": 2, "round": 1, "input": 100, "output": 20}) + "\n")
        payload = server.character_agent_payload(self.nd)
        self.assertEqual(len(payload["characters"]), 1)
        row = payload["characters"][0]
        self.assertEqual(row["recent_decision"], "选择2")
        self.assertEqual(row["activation_cycle"], 2)
        self.assertEqual(row["decision_cycle"], 2)
        self.assertEqual(payload["chapter_readiness_usage"]["tokens_in"], 100)
        self.assertNotIn("SECRET", json.dumps(payload, ensure_ascii=False))

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

    def test_character_usage_reports_partial_estimates_unknown_and_sleeping_zero(self):
        self.write_json("meta/character_agents/registry.json", {"entries": [
            {"agent_id": "ca_lin", "character": "林澄", "tier": "core", "status": "active"},
            {"agent_id": "ca_sleep", "character": "休眠角色", "status": "sleeping"},
            {"agent_id": "ca_free", "character": "免费调用", "status": "active"},
        ]})
        estimated = {"generation_id": "g1", "role": "character", "agent_id": "ca_lin", "chapter": 1,
                     "round": 1, "input": 200, "output": 50, "cost_usd": 0.0049,
                     "cost_source": "estimated", "provider": "codex", "model": "test-model"}
        legacy_copy = {k: v for k, v in estimated.items() if k not in ("cost_source", "provider", "model")}
        self.write_json("meta/planning/v2/.building/g1/chapters/0001.bundle.json", {
            "character_agent_evidence": {"chapter": 1, "usage": [legacy_copy]},
        })
        unknown = {"generation_id": "g1", "role": "character", "agent_id": "ca_lin", "chapter": 2,
                   "round": 1, "input": 100, "output": 20}
        reported = {"generation_id": "g1", "role": "character", "agent_id": "ca_lin", "chapter": 3,
                    "round": 1, "input": 80, "output": 30, "cost_usd": 0.01, "cost_source": "reported"}
        arbiter = {"generation_id": "g1", "role": "world_arbiter", "agent_id": "world_arbiter", "chapter": 1,
                   "round": 1, "input": 250, "output": 40, "cost_usd": 0.005, "cost_source": "unknown",
                   "provider": "codex", "model": "mixed", "models": [
                       {"provider": "codex", "model": "first", "private_context": "不得泄露模型私有上下文"},
                       {"provider": "test", "model": "second"},
                       {"model": {"secret": "不得转换成模型名的秘密对象"}},
                   ]}
        # Explicit reported zero is omitted by the Go struct's omitempty tag.
        free = {"generation_id": "g1", "role": "character", "agent_id": "ca_free", "chapter": 1,
                "round": 1, "input": 50, "output": 10, "cost_source": "reported"}
        path = self.nd / "meta/character_agents/usage.jsonl"
        path.write_text("\n".join(json.dumps(row, ensure_ascii=False)
                                   for row in [estimated, estimated, unknown, reported, arbiter, free]), encoding="utf-8")

        payload = server.character_agent_payload(self.nd)
        rows = {row["agent_id"]: row for row in payload["characters"]}
        lin = rows["ca_lin"]
        self.assertEqual(lin["usage_calls"], 3)
        self.assertEqual(lin["tokens_in"], 380)
        self.assertEqual(lin["tokens_out"], 100)
        self.assertAlmostEqual(lin["cost_usd"], 0.0149)
        self.assertFalse(lin["cost_complete"])
        self.assertEqual(lin["cost_source"], "unknown")
        self.assertEqual(lin["cost_sources"], {"reported": 1, "estimated": 1, "unknown": 1})
        self.assertEqual(lin["unpriced_calls"], 1)
        self.assertEqual(lin["models"], [{"provider": "codex", "model": "test-model"}])
        self.assertEqual(rows["ca_sleep"]["cost_usd"], 0)
        self.assertEqual(rows["ca_sleep"]["usage_calls"], 0)
        self.assertTrue(rows["ca_sleep"]["cost_complete"])
        self.assertEqual(rows["ca_free"]["usage_calls"], 1)
        self.assertTrue(rows["ca_free"]["cost_complete"])
        self.assertEqual(rows["ca_free"]["cost_source"], "reported")
        self.assertEqual(payload["world_arbiter_usage"]["cost_usd"], 0.005)
        self.assertFalse(payload["world_arbiter_usage"]["cost_complete"])
        self.assertEqual(payload["world_arbiter_usage"]["models"], [
            {"provider": "codex", "model": "first"}, {"provider": "test", "model": "second"},
        ])
        total = payload["usage_summary"]
        self.assertEqual(total["usage_calls"], 5)
        self.assertEqual(total["unpriced_calls"], 2)
        self.assertAlmostEqual(total["cost_usd"], 0.0199)
        self.assertEqual(total["tokens_in"], 680)
        self.assertEqual(total["tokens_out"], 150)
        self.assertNotIn("不得泄露", json.dumps(payload, ensure_ascii=False))
        self.assertNotIn("秘密对象", json.dumps(payload, ensure_ascii=False))

    def test_character_usage_dedupes_price_enrichment_but_preserves_rounds_and_generations(self):
        base = {"generation_id": "g1", "role": "character", "agent_id": "ca_lin", "chapter": 1,
                "round": 1, "input": 100, "output": 20}
        self.write_json("meta/planning/v2/.building/g1/chapters/0001.bundle.json", {
            "character_agent_evidence": {"chapter": 1, "usage": [base]},
        })
        priced = dict(base, cost_source="estimated", cost_usd=0.004)
        path = self.nd / "meta/character_agents/usage.jsonl"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("\n".join(json.dumps(row) for row in [
            priced, dict(priced, round=2), dict(priced, generation_id="g2"),
        ]), encoding="utf-8")
        row = server.character_agent_payload(self.nd)["characters"][0]
        self.assertEqual(row["usage_calls"], 3)
        self.assertEqual(row["tokens_in"], 300)
        self.assertEqual(row["cost_usd"], 0.012)
        self.assertTrue(row["cost_complete"])
        self.assertEqual(row["cost_source"], "estimated")

    def test_character_usage_does_not_treat_missing_or_invalid_prices_as_free(self):
        for item in [
            {"input": 100}, {"input": 0}, {"input": 100, "cost_usd": None},
            {"cost_source": "reported", "cost_usd": -1},
            {"cost_source": "estimated", "cost_usd": "not-a-price"},
            {"cost_source": "reported", "cost_usd": float("nan")},
            {"cost_source": "reported", "cost_usd": float("inf")},
        ]:
            with self.subTest(item=item):
                self.assertEqual(server.character_usage_cost(item), (0.0, "unknown"))
        self.assertEqual(server.character_usage_cost({"input": 100, "cost_usd": 0}), (0.0, "reported"))
        self.assertEqual(server.character_usage_cost({"input": 100, "cost_source": "reported"}), (0.0, "reported"))
        self.assertEqual(server.character_usage_cost({"input": 100, "cost_source": "estimated"}), (0.0, "estimated"))

    def test_character_usage_ids_preserve_failed_retries_and_unpriced_subcalls(self):
        base = {"generation_id": "g1", "role": "character", "agent_id": "ca_lin", "chapter": 1,
                "round": 1, "input": 100, "output": 20, "cost_source": "unknown", "cost_usd": 0.003,
                "provider": "codex", "model": "mixed", "models": ["codex/first", "openrouter/vendor/second"]}
        failed = dict(base, usage_id="run-1", status="failed", attempts=5, unpriced_calls=4)
        success = dict(base, usage_id="run-2", status="success", attempts=3, unpriced_calls=2)
        canceled = dict(base, usage_id="run-3", status="canceled", attempts=1, unpriced_calls=1)
        self.write_json("meta/planning/v2/.building/g1/chapters/0001.bundle.json", {
            "character_agent_evidence": {"chapter": 1, "usage": [failed, success]},
        })
        path = self.nd / "meta/character_agents/usage.jsonl"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("\n".join(json.dumps(row) for row in [failed, success, canceled, success]), encoding="utf-8")

        payload = server.character_agent_payload(self.nd)
        row = payload["characters"][0]
        self.assertEqual(row["usage_calls"], 3)
        self.assertEqual(row["attempts"], 9)
        self.assertEqual(row["tokens_in"], 300)
        self.assertEqual(row["unpriced_calls"], 7)
        self.assertEqual(row["cost_sources"]["unknown"], 3)
        self.assertEqual(row["usage_statuses"], {"failed": 1, "success": 1, "canceled": 1, "unknown": 0})
        self.assertEqual(row["cost_usd"], 0.009)
        self.assertFalse(row["cost_complete"])
        self.assertEqual(payload["usage_summary"]["unpriced_calls"], 7)
        self.assertEqual(row["models"], [
            {"provider": "codex", "model": "first"}, {"provider": "openrouter", "model": "vendor/second"},
        ])

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

    def test_long_book_includes_chapters_above_99_and_ignores_backup_names(self):
        self.seed_progress()
        for ch in (99, 100, 1000):
            (self.nd / "chapters" / f"{ch:02d}.md").write_text(f"# 第 {ch} 章\n正文。", encoding="utf-8")
        for name in ("00.md", "1.md", "0100.md", "100.md.pre-rewrite.md", "100.draft.md"):
            (self.nd / "chapters" / name).write_text("非正式正文", encoding="utf-8")
        (self.nd / "chapters" / "02.md").mkdir()
        self.write_json("meta/progress.json", {
            "completed_chapters": [1, 99, 100, 1000], "total_chapters": 1000,
        })
        self.write_json("reviews/100.json", {"verdict": "accept"})

        summary = server.summarize_run(self.run)
        self.assertEqual(server.chapter_files(self.nd), [1, 99, 100, 1000])
        self.assertEqual(summary["chapters_completed"], 4)
        self.assertEqual(summary["reviews_accepted"], 1)
        self.assertIn("1000", summary["chapter_words"])
        self.assertEqual([row["chapter"] for row in server.quality_payload(self.run)["chapters"]],
                         [1, 99, 100, 1000])
        self.assertEqual([row["n"] for row in server.run_detail(self.run)["chapters"]],
                         [1, 99, 100, 1000])

    def test_polling_reuses_one_body_read_across_summary_quality_and_detail(self):
        body = self.seed_progress()
        path = self.nd / "chapters" / "01.md"
        original = Path.read_bytes
        reads = []

        def read_bytes(candidate):
            if candidate == path:
                reads.append(candidate)
            return original(candidate)

        with mock.patch.object(Path, "read_bytes", autospec=True, side_effect=read_bytes):
            server.summarize_run(self.run)
            server.quality_payload(self.run)
            detail = server.run_detail(self.run)
            server.summarize_run(self.run)

        self.assertEqual(len(reads), 1)
        self.assertEqual(detail["chapters"][0]["words"], len(body))
        self.assertEqual(detail["chapters"][0]["title"], "第一章")

    def test_body_cache_invalidates_same_size_rewrite_and_atomic_replacement(self):
        body = self.seed_progress("# 标题\r\n旧正文。\r\n")
        path = self.nd / "chapters" / "01.md"
        self.write_json("reviews/01.json", {
            "verdict": "accept", "body_sha256": hashlib.sha256(body.encode()).hexdigest(),
        })
        self.assertEqual(server.count_words(self.nd, 1), len(body))
        self.assertEqual(server.quality_payload(self.run)["stale"], 0)
        old_stat = path.stat()
        replacement = body.replace("旧", "新")
        path.write_text(replacement, encoding="utf-8")
        os.utime(path, ns=(old_stat.st_atime_ns, old_stat.st_mtime_ns))
        self.assertEqual(server.file_sha256(path), hashlib.sha256(replacement.encode()).hexdigest())
        self.assertEqual(server.quality_payload(self.run)["stale"], 1)

        staged = path.with_suffix(".tmp")
        staged.write_text(body, encoding="utf-8")
        os.utime(staged, ns=(old_stat.st_atime_ns, old_stat.st_mtime_ns))
        staged.replace(path)
        self.assertEqual(server.quality_payload(self.run)["stale"], 0)
        path.unlink()
        self.assertEqual(server.file_sha256(path), "")
        self.assertEqual(server.count_words(self.nd, 1), 0)

    def test_body_cache_is_bounded_and_thread_safe(self):
        paths = []
        for ch in range(1, 13):
            path = self.nd / "chapters" / f"{ch:02d}.md"
            path.write_text(f"第 {ch} 章\n正文", encoding="utf-8")
            paths.append(path)
        expected = {path: hashlib.sha256(path.read_bytes()).hexdigest() for path in paths}
        with server._body_cache_lock:
            server._body_cache.clear()
        with mock.patch.object(server, "BODY_CACHE_ENTRIES", 4):
            with ThreadPoolExecutor(max_workers=8) as pool:
                results = list(pool.map(server.file_sha256, paths * 20))
            self.assertEqual(results, [expected[path] for path in paths * 20])
            with server._body_cache_lock:
                self.assertLessEqual(len(server._body_cache), 4)
                self.assertTrue(all(len(value[1]) == 3 for value in server._body_cache.values()))

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
        (rag_dir / "health.json").write_text(json.dumps({
            "version": "rag-health.v1", "healthy": True, "fact_chunks": 321,
            "vector_points": 321, "pending_chunks": 0, "issues": {},
        }), encoding="utf-8")

        data = server.rag_index_summary(self.nd)

        self.assertTrue(data["ready"])
        self.assertEqual(data["chunks"], 12345)
        self.assertEqual(data["provider"], "codex")
        self.assertEqual(data["model"], "qwen-test")
        self.assertEqual(data["store"], "qdrant")
        self.assertTrue(data["health"])
        self.assertEqual(data["fact_chunks"], 321)
        self.assertEqual(data["vector_points"], 321)


if __name__ == "__main__":
    unittest.main()
