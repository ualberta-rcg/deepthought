// DeepThought Unicomplex Server Console:
// Login (Password + OIDC + Browser-Link CLI approval), Live Fleet & Remote CLI controller,
// Systems & Cluster Capability Registry, Projects/Assets/Skills, Repeatable Pipelines,
// and SQL Tool Executions & 6-Tier Summary Inspector.
(function () {
  "use strict";

  var TOKEN_KEY = "deepthought.token";
  var USER_KEY = "deepthought.user";
  var selectedFleetNode = "";

  var $ = function (id) { return document.getElementById(id); };

  function token() { return localStorage.getItem(TOKEN_KEY) || ""; }

  function api(method, path, body) {
    return fetch(path, {
      method: method,
      headers: {
        "Authorization": "Bearer " + token(),
        "Content-Type": "application/json"
      },
      body: body === undefined ? undefined : JSON.stringify(body)
    }).then(function (res) {
      if (res.status === 401) { showLogin(); throw new Error("unauthorized"); }
      if (res.status === 503) { return res.json().then(function (j) { throw new Error(j.error || "auth not configured"); }); }
      if (!res.ok) { return res.json().then(function (j) { throw new Error(j.error || ("HTTP " + res.status)); }); }
      return res.json();
    });
  }

  function queryParams() {
    var out = {};
    var q = window.location.search.substring(1).split("&");
    for (var i = 0; i < q.length; i++) {
      var parts = q[i].split("=");
      if (parts[0]) {
        out[decodeURIComponent(parts[0])] = decodeURIComponent(parts[1] || "");
      }
    }
    return out;
  }

  function checkAuthConfig() {
    var qp = queryParams();
    if (qp.device_code) {
      if ($("device-code-input")) { $("device-code-input").value = qp.device_code; }
      if ($("device-login-hint")) {
        $("device-login-hint").hidden = false;
        $("device-login-hint").textContent = "CLI Browser-Link Login requested (Code: " + (qp.user_code || qp.device_code.slice(0, 8)) + "). Sign in below to approve.";
      }
    }
    fetch("/api/v1/auth/config").then(function (r) { return r.json(); }).then(function (cfg) {
      if (cfg && cfg.oidc_enabled && $("oidc-login-block")) {
        $("oidc-login-block").hidden = false;
        if (qp.device_code && $("oidc-login-btn")) {
          $("oidc-login-btn").href = "/api/v1/auth/oidc/login?device_code=" + encodeURIComponent(qp.device_code);
        }
      }
    }).catch(function () {});
  }

  function showLogin() {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(USER_KEY);
    $("login").hidden = false;
    $("dash").hidden = true;
    checkAuthConfig();
    $("login-user").focus();
  }

  function showDash(user) {
    $("login").hidden = true;
    $("dash").hidden = false;
    $("dash-user").textContent = user;
    var qp = queryParams();
    if (qp.device_code && $("device-approve-banner")) {
      $("device-approve-banner").hidden = false;
      $("device-code-input").value = qp.device_code;
      if (qp.user_code) {
        $("device-approve-text").textContent = "Approve DeepThought CLI Browser-Link login for code " + qp.user_code + " as " + user + "?";
      }
    }
    refreshAll();
  }

  function refreshAll() {
    refreshService();
    refreshDefaults();
    refreshUserSettings();
    refreshSessions();
    refreshFleet();
    refreshHosts();
    refreshProjectsAndSkills();
    refreshPipelinesAndJobs();
    refreshToolExecutions();
  }

  function refreshService() {
    api("GET", "/api/v1/version").then(function (v) {
      $("service-info").innerHTML = "";
      [["service", v.service], ["version", v.version], ["data dir", v.dataDir], ["user", localStorage.getItem(USER_KEY) || ""]].forEach(function (pair) {
        var dt = document.createElement("dt"); dt.textContent = pair[0];
        var dd = document.createElement("dd"); dd.textContent = pair[1];
        $("service-info").appendChild(dt); $("service-info").appendChild(dd);
      });
    }).catch(function (e) {
      if (e.message !== "unauthorized") { $("defaults-status").textContent = e.message; }
    });
  }

  function refreshDefaults() {
    api("GET", "/api/v1/settings/defaults").then(function (d) {
      $("defaults").value = JSON.stringify(d, null, 2);
      $("defaults-status").textContent = "";
    }).catch(function (e) {
      if (e.message !== "unauthorized") { $("defaults-status").textContent = e.message; }
    });
  }

  function refreshUserSettings() {
    api("GET", "/api/v1/user/settings").then(function (res) {
      var s = (res && res.settings) || {};
      var rf = s.ragflow || {};
      if ($("rag-base-url")) { $("rag-base-url").value = rf.base_url || ""; }
      if ($("rag-api-key")) { $("rag-api-key").value = rf.api_key || ""; }
      if ($("rag-datasets")) { $("rag-datasets").value = (rf.dataset_ids || []).join(", "); }
      window._userSettingsRev = res.revision || 0;
      window._userSettingsObj = s;
    }).catch(function () {});
  }

  function refreshSessions() {
    api("GET", "/api/v1/user/sessions").then(function (res) {
      var box = $("sessions-list");
      if (!box) { return; }
      box.innerHTML = "";
      (res.sessions || []).forEach(function (s) {
        var div = document.createElement("div");
        div.className = "item-card";
        div.innerHTML = '<div class="title"><span>' + escapeHTML(s.name || s.id) + (s.current ? " (current)" : "") + '</span><span>' + escapeHTML(s.id) + '</span></div>' +
          '<div class="meta">last seen: ' + escapeHTML(s.last_seen || "") + '</div>';
        box.appendChild(div);
      });
    }).catch(function () {});
  }

  function refreshFleet() {
    api("GET", "/api/v1/fleet/nodes").then(function (res) {
      var box = $("fleet-nodes-list");
      if (box) {
        box.innerHTML = "";
        var nodes = res.nodes || [];
        if (nodes.length === 0) {
          box.innerHTML = '<div class="hint">No online CLI nodes heartbeating yet. Connect a CLI with 2P MULTIPLAYER.</div>';
        }
        nodes.forEach(function (n) {
          var div = document.createElement("div");
          div.className = "item-card";
          var badge = n.busy ? "⚡ BUSY" : "● IDLE";
          if (n.pending_approval) { badge = "⚠ QUEEN ASK: " + n.pending_approval.tool_name; }
          div.innerHTML = '<div class="title"><span>' + escapeHTML((n.cluster || "host") + " · " + n.hostname) + '</span><span>' + escapeHTML(badge) + '</span></div>' +
            '<div class="meta">id=' + escapeHTML(n.system_id || n.client_id) + ' · model=' + escapeHTML(n.active_model || "-") + ' · caps=' + escapeHTML(n.capabilities || "-") + '</div>';
          div.addEventListener("click", function () {
            selectedFleetNode = n.client_id || n.system_id || n.hostname;
            if ($("fleet-target")) { $("fleet-target").value = selectedFleetNode; }
            if ($("fleet-terminal-view")) {
              $("fleet-terminal-view").textContent = n.terminal_frame || ("[" + n.hostname + " (" + n.system_id + ") online — no terminal frame snapshot yet]");
            }
          });
          box.appendChild(div);
        });
      }
      var tbox = $("fleet-tasks-list");
      if (tbox) {
        tbox.innerHTML = "";
        (res.tasks || []).slice(0, 20).forEach(function (t) {
          var div = document.createElement("div");
          div.className = "item-card";
          div.innerHTML = '<div class="title"><span>' + escapeHTML(t.kind + " → " + t.target_client) + '</span><span>' + escapeHTML(t.status) + '</span></div>' +
            '<div class="meta">' + escapeHTML(t.payload || t.tool_name || "") + (t.output ? (" | out: " + t.output.slice(0, 120)) : "") + '</div>';
          tbox.appendChild(div);
        });
      }
    }).catch(function () {});
  }

  function refreshHosts() {
    api("GET", "/api/v1/hosts").then(function (res) {
      var box = $("hosts-list");
      if (!box) { return; }
      box.innerHTML = "";
      var hosts = res.hosts || [];
      if (hosts.length === 0) {
        box.innerHTML = '<div class="hint">No host systems recorded yet.</div>';
      }
      hosts.forEach(function (h) {
        var r = h.record || {};
        var svcs = r.services ? Object.keys(r.services).map(function (k) { return k + "=" + r.services[k]; }).join(", ") : "";
        var div = document.createElement("div");
        div.className = "item-card";
        div.innerHTML = '<div class="title"><span>' + escapeHTML((r.cluster_id || r.site || "system") + " · " + (r.name || h.id)) + '</span><span>UUID: ' + escapeHTML(h.id) + '</span></div>' +
          '<div class="meta">OS/Arch: ' + escapeHTML((r.os || "") + "/" + (r.arch || "")) + ' · CPUs: ' + (r.cpus || 0) + ' · Mem: ' + (r.memory_gb || 0) + ' GB</div>' +
          '<div class="meta">Detected Capabilities: ' + escapeHTML(svcs || "none") + '</div>';
        box.appendChild(div);
      });
    }).catch(function () {});
  }

  function refreshProjectsAndSkills() {
    api("GET", "/api/v1/projects").then(function (res) {
      var box = $("projects-list");
      if (!box) { return; }
      box.innerHTML = "";
      (res.projects || []).forEach(function (item) {
        var p = item.record || {};
        var assets = (p.assets || []).length;
        var div = document.createElement("div");
        div.className = "item-card";
        div.innerHTML = '<div class="title"><span>' + escapeHTML(p.name || item.id) + (p.active ? " ★" : "") + '</span><span>' + assets + ' asset(s)</span></div>' +
          '<div class="meta">root: ' + escapeHTML(p.root_dir || "") + ' · clusters: ' + escapeHTML((p.clusters || []).join(",") || "any") + '</div>';
        box.appendChild(div);
      });
    }).catch(function () {});

    api("GET", "/api/v1/skills").then(function (res) {
      var box = $("skills-list");
      if (!box) { return; }
      box.innerHTML = "";
      (res.skills || []).forEach(function (item) {
        var s = item.record || {};
        var div = document.createElement("div");
        div.className = "item-card";
        div.innerHTML = '<div class="title"><span>' + escapeHTML(s.name || item.id) + '</span><span>' + escapeHTML(item.updated || "") + '</span></div>' +
          '<div class="meta">' + escapeHTML(s.description || "") + '</div>';
        box.appendChild(div);
      });
    }).catch(function () {});
  }

  function refreshPipelinesAndJobs() {
    api("GET", "/api/v1/pipelines").then(function (res) {
      var box = $("pipelines-list");
      if (!box) { return; }
      box.innerHTML = "";
      (res.pipelines || []).forEach(function (item) {
        var tpl = item.record || {};
        var steps = (tpl.steps || []).length;
        var exCount = tpl.exemplars ? Object.keys(tpl.exemplars).length : 0;
        var div = document.createElement("div");
        div.className = "item-card";
        div.innerHTML = '<div class="title"><span>' + escapeHTML(tpl.name || item.id) + '</span><span>' + steps + ' steps · ' + exCount + ' Worked Examples</span></div>' +
          '<div class="meta">' + escapeHTML(tpl.description || "") + '</div>';
        box.appendChild(div);
      });
    }).catch(function () {});

    api("GET", "/api/v1/experiments").then(function (res) {
      var box = $("experiments-list");
      if (!box) { return; }
      box.innerHTML = "";
      (res.experiments || []).forEach(function (item) {
        var p = item.record || {};
        var dir = p.directive || {};
        var div = document.createElement("div");
        div.className = "item-card";
        div.innerHTML = '<div class="title"><span>' + escapeHTML(dir.title || item.id) + '</span><span>' + escapeHTML(dir.status || "") + '</span></div>' +
          '<div class="meta">rev ' + (p.revision || 1) + ' · system=' + escapeHTML(p.system_id || "local") + '</div>';
        box.appendChild(div);
      });
    }).catch(function () {});

    api("GET", "/api/v1/jobs").then(function (res) {
      var box = $("jobs-list");
      if (!box) { return; }
      box.innerHTML = "";
      (res.jobs || []).forEach(function (item) {
        var j = item.record || {};
        var div = document.createElement("div");
        div.className = "item-card";
        div.innerHTML = '<div class="title"><span>Job ' + escapeHTML(j.job_id || item.id) + ' (' + escapeHTML(j.cluster || j.host || "cluster") + ')</span><span>' + escapeHTML(j.state || "") + '</span></div>' +
          '<div class="meta">system=' + escapeHTML(j.system_id || "-") + ' · script=' + escapeHTML(j.script_path || "") + '</div>';
        box.appendChild(div);
      });
    }).catch(function () {});
  }

  function refreshToolExecutions() {
    var tool = $("sql-tool-filter") ? $("sql-tool-filter").value.trim() : "";
    var path = "/api/v1/tools/executions" + (tool ? ("?tool=" + encodeURIComponent(tool)) : "");
    api("GET", path).then(function (res) {
      var box = $("tool-executions-list");
      if (!box) { return; }
      box.innerHTML = "";
      (res.executions || []).forEach(function (ex) {
        var div = document.createElement("div");
        div.className = "item-card";
        div.innerHTML = '<div class="title"><span>' + escapeHTML(ex.tool_name) + ' · ' + escapeHTML(ex.command || ex.target_path || ex.id) + '</span><span>exit=' + ex.exit_code + ' (' + ex.duration_ms + 'ms)</span></div>' +
          '<div class="meta">id=' + escapeHTML(ex.id) + ' · system=' + escapeHTML(ex.system_id || ex.cluster_name || "local") + ' · ' + escapeHTML(ex.response_summary || "") + '</div>';
        div.addEventListener("click", function () {
          if ($("summary-interaction-id")) {
            $("summary-interaction-id").value = ex.id;
            loadSummariesFor(ex.id);
          }
        });
        box.appendChild(div);
      });
    }).catch(function () {});
  }

  function loadSummariesFor(id) {
    if (!id) { return; }
    api("GET", "/api/v1/summaries/" + encodeURIComponent(id)).then(function (res) {
      var box = $("summaries-view");
      if (!box) { return; }
      box.innerHTML = "";
      (res.summaries || []).forEach(function (s) {
        var div = document.createElement("div");
        div.className = "item-card";
        div.innerHTML = '<div class="title"><span>Tier: ' + escapeHTML(s.state) + '</span><span>~' + s.tokens + ' tok (' + escapeHTML(s.summarizer) + ')</span></div>' +
          '<pre class="meta" style="white-space:pre-wrap;margin-top:6px;">' + escapeHTML(s.text) + '</pre>';
        box.appendChild(div);
      });
    }).catch(function () {});
  }

  function escapeHTML(s) {
    return String(s || "").replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  // Tab switching
  if ($("nav-tabs")) {
    $("nav-tabs").addEventListener("click", function (ev) {
      var btn = ev.target.closest(".tab");
      if (!btn) { return; }
      var target = btn.getAttribute("data-tab");
      document.querySelectorAll(".tab").forEach(function (b) { b.classList.toggle("active", b === btn); });
      document.querySelectorAll(".tab-panel").forEach(function (p) { p.hidden = (p.id !== target); });
    });
  }

  $("login-form").addEventListener("submit", function (ev) {
    ev.preventDefault();
    $("login-error").hidden = true;
    fetch("/api/v1/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ user: $("login-user").value, password: $("login-pass").value })
    }).then(function (res) {
      return res.json().then(function (j) { return { ok: res.ok, j: j }; });
    }).then(function (r) {
      if (!r.ok) { throw new Error(r.j.error || "login failed"); }
      localStorage.setItem(TOKEN_KEY, r.j.token);
      localStorage.setItem(USER_KEY, r.j.user);
      showDash(r.j.user);
    }).catch(function (e) {
      $("login-error").textContent = e.message;
      $("login-error").hidden = false;
    });
  });

  $("save-defaults").addEventListener("click", function () {
    var parsed;
    try { parsed = JSON.parse($("defaults").value); }
    catch (e) { $("defaults-status").textContent = "invalid JSON: " + e.message; return; }
    api("PUT", "/api/v1/settings/defaults", parsed).then(function () {
      $("defaults-status").textContent = "saved";
      refreshDefaults();
    }).catch(function (e) {
      if (e.message !== "unauthorized") { $("defaults-status").textContent = e.message; }
    });
  });

  if ($("save-user-settings")) {
    $("save-user-settings").addEventListener("click", function () {
      var base = window._userSettingsObj || {};
      var ds = ($("rag-datasets").value || "").split(",").map(function (x) { return x.trim(); }).filter(Boolean);
      base.ragflow = {
        base_url: $("rag-base-url").value.trim(),
        api_key: $("rag-api-key").value.trim(),
        dataset_ids: ds
      };
      api("PUT", "/api/v1/user/settings", {
        revision: window._userSettingsRev || 0,
        settings: base
      }).then(function (r) {
        window._userSettingsRev = r.revision;
        $("user-settings-status").textContent = "saved (rev " + r.revision + ")";
      }).catch(function (e) {
        $("user-settings-status").textContent = e.message;
      });
    });
  }

  if ($("device-approve-btn")) {
    $("device-approve-btn").addEventListener("click", function () {
      var code = $("device-code-input").value.trim();
      if (!code) { return; }
      api("POST", "/api/v1/auth/device/approve", { device_code: code }).then(function (res) {
        $("device-approve-status").textContent = "✓ Approved for " + (res.user || "CLI") + "! You can return to your terminal.";
      }).catch(function (e) {
        $("device-approve-status").textContent = "✗ " + e.message;
      });
    });
  }

  if ($("fleet-dispatch-btn")) {
    $("fleet-dispatch-btn").addEventListener("click", function () {
      var target = $("fleet-target").value.trim() || "any";
      var kind = $("fleet-kind").value;
      var payload = $("fleet-payload").value;
      api("POST", "/api/v1/fleet/dispatch", {
        source_client: "web-console",
        target_client: target,
        kind: kind,
        payload: payload
      }).then(function (res) {
        $("fleet-dispatch-status").textContent = "Queued task " + (res.task && res.task.id) + " → " + target;
        $("fleet-payload").value = "";
        refreshFleet();
      }).catch(function (e) {
        $("fleet-dispatch-status").textContent = "✗ " + e.message;
      });
    });
  }

  if ($("create-project-btn")) {
    $("create-project-btn").addEventListener("click", function () {
      var name = $("new-project-name").value.trim();
      if (!name) { return; }
      var id = "proj_" + name.toLowerCase().replace(/[^a-z0-9]+/g, "_");
      api("PUT", "/api/v1/projects/" + encodeURIComponent(id), {
        id: id,
        name: name,
        root_dir: $("new-project-root").value.trim(),
        active: true,
        created_at: new Date().toISOString()
      }).then(function () {
        $("new-project-name").value = "";
        refreshProjectsAndSkills();
      }).catch(function () {});
    });
  }

  if ($("sql-tool-refresh")) {
    $("sql-tool-refresh").addEventListener("click", refreshToolExecutions);
  }
  if ($("summary-fetch-btn")) {
    $("summary-fetch-btn").addEventListener("click", function () {
      loadSummariesFor($("summary-interaction-id").value.trim());
    });
  }
  if ($("refresh-all")) {
    $("refresh-all").addEventListener("click", refreshAll);
  }

  $("logout").addEventListener("click", function () { showLogin(); });

  // Boot: restore a session if we have one.
  if (token()) {
    api("GET", "/api/v1/version").then(function () {
      showDash(localStorage.getItem(USER_KEY) || "?");
    }).catch(function () { /* 401 path already showed login */ });
  } else {
    showLogin();
  }
})();
