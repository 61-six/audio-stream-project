/* ============================================================
 * 音频修复工作室 - 前端
 * 布局:红顶栏(搜索) + 状态 Tab + 歌曲表格 + 固定底部播放器
 * 技术:原生 JS,无依赖;数据驱动 state → 渲染
 * ============================================================ */

// ============ 全局状态 ============
const state = {
  songs: [],            // 全量歌曲(GET /api/songs)
  filter: '',           // 搜索关键字
  view: 'all',          // Tab: all | favorite | repairing | repaired | failed
  currentId: null,      // 当前播放歌曲 id
  mode: 'repaired',     // 用户偏好版本: original | repaired
  actualMode: 'repaired', // 实际播放版本(修复版未就绪时降级为原音)
  loop: 'list',         // 循环模式: list(列表循环) | single(单曲循环)
  progress: {},         // songId → 修复进度百分比(WS 实时更新)
  selected: new Set(),  // 批量删除选中的 id
  editingId: null,      // 正在编辑的歌曲 id
};

// ============ DOM 引用 ============
const $ = (id) => document.getElementById(id);
const fileInput   = $('file-input');
const btnImport   = $('btn-import');
const importToast = $('import-toast');
const importTitle = $('import-title');
const importFill  = $('import-fill');
const importFile  = $('import-file');
const searchInput = $('search');
const tbody       = $('songs-list');
const emptyTip    = $('empty-tip');
const dropMask    = $('drop-mask');
const player      = $('player');
const audio       = $('audio');
const pCover      = $('p-cover');
const pTitle      = $('p-title');
const pArtist     = $('p-artist');
const pDownload   = $('p-download');
const btnPlay     = $('btn-play');
const btnPrev     = $('btn-prev');
const btnNext     = $('btn-next');
const btnLoop     = $('btn-loop');
const modeOriginal = $('mode-original');
const modeRepaired = $('mode-repaired');
const progressBar  = $('progress-bar');
const progressPlay = $('progress-play');
const progressThumb= $('progress-thumb');
const timeCur      = $('time-cur');
const timeTotal    = $('time-total');
const volumeBar    = $('volume-bar');
const volumeFill   = $('volume-fill');

