(function () {
    const packetState = {
        groups: [],
        chatQuery: '',
        pageQuery: '',
        selectedIds: new Set()
    };
    function esc(value) { return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) { return ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]); }); }
    async function jsonFetch(url, options) { const response = await apiFetch(url, options || {}); const data = await response.json().catch(function () { return {}; }); if (!response.ok) throw new Error(data.error || ('请求失败: ' + response.status)); return data; }
    function groupsFrom(data) { return Array.isArray(data.groups) ? data.groups : []; }
    function groupMatches(group, query) {
        query = String(query || '').trim().toLocaleLowerCase();
        if (!query) return true;
        return [group.name, group.description, group.fingerprint, group.id]
            .map(function (value) { return String(value == null ? '' : value).toLocaleLowerCase(); })
            .some(function (value) { return value.indexOf(query) !== -1; });
    }
    function visibleGroups(query) { return packetState.groups.filter(function (group) { return groupMatches(group, query); }); }
    function updateSelectionCount(visibleCount) {
        const el = document.getElementById('chat-packet-groups-count');
        const total = packetState.groups.length;
        const selected = packetState.selectedIds.size;
        if (el) el.textContent = selected ? ('已选 ' + selected + ' / 当前 ' + visibleCount + ' / 共 ' + total) : ('当前 ' + visibleCount + ' / 共 ' + total);
        const selectAllButton = document.getElementById('chat-packet-select-all-filtered');
        if (selectAllButton) {
            const visible = visibleGroups(packetState.chatQuery);
            const allSelected = visible.length > 0 && visible.every(function (group) { return packetState.selectedIds.has(group.id); });
            selectAllButton.textContent = allSelected ? '取消当前筛选' : '全选当前筛选';
            selectAllButton.title = allSelected ? '取消选择当前筛选结果中的抓包分组' : '选择当前筛选结果中的全部抓包分组';
        }
        const badge = document.getElementById('chat-packet-picker-badge');
        if (badge) {
            badge.textContent = String(selected);
            badge.hidden = selected === 0;
        }
    }
    function syncSelectedIdsFromSelect() {
        const select = document.getElementById('chat-packet-groups');
        if (!select) return;
        Array.from(select.selectedOptions || []).forEach(function (option) { packetState.selectedIds.add(option.value); });
    }
    function syncVisibleSelectionFromSelect() {
        const select = document.getElementById('chat-packet-groups');
        if (!select) return;
        const selected = new Set(Array.from(select.selectedOptions || []).map(function (option) { return option.value; }));
        visibleGroups(packetState.chatQuery).forEach(function (group) {
            if (selected.has(group.id)) packetState.selectedIds.add(group.id);
            else packetState.selectedIds.delete(group.id);
        });
    }
    function renderChatGroupOptions() {
        const select = document.getElementById('chat-packet-groups');
        if (!select) return;
        const groups = visibleGroups(packetState.chatQuery);
        select.innerHTML = groups.map(function (g) {
            return '<option value="' + esc(g.id) + '"' + (packetState.selectedIds.has(g.id) ? ' selected' : '') + '>' + esc(g.name) + ' · ' + esc(g.request_count) + ' 包</option>';
        }).join('');
        select.title = groups.length ? '选择后，本次 Pi 对话将收到完整原始请求/响应包' : '暂无匹配的抓包分组';
        updateSelectionCount(groups.length);
    }
    function renderGroupOptions(groups) {
        packetState.groups = groups;
        const validIds = new Set(groups.map(function (group) { return group.id; }));
        packetState.selectedIds = new Set(Array.from(packetState.selectedIds).filter(function (id) { return validIds.has(id); }));
        renderChatGroupOptions();
    }
    window.getSelectedPacketGroupIDs = function () { return Array.from(packetState.selectedIds); };
    window.filterChatPacketGroups = function (query) { packetState.chatQuery = query || ''; renderChatGroupOptions(); };
    window.selectAllFilteredPacketGroups = function () {
        const visible = visibleGroups(packetState.chatQuery);
        const allSelected = visible.length > 0 && visible.every(function (group) { return packetState.selectedIds.has(group.id); });
        visible.forEach(function (group) {
            if (allSelected) packetState.selectedIds.delete(group.id);
            else packetState.selectedIds.add(group.id);
        });
        renderChatGroupOptions();
    };
    window.clearSelectedPacketGroups = function () {
        packetState.selectedIds.clear();
        const select = document.getElementById('chat-packet-groups');
        if (select) Array.from(select.options || []).forEach(function (option) { option.selected = false; });
        renderChatGroupOptions();
    };
    window.loadChatPacketGroups = async function () { try { renderGroupOptions(groupsFrom(await jsonFetch('/api/packet-groups?limit=500'))); } catch (e) { console.warn('加载抓包分组失败', e); } };
    window.closeChatPacketPicker = function () {
        const panel = document.getElementById('chat-packet-picker-panel');
        const button = document.getElementById('chat-packet-picker-btn');
        if (panel) panel.hidden = true;
        if (button) {
            button.classList.remove('active');
            button.setAttribute('aria-expanded', 'false');
        }
    };
    function layoutChatPacketPicker() {
        const panel = document.getElementById('chat-packet-picker-panel');
        const button = document.getElementById('chat-packet-picker-btn');
        const container = document.getElementById('chat-input-container');
        if (!panel || !button || panel.hidden || window.matchMedia('(max-width: 760px)').matches) {
            if (panel) {
                panel.style.left = '';
                panel.style.right = '';
                panel.style.width = '';
            }
            return;
        }
        const buttonRect = button.getBoundingClientRect();
        const containerRect = container ? container.getBoundingClientRect() : { left: 12, right: window.innerWidth - 12 };
        const desiredWidth = Math.min(520, window.innerWidth - 24);
        const spaceRight = Math.max(0, containerRect.right - buttonRect.left);
        const spaceLeft = Math.max(0, buttonRect.right - containerRect.left);
        const openRight = spaceRight >= spaceLeft;
        panel.style.left = openRight ? '0' : 'auto';
        panel.style.right = openRight ? 'auto' : '0';
        panel.style.width = Math.min(desiredWidth, openRight ? spaceRight : spaceLeft) + 'px';
    }
    window.toggleChatPacketPicker = function () {
        const panel = document.getElementById('chat-packet-picker-panel');
        const button = document.getElementById('chat-packet-picker-btn');
        if (!panel || !button) return;
        if (!panel.hidden) {
            window.closeChatPacketPicker();
            return;
        }
        panel.hidden = false;
        button.classList.add('active');
        button.setAttribute('aria-expanded', 'true');
        layoutChatPacketPicker();
        window.loadChatPacketGroups();
        const search = document.getElementById('chat-packet-group-search');
        if (search) window.setTimeout(function () { search.focus(); }, 0);
    };
    window.downloadPacketCaptureCA = async function () {
        try {
            const response = await apiFetch('/api/packet-capture/ca/download');
            if (!response.ok) {
                const data = await response.json().catch(function () { return {}; });
                throw new Error(data.error || ('请求失败: ' + response.status));
            }
            const blob = await response.blob();
            const url = URL.createObjectURL(blob);
            const link = document.createElement('a');
            link.href = url;
            link.download = 'provena-ca.crt';
            document.body.appendChild(link);
            link.click();
            link.remove();
            setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
        } catch (e) {
            alert(e.message);
        }
    };
    window.installPacketCaptureCA = async function () { try { await jsonFetch('/api/packet-capture/ca/install-windows', { method: 'POST' }); alert('平台 CA 已安装'); } catch (e) { alert(e.message); } };
    function formatPacketBody(value) {
        if (value == null || value === '') return '（空）';
        if (typeof value === 'string') return value;
        return JSON.stringify(value, null, 2);
    }
    function renderPacketDetail(data, groupName) {
        const target = document.getElementById('packet-capture-detail');
        if (!target) return;
        const packets = Array.isArray(data.packets) ? data.packets : [];
        target.hidden = false;
        target.innerHTML = '<div class="packet-capture-detail-head"><div><strong>' + esc(groupName || '请求包详情') + '</strong><span>' + packets.length + ' 条记录</span></div><button type="button" class="btn-ghost btn-small" onclick="closePacketGroupDetail()">关闭</button></div>' + (packets.length ? packets.map(function (p, index) {
            const url = p.scheme + '://' + p.host + (p.port && !((p.scheme === 'https' && p.port === 443) || (p.scheme === 'http' && p.port === 80)) ? ':' + p.port : '') + (p.path || '/') + (p.query ? '?' + p.query : '');
            return '<details class="packet-capture-packet"' + (index === 0 ? ' open' : '') + '><summary><span class="packet-method packet-method--' + esc(String(p.method || '').toLowerCase()) + '">' + esc(p.method) + '</span><span class="packet-url">' + esc(url) + '</span><span class="packet-status">' + esc(p.response_status) + '</span></summary><div class="packet-capture-packet-grid"><div><h4>请求头</h4><pre>' + esc(formatPacketBody(p.request_headers)) + '</pre></div><div><h4>响应头</h4><pre>' + esc(formatPacketBody(p.response_headers)) + '</pre></div><div><h4>请求体</h4><pre>' + esc(formatPacketBody(p.request_body)) + '</pre></div><div><h4>响应体</h4><pre>' + esc(formatPacketBody(p.response_body)) + '</pre></div></div></details>';
        }).join('') : '<div class="empty-state">分组暂无请求</div>');
        target.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    }
    window.closePacketGroupDetail = function () { const target = document.getElementById('packet-capture-detail'); if (target) { target.hidden = true; target.innerHTML = ''; } };
    window.loadPacketGroups = async function () {
        const target = document.getElementById('packet-capture-groups'); if (!target) return;
        try { const groups = groupsFrom(await jsonFetch('/api/packet-groups?limit=500')); packetState.groups = groups; renderGroupOptions(groups); const filtered = visibleGroups(packetState.pageQuery); target.innerHTML = filtered.length ? filtered.map(function (g) { return '<button class="packet-capture-group" onclick="showPacketGroup(\'' + String(g.id).replace(/'/g, '') + '\')"><span class="packet-capture-group-main"><strong>' + esc(g.name) + '</strong><small>' + esc(g.description || '按方法、主机和路径自动归纳') + '</small></span><span class="packet-capture-group-meta"><b>' + esc(g.request_count) + '</b> 包 · 最近 ' + esc(g.last_seen_at) + '<i>查看详情 →</i></span></button>'; }).join('') : '<div class="empty-state">' + (packetState.pageQuery ? '当前筛选条件下暂无分组' : '暂无抓包，请先配置 Yakit / Burp 代理。') + '</div>'; } catch (e) { target.textContent = e.message; }
    };
    window.filterPacketCaptureGroups = function (query) { packetState.pageQuery = query || ''; window.loadPacketGroups(); };
    window.showPacketGroup = async function (id) { try { const group = packetState.groups.find(function (item) { return item.id === id; }); const data = await jsonFetch('/api/packet-groups/' + encodeURIComponent(id) + '/packets?limit=100'); renderPacketDetail(data, group ? group.name : '请求包详情'); } catch (e) { alert(e.message); } };
    window.initPacketCapturePage = async function () {
        try { const data = await jsonFetch('/api/packet-capture/config'); const cfg = data.config || {}; document.getElementById('packet-capture-host').value = cfg.host || '127.0.0.1'; document.getElementById('packet-capture-port').value = cfg.port || 9081; document.getElementById('packet-capture-max-body').value = cfg.max_body_bytes || 33554432; document.getElementById('packet-capture-enabled').checked = !!cfg.enabled; document.getElementById('packet-capture-status').textContent = data.running ? '运行中：' + data.address : '已停止'; const ca = await jsonFetch('/api/packet-capture/ca'); document.getElementById('packet-capture-install').textContent = 'Windows 安装：' + ca.install_windows + '\nmacOS 安装：' + ca.install_macos; } catch (e) { document.getElementById('packet-capture-status').textContent = e.message; }
        await window.loadChatPacketGroups(); await window.loadPacketGroups();
    };
    window.savePacketCaptureConfig = async function () { const cfg = { enabled: document.getElementById('packet-capture-enabled').checked, host: document.getElementById('packet-capture-host').value.trim(), port: Number(document.getElementById('packet-capture-port').value), max_body_bytes: Number(document.getElementById('packet-capture-max-body').value), send_full_to_pi: true }; try { const data = await jsonFetch('/api/packet-capture/config', { method: 'PUT', headers: {'Content-Type':'application/json'}, body: JSON.stringify(cfg) }); document.getElementById('packet-capture-status').textContent = data.running ? '运行中：' + data.address : '已停止'; alert('抓包配置已应用'); } catch (e) { alert(e.message); } };
    document.addEventListener('DOMContentLoaded', function () {
        const select = document.getElementById('chat-packet-groups');
        if (select) select.addEventListener('change', function () { syncVisibleSelectionFromSelect(); renderChatGroupOptions(); });
        window.loadChatPacketGroups();
    });
    document.addEventListener('click', function (event) {
        const wrapper = document.getElementById('chat-packet-picker-wrapper');
        const panel = document.getElementById('chat-packet-picker-panel');
        if (wrapper && panel && !panel.hidden && !wrapper.contains(event.target)) window.closeChatPacketPicker();
    });
    document.addEventListener('keydown', function (event) {
        if (event.key === 'Escape') window.closeChatPacketPicker();
    });
    window.addEventListener('resize', layoutChatPacketPicker);
})();
