(function (global) {
    'use strict';

    let fgsCy = null;
    let refreshTimer = null;
    let loadSeq = 0;
    let selectedNodeId = '';
    let graphData = null;
    let currentViewportKey = '';

    const FGS_VIEWPORT_STORAGE_PREFIX = 'provena.fgs.viewport.v1';

    const KIND_STYLE = {
        origin: { color: '#64748b', label: 'Origin' },
        goal: { color: '#2563eb', label: 'Goal' },
        fact: { color: '#059669', label: 'Fact' },
        finding: { color: '#e11d48', label: 'Finding' },
        intent: { color: '#9333ea', label: 'Intent' },
        step: { color: '#d97706', label: 'Step' },
        sub_goal: { color: '#0891b2', label: 'Sub Goal' },
        hint: { color: '#94a3b8', label: 'Hint' },
    };

    function esc(value) {
        const text = String(value == null ? '' : value);
        return text.replace(/[&<>"']/g, (ch) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]));
    }

    function nodeStyle(kind) {
        return KIND_STYLE[String(kind || '').toLowerCase()] || KIND_STYLE.hint;
    }

    function shortText(value, max) {
        const text = String(value || '').trim();
        if (text.length <= max) return text;
        return text.slice(0, Math.max(0, max - 1)) + '…';
    }

    function setText(id, value) {
        const el = document.getElementById(id);
        if (el) el.textContent = value == null ? '' : String(value);
    }

    function formatTime(value) {
        if (!value) return '—';
        const date = new Date(value);
        return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString();
    }

    function statusLabel(status) {
        return ({ pending: '待处理', active: '进行中', confirmed: '已确认', completed: '已完成', abandoned: '已废弃', blocked: '已阻塞' }[status] || status || '未知');
    }

    function graphStatusLabel(status) {
        return ({ in_progress: '进行中', completed: '已完成', blocked: '已阻塞', abandoned: '已废弃' }[status] || '进行中');
    }

    function runOptionLabel(item, index, total) {
        const run = item || {};
        const marker = index === 0 ? '当前/最新' : `历史 ${total - index}`;
        const updated = run.updatedAt ? formatTime(run.updatedAt) : '未知时间';
        const counts = `${Number(run.nodeCount || 0)} 节点 · ${Number(run.edgeCount || 0)} 边`;
        return `${marker} · ${updated} · v${Number(run.version || 0)} · ${counts} · ${graphStatusLabel(run.graphStatus)}`;
    }

    function currentConversationId() {
        return String(global.currentConversationId || '').trim();
    }

    function updateFGSButton() {
        const button = document.getElementById('fgs-btn');
        const id = currentConversationId();
        if (!button) return;
        button.disabled = !id;
        button.title = id ? '查看当前对话的 FGS 图' : '请选择一个对话后查看 FGS 图';
    }

    function renderNodeDetails(node) {
        const container = document.getElementById('fgs-node-details');
        if (!container) return;
        if (!node) {
            container.innerHTML = '<p class="fgs-detail-placeholder">点击图中的节点查看状态、内容和证据。</p>';
            return;
        }
        const style = nodeStyle(node.kind);
        const evidence = Array.isArray(node.evidence) ? node.evidence : [];
        container.innerHTML = `
            <div class="fgs-node-heading"><span class="fgs-node-type" style="--fgs-node-color:${style.color}">${esc(style.label)}</span><span class="fgs-node-status fgs-status-${esc(node.status)}">${esc(statusLabel(node.status))}</span></div>
            <h4>${esc(node.label || node.id)}</h4>
            <div class="fgs-node-id">${esc(node.id)}</div>
            <p class="fgs-node-content">${esc(node.content || '没有附加内容。')}</p>
            <dl class="fgs-node-meta"><div><dt>优先级</dt><dd>${esc(node.priority == null ? 0 : node.priority)}</dd></div><div><dt>创建时间</dt><dd>${esc(formatTime(node.createdAt))}</dd></div></dl>
            ${evidence.length ? `<div class="fgs-evidence-title">证据</div><ul class="fgs-evidence-list">${evidence.map((item) => `<li>${esc(item)}</li>`).join('')}</ul>` : ''}
        `;
    }

    function renderSummary(data) {
        const snapshot = data && data.snapshot;
        const run = data && Array.isArray(data.runs) ? data.runs.find((item) => item.runId === data.runId) : null;
        setText('fgs-stats', snapshot ? `Nodes: ${snapshot.nodes.length} · Edges: ${snapshot.edges.length} · Version: ${snapshot.version} · Runs: ${(data && data.runs || []).length}` : 'Nodes: 0 · Edges: 0');
        setText('fgs-run-state', data && data.available ? `${data.active ? '运行中 · ' : ''}${graphStatusLabel(run && run.graphStatus)}` : '暂无运行');
        setText('fgs-goal-title', snapshot ? (snapshot.goal || '未命名目标') : '未加载 FGS');
        setText('fgs-goal-content', snapshot ? 'FGS 是当前任务的外置状态图；刷新后仍可从追加日志重建。' : '当前对话还没有可读取的 FGS 运行。');
        setText('fgs-goal-meta', snapshot ? `运行 ${data.runId} · 图版本 ${snapshot.version} · 更新于 ${formatTime(snapshot.updatedAt)}` : '');

        const select = document.getElementById('fgs-run-select');
        if (select && data && Array.isArray(data.runs)) {
            const previous = select.value || data.runId || '';
            select.innerHTML = '<option value="">当前/最新子对话</option>' + data.runs.map((item, index) => `<option value="${esc(item.runId)}">${esc(runOptionLabel(item, index, data.runs.length))}</option>`).join('');
            select.value = data.runId || previous || '';
            const picker = select.closest('.fgs-run-picker');
            if (picker) picker.title = `${data.runs.length} 个子对话/历史运行，可切换查看各自的 FGS 图`;
        }
    }

    function destroyGraph() {
        if (fgsCy) {
            saveViewport(currentViewportKey);
            fgsCy.destroy();
            fgsCy = null;
        }
    }

    function viewportStorageKey(conversationId, runId) {
        const conversation = String(conversationId || '').trim();
        const run = String(runId || 'latest').trim() || 'latest';
        if (!conversation) return '';
        return `${FGS_VIEWPORT_STORAGE_PREFIX}:${conversation}:${run}`;
    }

    function readViewport(key) {
        if (!key || !global.localStorage) return null;
        try {
            const value = JSON.parse(global.localStorage.getItem(key) || 'null');
            if (!value || !value.pan || !Number.isFinite(Number(value.pan.x)) || !Number.isFinite(Number(value.pan.y)) || !Number.isFinite(Number(value.zoom))) {
                return null;
            }
            const zoom = Math.max(0.3, Math.min(3, Number(value.zoom)));
            return { pan: { x: Number(value.pan.x), y: Number(value.pan.y) }, zoom };
        } catch (error) {
            return null;
        }
    }

    function saveViewport(key) {
        if (!key || !fgsCy || !global.localStorage) return;
        try {
            const pan = fgsCy.pan();
            const zoom = Number(fgsCy.zoom());
            if (!pan || !Number.isFinite(Number(pan.x)) || !Number.isFinite(Number(pan.y)) || !Number.isFinite(zoom)) return;
            global.localStorage.setItem(key, JSON.stringify({
                pan: { x: Number(pan.x), y: Number(pan.y) },
                zoom,
                savedAt: new Date().toISOString(),
            }));
        } catch (error) {
            // Private browsing or a full storage quota must not break the graph.
        }
    }

    function resetFGSViewport() {
        if (!fgsCy) return;
        if (currentViewportKey && global.localStorage) {
            try { global.localStorage.removeItem(currentViewportKey); } catch (error) { /* ignore */ }
        }
        fgsCy.fit(undefined, 32);
        saveViewport(currentViewportKey);
    }

    function bindViewportPersistence() {
        if (!fgsCy) return;
        fgsCy.on('pan zoom', () => saveViewport(currentViewportKey));
    }

    function renderGraph(snapshot, viewportKey) {
        const container = document.getElementById('fgs-graph-container');
        if (!container) return;
        destroyGraph();
        currentViewportKey = viewportKey || '';
        if (!snapshot || !Array.isArray(snapshot.nodes) || snapshot.nodes.length === 0 || typeof global.cytoscape !== 'function') {
            container.innerHTML = `<div class="fgs-empty-state">${typeof global.cytoscape !== 'function' ? '图形组件未加载，请刷新页面。' : '当前 FGS 还没有节点。'}</div>`;
            renderNodeDetails(null);
            return;
        }
        container.innerHTML = '';
        const nodes = snapshot.nodes.map((node) => {
            const kind = String(node.kind || 'hint').toLowerCase();
            const style = nodeStyle(kind);
            return { data: { id: String(node.id), label: shortText(node.label || node.content || node.id, 54), kind, color: style.color, status: node.status || 'pending', raw: node } };
        });
        const nodeIds = new Set(nodes.map((item) => item.data.id));
        const edges = (snapshot.edges || []).filter((edge) => nodeIds.has(String(edge.from)) && nodeIds.has(String(edge.to))).map((edge, index) => ({
            data: { id: `fgs-edge-${index}-${String(edge.from)}-${String(edge.to)}`, source: String(edge.from), target: String(edge.to), relation: edge.relation || '' },
        }));
        fgsCy = global.cytoscape({
            container,
            elements: nodes.concat(edges),
            style: [
                { selector: 'node', style: { label: 'data(label)', 'text-wrap': 'wrap', 'text-max-width': 190, 'font-size': 12, 'font-weight': 600, color: '#0f172a', 'text-valign': 'center', 'text-halign': 'center', width: 190, height: 64, shape: 'round-rectangle', 'background-color': 'data(color)', 'background-opacity': 0.13, 'border-width': 2, 'border-color': 'data(color)', 'padding': 8 } },
                { selector: 'node:selected', style: { 'border-width': 4, 'border-color': '#2563eb', 'background-opacity': 0.28 } },
                { selector: 'edge', style: { width: 2, 'line-color': '#94a3b8', 'target-arrow-color': '#64748b', 'target-arrow-shape': 'triangle', 'curve-style': 'bezier', label: 'data(relation)', 'font-size': 9, color: '#64748b', 'text-background-color': '#ffffff', 'text-background-opacity': 0.85, 'text-background-padding': 2 } },
            ],
            minZoom: 0.3,
            maxZoom: 3,
        });
        fgsCy.on('tap', 'node', (event) => {
            const node = event.target.data('raw');
            selectedNodeId = String(node.id);
            renderNodeDetails(node);
        });
        // Cytoscape does not load ELK as a layout plugin here; breadthfirst is
        // built in and is sufficient for the small, acyclic FGS state graph.
        fgsCy.layout({ name: 'breadthfirst', directed: true, padding: 36, spacingFactor: 1.2 }).run();
        const savedViewport = readViewport(currentViewportKey);
        if (savedViewport) {
            fgsCy.zoom(savedViewport.zoom);
            fgsCy.pan(savedViewport.pan);
        } else {
            fgsCy.fit(undefined, 32);
        }
        bindViewportPersistence();
        if (selectedNodeId) {
            const selected = fgsCy.getElementById(selectedNodeId);
            if (selected && selected.length) selected.select();
            const selectedRaw = snapshot.nodes.find((node) => String(node.id) === selectedNodeId);
            renderNodeDetails(selectedRaw || null);
        } else {
            renderNodeDetails(null);
        }
    }

    async function loadFGSGraph(conversationId, runId) {
        conversationId = String(conversationId || '').trim();
        if (!conversationId || typeof global.apiFetch !== 'function') return;
        const seq = ++loadSeq;
        global.currentFGSConversationId = conversationId;
        try {
            const query = runId ? `?run_id=${encodeURIComponent(runId)}` : '';
            const response = await global.apiFetch(`/api/conversations/${encodeURIComponent(conversationId)}/fgs${query}`);
            const data = await response.json().catch(() => ({}));
            if (seq !== loadSeq || global.currentFGSConversationId !== conversationId) return;
            if (!response.ok) throw new Error(data.error || 'FGS 加载失败');
            graphData = data;
            renderSummary(data);
            renderGraph(data.snapshot || null, viewportStorageKey(conversationId, data.runId));
        } catch (error) {
            if (seq !== loadSeq) return;
            setText('fgs-run-state', '加载失败');
            const container = document.getElementById('fgs-graph-container');
            if (container) container.innerHTML = `<div class="fgs-empty-state fgs-empty-state-error">${esc(error && error.message ? error.message : 'FGS 加载失败')}</div>`;
        }
    }

    function startRefresh() {
        stopRefresh();
        refreshTimer = global.setInterval(() => {
            if (global.isAppModalOpen && !global.isAppModalOpen('fgs-modal')) return;
            const id = String(global.currentFGSConversationId || '').trim();
            if (id) loadFGSGraph(id, document.getElementById('fgs-run-select')?.value || '');
        }, 2500);
    }

    function stopRefresh() {
        if (refreshTimer) {
            global.clearInterval(refreshTimer);
            refreshTimer = null;
        }
    }

    async function showFGSGraph(conversationId) {
        conversationId = String(conversationId || currentConversationId()).trim();
        if (!conversationId) return;
        const modal = document.getElementById('fgs-modal');
        if (!modal) return;
        global.currentFGSConversationId = conversationId;
        if (typeof global.openAppModal === 'function') global.openAppModal('fgs-modal', { focus: false });
        await loadFGSGraph(conversationId, '');
        startRefresh();
    }

    function refreshFGSGraph() {
        const id = String(global.currentFGSConversationId || '').trim();
        if (id) loadFGSGraph(id, document.getElementById('fgs-run-select')?.value || '');
    }

    function closeFGSGraph() {
        stopRefresh();
        ++loadSeq;
        destroyGraph();
        graphData = null;
        global.currentFGSConversationId = '';
        if (typeof global.closeAppModal === 'function') global.closeAppModal('fgs-modal');
    }

    global.updateFGSButton = updateFGSButton;
    global.showFGSGraph = showFGSGraph;
    global.loadFGSGraph = loadFGSGraph;
    global.refreshFGSGraph = refreshFGSGraph;
    global.resetFGSViewport = resetFGSViewport;
    global.closeFGSGraph = closeFGSGraph;
    document.addEventListener('DOMContentLoaded', updateFGSButton);
})(typeof window !== 'undefined' ? window : globalThis);
