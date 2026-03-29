"""Model registry with SQLite backend for inference lifecycle management."""

import enum
import logging
import sqlite3
import uuid
from dataclasses import dataclass, field, asdict
from datetime import datetime
from pathlib import Path
from typing import List, Optional

logger = logging.getLogger(__name__)


class ModelStatus(enum.Enum):
    """Lifecycle states for a model."""
    discovered = "discovered"
    downloading = "downloading"
    validating = "validating"
    passed = "passed"
    failed = "failed"
    registered = "registered"
    serving = "serving"
    retired = "retired"


# Valid state transitions
VALID_TRANSITIONS = {
    ModelStatus.discovered: {ModelStatus.downloading, ModelStatus.failed},
    ModelStatus.downloading: {ModelStatus.validating, ModelStatus.failed},
    ModelStatus.validating: {ModelStatus.passed, ModelStatus.failed},
    ModelStatus.passed: {ModelStatus.registered, ModelStatus.failed},
    ModelStatus.failed: {ModelStatus.discovered},  # allow retry
    ModelStatus.registered: {ModelStatus.serving, ModelStatus.retired},
    ModelStatus.serving: {ModelStatus.retired},
    ModelStatus.retired: {ModelStatus.registered},  # allow re-register
}


@dataclass
class ModelRecord:
    """Complete record for a tracked model."""
    id: str
    name: str
    gguf_path: Optional[str] = None
    quant: Optional[str] = None
    size_gb: Optional[float] = None
    vram_required_gb: Optional[float] = None
    toolcall15_score: Optional[float] = None
    tok_s_gen: Optional[float] = None
    tok_s_prompt: Optional[float] = None
    source_url: Optional[str] = None
    signal_score: Optional[float] = None
    status: ModelStatus = ModelStatus.discovered
    discovered_at: Optional[str] = None
    validated_at: Optional[str] = None
    registered_at: Optional[str] = None
    serving_since: Optional[str] = None


