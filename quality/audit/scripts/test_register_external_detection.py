import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


MODULE_PATH = Path(__file__).with_name("register_external_detection.py")
SPEC = importlib.util.spec_from_file_location("register_external_detection", MODULE_PATH)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


class RegisterExternalDetectionTests(unittest.TestCase):
    def write_pending_draft(
        self,
        project: Path,
        *,
        detector: str = "zhuque",
        mode: str = "whole",
        structural_block: bool = False,
        checkpoint_step: str = "draft",
    ) -> tuple[Path, str, str]:
        canonical = project / "chapters" / "01.md"
        canonical.parent.mkdir(parents=True, exist_ok=True)
        canonical.write_text("第一章\n\n旧正式正文\n", encoding="utf-8")
        evaluated_sha = MODULE.sha256_bytes(canonical.read_bytes())

        draft = project / "drafts" / "01.draft.md"
        draft.parent.mkdir(parents=True, exist_ok=True)
        draft.write_text("第一章\n\n完成重渲染后的候选正文。\n", encoding="utf-8")
        draft_sha = MODULE.sha256_bytes(draft.read_bytes())

        marker = project / "reviews" / "drafts" / "01_full_rerender_required.json"
        marker.parent.mkdir(parents=True, exist_ok=True)
        marker.write_text(json.dumps({
            "chapter": 1,
            "evaluated_body_sha256": evaluated_sha,
            "source": "registered_external_detection",
            "evaluator": "registered_external_detector",
            "required_detector": detector,
            "required_mode": mode,
            "required_external_retests": [{
                "detector": detector,
                "mode": mode,
                "trigger_body_sha256": evaluated_sha,
            }],
            "advice_complete": True,
            "revision_plan": ["整章重渲染"],
        }), encoding="utf-8")

        checkpoints = project / "meta" / "checkpoints.jsonl"
        checkpoints.parent.mkdir(parents=True, exist_ok=True)
        rows = [{
            "seq": 1,
            "scope": {"kind": "chapter", "chapter": 1},
            "step": checkpoint_step,
            "artifact": "drafts/01.draft.md",
            "digest": "sha256:" + draft_sha,
        }]
        if structural_block:
            rows.append({
                "seq": 2,
                "scope": {"kind": "chapter", "chapter": 1},
                "step": "draft-structural-block",
                "artifact": "drafts/01.draft.md",
                "digest": "sha256:" + "f" * 64,
            })
        checkpoints.write_text(
            "".join(json.dumps(row) + "\n" for row in rows),
            encoding="utf-8",
        )
        return draft, draft_sha, evaluated_sha

    def pending_draft_argv(
        self,
        project: Path,
        draft: Path,
        draft_sha: str,
        *,
        detector: str = "zhuque",
        mode: str = "whole",
    ) -> list[str]:
        return [
            str(MODULE_PATH), "--project", str(project), "--chapter", "1",
            "--detector", detector, "--mode", mode, "--score", "2",
            "--score-scale", "percent", "--verdict", "human_like",
            "--payload-file", str(draft), "--expected-sha256", draft_sha,
        ]

    def test_score_scale_is_explicit(self):
        self.assertEqual(MODULE.normalize_score(0.86, "probability"), 86)
        self.assertEqual(MODULE.normalize_score(86, "percent"), 86)
        with self.assertRaises(ValueError):
            MODULE.normalize_score(86, "probability")
        with self.assertRaises(ValueError):
            MODULE.normalize_score(101, "percent")

    def test_payload_must_exist(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            with self.assertRaises(FileNotFoundError):
                MODULE.resolve_payload(project, 1, "")

    def test_default_payload_hashes_exact_bytes(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            path = project / "chapters" / "01.md"
            path.parent.mkdir(parents=True)
            path.write_bytes("第一章\n\n正文".encode("utf-8"))
            resolved = MODULE.resolve_payload(project, 1, "")
            self.assertEqual(resolved, path.resolve())
            self.assertEqual(MODULE.sha256_bytes(resolved.read_bytes()), MODULE.sha256_bytes(path.read_bytes()))

    def test_custom_payload_must_be_byte_identical_to_canonical_chapter(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            canonical = project / "chapters" / "01.md"
            canonical.parent.mkdir(parents=True)
            canonical.write_bytes("第一章\n\n正文\n".encode("utf-8"))
            submitted = project / "submitted.txt"
            # Visually equivalent text with a missing trailing newline is still
            # a different detector subject and must not enter the hard gate.
            submitted.write_bytes("第一章\n\n正文".encode("utf-8"))
            sha = MODULE.sha256_bytes(submitted.read_bytes())
            argv = [
                str(MODULE_PATH), "--project", str(project), "--chapter", "1",
                "--detector", "zhuque", "--mode", "whole", "--score", "86",
                "--score-scale", "percent", "--verdict", "ai_like",
                "--payload-file", str(submitted), "--expected-sha256", sha,
            ]
            with mock.patch.object(sys, "argv", argv), self.assertRaisesRegex(
                SystemExit, "not byte-identical to the canonical chapter"
            ):
                MODULE.main()

    def test_custom_exact_copy_can_register_canonical_hash(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            canonical = project / "chapters" / "01.md"
            canonical.parent.mkdir(parents=True)
            body = "第一章\n\n正文\n".encode("utf-8")
            canonical.write_bytes(body)
            submitted = project / "submitted.txt"
            submitted.write_bytes(body)
            sha = MODULE.sha256_bytes(body)
            argv = [
                str(MODULE_PATH), "--project", str(project), "--chapter", "1",
                "--detector", "zhuque", "--mode", "whole", "--score", "86",
                "--score-scale", "percent", "--verdict", "ai_like",
                "--payload-file", str(submitted), "--expected-sha256", sha,
            ]
            with mock.patch.object(sys, "argv", argv):
                self.assertEqual(MODULE.main(), 0)
            row = json.loads((project / "meta" / "external_detection_log.jsonl").read_text(encoding="utf-8"))
            self.assertEqual(row["body_sha256"], MODULE.sha256_bytes(canonical.read_bytes()))
            self.assertEqual(row["payload_kind"], "canonical_chapter")

    def test_exact_pending_draft_can_register_named_identity(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            draft, draft_sha, _ = self.write_pending_draft(project)
            argv = self.pending_draft_argv(project, draft, draft_sha)
            with mock.patch.object(sys, "argv", argv):
                self.assertEqual(MODULE.main(), 0)
            row = json.loads(
                (project / "meta" / "external_detection_log.jsonl").read_text(encoding="utf-8")
            )
            self.assertEqual(row["body_sha256"], draft_sha)
            self.assertEqual(row["payload_path"], "drafts/01.draft.md")
            self.assertEqual(row["payload_kind"], "pending_draft")

    def test_exact_pending_edit_can_register_named_identity(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            draft, draft_sha, _ = self.write_pending_draft(
                project, checkpoint_step="edit"
            )
            argv = self.pending_draft_argv(project, draft, draft_sha)
            with mock.patch.object(sys, "argv", argv):
                self.assertEqual(MODULE.main(), 0)
            row = json.loads(
                (project / "meta" / "external_detection_log.jsonl").read_text(encoding="utf-8")
            )
            self.assertEqual(row["body_sha256"], draft_sha)
            self.assertEqual(row["payload_kind"], "pending_draft")

    def test_arbitrary_draft_without_named_marker_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            draft, draft_sha, _ = self.write_pending_draft(project)
            (project / "reviews" / "drafts" / "01_full_rerender_required.json").unlink()
            argv = self.pending_draft_argv(project, draft, draft_sha)
            with mock.patch.object(sys, "argv", argv), self.assertRaisesRegex(
                SystemExit, "named draft retest marker does not exist"
            ):
                MODULE.main()

    def test_pending_draft_identity_must_match_requested_detector_and_mode(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            draft, draft_sha, _ = self.write_pending_draft(project)
            argv = self.pending_draft_argv(
                project, draft, draft_sha, detector="other", mode="whole"
            )
            with mock.patch.object(sys, "argv", argv), self.assertRaisesRegex(
                SystemExit, "does not require this detector/mode"
            ):
                MODULE.main()

    def test_local_only_marker_cannot_authorize_named_draft_registration(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            draft, draft_sha, _ = self.write_pending_draft(project)
            marker = project / "reviews" / "drafts" / "01_full_rerender_required.json"
            value = json.loads(marker.read_text(encoding="utf-8"))
            value["evaluator"] = ""
            value["required_external_retests"] = []
            marker.write_text(json.dumps(value), encoding="utf-8")
            argv = self.pending_draft_argv(project, draft, draft_sha)
            with mock.patch.object(sys, "argv", argv), self.assertRaisesRegex(
                SystemExit, "not a named external-detector contract"
            ):
                MODULE.main()

    def test_existing_current_identity_is_not_pending_again(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            draft, draft_sha, _ = self.write_pending_draft(project)
            log = project / "meta" / "external_detection_log.jsonl"
            log.write_text(json.dumps({
                "chapter": 1,
                "detector": "zhuque",
                "mode": "whole",
                "score": 2,
                "score_scale": "percent",
                "score_percent": 2,
                "verdict": "human_like",
                "body_sha256": draft_sha,
            }) + "\n", encoding="utf-8")
            argv = self.pending_draft_argv(project, draft, draft_sha)
            with mock.patch.object(sys, "argv", argv), self.assertRaisesRegex(
                SystemExit, "already has a registered result"
            ):
                MODULE.main()

    def test_formal_high_with_no_initial_hash_keeps_retained_draft_authorized(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            draft, draft_sha, evaluated_sha = self.write_pending_draft(project)
            log = project / "meta" / "external_detection_log.jsonl"
            log.write_text(json.dumps({
                "chapter": 1,
                "detector": "zhuque",
                "mode": "whole",
                "score": 86,
                "score_scale": "percent",
                "score_percent": 86,
                "verdict": "ai_like",
                "body_sha256": evaluated_sha,
            }) + "\n", encoding="utf-8")
            argv = self.pending_draft_argv(project, draft, draft_sha)
            with mock.patch.object(sys, "argv", argv), self.assertRaisesRegex(
                SystemExit, "marker has no initial draft hash"
            ):
                MODULE.main()

    def test_pending_draft_with_write_intent_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            draft, draft_sha, _ = self.write_pending_draft(project)
            (project / "drafts" / "01.draft_write_intent.json").write_text(
                "{}", encoding="utf-8"
            )
            argv = self.pending_draft_argv(project, draft, draft_sha)
            with mock.patch.object(sys, "argv", argv), self.assertRaisesRegex(
                SystemExit, "draft write intent is still pending"
            ):
                MODULE.main()

    def test_current_marker_hash_is_not_a_rejudge_pending_candidate(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            draft, draft_sha, _ = self.write_pending_draft(project)
            marker = project / "reviews" / "drafts" / "01_full_rerender_required.json"
            value = json.loads(marker.read_text(encoding="utf-8"))
            value["evaluated_body_sha256"] = draft_sha
            marker.write_text(json.dumps(value), encoding="utf-8")
            argv = self.pending_draft_argv(project, draft, draft_sha)
            with mock.patch.object(sys, "argv", argv), self.assertRaisesRegex(
                SystemExit, "rerender_authorized, not rejudge_pending"
            ):
                MODULE.main()

    def test_structurally_blocked_draft_is_not_ready_for_platform_retest(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            draft, draft_sha, _ = self.write_pending_draft(
                project, structural_block=True
            )
            argv = self.pending_draft_argv(project, draft, draft_sha)
            with mock.patch.object(sys, "argv", argv), self.assertRaisesRegex(
                SystemExit, "current structural-block checkpoint"
            ):
                MODULE.main()

    def test_draft_copy_cannot_use_pending_candidate_bridge(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            draft, draft_sha, _ = self.write_pending_draft(project)
            copied = project / "submitted.txt"
            copied.write_bytes(draft.read_bytes())
            argv = self.pending_draft_argv(project, copied, draft_sha)
            with mock.patch.object(sys, "argv", argv), self.assertRaisesRegex(
                SystemExit, "not byte-identical to the canonical chapter"
            ):
                MODULE.main()

    def test_missing_canonical_chapter_is_rejected_even_with_payload(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            submitted = project / "submitted.txt"
            submitted.write_text("第一章\n\n正文", encoding="utf-8")
            sha = MODULE.sha256_bytes(submitted.read_bytes())
            argv = [
                str(MODULE_PATH), "--project", str(project), "--chapter", "1",
                "--detector", "zhuque", "--mode", "whole", "--score", "86",
                "--score-scale", "percent", "--verdict", "ai_like",
                "--payload-file", str(submitted), "--expected-sha256", sha,
            ]
            with mock.patch.object(sys, "argv", argv), self.assertRaisesRegex(
                SystemExit, "canonical chapter does not exist"
            ):
                MODULE.main()

    def test_blank_detector_or_mode_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            project = Path(tmp)
            payload = project / "chapters" / "01.md"
            payload.parent.mkdir(parents=True)
            payload.write_text("第一章\n\n正文", encoding="utf-8")
            sha = MODULE.sha256_bytes(payload.read_bytes())
            argv = [
                str(MODULE_PATH), "--project", str(project), "--chapter", "1",
                "--detector", "   ", "--mode", "whole", "--score", "86",
                "--score-scale", "percent", "--verdict", "ai_like",
                "--expected-sha256", sha,
            ]
            with mock.patch.object(sys, "argv", argv), self.assertRaises(SystemExit):
                MODULE.main()

    def test_invalid_existing_log_fails_closed(self):
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / "external_detection_log.jsonl"
            log.write_text("{not-json}\n", encoding="utf-8")
            with self.assertRaises(ValueError):
                MODULE.existing_equivalent(log, {"chapter": 1})

    def test_equivalent_row_requires_exact_identity(self):
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / "external_detection_log.jsonl"
            row = {
                "chapter": 1, "detector": "zhuque", "mode": "whole",
                "score_percent": 86.0, "verdict": "ai_like",
                "body_sha256": "a" * 64, "payload_path": "chapters/01.md",
                "evidence_sha256": "",
            }
            log.write_text(json.dumps(row) + "\n", encoding="utf-8")
            self.assertEqual(MODULE.existing_equivalent(log, row), row)


if __name__ == "__main__":
    unittest.main()
