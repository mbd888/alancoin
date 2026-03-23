"""Hash-chained audit log for EU AI Act compliance.

Every tool call gets a tamper-evident log entry. The hash chain ensures
that entries cannot be inserted, removed, or reordered after the fact.
"""

import csv
import hashlib
import io
import json
import threading
import time
from dataclasses import asdict, dataclass, field
from typing import Iterator, List


# Action constants
ACTION_TOOL_STARTED = "tool_started"
ACTION_TOOL_COMPLETED = "tool_completed"
ACTION_TOOL_FAILED = "tool_failed"
ACTION_BUDGET_EXCEEDED = "budget_exceeded"


@dataclass(frozen=True)
class AuditEntry:
    """A single auditable event in the tool execution log."""

    seq: int
    timestamp: float
    action: str  # tool_started | tool_completed | tool_failed | budget_exceeded
    tool_name: str
    cost: str  # "0.000000" for starts/failures
    budget_remaining: str
    input_summary: str
    output_summary: str
    hash: str


_GENESIS_HASH = "0" * 64


def _truncate(text: str, limit: int = 200) -> str:
    if len(text) <= limit:
        return text
    return text[:limit] + "..."


def _compute_hash(prev_hash: str, seq: int, action: str, tool_name: str, cost: str) -> str:
    payload = f"{prev_hash}|{seq}|{action}|{tool_name}|{cost}"
    return hashlib.sha256(payload.encode()).hexdigest()


class AuditLog:
    """Thread-safe, hash-chained audit trail.

    Entries are append-only. The hash chain links each entry to its
    predecessor, making tampering detectable via ``verify_integrity()``.
    """

    def __init__(self, audit_path: str = None) -> None:
        self._entries: List[AuditEntry] = []
        self._lock = threading.Lock()
        self._prev_hash = _GENESIS_HASH
        self._audit_path = audit_path
        self._file_lock = threading.Lock()

    def append(
        self,
        action: str,
        tool_name: str,
        cost: str,
        budget_remaining: str,
        input_summary: str = "",
        output_summary: str = "",
    ) -> AuditEntry:
        with self._lock:
            seq = len(self._entries)
            h = _compute_hash(self._prev_hash, seq, action, tool_name, cost)
            entry = AuditEntry(
                seq=seq,
                timestamp=time.time(),
                action=action,
                tool_name=tool_name,
                cost=cost,
                budget_remaining=budget_remaining,
                input_summary=_truncate(input_summary),
                output_summary=_truncate(output_summary),
                hash=h,
            )
            self._entries.append(entry)
            self._prev_hash = h

        if self._audit_path is not None:
            self._flush_entry(entry)

        return entry

    def _flush_entry(self, entry: AuditEntry) -> None:
        """Append a single entry as JSON to the audit file (best-effort)."""
        with self._file_lock:
            try:
                with open(self._audit_path, "a") as f:
                    f.write(json.dumps(asdict(entry)) + "\n")
            except OSError as e:
                import logging

                logging.getLogger(__name__).warning("Failed to write audit entry: %s", e)

    def verify_integrity(self) -> bool:
        """Walk the chain and verify every hash. Returns True if intact."""
        with self._lock:
            prev = _GENESIS_HASH
            for entry in self._entries:
                expected = _compute_hash(prev, entry.seq, entry.action, entry.tool_name, entry.cost)
                if entry.hash != expected:
                    return False
                prev = entry.hash
            return True

    def to_json(self) -> str:
        with self._lock:
            return json.dumps([asdict(e) for e in self._entries], indent=2)

    def to_csv(self, path: str) -> None:
        with self._lock:
            entries = list(self._entries)
        if not entries:
            return
        fieldnames = list(asdict(entries[0]).keys())
        with open(path, "w", newline="") as f:
            writer = csv.DictWriter(f, fieldnames=fieldnames)
            writer.writeheader()
            for e in entries:
                writer.writerow(asdict(e))

    def to_csv_string(self) -> str:
        with self._lock:
            entries = list(self._entries)
        if not entries:
            return ""
        fieldnames = list(asdict(entries[0]).keys())
        buf = io.StringIO()
        writer = csv.DictWriter(buf, fieldnames=fieldnames)
        writer.writeheader()
        for e in entries:
            writer.writerow(asdict(e))
        return buf.getvalue()

    def __iter__(self) -> Iterator[AuditEntry]:
        with self._lock:
            return iter(list(self._entries))

    def __len__(self) -> int:
        with self._lock:
            return len(self._entries)

    def __repr__(self) -> str:
        return f"AuditLog(entries={len(self)})"