class Registry:
    """SQLite-backed model registry managing the full inference lifecycle."""

    def __init__(self, db_path: str = "inference_lifecycle.db"):
        self.db_path = db_path
        self._conn: Optional[sqlite3.Connection] = None
        self.init_db()

    def _get_conn(self) -> sqlite3.Connection:
        if self._conn is None:
            self._conn = sqlite3.connect(self.db_path)
            self._conn.row_factory = sqlite3.Row
        return self._conn

    def init_db(self) -> None:
        """Create tables if they don't exist."""
        conn = self._get_conn()
        conn.executescript("""
            CREATE TABLE IF NOT EXISTS models (
                id TEXT PRIMARY KEY,
                name TEXT NOT NULL,
                gguf_path TEXT,
                quant TEXT,
                size_gb REAL,
                vram_required_gb REAL,
                toolcall15_score REAL,
                tok_s_gen REAL,
                tok_s_prompt REAL,
                source_url TEXT,
                signal_score REAL,
                status TEXT NOT NULL DEFAULT 'discovered',
                discovered_at TEXT,
                validated_at TEXT,
                registered_at TEXT,
                serving_since TEXT
            );
            CREATE TABLE IF NOT EXISTS benchmark_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                model_id TEXT NOT NULL,
                eval_suite TEXT NOT NULL,
                score REAL,
                tok_s_gen REAL,
                tok_s_prompt REAL,
                timestamp TEXT NOT NULL,
                FOREIGN KEY (model_id) REFERENCES models(id)
            );
            CREATE INDEX IF NOT EXISTS idx_models_status ON models(status);
            CREATE INDEX IF NOT EXISTS idx_bench_model ON benchmark_history(model_id);
        """)
        conn.commit()
        logger.info("Registry database initialized at %s", self.db_path)

    def add_model(self, name: str, source_url: Optional[str] = None,
                  quant: Optional[str] = None, size_gb: Optional[float] = None,
                  signal_score: Optional[float] = None) -> ModelRecord:
        """Add a newly discovered model to the registry."""
        model_id = str(uuid.uuid4())[:12]
        now = datetime.utcnow().isoformat()
        record = ModelRecord(
            id=model_id, name=name, source_url=source_url, quant=quant,
            size_gb=size_gb, signal_score=signal_score,
            status=ModelStatus.discovered, discovered_at=now,
        )
        conn = self._get_conn()
        conn.execute(
            """INSERT INTO models (id, name, gguf_path, quant, size_gb, vram_required_gb,
               toolcall15_score, tok_s_gen, tok_s_prompt, source_url, signal_score,
               status, discovered_at, validated_at, registered_at, serving_since)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            (record.id, record.name, record.gguf_path, record.quant, record.size_gb,
             record.vram_required_gb, record.toolcall15_score, record.tok_s_gen,
             record.tok_s_prompt, record.source_url, record.signal_score,
             record.status.value, record.discovered_at, record.validated_at,
             record.registered_at, record.serving_since)
        )
        conn.commit()
        logger.info("Added model %s (%s) to registry", model_id, name)
        return record

    def update_status(self, model_id: str, new_status: ModelStatus) -> ModelRecord:
        """Transition a model to a new status with validation."""
        current = self.get_model(model_id)
        if current is None:
            raise ValueError(f"Model {model_id} not found")
        if new_status not in VALID_TRANSITIONS.get(current.status, set()):
            raise ValueError(
                f"Invalid transition: {current.status.value} -> {new_status.value}"
            )
        conn = self._get_conn()
        now = datetime.utcnow().isoformat()
        updates = {"status": new_status.value}
        if new_status == ModelStatus.validating:
            updates["validated_at"] = now
        elif new_status == ModelStatus.registered:
            updates["registered_at"] = now
        elif new_status == ModelStatus.serving:
            updates["serving_since"] = now

        set_clause = ", ".join(f"{k} = ?" for k in updates)
        conn.execute(f"UPDATE models SET {set_clause} WHERE id = ?",
                     list(updates.values()) + [model_id])
        conn.commit()
        logger.info("Model %s: %s -> %s", model_id, current.status.value, new_status.value)
        return self.get_model(model_id)

    def update_benchmark(self, model_id: str, eval_suite: str, score: float,
                         tok_s_gen: float, tok_s_prompt: float) -> None:
        """Record benchmark results for a model."""
        conn = self._get_conn()
        now = datetime.utcnow().isoformat()
        conn.execute(
            """INSERT INTO benchmark_history (model_id, eval_suite, score,
               tok_s_gen, tok_s_prompt, timestamp) VALUES (?, ?, ?, ?, ?, ?)""",
            (model_id, eval_suite, score, tok_s_gen, tok_s_prompt, now)
        )
        conn.execute(
            """UPDATE models SET toolcall15_score = ?, tok_s_gen = ?,
               tok_s_prompt = ? WHERE id = ?""",
            (score, tok_s_gen, tok_s_prompt, model_id)
        )
        conn.commit()
        logger.info("Benchmark recorded for %s: score=%.1f gen=%.1f prompt=%.1f",
                     model_id, score, tok_s_gen, tok_s_prompt)

    def get_model(self, model_id: str) -> Optional[ModelRecord]:
        """Retrieve a single model by ID."""
        conn = self._get_conn()
        row = conn.execute("SELECT * FROM models WHERE id = ?", (model_id,)).fetchone()
        if row is None:
            return None
        return self._row_to_record(row)

    def list_models(self, status_filter: Optional[ModelStatus] = None) -> List[ModelRecord]:
        """List models, optionally filtered by status."""
        conn = self._get_conn()
        if status_filter:
            rows = conn.execute("SELECT * FROM models WHERE status = ? ORDER BY discovered_at DESC",
                                (status_filter.value,)).fetchall()
        else:
            rows = conn.execute("SELECT * FROM models ORDER BY discovered_at DESC").fetchall()
        return [self._row_to_record(r) for r in rows]

    def get_serving_model(self) -> Optional[ModelRecord]:
        """Get the currently serving model, if any."""
        models = self.list_models(ModelStatus.serving)
        return models[0] if models else None

    def promote(self, model_id: str) -> ModelRecord:
        """Promote a model to serving, retiring any current serving model."""
        current_serving = self.get_serving_model()
        if current_serving and current_serving.id != model_id:
            self.retire(current_serving.id)
        model = self.get_model(model_id)
        if model is None:
            raise ValueError(f"Model {model_id} not found")
        if model.status == ModelStatus.serving:
            return model
        if model.status not in (ModelStatus.registered, ModelStatus.passed):
            # Allow promoting from registered or passed
            if model.status != ModelStatus.registered:
                self.update_status(model_id, ModelStatus.registered)
        return self.update_status(model_id, ModelStatus.serving)

    def retire(self, model_id: str) -> ModelRecord:
        """Retire a model from serving."""
        model = self.get_model(model_id)
        if model is None:
            raise ValueError(f"Model {model_id} not found")
        if model.status == ModelStatus.retired:
            return model
        if model.status != ModelStatus.serving:
            raise ValueError(f"Can only retire serving models, got {model.status.value}")
        return self.update_status(model_id, ModelStatus.retired)

    def _row_to_record(self, row: sqlite3.Row) -> ModelRecord:
        """Convert a database row to a ModelRecord."""
        return ModelRecord(
            id=row["id"], name=row["name"], gguf_path=row["gguf_path"],
            quant=row["quant"], size_gb=row["size_gb"],
            vram_required_gb=row["vram_required_gb"],
            toolcall15_score=row["toolcall15_score"],
            tok_s_gen=row["tok_s_gen"], tok_s_prompt=row["tok_s_prompt"],
            source_url=row["source_url"], signal_score=row["signal_score"],
            status=ModelStatus(row["status"]),
            discovered_at=row["discovered_at"], validated_at=row["validated_at"],
            registered_at=row["registered_at"], serving_since=row["serving_since"],
        )

    def close(self) -> None:
        """Close the database connection."""
        if self._conn:
            self._conn.close()
            self._conn = None
