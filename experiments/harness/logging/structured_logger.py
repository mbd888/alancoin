"""
Thread-safe JSONL logger for experiment events.

Writes one JSON object per line, enabling efficient streaming analysis
and filtering by event type.
"""

import atexit
import json
import threading
from datetime import datetime
from pathlib import Path
from typing import Optional, TextIO

from .events import Event, EventType


class StructuredLogger:
    """
    Thread-safe structured event logger.

    Writes events to JSONL files (one JSON object per line).
    Supports filtering, buffering, and atomic writes.
    """

    def __init__(
        self,
        log_dir: str | Path,
        run_id: str,
        study: str = "",
        buffer_size: int = 100,
        enabled: bool = True,
    ):
        """
        Initialize the structured logger.

        Args:
            log_dir: Directory to write log files
            run_id: Unique identifier for this experiment run
            study: Study name (e.g., "study1", "pre_study")
            buffer_size: Number of events to buffer before flushing
            enabled: Whether logging is enabled
        """
        self.log_dir = Path(log_dir)
        self.run_id = run_id
        self.study = study
        self.buffer_size = buffer_size
        self.enabled = enabled

        self._buffer: list[dict] = []
        self._lock = threading.Lock()
        self._file_handles: dict[str, TextIO] = {}
        self._event_count = 0

        if self.enabled:
            self._ensure_log_dir()
            atexit.register(self.close)

    def _ensure_log_dir(self):
        """Create log directory structure."""
        study_dir = self.log_dir / self.study if self.study else self.log_dir
        study_dir.mkdir(parents=True, exist_ok=True)
        self._study_dir = study_dir

    def _get_log_path(self, event_type: Optional[EventType] = None) -> Path:
        """Get log file path for an event type."""
        if event_type:
            filename = f"{self.run_id}_{event_type.value}.jsonl"
        else:
            filename = f"{self.run_id}_all.jsonl"
        return self._study_dir / filename

    def _get_file_handle(self, event_type: EventType) -> TextIO:
        """Get or create file handle for event type."""
        key = event_type.value
        if key not in self._file_handles:
            path = self._get_log_path(event_type)
            self._file_handles[key] = open(path, "a", encoding="utf-8")

            # Also open the combined log
            if "all" not in self._file_handles:
                all_path = self._get_log_path(None)
                self._file_handles["all"] = open(all_path, "a", encoding="utf-8")

        return self._file_handles[key]

    def log(self, event: Event):
        """
        Log an event.

        Thread-safe. Events are buffered and flushed periodically.

        Args:
            event: Event to log
        """
        if not self.enabled:
            return

        # Ensure event has run_id and study
        event.run_id = self.run_id
        event.study = self.study

        event_dict = event.to_dict()

        with self._lock:
            self._buffer.append(event_dict)
            self._event_count += 1

            if len(self._buffer) >= self.buffer_size:
                self._flush_buffer()

    def _flush_buffer(self):
        """Flush buffered events to disk. Must be called with lock held."""
        if not self._buffer:
            return

        for event_dict in self._buffer:
            event_type = EventType(event_dict.get("event_type", "error"))

            # Write to type-specific log
            handle = self._get_file_handle(event_type)
            handle.write(json.dumps(event_dict, default=str) + "\n")

            # Write to combined log
            if "all" in self._file_handles:
                self._file_handles["all"].write(json.dumps(event_dict, default=str) + "\n")

        self._buffer.clear()

        # Flush all file handles
        for handle in self._file_handles.values():
            handle.flush()

    def flush(self):
        """Manually flush all buffered events."""
        with self._lock:
            self._flush_buffer()

    def close(self):
        """Close all file handles and flush remaining events."""
        with self._lock:
            self._flush_buffer()
            for handle in self._file_handles.values():
                handle.close()
            self._file_handles.clear()

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()

    @property
    def event_count(self) -> int:
        """Total number of events logged."""
        with self._lock:
            return self._event_count

    def get_log_paths(self) -> dict[str, Path]:
        """Get paths to all log files created."""
        paths = {}
        if hasattr(self, "_study_dir"):
            for path in self._study_dir.glob(f"{self.run_id}_*.jsonl"):
                event_type = path.stem.replace(f"{self.run_id}_", "")
                paths[event_type] = path
        return paths


class LogReader:
    """Reader for JSONL log files."""

    def __init__(self, log_path: str | Path):
        self.log_path = Path(log_path)

    def iter_events(self, event_types: Optional[list[str]] = None):
        """
        Iterate over events in the log file.

        Args:
            event_types: Optional filter for event types

        Yields:
            Event dictionaries
        """
        with open(self.log_path, "r", encoding="utf-8") as f:
            for line in f:
                if not line.strip():
                    continue
                try:
                    event = json.loads(line)
                    if event_types is None or event.get("event_type") in event_types:
                        yield event
                except json.JSONDecodeError:
                    continue

    def get_llm_calls(self, agent_id: Optional[str] = None, model: Optional[str] = None):
        """Get LLM call events with optional filtering."""
        for event in self.iter_events(["llm_call"]):
            if agent_id and event.get("agent_id") != agent_id:
                continue
            if model and event.get("model") != model:
                continue
            yield event

    def get_transactions(self, status: Optional[str] = None):
        """Get transaction events with optional filtering."""
        for event in self.iter_events(["transaction"]):
            if status and event.get("status") != status:
                continue
            yield event

    def get_reasoning_traces(self, model: Optional[str] = None):
        """Get reasoning trace events with optional filtering."""
        for event in self.iter_events(["reasoning_trace"]):
            if model and event.get("model") != model:
                continue
            yield event

    def count_by_type(self) -> dict[str, int]:
        """Count events by type."""
        counts: dict[str, int] = {}
        for event in self.iter_events():
            event_type = event.get("event_type", "unknown")
            counts[event_type] = counts.get(event_type, 0) + 1
        return counts


def generate_run_id(study: str = "") -> str:
    """Generate a unique run ID."""
    timestamp = datetime.utcnow().strftime("%Y%m%d_%H%M%S")
    import random
    suffix = f"{random.randint(1000, 9999):04d}"
    if study:
        return f"{study}_{timestamp}_{suffix}"
    return f"run_{timestamp}_{suffix}"
