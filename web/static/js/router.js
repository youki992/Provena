// 页面路由管理
let currentPage = null;
const SIMPLE_NAV_PAGES = new Set(['chat', 'projects', 'asset-overview', 'asset-library', 'vulnerabilities', 'mcp-management', 'local-tools-management', 'packet-capture', 'settings', 'mcp-monitor', 'platform-rbac', 'skills-management', 'roles-management', 'hitl']);

function normalizePageId(pageId) {
    return pageId === 'c2' ? 'c2-listeners' : pageId;
}

function isSupportedPage(pageId) {
    return SIMPLE_NAV_PAGES.has(pageId);
}

function getNavPageId(pageId) {
    if (pageId === 'mcp-monitor') return 'mcp-management';
    if (pageId === 'platform-rbac') return 'settings';
    if (pageId === 'asset-overview' || pageId === 'asset-library') return 'assets';
    return pageId;
}

/** chat、项目、漏洞结果页在切换时保留当前 hash 上的查询串 */
function buildHashForPage(pageId) {
    if (pageId !== 'chat' && pageId !== 'projects' && pageId !== 'vulnerabilities') {
        return pageId;
    }
    const full = window.location.hash.slice(1);
    const parts = full.split('?');
    const curPage = parts[0];
    const q = parts.length > 1 ? parts.slice(1).join('?') : '';
    if (curPage === pageId && q) {
        return pageId + '?' + q;
    }
    return pageId;
}

let chatConversationFromHashSeq = 0;
function scheduleChatConversationFromHash(delayMs) {
    const hash = window.location.hash.slice(1);
    const hashParts = hash.split('?');
    if (hashParts[0] !== 'chat' || hashParts.length < 2) {
        return;
    }
    const params = new URLSearchParams(hashParts.slice(1).join('?'));
    const conversationId = params.get('conversation');
    const projectId = params.get('project');
    if (projectId && typeof setActiveProjectId === 'function') {
        setActiveProjectId(projectId);
        if (typeof refreshChatProjectSelector === 'function') {
            refreshChatProjectSelector();
        }
    }
    if (!conversationId) {
        return;
    }
    const token = ++chatConversationFromHashSeq;
    setTimeout(() => {
        if (token !== chatConversationFromHashSeq) {
            return;
        }
        if (typeof loadConversation === 'function') {
            loadConversation(conversationId);
        } else if (typeof window.loadConversation === 'function') {
            window.loadConversation(conversationId);
        } else {
            console.warn('loadConversation function not found');
        }
    }, delayMs);
}

/** 跳转到指定对话：单次切页 + 单次加载，避免 hashchange 与手动 load 重复触发导致闪烁 */
function navigateToConversation(conversationId) {
    const cid = String(conversationId || '').trim();
    if (!cid) return;
    const targetHash = 'chat?conversation=' + encodeURIComponent(cid);
    const alreadyOnChat = currentPage === 'chat';

    if (window.location.hash.slice(1) !== targetHash) {
        history.replaceState(null, '', '#' + targetHash);
    }

    if (!alreadyOnChat) {
        switchPage('chat');
    }

    if (typeof loadConversation === 'function') {
        void loadConversation(cid);
    } else if (typeof window.loadConversation === 'function') {
        void window.loadConversation(cid);
    }
}
window.navigateToConversation = navigateToConversation;

// 初始化路由
function initRouter() {
    // 从URL hash读取页面（如果有）
    const hash = window.location.hash.slice(1);
    if (hash) {
        const hashParts = hash.split('?');
        const pageId = normalizePageId(hashParts[0]);
        if (pageId && isSupportedPage(pageId)) {
            switchPage(pageId);
            if (pageId === 'chat') {
                scheduleChatConversationFromHash(500);
            }
            return;
        }
    }
    
    // 默认显示对话
    switchPage('chat');
}

