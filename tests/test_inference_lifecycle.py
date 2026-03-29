"""Unit tests for inference lifecycle modules."""

import json
import os
import sys
import tempfile
import pytest

# Add project root to path for imports
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from internal.inference.lifecycle.registry import Registry, ModelStatus, ModelRecord
from internal.inference.lifecycle.hardware import compute_optimal_ngl, HardwareProfile
from internal.inference.lifecycle.discover import (
    score_signal, filter_candidates, parse_candidates, DiscoveryCandidate
)
from internal.inference.lifecycle.serve import generate_systemd_unit
from internal.inference.lifecycle.validate import parse_sse_results


class TestRegistryStateMachine:
    """Test model lifecycle state transitions in the registry."""

    def setup_method(self):
        """Create a temporary database for each test."""
        self.tmp = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp.close()
        self.registry = Registry(db_path=self.tmp.name)

    def teardown_method(self):
        """Clean up temporary database."""
        self.registry.close()
        os.unlink(self.tmp.name)

    def test_registry_state_machine(self):
        """Test full lifecycle: discovered -> downloading -> validating -> passed -> registered -> serving."""
        model = self.registry.add_model("test-model-7b", source_url="https://hf.co/test")
        assert model.status == ModelStatus.discovered
        assert model.id is not None
        assert model.name == "test-model-7b"

        # discovered -> downloading
        model = self.registry.update_status(model.id, ModelStatus.downloading)
        assert model.status == ModelStatus.downloading

        # downloading -> validating
        model = self.registry.update_status(model.id, ModelStatus.validating)
        assert model.status == ModelStatus.validating
        assert model.validated_at is not None

        # validating -> passed
        model = self.registry.update_status(model.id, ModelStatus.passed)
        assert model.status == ModelStatus.passed

        # passed -> registered
        model = self.registry.update_status(model.id, ModelStatus.registered)
        assert model.status == ModelStatus.registered
        assert model.registered_at is not None

        # registered -> serving
        model = self.registry.update_status(model.id, ModelStatus.serving)
        assert model.status == ModelStatus.serving
        assert model.serving_since is not None

    def test_invalid_transition_raises(self):
        """Test that invalid state transitions raise ValueError."""
        model = self.registry.add_model("bad-transition")
        with pytest.raises(ValueError, match="Invalid transition"):
            self.registry.update_status(model.id, ModelStatus.serving)

    def test_registry_promote(self):
        """Test that promoting a model retires the current serving model."""
        # Create and promote first model
        m1 = self.registry.add_model("model-a")
        self.registry.update_status(m1.id, ModelStatus.downloading)
        self.registry.update_status(m1.id, ModelStatus.validating)
        self.registry.update_status(m1.id, ModelStatus.passed)
        self.registry.update_status(m1.id, ModelStatus.registered)
        self.registry.update_status(m1.id, ModelStatus.serving)

        # Create and promote second model
        m2 = self.registry.add_model("model-b")
        self.registry.update_status(m2.id, ModelStatus.downloading)
        self.registry.update_status(m2.id, ModelStatus.validating)
        self.registry.update_status(m2.id, ModelStatus.passed)
        self.registry.update_status(m2.id, ModelStatus.registered)

        result = self.registry.promote(m2.id)
        assert result.status == ModelStatus.serving

        # First model should be retired
        m1_updated = self.registry.get_model(m1.id)
        assert m1_updated.status == ModelStatus.retired

        # Only one serving model
        serving = self.registry.get_serving_model()
        assert serving.id == m2.id

    def test_list_models_with_filter(self):
        """Test listing models with status filter."""
        self.registry.add_model("alpha")
        m2 = self.registry.add_model("beta")
        self.registry.update_status(m2.id, ModelStatus.downloading)

        discovered = self.registry.list_models(ModelStatus.discovered)
        assert len(discovered) == 1
        assert discovered[0].name == "alpha"

        downloading = self.registry.list_models(ModelStatus.downloading)
        assert len(downloading) == 1
        assert downloading[0].name == "beta"

        all_models = self.registry.list_models()
        assert len(all_models) == 2

    def test_update_benchmark(self):
        """Test recording benchmark results."""
        model = self.registry.add_model("bench-model")
        self.registry.update_benchmark(model.id, "toolcall-15", 12.0, 45.5, 120.3)

        updated = self.registry.get_model(model.id)
        assert updated.toolcall15_score == 12.0
        assert updated.tok_s_gen == 45.5
        assert updated.tok_s_prompt == 120.3


