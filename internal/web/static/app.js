'use strict';
const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const api = (p, o) => fetch(p, o).then(r => {
  if (r.status === 401) { location.href = '/login'; return new Promise(() => {}); }
  return r.ok ? (r.status === 204 ? null : r.json()) : Promise.reject(r);
});

let SUMMARY = null, LIBRARY = null;
const css = k => getComputedStyle(document.documentElement).getPropertyValue(k).trim();

// ---- formatting ----
function fmtDuration(sec) {
  if (!sec) return '0m';
  const h = Math.floor(sec / 3600), m = Math.round((sec % 3600) / 60);
  if (h >= 100) return `${h}h`;
  if (h) return `${h}h ${m}m`;
  return `${m}m`;
}
function fmtNum(n) { return n >= 1000 ? (n / 1000).toFixed(1).replace(/\.0$/, '') + 'k' : '' + n; }
function timeAgo(ts) {
  if (!ts) return '';
  const d = Date.now() / 1000 - ts;
  if (d < 3600) return Math.round(d / 60) + 'm ago';
  if (d < 86400) return Math.round(d / 3600) + 'h ago';
  if (d < 86400 * 30) return Math.round(d / 86400) + 'd ago';
  return new Date(ts * 1000).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

// ---- tabs ----
$$('.tab').forEach(t => t.onclick = () => {
  $$('.tab').forEach(x => x.classList.remove('active'));
  $$('.tabpane').forEach(x => x.classList.remove('active'));
  t.classList.add('active');
  $('#tab-' + t.dataset.tab).classList.add('active');
  if (t.dataset.tab === 'books') loadLibraryGrid();
  if (t.dataset.tab === 'curate') loadCollections();
  if (t.dataset.tab === 'journal') renderJournal();
});

// ---- theme ----
// Inline SVGs: font glyphs (☀︎/☾) render inconsistently across platforms.
const THEME_ICONS = {
  light: `<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
    <circle cx="12" cy="12" r="4.2"/>
    <path d="M12 2.5v2.4M12 19.1v2.4M2.5 12h2.4M19.1 12h2.4M5.3 5.3l1.7 1.7M17 17l1.7 1.7M18.7 5.3L17 7M7 17l-1.7 1.7"/>
  </svg>`,
  dark: `<svg viewBox="0 0 24 24" width="17" height="17" fill="currentColor">
    <path d="M20.4 14.3A8.5 8.5 0 0 1 9.7 3.6a.6.6 0 0 0-.8-.7 9.3 9.3 0 1 0 12.2 12.2.6.6 0 0 0-.7-.8z"/>
  </svg>`,
  auto: `<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2">
    <circle cx="12" cy="12" r="8.2"/>
    <path d="M12 3.8a8.2 8.2 0 0 1 0 16.4z" fill="currentColor" stroke="none"/>
  </svg>`,
};
function reflectTheme() {
  const cur = document.documentElement.dataset.theme;
  const btn = $('#theme-toggle');
  btn.innerHTML = THEME_ICONS[cur] || THEME_ICONS.auto;
  btn.title = `Theme: ${cur} (click to switch)`;
}
$('#theme-toggle').onclick = () => {
  const order = ['auto', 'light', 'dark'];
  const cur = document.documentElement.dataset.theme;
  const next = order[(order.indexOf(cur) + 1) % order.length];
  document.documentElement.dataset.theme = next;
  localStorage.theme = next;
  reflectTheme();
  if (SUMMARY) {
    renderCharts();
    if ($('#tab-journal').classList.contains('active')) renderJournalCharts();
    renderOpenPanels();
  }
};
if (localStorage.theme) document.documentElement.dataset.theme = localStorage.theme;
reflectTheme();

// ---- canvas helpers (retina) ----
function prep(canvas) {
  const dpr = window.devicePixelRatio || 1;
  const w = canvas.clientWidth, h = canvas.clientHeight;
  canvas.width = w * dpr; canvas.style.height = h + 'px';
  canvas.height = h * dpr;
  const ctx = canvas.getContext('2d');
  ctx.scale(dpr, dpr);
  return { ctx, w, h };
}

function barChart(canvas, labels, values, opts = {}) {
  const { ctx, w, h } = prep(canvas);
  ctx.clearRect(0, 0, w, h);
  const pad = { l: 6, r: 6, t: 10, b: 18 };
  const max = Math.max(1, ...values);
  const n = values.length;
  const bw = (w - pad.l - pad.r) / n;
  const accent = css('--accent'), muted = css('--muted');
  ctx.fillStyle = muted; ctx.font = '10px system-ui'; ctx.textAlign = 'center';
  const bars = [];
  // Cap bar width so sparse data (a single reading day) doesn't render one
  // slab across the whole card; bars stay centered in their slot.
  const barW = Math.min(bw * .7, 42);
  for (let i = 0; i < n; i++) {
    const v = values[i];
    const bh = (h - pad.t - pad.b) * (v / max);
    const x = pad.l + i * bw + (bw - barW) / 2, y = h - pad.b - bh;
    bars.push({ x, y, w: barW, h: bh, i, sx: pad.l + i * bw, sw: bw });
    ctx.fillStyle = accent;
    const r = Math.min(3, barW * .4);
    roundRect(ctx, x, y, barW, bh, r);
    ctx.fill();
    if (opts.labelEvery && i % opts.labelEvery === 0) {
      ctx.fillStyle = muted;
      ctx.fillText(labels[i], x + bw / 2, h - 5);
    }
  }
  if (opts.tooltip) attachBarHover(canvas, bars, labels, values, opts.tooltip);
}
function roundRect(ctx, x, y, w, h, r) {
  if (h < r) r = h < 0 ? 0 : h;
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, 0);
  ctx.arcTo(x, y + h, x, y, 0);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

function barTip() {
  let t = $('#bar-tip');
  if (!t) { t = document.createElement('div'); t.id = 'bar-tip'; t.className = 'bar-tip'; document.body.appendChild(t); }
  return t;
}
function hideTip() { barTip().classList.remove('show'); }
function showTip(html, cx, cy) {
  const tip = barTip();
  tip.innerHTML = html;
  tip.classList.add('show');
  const tw = tip.offsetWidth, th = tip.offsetHeight;
  let tx = cx + 12, ty = cy - th - 8;
  if (tx + tw > window.innerWidth - 6) tx = cx - tw - 12;
  if (tx < 6) tx = 6;
  if (ty < 6) ty = cy + 12;
  tip.style.left = tx + 'px'; tip.style.top = ty + 'px';
}
// Taps show the tip; anything else (tap elsewhere, scroll) dismisses it. Installed once.
let tipDismissBound = false;
function bindTipDismiss() {
  if (tipDismissBound) return;
  tipDismissBound = true;
  document.addEventListener('click', e => { if (!e.target.closest('.cell, canvas')) hideTip(); }, true);
  document.addEventListener('scroll', hideTip, true);
}

function attachBarHover(canvas, bars, labels, values, fmt) {
  const at = (cx, cy) => {
    const x = cx - canvas.getBoundingClientRect().left;
    // Hit-test the whole slot, not just the (possibly narrow) bar.
    const b = bars.find(bar => x >= bar.sx && x <= bar.sx + bar.sw);
    if (!b) { hideTip(); return; }
    showTip(fmt ? fmt(b.i, labels[b.i], values[b.i]) : `${labels[b.i]}: ${values[b.i]}`, cx, cy);
  };
  canvas.onmousemove = e => at(e.clientX, e.clientY);
  canvas.onclick = e => at(e.clientX, e.clientY);
  canvas.onmouseleave = hideTip;
  bindTipDismiss();
}

// fmt(cell) => tip html, defaults to the heatmap's day/seconds/pages format.
function attachCellHover(el, fmt) {
  const at = (target, cx, cy) => {
    const c = target.closest && target.closest('.cell');
    if (!c) { hideTip(); return; }
    const html = fmt ? fmt(c) : (c.dataset.day ? `${c.dataset.day}: ${fmtDuration(+c.dataset.sec)}, ${fmtNum(+c.dataset.pages)} pages` : null);
    if (!html) { hideTip(); return; }
    showTip(html, cx, cy);
  };
  el.onmousemove = e => at(e.target, e.clientX, e.clientY);
  el.onclick = e => at(e.target, e.clientX, e.clientY);
  el.onmouseleave = hideTip;
  bindTipDismiss();
}

// ---- dashboard ----
let rangeDays = 30;
async function loadDash() {
  try { SUMMARY = await api('/api/summary'); }
  catch (e) { return; }
  // books_tracked counts included books only; keep the dashboard up whenever
  // anything was ingested so excluded books stay reachable via Manage.
  if (!SUMMARY || !(SUMMARY.books || []).length) { $('#empty').classList.remove('hidden'); return; }
  $('#empty').classList.add('hidden');
  $('#dash-content').classList.remove('hidden');

  $('#s-time').textContent = fmtDuration(SUMMARY.total_seconds);
  $('#s-streak').textContent = SUMMARY.current_streak;
  $('#s-books').textContent = `${SUMMARY.books_tracked} · ${SUMMARY.books_finished}`;
  $('#s-pages').textContent = fmtNum(SUMMARY.total_pages);
  $('#s-speed').textContent = SUMMARY.pages_per_hour ? SUMMARY.pages_per_hour.toFixed(0) : '–';
  $('#s-week').textContent = fmtDuration(SUMMARY.this_week_seconds);

  renderHeatmap();
  renderPunchcard();
  renderBooks();
  renderSessions();
  renderCharts();
}

function renderCharts() {
  const daily = (SUMMARY.daily || []).slice(-rangeDays);
  const labels = daily.map(d => d.day.slice(5));
  const mins = daily.map(d => Math.round(d.seconds / 60));
  barChart($('#chart-daily'), labels, mins, {
    labelEvery: Math.ceil(daily.length / 8),
    tooltip: (i) => `${daily[i].day}: ${fmtDuration(daily[i].seconds)}, ${fmtNum(daily[i].pages)} pages`,
  });
}

function ampmLabel(h) { return h === 0 ? '12am' : h < 12 ? `${h}am` : h === 12 ? '12pm' : `${h - 12}pm`; }

function renderPunchcard() {
  const el = $('#punchcard'); el.innerHTML = '';
  const wdNames = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
  const data = SUMMARY.punchcard || [];
  // Cells aggregate a year, so scale levels to the busiest cell — the daily
  // heatLevel() thresholds would saturate every regular slot at max.
  const max = Math.max(1, ...data.map(row => Math.max(...row)));
  for (let d = 0; d < 7; d++) {
    const wd = document.createElement('div'); wd.className = 'wd'; wd.textContent = wdNames[d];
    el.appendChild(wd);
    for (let hr = 0; hr < 24; hr++) {
      const sec = (data[d] && data[d][hr]) || 0;
      const c = document.createElement('div'); c.className = 'cell';
      c.dataset.l = sec ? Math.max(1, Math.ceil(4 * sec / max)) : 0;
      c.dataset.wd = wdNames[d]; c.dataset.hr = hr; c.dataset.sec = sec;
      el.appendChild(c);
    }
  }
  attachCellHover(el, c => `${c.dataset.wd} ${ampmLabel(+c.dataset.hr)}: ${fmtDuration(+c.dataset.sec)}`);

  const hours = $('#punchcard-hours'); hours.innerHTML = '';
  [0, 6, 12, 18].forEach(h => { const s = document.createElement('span'); s.textContent = ampmLabel(h); hours.appendChild(s); });
}

function heatLevel(sec) {
  if (!sec) return 0;
  const m = sec / 60;
  if (m < 10) return 1;
  if (m < 30) return 2;
  if (m < 60) return 3;
  return 4;
}
function renderHeatmap() {
  const el = $('#heatmap'); el.innerHTML = '';
  const data = SUMMARY.heatmap || [];
  // pad front so weeks align to columns (first cell at its weekday row)
  if (data.length) {
    const firstDow = new Date(data[0].day + 'T00:00:00').getDay();
    for (let i = 0; i < firstDow; i++) el.appendChild(emptyCell());
  }
  let total = 0;
  for (const d of data) {
    total += d.seconds;
    const c = document.createElement('div');
    c.className = 'cell';
    c.dataset.l = heatLevel(d.seconds);
    c.dataset.day = d.day;
    c.dataset.sec = d.seconds;
    c.dataset.pages = d.pages;
    el.appendChild(c);
  }
  attachCellHover(el);
  el.scrollLeft = el.scrollWidth; // land on the most recent day, scroll back for history
  $('#heat-total').textContent = `${fmtDuration(total)} over the last year`;
}
function emptyCell() { const c = document.createElement('div'); c.className = 'cell'; c.style.visibility = 'hidden'; return c; }

function coverEl(b, cls) {
  if (b.calibre_id) {
    const img = document.createElement('img');
    img.className = cls; img.loading = 'lazy';
    img.src = `/cover/${b.calibre_id}`;
    img.onerror = () => { img.replaceWith(placeholder(b, cls)); };
    return img;
  }
  return placeholder(b, cls);
}
function placeholder(b, cls) {
  const d = document.createElement('div');
  d.className = cls + ' placeholder';
  d.textContent = '📕';
  return d;
}

// A book counts as started once it clears 2% AND 2 minutes (percent alone
// lies: jumping to a late page scores high with seconds of reading). Bare
// opens stay out of the reading list and timeline but still count in stats.
function started(b) { return b.finished || (b.percent >= 2 && b.seconds >= 120); }

function renderBooks() {
  const list = $('#book-list'); list.innerHTML = '';
  const books = SUMMARY.books.filter(b => !b.excluded && started(b))
    .sort((a, b) => b.last_open - a.last_open).slice(0, 12);
  for (const b of books) {
    const row = document.createElement('div'); row.className = 'book-row expandable';
    row.appendChild(coverEl(b, 'cover'));
    const meta = document.createElement('div'); meta.className = 'meta';
    const fin = b.finished ? ' <span class="badge">DONE</span>' : '';
    meta.innerHTML = `<div class="title">${esc(b.title || 'Untitled')}${fin}</div>
      <div class="sub">${esc(b.authors || '')}</div>
      <div class="progress"><i style="width:${Math.min(100, b.percent).toFixed(0)}%"></i></div>`;
    const nums = document.createElement('div'); nums.className = 'nums';
    nums.title = 'Total time spent in this book';
    let forecast = '';
    if (b.forecast_seconds > 0 && !b.finished) {
      forecast = ` · <span title="Estimated reading time to finish, at your pace in this book">~${fmtDuration(b.forecast_seconds)} left</span>`;
    }
    nums.innerHTML = `<b>${fmtDuration(b.seconds)}</b> read<br>${b.percent.toFixed(0)}% · ${timeAgo(b.last_open)}${forecast}`;
    const caret = document.createElement('span'); caret.className = 'caret'; caret.textContent = '▸';
    row.append(meta, nums, caret);
    const panel = document.createElement('div'); panel.className = 'progress-panel hidden';
    row.onclick = () => toggleProgressPanel(b, row, panel);
    list.append(row, panel);
  }
}

// ---- book progress expand ----
const progressCache = new Map(); // md5 -> {pages, points} | null (unavailable)
function toggleProgressPanel(b, row, panel) {
  const opening = panel.classList.contains('hidden');
  panel.classList.toggle('hidden');
  row.classList.toggle('open', opening);
  if (opening) drawProgressPanel(b, panel);
}
async function drawProgressPanel(b, panel) {
  let data = progressCache.get(b.md5);
  if (data === undefined) {
    panel.innerHTML = '<p class="muted">Loading…</p>';
    try {
      const r = await api(`/api/books/${b.md5}/progress`);
      data = { pages: r.pages, points: r.points || [] };
    } catch (e) { data = { pages: 0, points: [] }; }
    progressCache.set(b.md5, data);
  }
  panel.dataset.md5 = b.md5;
  if (data.points.length < 2) { panel.innerHTML = '<p class="muted">Not enough data yet.</p>'; return; }
  panel.innerHTML = `<canvas class="progress-canvas"></canvas>
    <div class="progress-labels"><span class="first muted"></span><span class="last muted"></span></div>`;
  drawProgressChart(panel, data);
}
function renderOpenPanels() {
  $$('.progress-panel:not(.hidden)').forEach(panel => {
    const data = progressCache.get(panel.dataset.md5);
    if (data && data.points.length >= 2) drawProgressChart(panel, data);
  });
}
function fmtShortDate(dayStr) { return new Date(dayStr + 'T00:00:00').toLocaleDateString(undefined, { month: 'short', day: 'numeric' }); }
function drawProgressChart(panel, data) {
  const canvas = $('.progress-canvas', panel);
  if (!canvas) return;
  const { ctx, w, h } = prep(canvas);
  ctx.clearRect(0, 0, w, h);
  const pad = { l: 4, r: 4, t: 10, b: 6 };
  const points = data.points;
  const maxPage = data.pages > 0 ? data.pages : Math.max(1, ...points.map(p => p.page));
  const t0 = new Date(points[0].day + 'T00:00:00').getTime();
  // End the axis at the last activity, not today — otherwise a long-idle or
  // finished book squeezes into the left edge with dead space after it.
  const tEnd = new Date(points[points.length - 1].day + 'T00:00:00').getTime();
  const span = Math.max(86400000, tEnd - t0);
  const accent = css('--accent'), muted = css('--muted'), line = css('--line');
  const xy = p => {
    const t = new Date(p.day + 'T00:00:00').getTime();
    const x = pad.l + (w - pad.l - pad.r) * (t - t0) / span;
    const y = pad.t + (h - pad.t - pad.b) * (1 - Math.min(1, p.page / maxPage));
    return [x, y];
  };
  ctx.strokeStyle = line; ctx.lineWidth = 1;
  ctx.beginPath(); ctx.moveTo(pad.l, h - pad.b); ctx.lineTo(w - pad.r, h - pad.b); ctx.stroke();

  ctx.beginPath();
  points.forEach((p, i) => { const [x, y] = xy(p); i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); });
  ctx.strokeStyle = accent; ctx.lineWidth = 2; ctx.lineJoin = 'round'; ctx.stroke();
  const [lx] = xy(points[points.length - 1]), [fx] = xy(points[0]);
  ctx.lineTo(lx, h - pad.b); ctx.lineTo(fx, h - pad.b); ctx.closePath();
  ctx.globalAlpha = .12; ctx.fillStyle = accent; ctx.fill(); ctx.globalAlpha = 1;

  ctx.fillStyle = muted; ctx.font = '10px system-ui'; ctx.textAlign = 'right';
  ctx.fillText(fmtNum(maxPage), w - pad.r, pad.t + 8);

  const firstLbl = $('.progress-labels .first', panel), lastLbl = $('.progress-labels .last', panel);
  if (firstLbl) firstLbl.textContent = fmtShortDate(points[0].day);
  if (lastLbl) lastLbl.textContent = fmtShortDate(points[points.length - 1].day);

  attachProgressHover(canvas, points, xy, maxPage);
}
function attachProgressHover(canvas, points, xy, maxPage) {
  const at = (cx, cy) => {
    const x = cx - canvas.getBoundingClientRect().left;
    let best = 0, bestDist = Infinity;
    points.forEach((p, i) => { const [px] = xy(p); const d = Math.abs(px - x); if (d < bestDist) { bestDist = d; best = i; } });
    const p = points[best];
    const pct = maxPage ? Math.round(100 * p.page / maxPage) : 0;
    showTip(`${fmtShortDate(p.day)}: page ${fmtNum(p.page)} (${pct}%) · ${fmtDuration(p.seconds)} read that day`, cx, cy);
  };
  canvas.onmousemove = e => at(e.clientX, e.clientY);
  canvas.onclick = e => at(e.clientX, e.clientY);
  canvas.onmouseleave = hideTip;
  bindTipDismiss();
}