// 切换页面
function switchPage(pageId) {
    if (!isSupportedPage(pageId)) return;
    const targetPage = document.getElementById(`page-${pageId}`);
    if (!targetPage) return;

    // 导航点击会修改 hash，随后浏览器还会触发 hashchange。
    // 同一页面已经激活时不再重复初始化，避免接口重复请求和页面二次重绘。
    if (currentPage === pageId && targetPage.classList.contains('active')) {
        const currentHash = buildHashForPage(pageId);
        if (window.location.hash.slice(1) !== currentHash) {
            window.location.hash = currentHash;
        }
        return;
    }

    if (typeof window.syncC2NavOnceFromServer === 'function') {
        void window.syncC2NavOnceFromServer();
    }
    // 隐藏所有页面
    document.querySelectorAll('.page').forEach(page => {
        page.classList.remove('active');
    });
    
    // 显示目标页面
    targetPage.classList.add('active');
    currentPage = pageId;
        
    const newHash = buildHashForPage(pageId);
    if (window.location.hash.slice(1) !== newHash) {
        window.location.hash = newHash;
    }
        
    // 更新导航状态
    updateNavState(pageId);
        
    // 页面特定的初始化
    initPage(pageId);

    if (typeof applyRBACToUI === 'function') {
        applyRBACToUI(targetPage);
    }
}
window.switchPage = switchPage;

// 更新导航状态
function updateNavState(pageId) {
    document.querySelectorAll('.nav-item').forEach(item => {
        item.classList.remove('active');
        item.classList.remove('expanded');
    });
    document.querySelectorAll('.nav-submenu-item').forEach(item => {
        item.classList.remove('active');
    });
    const navPageId = getNavPageId(pageId);
    const navItem = document.querySelector(`.nav-item[data-page="${navPageId}"]`);
    if (navItem) {
        navItem.classList.add('active');
        const submenuItem = navItem.querySelector(`.nav-submenu-item[data-page="${pageId}"]`);
        if (submenuItem) {
            submenuItem.classList.add('active');
            navItem.classList.add('expanded');
        }
    }
}

function getSimplifiedSidebarLocale() {
    try {
        const stored = localStorage.getItem('csai_lang');
        if (stored === 'en-US' || stored === 'zh-CN') {
            return stored;
        }
    } catch (e) {
        // ignore
    }
    const navLang = (navigator.language || navigator.userLanguage || '').toLowerCase();
    return navLang.indexOf('en') === 0 ? 'en-US' : 'zh-CN';
}

