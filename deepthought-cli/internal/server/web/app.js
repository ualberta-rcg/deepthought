// DeepThought server UI — phase 1: login + service info + settings defaults.
(function () {
  "use strict";

  var TOKEN_KEY = "deepthought.token";
  var USER_KEY = "deepthought.user";

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

  function showLogin() {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(USER_KEY);
    $("login").hidden = false;
    $("dash").hidden = true;
    $("login-user").focus();
  }

  function showDash(user) {
    $("login").hidden = true;
    $("dash").hidden = false;
    $("dash-user").textContent = user;
    refreshService();
    refreshDefaults();
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