function renderSessions() {
  const list = $('#session-list'); list.innerHTML = '';
  for (const s of (SUMMARY.recent_sessions || []).slice(0, 12)) {
    const row = document.createElement('div'); row.className = 'session-row';
    row.innerHTML = `<span>${esc(s.title || 'Reading')} <span class="muted">· ${s.pages}p</span></span>
      <span><b>${fmtDuration(s.seconds)}</b> <span class="when">· ${timeAgo(s.started)}</span></span>`;
    list.appendChild(row);
  }
}

$$('#range-seg button').forEach(b => b.onclick = () => {
  $$('#range-seg button').forEach(x => x.classList.remove('active'));
  b.classList.add('active'); rangeDays = +b.dataset.days; renderCharts();
});

// ---- journal ----
let journalYear = null;
function fmtMonthShort(ym) {
  const [y, m] = ym.split('-').map(Number);
  const mon = new Date(y, m - 1, 1).toLocaleDateString(undefined, { month: 'short' });
  return m === 1 ? `${mon} ${('' + y).slice(2)}` : mon;
}
function fmtMonthFull(ym) {
  const [y, m] = ym.split('-').map(Number);
  return new Date(y, m - 1, 1).toLocaleDateString(undefined, { month: 'short', year: 'numeric' });
}
function journalYears() {
  const years = new Set();
  for (const m of (SUMMARY.monthly || [])) {
    if (m.seconds > 0 || m.pages > 0 || m.books_finished > 0) years.add(+m.month.slice(0, 4));
  }
  for (const b of SUMMARY.books) {
    if (!b.excluded && b.finished_at) years.add(new Date(b.finished_at * 1000).getFullYear());
  }
  return [...years].sort((a, b) => b - a);
}
async function renderJournal() {
  if (!SUMMARY) { await loadDash(); }
  if (!SUMMARY || !(SUMMARY.books || []).length) return;
  renderYearSeg();
  renderJournalStats();
  renderReadingLog();
  renderJournalCharts();
}
function renderJournalCharts() {
  renderMonthlyChart();
  renderGantt();
}
function renderYearSeg() {
  const years = journalYears();
  const curYear = new Date().getFullYear();
  if (journalYear === null) journalYear = years.includes(curYear) ? curYear : 'all';
  const seg = $('#year-seg'); seg.innerHTML = '';
  const mk = (val, label) => {
    const btn = document.createElement('button');
    btn.textContent = label; btn.classList.toggle('active', journalYear === val);
    btn.onclick = () => { journalYear = val; renderYearSeg(); renderJournalStats(); renderReadingLog(); };
    return btn;
  };
  years.forEach(y => seg.appendChild(mk(y, '' + y)));
  seg.appendChild(mk('all', 'All'));
}
function renderJournalStats() {
  const monthly = SUMMARY.monthly || [];
  const inYear = m => journalYear === 'all' || +m.month.slice(0, 4) === journalYear;
  let seconds = 0, pages = 0;
  for (const m of monthly) if (inYear(m)) { seconds += m.seconds; pages += m.pages; }
  const booksFinished = SUMMARY.books.filter(b => !b.excluded && b.finished_at &&
    (journalYear === 'all' || new Date(b.finished_at * 1000).getFullYear() === journalYear)).length;
  $('#j-books').textContent = booksFinished;
  $('#j-hours').textContent = fmtDuration(seconds);
  $('#j-pages').textContent = fmtNum(pages);
}
function renderMonthlyChart() {
  const all = SUMMARY.monthly || [];
  const monthly = all.length > 36 ? all.slice(-36) : all;
  const mins = monthly.map(m => Math.round(m.seconds / 60));
  const labels = monthly.map(m => fmtMonthShort(m.month));
  barChart($('#chart-monthly'), labels, mins, {
    labelEvery: Math.max(1, Math.ceil(monthly.length / 8)),
    tooltip: (i) => `${fmtMonthFull(monthly[i].month)}: ${fmtDuration(monthly[i].seconds)}, ${fmtNum(monthly[i].pages)} pages`,
  });
}
function renderReadingLog() {
  const el = $('#reading-log'); el.innerHTML = '';
  const books = SUMMARY.books.filter(b => !b.excluded && b.finished_at > 0 &&
    (journalYear === 'all' || new Date(b.finished_at * 1000).getFullYear() === journalYear))
    .sort((a, b) => b.finished_at - a.finished_at);
  if (!books.length) {
    el.innerHTML = `<p class="muted">No books finished ${journalYear === 'all' ? '' : 'in ' + journalYear + ' '}yet.</p>`;
    return;
  }
  let curMonth = null;
  for (const b of books) {
    const d = new Date(b.finished_at * 1000);
    const monthKey = d.toLocaleDateString(undefined, { month: 'long', year: 'numeric' });
    if (monthKey !== curMonth) {
      curMonth = monthKey;
      const h = document.createElement('div'); h.className = 'log-month muted'; h.textContent = monthKey;
      el.appendChild(h);
    }
    const row = document.createElement('div'); row.className = 'book-row log-row';
    row.appendChild(coverEl(b, 'cover'));
    const meta = document.createElement('div'); meta.className = 'meta';
    meta.innerHTML = `<div class="title">${esc(b.title || 'Untitled')}</div><div class="sub">${esc(b.authors || '')}</div>`;
    const nums = document.createElement('div'); nums.className = 'nums';
    const days = Math.max(1, Math.ceil((b.finished_at - b.first_read) / 86400));
    nums.innerHTML = `<b>${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}</b><br>took ${days} day${days === 1 ? '' : 's'} · ${fmtDuration(b.seconds)}`;
    row.append(meta, nums);
    el.appendChild(row);
  }
}
function ellipsize(ctx, str, maxW) {
  if (maxW <= 0) return '';
  if (ctx.measureText(str).width <= maxW) return str;
  let s = str;
  while (s.length > 1 && ctx.measureText(s + '…').width > maxW) s = s.slice(0, -1);
  return s.length < str.length ? s + '…' : s;
}
function renderGantt() {
  const now = Date.now();
  const windowStart = new Date(); windowStart.setMonth(windowStart.getMonth() - 12);
  const wStart = windowStart.getTime(), wEnd = now;
  let rows = (SUMMARY.books || []).filter(b => !b.excluded && b.first_read > 0 && started(b)).map(b => ({
    b, start: b.first_read * 1000, end: (b.finished_at || b.last_open || b.first_read) * 1000,
  })).filter(r => r.end >= wStart && r.start <= wEnd);
  rows.sort((a, b) => a.start - b.start);
  let capped = false;
  if (rows.length > 20) {
    capped = true;
    rows = rows.slice().sort((a, b) => b.b.seconds - a.b.seconds).slice(0, 20).sort((a, b) => a.start - b.start);
  }
  $('#gantt-note').textContent = capped ? 'top 20 by time' : '';

  const canvas = $('#chart-gantt');
  const rowH = 24, marginT = 10, marginB = 22;
  canvas.style.height = Math.max(rowH, rows.length * rowH + marginT + marginB) + 'px';
  const { ctx, w, h } = prep(canvas);
  ctx.clearRect(0, 0, w, h);
  const muted = css('--muted'), text = css('--text'), line = css('--line');
  if (!rows.length) {
    ctx.fillStyle = muted; ctx.font = '12px system-ui'; ctx.textAlign = 'center';
    ctx.fillText('No reading in the last 12 months', w / 2, h / 2);
    return;
  }
  const accent = css('--accent'), accentSoft = css('--accent-soft');
  const trackX = 4, trackW = w - 8;
  const xOf = t => trackX + (t - wStart) / (wEnd - wStart) * trackW;

  const months = [];
  for (let d = new Date(windowStart.getFullYear(), windowStart.getMonth(), 1); d.getTime() <= wEnd; d.setMonth(d.getMonth() + 1)) {
    months.push(new Date(d));
  }
  const everyN = w < 380 ? 3 : w < 600 ? 2 : 1;
  ctx.strokeStyle = line; ctx.lineWidth = 1; ctx.font = '10px system-ui'; ctx.fillStyle = muted; ctx.textAlign = 'center';
  months.forEach((m, i) => {
    const x = xOf(m.getTime());
    ctx.beginPath(); ctx.moveTo(x, marginT); ctx.lineTo(x, h - marginB); ctx.stroke();
    if (i % everyN === 0) ctx.fillText(m.toLocaleDateString(undefined, { month: 'narrow' }), x, h - marginB + 12);
  });

  const barsInfo = [];
  rows.forEach((r, i) => {
    const y = marginT + i * rowH + rowH / 2 - 4;
    const x0 = xOf(Math.max(r.start, wStart)), x1 = xOf(Math.min(r.end, wEnd));
    const bw = Math.max(2, x1 - x0);
    const finished = !!r.b.finished_at;
    roundRect(ctx, x0, y, bw, 8, 4);
    if (finished) { ctx.fillStyle = accent; ctx.fill(); }
    else { ctx.fillStyle = accentSoft; ctx.fill(); ctx.lineWidth = 1.5; ctx.strokeStyle = accent; roundRect(ctx, x0, y, bw, 8, 4); ctx.stroke(); }

    ctx.font = '11px system-ui';
    const title = r.b.title || 'Untitled';
    const maxLeftW = x0 - trackX - 6;
    if (maxLeftW > 30) {
      ctx.textAlign = 'right'; ctx.fillStyle = text;
      ctx.fillText(ellipsize(ctx, title, maxLeftW), x0 - 5, y + 7);
    } else {
      ctx.textAlign = 'left'; ctx.fillStyle = muted;
      const maxRightW = trackX + trackW - (x0 + 5);
      ctx.fillText(ellipsize(ctx, title, Math.max(20, maxRightW)), x0 + 5, y + 7);
    }
    barsInfo.push({ y: marginT + i * rowH, h: rowH, r });
  });
  attachGanttHover(canvas, barsInfo);
}
function attachGanttHover(canvas, bars) {
  const at = (cx, cy) => {
    const rect = canvas.getBoundingClientRect();
    const y = cy - rect.top;
    const bar = bars.find(bar => y >= bar.y && y <= bar.y + bar.h);
    if (!bar) { hideTip(); return; }
    const r = bar.r;
    const finished = !!r.b.finished_at;
    const status = finished ? 'finished' : `${(+r.b.percent).toFixed(0)}%, in progress`;
    const dfmt = ms => new Date(ms).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
    showTip(`${esc(r.b.title || 'Untitled')} — ${dfmt(r.start)} → ${dfmt(r.end)} · ${fmtDuration(r.b.seconds)} · ${status}`, cx, cy);
  };
  canvas.onmousemove = e => at(e.clientX, e.clientY);
  canvas.onclick = e => at(e.clientX, e.clientY);
  canvas.onmouseleave = hideTip;
  bindTipDismiss();
}

