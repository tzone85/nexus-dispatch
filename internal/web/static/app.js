// ── State ───────────────────────────────────────────────────────────
let ws = null;
let reconnectDelay = 1000;
let currentState = null;
let sortField = "status";
let sortDir = 1;
let activeFilter = "all";
let confirmCallback = null;

// ── WebSocket connection ─────────────────────────────────────────────
function connect() {
  const protocol = location.protocol === "https:" ? "wss:" : "ws:";
  ws = new WebSocket(`${protocol}//${location.host}/ws`);

  ws.onopen = () => {
    reconnectDelay = 1000;
    document.getElementById("connection-status").textContent = "Connected";
    document.getElementById("connection-status").className = "connected";
    document.getElementById("banner").classList.add("hidden");
  };

  ws.onclose = () => {
    document.getElementById("connection-status").textContent = "Disconnected";
    document.getElementById("connection-status").className = "disconnected";
    showBanner("Disconnected \u2014 reconnecting...");
    setTimeout(connect, reconnectDelay);
    reconnectDelay = Math.min(reconnectDelay * 2, 30000);
  };

  ws.onerror = () => {
    // onclose fires after onerror; no extra action needed here.
  };

  ws.onmessage = (event) => {
    let msg;
    try {
      msg = JSON.parse(event.data);
    } catch (e) {
      console.error("Failed to parse WebSocket message:", e);
      return;
    }
    switch (msg.type) {
      case "state":
        currentState = msg.data;
        renderState(msg.data);
        break;
      case "event":
        appendEvent(msg.data);
        break;
      case "command_result":
        showToast(msg.message, msg.success ? "success" : "error");
        break;
      default:
        console.warn("Unknown message type:", msg.type);
    }
  };
}

// ── XSS-safe DOM builder ─────────────────────────────────────────────
//
// All user-supplied data goes through esc() before being embedded in
// HTML template strings. esc() relies on the browser's own text node
// serialisation — no regex, no allow-lists needed.
//
// innerHTML is used only to insert those pre-escaped strings. Raw
// server values are NEVER placed in innerHTML directly.

/**
 * Return an HTML-escaped version of str.
 * Safe to embed inside element content (not inside attribute values).
 */
function esc(str) {
  if (str == null) return "";
  const div = document.createElement("div");
  div.textContent = String(str);
  return div.innerHTML;
}

// ── Rendering ────────────────────────────────────────────────────────
function renderState(data) {
  renderAgents(data.agents || []);
  renderPipeline(data.pipeline || {});
  renderStories(data.stories || []);
  renderEvents(data.events || []);
  renderEscalations(data.escalations || []);
}

function renderAgents(agents) {
  const tbody = document.querySelector("#agents-table tbody");
  if (!agents.length) {
    tbody.innerHTML =
      '<tr><td colspan="7" class="muted">No agents running</td></tr>';
    return;
  }
  // All values passed to innerHTML are routed through esc() first.
  tbody.innerHTML = agents
    .map((a) => {
      const statusClass = "status-" + esc(a.status);
      const storyId = esc(a.current_story_id || "\u2014");
      const session = esc(a.session_name || "\u2014");
      const agentId = esc(a.id);
      return (
        "<tr>" +
        "<td>" +
        agentId +
        "</td>" +
        "<td>" +
        esc(a.type) +
        "</td>" +
        "<td>" +
        esc(a.model) +
        "</td>" +
        '<td class="' +
        statusClass +
        '">' +
        esc(a.status) +
        "</td>" +
        "<td>" +
        storyId +
        "</td>" +
        "<td>" +
        session +
        "</td>" +
        '<td><button class="btn-danger"' +
        " onclick=\"confirmAction('Kill agent " +
        agentId +
        "?'," +
        " function(){ sendCommand('kill_agent', {agent_id:'" +
        agentId +
        "'}); })\">" +
        "&#x2715;</button></td>" +
        "</tr>"
      );
    })
    .join("");
}