function rebuildSimplifiedSidebarNav() {
    const nav = document.querySelector('.main-sidebar-nav');
    if (!nav) return;

    const locale = getSimplifiedSidebarLocale();
    const labels = locale === 'en-US'
        ? {
            workbench: 'Workspace',
            operations: 'Security Operations',
            administration: 'Administration',
            chat: 'Chat',
            factBoard: 'Fact Board',
            assets: 'Asset Management',
            assetOverview: 'Asset Overview',
            assetLibrary: 'Asset Library',
            vulnerabilities: 'Vulnerabilities',
            mcpManagement: 'MCP Management',
            localToolsManagement: 'Local Tools',
            packetCapture: 'Packet Capture',
            skillsManagement: 'Skill Management',
            rolesManagement: 'Role Management',
            settings: 'Settings'
        }
        : {
            workbench: '工作台',
            operations: '安全作业',
            administration: '平台管理',
            chat: '对话',
            factBoard: '事实黑板',
            assets: '资产管理',
            assetOverview: '资产概览',
            assetLibrary: '资产库',
            vulnerabilities: '漏洞结果',
            mcpManagement: 'MCP管理',
            localToolsManagement: '本地工具',
            packetCapture: '抓包测试',
            skillsManagement: '技能管理',
            rolesManagement: '角色管理',
            settings: '设置'
        };

    nav.innerHTML = `
        <div class="nav-section-label" role="presentation">
            <span data-i18n="navGroups.workbench">${labels.workbench}</span>
        </div>
        <div class="nav-item" data-page="chat">
            <div class="nav-item-content" data-title="${labels.chat}" onclick="switchPage('chat')" data-i18n="nav.chat" data-i18n-attr="data-title" data-i18n-skip-text="true">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"></path>
                </svg>
                <span data-i18n="nav.chat">${labels.chat}</span>
            </div>
        </div>
        <div class="nav-section-label" role="presentation">
            <span data-i18n="navGroups.operations">${labels.operations}</span>
        </div>
        <div class="nav-item" data-page="projects">
            <div class="nav-item-content" data-title="${labels.factBoard}" onclick="switchPage('projects')" data-i18n="nav.factBoard" data-i18n-attr="data-title" data-i18n-skip-text="true">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M8 6h11"></path>
                    <path d="M5 12h10"></path>
                    <path d="M10 18h9"></path>
                </svg>
                <span data-i18n="nav.factBoard">${labels.factBoard}</span>
            </div>
        </div>
        <div class="nav-item nav-item-has-submenu" data-page="assets" data-require-permission-any="asset:read fofa:execute">
            <div class="nav-item-content" data-title="${labels.assets}" onclick="window.toggleSubmenu('assets')" data-i18n="nav.assets" data-i18n-attr="data-title" data-i18n-skip-text="true">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M3 7l9-4 9 4-9 4-9-4z"></path><path d="M3 12l9 4 9-4"></path><path d="M3 17l9 4 9-4"></path>
                </svg>
                <span data-i18n="nav.assets">${labels.assets}</span>
                <svg class="submenu-arrow" width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="m9 18 6-6-6-6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
            </div>
            <div class="nav-submenu">
                <div class="nav-submenu-item" data-page="asset-overview" onclick="switchPage('asset-overview')" data-i18n="nav.assetOverview">${labels.assetOverview}</div>
                <div class="nav-submenu-item" data-page="asset-library" onclick="switchPage('asset-library')" data-i18n="nav.assetLibrary">${labels.assetLibrary}</div>
            </div>
        </div>
        <div class="nav-item" data-page="vulnerabilities">
            <div class="nav-item-content" data-title="${labels.vulnerabilities}" onclick="switchPage('vulnerabilities')" data-i18n="nav.vulnerabilities" data-i18n-attr="data-title" data-i18n-skip-text="true">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"></path>
                    <path d="M9 12l2 2 4-4"></path>
                </svg>
                <span data-i18n="nav.vulnerabilities">${labels.vulnerabilities}</span>
            </div>
        </div>
        <div class="nav-item" data-page="mcp-management">
            <div class="nav-item-content" data-title="${labels.mcpManagement}" onclick="switchPage('mcp-management')" data-i18n="nav.mcpManagement" data-i18n-attr="data-title" data-i18n-skip-text="true">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <rect x="4" y="4" width="16" height="5" rx="1.5"></rect>
                    <rect x="4" y="10" width="10" height="5" rx="1.5"></rect>
                    <rect x="4" y="16" width="16" height="4" rx="1.5"></rect>
                </svg>
                <span data-i18n="nav.mcpManagement">${labels.mcpManagement}</span>
            </div>
        </div>
        <div class="nav-item" data-page="local-tools-management" data-require-permission="config:read">
            <div class="nav-item-content" data-title="${labels.localToolsManagement}" onclick="switchPage('local-tools-management')" data-i18n="nav.localToolsManagement" data-i18n-attr="data-title" data-i18n-skip-text="true">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <rect x="3" y="4" width="18" height="16" rx="2"></rect>
                    <path d="M7 8h10M7 12h6M7 16h3"></path>
                </svg>
                <span data-i18n="nav.localToolsManagement">${labels.localToolsManagement}</span>
            </div>
        </div>
        <div class="nav-item" data-page="packet-capture" data-require-permission="config:read">
            <div class="nav-item-content" data-title="${labels.packetCapture}" onclick="switchPage('packet-capture')" data-i18n="nav.packetCapture" data-i18n-attr="data-title" data-i18n-skip-text="true">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 4h16v16H4z"></path><path d="M8 8h8M8 12h5M8 16h8"></path></svg>
                <span data-i18n="nav.packetCapture">${labels.packetCapture}</span>
            </div>
        </div>
        <div class="nav-section-label" role="presentation">
            <span data-i18n="navGroups.administration">${labels.administration}</span>
        </div>
        <div class="nav-item" data-page="skills-management" data-require-permission="skills:read">
            <div class="nav-item-content" data-title="${labels.skillsManagement}" onclick="switchPage('skills-management')" data-i18n="nav.skillsManagement" data-i18n-attr="data-title" data-i18n-skip-text="true">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5L14.5 2z"></path>
                    <polyline points="14 2 14 8 20 8"></polyline>
                    <path d="M8 13h8M8 17h5"></path>
                </svg>
                <span data-i18n="nav.skillsManagement">${labels.skillsManagement}</span>
            </div>
        </div>
        <div class="nav-item" data-page="roles-management" data-require-permission="roles:read">
            <div class="nav-item-content" data-title="${labels.rolesManagement}" onclick="switchPage('roles-management')" data-i18n="nav.rolesManagement" data-i18n-attr="data-title" data-i18n-skip-text="true">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"></path>
                    <circle cx="9" cy="7" r="4"></circle>
                    <path d="M22 21v-2a4 4 0 0 0-3-3.87"></path>
                    <path d="M16 3.13a4 4 0 0 1 0 7.75"></path>
                </svg>
                <span data-i18n="nav.rolesManagement">${labels.rolesManagement}</span>
            </div>
        </div>
        <div class="nav-item" data-page="settings">
            <div class="nav-item-content" data-title="${labels.settings}" onclick="switchPage('settings')" data-i18n="nav.settings" data-i18n-attr="data-title" data-i18n-skip-text="true">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <circle cx="12" cy="12" r="3"></circle>
                    <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"></path>
                </svg>
                <span data-i18n="nav.settings">${labels.settings}</span>
            </div>
        </div>
    `;

    nav.style.visibility = 'visible';
    if (typeof applyRBACToUI === 'function') {
        applyRBACToUI(nav);
    }
    if (typeof window.applyTranslations === 'function') {
        window.applyTranslations(nav);
    } else if (window.i18nReady && typeof window.i18nReady.then === 'function') {
        window.i18nReady.then(() => {
            if (typeof window.applyTranslations === 'function') {
                window.applyTranslations(nav);
            }
        }).catch(() => {});
    }
}

