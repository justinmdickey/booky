'use strict';
const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const api = (p, o) => fetch(p, o).then(r => r.ok ? (r.status === 204 ? null : r.json()) : Promise.reject(r));

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
});

// ---- theme ----
$('#theme-toggle').onclick = () => {
  const order = ['auto', 'light', 'dark'];
  const cur = document.documentElement.dataset.theme;
  const next = order[(order.indexOf(cur) + 1) % order.length];
  document.documentElement.dataset.theme = next;
  localStorage.theme = next;
  if (SUMMARY) renderCharts();
};
if (localStorage.theme) document.documentElement.dataset.theme = localStorage.theme;

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
  for (let i = 0; i < n; i++) {
    const v = values[i];
    const bh = (h - pad.t - pad.b) * (v / max);
    const x = pad.l + i * bw, y = h - pad.b - bh;
    bars.push({ x: x + bw * .15, y, w: bw * .7, h: bh, i });
    ctx.fillStyle = accent;
    const r = Math.min(3, bw * .4);
    roundRect(ctx, x + bw * .15, y, bw * .7, bh, r);
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
function attachBarHover(canvas, bars, labels, values, fmt) {
  const tip = barTip();
  canvas.onmousemove = e => {
    const rect = canvas.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const b = bars.find(bar => x >= bar.x && x <= bar.x + bar.w);
    if (!b) { tip.classList.remove('show'); return; }
    tip.innerHTML = fmt ? fmt(b.i, labels[b.i], values[b.i]) : `${labels[b.i]}: ${values[b.i]}`;
    tip.classList.add('show');
    const tw = tip.offsetWidth, th = tip.offsetHeight;
    let tx = e.clientX + 12, ty = e.clientY - th - 8;
    if (tx + tw > window.innerWidth - 6) tx = e.clientX - tw - 12;
    if (ty < 6) ty = e.clientY + 12;
    tip.style.left = tx + 'px'; tip.style.top = ty + 'px';
  };
  canvas.onmouseleave = () => tip.classList.remove('show');
}

function attachCellHover(el) {
  const tip = barTip();
  el.onmousemove = e => {
    const c = e.target.closest('.cell');
    if (!c || !c.dataset.day) { tip.classList.remove('show'); return; }
    const sec = +c.dataset.sec, pages = +c.dataset.pages;
    tip.innerHTML = `${c.dataset.day}: ${fmtDuration(sec)}, ${fmtNum(pages)} pages`;
    tip.classList.add('show');
    const tw = tip.offsetWidth, th = tip.offsetHeight;
    let tx = e.clientX + 12, ty = e.clientY - th - 8;
    if (tx + tw > window.innerWidth - 6) tx = e.clientX - tw - 12;
    if (ty < 6) ty = e.clientY + 12;
    tip.style.left = tx + 'px'; tip.style.top = ty + 'px';
  };
  el.onmouseleave = () => tip.classList.remove('show');
}

// ---- dashboard ----
let rangeDays = 30;
async function loadDash() {
  try { SUMMARY = await api('/api/summary'); }
  catch (e) { return; }
  if (!SUMMARY || !SUMMARY.books_tracked) { $('#empty').classList.remove('hidden'); return; }
  $('#dash-content').classList.remove('hidden');

  $('#s-time').textContent = fmtDuration(SUMMARY.total_seconds);
  $('#s-streak').textContent = SUMMARY.current_streak;
  $('#s-books').textContent = `${SUMMARY.books_tracked} · ${SUMMARY.books_finished}`;
  $('#s-pages').textContent = fmtNum(SUMMARY.total_pages);
  $('#s-speed').textContent = SUMMARY.pages_per_hour ? SUMMARY.pages_per_hour.toFixed(0) : '–';
  $('#s-week').textContent = fmtDuration(SUMMARY.this_week_seconds);

  renderHeatmap();
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

  const hourlyRaw = SUMMARY.hourly || [];
  const hourly = hourlyRaw.map(s => Math.round(s / 60));
  const hourLabels = [...Array(24)].map((_, i) => i);
  barChart($('#chart-hourly'), hourLabels, hourly, {
    labelEvery: 4,
    tooltip: (i) => {
      const h = +hourLabels[i];
      const ampm = h === 0 ? '12am' : h < 12 ? `${h}am` : h === 12 ? '12pm' : `${h - 12}pm`;
      return `${ampm}: ${fmtDuration(hourlyRaw[i])}`;
    },
  });

  const wdNames = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
  const wd = ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa'];
  const weekdayRaw = SUMMARY.weekday || [];
  const weekday = weekdayRaw.map(s => Math.round(s / 60));
  barChart($('#chart-weekday'), wd, weekday, {
    labelEvery: 1,
    tooltip: (i) => `${wdNames[i]}: ${fmtDuration(weekdayRaw[i])}`,
  });
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

function renderBooks() {
  const list = $('#book-list'); list.innerHTML = '';
  const books = [...SUMMARY.books].sort((a, b) => b.last_open - a.last_open).slice(0, 12);
  for (const b of books) {
    const row = document.createElement('div'); row.className = 'book-row';
    row.appendChild(coverEl(b, 'cover'));
    const meta = document.createElement('div'); meta.className = 'meta';
    const fin = b.finished ? ' <span class="badge">DONE</span>' : '';
    meta.innerHTML = `<div class="title">${esc(b.title || 'Untitled')}${fin}</div>
      <div class="sub">${esc(b.authors || '')}</div>
      <div class="progress"><i style="width:${Math.min(100, b.percent).toFixed(0)}%"></i></div>`;
    const nums = document.createElement('div'); nums.className = 'nums';
    nums.innerHTML = `<b>${fmtDuration(b.seconds)}</b><br>${b.percent.toFixed(0)}% · ${timeAgo(b.last_open)}`;
    row.append(meta, nums);
    list.appendChild(row);
  }
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

window.addEventListener('resize', () => { if (SUMMARY) renderCharts(); });
loadDash();
