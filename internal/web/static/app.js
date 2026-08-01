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
  if (SUMMARY) renderCharts();
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

function attachCellHover(el) {
  const at = (target, cx, cy) => {
    const c = target.closest && target.closest('.cell');
    if (!c || !c.dataset.day) { hideTip(); return; }
    showTip(`${c.dataset.day}: ${fmtDuration(+c.dataset.sec)}, ${fmtNum(+c.dataset.pages)} pages`, cx, cy);
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

function renderBooks() {
  const list = $('#book-list'); list.innerHTML = '';
  const books = SUMMARY.books.filter(b => !b.excluded)
    .sort((a, b) => b.last_open - a.last_open).slice(0, 12);
  for (const b of books) {
    const row = document.createElement('div'); row.className = 'book-row';
    row.appendChild(coverEl(b, 'cover'));
    const meta = document.createElement('div'); meta.className = 'meta';
    const fin = b.finished ? ' <span class="badge">DONE</span>' : '';
    meta.innerHTML = `<div class="title">${esc(b.title || 'Untitled')}${fin}</div>
      <div class="sub">${esc(b.authors || '')}</div>
      <div class="progress"><i style="width:${Math.min(100, b.percent).toFixed(0)}%"></i></div>`;
    const nums = document.createElement('div'); nums.className = 'nums';
    nums.title = 'Total time spent in this book';
    nums.innerHTML = `<b>${fmtDuration(b.seconds)}</b> read<br>${b.percent.toFixed(0)}% · ${timeAgo(b.last_open)}`;
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

window.addEventListener('resize', () => { if (SUMMARY) renderCharts(); });
loadDash();
