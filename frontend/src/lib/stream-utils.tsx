"use client";

import React from "react";

// ─── line rendering ─────────────────────────────────────────

/** Renders a single parsed stream line with high-contrast styling for dark logs. */
export function StreamLine({ line }: { line: string }) {
  if (line.startsWith("[system]")) {
    return (
      <div className="flex items-start gap-2 py-1.5 px-2 rounded bg-amber-200 text-amber-950 text-sm font-medium">
        <span className="shrink-0 mt-0.5">⚙</span>
        <span className="break-words whitespace-pre-wrap">{line.slice(9)}</span>
      </div>
    );
  }
  if (line.startsWith("[thinking]")) {
    return (
      <div className="py-1.5 px-2 text-gray-100 text-sm italic border-l-2 border-gray-300 ml-1 break-words whitespace-pre-wrap">
        💭 {line.slice(11)}
      </div>
    );
  }
  if (line.startsWith("[tool_error]")) {
    const detail = line.slice(13);
    const colonIdx = detail.indexOf(":");
    const toolName = colonIdx > 0 ? detail.slice(0, colonIdx) : detail;
    const toolDetail = colonIdx > 0 ? detail.slice(colonIdx + 1).trim() : "";
    return (
      <div className="flex items-start gap-2 py-1.5 px-2 rounded bg-red-950 text-red-100 text-sm border border-red-700/80">
        <span className="shrink-0 font-mono font-bold text-white bg-red-600 px-1.5 py-0.5 rounded text-xs">
          {toolName}
        </span>
        {toolDetail && (
          <span className="text-red-100 break-words whitespace-pre-wrap">{toolDetail}</span>
        )}
      </div>
    );
  }
  if (line.startsWith("[tool]")) {
    const detail = line.slice(7);
    const colonIdx = detail.indexOf(":");
    const toolName = colonIdx > 0 ? detail.slice(0, colonIdx) : detail;
    const toolDetail = colonIdx > 0 ? detail.slice(colonIdx + 1).trim() : "";
    return (
      <div className="flex items-start gap-2 py-1.5 px-2 rounded bg-slate-700 text-sm border border-slate-500/70">
        <span className="shrink-0 font-mono font-bold text-white bg-blue-600 px-1.5 py-0.5 rounded text-xs">
          {toolName}
        </span>
        {toolDetail && (
          <span className="text-gray-50 break-words whitespace-pre-wrap">{toolDetail}</span>
        )}
      </div>
    );
  }
  if (line.startsWith("[result]")) {
    const content = line.slice(9);
    // Detect stderr-like error output in results and render prominently.
    const isErrorOutput = /^\/bin\/\w+:|^sh:|^bash:|^error:|^fatal:|^E:|^Traceback /i.test(content.trimStart());
    return (
      <div className={`py-1 px-2 text-sm border-l-2 ml-1 break-words whitespace-pre-wrap font-mono ${
        isErrorOutput
          ? "text-red-200 border-red-300"
          : "text-gray-100 border-emerald-300"
      }`}>
        {content}
      </div>
    );
  }
  if (line.startsWith("[error]")) {
    return (
      <div className="flex items-start gap-2 py-1.5 px-2 rounded bg-red-900 text-red-50 text-sm border-l-2 border-red-300">
        <span className="shrink-0 text-red-200 font-bold">✕</span>
        <span className="break-words whitespace-pre-wrap font-mono font-semibold">{line.slice(8)}</span>
      </div>
    );
  }
  if (line.startsWith("[stderr]")) {
    return (
      <div className="py-1 px-2 text-red-200 text-sm font-mono border-l-2 border-red-400/80 ml-1 break-words whitespace-pre-wrap">
        {line.slice(9)}
      </div>
    );
  }
  return (
    <div className="py-1 px-2 text-gray-100 text-sm break-words whitespace-pre-wrap">
      {line}
    </div>
  );
}

// ─── stream parsing ─────────────────────────────────────────

/** Parse a stream-json line (Claude CLI or OpenCode) into a human-readable
 *  string. Returns "" for skipped/unrecognized events. */
