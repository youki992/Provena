// 本地命令工具管理。普通 CLI 工具与 MCP 服务分开管理，配置文件仍由后端统一热加载。
(function () {
    'use strict';

    let currentEditingLocalToolName = null;
    let currentEditingLocalTool = null;

    function localToolsT(key, fallback, vars) {
        if (typeof window.t === 'function') {
            const value = window.t(key, vars);
            return value && value !== key ? value : fallback;
        }
        return fallback;
    }

    function localToolEscape(value) {
        return String(value ?? '')
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

    function localToolJsString(value) {
        return JSON.stringify(String(value ?? ''))
            .replace(/&/g, '&amp;')
            .replace(/</g, '\\u003c')
            .replace(/>/g, '\\u003e')
            .replace(/"/g, '&quot;');
    }

    function localToolErrorMessage(error, fallback) {
        return error && error.message ? error.message : fallback;
    }

    async function fetchLocalTools() {
        const response = await apiFetch('/api/local-tools');
        if (!response.ok) {
            let message = localToolsT('localTools.loadFailed', '加载本地工具失败');
            try {
                const body = await response.json();
                message = body.error || message;
            } catch (_) {}
            throw new Error(message);
        }
        return response.json();
    }

    function renderLocalTools(tools) {
        const list = document.getElementById('local-tools-list');
        if (!list) return;
        const items = Array.isArray(tools) ? tools : [];
        if (!items.length) {
            list.innerHTML = `<div class="empty">🧰 ${localToolsT('localTools.empty', '暂无本地工具')}<br><span style="font-size: 0.875rem; margin-top: 8px; display: block;">${localToolsT('localTools.emptyHint', '点击“添加本地工具”开始配置')}</span></div>`;
            return;
        }

        const enabledText = localToolsT('localTools.enabled', '已启用');
        const disabledText = localToolsT('localTools.disabled', '已停用');
        const enableText = localToolsT('localTools.enable', '启用');
        const disableText = localToolsT('localTools.disable', '停用');
        const editText = localToolsT('common.edit', '编辑');
        const deleteText = localToolsT('common.delete', '删除');
        const commandText = localToolsT('localTools.commandLabel', '命令');
        const argsText = localToolsT('localTools.argsLabel', '固定参数');
        const descriptionText = localToolsT('localTools.descriptionLabel', '用途说明');

        list.innerHTML = `<div class="external-mcp-items">${items.map(tool => {
            const name = String(tool.name || '');
            const enabled = tool.enabled !== false;
            const args = Array.isArray(tool.args) ? tool.args.join(' ') : '';
            return `
                <div class="external-mcp-item">
                    <div class="external-mcp-item-header">
                        <div class="external-mcp-item-info">
                            <h4>🧰 ${localToolEscape(name)}</h4>
                            <span class="external-mcp-status ${enabled ? 'status-connected' : 'status-disabled'}">${enabled ? enabledText : disabledText}</span>
                        </div>
                        <div class="external-mcp-item-actions">
                            <button class="btn-small" type="button" onclick="toggleLocalTool(${localToolJsString(name)})">${enabled ? '⏸ ' + disableText : '▶ ' + enableText}</button>
                            <button class="btn-small" type="button" onclick="editLocalTool(${localToolJsString(name)})">✏️ ${editText}</button>
                            <button class="btn-small btn-danger" type="button" onclick="deleteLocalTool(${localToolJsString(name)})">🗑 ${deleteText}</button>
                        </div>
                    </div>
                    <div class="external-mcp-item-details">
                        <div><strong>${commandText}</strong><span style="font-family: monospace; font-size: 0.8125rem; word-break: break-all;">${localToolEscape(tool.command)}</span></div>
                        ${args ? `<div><strong>${argsText}</strong><span style="font-family: monospace; font-size: 0.8125rem; word-break: break-all;">${localToolEscape(args)}</span></div>` : ''}
                        <div><strong>${descriptionText}</strong><span>${localToolEscape(tool.description || '')}</span></div>
                    </div>
                </div>`;
        }).join('')}</div>`;
    }

    async function loadLocalTools() {
        const list = document.getElementById('local-tools-list');
        if (list) list.innerHTML = `<div class="empty">${localToolsT('common.loading', '加载中...')}</div>`;
        try {
            if (window.i18nReady) await window.i18nReady;
            const data = await fetchLocalTools();
            renderLocalTools(data.tools || []);
        } catch (error) {
            console.error('加载本地工具失败:', error);
            if (list) {
                list.innerHTML = `<div class="error">${localToolEscape(localToolErrorMessage(error, localToolsT('localTools.loadFailed', '加载本地工具失败')))}</div>`;
            }
        }
    }

    function resetLocalToolForm(tool) {
        const current = tool || {};
        const fields = {
            'local-tool-name': current.name || '',
            'local-tool-command': current.command || '',
            'local-tool-args': Array.isArray(current.args) ? current.args.join('\n') : '',
            'local-tool-description': current.description || ''
        };
        Object.entries(fields).forEach(([id, value]) => {
            const element = document.getElementById(id);
            if (element) element.value = value;
        });
        const enabled = document.getElementById('local-tool-enabled');
        if (enabled) enabled.checked = current.enabled !== false;
        const error = document.getElementById('local-tool-error');
        if (error) {
            error.textContent = '';
            error.style.display = 'none';
        }
    }

    function showAddLocalToolModal() {
        if (typeof requirePermission === 'function' && !requirePermission('config:write')) return;
        currentEditingLocalToolName = null;
        currentEditingLocalTool = null;
        const nameInput = document.getElementById('local-tool-name');
        if (nameInput) nameInput.disabled = false;
        const title = document.getElementById('local-tool-modal-title');
        if (title) title.textContent = localToolsT('localTools.add', '添加本地工具');
        resetLocalToolForm();
        openAppModal('local-tool-modal');
    }

    function closeLocalToolModal() {
        closeAppModal('local-tool-modal');
        const nameInput = document.getElementById('local-tool-name');
        if (nameInput) nameInput.disabled = false;
        currentEditingLocalToolName = null;
        currentEditingLocalTool = null;
    }

    function editLocalTool(name) {
        if (typeof requirePermission === 'function' && !requirePermission('config:write')) return;
        fetchLocalTools().then(data => {
            const tool = (data.tools || []).find(item => item.name === name);
            if (!tool) throw new Error(localToolsT('localTools.notFound', '本地工具不存在'));
            currentEditingLocalToolName = name;
            currentEditingLocalTool = tool;
            const title = document.getElementById('local-tool-modal-title');
            if (title) title.textContent = localToolsT('localTools.edit', '编辑本地工具');
            resetLocalToolForm(tool);
            const nameInput = document.getElementById('local-tool-name');
            if (nameInput) nameInput.disabled = true;
            openAppModal('local-tool-modal');
        }).catch(error => {
            console.error('读取本地工具失败:', error);
            alert(localToolErrorMessage(error, localToolsT('localTools.loadFailed', '加载本地工具失败')));
        });
    }

    async function saveLocalTool() {
        if (typeof requirePermission === 'function' && !requirePermission('config:write')) return;
        const error = document.getElementById('local-tool-error');
        const nameInput = document.getElementById('local-tool-name');
        const commandInput = document.getElementById('local-tool-command');
        const argsInput = document.getElementById('local-tool-args');
        const descriptionInput = document.getElementById('local-tool-description');
        const enabledInput = document.getElementById('local-tool-enabled');
        const name = nameInput?.value.trim() || '';
        const command = commandInput?.value.trim() || '';
        const description = descriptionInput?.value.trim() || '';
        const args = (argsInput?.value || '').split(/\r?\n/).map(value => value.trim()).filter(Boolean);
        const showError = message => {
            if (error) {
                error.textContent = message;
                error.style.display = 'block';
            }
        };
        if (!name || !command || !description) {
            showError(localToolsT('localTools.required', '请填写工具名称、命令和用途说明'));
            return;
        }

        const payload = { name, command, args, description, enabled: enabledInput ? enabledInput.checked : true };
        const endpoint = currentEditingLocalToolName
            ? `/api/local-tools/${encodeURIComponent(currentEditingLocalToolName)}`
            : '/api/local-tools';
        try {
            const response = await apiFetch(endpoint, {
                method: currentEditingLocalToolName ? 'PUT' : 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload)
            });
            if (!response.ok) {
                let message = localToolsT('localTools.saveFailed', '保存本地工具失败');
                try {
                    const body = await response.json();
                    message = body.error || message;
                } catch (_) {}
                throw new Error(message);
            }
            closeLocalToolModal();
            await loadLocalTools();
            if (typeof loadToolsList === 'function') await loadToolsList(1, '');
            if (typeof window.refreshMentionTools === 'function') window.refreshMentionTools();
            alert(localToolsT('localTools.saveSuccess', '本地工具已保存'));
        } catch (err) {
            console.error('保存本地工具失败:', err);
            showError(localToolErrorMessage(err, localToolsT('localTools.saveFailed', '保存本地工具失败')));
        }
    }

    async function deleteLocalTool(name) {
        if (typeof requirePermission === 'function' && !requirePermission('config:write')) return;
        const confirmText = localToolsT('localTools.deleteConfirm', '确定要删除本地工具“{{name}}”吗？', { name });
        if (!window.confirm(confirmText)) return;
        try {
            const response = await apiFetch(`/api/local-tools/${encodeURIComponent(name)}`, { method: 'DELETE' });
            if (!response.ok) {
                let message = localToolsT('localTools.deleteFailed', '删除本地工具失败');
                try {
                    const body = await response.json();
                    message = body.error || message;
                } catch (_) {}
                throw new Error(message);
            }
            await loadLocalTools();
            if (typeof loadToolsList === 'function') await loadToolsList(1, '');
            if (typeof window.refreshMentionTools === 'function') window.refreshMentionTools();
            alert(localToolsT('localTools.deleteSuccess', '本地工具已删除'));
        } catch (error) {
            console.error('删除本地工具失败:', error);
            alert(localToolErrorMessage(error, localToolsT('localTools.deleteFailed', '删除本地工具失败')));
        }
    }

    async function toggleLocalTool(name) {
        if (typeof requirePermission === 'function' && !requirePermission('config:write')) return;
        try {
            const data = await fetchLocalTools();
            const tool = (data.tools || []).find(item => item.name === name);
            if (!tool) throw new Error(localToolsT('localTools.notFound', '本地工具不存在'));
            const response = await apiFetch(`/api/local-tools/${encodeURIComponent(name)}`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    name: tool.name,
                    command: tool.command,
                    args: Array.isArray(tool.args) ? tool.args : [],
                    description: tool.description || '',
                    enabled: tool.enabled === false
                })
            });
            if (!response.ok) {
                let message = localToolsT('localTools.saveFailed', '保存本地工具失败');
                try {
                    const body = await response.json();
                    message = body.error || message;
                } catch (_) {}
                throw new Error(message);
            }
            await loadLocalTools();
            if (typeof loadToolsList === 'function') await loadToolsList(1, '');
            if (typeof window.refreshMentionTools === 'function') window.refreshMentionTools();
        } catch (error) {
            console.error('切换本地工具状态失败:', error);
            alert(localToolErrorMessage(error, localToolsT('localTools.saveFailed', '保存本地工具失败')));
        }
    }

    window.loadLocalTools = loadLocalTools;
    window.showAddLocalToolModal = showAddLocalToolModal;
    window.closeLocalToolModal = closeLocalToolModal;
    window.editLocalTool = editLocalTool;
    window.saveLocalTool = saveLocalTool;
    window.deleteLocalTool = deleteLocalTool;
    window.toggleLocalTool = toggleLocalTool;
})();
