// ── State ───────────────────────────────────────────────────────────
let ws = null;
let reconnectDelay = 1000;
let currentState = null;
let sortField = "status";
let sortDir = 1;
let activeFilter = "all";
let confirmCallback = null;

// ── WebSocket connection ─────────────────────────────────────────────
// C1: forward the auth token from the query string to the WebSocket upgrade.
// The dashboard URL printed by the CLI carries ?token=<hex>; without it the
// server returns 401 on /ws.
function getAuthToken() {
  const params = new URLSearchParams(location.search);
  return params.get("token") || "";
}

function connect() {
  const protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const token = encodeURIComponent(getAuthToken());
  ws = new WebSocket(`${protocol}//${location.host}/ws?token=${token}`);

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
  renderHumanReview(data.human_review);
  renderAgents(data.agents || [], data.agent_traces || []);
  renderPipeline(data.pipeline || {});
  renderDAG(data.dag, data.stories || []);
  renderStories(data.stories || []);
  renderEvents(data.events || []);
  renderEscalations(data.escalations || []);
  renderMetrics(data.metrics);
  renderMemPalaceStatus(data.mempalace_status);
  renderReviewGates(data.review_gates);
  renderInvestigations(data.investigations);
  renderRecoveryLog(data.recovery_log);
  renderSuggestions(data.suggestions);
}

// seenSuggestionIds tracks which improver suggestions we've already
// shown a toast for, so we don't re-toast on every WebSocket tick.
const seenSuggestionIds = new Set();

function renderSuggestions(items) {
  const section = document.getElementById("suggestions");
  const list = document.getElementById("suggestions-list");
  if (!items || items.length === 0) {
    section.style.display = "none";
    return;
  }
  section.style.display = "";
  list.textContent = "";

  items.forEach(function (s) {
    const item = document.createElement("div");
    item.className = "suggestion-item severity-" + (s.severity || "info");

    const head = document.createElement("div");
    head.className = "sugg-head";

    const sev = document.createElement("span");
    sev.className = "badge badge-" + (s.severity || "info");
    sev.textContent = (s.severity || "info").toUpperCase();
    head.appendChild(sev);

    const title = document.createElement("strong");
    title.textContent = " " + (s.title || s.id || "(no title)");
    head.appendChild(title);

    if (s.source) {
      const src = document.createElement("span");
      src.className = "muted";
      src.textContent = " · " + s.source;
      head.appendChild(src);
    }
    item.appendChild(head);

    if (s.description) {
      const desc = document.createElement("p");
      desc.className = "sugg-desc";
      desc.textContent = s.description;
      item.appendChild(desc);
    }
    if (s.action) {
      const act = document.createElement("p");
      act.className = "sugg-action";
      act.textContent = "→ " + s.action;
      item.appendChild(act);
    }

    list.appendChild(item);

    // Toast new critical/warning suggestions once per session so
    // operators don't miss them on a busy dashboard. Info-level stays
    // silent in the panel.
    if (
      (s.severity === "critical" || s.severity === "warning") &&
      s.id &&
      !seenSuggestionIds.has(s.id)
    ) {
      seenSuggestionIds.add(s.id);
      showToast(
        "Suggestion: " + (s.title || s.id),
        s.severity === "critical" ? "error" : "success",
      );
    }
  });
}

// expandedAgents tracks which agent rows are currently expanded so the
// drill-down state survives WebSocket-driven re-renders.
const expandedAgents = new Set();

function renderAgents(agents, traces) {
  const tbody = document.querySelector("#agents-table tbody");
  if (!agents.length) {
    tbody.innerHTML =
      '<tr><td colspan="7" class="muted">No agents running</td></tr>';
    return;
  }
  // Build an agent_id -> trace lookup once instead of scanning per row.
  const traceByAgent = (traces || []).reduce(function (acc, t) {
    acc[t.agent_id] = t;
    return acc;
  }, {});

  // All values passed to innerHTML are routed through esc() first.
  tbody.innerHTML = agents
    .map((a) => {
      const statusClass = "status-" + esc(a.status);
      const storyId = esc(a.current_story_id || "\u2014");
      const session = esc(a.session_name || "\u2014");
      const agentId = esc(a.id);
      const isExpanded = expandedAgents.has(a.id);
      const arrow = isExpanded ? "\u25BC" : "\u25B6";
      const trace = traceByAgent[a.id];
      const drillRow = isExpanded && trace ? buildAgentDrillRow(trace) : "";
      return (
        '<tr class="agent-row" data-agent-id="' +
        agentId +
        '">' +
        '<td><button class="agent-toggle" data-agent-id="' +
        agentId +
        '">' +
        arrow +
        " " +
        agentId +
        "</button></td>" +
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
        "</tr>" +
        drillRow
      );
    })
    .join("");

  // Wire toggle buttons after re-render. Agent count is small, so
  // re-attaching listeners on every render is simpler than maintaining a
  // delegated listener through DOM replacement.
  tbody.querySelectorAll(".agent-toggle").forEach(function (btn) {
    btn.addEventListener("click", function () {
      const id = btn.getAttribute("data-agent-id");
      if (expandedAgents.has(id)) {
        expandedAgents.delete(id);
      } else {
        expandedAgents.add(id);
      }
      if (currentState) {
        renderAgents(
          currentState.agents || [],
          currentState.agent_traces || [],
        );
      }
    });
  });
}

