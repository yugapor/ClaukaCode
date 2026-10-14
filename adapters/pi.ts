// @ts-nocheck
// installed by `cc-ukagaka install pi`; reinstalling overwrites this file.
import { spawn } from "node:child_process";

const BIN = "__CC_UKAGAKA_BIN__";

function notify(event, ctx) {
  if (ctx?.mode !== "tui") return; // headless runs have no ghost to talk to
  try {
    const p = spawn(BIN, [event], { stdio: ["pipe", "ignore", "ignore"], windowsHide: true });
    p.on("error", () => {});
    p.stdin.on("error", () => {});
    p.stdin.end(JSON.stringify({ session_id: ctx?.sessionManager?.getSessionId?.() }));
    p.unref();
  } catch {}
}

export default function (pi) {
  pi.on("session_start", (_e, ctx) => notify("OnClaudeSessionStart", ctx));
  pi.on("agent_start", (_e, ctx) => notify("OnClaudePromptSubmit", ctx));
  pi.on("agent_settled", (_e, ctx) => {
    if (ctx?.isIdle?.() === true) notify("OnClaudeTaskComplete", ctx);
  });
}
