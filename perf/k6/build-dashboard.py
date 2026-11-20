#!/usr/bin/env python3
"""Build an engineering-style (Grafana/terminal) dashboard from k6 output.

Reads:  results.json (k6 --out json), summary.json (handleSummary)
Writes: dashboard.html  (single file, inline SVG, no external deps)

Panels: run metadata, SLO stats, latency-vs-time scatter, CDF, histogram,
per-route bars with datastore class, phase (TTFB) table, load curve,
full percentile table, endpoint mix + threshold verdicts.
"""
import json
import math
import sys
from collections import defaultdict
from datetime import datetime

RESULTS = sys.argv[1] if len(sys.argv) > 1 else "results.json"
SUMMARY = sys.argv[2] if len(sys.argv) > 2 else "summary.json"
OUT = sys.argv[3] if len(sys.argv) > 3 else "dashboard.html"

BG, PANEL, BORDER, GRID = "#0b0d10", "#12161b", "#242a33", "#1c222a"
TXT, TXT2 = "#d5dae2", "#7d8794"
GREEN, YELLOW, BLUE, ORANGE, RED, PURPLE = (
    "#4ade80", "#fbbf24", "#5b9bf8", "#fb923c", "#f87171", "#c084fc")
MONO = '"JetBrains Mono","SF Mono","DejaVu Sans Mono",Menlo,Consolas,monospace'
SANS = '-apple-system,"Segoe UI",Roboto,"DejaVu Sans",sans-serif'

# What a route must touch before it can answer (read from the Go handlers).
CLASS = {
    "/api/push/vapid-public-key": ("config", GREEN),
    "/api/location": ("redis", YELLOW),
    "/api/location/nearby": ("redis", YELLOW),
    "/api/users/_current": ("mysql", BLUE),
    "/api/contacts": ("mysql", BLUE),
    "/api/emergency-contacts": ("mysql", BLUE),
    "/api/push/subscriptions": ("mysql", BLUE),
    "/api/sos/active": ("mysql", BLUE),
    "/api/sos/inbox": ("mysql", BLUE),
    "/api/sos/history": ("mysql", BLUE),
    "/api/sos/summary": ("mysql", BLUE),
}


def pct(sorted_vals, p):
    if not sorted_vals:
        return 0.0
    k = (len(sorted_vals) - 1) * p
    lo, hi = math.floor(k), math.ceil(k)
    if lo == hi:
        return sorted_vals[int(k)]
    return sorted_vals[lo] * (hi - k) + sorted_vals[hi] * (k - lo)


def fmt_ms(v):
    if v >= 1000:
        return f"{v / 1000:.2f}s"
    return f"{v:.0f}ms"


def fmt_small(v):
    if v < 0.005:
        return "0"
    if v < 10:
        return f"{v:.2f}ms"
    return fmt_ms(v)


def nice_step(span, n=4):
    if span <= 0:
        return 1
    raw = span / n
    mag = 10 ** math.floor(math.log10(raw))
    for mult in (1, 2, 2.5, 5, 10):
        if raw <= mult * mag:
            return mult * mag
    return 10 * mag


def load_points(path):
    series = defaultdict(list)
    with open(path) as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                o = json.loads(line)
            except json.JSONDecodeError:
                continue
            if o.get("type") == "Point":
                d = o["data"]
                series[o["metric"]].append(
                    (datetime.fromisoformat(d["time"]).timestamp(), d["value"], d.get("tags") or {}))
    for v in series.values():
        v.sort(key=lambda x: x[0])
    return series