// ---- library grid ----
async function ensureLibrary() { if (!LIBRARY) LIBRARY = await api('/api/library').catch(() => []); return LIBRARY; }
async function loadLibraryGrid() {
  await ensureLibrary();
  renderGrid($('#library-grid'), LIBRARY, null);
}
$('#book-search').oninput = e => {
  const q = e.target.value.toLowerCase();
  const f = (LIBRARY || []).filter(b => (b.title + ' ' + b.authors).toLowerCase().includes(q));
  renderGrid($('#library-grid'), f, null);
};
function renderGrid(el, books, onAdd) {
  el.innerHTML = '';
  for (const b of (books || [])) {
    const g = document.createElement('div'); g.className = 'gbook';
    const cov = b.has_cover
      ? `<div class="cover"><img src="/cover/${b.id}" loading="lazy" style="width:100%;height:100%;object-fit:cover;border-radius:7px" onerror="this.replaceWith(document.createTextNode('📕'))"></div>`
      : `<div class="cover">${esc(b.title || '📕').slice(0, 24)}</div>`;
    g.innerHTML = cov + `<div class="t">${esc(b.title || '')}</div><div class="a">${esc(b.authors || '')}</div>`;
    if (onAdd) {
      const btn = document.createElement('button'); btn.className = 'add'; btn.textContent = '+';
      btn.onclick = ev => { ev.stopPropagation(); onAdd(b); };
      g.appendChild(btn);
    }
    el.appendChild(g);
  }
}

