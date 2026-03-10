import asyncio
import json
import os
from typing import AsyncIterator, List, Optional

from fastapi import FastAPI
from fastapi.responses import JSONResponse, StreamingResponse
from pydantic import BaseModel, Field


app = FastAPI(
    title="OptiTree Mock LLM Server",
    version="0.1.0",
    description="用于联调 Go `internal/ai/llm_server.go` 的简易 FastAPI 模拟服务。",
)


def get_delay() -> float:
    raw = os.getenv("LLM_DEBUG_DELAY", "0.25")
    try:
        value = float(raw)
    except ValueError:
        return 0.25
    return max(0.0, value)


def sse_data(payload: object) -> str:
    if payload == "[DONE]":
        return "data: [DONE]\n\n"
    return f"data: {json.dumps(payload, ensure_ascii=False)}\n\n"


class FTConfig(BaseModel):
    quality: Optional[str] = None
    model: Optional[str] = None
    depth: Optional[int] = 4
    max_nodes: Optional[int] = 30


class FaultTreeRequest(BaseModel):
    documents: List[str] = Field(default_factory=list)
    top_event: str
    config: FTConfig = Field(default_factory=FTConfig)


class KGConfig(BaseModel):
    quality: Optional[str] = None
    model: Optional[str] = None
    entity_types: List[str] = Field(default_factory=lambda: ["component", "event", "cause"])


class KnowledgeGraphRequest(BaseModel):
    documents: List[str] = Field(default_factory=list)
    config: KGConfig = Field(default_factory=KGConfig)


def should_fail(model: Optional[str]) -> bool:
    if not model:
        return False
    lowered = model.lower()
    return lowered in {"fail", "error", "mock-error"}


def first_doc_excerpt(documents: List[str], fallback: str) -> str:
    for doc in documents:
        text = " ".join(doc.replace("\n", " ").split())
        if text:
            return text[:32]
    return fallback


def build_fault_tree(req: FaultTreeRequest) -> dict:
    top_event = req.top_event.strip() or "顶事件"
    cause_a = first_doc_excerpt(req.documents, "液压泄漏")
    cause_b = "压力阀卡滞"
    cause_c = "泵体过热"

    nodes = [
        {
            "id": "ft_root",
            "type": "topEvent",
            "data": {"label": top_event, "nodeType": "topEvent"},
            "position": {"x": 420, "y": 40},
        },
        {
            "id": "ft_gate_1",
            "type": "gateNode",
            "data": {"label": "OR", "nodeType": "orGate"},
            "position": {"x": 420, "y": 170},
        },
        {
            "id": "ft_mid_1",
            "type": "intermediateEvent",
            "data": {"label": "主回路异常", "nodeType": "intermediateEvent"},
            "position": {"x": 240, "y": 320},
        },
        {
            "id": "ft_basic_1",
            "type": "basicEvent",
            "data": {"label": cause_a, "nodeType": "basicEvent", "probability": 0.03},
            "position": {"x": 600, "y": 320},
        },
        {
            "id": "ft_gate_2",
            "type": "gateNode",
            "data": {"label": "AND", "nodeType": "andGate"},
            "position": {"x": 240, "y": 460},
        },
        {
            "id": "ft_basic_2",
            "type": "basicEvent",
            "data": {"label": cause_b, "nodeType": "basicEvent", "probability": 0.02},
            "position": {"x": 130, "y": 610},
        },
        {
            "id": "ft_basic_3",
            "type": "basicEvent",
            "data": {"label": cause_c, "nodeType": "basicEvent", "probability": 0.01},
            "position": {"x": 350, "y": 610},
        },
    ]

    edges = [
        {"id": "fte_1", "source": "ft_root", "target": "ft_gate_1"},
        {"id": "fte_2", "source": "ft_gate_1", "target": "ft_mid_1"},
        {"id": "fte_3", "source": "ft_gate_1", "target": "ft_basic_1"},
        {"id": "fte_4", "source": "ft_mid_1", "target": "ft_gate_2"},
        {"id": "fte_5", "source": "ft_gate_2", "target": "ft_basic_2"},
        {"id": "fte_6", "source": "ft_gate_2", "target": "ft_basic_3"},
    ]

    return {
        "type": "result",
        "nodes": nodes,
        "edges": edges,
        "accuracy": 0.87,
        "summary": f"已为顶事件“{top_event}”生成 mock 故障树，共 {len(nodes)} 个节点。",
    }