export function extractStreamMessage(line: string): string {
  line = line.trim();
  if (!line || line[0] !== "{") return "";
  let raw: Record<string, unknown>;
  try {
    raw = JSON.parse(line);
  } catch {
    return "";
  }
  const eventType = raw.type as string;
  if (!eventType) return "";

  switch (eventType) {
    // --- Claude format ---
    case "assistant":
      return parseAssistantEvent(raw);
    case "user":
      return parseUserEvent(raw);
    case "result": {
      const result = raw.result as string;
      return result ? "[result] " + truncateStr(result, 500) : "";
    }
    case "system": {
      const msg = (raw as { message?: string }).message;
      return msg ? "[system] " + msg : "";
    }
    // --- OpenCode format ---
    case "text":
      return parseOpenCodeTextEvent(raw);
    case "tool_use":
      return parseOpenCodeToolUseEvent(raw);
    case "error": {
      const errField = raw.error;
      if (typeof errField === "string" && errField) {
        return "[error] " + errField;
      }
      if (errField && typeof errField === "object") {
        const errObj = errField as Record<string, unknown>;
        const data = errObj.data as Record<string, unknown> | undefined;
        const msg = data?.message as string;
        const name = (errObj.name as string) || "error";
        if (msg) return "[error] " + name + ": " + msg;
        if (errObj.name) return "[error] " + errObj.name;
      }
      const part = raw.part as Record<string, unknown> | undefined;
      const errMsg = part?.error as string;
      return errMsg ? "[error] " + errMsg : "";
    }
    case "step_start":
    case "step_finish":
      return "";
    default:
      return "";
  }
}

/** Process raw log lines into formatted display lines. */
export function processLogLines(rawLines: string[]): string[] {
  const out: string[] = [];
  for (const line of rawLines) {
    const parsed = extractStreamMessage(line);
    if (parsed === "") continue;
    out.push(...parsed.split("\n"));
  }
  return out;
}

export function truncateStr(s: string, n: number): string {
  return s.length <= n ? s : s.slice(0, n) + "…";
}

// ─── token usage tracking ───────────────────────────────────

/** Shared model pricing table — single source of truth for both Go and TS. */
import modelPricingData from "@pricing/model_pricing.json";

interface PricingEntry {
  substring: string;
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
}

const MODEL_PRICING: PricingEntry[] = modelPricingData as PricingEntry[];

function lookupPricing(model: string): PricingEntry | null {
  const lower = model.toLowerCase();
  for (const entry of MODEL_PRICING) {
    if (lower.includes(entry.substring)) return entry;
  }
  return null;
}

/** Estimate cost for a usage record using the pricing table. Returns 0 for unknown models. */
export function estimateCostFromUsage(u: StreamTokenUsage): number {
  const p = lookupPricing(u.model);
  if (!p) return 0;
  return (
    (u.input_tokens * p.input) / 1_000_000 +
    (u.output_tokens * p.output) / 1_000_000 +
    (u.cache_read_tokens * p.cache_read) / 1_000_000 +
    (u.cache_write_tokens * p.cache_write) / 1_000_000
  );
}

/** Per-model token usage accumulated from stream events. */
export interface StreamTokenUsage {
  model: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  cost_usd: number;
}

/** Extract token usage from a raw JSON stream line. Returns null if not a usage event. */
export function extractTokenUsage(line: string): StreamTokenUsage | null {
  line = line.trim();
  if (!line || line[0] !== "{") return null;
  let raw: Record<string, unknown>;
  try {
    raw = JSON.parse(line);
  } catch {
    return null;
  }
  const eventType = raw.type as string;
  if (!eventType) return null;

  if (eventType === "assistant") {
    return extractClaudeUsage(raw);
  }
  if (eventType === "step_finish") {
    return extractOpenCodeUsage(raw);
  }
  return null;
}

function extractClaudeUsage(raw: Record<string, unknown>): StreamTokenUsage | null {
  const msg = raw.message as Record<string, unknown> | undefined;
  if (!msg) return null;
  const usage = msg.usage as Record<string, unknown> | undefined;
  if (!usage) return null;
  const model = (msg.model as string) || "unknown";
  const u: StreamTokenUsage = {
    model,
    input_tokens: (usage.input_tokens as number) || 0,
    output_tokens: (usage.output_tokens as number) || 0,
    cache_read_tokens: (usage.cache_read_input_tokens as number) || 0,
    cache_write_tokens: (usage.cache_creation_input_tokens as number) || 0,
    cost_usd: 0,
  };
  u.cost_usd = estimateCostFromUsage(u);
  return u;
}

function extractOpenCodeUsage(raw: Record<string, unknown>): StreamTokenUsage | null {
  const part = raw.part as Record<string, unknown> | undefined;
  if (!part) return null;
  const tokens = part.tokens as Record<string, unknown> | undefined;
  if (!tokens) return null;
  const cache = tokens.cache as Record<string, unknown> | undefined;
  const model = (part.modelID as string) || "opencode";
  return {
    model,
    input_tokens: (tokens.input as number) || 0,
    output_tokens: (tokens.output as number) || 0,
    cache_read_tokens: cache ? ((cache.read as number) || 0) : 0,
    cache_write_tokens: cache ? ((cache.write as number) || 0) : 0,
    cost_usd: (part.cost as number) || 0,
  };
}