// buildAgentDrillRow returns a <tr> string with the recent progress events
// for one agent. Values are escaped via esc().
function buildAgentDrillRow(trace) {
  if (!trace.recent || trace.recent.length === 0) {
    return (
      '<tr class="agent-drill"><td colspan="7" class="muted">' +
      "No recent progress for " +
      esc(trace.story_id) +
      "</td></tr>"
    );
  }
  const rows = trace.recent
    .map(function (r) {
      const toolBadge = r.tool
        ? '<span class="badge badge-warn">' + esc(r.tool) + "</span> "
        : "";
      return (
        '<li class="drill-row">' +
        '<span class="timestamp">' +
        esc(r.timestamp) +
        "</span> " +
        '<span class="badge">iter ' +
        esc(String(r.iteration)) +
        "</span> " +
        '<span class="badge">' +
        esc(r.phase || "?") +
        "</span> " +
        toolBadge +
        '<span class="drill-detail">' +
        esc(r.detail || "") +
        "</span>" +
        "</li>"
      );
    })
    .join("");
  return (
    '<tr class="agent-drill"><td colspan="7">' +
    '<div class="drill-header">Story <strong>' +
    esc(trace.story_id) +
    "</strong> recent progress:</div>" +
    '<ul class="drill-list">' +
    rows +
    "</ul>" +
    "</td></tr>"
  );
}