// ============ 工具函数 ============
function esc(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;')
    .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// 毫秒时长 → m:ss
function fmtDur(ms) {
  if (!ms || ms <= 0) return '--:--';
  return fmtTime(ms / 1000);
}

// 秒 → m:ss
function fmtTime(sec) {
  if (!isFinite(sec) || sec < 0) return '0:00';
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return `${m}:${String(s).padStart(2, '0')}`;
}

function currentSong() {
  return state.songs.find(s => s.id === state.currentId) || null;
}

// 当前 Tab + 搜索过滤后的可见列表(也是播放队列)
function visibleSongs() {
  const q = state.filter.trim().toLowerCase();
  return state.songs.filter(s => {
    let okView = true;
    if (state.view === 'favorite') {
      okView = s.favorite;
    } else if (state.view === 'repairing') {
      okView = s.status === 'pending' || s.status === 'repairing';
    } else if (state.view !== 'all') {
      okView = s.status === state.view;
    }
    const okQ = !q || [s.title, s.artist, s.album, s.original_filename]
      .some(v => (v || '').toLowerCase().includes(q));
    return okView && okQ;
  });
}

// ============ API 调用 ============
async function fetchSongs() {
  try {
    const res = await fetch('/api/songs');
    const data = await res.json();
    state.songs = data.songs || [];
    renderAll();
  } catch (e) {
    console.error('fetchSongs failed:', e);
  }
}

// collectRepairParams 读取修复选项面板,返回 JSON 字符串(默认参数则返回空串)
function collectRepairParams() {
  const panel = $('repair-options');
  if (panel.hidden) return ''; // 面板收起时用默认参数
  const denoise = parseFloat($('opt-denoise').value);
  const loudness = parseFloat($('opt-loudness').value);
  const fmt = $('opt-format').value;
  // 只传与默认值不同的字段,服务端会用 DefaultParams 补齐其余项
  const params = {
    enable_denoise: true,
    denoise_strength: denoise,
    enable_declick: true,
    enable_loudnorm: true,
    target_loudness: loudness,
    enable_resample: true,
    target_sample_rate: 48000,
    output_format: fmt,
  };
  return JSON.stringify(params);
}

async function uploadOne(file) {
  const form = new FormData();
  form.append('file', file);
  const params = collectRepairParams();
  if (params) form.append('repair_params', params);
  try {
    const res = await fetch('/api/songs', { method: 'POST', body: form });
    if (!res.ok) {
      const d = await res.json().catch(() => ({}));
      alert(`上传失败: ${file.name}\n${d.error || ''}`);
    }
  } catch (e) {
    alert('上传失败: ' + file.name);
  }
}

async function uploadFiles(files) {
  const list = files.filter(f => /\.(mp3|flac|wav|m4a|aac|ogg|wma)$/i.test(f.name));
  if (!list.length) return;
  for (const f of list) {
    await uploadOne(f); // 串行上传,避免修复队列瞬间堆积
  }
  fetchSongs();
}

async function deleteSong(id) {
  if (!confirm('确定删除这首歌曲?')) return;
  try {
    await fetch(`/api/songs/${id}`, { method: 'DELETE' });
  } catch (e) { /* 忽略网络错误,继续刷新 */ }
  if (state.currentId === id) {
    audio.pause();
    audio.removeAttribute('src');
    audio.load();
    state.currentId = null;
    player.hidden = true;
  }
  delete state.progress[id];
  fetchSongs();
}

async function retryRepair(id) {
  const form = new FormData();
  const params = collectRepairParams();
  if (params) form.append('repair_params', params);
  try {
    const res = await fetch(`/api/songs/${id}/retry`, { method: 'POST', body: params ? form : undefined });
    if (!res.ok) {
      const d = await res.json().catch(() => ({}));
      alert('重试失败: ' + (d.error || ''));
      return;
    }
  } catch (e) { /* ignore */ }
  fetchSongs();
}

// 切换收藏
async function toggleFavorite(id) {
  try {
    const res = await fetch(`/api/songs/${id}/favorite`, { method: 'POST' });
    if (res.ok) {
      const song = state.songs.find(s => s.id === id);
      if (song) song.favorite = !song.favorite;
      renderTable();
      updateCounts();
    }
  } catch (e) { /* ignore */ }
}

// 打开编辑弹窗
function openEditModal(id) {
  const song = state.songs.find(s => s.id === id);
  if (!song) return;
  state.editingId = id;
  $('edit-title').value = song.title || '';
  $('edit-artist').value = song.artist || '';
  $('edit-album').value = song.album || '';
  $('edit-year').value = song.year || '';
  $('edit-modal').hidden = false;
}

// 保存编辑
async function saveEdit() {
  const id = state.editingId;
  if (!id) return;
  const body = {
    title: $('edit-title').value.trim(),
    artist: $('edit-artist').value.trim(),
    album: $('edit-album').value.trim(),
    year: parseInt($('edit-year').value) || 0,
  };
  try {
    const res = await fetch(`/api/songs/${id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      const d = await res.json().catch(() => ({}));
      alert('保存失败: ' + (d.error || ''));
      return;
    }
    $('edit-modal').hidden = true;
    state.editingId = null;
    fetchSongs();
  } catch (e) { alert('保存失败'); }
}

$('edit-cancel').addEventListener('click', () => { $('edit-modal').hidden = true; state.editingId = null; });
$('edit-save').addEventListener('click', saveEdit);
$('edit-modal').addEventListener('click', (e) => {
  if (e.target.id === 'edit-modal') { $('edit-modal').hidden = true; state.editingId = null; }
});

// ============ 封面(统一入口:cover_path → /covers/xxx.jpg,空则音符占位) ============
function setCover(container, song) {
  container.innerHTML = '';
  if (song.cover_path) {
    const img = document.createElement('img');
    img.className = 'cover-img';
    img.src = '/' + song.cover_path;
    img.alt = '';
    container.appendChild(img);
  } else {
    container.textContent = '♪';
  }
}

// 封面加载失败全局兜底(捕获阶段:img error 不冒泡)
// 失败时移除 img,容器的红渐变底色 + ♪ 自动露出
document.addEventListener('error', (e) => {
  const img = e.target;
  if (img && img.tagName === 'IMG' && img.classList.contains('cover-img')) {
    const box = img.parentElement;
    img.remove();
    if (box) box.textContent = '♪';
  }
}, true);

// ============ 渲染:Tab 角标 ============
function updateCounts() {
  $('count-all').textContent = state.songs.length;
  $('count-favorite').textContent = state.songs.filter(s => s.favorite).length;
  $('count-repairing').textContent = state.songs.filter(
    s => s.status === 'pending' || s.status === 'repairing').length;
  $('count-repaired').textContent = state.songs.filter(
    s => s.status === 'repaired').length;
  $('count-failed').textContent = state.songs.filter(
    s => s.status === 'failed').length;
}

// ============ 渲染:歌曲表格 ============
function statusBadge(s) {
  const pct = state.progress[s.id] || 0;
  switch (s.status) {
    case 'pending':
      return '<span class="badge badge-pending">待修复</span>';
    case 'repairing':
      // 短歌 ffmpeg 可能还没输出进度行就完成了,避免一直显示 0%
      return `<span class="badge badge-repairing">修复中${pct > 0 ? ' ' + pct + '%' : '…'}</span>`;
    case 'repaired':
      return '<span class="badge badge-repaired">已修复</span>';
    case 'failed':
      return `<span class="badge badge-failed" title="${esc(s.error_msg || '修复失败')}">失败</span>`;
    default:
      return '';
  }
}

function rowHtml(s, i) {
  const playing = s.id === state.currentId;
  const cover = s.cover_path
    ? `<img class="cover-img" src="/${esc(s.cover_path)}" alt="">`
    : '♪';
  const pct = state.progress[s.id] || 0;
  const progressBarHtml = s.status === 'repairing'
    ? `<div class="row-progress"><div class="row-progress-bar" style="width:${pct}%"></div></div>`
    : '';
  // 下载:仅修复完成可用(直链,浏览器走 Content-Disposition 另存为)
  const download = s.status === 'repaired'
    ? `<a class="icon-btn" data-action="download" href="/api/songs/${s.id}/download?mode=repaired" download title="下载修复版">⬇</a>`
    : '<span class="icon-btn disabled" title="修复完成后可下载">⬇</span>';
  const retry = s.status === 'failed'
    ? `<button class="icon-btn" data-action="retry" title="重试修复">↻</button>`
    : '';
  const playTitle = s.status === 'repaired' ? '播放修复版' : '播放原音';
  const favCls = s.favorite ? 'fav-btn active' : 'fav-btn';
  const checked = state.selected.has(s.id) ? 'checked' : '';

  return `
    <tr class="song-row ${playing ? 'playing' : ''}" data-id="${s.id}">
      <td class="col-check"><input type="checkbox" data-action="check" ${checked}></td>
      <td class="col-index">
        <span class="row-num">${i + 1}</span>
        <span class="row-eq"><i></i><i></i><i></i></span>
      </td>
      <td class="col-title">
        <div class="title-line">
          <button class="${favCls}" data-action="favorite" title="${s.favorite ? '取消收藏' : '收藏'}">${s.favorite ? '❤' : '♡'}</button>
          <div class="row-cover">${cover}</div>
          <span class="row-title-text">${esc(s.title || s.original_filename)}</span>
        </div>
        ${progressBarHtml}
      </td>
      <td class="col-artist">${esc(s.artist || '未知艺术家')}</td>
      <td class="col-album">${esc(s.album || '-')}</td>
      <td class="col-duration">${fmtDur(s.duration_ms)}</td>
      <td class="col-status">${statusBadge(s)}</td>
      <td class="col-actions">
        <div class="row-actions">
          <button class="icon-btn" data-action="play" title="${playTitle}">▶</button>
          ${download}
          ${retry}
          <button class="icon-btn" data-action="edit" title="编辑信息">✎</button>
          <button class="icon-btn" data-action="delete" title="删除">🗑</button>
        </div>
      </td>
    </tr>`;
}

function renderTable() {
  const list = visibleSongs();
  if (!list.length) {
    tbody.innerHTML = '';
    emptyTip.hidden = false;
    emptyTip.textContent = state.songs.length
      ? '没有匹配的音乐'
      : '暂无音乐,点击右上角「上传音乐」或直接拖入音频文件';
    return;
  }
  emptyTip.hidden = true;
  tbody.innerHTML = list.map(rowHtml).join('');
}

// WS 进度事件:只局部更新对应行,不整表重绘(避免列表闪烁)
function updateRowProgress(id) {
  const tr = tbody.querySelector(`tr[data-id="${id}"]`);
  if (!tr) return;
  const pct = state.progress[id] || 0;
  const bar = tr.querySelector('.row-progress-bar');
  if (bar) bar.style.width = pct + '%';
  const badge = tr.querySelector('.badge-repairing');
  if (badge) badge.textContent = pct > 0 ? `修复中 ${pct}%` : '修复中…';
}

function renderAll() {
  updateCounts();
  renderTable();
  // 清理已不存在的选中 id,并刷新批量删除按钮
  const validIds = new Set(state.songs.map(s => s.id));
  for (const id of state.selected) if (!validIds.has(id)) state.selected.delete(id);
  updateSelectionBar();
  // 当前播放歌曲仍在库中时刷新底栏(修复完成后下载按钮/版本开关要更新)
  if (state.currentId) {
    if (currentSong()) updatePlayerUI();
    else { player.hidden = true; state.currentId = null; }
  }
}

// ============ 播放器 ============
function playSong(id) {
  const song = state.songs.find(s => s.id === id);
  if (!song) return;
  state.currentId = id;

  // 修复版优先;未就绪自动降级原音
  const useRepaired = state.mode === 'repaired'
    && song.status === 'repaired' && song.repaired_path;
  const mode = useRepaired ? 'repaired' : 'original';
  state.actualMode = mode;

  audio.src = `/play/${id}?mode=${mode}`;
  audio.play().catch(err => console.warn('play failed:', err));
  player.hidden = false;
  updatePlayerUI();
  renderTable();
}

// 上一首/下一首(在当前可见列表内循环)
function playOffset(delta) {
  const list = visibleSongs();
  if (!list.length) return;
  let idx = list.findIndex(s => s.id === state.currentId);
  if (idx === -1) idx = delta > 0 ? -1 : 0;
  idx = (idx + delta + list.length) % list.length;
  playSong(list[idx].id);
}

// 原音/修复版切换:记住播放位置,换源后 seek 回原位(A-B 对比不重头)
function switchMode(mode) {
  const song = currentSong();
  if (!song) return;
  if (mode === 'repaired' && song.status !== 'repaired') return;
  state.mode = mode;
  state.actualMode = mode;

  const savedTime = audio.currentTime;
  const wasPlaying = !audio.paused;
  audio.src = `/play/${song.id}?mode=${mode}`;
  audio.addEventListener('loadedmetadata', function resume() {
    if (isFinite(savedTime) && savedTime > 0) {
      audio.currentTime = Math.min(savedTime, audio.duration || savedTime);
    }
    if (wasPlaying) audio.play().catch(() => {});
  }, { once: true });

  updateModeButtons();
}

function updatePlayerUI() {
  const song = currentSong();
  if (!song) { player.hidden = true; return; }
  setCover(pCover, song);
  pTitle.textContent = song.title || song.original_filename;
  pArtist.textContent = song.artist || '未知艺术家';
  if (song.status === 'repaired') {
    pDownload.href = `/api/songs/${song.id}/download?mode=repaired`;
    pDownload.classList.remove('disabled');
  } else {
    pDownload.removeAttribute('href');
    pDownload.classList.add('disabled');
  }
  updateModeButtons();
}

function updateModeButtons() {
  const song = currentSong();
  modeOriginal.classList.toggle('active', state.actualMode === 'original');
  modeRepaired.classList.toggle('active', state.actualMode === 'repaired');
  modeRepaired.disabled = !(song && song.status === 'repaired');
}

// -- 音频引擎事件 --
audio.addEventListener('play',  () => { btnPlay.textContent = '⏸'; });
audio.addEventListener('pause', () => { btnPlay.textContent = '▶'; });
audio.addEventListener('loadedmetadata', () => {
  timeTotal.textContent = fmtTime(audio.duration);
});
audio.addEventListener('timeupdate', () => {
  if (audio.duration) {
    const pct = (audio.currentTime / audio.duration) * 100;
    progressPlay.style.width = pct + '%';
    progressThumb.style.left = pct + '%';
  }
  timeCur.textContent = fmtTime(audio.currentTime);
});
audio.addEventListener('ended', () => {
  if (state.loop === 'single') {
    audio.currentTime = 0;
    audio.play().catch(() => {});
  } else {
    playOffset(1);
  }
});

// -- 播放控制按钮 --
btnPlay.addEventListener('click', () => {
  if (!currentSong()) return;
  if (audio.paused) audio.play().catch(() => {});
  else audio.pause();
});
btnPrev.addEventListener('click', () => playOffset(-1));
btnNext.addEventListener('click', () => playOffset(1));
modeOriginal.addEventListener('click', () => switchMode('original'));
modeRepaired.addEventListener('click', () => switchMode('repaired'));

// 循环模式:列表循环 ↔ 单曲循环
btnLoop.addEventListener('click', () => {
  state.loop = state.loop === 'list' ? 'single' : 'list';
  btnLoop.textContent = state.loop === 'list' ? '🔁' : '🔂';
  btnLoop.title = state.loop === 'list' ? '列表循环' : '单曲循环';
});

// -- 进度条:点击/拖动 seek --
let seeking = false;
function seekTo(clientX) {
  const rect = progressBar.getBoundingClientRect();
  const ratio = Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
  if (audio.duration) audio.currentTime = ratio * audio.duration;
}
progressBar.addEventListener('mousedown', (e) => { seeking = true; seekTo(e.clientX); });
window.addEventListener('mousemove', (e) => { if (seeking) seekTo(e.clientX); });
window.addEventListener('mouseup', () => { seeking = false; });

// -- 音量条 --
audio.volume = 0.8;
let volDrag = false;
function setVolume(clientX) {
  const rect = volumeBar.getBoundingClientRect();
  const v = Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
  audio.volume = v;
  volumeFill.style.width = (v * 100) + '%';
}
volumeBar.addEventListener('mousedown', (e) => { volDrag = true; setVolume(e.clientX); });
window.addEventListener('mousemove', (e) => { if (volDrag) setVolume(e.clientX); });
window.addEventListener('mouseup', () => { volDrag = false; });

// ============ 表格事件委托(双击行播放;单击操作按钮) ============
tbody.addEventListener('click', (e) => {
  const btn = e.target.closest('[data-action]');
  if (btn) {
    const action = btn.dataset.action;
    if (action === 'download') return; // 直链下载,走浏览器默认行为
    if (action === 'check') {
      // 复选框:更新选中集合并刷新批量删除按钮
      const id = btn.closest('tr[data-id]').dataset.id;
      if (btn.checked) state.selected.add(id);
      else state.selected.delete(id);
      updateSelectionBar();
      return;
    }
    const id = btn.closest('tr[data-id]').dataset.id;
    if (action === 'play') playSong(id);
    else if (action === 'retry') retryRepair(id);
    else if (action === 'delete') deleteSong(id);
    else if (action === 'favorite') toggleFavorite(id);
    else if (action === 'edit') openEditModal(id);
    return;
  }
});

// 更新批量删除按钮显示
function updateSelectionBar() {
  const btn = $('btn-del-selected');
  const count = state.selected.size;
  btn.hidden = count === 0;
  $('sel-count').textContent = count;
  // 同步全选框状态
  const visible = visibleSongs();
  const checkAll = $('check-all');
  if (visible.length === 0) { checkAll.checked = false; checkAll.indeterminate = false; }
  else {
    const selectedInView = visible.filter(s => state.selected.has(s.id)).length;
    checkAll.checked = selectedInView === visible.length;
    checkAll.indeterminate = selectedInView > 0 && selectedInView < visible.length;
  }
}

// 全选/取消全选(仅当前可见列表)
$('check-all').addEventListener('change', (e) => {
  const visible = visibleSongs();
  if (e.target.checked) visible.forEach(s => state.selected.add(s.id));
  else visible.forEach(s => state.selected.delete(s.id));
  renderTable();
  updateSelectionBar();
});

// 批量删除
$('btn-del-selected').addEventListener('click', async () => {
  const ids = Array.from(state.selected);
  if (!ids.length) return;
  if (!confirm(`确定删除选中的 ${ids.length} 首歌曲?`)) return;
  try {
    await fetch(`/api/songs?ids=${ids.join(',')}`, { method: 'DELETE' });
  } catch (e) { /* ignore */ }
  // 清理播放状态
  if (state.currentId && state.selected.has(state.currentId)) {
    audio.pause();
    audio.removeAttribute('src');
    audio.load();
    state.currentId = null;
    player.hidden = true;
  }
  state.selected.clear();
  fetchSongs();
});
tbody.addEventListener('dblclick', (e) => {
  const tr = e.target.closest('tr[data-id]');
  if (tr) playSong(tr.dataset.id);
});

// ============ 搜索 + Tab ============
searchInput.addEventListener('input', () => {
  state.filter = searchInput.value;
  renderTable();
});
document.querySelectorAll('.tab').forEach(tab => {
  tab.addEventListener('click', () => {
    state.view = tab.dataset.view;
    document.querySelectorAll('.tab').forEach(t =>
      t.classList.toggle('active', t === tab));
    renderTable();
  });
});

// ============ 修复选项面板 ============
const btnGear = $('btn-gear');
const repairOptions = $('repair-options');
const optDenoise = $('opt-denoise');
const optLoudness = $('opt-loudness');
const optDenoiseVal = $('opt-denoise-val');
const optLoudnessVal = $('opt-loudness-val');
btnGear.addEventListener('click', () => {
  repairOptions.hidden = !repairOptions.hidden;
  btnGear.classList.toggle('active', !repairOptions.hidden);
});
optDenoise.addEventListener('input', () => { optDenoiseVal.textContent = parseFloat(optDenoise.value).toFixed(2); });
optLoudness.addEventListener('input', () => { optLoudnessVal.textContent = optLoudness.value; });

// ============ 扫描目录导入 ============
function showImportToast(title) {
  importFill.style.width = '0';
  importFile.textContent = '';
  importTitle.textContent = title;
  importToast.hidden = false;
}

btnImport.addEventListener('click', async () => {
  btnImport.disabled = true;
  try {
    const res = await fetch('/api/import', { method: 'POST' });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      alert(data.error === 'import already running' ? '已有导入任务正在执行' : `导入失败: ${data.error || ''}`);
      return;
    }
    if (!data.found) {
      // 目录为空:告诉用户把歌放到服务器哪个目录
      alert(`导入目录中没有找到音频文件。\n\n请把歌曲文件(可含子目录)放到服务器目录:\n${data.dir || 'storage/import'}\n\n放好后再点「扫描导入」。`);
      return;
    }
    showImportToast(`正在扫描导入 ${data.found} 首歌曲…`);
  } catch (e) {
    alert('导入请求失败: ' + e);
  } finally {
    btnImport.disabled = false;
  }
});

// ============ 上传:按钮选择 + 整窗拖拽 ============
fileInput.addEventListener('change', (e) => {
  uploadFiles(Array.from(e.target.files));
  fileInput.value = '';
});

// 拖拽计数器:dragenter/dragleave 在子元素间会反复触发,用计数避免遮罩闪烁
let dragDepth = 0;
function hasFiles(e) {
  return e.dataTransfer &&
    Array.from(e.dataTransfer.types || []).includes('Files');
}
window.addEventListener('dragenter', (e) => {
  if (!hasFiles(e)) return;
  e.preventDefault();
  dragDepth++;
  dropMask.hidden = false;
});
window.addEventListener('dragover', (e) => {
  if (hasFiles(e)) e.preventDefault(); // 必须阻止,否则 drop 不触发
});
window.addEventListener('dragleave', (e) => {
  if (!hasFiles(e)) return;
  e.preventDefault();
  dragDepth = Math.max(0, dragDepth - 1);
  if (dragDepth === 0) dropMask.hidden = true;
});
window.addEventListener('drop', (e) => {
  if (!hasFiles(e)) return;
  e.preventDefault();
  dragDepth = 0;
  dropMask.hidden = true;
  uploadFiles(Array.from(e.dataTransfer.files));
});

// ============ WebSocket 修复进度 ============
async function connectWs() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  let url = `${proto}://${location.host}/ws/progress`;

  // Basic Auth 启用时,浏览器 WS API 无法设置 Authorization 头,
  // 先通过可自动携带凭证的 fetch 换取一次性 ticket,再拼到 WS URL
  try {
    const res = await fetch('/api/ws-ticket');
    if (res.ok) {
      const d = await res.json();
      if (d.required && d.ticket) {
        url += '?ticket=' + encodeURIComponent(d.ticket);
      }
    }
  } catch (e) { /* 未启用认证/网络抖动时直连,由后续重连兜底 */ }

  const ws = new WebSocket(url);
  ws.onmessage = (e) => {
    let ev;
    try { ev = JSON.parse(e.data); } catch { return; }

    // 扫描导入进度事件(song_id 为空,按 stage 识别)
    if (ev.stage === 'import_start') {
      showImportToast('正在扫描导入…');
      return;
    }
    if (ev.stage === 'import_copy') {
      // 大文件复制中:percent 已折算为整体百分比,文件名前加提示
      importFill.style.width = (ev.percent || 0) + '%';
      importFile.textContent = '正在复制 ' + (ev.detail || '');
      return;
    }
    if (ev.stage === 'import_progress') {
      importFill.style.width = (ev.percent || 0) + '%';
      importFile.textContent = ev.detail || '';
      return;
    }
    if (ev.stage === 'import_done') {
      importFill.style.width = '100%';
      importTitle.textContent = '导入完成';
      importFile.textContent = ev.detail || '';
      setTimeout(() => { importToast.hidden = true; }, 3000);
      fetchSongs();
      return;
    }

    if (!ev.song_id) return;
    state.progress[ev.song_id] = ev.percent || 0;
    // 终态(完成/失败)文件可用性变化,全量刷新;过程中只局部更新进度条
    if (ev.stage === 'completed' || ev.stage === 'failed') {
      fetchSongs();
    } else {
      updateRowProgress(ev.song_id);
    }
  };
  ws.onclose = () => setTimeout(connectWs, 2000); // 自动重连
  ws.onerror = () => ws.close();
}

// ============ 启动 ============
fetchSongs();
connectWs();