/** Accumulate a usage event into a per-model map. Returns a new map. */
export function accumulateUsage(
  current: Record<string, StreamTokenUsage>,
  usage: StreamTokenUsage,
): Record<string, StreamTokenUsage> {
  const existing = current[usage.model];
  if (existing) {
    return {
      ...current,
      [usage.model]: {
        model: usage.model,
        input_tokens: existing.input_tokens + usage.input_tokens,
        output_tokens: existing.output_tokens + usage.output_tokens,
        cache_read_tokens: existing.cache_read_tokens + usage.cache_read_tokens,
        cache_write_tokens: existing.cache_write_tokens + usage.cache_write_tokens,
        cost_usd: existing.cost_usd + usage.cost_usd,
      },
    };
  }
  return { ...current, [usage.model]: { ...usage } };
}

/** Format a token count for display (e.g. 1234 → "1.2K", 1234567 → "1.2M"). */
export function formatTokenCount(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M";
  if (n >= 1_000) return (n / 1_000).toFixed(1) + "K";
  return String(n);
}

// ─── internal helpers ───────────────────────────────────────

function parseOpenCodeTextEvent(raw: Record<string, unknown>): string {
  const part = raw.part as Record<string, unknown> | undefined;
  if (!part) return "";
  const text = part.text as string;
  return text || "";
}

function parseOpenCodeToolUseEvent(raw: Record<string, unknown>): string {
  const part = raw.part as Record<string, unknown> | undefined;
  if (!part) return "";
  const toolName = (part.tool as string) || "unknown";
  const state = part.state as Record<string, unknown> | undefined;
  if (!state) return `[tool] ${toolName}`;

  let detail = (state.title as string) || "";
  if (!detail) {
    const input = state.input as Record<string, unknown> | undefined;
    if (input) {
      const lname = toolName.toLowerCase();
      if (lname === "bash") {
        detail = (input.description as string) || truncateStr((input.command as string) || "", 120);
      } else if (["read", "write", "edit", "view", "glob", "grep", "patch"].includes(lname)) {
        detail = (input.file_path as string) || (input.filePath as string) || "";
      } else {
        for (const key of ["description", "file_path", "filePath", "command", "query", "url"]) {
          if (input[key]) { detail = truncateStr(String(input[key]), 120); break; }
        }
      }
    }
  }

  const status = state.status as string | undefined;
  const errStr = ((state.error as string) || "").trim();
  const isError = status === "error";
  let msg = detail
    ? (isError ? `[tool_error] ${toolName}: ${detail}` : `[tool] ${toolName}: ${detail}`)
    : (isError ? `[tool_error] ${toolName}` : `[tool] ${toolName}`);
  if (isError && errStr) {
    msg += "\n[error] " + truncateStr(errStr, 300);
  }
  const output = ((state.output as string) || "").trim();
  if (output) {
    msg += "\n[result] " + truncateStr(output, 200);
  }
  return msg;
}

function parseAssistantEvent(raw: Record<string, unknown>): string {
  const msg = raw.message as { content?: Array<Record<string, unknown>> };
  if (!msg?.content?.length) return "";
  const parts: string[] = [];
  for (const block of msg.content) {
    switch (block.type) {
      case "text":
        if (block.text) parts.push(block.text as string);
        break;
      case "thinking": {
        const t = block.thinking as string;
        if (t) parts.push("[thinking] " + truncateStr(t, 200));
        break;
      }
      case "tool_use":
        parts.push(formatToolUse(block));
        break;
    }
  }
  return parts.join("\n");
}

function formatToolUse(block: Record<string, unknown>): string {
  const name = block.name as string || "unknown";
  const input = block.input as Record<string, unknown> | undefined;
  if (!input) return `[tool] ${name}`;
  let detail = "";
  switch (name) {
    case "Bash":
      detail = (input.description as string) || truncateStr((input.command as string) || "", 120);
      break;
    case "Read":
    case "Write":
    case "Edit":
      detail = (input.file_path as string) || "";
      break;
    case "Agent":
      detail = (input.description as string) || truncateStr((input.prompt as string) || "", 120);
      break;
    case "WebFetch":
      detail = (input.url as string) || "";
      break;
    default:
      for (const key of ["description", "file_path", "command", "query", "url"]) {
        if (input[key]) { detail = truncateStr(String(input[key]), 120); break; }
      }
  }
  return detail ? `[tool] ${name}: ${detail}` : `[tool] ${name}`;
}

function parseUserEvent(raw: Record<string, unknown>): string {
  const msg = raw.message as { content?: unknown };
  if (!msg?.content) return "";
  const blocks = msg.content as Array<{ type: string; content?: string; is_error?: boolean }>;
  if (!Array.isArray(blocks)) return "";
  for (const b of blocks) {
    if (b.type === "tool_result") {
      const summary = truncateStr(b.content || "", 200);
      if (b.is_error) return "[error] " + summary;
      return summary ? "[result] " + summary : "[result] (ok)";
    }
  }
  return "";
}