// ---- collections / curation ----
async function loadCollections() {
  const url = location.origin + '/opds';
  if ($('#opds-url')) $('#opds-url').textContent = url;
  const cols = await api('/api/collections').catch(() => []);
  await ensureLibrary();
  const byId = Object.fromEntries((LIBRARY || []).map(b => [b.id, b]));
  const el = $('#collection-list'); el.innerHTML = '';
  if (!cols.length) { el.innerHTML = '<p class="muted">No collections yet. Create one to curate what shows up on your reader.</p>'; }
  for (const c of cols) {
    const div = document.createElement('div'); div.className = 'collection';
    const head = document.createElement('div'); head.className = 'collection-head';
    head.innerHTML = `<span class="name">${esc(c.icon || '📖')} ${esc(c.name)} <span class="muted">(${c.count})</span></span>`;
    const actions = document.createElement('div'); actions.className = 'col-actions';
    const addBtn = document.createElement('button'); addBtn.className = 'primary'; addBtn.textContent = '+ Add books';
    addBtn.onclick = () => openPicker(c);
    const delBtn = document.createElement('button'); delBtn.className = 'ghost'; delBtn.textContent = '🗑';
    delBtn.onclick = async () => { if (confirm(`Delete collection "${c.name}"?`)) { await api(`/api/collections/${c.id}`, { method: 'DELETE' }); loadCollections(); } };
    actions.append(addBtn, delBtn); head.appendChild(actions); div.appendChild(head);

    const grid = document.createElement('div'); grid.className = 'library-grid compact';
    const ids = await api(`/api/collections`).then(() => fetchCollectionBookIds(c.id)).catch(() => []);
    for (const id of ids) {
      const b = byId[id]; if (!b) continue;
      const g = document.createElement('div'); g.className = 'gbook';
      g.innerHTML = (b.has_cover
        ? `<div class="cover"><img src="/cover/${b.id}" loading="lazy" style="width:100%;height:100%;object-fit:cover;border-radius:7px"></div>`
        : `<div class="cover">${esc(b.title || '').slice(0, 24)}</div>`)
        + `<div class="t">${esc(b.title)}</div>`;
      const rm = document.createElement('button'); rm.className = 'add'; rm.textContent = '✕'; rm.style.opacity = 1;
      rm.onclick = async () => { await api(`/api/collections/${c.id}/books/${b.id}`, { method: 'DELETE' }); loadCollections(); };
      g.appendChild(rm); grid.appendChild(g);
    }
    div.appendChild(grid); el.appendChild(div);
  }
}
// collection book ids aren't in /api/collections; derive from OPDS-less endpoint via library + membership
async function fetchCollectionBookIds(id) {
  // We expose membership through the OPDS collection feed; parse ids out of it.
  const xml = await fetch(`/opds/collection/${id}`).then(r => r.text());
  const ids = [...xml.matchAll(/urn:booky:book:(\d+)/g)].map(m => +m[1]);
  return ids;
}