class TestHardwareNGL:
    """Test GPU layer computation heuristics."""

    def test_compute_optimal_ngl_standard(self):
        """Test NGL calculation for standard case."""
        # 24GB VRAM, 7GB model, 80 layers
        ngl = compute_optimal_ngl(7.0, 24.0, total_layers=80)
        expected = int((24.0 - 2.0) / 7.0 * 80)  # ~251 -> capped at 251
        assert ngl == min(expected, 999)
        assert ngl > 0

    def test_compute_optimal_ngl_small_vram(self):
        """Test NGL with insufficient VRAM."""
        ngl = compute_optimal_ngl(7.0, 2.0)
        assert ngl == 0

    def test_compute_optimal_ngl_zero_vram(self):
        """Test NGL with no VRAM (CPU only)."""
        ngl = compute_optimal_ngl(7.0, 0.0)
        assert ngl == 0

    def test_compute_optimal_ngl_large_model(self):
        """Test NGL with model larger than VRAM."""
        # 8GB VRAM, 70GB model
        ngl = compute_optimal_ngl(70.0, 8.0, total_layers=80)
        assert ngl > 0
        assert ngl < 80  # Can't fit all layers

    def test_compute_optimal_ngl_cap_at_999(self):
        """Test NGL is capped at 999."""
        ngl = compute_optimal_ngl(0.5, 48.0, total_layers=200)
        assert ngl == 999

    def test_compute_optimal_ngl_zero_model_size(self):
        """Test NGL with zero model size."""
        ngl = compute_optimal_ngl(0.0, 24.0)
        assert ngl == 0


class TestSignalScoring:
    """Test social signal scoring."""

    def test_score_signal_basic(self):
        """Test score calculation with all metrics."""
        tweet = {"likes": 100, "bookmarks": 50, "retweets": 30}
        score = score_signal(tweet)
        expected = 100 * 1.0 + 50 * 2.0 + 30 * 1.5  # 100 + 100 + 45 = 245
        assert score == expected

    def test_score_signal_alternative_keys(self):
        """Test score with alternative key names."""
        tweet = {"like_count": 200, "bookmark_count": 10, "retweet_count": 40}
        score = score_signal(tweet)
        expected = 200 * 1.0 + 10 * 2.0 + 40 * 1.5
        assert score == expected

    def test_score_signal_missing_fields(self):
        """Test score with missing engagement fields."""
        tweet = {"likes": 50}
        score = score_signal(tweet)
        assert score == 50.0

    def test_score_signal_empty(self):
        """Test score with empty tweet."""
        assert score_signal({}) == 0.0


class TestCandidateFiltering:
    """Test candidate filtering by hardware constraints."""

    def _make_candidate(self, name: str, size_gb=None):
        return DiscoveryCandidate(
            name=name, hf_url=f"https://hf.co/{name}",
            quant="Q4_K_M", size_gb_est=size_gb,
            signal_score=100.0, source_tweet_id="123",
            discovered_at="2025-01-01T00:00:00",
        )

    def test_filter_by_size(self):
        """Test filtering out models too large for hardware."""
        candidates = [
            self._make_candidate("small", 3.0),
            self._make_candidate("medium", 7.0),
            self._make_candidate("large", 40.0),
        ]
        filtered = filter_candidates(candidates, max_size_gb=10.0)
        assert len(filtered) == 2
        names = [c.name for c in filtered]
        assert "small" in names
        assert "medium" in names
        assert "large" not in names

    def test_filter_keeps_unknown_size(self):
        """Test that candidates with unknown size are kept."""
        candidates = [
            self._make_candidate("known", 5.0),
            self._make_candidate("unknown", None),
        ]
        filtered = filter_candidates(candidates, max_size_gb=4.0)
        assert len(filtered) == 1  # unknown kept, known=5 filtered out
        # Wait, known=5 > 4, so filtered out. unknown=None kept.
        names = [c.name for c in filtered]
        assert "unknown" in names

    def test_filter_all_pass(self):
        """Test when all candidates fit."""
        candidates = [self._make_candidate("a", 2.0), self._make_candidate("b", 3.0)]
        filtered = filter_candidates(candidates, max_size_gb=50.0)
        assert len(filtered) == 2


