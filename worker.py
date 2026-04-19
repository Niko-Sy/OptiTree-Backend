import base64
import hashlib
import json
import os
import socket
import sys
import threading
import time
import traceback
import uuid
from contextlib import contextmanager
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any, Callable, Dict, Generator, List, Optional
from urllib.parse import urlparse

import redis
import requests


@dataclass
class WorkerConfig:
    redis_url: str = os.getenv("REDIS_URL", "redis://120.27.227.190:6888/0")
    redis_username: str = os.getenv("REDIS_USERNAME", "")
    redis_password: str = os.getenv("REDIS_PASSWORD", "Sy20050401")
    stream: str = os.getenv("AI_TASK_STREAM", "stream:ai:tasks")
    group: str = os.getenv("AI_TASK_GROUP", "ai-workers")
    consumer: str = os.getenv("AI_TASK_CONSUMER", f"worker-{socket.gethostname()}-{os.getpid()}")
    read_count: int = int(os.getenv("AI_TASK_READ_COUNT", "1"))
    block_ms: int = int(os.getenv("AI_TASK_BLOCK_MS", "5000"))
    max_retries: int = int(os.getenv("AI_TASK_MAX_RETRIES", "3"))
    dlq_stream: str = os.getenv("AI_TASK_DLQ_STREAM", "stream:ai:tasks:dlq")
    delayed_zset: str = os.getenv("AI_TASK_DELAYED_ZSET", "zset:ai:tasks:delayed")
    retry_backoff_base_ms: int = int(os.getenv("AI_TASK_RETRY_BACKOFF_BASE_MS", "2000"))
    retry_backoff_max_ms: int = int(os.getenv("AI_TASK_RETRY_BACKOFF_MAX_MS", "60000"))
    reclaim_idle_ms: int = int(os.getenv("AI_TASK_RECLAIM_IDLE_MS", "60000"))
    reclaim_batch: int = int(os.getenv("AI_TASK_RECLAIM_BATCH", "20"))
    release_batch: int = int(os.getenv("AI_TASK_RELEASE_BATCH", "20"))
    user_consumer_max_workers: int = int(os.getenv("AI_TASK_USER_CONSUMER_MAX_WORKERS", "1"))

    # callback_url: str = os.getenv("GO_CALLBACK_URL", "http://10.84.250.156:8000/internal/ai/tasks/callback")
    callback_url: str = os.getenv("GO_CALLBACK_URL", "http://10.84.250.156:8000/internal/ai/tasks/callback")
    callback_header: str = os.getenv("AI_TASK_CALLBACK_HEADER", "X-Internal-Token")
    callback_header_alt: str = os.getenv("AI_TASK_CALLBACK_HEADER_ALT", "")
    callback_token: str = os.getenv("AI_TASK_CALLBACK_TOKEN", "ZoCjxRlaqMaffTlRy2dIi2arr65Bq6drT0EGqUStlzbC3DyUmlI8K5Em92RUrc2S")
    callback_require_token: bool = os.getenv("AI_TASK_CALLBACK_REQUIRE_TOKEN", "0") == "1"

    storage_base_url: str = os.getenv("STORAGE_BASE_URL", "http://10.84.250.156:8000/static")
    storage_local_path: str = os.getenv("STORAGE_LOCAL_PATH", "./storage")
    storage_download_timeout_sec: int = int(os.getenv("STORAGE_DOWNLOAD_TIMEOUT_SEC", "60"))
    merged_markdown_dir: str = os.getenv("AI_TASK_MERGED_MARKDOWN_DIR", "./files")

    llm_server_url: str = os.getenv("LLM_SERVER_URL", "http://127.0.0.1:8001")

    ocr_url: str = os.getenv("OCR_URL", "https://v0ab68sfjbcf9ale.aistudio-app.com/layout-parsing")
    ocr_token: str = os.getenv("OCR_TOKEN", "1b20b0b886ddac0ac7326a00ab12284f7e8abd5c")
    ocr_vl15_url: str = os.getenv("OCR_VL15_URL", os.getenv("OCR_URL", "https://v0ab68sfjbcf9ale.aistudio-app.com/layout-parsing"))
    ocr_vl1_url: str = os.getenv("OCR_VL1_URL", "https://edfdvai6d9o0d2ce.aistudio-app.com/layout-parsing")
    ocr_v5_url: str = os.getenv("OCR_V5_URL", "https://u29fd7o5j4ff04ze.aistudio-app.com/ocr")
    ocr_vl15_token: str = os.getenv("OCR_VL15_TOKEN", os.getenv("OCR_TOKEN", "1b20b0b886ddac0ac7326a00ab12284f7e8abd5c"))
    ocr_vl1_token: str = os.getenv("OCR_VL1_TOKEN", os.getenv("OCR_TOKEN", "1b20b0b886ddac0ac7326a00ab12284f7e8abd5c"))
    ocr_v5_token: str = os.getenv("OCR_V5_TOKEN", os.getenv("OCR_TOKEN", "1b20b0b886ddac0ac7326a00ab12284f7e8abd5c"))
    ocr_timeout_sec: int = int(os.getenv("OCR_TIMEOUT_SEC", "300"))
    log_error_max_len: int = int(os.getenv("WORKER_LOG_ERROR_MAX_LEN", "4000"))
    log_traceback_max_lines: int = int(os.getenv("WORKER_LOG_TRACEBACK_MAX_LINES", "20"))


class CallbackError(RuntimeError):
    pass


class RetryableWorkerError(RuntimeError):
    pass