/** 读取侧栏子菜单项（仅 .nav-submenu 内，避免误匹配） */
function getNavSubmenuItems(navItem) {
    if (!navItem) return [];
    const submenu = navItem.querySelector('.nav-submenu');
    if (!submenu) return [];
    return Array.from(submenu.querySelectorAll('.nav-submenu-item'));
}

// 切换子菜单
function toggleSubmenu(menuId) {
    const sidebar = document.getElementById('main-sidebar');
    const navItem = document.querySelector(`.nav-item[data-page="${menuId}"]`);
    
    if (!navItem) return;
    
    const collapsed = sidebar && sidebar.classList.contains('collapsed');

    // 检查侧边栏是否折叠
    if (collapsed) {
        // 折叠状态下显示弹出菜单
        showSubmenuPopup(navItem, menuId);
        return;
    }

    // 展开状态下切换子菜单，并滚入视口以便看到子项
    const willExpand = !navItem.classList.contains('expanded');
    navItem.classList.toggle('expanded');
    if (willExpand) {
        requestAnimationFrame(() => {
            navItem.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
            const items = getNavSubmenuItems(navItem);
            const last = items[items.length - 1];
            if (last) {
                last.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
            }
        });
    }
}
window.toggleSubmenu = toggleSubmenu;

// 显示子菜单弹出框
function showSubmenuPopup(navItem, menuId) {
    const existingPopup = document.querySelector('.submenu-popup');
    if (existingPopup) {
        const sameMenu = existingPopup.dataset.menuId === menuId;
        existingPopup.remove();
        // 再次点击同一项：仅关闭；点击另一项：继续打开新菜单
        if (sameMenu) {
            return;
        }
    }

    const navItemContent = navItem.querySelector('.nav-item-content');
    const submenu = navItem.querySelector('.nav-submenu');
    
    if (!submenu) return;
    
    // 获取菜单位置
    const rect = navItemContent.getBoundingClientRect();
    
    // 创建弹出菜单
    const popup = document.createElement('div');
    popup.className = 'submenu-popup';
    popup.dataset.menuId = menuId;
    popup.style.position = 'fixed';
    popup.style.left = (rect.right + 8) + 'px';
    popup.style.top = rect.top + 'px';
    popup.style.zIndex = '1000';
    
    // 复制子菜单项到弹出菜单
    const submenuItems = submenu.querySelectorAll('.nav-submenu-item');
    submenuItems.forEach(item => {
        const popupItem = document.createElement('div');
        popupItem.className = 'submenu-popup-item';
        popupItem.textContent = item.textContent.trim();
        
        // 检查是否是当前激活的页面
        const pageId = item.getAttribute('data-page');
        if (pageId && document.querySelector(`.nav-submenu-item[data-page="${pageId}"].active`)) {
            popupItem.classList.add('active');
        }
        
        popupItem.onclick = function(e) {
            e.stopPropagation();
            e.preventDefault();
            
            // 获取页面ID并切换
            const pageId = item.getAttribute('data-page');
            if (pageId) {
                switchPage(pageId);
            }
            
            // 关闭弹出菜单
            popup.remove();
            document.removeEventListener('click', closePopup);
        };
        popup.appendChild(popupItem);
    });
    
    document.body.appendChild(popup);
    
    // 点击外部关闭弹出菜单
    const closePopup = function(e) {
        if (!popup.contains(e.target) && !navItem.contains(e.target)) {
            popup.remove();
            document.removeEventListener('click', closePopup);
        }
    };
    
    // 延迟添加事件监听，避免立即触发
    setTimeout(() => {
        document.addEventListener('click', closePopup);
    }, 0);
}

