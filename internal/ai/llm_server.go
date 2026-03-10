// Package ai — LLM Server client.
// LLMServerClient drives the self-hosted FastAPI generation service.
// The service streams progress and results via Server-Sent Events (SSE).
//
// ─── FastAPI interface contract ────────────────────────────────────────────────
//
// POST /generate/fault-tree
//
//	Request body (JSON):
//	  {
//	    "documents":  ["page1 markdown", "page2 markdown"],
//	    "top_event":  "液压系统完全失效",
//	    "config": {
//	      "quality":   "balanced",    // fast | balanced | precise
//	      "model":     "qwen-plus",   // passed through to the LLM
//	      "depth":     4,
//	      "max_nodes": 30
//	    }
//	  }
//
// POST /generate/knowledge-graph
//
//	Request body (JSON):
//	  {
//	    "documents":    ["page1 markdown", "page2 markdown"],
//	    "config": {
//	      "quality":      "balanced",
//	      "model":        "qwen-plus",
//	      "entity_types": ["component", "event", "cause"]
//	    }
//	  }
//
// ─── SSE event stream (Content-Type: text/event-stream) ───────────────────────
//
// Progress event:
//
//	data: {"type":"progress","stage":"analyzing","progress":30,"message":"正在分析文档..."}
//
// Result event (fault-tree):
//
//	data: {"type":"result","nodes":[...],"edges":[...],"accuracy":0.85,"summary":"..."}
//
// Result event (knowledge-graph):
//
//	data: {"type":"result","nodes":[...],"edges":[...],"entity_count":12,"relation_count":18,"summary":"..."}
//
// Error event:
//
//	data: {"type":"error","message":"..."}
//
// End-of-stream:
//
//	data: [DONE]
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LLMServerClient calls the self-hosted FastAPI LLM generation service.
type LLMServerClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewLLMServerClient creates a new LLMServerClient.
// baseURL should be the service root without a trailing slash (e.g. "http://localhost:8001").
// timeout sets the HTTP round-trip timeout; 0 defaults to 300s.
func NewLLMServerClient(baseURL string, timeout time.Duration) *LLMServerClient {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	return &LLMServerClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// ─── Request types sent to FastAPI ───────────────────────────────────────────

type llmFTRequest struct {
	Documents []string    `json:"documents"`
	TopEvent  string      `json:"top_event"`
	Config    llmFTConfig `json:"config"`
}

type llmFTConfig struct {
	Quality  string `json:"quality,omitempty"`
	Model    string `json:"model,omitempty"`
	Depth    int    `json:"depth,omitempty"`
	MaxNodes int    `json:"max_nodes,omitempty"`
}

type llmKGRequest struct {
	Documents []string    `json:"documents"`
	Config    llmKGConfig `json:"config"`
}

type llmKGConfig struct {
	Quality     string   `json:"quality,omitempty"`
	Model       string   `json:"model,omitempty"`
	EntityTypes []string `json:"entity_types,omitempty"`
}

func normalizeDocuments(documents []string) []string {
	if documents == nil {
		return []string{}
	}
	return documents
}

// ─── SSE event wire types ─────────────────────────────────────────────────────

// sseEvent is the union type for all SSE event shapes.
type sseEvent struct {
	Type string `json:"type"`

	// progress fields
	Stage    string `json:"stage,omitempty"`
	Progress int    `json:"progress,omitempty"`
	Message  string `json:"message,omitempty"`

	// fault-tree result fields
	Nodes    []map[string]interface{} `json:"nodes,omitempty"`
	Edges    []map[string]interface{} `json:"edges,omitempty"`
	Accuracy float64                  `json:"accuracy,omitempty"`
	Summary  string                   `json:"summary,omitempty"`

	// knowledge-graph extra result fields
	EntityCount   int `json:"entity_count,omitempty"`
	RelationCount int `json:"relation_count,omitempty"`

	// error field
	Error string `json:"message,omitempty"` // aliased to avoid clash with Message above
}

// ─── Public methods ───────────────────────────────────────────────────────────

// GenerateFaultTree streams a fault-tree generation request to the FastAPI service.
// onProgress is called for every SSE progress event (may be nil).
// Returns the final FaultTreeResult when the stream ends with a "result" event.
func (c *LLMServerClient) GenerateFaultTree(
	ctx context.Context,
	documents []string,
	topEvent string,
	cfg GenerateConfig,
	onProgress func(stage string, pct int),
) (*FaultTreeResult, error) {
	payload := llmFTRequest{
		Documents: normalizeDocuments(documents),
		TopEvent:  topEvent,
		Config: llmFTConfig{
			Quality:  cfg.Quality,
			Model:    cfg.Model,
			Depth:    cfg.Depth,
			MaxNodes: cfg.MaxNodes,
		},
	}

	events, err := c.streamSSE(ctx, "/generate/fault-tree", payload)
	if err != nil {
		return nil, err
	}

	for evt := range events {
		switch evt.typ {
		case "error":
			return nil, fmt.Errorf("llm_server: fault-tree generation failed: %s", evt.raw)
		case "progress":
			if onProgress != nil {
				var e sseEvent
				if err := json.Unmarshal([]byte(evt.raw), &e); err == nil {
					onProgress(e.Stage, e.Progress)
				}
			}
		case "result":
			var e sseEvent
			if err := json.Unmarshal([]byte(evt.raw), &e); err != nil {
				return nil, fmt.Errorf("llm_server: parse fault-tree result: %w", err)
			}
			if len(e.Nodes) == 0 {
				return nil, fmt.Errorf("llm_server: fault-tree result has no nodes")
			}
			summary := e.Summary
			if summary == "" {
				summary = fmt.Sprintf("生成故障树：%d 个节点，%d 条连接", len(e.Nodes), len(e.Edges))
			}
			return &FaultTreeResult{
				Nodes:    e.Nodes,
				Edges:    e.Edges,
				Accuracy: e.Accuracy,
				Summary:  summary,
			}, nil
		}
	}
	return nil, fmt.Errorf("llm_server: stream ended without a result event")
}

// GenerateKnowledgeGraph streams a knowledge-graph generation request to the FastAPI service.
// onProgress is called for every SSE progress event (may be nil).
func (c *LLMServerClient) GenerateKnowledgeGraph(
	ctx context.Context,
	documents []string,
	cfg GenerateConfig,
	onProgress func(stage string, pct int),
) (*KnowledgeGraphResult, error) {
	payload := llmKGRequest{
		Documents: normalizeDocuments(documents),
		Config: llmKGConfig{
			Quality: cfg.Quality,
			Model:   cfg.Model,
		},
	}

	events, err := c.streamSSE(ctx, "/generate/knowledge-graph", payload)
	if err != nil {
		return nil, err
	}

	for evt := range events {
		switch evt.typ {
		case "error":
			return nil, fmt.Errorf("llm_server: knowledge-graph generation failed: %s", evt.raw)
		case "progress":
			if onProgress != nil {
				var e sseEvent
				if err := json.Unmarshal([]byte(evt.raw), &e); err == nil {
					onProgress(e.Stage, e.Progress)
				}
			}
		case "result":
			var e sseEvent
			if err := json.Unmarshal([]byte(evt.raw), &e); err != nil {
				return nil, fmt.Errorf("llm_server: parse kg result: %w", err)
			}
			if len(e.Nodes) == 0 {
				return nil, fmt.Errorf("llm_server: knowledge-graph result has no nodes")
			}
			summary := e.Summary
			if summary == "" {
				summary = fmt.Sprintf("生成知识图谱：%d 个实体，%d 条关系", e.EntityCount, e.RelationCount)
			}
			return &KnowledgeGraphResult{
				Nodes:         e.Nodes,
				Edges:         e.Edges,
				EntityCount:   e.EntityCount,
				RelationCount: e.RelationCount,
				Summary:       summary,
			}, nil
		}
	}
	return nil, fmt.Errorf("llm_server: stream ended without a result event")
}

// ─── SSE streaming internals ─────────────────────────────────────────────────

type sseRaw struct {
	typ string // "progress" | "result" | "error"
	raw string // raw JSON payload (the part after "data: ")
}

// streamSSE sends a POST request and returns a channel of parsed SSE events.
// The channel is closed when the stream ends or the context is cancelled.
func (c *LLMServerClient) streamSSE(ctx context.Context, path string, payload interface{}) (<-chan sseRaw, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("llm_server: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm_server: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm_server: http request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("llm_server: server returned HTTP %d: %s", resp.StatusCode, string(preview))
	}

	ch := make(chan sseRaw, 16)
	go consumeSSE(resp.Body, ch)
	return ch, nil
}

// consumeSSE reads SSE lines from r, parses each data line, and sends typed events to ch.
// The channel is closed when [DONE] is received or the reader returns EOF/error.
func consumeSSE(r io.ReadCloser, ch chan<- sseRaw) {
	defer r.Close()
	defer close(ch)

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()

		// blank lines separate SSE events — skip them
		if line == "" {
			continue
		}

		// Only handle "data: ..." lines
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}

		// End-of-stream sentinel
		if data == "[DONE]" {
			return
		}

		// Parse just the "type" field to route the event
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &probe); err != nil {
			// Malformed event — skip
			continue
		}

		ch <- sseRaw{typ: probe.Type, raw: data}

		// Stop consuming after the result arrives; anything after is noise.
		if probe.Type == "result" || probe.Type == "error" {
			return
		}
	}
}