class StreamWorker:
    def __init__(self, cfg: WorkerConfig) -> None:
        self.cfg = cfg
        self.redis = redis.Redis.from_url(
            cfg.redis_url,
            decode_responses=True,
            username=(cfg.redis_username or None),
            password=(cfg.redis_password or None),
        )
        self._executor_lock = threading.Lock()
        self._user_executors: Dict[str, ThreadPoolExecutor] = {}

    def _short_text(self, value: Any, max_len: int = 512) -> str:
        text = str(value)
        if len(text) <= max_len:
            return text
        return text[:max_len] + f"...[truncated {len(text) - max_len} chars]"

    def _format_exception_detail(self, exc: BaseException) -> str:
        tb_lines = traceback.format_exception(type(exc), exc, exc.__traceback__)
        max_lines = max(1, int(self.cfg.log_traceback_max_lines))
        detail = "".join(tb_lines[:max_lines]).strip()
        max_len = max(256, int(self.cfg.log_error_max_len))
        return self._short_text(detail, max_len=max_len)

    def _log(self, level: str, message: str, **fields: Any) -> None:
        timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S.%f")[:-3]
        parts = [f"[{timestamp}]", f"[worker]", f"[{level.lower()}]", message]
        for key, value in fields.items():
            if value is None:
                continue
            text = self._short_text(value, max_len=600)
            parts.append(f"{key}={text}")
        line = " ".join(parts)
        if level.lower() in {"error", "warn", "warning"}:
            print(line, file=sys.stderr)
        else:
            print(line)

    def _run_stage(
        self,
        stage: str,
        fn: Callable[..., Any],
        *args: Any,
        task_id: str = "",
        project_id: str = "",
        attempt: int = 0,
        trace_id: str = "",
        message_id: str = "",
        **kwargs: Any,
    ) -> Any:
        started = time.perf_counter()
        self._log(
            "info",
            "stage start",
            stage=stage,
            taskId=task_id,
            projectId=project_id,
            attempt=attempt,
            traceId=trace_id,
            messageId=message_id,
        )
        try:
            result = fn(*args, **kwargs)
            elapsed_ms = int((time.perf_counter() - started) * 1000)
            self._log(
                "info",
                "stage done",
                stage=stage,
                elapsedMs=elapsed_ms,
                taskId=task_id,
                projectId=project_id,
                attempt=attempt,
                traceId=trace_id,
                messageId=message_id,
            )
            return result
        except Exception as exc:
            elapsed_ms = int((time.perf_counter() - started) * 1000)
            self._log(
                "error",
                "stage failed",
                stage=stage,
                elapsedMs=elapsed_ms,
                taskId=task_id,
                projectId=project_id,
                attempt=attempt,
                traceId=trace_id,
                messageId=message_id,
                error=f"{type(exc).__name__}: {exc}",
                traceback=self._format_exception_detail(exc),
            )
            if isinstance(exc, RetryableWorkerError):
                raise
            raise RetryableWorkerError(f"stage={stage} failed: {type(exc).__name__}: {exc}") from exc

    @contextmanager
    def _temporary_callback_override(
        self,
        callback: Callable[..., None],
    ) -> Generator[None, None, None]:
        original = self._callback
        self._callback = callback  # type: ignore[assignment]
        try:
            yield
        finally:
            self._callback = original  # type: ignore[assignment]

    def _normalize_local_documents(self, docs: Any) -> List[Dict[str, Any]]:
        if not isinstance(docs, list):
            return []

        normalized: List[Dict[str, Any]] = []
        for idx, doc in enumerate(docs, start=1):
            if isinstance(doc, str):
                source_url = doc
                file_path = Path(source_url)
                normalized.append(
                    {
                        "fileName": file_path.name,
                        "fileType": file_path.suffix.lstrip(".").lower(),
                        "sourceUrl": source_url,
                    }
                )
                continue

            if not isinstance(doc, dict):
                continue

            source_url = str(doc.get("sourceUrl") or doc.get("path") or doc.get("filePath") or "").strip()
            inline_text = str(doc.get("inlineText") or doc.get("content") or "").strip()
            file_name = str(doc.get("fileName") or "").strip()
            file_type = str(doc.get("fileType") or "").strip().lower()

            if source_url:
                file_path = Path(source_url)
                normalized.append(
                    {
                        "fileName": file_name or file_path.name,
                        "fileType": file_type or file_path.suffix.lstrip(".").lower(),
                        "sourceUrl": source_url,
                    }
                )
                continue

            if inline_text:
                normalized.append(
                    {
                        "fileName": file_name or f"inline-{idx}.md",
                        "fileType": file_type or "md",
                        "inlineText": inline_text,
                    }
                )

        return normalized

    def run_local_payload(
        self,
        payload: Dict[str, Any],
        progress_hook: Optional[Callable[[Dict[str, Any]], None]] = None,
    ) -> Dict[str, Any]:
        task_id = str(payload.get("taskId") or f"local-{int(time.time())}")
        project_id = str(payload.get("projectId") or "local-project")
        user_id = str(payload.get("userId") or "local-user")
        attempt = int(payload.get("attempt", 1) or 1)
        if attempt <= 0:
            attempt = 1
        trace_id = str(payload.get("traceId") or f"trace-{task_id}-{attempt}-{uuid.uuid4().hex[:8]}").strip()
        payload["traceId"] = trace_id

        events: List[Dict[str, Any]] = []

        def local_callback(
            task_id: str,
            project_id: str,
            status: str,
            progress: int,
            stage: str,
            stage_label: str,
            attempt: int,
            result: Optional[Dict[str, Any]] = None,
            error_message: str = "",
            trace_id: str = "",
            event_key: str = "",
        ) -> None:
            resolved_event_key = str(event_key or "").strip() or self._build_callback_event_key(
                task_id=task_id,
                attempt=attempt,
                status=status,
                stage=stage,
                progress=progress,
            )
            event = {
                "taskId": task_id,
                "projectId": project_id,
                "traceId": trace_id,
                "eventKey": resolved_event_key,
                "eventVersion": 1,
                "status": status,
                "progress": progress,
                "stage": stage,
                "stageLabel": stage_label,
                "attempt": attempt,
                "errorMessage": error_message,
                "result": result or {},
            }
            events.append(event)
            if progress_hook:
                progress_hook(event)

        with self._temporary_callback_override(local_callback):
            try:
                self._callback(
                    task_id=task_id,
                    project_id=project_id,
                    status="processing",
                    progress=5,
                    stage="accepted",
                    stage_label="本地任务已接收",
                    attempt=attempt,
                    trace_id=trace_id,
                )

                docs = self._normalize_local_documents(payload.get("documents", []))
                documents_text = self._run_stage(
                    "local-extract-documents",
                    self._extract_documents,
                    docs,
                    task_id,
                    project_id,
                    attempt,
                    trace_id,
                    task_id=task_id,
                    project_id=project_id,
                    attempt=attempt,
                    trace_id=trace_id,
                )
                merged_markdown = self._run_stage(
                    "local-merge-markdown",
                    self._merge_markdown_documents,
                    documents_text,
                    task_id=task_id,
                    project_id=project_id,
                    attempt=attempt,
                    trace_id=trace_id,
                )
                merged_markdown_path: Optional[str] = None
                if self._has_pdf_documents(docs):
                    merged_markdown_path = self._run_stage(
                        "local-save-merged-markdown",
                        self._save_merged_markdown,
                        merged_markdown,
                        task_id,
                        project_id,
                        task_id=task_id,
                        project_id=project_id,
                        attempt=attempt,
                        trace_id=trace_id,
                    )
                self._callback(
                    task_id=task_id,
                    project_id=project_id,
                    status="processing",
                    progress=35,
                    stage="ocr-complete",
                    stage_label="OCR 解析完成，正在进入 LLM 生成",
                    attempt=attempt,
                    trace_id=trace_id,
                )

                result = self._run_stage(
                    "local-llm-generation",
                    self._run_in_user_consumer,
                    user_id,
                    payload,
                    documents_text,
                    merged_markdown,
                    task_id,
                    project_id,
                    attempt,
                    trace_id,
                    task_id=task_id,
                    project_id=project_id,
                    attempt=attempt,
                    trace_id=trace_id,
                )
                result = self._attach_merged_markdown_path(result, merged_markdown_path)

                self._callback(
                    task_id=task_id,
                    project_id=project_id,
                    status="completed",
                    progress=100,
                    stage="completed",
                    stage_label="本地任务执行完成",
                    result=result,
                    attempt=attempt,
                    trace_id=trace_id,
                )

                return {
                    "taskId": task_id,
                    "projectId": project_id,
                    "attempt": attempt,
                    "mergedMarkdownPath": merged_markdown_path,
                    "result": result,
                    "events": events,
                }
            except Exception as exc:
                self._log(
                    "error",
                    "local payload failed",
                    taskId=task_id,
                    projectId=project_id,
                    attempt=attempt,
                    traceId=trace_id,
                    error=f"{type(exc).__name__}: {exc}",
                    traceback=self._format_exception_detail(exc),
                )
                self._safe_callback(
                    task_id=task_id,
                    project_id=project_id,
                    status="failed",
                    progress=0,
                    stage="failed",
                    stage_label="本地任务执行失败",
                    error_message=str(exc),
                    attempt=attempt,
                    trace_id=trace_id,
                )
                raise

    def ensure_group(self) -> None:
        try:
            self.redis.xgroup_create(self.cfg.stream, self.cfg.group, id="0", mkstream=True)
            print(f"[worker] created group={self.cfg.group} stream={self.cfg.stream}")
        except redis.exceptions.ResponseError as exc:
            if "BUSYGROUP" not in str(exc):
                raise

    def run(self) -> None:
        try:
            self.ensure_group()
        except redis.exceptions.AuthenticationError as exc:
            masked_url = self.cfg.redis_url
            if "@" in masked_url and ":" in masked_url:
                # Hide inline password if REDIS_URL already embeds credentials.
                scheme_and_auth, host_part = masked_url.split("@", 1)
                if ":" in scheme_and_auth:
                    scheme = scheme_and_auth.split(":", 1)[0]
                    masked_url = f"{scheme}://***@{host_part}"
            hint = (
                "Redis 认证失败，请配置 REDIS_PASSWORD（可选 REDIS_USERNAME）"
                " 或在 REDIS_URL 中携带凭据，例如 redis://:password@host:port/0"
            )
            raise RuntimeError(f"{hint}; current REDIS_URL={masked_url}") from exc

        print(f"[worker] start consumer={self.cfg.consumer}")
        if not self.cfg.callback_token.strip():
            print(
                "[worker] warning: AI_TASK_CALLBACK_TOKEN is empty; callback may fail with 401",
                file=sys.stderr,
            )

        while True:
            try:
                self._release_delayed_tasks()
                self._claim_stale_pending()

                entries = self.redis.xreadgroup(
                    groupname=self.cfg.group,
                    consumername=self.cfg.consumer,
                    streams={self.cfg.stream: ">"},
                    count=self.cfg.read_count,
                    block=self.cfg.block_ms,
                )
                if not entries:
                    continue

                for stream_name, messages in entries:
                    for message_id, values in messages:
                        self._handle_message(stream_name, message_id, values)
            except KeyboardInterrupt:
                self._log("info", "stopping")
                self._shutdown_user_executors()
                return
            except Exception as exc:
                self._log(
                    "error",
                    "loop error",
                    error=f"{type(exc).__name__}: {exc}",
                    traceback=self._format_exception_detail(exc),
                )
                time.sleep(1)

    def _get_user_executor(self, user_key: str) -> ThreadPoolExecutor:
        with self._executor_lock:
            existing = self._user_executors.get(user_key)
            if existing:
                return existing

            safe_name = "".join(ch if ch.isalnum() else "-" for ch in user_key).strip("-")[:24] or "anonymous"
            executor = ThreadPoolExecutor(
                max_workers=max(1, self.cfg.user_consumer_max_workers),
                thread_name_prefix=f"user-consumer-{safe_name}",
            )
            self._user_executors[user_key] = executor
            return executor

    def _shutdown_user_executors(self) -> None:
        with self._executor_lock:
            executors = list(self._user_executors.values())
            self._user_executors.clear()
        for executor in executors:
            executor.shutdown(wait=False, cancel_futures=True)

    def _merge_markdown_documents(self, documents_text: List[str]) -> str:
        merged_parts: List[str] = []
        for idx, text in enumerate(documents_text, start=1):
            cleaned = text.strip()
            if not cleaned:
                continue
            merged_parts.append(f"## Document {idx}\n\n{cleaned}")

        merged = "\n\n---\n\n".join(merged_parts).strip()
        if not merged:
            raise RetryableWorkerError("ocr markdown is empty after merge")
        return merged

    def _has_pdf_documents(self, docs: Any) -> bool:
        if not isinstance(docs, list):
            return False
        for doc in docs:
            if isinstance(doc, str):
                if Path(doc).suffix.lower() == ".pdf":
                    return True
                continue
            if not isinstance(doc, dict):
                continue
            file_type = str(doc.get("fileType", "")).strip().lower()
            if file_type == "pdf":
                return True
            source_url = str(doc.get("sourceUrl") or doc.get("path") or doc.get("filePath") or "").strip().lower()
            if source_url.endswith(".pdf"):
                return True
        return False

    def _save_merged_markdown(
        self,
        merged_markdown: str,
        task_id: str,
        project_id: str,
    ) -> str:
        output_dir = Path(self.cfg.merged_markdown_dir)
        output_dir.mkdir(parents=True, exist_ok=True)

        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S_%f")
        safe_task_id = "".join(ch if ch.isalnum() or ch in {"-", "_"} else "-" for ch in task_id).strip("-")
        if not safe_task_id:
            safe_task_id = "task"

        file_name = f"merged_markdown_{timestamp}_{safe_task_id}.md"
        output_path = output_dir / file_name
        output_path.write_text(merged_markdown, encoding="utf-8")

        print(
            "[worker] merged markdown saved "
            f"taskId={task_id} projectId={project_id} path={output_path}"
        )
        return str(output_path)

    def _attach_merged_markdown_path(
        self,
        result: Dict[str, Any],
        merged_markdown_path: Optional[str],
    ) -> Dict[str, Any]:
        if not merged_markdown_path:
            return result

        output = dict(result)
        output["mergedMarkdownPath"] = merged_markdown_path
        return output

    def _to_float(self, value: Any, default: float = 0.0) -> float:
        try:
            return float(value)
        except (TypeError, ValueError):
            return default

    def _summary_to_text(self, summary: Any) -> str:
        if isinstance(summary, str):
            text = summary.strip()
            return text or "生成完成"

        if isinstance(summary, dict):
            for key in ("text", "summary", "message", "phase"):
                candidate = summary.get(key)
                if isinstance(candidate, str) and candidate.strip():
                    return candidate.strip()
            return "生成完成"

        if summary is None:
            return "生成完成"

        text = str(summary).strip()
        return text or "生成完成"

    def _normalize_llm_result(
        self,
        endpoint: str,
        event: Dict[str, Any],
        trace_id: str,
    ) -> Dict[str, Any]:
        if endpoint == "/generate/fault-tree":
            fault_tree = event.get("faultTree")
            nodes = []
            edges = []
            if isinstance(fault_tree, dict):
                if isinstance(fault_tree.get("nodes"), list):
                    nodes = fault_tree.get("nodes", [])
                if isinstance(fault_tree.get("edges"), list):
                    edges = fault_tree.get("edges", [])

            if not nodes and isinstance(event.get("nodes"), list):
                nodes = event.get("nodes", [])
            if not edges and isinstance(event.get("edges"), list):
                edges = event.get("edges", [])

            summary_raw = event.get("summary")
            accuracy_raw = event.get("accuracy")
            if accuracy_raw is None and isinstance(summary_raw, dict):
                metrics = summary_raw.get("qualityMetrics")
                if isinstance(metrics, dict):
                    accuracy_raw = metrics.get("accuracy", metrics.get("score"))

            normalized: Dict[str, Any] = {
                "nodes": nodes,
                "edges": edges,
                "accuracy": self._to_float(accuracy_raw, 0.0),
                "summary": self._summary_to_text(summary_raw),
            }
            if isinstance(event.get("faultCodes"), list):
                normalized["faultCodes"] = event.get("faultCodes", [])

            resolved_trace = str(event.get("traceId") or trace_id or "").strip()
            if resolved_trace:
                normalized["traceId"] = resolved_trace
            return normalized

        normalized = dict(event)

        if "nodes" not in normalized and isinstance(normalized.get("rfNodes"), list):
            normalized["nodes"] = normalized.get("rfNodes", [])
        if "edges" not in normalized and isinstance(normalized.get("rfEdges"), list):
            normalized["edges"] = normalized.get("rfEdges", [])

        normalized["summary"] = self._summary_to_text(normalized.get("summary"))
        if isinstance(normalized.get("nodes"), list):
            normalized.setdefault("entityCount", len(normalized.get("nodes", [])))
        if isinstance(normalized.get("edges"), list):
            normalized.setdefault("relationCount", len(normalized.get("edges", [])))

        resolved_trace = str(normalized.get("traceId") or trace_id or "").strip()
        if resolved_trace:
            normalized["traceId"] = resolved_trace
        return normalized

    def _execute_llm_for_user(
        self,
        payload: Dict[str, Any],
        documents_text: List[str],
        merged_markdown: str,
        task_id: str,
        project_id: str,
        attempt: int,
        trace_id: str = "",
    ) -> Dict[str, Any]:
        current_trace_id = str(trace_id or payload.get("traceId") or f"trace-{task_id}-{attempt}-{uuid.uuid4().hex[:8]}").strip()
        task_type = str(payload.get("taskType", ""))
        if task_type == "generateFaultTree":
            return self._generate_fault_tree(payload, documents_text, merged_markdown, task_id, project_id, attempt, current_trace_id)
        if task_type == "generateKnowledgeGraph":
            return self._generate_knowledge_graph(payload, documents_text, merged_markdown, task_id, project_id, attempt, current_trace_id)
        raise RetryableWorkerError(f"unsupported taskType: {task_type}")

    def _run_in_user_consumer(
        self,
        user_id: str,
        payload: Dict[str, Any],
        documents_text: List[str],
        merged_markdown: str,
        task_id: str,
        project_id: str,
        attempt: int,
        trace_id: str = "",
    ) -> Dict[str, Any]:
        user_key = user_id.strip() or f"anonymous-{project_id or task_id}"
        executor = self._get_user_executor(user_key)
        future = executor.submit(
            self._execute_llm_for_user,
            payload,
            documents_text,
            merged_markdown,
            task_id,
            project_id,
            attempt,
            trace_id,
        )
        return future.result()

    def _release_delayed_tasks(self) -> None:
        now_ms = int(time.time() * 1000)
        due_payloads = self.redis.zrangebyscore(
            self.cfg.delayed_zset,
            "-inf",
            now_ms,
            start=0,
            num=self.cfg.release_batch,
        )
        if not due_payloads:
            return

        for payload_text in due_payloads:
            # zrem succeeds only once across all workers, preventing duplicate release.
            if self.redis.zrem(self.cfg.delayed_zset, payload_text):
                self.redis.xadd(self.cfg.stream, {"payload": payload_text})

    def _claim_stale_pending(self) -> None:
        try:
            pending = self.redis.xpending_range(
                self.cfg.stream,
                self.cfg.group,
                min="-",
                max="+",
                count=self.cfg.reclaim_batch,
                idle=self.cfg.reclaim_idle_ms,
            )
        except redis.exceptions.ResponseError as exc:
            if "NOGROUP" in str(exc):
                return
            raise

        message_ids: List[str] = []
        for item in pending:
            if isinstance(item, dict):
                msg_id = str(item.get("message_id", ""))
            else:
                msg_id = str(item[0]) if item else ""
            if msg_id:
                message_ids.append(msg_id)

        if not message_ids:
            return

        claimed = self.redis.xclaim(
            self.cfg.stream,
            self.cfg.group,
            self.cfg.consumer,
            min_idle_time=self.cfg.reclaim_idle_ms,
            message_ids=message_ids,
        )
        for msg_id, values in claimed:
            self._handle_message(self.cfg.stream, msg_id, values)

    def _handle_message(self, stream_name: str, message_id: str, values: Dict[str, str]) -> None:
        payload_text = values.get("payload", "")
        if not payload_text:
            self._log("warn", "drop message with empty payload", stream=stream_name, messageId=message_id)
            self.redis.xack(stream_name, self.cfg.group, message_id)
            return

        try:
            payload = json.loads(payload_text)
        except Exception as exc:
            self._log(
                "error",
                "invalid payload, message dropped",
                stream=stream_name,
                messageId=message_id,
                error=f"{type(exc).__name__}: {exc}",
                payloadSnippet=self._short_text(payload_text, max_len=300),
                traceback=self._format_exception_detail(exc),
            )
            self.redis.xack(stream_name, self.cfg.group, message_id)
            return

        stream_user_requirements = str(
            values.get("userRequirements")
            or values.get("user_requirements")
            or ""
        ).strip()
        payload_user_requirements = str(payload.get("userRequirements") or "").strip()
        payload["userRequirements"] = stream_user_requirements or payload_user_requirements

        stream_filename = str(values.get("filename") or "").strip()
        payload_filename = str(payload.get("filename") or "").strip()
        payload["filename"] = payload_filename or stream_filename

        task_id = payload.get("taskId", "")
        project_id = payload.get("projectId", "")
        user_id = str(payload.get("userId", ""))
        attempt = int(payload.get("attempt", 0))
        if attempt <= 0:
            attempt = 1
        trace_id = str(payload.get("traceId") or f"trace-{task_id}-{attempt}-{uuid.uuid4().hex[:8]}").strip()
        payload["traceId"] = trace_id
        resume_stage = str(payload.get("_resumeStage", "") or "").strip()
        resume_data = payload.get("_resumeData", {}) if isinstance(payload.get("_resumeData", {}), dict) else {}
        self._log(
            "info",
            "message accepted",
            stream=stream_name,
            messageId=message_id,
            taskId=task_id,
            projectId=project_id,
            taskType=payload.get("taskType", ""),
            attempt=attempt,
            traceId=trace_id,
            documentsCount=len(payload.get("documents", [])) if isinstance(payload.get("documents", []), list) else 0,
            hasUserRequirements=bool(payload.get("userRequirements")),
            filename=payload.get("filename", ""),
            resumeStage=resume_stage or "none",
        )

        try:
            self._callback(
                task_id=task_id,
                project_id=project_id,
                status="processing",
                progress=5,
                stage="accepted",
                stage_label="Worker 已接单",
                attempt=attempt,
                trace_id=trace_id,
            )

            docs = payload.get("documents", [])
            documents_text: List[str] = []
            merged_markdown = ""
            merged_markdown_path: Optional[str] = None
            resumed_from_llm = False

            if resume_stage == "llm-generation":
                candidate_path = str(resume_data.get("mergedMarkdownPath") or "").strip()
                if candidate_path and os.path.exists(candidate_path):
                    loaded = Path(candidate_path).read_text(encoding="utf-8", errors="ignore")
                    if loaded.strip():
                        resumed_from_llm = True
                        merged_markdown = loaded
                        merged_markdown_path = candidate_path
                        payload["_skipDynamicIndexing"] = True
                        self._log(
                            "info",
                            "resume from llm-generation",
                            taskId=task_id,
                            projectId=project_id,
                            attempt=attempt,
                            traceId=trace_id,
                            messageId=message_id,
                            mergedMarkdownPath=candidate_path,
                            mergedMarkdownChars=len(loaded),
                        )
                if not resumed_from_llm:
                    self._log(
                        "warn",
                        "resume requested but cache unavailable, fallback to full pipeline",
                        taskId=task_id,
                        projectId=project_id,
                        attempt=attempt,
                        traceId=trace_id,
                        messageId=message_id,
                        mergedMarkdownPath=str(resume_data.get("mergedMarkdownPath") or ""),
                    )

            if not resumed_from_llm:
                documents_text = self._run_stage(
                    "extract-documents",
                    self._extract_documents,
                    docs,
                    task_id,
                    project_id,
                    attempt,
                    trace_id,
                    task_id=task_id,
                    project_id=project_id,
                    attempt=attempt,
                    trace_id=trace_id,
                    message_id=message_id,
                )
                merged_markdown = self._run_stage(
                    "merge-markdown",
                    self._merge_markdown_documents,
                    documents_text,
                    task_id=task_id,
                    project_id=project_id,
                    attempt=attempt,
                    trace_id=trace_id,
                    message_id=message_id,
                )
                if self._has_pdf_documents(docs):
                    merged_markdown_path = self._run_stage(
                        "save-merged-markdown",
                        self._save_merged_markdown,
                        merged_markdown,
                        task_id,
                        project_id,
                        task_id=task_id,
                        project_id=project_id,
                        attempt=attempt,
                        trace_id=trace_id,
                        message_id=message_id,
                    )
                payload["_resumeStage"] = "llm-generation"
                payload["_resumeData"] = {"mergedMarkdownPath": str(merged_markdown_path or "")}
                payload["_skipDynamicIndexing"] = True
                self._callback(
                    task_id=task_id,
                    project_id=project_id,
                    status="processing",
                    progress=35,
                    stage="ocr-complete",
                    stage_label="OCR 解析完成，正在进入 LLM 生成",
                    attempt=attempt,
                    trace_id=trace_id,
                )
            else:
                self._callback(
                    task_id=task_id,
                    project_id=project_id,
                    status="processing",
                    progress=35,
                    stage="resume-llm",
                    stage_label="已复用文档解析结果，直接进入 LLM 生成",
                    attempt=attempt,
                    trace_id=trace_id,
                )

            result = self._run_stage(
                "llm-generation",
                self._run_in_user_consumer,
                user_id,
                payload,
                documents_text,
                merged_markdown,
                task_id,
                project_id,
                attempt,
                trace_id,
                task_id=task_id,
                project_id=project_id,
                attempt=attempt,
                trace_id=trace_id,
                message_id=message_id,
            )
            result = self._attach_merged_markdown_path(result, merged_markdown_path)

            self._callback(
                task_id=task_id,
                project_id=project_id,
                status="completed",
                progress=100,
                stage="completed",
                stage_label="生成完成",
                result=result,
                attempt=attempt,
                trace_id=trace_id,
            )
            payload.pop("_resumeStage", None)
            payload.pop("_resumeData", None)
            payload.pop("_skipDynamicIndexing", None)
            self.redis.xack(stream_name, self.cfg.group, message_id)
            self._log(
                "info",
                "message completed and acked",
                stream=stream_name,
                messageId=message_id,
                taskId=task_id,
                projectId=project_id,
                attempt=attempt,
                traceId=trace_id,
            )

        except RetryableWorkerError as exc:
            self._retry_or_dead_letter(
                stream_name,
                message_id,
                payload,
                attempt,
                project_id,
                task_id,
                str(exc),
                self._format_exception_detail(exc),
            )
        except Exception as exc:
            self._retry_or_dead_letter(
                stream_name,
                message_id,
                payload,
                attempt,
                project_id,
                task_id,
                f"unhandled exception: {type(exc).__name__}: {exc}",
                self._format_exception_detail(exc),
            )

    def _retry_or_dead_letter(
        self,
        stream_name: str,
        message_id: str,
        payload: Dict[str, Any],
        attempt: int,
        project_id: str,
        task_id: str,
        error_message: str,
        error_detail: str = "",
    ) -> None:
        trace_id = str(payload.get("traceId", "")).strip()
        next_attempt = attempt + 1
        final_error = self._short_text(error_message, max_len=1000)
        final_detail = self._short_text(error_detail, max_len=max(1000, self.cfg.log_error_max_len))
        if next_attempt <= self.cfg.max_retries:
            delay_ms = self._compute_backoff_ms(next_attempt)
            retry_at = int(time.time() * 1000) + delay_ms
            payload["attempt"] = next_attempt
            payload["lastError"] = final_error
            if final_detail:
                payload["lastErrorDetail"] = final_detail
            payload["retryAt"] = retry_at
            payload_text = json.dumps(payload, ensure_ascii=False)
            self.redis.zadd(self.cfg.delayed_zset, {payload_text: retry_at})
            self.redis.xack(stream_name, self.cfg.group, message_id)
            self._log(
                "warn",
                "message scheduled for retry",
                stream=stream_name,
                messageId=message_id,
                taskId=task_id,
                projectId=project_id,
                attempt=attempt,
                nextAttempt=next_attempt,
                traceId=trace_id,
                delayMs=delay_ms,
                error=final_error,
                errorDetail=final_detail,
            )
            self._safe_callback(
                task_id=task_id,
                project_id=project_id,
                status="retrying",
                progress=0,
                stage="retrying",
                stage_label=f"任务重试中 ({next_attempt}/{self.cfg.max_retries})，{delay_ms}ms 后重试",
                error_message=final_error,
                attempt=next_attempt,
                trace_id=trace_id,
            )
            return

        self.redis.xack(stream_name, self.cfg.group, message_id)
        self.redis.xadd(
            self.cfg.dlq_stream,
            {
                "payload": json.dumps(payload, ensure_ascii=False),
                "error": final_error,
                "errorDetail": final_detail,
                "failedAt": int(time.time()),
            },
        )
        self._log(
            "error",
            "message moved to dead letter queue",
            stream=stream_name,
            messageId=message_id,
            dlqStream=self.cfg.dlq_stream,
            taskId=task_id,
            projectId=project_id,
            attempt=attempt,
            traceId=trace_id,
            error=final_error,
            errorDetail=final_detail,
        )
        self._safe_callback(
            task_id=task_id,
            project_id=project_id,
            status="dead",
            progress=0,
            stage="dead",
            stage_label="任务进入死信队列",
            error_message=final_error,
            attempt=attempt,
            trace_id=trace_id,
        )

    def _compute_backoff_ms(self, attempt: int) -> int:
        if attempt <= 2:
            return self.cfg.retry_backoff_base_ms
        delay = self.cfg.retry_backoff_base_ms * (2 ** (attempt - 2))
        if delay > self.cfg.retry_backoff_max_ms:
            delay = self.cfg.retry_backoff_max_ms
        return delay

    def _build_callback_event_key(
        self,
        task_id: str,
        attempt: int,
        status: str,
        stage: str,
        progress: int,
    ) -> str:
        safe_task_id = "".join(ch if ch.isalnum() or ch in {"-", "_"} else "-" for ch in str(task_id or "task"))
        safe_task_id = safe_task_id.strip("-") or "task"
        safe_stage = "".join(ch if ch.isalnum() or ch in {"-", "_"} else "-" for ch in str(stage or "stage"))
        safe_stage = safe_stage.strip("-") or "stage"
        base = f"{safe_task_id}|{attempt}|{status}|{safe_stage}|{progress}"
        digest = hashlib.sha1(base.encode("utf-8")).hexdigest()[:12]
        return f"{safe_task_id}:{attempt}:{status}:{safe_stage}:{progress}:{digest}"

    def _safe_callback(self, **kwargs: Any) -> None:
        try:
            self._callback(**kwargs)
        except Exception as exc:
            self._log(
                "warn",
                "callback warning",
                error=f"{type(exc).__name__}: {exc}",
                traceback=self._format_exception_detail(exc),
                taskId=kwargs.get("task_id", ""),
                projectId=kwargs.get("project_id", ""),
                attempt=kwargs.get("attempt", ""),
                traceId=kwargs.get("trace_id", ""),
                stage=kwargs.get("stage", ""),
                status=kwargs.get("status", ""),
            )

    def _callback(
        self,
        task_id: str,
        project_id: str,
        status: str,
        progress: int,
        stage: str,
        stage_label: str,
        attempt: int,
        result: Optional[Dict[str, Any]] = None,
        error_message: str = "",
        trace_id: str = "",
        event_key: str = "",
    ) -> None:
        resolved_trace_id = str(trace_id or "").strip()
        resolved_event_key = str(event_key or "").strip() or self._build_callback_event_key(
            task_id=task_id,
            attempt=attempt,
            status=status,
            stage=stage,
            progress=progress,
        )

        headers = self._build_callback_headers(resolved_trace_id)
        body = {
            "taskId": task_id,
            "projectId": project_id,
            "traceId": resolved_trace_id,
            "eventKey": resolved_event_key,
            "eventVersion": 1,
            "status": status,
            "progress": progress,
            "stage": stage,
            "stageLabel": stage_label,
            "errorMessage": error_message,
            "result": result or {},
            "workerId": self.cfg.consumer,
            "attempt": attempt,
        }

        self._log(
            "info",
            "callback send",
            taskId=task_id,
            projectId=project_id,
            status=status,
            progress=progress,
            stage=stage,
            stageLabel=stage_label,
            attempt=attempt,
            traceId=resolved_trace_id,
            eventKey=resolved_event_key,
            callbackUrl=self.cfg.callback_url,
        )

        resp = requests.post(self.cfg.callback_url, json=body, headers=headers, timeout=30)
        if resp.status_code != 200:
            raise CallbackError(f"callback status={resp.status_code} body={resp.text[:256]}")

        try:
            data = resp.json()
        except Exception as exc:
            raise CallbackError(f"callback non-json response: {exc}") from exc

        if data.get("code") != 0:
            raise CallbackError(f"callback rejected: {data}")

        self._log(
            "info",
            "callback ack",
            taskId=task_id,
            projectId=project_id,
            statusCode=resp.status_code,
            code=data.get("code"),
            stage=stage,
            status=status,
            attempt=attempt,
            traceId=resolved_trace_id,
        )

    def _format_callback_token(self, header_name: str, token: str) -> str:
        if header_name.lower() == "authorization":
            lowered = token.lower()
            if not lowered.startswith("bearer ") and not lowered.startswith("basic "):
                return f"Bearer {token}"
        return token

    def _build_callback_headers(self, trace_id: str = "") -> Dict[str, str]:
        headers: Dict[str, str] = {"Content-Type": "application/json"}
        if trace_id:
            headers["X-Trace-Id"] = trace_id
        token = self.cfg.callback_token.strip()
        if self.cfg.callback_require_token and not token:
            raise CallbackError("callback token is required but empty")
        if not token:
            return headers

        primary = self.cfg.callback_header.strip()
        alt = self.cfg.callback_header_alt.strip()

        if primary:
            headers[primary] = self._format_callback_token(primary, token)
        if alt and alt not in headers:
            headers[alt] = self._format_callback_token(alt, token)
        return headers

    def _extract_documents(
        self,
        docs: List[Dict[str, Any]],
        task_id: str,
        project_id: str,
        attempt: int,
        trace_id: str = "",
    ) -> List[str]:
        if not docs:
            raise RetryableWorkerError("documents is empty")

        total = len(docs)
        output: List[str] = []
        for idx, doc in enumerate(docs, start=1):
            file_name = str(doc.get("fileName", ""))
            file_type = str(doc.get("fileType", "")).lower()
            inline_text = str(doc.get("inlineText", ""))
            self._log(
                "info",
                "document parse start",
                taskId=task_id,
                projectId=project_id,
                attempt=attempt,
                traceId=trace_id,
                docIndex=f"{idx}/{total}",
                fileName=file_name,
                fileType=file_type,
                hasInlineText=bool(inline_text),
            )
            if inline_text:
                text = inline_text[:100000]
                if not text.strip():
                    raise RetryableWorkerError(f"empty extracted text: {file_name or f'inline-{idx}'}")

                output.append(text)
                progress = 10 + int((idx / total) * 25)
                self._callback(
                    task_id=task_id,
                    project_id=project_id,
                    status="processing",
                    progress=progress,
                    stage="parsing",
                    stage_label=f"正在解析文档 ({idx}/{total})",
                    attempt=attempt,
                    trace_id=trace_id,
                )
                self._log(
                    "info",
                    "document parse done",
                    taskId=task_id,
                    projectId=project_id,
                    attempt=attempt,
                    traceId=trace_id,
                    docIndex=f"{idx}/{total}",
                    charCount=len(text),
                    source="inline",
                )
                continue

            source_url = str(doc.get("sourceUrl", ""))
            local_path = self._source_url_to_local_path(source_url)
            self._log(
                "info",
                "document path resolved",
                taskId=task_id,
                projectId=project_id,
                attempt=attempt,
                traceId=trace_id,
                docIndex=f"{idx}/{total}",
                sourceUrl=source_url,
                localPath=str(local_path),
                fileType=file_type,
            )
            if not local_path.exists() and self._is_http_url(source_url):
                local_path = self._download_source_url(source_url, file_name, file_type)
            if not local_path.exists():
                raise RetryableWorkerError(
                    f"document not found: sourceUrl={source_url} localPath={local_path}"
                )

            if file_type in {"txt", "md"}:
                text = local_path.read_text(encoding="utf-8", errors="ignore")[:100000]
            elif file_type == "pdf":
                text = self._extract_binary_document(local_path, file_type)
            elif file_type in {"doc", "docx", "xls", "xlsx"}:
                # Keep generation flowing for non-PDF binary docs until dedicated parsers are added.
                text = f"[Document: {file_name} ({file_type}) - non-PDF binary extraction not yet supported]"
            else:
                text = f"[Document: {file_name} ({file_type}) - unsupported format]"

            if not text.strip():
                raise RetryableWorkerError(f"empty extracted text: {file_name}")

            output.append(text)

            progress = 10 + int((idx / total) * 25)
            self._callback(
                task_id=task_id,
                project_id=project_id,
                status="processing",
                progress=progress,
                stage="parsing",
                stage_label=f"正在解析文档 ({idx}/{total})",
                attempt=attempt,
                trace_id=trace_id,
            )
            self._log(
                "info",
                "document parse done",
                taskId=task_id,
                projectId=project_id,
                attempt=attempt,
                traceId=trace_id,
                docIndex=f"{idx}/{total}",
                fileName=file_name,
                fileType=file_type,
                localPath=str(local_path),
                charCount=len(text),
            )

        return output

    def _source_url_to_local_path(self, source_url: str) -> Path:
        source_url = source_url.split("?", 1)[0].strip()
        if not source_url:
            return Path("")

        prefix = self.cfg.storage_base_url.rstrip("/") + "/"
        if source_url.startswith(prefix):
            rel_path = source_url[len(prefix):]
            rel_path = rel_path.replace("/", os.sep)
            return Path(self.cfg.storage_local_path) / rel_path

        parsed = urlparse(source_url)
        if parsed.scheme and parsed.path:
            # Allow source URL host mismatch as long as URL path is under /static/.
            if parsed.path.startswith("/static/"):
                rel_path = parsed.path[len("/static/"):].replace("/", os.sep)
                return Path(self.cfg.storage_local_path) / rel_path

        if source_url.startswith("/static/"):
            rel_path = source_url[len("/static/"):].replace("/", os.sep)
            return Path(self.cfg.storage_local_path) / rel_path
        if source_url.startswith("static/"):
            rel_path = source_url[len("static/"):].replace("/", os.sep)
            return Path(self.cfg.storage_local_path) / rel_path
        return Path(source_url)

    def _is_http_url(self, source_url: str) -> bool:
        parsed = urlparse(str(source_url or "").strip())
        return parsed.scheme in {"http", "https"} and bool(parsed.netloc)

    def _download_source_url(self, source_url: str, file_name: str, file_type: str) -> Path:
        parsed = urlparse(source_url)
        if parsed.scheme not in {"http", "https"}:
            return Path(source_url)

        ext = ("." + file_type.strip().lower()) if file_type else ""
        if not ext:
            ext = Path(parsed.path).suffix.lower()
        if not ext:
            ext = ".bin"

        safe_name = "".join(ch if ch.isalnum() or ch in {"-", "_"} else "-" for ch in (file_name or "doc"))
        safe_name = safe_name.strip("-") or "doc"
        digest = hashlib.sha1(source_url.encode("utf-8")).hexdigest()[:12]

        cache_dir = Path(self.cfg.storage_local_path) / "_worker_cache"
        cache_dir.mkdir(parents=True, exist_ok=True)
        cache_path = cache_dir / f"{safe_name}-{digest}{ext}"
        if cache_path.exists() and cache_path.stat().st_size > 0:
            self._log("info", "download cache hit", sourceUrl=source_url, cachePath=str(cache_path))
            return cache_path

        timeout_sec = max(5, int(self.cfg.storage_download_timeout_sec))
        self._log("info", "download start", sourceUrl=source_url, timeoutSec=timeout_sec, cachePath=str(cache_path))
        try:
            with requests.get(source_url, stream=True, timeout=timeout_sec) as resp:
                if resp.status_code != 200:
                    raise RetryableWorkerError(
                        f"download source failed: status={resp.status_code} url={source_url}"
                    )
                with open(cache_path, "wb") as fp:
                    for chunk in resp.iter_content(chunk_size=64 * 1024):
                        if chunk:
                            fp.write(chunk)
        except RetryableWorkerError:
            raise
        except Exception as exc:
            raise RetryableWorkerError(f"download source failed: url={source_url} err={exc}") from exc

        if not cache_path.exists() or cache_path.stat().st_size == 0:
            raise RetryableWorkerError(f"download source empty: url={source_url}")

        self._log(
            "info",
            "download done",
            sourceUrl=source_url,
            cachePath=str(cache_path),
            bytes=cache_path.stat().st_size,
        )

        return cache_path

    def _extract_binary_document(self, local_path: Path, file_type: str) -> str:
        if not local_path.exists():
            raise RetryableWorkerError(f"ocr source file not found: {local_path}")

        file_b64 = base64.b64encode(local_path.read_bytes()).decode("ascii")
        ocr_file_type = 0 if file_type == "pdf" else 1
        timeout_sec = max(30, int(self.cfg.ocr_timeout_sec))

        candidates = [
            {
                "name": "vl1.5",
                "kind": "layout-parsing",
                "url": (self.cfg.ocr_vl15_url or "").strip(),
                "token": (self.cfg.ocr_vl15_token or "").strip(),
            },
            {
                "name": "vl1",
                "kind": "layout-parsing",
                "url": (self.cfg.ocr_vl1_url or "").strip(),
                "token": (self.cfg.ocr_vl1_token or "").strip(),
            },
            {
                "name": "v5",
                "kind": "ocr-v5",
                "url": (self.cfg.ocr_v5_url or "").strip(),
                "token": (self.cfg.ocr_v5_token or "").strip(),
            },
        ]

        self._log(
            "info",
            "ocr fallback start",
            filePath=str(local_path),
            fileType=file_type,
            timeoutSec=timeout_sec,
            candidateOrder=">".join(item["name"] for item in candidates),
        )

        failures: List[str] = []
        for api in candidates:
            api_name = str(api["name"])
            api_kind = str(api["kind"])
            api_url = str(api["url"])
            api_token = str(api["token"])

            if not api_url:
                reason = f"{api_name}: empty url"
                failures.append(reason)
                self._log("warn", "ocr candidate skipped", api=api_name, reason=reason)
                continue
            if not api_token:
                reason = f"{api_name}: empty token"
                failures.append(reason)
                self._log("warn", "ocr candidate skipped", api=api_name, reason=reason, url=api_url)
                continue

            self._log("info", "ocr candidate start", api=api_name, kind=api_kind, url=api_url)
            try:
                markdown_text = self._invoke_single_ocr(
                    api_name=api_name,
                    api_kind=api_kind,
                    api_url=api_url,
                    api_token=api_token,
                    file_b64=file_b64,
                    file_type=ocr_file_type,
                    timeout_sec=timeout_sec,
                )
                cleaned = markdown_text.strip()
                if not cleaned:
                    raise RetryableWorkerError(f"{api_name} returned empty markdown")
                self._log(
                    "info",
                    "ocr candidate success",
                    api=api_name,
                    kind=api_kind,
                    charCount=len(cleaned),
                )
                return cleaned
            except Exception as exc:
                detail = f"{api_name} failed: {type(exc).__name__}: {exc}"
                failures.append(self._short_text(detail, max_len=500))
                self._log(
                    "warn",
                    "ocr candidate failed",
                    api=api_name,
                    kind=api_kind,
                    url=api_url,
                    error=detail,
                    traceback=self._format_exception_detail(exc),
                )

        raise RetryableWorkerError(
            "all OCR APIs failed in order [vl1.5 -> vl1 -> v5]: " + " | ".join(failures)
        )

    def _invoke_single_ocr(
        self,
        api_name: str,
        api_kind: str,
        api_url: str,
        api_token: str,
        file_b64: str,
        file_type: int,
        timeout_sec: int,
    ) -> str:
        headers = {
            "Content-Type": "application/json",
            "Authorization": f"token {api_token}",
        }

        if api_kind == "layout-parsing":
            payload = {
                "file": file_b64,
                "fileType": file_type,
                "useDocOrientationClassify": False,
                "useDocUnwarping": False,
                "useChartRecognition": False,
            }
        else:
            payload = {
                "file": file_b64,
                "fileType": file_type,
                "visualize": False,
                "useDocOrientationClassify": False,
                "useDocUnwarping": False,
                "useTextlineOrientation": False,
            }

        resp = requests.post(api_url, json=payload, headers=headers, timeout=timeout_sec)
        if resp.status_code != 200:
            raise RetryableWorkerError(
                f"{api_name} status={resp.status_code} body={self._short_text(resp.text, max_len=300)}"
            )

        try:
            data = resp.json()
        except Exception as exc:
            raise RetryableWorkerError(f"{api_name} response is not json: {exc}") from exc

        if not isinstance(data, dict):
            raise RetryableWorkerError(f"{api_name} response root is not object")

        if api_kind == "layout-parsing":
            return self._parse_layout_markdown(data, api_name)
        return self._parse_v5_markdown(data, api_name)

    def _parse_layout_markdown(self, data: Dict[str, Any], api_name: str) -> str:
        result = data.get("result", {})
        if not isinstance(result, dict):
            raise RetryableWorkerError(f"{api_name} missing result object")

        pages = result.get("layoutParsingResults", [])
        if not isinstance(pages, list) or not pages:
            raise RetryableWorkerError(f"{api_name} missing layoutParsingResults")

        texts: List[str] = []
        for page_idx, page in enumerate(pages, start=1):
            if not isinstance(page, dict):
                continue
            markdown = page.get("markdown", {})
            if not isinstance(markdown, dict):
                continue
            txt = str(markdown.get("text", "")).strip()
            if txt:
                texts.append(f"<!-- page:{page_idx} api:{api_name} -->\n{txt}")

        if not texts:
            raise RetryableWorkerError(f"{api_name} returned empty markdown text")
        return "\n\n---\n\n".join(texts)

    def _parse_v5_markdown(self, data: Dict[str, Any], api_name: str) -> str:
        result = data.get("result", {})
        if not isinstance(result, dict):
            raise RetryableWorkerError(f"{api_name} missing result object")

        pages = result.get("ocrResults", [])
        if not isinstance(pages, list) or not pages:
            raise RetryableWorkerError(f"{api_name} missing ocrResults")

        page_texts: List[str] = []
        for page_idx, page in enumerate(pages, start=1):
            if not isinstance(page, dict):
                continue

            lines: List[str] = []
            pruned = page.get("prunedResult", {})
            if isinstance(pruned, dict):
                rec_texts = pruned.get("rec_texts")
                if isinstance(rec_texts, list):
                    lines.extend(str(item).strip() for item in rec_texts if str(item).strip())
                elif isinstance(rec_texts, str) and rec_texts.strip():
                    lines.append(rec_texts.strip())

            if not lines:
                fallback_text = page.get("text")
                if isinstance(fallback_text, str) and fallback_text.strip():
                    lines.append(fallback_text.strip())

            if lines:
                page_body = "\n".join(lines)
                page_texts.append(f"<!-- page:{page_idx} api:{api_name} -->\n{page_body}")

        if not page_texts:
            raise RetryableWorkerError(f"{api_name} returned empty text list")
        return "\n\n---\n\n".join(page_texts)

    def _generate_fault_tree(
        self,
        payload: Dict[str, Any],
        documents_text: List[str],
        merged_markdown: str,
        task_id: str,
        project_id: str,
        attempt: int,
        trace_id: str,
    ) -> Dict[str, Any]:
        req = {
            "docsContext": merged_markdown,
            "topEvent": payload.get("topEvent", ""),
            "userRequirements": str(payload.get("userRequirements") or "").strip(),
            "filename": str(payload.get("filename") or "").strip(),
            "faultCodeMap": payload.get("faultCodeMap", {}),
            "projectId": payload.get("projectId", ""),
            "traceId": trace_id,
            "skipDynamicIndexing": bool(payload.get("_skipDynamicIndexing", False)),
            "config": {
                "quality": payload.get("config", {}).get("quality", "balanced"),
                "model": payload.get("config", {}).get("model", ""),
                "depth": payload.get("config", {}).get("depth", 8),
                "maxNodes": payload.get("config", {}).get("maxNodes", 60),
            },
        }
        return self._consume_llm_sse(
            endpoint="/generate/fault-tree",
            request_payload=req,
            task_id=task_id,
            project_id=project_id,
            attempt=attempt,
            trace_id=trace_id,
        )

    def _generate_knowledge_graph(
        self,
        payload: Dict[str, Any],
        documents_text: List[str],
        merged_markdown: str,
        task_id: str,
        project_id: str,
        attempt: int,
        trace_id: str,
    ) -> Dict[str, Any]:
        req = {
            "docsContext": merged_markdown,
            "projectId": payload.get("projectId", ""),
            "traceId": trace_id,
            "config": {
                "quality": payload.get("config", {}).get("quality", "balanced"),
                "model": payload.get("config", {}).get("model", ""),
                "entityTypes": payload.get("config", {}).get("entityTypes", []),
            },
        }
        return self._consume_llm_sse(
            endpoint="/generate/knowledge-graph",
            request_payload=req,
            task_id=task_id,
            project_id=project_id,
            attempt=attempt,
            trace_id=trace_id,
        )

    def _consume_llm_sse(
        self,
        endpoint: str,
        request_payload: Dict[str, Any],
        task_id: str,
        project_id: str,
        attempt: int,
        trace_id: str = "",
    ) -> Dict[str, Any]:
        url = self.cfg.llm_server_url.rstrip("/") + endpoint
        headers = {"X-Trace-Id": trace_id} if trace_id else None
        self._log(
            "info",
            "llm request start",
            taskId=task_id,
            projectId=project_id,
            attempt=attempt,
            traceId=trace_id,
            endpoint=endpoint,
            url=url,
        )
        with requests.post(url, json=request_payload, headers=headers, stream=True, timeout=600) as resp:
            if resp.status_code != 200:
                raise RetryableWorkerError(f"llm server failed: status={resp.status_code} body={resp.text[:200]}")

            result: Optional[Dict[str, Any]] = None
            event_count = 0
            for raw_line in resp.iter_lines(decode_unicode=True):
                if not raw_line:
                    continue
                line = raw_line.strip()
                if not line.startswith("data:"):
                    continue
                body = line[5:].strip()
                if body == "[DONE]":
                    break

                try:
                    event = json.loads(body)
                except json.JSONDecodeError:
                    self._log(
                        "warn",
                        "llm sse decode failed",
                        taskId=task_id,
                        projectId=project_id,
                        attempt=attempt,
                        traceId=trace_id,
                        endpoint=endpoint,
                        lineSnippet=self._short_text(body, max_len=200),
                    )
                    continue
                event_count += 1
                event_type = event.get("type")
                if event_type == "progress":
                    llm_progress = int(event.get("progress", 0))
                    mapped = 35 + int(llm_progress * 0.55)
                    if mapped > 90:
                        mapped = 90
                    self._callback(
                        task_id=task_id,
                        project_id=project_id,
                        status="processing",
                        progress=mapped,
                        stage=str(event.get("stage", "generating")),
                        stage_label=str(event.get("message", "AI 生成中")),
                        attempt=attempt,
                        trace_id=trace_id,
                    )
                elif event_type == "error":
                    raise RetryableWorkerError(str(event.get("message", "llm generation failed")))
                elif event_type == "result":
                    result = self._normalize_llm_result(endpoint, event, trace_id)
                    self._log(
                        "info",
                        "llm result event received",
                        taskId=task_id,
                        projectId=project_id,
                        attempt=attempt,
                        traceId=trace_id,
                        endpoint=endpoint,
                        eventCount=event_count,
                    )

            if not result:
                raise RetryableWorkerError("llm result missing")
            self._log(
                "info",
                "llm request done",
                taskId=task_id,
                projectId=project_id,
                attempt=attempt,
                traceId=trace_id,
                endpoint=endpoint,
                eventCount=event_count,
            )
            return result


def main() -> None:
    cfg = WorkerConfig()
    worker = StreamWorker(cfg)
    worker.run()


if __name__ == "__main__":
    main()
