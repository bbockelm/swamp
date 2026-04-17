"use client";

import React from "react";

// ─── line rendering ─────────────────────────────────────────

/** Renders a single parsed stream line with appropriate styling. */
export function StreamLine({ line }: { line: string }) {
  if (line.startsWith("[system]")) {
    return (
      <div className="flex items-start gap-2 py-1 px-2 rounded bg-yellow-950/30 text-yellow-300 text-xs">
        <span className="shrink-0 mt-0.5">⚙</span>
        <span className="break-words whitespace-pre-wrap">{line.slice(9)}</span>
      </div>
    );
  }
  if (line.startsWith("[thinking]")) {
    return (
      <div className="py-1 px-2 text-gray-500 text-xs italic border-l-2 border-gray-700 ml-1 break-words whitespace-pre-wrap">
        💭 {line.slice(11)}
      </div>
    );
  }
  if (line.startsWith("[tool]")) {
    const detail = line.slice(7);
    const colonIdx = detail.indexOf(":");
    const toolName = colonIdx > 0 ? detail.slice(0, colonIdx) : detail;
    const toolDetail = colonIdx > 0 ? detail.slice(colonIdx + 1).trim() : "";
    return (
      <div className="flex items-start gap-2 py-1.5 px-2 rounded bg-cyan-950/30 text-xs">
        <span className="shrink-0 font-mono font-semibold text-cyan-400 bg-cyan-950 px-1.5 py-0.5 rounded text-[10px]">
          {toolName}
        </span>
        {toolDetail && (
          <span className="text-cyan-200/80 break-words whitespace-pre-wrap">{toolDetail}</span>
        )}
      </div>
    );
  }
  if (line.startsWith("[result]")) {
    return (
      <div className="py-1 px-2 text-xs text-gray-400 border-l-2 border-green-800 ml-1 break-words whitespace-pre-wrap font-mono">
        {line.slice(9)}
      </div>
    );
  }
  if (line.startsWith("[error]")) {
    return (
      <div className="flex items-start gap-2 py-1.5 px-2 rounded bg-red-500/20 text-red-400 text-xs border-l-2 border-red-500">
        <span className="shrink-0 text-red-500 font-bold">✕</span>
        <span className="break-words whitespace-pre-wrap font-mono font-semibold">{line.slice(8)}</span>
      </div>
    );
  }
  if (line.startsWith("[stderr]")) {
    return (
      <div className="py-0.5 px-2 text-red-400/70 text-xs font-mono break-words whitespace-pre-wrap">
        {line.slice(9)}
      </div>
    );
  }
  return (
    <div className="py-0.5 px-2 text-gray-200 text-sm break-words whitespace-pre-wrap">
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
        detail = (input.file_path as string) || "";
      } else {
        for (const key of ["description", "file_path", "command", "query", "url"]) {
          if (input[key]) { detail = truncateStr(String(input[key]), 120); break; }
        }
      }
    }
  }

  let msg = detail ? `[tool] ${toolName}: ${detail}` : `[tool] ${toolName}`;
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