const newColBtn = $('#new-collection');
if (newColBtn) newColBtn.onclick = async () => {
  const name = prompt('Collection name (e.g. "On Deck", "Want to Read"):');
  if (!name) return;
  const icon = prompt('Emoji icon (optional):', '📖') || '';
  await api('/api/collections', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name, icon }) });
  loadCollections();
};

let pickerCol = null;
function openPicker(col) {
  pickerCol = col;
  $('#picker-title').textContent = `Add to ${col.name}`;
  $('#picker').classList.remove('hidden');
  renderGrid($('#picker-grid'), LIBRARY, async b => {
    await api(`/api/collections/${col.id}/books/${b.id}`, { method: 'POST' });
    b._added = true;
  });
}
$('#picker-close').onclick = () => { $('#picker').classList.add('hidden'); loadCollections(); };
$('#picker-search').oninput = e => {
  const q = e.target.value.toLowerCase();
  const f = (LIBRARY || []).filter(b => (b.title + ' ' + b.authors).toLowerCase().includes(q));
  renderGrid($('#picker-grid'), f, async b => { await api(`/api/collections/${pickerCol.id}/books/${b.id}`, { method: 'POST' }); });
};

function esc(s) { return (s || '').replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c])); }

// ---- manage tracked books (exclude from stats) ----
$('#manage-books').onclick = () => {
  $('#manage-search').value = '';
  renderManageList('');
  $('#manage-modal').classList.remove('hidden');
};
$('#manage-close').onclick = () => $('#manage-modal').classList.add('hidden');
$('#manage-search').oninput = e => renderManageList(e.target.value.toLowerCase());

