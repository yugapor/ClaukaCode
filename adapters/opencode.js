// installed by `cc-ukagaka install opencode`; reinstalling overwrites this file.
import { spawn } from "node:child_process";

const BIN = "__CC_UKAGAKA_BIN__";

function notify(event, sessionID) {
  try {
    const p = spawn(BIN, [event], { stdio: ["pipe", "ignore", "ignore"], windowsHide: true });
    p.on("error", () => {});
    p.stdin.on("error", () => {});
    p.stdin.end(JSON.stringify({ session_id: sessionID }));
    p.unref();
  } catch {}
}

export const CcUkagakaPlugin = async () => {
  const children = new Set(); // subagent sessions must not look like the main task finishing
  return {
    "chat.message": async ({ sessionID }) => {
      if (!children.has(sessionID)) notify("OnClaudePromptSubmit", sessionID);
    },
    event: async ({ event }) => {
      const p = event?.properties ?? {};
      const id = p.sessionID ?? p.info?.id;
      if (p.info?.id && p.info.parentID) children.add(p.info.id);
      if (children.has(id)) return;
      switch (event?.type) {
        case "session.created": return notify("OnClaudeSessionStart", id);
        case "permission.asked": return notify("OnClaudePermissionRequest", id);
        case "session.idle": return notify("OnClaudeTaskComplete", id);
      }
    },
  };
};

export default { id: "cc-ukagaka", server: CcUkagakaPlugin };