function renderPipeline(p) {
  const planned = p.planned || 0;
  const inProgress = p.in_progress || 0;
  const review = p.review || 0;
  const qa = p.qa || 0;
  const pr = p.pr || 0;
  const merged = p.merged || 0;
  const split = p.split || 0;

  // textContent — no escaping needed here.
  document.getElementById("pipeline-counts").textContent =
    "Planned: " +
    planned +
    "  In Prog: " +
    inProgress +
    "  Review: " +
    review +
    "  QA: " +
    qa +
    "  PR: " +
    pr +
    "  Merged: " +
    merged +
    "  Split: " +
    split;

  const total = planned + inProgress + review + qa + pr + merged + split;
  const completed = merged + pr;
  const pct = total > 0 ? Math.round((completed * 100) / total) : 0;

  document.getElementById("progress-bar").style.width = pct + "%";
  document.getElementById("progress-text").textContent = pct + "% complete";
}

function renderStories(stories) {
  const sorted = [...stories].sort((a, b) => {
    const va = a[sortField] != null ? a[sortField] : "";
    const vb = b[sortField] != null ? b[sortField] : "";
    if (typeof va === "number" && typeof vb === "number") {
      return (va - vb) * sortDir;
    }
    return String(va).localeCompare(String(vb)) * sortDir;
  });

  const tbody = document.querySelector("#stories-table tbody");
  if (!sorted.length) {
    tbody.innerHTML =
      '<tr><td colspan="6" class="muted">No stories \u2014 run \'nxd plan\' to create a requirement</td></tr>';
    return;
  }
  // All values routed through esc().
  tbody.innerHTML = sorted
    .map((s) => {
      const statusClass = "status-" + esc(s.status);
      const statusText = esc(
        s.escalation_tier > 0
          ? (s.status || "") + "|T" + s.escalation_tier
          : s.status || "",
      );
      const storyId = esc(s.id);
      return (
        "<tr>" +
        "<td>" +
        storyId +
        "</td>" +
        '<td class="' +
        statusClass +
        '">' +
        statusText +
        "</td>" +
        "<td>" +
        (s.complexity != null ? esc(String(s.complexity)) : "") +
        "</td>" +
        "<td>" +
        (s.escalation_tier || 0) +
        "</td>" +
        "<td>" +
        esc(s.title) +
        "</td>" +
        "<td>" +
        '<button class="btn-action" title="Retry"' +
        " onclick=\"sendCommand('retry_story', {story_id:'" +
        storyId +
        "'})\">" +
        "&#x21BB;</button>" +
        '<button class="btn-action" title="Escalate"' +
        " onclick=\"sendCommand('escalate_story', {story_id:'" +
        storyId +
        "'})\">" +
        "&#x2191;</button>" +
        '<button class="btn-action" title="Reassign"' +
        " onclick=\"confirmAction('Reassign " +
        storyId +
        "?'," +
        " function(){ sendCommand('reassign_story', {story_id:'" +
        storyId +
        "', target_tier:0}); })\">" +
        "&#x21C4;</button>" +
        "</td>" +
        "</tr>"
      );
    })
    .join("");
}

function eventClass(type) {
  if (!type) return "event-default";
  if (type.startsWith("REQ")) return "event-req";
  if (type.startsWith("STORY")) return "event-story";
  if (type.startsWith("AGENT")) return "event-agent";
  if (type.startsWith("ESCALATION")) return "event-escalation";
  return "event-default";
}

/** Build the four inner spans for an event row. All values go through esc(). */
function buildEventRowInner(e) {
  return (
    '<span class="event-time">' +
    esc(e.timestamp) +
    "</span>" +
    '<span class="' +
    eventClass(e.type) +
    '">' +
    esc(e.type) +
    "</span>" +
    "<span>" +
    esc(e.agent_id || "\u2014") +
    "</span>" +
    "<span>" +
    esc(e.story_id || "\u2014") +
    "</span>"
  );
}

function renderEvents(events) {
  const log = document.getElementById("activity-log");
  const filtered =
    activeFilter === "all"
      ? events
      : events.filter((e) => e.type && e.type.startsWith(activeFilter));

  // All values routed through esc() inside buildEventRowInner.
  log.innerHTML = filtered
    .map((e) => '<div class="event-row">' + buildEventRowInner(e) + "</div>")
    .join("");
  log.scrollTop = log.scrollHeight;
}