function renderManageList(q) {
  const el = $('#manage-list'); el.innerHTML = '';
  const books = (SUMMARY?.books || [])
    .filter(b => !q || (b.title + ' ' + b.authors).toLowerCase().includes(q))
    .sort((a, b) => b.last_open - a.last_open);
  if (!books.length) { el.innerHTML = '<p class="muted">No matching books.</p>'; return; }
  for (const b of books) {
    const row = document.createElement('div');
    row.className = 'manage-row' + (b.excluded ? ' off' : '');
    const meta = document.createElement('div'); meta.className = 'meta';
    meta.innerHTML = `<div class="title">${esc(b.title || 'Untitled')}</div>
      <div class="sub">${esc(b.authors || '')}${b.seconds ? ' · ' + fmtDuration(b.seconds) : ''}</div>`;
    const btn = document.createElement('button');
    btn.className = b.excluded ? 'primary' : 'ghost wide';
    btn.textContent = b.excluded ? 'Include' : 'Exclude';
    btn.onclick = async () => {
      btn.disabled = true;
      try {
        await api(`/api/books/${b.md5}/excluded`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ excluded: !b.excluded }),
        });
        await loadDash();
        renderManageList($('#manage-search').value.toLowerCase());
      } catch (e) { btn.disabled = false; }
    };
    row.append(meta, btn);
    el.appendChild(row);
  }
}

