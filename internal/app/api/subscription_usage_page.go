package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// wantsSubscriptionUsagePage reports whether a /usage request comes from a
// browser opening the link (the "usage details" button of a subscription
// page). Scripts, apps and ?format=json keep getting the JSON payload.
func wantsSubscriptionUsagePage(r *http.Request) bool {
	if strings.EqualFold(r.URL.Query().Get("format"), "json") {
		return false
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}

// writeSubscriptionUsagePage renders the usage payload as a readable page.
// encoding/json escapes <, > and &, so the payload is safe inside <script>.
func writeSubscriptionUsagePage(w http.ResponseWriter, payload map[string]any) {
	data, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(strings.Replace(subscriptionUsagePage, "__USAGE_JSON__", string(data), 1)))
}

const subscriptionUsagePage = `<!DOCTYPE html>
<html lang="fa" dir="rtl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="robots" content="noindex, nofollow">
<meta name="theme-color" content="#080b16">
<title>Usage</title>
<style>
@font-face { font-family: "Arad"; src: url("/statics/fonts/Arad-Regular.woff2") format("woff2"); font-weight: 400; font-display: swap; }
@font-face { font-family: "Arad"; src: url("/statics/fonts/Arad-Bold.woff2") format("woff2"); font-weight: 700; font-display: swap; }
:root { color-scheme: dark; --bg: #080b16; --surface: #0f1426; --surface-2: #151b31; --line: rgba(255,255,255,.09); --text: #eef1fb; --muted: #97a0bd; --accent: #e3b95f; --accent-2: #8b7cf6; --faint: rgba(255,255,255,.06); }
* { box-sizing: border-box; }
[hidden] { display: none !important; }
body { margin: 0; min-height: 100vh; background: radial-gradient(1100px 520px at 50% -10%, #1b2345 0%, #080b16 62%) fixed, var(--bg); color: var(--text); font: 15px/1.6 "Arad", Inter, "Segoe UI", system-ui, sans-serif; }
:root[dir="ltr"] body { font-family: Inter, "Segoe UI", system-ui, sans-serif; }
a, button { color: inherit; font: inherit; }
.wrap { width: min(760px, 100%); margin: 0 auto; padding: 22px 16px 44px; display: grid; grid-template-columns: minmax(0, 1fr); gap: 14px; }
.top { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.back { text-decoration: none; color: var(--muted); font-size: 14px; }
.lang { border: 1px solid var(--line); background: var(--surface); border-radius: 999px; padding: 5px 14px; font-size: 13px; font-weight: 700; cursor: pointer; }
h1 { margin: 0; font-size: 20px; }
.sub { margin: 2px 0 0; color: var(--muted); font-size: 13px; }
.section { background: var(--surface); border: 1px solid var(--line); border-radius: 20px; padding: 18px; }
.tabs { display: flex; gap: 6px; flex-wrap: wrap; }
.tab { border: 1px solid var(--line); background: transparent; border-radius: 999px; padding: 5px 14px; font-size: 13px; color: var(--muted); cursor: pointer; }
.tab[aria-pressed="true"] { background: rgba(227,185,95,.14); border-color: var(--accent); color: var(--text); font-weight: 700; }
.kpis { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; }
.kpi { background: var(--surface-2); border: 1px solid var(--line); border-radius: 14px; padding: 10px 12px; min-width: 0; }
.kpi span { display: block; color: var(--muted); font-size: 12px; }
.kpi strong { display: block; font-size: 17px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.kpi small { display: block; color: var(--muted); font-size: 12px; }
.chart { display: flex; align-items: flex-end; gap: 3px; height: 180px; padding-top: 8px; direction: ltr; }
:root[dir="rtl"] .chart { direction: rtl; }
.bar { flex: 1; min-width: 3px; height: 100%; display: flex; align-items: flex-end; position: relative; }
.bar i { display: block; width: 100%; min-height: 2px; border-radius: 4px 4px 1px 1px; background: linear-gradient(180deg, var(--accent), #b8893a); }
.bar.zero i { background: var(--faint); }
.bar:hover i, .bar:focus i { background: linear-gradient(180deg, #fff1c9, var(--accent)); }
.axis { display: flex; justify-content: space-between; color: var(--muted); font-size: 11px; margin-top: 6px; }
.tip { min-height: 22px; margin-top: 8px; color: var(--muted); font-size: 13px; text-align: center; }
.nodes { display: grid; gap: 8px; }
.node { background: var(--surface-2); border: 1px solid var(--line); border-radius: 14px; padding: 10px 12px; }
.node-head { display: flex; justify-content: space-between; gap: 10px; font-size: 14px; }
.node-head span { color: var(--muted); font-size: 13px; }
.meter { height: 6px; border-radius: 99px; background: var(--faint); margin-top: 8px; overflow: hidden; }
.meter i { display: block; height: 100%; background: linear-gradient(90deg, var(--accent), var(--accent-2)); border-radius: inherit; }
.empty { color: var(--muted); text-align: center; padding: 18px 0; font-size: 14px; }
h2 { margin: 0 0 12px; font-size: 15px; }
@media (max-width: 480px) { .kpi strong { font-size: 15px; } .chart { height: 150px; gap: 2px; } }
</style>
</head>
<body>
<main class="wrap">
  <div class="top">
    <a class="back" id="back" href="./"></a>
    <button class="lang" id="lang" type="button">EN</button>
  </div>
  <div>
    <h1 id="title"></h1>
    <p class="sub"><bdi id="username"></bdi> · <span id="range"></span></p>
  </div>
  <div class="tabs" id="tabs"></div>
  <section class="kpis">
    <div class="kpi"><span data-i="total"></span><strong id="total">&ndash;</strong></div>
    <div class="kpi"><span data-i="average"></span><strong id="average">&ndash;</strong></div>
    <div class="kpi"><span data-i="peak"></span><strong id="peak">&ndash;</strong><small id="peakDay"></small></div>
  </section>
  <section class="section">
    <h2 data-i="daily"></h2>
    <div class="chart" id="chart" role="img"></div>
    <div class="axis"><span id="axisStart"></span><span id="axisEnd"></span></div>
    <div class="tip" id="tip"></div>
  </section>
  <section class="section">
    <h2 data-i="byServer"></h2>
    <div class="nodes" id="nodes"></div>
  </section>
</main>
<script id="usageData" type="application/json">__USAGE_JSON__</script>
<script>
(function () {
  "use strict";
  var TEXT = {
    fa: { title: "جزئیات مصرف", back: "بازگشت به اشتراک", total: "مصرف این بازه", average: "میانگین روزانه", peak: "بیشترین روز", daily: "مصرف روزانه", byServer: "مصرف به تفکیک سرور", noUsage: "در این بازه مصرفی ثبت نشده است.", days: "{n} روز", tapHint: "روی هر ستون بزنید تا مصرف آن روز را ببینید.", share: "{n}٪ از کل" },
    en: { title: "Usage details", back: "Back to subscription", total: "Used in period", average: "Daily average", peak: "Busiest day", daily: "Daily usage", byServer: "Usage by server", noUsage: "No usage in this period.", days: "{n} days", tapHint: "Tap a bar to see that day's usage.", share: "{n}% of total" }
  };
  var PERIODS = [7, 30, 90];
  var data = JSON.parse(document.getElementById("usageData").textContent || "{}");
  var lang = (function () { try { return localStorage.getItem("next-sub-lang"); } catch (e) { return null; } })() || "fa";
  if (!TEXT[lang]) lang = "fa";
  function $(id) { return document.getElementById(id); }
  function t(key, n) { var v = TEXT[lang][key] || key; return n == null ? v : v.replace("{n}", n); }
  function nf(v, d) { return new Intl.NumberFormat(lang === "fa" ? "fa-IR" : "en-US", { maximumFractionDigits: d == null ? 1 : d }).format(v); }
  function bytes(b) {
    var u = lang === "fa" ? ["بایت", "کیلوبایت", "مگابایت", "گیگ", "ترابایت"] : ["B", "KB", "MB", "GB", "TB"], i = 0; b = Math.max(0, Number(b) || 0);
    while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
    return nf(b, b >= 100 || i === 0 ? 0 : 1) + " " + u[i];
  }
  function day(iso, withYear) {
    try { return new Intl.DateTimeFormat(lang === "fa" ? "fa-IR-u-ca-persian" : "en-GB", withYear ? { year: "numeric", month: "short", day: "numeric" } : { month: "short", day: "numeric" }).format(new Date(iso + (iso.length === 10 ? "T12:00:00Z" : ""))); }
    catch (e) { return iso.slice(0, 10); }
  }
  function periodDays() {
    var s = Date.parse(data.start), e = Date.parse(data.end);
    return isFinite(s) && isFinite(e) ? Math.round((e - s) / 86400000) : 30;
  }
  function render() {
    document.documentElement.lang = lang; document.documentElement.dir = lang === "fa" ? "rtl" : "ltr";
    $("lang").textContent = lang === "fa" ? "EN" : "فا";
    document.title = t("title");
    Array.prototype.forEach.call(document.querySelectorAll("[data-i]"), function (el) { el.textContent = t(el.getAttribute("data-i")); });
    $("title").textContent = t("title"); $("back").textContent = (lang === "fa" ? "→ " : "← ") + t("back");
    $("username").textContent = data.username || "";
    $("range").textContent = data.start ? day(data.start, true) + " – " + day(data.end, true) : "";

    var current = periodDays(), tabs = $("tabs"); tabs.innerHTML = "";
    PERIODS.forEach(function (n) {
      var b = document.createElement("button"); b.type = "button"; b.className = "tab";
      b.setAttribute("aria-pressed", String(Math.abs(current - n) <= 1)); b.textContent = t("days", nf(n, 0));
      b.addEventListener("click", function () {
        var end = new Date(), start = new Date(end.getTime() - n * 86400000);
        location.search = "?start=" + encodeURIComponent(start.toISOString()) + "&end=" + encodeURIComponent(end.toISOString());
      });
      tabs.appendChild(b);
    });

    var usages = Array.isArray(data.usages) ? data.usages : [];
    var total = 0, peak = null;
    usages.forEach(function (u) { var v = Number(u.used_traffic) || 0; total += v; if (!peak || v > peak.v) peak = { v: v, d: u.date }; });
    $("total").textContent = bytes(total);
    $("average").textContent = bytes(usages.length ? total / usages.length : 0);
    $("peak").textContent = peak && peak.v > 0 ? bytes(peak.v) : "–";
    $("peakDay").textContent = peak && peak.v > 0 ? day(peak.d) : "";

    var chart = $("chart"), max = peak ? peak.v : 0; chart.innerHTML = "";
    usages.forEach(function (u) {
      var v = Number(u.used_traffic) || 0, bar = document.createElement("div"), fill = document.createElement("i");
      bar.className = "bar" + (v > 0 ? "" : " zero"); bar.tabIndex = 0;
      fill.style.height = (max > 0 ? Math.max(2, v / max * 100) : 2) + "%";
      var label = day(u.date, true) + " · " + bytes(v); bar.title = label; bar.setAttribute("aria-label", label);
      var show = function () { $("tip").textContent = label; };
      bar.addEventListener("click", show); bar.addEventListener("focus", show); bar.addEventListener("mouseenter", show);
      bar.appendChild(fill); chart.appendChild(bar);
    });
    $("axisStart").textContent = usages.length ? day(usages[0].date) : "";
    $("axisEnd").textContent = usages.length ? day(usages[usages.length - 1].date) : "";
    $("tip").textContent = total > 0 ? t("tapHint") : t("noUsage");

    var nodes = (Array.isArray(data.node_usages) ? data.node_usages : []).map(function (n) {
      return { name: n.node_name || ("#" + n.node_id), v: (Number(n.uplink) || 0) + (Number(n.downlink) || 0) };
    }).filter(function (n) { return n.v > 0; }).sort(function (a, b) { return b.v - a.v; });
    var nodeTotal = nodes.reduce(function (s, n) { return s + n.v; }, 0), box = $("nodes"); box.innerHTML = "";
    if (!nodes.length) { var e = document.createElement("div"); e.className = "empty"; e.textContent = t("noUsage"); box.appendChild(e); }
    nodes.forEach(function (n) {
      var row = document.createElement("div"); row.className = "node";
      var head = document.createElement("div"); head.className = "node-head";
      var name = document.createElement("strong"); name.textContent = n.name; name.dir = "auto";
      var val = document.createElement("span"); var pct = nodeTotal ? Math.round(n.v / nodeTotal * 100) : 0;
      val.textContent = bytes(n.v) + " · " + t("share", nf(pct, 0));
      var meter = document.createElement("div"); meter.className = "meter"; var fill = document.createElement("i"); fill.style.width = pct + "%";
      head.appendChild(name); head.appendChild(val); meter.appendChild(fill); row.appendChild(head); row.appendChild(meter); box.appendChild(row);
    });
  }
  $("back").href = location.pathname.replace(/\/usage\/?$/, "") || "./";
  $("lang").addEventListener("click", function () { lang = lang === "fa" ? "en" : "fa"; try { localStorage.setItem("next-sub-lang", lang); } catch (e) {} render(); });
  render();
})();
</script>
</body>
</html>`