def build_knowledge_graph(req: KnowledgeGraphRequest) -> dict:
    entity_type = req.config.entity_types[0] if req.config.entity_types else "component"
    entity_a = first_doc_excerpt(req.documents, "液压泵")
    entity_b = "压力传感器"
    entity_c = "压力不足"

    nodes = [
        {
            "id": "kg_1",
            "type": "entityNode",
            "data": {"label": entity_a, "entityType": entity_type, "description": "文档中提取的核心实体"},
            "position": {"x": 120, "y": 120},
        },
        {
            "id": "kg_2",
            "type": "entityNode",
            "data": {"label": entity_b, "entityType": "component", "description": "用于监测系统压力"},
            "position": {"x": 420, "y": 120},
        },
        {
            "id": "kg_3",
            "type": "eventNode",
            "data": {"label": entity_c, "entityType": "event", "description": "系统异常表现"},
            "position": {"x": 270, "y": 320},
        },
    ]

    edges = [
        {"id": "kge_1", "source": "kg_1", "target": "kg_3", "data": {"label": "导致"}},
        {"id": "kge_2", "source": "kg_2", "target": "kg_3", "data": {"label": "检测到"}},
    ]

    return {
        "type": "result",
        "nodes": nodes,
        "edges": edges,
        "entity_count": len(nodes),
        "relation_count": len(edges),
        "summary": f"已生成 mock 知识图谱，共 {len(nodes)} 个实体、{len(edges)} 条关系。",
    }


async def fault_tree_events(req: FaultTreeRequest) -> AsyncIterator[str]:
    delay = get_delay()

    if should_fail(req.config.model):
        yield sse_data({"type": "error", "message": f"mock fault-tree error for model: {req.config.model}"})
        yield sse_data("[DONE]")
        return

    stages = [
        {"type": "progress", "stage": "received", "progress": 10, "message": "已接收请求"},
        {"type": "progress", "stage": "analyzing", "progress": 35, "message": "正在分析文档"},
        {"type": "progress", "stage": "structuring", "progress": 68, "message": "正在构建故障树结构"},
        {"type": "progress", "stage": "finalizing", "progress": 90, "message": "正在整理结果"},
    ]
    for event in stages:
        yield sse_data(event)
        await asyncio.sleep(delay)

    yield sse_data(build_fault_tree(req))
    yield sse_data("[DONE]")


async def knowledge_graph_events(req: KnowledgeGraphRequest) -> AsyncIterator[str]:
    delay = get_delay()

    if should_fail(req.config.model):
        yield sse_data({"type": "error", "message": f"mock knowledge-graph error for model: {req.config.model}"})
        yield sse_data("[DONE]")
        return

    stages = [
        {"type": "progress", "stage": "received", "progress": 10, "message": "已接收请求"},
        {"type": "progress", "stage": "extracting", "progress": 40, "message": "正在提取实体关系"},
        {"type": "progress", "stage": "linking", "progress": 72, "message": "正在构建图谱关系"},
        {"type": "progress", "stage": "finalizing", "progress": 92, "message": "正在整理结果"},
    ]
    for event in stages:
        yield sse_data(event)
        await asyncio.sleep(delay)

    yield sse_data(build_knowledge_graph(req))
    yield sse_data("[DONE]")


@app.get("/healthz")
async def healthz() -> JSONResponse:
    return JSONResponse({"status": "ok"})


@app.post("/generate/fault-tree")
async def generate_fault_tree(req: FaultTreeRequest) -> StreamingResponse:
    return StreamingResponse(fault_tree_events(req), media_type="text/event-stream")


@app.post("/generate/knowledge-graph")
async def generate_knowledge_graph(req: KnowledgeGraphRequest) -> StreamingResponse:
    return StreamingResponse(knowledge_graph_events(req), media_type="text/event-stream")


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(
        "app:app",
        host=os.getenv("LLM_DEBUG_HOST", "127.0.0.1"),
        port=int(os.getenv("LLM_DEBUG_PORT", "8001")),
        reload=False,
    )