// ---- account ----
if ($('#account-btn')) {
  const modal = $('#account-modal');
  $('#account-btn').onclick = () => {
    $('#acct-error').classList.add('hidden');
    $('#acct-ok').classList.add('hidden');
    modal.classList.remove('hidden');
  };
  $('#account-close').onclick = () => modal.classList.add('hidden');
  $('#logout-btn').onclick = async () => {
    await fetch('/api/logout', { method: 'POST' });
    location.href = '/login';
  };
  $('#account-form').onsubmit = async e => {
    e.preventDefault();
    $('#acct-error').classList.add('hidden');
    $('#acct-ok').classList.add('hidden');
    const r = await fetch('/api/account', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        current_password: $('#acct-current').value,
        new_username: $('#acct-user').value.trim(),
        new_password: $('#acct-pass').value,
      }),
    });
    const body = await r.json().catch(() => ({}));
    if (!r.ok) {
      $('#acct-error').textContent = body.error || 'Update failed';
      $('#acct-error').classList.remove('hidden');
      return;
    }
    $('#acct-pass').value = '';
    $('#acct-current').value = '';
    $('#acct-ok').classList.remove('hidden');
  };
}

// Close any modal by clicking its backdrop or pressing Escape.
function closeModal(m) {
  if (m.classList.contains('hidden')) return;
  m.classList.add('hidden');
  if (m.id === 'picker') loadCollections(); // same refresh as the ✕ button
}
$$('.modal').forEach(m => m.addEventListener('click', e => {
  if (e.target === m) closeModal(m);
}));
window.addEventListener('keydown', e => {
  if (e.key === 'Escape') $$('.modal').forEach(closeModal);
});

window.addEventListener('resize', () => {
  if (!SUMMARY) return;
  renderCharts();
  if ($('#tab-journal').classList.contains('active')) renderJournalCharts();
  renderOpenPanels();
});
loadDash();