function renderHumanReview(items) {
  const section = document.getElementById("human-review");
  const list = document.getElementById("human-review-list");
  if (!items || items.length === 0) {
    section.style.display = "none";
    return;
  }
  section.style.display = "";
  list.textContent = "";
  items.forEach(function (h) {
    const item = document.createElement("div");
    item.className = "human-review-item";

    const head = document.createElement("div");
    head.className = "review-head";
    const ts = document.createElement("span");
    ts.className = "timestamp";
    ts.textContent = h.timestamp + " ";
    head.appendChild(ts);
    const story = document.createElement("strong");
    story.textContent = h.story_id || "(no story)";
    head.appendChild(story);
    if (h.reason) {
      const reasonBadge = document.createElement("span");
      reasonBadge.className = "badge badge-danger";
      reasonBadge.textContent = " " + h.reason;
      head.appendChild(reasonBadge);
    }
    item.appendChild(head);

    if (h.diagnosis) {
      const diag = document.createElement("p");
      diag.className = "review-diagnosis";
      diag.textContent = h.diagnosis;
      item.appendChild(diag);
    }

    if (h.directives && h.directives.length > 0) {
      const ul = document.createElement("ul");
      ul.className = "review-directives";
      h.directives.forEach(function (d) {
        const li = document.createElement("li");
        li.textContent = d;
        ul.appendChild(li);
      });
      item.appendChild(ul);
    }

    list.appendChild(item);
  });
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

function renderMetrics(m) {
  if (!m) return;
  document.getElementById("metric-tokens").textContent =
    Math.round(m.total_tokens / 1000) + "K";
  document.getElementById("metric-success").textContent =
    m.success_rate.toFixed(0) + "%";
  var total = 0;
  if (m.by_phase) {
    for (var p in m.by_phase) {
      total += m.by_phase[p].count;
    }
  }
  document.getElementById("metric-calls").textContent = total;
  document.getElementById("metric-latency").textContent =
    m.avg_latency_ms + "ms";
  document.getElementById("metric-escalations").textContent =
    m.escalation_count;
}

function renderMemPalaceStatus(s) {
  var el = document.getElementById("mempalace-status");
  if (!el) return;
  if (!s) {
    el.textContent = "";
    return;
  }
  el.textContent = s.available ? "Memory: Active" : "Memory: Offline";
  el.className =
    "status-indicator " + (s.available ? "mem-active" : "mem-offline");
}

function renderReviewGates(gates) {
  var section = document.getElementById("review-gates");
  var list = document.getElementById("review-gates-list");
  if (!gates || gates.length === 0) {
    section.style.display = "none";
    return;
  }
  section.style.display = "";
  list.textContent = ""; // clear
  gates.forEach(function (g) {
    var item = document.createElement("div");
    item.className = "review-gate-item";

    var badge = document.createElement("span");
    badge.className = "badge badge-" + g.status;
    badge.textContent = g.status;
    item.appendChild(badge);

    var title = document.createElement("strong");
    title.textContent = " " + g.title + " ";
    item.appendChild(title);

    var idSpan = document.createElement("span");
    idSpan.className = "gate-id";
    idSpan.textContent = g.id;
    item.appendChild(idSpan);

    if (g.type === "requirement" && g.status === "pending_review") {
      var approveBtn = document.createElement("button");
      approveBtn.className = "btn-approve";
      approveBtn.textContent = "Approve";
      approveBtn.onclick = function () {
        sendCommand("approve_requirement", { req_id: g.id });
      };
      item.appendChild(document.createTextNode(" "));
      item.appendChild(approveBtn);

      var rejectBtn = document.createElement("button");
      rejectBtn.className = "btn-reject";
      rejectBtn.textContent = "Reject";
      rejectBtn.onclick = function () {
        sendCommand("reject_requirement", { req_id: g.id });
      };
      item.appendChild(document.createTextNode(" "));
      item.appendChild(rejectBtn);
    } else if (g.type === "story" && g.status === "merge_ready") {
      var mergeBtn = document.createElement("button");
      mergeBtn.className = "btn-merge";
      mergeBtn.textContent = "Merge";
      mergeBtn.onclick = function () {
        sendCommand("merge_story", { story_id: g.id });
      };
      item.appendChild(document.createTextNode(" "));
      item.appendChild(mergeBtn);
    }

    list.appendChild(item);
  });
}

function renderInvestigations(items) {
  var section = document.getElementById("investigations");
  var list = document.getElementById("investigations-list");
  if (!items || items.length === 0) {
    section.style.display = "none";
    return;
  }
  section.style.display = "";
  list.textContent = "";
  items.forEach(function (inv) {
    var item = document.createElement("div");
    item.className = "investigation-item";

    var reqId = document.createElement("strong");
    reqId.textContent = inv.req_id + " ";
    item.appendChild(reqId);

    var summary = document.createElement("span");
    summary.textContent = inv.summary;
    item.appendChild(summary);

    var badges = document.createElement("span");
    badges.className = "inv-badges";

    var modBadge = document.createElement("span");
    modBadge.className = "badge";
    modBadge.textContent = inv.module_count + " modules";
    badges.appendChild(document.createTextNode(" "));
    badges.appendChild(modBadge);

    var smellBadge = document.createElement("span");
    smellBadge.className = "badge badge-warn";
    smellBadge.textContent = inv.smell_count + " smells";
    badges.appendChild(document.createTextNode(" "));
    badges.appendChild(smellBadge);

    var riskBadge = document.createElement("span");
    riskBadge.className = "badge badge-danger";
    riskBadge.textContent = inv.risk_count + " risks";
    badges.appendChild(document.createTextNode(" "));
    badges.appendChild(riskBadge);

    item.appendChild(badges);
    list.appendChild(item);
  });
}

function renderRecoveryLog(items) {
  var section = document.getElementById("recovery-log");
  var list = document.getElementById("recovery-list");
  if (!items || items.length === 0) {
    section.style.display = "none";
    return;
  }
  section.style.display = "";
  list.textContent = "";
  items.forEach(function (r) {
    var item = document.createElement("div");
    item.className = "recovery-item";

    var ts = document.createElement("span");
    ts.className = "timestamp";
    ts.textContent = r.timestamp;
    item.appendChild(ts);

    var typeBadge = document.createElement("span");
    typeBadge.className = "badge";
    typeBadge.textContent = r.type;
    item.appendChild(document.createTextNode(" "));
    item.appendChild(typeBadge);

    var storyId = document.createElement("strong");
    storyId.textContent = " " + r.story_id + " ";
    item.appendChild(storyId);

    var desc = document.createElement("span");
    desc.textContent = r.description;
    item.appendChild(desc);

    list.appendChild(item);
  });
}

// ── DAG Visualization ────────────────────────────────────────────────

/** Status → fill color for DAG nodes. */
function dagNodeColor(status) {
  switch (status) {
    case "merged":
      return "#00CC66";
    case "in_progress":
      return "#00CCCC";
    case "review":
    case "qa":
      return "#FF9933";
    case "draft":
    case "planned":
    case "assigned":
      return "#555555";
    case "split":
      return "#6f42c1";
    default:
      return "#333333";
  }
}

/**
 * Render the dependency DAG as an SVG inside #dag-container.
 * Layout: nodes are grouped by wave (left-to-right), evenly spaced vertically
 * within each wave column.
 */
function renderDAG(dag, stories) {
  var section = document.getElementById("dag");
  var container = document.getElementById("dag-container");
  if (!dag || !dag.nodes || dag.nodes.length === 0) {
    section.style.display = "none";
    return;
  }
  section.style.display = "";

  // Build status lookup from stories array.
  var statusMap = {};
  (stories || []).forEach(function (s) {
    statusMap[s.id] = s.status;
  });

  // Group nodes by wave.
  var waves = {};
  var maxWave = 0;
  dag.nodes.forEach(function (n) {
    var w = n.wave || 0;
    if (!waves[w]) waves[w] = [];
    waves[w].push(n.id);
    if (w > maxWave) maxWave = w;
  });

  // Layout parameters.
  var nodeW = 120,
    nodeH = 32,
    padX = 60,
    padY = 20,
    marginL = 30,
    marginT = 40;
  var waveCount = maxWave + 1;

  // Compute positions: { id: {x, y} }.
  var pos = {};
  for (var w = 0; w <= maxWave; w++) {
    var col = waves[w] || [];
    var x = marginL + w * (nodeW + padX);
    for (var i = 0; i < col.length; i++) {
      var y = marginT + i * (nodeH + padY);
      pos[col[i]] = { x: x, y: y };
    }
  }

  // Determine max row count for SVG height.
  var maxRows = 0;
  for (var wk in waves) {
    if (waves[wk].length > maxRows) maxRows = waves[wk].length;
  }

  var svgW = marginL * 2 + waveCount * (nodeW + padX);
  var svgH = marginT + maxRows * (nodeH + padY) + 20;

  // Build SVG.
  var svg =
    '<svg xmlns="http://www.w3.org/2000/svg" width="' +
    svgW +
    '" height="' +
    svgH +
    '" style="background:#111;border-radius:6px">';

  // Wave band labels.
  for (var wb = 0; wb <= maxWave; wb++) {
    var bx = marginL + wb * (nodeW + padX) + nodeW / 2;
    svg +=
      '<text x="' +
      bx +
      '" y="18" fill="#00CCCC" font-size="11" text-anchor="middle" font-family="monospace">Wave ' +
      wb +
      "</text>";
  }

  // Edges.
  (dag.edges || []).forEach(function (e) {
    var from = pos[e.from];
    var to = pos[e.to];
    if (!from || !to) return;
    var x1 = from.x;
    var y1 = from.y + nodeH / 2;
    var x2 = to.x + nodeW;
    var y2 = to.y + nodeH / 2;
    svg +=
      '<line x1="' +
      x1 +
      '" y1="' +
      y1 +
      '" x2="' +
      x2 +
      '" y2="' +
      y2 +
      '" stroke="#444" stroke-width="1.5" marker-end="url(#arrow)"/>';
  });

  // Arrow marker.
  svg +=
    '<defs><marker id="arrow" markerWidth="8" markerHeight="6" refX="8" refY="3" orient="auto"><polygon points="0 0, 8 3, 0 6" fill="#444"/></marker></defs>';

  // Nodes.
  dag.nodes.forEach(function (n) {
    var p = pos[n.id];
    if (!p) return;
    var color = dagNodeColor(statusMap[n.id] || "draft");
    var label = n.id.length > 14 ? n.id.substring(0, 14) : n.id;
    svg +=
      '<rect x="' +
      p.x +
      '" y="' +
      p.y +
      '" width="' +
      nodeW +
      '" height="' +
      nodeH +
      '" rx="4" fill="' +
      color +
      '" opacity="0.85"/>';
    svg +=
      '<text x="' +
      (p.x + nodeW / 2) +
      '" y="' +
      (p.y + nodeH / 2 + 4) +
      '" fill="#fff" font-size="10" text-anchor="middle" font-family="monospace">' +
      esc(label) +
      "</text>";
  });

  svg += "</svg>";
  container.innerHTML = svg;
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