// 初始化页面
async function initPage(pageId) {
    // 等待 i18n 就绪，避免快速刷新时翻译函数未初始化导致页面显示原始占位符 key
    if (window.i18nReady) await window.i18nReady;
    if (typeof stopExternalMcpPoll === 'function') {
        stopExternalMcpPoll();
    }
    if (typeof stopAssetAutoRefresh === 'function') {
        stopAssetAutoRefresh();
    }
    switch(pageId) {
        case 'dashboard':
            if (typeof refreshDashboard === 'function') {
                refreshDashboard();
            }
            break;
        case 'chat':
            // 恢复对话列表折叠状态（从其他页返回时保持用户选择）
            initConversationSidebarState();
            if (typeof prefetchProjectsForChat === 'function') {
                prefetchProjectsForChat();
            }
            if (typeof refreshChatProjectSelector === 'function') {
                refreshChatProjectSelector();
            }
            if (typeof loadChatPacketGroups === 'function') {
                loadChatPacketGroups();
            }
            break;
        case 'hitl':
            if (typeof refreshHitlActivePanel === 'function') {
                refreshHitlActivePanel();
            } else if (typeof refreshHitlPending === 'function') {
                refreshHitlPending();
            }
            break;
        case 'info-collect':
            // 信息收集页面
            if (typeof initInfoCollectPage === 'function') {
                initInfoCollectPage();
            }
            break;
        case 'asset-overview':
            if (typeof loadAssetOverview === 'function') loadAssetOverview();
            if (typeof startAssetAutoRefresh === 'function') startAssetAutoRefresh();
            break;
        case 'asset-library':
            if (typeof loadAssets === 'function') loadAssets();
            if (typeof startAssetAutoRefresh === 'function') startAssetAutoRefresh();
            break;
        case 'tasks':
            // 初始化任务管理页面
            if (typeof initTasksPage === 'function') {
                initTasksPage();
            }
            break;
        case 'mcp-monitor':
            // 初始化监控面板
            if (typeof refreshMonitorPanel === 'function') {
                refreshMonitorPanel();
            }
            if (typeof startMonitorPoll === 'function') {
                startMonitorPoll();
            }
            break;
        case 'mcp-management':
            // 初始化MCP管理
            const startLoadMcpTools = () => {
                // 加载工具列表（MCP工具配置已移到MCP管理页面）
                // 使用异步加载，避免阻塞页面渲染
                if (typeof loadToolsList === 'function') {
                    // 确保工具分页设置已初始化
                    if (typeof getToolsPageSize === 'function' && typeof toolsPagination !== 'undefined') {
                        toolsPagination.pageSize = getToolsPageSize();
                    }
                    // 延迟加载，让页面先渲染
                    setTimeout(() => {
                        loadToolsList(1, '').catch(err => {
                            console.error('加载工具列表失败:', err);
                        });
                    }, 100);
                }
            };
            const afterMcpConfigReady = () => {
                startLoadMcpTools();
                if (typeof loadExternalMCPs === 'function') {
                    loadExternalMCPs().catch(err => {
                        console.warn('加载外部MCP列表失败:', err);
                    });
                }
                if (typeof startExternalMcpPoll === 'function') {
                    startExternalMcpPoll();
                }
            };
            // 先拉取配置（含 tool_search 常驻列表），再加载工具与外部 MCP。
            // 仅有 mcp:read 的用户无需拉全量 /api/config（需 config:read）。
            const canLoadFullConfig = typeof hasPermission !== 'function' || hasPermission('config:read');
            if (typeof loadConfig === 'function' && canLoadFullConfig) {
                loadConfig(false, { silent: true })
                    .catch(err => {
                        console.warn('加载配置失败（将继续加载 MCP 列表）:', err);
                    })
                    .finally(afterMcpConfigReady);
            } else {
                afterMcpConfigReady();
            }
            break;
        case 'local-tools-management':
            if (typeof loadLocalTools === 'function') {
                loadLocalTools();
            }
            break;
        case 'packet-capture':
            if (typeof initPacketCapturePage === 'function') initPacketCapturePage();
            break;
        case 'projects':
            if (typeof initProjectsPage === 'function') {
                initProjectsPage();
            }
            break;
        case 'vulnerabilities':
            // 初始化漏洞管理页面
            if (typeof initVulnerabilityPage === 'function') {
                initVulnerabilityPage();
            }
            break;
        case 'webshell':
            // 初始化 WebShell 管理页面
            if (typeof initWebshellPage === 'function') {
                initWebshellPage();
            }
            break;
        case 'chat-files':
            if (typeof initChatFilesPage === 'function') {
                initChatFilesPage();
            }
            break;
        case 'settings':
            // 初始化设置页面（不需要加载工具列表）
            if (typeof loadConfig === 'function') {
                loadConfig(false);
            }
            break;
        case 'roles-management':
            // 初始化角色管理页面
            // 重置搜索UI（变量会在下次搜索时自动更新）
            const rolesSearchInput = document.getElementById('roles-search');
            if (rolesSearchInput) {
                rolesSearchInput.value = '';
            }
            const rolesSearchClear = document.getElementById('roles-search-clear');
            if (rolesSearchClear) {
                rolesSearchClear.style.display = 'none';
            }
            if (typeof loadRoles === 'function') {
                loadRoles().then(() => {
                    if (typeof renderRolesList === 'function') {
                        renderRolesList();
                    }
                });
            }
            break;
        case 'platform-rbac':
            if (typeof initPlatformRbacPage === 'function') {
                initPlatformRbacPage();
            }
            break;
        case 'workflows':
            if (typeof refreshWorkflows === 'function') {
                refreshWorkflows();
            }
            break;
        case 'skills-monitor':
            // 初始化Skills状态监控页面
            if (typeof loadSkillsMonitor === 'function') {
                loadSkillsMonitor();
            }
            break;
        case 'skills-management':
            // 初始化Skills管理页面
            // 重置搜索UI（变量会在下次搜索时自动更新）
            const skillsSearchInput = document.getElementById('skills-search');
            if (skillsSearchInput) {
                skillsSearchInput.value = '';
            }
            const skillsSearchClear = document.getElementById('skills-search-clear');
            if (skillsSearchClear) {
                skillsSearchClear.style.display = 'none';
            }
            if (typeof initSkillsPagination === 'function') {
                initSkillsPagination();
            }
            if (typeof loadSkills === 'function') {
                loadSkills();
            }
            break;
        case 'agents-management':
            if (typeof loadMarkdownAgents === 'function') {
                loadMarkdownAgents();
            }
            break;
        case 'c2-listeners':
        case 'c2-sessions':
        case 'c2-tasks':
        case 'c2-payloads':
        case 'c2-events':
        case 'c2-profiles':
            window.currentPageId = pageId;
            if (window.C2 && typeof window.C2.init === 'function') {
                window.C2.init();
            }
            break;
    }
    
    // 清理其他页面的定时器
    if (pageId !== 'tasks' && typeof cleanupTasksPage === 'function') {
        cleanupTasksPage();
    }
}