class TestSystemdUnitGeneration:
    """Test systemd unit file generation."""

    def test_generate_unit_content(self):
        """Test that generated unit file has correct content."""
        unit = generate_systemd_unit(
            model_path="/models/test.gguf",
            ngl=33, port=8080, threads=16, context_size=4096
        )
        assert "[Unit]" in unit
        assert "[Service]" in unit
        assert "[Install]" in unit
        assert "/models/test.gguf" in unit
        assert "-ngl 33" in unit
        assert "--port 8080" in unit
        assert "-t 16" in unit
        assert "-c 4096" in unit
        assert "Restart=on-failure" in unit
        assert "WantedBy=multi-user.target" in unit

    def test_generate_unit_binary_path(self):
        """Test that the unit references the correct binary."""
        unit = generate_systemd_unit("/m.gguf", ngl=0, port=9090, threads=4, context_size=2048)
        assert "llama-server" in unit
        assert "llama-cpp-turboquant-cuda" in unit


class TestValidationResultParsing:
    """Test ToolCall-15 SSE output parsing."""

    def test_parse_sse_all_pass(self):
        """Test parsing SSE output where all scenarios pass."""
        sse = "\n".join([
            f'data: {{"type": "scenario_result", "scenario": "scenario_{i}", "passed": true}}'
            for i in range(15)
        ])
        result = parse_sse_results(sse)
        assert result["score"] == 15
        assert len(result["details"]) == 15
        assert all(result["details"].values())

    def test_parse_sse_mixed_results(self):
        """Test parsing SSE output with mixed pass/fail."""
        lines = []
        for i in range(15):
            passed = i < 10  # 10 pass, 5 fail
            lines.append(
                f'data: {{"type": "scenario_result", "scenario": "sc_{i}", "passed": {str(passed).lower()}}}'
            )
        sse = "\n".join(lines)
        result = parse_sse_results(sse)
        assert result["score"] == 10
        assert sum(1 for v in result["details"].values() if v) == 10
        assert sum(1 for v in result["details"].values() if not v) == 5

    def test_parse_sse_with_noise(self):
        """Test parsing SSE with non-result events mixed in."""
        sse = """data: {"type": "status", "message": "starting"}
data: {"type": "scenario_result", "scenario": "weather", "passed": true}
data: {"type": "progress", "percent": 50}
data: {"type": "scenario_result", "scenario": "calendar", "passed": false}
data: {"type": "status", "message": "done"}
"""
        result = parse_sse_results(sse)
        assert result["score"] == 1
        assert result["details"]["weather"] is True
        assert result["details"]["calendar"] is False

    def test_parse_sse_empty(self):
        """Test parsing empty SSE output."""
        result = parse_sse_results("")
        assert result["score"] == 0
        assert result["details"] == {}

    def test_parse_sse_malformed_json(self):
        """Test parsing SSE with malformed JSON lines."""
        sse = """data: {"type": "scenario_result", "scenario": "ok", "passed": true}
data: {invalid json here
data: {"type": "scenario_result", "scenario": "also_ok", "passed": true}
"""
        result = parse_sse_results(sse)
        assert result["score"] == 2


class TestParseCandidates:
    """Test tweet parsing for model candidates."""

    def test_parse_hf_url(self):
        """Test extracting HuggingFace URLs from tweets."""
        tweets = json.dumps([{
            "id": "1",
            "text": "New model released! Check out https://huggingface.co/TheBloke/Llama-2-7B-GGUF Q4_K_M quantization",
            "likes": 50, "bookmarks": 10, "retweets": 5,
        }])
        candidates = parse_candidates(tweets)
        assert len(candidates) == 1
        assert candidates[0].hf_url == "https://huggingface.co/TheBloke/Llama-2-7B-GGUF"
        assert candidates[0].quant == "Q4_K_M"

    def test_parse_deduplicates(self):
        """Test that duplicate HF URLs are deduplicated."""
        tweets = json.dumps([
            {"id": "1", "text": "https://huggingface.co/user/model-GGUF great!", "likes": 10},
            {"id": "2", "text": "https://huggingface.co/user/model-GGUF amazing!", "likes": 20},
        ])
        candidates = parse_candidates(tweets)
        assert len(candidates) == 1


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