def bucketize(points, size, agg):
    if not points:
        return []
    if agg == "count":
        out = defaultdict(int)
        for t, _, _ in points:
            out[int(t // size) * size] += 1
        return [(k, v) for k, v in sorted(out.items())]
    q = {"avg": None, "p50": .5, "p95": .95, "p99": .99, "p999": .999}[agg]
    out = defaultdict(list)
    for t, v, _ in points:
        out[int(t // size) * size].append(v)
    res = []
    for k in sorted(out):
        vals = sorted(out[k])
        res.append((k, sum(vals) / len(vals) if q is None else pct(vals, q)))
    return res


def axes(width, height, ml, mr, mt, mb, hi, step, yfmt, t0, span, show_x, xticks=6):
    pw, ph = width - ml - mr, height - mt - mb
    out = []

    def X(t):
        return ml + (t - t0) / span * pw

    def Y(v):
        return mt + ph - v / (hi or 1) * ph

    for i in range(max(1, round(hi / step)) + 1):
        y = Y(step * i)
        out.append(f'<line x1="{ml}" y1="{y:.1f}" x2="{width - mr}" y2="{y:.1f}" stroke="{GRID}"/>')
        out.append(f'<text x="{ml - 8}" y="{y + 4:.1f}" fill="{TXT2}" font-size="10" '
                   f'text-anchor="end" font-family={MONO!r}>{yfmt(step * i)}</text>')
    if show_x:
        for i in range(xticks + 1):
            t = t0 + span * i / xticks
            x = X(t)
            anchor = "middle"
            if x < ml + 26:
                anchor = "start"
            elif x > width - mr - 26:
                anchor = "end"
            out.append(f'<text x="{x:.1f}" y="{height - 8}" fill="{TXT2}" font-size="10" '
                       f'text-anchor="{anchor}" font-family={MONO!r}>{datetime.fromtimestamp(t).strftime("%H:%M:%S")}</text>')
    return "".join(out), X, Y


def line_chart(width, height, series, yfmt=fmt_ms, legend_values=None, show_x=True):
    ml, mr, mt, mb = 62, 14, 12, 30 if show_x else 8
    allv = [v for _, _, pts, _ in series for _, v in pts]
    if not allv:
        return ""
    step = nice_step(max(allv) * 1.08, 4)
    hi = math.ceil(max(allv) * 1.08 / step) * step
    times = sorted({t for _, _, pts, _ in series for t, _ in pts})
    t0, t1 = min(times), max(times)
    span = (t1 - t0) or 1
    grid, X, Y = axes(width, height, ml, mr, mt, mb, hi, step, yfmt, t0, span, show_x)
    parts = [f'<svg viewBox="0 0 {width} {height}" width="100%" height="{height}">', grid]
    for _label, color, pts, fill in series:
        d = " ".join(f'{"M" if i == 0 else "L"}{X(t):.1f},{Y(v):.1f}' for i, (t, v) in enumerate(pts))
        if fill:
            parts.append(f'<path d="{d} L{X(pts[-1][0]):.1f},{Y(0):.1f} L{X(pts[0][0]):.1f},{Y(0):.1f} Z" '
                         f'fill="{color}" opacity="0.10"/>')
        parts.append(f'<path d="{d}" fill="none" stroke="{color}" stroke-width="1.8"/>')
    parts.append("</svg>")
    legend = ""
    if legend_values:
        legend = '<div class="legend">' + "".join(
            f'<span class="lg"><i style="background:{c}"></i>{l} <b>{v}</b></span>'
            for (l, c, _, _), v in zip(series, legend_values)) + "</div>"
    return legend + "".join(parts)


def scatter_chart(width, height, samples, series_meta, p95_line):
    """samples: [(t, dur, class_key)] — one dot per request."""
    ml, mr, mt, mb = 62, 14, 12, 30
    maxv = max(s[1] for s in samples)
    step = nice_step(maxv * 1.05, 5)
    hi = math.ceil(maxv * 1.05 / step) * step
    t0 = min(s[0] for s in samples)
    t1 = max(s[0] for s in samples)
    span = (t1 - t0) or 1
    grid, X, Y = axes(width, height, ml, mr, mt, mb, hi, step, fmt_ms, t0, span, True)
    parts = [f'<svg viewBox="0 0 {width} {height}" width="100%" height="{height}">', grid]
    d = " ".join(f'{"M" if i == 0 else "L"}{X(t):.1f},{Y(v):.1f}' for i, (t, v) in enumerate(p95_line))
    parts.append(f'<path d="{d}" fill="none" stroke="{RED}" stroke-width="1.5" stroke-dasharray="5 4" opacity="0.85"/>')
    for key, color, _label in series_meta:
        parts.extend(f'<circle cx="{X(t):.1f}" cy="{Y(v):.1f}" r="2.1" fill="{color}" opacity="0.62"/>'
                     for t, v, k in samples if k == key)
    parts.append("</svg>")
    legend = '<div class="legend">' + "".join(
        f'<span class="lg"><i style="background:{c}"></i>{l}</span>' for _, c, l in series_meta)
    legend += f'<span class="lg"><i class="dash" style="background:{RED}"></i>p95 per 5s bucket</span></div>'
    return legend + "".join(parts)


def cdf_chart(width, height, sorted_vals, marks=(50, 90, 95, 99)):
    ml, mr, mt, mb = 62, 18, 14, 30
    maxv = sorted_vals[-1]
    step = nice_step(maxv, 5)
    hi = math.ceil(maxv / step) * step
    pw, ph = width - ml - mr, height - mt - mb
    n = len(sorted_vals)
    parts = [f'<svg viewBox="0 0 {width} {height}" width="100%" height="{height}">']
    for i in range(int(hi / step) + 1):
        x = ml + step * i / hi * pw
        parts.append(f'<line x1="{x:.1f}" y1="{mt}" x2="{x:.1f}" y2="{mt + ph}" stroke="{GRID}"/>')
        parts.append(f'<text x="{x:.1f}" y="{height - 8}" fill="{TXT2}" font-size="10" text-anchor="middle" '
                     f'font-family={MONO!r}>{fmt_ms(step * i)}</text>')
    for p in (0, 25, 50, 75, 100):
        y = mt + ph - p / 100 * ph
        parts.append(f'<line x1="{ml}" y1="{y:.1f}" x2="{width - mr}" y2="{y:.1f}" stroke="{GRID}"/>')
        parts.append(f'<text x="{ml - 8}" y="{y + 4:.1f}" fill="{TXT2}" font-size="10" text-anchor="end" '
                     f'font-family={MONO!r}>{p}%</text>')
    d = " ".join(f'{"M" if i == 0 else "L"}{ml + v / hi * pw:.1f},{mt + ph - (i + 1) / n * ph:.1f}'
                 for i, v in enumerate(sorted_vals))
    parts.append(f'<path d="{d}" fill="none" stroke="{BLUE}" stroke-width="2"/>')
    for idx, p in enumerate(marks):
        v = pct(sorted_vals, p / 100)
        x = ml + v / hi * pw
        y = mt + ph - p / 100 * ph
        parts.append(f'<line x1="{x:.1f}" y1="{mt}" x2="{x:.1f}" y2="{mt + ph}" stroke="{YELLOW}" '
                     f'stroke-width="1" stroke-dasharray="3 3" opacity="0.5"/>')
        parts.append(f'<circle cx="{x:.1f}" cy="{y:.1f}" r="3" fill="{YELLOW}"/>')
        # stagger labels: p95/p99 often land within a few px of each other
        ly = y - 8 - (idx % 2) * 13
        ax = min(x + 6, width - mr - 74)
        parts.append(f'<text x="{ax:.1f}" y="{ly:.1f}" fill="{YELLOW}" font-size="10" '
                     f'font-family={MONO!r}>p{p} {fmt_ms(v)}</text>')
    parts.append("</svg>")
    return "".join(parts)


def histogram(width, height, vals, total):
    bins = [(0, 200), (200, 400), (400, 600), (600, 800), (800, 1000),
            (1000, 1250), (1250, 1500), (1500, 1750), (1750, 2000), (2000, 2500), (2500, 4000)]
    counts = defaultdict(int)
    for v in vals:
        for b in bins:
            if b[0] <= v < b[1]:
                counts[b] += 1
                break
        else:
            counts[bins[-1]] += 1
    ml, mr, mt, mb = 44, 12, 14, 30
    pw, ph = width - ml - mr, height - mt - mb
    hmax = max(counts.values()) or 1
    step = nice_step(hmax * 1.12, 4)
    hi = math.ceil(hmax * 1.12 / step) * step
    parts = [f'<svg viewBox="0 0 {width} {height}" width="100%" height="{height}">']
    for i in range(int(hi / step) + 1):
        y = mt + ph - step * i / hi * ph
        parts.append(f'<line x1="{ml}" y1="{y:.1f}" x2="{width - mr}" y2="{y:.1f}" stroke="{GRID}"/>')
        parts.append(f'<text x="{ml - 8}" y="{y + 4:.1f}" fill="{TXT2}" font-size="10" text-anchor="end" '
                     f'font-family={MONO!r}>{int(step * i)}</text>')
    bw = pw / len(bins)
    for i, b in enumerate(bins):
        c = counts[b]
        h = c / hi * ph
        x = ml + i * bw + 4
        col = GREEN if c / total > 0.04 else BLUE
        parts.append(f'<rect x="{x:.1f}" y="{mt + ph - h:.1f}" width="{bw - 8:.1f}" height="{h:.1f}" '
                     f'fill="{col}" opacity="0.8"/>')
        if c:
            parts.append(f'<text x="{x + (bw - 8) / 2:.1f}" y="{mt + ph - h - 4:.1f}" fill="{TXT2}" '
                         f'font-size="9" text-anchor="middle" font-family={MONO!r}>{c}</text>')
        parts.append(f'<text x="{x + (bw - 8) / 2:.1f}" y="{height - 8}" fill="{TXT2}" font-size="9" '
                     f'text-anchor="middle" font-family={MONO!r}>{b[0]}</text>')
    parts.append("</svg>")
    return "".join(parts)


def ep_bars(rows, width, height):
    """rows: [(label, avg, p95, p99, share, cls_color, cls_label)]"""
    label_w, col_w, right_pad = 330, 78, 14
    track_r = width - right_pad - col_w * 3 - 84
    track_w = track_r - label_w
    top = 40
    row_h = max(26, (height - 50) // max(1, len(rows)))
    vmax = max(r[3] for r in rows) or 1

    def X(v):
        return label_w + v / vmax * track_w

    svg = [f'<svg viewBox="0 0 {width} {height}" width="100%" height="{height}">']
    for i, (lbl, col) in enumerate([("avg", BLUE), ("p95", YELLOW), ("p99", RED)]):
        svg.append(f'<rect x="{label_w + i * 84}" y="12" width="8" height="8" rx="1" fill="{col}"/>')
        svg.append(f'<text x="{label_w + i * 84 + 13}" y="20" fill="{TXT2}" font-size="10">{lbl}</text>')
    svg.append(f'<text x="22" y="20" fill="{TXT2}" font-size="10">route · datastore class · share of traffic</text>')
    for i, h in enumerate(("avg", "p95", "p99")):
        x = track_r + 18 + col_w * i + col_w - 10
        svg.append(f'<text x="{x}" y="20" fill="{TXT2}" font-size="10" text-anchor="end" '
                   f'font-family={MONO!r}>{h}</text>')
    svg.append(f'<text x="{width - 12}" y="20" fill="{TXT2}" font-size="10" text-anchor="end" '
               f'font-family={MONO!r}>share</text>')
    for gi, (label, avg, p95, p99, share, cls_color, cls_label) in enumerate(rows):
        y0 = top + gi * row_h
        mid = y0 + row_h / 2
        svg.append(f'<circle cx="26" cy="{mid:.1f}" r="4" fill="{cls_color}"/>')
        svg.append(f'<text x="38" y="{mid + 4:.1f}" fill="{TXT}" font-size="11.5" font-family={MONO!r}>{label}</text>')
        svg.append(f'<text x="{label_w - 10}" y="{mid + 4:.1f}" fill="{TXT2}" font-size="10" '
                   f'text-anchor="end" font-family={MONO!r}>{cls_label}</text>')
        svg.append(f'<rect x="{label_w}" y="{mid - 5.5:.1f}" width="{max(2, X(avg) - label_w):.1f}" '
                   f'height="11" fill="{BLUE}" opacity="0.5"/>')
        for v, col in ((p95, YELLOW), (p99, RED)):
            svg.append(f'<line x1="{X(v):.1f}" y1="{mid - 8:.1f}" x2="{X(v):.1f}" y2="{mid + 8:.1f}" '
                       f'stroke="{col}" stroke-width="2"/>')
        for i, v in enumerate((avg, p95, p99)):
            x = track_r + 18 + col_w * i + col_w - 10
            svg.append(f'<text x="{x}" y="{mid + 4:.1f}" fill="{TXT}" font-size="11" '
                       f'text-anchor="end" font-family={MONO!r}>{fmt_ms(v)}</text>')
        svg.append(f'<text x="{width - 12}" y="{mid + 4:.1f}" fill="{TXT2}" font-size="10.5" '
                   f'text-anchor="end" font-family={MONO!r}>{share:.1f}%</text>')
        if gi:
            svg.append(f'<line x1="20" y1="{y0:.1f}" x2="{width - 12}" y2="{y0:.1f}" stroke="#1a2028"/>')
    svg.append("</svg>")
    return "".join(svg)


def kv_table(rows, headers):
    head = "".join(f"<th>{h}</th>" for h in headers)
    body = ""
    for r in rows:
        cells = "".join(f'<td class="{"k" if i == 0 else ""}">{c}</td>' for i, c in enumerate(r))
        body += f"<tr>{cells}</tr>"
    return f"<table><tr>{head}</tr>{body}</table>"


def panel(title, body, extra="", style=""):
    return (f'<div class="panel" style="{style}"><div class="ph"><span>{title}</span>'
            f'<span class="phx">{extra}</span></div><div class="pb">{body}</div></div>')


def stat(label, value, sub="", color=TXT):
    return (f'<div class="stat"><div class="sl">{label}</div>'
            f'<div class="sv" style="color:{color}">{value}</div><div class="ss">{sub}</div></div>')


# ------------------------------------------------------------------ load
pts = load_points(RESULTS)
summary = json.load(open(SUMMARY))
sm = summary["metrics"]

durations = sorted(v for _, v, _ in pts["http_req_duration"])
total = len(durations)
o = sm["http_req_duration"]["values"]
fail_rate = sm["http_req_failed"]["values"]["rate"] * 100
max_vus = int(sm["vus_max"]["values"]["max"])
dur_s = (pts["http_req_duration"][-1][0] - pts["http_req_duration"][0][0]) or 1
rps_avg = total / dur_s
reqs_b = [(t, v / 5) for t, v in bucketize(pts["http_reqs"], 5, "count")]
rps_peak = max((v for _, v in reqs_b), default=0)
vus_b = bucketize(pts["vus"], 5, "avg")
fail_b = [(t, v * 100) for t, v in bucketize(pts["http_req_failed"], 5, "avg")]
p95_line = bucketize(pts["http_req_duration"], 5, "p95")
dropped = int(sm.get("dropped_iterations", {}).get("values", {}).get("count", 0))
status_counts = defaultdict(int)
for _, _, tags in pts["http_reqs"]:
    status_counts[tags.get("status", "?")] += 1

E = [("GET /api/users/_current", "ep_api_users_current"),
     ("GET /api/contacts", "ep_api_contacts"),
     ("GET /api/location", "ep_api_location"),
     ("GET /api/location/nearby", "ep_api_location_nearby"),
     ("GET /api/sos/active", "ep_api_sos_active"),
     ("GET /api/sos/history", "ep_api_sos_history"),
     ("GET /api/sos/summary", "ep_api_sos_summary"),
     ("GET /api/sos/inbox", "ep_api_sos_inbox"),
     ("GET /api/emergency-contacts", "ep_api_emergency_contacts"),
     ("GET /api/push/vapid-public-key", "ep_api_push_vapid_public_key"),
     ("GET /api/push/subscriptions", "ep_api_push_subscriptions")]
ep = [(label, (sm.get(key) or {}).get("values")) for label, key in E]
ep = [(label, v) for label, v in ep if v]
ep.sort(key=lambda r: -r[1]["avg"])
total_ep = sum(v["count"] for _, v in ep) or 1

ep_rows = []
for label, v in ep:
    path = label.split(" ", 1)[1]
    cls, color = CLASS.get(path, ("?", TXT2))
    ep_rows.append((path, v["avg"], v["p(95)"], v["p(99)"], v["count"] / total_ep * 100, color, cls))

samples = []
for t, v, tags in pts["http_req_duration"]:
    path = (tags.get("name") or "").split(" ", 1)[-1]
    cls, _ = CLASS.get(path, ("mysql", BLUE))
    samples.append((t, v, cls))
class_meta = [("config", GREEN, "config only (no datastore)"),
              ("redis", YELLOW, "redis (geo / cache)"),
              ("mysql", BLUE, "mysql (+ redis session)")]

phase_rows = []
for name, key in [("waiting (TTFB)", "http_req_waiting"), ("receiving", "http_req_receiving"),
                  ("blocked", "http_req_blocked"), ("tls handshake", "http_req_tls_handshaking"),
                  ("connecting", "http_req_connecting"), ("sending", "http_req_sending")]:
    v = (sm.get(key) or {}).get("values")
    if not v:
        continue
    phase_rows.append((name, fmt_small(v["avg"]), fmt_small(v["p(95)"]), fmt_small(v["max"]),
                       f'{v["avg"] / (o["avg"] or 1) * 100:.1f}%'))

detail_rows = []
for label, v in ep:
    path = label.split(" ", 1)[1]
    cls, color = CLASS.get(path, ("?", TXT2))
    detail_rows.append((f'<span class="dot" style="background:{color}"></span>{path}', cls,
                        int(v["count"]), f'{v["count"] / total_ep * 100:.1f}%',
                        f'{v["count"] / dur_s:.2f}', f'{v["avg"]:.0f}', f'{v["med"]:.0f}',
                        f'{v["p(90)"]:.0f}', f'{v["p(95)"]:.0f}', f'{v["p(99)"]:.0f}', f'{v["max"]:.0f}'))

thr = []
for name, m in sm.items():
    for expr, res in (m.get("thresholds") or {}).items():
        thr.append((f"{name} {expr}", res.get("ok")))
thr_html = "".join(f'<div class="thr {"ok" if ok else "bad"}"><span>{"PASS" if ok else "FAIL"}</span>'
                   f'<code>{n}</code></div>' for n, ok in thr) or '<div class="thr ok"><span>PASS</span></div>'

mix = []
for label, v in ep:
    path = label.split(" ", 1)[1]
    cls, color = CLASS.get(path, ("?", TXT2))
    mix.append(f'<span class="dot" style="background:{color}"></span><code>{path}</code>'
               f'<b>{int(v["count"])}</b><i>{v["count"] / total_ep * 100:4.1f}%</i> {cls}')

start = datetime.fromtimestamp(min(t for t, _, _ in pts["http_req_duration"]))
end = datetime.fromtimestamp(max(t for t, _, _ in pts["http_req_duration"]))

CSS = f"""
* {{ box-sizing:border-box; }}
body {{ margin:0; background:{BG}; color:{TXT}; font-family:{SANS}; }}
.wrap {{ width:1560px; margin:0 auto; padding:20px 24px 26px; }}
.top {{ display:flex; align-items:flex-start; gap:26px; margin-bottom:14px; }}
h1 {{ font-size:19px; margin:0 0 7px; font-weight:600; letter-spacing:.2px; }}
h1 code {{ font-family:{MONO}; background:#151a20; border:1px solid {BORDER}; color:{GREEN};
           font-size:11px; padding:2px 6px; border-radius:3px; margin-left:8px; vertical-align:2px; }}
.run {{ font-family:{MONO}; font-size:11.5px; color:{TXT2}; line-height:1.8; }}
.run b {{ color:{TXT}; font-weight:500; }}
.thrs {{ margin-left:auto; width:430px; display:flex; flex-direction:column; gap:4px; }}
.thr {{ display:flex; align-items:center; gap:8px; font-family:{MONO}; font-size:10.5px;
        background:{PANEL}; border:1px solid {BORDER}; border-left-width:3px; padding:4px 8px; border-radius:3px; }}
.thr.ok {{ border-left-color:{GREEN}; }}
.thr.bad {{ border-left-color:{RED}; }}
.thr span {{ color:{GREEN}; font-weight:700; font-size:9.5px; letter-spacing:.5px; }}
.thr.bad span {{ color:{RED}; }}
.thr code {{ color:{TXT2}; }}
.row {{ display:flex; gap:12px; margin-bottom:12px; }}
.stat {{ flex:1; background:{PANEL}; border:1px solid {BORDER}; border-radius:4px; padding:10px 12px; }}
.sl {{ color:{TXT2}; font-size:10px; text-transform:uppercase; letter-spacing:.8px; }}
.sv {{ font-size:23px; font-weight:600; margin:4px 0 0; font-family:{MONO}; letter-spacing:-.5px; }}
.ss {{ color:{TXT2}; font-size:10.5px; font-family:{MONO}; margin-top:2px; }}
.panel {{ background:{PANEL}; border:1px solid {BORDER}; border-radius:4px; flex:1; padding-bottom:6px; }}
.ph {{ display:flex; justify-content:space-between; align-items:baseline; padding:8px 12px 5px;
       font-size:12px; font-weight:600; color:{TXT}; border-bottom:1px solid {GRID}; margin-bottom:4px; }}
.phx {{ color:{TXT2}; font-weight:400; font-size:10.5px; font-family:{MONO}; }}
.pb {{ padding:0 6px; }}
.legend {{ display:flex; gap:14px; padding:0 10px 2px; font-size:10px; color:{TXT2}; font-family:{MONO}; }}
.legend i {{ display:inline-block; width:8px; height:8px; border-radius:2px; margin-right:4px; }}
.legend i.dash {{ height:2px; width:12px; vertical-align:2px; }}
.legend b {{ color:{TXT}; }}
table {{ width:100%; border-collapse:collapse; font-size:11px; font-family:{MONO}; }}
th {{ text-align:right; color:{TXT2}; font-weight:500; font-size:9.5px; padding:5px 7px;
      border-bottom:1px solid {BORDER}; text-transform:uppercase; letter-spacing:.6px; }}
td {{ text-align:right; padding:5px 7px; border-bottom:1px solid #171c23; }}
td.k {{ text-align:left; color:{TXT}; }}
.dot {{ display:inline-block; width:7px; height:7px; border-radius:50%; margin-right:7px; vertical-align:1px; }}
.mix {{ display:grid; grid-template-columns:1fr; gap:1px 20px; font-family:{MONO}; font-size:10.5px;
        color:{TXT2}; padding:4px 10px 2px; line-height:1.55; }}
.mix b {{ color:{TXT}; display:inline-block; min-width:32px; text-align:right; margin-right:8px; }}
.mix code {{ color:{TXT}; }}
.note {{ color:{TXT2}; font-size:10.5px; line-height:1.65; font-family:{MONO}; margin-top:10px;
         border-top:1px solid {BORDER}; padding-top:9px; }}
"""

HTML = f"""<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>SOS-Allert API — k6 load report</title>
<style>{CSS}</style></head><body><div class="wrap">

<div class="top">
  <div>
    <h1>SOS-Allert API — load test report <code>read-only · prod</code></h1>
    <div class="run">
      <b>target&nbsp;&nbsp;</b> https://api.allert.my.id → cloudflare edge → cloudflared → go/fiber :3000<br>
      <b>window&nbsp;&nbsp;</b> {start.strftime('%Y-%m-%d %H:%M:%S')} → {end.strftime('%H:%M:%S')} ({dur_s:.0f}s) · k6 v2.4.0 linux/amd64<br>
      <b>workload</b> ramping-arrival-rate 2→10 req/s · preAlloc 10 / max 30 VU · 11 weighted GET routes<br>
      <b>auth&nbsp;&nbsp;&nbsp;&nbsp;</b> one session token reused by all VUs (auth limiter 5/min per IP)
    </div>
  </div>
  <div class="thrs">{thr_html}</div>
</div>

<div class="row">
  {stat("requests", f"{total:,}", f"200:{status_counts.get('200', 0)} · dropped {dropped}")}
  {stat("throughput", f"{rps_avg:.2f}/s", f"peak {rps_peak:.1f}/s · max VU {max_vus}")}
  {stat("p50", fmt_ms(pct(durations, .5)), f"min {fmt_ms(durations[0])}")}
  {stat("p95", fmt_ms(pct(durations, .95)), "slo < 2500ms", YELLOW)}
  {stat("p99", fmt_ms(pct(durations, .99)), f"p99.9 {fmt_ms(pct(durations, .999))}", RED)}
  {stat("failed", f"{fail_rate:.2f}%", "slo < 2%", GREEN if fail_rate < 2 else RED)}
</div>

{panel("latency per request · scatter by datastore class", scatter_chart(1498, 270, samples, class_meta, p95_line),
       f"n={total} · max {fmt_ms(max(durations))}")}

<div class="row">
  {panel("latency CDF", cdf_chart(735, 250, durations),
         f"p50 {fmt_ms(pct(durations, .5))} / p99 {fmt_ms(pct(durations, .99))}")}
  {panel("latency histogram", histogram(735, 250, durations, total), "buckets 200 / 250ms")}
</div>

<div class="row">
  {panel("per-route latency · bar = avg · ticks = p95/p99", ep_bars(ep_rows, 1498, 52 + 27 * len(ep_rows)),
         "sorted by avg · scale = max p99")}
</div>

<div class="row">
  {panel("phase timings (k6 http_req_*)", kv_table(phase_rows, ["phase", "avg", "p95", "max", "% of total"]),
         "waiting = TTFB incl. tunnel hop")}
  {panel("load · req/s vs VU", line_chart(735, 232,
      [("req/s", BLUE, reqs_b, True), ("VU", PURPLE, vus_b, False)],
      yfmt=lambda v: f"{v:.0f}", legend_values=[f"avg {rps_avg:.2f}/s", f"max {max_vus}"]),
      f"error rate {fail_rate:.2f}%")}
</div>

{panel("per-route detail", kv_table(detail_rows,
    ["route", "class", "reqs", "share", "rps", "avg", "p50", "p90", "p95", "p99", "max"]),
    "values in ms")}

<div class="row">
  {panel("traffic mix · weight → observed", '<div class="mix">' + "".join(f"<div>{m}</div>" for m in mix) + "</div>",
         "weighted random pick")}
  {panel("error rate · 5s bucket", line_chart(735, 190, [("failed %", RED, fail_b, True)],
      yfmt=lambda v: f"{v:.0f}%", legend_values=[f"{fail_rate:.2f}%"]), "flat 0 = clean run")}
</div>

<div class="note">
  Method: k6 ramping-arrival-rate, weighted random route pick, one login in setup() reused by every VU.
  Read-only GETs only — no SOS trigger, no /api/sos/classify (spends STT+LLM), no writes.<br>
  Latency includes the runner → Cloudflare hop; the phase split shows connection setup ≈ 0, so the
  budget sits in TTFB (go handler + Aiven MySQL / Redis Cloud round-trip).<br>
  Artifacts: k6-sos-api.js · results.json · summary.json · report.html (native k6 UI)
</div>
</div></body></html>
"""

open(OUT, "w").write(HTML)
print(f"wrote {OUT} ({len(HTML)} bytes) · {len(ep_rows)} routes · {total} reqs")