function appendEvent(e) {
  const log = document.getElementById("activity-log");
  if (activeFilter !== "all" && !(e.type && e.type.startsWith(activeFilter)))
    return;

  const div = document.createElement("div");
  div.className = "event-row";
  // All values routed through esc() inside buildEventRowInner.
  div.innerHTML = buildEventRowInner(e);
  log.appendChild(div);
  log.scrollTop = log.scrollHeight;
}

function renderEscalations(escalations) {
  const container = document.getElementById("escalations-list");
  if (!escalations || !escalations.length) {
    container.innerHTML = '<span class="muted">No escalations</span>';
    return;
  }
  // All values routed through esc().
  container.innerHTML = escalations
    .map((e) => {
      const cls = e.status === "pending" ? "status-stuck" : "status-active";
      return (
        '<div class="escalation-row">' +
        "<span>" +
        esc(e.story_id) +
        "</span>" +
        "<span>" +
        esc(e.from_agent) +
        "</span>" +
        "<span>T" +
        esc(String(e.from_tier)) +
        "&#x2192;" +
        esc(String(e.to_tier)) +
        "</span>" +
        '<span class="' +
        cls +
        '">' +
        esc(e.status) +
        "</span>" +
        '<span class="muted">' +
        esc(e.reason) +
        "</span>" +
        "</div>"
      );
    })
    .join("");
}

// ── Commands ─────────────────────────────────────────────────────────
function sendCommand(action, payload) {
  if (!ws || ws.readyState !== WebSocket.OPEN) {
    showToast("Not connected", "error");
    return;
  }
  ws.send(
    JSON.stringify({
      type: "command",
      action,
      payload: JSON.stringify(payload),
    }),
  );
}

function confirmAction(message, callback) {
  // message is already esc()'d by the caller; use textContent for safety.
  document.getElementById("confirm-message").textContent = message;
  document.getElementById("confirm-overlay").classList.remove("hidden");
  confirmCallback = callback;
}

// ── UI Helpers ────────────────────────────────────────────────────────
function showBanner(text) {
  const banner = document.getElementById("banner");
  banner.textContent = text;
  banner.classList.remove("hidden");
}

let toastTimer = null;
function showToast(message, type) {
  const toast = document.getElementById("toast");
  toast.textContent = message;
  toast.className = "toast-" + type;
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    toast.className = "hidden";
    toastTimer = null;
  }, 5000);
}

// ── Event Listeners ───────────────────────────────────────────────────
document.getElementById("confirm-yes").addEventListener("click", () => {
  document.getElementById("confirm-overlay").classList.add("hidden");
  if (confirmCallback) confirmCallback();
  confirmCallback = null;
});

document.getElementById("confirm-no").addEventListener("click", () => {
  document.getElementById("confirm-overlay").classList.add("hidden");
  confirmCallback = null;
});

// Close confirm overlay on backdrop click.
document.getElementById("confirm-overlay").addEventListener("click", (e) => {
  if (e.target === document.getElementById("confirm-overlay")) {
    document.getElementById("confirm-overlay").classList.add("hidden");
    confirmCallback = null;
  }
});

// Sort headers.
document.querySelectorAll("#stories-table th[data-sort]").forEach((th) => {
  th.addEventListener("click", () => {
    const field = th.dataset.sort;
    if (sortField === field) {
      sortDir *= -1;
    } else {
      sortField = field;
      sortDir = 1;
    }
    if (currentState) renderStories(currentState.stories || []);
  });
});

// Activity filters.
document.querySelectorAll(".filter-btn").forEach((btn) => {
  btn.addEventListener("click", () => {
    document
      .querySelectorAll(".filter-btn")
      .forEach((b) => b.classList.remove("active"));
    btn.classList.add("active");
    activeFilter = btn.dataset.filter;
    if (currentState) renderEvents(currentState.events || []);
  });
});

// ── Start ─────────────────────────────────────────────────────────────
connect();