// 页面加载完成后初始化路由
document.addEventListener('DOMContentLoaded', function() {
    rebuildSimplifiedSidebarNav();
    initRouter();
    initSidebarState();
    
    // 监听hash变化
    window.addEventListener('hashchange', function() {
        const hash = window.location.hash.slice(1);
        // 处理带参数的hash（如 chat?conversation=xxx）
        const hashParts = hash.split('?');
        const pageId = normalizePageId(hashParts[0]);

        if (pageId && isSupportedPage(pageId)) {
            switchPage(pageId);
            if (pageId === 'chat') {
                scheduleChatConversationFromHash(200);
            }
        }
    });
});

// 切换侧边栏折叠/展开
function toggleSidebar() {
    const sidebar = document.getElementById('main-sidebar');
    if (sidebar) {
        sidebar.classList.toggle('collapsed');
        // 保存折叠状态到localStorage
        const isCollapsed = sidebar.classList.contains('collapsed');
        localStorage.setItem('sidebarCollapsed', isCollapsed ? 'true' : 'false');
    }
}
window.toggleSidebar = toggleSidebar;

// 初始化侧边栏状态
function initSidebarState() {
    const sidebar = document.getElementById('main-sidebar');
    if (sidebar) {
        const savedState = localStorage.getItem('sidebarCollapsed');
        if (savedState === 'true') {
            sidebar.classList.add('collapsed');
        }
    }
    initConversationSidebarState();
}

// 切换对话页左侧列表折叠/展开
function toggleConversationSidebar() {
    const sidebar = document.getElementById('conversation-sidebar');
    if (sidebar) {
        sidebar.classList.toggle('collapsed');
        const isCollapsed = sidebar.classList.contains('collapsed');
        localStorage.setItem('conversationSidebarCollapsed', isCollapsed ? 'true' : 'false');
    }
}
window.toggleConversationSidebar = toggleConversationSidebar;

// 恢复对话列表折叠状态（进入对话页时生效）
function initConversationSidebarState() {
    const sidebar = document.getElementById('conversation-sidebar');
    if (sidebar) {
        const savedState = localStorage.getItem('conversationSidebarCollapsed');
        if (savedState === 'true') {
            sidebar.classList.add('collapsed');
        } else {
            sidebar.classList.remove('collapsed');
        }
    }
}

// 导出函数供其他脚本使用（与上方尽早绑定保持一致，便于外部脚本探测）
window.currentPage = function() { return currentPage; };
